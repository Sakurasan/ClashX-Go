package main

import (
	"testing"

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

func TestPanelOverview(t *testing.T) {
	a := testApp(t)
	p := &panelApp{app: a}
	tt := ui.NewTester(p.view, 420, 560)
	if !tt.HasText("ClashX Go") {
		t.Errorf("title not rendered, texts %q", tt.Texts())
	}
	if !tt.HasText("核心已停止") {
		t.Errorf("stopped hint not rendered, texts %q", tt.Texts())
	}
	if !tt.HasText("启动核心") {
		t.Errorf("start button not rendered, texts %q", tt.Texts())
	}
}

// TestPanelSettings exercises the Settings tab, which renders without the
// core running and needs no controller endpoints to build.
func TestPanelSettings(t *testing.T) {
	a := testApp(t)
	p := &panelApp{app: a}
	p.tab = tabSettings
	tt := ui.NewTester(p.view, 420, 560)
	if !tt.HasText("核心端点") {
		t.Errorf("settings header not rendered, texts %q", tt.Texts())
	}
	if !tt.HasText("混合端口") {
		t.Errorf("mixed port field missing, texts %q", tt.Texts())
	}
	if !tt.HasText("保存并重启核心") {
		t.Errorf("save button missing, texts %q", tt.Texts())
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
