package main

import (
	"fmt"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	tokens "github.com/smallnest/uix/components/base"
	"github.com/smallnest/uix/components/button"
)

// manageWindow is the ClashX-style 托管的配置文件 list: every managed
// subscription with its link and update time, edited with 添加/更新/删除.
type manageWindow struct {
	app      *app
	win      *mygo.Window
	list     ui.ListState
	selected int
}

func (a *app) showManageWindow() {
	m := a.manage
	if m == nil {
		m = &manageWindow{app: a, selected: -1}
		m.list.Selected = &m.selected
		m.win = mygo.NewWindow(mygo.WindowOptions{
			Title: "托管的配置文件", Width: 640, Height: 360,
			Hidden: true, DisableMinimize: true,
			Content: ui.View(m.view),
		})
		m.win.OnClose(func(e *mygo.CloseEvent) { e.PreventDefault(); m.win.Hide() })
		a.manage = m
	}
	for i, p := range a.settings.profiles {
		if p.Name == a.settings.activeProfile {
			m.selected = i
		}
	}
	m.win.Show()
	m.win.Center()
	m.win.Update(func() {})
}

func (m *manageWindow) view(c *ui.Context) {
	t := c.Theme()
	c.SetTheme(tokens.For(t.Dark))
	profiles := m.app.settings.profiles
	ui.Column(c).Fill().Padding(14).Gap(10).Children(func() {
		ui.Text(c, "托管的配置文件：").FontSize(13).Bold()
		cols := []ui.TableColumn{
			{Title: "链接"},
			{Title: "配置文件名称", Width: 150},
			{Title: "更新时间", Width: 110},
		}
		if ui.Table(c, &m.list, cols, len(profiles), func(row, col int) {
			p := profiles[row]
			switch col {
			case 0:
				ui.Text(c, p.URL).SingleLine()
			case 1:
				ui.Text(c, p.Name).SingleLine()
			case 2:
				if p.UpdatedAt.IsZero() {
					ui.Text(c, "—")
				} else {
					ui.Text(c, p.UpdatedAt.Local().Format("01-02 15:04"))
				}
			}
		}).Grow(1).Submitted() {
			if p := m.current(); p != nil {
				m.app.selectProfile(p.Name)
			}
		}
		ui.Row(c).Gap(8).Children(func() {
			button.Button(c, button.Props{Label: "添加", Variant: button.Primary, OnClick: m.app.showSubscriptionDialog})
			button.Button(c, button.Props{Label: "更新", Variant: button.Outline, OnClick: m.onUpdate})
			button.Button(c, button.Props{Label: "删除", Variant: button.Destructive, OnClick: m.onDelete})
		})
	})
}

func (m *manageWindow) current() *subscriptionProfile {
	if m.selected >= 0 && m.selected < len(m.app.settings.profiles) {
		return &m.app.settings.profiles[m.selected]
	}
	return nil
}

func (m *manageWindow) onUpdate() {
	p := m.current()
	if p == nil {
		m.app.reportError("更新", "请先在列表中选中一个订阅。")
		return
	}
	a, name := m.app, p.Name
	go func() {
		n, err := a.updateProfile(name)
		if err != nil {
			a.reportError("更新订阅", err.Error())
			return
		}
		a.menu.rebuild()
		m.win.Update(func() {})
		a.showToast("订阅已更新", fmt.Sprintf("%s：%d 个节点。", name, n))
	}()
}

func (m *manageWindow) onDelete() {
	p := m.current()
	if p == nil {
		m.app.reportError("删除", "请先在列表中选中一个订阅。")
		return
	}
	r, err := mygo.Dialog.Message(mygo.MessageOptions{
		Type: mygo.MessageQuestion, Title: "删除订阅",
		Message: fmt.Sprintf("确定删除订阅「%s」？其订阅文件会一并移除。", p.Name),
		Buttons: []string{"删除", "取消"}, CancelButton: 1,
	})
	if err != nil || r.Button != 0 {
		return
	}
	m.selected = -1
	m.app.removeProfile(p.Name)
	m.win.Update(func() {})
}
