package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

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

// subscriptionProfile is one managed subscription, the ClashX 托管配置
// entry: a display name, its URL and the local file holding the download.
type subscriptionProfile struct {
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	File      string    `json:"file"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// settings holds the editable controller configuration.
type settings struct {
	mixedPort      string
	controller     string
	secret         string
	configPath     string
	profiles       []subscriptionProfile
	activeProfile  string
	autoUpdate     bool
	updateInterval int // minutes
	startAtLogin   bool
	systemProxy    bool
	allowLAN       bool
	showTraffic    bool
	tun            bool
	// proxyBackup is the user's own system-proxy config captured before we
	// overwrote it; restoring it beats switching ours off.
	proxyBackup *systemProxyBackup
}

// settingsJSON is the wire form of settings. It exists because the in-memory
// fields are unexported (so the view can mutate them directly), and
// encoding/json cannot unmarshal into unexported fields.
type settingsJSON struct {
	MixedPort      string                `json:"mixedPort"`
	Controller     string                `json:"controller"`
	Secret         string                `json:"secret"`
	ConfigPath     string                `json:"configPath"`
	Profiles       []subscriptionProfile `json:"profiles"`
	ActiveProfile  string                `json:"activeProfile"`
	AutoUpdate     bool                  `json:"autoUpdate"`
	UpdateInterval int                   `json:"updateInterval"`
	StartAtLogin   bool                  `json:"startAtLogin"`
	SystemProxy    bool                  `json:"systemProxy"`
	AllowLAN       bool                  `json:"allowLAN"`
	ShowTraffic    *bool                 `json:"showTraffic,omitempty"`
	TUN            bool                  `json:"tun"`
	ProxyBackup    *systemProxyBackup    `json:"proxyBackup,omitempty"`
	// Legacy single-subscription field, migrated into Profiles on load.
	SubscriptionURL string `json:"subscriptionURL,omitempty"`
}

// loadSettings reads persisted settings from the app config dir.
func loadSettings(dir string) *settings {
	s := &settings{
		mixedPort:      "7890",
		controller:     "9090",
		showTraffic:    true,
		autoUpdate:     true,
		updateInterval: 360,
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
	s.profiles = w.Profiles
	s.activeProfile = w.ActiveProfile
	s.autoUpdate = w.AutoUpdate
	if w.UpdateInterval > 0 {
		s.updateInterval = w.UpdateInterval
	}
	s.startAtLogin = w.StartAtLogin
	s.systemProxy = w.SystemProxy
	s.allowLAN = w.AllowLAN
	s.tun = w.TUN
	s.proxyBackup = w.ProxyBackup
	if w.ShowTraffic != nil {
		s.showTraffic = *w.ShowTraffic
	}
	// Migrate the single-URL model: the old default file stays in place.
	// A hand-picked local config keeps priority, as before.
	if w.SubscriptionURL != "" && len(s.profiles) == 0 {
		name := profileNameFromURL(w.SubscriptionURL)
		s.profiles = append(s.profiles, subscriptionProfile{
			Name: name, URL: w.SubscriptionURL, File: "sub.yaml",
			UpdatedAt: subFileMtime(dir, "sub.yaml"),
		})
		if s.activeProfile == "" && s.configPath == "" {
			s.activeProfile = name
		}
	}
	return s
}

func subFileMtime(dir, file string) time.Time {
	fi, err := os.Stat(filepath.Join(dir, file))
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// saveSettings persists settings to the app config dir.
func saveSettings(dir string, s *settings) {
	w := settingsJSON{
		MixedPort:      s.mixedPort,
		Controller:     s.controller,
		Secret:         s.secret,
		ConfigPath:     s.configPath,
		Profiles:       s.profiles,
		ActiveProfile:  s.activeProfile,
		AutoUpdate:     s.autoUpdate,
		UpdateInterval: s.updateInterval,
		StartAtLogin:   s.startAtLogin,
		SystemProxy:    s.systemProxy,
		AllowLAN:       s.allowLAN,
		TUN:            s.tun,
		ProxyBackup:    s.proxyBackup,
		ShowTraffic:    &s.showTraffic,
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
		field.Field(c, field.Props{Label: "本地配置", Hint: "加载本地 config.yaml（选择后停用托管订阅）"}, func() {
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
	st := p.app.settings
	if st.configPath != "" {
		st.activeProfile = "" // a hand-set local path wins over a profile
	}
	saveSettings(coreDir(), st)
	p.app.menu.rebuild()
	cfg := p.app.settingsConfig()
	go func() {
		// Refresh the subscription while the old core may still be running,
		// so the fetch itself can be proxied through it (the subscription
		// host is often unreachable directly). A failure is non-fatal: the
		// previous copy stays in place.
		if cfg.SubscriptionURL != "" && p.app.ctrl != nil {
			if _, _, err := p.app.ctrl.Manager().RefreshSubscription(context.Background(), cfg); err != nil {
				log.Println("subscription refresh: ", err)
			}
		}
		if err := p.app.applyConfig(cfg); err != nil {
			p.app.showToast("重启失败", err.Error())
		} else {
			p.app.showToast("核心已重启", "正在使用新的设置运行。")
		}
	}()
}

func parsePort(s string, def int) int {
	n := 0
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n <= 0 {
		return def
	}
	return n
}
