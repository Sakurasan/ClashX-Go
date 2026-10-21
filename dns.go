package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/egoist/mygo"
)

// tunDNSMu serialises apply/restore: a TUN toggle, a live reload and the
// start-up reconcile can otherwise race on the same networksetup calls.
var tunDNSMu sync.Mutex

// The Tun DNS helper is a tiny standalone binary bundled in Resources. On
// first use the app installs it as a root-owned setuid copy, the same
// one-time authorisation model as the privileged core, and afterwards it
// points the system DNS at the configured address while TUN is on.
const (
	dnsHelperName = "clashx-go-dns-helper"
	dnsHelperDir  = "/Library/Application Support/clashx-go"
)

func dnsHelperPath() string {
	return filepath.Join(dnsHelperDir, dnsHelperName)
}

func dnsBackupPath() string {
	return filepath.Join(dnsHelperDir, "dns-backup.json")
}

// dnsHelperReady reports whether dest is the installed root-owned setuid
// helper; a user-owned copy would silently fail to change DNS.
func dnsHelperReady(dest string) bool {
	fi, err := os.Stat(dest)
	if err != nil {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 {
		return false
	}
	return fi.Mode()&os.ModeSetuid != 0
}

// dnsBackupExists says whether the helper currently holds the original DNS,
// i.e. whether a restore is needed. The file itself is root-only.
func dnsBackupExists() bool {
	_, err := os.Stat(dnsBackupPath())
	return err == nil
}

// ensureDNSHelper installs the bundled helper on first use. The
// administrator prompt appears once per installation, not on every TUN
// toggle.
func (a *app) ensureDNSHelper() (string, error) {
	dest := dnsHelperPath()
	if dnsHelperReady(dest) {
		return dest, nil
	}
	res, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		return "", err
	}
	src := filepath.Join(res, "dns-helper")
	if fi, err := os.Stat(src); err != nil || fi.IsDir() {
		return "", fmt.Errorf("%s", T("安装包缺少 Tun DNS 组件（dns-helper）"))
	}
	inner := fmt.Sprintf("mkdir -p %s && chmod 755 %s && cp %s %s && { xattr -d com.apple.quarantine %s 2>/dev/null || true; } && chown root:wheel %s && chmod 4755 %s",
		quote(dnsHelperDir), quote(dnsHelperDir), quote(src), quote(dest), quote(dest), quote(dest), quote(dest))
	scriptPath := filepath.Join(coreDir(), "dns_install.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n"+inner+"\n"), 0o700); err != nil {
		return "", err
	}
	out, err := exec.Command("osascript", "-e", fmt.Sprintf(
		`do shell script "/bin/sh '%s'" with administrator privileges with prompt "%s"`,
		scriptPath,
		T("ClashX Go 正在安装 Tun DNS 提权组件（仅需一次，之后切换 TUN 不再询问密码）。"))).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "-128") {
			return "", fmt.Errorf("%s", T("已取消管理员授权"))
		}
		return "", fmt.Errorf("%s%s", T("Tun DNS 安装失败: "), msg)
	}
	if !dnsHelperReady(dest) {
		return "", fmt.Errorf("%s", T("Tun DNS 组件安装校验失败"))
	}
	log.Printf("Tun DNS 组件已安装: %s", dest)
	return dest, nil
}

// applyTunDNS points the system DNS at the configured address. Failures are
// reported but never roll back TUN: the tunnel still hijacks DNS on its own.
func (a *app) applyTunDNS() {
	if runtime.GOOS != "darwin" || a.settings == nil || a.quitting {
		return
	}
	dns := strings.TrimSpace(a.settings.tunDNS)
	if dns == "" {
		return
	}
	tunDNSMu.Lock()
	defer tunDNSMu.Unlock()
	if net.ParseIP(dns) == nil {
		a.showToast(T("Tun DNS"), T("请输入有效的 DNS 地址"))
		return
	}
	helper, err := a.ensureDNSHelper()
	if err != nil {
		log.Printf("Tun DNS 组件不可用: %v", err)
		a.showToast(T("Tun DNS"), err.Error())
		return
	}
	if a.quitting {
		return
	}
	if err := runDNSHelper(helper, "apply", dns); err != nil {
		log.Printf("Tun DNS 应用失败: %v", err)
		a.showToast(T("Tun DNS"), err.Error())
	}
}

// restoreTunDNS puts the originals back if the helper hijacked them. It is
// safe to call at any time: without a backup it does nothing and never
// prompts for authorisation.
func (a *app) restoreTunDNS() {
	if runtime.GOOS != "darwin" || !dnsBackupExists() {
		return
	}
	tunDNSMu.Lock()
	defer tunDNSMu.Unlock()
	if !dnsBackupExists() {
		return // another caller restored it while we waited
	}
	helper := dnsHelperPath()
	if !dnsHelperReady(helper) {
		return
	}
	if err := runDNSHelper(helper, "restore", ""); err != nil {
		log.Printf("Tun DNS 还原失败: %v", err)
	}
}

// reconcileTunDNS makes the system DNS follow the current TUN state. It runs
// after a core start, a live reload, a TUN toggle and at launch, so a crash
// that left the DNS hijacked is repaired on the next run.
func (a *app) reconcileTunDNS() {
	if runtime.GOOS != "darwin" || a.settings == nil {
		return
	}
	if !a.settings.tun || strings.TrimSpace(a.settings.tunDNS) == "" {
		a.restoreTunDNS()
		return
	}
	if !a.ctrl.State().Running {
		// A start in progress reconciles through postStartConfig once the
		// core answers. Meanwhile a hijack left by a crash must not outlive
		// the tunnel: put the original DNS back until TUN is really up.
		a.restoreTunDNS()
		return
	}
	a.applyTunDNS()
}

// restoreTunDNSOnQuit bounds the wait: an apply waiting on an administrator
// prompt must not freeze the quit.
func (a *app) restoreTunDNSOnQuit() {
	done := make(chan struct{})
	go func() {
		a.restoreTunDNS()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		log.Printf("退出：Tun DNS 还原超时，继续退出")
	}
}

func runDNSHelper(helper, action, dns string) error {
	args := []string{action}
	if dns != "" {
		args = append(args, dns)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, helper, args...).CombinedOutput()
	msg := strings.TrimSpace(string(out))
	if err != nil {
		return fmt.Errorf("%v: %s", err, msg)
	}
	if msg != "" {
		log.Printf("Tun DNS %s: %s", action, msg)
	}
	return nil
}

// quote single-quotes a path for the shell install script, the same way
// core's privileged installer does.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
