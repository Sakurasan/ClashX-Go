package main

import (
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	tokens "github.com/smallnest/uix/components/base"
	"github.com/smallnest/uix/components/button"
	"github.com/smallnest/uix/components/input"
)

// subscriptionDialog is the small add-subscription editor, the
// ClashX-style replacement for opening the whole settings panel.
type subscriptionDialog struct {
	app   *app
	title string
	url   string
	win   *mygo.Window
}

// showSubscriptionDialog opens the add/edit sheet for a managed profile.
func (a *app) showSubscriptionDialog() {
	d := a.subDlg
	if d == nil {
		d = &subscriptionDialog{app: a, title: "添加订阅"}
		d.win = mygo.NewWindow(mygo.WindowOptions{
			Title: "添加订阅", Width: 480, Height: 170,
			Hidden: true, DisableMinimize: true, DisableMaximize: true, DisableFullScreen: true,
			Content: ui.View(d.view),
		})
		d.win.OnClose(func(e *mygo.CloseEvent) { e.PreventDefault(); d.win.Hide() })
		a.subDlg = d
	}
	d.url = ""
	d.win.Show()
	d.win.Center()
}

func (d *subscriptionDialog) view(c *ui.Context) {
	t := c.Theme()
	c.SetTheme(tokens.For(t.Dark))
	ui.Column(c).Fill().Padding(16).Gap(10).Children(func() {
		ui.Text(c, "Clash 订阅链接 — 下载后作为一个托管配置文件加入列表").FontSize(12).TextColor(t.TextMuted)
		input.Input(c, input.Props{Value: &d.url, Placeholder: "https://…/subscribe?token=…"}).FillWidth()
		ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Children(func() {
			button.Button(c, button.Props{Label: "取消", Variant: button.Outline, OnClick: d.win.Hide})
			button.Button(c, button.Props{Label: "添加", Variant: button.Primary, OnClick: d.save})
		})
	})
}

func (d *subscriptionDialog) save() {
	url := strings.TrimSpace(d.url)
	d.win.Hide()
	if url == "" {
		return
	}
	a := d.app
	go func() {
		if err := a.addProfile(url); err != nil {
			a.reportError("添加订阅", err.Error())
		}
	}()
}
