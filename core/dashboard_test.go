package core

import (
	"os"
	"path/filepath"
	"testing"
)

// The dashboard ships in the bundle (resources/dashboard) and is copied
// into the core's working directory on first launch so external-ui can
// serve it. Skips when the dashboard was not fetched for this checkout.
func TestEnsureDashboard(t *testing.T) {
	root := filepath.Dir(mustAbs(t, "."))
	if _, err := os.Stat(filepath.Join(root, "resources", "dashboard", "index.html")); err != nil {
		t.Skip("resources/dashboard not fetched")
	}
	mgr, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mgr.ensureDashboard()
	if _, err := os.Stat(filepath.Join(mgr.dir, "dashboard", "index.html")); err != nil {
		t.Fatalf("dashboard not copied: %v", err)
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
