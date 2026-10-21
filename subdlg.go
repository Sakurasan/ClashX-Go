package main

import (
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

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
		d = &subscriptionDialog{app: a, title: T("添加订阅")}
		d.win = mygo.NewWindow(mygo.WindowOptions{
			Title: T("添加订阅"), Width: 480, Height: 170,
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
	c.SetTheme(appleTheme(t.Dark))
	ui.Column(c).Fill().Padding(16).Gap(10).Children(func() {
		ui.Text(c, T("Clash 订阅链接 — 下载后作为一个托管配置文件加入列表")).FontSize(12).TextColor(t.TextMuted)
		input.Input(c, input.Props{Value: &d.url, Placeholder: "https://…/subscribe?token=…"}).FillWidth()
		ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Children(func() {
			nativeButton(c, T("取消"), buttonSecondary, d.win.Hide)
			nativeButton(c, T("添加"), buttonPrimary, d.save)
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
			a.reportError(T("添加订阅"), err.Error())
		}
	}()
}
