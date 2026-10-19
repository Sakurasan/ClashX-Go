package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/egoist/mygo"
)

// shortcutAction is one bindable command, mirroring ClashX Meta's
// KeyboardShortcuts names.
type shortcutAction struct {
	id, label string
	fn        func()
}

func (a *app) shortcutActions() []shortcutAction {
	return []shortcutAction{
		{"systemProxy", "系统代理", func() { a.setSystemProxy(!a.settings.systemProxy) }},
		{"tun", "Tun 模式", func() { a.setTUN(!a.settings.tun) }},
		{"copyCommand", "复制终端命令", a.copyProxyCommand},
		{"modeDirect", "Direct 模式", func() { go a.setMode("direct") }},
		{"modeRule", "Rule 模式", func() { go a.setMode("rule") }},
		{"modeGlobal", "Global 模式", func() { go a.setMode("global") }},
		{"openMenu", "打开菜单", func() {
			a.menu.rebuild()
			a.tray.PopUpMenu()
		}},
		{"openLog", "打开日志", func() { _ = mygo.Shell.OpenPath(a.ctrl.Manager().LogPath()) }},
		{"openConsole", "打开控制台", a.openConsole},
	}
}

// applyShortcuts registers every configured global shortcut at launch.
func (a *app) applyShortcuts() {
	for _, act := range a.shortcutActions() {
		acc := strings.TrimSpace(a.settings.shortcuts[act.id])
		if acc == "" {
			continue
		}
		if err := mygo.GlobalShortcut.Register(acc, act.fn); err != nil {
			log.Printf("快捷键 %s (%s) 注册失败: %v", act.label, acc, err)
		} else {
			log.Printf("已注册快捷键 %s: %s", act.label, acc)
		}
	}
}

// setShortcut binds (or clears, with an empty accelerator) one action and
// persists it. A failed registration restores the previous binding.
func (a *app) setShortcut(id, label, acc string) {
	acc = strings.TrimSpace(acc)
	if a.settings.shortcuts == nil {
		a.settings.shortcuts = map[string]string{}
	}
	old := a.settings.shortcuts[id]
	if acc == old {
		return
	}
	var fn func()
	for _, act := range a.shortcutActions() {
		if act.id == id {
			fn = act.fn
			break
		}
	}
	if fn == nil {
		return
	}
	if old != "" {
		mygo.GlobalShortcut.Unregister(old)
	}
	if acc == "" {
		delete(a.settings.shortcuts, id)
		saveSettings(coreDir(), a.settings)
		return
	}
	if err := mygo.GlobalShortcut.Register(acc, fn); err != nil {
		if old != "" {
			_ = mygo.GlobalShortcut.Register(old, fn)
		}
		a.reportError("快捷键", fmt.Sprintf("%s 注册失败：%v", label, err))
		return
	}
	a.settings.shortcuts[id] = acc
	saveSettings(coreDir(), a.settings)
}
