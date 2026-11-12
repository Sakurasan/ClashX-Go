package core

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

// TestApplyProxiesKeepsDelayCache guards the bug where the 2s poll rebuilt
// GroupViews and wiped full test results, leaving only the current member
// with a delay.
func TestApplyProxiesKeepsDelayCache(t *testing.T) {
	mgr, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := NewController(mgr, DefaultConfig())
	proxies := &Proxies{Proxies: map[string]*Proxy{
		"G":      {Name: "G", Type: "Selector", Now: "a", All: []string{"a", "b", "c"}},
		"DIRECT": {Name: "DIRECT", Type: "Direct"},
	}}
	c.applyProxies("rule", false, proxies)
	if g := c.State().Groups[0]; len(g.Delays) != 0 {
		t.Fatalf("fresh group should have no delays, got %v", g.Delays)
	}

	c.mu.Lock()
	c.delays = map[string]map[string]int{"G": {"a": 10, "b": 20, "c": -1}}
	c.mu.Unlock()
	c.applyProxies("rule", false, proxies)
	g := c.State().Groups[0]
	if len(g.Delays) != 3 || g.Delays["c"] != -1 {
		t.Errorf("cached delays not merged: %v", g.Delays)
	}
}

// TestConnectionIDsInGroup mirrors ClashX Meta's node-switch cleanup: only
// connections whose chain contains the switched group are dropped.
func TestConnectionIDsInGroup(t *testing.T) {
	conns := &Connections{Connections: []Connection{
		{ID: "a", Chains: []string{"JP", "Proxies"}},
		{ID: "b", Chains: []string{"Proxies"}},
		{ID: "c", Chains: []string{"DIRECT"}},
	}}
	got := connectionIDsInGroup(conns, "Proxies")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("IDs = %v", got)
	}
	if got := connectionIDsInGroup(conns, "JP"); len(got) != 1 || got[0] != "a" {
		t.Fatalf("node IDs = %v", got)
	}
}

// TestApplyProxiesCarriesRuntimeConfig keeps the menu in step with changes
// made from the dashboard, which only exist in the core until saved.
func TestApplyProxiesCarriesRuntimeConfig(t *testing.T) {
	mgr, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := NewController(mgr, DefaultConfig())
	if c.State().ConfigLoaded {
		t.Fatal("state should start without config")
	}
	c.applyProxies("global", true, &Proxies{Proxies: map[string]*Proxy{}})
	s := c.State()
	if !s.ConfigLoaded || s.Mode != "global" || !s.AllowLAN {
		t.Fatalf("runtime config not applied: %+v", s)
	}
}

// TestProxyGroupsFollowMode mirrors ClashX: the built-in GLOBAL group is
// shown only in global mode, and comes first there.
func TestProxyGroupsFollowMode(t *testing.T) {
	px := &Proxies{Proxies: map[string]*Proxy{
		"GLOBAL":  {Name: "GLOBAL", Type: "Selector", Now: "DIRECT"},
		"Proxies": {Name: "Proxies", Type: "Selector", Now: "DIRECT"},
	}}
	if _, ok := px.ProxyGroups("rule")["GLOBAL"]; ok {
		t.Error("GLOBAL shown in rule mode")
	}
	if _, ok := px.ProxyGroups("global")["GLOBAL"]; !ok {
		t.Error("GLOBAL missing in global mode")
	}
	groups := []*GroupView{{Name: "Proxies"}, {Name: "GLOBAL"}}
	sortGroups(groups, []string{"Proxies"})
	if groups[0].Name != "GLOBAL" {
		t.Errorf("GLOBAL is not first: %v, %v", groups[0].Name, groups[1].Name)
	}
}

// TestNewControllerReadsGroupOrder guards startup: the core boots with a
// bootstrap config without proxy groups, so the order must already come
// from the subscription file, not wait for the first live reload.
func TestNewControllerReadsGroupOrder(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	profile := "proxy-groups:\n  - name: Proxies\n    type: select\n  - name: HK\n    type: select\n"
	if err := os.WriteFile(filepath.Join(dir, "sub-test.yaml"), []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.SubscriptionURL = "https://example.com/sub"
	cfg.SubscriptionFile = "sub-test.yaml"
	c := NewController(mgr, cfg)
	if want := []string{"Proxies", "HK"}; !slices.Equal(c.groupOrder, want) {
		t.Errorf("groupOrder = %v, want %v", c.groupOrder, want)
	}
}

// TestTrafficLoopSurvivesFailedStream guards the tray read-out dying for the
// whole session: the core recreates its controller listener during a config
// reload, so a reconnect can fail to get any response at all. StreamTraffic
// returns on that, and trafficLoop used to discard the error — one blip and
// the rate froze at its last sample until the core restarted.
func TestTrafficLoopSurvivesFailedStream(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	served := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n == 1 {
			// The listener is mid-recreation: hang up before any response,
			// which the HTTP client reports as a transport error.
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("no hijacker")
				return
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("no hijacker")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		// One sample, then hold the stream open like the real core does.
		_, err = fmt.Fprint(conn,
			"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{\"up\":131,\"down\":357}\n")
		if err != nil {
			t.Error(err)
			return
		}
		select {
		case served <- struct{}{}:
		default:
		}
		time.Sleep(2 * time.Second)
	}))
	t.Cleanup(srv.Close)

	mgr, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := NewController(mgr, DefaultConfig())
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	c.client.Base = "http://" + u.Host

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.trafficLoop(ctx)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if c.State().Traffic.Up == 131 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("traffic never resumed after a failed stream: %+v", c.State().Traffic)
}
