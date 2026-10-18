package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"clashx-go/core"

	"github.com/smallnest/uix/components/button"
	"github.com/smallnest/uix/components/field"
	"github.com/smallnest/uix/components/input"
	switcher "github.com/smallnest/uix/components/switch"
)

// toastMsg is a pending toast to show on the next frame.
type toastMsg struct {
	title string
	desc  string
}

// settings holds the editable controller configuration.
type settings struct {
	mixedPort       string
	controller      string
	secret          string
	configPath      string
	subscriptionURL string
	startAtLogin    bool
	systemProxy     bool
	allowLAN        bool
	showTraffic     bool
}

// settingsJSON is the wire form of settings. It exists because the in-memory
// fields are unexported (so the view can mutate them directly), and
// encoding/json cannot unmarshal into unexported fields.
type settingsJSON struct {
	MixedPort       string `json:"mixedPort"`
	Controller      string `json:"controller"`
	Secret          string `json:"secret"`
	ConfigPath      string `json:"configPath"`
	SubscriptionURL string `json:"subscriptionURL"`
	StartAtLogin    bool   `json:"startAtLogin"`
	SystemProxy     bool   `json:"systemProxy"`
	AllowLAN        bool   `json:"allowLAN"`
	ShowTraffic     *bool  `json:"showTraffic,omitempty"`
}

// loadSettings reads persisted settings from the app config dir.
func loadSettings(dir string) *settings {
	s := &settings{
		mixedPort:   "7890",
		controller:  "9090",
		showTraffic: true,
	}
	data, err := os.ReadFile(filepath.Join(dir, "app.json"))
	if err != nil {
		return s
	}
	var w settingsJSON
	if err := json.Unmarshal(data, &w); err != nil {
		return s
	}
	s.mixedPort = w.MixedPort
	s.controller = w.Controller
	s.secret = w.Secret
	s.configPath = w.ConfigPath
	s.subscriptionURL = w.SubscriptionURL
	s.startAtLogin = w.StartAtLogin
	s.systemProxy = w.SystemProxy
	s.allowLAN = w.AllowLAN
	if w.ShowTraffic != nil {
		s.showTraffic = *w.ShowTraffic
	}
	return s
}

// saveSettings persists settings to the app config dir.
func saveSettings(dir string, s *settings) {
	w := settingsJSON{
		MixedPort:       s.mixedPort,
		Controller:      s.controller,
		Secret:          s.secret,
		ConfigPath:      s.configPath,
		SubscriptionURL: s.subscriptionURL,
		StartAtLogin:    s.startAtLogin,
		SystemProxy:     s.systemProxy,
		AllowLAN:        s.allowLAN,
		ShowTraffic:     &s.showTraffic,
	}
	data, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "app.json"), data, 0o644)
}

// settingsPane is the Settings tab of the panel: endpoints, secret,
// config profile and a Start at login switch.
func (p *panelApp) settingsPane(c *ui.Context) {
	s := p.app.settings
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		ui.Text(c, "核心端点").FontSize(13).Bold()
		field.Field(c, field.Props{Label: "混合端口", Hint: "HTTP/SOCKS5 代理端口"}, func() {
			input.Input(c, input.Props{Value: &s.mixedPort})
		})
		field.Field(c, field.Props{Label: "外部控制器", Hint: "控制器 API 监听端口"}, func() {
			input.Input(c, input.Props{Value: &s.controller})
		})
		field.Field(c, field.Props{Label: "密钥", Hint: "控制器 API 的 Bearer 令牌"}, func() {
			input.Input(c, input.Props{Value: &s.secret, Placeholder: "可选"})
		})
		ui.Divider(c)
		field.Field(c, field.Props{Label: "订阅地址", Hint: "Clash 订阅链接 — 自动拉取并更新节点"}, func() {
			ui.Row(c).Gap(6).Children(func() {
				input.Input(c, input.Props{Value: &s.subscriptionURL, Placeholder: "https://…/subscribe?token=…"}).Grow(1)
			})
		})
		ui.Divider(c)
		field.Field(c, field.Props{Label: "本地配置", Hint: "加载本地 config.yaml（优先于订阅）"}, func() {
			ui.Row(c).Gap(6).Children(func() {
				input.Input(c, input.Props{Value: &s.configPath, Placeholder: "config.yaml 路径"}).Grow(1)
				button.Button(c, button.Props{
					Label:   "浏览…",
					Variant: button.Outline,
					OnClick: func() {
						files, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
							Title:   "选择 Clash 配置",
							Filters: []mygo.FileFilter{{Name: "YAML 配置", Extensions: []string{"yaml", "yml"}}},
						})
						if err == nil && len(files) > 0 {
							s.configPath = files[0]
						}
					},
				})
			})
		})
		switcher.Switch(c, switcher.Props{
			On:    &s.startAtLogin,
			Label: "开机启动",
			Changed: func(on bool) {
				if err := mygo.App.SetOpenAtLogin(on); err != nil {
					// Roll the switch back and surface the error.
					s.startAtLogin = !on
					p.app.showToast("开机启动", "无法修改登录项："+err.Error())
					return
				}
				saveSettings(coreDir(), p.app.settings)
			},
		})
		ui.Row(c).FillWidth().Gap(6).Justify(ui.End).Children(func() {
			button.Button(c, button.Props{
				Label:   "保存并重启核心",
				Variant: button.Primary,
				OnClick: func() {
					p.saveAndRestart()
				},
			})
		})
	})
}

// saveAndRestart persists the settings and restarts the core with them.
func (p *panelApp) saveAndRestart() {
	cfg := p.currentConfig()
	cfg.MixedPort = parsePort(p.app.settings.mixedPort, cfg.MixedPort)
	cfg.ControllerPort = parsePort(p.app.settings.controller, cfg.ControllerPort)
	cfg.Secret = p.app.settings.secret
	cfg.ConfigPath = p.app.settings.configPath
	cfg.SubscriptionURL = p.app.settings.subscriptionURL
	saveSettings(coreDir(), p.app.settings)
	go func() {
		// Refresh the subscription while the old core may still be running,
		// so the fetch itself can be proxied through it (the subscription
		// host is often unreachable directly). A failure is non-fatal: the
		// previous copy stays in place.
		if cfg.SubscriptionURL != "" && p.app.ctrl != nil {
			if _, err := p.app.ctrl.Manager().RefreshSubscription(context.Background(), cfg); err != nil {
				log.Println("subscription refresh: ", err)
			}
		}
		if p.app.ctrl != nil {
			p.app.ctrl.Stop()
		}
		if err := p.app.restartCore(cfg); err != nil {
			p.app.showToast("重启失败", err.Error())
		} else {
			p.app.showToast("核心已重启", "正在使用新的设置运行。")
		}
	}()
}

// currentConfig returns the controller's current configuration, or the
// defaults when no controller exists yet.
func (p *panelApp) currentConfig() core.Config {
	if p.app.ctrl != nil {
		return p.app.ctrl.Config()
	}
	return core.DefaultConfig()
}

func parsePort(s string, def int) int {
	n := 0
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n <= 0 {
		return def
	}
	return n
}
