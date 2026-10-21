package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/smallnest/uix/components/lucide"
	"github.com/smallnest/uix/components/toast"

	"clashx-go/core"
)

const (
	tabGeneral = iota
	tabConnections
	tabRouting
	tabShortcuts
	tabDebug
	tabMeta
)

// panelApp is the state of the settings window. It is owned by main and only
// touched on the main thread; the controller pushes state via OnChange.
type panelApp struct {
	app *app

	tab int
	// toast is a pending toast pushed on the next frame.
	toast *toastMsg

	// Connections tab state, refreshed by connectionsLoop while the tab is
	// visible. Owned by the main thread like the rest of the view.
	conns         *core.Connections
	connErr       string
	connFetching  bool
	connView      int
	connFilter    string
	connSel       int
	connTable     ui.ListState
	rules         []core.Rule
	rulesFetching bool
	rulesAt       time.Time
	ruleFilter    string
	ruleTable     ui.ListState

	// ruleDirty marks app-routing edits that have not been applied to the
	// core yet.
	ruleDirty bool
}

const (
	connViewActive = iota
	connViewRules
)

// view builds the settings window with a toolbar of icon tabs, the native
// preferences layout of ClashX Meta (NSTabViewController's toolbar style).
func (p *panelApp) view(c *ui.Context) {
	t := c.Theme()
	c.SetTheme(appleTheme(t.Dark))
	ui.Column(c).Fill().Children(func() {
		ui.Column(c).FillWidth().Shrink(0).Padding(8, 12).Children(func() {
			p.tabBar(c)
		})
		ui.Box(c).FillWidth().Height(1).Shrink(0).Background(t.Border)
		if p.tab == tabConnections {
			// The table scrolls itself, so this page must not sit inside the
			// page scroll: a grow table in an unbounded scroller has no height.
			ui.Column(c).FillWidth().Grow(1).Padding(14, 20).Children(func() {
				p.connectionsPane(c)
			})
			return
		}
		// Scroll the page: the 通用 tab is taller than the window.
		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			ui.Column(c).FillWidth().Padding(16, 20).Children(func() {
				switch p.tab {
				case tabRouting:
					p.routingPane(c)
				case tabShortcuts:
					p.shortcutsPane(c)
				case tabDebug:
					p.debugPane(c)
				case tabMeta:
					p.metaPane(c)
				default:
					p.settingsPane(c)
				}
			})
		})
	})
	p.toastViewport(c)
}

// tabBar draws the preference toolbar: an icon over each label, with the
// selected tab on a subtle material, like macOS's toolbar tabs.
func (p *panelApp) tabBar(c *ui.Context) {
	t := c.Theme()
	tabs := []struct {
		label string
		icon  *ui.SVG
	}{
		{T("通用"), lucide.Settings()},
		{T("连接"), lucide.Network()},
		{T("分流"), lucide.Waypoints()},
		{T("全局快捷键"), lucide.Keyboard()},
		{T("调试"), lucide.Hammer()},
		{"Meta", lucide.Info()},
	}
	ui.Row(c).FillWidth().Justify(ui.Center).Gap(4).Children(func() {
		for i, tab := range tabs {
			i, tab := i, tab
			selected := p.tab == i
			ink := t.TextMuted
			if selected {
				ink = t.Text
			}
			b := ui.ButtonBase(c).Key(i).Padding(7, 12).Radius(8)
			if selected {
				b.Background(t.Text.Alpha(0.07))
			}
			b.Label(tab.label)
			b.Children(func() {
				ui.Column(c).AlignItems(ui.Center).Gap(3).Children(func() {
					ui.Icon(c, tab.icon).Size(18, 18).TextColor(ink)
					label := ui.Text(c, tab.label).FontSize(11).TextColor(ink)
					if selected {
						label.FontWeight(600)
					}
				})
			})
			if b.Clicked() {
				p.tab = i
				if p.app.win != nil {
					p.app.win.Update(func() {})
				}
			}
		}
	})
}

// settingsRow puts the label left and its control right, like the rows of
// ClashX Meta's settings groups.
func settingsRow(c *ui.Context, label string, control func()) {
	ui.Row(c).FillWidth().MinHeight(26).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, label).FontSize(13).Grow(1)
		control()
	})
}

// settingsSwitch is a settings row whose control is a native switch.
func settingsSwitch(c *ui.Context, label string, on *bool, onChange func()) {
	settingsRow(c, label, func() {
		ui.Switch(c, on).Label(label).OnChange(onChange)
	})
}

// settingsAction draws an inline action in the system link color, the way
// macOS preferences offer Open, Reset and similar secondary actions.
func settingsAction(c *ui.Context, label string, fn func()) {
	ui.Text(c, label).FontSize(12).TextColor(c.Theme().Accent).OnClick(fn)
}

// settingsDanger is settingsAction for destructive actions.
func settingsDanger(c *ui.Context, label string, fn func()) {
	ui.Text(c, label).FontSize(12).TextColor(c.Theme().Danger).OnClick(fn)
}

// settingsGroup wraps related settings in one rounded box, mirroring the
// grouped NSBoxes of ClashX Meta's preferences window.
func settingsGroup(c *ui.Context, title string, body func()) {
	t := c.Theme()
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		if title != "" {
			ui.Text(c, title).FontSize(12).FontWeight(600).TextColor(t.TextMuted)
		}
		ui.Column(c).FillWidth().Padding(12).Background(t.Surface).Radius(10).
			Border(1, t.Border).Gap(8).Children(body)
	})
}

// shortcutsPane binds global hotkeys, mirroring ClashX Meta's 全局快捷键.
func (p *panelApp) shortcutsPane(c *ui.Context) {
	t := c.Theme()
	a := p.app
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		ui.Text(c, T("格式例如 CmdOrCtrl+Shift+P；留空并点设置即取消绑定。")).FontSize(11).TextColor(t.TextMuted)
		settingsGroup(c, "", func() {
			for _, act := range a.shortcutActions() {
				act := act
				acc := a.settings.shortcuts[act.id]
				settingsRow(c, act.label, func() {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.TextInput(c, &acc).Width(180).Placeholder(T("未设置"))
						settingsAction(c, T("设置"), func() {
							a.setShortcut(act.id, act.label, acc)
						})
					})
				})
			}
		})
	})
}

// debugPane mirrors ClashX Meta's 调试 tab with actions that exist in this
// app (no helper uninstall or update channels).
func (p *panelApp) debugPane(c *ui.Context) {
	t := c.Theme()
	a := p.app
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		settingsGroup(c, T("日志"), func() {
			ui.Text(c, a.ctrl.Manager().LogPath()).FontSize(11).TextColor(t.TextMuted).SingleLine()
			ui.Row(c).Gap(8).Children(func() {
				settingsAction(c, T("打开日志"), func() {
					_ = mygo.Shell.OpenPath(a.ctrl.Manager().LogPath())
				})
				settingsAction(c, T("打开配置目录"), func() {
					_ = mygo.Shell.OpenPath(coreDir())
				})
				settingsAction(c, T("导出诊断包"), a.exportDiagnostics)
			})
		})
		settingsGroup(c, T("系统代理"), func() {
			ui.Text(c, T("立即把系统代理恢复为开启前的配置。")).FontSize(11).TextColor(t.TextMuted)
			settingsAction(c, T("还原系统代理"), func() {
				go func() {
					if err := restoreProxy(a.settings.proxyBackup); err != nil {
						a.reportError("系统代理", err.Error())
					}
					a.settings.systemProxy = false
					a.settings.proxyBackup = nil
					saveSettings(coreDir(), a.settings)
					a.refreshTrayIcon()
				}()
			})
		})
		settingsGroup(c, T("核心"), func() {
			ui.Row(c).Gap(8).Children(func() {
				settingsAction(c, T("重载核心配置"), a.reloadCore)
				settingsAction(c, T("关闭所有连接"), func() {
					go a.closeConnections()
				})
			})
		})
	})
}

// metaPane mirrors ClashX Meta's Meta tab with this app's core info.
func (p *panelApp) metaPane(c *ui.Context) {
	t := c.Theme()
	a := p.app
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		settingsGroup(c, T("TUN"), func() {
			settingsRow(c, T("Tun DNS"), func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.TextInput(c, &a.settings.tunDNS).Width(150).Placeholder("8.8.8.8").
						OnChange(func() { saveSettings(coreDir(), a.settings) }).
						OnSubmit(func() {
							saveSettings(coreDir(), a.settings)
							go a.reconcileTunDNS()
						})
					settingsAction(c, T("应用 DNS"), func() {
						saveSettings(coreDir(), a.settings)
						go a.reconcileTunDNS()
					})
				})
			})
			ui.Text(c, T("留空使用本地 DNS（推荐）；填写后开启 TUN 时会把系统 DNS 指向该地址。")).
				FontSize(11).TextColor(t.TextMuted)
		})
		settingsGroup(c, T("内核"), func() {
			ui.Text(c, "Meta Core: "+core.CoreVersion).FontSize(12)
			ui.Link(c, "GitHub for Clash Meta: https://github.com/MetaCubeX/mihomo",
				"https://github.com/MetaCubeX/mihomo").FontSize(11)
		})
		settingsGroup(c, T("配置目录"), func() {
			ui.Text(c, "~/.config/clashx-go").FontSize(12).TextColor(t.TextMuted)
			settingsAction(c, T("打开配置目录"), func() {
				_ = mygo.Shell.OpenPath(coreDir())
			})
		})
		settingsGroup(c, T("应用"), func() {
			ui.Row(c).FillWidth().Justify(ui.Start).Children(func() {
				nativeButton(c, T("关于 ClashX Go"), buttonSecondary, a.showAbout)
			})
		})
	})
}

// toastViewport shows any pending toast, then draws the viewport.
func (p *panelApp) toastViewport(c *ui.Context) {
	if p.toast != nil {
		toast.Push(c, toast.Props{Title: p.toast.title, Description: p.toast.desc, Type: toast.Success})
		p.toast = nil
	}
	toast.Viewport(c)
}

// humanBytes formats a byte count.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
