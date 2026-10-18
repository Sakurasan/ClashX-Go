package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Client talks to the mihomo external controller. The controller answers
// at http://127.0.0.1:<port> and, when a secret is set, requires the
// bearer token on every request.
type Client struct {
	Base   string // http://127.0.0.1:9090
	Secret string
	HTTP   *http.Client
}

// NewClient returns a controller client for the given endpoint and secret.
func NewClient(controllerPort int, secret string) *Client {
	return &Client{
		Base:   fmt.Sprintf("http://127.0.0.1:%d", controllerPort),
		Secret: secret,
		HTTP:   &http.Client{Timeout: 5 * time.Second},
	}
}

// do performs a request, attaching the bearer token.
func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return nil, err
	}
	if c.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.Secret)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.HTTP.Do(req)
}

// Ping returns whether the controller answers.
func (c *Client) Ping(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodGet, "/version", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("controller status %d", resp.StatusCode)
	}
	return nil
}

// Version reports the core version string, e.g. "v1.19.32".
func (c *Client) Version(ctx context.Context) (string, error) {
	resp, err := c.do(ctx, http.MethodGet, "/version", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var v struct {
		Version string `json:"version"`
		Meta    bool   `json:"meta"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", err
	}
	return v.Version, nil
}

// Configs holds the running configuration summary from /configs.
type Configs struct {
	Port          int    `json:"port"`
	SocksPort     int    `json:"socks-port"`
	MixedPort     int    `json:"mixed-port"`
	Mode          string `json:"mode"`
	AllowLAN      bool   `json:"allow-lan"`
	IPv6          bool   `json:"ipv6"`
	LogLevel      string `json:"log-level"`
	Controller    string `json:"external-controller"`
	TunEnabled    bool   `json:"tun.enable"`
	TunStack      string `json:"tun.stack"`
	UnifiedGUID   bool   `json:"unified-delay"`
	TCPConcurrent bool   `json:"tcp-concurrent"`
}

// GetConfigs reads the running config.
func (c *Client) GetConfigs(ctx context.Context) (*Configs, error) {
	resp, err := c.do(ctx, http.MethodGet, "/configs", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /configs: HTTP %d", resp.StatusCode)
	}
	var cfg Configs
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// SetMode switches the running mode: rule, global or direct.
func (c *Client) SetMode(ctx context.Context, mode string) error {
	body := strings.NewReader(fmt.Sprintf(`{"mode":%q}`, mode))
	resp, err := c.do(ctx, http.MethodPatch, "/configs", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("PATCH /configs: HTTP %d", resp.StatusCode)
	}
	return nil
}

// SetTUN toggles the TUN interface at runtime (mihomo hot-patches it),
// avoiding a core restart.
func (c *Client) SetTUN(ctx context.Context, enable bool) error {
	body := strings.NewReader(fmt.Sprintf(`{"tun":{"enable":%t}}`, enable))
	resp, err := c.do(ctx, http.MethodPatch, "/configs", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("PATCH /configs tun: HTTP %d", resp.StatusCode)
	}
	return nil
}

// Proxy describes one proxy or policy group entry from /proxies.
type Proxy struct {
	Name    string          `json:"name"`
	Type    string          `json:"type"`
	Hidden  bool            `json:"hidden"`
	Now     string          `json:"now"`
	All     []string        `json:"all"`
	History []DelayEntry    `json:"history"`
	Extra   json.RawMessage `json:"extra"`
}

// DelayEntry is one history sample of a proxy.
type DelayEntry struct {
	Time  time.Time `json:"time"`
	Delay uint16    `json:"delay"`
}

// Proxies holds the /proxies response.
type Proxies struct {
	Proxies map[string]*Proxy `json:"proxies"`
}

// ProxyGroups returns the proxies whose type is a policy group (Selector,
// URLTest, Fallback, LoadBalance), skipping hidden built-ins like GLOBAL.
func (p *Proxies) ProxyGroups() map[string]*Proxy {
	out := map[string]*Proxy{}
	for name, pr := range p.Proxies {
		if pr.Hidden || name == "GLOBAL" { // ClashX hides the built-in catch-all
			continue
		}
		switch pr.Type {
		case "Selector", "URLTest", "Fallback", "LoadBalance":
			out[name] = pr
		}
	}
	return out
}

// GetProxies reads all proxies and groups.
func (c *Client) GetProxies(ctx context.Context) (*Proxies, error) {
	resp, err := c.do(ctx, http.MethodGet, "/proxies", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /proxies: HTTP %d", resp.StatusCode)
	}
	var p Proxies
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ReloadProvider asks the core to re-read a proxy provider (a file
// provider picks up an updated subscription file without a restart).
func (c *Client) ReloadProvider(ctx context.Context, name string) error {
	path := "/providers/proxies/" + url.PathEscape(name)
	resp, err := c.do(ctx, http.MethodPut, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("PUT %s: HTTP %d", path, resp.StatusCode)
	}
	return nil
}

// SelectProxy sets group's chosen member: PUT /proxies/<group> {"name": member}.
func (c *Client) SelectProxy(ctx context.Context, group, member string) error {
	path := "/proxies/" + url.PathEscape(group)
	body := strings.NewReader(fmt.Sprintf(`{"name":%q}`, member))
	resp, err := c.do(ctx, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("PUT %s: HTTP %d", path, resp.StatusCode)
	}
	return nil
}

// TestProxyDelay measures one proxy's latency in ms. mihomo answers 503
// with {"message":"timeout"} for unreachable nodes, reported as -1.
func (c *Client) TestProxyDelay(ctx context.Context, name string) (int, error) {
	path := "/proxies/" + url.PathEscape(name) + "/delay?timeout=5000&url=http://www.gstatic.com/generate_204"
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return -1, err
	}
	defer io.Copy(io.Discard, resp.Body) // drain so the connection is reusable
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return -1, fmt.Errorf("delay test %s: HTTP %d", name, resp.StatusCode)
	}
	var v struct {
		Delay int `json:"delay"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return -1, err
	}
	return v.Delay, nil
}

// groupTestConcurrency is how many nodes one group test probes at once.
const groupTestConcurrency = 16

// TestGroupDelay tests every member of a group concurrently and returns
// name→ms (-1 for unreachable). mihomo's legacy /group/{name}/delay only
// answers for the group's current selection, so each node is tested via
// its own endpoint, like ClashX does.
func (c *Client) TestGroupDelay(ctx context.Context, members []string) map[string]int {
	out := make(map[string]int, len(members))
	var mu sync.Mutex
	sem := make(chan struct{}, groupTestConcurrency)
	var wg sync.WaitGroup
	for _, m := range members {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			d, err := c.TestProxyDelay(ctx, name)
			if err != nil {
				d = -1
			}
			mu.Lock()
			out[name] = d
			mu.Unlock()
		}(m)
	}
	wg.Wait()
	return out
}

// Traffic is one sample of the /traffic stream.
type Traffic struct {
	Up        int64 `json:"up"`
	Down      int64 `json:"down"`
	UpTotal   int64 `json:"upTotal"`
	DownTotal int64 `json:"downTotal"`
}

// StreamTraffic reads the line-delimited /traffic stream, sending each
// sample to fn until ctx is cancelled. It reconnects automatically.
func (c *Client) StreamTraffic(ctx context.Context, fn func(Traffic)) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		resp, err := c.do(ctx, http.MethodGet, "/traffic", nil)
		if err != nil {
			return err
		}
		dec := json.NewDecoder(resp.Body)
		for {
			var t Traffic
			if err := dec.Decode(&t); err != nil {
				break
			}
			fn(t)
			if err := ctx.Err(); err != nil {
				resp.Body.Close()
				return ctx.Err()
			}
		}
		resp.Body.Close()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// Connection is one row of /connections.
type Connection struct {
	ID       string `json:"id"`
	Metadata struct {
		Host        string `json:"host"`
		SourceIP    string `json:"sourceIP"`
		SourcePort  string `json:"sourcePort"`
		DestIP      string `json:"destinationIP"`
		DestPort    string `json:"destinationPort"`
		Network     string `json:"network"`
		Type        string `json:"type"`
		Process     string `json:"process"`
		ProcessPath string `json:"processPath"`
	} `json:"metadata"`
	Upload      int64     `json:"upload"`
	Download    int64     `json:"download"`
	Start       time.Time `json:"start"`
	Chains      []string  `json:"chains"`
	Rule        string    `json:"rule"`
	RulePayload string    `json:"rulePayload"`
}

// Connections holds the /connections response.
type Connections struct {
	DownloadTotal int64         `json:"downloadTotal"`
	UploadTotal   int64         `json:"uploadTotal"`
	Connections   []*Connection `json:"connections"`
}

// GetConnections reads active connections.
func (c *Client) GetConnections(ctx context.Context) (*Connections, error) {
	resp, err := c.do(ctx, http.MethodGet, "/connections", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /connections: HTTP %d", resp.StatusCode)
	}
	var cs Connections
	if err := json.NewDecoder(resp.Body).Decode(&cs); err != nil {
		return nil, err
	}
	return &cs, nil
}

// CloseConnection closes one connection by id.
func (c *Client) CloseConnection(ctx context.Context, id string) error {
	path := "/connections/" + url.PathEscape(id)
	resp, err := c.do(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("DELETE %s: HTTP %d", path, resp.StatusCode)
	}
	return nil
}

// CloseConnections closes every active connection.
func (c *Client) CloseConnections(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodDelete, "/connections", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("DELETE /connections: HTTP %d", resp.StatusCode)
	}
	return nil
}
