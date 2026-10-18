//go:build darwin

package main

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// setSystemProxy points the active network service's HTTP and HTTPS proxy at
// 127.0.0.1:port, or switches them off. networksetup edits the per-user
// network configuration, so it needs no administrator privileges.
func setSystemProxy(enable bool, port int) error {
	svc, err := activeNetworkService()
	if err != nil {
		return err
	}
	if !enable {
		var errs []string
		for _, arg := range []string{"-setwebproxystate", "-setsecurewebproxystate"} {
			if out, err := networksetup(arg, svc, "off"); err != nil {
				errs = append(errs, out)
			}
		}
		if len(errs) > 0 {
			return errors.New(strings.Join(errs, "; "))
		}
		return nil
	}
	where := []string{"127.0.0.1", fmt.Sprint(port)}
	for _, arg := range []string{"-setwebproxy", "-setsecurewebproxy"} {
		if out, err := networksetup(append([]string{arg, svc}, where...)...); err != nil {
			return errors.New(out)
		}
	}
	return nil
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
