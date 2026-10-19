package main

import (
	"fmt"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	tokens "github.com/smallnest/uix/components/base"
	"github.com/smallnest/uix/components/segmented"
	"github.com/smallnest/uix/components/toast"

	"clashx-go/core"
)

const (
	tabGeneral = iota
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
}

// view builds the settings window with ClashX Meta's four tabs. The page
// title lives in the window chrome, as in the original.
func (p *panelApp) view(c *ui.Context) {
	t := c.Theme()
	c.SetTheme(tokens.For(t.Dark))
	ui.Column(c).Fill().Padding(16).Gap(12).Children(func() {
		ui.Row(c).FillWidth().Justify(ui.Center).Children(func() {
			segmented.Segmented(c, segmented.Props{
				Selected: &p.tab,
				Labels:   []string{"通用", "全局快捷键", "调试", "Meta"},
				Label:    "设置页",
			})
		})
		// Scroll the page: the 通用 tab is taller than the window and used
		// to be clipped at the bottom.
		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			switch p.tab {
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
	p.toastViewport(c)
}

// settingsRow puts the label left and its control right, like the rows of
// ClashX Meta's settings groups.
func settingsRow(c *ui.Context, label string, control func()) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, label).FontSize(13).Grow(1)
		control()
	})
}

// settingsGroup wraps related settings in one rounded card, mirroring the
// grouped boxes of ClashX Meta's preferences window.
func settingsGroup(c *ui.Context, title string, body func()) {
	t := c.Theme()
	if title != "" {
		ui.Text(c, title).FontSize(11).TextColor(t.TextMuted)
	}
	ui.Column(c).FillWidth().Padding(10).Background(t.Surface).Radius(t.Radius).Gap(6).Children(body)
}

// shortcutsPane binds global hotkeys, mirroring ClashX Meta's 全局快捷键.
func (p *panelApp) shortcutsPane(c *ui.Context) {
	t := c.Theme()
	a := p.app
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		ui.Text(c, "格式例如 CmdOrCtrl+Shift+P；留空并点设置即取消绑定。").FontSize(11).TextColor(t.TextMuted)
		settingsGroup(c, "", func() {
			for _, act := range a.shortcutActions() {
				act := act
				acc := a.settings.shortcuts[act.id]
				settingsRow(c, act.label, func() {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.TextInput(c, &acc).Width(180).Placeholder("未设置")
						ui.Button(c, "设置").OnClick(func() {
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
		settingsGroup(c, "日志", func() {
			ui.Text(c, a.ctrl.Manager().LogPath()).FontSize(11).TextColor(t.TextMuted).SingleLine()
			ui.Row(c).Gap(8).Children(func() {
				ui.Button(c, "打开日志").OnClick(func() {
					_ = mygo.Shell.OpenPath(a.ctrl.Manager().LogPath())
				})
				ui.Button(c, "打开配置目录").OnClick(func() {
					_ = mygo.Shell.OpenPath(coreDir())
				})
			})
		})
		settingsGroup(c, "系统代理", func() {
			ui.Text(c, "立即把系统代理恢复为开启前的配置。").FontSize(11).TextColor(t.TextMuted)
			ui.Button(c, "还原系统代理").OnClick(func() {
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
		settingsGroup(c, "核心", func() {
			ui.Row(c).Gap(8).Children(func() {
				ui.Button(c, "重载核心配置").OnClick(a.reloadCore)
				ui.Button(c, "关闭所有连接").OnClick(func() {
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
		settingsGroup(c, "内核", func() {
			ui.Text(c, "Meta Core: "+core.CoreVersion).FontSize(12)
			ui.Link(c, "GitHub for Clash Meta: https://github.com/MetaCubeX/mihomo",
				"https://github.com/MetaCubeX/mihomo").FontSize(11)
		})
		settingsGroup(c, "配置目录", func() {
			ui.Text(c, "~/.config/clashx-go").FontSize(12).TextColor(t.TextMuted)
			ui.Button(c, "打开配置目录").OnClick(func() {
				_ = mygo.Shell.OpenPath(coreDir())
			})
		})
		settingsGroup(c, "应用", func() {
			ui.Button(c, "关于 ClashX Go").OnClick(a.showAbout)
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
