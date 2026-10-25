package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestSettingsRoundTrip verifies settings persist and reload correctly.
// The in-memory fields are intentionally unexported (the view mutates them
// in place), so persistence must go through the exported tagged mirror;
// a regression here silently drops the subscription URL and all other
// settings on restart.
func TestSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := loadSettings(dir)
	s.mixedPort = "8899"
	s.controller = "19100"
	s.secret = "abc"
	s.profiles = []subscriptionProfile{{Name: "example", URL: "https://example.com/sub?token=xyz", File: "sub-example.yaml"}}
	s.activeProfile = "example"
	s.configPath = "/tmp/my.yaml"
	s.startAtLogin = true
	s.tunDNS = "8.8.8.8"
	s.lang = "en"
	s.mode = "global"
	s.appRules = []appRoutingRule{{Name: "Telegram", Process: "Telegram", Path: "/Applications/Telegram.app", Target: "proxy"}}
	saveSettings(dir, s)

	got := loadSettings(dir)
	if got.mixedPort != "8899" {
		t.Errorf("mixedPort = %q, want 8899", got.mixedPort)
	}
	if got.controller != "19100" {
		t.Errorf("controller = %q, want 19100", got.controller)
	}
	if got.secret != "abc" {
		t.Errorf("secret = %q, want abc", got.secret)
	}
	if len(got.profiles) != 1 || got.profiles[0].URL != "https://example.com/sub?token=xyz" || got.activeProfile != "example" {
		t.Errorf("profiles = %+v active=%q, want the persisted profile", got.profiles, got.activeProfile)
	}
	if got.configPath != "/tmp/my.yaml" {
		t.Errorf("configPath = %q, want /tmp/my.yaml", got.configPath)
	}
	if !got.startAtLogin {
		t.Errorf("startAtLogin = false, want true")
	}
	if got.tunDNS != "8.8.8.8" {
		t.Errorf("tunDNS = %q, want 8.8.8.8", got.tunDNS)
	}
	if got.lang != "en" {
		t.Errorf("lang = %q, want en", got.lang)
	}
	if got.mode != "global" {
		t.Errorf("mode = %q, want global", got.mode)
	}
	if len(got.appRules) != 1 || got.appRules[0].Process != "Telegram" ||
		got.appRules[0].Path != "/Applications/Telegram.app" || got.appRules[0].Target != "proxy" {
		t.Errorf("appRules = %+v, want the persisted Telegram rule", got.appRules)
	}
	// The on-disk file must contain the subscription URL in plain JSON.
	data, err := os.ReadFile(filepath.Join(dir, "app.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("profiles")) || !bytes.Contains(data, []byte("activeProfile")) {
		t.Errorf("app.json missing profiles keys:\n%s", data)
	}
}

// TestSettingsMigrateLegacyURL checks the old single-subscription file
// becomes a managed profile, and a local config path keeps priority.
func TestSettingsMigrateLegacyURL(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"mixedPort":"7890","controller":"9090","subscriptionURL":"http://pan.example.xyz/sub.yaml#next","configPath":"/tmp/local.yaml"}`
	if err := os.WriteFile(filepath.Join(dir, "app.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	s := loadSettings(dir)
	if len(s.profiles) != 1 || s.profiles[0].Name != "next" || s.profiles[0].File != "sub.yaml" {
		t.Fatalf("profiles = %+v", s.profiles)
	}
	if s.activeProfile != "" {
		t.Errorf("local config should keep priority, active=%q", s.activeProfile)
	}
	s.configPath = ""
	saveSettings(dir, s)
	if got := loadSettings(dir); len(got.profiles) != 1 {
		t.Errorf("profiles lost on round trip: %+v", got.profiles)
	}
}
