package core

import (
	"os"
	"strings"
	"testing"
)

// TestWriteYAMLSubscription verifies that a config with a subscription URL
// generates a proxy-provider block, policy groups and GEOIP rules, so
// mihomo can pull real nodes. This is a pure string check; the smoke test
// boots mihomo against the real provider.
func TestWriteYAMLSubscription(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.SubscriptionURL = "https://example.com/sub?token=abc"
	path, err := cfg.WriteYAML(dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		"proxy-providers:",
		`url: "https://example.com/sub?token=abc"`,
		"proxy-groups:",
		"name: PROXY",
		"type: select",
		"name: AUTO",
		"type: url-test",
		"GEOIP,CN,DIRECT",
		"MATCH,PROXY",
		"fake-ip-range: 198.18.0.1/16",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in generated config:\n%s", want, s)
		}
	}
}

// TestWriteYAMLDirect verifies the no-subscription path still generates a
// minimal DIRECT config that boots (used when no profile or subscription
// is set).
func TestWriteYAMLDirect(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	path, err := cfg.WriteYAML(dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "MATCH,DIRECT") {
		t.Errorf("missing MATCH,DIRECT in DIRECT config:\n%s", s)
	}
	if strings.Contains(s, "proxy-providers:") {
		t.Errorf("DIRECT config must not include providers:\n%s", s)
	}
}
