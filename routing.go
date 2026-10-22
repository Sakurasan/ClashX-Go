package main

import (
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// routingPane lists the per-application rules. They are written at the top
// of the running rule list (PROCESS-NAME plus a bundle-path regex) so they
// win over the profile's domain rules in rule mode.
func (p *panelApp) routingPane(c *ui.Context) {
	t := c.Theme()
	a := p.app
	ui.Column(c).FillWidth().Gap(12).Children(func() {
		ui.Text(c, T("在规则模式下，这些应用的流量会在域名规则之前按下面的选择分流。TUN 与系统代理都生效。")).
			FontSize(11).TextColor(t.TextMuted)
		settingsGroup(c, T("应用规则"), func() {
			if len(a.settings.appRules) == 0 {
				ui.Text(c, T("还没有添加应用。")).FontSize(11).TextColor(t.TextMuted)
			}
			for i := range a.settings.appRules {
				i := i
				rule := a.settings.appRules[i]
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
					ui.Column(c).Gap(1).Grow(1).Children(func() {
						ui.Text(c, rule.Name).FontSize(13)
						ui.Text(c, "PROCESS-NAME "+rule.Process).FontSize(10).TextColor(t.TextMuted).SingleLine()
					})
					ui.Row(c).Padding(2).Radius(7).Background(t.Text.Alpha(0.08)).Children(func() {
						choiceSegment(c, T("直连"), rule.Target == "direct", func() {
							a.setAppRuleTarget(i, "direct")
						})
						choiceSegment(c, T("代理"), rule.Target != "direct", func() {
							a.setAppRuleTarget(i, "proxy")
						})
					})
					settingsDanger(c, T("删除"), func() { a.removeAppRule(i) })
				})
			}
			ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
				settingsAction(c, "+ "+T("添加应用…"), a.addAppRules)
			})
		})
		if p.ruleDirty {
			ui.Text(c, T("有未应用的修改")).FontSize(11).TextColor(t.Warning)
		}
		ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
			nativeButton(c, T("保存并应用"), buttonPrimary, a.applyAppRules)
		})
	})
}

// addAppRules lets the user pick .app bundles and adds one rule per app.
// The bundle's CFBundleExecutable is the process name; the bundle path
// feeds a PROCESS-PATH-REGEX that also matches its helper processes.
func (a *app) addAppRules() {
	files, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:       T("选择应用"),
		Message:     T("选择要单独分流的 .app（可多选）"),
		DefaultPath: "/Applications",
		Multiple:    true,
		// No type filter: MyGo turns an "app" extension into
		// com.apple.application-file, which does not match the
		// com.apple.application-bundle type of a real .app, so every app is
		// greyed out. Packages are selectable as files; the suffix check
		// below rejects anything that is not an .app.
	})
	if err != nil || len(files) == 0 {
		return
	}
	added := 0
	for _, path := range files {
		path = strings.TrimRight(path, "/")
		if !strings.HasSuffix(strings.ToLower(path), ".app") {
			continue
		}
		name, process := appBundleInfo(path)
		if process == "" {
			continue
		}
		duplicate := false
		for _, rule := range a.settings.appRules {
			if strings.EqualFold(rule.Process, process) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		a.settings.appRules = append(a.settings.appRules, appRoutingRule{
			Name: name, Process: process, Path: path, Target: "direct",
		})
		added++
	}
	if added == 0 {
		a.showToast(T("添加应用"), T("没有添加新的应用（请选择 .app 包）"))
		return
	}
	a.panel.ruleDirty = true
	saveSettings(coreDir(), a.settings)
	a.win.Update(func() {})
}

// appBundleInfo reads the display name and executable name from a .app's
// Info.plist, falling back to the bundle file name.
func appBundleInfo(path string) (name, process string) {
	base := filepath.Base(path)
	name = strings.TrimSuffix(base, filepath.Ext(base))
	process = name
	plist := filepath.Join(path, "Contents", "Info.plist")
	if v := plistValue(plist, "CFBundleExecutable"); v != "" {
		process = v
	}
	for _, key := range []string{"CFBundleDisplayName", "CFBundleName"} {
		if v := plistValue(plist, key); v != "" {
			name = v
			break
		}
	}
	return name, process
}

func plistValue(plist, key string) string {
	out, err := exec.Command("/usr/bin/plutil", "-extract", key, "raw", "-o", "-", plist).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (a *app) setAppRuleTarget(i int, target string) {
	if i < 0 || i >= len(a.settings.appRules) {
		return
	}
	a.settings.appRules[i].Target = target
	a.panel.ruleDirty = true
	saveSettings(coreDir(), a.settings)
	a.win.Update(func() {})
}

func (a *app) removeAppRule(i int) {
	if i < 0 || i >= len(a.settings.appRules) {
		return
	}
	a.settings.appRules = append(a.settings.appRules[:i], a.settings.appRules[i+1:]...)
	a.panel.ruleDirty = true
	saveSettings(coreDir(), a.settings)
	a.win.Update(func() {})
}

// applyAppRules writes the rules into the running config through the normal
// live-reload path (a stopped core starts with them instead).
func (a *app) applyAppRules() {
	a.panel.ruleDirty = false
	saveSettings(coreDir(), a.settings)
	a.win.Update(func() {})
	cfg := a.settingsConfig()
	go func() {
		if err := a.applyConfig(cfg); err != nil {
			a.reportError(T("应用分流失败"), err.Error())
			return
		}
		a.showToast(T("分流规则已应用"), T("新的应用规则已写入配置。"))
	}()
}
