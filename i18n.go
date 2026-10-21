package main

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// uiLang is the language of the native UI: "zh" (default) or "en". It
// follows macOS's first preferred language, and CLASHX_GO_LANG overrides it
// (used by tests and for trying the English UI).
var uiLang = detectLang()

func detectLang() string {
	if v := strings.ToLower(os.Getenv("CLASHX_GO_LANG")); v != "" {
		if strings.HasPrefix(v, "en") {
			return "en"
		}
		return "zh"
	}
	out, err := exec.Command("defaults", "read", "-g", "AppleLanguages").Output()
	if err != nil {
		return "zh"
	}
	if m := langTagRE.FindStringSubmatch(string(out)); m != nil {
		if strings.HasPrefix(strings.ToLower(m[1]), "en") {
			return "en"
		}
	}
	return "zh"
}

var langTagRE = regexp.MustCompile(`"([A-Za-z-]+)"`)

// T translates a Chinese UI string to English when the UI runs in English.
// Unknown strings stay as they are, so new text degrades to Chinese instead
// of disappearing.
func T(zh string) string {
	if uiLang != "en" {
		return zh
	}
	if en, ok := en[zh]; ok {
		return en
	}
	return zh
}

var en = map[string]string{
	// Tray menu
	"出站模式":          "Outbound Mode",
	"规则":            "Rule",
	"全局":            "Global",
	"直连":            "Direct",
	"Tun 模式":        "TUN Mode",
	"系统代理":          "System Proxy",
	"开机启动":          "Launch at Login",
	"显示速率":          "Show Speed",
	"允许局域网":         "Allow LAN",
	"延迟测速":          "Test Latency",
	"控制台":           "Dashboard",
	"配置":            "Config",
	"本地配置":          "Local Config",
	"托管配置":          "Managed Config",
	"管理…":           "Manage…",
	"更新":            "Update",
	"自动更新":          "Auto Update",
	"设置更新间隔…":       "Update Interval…",
	"更多设置":          "More Settings",
	"偏好设置…":         "Preferences…",
	"重载核心配置":        "Reload Core Config",
	"关闭所有连接":        "Close All Connections",
	"帮助":            "Help",
	"检查更新…":         "Check for Updates…",
	"导出诊断…":         "Export Diagnostics…",
	"关于…":           "About…",
	"日志等级":          "Log Level",
	"打开日志文件":        "Open Log File",
	"退出":            "Quit",
	"复制终端命令":        "Copy Shell Command",
	"HTTP/SOCKS 端口": "HTTP/SOCKS Port",
	"API 端口":        "API Port",

	// Settings tabs and General
	"通用":     "General",
	"全局快捷键":  "Shortcuts",
	"调试":     "Debug",
	"减少通知":   "Reduce Notifications",
	"自动检查更新": "Check for Updates Automatically",
	"Clash 设置（保存并重启核心后生效）": "Clash Settings (applies after saving and restarting the core)",
	"启用 IPv6":             "Enable IPv6",
	"代理端口":                "Proxy Port",
	"Api 端口":              "API Port",
	"Api 密钥":              "API Secret",
	"可选":                  "Optional",
	"允许局域网控制（不推荐）":        "Allow LAN Control (not recommended)",
	"在特定 WiFi SSID 下自动暂停": "Pause on These Wi-Fi SSIDs",
	"使用英文逗号 (,) 分隔":       "Separate with commas (,)",
	"代理延迟测试地址":            "Latency Test URL",
	"忽略这些主机与域的代理设置":       "Proxy Bypass Domains",
	"重置":          "Reset",
	"配置文件":        "Profiles",
	"浏览…":         "Browse…",
	"保存并重启核心":     "Save & Restart Core",
	"选择 Clash 配置": "Choose a Clash Config",
	"YAML 配置":     "YAML Config",

	// Shortcuts pane
	"格式例如 CmdOrCtrl+Shift+P；留空并点设置即取消绑定。": "For example CmdOrCtrl+Shift+P; leave empty and click Set to unbind.",
	"未设置":       "Not set",
	"设置":        "Set",
	"Direct 模式": "Direct Mode",
	"Rule 模式":   "Rule Mode",
	"Global 模式": "Global Mode",
	"打开菜单":      "Open Menu",
	"打开日志":      "Open Log",
	"打开控制台":     "Open Dashboard",

	// Debug pane
	"打开配置目录": "Open Config Folder",
	"导出诊断包":  "Export Diagnostics",
	"立即把系统代理恢复为开启前的配置。": "Restore the system proxy to its state before the app enabled it.",
	"还原系统代理": "Restore System Proxy",
	"核心":     "Core",

	// Meta pane / About
	"内核":                        "Core",
	"配置目录":                      "Config Folder",
	"应用":                        "Application",
	"关于 ClashX go":              "About ClashX go",
	"关于 ClashX Go":              "About ClashX Go",
	"ClashX 设置":                 "ClashX Settings",
	"设置页":                       "Settings",
	"版本: ":                      "Version: ",
	"Meta Core: ":               "Meta Core: ",
	"配置目录: ~/.config/clashx-go": "Config folder: ~/.config/clashx-go",
	"打开":                        "Open",
	"检查更新":                      "Check for Updates",
	"当前构建未启用更新检查。":              "Update checks are not enabled in this build.",
	"需要在 mygo.json 配置 updates（publicKey + github 仓库），并用 mygo build 打包出 .app 运行。": "Configure updates (publicKey + github repository) in mygo.json and run the packaged .app.",
	"已是最新版本":         "You're up to date",
	"发现新版本":          "New Version Available",
	"下载并安装":          "Download & Install",
	"取消":             "Cancel",
	"安装更新":           "Install Update",
	"ClashX go 有新版本": "ClashX go has an update",
	"可用，点击检查更新":      "is available — click to check for updates",
	"订阅流量即将用尽":       "Subscription quota is almost used up",
	"订阅即将到期":         "Subscription is expiring soon",
	"当前以开发模式运行（非 .app 包），登录项不可用；用 mygo build 打包运行后即可设置。": "Login items need a packaged .app; build with mygo build to enable this.",
	"无法修改登录项：":              "Cannot change the login item: ",
	"设置为系统代理":               "Set as System Proxy",
	"复制终端代理命令":              "Copy Shell Command",
	"显示实时速率":                "Show Live Speed",
	"允许局域网连接":               "Allow LAN",
	"本地配置…":                 "Local Config…",
	"本地配置: ":                "Local Config: ",
	"打开配置文件夹":               "Open Config Folder",
	"重载配置文件":                "Reload Config",
	"更新外部资源":                "Update External Resources",
	"管理":                    "Manage",
	"设置更新间隔":                "Update Interval",
	"30 分钟":                 "30 minutes",
	"1 小时":                  "1 hour",
	"6 小时":                  "6 hours",
	"12 小时":                 "12 hours",
	"24 小时":                 "24 hours",
	"订阅：%d 个节点":             "Subscription: %d nodes",
	"订阅：无":                  "Subscription: none",
	"订阅流量 %s / %s · %d 个节点": "Traffic %s / %s · %d nodes",
	" · 到期 ":                " · expires ",
	"选择本地 Clash 配置":         "Choose a Local Clash Config",
	"托管的配置文件":               "Managed Configs",
	"链接":                    "URL",
	"配置文件名称":                "Name",
	"更新时间":                  "Updated",
	"添加":                    "Add",
	"删除":                    "Delete",
	"删除订阅":                  "Delete Subscription",
	"确定删除订阅「%s」？其订阅文件会一并移除。": "Delete subscription %s? Its files will be removed too.",
	"添加订阅": "Add Subscription",
	"Clash 订阅链接 — 下载后作为一个托管配置文件加入列表": "Clash subscription URL — downloaded and added as a managed config",
	"重启失败":        "Restart Failed",
	"核心已重启":       "Core Restarted",
	"正在使用新的设置运行。": "Running with the new settings.",
	"更新订阅":        "Update Subscription",
	"订阅已更新":       "Subscription Updated",
	"复制失败":        "Copy Failed",
	"切换模式失败":      "Failed to Switch Mode",
	"切换节点失败":      "Failed to Switch Node",
}
