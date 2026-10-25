package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
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

// savedSelection remembers a proxy group's chosen member per config, so a
// reload or config switch replays it like ClashX Meta's SavedProxyModel.
type savedSelection struct {
	Config   string `json:"config"`
	Group    string `json:"group"`
	Selected string `json:"selected"`
}

// appRoutingRule sends one application's traffic straight out (direct) or
// through the proxy before the profile's domain rules. Process is the
// executable name; Path is the .app bundle, used for a prefix regex that
// also catches helper processes.
type appRoutingRule struct {
	Name    string `json:"name"`
	Process string `json:"process"`
	Path    string `json:"path,omitempty"`
	Target  string `json:"target"` // "direct" or "proxy"
}

// defaultProxyBypass keeps local and Apple time-sync traffic off the proxy;
// the user can edit the list in the settings pane and reset it here.
const defaultProxyBypass = "127.*,192.168.*,10.*,localhost,*.local,timestamp.apple.com"

// defaultBenchmarkURL is ClashX Meta's latency test endpoint.
const defaultBenchmarkURL = "http://cp.cloudflare.com/generate_204"

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
	tunDNS         string
	ipv6           bool
	benchmarkURL   string
	proxyBypass    string
	suspendSSIDs   string
	noNotify       bool
	autoCheckUpd   bool
	appRules       []appRoutingRule
	selections     []savedSelection
	// shortcuts maps action id -> accelerator (e.g. "CmdOrCtrl+Shift+P").
	shortcuts map[string]string
	// lang is the interface language: "", "zh" or "en"; empty follows the
	// system.
	lang string
	// mode is the last outbound mode seen from the core, persisted so a
	// change made in the dashboard survives a restart.
	mode string
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
	TunDNS         string                `json:"tunDNS,omitempty"`
	IPv6           bool                  `json:"ipv6,omitempty"`
	BenchmarkURL   string                `json:"benchmarkURL,omitempty"`
	ProxyBypass    string                `json:"proxyBypass,omitempty"`
	SuspendSSIDs   string                `json:"suspendSSIDs,omitempty"`
	NoNotify       bool                  `json:"noNotify,omitempty"`
	AutoCheckUpd   *bool                 `json:"autoCheckUpdate,omitempty"`
	AppRules       []appRoutingRule      `json:"appRules,omitempty"`
	Selections     []savedSelection      `json:"selections,omitempty"`
	Shortcuts      map[string]string     `json:"shortcuts,omitempty"`
	Lang           string                `json:"lang,omitempty"`
	Mode           string                `json:"mode,omitempty"`
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
		mode:           "rule",
		autoUpdate:     true,
		updateInterval: 360,
		benchmarkURL:   defaultBenchmarkURL,
		proxyBypass:    defaultProxyBypass,
		shortcuts:      map[string]string{},
		autoCheckUpd:   true,
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
	s.tunDNS = strings.TrimSpace(w.TunDNS)
	s.ipv6 = w.IPv6
	if w.BenchmarkURL != "" {
		s.benchmarkURL = w.BenchmarkURL
	}
	if w.ProxyBypass != "" {
		s.proxyBypass = w.ProxyBypass
	}
	s.suspendSSIDs = w.SuspendSSIDs
	s.noNotify = w.NoNotify
	if w.AutoCheckUpd != nil {
		s.autoCheckUpd = *w.AutoCheckUpd
	}
	for i := range w.AppRules {
		if !strings.EqualFold(w.AppRules[i].Target, "proxy") {
			w.AppRules[i].Target = "direct"
		}
	}
	s.appRules = w.AppRules
	s.selections = w.Selections
	if w.Shortcuts != nil {
		s.shortcuts = w.Shortcuts
	}
	s.lang = w.Lang
	if w.Mode != "" {
		s.mode = w.Mode
	}
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

// splitList parses a comma-separated settings field into trimmed entries.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
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
		TunDNS:         s.tunDNS,
		IPv6:           s.ipv6,
		BenchmarkURL:   s.benchmarkURL,
		ProxyBypass:    s.proxyBypass,
		SuspendSSIDs:   s.suspendSSIDs,
		NoNotify:       s.noNotify,
		AutoCheckUpd:   &s.autoCheckUpd,
		AppRules:       s.appRules,
		Selections:     s.selections,
		Shortcuts:      s.shortcuts,
		Lang:           s.lang,
		Mode:           s.mode,
		ProxyBackup:    s.proxyBackup,
		ShowTraffic:    &s.showTraffic,
	}
	data, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "app.json"), data, 0o644)
}

// settingsPane is the 通用 page, laid out like ClashX Meta's: compact rows
// with the label left and the control right, grouped in rounded boxes.
func (p *panelApp) settingsPane(c *ui.Context) {
	s := p.app.settings
	t := c.Theme()
	ui.Column(c).FillWidth().Gap(14).Children(func() {
		settingsGroup(c, T("语言"), func() {
			settingsRow(c, T("界面语言"), func() {
				ui.Row(c).Padding(2).Radius(7).Background(t.Text.Alpha(0.08)).Children(func() {
					choiceSegment(c, T("跟随系统"), s.lang == "", func() { p.app.setLanguage("") })
					choiceSegment(c, "简体中文", s.lang == "zh", func() { p.app.setLanguage("zh") })
					choiceSegment(c, "English", s.lang == "en", func() { p.app.setLanguage("en") })
				})
			})
			ui.Text(c, T("跟随系统时使用 macOS 的首选语言。")).FontSize(11).TextColor(t.TextMuted)
		})
		settingsGroup(c, "", func() {
			settingsSwitch(c, T("开机启动"), &s.startAtLogin, func() {
				if err := mygo.App.SetOpenAtLogin(s.startAtLogin); err != nil {
					// Roll the switch back and surface the error.
					s.startAtLogin = !s.startAtLogin
					msg := err.Error()
					if strings.Contains(msg, "needs an app bundle") {
						msg = "当前以开发模式运行（非 .app 包），登录项不可用；用 mygo build 打包运行后即可设置。"
					}
					p.app.showToast(T("开机启动"), msg)
					return
				}
				saveSettings(coreDir(), p.app.settings)
			})
			settingsSwitch(c, T("减少通知"), &s.noNotify, func() {
				saveSettings(coreDir(), p.app.settings)
			})
			settingsSwitch(c, T("自动检查更新"), &s.autoCheckUpd, func() {
				saveSettings(coreDir(), p.app.settings)
			})
		})
		settingsGroup(c, T("Clash 设置"), func() {
			settingsSwitch(c, T("启用 IPv6"), &s.ipv6, func() {
				saveSettings(coreDir(), p.app.settings)
			})
			settingsRow(c, T("代理端口"), func() {
				ui.TextInput(c, &s.mixedPort).Width(150)
			})
			settingsRow(c, T("Api 端口"), func() {
				ui.TextInput(c, &s.controller).Width(150)
			})
			settingsRow(c, T("Api 密钥"), func() {
				ui.TextInput(c, &s.secret).Width(150).Placeholder(T("可选"))
			})
			settingsSwitch(c, T("允许局域网控制（不推荐）"), &s.allowLAN, func() {
				saveSettings(coreDir(), p.app.settings)
			})
			ui.Text(c, T("保存并重启核心后生效")).FontSize(11).TextColor(t.TextMuted)
		})
		settingsGroup(c, T("在特定 WiFi SSID 下自动暂停"), func() {
			ui.Text(c, T("使用英文逗号 (,) 分隔")).FontSize(11).TextColor(t.TextMuted)
			ui.TextInput(c, &s.suspendSSIDs)
		})
		settingsGroup(c, T("代理延迟测试地址"), func() {
			ui.TextInput(c, &s.benchmarkURL).Placeholder(defaultBenchmarkURL)
		})
		settingsGroup(c, T("忽略这些主机与域的代理设置"), func() {
			ui.Text(c, T("使用英文逗号 (,) 分隔")).FontSize(11).TextColor(t.TextMuted)
			ui.TextInput(c, &s.proxyBypass)
			ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
				settingsAction(c, T("重置"), func() {
					s.proxyBypass = defaultProxyBypass
					saveSettings(coreDir(), p.app.settings)
					p.app.win.Update(func() {})
				})
			})
		})
		settingsGroup(c, T("配置文件"), func() {
			settingsRow(c, T("本地配置"), func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.TextInput(c, &s.configPath).Width(240).Placeholder("config.yaml")
					settingsAction(c, T("浏览…"), func() {
						files, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
							Title:   T("选择 Clash 配置"),
							Filters: []mygo.FileFilter{{Name: "YAML 配置", Extensions: []string{"yaml", "yml"}}},
						})
						if err == nil && len(files) > 0 {
							s.configPath = files[0]
						}
					})
				})
			})
			ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
				nativeButton(c, T("保存并重启核心"), buttonPrimary, p.saveAndRestart)
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
	// The API port field may have changed; pick the run's TCP port from it.
	p.app.chooseControllerPort()
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
			p.app.showToast(T("重启失败"), err.Error())
		} else {
			p.app.showToast(T("核心已重启"), T("正在使用新的设置运行。"))
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
