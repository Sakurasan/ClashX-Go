package main

import (
	"archive/zip"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"clashx-go/core"
)

// exportDiagnostics bundles the app/core logs and a redacted environment
// summary so a user can attach one file to a bug report.
func (a *app) exportDiagnostics() {
	dir := coreDir()
	outDir := filepath.Join(os.Getenv("HOME"), "Desktop")
	if fi, err := os.Stat(outDir); err != nil || !fi.IsDir() {
		outDir = dir
	}
	out := filepath.Join(outDir, "clashx-go-diagnostics-"+time.Now().Format("20060102-150405")+".zip")
	go func() {
		if err := writeDiagnostics(out, dir, a); err != nil {
			a.reportError("导出诊断", err.Error())
			return
		}
		mygo.Shell.ShowItemInFolder(out)
	}()
}

func writeDiagnostics(out, dir string, a *app) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	add := func(name string, data []byte) {
		if w, err := zw.Create(name); err == nil {
			_, _ = w.Write(data)
		}
	}
	add("summary.txt", []byte(diagSummary(a)))
	add("app.log", tailFile(filepath.Join(dir, "app.log"), 512<<10))
	add("core.log", tailFile(filepath.Join(dir, "core.log"), 1<<20))
	return zw.Close()
}

func diagSummary(a *app) string {
	var b strings.Builder
	s := a.settings
	st := a.ctrl.State()
	fmt.Fprintf(&b, "ClashX go %s (core %s)\n", appVersion, core.CoreVersion)
	fmt.Fprintf(&b, "time: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "os: %s/%s, macOS %s\n", runtime.GOOS, runtime.GOARCH, strings.TrimSpace(cmdOut("sw_vers", "-productVersion")))
	fmt.Fprintf(&b, "running: %t, mode: %s, groups: %d\n", st.Running, st.Mode, len(st.Groups))
	fmt.Fprintf(&b, "tun: %t, systemProxy: %t, allowLAN: %t, ipv6: %t\n",
		s.tun, s.systemProxy, s.allowLAN, s.ipv6)
	tunDNS := s.tunDNS
	if tunDNS == "" {
		tunDNS = "local"
	}
	fmt.Fprintf(&b, "tun DNS: %s (hijacked: %t)\n", tunDNS, dnsBackupExists())
	fmt.Fprintf(&b, "app routing rules: %d\n", len(s.appRules))
	fmt.Fprintf(&b, "ports: mixed %d, controller %d\n", a.mixedPort(), a.apiPort())
	fmt.Fprintf(&b, "secret: %s\n", map[bool]string{true: "set", false: "empty"}[s.secret != ""])
	fmt.Fprintf(&b, "profile: %q, config: %q, autoUpdate: %t\n", s.activeProfile, s.configPath, s.autoUpdate)
	for _, p := range s.profiles {
		fmt.Fprintf(&b, "subscription: %q -> %s (updated %s)\n",
			p.Name, maskURL(p.URL), p.UpdatedAt.Format("2006-01-02 15:04"))
	}
	privileged := "missing"
	if fi, err := os.Stat(core.PrivilegedCorePath()); err == nil {
		privileged = fi.Mode().String()
	}
	fmt.Fprintf(&b, "privileged core: %s\n", privileged)
	b.WriteString("\n== listeners\n")
	b.WriteString(cmdOut("lsof", "-nP", "-iTCP:7890", "-iTCP:9090", "-sTCP:LISTEN"))
	b.WriteString("\n\n== system proxy\n")
	b.WriteString(cmdOut("scutil", "--proxy"))
	b.WriteString("\n\n== recent TUN log\n")
	for _, line := range strings.Split(tailString(filepath.Join(coreDir(), "core.log"), 256<<10), "\n") {
		if strings.Contains(line, "TUN") {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

// maskURL keeps only the scheme and host: subscription paths often carry
// access tokens.
func maskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "(invalid url)"
	}
	return u.Scheme + "://" + u.Host + "/…"
}

func cmdOut(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil && len(out) == 0 {
		return "(" + err.Error() + ")"
	}
	return strings.TrimSpace(string(out))
}

func tailFile(path string, max int64) []byte {
	f, err := os.Open(path)
	if err != nil {
		return []byte("(missing: " + err.Error() + ")")
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	off := fi.Size() - max
	if off < 0 {
		off = 0
	}
	if _, err := f.Seek(off, 0); err != nil {
		return nil
	}
	data := make([]byte, fi.Size()-off)
	_, _ = f.Read(data)
	return data
}

func tailString(path string, max int64) string { return string(tailFile(path, max)) }
