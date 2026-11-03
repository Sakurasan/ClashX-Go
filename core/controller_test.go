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
