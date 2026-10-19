//go:build darwin

package main

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// proxySnapshot is one protocol's (HTTP or HTTPS) user proxy config.
type proxySnapshot struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host"`
	Port    string `json:"port"`
}

// systemProxyBackup captures everything setSystemProxy overwrites, so
// disabling ours restores what was there before — ClashX's restoreProxy
// behaviour, which preserves a pre-existing corporate proxy.
type systemProxyBackup struct {
	Service string        `json:"service"`
	Web     proxySnapshot `json:"web"`
	Secure  proxySnapshot `json:"secure"`
	Bypass  []string      `json:"bypass,omitempty"`
}

// proxyBypassDomains keeps local and Apple time-sync traffic off the proxy.
var proxyBypassDomains = []string{
	"127.*", "192.168.*", "10.*", "localhost", "*.local", "timestamp.apple.com",
}

// snapshotProxy reads the active service's current HTTP/HTTPS proxy config.
func snapshotProxy() (*systemProxyBackup, error) {
	svc, err := activeNetworkService()
	if err != nil {
		return nil, err
	}
	b := &systemProxyBackup{Service: svc}
	if b.Web, err = getProxySetting("-getwebproxy", svc); err != nil {
		return nil, err
	}
	if b.Secure, err = getProxySetting("-getsecurewebproxy", svc); err != nil {
		return nil, err
	}
	b.Bypass = getBypassDomains(svc)
	return b, nil
}

// pointProxyAt routes the active service's HTTP/HTTPS proxy at the core.
func pointProxyAt(port int) error {
	svc, err := activeNetworkService()
	if err != nil {
		return err
	}
	where := []string{"127.0.0.1", strconv.Itoa(port)}
	for _, arg := range []string{"-setwebproxy", "-setsecurewebproxy"} {
		if out, err := networksetup(append([]string{arg, svc}, where...)...); err != nil {
			return errors.New(out)
		}
	}
	if out, err := networksetup(append([]string{"-setproxybypassdomains", svc}, proxyBypassDomains...)...); err != nil {
		return errors.New(out)
	}
	return nil
}

// restoreProxy puts back a captured config; a nil backup just switches our
// own settings off (the common case: the user had no proxy before us).
func restoreProxy(b *systemProxyBackup) error {
	if b == nil {
		b = &systemProxyBackup{}
	}
	svc := b.Service
	if svc == "" {
		var err error
		if svc, err = activeNetworkService(); err != nil {
			return err
		}
	}
	var errs []string
	set := func(flag, stateFlag string, snap proxySnapshot) {
		if snap.Enabled && snap.Host != "" && snap.Port != "" {
			if out, err := networksetup(flag, svc, snap.Host, snap.Port); err != nil {
				errs = append(errs, out)
				return
			}
			state := "on"
			if out, err := networksetup(stateFlag, svc, state); err != nil {
				errs = append(errs, out)
			}
			return
		}
		if out, err := networksetup(stateFlag, svc, "off"); err != nil {
			errs = append(errs, out)
		}
	}
	set("-setwebproxy", "-setwebproxystate", b.Web)
	set("-setsecurewebproxy", "-setsecurewebproxystate", b.Secure)
	if len(b.Bypass) > 0 {
		if out, err := networksetup(append([]string{"-setproxybypassdomains", svc}, b.Bypass...)...); err != nil {
			errs = append(errs, out)
		}
	} else {
		networksetup("-setproxybypassdomains", svc) // empty list clears
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func getProxySetting(flag, svc string) (proxySnapshot, error) {
	out, err := networksetup(flag, svc)
	if err != nil {
		return proxySnapshot{}, errors.New(out)
	}
	var s proxySnapshot
	for _, line := range strings.Split(out, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(key) {
		case "Enabled":
			s.Enabled = val == "Yes"
		case "Server":
			s.Host = val
		case "Port":
			s.Port = val
		}
	}
	return s, nil
}

func getBypassDomains(svc string) []string {
	out, _ := networksetup("-getproxybypassdomains", svc)
	var list []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "There are no") {
			continue
		}
		list = append(list, line)
	}
	return list
}

// activeNetworkService returns the networksetup service name ("Wi-Fi",
// "Thunderbolt Ethernet", …) owning the default route.
func activeNetworkService() (string, error) {
	out, err := exec.Command("route", "-n", "get", "default").Output()
	if err != nil {
		return "", fmt.Errorf("no default route: %w", err)
	}
	device := ""
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "interface:"); ok {
			device = strings.TrimSpace(rest)
			break
		}
	}
	if device == "" {
		return "", errors.New("default route has no interface")
	}
	hw, err := exec.Command("networksetup", "-listallhardwareports").Output()
	if err != nil {
		return "", err
	}
	// Blocks of "Hardware Port: <name>" / "Device: <iface>".
	service := ""
	port := ""
	for _, line := range strings.Split(string(hw), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "Hardware Port:"); ok {
			port = strings.TrimSpace(rest)
		} else if rest, ok := strings.CutPrefix(line, "Device:"); ok {
			if strings.TrimSpace(rest) == device && port != "" {
				service = port
			}
		}
	}
	if service == "" {
		return "", fmt.Errorf("no networksetup service for interface %s", device)
	}
	return service, nil
}

func networksetup(args ...string) (string, error) {
	out, err := exec.Command("networksetup", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
