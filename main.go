package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"os"
	"path/filepath"

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
	win      *mygo.Window
	tray     *mygo.Tray
	quitting bool
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

// ready wires the tray, the controller and the panel window.
func (a *app) ready() {
	dir := coreDir()
	mgr, err := core.NewManager(dir)
	if err != nil {
		log.Fatal("core dir: ", err)
	}
	a.settings = loadSettings(dir)
	// Reflect the real system login-item state so the switch shows the
	// truth even if the user toggled it in System Settings.
	a.settings.startAtLogin = mygo.App.OpenAtLogin()
	cfg := a.settingsConfig()
	a.ctrl = core.NewController(mgr, cfg)
	a.ctrl.OnChange(a.onState)

	// Tray: clicking opens the ClashX-style menu; the panel window stays
	// as the preferences window behind 更多设置.
	a.tray, err = mygo.NewTray(mygo.TrayOptions{
		Icon:           trayIcon(),
		IconIsTemplate: true,
		ToolTip:        "ClashX Go",
	})
	if err != nil {
		log.Fatal("tray: ", err)
	}
	a.menu = &trayMenu{app: a}
	a.menu.build(a.ctrl.State())
	a.tray.SetMenu(a.menu.menu)
	a.panel = &panelApp{app: a}
	a.win = mygo.NewWindow(mygo.WindowOptions{
		Title:             "ClashX Go",
		Width:             420,
		Height:            560,
		Hidden:            true,
		Frameless:         true,
		DisableResize:     true,
		DisableMinimize:   true,
		DisableMaximize:   true,
		DisableFullScreen: true,
		AlwaysOnTop:       true,
		SkipTaskbar:       true,
		Content:           ui.View(a.panel.view),
	})
	a.win.OnBlur(a.win.Hide)
	a.win.OnClose(func(e *mygo.CloseEvent) {
		if !a.quitting {
			e.PreventDefault()
			a.win.Hide()
		}
	})
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		a.ctrl.Stop()
		a.tray.Destroy()
	})

	// Boot the core in the background on launch.
	go a.startCore()
}

func (a *app) onState(s core.State) {
	if a.tray != nil {
		status := "核心已停止"
		if s.Running {
			status = "核心运行中 · 模式 " + s.Mode
		}
		a.tray.SetToolTip("ClashX Go — " + status)
	}
	if a.win != nil {
		// Marshal onto the main thread: the menu's items and structure are
		// only ever mutated there, together with the click handlers.
		a.win.Update(func() { a.menu.refresh(s) })
	}
}

func (a *app) startCore() {
	ctx := context.Background()
	// Re-read settings so a fresh Start (e.g. from the panel footer)
	// uses the current endpoints.
	a.ctrl = core.NewController(a.ctrl.Manager(), a.settingsConfig())
	a.ctrl.OnChange(a.onState)
	// Flag the first subscription download (Start pulls it before booting
	// the core) so the UI can show it and avoid double starts.
	if a.settingsConfig().SubscriptionURL != "" {
		a.ctrl.SetSubDownloading(true)
	}
	if err := a.ctrl.Start(ctx); err != nil {
		log.Println("start core: ", err)
		a.showToast("核心启动失败", err.Error())
	}
	a.ctrl.SetSubDownloading(false)
}

func (a *app) stopCore() {
	a.ctrl.Stop()
}

// restartCore stops the old controller config and starts a new one.
func (a *app) restartCore(cfg core.Config) error {
	a.ctrl = core.NewController(a.ctrl.Manager(), cfg)
	a.ctrl.OnChange(a.onState)
	return a.ctrl.Start(context.Background())
}

// settingsConfig builds the controller config from the app settings.
func (a *app) settingsConfig() core.Config {
	cfg := core.DefaultConfig()
	if a.settings != nil {
		cfg.MixedPort = parsePort(a.settings.mixedPort, cfg.MixedPort)
		cfg.ControllerPort = parsePort(a.settings.controller, cfg.ControllerPort)
		cfg.Secret = a.settings.secret
		cfg.ConfigPath = a.settings.configPath
		cfg.SubscriptionURL = a.settings.subscriptionURL
		cfg.AllowLAN = a.settings.allowLAN
	}
	return cfg
}

// showToast asks the panel to show a toast on its next frame.
func (a *app) showToast(title, desc string) {
	if a.panel != nil {
		a.panel.toast = &toastMsg{title: title, desc: desc}
		a.win.Update(func() {})
	}
}

// showPanel opens the preferences window on the given tab.
func (a *app) showPanel(tab panelTab) {
	a.panel.tab = tab
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

// trayIcon draws a filled ring, black on transparent, so macOS tints it.
func trayIcon() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	ink := color.NRGBA{A: 255}
	// Outer ring
	for y := range 32 {
		for x := range 32 {
			dx, dy := x-16, y-16
			d := dx*dx + dy*dy
			if d <= 14*14 && d >= 7*7 {
				img.Set(x, y, ink)
			}
		}
	}
	// Vertical gap to suggest an open proxy
	draw.Draw(img, image.Rect(14, 8, 18, 24), image.NewUniform(color.NRGBA{A: 0}), image.Point{}, draw.Src)
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// coreDir returns the per-user directory for the core binary and config.
func coreDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "clashx-go")
}
