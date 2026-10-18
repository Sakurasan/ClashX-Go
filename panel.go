package main

import (
	"context"
	"fmt"

	"github.com/egoist/mygo/ui"

	"clashx-go/core"

	"github.com/smallnest/uix/components/badge"
	tokens "github.com/smallnest/uix/components/base"
	"github.com/smallnest/uix/components/button"
	"github.com/smallnest/uix/components/card"
	selector "github.com/smallnest/uix/components/select"
	"github.com/smallnest/uix/components/tabs"
	"github.com/smallnest/uix/components/toast"
)

// panelTab is the selected tab of the panel window.
type panelTab int

const (
	tabOverview panelTab = iota
	tabProxies
	tabSettings
)

// panelApp is the state of the panel view. It is owned by main and only
// touched on the main thread; the controller pushes state via OnChange.
type panelApp struct {
	app *app

	tab panelTab
	// toast is a pending toast pushed on the next frame.
	toast *toastMsg
}

// Root returns the root view function for the panel window.
func Root(a *app) func(*ui.Context) {
	p := &panelApp{app: a}
	return p.view
}

// view builds the whole panel.
func (p *panelApp) view(c *ui.Context) {
	t := c.Theme()
	c.SetTheme(tokens.For(t.Dark))
	ui.Column(c).Fill().Padding(16).Gap(12).Children(func() {
		p.header(c)
		tabs.Tabs(c, tabs.Props{Selected: (*int)(&p.tab), Labels: []string{"概览", "代理", "设置"}})
		switch p.tab {
		case tabOverview:
			p.overviewPane(c)
		case tabProxies:
			p.proxiesPane(c)
		case tabSettings:
			p.settingsPane(c)
		}
		ui.Spacer(c)
		p.footer(c)
	})
	p.toastViewport(c)
}

func (p *panelApp) header(c *ui.Context) {
	t := c.Theme()
	s := p.app.ctrl.State()
	status := "已停止"
	if s.Running {
		status = "运行中"
	}
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, "ClashX Go").FontSize(18).Bold()
			ui.Text(c, "mihomo 核心 · 菜单栏代理工具").FontSize(12).TextColor(t.TextMuted)
		})
		if s.Running {
			badge.Badge(c, badge.Props{Label: status, Variant: badge.Secondary})
		} else {
			badge.Badge(c, badge.Props{Label: status, Variant: badge.Outline})
		}
	})
}

func (p *panelApp) overviewPane(c *ui.Context) {
	t := c.Theme()
	s := p.app.ctrl.State()
	if !s.Running {
		ui.Column(c).Fill().Center().Gap(8).Children(func() {
			ui.Text(c, "核心已停止").FontSize(14).Bold()
			ui.Text(c, "点击「启动核心」拉起 mihomo。").FontSize(12).TextColor(t.TextMuted)
			if s.SubDownloading {
				ui.Text(c, "正在下载订阅…").FontSize(12).TextColor(t.TextMuted)
			}
			button.Button(c, button.Props{Label: "启动核心", Variant: button.Primary, Disabled: s.SubDownloading, OnClick: p.app.startCore})
		})
		return
	}
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			p.statBox(c, "下载", humanBytes(s.Traffic.Down), "累计 "+humanBytes(s.Traffic.DownTotal))
			p.statBox(c, "上传", humanBytes(s.Traffic.Up), "累计 "+humanBytes(s.Traffic.UpTotal))
		})
		ui.Row(c).Gap(8).Children(func() {
			p.statBox(c, "模式", s.Mode, "")
			p.statBox(c, "核心", s.Version, "")
		})
		if s.SubProxies > 0 {
			p.subscriptionBox(c, s)
		}
	})
}

func (p *panelApp) statBox(c *ui.Context, label, value, sub string) {
	t := c.Theme()
	ui.Column(c).Grow(1).Padding(10).Background(t.Surface).Radius(t.Radius).Gap(2).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(t.TextMuted)
		ui.Text(c, value).FontSize(16).Bold()
		if sub != "" {
			ui.Text(c, sub).FontSize(11).TextColor(t.TextMuted).SingleLine()
		}
	})
}

// subscriptionBox shows the downloaded subscription status and an Update
// button that re-fetches it and reloads the running provider.
func (p *panelApp) subscriptionBox(c *ui.Context, s core.State) {
	t := c.Theme()
	ui.Column(c).FillWidth().Gap(2).Background(t.Surface).Radius(t.Radius).Padding(10).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, "订阅").FontSize(12).TextColor(t.TextMuted)
				ui.Text(c, fmt.Sprintf("%d 个节点", s.SubProxies)).FontSize(16).Bold()
				if info := s.SubInfo; info != nil && info.Total > 0 {
					quota := fmt.Sprintf("流量 %s / %s", humanBytes(info.Upload+info.Download), humanBytes(info.Total))
					if !info.Expire.IsZero() {
						quota += " · 到期 " + info.Expire.Local().Format("2006-01-02")
					}
					ui.Text(c, quota).FontSize(11).TextColor(t.TextMuted).SingleLine()
				}
				if !s.SubUpdated.IsZero() {
					ui.Text(c, "更新于 "+s.SubUpdated.Local().Format("2006-01-02 15:04")).FontSize(11).TextColor(t.TextMuted).SingleLine()
				}
			})
			button.Button(c, button.Props{
				Label:    "更新",
				Variant:  button.Outline,
				Disabled: s.SubDownloading,
				OnClick: func() {
					go func() {
						n, err := p.app.ctrl.RefreshSubscription(context.Background())
						if err != nil {
							p.app.showToast("订阅更新失败", err.Error())
							return
						}
						p.app.showToast("订阅已更新", fmt.Sprintf("已重载 %d 个节点。", n))
					}()
				},
			})
		})
	})
}

func (p *panelApp) proxiesPane(c *ui.Context) {
	t := c.Theme()
	s := p.app.ctrl.State()
	if len(s.Groups) == 0 {
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Text(c, "暂无策略组。").TextColor(t.TextMuted).FontSize(12)
			ui.Text(c, "核心在运行但没有暴露策略组 — 加载包含 proxies 的配置后这里会显示节点。").
				TextColor(t.TextMuted).FontSize(11).MaxWidth(300)
		})
		return
	}
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		for _, g := range s.Groups {
			p.groupRow(c, g)
		}
	})
}

func (p *panelApp) groupRow(c *ui.Context, g *core.GroupView) {
	current := g.Current
	card.Card(c, card.Props{
		Title:       g.Name,
		Description: fmt.Sprintf("%s · %d 个节点", g.Type, len(g.Members)),
	}, func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			selector.Select(c, selector.Props{
				Selected:    &current,
				Options:     g.Members,
				Placeholder: "选择节点",
			}).Grow(1)
			if d, ok := g.Delays[current]; ok && d > 0 {
				badge.Badge(c, badge.Props{Label: fmt.Sprintf("%d ms", d), Variant: badge.Secondary})
			}
			button.Button(c, button.Props{
				Label:   "测速",
				Variant: button.Outline,
				OnClick: func() { _ = p.app.ctrl.TestGroup(context.Background(), g.Name) },
			})
		})
		if current != g.Current {
			_ = p.app.ctrl.SelectProxy(context.Background(), g.Name, current)
		}
	})
}

func (p *panelApp) footer(c *ui.Context) {
	t := c.Theme()
	s := p.app.ctrl.State()
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		status := "核心已停止"
		if s.Running {
			status = fmt.Sprintf("核心运行中 · 模式 %s", s.Mode)
		}
		ui.Text(c, status).Grow(1).FontSize(11).TextColor(t.TextMuted)
		if s.Running {
			button.Button(c, button.Props{Label: "停止", Variant: button.Destructive, OnClick: p.app.stopCore})
		} else {
			button.Button(c, button.Props{Label: "启动", Variant: button.Primary, OnClick: p.app.startCore})
		}
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
