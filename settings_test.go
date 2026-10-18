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
	s.subscriptionURL = "https://example.com/sub?token=xyz"
	s.configPath = "/tmp/my.yaml"
	s.startAtLogin = true
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
	if got.subscriptionURL != "https://example.com/sub?token=xyz" {
		t.Errorf("subscriptionURL = %q, want the persisted URL", got.subscriptionURL)
	}
	if got.configPath != "/tmp/my.yaml" {
		t.Errorf("configPath = %q, want /tmp/my.yaml", got.configPath)
	}
	if !got.startAtLogin {
		t.Errorf("startAtLogin = false, want true")
	}
	// The on-disk file must contain the subscription URL in plain JSON.
	data, err := os.ReadFile(filepath.Join(dir, "app.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("subscriptionURL")) {
		t.Errorf("app.json missing subscriptionURL key:\n%s", data)
	}
}
