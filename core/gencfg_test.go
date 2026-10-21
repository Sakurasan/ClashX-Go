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

// Per-app rules are rendered before the provider's catch-all rules, with a
// bundle-path regex so helper processes inside the app match too.
func TestWriteYAMLProcessRules(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.SubscriptionURL = "https://example.com/sub"
	cfg.ProcessRules = []ProcessRule{
		{Process: "Telegram", Path: "/Applications/Telegram.app", Target: "PROXY"},
		{Process: "Steam", Target: "DIRECT"},
	}
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
		"- PROCESS-NAME,Telegram,PROXY",
		`- PROCESS-PATH-REGEX,^/Applications/Telegram\.app/,PROXY`,
		"- PROCESS-NAME,Steam,DIRECT",
		"find-process-mode: strict",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in generated config:\n%s", want, s)
		}
	}
	firstRule := strings.Index(s, "PROCESS-NAME,Telegram")
	if firstRule < 0 || firstRule > strings.Index(s, "GEOIP,CN,DIRECT") {
		t.Errorf("process rules must precede the profile rules:\n%s", s)
	}
}

// A direct-only generated config has no proxy group, so proxy-targeted app
// rules are dropped instead of referring to a group that does not exist.
func TestWriteYAMLDirectDropsProxyRules(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.ProcessRules = []ProcessRule{
		{Process: "Telegram", Target: "PROXY"},
		{Process: "Steam", Target: "DIRECT"},
	}
	path, err := cfg.WriteYAML(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := string(mustRead(t, path))
	if strings.Contains(s, "PROCESS-NAME,Telegram") {
		t.Errorf("proxy rule without a group should be dropped:\n%s", s)
	}
	if !strings.Contains(s, "PROCESS-NAME,Steam,DIRECT") {
		t.Errorf("direct rule missing:\n%s", s)
	}
}

// Full profiles keep their own rule order: the app rules go first and the
// PROXY target resolves to the profile's first group.
func TestWriteRunningConfigProcessRules(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "profile.yaml")
	dest := filepath.Join(dir, "running.yaml")
	profile := "proxy-groups:\n" +
		"  - name: 自动选择\n    type: url-test\n" +
		"  - name: 节点选择\n    type: select\n" +
		"rules:\n" +
		"  - DOMAIN-SUFFIX,example.com,节点选择\n" +
		"  - MATCH,节点选择\n"
	if err := os.WriteFile(src, []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.ProcessRules = []ProcessRule{{Process: "Telegram", Target: "PROXY"}}
	if _, err := writeRunningConfig(src, dest, cfg); err != nil {
		t.Fatal(err)
	}
	s := string(mustRead(t, dest))
	if !strings.Contains(s, "find-process-mode: strict") {
		t.Errorf("find-process-mode missing:\n%s", s)
	}
	idxRules := strings.Index(s, "rules:")
	idxPR := strings.Index(s, "PROCESS-NAME,Telegram,节点选择")
	idxMatch := strings.Index(s, "MATCH,节点选择")
	if idxPR < 0 || idxPR < idxRules || idxPR > idxMatch {
		t.Errorf("process rule not first in rules block:\n%s", s)
	}
}

// Orphan cleanup must match the core's own -d argument, never another
// instance's config dir.
func TestHasDirArg(t *testing.T) {
	cmd := "/Library/Application Support/clashx-go/mihomo -d /Users/x/.config/clashx-go -f /Users/x/.config/clashx-go/config.yaml -ext-ctl 127.0.0.1:9091"
	if !hasDirArg(cmd, "/Users/x/.config/clashx-go") {
		t.Error("matching config dir not found")
	}
	if hasDirArg(cmd, "/Users/x/.config/clash.meta") {
		t.Error("matched another config dir")
	}
}

func TestProfileMatchTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.yaml")
	profile := "rules:\n- DOMAIN-SUFFIX,x.com,DIRECT\n- MATCH,PROXY\n"
	if err := os.WriteFile(path, []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := profileMatchTarget(path); got != "PROXY" {
		t.Errorf("profileMatchTarget = %q, want PROXY", got)
	}
}

// The injected list must follow the profile's own indentation, including a
// zero-indented rules list.
func TestInjectProcessRulesIndent(t *testing.T) {
	src := "rules:\n- MATCH,DIRECT\n"
	out := injectProcessRules(src, []string{"PROCESS-NAME,Steam,DIRECT"}, "DIRECT")
	want := "rules:\n- PROCESS-NAME,Steam,DIRECT\n- MATCH,DIRECT\n"
	if out != want {
		t.Errorf("injected = %q, want %q", out, want)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The controller Unix socket is written into both generated and overlaid
// configs, and a profile's own socket is replaced like the other app-owned
// keys.
func TestControllerSocketConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.ControllerSocket = "/Users/x/.config/clashx-go/run/controller.sock"
	path, err := cfg.WriteYAML(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := string(mustRead(t, path))
	if !strings.Contains(s, `external-controller-unix: "/Users/x/.config/clashx-go/run/controller.sock"`) {
		t.Errorf("generated config missing unix socket:\n%s", s)
	}

	src := filepath.Join(dir, "profile.yaml")
	dest := filepath.Join(dir, "running.yaml")
	profile := "external-controller-unix: /old.sock\nproxy-groups:\n  - name: A\n    type: select\nrules:\n  - MATCH,A\n"
	if err := os.WriteFile(src, []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeRunningConfig(src, dest, cfg); err != nil {
		t.Fatal(err)
	}
	s = string(mustRead(t, dest))
	if strings.Contains(s, "/old.sock") {
		t.Errorf("profile socket not replaced:\n%s", s)
	}
	if !strings.Contains(s, `external-controller-unix: "/Users/x/.config/clashx-go/run/controller.sock"`) {
		t.Errorf("overlaid config missing new socket:\n%s", s)
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

// TestWriteRunningConfig checks that app-managed keys in a user profile are
// replaced (including the whole tun: block) while everything else survives.
func TestWriteRunningConfig(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "profile.yaml")
	dest := filepath.Join(dir, "running.yaml")
	profile := "Gmixed-port: 7890\n" + // corrupted key from a bad download: kept, ignored
		"mixed-port: 1111\n" +
		"allow-lan: true\n" +
		"tun:\n  enable: true\n  stack: mixed\n" +
		"mode: Rule\n" +
		"proxy-groups:\n  - name: A\n    type: select\n"
	if err := os.WriteFile(src, []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.MixedPort = 7890
	if _, err := writeRunningConfig(src, dest, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{"mixed-port: 7890", "allow-lan: false", "Gmixed-port", "name: A", "mode: Rule", "tun:\n  enable: false"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	for _, bad := range []string{"mixed-port: 1111", "allow-lan: true", "stack: mixed"} {
		if strings.Contains(s, bad) {
			t.Errorf("should not contain %q in:\n%s", bad, s)
		}
	}
	if n := len(GroupOrder(dest)); n != 1 {
		t.Errorf("group order lost: %d", n)
	}
}
