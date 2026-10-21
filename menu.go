package main

import (
	"context"
	"fmt"
	"log"
	"net"
	neturl "net/url"
	"os"
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
	level      string
	levelRadio map[string]*mygo.MenuItem
	httpIt     *mygo.MenuItem
	apiIt      *mygo.MenuItem
	ipIt       *mygo.MenuItem
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

var modeNames = map[string]string{"rule": T("规则"), "global": T("全局"), "direct": T("直连")}

// build constructs the whole menu from the current state and settings.
func (m *trayMenu) build(s core.State) {
	a := m.app
	st := a.settings
	m.groups = nil

	m.modeRadio = map[string]*mygo.MenuItem{}
	var modeSub []*mygo.MenuItem
	modeAccel := map[string]string{"rule": "Alt+R", "global": "Alt+G", "direct": "Alt+D"}
	for _, mode := range []string{"rule", "global", "direct"} {
		it := &mygo.MenuItem{
			Label:       modeNames[mode],
			Type:        mygo.MenuItemRadio,
			Checked:     s.Mode == mode,
			Accelerator: modeAccel[mode],
		}
		mode := mode
		it.Click = func(*mygo.MenuItem, *mygo.Window) { go a.setMode(mode) }
		m.modeRadio[mode] = it
		modeSub = append(modeSub, it)
	}
	m.mode = s.Mode
	m.modeItem = &mygo.MenuItem{Label: T("出站模式") + " (" + modeNames[s.Mode] + ")", Submenu: modeSub}

	items := []*mygo.MenuItem{m.modeItem, mygo.Separator()}
	for _, g := range s.Groups {
		items = append(items, m.groupItem(g))
	}
	items = append(items, mygo.Separator())

	m.sysProxy = &mygo.MenuItem{
		Label:       T("设置为系统代理"),
		Type:        mygo.MenuItemCheckbox,
		Checked:     st.systemProxy,
		Accelerator: "Cmd+S",
	}
	m.sysProxy.Click = func(it *mygo.MenuItem, _ *mygo.Window) { a.setSystemProxyToggle(it) }
	m.tunItem = &mygo.MenuItem{
		Label:       T("Tun 模式"),
		Type:        mygo.MenuItemCheckbox,
		Checked:     st.tun,
		Accelerator: "Cmd+E",
		ToolTip:     "首次开启需授权安装提权组件，之后免密",
	}
	m.tunItem.Click = func(it *mygo.MenuItem, _ *mygo.Window) { a.setTUNToggle(it) }
	copyCmd := &mygo.MenuItem{Label: T("复制终端代理命令"), Accelerator: "Cmd+C", Click: func(*mygo.MenuItem, *mygo.Window) { a.copyProxyCommand() }}
	items = append(items, m.sysProxy, m.tunItem, copyCmd, mygo.Separator())

	m.loginItem = &mygo.MenuItem{
		Label:   T("开机启动"),
		Type:    mygo.MenuItemCheckbox,
		Checked: st.startAtLogin,
	}
	m.loginItem.Click = func(it *mygo.MenuItem, _ *mygo.Window) { a.setLoginToggle(it) }
	m.speedItem = &mygo.MenuItem{
		Label:   T("显示实时速率"),
		Type:    mygo.MenuItemCheckbox,
		Checked: st.showTraffic,
	}
	m.speedItem.Click = func(it *mygo.MenuItem, _ *mygo.Window) {
		st.showTraffic = it.IsChecked()
		saveSettings(coreDir(), st)
		m.setSpeedTitle(a.ctrl.State())
	}
	m.lanItem = &mygo.MenuItem{
		Label:   T("允许局域网连接"),
		Type:    mygo.MenuItemCheckbox,
		Checked: st.allowLAN,
	}
	m.lanItem.Click = func(it *mygo.MenuItem, _ *mygo.Window) { a.setLANToggle(it) }
	items = append(items, m.loginItem, m.speedItem, m.lanItem, mygo.Separator())

	testAll := &mygo.MenuItem{Label: T("延迟测速"), Accelerator: "Cmd+T", Click: func(*mygo.MenuItem, *mygo.Window) { a.testAllGroups(); a.reopenMenu() }}
	console := &mygo.MenuItem{Label: T("控制台"), Accelerator: "Cmd+D", Click: func(*mygo.MenuItem, *mygo.Window) { a.openConsole() }}
	items = append(items, testAll, console, mygo.Separator())

	m.subInfoIt = &mygo.MenuItem{Label: subInfoLabel(s), Disabled: true}
	config := &mygo.MenuItem{Label: T("配置"), Submenu: m.configSubmenu()}
	more := &mygo.MenuItem{Label: T("更多设置"), Submenu: []*mygo.MenuItem{
		{Label: T("偏好设置…"), Accelerator: "Cmd+,", Click: func(*mygo.MenuItem, *mygo.Window) { a.showSettings() }},
	}}
	meta := &mygo.MenuItem{Label: "Meta", Submenu: []*mygo.MenuItem{
		{Label: T("重载核心配置"), Click: func(*mygo.MenuItem, *mygo.Window) { a.reloadCore() }},
		{Label: T("关闭所有连接"), Click: func(*mygo.MenuItem, *mygo.Window) { go a.closeConnections() }},
	}}
	items = append(items, config, more, meta, m.helpSubmenu(s), mygo.Separator(), &mygo.MenuItem{Role: mygo.RoleQuit})

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
		{Label: T("延迟测速"), Click: func(*mygo.MenuItem, *mygo.Window) { go m.app.testGroup(group); m.app.reopenMenu() }},
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
		m.modeItem.SetLabel(T("出站模式") + " (" + modeNames[s.Mode] + ")")
		for mode, it := range m.modeRadio {
			it.SetChecked(mode == s.Mode)
		}
	}
	if s.Running != m.coreRun {
		m.coreRun = s.Running
		m.speedTitle = "" // force the speed bitmap to redraw with the new state
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

// setSpeedTitle mirrors ClashX's traffic read-out: the paw plus two speed
// lines, composed into one template bitmap (the tray title cannot show
// two lines). Like ClashX Meta, the paw is dimmed while neither system
// proxy nor TUN routes traffic.
func (m *trayMenu) setSpeedTitle(s core.State) {
	a := m.app
	active := a.proxyActive()
	if a.settings.showTraffic && s.Running {
		up, down := humanSpeed(s.Traffic.Up), humanSpeed(s.Traffic.Down)
		if key := fmt.Sprintf("%s|%s|%t", up, down, active); key != m.speedTitle {
			m.speedTitle = key
			_ = a.tray.SetIcon(traySpeedIcon(up, down, active), true)
		}
		return
	}
	if m.speedTitle != "" {
		m.speedTitle = ""
		a.updateTrayIcon(s.Running)
	}
}

// refreshTrayIcon redraws the status icon after a proxy/TUN toggle so the
// dimmed state updates immediately instead of on the next speed tick.
func (a *app) refreshTrayIcon() {
	s := a.ctrl.State()
	if a.settings.showTraffic && s.Running {
		a.menu.speedTitle = "" // force a redraw with the same numbers
		a.menu.setSpeedTitle(s)
		return
	}
	a.updateTrayIcon(s.Running)
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
		a.reportError(T("切换模式失败"), err.Error())
	}
}

func (a *app) setSystemProxyToggle(it *mygo.MenuItem) {
	on := it.IsChecked()
	if on && !a.ctrl.State().Running {
		it.SetChecked(false)
		a.reportError("系统代理", "核心未运行，开启后浏览器会断网；请先启动核心。")
		return
	}
	a.setSystemProxy(on)
}

// setSystemProxy enables or restores the system proxy; shared by the menu
// item and the global shortcut.
func (a *app) setSystemProxy(on bool) {
	go func() {
		if on {
			if !core.PortListening(a.mixedPort()) {
				// Pointing the system proxy at a dead port breaks every app;
				// refuse instead and say what to do.
				a.settings.systemProxy = false
				saveSettings(coreDir(), a.settings)
				if a.menu != nil && a.menu.sysProxy != nil {
					a.menu.sysProxy.SetChecked(false)
				}
				a.refreshTrayIcon()
				a.reportError("系统代理", fmt.Sprintf(
					"代理端口 %d 没有监听（可能被其他代理占用）。请退出其他 Clash，或在偏好设置里更换代理端口后保存并重启核心。",
					a.mixedPort()))
				return
			}
			if a.settings.proxyBackup == nil {
				if b, err := snapshotProxy(); err == nil {
					a.settings.proxyBackup = b
				}
			}
			if err := pointProxyAt(a.mixedPort(), splitList(a.settings.proxyBypass)); err != nil {
				a.settings.systemProxy = false
				saveSettings(coreDir(), a.settings)
				if a.menu != nil && a.menu.sysProxy != nil {
					a.menu.sysProxy.SetChecked(false)
				}
				a.refreshTrayIcon()
				a.reportError("系统代理", err.Error())
				return
			}
			a.settings.systemProxy = true
			saveSettings(coreDir(), a.settings)
			a.refreshTrayIcon()
			return
		}
		// Off: put the user's own proxy config back (ClashX restoreProxy).
		err := restoreProxy(a.settings.proxyBackup)
		if err != nil {
			a.reportError("系统代理", "恢复原代理设置失败："+err.Error())
		}
		a.settings.systemProxy = false
		a.settings.proxyBackup = nil
		saveSettings(coreDir(), a.settings)
		a.refreshTrayIcon()
	}()
}

func (a *app) setLoginToggle(it *mygo.MenuItem) {
	on := it.IsChecked()
	if err := mygo.App.SetOpenAtLogin(on); err != nil {
		it.SetChecked(!on)
		a.reportError(T("开机启动"), "无法修改登录项："+err.Error())
		return
	}
	a.settings.startAtLogin = mygo.App.OpenAtLogin()
	saveSettings(coreDir(), a.settings)
}

// setLANToggle patches allow-lan live on a running core (ClashX does not
// restart either); only a cold core goes through the config path.
func (a *app) setLANToggle(it *mygo.MenuItem) {
	on := it.IsChecked()
	a.settings.allowLAN = on
	saveSettings(coreDir(), a.settings)
	go func() {
		if a.ctrl.State().Running {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := a.ctrl.SetAllowLAN(ctx, on)
			cancel()
			if err == nil {
				return
			}
			log.Printf("局域网热切换失败，转重启: %v", err)
		}
		if err := a.applyConfig(a.settingsConfig()); err != nil {
			log.Printf("局域网重启待重试: %v", err)
		}
	}()
}

// setTUNToggle flips Tun mode. With a privileged core already running the
// switch is a hot API patch (ClashX-instant); otherwise it persists the
// setting and restarts the core, rolling back if the restart fails.
func (a *app) setTUNToggle(it *mygo.MenuItem) {
	a.setTUN(it.IsChecked())
}

// setTUN flips Tun mode; shared by the menu item and the global shortcut.
// With a privileged core already running the switch is a hot API patch
// (ClashX-instant); otherwise it persists the setting and restarts the
// core, rolling back if the restart fails.
func (a *app) setTUN(on bool) {
	a.settings.tun = on
	saveSettings(coreDir(), a.settings)
	a.refreshTrayIcon()
	mgr := a.ctrl.Manager()
	if on && a.ctrl.State().Running && !mgr.Privileged() {
		// A core started without the setuid copy cannot open a utun; a live
		// PATCH only logs "operation not permitted". Restart with TUN so
		// Start installs/uses the privileged binary (one-time authorization).
		go func() {
			if err := a.restartCore(a.settingsConfig()); err != nil {
				log.Printf("TUN 重启待重试: %v", err)
			}
		}()
		return
	}
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
				// Hot switch failed: fall back to a full restart; its
				// retry loop reports if the core still cannot come up.
				err = a.applyConfig(a.settingsConfig())
				if err == nil {
					return
				}
				log.Printf("TUN 重启待重试: %v", err)
			}()
			return
		}
	}
	go func() {
		if err := a.applyConfig(a.settingsConfig()); err != nil {
			log.Printf("TUN 重启待重试: %v", err)
		}
	}()
}

func (a *app) copyProxyCommand() {
	port := a.mixedPort()
	cmd := fmt.Sprintf("export https_proxy=http://127.0.0.1:%d http_proxy=http://127.0.0.1:%d all_proxy=socks5://127.0.0.1:%d", port, port, port)
	if err := mygo.Clipboard.Write(transfer.TextData(cmd)); err != nil {
		a.reportError(T("复制失败"), err.Error())
	}
}

func (a *app) testGroup(group string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := a.ctrl.TestGroup(ctx, group); err != nil {
		a.reportError(T("延迟测速"), fmt.Sprintf("%s: %v", group, err))
	}
}

func (a *app) testAllGroups() {
	for _, g := range a.ctrl.State().Groups {
		go a.testGroup(g.Name)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		a.ctrl.HealthCheckProviders(ctx)
	}()
}

func (a *app) selectProxy(group, member string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.ctrl.SelectProxy(ctx, group, member); err != nil {
		a.reportError(T("切换节点失败"), err.Error())
		return
	}
	a.recordSelection(group, member)
}

func (a *app) updateSubscription() {
	p := a.activeSub()
	if p == nil {
		a.reportError(T("更新订阅"), "当前没有选中的托管订阅。")
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
		a.showToast(T("订阅已更新"), fmt.Sprintf("%s：%d 个节点已重载。", p.Name, n))
	}()
}

func (a *app) chooseLocalConfig() {
	files, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:   T("选择本地 Clash 配置"),
		Filters: []mygo.FileFilter{{Name: "YAML 配置", Extensions: []string{"yaml", "yml"}}},
	})
	if err != nil || len(files) == 0 {
		return
	}
	a.selectLocalConfig(files[0])
}

// reloadCore restarts the core; a transient port conflict self-heals via
// restartCore's silent retry, so only log here.
func (a *app) reloadCore() {
	go func() {
		if err := a.applyConfig(a.settingsConfig()); err != nil {
			log.Printf("重载待重试: %v", err)
		}
	}()
}

func (a *app) closeConnections() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = a.ctrl.CloseConnections(ctx)
}

// helpSubmenu mirrors ClashX's 帮助: about, live log level, log file and
// the read-only port/IP info lines refreshed with every poll.
func (m *trayMenu) helpSubmenu(s core.State) *mygo.MenuItem {
	a := m.app
	m.levelRadio = map[string]*mygo.MenuItem{}
	var levelSub []*mygo.MenuItem
	for _, lv := range []string{"silent", "error", "warning", "info", "debug"} {
		lv := lv
		it := &mygo.MenuItem{
			Label:   strings.ToUpper(lv),
			Type:    mygo.MenuItemRadio,
			Checked: strings.EqualFold(s.Level, lv),
		}
		it.Click = func(*mygo.MenuItem, *mygo.Window) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := a.ctrl.SetLogLevel(ctx, lv); err != nil {
				a.reportError("日志等级", err.Error())
			}
		}
		m.levelRadio[lv] = it
		levelSub = append(levelSub, it)
	}
	m.level = s.Level
	m.httpIt = &mygo.MenuItem{Label: portLabel("HTTP/SOCKS 端口", a.mixedPort(), s.Running), Disabled: true}
	m.apiIt = &mygo.MenuItem{Label: portLabel("API 端口", a.apiPort(), s.Running), Disabled: true}
	m.ipIt = &mygo.MenuItem{Label: "IP: " + localIP(), Disabled: true}
	return &mygo.MenuItem{Label: T("帮助"), Submenu: []*mygo.MenuItem{
		{Label: T("检查更新…"), Click: func(*mygo.MenuItem, *mygo.Window) {
			a.checkForUpdates()
		}},
		{Label: T("导出诊断…"), Click: func(*mygo.MenuItem, *mygo.Window) {
			a.exportDiagnostics()
		}},
		{Label: T("关于…"), Click: func(*mygo.MenuItem, *mygo.Window) {
			a.showAbout()
		}},
		mygo.Separator(),
		{Label: T("日志等级"), Submenu: levelSub},
		{Label: T("打开日志文件"), Accelerator: "Cmd+L", Click: func(*mygo.MenuItem, *mygo.Window) {
			_ = mygo.Shell.OpenPath(a.ctrl.Manager().LogPath())
		}},
		mygo.Separator(),
		m.httpIt, m.apiIt, m.ipIt,
	}}
}

func portLabel(name string, port int, running bool) string {
	if !running {
		return name + ": —"
	}
	return fmt.Sprintf("%s: %d", name, port)
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
	localLabel := T("本地配置…")
	if st.configPath != "" {
		localLabel = T("本地配置: ") + filepath.Base(st.configPath)
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
		&mygo.MenuItem{Label: T("打开配置文件夹"), Accelerator: "Cmd+O",
			Click: func(*mygo.MenuItem, *mygo.Window) { _ = mygo.Shell.OpenPath(coreDir()) }},
		&mygo.MenuItem{Label: T("重载配置文件"), Accelerator: "Cmd+R",
			Click: func(*mygo.MenuItem, *mygo.Window) { a.reloadCore() }},
		&mygo.MenuItem{Label: T("更新外部资源"), Accelerator: "Shift+Cmd+U",
			Click: func(*mygo.MenuItem, *mygo.Window) { a.updateSubscription() }},
	)
	managed := &mygo.MenuItem{Label: T("托管配置"), Submenu: []*mygo.MenuItem{
		{Label: T("管理"), Accelerator: "Cmd+M", Click: func(*mygo.MenuItem, *mygo.Window) { a.showManageWindow() }},
		{Label: T("更新"), Accelerator: "Cmd+U", Click: func(*mygo.MenuItem, *mygo.Window) { a.updateSubscription() }},
		m.autoUpdateItem(),
		{Label: T("设置更新间隔"), Submenu: intervalItems(a)},
	}}
	return append(items, managed)
}

// autoUpdateItem builds the 自动更新 checkbox bound to settings.
func (m *trayMenu) autoUpdateItem() *mygo.MenuItem {
	it := &mygo.MenuItem{
		Label:   T("自动更新"),
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
	{T("30 分钟"), 30}, {T("1 小时"), 60}, {T("6 小时"), 360}, {T("12 小时"), 720}, {T("24 小时"), 1440},
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

// openConsole shows the core's built-in dashboard (zashboard served at
// /ui by external-ui), falling back to the panel overview when the
// dashboard assets are not installed. ClashX Meta's own ui/ bundle counts.
func (a *app) openConsole() {
	installed := false
	for _, ui := range []string{"dashboard", "ui"} {
		if _, err := os.Stat(filepath.Join(coreDir(), ui, "index.html")); err == nil {
			installed = true
			break
		}
	}
	if !installed {
		a.reportError(T("控制台"), "仪表盘未安装：请运行 scripts/fetch-dashboard.sh 或把 ui/ 放入配置目录。")
		return
	}
	if !a.ctrl.State().Running {
		a.reportError(T("控制台"), "核心未运行。")
		return
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/ui", a.apiPort())
	if a.settings.secret != "" {
		url += "?token=" + urlquery(a.settings.secret)
	}
	_ = mygo.Shell.OpenExternal(url)
}

func urlquery(s string) string { return neturl.QueryEscape(s) }

func (a *app) mixedPort() int {
	return parsePort(a.settings.mixedPort, core.DefaultConfig().MixedPort)
}

func (a *app) apiPort() int {
	return parsePort(a.settings.controller, core.DefaultConfig().ControllerPort)
}

// localIP returns the primary LAN IPv4 for the 帮助 info line.
func localIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "—"
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && ipn.IP.To4() != nil {
			return ipn.IP.String()
		}
	}
	return "—"
}

// --- label helpers ---

func groupLabel(name, current string) string {
	if current == "" {
		return name
	}
	return name + "  (" + current + ")"
}

// memberLabel renders a node with a ClashX-style delay badge. Native menus
// cannot draw the coloured pill, so small text-grade symbols stand in:
// ✅ ≤300ms, ⚠️ slower, ❌ for failures.
func memberLabel(name string, delays map[string]int) string {
	d, ok := delays[name]
	switch {
	case !ok || d == 0:
		return name
	case d < 0:
		return name + "  ❌  失败"
	case d <= 300:
		return fmt.Sprintf("%s  ✅ %4d ms", name, d)
	default:
		return fmt.Sprintf("%s  ⚠️ %4d ms", name, d)
	}
}

// subInfoLabel renders the subscription quota line shown (disabled) at the
// top of the 配置 submenu, ClashX style.
func subInfoLabel(s core.State) string {
	info := s.SubInfo
	if info == nil || info.Total <= 0 {
		if s.SubProxies > 0 {
			return fmt.Sprintf(T("订阅：%d 个节点"), s.SubProxies)
		}
		return T("订阅：无")
	}
	line := fmt.Sprintf(T("订阅流量 %s / %s · %d 个节点"),
		humanBytes(info.Upload+info.Download), humanBytes(info.Total), s.SubProxies)
	if !info.Expire.IsZero() {
		line += T(" · 到期 ") + info.Expire.Local().Format("2006-01-02")
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

// humanSpeed caps the readout at three digits with a single decimal:
// 0–999K/s, 0.1–99.9M/s, then G/s (byte "B" dropped per user preference) —
// right-padded to a fixed 9-column field so the menu title stays stable.
func humanSpeed(n int64) string {
	kb := n / 1024
	var s string
	switch {
	case kb < 1000:
		s = fmt.Sprintf("%dK/s", kb)
	case kb < 100*1024:
		s = fmt.Sprintf("%.1fM/s", float64(kb)/1024)
	default:
		s = fmt.Sprintf("%.1fG/s", float64(kb)/1024/1024)
	}
	return fmt.Sprintf("%9s", s)
}
