package core

import (
	"os"
	"path/filepath"
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

// TestWriteYAMLTunAndLAN checks the toggles that ClashX exposes in the
// menu bar reach the generated config.
func TestWriteYAMLTunAndLAN(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.TUN = true
	cfg.AllowLAN = true
	path, err := cfg.WriteYAML(dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{"tun:\n  enable: true", "stack: system", "any:53", "allow-lan: true"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in config:\n%s", want, s)
		}
	}
}

func TestParseSubscriptionUserinfo(t *testing.T) {
	info, ok := parseSubscriptionUserinfo("upload=29571824; download=30535353; total=107374182400; expire=1790000000")
	if !ok {
		t.Fatal("header not parsed")
	}
	if info.Upload != 29571824 || info.Download != 30535353 || info.Total != 107374182400 {
		t.Errorf("numbers wrong: %+v", info)
	}
	if info.Expire.Unix() != 1790000000 {
		t.Errorf("expire = %v", info.Expire)
	}
	if _, ok := parseSubscriptionUserinfo(""); ok {
		t.Error("empty header must not parse")
	}
}

func TestGroupOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	yaml := "mixed-port: 7890\n" +
		"proxy-groups:\n" +
		"  - name: PROXY\n" +
		"    type: select\n" +
		"  - {name: JP Tokyo, type: select, proxies: [a, b]} # 注释\n" +
		"  - name: \"AUTO\"\n" +
		"    type: url-test\n" +
		"rules:\n" +
		"  - MATCH,PROXY\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	got := GroupOrder(path)
	want := []string{"PROXY", "JP Tokyo", "AUTO"}
	if len(got) != len(want) {
		t.Fatalf("order = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %q, want %q", got, want)
		}
	}
	if n := GroupOrder(filepath.Join(dir, "missing.yaml")); n != nil {
		t.Errorf("missing file should yield nil, got %q", n)
	}
}

func TestSortGroups(t *testing.T) {
	mk := func(names ...string) []*GroupView {
		out := []*GroupView{}
		for _, n := range names {
			out = append(out, &GroupView{Name: n})
		}
		return out
	}
	groups := mk("US", "Apple", "PROXY", "Zebra")
	sortGroups(groups, []string{"PROXY", "Apple"})
	var got []string
	for _, g := range groups {
		got = append(got, g.Name)
	}
	want := []string{"PROXY", "Apple", "US", "Zebra"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("sorted = %q, want %q", got, want)
	}
}
