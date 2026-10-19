package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"clashx-go/core"
)

// profile returns a managed subscription by name, or nil.
func (s *settings) profile(name string) *subscriptionProfile {
	for i := range s.profiles {
		if s.profiles[i].Name == name {
			return &s.profiles[i]
		}
	}
	return nil
}

// activeSub returns the selected managed subscription, or nil when a local
// config file drives the core.
func (a *app) activeSub() *subscriptionProfile {
	if a.settings.activeProfile == "" {
		return nil
	}
	return a.settings.profile(a.settings.activeProfile)
}

// profileNameFromURL derives a ClashX-style display name: the URL fragment
// if present, else the host without a leading "www.".
func profileNameFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "sub"
	}
	if u.Fragment != "" {
		if name, err := url.QueryUnescape(u.Fragment); err == nil && name != "" {
			return name
		}
	}
	return strings.TrimPrefix(u.Hostname(), "www.")
}

// sanitizeFileName keeps profile names usable as file names.
func sanitizeFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '_', r == '-', r == '.', r > 0x7F:
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	s := b.String()
	if s == "" {
		s = "sub"
	}
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// uniqueFile picks sub-<name>.yaml, disambiguating against existing profiles.
func (a *app) uniqueFile(name string) string {
	base := sanitizeFileName(name)
	file := "sub-" + base + ".yaml"
	for i := 2; a.settings.profileByFile(file) != nil; i++ {
		file = fmt.Sprintf("sub-%s-%d.yaml", base, i)
	}
	return file
}

func (s *settings) profileByFile(file string) *subscriptionProfile {
	for i := range s.profiles {
		if s.profiles[i].File == file {
			return &s.profiles[i]
		}
	}
	return nil
}

// addProfile downloads url as a new managed subscription, makes it active
// and restarts the core (ClashX: 添加 → 立即使用).
func (a *app) addProfile(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return fmt.Errorf("请输入 http(s) 订阅链接")
	}
	for _, p := range a.settings.profiles {
		if p.URL == rawURL {
			a.selectProfile(p.Name)
			return nil
		}
	}
	name := profileNameFromURL(rawURL)
	mgr := a.ctrl.Manager()
	// Land the download in a scratch file first: the provider's title header
	// may rename the profile, and the final name must not clash.
	const incoming = "sub-incoming.yaml"
	_, title, err := mgr.DownloadSubscription(context.Background(), incoming, rawURL, "")
	if err != nil {
		return err
	}
	if title != "" {
		name = title
	}
	file := a.uniqueFile(name)
	if err := mgr.RenameSubscriptionFile(incoming, file); err != nil {
		return err
	}
	a.settings.profiles = append(a.settings.profiles, subscriptionProfile{
		Name: name, URL: rawURL, File: file, UpdatedAt: time.Now(),
	})
	a.settings.activeProfile = name
	a.settings.configPath = ""
	saveSettings(coreDir(), a.settings)
	a.menu.rebuild()
	go func() {
		if err := a.applyConfig(a.settingsConfig()); err != nil {
			log.Printf("添加订阅待重试: %v", err)
		}
	}()
	return nil
}

// selectProfile switches the active managed subscription, downloading a
// fresh copy when the stored one is stale, then restarting the core.
func (a *app) selectProfile(name string) {
	st := a.settings
	if st.activeProfile == name && st.configPath == "" {
		return
	}
	p := st.profile(name)
	if p == nil {
		return
	}
	st.activeProfile = name
	st.configPath = ""
	saveSettings(coreDir(), st)
	a.menu.rebuild()
	go func() {
		mgr := a.ctrl.Manager()
		if mgr.SubscriptionStale(p.File, core.SubscriptionMaxAge) {
			if _, _, err := mgr.DownloadSubscription(context.Background(), p.File, p.URL, ""); err == nil {
				p.UpdatedAt = time.Now()
				saveSettings(coreDir(), st)
			}
		}
		if err := a.applyConfig(a.settingsConfig()); err != nil {
			log.Printf("切换配置待重试: %v", err)
		}
	}()
}

// selectLocalConfig activates a local config file profile (the "config"
// entry in ClashX's list).
func (a *app) selectLocalConfig(path string) {
	if a.settings.configPath == path && a.settings.activeProfile == "" {
		return
	}
	a.settings.configPath = path
	a.settings.activeProfile = ""
	saveSettings(coreDir(), a.settings)
	a.menu.rebuild()
	go func() {
		if err := a.applyConfig(a.settingsConfig()); err != nil {
			log.Printf("本地配置待重试: %v", err)
		}
	}()
}

// removeProfile deletes a managed subscription and its files.
func (a *app) removeProfile(name string) {
	st := a.settings
	idx := -1
	for i, p := range st.profiles {
		if p.Name == name {
			idx = i
		}
	}
	if idx < 0 {
		return
	}
	mgr := a.ctrl.Manager()
	mgr.RemoveSubscriptionFiles(st.profiles[idx].File)
	wasActive := st.activeProfile == name
	st.profiles = append(st.profiles[:idx], st.profiles[idx+1:]...)
	if wasActive {
		if len(st.profiles) > 0 {
			st.activeProfile = st.profiles[0].Name
			st.configPath = ""
		} else {
			st.activeProfile = ""
		}
	}
	saveSettings(coreDir(), st)
	a.menu.rebuild()
	if wasActive {
		go func() {
			if err := a.applyConfig(a.settingsConfig()); err != nil {
				a.reportError("删除订阅", err.Error())
			}
		}()
	}
}

// updateProfile re-downloads one managed subscription.
func (a *app) updateProfile(name string) (int, error) {
	p := a.settings.profile(name)
	if p == nil {
		return 0, fmt.Errorf("订阅 %s 不存在", name)
	}
	mgr := a.ctrl.Manager()
	proxy := ""
	if mgr.Running() {
		proxy = fmt.Sprintf("127.0.0.1:%d", a.mixedPort())
	}
	n, _, err := mgr.DownloadSubscription(context.Background(), p.File, p.URL, proxy)
	if err != nil {
		return 0, err
	}
	p.UpdatedAt = time.Now()
	saveSettings(coreDir(), a.settings)
	if name == a.settings.activeProfile && mgr.Running() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = a.ctrl.Client().ReloadProvider(ctx, "provider")
		a.win.Update(func() {})
	}
	return n, nil
}

// autoUpdateLoop refreshes the active subscription on the configured
// interval while auto-update is on (ClashX 自动更新).
func (a *app) autoUpdateLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		st := a.settings
		p := a.activeSub()
		if !st.autoUpdate || p == nil {
			continue
		}
		age := time.Duration(st.updateInterval) * time.Minute
		if a.ctrl.Manager().SubscriptionStale(p.File, age) {
			if _, err := a.updateProfile(p.Name); err != nil {
				log.Printf("auto update %s: %v", p.Name, err)
			} else {
				a.menu.rebuild()
			}
		}
	}
}
