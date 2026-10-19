package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	tokens "github.com/smallnest/uix/components/base"
	"github.com/smallnest/uix/components/toast"
)

// panelApp is the state of the settings window. It is owned by main and only
// touched on the main thread; the controller pushes state via OnChange.
type panelApp struct {
	app *app

	// toast is a pending toast pushed on the next frame.
	toast *toastMsg
}

// view builds the settings window. The old overview/proxies tabs duplicated
// the menu and were removed; only preferences remain.
func (p *panelApp) view(c *ui.Context) {
	t := c.Theme()
	c.SetTheme(tokens.For(t.Dark))
	ui.Column(c).Fill().Padding(16).Gap(12).Children(func() {
		p.header(c)
		p.settingsPane(c)
		ui.Spacer(c)
	})
	p.toastViewport(c)
}

func (p *panelApp) header(c *ui.Context) {
	t := c.Theme()
	status := "已停止"
	if p.app.ctrl.State().Running {
		status = "运行中"
	}
	ui.Column(c).FillWidth().Gap(2).Children(func() {
		ui.Text(c, "ClashX 设置").FontSize(18).Bold()
		ui.Text(c, "mihomo 核心 · "+status).FontSize(12).TextColor(t.TextMuted)
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
