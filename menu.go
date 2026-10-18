package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"

	"clashx-go/core"
)

// trayMenu is the ClashX-style menu bar menu. It owns the native menu and
// the item handles the state poll refreshes in place; the whole menu is
// rebuilt only when the proxy groups' structure changes.
type trayMenu struct {
	app *app

	menu *mygo.Menu

	modeItem   *mygo.MenuItem
	modeRadio  map[string]*mygo.MenuItem
	mode       string
	coreItem   *mygo.MenuItem
	coreRun    bool
	sysProxy   *mygo.MenuItem
	loginItem  *mygo.MenuItem
	speedItem  *mygo.MenuItem
	lanItem    *mygo.MenuItem
	groups     []*groupEntry
	groupSig   string
	speedTitle string
}

// groupEntry tracks one policy group: its top-level item (labelled with the
// current selection) and its member radio items.
type groupEntry struct {
	name    string
	item    *mygo.MenuItem
	members map[string]*mygo.MenuItem
	labels  map[string]string // last rendered label per member, to skip no-op updates
	current string
}

var modeNames = map[string]string{"rule": "规则", "global": "全局", "direct": "直连"}

// build constructs the whole menu from the current state and settings.
func (m *trayMenu) build(s core.State) {
	a := m.app
	st := a.settings
	m.groups = nil

	m.modeRadio = map[string]*mygo.MenuItem{}
	var modeSub []*mygo.MenuItem
	for _, mode := range []string{"rule", "global", "direct"} {
		it := &mygo.MenuItem{
			Label:   modeNames[mode],
			Type:    mygo.MenuItemRadio,
			Checked: s.Mode == mode,
		}
		mode := mode
		it.Click = func(*mygo.MenuItem, *mygo.Window) { go a.setMode(mode) }
		m.modeRadio[mode] = it
		modeSub = append(modeSub, it)
	}
	m.mode = s.Mode
	m.modeItem = &mygo.MenuItem{Label: "出站模式 (" + modeNames[s.Mode] + ")", Submenu: modeSub}

	m.coreItem = &mygo.MenuItem{Label: coreItemLabel(s.Running), Click: func(*mygo.MenuItem, *mygo.Window) { a.toggleCore() }}

	items := []*mygo.MenuItem{m.modeItem, m.coreItem, mygo.Separator()}
	for _, g := range s.Groups {
		items = append(items, m.groupItem(g))
	}
	items = append(items, mygo.Separator())

	m.sysProxy = &mygo.MenuItem{
		Label:       "设置为系统代理",
		Type:        mygo.MenuItemCheckbox,
		Checked:     st.systemProxy,
		Accelerator: "Cmd+S",
	}
	m.sysProxy.Click = func(it *mygo.MenuItem, _ *mygo.Window) { a.setSystemProxyToggle(it) }
	tun := &mygo.MenuItem{
		Label:       "Tun 模式",
		Type:        mygo.MenuItemCheckbox,
		Disabled:    true,
		Accelerator: "Cmd+E",
		ToolTip:     "需要管理员权限，暂未实现",
	}
	copyCmd := &mygo.MenuItem{Label: "复制终端代理命令", Accelerator: "Cmd+C", Click: func(*mygo.MenuItem, *mygo.Window) { a.copyProxyCommand() }}
	items = append(items, m.sysProxy, tun, copyCmd, mygo.Separator())

	m.loginItem = &mygo.MenuItem{
		Label:   "开机启动",
		Type:    mygo.MenuItemCheckbox,
		Checked: st.startAtLogin,
	}
	m.loginItem.Click = func(it *mygo.MenuItem, _ *mygo.Window) { a.setLoginToggle(it) }
	m.speedItem = &mygo.MenuItem{
		Label:   "显示实时速率",
		Type:    mygo.MenuItemCheckbox,
		Checked: st.showTraffic,
	}
	m.speedItem.Click = func(it *mygo.MenuItem, _ *mygo.Window) {
		st.showTraffic = it.IsChecked()
		saveSettings(coreDir(), st)
		m.setSpeedTitle(a.ctrl.State())
	}
	m.lanItem = &mygo.MenuItem{
		Label:   "允许局域网连接",
		Type:    mygo.MenuItemCheckbox,
		Checked: st.allowLAN,
	}
	m.lanItem.Click = func(it *mygo.MenuItem, _ *mygo.Window) { a.setLANToggle(it) }
	items = append(items, m.loginItem, m.speedItem, m.lanItem, mygo.Separator())

	testAll := &mygo.MenuItem{Label: "延迟测速", Accelerator: "Cmd+T", Click: func(*mygo.MenuItem, *mygo.Window) { a.testAllGroups() }}
	console := &mygo.MenuItem{Label: "控制台", Accelerator: "Cmd+D", Click: func(*mygo.MenuItem, *mygo.Window) { a.showPanel(tabOverview) }}
	items = append(items, testAll, console, mygo.Separator())

	config := &mygo.MenuItem{Label: "配置", Submenu: []*mygo.MenuItem{
		{Label: "更新订阅", Click: func(*mygo.MenuItem, *mygo.Window) { a.updateSubscription() }},
		{Label: "托管配置…", Click: func(*mygo.MenuItem, *mygo.Window) { a.showPanel(tabSettings) }},
		{Label: "本地配置…", Click: func(*mygo.MenuItem, *mygo.Window) { a.chooseLocalConfig() }},
		mygo.Separator(),
		{Label: "打开配置文件夹", Click: func(*mygo.MenuItem, *mygo.Window) { _ = mygo.Shell.OpenPath(coreDir()) }},
	}}
	more := &mygo.MenuItem{Label: "更多设置", Submenu: []*mygo.MenuItem{
		{Label: "偏好设置…", Accelerator: "Cmd+,", Click: func(*mygo.MenuItem, *mygo.Window) { a.showPanel(tabSettings) }},
	}}
	meta := &mygo.MenuItem{Label: "Meta", Submenu: []*mygo.MenuItem{
		{Label: "重载核心配置", Click: func(*mygo.MenuItem, *mygo.Window) { a.reloadCore() }},
		{Label: "关闭所有连接", Click: func(*mygo.MenuItem, *mygo.Window) { go a.closeConnections() }},
	}}
	items = append(items, config, more, meta, mygo.Separator(), &mygo.MenuItem{Role: mygo.RoleQuit})

	m.menu = mygo.NewMenu(items)
	m.groupSig = groupsSignature(s.Groups)
	m.setSpeedTitle(s)
}

// groupItem renders one policy group as a top-level item whose submenu holds
// a delay-test header and the member radio items.
func (m *trayMenu) groupItem(g *core.GroupView) *mygo.MenuItem {
	ge := &groupEntry{name: g.Name, members: map[string]*mygo.MenuItem{}, labels: map[string]string{}, current: g.Current}
	group := g.Name
	sub := []*mygo.MenuItem{
		{Label: "延迟测速", Click: func(*mygo.MenuItem, *mygo.Window) { m.app.testGroup(group) }},
		mygo.Separator(),
	}
	for _, name := range g.Members {
		it := &mygo.MenuItem{
			Label:   memberLabel(name, g.Delays),
			Type:    mygo.MenuItemRadio,
			Checked: name == g.Current,
		}
		name := name
		it.Click = func(*mygo.MenuItem, *mygo.Window) { m.app.selectProxy(group, name) }
		ge.members[name] = it
		ge.labels[name] = it.Label
		sub = append(sub, it)
	}
	ge.item = &mygo.MenuItem{Label: groupLabel(g.Name, g.Current), Submenu: sub}
	m.groups = append(m.groups, ge)
	return ge.item
}

// refresh syncs the menu with a new state snapshot. Called on the main
// thread from the window update.
func (m *trayMenu) refresh(s core.State) {
	a := m.app
	if groupsSignature(s.Groups) != m.groupSig {
		m.build(s)
		a.tray.SetMenu(m.menu)
		return
	}
	if s.Mode != m.mode {
		m.mode = s.Mode
		m.modeItem.SetLabel("出站模式 (" + modeNames[s.Mode] + ")")
		for mode, it := range m.modeRadio {
			it.SetChecked(mode == s.Mode)
		}
	}
	want := coreItemLabel(s.Running)
	if s.Running != m.coreRun {
		m.coreRun = s.Running
		m.coreItem.SetLabel(want)
	}
	for _, g := range s.Groups {
		ge := m.findGroup(g.Name)
		if ge == nil {
			continue
		}
		if g.Current != ge.current {
			ge.current = g.Current
			ge.item.SetLabel(groupLabel(g.Name, g.Current))
			for name, it := range ge.members {
				it.SetChecked(name == g.Current)
			}
		}
		for name, it := range ge.members {
			if want := memberLabel(name, g.Delays); want != ge.labels[name] {
				ge.labels[name] = want
				it.SetLabel(want)
			}
		}
	}
	m.setSpeedTitle(s)
}

func (m *trayMenu) findGroup(name string) *groupEntry {
	for _, ge := range m.groups {
		if ge.name == name {
			return ge
		}
	}
	return nil
}

// setSpeedTitle mirrors ClashX's traffic read-out next to the menu bar icon.
func (m *trayMenu) setSpeedTitle(s core.State) {
	want := ""
	if m.app.settings.showTraffic && s.Running {
		want = fmt.Sprintf("↓ %s ↑ %s", humanSpeed(s.Traffic.Down), humanSpeed(s.Traffic.Up))
	}
	if want != m.speedTitle {
		m.speedTitle = want
		m.app.tray.SetTitle(want)
	}
}

// --- menu actions (main thread) ---

func (a *app) setMode(mode string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.ctrl.SetMode(ctx, mode); err != nil {
		a.showToast("切换模式失败", err.Error())
	}
}

func (a *app) toggleCore() {
	if a.ctrl.State().Running {
		a.stopCore()
	} else {
		go a.startCore()
	}
}

func (a *app) setSystemProxyToggle(it *mygo.MenuItem) {
	on := it.IsChecked()
	go func() {
		if err := setSystemProxy(on, a.mixedPort()); err != nil {
			a.settings.systemProxy = !on
			saveSettings(coreDir(), a.settings)
			a.menu.sysProxy.SetChecked(!on)
			a.showToast("系统代理", err.Error())
			return
		}
		a.settings.systemProxy = on
		saveSettings(coreDir(), a.settings)
	}()
}

func (a *app) setLoginToggle(it *mygo.MenuItem) {
	on := it.IsChecked()
	if err := mygo.App.SetOpenAtLogin(on); err != nil {
		it.SetChecked(!on)
		a.showToast("开机启动", "无法修改登录项："+err.Error())
		return
	}
	a.settings.startAtLogin = mygo.App.OpenAtLogin()
	saveSettings(coreDir(), a.settings)
}

func (a *app) setLANToggle(it *mygo.MenuItem) {
	a.settings.allowLAN = it.IsChecked()
	saveSettings(coreDir(), a.settings)
	go func() {
		if err := a.restartCore(a.settingsConfig()); err != nil {
			a.showToast("局域网", "重启核心失败："+err.Error())
		}
	}()
}

func (a *app) copyProxyCommand() {
	port := a.mixedPort()
	cmd := fmt.Sprintf("export http_proxy=http://127.0.0.1:%d\nexport https_proxy=http://127.0.0.1:%d\nexport all_proxy=socks5://127.0.0.1:%d", port, port, port)
	if err := mygo.Clipboard.Write(transfer.TextData(cmd)); err != nil {
		a.showToast("复制失败", err.Error())
	}
}

func (a *app) testGroup(group string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := a.ctrl.TestGroup(ctx, group); err != nil {
		a.showToast("延迟测速", fmt.Sprintf("%s: %v", group, err))
	}
}

func (a *app) testAllGroups() {
	for _, g := range a.ctrl.State().Groups {
		go a.testGroup(g.Name)
	}
}

func (a *app) selectProxy(group, member string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.ctrl.SelectProxy(ctx, group, member); err != nil {
		a.showToast("切换节点失败", err.Error())
	}
}

func (a *app) updateSubscription() {
	go func() {
		n, err := a.ctrl.RefreshSubscription(context.Background())
		if err != nil {
			a.showToast("订阅更新失败", err.Error())
			return
		}
		a.showToast("订阅已更新", fmt.Sprintf("%d 个节点已重载。", n))
	}()
}

func (a *app) chooseLocalConfig() {
	files, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:   "选择本地 Clash 配置",
		Filters: []mygo.FileFilter{{Name: "YAML 配置", Extensions: []string{"yaml", "yml"}}},
	})
	if err != nil || len(files) == 0 {
		return
	}
	a.settings.configPath = files[0]
	saveSettings(coreDir(), a.settings)
	go func() {
		if err := a.restartCore(a.settingsConfig()); err != nil {
			a.showToast("加载本地配置失败", err.Error())
		}
	}()
}

func (a *app) reloadCore() {
	go func() {
		if err := a.restartCore(a.settingsConfig()); err != nil {
			a.showToast("重载失败", err.Error())
		}
	}()
}

func (a *app) closeConnections() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = a.ctrl.CloseConnections(ctx)
}

func (a *app) mixedPort() int {
	return parsePort(a.settings.mixedPort, core.DefaultConfig().MixedPort)
}

// --- label helpers ---

func coreItemLabel(running bool) string {
	if running {
		return "停止核心"
	}
	return "启动核心"
}

func groupLabel(name, current string) string {
	if current == "" {
		return name
	}
	return name + "  (" + current + ")"
}

func memberLabel(name string, delays map[string]int) string {
	d, ok := delays[name]
	switch {
	case !ok || d == 0:
		return name
	case d < 0:
		return name + "  ·  超时"
	default:
		return fmt.Sprintf("%s  ·  %d ms", name, d)
	}
}

// groupsSignature identifies the menu structure: group names and members.
// Selections and delays change in place and must not trigger a rebuild.
func groupsSignature(groups []*core.GroupView) string {
	var b strings.Builder
	for _, g := range groups {
		b.WriteString(g.Name)
		b.WriteString(strings.Join(g.Members, "\n"))
		b.WriteString("\x00")
	}
	return b.String()
}

func humanSpeed(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B/s", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB/s", float64(n)/float64(div), "KMGTPE"[exp])
}
