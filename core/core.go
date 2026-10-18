package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/egoist/mygo"
)

// CoreVersion is the mihomo release to download when the binary is missing.
const CoreVersion = "v1.19.32"

// binaryName returns the extracted executable name.
func binaryName() string {
	if runtime.GOOS == "windows" {
		return "mihomo.exe"
	}
	return "mihomo"
}

// Manager owns the mihomo process: its binary, its config directory and
// its lifecycle. All methods are safe for concurrent use.
type Manager struct {
	dir string // working directory (config.yaml + downloaded binary live here)

	mu       sync.Mutex
	cmd      *exec.Cmd
	exitCh   chan error // receives cmd.Wait() once the process is up
	elevated bool       // core runs as root via the TUN watcher script
	procPID  int
	running  bool
}

// NewManager returns a Manager rooted at dir, creating it if needed.
func NewManager(dir string) (*Manager, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Manager{dir: dir}, nil
}

// Dir returns the working directory.
func (m *Manager) Dir() string { return m.dir }

// Binary returns the path of the core executable.
func (m *Manager) Binary() string { return filepath.Join(m.dir, binaryName()) }

// EnsureBinary ensures the core executable for the host exists in the
// Manager's directory, copying it from the app's bundled resources (the
// core is downloaded at build time by scripts/fetch-cores.sh, never at
// runtime). It also copies the bundled GeoIP database when present, so
// first launch needs no network. It reports the core version.
func (m *Manager) EnsureBinary() (string, error) {
	bin := m.Binary()
	if _, err := os.Stat(bin); err == nil {
		return CoreVersion, nil
	}
	res := bundledBinary()
	if res == "" {
		return "", fmt.Errorf("no bundled mihomo for %s/%s in resources", runtime.GOOS, runtime.GOARCH)
	}
	if err := copyFile(res, bin); err != nil {
		return "", err
	}
	// GeoIP database: copy it when bundled (exists in resources/geoip.metadb
	// or resources/darwin-<arch>/geoip.metadb).
	for _, cand := range bundledGeoIPCandidates() {
		if err := copyFile(cand, filepath.Join(m.dir, filepath.Base(cand))); err != nil {
			// The database is optional — a subscription config without
			// GEOIP rules still works; only log and continue.
			continue
		}
		break
	}
	return CoreVersion, nil
}

// copyFile copies src to dest with 0755 permissions, atomically via a tmp
// file.
func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest+".tmp", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chmod(dest+".tmp", 0o755); err != nil {
		return err
	}
	return os.Rename(dest+".tmp", dest)
}

// bundledGeoIPCandidates returns possible bundled geoip.metadb paths.
func bundledGeoIPCandidates() []string {
	var out []string
	add := func(dir string) {
		if dir == "" {
			return
		}
		for _, sub := range []string{"geoip.metadb", "darwin-" + runtime.GOARCH + "/geoip.metadb", runtime.GOARCH + "/geoip.metadb"} {
			out = append(out, filepath.Join(dir, sub))
		}
	}
	if dir, err := mygo.App.Path(mygo.PathResources); err == nil {
		add(dir)
	}
	if wd, err := os.Getwd(); err == nil {
		for d := wd; ; d = filepath.Dir(d) {
			if filepath.Base(d) == "resources" {
				break
			}
			add(filepath.Join(d, "resources"))
			parent := filepath.Dir(d)
			if parent == d {
				break
			}
		}
	}
	return out
}

// bundledBinary returns the path of the core binary bundled in the app's
// resources for the running architecture, or "" when absent. The resources
// directory is Contents/Resources in a packaged app and the project's
// resources directory under `go run`/`go test`, with the platform
// subdirectories (darwin-arm64, darwin-amd64) merged in by mygo build for
// darwin/universal. Under `go test` run from a sub-package the working
// directory is the package dir, so we also walk up to the module root's
// resources dir.
func bundledBinary() string {
	candidates := []string{}
	if dir, err := mygo.App.Path(mygo.PathResources); err == nil && dir != "" {
		candidates = append(candidates, dir)
	}
	if wd, err := os.Getwd(); err == nil {
		for d := wd; ; d = filepath.Dir(d) {
			if filepath.Base(d) == "resources" {
				break
			}
			candidates = append(candidates, filepath.Join(d, "resources"))
			parent := filepath.Dir(d)
			if parent == d {
				break
			}
		}
	}
	for _, dir := range candidates {
		// mygo build flattens the platform directory (resources/darwin-<arch>)
		// into Contents/Resources for single-platform builds, and keeps the
		// platform subdirectories for darwin/universal. Try every layout.
		for _, sub := range []string{"darwin-" + runtime.GOARCH, runtime.GOARCH, ""} {
			p := filepath.Join(dir, sub, "mihomo")
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// Start launches mihomo with the given config, waiting until its external
// controller answers or the process exits. When the user supplied a
// profile (ConfigPath), it is used as the config file and our controller
// endpoint and secret are overridden via -ext-ctl/-secret; otherwise a
// runtime config.yaml is generated. When a subscription is configured, the
// app downloads it first (via ensureSubscription) so the core reads a
// local file provider rather than reaching the host itself — the config
// falls back to an http provider only if the download fails.
func (m *Manager) Start(ctx context.Context, cfg Config, ping func() error) (string, error) {
	// Download the subscription before taking the lock: no shared state.
	if cfg.ConfigPath == "" && cfg.SubscriptionURL != "" {
		_ = m.ensureSubscription(ctx, cfg)
	}

	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return "", fmt.Errorf("core is already running")
	}
	m.killStale()
	// The controller port is ours to bind (we pass -ext-ctl). If another
	// Clash core already owns it, mihomo would start half-dead and our
	// ping would answer from the foreign core: fail early instead.
	if cfg.ControllerPort > 0 && !portAvailable(cfg.ControllerPort) {
		m.mu.Unlock()
		return "", fmt.Errorf("端口 %d 已被占用（可能另有 Clash 核心在运行），请在偏好设置里换一个控制器端口", cfg.ControllerPort)
	}
	bin := m.Binary()
	if _, err := os.Stat(bin); err != nil {
		m.mu.Unlock()
		return "", fmt.Errorf("core binary missing: %w", err)
	}
	configPath := cfg.ConfigPath
	if configPath != "" {
		if _, err := os.Stat(configPath); err != nil {
			configPath = ""
		} else if overlay, err := writeRunningConfig(configPath, filepath.Join(m.dir, "running.yaml"), cfg); err == nil {
			configPath = overlay
		} else {
			m.mu.Unlock()
			return "", err
		}
	}
	if configPath == "" {
		var err error
		configPath, err = cfg.WriteYAML(m.dir)
		if err != nil {
			m.mu.Unlock()
			return "", err
		}
	}
	args := []string{"-d", m.dir, "-f", configPath}
	if cfg.ControllerPort > 0 {
		args = append(args, "-ext-ctl", fmt.Sprintf("127.0.0.1:%d", cfg.ControllerPort))
	}
	if cfg.Secret != "" {
		args = append(args, "-secret", cfg.Secret)
	}
	exited := make(chan error, 1)
	var cmd *exec.Cmd
	var procPID int
	logPath := filepath.Join(m.dir, "core.log")
	var logOffset int64
	if fi, err := os.Stat(logPath); err == nil {
		logOffset = fi.Size()
	}
	elevated := cfg.TUN && runtime.GOOS == "darwin" && os.Geteuid() != 0
	if elevated {
		// TUN needs root to open a utun device: authorise once via the
		// system dialog and let a detached watcher run the core as root.
		// Later stops go through the stop flag without prompting again.
		pid, err := m.startElevated(bin, args)
		if err != nil {
			m.mu.Unlock()
			return "", err
		}
		procPID = pid
		go m.watchElevated(pid, exited)
	} else {
		cmd = exec.Command(bin, args...)
		cmd.Dir = m.dir
		logf, err := os.OpenFile(filepath.Join(m.dir, "core.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			m.mu.Unlock()
			return "", err
		}
		cmd.Stdout = logf
		cmd.Stderr = logf
		if err := cmd.Start(); err != nil {
			m.mu.Unlock()
			return "", fmt.Errorf("start mihomo: %w", err)
		}
		procPID = cmd.Process.Pid
		go func() { exited <- cmd.Wait() }()
	}
	m.cmd = cmd
	m.elevated = elevated
	m.procPID = procPID
	m.running = true
	m.exitCh = exited
	m.mu.Unlock()
	_ = os.WriteFile(m.pidPath(), []byte(strconv.Itoa(procPID)), 0o644)

	// Watch for process exit so a core that dies during startup fails fast
	// with the log tail instead of timing out.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if ping() == nil {
			if cfg.TUN {
				// mihomo logs a TUN failure but keeps running without it:
				// treat that as a failed start so the switch rolls back.
				if err := checkTUNReady(logPath, logOffset); err != nil {
					m.Stop()
					return "", err
				}
			}
			return configPath, nil
		}
		select {
		case <-ctx.Done():
			m.Stop()
			return "", ctx.Err()
		case <-exited:
			// Leave the buffered error for Stop to consume.
			m.Stop()
			return "", fmt.Errorf("核心进程提前退出%s", coreLogTail(filepath.Join(m.dir, "core.log")))
		case <-time.After(200 * time.Millisecond):
		}
	}
	m.Stop()
	return "", fmt.Errorf("core did not answer on the controller within 15s")
}

// stopFlag asks the root watcher to terminate the core without another
// administrator prompt.
func (m *Manager) stopFlag() string { return filepath.Join(m.dir, "core.stop") }

// startElevated launches the core as root through one administrator
// authorisation (osascript). The detached watcher keeps the process,
// records its pid and obeys the stop flag. Returns the core pid.
func (m *Manager) startElevated(bin string, args []string) (int, error) {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = strconv.Quote(a)
	}
	logPath := strconv.Quote(filepath.Join(m.dir, "core.log"))
	pidPath := strconv.Quote(m.pidPath())
	stopPath := strconv.Quote(m.stopFlag())
	inner := fmt.Sprintf(
		"%s %s >> %s 2>&1 & CPID=$!; echo $CPID > %s; "+
			"while kill -0 $CPID 2>/dev/null; do "+
			"if [ -f %s ]; then kill $CPID; rm -f %s; break; fi; "+
			"sleep 0.5; done",
		strconv.Quote(bin), strings.Join(quoted, " "), logPath, pidPath, stopPath, stopPath)
	// The watcher body lives in its own file so the launcher below needs no
	// nested quoting. Single quotes in paths would break the sh -c layers.
	if strings.ContainsAny(inner, "'\n") {
		return 0, fmt.Errorf("配置路径含非法字符，无法授权启动 TUN")
	}
	watchPath := filepath.Join(m.dir, "tun_watch.sh")
	watch := "#!/bin/sh\n" + inner + "\n"
	if err := os.WriteFile(watchPath, []byte(watch), 0700); err != nil {
		return 0, err
	}
	// `do shell script` cleans up leftover children when it returns, which
	// killed the detached watcher. Hand the process to the user's launchd
	// session via `launchctl asuser` (still root, but outside the
	// authorized command's process group).
	script := "#!/bin/sh\nlaunchctl asuser " + strconv.Itoa(os.Getuid()) +
		" /bin/sh -c 'nohup /bin/sh " + strconv.Quote(watchPath) + " >/dev/null 2>&1 &'\nexit 0\n"
	scriptPath := filepath.Join(m.dir, "tun.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0700); err != nil {
		return 0, err
	}
	_ = os.Remove(m.stopFlag())
	// Single quotes keep the shell happy with spaces in the path, and keep
	// the AppleScript string literal free of embedded double quotes.
	if strings.ContainsAny(scriptPath, "'\n") {
		return 0, fmt.Errorf("配置目录名含非法字符，无法授权启动 TUN")
	}
	out, err := exec.Command("osascript", "-e",
		fmt.Sprintf(`do shell script "/bin/sh '%s' " with administrator privileges with prompt "ClashX Go 需要管理员权限开启 TUN 模式（创建 utun 设备）。授权后关闭 TUN 不再重复询问。"`,
			scriptPath)).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "-128") {
			return 0, fmt.Errorf("已取消管理员授权")
		}
		return 0, fmt.Errorf("管理员授权失败: %s", msg)
	}
	for range 40 {
		if data, err := os.ReadFile(m.pidPath()); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
				return pid, nil
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return 0, fmt.Errorf("核心未能以管理员权限启动")
}

// watchElevated reports when the root-owned core process disappears.
func (m *Manager) watchElevated(pid int, exited chan<- error) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		exited <- err
		return
	}
	for {
		if err := proc.Signal(syscall.Signal(0)); err != nil && !errors.Is(err, syscall.EPERM) {
			// EPERM means alive but root-owned; anything else is gone.
			exited <- fmt.Errorf("core exited")
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// checkTUNReady waits briefly after the core answers and rejects a start
// whose log tail reports the TUN device failing to come up.
func checkTUNReady(logPath string, offset int64) error {
	time.Sleep(2 * time.Second)
	f, err := os.Open(logPath)
	if err != nil {
		return nil
	}
	defer f.Close()
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil
		}
	}
	tail, _ := io.ReadAll(io.LimitReader(f, 64<<10))
	if i := bytes.Index(tail, []byte("Start TUN listening error")); i >= 0 {
		line := tail[i:]
		if j := bytes.IndexByte(line, '\n'); j > 0 {
			line = line[:j]
		}
		return fmt.Errorf("TUN 设备创建失败：%s", strings.TrimSpace(string(line)))
	}
	return nil
}

// coreLogTail quotes the last lines of the core log for startup errors.
func coreLogTail(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return ""
	}
	const max = 400
	if len(data) > max {
		data = data[len(data)-max:]
		if i := strings.IndexByte(string(data), '\n'); i >= 0 && i < len(data)-1 {
			data = data[i+1:]
		}
	}
	return "\n" + strings.TrimSpace(string(data))
}

// Stop terminates the mihomo process, waiting for it to exit.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.running {
		return
	}
	// Send SIGTERM first, then SIGKILL after a grace period. The startup
	// watcher goroutine delivers the real exit to exitCh; Stop must not
	// call cmd.Wait itself. An elevated core cannot be signalled from here
	// — the root watcher obeys the stop flag instead, without a second
	// administrator prompt.
	exit := m.exitCh
	if m.elevated {
		_ = os.WriteFile(m.stopFlag(), nil, 0o644)
	} else if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Signal(os.Interrupt)
	}
	if exit != nil {
		select {
		case <-exit:
		case <-time.After(5 * time.Second):
			if m.cmd != nil && m.cmd.Process != nil {
				_ = m.cmd.Process.Kill()
				<-exit
			}
		}
	}
	m.running = false
	m.cmd = nil
	m.elevated = false
	m.exitCh = nil
	os.Remove(m.pidPath())
}

// pidPath is where the core's process id is stored so a later run can clean
// up a core orphaned by a killed app (SIGKILL skips OnWillQuit).
func (m *Manager) pidPath() string { return filepath.Join(m.dir, "core.pid") }

// portAvailable reports whether a TCP port can be bound on localhost.
func portAvailable(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// killStale terminates a core left over from a previous run.
func (m *Manager) killStale() {
	data, err := os.ReadFile(m.pidPath())
	os.Remove(m.pidPath())
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil || proc.Signal(syscall.Signal(0)) != nil {
		return
	}
	_ = proc.Signal(os.Interrupt)
	for range 25 {
		time.Sleep(200 * time.Millisecond)
		if proc.Signal(syscall.Signal(0)) != nil {
			return
		}
	}
	_ = proc.Kill()
}

// Running reports whether the process is currently up.
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// PID returns the process id, or 0.
func (m *Manager) PID() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.procPID
}

// Version returns the pinned core version.
func (m *Manager) Version() string { return CoreVersion }

// SubscriptionMaxAge is how long a downloaded subscription is reused
// before Start fetches a fresh copy.
const SubscriptionMaxAge = time.Hour

// ensureSubscription re-downloads the subscription when the stored copy is
// missing or older than SubscriptionMaxAge. A failed download is
// non-fatal: the config then falls back to an http provider that mihomo
// fetches itself.
func (m *Manager) ensureSubscription(ctx context.Context, cfg Config) error {
	p := m.SubscriptionPath()
	if fi, err := os.Stat(p); err == nil && time.Since(fi.ModTime()) < SubscriptionMaxAge {
		return nil
	}
	// If the core is already up, route the download through its mixed port
	// so the subscription fetch itself is proxied.
	proxy := ""
	if m.Running() {
		proxy = fmt.Sprintf("127.0.0.1:%d", cfg.MixedPort)
	}
	n, err := m.DownloadSubscription(ctx, cfg.SubscriptionURL, proxy)
	if err != nil {
		// Log for diagnosis; keep the old copy.
		fmt.Fprintf(os.Stderr, "subscription download failed: %v\n", err)
		return err
	}
	fmt.Fprintf(os.Stderr, "subscription: downloaded %d proxies\n", n)
	return nil
}

// RefreshSubscription forces a re-download regardless of the stored copy's
// age, routing through the running core's mixed port when it is up so the
// fetch itself is proxied. It returns the new proxy count.
func (m *Manager) RefreshSubscription(ctx context.Context, cfg Config) (int, error) {
	proxy := ""
	if m.Running() {
		proxy = fmt.Sprintf("127.0.0.1:%d", cfg.MixedPort)
	}
	return m.DownloadSubscription(ctx, cfg.SubscriptionURL, proxy)
}

// LogPath returns where the core writes its stdout/stderr.
func (m *Manager) LogPath() string { return filepath.Join(m.dir, "core.log") }

// ErrNotRunning is returned when an API call needs a live core.
var ErrNotRunning = fmt.Errorf("core is not running")

// ParseSubscription extracts server URIs (vmess://, vless://, trojan://,
// ss://, ssr://, socks5://, hysteria2://, tuic://) from a subscription
// text, one per line. It is used to offer a quick "use this subscription
// URL" flow; the profile itself is downloaded by the core via proxy
// providers, which is out of scope for this first version.
func ParseSubscription(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"vmess://", "vless://", "trojan://", "ss://", "ssr://", "socks5://", "hysteria2://", "tuic://"} {
			if strings.HasPrefix(line, prefix) {
				out = append(out, line)
				break
			}
		}
	}
	return out
}
