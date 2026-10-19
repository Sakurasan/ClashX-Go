package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// SubscriptionFile is the default file name of a downloaded subscription
// inside the core working directory. When it exists, the generated config
// uses a file provider pointing at it, so mihomo never has to reach the
// subscription host itself. Managed profiles each keep their own copy.
const SubscriptionFile = "sub.yaml"

// ErrNotClashYAML reports that a downloaded subscription is not a Clash
// YAML document (it is likely a base64 or plain node list), so the file
// provider cannot load it and the config falls back to an http provider.
var ErrNotClashYAML = fmt.Errorf("subscription is not a Clash YAML document")

// subscriptionPath returns the full path of a subscription file name.
func (m *Manager) subscriptionPath(file string) string {
	if file == "" {
		file = SubscriptionFile
	}
	return filepath.Join(m.dir, file)
}

// SubscriptionProxyCount returns how many proxies the stored subscription
// holds, or 0 when there is none.
func (m *Manager) SubscriptionProxyCount(file string) int {
	data, err := os.ReadFile(m.subscriptionPath(file))
	if err != nil {
		return 0
	}
	return countProxies(data)
}

// SubscriptionStale reports whether the stored subscription is missing or
// older than maxAge, i.e. whether a Start will have to download it before
// the core can boot.
func (m *Manager) SubscriptionStale(file string, maxAge time.Duration) bool {
	fi, err := os.Stat(m.subscriptionPath(file))
	if err != nil || fi.Size() == 0 {
		return true
	}
	return time.Since(fi.ModTime()) >= maxAge
}

// RenameSubscriptionFile moves a downloaded subscription (and its quota
// sidecar) to a new file name.
func (m *Manager) RenameSubscriptionFile(from, to string) error {
	if err := os.Rename(m.subscriptionPath(from), m.subscriptionPath(to)); err != nil {
		return err
	}
	_ = os.Rename(m.subscriptionInfoPath(from), m.subscriptionInfoPath(to))
	return nil
}

// RemoveSubscriptionFiles deletes a subscription's stored files.
func (m *Manager) RemoveSubscriptionFiles(file string) {
	_ = os.Remove(m.subscriptionPath(file))
	_ = os.Remove(m.subscriptionInfoPath(file))
}

// SubscriptionInfo is the quota metadata a provider returns in the
// subscription-userinfo response header, stored beside the subscription.
type SubscriptionInfo struct {
	Upload   int64     `json:"upload"`
	Download int64     `json:"download"`
	Total    int64     `json:"total"`
	Expire   time.Time `json:"expire"`
}

func (m *Manager) subscriptionInfoPath(file string) string {
	return m.subscriptionPath(file) + "-info.json"
}

// SubscriptionInfo returns the stored quota info for a subscription file,
// or nil when the provider never sent one.
func (m *Manager) SubscriptionInfo(file string) *SubscriptionInfo {
	data, err := os.ReadFile(m.subscriptionInfoPath(file))
	if err != nil {
		return nil
	}
	var info SubscriptionInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil
	}
	return &info
}

// subscriptionUserinfoRE matches the key=value pairs of the header.
var subscriptionUserinfoRE = regexp.MustCompile(`(\w+)\s*=\s*(\d+)`)

// parseSubscriptionUserinfo parses "upload=…; download=…; total=…; expire=…"
// (RFC 9972 style). ok is false when the header carries no usable numbers.
func parseSubscriptionUserinfo(h string) (info SubscriptionInfo, ok bool) {
	for _, m := range subscriptionUserinfoRE.FindAllStringSubmatch(h, -1) {
		n, err := strconv.ParseInt(m[2], 10, 64)
		if err != nil {
			continue
		}
		ok = true
		switch m[1] {
		case "upload":
			info.Upload = n
		case "download":
			info.Download = n
		case "total":
			info.Total = n
		case "expire":
			if n > 0 {
				info.Expire = time.Unix(n, 0)
			}
		}
	}
	return info, ok
}

// DownloadSubscription fetches url and stores it under file for the file
// provider, returning the number of proxies and the profile title the
// provider suggested (profile-title / profile-name header, URL fragment).
// A subscription that is not a Clash YAML document is rejected with
// ErrNotClashYAML (the config then falls back to an http provider, letting
// mihomo convert it).
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
func (m *Manager) DownloadSubscription(ctx context.Context, file, url, proxyAddr string) (int, string, error) {
	if url == "" {
		return 0, "", fmt.Errorf("empty subscription url")
	}
	var lastErr error
	for attempt := 0; attempt < subscriptionAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return 0, "", ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		n, title, err := m.downloadSubscriptionOnce(ctx, file, url, proxyAddr)
		if err == nil {
			return n, title, nil
		}
		lastErr = err
		fmt.Fprintf(os.Stderr, "subscription attempt %d/%d failed: %v\n",
			attempt+1, subscriptionAttempts, err)
		if errors.Is(err, ErrNotClashYAML) {
			break // no point retrying a body we can never use
		}
		if errors.Is(err, ctx.Err()) {
			return 0, "", err
		}
	}
	return 0, "", lastErr
}

// subscriptionAttempts is how many times a subscription download is tried.
const subscriptionAttempts = 3

// downloadSubscriptionOnce performs a single download attempt.
func (m *Manager) downloadSubscriptionOnce(ctx context.Context, file, url, proxyAddr string) (int, string, error) {
	client := &http.Client{Timeout: 90 * time.Second}
	if proxyAddr != "" {
		client.Transport = &http.Transport{Proxy: http.ProxyURL(parseProxyURL(proxyAddr))}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, "", err
	}
	// Some providers serve different content by user agent.
	req.Header.Set("User-Agent", "clash-verge/v1.0.0")
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("subscription HTTP %d", resp.StatusCode)
	}
	// Quota metadata and the profile title live in response headers.
	info, hasInfo := parseSubscriptionUserinfo(resp.Header.Get("subscription-userinfo"))
	title := subscriptionTitle(resp.Header, url)
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return 0, "", err
	}
	if decoded, ok := decodeBase64Subscription(data); ok {
		data = decoded
	}
	if !isClashYAML(data) {
		return 0, "", ErrNotClashYAML
	}
	n := countProxies(data)
	if n == 0 {
		return 0, "", fmt.Errorf("subscription has no proxies")
	}
	dest := m.subscriptionPath(file)
	if err := os.WriteFile(dest+".tmp", data, 0o644); err != nil {
		return 0, "", err
	}
	if err := os.Rename(dest+".tmp", dest); err != nil {
		return 0, "", err
	}
	if hasInfo {
		if encoded, err := json.MarshalIndent(info, "", "  "); err == nil {
			_ = os.WriteFile(m.subscriptionInfoPath(file), encoded, 0o644)
		}
	} else {
		// The provider stopped reporting quota: drop the stale copy.
		_ = os.Remove(m.subscriptionInfoPath(file))
	}
	return n, title, nil
}

// subscriptionTitle derives a display name from the headers ClashX
// honours (profile-name, then base64 profile-title), falling back to the
// URL fragment.
func subscriptionTitle(h http.Header, rawurl string) string {
	if t := h.Get("profile-name"); t != "" {
		return strings.TrimSpace(t)
	}
	if t := h.Get("profile-title"); t != "" {
		if enc, ok := strings.CutPrefix(t, "base64,"); ok {
			if dec, err := base64.StdEncoding.DecodeString(enc); err == nil {
				return strings.TrimSpace(string(dec))
			}
		}
		return strings.TrimSpace(t)
	}
	if u, err := url.Parse(rawurl); err == nil && u.Fragment != "" {
		if name, err := url.QueryUnescape(u.Fragment); err == nil {
			return name
		}
		return u.Fragment
	}
	return ""
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
