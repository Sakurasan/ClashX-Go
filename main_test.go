package main

import (
	"archive/zip"
	"bytes"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"clashx-go/core"
)

// The panel view runs without a window in tests. The core is stopped, so
// the Overview tab shows the Start button and the footer a Start button;
// both call startCore which errors without a real core, so we only assert
// the rendered text.
func testApp(t *testing.T) *app {
	t.Helper()
	mgr, err := core.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &app{
		settings: &settings{mixedPort: "7890", controller: "9090"},
		ctrl:     core.NewController(mgr, core.DefaultConfig()),
	}
	return a
}

// TestPanelSettings exercises the settings window, which renders without the
// core running and needs no controller endpoints to build.
func TestPanelSettings(t *testing.T) {
	a := testApp(t)
	p := &panelApp{app: a}
	tt := ui.NewTester(p.view, 520, 720)
	for _, tab := range []string{"通用", "连接", "分流", "全局快捷键", "调试", "Meta"} {
		if !tt.HasText(tab) {
			t.Errorf("tab %q missing, texts %q", tab, tt.Texts())
		}
	}
	if !tt.HasText("代理端口") {
		t.Errorf("mixed port field missing, texts %q", tt.Texts())
	}
	if !tt.HasText("保存并重启核心") {
		t.Errorf("save button missing, texts %q", tt.Texts())
	}
	for _, want := range []string{"界面语言", "跟随系统", "启用 IPv6", "允许局域网控制（不推荐）", "代理延迟测试地址", "在特定 WiFi SSID 下自动暂停", "忽略这些主机与域的代理设置", "减少通知", "自动检查更新", "重置"} {
		if !tt.HasText(want) {
			t.Errorf("meta general setting %q missing, texts %q", want, tt.Texts())
		}
	}
	if tt.HasText("概览") || tt.HasText("暂无策略组。") {
		t.Errorf("legacy panel panes still rendered, texts %q", tt.Texts())
	}
}

// TestPanelTabs renders the shortcuts, debug and Meta tabs.
func TestPanelTabs(t *testing.T) {
	a := testApp(t)
	for tab, want := range map[int]string{
		tabRouting:   "应用规则",
		tabShortcuts: "系统代理",
		tabDebug:     "还原系统代理",
		tabMeta:      "Meta Core: " + core.CoreVersion,
	} {
		p := &panelApp{app: a, tab: tab}
		tt := ui.NewTester(p.view, 520, 720)
		if !tt.HasText(want) {
			t.Errorf("tab %d missing %q, texts %q", tab, want, tt.Texts())
		}
		if tab == tabMeta && !tt.HasText("Tun DNS") {
			t.Errorf("Meta tab missing Tun DNS, texts %q", tt.Texts())
		}
	}
}

// The Help IP must come from a physical LAN interface: a VPN alias on lo0
// (172.30.210.8 here) or the TUN device must not win.
func TestChooseLocalIP(t *testing.T) {
	cands := []lanCandidate{
		{name: "lo0", up: true, loopback: true, ipv4: []string{"172.30.210.8"}},
		{name: "utun4", up: true, ipv4: []string{"198.18.0.1"}},
		{name: "en0", up: true},
		{name: "en1", up: true, ipv4: []string{"192.168.1.4"}},
	}
	if got := chooseLocalIP(cands, "en1"); got != "192.168.1.4" {
		t.Errorf("default-route IP = %q, want 192.168.1.4", got)
	}
	if got := chooseLocalIP(cands, "utun4"); got != "192.168.1.4" {
		t.Errorf("VPN default-route IP = %q, want the physical 192.168.1.4", got)
	}
	if got := chooseLocalIP(cands[:2], ""); got != "—" {
		t.Errorf("virtual-only IP = %q, want —", got)
	}
}

func TestPhysicalInterface(t *testing.T) {
	for _, name := range []string{"en0", "en1", "bridge0"} {
		if !physicalInterface(name) {
			t.Errorf("%s should be physical", name)
		}
	}
	for _, name := range []string{"lo0", "utun0", "utun4", "gif0", "stf0", "awdl0", "llw0", "anpi0"} {
		if physicalInterface(name) {
			t.Errorf("%s should be virtual", name)
		}
	}
}

// TestTrayMenu exercises the ClashX-style menu build and its in-place
// refresh: selections and delays update items without a rebuild.
func TestTrayMenu(t *testing.T) {
	a := testApp(t)
	m := &trayMenu{app: a}
	s := core.State{Mode: "rule", Groups: []*core.GroupView{{
		Name: "PROXY", Current: "HK", Members: []string{"HK", "US"},
		Delays: map[string]int{"US": -1},
	}}}
	m.build(s)
	if got := m.modeItem.Label; got != "出站模式 (规则)" {
		t.Errorf("mode label = %q", got)
	}
	ge := m.groups[0]
	if !ge.members["HK"].IsChecked() || ge.members["US"].IsChecked() {
		t.Error("initial member checks wrong")
	}
	if got := ge.members["US"].Label; got != "US  ❌  失败" {
		t.Errorf("timeout member label = %q", got)
	}

	s2 := core.State{Mode: "global", Groups: []*core.GroupView{{
		Name: "PROXY", Current: "US", Members: []string{"HK", "US"},
		Delays: map[string]int{"US": 210},
	}}}
	m.refresh(s2)
	if got := m.modeItem.Label; got != "出站模式 (全局)" {
		t.Errorf("mode label after refresh = %q", got)
	}
	if !ge.members["US"].IsChecked() || ge.members["HK"].IsChecked() {
		t.Error("member checks not refreshed")
	}
	if got := ge.members["US"].Label; got != "US  ✅  210 ms" {
		t.Errorf("delay label = %q", got)
	}
	if got := ge.item.Label; got != "PROXY  (US)" {
		t.Errorf("group label = %q", got)
	}
}

// Settings edited in the dashboard live only in the core until the app
// adopts them; otherwise the next start reverts them and the menu lies.
func TestAdoptRuntimeSettings(t *testing.T) {
	a := testApp(t)
	a.coreLAN = false
	if a.adoptRuntimeSettings(core.State{Mode: "global", AllowLAN: true}) {
		t.Fatal("unloaded state must not be adopted")
	}
	if a.settings.mode != "" || a.settings.allowLAN {
		t.Fatalf("state adopted before config load: %+v", a.settings)
	}

	s := core.State{ConfigLoaded: true, Mode: "global", AllowLAN: true}
	if !a.adoptRuntimeSettings(s) {
		t.Fatal("runtime change not adopted")
	}
	if a.settings.mode != "global" || !a.settings.allowLAN {
		t.Fatalf("settings = %+v", a.settings)
	}
	if a.adoptRuntimeSettings(s) {
		t.Fatal("unchanged state should not rewrite settings")
	}

	// A LAN edit saved in the settings window is not applied until the
	// restart, so the poll must leave it alone.
	a.settings.allowLAN = false
	if a.adoptRuntimeSettings(s) {
		t.Fatal("pending LAN edit was reverted by an unchanged core")
	}
	if a.settings.allowLAN {
		t.Fatal("pending LAN edit lost")
	}
}

func TestAboutView(t *testing.T) {
	tt := ui.NewTester(aboutView, 400, 460)
	for _, want := range []string{"ClashX Go", "版本: 0.2.6", "Meta Core " + core.CoreVersion, "mihomo", "AGPL-3.0", "打开配置文件夹"} {
		if !tt.HasText(want) {
			t.Errorf("about page missing %q, texts %q", want, tt.Texts())
		}
	}
}

func TestHumanSpeed(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{512, "     0K/s"},
		{47206, "    46K/s"},
		{999 * 1024, "   999K/s"},
		{1024 * 1024, "   1.0M/s"},
		{150 * 1024 * 1024, "   0.1G/s"},
		{2 * 1024 * 1024 * 1024, "   2.0G/s"},
	}
	for _, c := range cases {
		if got := humanSpeed(c.n); got != c.want {
			t.Errorf("humanSpeed(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

// The paw is drawn at full ink when the proxy is active and dimmed (still
// visible, like ClashX Meta's disabled tint) when it is not.
func TestTraySpeedIconDimState(t *testing.T) {
	maxAlpha := func(active bool) uint8 {
		img, err := png.Decode(bytes.NewReader(traySpeedIcon("3K/s", "9.9M/s", active)))
		if err != nil {
			t.Fatal(err)
		}
		var m uint8
		b := img.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < 46; x++ { // paw column only
				_, _, _, a := img.At(x, y).RGBA()
				if v := uint8(a >> 8); v > m {
					m = v
				}
			}
		}
		return m
	}
	if got := maxAlpha(true); got != 255 {
		t.Errorf("active paw alpha = %d, want 255", got)
	}
	if got := maxAlpha(false); got != 110 {
		t.Errorf("inactive paw alpha = %d, want 110", got)
	}
}

// The diagnostics bundle must carry the summary without leaking the API
// secret or subscription tokens.
func TestDiagnosticsRedaction(t *testing.T) {
	a := testApp(t)
	a.settings.secret = "topsecret123"
	a.settings.profiles = []subscriptionProfile{{
		Name: "test", URL: "https://example.com/sub?token=abc123",
	}}
	out := filepath.Join(t.TempDir(), "diag.zip")
	if err := writeDiagnostics(out, t.TempDir(), a); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var summary string
	for _, f := range zr.File {
		if f.Name == "summary.txt" {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(rc)
			rc.Close()
			summary = string(data)
		}
	}
	if summary == "" {
		t.Fatal("summary.txt missing")
	}
	if strings.Contains(summary, "topsecret123") {
		t.Error("summary leaks the API secret")
	}
	if strings.Contains(summary, "token=abc123") {
		t.Error("summary leaks the subscription token")
	}
	if !strings.Contains(summary, "https://example.com/…") {
		t.Errorf("summary missing masked subscription URL: %s", summary)
	}
}

// Selecting an .app reads the executable and display name from its
// Info.plist; the executable is what PROCESS-NAME matches.
func TestAppBundleInfo(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("reads Info.plist with plutil")
	}
	app := filepath.Join(t.TempDir(), "Test App.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>TestApp</string>
<key>CFBundleDisplayName</key><string>Test App Display</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	name, process := appBundleInfo(app)
	if process != "TestApp" {
		t.Errorf("process = %q, want TestApp", process)
	}
	if name != "Test App Display" {
		t.Errorf("name = %q, want Test App Display", name)
	}
}

// The routing pane lists each app with its process name and target choice.
func TestRoutingPane(t *testing.T) {
	a := testApp(t)
	a.settings.appRules = []appRoutingRule{
		{Name: "Telegram", Process: "Telegram", Path: "/Applications/Telegram.app", Target: "proxy"},
		{Name: "Steam", Process: "steam_osx", Path: "/Applications/Steam.app", Target: "direct"},
	}
	p := &panelApp{app: a}
	tt := ui.NewTester(p.routingPane, 520, 620)
	for _, want := range []string{"Telegram", "PROCESS-NAME Telegram", "steam_osx", "直连", "代理", "添加应用…", "保存并应用"} {
		if !tt.HasText(want) {
			t.Errorf("routing pane missing %q, texts %q", want, tt.Texts())
		}
	}
	if a.settings.appRules[0].Target != "proxy" {
		t.Errorf("target mutated during render: %q", a.settings.appRules[0].Target)
	}
}

// The connections page names a destination from the sniffed host, else the
// IP, and always carries the port.
func TestConnHost(t *testing.T) {
	cases := []struct {
		m    core.ConnectionMetadata
		want string
	}{
		{core.ConnectionMetadata{Host: "example.com", DestinationPort: "443"}, "example.com:443"},
		{core.ConnectionMetadata{SniffHost: "sniffed.io", DestinationIP: "1.2.3.4", DestinationPort: "80"}, "sniffed.io:80"},
		{core.ConnectionMetadata{DestinationIP: "1.1.1.1", DestinationPort: "53"}, "1.1.1.1:53"},
		{core.ConnectionMetadata{DestinationIP: "2606:4700::1111", DestinationPort: "443"}, "[2606:4700::1111]:443"},
		{core.ConnectionMetadata{}, "—"},
	}
	for _, tc := range cases {
		if got := connHost(core.Connection{Metadata: tc.m}); got != tc.want {
			t.Errorf("connHost(%+v) = %q, want %q", tc.m, got, tc.want)
		}
	}
}

// The search box matches host, process, rule and chain and keeps the
// newest-first order of the snapshot.
func TestFilteredConnections(t *testing.T) {
	p := &panelApp{connSel: -1, conns: &core.Connections{Connections: []core.Connection{
		{ID: "new", Metadata: core.ConnectionMetadata{Host: "example.com", Process: "Safari"}, RulePayload: "example.com"},
		{ID: "old", Metadata: core.ConnectionMetadata{Host: "api.test", Process: "curl"}, Chains: []string{"HK", "PROXY"}},
	}}}
	if got := p.filteredConnections(); len(got) != 2 || got[0].ID != "new" {
		t.Fatalf("unfiltered = %+v", got)
	}
	for q, wantID := range map[string]string{
		"curl":   "old",
		"Safari": "new",
		"hK":     "old", // case-insensitive chain match
	} {
		p.connFilter = q
		got := p.filteredConnections()
		if len(got) != 1 || got[0].ID != wantID {
			t.Errorf("filter %q = %+v, want %s", q, got, wantID)
		}
	}
	p.connFilter = "nothing-matches"
	if got := p.filteredConnections(); len(got) != 0 {
		t.Errorf("unexpected rows: %+v", got)
	}
}

// TestConnectionsView renders the live table and the empty state.
func TestConnectionsView(t *testing.T) {
	a := testApp(t)
	p := &panelApp{app: a, connSel: -1}
	p.conns = &core.Connections{
		DownloadTotal: 2048,
		UploadTotal:   1024,
		Connections: []core.Connection{{
			ID:          "c1",
			Start:       time.Unix(100, 0),
			Metadata:    core.ConnectionMetadata{Host: "example.com", DestinationPort: "443", Process: "Safari"},
			Chains:      []string{"HK", "PROXY"},
			Rule:        "DomainSuffix",
			RulePayload: "example.com",
			Download:    2 * 1024,
			Upload:      100,
		}},
	}
	tt := ui.NewTester(p.activeConnsView, 520, 620)
	for _, want := range []string{"活动连接 1", "example.com:443", "HK → PROXY", "双击一行查看连接详情"} {
		if !tt.HasText(want) {
			t.Errorf("connections page missing %q, texts %q", want, tt.Texts())
		}
	}

	p.conns = nil
	tt = ui.NewTester(p.activeConnsView, 520, 620)
	if !tt.HasText("正在读取连接…") {
		t.Errorf("loading state missing, texts %q", tt.Texts())
	}
}

// The tab renders the core-stopped state, and the rules sub-view keeps the
// core's matching order.
func TestConnectionsPaneStates(t *testing.T) {
	a := testApp(t)
	p := &panelApp{app: a, tab: tabConnections, connSel: -1}
	tt := ui.NewTester(p.view, 520, 720)
	for _, want := range []string{"活动连接", "规则列表", "核心未运行"} {
		if !tt.HasText(want) {
			t.Errorf("connections tab missing %q, texts %q", want, tt.Texts())
		}
	}

	p.connView = connViewRules
	p.rules = []core.Rule{
		{Type: "DomainSuffix", Payload: "example.com", Proxy: "Proxies"},
		{Type: "MATCH", Payload: "", Proxy: "DIRECT"},
	}
	tt = ui.NewTester(p.rulesView, 520, 620)
	for _, want := range []string{"DomainSuffix", "example.com", "Proxies", "共 2 条规则"} {
		if !tt.HasText(want) {
			t.Errorf("rules view missing %q, texts %q", want, tt.Texts())
		}
	}
	p.ruleFilter = "direct" // case-insensitive policy match
	if got := p.filteredRules(); len(got) != 1 || got[0].Type != "MATCH" {
		t.Errorf("filteredRules(direct) = %+v", got)
	}
}

// The environment override wins over the saved setting; a saved "en" or
// "zh" wins over the system language.
func TestResolveLang(t *testing.T) {
	t.Setenv("CLASHX_GO_LANG", "")
	if got := resolveLang("en"); got != "en" {
		t.Errorf("resolveLang(en) = %q", got)
	}
	if got := resolveLang("zh"); got != "zh" {
		t.Errorf("resolveLang(zh) = %q", got)
	}
	t.Setenv("CLASHX_GO_LANG", "en")
	if got := resolveLang("zh"); got != "en" {
		t.Errorf("env override = %q, want en", got)
	}
	t.Setenv("CLASHX_GO_LANG", "zh")
	if got := resolveLang("en"); got != "zh" {
		t.Errorf("env override = %q, want zh", got)
	}
}

func TestMain(m *testing.M) {
	// Tests assert the Chinese UI; CI runners default to English.
	os.Setenv("CLASHX_GO_LANG", "zh")
	uiLang = "zh"
	os.Exit(m.Run())
}

func TestPanelSettingsEnglish(t *testing.T) {
	old := uiLang
	uiLang = "en"
	defer func() { uiLang = old }()
	a := testApp(t)
	p := &panelApp{app: a}
	tt := ui.NewTester(p.view, 520, 720)
	for _, want := range []string{"Connections", "Interface Language", "System", "Proxy Port", "Save & Restart Core", "Enable IPv6", "Check for Updates Automatically"} {
		if !tt.HasText(want) {
			t.Errorf("english UI missing %q, texts %q", want, tt.Texts())
		}
	}
}
