package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
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
	tunItem    *mygo.MenuItem
	loginItem  *mygo.MenuItem
	speedItem  *mygo.MenuItem
	lanItem    *mygo.MenuItem
	subInfo    string
	subInfoIt  *mygo.MenuItem
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

	items := []*mygo.MenuItem{m.modeItem, mygo.Separator()}
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
	m.tunItem = &mygo.MenuItem{
		Label:       "Tun 模式",
		Type:        mygo.MenuItemCheckbox,
		Checked:     st.tun,
		Accelerator: "Cmd+E",
		ToolTip:     "首次开启需授权安装提权组件，之后免密",
	}
	m.tunItem.Click = func(it *mygo.MenuItem, _ *mygo.Window) { a.setTUNToggle(it) }
	copyCmd := &mygo.MenuItem{Label: "复制终端代理命令", Accelerator: "Cmd+C", Click: func(*mygo.MenuItem, *mygo.Window) { a.copyProxyCommand() }}
	items = append(items, m.sysProxy, m.tunItem, copyCmd, mygo.Separator())

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

	testAll := &mygo.MenuItem{Label: "延迟测速", Accelerator: "Cmd+T", Click: func(*mygo.MenuItem, *mygo.Window) { a.testAllGroups(); a.reopenMenu() }}
	console := &mygo.MenuItem{Label: "控制台", Accelerator: "Cmd+D", Click: func(*mygo.MenuItem, *mygo.Window) { a.showPanel(tabOverview) }}
	items = append(items, testAll, console, mygo.Separator())

	m.subInfoIt = &mygo.MenuItem{Label: subInfoLabel(s), Disabled: true}
	config := &mygo.MenuItem{Label: "配置", Submenu: m.configSubmenu()}
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
	m.subInfo = subInfoLabel(s)
	m.setSpeedTitle(s)
}

// groupItem renders one policy group as a top-level item whose submenu holds
// a delay-test header and the member radio items.
func (m *trayMenu) groupItem(g *core.GroupView) *mygo.MenuItem {
	ge := &groupEntry{name: g.Name, members: map[string]*mygo.MenuItem{}, labels: map[string]string{}, current: g.Current}
	group := g.Name
	sub := []*mygo.MenuItem{
		{Label: "延迟测速", Click: func(*mygo.MenuItem, *mygo.Window) { go m.app.testGroup(group); m.app.reopenMenu() }},
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
		log.Printf("menu rebuild: %d groups", len(s.Groups))
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
	if s.Running != m.coreRun {
		m.coreRun = s.Running
		a.updateTrayIcon(s.Running)
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
	if want := subInfoLabel(s); want != m.subInfo {
		m.subInfo = want
		m.subInfoIt.SetLabel(want)
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

// reportError surfaces a menu action failure as a real dialog: the panel
// window is usually hidden, so a queued toast would go unseen.
func (a *app) reportError(title, msg string) {
	log.Printf("%s: %s", title, msg)
	mygo.Dialog.Error(title, msg)
}

// --- menu actions (main thread) ---

func (a *app) setMode(mode string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.ctrl.SetMode(ctx, mode); err != nil {
		a.reportError("切换模式失败", err.Error())
	}
}

func (a *app) setSystemProxyToggle(it *mygo.MenuItem) {
	on := it.IsChecked()
	if on && !a.ctrl.State().Running {
		it.SetChecked(false)
		a.reportError("系统代理", "核心未运行，开启后浏览器会断网；请先启动核心。")
		return
	}
	go func() {
		if err := setSystemProxy(on, a.mixedPort()); err != nil {
			a.settings.systemProxy = !on
			saveSettings(coreDir(), a.settings)
			a.menu.sysProxy.SetChecked(!on)
			a.reportError("系统代理", err.Error())
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
		a.reportError("开机启动", "无法修改登录项："+err.Error())
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
			a.reportError("局域网", "重启核心失败："+err.Error())
		}
	}()
}

// setTUNToggle flips Tun mode. With a privileged core already running the
// switch is a hot API patch (ClashX-instant); otherwise it persists the
// setting and restarts the core, rolling back if the restart fails.
func (a *app) setTUNToggle(it *mygo.MenuItem) {
	on := it.IsChecked()
	a.settings.tun = on
	saveSettings(coreDir(), a.settings)
	mgr := a.ctrl.Manager()
	if on || mgr.Privileged() {
		if a.ctrl.State().Running && mgr.Privileged() {
			go func() {
				offset := mgr.LogSize()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				err := a.ctrl.SetTUN(ctx, on)
				cancel()
				if err == nil && on {
					// mihomo reports a failed utun only in the log.
					time.Sleep(1500 * time.Millisecond)
					err = mgr.TUNErrorAfter(offset)
				}
				if err == nil {
					return
				}
				// Hot switch failed: fall back to a full restart.
				if rerr := a.restartCore(a.settingsConfig()); rerr == nil {
					return
				}
				a.settings.tun = !on
				saveSettings(coreDir(), a.settings)
				a.menu.tunItem.SetChecked(!on)
				a.reportError("Tun 模式", err.Error())
			}()
			return
		}
	}
	go func() {
		err := a.restartCore(a.settingsConfig())
		if err == nil {
			return
		}
		a.settings.tun = !on
		saveSettings(coreDir(), a.settings)
		a.menu.tunItem.SetChecked(!on)
		// A failed elevation must be seen: the panel toast window is hidden.
		a.reportError("Tun 模式", err.Error())
	}()
}

func (a *app) copyProxyCommand() {
	port := a.mixedPort()
	cmd := fmt.Sprintf("export http_proxy=http://127.0.0.1:%d\nexport https_proxy=http://127.0.0.1:%d\nexport all_proxy=socks5://127.0.0.1:%d", port, port, port)
	if err := mygo.Clipboard.Write(transfer.TextData(cmd)); err != nil {
		a.reportError("复制失败", err.Error())
	}
}

func (a *app) testGroup(group string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := a.ctrl.TestGroup(ctx, group); err != nil {
		a.reportError("延迟测速", fmt.Sprintf("%s: %v", group, err))
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
		a.reportError("切换节点失败", err.Error())
	}
}

func (a *app) updateSubscription() {
	p := a.activeSub()
	if p == nil {
		a.reportError("更新订阅", "当前没有选中的托管订阅。")
		a.showSubscriptionDialog()
		return
	}
	go func() {
		n, err := a.updateProfile(p.Name)
		if err != nil {
			a.reportError("订阅更新失败", err.Error())
			return
		}
		a.menu.rebuild()
		a.showToast("订阅已更新", fmt.Sprintf("%s：%d 个节点已重载。", p.Name, n))
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
	a.selectLocalConfig(files[0])
}

func (a *app) reloadCore() {
	go func() {
		if err := a.restartCore(a.settingsConfig()); err != nil {
			a.reportError("重载失败", err.Error())
		}
	}()
}

func (a *app) closeConnections() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = a.ctrl.CloseConnections(ctx)
}

// configSubmenu mirrors ClashX Meta's 配置 menu: the managed profiles list
// with the active one checked, file operations, and the 托管配置 submenu.
func (m *trayMenu) configSubmenu() []*mygo.MenuItem {
	a := m.app
	st := a.settings
	var items []*mygo.MenuItem
	for _, p := range st.profiles {
		p := p
		it := &mygo.MenuItem{
			Label:   p.Name,
			Type:    mygo.MenuItemRadio,
			Checked: st.activeProfile == p.Name,
		}
		it.Click = func(*mygo.MenuItem, *mygo.Window) { a.selectProfile(p.Name) }
		items = append(items, it)
	}
	localLabel := "本地配置…"
	if st.configPath != "" {
		localLabel = "本地配置: " + filepath.Base(st.configPath)
	}
	local := &mygo.MenuItem{
		Label:   localLabel,
		Type:    mygo.MenuItemRadio,
		Checked: st.activeProfile == "" && st.configPath != "",
	}
	local.Click = func(*mygo.MenuItem, *mygo.Window) {
		if st.configPath == "" {
			a.chooseLocalConfig()
		} else {
			a.selectLocalConfig(st.configPath)
		}
	}
	items = append(items, local, mygo.Separator(),
		&mygo.MenuItem{Label: "打开配置文件夹", Accelerator: "Cmd+O",
			Click: func(*mygo.MenuItem, *mygo.Window) { _ = mygo.Shell.OpenPath(coreDir()) }},
		&mygo.MenuItem{Label: "重载配置文件", Accelerator: "Cmd+R",
			Click: func(*mygo.MenuItem, *mygo.Window) { a.reloadCore() }},
		&mygo.MenuItem{Label: "更新外部资源", Accelerator: "Shift+Cmd+U",
			Click: func(*mygo.MenuItem, *mygo.Window) { a.updateSubscription() }},
	)
	managed := &mygo.MenuItem{Label: "托管配置", Submenu: []*mygo.MenuItem{
		{Label: "管理", Accelerator: "Cmd+M", Click: func(*mygo.MenuItem, *mygo.Window) { a.showManageWindow() }},
		{Label: "更新", Accelerator: "Cmd+U", Click: func(*mygo.MenuItem, *mygo.Window) { a.updateSubscription() }},
		m.autoUpdateItem(),
		{Label: "设置更新间隔", Submenu: intervalItems(a)},
	}}
	return append(items, managed)
}

// autoUpdateItem builds the 自动更新 checkbox bound to settings.
func (m *trayMenu) autoUpdateItem() *mygo.MenuItem {
	it := &mygo.MenuItem{
		Label:   "自动更新",
		Type:    mygo.MenuItemCheckbox,
		Checked: m.app.settings.autoUpdate,
	}
	it.Click = func(it *mygo.MenuItem, _ *mygo.Window) {
		m.app.settings.autoUpdate = it.IsChecked()
		saveSettings(coreDir(), m.app.settings)
	}
	return it
}

var updateIntervals = []struct {
	label string
	min   int
}{
	{"30 分钟", 30}, {"1 小时", 60}, {"6 小时", 360}, {"12 小时", 720}, {"24 小时", 1440},
}

func intervalItems(a *app) []*mygo.MenuItem {
	var items []*mygo.MenuItem
	for _, iv := range updateIntervals {
		iv := iv
		it := &mygo.MenuItem{
			Label:   iv.label,
			Type:    mygo.MenuItemRadio,
			Checked: a.settings.updateInterval == iv.min,
		}
		it.Click = func(*mygo.MenuItem, *mygo.Window) {
			a.settings.updateInterval = iv.min
			saveSettings(coreDir(), a.settings)
			a.menu.rebuild()
		}
		items = append(items, it)
	}
	return items
}

// rebuild reconstructs the whole tray menu after a structural change
// (profiles, groups) and reinstalls it on the tray.
func (m *trayMenu) rebuild() {
	m.build(m.app.ctrl.State())
	m.app.tray.SetMenu(m.menu)
}

// reopenMenu re-pops the tray menu after an action ClashX keeps the menu
// open for (延迟测速): NSMenu dismisses on any click, so we show it again
// on the next main-loop pass while the tests run in the background.
func (a *app) reopenMenu() {
	a.win.Update(func() {
		if b := a.tray.Bounds(); b.Width > 0 && b.Height > 0 {
			a.tray.PopUpMenu()
		}
	})
}

func (a *app) mixedPort() int {
	return parsePort(a.settings.mixedPort, core.DefaultConfig().MixedPort)
}

// --- label helpers ---

func groupLabel(name, current string) string {
	if current == "" {
		return name
	}
	return name + "  (" + current + ")"
}

// memberLabel renders a node with a ClashX-style coloured delay badge.
// Native menus cannot draw the coloured pill, so a coloured circle stands
// in: green ≤300ms, orange slower, red for failures.
func memberLabel(name string, delays map[string]int) string {
	d, ok := delays[name]
	switch {
	case !ok || d == 0:
		return name
	case d < 0:
		return name + "  🔴 失败"
	case d <= 300:
		return fmt.Sprintf("%s  🟢 %d ms", name, d)
	default:
		return fmt.Sprintf("%s  🟠 %d ms", name, d)
	}
}

// subInfoLabel renders the subscription quota line shown (disabled) at the
// top of the 配置 submenu, ClashX style.
func subInfoLabel(s core.State) string {
	info := s.SubInfo
	if info == nil || info.Total <= 0 {
		if s.SubProxies > 0 {
			return fmt.Sprintf("订阅：%d 个节点", s.SubProxies)
		}
		return "订阅：无"
	}
	line := fmt.Sprintf("订阅流量 %s / %s · %d 个节点",
		humanBytes(info.Upload+info.Download), humanBytes(info.Total), s.SubProxies)
	if !info.Expire.IsZero() {
		line += " · 到期 " + info.Expire.Local().Format("2006-01-02")
	}
	return line
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
