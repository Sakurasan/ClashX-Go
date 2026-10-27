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

// tunDNSWarnOnce keeps the "another resolver bypasses the tunnel" warning
// to once per run instead of every reload.
var tunDNSWarnOnce sync.Once

// tailscaleMagicDNS reports whether Tailscale's MagicDNS resolver is
// installed. Tailscale answers 100.100.100.100 over its own utun host
// route, so those queries never reach the Clash TUN's any:53 hijack:
// domains resolve outside Clash (and can be poisoned) before it sees them.
func tailscaleMagicDNS() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "scutil", "--dns").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "100.100.100.100")
}

// tailscaleCLI returns the tailscale command-line tool, trying PATH and the
// App Store app bundle.
func tailscaleCLI() string {
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}
	if fi, err := os.Stat("/Applications/Tailscale.app/Contents/MacOS/Tailscale"); err == nil && !fi.IsDir() {
		return "/Applications/Tailscale.app/Contents/MacOS/Tailscale"
	}
	return ""
}

// refreshTailscaleState re-checks whether Tailscale's MagicDNS is taking
// over system resolution; the tray menu surfaces it while TUN is on. The
// takeover only matters to TUN: the system proxy forwards domains to the
// node and never resolves locally.
func (a *app) refreshTailscaleState() {
	if runtime.GOOS != "darwin" || a.settings == nil || !a.settings.tun {
		a.tsMagicDNS = false
		return
	}
	a.tsMagicDNS = tailscaleMagicDNS()
}

// disableTailscaleDNS turns Tailscale's DNS takeover off so its resolver
// stops bypassing the TUN hijack. No administrator rights are needed: the
// CLI talks to the daemon as the user.
//
// Two wrinkles make a single call unreliable: the CLI can exit 0 while
// printing a failure ("The Tailscale GUI failed to start"), and Tailscale
// re-applies its DNS settings on network changes — including our own TUN
// coming up right after the click. So the set is retried and re-asserted
// for a short window, and success is judged by the resolver actually
// leaving the system DNS, not by the CLI's exit code.
func (a *app) disableTailscaleDNS() {
	cli := tailscaleCLI()
	if cli == "" {
		a.showToast(T("Tailscale"), T("未找到 tailscale 命令，请在 Tailscale 应用里关闭“使用 Tailscale DNS”。"))
		return
	}
	go func() {
		a.showToast(T("Tailscale"), T("正在关闭 Tailscale 的 DNS 接管…"))
		var last string
		for i := 0; i < 5; i++ {
			if i > 0 {
				time.Sleep(5 * time.Second)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			// The bundled binary picks GUI vs CLI mode from the
			// environment: a child of a Dock-launched app has no
			// TTY/TERM, so it takes the GUI-relaunch path and fails with
			// a misleading exit 0 (tailscale#16063). Force CLI mode.
			cmd := exec.CommandContext(ctx, cli, "set", "--accept-dns=false")
			cmd.Env = append(os.Environ(), "TAILSCALE_BE_CLI=1", "TERM=xterm-256color")
			out, err := cmd.CombinedOutput()
			cancel()
			last = strings.TrimSpace(string(out))
			if err != nil {
				last = fmt.Sprintf("%v: %s", err, last)
			}
			log.Printf("tailscale set --accept-dns=false: %s", last)
			// scutil picks the change up a moment later; poll briefly.
			for t := 0; t < 6; t++ {
				time.Sleep(500 * time.Millisecond)
				if !tailscaleMagicDNS() {
					a.tsMagicDNS = false
					log.Printf("Tailscale DNS 接管已关闭")
					a.showToast(T("Tailscale"), T("已关闭 Tailscale 的 DNS 接管，TUN 域名规则恢复。"))
					return
				}
			}
		}
		a.showToast(T("Tailscale"), fmt.Sprintf("%s: %s", T("Tailscale 的 DNS 接管没有关掉"), last))
	}()
}

// reconcileTunDNS makes the system DNS follow the current TUN state. It runs
// after a core start, a live reload, a TUN toggle and at launch, so a crash
// that left the DNS hijacked is repaired on the next run.
func (a *app) reconcileTunDNS() {
	if runtime.GOOS != "darwin" || a.settings == nil {
		return
	}
	if !a.settings.tun {
		a.restoreTunDNS()
		return
	}
	if strings.TrimSpace(a.settings.tunDNS) == "" {
		// Empty means "let the TUN hijack answer": no address to point the
		// system at, so undo any address applied by an earlier setting.
		a.restoreTunDNS()
	}
	if !a.ctrl.State().Running {
		// A start in progress reconciles through postStartConfig once the
		// core answers. Meanwhile a hijack left by a crash must not outlive
		// the tunnel: put the original DNS back until TUN is really up.
		a.restoreTunDNS()
		return
	}
	if tailscaleMagicDNS() {
		tunDNSWarnOnce.Do(func() {
			log.Printf("Tun DNS：检测到 Tailscale MagicDNS，域名解析会绕过 TUN 劫持")
			a.showToast(T("Tun DNS"), T("检测到 Tailscale 接管了系统 DNS：域名解析会绕过 Clash，TUN 的规则与 fake-ip 不会生效。可点击菜单栏的 Tailscale 项一键处理，或执行 tailscale set --accept-dns=false。"))
		})
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
