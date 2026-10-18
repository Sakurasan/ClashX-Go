package core

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"
)

// TestSmokeStart verifies the whole core lifecycle against the bundled
// mihomo binary for the running architecture: EnsureBinary copies it from
// the app resources, Manager.Start boots it with a generated config, the
// external controller answers with the secret override, /proxies and
// /configs work, and Stop terminates the process. It is skipped when the
// bundled binary for the host is absent (e.g. build without running
// scripts/fetch-cores.sh).
func TestSmokeStart(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	if bundledBinary() == "" {
		t.Skip("no bundled mihomo for " + runtime.GOARCH + "; run scripts/fetch-cores.sh")
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
	cfg.Secret = "smoke-secret"
	cfg.ControllerPort = 19090
	cfg.MixedPort = 17890
	client := NewClient(cfg.ControllerPort, cfg.Secret)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	path, err := mgr.Start(ctx, cfg, func() error { return client.Ping(ctx) })
	if err != nil {
		t.Fatal("Start:", err)
	}
	t.Log("config path:", path)
	ver, err := client.Version(ctx)
	if err != nil {
		t.Fatal("Version:", err)
	}
	fmt.Printf("SMOKE version=%s\n", ver)
	cfg2, err := client.GetConfigs(ctx)
	if err != nil {
		t.Fatal("GetConfigs:", err)
	}
	fmt.Printf("SMOKE mode=%s mixed=%d ctl=%s\n", cfg2.Mode, cfg2.MixedPort, cfg2.Controller)
	px, err := client.GetProxies(ctx)
	if err != nil {
		t.Fatal("GetProxies:", err)
	}
	fmt.Printf("SMOKE proxies=%d\n", len(px.Proxies))
	// Secret must be required: a client without it fails.
	bad := NewClient(cfg.ControllerPort, "")
	if err := bad.Ping(ctx); err == nil {
		t.Fatal("expected auth failure without secret")
	}
	mgr.Stop()
	if mgr.Running() {
		t.Fatal("still running after Stop")
	}
	fmt.Println("SMOKE OK")
}
