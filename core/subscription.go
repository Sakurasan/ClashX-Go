package core

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// SubscriptionFile is the file name of the downloaded subscription inside
// the core working directory. When it exists, the generated config uses a
// file provider pointing at it, so mihomo never has to reach the
// subscription host itself.
const SubscriptionFile = "sub.yaml"

// ErrNotClashYAML reports that a downloaded subscription is not a Clash
// YAML document (it is likely a base64 or plain node list), so the file
// provider cannot load it and the config falls back to an http provider.
var ErrNotClashYAML = fmt.Errorf("subscription is not a Clash YAML document")

// SubscriptionPath returns where the downloaded subscription is stored.
func (m *Manager) SubscriptionPath() string {
	return filepath.Join(m.dir, SubscriptionFile)
}

// SubscriptionProxyCount returns how many proxies the stored subscription
// holds, or 0 when there is none.
func (m *Manager) SubscriptionProxyCount() int {
	data, err := os.ReadFile(m.SubscriptionPath())
	if err != nil {
		return 0
	}
	return countProxies(data)
}

// SubscriptionUpdatedAt returns the modification time of the stored
// subscription, or the zero time.
func (m *Manager) SubscriptionUpdatedAt() time.Time {
	fi, err := os.Stat(m.SubscriptionPath())
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// SubscriptionStale reports whether the stored subscription is missing or
// older than maxAge, i.e. whether a Start will have to download it before
// the core can boot.
func (m *Manager) SubscriptionStale(maxAge time.Duration) bool {
	fi, err := os.Stat(m.SubscriptionPath())
	if err != nil || fi.Size() == 0 {
		return true
	}
	return time.Since(fi.ModTime()) >= maxAge
}

// DownloadSubscription fetches url and stores it for the file provider,
// returning the number of proxies it holds. A subscription that is not a
// Clash YAML document is rejected with ErrNotClashYAML (the config then
// falls back to an http provider, letting mihomo convert it).
//
// The download goes through the app, not the core, so it works when the
// core cannot reach the subscription host directly (a common case with
// raw.githubusercontent.com). When a proxyAddr is given (e.g. the core's
// own mixed port), the download is routed through it.
//
// Transient failures are retried (subscription hosts — raw.githubusercontent
// especially — stall or refuse connections intermittently), and each
// attempt is bounded by its own timeout so a stalled connection cannot
// hang the whole download.
func (m *Manager) DownloadSubscription(ctx context.Context, url, proxyAddr string) (int, error) {
	if url == "" {
		return 0, fmt.Errorf("empty subscription url")
	}
	var lastErr error
	for attempt := 0; attempt < subscriptionAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		n, err := m.downloadSubscriptionOnce(ctx, url, proxyAddr)
		if err == nil {
			return n, nil
		}
		lastErr = err
		fmt.Fprintf(os.Stderr, "subscription attempt %d/%d failed: %v\n",
			attempt+1, subscriptionAttempts, err)
		if errors.Is(err, ErrNotClashYAML) {
			break // no point retrying a body we can never use
		}
		if errors.Is(err, ctx.Err()) {
			return 0, err
		}
	}
	return 0, lastErr
}

// subscriptionAttempts is how many times a subscription download is tried.
const subscriptionAttempts = 3

// downloadSubscriptionOnce performs a single download attempt.
func (m *Manager) downloadSubscriptionOnce(ctx context.Context, url, proxyAddr string) (int, error) {
	client := &http.Client{Timeout: 90 * time.Second}
	if proxyAddr != "" {
		client.Transport = &http.Transport{Proxy: http.ProxyURL(parseProxyURL(proxyAddr))}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	// Some providers serve different content by user agent.
	req.Header.Set("User-Agent", "clash-verge/v1.0.0")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("subscription HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return 0, err
	}
	if decoded, ok := decodeBase64Subscription(data); ok {
		data = decoded
	}
	if !isClashYAML(data) {
		return 0, ErrNotClashYAML
	}
	n := countProxies(data)
	if n == 0 {
		return 0, fmt.Errorf("subscription has no proxies")
	}
	dest := m.SubscriptionPath()
	if err := os.WriteFile(dest+".tmp", data, 0o644); err != nil {
		return 0, err
	}
	if err := os.Rename(dest+".tmp", dest); err != nil {
		return 0, err
	}
	return n, nil
}

// isClashYAML reports whether data looks like a Clash config with a
// proxies list.
func isClashYAML(data []byte) bool {
	s := string(data)
	return strings.Contains(s, "proxies:") || strings.Contains(s, "proxy-providers:")
}

// countProxies counts the top-level list items under a proxies: key. It is
// a simple scan: it counts lines that start with "- " at the first
// indentation level after "proxies:".
func countProxies(data []byte) int {
	lines := strings.Split(string(data), "\n")
	inProxies := false
	indent := -1
	count := 0
	itemRe := regexp.MustCompile(`^(\s*)-\s`)
	for _, line := range lines {
		trimmed := strings.TrimRight(line, "\r")
		if !inProxies {
			if strings.HasPrefix(strings.TrimSpace(trimmed), "proxies:") {
				inProxies = true
			}
			continue
		}
		// A non-indented, non-empty line ends the block.
		if strings.TrimSpace(trimmed) == "" {
			continue
		}
		m := itemRe.FindStringSubmatch(trimmed)
		if m == nil {
			// A key at the top level ends the proxies block.
			if !strings.HasPrefix(trimmed, " ") && !strings.HasPrefix(trimmed, "\t") {
				break
			}
			continue
		}
		cur := len(m[1])
		if indent == -1 {
			indent = cur
		}
		if cur == indent {
			count++
		}
	}
	return count
}

// decodeBase64Subscription decodes a base64 subscription body (the common
// non-YAML format, a base64 of a node-URI list) when it decodes cleanly
// and contains proxy URIs.
func decodeBase64Subscription(data []byte) ([]byte, bool) {
	s := strings.TrimSpace(string(data))
	if s == "" || strings.Contains(s, "proxies:") {
		return nil, false
	}
	// Strip whitespace/newlines that base64 encoders sometimes insert.
	compact := strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', ' ', '\t':
			return -1
		}
		return r
	}, s)
	decoded, err := base64.StdEncoding.DecodeString(compact)
	if err != nil {
		if decoded, err = base64.RawStdEncoding.DecodeString(compact); err != nil {
			return nil, false
		}
	}
	if strings.Contains(string(decoded), "://") {
		return decoded, true
	}
	return nil, false
}

// parseProxyURL parses a host:port or http://host:port proxy address into
// a URL the http transport can use.
func parseProxyURL(addr string) *url.URL {
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil {
		return nil
	}
	return u
}
