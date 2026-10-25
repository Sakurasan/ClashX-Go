package core

import (
	"testing"
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
