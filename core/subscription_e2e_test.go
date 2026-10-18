package core

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestSubscriptionE2E exercises the real subscription path against the
// bundled mihomo: the app downloads the subscription itself
// (Manager.DownloadSubscription), WriteYAML generates a config with a
// file provider pointing at the local copy, and Manager.Start boots
// mihomo which loads proxies from that local file — mihomo never fetches
// the subscription host.
//
// It is skipped unless RUN_SUBSCRIPTION_E2E=1 because it needs the
// network and a reachable free subscription.
func TestSubscriptionE2E(t *testing.T) {
	if os.Getenv("RUN_SUBSCRIPTION_E2E") != "1" {
		t.Skip("set RUN_SUBSCRIPTION_E2E=1 to run (needs network)")
	}
	if testing.Short() {
		t.Skip("short mode")
	}
	if bundledBinary() == "" {
		t.Skip("no bundled mihomo for " + runtime.GOARCH)
	}
	url := os.Getenv("SUBSCRIPTION_URL")
	if url == "" {
		url = "https://raw.githubusercontent.com/mahdibland/V2RayAggregator/master/sub/sub_merge_yaml.yml"
	}

	dir := t.TempDir()
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.EnsureBinary(); err != nil {
		t.Fatal("EnsureBinary:", err)
	}

	cfg := DefaultConfig()
	cfg.Secret = "e2e-secret"
	cfg.ControllerPort = 19091
	cfg.MixedPort = 17891
	cfg.SubscriptionURL = url

	// App downloads the subscription first (not the core).
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	n, err := mgr.DownloadSubscription(ctx, url, "")
	if err != nil {
		t.Fatalf("DownloadSubscription: %v", err)
	}
	fmt.Printf("E2E downloaded %d proxies\n", n)
	if n == 0 {
		t.Fatal("expected some proxies")
	}

	// WriteYAML must pick the local file provider (sub.yaml exists now).
	path, err := cfg.WriteYAML(dir)
	if err != nil {
		t.Fatal("WriteYAML:", err)
	}
	y, _ := os.ReadFile(path)
	if !strings.Contains(string(y), "type: file") {
		t.Fatalf("config did not use a file provider:\n%s", y)
	}

	client := NewClient(cfg.ControllerPort, cfg.Secret)
	sctx, scancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer scancel()
	if _, err := mgr.Start(sctx, cfg, func() error { return client.Ping(sctx) }); err != nil {
		t.Fatal("Start:", err)
	}
	defer mgr.Stop()

	// The core answers the controller before it has finished expanding the
	// file provider, so poll until the proxy group actually holds nodes.
	var last *Proxies
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		px, err := client.GetProxies(sctx)
		if err != nil {
			t.Fatal("GetProxies:", err)
		}
		last = px
		// PROXY/AUTO come from the file provider; they must have >1 member
		// once the provider has expanded (GLOBAL always has defaults).
		loaded := 0
		for name, g := range px.ProxyGroups() {
			if name == "PROXY" || name == "AUTO" {
				loaded += len(g.All)
			}
		}
		if loaded > 2 {
			break
		}
		select {
		case <-sctx.Done():
			t.Fatal("timeout waiting for provider expansion")
		case <-time.After(500 * time.Millisecond):
		}
	}
	groups := last.ProxyGroups()
	fmt.Printf("E2E groups=%d\n", len(groups))
	for name, g := range groups {
		fmt.Printf("E2E group %s type=%s members=%d now=%s\n", name, g.Type, len(g.All), g.Now)
	}
	pok := false
	for name, g := range groups {
		if name == "PROXY" && len(g.All) > 1 {
			pok = true
		}
	}
	if !pok {
		t.Fatal("file provider did not load proxies into PROXY")
	}
	fmt.Println("E2E OK")
}
