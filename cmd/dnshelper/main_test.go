package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseServiceOrder(t *testing.T) {
	out := `An asterisk (*) denotes that a network service is disabled.
(1) Ethernet
(Hardware Port: Ethernet, Device: en0)

(2) Wi-Fi
(Hardware Port: Wi-Fi, Device: en1)

(3) iPhone USB
(Hardware Port: iPhone USB, Device: en2)

(*) (4) USB LAN
(Hardware Port: Ethernet, Device: en5)

(5) Tailscale
(Hardware Port: io.tailscale.ipn.macos, Device: )
`
	got := parseServiceOrder(out)
	want := []string{"Ethernet", "Wi-Fi", "USB LAN"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseServiceOrder = %v, want %v", got, want)
	}
}

func TestParseDNS(t *testing.T) {
	cases := []struct {
		out  string
		want []string
	}{
		{"119.29.29.29\n1.2.4.8\n", []string{"119.29.29.29", "1.2.4.8"}},
		{"There aren't any DNS Servers set on Wi-Fi.\n", nil},
		{"", nil},
	}
	for _, tc := range cases {
		if got := parseDNS(tc.out); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parseDNS(%q) = %v, want %v", tc.out, got, tc.want)
		}
	}
}

func TestValidDNS(t *testing.T) {
	for _, dns := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111", "2001:db8::1"} {
		if !validDNS(dns) {
			t.Errorf("validDNS(%q) = false", dns)
		}
	}
	for _, dns := range []string{"", "example.com", "8.8.8.8; rm -rf /", "999.1.1.1"} {
		if validDNS(dns) {
			t.Errorf("validDNS(%q) = true", dns)
		}
	}
}

// TestApplyRestore drives the whole helper against a fake networksetup: the
// originals are saved once, a changed address keeps them, and restore puts
// both services back (including the one that had no explicit DNS).
func TestApplyRestore(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "networksetup")
	body := `#!/bin/sh
state="$DNSHELPER_TEST_STATE"
case "$1" in
-listnetworkserviceorder)
  printf '(1) Ethernet\n(Hardware Port: Ethernet, Device: en0)\n\n'
  printf '(2) Wi-Fi\n(Hardware Port: Wi-Fi, Device: en1)\n\n'
  ;;
-getdnsservers)
  f="$state/dns_$2"
  if [ -s "$f" ]; then cat "$f"; else echo "There aren't any DNS Servers set on $2."; fi
  ;;
-setdnsservers)
  svc="$2"; shift 2
  f="$state/dns_$svc"
  if [ "$1" = "Empty" ]; then : > "$f"; else printf '%s\n' "$@" > "$f"; fi
  ;;
*)
  exit 64
  ;;
esac
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DNSHELPER_TEST_STATE", state)
	oldNet, oldBackup, oldFlush := networksetupBin, backupFile, flushCaches
	defer func() { networksetupBin, backupFile, flushCaches = oldNet, oldBackup, oldFlush }()
	networksetupBin = script
	backupFile = filepath.Join(dir, "backup.json")
	flushes := 0
	flushCaches = func() { flushes++ }

	if err := os.WriteFile(filepath.Join(state, "dns_Ethernet"), []byte("192.168.1.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readState := func(service string) []string {
		data, err := os.ReadFile(filepath.Join(state, "dns_"+service))
		if err != nil {
			return nil
		}
		return parseDNS(string(data))
	}

	if err := applyDNS("8.8.8.8"); err != nil {
		t.Fatal(err)
	}
	if got := readState("Ethernet"); !reflect.DeepEqual(got, []string{"8.8.8.8"}) {
		t.Errorf("Ethernet DNS = %v, want 8.8.8.8", got)
	}
	if got := readState("Wi-Fi"); !reflect.DeepEqual(got, []string{"8.8.8.8"}) {
		t.Errorf("Wi-Fi DNS = %v, want 8.8.8.8", got)
	}
	backup := loadBackup()
	if backup == nil || backup.DNS != "8.8.8.8" {
		t.Fatalf("backup = %+v", backup)
	}
	if got := backup.Services["Ethernet"]; !reflect.DeepEqual(got, []string{"192.168.1.1"}) {
		t.Errorf("saved Ethernet DNS = %v", got)
	}
	if got := backup.Services["Wi-Fi"]; len(got) != 0 {
		t.Errorf("saved Wi-Fi DNS = %v, want empty", got)
	}
	if flushes != 1 {
		t.Errorf("flushes = %d, want 1", flushes)
	}

	// Changing the address while TUN is on keeps the original values.
	if err := applyDNS("1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	backup = loadBackup()
	if backup == nil || backup.DNS != "1.1.1.1" ||
		!reflect.DeepEqual(backup.Services["Ethernet"], []string{"192.168.1.1"}) {
		t.Fatalf("backup after change = %+v", backup)
	}

	if err := restoreDNS(); err != nil {
		t.Fatal(err)
	}
	if got := readState("Ethernet"); !reflect.DeepEqual(got, []string{"192.168.1.1"}) {
		t.Errorf("restored Ethernet DNS = %v", got)
	}
	if got := readState("Wi-Fi"); len(got) != 0 {
		t.Errorf("restored Wi-Fi DNS = %v, want empty", got)
	}
	if _, err := os.Stat(backupFile); !os.IsNotExist(err) {
		t.Errorf("backup still exists after restore")
	}
}

// A value the user set by hand while TUN was on must survive the restore;
// only the helper's own value is reverted.
func TestShouldRestore(t *testing.T) {
	original := []string{"192.168.1.1"}
	cases := []struct {
		name     string
		current  []string
		original []string
		applied  string
		want     bool
	}{
		{"still hijacked", []string{"8.8.8.8"}, original, "8.8.8.8", true},
		{"already restored", original, original, "8.8.8.8", false},
		{"user changed it", []string{"1.1.1.1"}, original, "8.8.8.8", false},
		{"no recorded address", []string{"8.8.8.8"}, original, "", true},
	}
	for _, tc := range cases {
		if got := shouldRestore(tc.current, tc.original, tc.applied); got != tc.want {
			t.Errorf("%s: shouldRestore = %t, want %t", tc.name, got, tc.want)
		}
	}
}
