package core

import (
	"fmt"
	"os"
	"path/filepath"
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
	// Secret is the external-controller API bearer secret.
	Secret string `json:"secret"`
	// Mode is the running mode: rule, global, or direct.
	Mode string `json:"mode"`
	// ConfigPath is the user's config/profile YAML to load, if any.
	ConfigPath string `json:"config-path"`
	// SubscriptionURL, when set, adds a proxy provider for the nodes. The
	// app downloads the subscription itself first (see
	// Manager.DownloadSubscription), so the generated config uses a file
	// provider pointing at the local copy; only when the download failed
	// or the body is not Clash YAML does it fall back to an http provider.
	SubscriptionURL string `json:"subscription-url"`
	// AllowLAN lets devices on the local network use the mixed port.
	AllowLAN bool `json:"allow-lan"`
	// TUN enables the core's TUN interface. Opening the device needs
	// administrator rights; without them the core exits at startup.
	TUN bool `json:"tun"`
}

// DefaultConfig returns the settings used on first launch.
func DefaultConfig() Config {
	return Config{
		MixedPort:      7890,
		ControllerPort: 9090,
		Mode:           "rule",
	}
}

// ConfigError reports a requested profile that does not exist.
type ConfigError struct{ Path string }

func (e *ConfigError) Error() string { return "config file not found: " + e.Path }

// providerBlock renders the proxy-providers block plus the policy groups
// and rules. When localFile is non-empty the provider is a file provider
// reading that file (the app-downloaded subscription); otherwise it is an
// http provider that mihomo fetches itself.
func providerBlock(localFile, url string) string {
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
	p += "\nrules:\n" +
		"  - GEOIP,CN,DIRECT\n" +
		"  - MATCH,PROXY\n"
	return p
}

// WriteYAML writes the runtime config.yaml for mihomo under dir, returning
// its path.
//
//   - When the user supplied a profile (ConfigPath), that file is used as
//     the config and endpoints are overridden on the CLI by Start.
//   - When a subscription URL is set and the app has already downloaded it
//     to dir/sub.yaml, a file provider loads the local copy (mihomo needs
//     no network for the nodes).
//   - Otherwise the subscription URL is used as an http provider, which
//     mihomo fetches itself.
//   - With neither, a minimal DIRECT config is generated.
func (c Config) WriteYAML(dir string) (string, error) {
	path := filepath.Join(dir, "config.yaml")
	var b strings.Builder
	fmt.Fprintf(&b, "mixed-port: %d\n", c.MixedPort)
	fmt.Fprintf(&b, "external-controller: 127.0.0.1:%d\n", c.ControllerPort)
	if c.Secret != "" {
		fmt.Fprintf(&b, "secret: %s\n", c.Secret)
	}
	fmt.Fprintf(&b, "allow-lan: %t\n", c.AllowLAN)
	fmt.Fprintf(&b, "mode: %s\n", c.Mode)
	b.WriteString("ipv6: false\n")
	b.WriteString("log-level: info\n")
	b.WriteString("dns:\n  enable: true\n  listen: 0.0.0.0:1053\n  enhanced-mode: fake-ip\n" +
		"  fake-ip-range: 198.18.0.1/16\n  nameserver:\n    - 223.5.5.5\n    - 119.29.29.29\n")
	if c.TUN {
		b.WriteString("tun:\n  enable: true\n  stack: system\n  device: utun-clash\n" +
			"  strict-route: false\n  dns-hijack:\n    - any:53\n")
	}

	switch {
	case c.ConfigPath != "":
		b.WriteString("rules:\n  - MATCH,DIRECT\n")
	case c.SubscriptionURL != "":
		local := ""
		if fi, err := os.Stat(filepath.Join(dir, SubscriptionFile)); err == nil && fi.Size() > 0 {
			local = SubscriptionFile
		}
		b.WriteString(providerBlock(local, c.SubscriptionURL))
	default:
		b.WriteString("rules:\n  - MATCH,DIRECT\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
