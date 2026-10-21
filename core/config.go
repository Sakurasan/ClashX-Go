package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Config describes the Clash/Mihomo core runtime configuration. The app
// writes a runtime config.yaml for the external-controller and the mixed
// port, then lets the core manage proxies, DNS and rules.
type Config struct {
	// MixedPort is the mixed HTTP/SOCKS5 listen port.
	MixedPort int `json:"mixed-port"`
	// ControllerPort is the external-controller listen port.
	ControllerPort int `json:"controller-port"`
	// ControllerSocket is the Unix socket mihomo serves the same API on.
	// The app talks to the core over it, so a TCP port still finishing its
	// shutdown never delays a restart; the TCP port remains for the
	// dashboard and external tools.
	ControllerSocket string `json:"controller-socket,omitempty"`
	// Secret is the external-controller API bearer secret.
	Secret string `json:"secret"`
	// Mode is the running mode: rule, global, or direct.
	Mode string `json:"mode"`
	// ConfigPath is the user's config/profile YAML to load, if any.
	ConfigPath string `json:"config-path"`
	// SubscriptionURL, when set, activates the downloaded subscription. A
	// full Clash config (one that declares proxy-groups) is used as-is with
	// the app-managed keys overlaid, so the provider's own groups, rules
	// and DNS survive; a provider-style node list gets a generated config
	// with a file provider pointing at the local copy.
	SubscriptionURL string `json:"subscription-url"`
	// SubscriptionFile overrides the downloaded-subscription file name
	// (default SubscriptionFile). Managed profiles each keep their own.
	SubscriptionFile string `json:"subscription-file"`
	// AllowLAN lets devices on the local network use the mixed port.
	AllowLAN bool `json:"allow-lan"`
	// ExternalUI is a directory (relative to the working dir) served at
	// /ui by the controller — the bundled dashboard for the 控制台 item.
	ExternalUI string `json:"external-ui,omitempty"`
	// TUN enables the core's TUN interface. Opening the device needs
	// administrator rights; without them the core exits at startup.
	TUN bool `json:"tun"`
	// IPv6 enables IPv6 in the core runtime config.
	IPv6 bool `json:"ipv6"`
	// BenchmarkURL is the URL used for per-node latency tests.
	BenchmarkURL string `json:"benchmark-url"`
	// ProcessRules route selected applications before the profile's own
	// rules. An empty Path matches only the executable name; a Path adds a
	// bundle-prefix regex so helper processes inside a .app match too.
	ProcessRules []ProcessRule `json:"process-rules,omitempty"`
}

// ProcessRule is one per-application routing rule: Target is DIRECT or
// PROXY.
type ProcessRule struct {
	Process string `json:"process"`
	Path    string `json:"path,omitempty"`
	Target  string `json:"target"`
}

// DefaultConfig returns the settings used on first launch.
func DefaultConfig() Config {
	return Config{
		MixedPort:      7890,
		ControllerPort: 9090,
		Mode:           "rule",
	}
}

// subFile returns the subscription file name this config uses.
func (c Config) subFile() string {
	if c.SubscriptionFile != "" {
		return c.SubscriptionFile
	}
	return SubscriptionFile
}

// flowNameRE extracts the name from a flow-style group entry,
// e.g. `- {name: 节点选择, type: select, …}`.
var flowNameRE = regexp.MustCompile(`^\-\s*\{.*?name:\s*"?([^",}\s][^",}]*)"?\s*,`)

// runningOverrideKeys are top-level keys the app owns: the settings panel
// and menu toggles win over whatever the user's profile declares.
var runningOverrideKeys = []string{"mixed-port", "port", "socks-port", "redir-port", "tproxy-port", "allow-lan", "tun", "external-ui", "find-process-mode", "external-controller-unix"}

// writeRunningConfig copies a user profile to dest, stripping its
// app-managed keys (including whole mapping blocks like tun:) and
// prepending ours, so the 端口设置/局域网/Tun toggles take effect exactly
// like ClashX's port settings override.
func writeRunningConfig(src, dest string, cfg Config) (string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "mixed-port: %d\n", cfg.MixedPort)
	fmt.Fprintf(&b, "allow-lan: %t\n", cfg.AllowLAN)
	if cfg.ControllerSocket != "" {
		fmt.Fprintf(&b, "external-controller-unix: %q\n", cfg.ControllerSocket)
	}
	// Process rules need the core to resolve the owning process; strict only
	// does that lookup when such a rule exists.
	b.WriteString("find-process-mode: strict\n")
	if cfg.ExternalUI != "" {
		fmt.Fprintf(&b, "external-ui: %s\n", cfg.ExternalUI)
	}
	fmt.Fprintf(&b, "tun:\n  enable: %t\n  stack: system\n"+
		"  strict-route: false\n  dns-hijack:\n    - any:53\n", cfg.TUN)
	var body strings.Builder
	skipBlock := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		indented := trimmed != "" && (line[0] == ' ' || line[0] == '\t')
		if skipBlock {
			if indented {
				continue
			}
			skipBlock = false
		}
		if !indented && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			if key, _, ok := strings.Cut(trimmed, ":"); ok && slices.Contains(runningOverrideKeys, strings.TrimSpace(key)) {
				skipBlock = strings.HasSuffix(trimmed, ":") // mapping key whose indented block follows
				continue
			}
		}
		body.WriteString(line)
		body.WriteString("\n")
	}
	groups := GroupOrder(src)
	proxyTarget := "GLOBAL"
	if len(groups) > 0 {
		proxyTarget = groups[0]
	}
	// The catch-all target is where unmatched traffic already goes, so it is
	// the least surprising destination for "via proxy" app rules.
	if match := profileMatchTarget(src); match != "" && !strings.EqualFold(match, "DIRECT") && !strings.EqualFold(match, "REJECT") {
		proxyTarget = match
	}
	ruleLines := processRuleLines(cfg.ProcessRules, func(target string) string {
		if target == "PROXY" {
			return proxyTarget
		}
		return target
	})
	injected := injectProcessRules(body.String(), ruleLines, proxyTarget)
	b.WriteString(injected)
	if err := os.WriteFile(dest, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return dest, nil
}

// processRuleLines renders the mihomo rules for the app routing list. A
// target resolver may drop PROXY when the config has no proxy group.
func processRuleLines(rules []ProcessRule, resolve func(string) string) []string {
	var lines []string
	for _, rule := range rules {
		process := strings.TrimSpace(rule.Process)
		path := strings.TrimSpace(rule.Path)
		if process == "" && path == "" {
			continue
		}
		target := resolve(normalizeRuleTarget(rule.Target))
		if target == "" {
			continue
		}
		// Commas separate rule fields; an app name or path with one cannot
		// be expressed in this rule syntax.
		if process != "" && !strings.ContainsAny(process, ",\n") {
			lines = append(lines, "PROCESS-NAME,"+process+","+target)
		}
		if path != "" && !strings.ContainsAny(path, ",\n") {
			lines = append(lines, "PROCESS-PATH-REGEX,^"+regexp.QuoteMeta(path)+"/,"+target)
		}
	}
	return lines
}

// profileMatchTarget returns the destination of the profile's MATCH rule,
// which is where traffic not matched by any earlier rule goes.
func profileMatchTarget(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	inRules := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == line && strings.HasPrefix(trimmed, "rules:") {
			inRules = true
			continue
		}
		if !inRules {
			continue
		}
		if trimmed == line && trimmed != "" && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "-") {
			break // the next top-level key
		}
		item := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		if len(item) > 6 && strings.EqualFold(item[:6], "MATCH,") {
			return strings.Trim(strings.TrimSpace(item[6:]), `"'`)
		}
	}
	return ""
}

func normalizeRuleTarget(target string) string {
	if strings.EqualFold(strings.TrimSpace(target), "PROXY") {
		return "PROXY"
	}
	return "DIRECT"
}

// injectProcessRules puts the app routing rules at the top of the profile's
// rules block, matching its list indentation. If the profile has no rules
// block at all, it appends one with a catch-all after the rules.
func injectProcessRules(data string, rules []string, catchAll string) string {
	if len(rules) == 0 {
		return data
	}
	lines := strings.Split(data, "\n")
	out := make([]string, 0, len(lines)+len(rules)+2)
	injected := false
	for i, line := range lines {
		out = append(out, line)
		if injected || line != strings.TrimLeft(line, " \t") || strings.TrimSpace(line) != "rules:" {
			continue
		}
		indent := "  "
		for j := i + 1; j < len(lines); j++ {
			trimmed := strings.TrimSpace(lines[j])
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
				indent = lines[j][:len(lines[j])-len(strings.TrimLeft(lines[j], " \t"))]
			}
			break
		}
		for _, rule := range rules {
			out = append(out, indent+"- "+rule)
		}
		injected = true
	}
	if !injected {
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
		out = append(out, "rules:")
		for _, rule := range rules {
			out = append(out, "  - "+rule)
		}
		out = append(out, "  - MATCH,"+catchAll)
	}
	return strings.Join(out, "\n")
}

// fullProfile reports whether path is a complete Clash config (it declares
// proxy-groups) rather than a provider-style node list. Such subscriptions
// keep their own groups, rules and DNS instead of the generated fallback.
func fullProfile(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == line && strings.HasPrefix(trimmed, "proxy-groups:") {
			return true
		}
	}
	return false
}

// GroupOrder returns the proxy-groups names in the order the config file
// declares them, so the UI can list groups like ClashX does instead of the
// random order of the core's JSON map. Unknown or missing files yield nil.
func GroupOrder(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var names []string
	inBlock := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed == line { // no indent: a new top-level key
			inBlock = strings.HasPrefix(trimmed, "proxy-groups:")
			continue
		}
		if !inBlock {
			continue
		}
		var name string
		switch rest, ok := strings.CutPrefix(trimmed, "- name:"); {
		case ok:
			name = rest
		default:
			m := flowNameRE.FindStringSubmatch(trimmed)
			if m == nil {
				continue
			}
			name = m[1]
		}
		// Drop a trailing YAML comment, then surrounding quotes.
		if i := strings.Index(name, " #"); i >= 0 {
			name = name[:i]
		}
		names = append(names, strings.Trim(strings.TrimSpace(name), `"'`))
	}
	return names
}

// providerBlock renders the proxy-providers block plus the policy groups
// and rules. When localFile is non-empty the provider is a file provider
// reading that file (the app-downloaded subscription); otherwise it is an
// http provider that mihomo fetches itself.
func providerBlock(localFile, url string, rules []string) string {
	var p string
	if localFile != "" {
		p = "proxy-providers:\n" +
			"  provider:\n" +
			"    type: file\n" +
			"    path: \"" + localFile + "\"\n" +
			"    health-check:\n" +
			"      enable: true\n" +
			"      url: https://www.gstatic.com/generate_204\n" +
			"      interval: 300\n"
	} else {
		p = "proxy-providers:\n" +
			"  provider:\n" +
			"    type: http\n" +
			"    url: \"" + url + "\"\n" +
			"    interval: 3600\n" +
			"    health-check:\n" +
			"      enable: true\n" +
			"      url: https://www.gstatic.com/generate_204\n" +
			"      interval: 300\n"
	}
	p += "\nproxy-groups:\n" +
		"  - name: PROXY\n" +
		"    type: select\n" +
		"    use:\n" +
		"      - provider\n" +
		"  - name: AUTO\n" +
		"    type: url-test\n" +
		"    use:\n" +
		"      - provider\n" +
		"    url: https://www.gstatic.com/generate_204\n" +
		"    interval: 300\n"
	p += "\nrules:\n"
	for _, rule := range rules {
		p += "  - " + rule + "\n"
	}
	p += "  - GEOIP,CN,DIRECT\n" +
		"  - MATCH,PROXY\n"
	return p
}

// WriteYAML writes the runtime config.yaml for mihomo under dir, returning
// its path.
//
//   - When the user supplied a profile (ConfigPath), that file is used as
//     the config and endpoints are overridden on the CLI by Start.
//   - When a full subscription config is stored in dir/sub.yaml, that file
//     becomes the config (overlaid by Start), preserving its groups and
//     rules.
//   - When the subscription is only a node list, a file provider loads the
//     local copy (mihomo needs no network for the nodes).
//   - Otherwise the subscription URL is used as an http provider, which
//     mihomo fetches itself.
//   - With neither, a minimal DIRECT config is generated.
func (c Config) WriteYAML(dir string) (string, error) {
	path := filepath.Join(dir, "config.yaml")
	var b strings.Builder
	c.writeBase(&b)

	// A config without a proxy group can only honour DIRECT app rules.
	directRules := processRuleLines(c.ProcessRules, func(target string) string {
		if target == "PROXY" {
			return ""
		}
		return target
	})
	switch {
	case c.ConfigPath != "":
		writeRuleBlock(&b, directRules, "MATCH,DIRECT")
	case c.SubscriptionURL != "":
		file := c.SubscriptionFile
		if file == "" {
			file = SubscriptionFile
		}
		local := ""
		if fi, err := os.Stat(filepath.Join(dir, file)); err == nil && fi.Size() > 0 {
			local = file
		}
		rules := processRuleLines(c.ProcessRules, func(target string) string { return target })
		b.WriteString(providerBlock(local, c.SubscriptionURL, rules))
	default:
		writeRuleBlock(&b, directRules, "MATCH,DIRECT")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func writeRuleBlock(b *strings.Builder, rules []string, catchAll string) {
	b.WriteString("rules:\n")
	for _, rule := range rules {
		b.WriteString("  - " + rule + "\n")
	}
	b.WriteString("  - " + catchAll + "\n")
}

// writeBase renders the app-managed header: endpoints, ports, DNS and TUN.
func (c Config) writeBase(b *strings.Builder) {
	fmt.Fprintf(b, "mixed-port: %d\n", c.MixedPort)
	fmt.Fprintf(b, "external-controller: 127.0.0.1:%d\n", c.ControllerPort)
	if c.Secret != "" {
		fmt.Fprintf(b, "secret: %s\n", c.Secret)
	}
	fmt.Fprintf(b, "allow-lan: %t\n", c.AllowLAN)
	fmt.Fprintf(b, "mode: %s\n", c.Mode)
	if c.ExternalUI != "" {
		fmt.Fprintf(b, "external-ui: %s\n", c.ExternalUI)
	}
	b.WriteString("ipv6: " + strconv.FormatBool(c.IPv6) + "\n")
	if c.ControllerSocket != "" {
		fmt.Fprintf(b, "external-controller-unix: %q\n", c.ControllerSocket)
	}
	// Resolve the owning process only when a PROCESS-* rule exists.
	b.WriteString("find-process-mode: strict\n")
	b.WriteString("log-level: info\n")
	b.WriteString("dns:\n  enable: true\n  listen: 0.0.0.0:1053\n  enhanced-mode: fake-ip\n" +
		"  fake-ip-range: 198.18.0.1/16\n  nameserver:\n    - 223.5.5.5\n    - 119.29.29.29\n")
	fmt.Fprintf(b, "tun:\n  enable: %t\n  stack: system\n"+
		"  strict-route: false\n  dns-hijack:\n    - any:53\n", c.TUN)
}

// WriteBootstrapYAML writes the minimal config the core boots with: just
// the endpoints, TUN and a DIRECT catch-all. ClashX Meta starts this way and
// pushes the real config over the API once the controller answers, so
// provider and rule loading never delay the core coming up.
func (c Config) WriteBootstrapYAML(dir string) (string, error) {
	path := filepath.Join(dir, "config.yaml")
	var b strings.Builder
	c.writeBase(&b)
	b.WriteString("rules:\n  - MATCH,DIRECT\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// runtimeConfigPath picks and materialises the config mihomo loads: the
// user profile, or a full subscription config overlaid with the
// app-managed keys so its own groups and rules survive. Provider-style
// node lists and empty configs fall back to the generated config.
func runtimeConfigPath(dir string, cfg Config) (string, error) {
	src := cfg.ConfigPath
	if src == "" && cfg.SubscriptionURL != "" {
		if p := filepath.Join(dir, cfg.subFile()); fullProfile(p) {
			src = p
		}
	}
	if src != "" {
		if _, err := os.Stat(src); err == nil {
			return writeRunningConfig(src, filepath.Join(dir, "running.yaml"), cfg)
		}
	}
	return cfg.WriteYAML(dir)
}
