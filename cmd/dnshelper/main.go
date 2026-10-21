// Command dnshelper points the system DNS servers of the Wi-Fi and Ethernet
// services at the address TUN mode wants, and puts the originals back. It is
// installed once as a root-owned setuid binary by the app (the user
// authorises it together with the TUN core), so toggling TUN never asks for a
// password again.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	version = "1"

	// backupPath keeps the originals in a root-only file: the app can stat
	// it to know whether DNS is currently hijacked, but cannot read it.
	backupPath   = "/Library/Application Support/clashx-go/dns-backup.json"
	networksetup = "/usr/sbin/networksetup"
)

// Variables rather than uses of the constants, so tests can drive apply and
// restore against a fake networksetup without touching the real DNS.
var (
	backupFile      = backupPath
	networksetupBin = networksetup
	flushCaches     = flushCache
)

type dnsBackup struct {
	DNS      string              `json:"dns"`      // address currently applied
	Services map[string][]string `json:"services"` // service -> original servers
}

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}
	switch os.Args[1] {
	case "version":
		fmt.Println(version)
	case "status":
		if _, err := os.Stat(backupFile); err == nil {
			fmt.Println("applied")
		} else {
			fmt.Println("clean")
		}
	case "apply":
		requireRoot()
		requireConsoleUser()
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: dns-helper apply <dns>")
			os.Exit(2)
		}
		if err := applyDNS(os.Args[2]); err != nil {
			fail(err)
		}
	case "restore":
		requireRoot()
		requireConsoleUser()
		if err := restoreDNS(); err != nil {
			fail(err)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: dns-helper apply <dns> | restore | status | version")
}

func requireRoot() {
	if os.Geteuid() != 0 {
		fail(errors.New("dns-helper must run as root"))
	}
}

// requireConsoleUser keeps the setuid helper from serving arbitrary local
// accounts: DNS changes are only accepted from the user logged in at the
// console, which is who runs the app.
func requireConsoleUser() {
	if os.Getuid() == 0 {
		return
	}
	out, err := exec.Command("/usr/bin/stat", "-f", "%Su", "/dev/console").Output()
	if err != nil {
		fail(fmt.Errorf("cannot identify the console user: %w", err))
	}
	u, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil {
		fail(err)
	}
	console := strings.TrimSpace(string(out))
	if u.Username != console {
		fail(fmt.Errorf("dns-helper is only available to the console user (%s)", console))
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// validDNS accepts an IPv4 or IPv6 address, never a host name: the value
// comes from a settings field and ends up in a root-run networksetup call.
func validDNS(s string) bool {
	return net.ParseIP(strings.TrimSpace(s)) != nil
}

var (
	serviceLineRE  = regexp.MustCompile(`^(?:\(\*\)\s*)?\(\d+\)\s+(.+)$`)
	hardwareLineRE = regexp.MustCompile(`^\(Hardware Port: ([^,]+), Device: .*\)$`)
)

// parseServiceOrder lists the enabled services whose hardware is Wi-Fi,
// AirPort or Ethernet — the same set ClashX Meta's helper touches. VPN,
// iPhone USB and Tailscale services keep their own DNS.
func parseServiceOrder(out string) []string {
	var services []string
	name := ""
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if m := serviceLineRE.FindStringSubmatch(line); m != nil {
			name = strings.TrimSpace(m[1])
			continue
		}
		if name == "" {
			continue
		}
		if m := hardwareLineRE.FindStringSubmatch(line); m != nil {
			switch port := strings.TrimSpace(m[1]); port {
			case "Wi-Fi", "AirPort", "Ethernet":
				services = append(services, name)
			}
			name = ""
		}
	}
	return services
}

// parseDNS reads networksetup -getdnsservers output, which says "There
// aren't any DNS Servers set on …" when the service inherits DHCP DNS.
func parseDNS(out string) []string {
	var servers []string
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.Contains(line, "There aren't any DNS Servers") {
			continue
		}
		servers = append(servers, line)
	}
	return servers
}

// shouldRestore only undoes a DNS value this helper set: a manual change made
// while TUN was on is left alone, as ClashX Meta's helper does.
func shouldRestore(current, original []string, applied string) bool {
	if slices.Equal(current, original) {
		return false
	}
	if applied == "" {
		return true
	}
	return slices.Equal(current, []string{applied})
}

func applyDNS(dns string) error {
	dns = strings.TrimSpace(dns)
	if !validDNS(dns) {
		return fmt.Errorf("invalid DNS address %q", dns)
	}
	services, err := targetServices()
	if err != nil {
		return err
	}
	backup := loadBackup()
	if backup == nil {
		backup = &dnsBackup{}
	}
	if backup.Services == nil {
		backup.Services = map[string][]string{}
	}
	var changed []string
	for _, service := range services {
		current, err := getDNS(service)
		if err != nil {
			return fmt.Errorf("read DNS of %s: %w", service, err)
		}
		if _, ok := backup.Services[service]; !ok {
			backup.Services[service] = current
		}
		if !slices.Equal(current, []string{dns}) {
			changed = append(changed, service)
		}
	}
	backup.DNS = dns
	// Persist the originals before touching anything: a crash halfway
	// through must still leave enough to restore.
	if err := saveBackup(backup); err != nil {
		return err
	}
	for _, service := range changed {
		if err := setDNS(service, []string{dns}); err != nil {
			return err
		}
	}
	if len(changed) > 0 {
		flushCaches()
	}
	fmt.Printf("applied %s to %d services\n", dns, len(services))
	return nil
}

func restoreDNS() error {
	backup := loadBackup()
	if backup == nil {
		fmt.Println("no saved DNS")
		return nil
	}
	var failed []string
	for service, original := range backup.Services {
		current, err := getDNS(service)
		if err != nil {
			// The service disappeared (USB adapter unplugged); nothing to
			// restore for it.
			continue
		}
		if !shouldRestore(current, original, backup.DNS) {
			continue
		}
		if err := setDNS(service, original); err != nil {
			failed = append(failed, err.Error())
		}
	}
	if len(failed) > 0 {
		return errors.New(strings.Join(failed, "; "))
	}
	if err := os.Remove(backupFile); err != nil && !os.IsNotExist(err) {
		return err
	}
	flushCaches()
	fmt.Printf("restored %d services\n", len(backup.Services))
	return nil
}

func targetServices() ([]string, error) {
	out, err := exec.Command(networksetupBin, "-listnetworkserviceorder").Output()
	if err != nil {
		return nil, fmt.Errorf("list network services: %w", err)
	}
	services := parseServiceOrder(string(out))
	if len(services) > 0 {
		return services, nil
	}
	// Fall back to every enabled service if the order output changed shape.
	out, err = exec.Command(networksetupBin, "-listallnetworkservices").Output()
	if err != nil {
		return nil, fmt.Errorf("list network services: %w", err)
	}
	for _, raw := range strings.Split(string(out), "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "*"))
		if line == "" || strings.HasPrefix(line, "An asterisk") {
			continue
		}
		services = append(services, line)
	}
	if len(services) == 0 {
		return nil, errors.New("no network services found")
	}
	return services, nil
}

func getDNS(service string) ([]string, error) {
	out, err := exec.Command(networksetupBin, "-getdnsservers", service).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return parseDNS(string(out)), nil
}

func setDNS(service string, servers []string) error {
	args := []string{"-setdnsservers", service}
	if len(servers) == 0 {
		args = append(args, "Empty")
	} else {
		args = append(args, servers...)
	}
	out, err := exec.Command(networksetupBin, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("set DNS of %s: %v: %s", service, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func flushCache() {
	_ = exec.Command("/usr/bin/dscacheutil", "-flushcache").Run()
	_ = exec.Command("/usr/bin/killall", "-HUP", "mDNSResponder").Run()
}

func loadBackup() *dnsBackup {
	data, err := os.ReadFile(backupFile)
	if err != nil {
		return nil
	}
	var backup dnsBackup
	if json.Unmarshal(data, &backup) != nil {
		return nil
	}
	if backup.Services == nil {
		backup.Services = map[string][]string{}
	}
	return &backup
}

func saveBackup(backup *dnsBackup) error {
	dir := filepath.Dir(backupFile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// The app runs as the user and only stats the backup, so the directory
	// must stay traversable while the file itself stays root-only.
	_ = os.Chmod(dir, 0o755)
	data, err := json.MarshalIndent(backup, "", "  ")
	if err != nil {
		return err
	}
	tmp := backupFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, backupFile)
}
