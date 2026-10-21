package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"clashx-go/core"
)

// app is the menubar controller. All fields are only touched on the main
// thread (tray callbacks and the view), so no locking is needed here.
type app struct {
	ctrl     *core.Controller
	settings *settings
	panel    *panelApp
	menu     *trayMenu
	subDlg   *subscriptionDialog
	manage   *manageWindow
	win      *mygo.Window
	aboutWin *mygo.Window
	tray     *mygo.Tray
	quitting bool

	// coreOp serialises start/stop/restart so menu clicks cannot stack up
	// against a still-finishing operation. reloadMu serialises background
	// live reloads (a new switch waits for the previous one).
	coreOp   sync.Mutex
	reloadMu sync.Mutex
}

func main() {
	a := &app{}
	mygo.App.SetName("ClashX Go")
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.quitting = true })
	mygo.App.WhenReady(a.ready)
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// setupLogging mirrors the app log to app.log in the config dir so an
// installed app can be diagnosed remotely without a terminal.
func setupLogging(dir string) {
	f, err := os.OpenFile(filepath.Join(dir, "app.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
}

// ready wires the tray, the controller and the panel window.
func (a *app) ready() {
	dir := coreDir()
	setupLogging(dir)
	log.Printf("启动：配置目录 %s", dir)
	if !singleInstanceLock(filepath.Join(dir, "app.lock")) {
		mygo.Dialog.Error("ClashX Go", "程序已在运行中，请勿启动第二个实例。")
		mygo.App.Quit()
		return
	}
	mgr, err := core.NewManager(dir)
	if err != nil {
		log.Fatal("core dir: ", err)
	}
	a.settings = loadSettings(dir)
	log.Printf("启动：tun=%t systemProxy=%t profile=%q config=%q",
		a.settings.tun, a.settings.systemProxy, a.settings.activeProfile, a.settings.configPath)
	// Reflect the real system login-item state so the switch shows the
	// truth even if the user toggled it in System Settings.
	a.settings.startAtLogin = mygo.App.OpenAtLogin()
	cfg := a.settingsConfig()
	a.ctrl = core.NewController(mgr, cfg)
	a.ctrl.OnChange(a.onState)

	// Tray: clicking opens the ClashX-style menu; the window behind
	// 更多设置 is the preferences window.
	a.tray, err = mygo.NewTray(mygo.TrayOptions{
		Icon:           trayIcon(color.NRGBA{A: 255}),
		IconIsTemplate: true,
		ToolTip:        "ClashX Go",
	})
	if err != nil {
		log.Fatal("tray: ", err)
	}
	a.menu = &trayMenu{app: a}
	a.menu.build(a.ctrl.State())
	a.applyShortcuts()
	if runtime.GOOS == "linux" {
		a.tray.SetMenu(a.menu.menu)
	} else {
		// ClashX refreshes on open (NSMenuDelegate menuNeedsUpdate), which
		// MyGo does not expose. Leave the item menu unset so clicks reach
		// OnClick: rebuild from the freshest state, then pop it up.
		a.tray.OnClick(func() {
			a.menu.rebuild()
			a.tray.PopUpMenu()
		})
	}
	a.panel = &panelApp{app: a}
	a.win = mygo.NewWindow(mygo.WindowOptions{
		Title:   "ClashX 设置",
		Width:   520,
		Height:  720,
		Hidden:  true,
		Content: ui.View(a.panel.view),
	})
	a.win.OnClose(func(e *mygo.CloseEvent) {
		if !a.quitting {
			e.PreventDefault()
			a.win.Hide()
		}
	})
	mygo.App.OnNotificationClick(func(id string) {
		if id == notificationUpdateID {
			a.checkForUpdates()
		}
	})
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		// Never leave the system pointing at a port that is about to close;
		// restore the user's own proxy config, not just "off". Log each step
		// so a stuck quit is diagnosable, and keep every wait bounded (the
		// helpers time out; see Controller.Stop and networksetup).
		log.Printf("退出：开始清理")
		if a.settings != nil && a.settings.systemProxy {
			if err := restoreProxy(a.settings.proxyBackup); err != nil {
				log.Printf("退出：还原系统代理失败: %v", err)
			}
			a.settings.systemProxy = false
			a.settings.proxyBackup = nil
			saveSettings(dir, a.settings)
		}
		log.Printf("退出：停止核心")
		a.ctrl.Stop()
		log.Printf("退出：销毁托盘")
		a.tray.Destroy()
		log.Printf("退出：清理完成")
	})

	// Boot the core in the background on launch.
	go a.startCore()
	go a.autoUpdateLoop(context.Background())
	go a.autoUpdateCheckLoop()
	go a.subscriptionNoticeLoop()
	go a.watchConfigSource()
	go a.ssidSuspendLoop()
}

// ssidSuspendLoop pauses the system proxy and TUN while the default route is
// Wi-Fi whose SSID is in the user's suspend list, then restores on leave
// (ClashX Meta's SSID 自动暂停).
func (a *app) ssidSuspendLoop() {
	var suspended, prevSys bool
	for {
		time.Sleep(15 * time.Second)
		if a.quitting {
			return
		}
		list := splitList(a.settings.suspendSSIDs)
		if len(list) == 0 {
			if suspended {
				a.resumeFromSSID(prevSys)
				suspended = false
			}
			continue
		}
		ssid := currentSSID()
		hit := false
		for _, want := range list {
			if want == ssid {
				hit = true
				break
			}
		}
		switch {
		case hit && !suspended:
			suspended = true
			prevSys = a.settings.systemProxy
			log.Printf("WiFi %q 命中暂停列表，暂停代理", ssid)
			a.suspendProxies()
		case !hit && suspended:
			suspended = false
			log.Printf("WiFi %q 已离开，恢复代理", ssid)
			a.resumeFromSSID(prevSys)
		}
	}
}

// suspendProxies turns off the system proxy (restoring the user's own
// config) and TUN without touching the saved intentions.
func (a *app) suspendProxies() {
	if a.settings.systemProxy {
		_ = restoreProxy(a.settings.proxyBackup)
		a.settings.systemProxy = false
		a.settings.proxyBackup = nil
		saveSettings(coreDir(), a.settings)
		a.refreshTrayIcon()
	}
	if a.ctrl.State().Running {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = a.ctrl.SetTUN(ctx, false)
		cancel()
	}
}

// resumeFromSSID re-applies the system proxy (and TUN) that were suspended.
func (a *app) resumeFromSSID(prevSys bool) {
	if prevSys {
		if a.settings.proxyBackup == nil {
			if b, err := snapshotProxy(); err == nil {
				a.settings.proxyBackup = b
			}
		}
		if err := pointProxyAt(a.mixedPort(), splitList(a.settings.proxyBypass)); err == nil {
			a.settings.systemProxy = true
			saveSettings(coreDir(), a.settings)
		}
	}
	if a.settings.tun && a.ctrl.State().Running {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = a.ctrl.SetTUN(ctx, true)
		cancel()
	}
	a.refreshTrayIcon()
}

// onState marshals onto the main thread with an async post (never a
// synchronous onMain): a modal dialog open on the main thread must not
// stall the controller goroutines that emit state.
func (a *app) onState(s core.State) {
	if a.win == nil {
		return
	}
	a.win.Update(func() {
		if a.tray != nil {
			status := "核心已停止"
			if s.Running {
				status = "核心运行中 · 模式 " + s.Mode
			}
			a.tray.SetToolTip("ClashX Go — " + status)
		}
		a.menu.refresh(s)
	})
}

func (a *app) startCore() { a.startCoreReport(true) }

// startCoreReport brings the core up. A failed start schedules a silent
// retry (ClashX has no manual start switch: transient conflicts must
// self-heal). The error dialog runs on its own goroutine: it is modal and
// must never hold coreOp, or an undismissed dialog freezes the retry chain.
func (a *app) startCoreReport(report bool) {
	err := a.startCoreLocked()
	if err == nil {
		return
	}
	time.AfterFunc(30*time.Second, func() {
		if a.quitting || a.ctrl.State().Running {
			return
		}
		a.startCoreReport(false)
	})
	if report {
		go a.reportError("核心启动失败", err.Error())
	}
}

func (a *app) startCoreLocked() error {
	a.coreOp.Lock()
	defer a.coreOp.Unlock()
	if a.ctrl.Manager().Running() {
		// A quick 停止→启动 or double click: the previous op already won
		// the race, and Manager.Start would only error "already running".
		return nil
	}
	// Re-read settings so a fresh Start uses the current endpoints.
	if mp := a.mixedPort(); !core.PortAvailable(mp) {
		// Same as the controller port: shift instead of starting with a
		// mixed port another proxy already owns (mihomo would log a bind
		// error and keep running without a listener).
		next := core.NextFreePort(mp + 1)
		if next > mp {
			log.Printf("混合端口 %d 被占用，改用 %d", mp, next)
			a.settings.mixedPort = strconv.Itoa(next)
			saveSettings(coreDir(), a.settings)
			if a.settings.systemProxy {
				go func(port int) {
					if err := pointProxyAt(port, splitList(a.settings.proxyBypass)); err != nil {
						log.Printf("系统代理改用端口 %d 失败: %v", port, err)
					}
				}(next)
			}
		}
	}
	a.ctrl = core.NewController(a.ctrl.Manager(), a.settingsConfig())
	a.ctrl.OnChange(a.onState)
	err := a.ctrl.Start(context.Background())
	if err != nil && !core.PortAvailable(a.apiPort()) {
		// ClashX Meta shifts to the next free port instead of refusing to
		// start; persist the new controller port and retry once.
		next := core.NextFreePort(a.apiPort() + 1)
		if next > a.apiPort() {
			log.Printf("控制器端口 %d 被占用，改用 %d", a.apiPort(), next)
			a.settings.controller = strconv.Itoa(next)
			saveSettings(coreDir(), a.settings)
			a.ctrl = core.NewController(a.ctrl.Manager(), a.settingsConfig())
			a.ctrl.OnChange(a.onState)
			err = a.ctrl.Start(context.Background())
		}
	}
	if err == nil {
		go a.postStartConfig()
	}
	return err
}

// postStartConfig pushes the real config after the core answered on its
// bootstrap config, then refreshes a stale subscription and restores the
// remembered node selections.
func (a *app) postStartConfig() {
	if a.configSourcePath() != "" {
		a.reloadLive(a.settingsConfig())
	}
	a.refreshActiveSubIfStale()
}

// configSourcePath is the file driving the running config: the active
// subscription's copy or the local config picked by the user.
func (a *app) configSourcePath() string {
	if p := a.activeSub(); p != nil {
		return filepath.Join(coreDir(), p.File)
	}
	if a.settings != nil && a.settings.configPath != "" {
		return a.settings.configPath
	}
	return ""
}

// configKey identifies the active config for saved node selections.
func (a *app) configKey() string {
	if p := a.activeSub(); p != nil {
		return p.Name
	}
	if a.settings != nil && a.settings.configPath != "" {
		base := filepath.Base(a.settings.configPath)
		return strings.TrimSuffix(base, filepath.Ext(base))
	}
	return "config"
}

// recordSelection remembers a group's chosen member so reloads and config
// switches replay it, like ClashX Meta's SavedProxyModel.
func (a *app) recordSelection(group, member string) {
	key := a.configKey()
	for i := range a.settings.selections {
		if a.settings.selections[i].Config == key && a.settings.selections[i].Group == group {
			a.settings.selections[i].Selected = member
			saveSettings(coreDir(), a.settings)
			return
		}
	}
	a.settings.selections = append(a.settings.selections,
		savedSelection{Config: key, Group: group, Selected: member})
	saveSettings(coreDir(), a.settings)
}

// replaySelections re-applies remembered members after a config load and
// drops records that no longer resolve (renamed or removed groups).
func (a *app) replaySelections() {
	key := a.configKey()
	kept := a.settings.selections[:0]
	dropped := false
	for _, sel := range a.settings.selections {
		if sel.Config != key {
			kept = append(kept, sel)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := a.ctrl.SelectProxy(ctx, sel.Group, sel.Selected)
		cancel()
		if err != nil {
			log.Printf("恢复节点选择失败 %s/%s: %v", sel.Group, sel.Selected, err)
			dropped = true
			continue
		}
		kept = append(kept, sel)
	}
	if dropped {
		a.settings.selections = kept
		saveSettings(coreDir(), a.settings)
	}
}

// watchConfigSource reloads when the active config file is edited outside
// the app (ClashX Meta watches the selected config file the same way).
func (a *app) watchConfigSource() {
	var lastPath string
	var lastMod time.Time
	for {
		time.Sleep(2 * time.Second)
		if a.quitting {
			return
		}
		p := a.configSourcePath()
		if p == "" {
			lastPath, lastMod = "", time.Time{}
			continue
		}
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if p != lastPath {
			lastPath, lastMod = p, fi.ModTime()
			continue
		}
		if fi.ModTime().After(lastMod) {
			lastMod = fi.ModTime()
			if a.ctrl.State().Running {
				log.Printf("配置已变更，后台重载：%s", filepath.Base(p))
				go a.reloadLive(a.settingsConfig())
			}
		}
	}
}

// applyConfig switches configuration live: mihomo re-reads the config file
// in place (the ClashX-instant path, the port never opens). Only changes a
// running core cannot absorb (API port/secret) or a failed reload fall
// back to a full process restart.
func (a *app) applyConfig(cfg core.Config) error {
	old := a.ctrl.CurrentConfig()
	if a.ctrl.State().Running && cfg.ControllerPort == old.ControllerPort && cfg.Secret == old.Secret {
		cfg.Mode = a.ctrl.State().Mode // keep the live mode across the reload
		go a.reloadLive(cfg)
		return nil
	}
	return a.restartCore(cfg)
}

// reloadLive applies the config in the background. mihomo's PUT /configs
// blocks until rule/proxy providers finish initialising (seconds when they
// pull from the network) while the old routing keeps serving, so waiting
// on it in the caller made every switch feel slow. A failed reload falls
// back to a process restart.
func (a *app) reloadLive(cfg core.Config) {
	a.reloadMu.Lock()
	defer a.reloadMu.Unlock()
	startedAt := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	err := a.ctrl.ReloadConfig(ctx, cfg)
	cancel()
	if err != nil {
		if a.quitting || !a.ctrl.Manager().Running() {
			return
		}
		log.Printf("live reload failed, restarting core: %v", err)
		if rerr := a.restartCore(cfg); rerr != nil {
			log.Printf("restart after failed reload: %v", rerr)
		}
		return
	}
	log.Printf("live reload in %s", time.Since(startedAt).Round(time.Millisecond))
	a.replaySelections()
	// ClashX closes established chains after a config change so new
	// routing applies immediately to persistent connections.
	go a.closeConnections()
}

// restartCore stops the old controller config and starts a new one.
func (a *app) restartCore(cfg core.Config) error {
	a.coreOp.Lock()
	defer a.coreOp.Unlock()
	startedAt := time.Now()
	a.ctrl.Stop()
	a.ctrl = core.NewController(a.ctrl.Manager(), cfg)
	a.ctrl.OnChange(a.onState)
	if err := a.ctrl.Start(context.Background()); err != nil {
		// A killed TUN core can take a moment to release its port while the
		// kernel tears the interface down; Start already waits for the port,
		// so retry almost immediately and only bother the user if that
		// retry also fails.
		go func() {
			time.Sleep(300 * time.Millisecond)
			if a.quitting || a.ctrl.Manager().Running() {
				return
			}
			a.startCoreReport(true)
		}()
		return err
	}
	go a.postStartConfig()
	log.Printf("core restarted in %s", time.Since(startedAt).Round(time.Millisecond))
	return nil
}

// settingsConfig builds the controller config from the app settings.
func (a *app) settingsConfig() core.Config {
	cfg := core.DefaultConfig()
	if s := a.settings; s != nil {
		cfg.MixedPort = parsePort(s.mixedPort, cfg.MixedPort)
		cfg.ControllerPort = parsePort(s.controller, cfg.ControllerPort)
		cfg.Secret = s.secret
		cfg.AllowLAN = s.allowLAN
		cfg.TUN = s.tun
		cfg.IPv6 = s.ipv6
		cfg.BenchmarkURL = s.benchmarkURL
		if p := a.activeSub(); p != nil {
			cfg.SubscriptionURL = p.URL
			cfg.SubscriptionFile = p.File
		} else {
			cfg.ConfigPath = s.configPath
		}
	}
	return cfg
}

// showToast asks the panel to show a toast on its next frame. The "减少通知"
// setting suppresses these, but never error dialogs.
func (a *app) showToast(title, desc string) {
	if a.settings != nil && a.settings.noNotify {
		return
	}
	if a.panel != nil {
		a.panel.toast = &toastMsg{title: title, desc: desc}
		a.win.Update(func() {})
	}
}

// showSettings opens the settings window next to the tray icon.
func (a *app) showSettings() {
	a.show()
}

// show places the panel next to the tray icon and shows it.
func (a *app) show() {
	anchor := a.tray.Bounds()
	if anchor.Width == 0 || anchor.Height == 0 {
		a.win.Center()
	} else {
		area := mygo.Screen.DisplayNearestPoint(mygo.Point{
			X: anchor.X + anchor.Width/2,
			Y: anchor.Y + anchor.Height/2,
		}).WorkArea
		b := a.win.Bounds()
		x := anchor.X + (anchor.Width-b.Width)/2
		y := anchor.Y + anchor.Height + 6
		if y+b.Height > area.Y+area.Height {
			y = anchor.Y - b.Height - 6
		}
		x = max(area.X, min(x, area.X+max(0, area.Width-b.Width)))
		y = max(area.Y, min(y, area.Y+max(0, area.Height-b.Height)))
		a.win.SetPosition(x, y)
	}
	a.win.Show()
}

// trayIcon renders the status mark (custom menuImage.png or the paw)
// tinted with ink on a 32px canvas — black for the template
// (system-tinted) stopped icon.
func trayIcon(ink color.NRGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	drawGlyph(img, img.Rect, ink)
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// proxyActive reports whether traffic is actually routed: system proxy or
// TUN on. The status icon is dimmed while neither is.
func (a *app) proxyActive() bool {
	return a.settings.systemProxy || a.settings.tun
}

// updateTrayIcon swaps between the monochrome stopped icon and a colored
// running one, like ClashX's icon state. Without system proxy or TUN the
// running paw is drawn at reduced opacity (ClashX Meta's dimmed state).
func (a *app) updateTrayIcon(running bool) {
	switch {
	case !running:
		_ = a.tray.SetIcon(trayIcon(color.NRGBA{A: 255}), true)
	case a.proxyActive():
		_ = a.tray.SetIcon(trayIcon(color.NRGBA{R: 37, G: 99, B: 235, A: 255}), false)
	default:
		_ = a.tray.SetIcon(trayIcon(color.NRGBA{R: 37, G: 99, B: 235, A: 110}), false)
	}
}

// coreDir is our own config folder, a sibling of ClashX Meta's
// ~/.config/clash.meta. Keeping it separate means no shared cache.db,
// configs or remembered node selections.
func coreDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		dir := filepath.Join(home, ".config", "clashx-go")
		if err := os.MkdirAll(dir, 0o755); err == nil {
			migrateLegacyCoreDir(dir)
			return dir
		}
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "clashx-go")
}

// migrateLegacyCoreDir copies settings, downloaded subscriptions and the
// dashboard from the pre-~/.config location on first launch there. The old
// directory is left in place.
func migrateLegacyCoreDir(dest string) {
	if _, err := os.Stat(filepath.Join(dest, "app.json")); err == nil {
		return
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return
	}
	old := filepath.Join(base, "clashx-go")
	data, err := os.ReadFile(filepath.Join(old, "app.json"))
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dest, "app.json"), data, 0o644)
	entries, _ := os.ReadDir(old)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "sub") || !strings.HasSuffix(name, ".yaml") {
			continue
		}
		if _, err := os.Stat(filepath.Join(dest, name)); err == nil {
			continue
		}
		if data, err := os.ReadFile(filepath.Join(old, name)); err == nil {
			_ = os.WriteFile(filepath.Join(dest, name), data, 0o644)
		}
	}
	if st, err := os.Stat(filepath.Join(old, "dashboard")); err == nil && st.IsDir() {
		if _, err := os.Stat(filepath.Join(dest, "dashboard")); os.IsNotExist(err) {
			_ = os.CopyFS(filepath.Join(dest, "dashboard"), os.DirFS(filepath.Join(old, "dashboard")))
		}
	}
}
