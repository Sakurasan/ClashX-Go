package main

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// checkForUpdates asks the update feed of the chosen channel (stable, or
// nightly when the Nightly channel is set) for a newer release, then
// installs it and relaunches. Builds without update configuration say so
// instead of silently failing.
func (a *app) checkForUpdates() {
	if !mygo.Updater.Enabled() {
		_, _ = mygo.Dialog.Message(mygo.MessageOptions{
			Type:    mygo.MessageInfo,
			Title:   T("检查更新"),
			Message: "当前构建未启用更新检查。",
			Detail: "需要在 mygo.json 配置 updates（publicKey + github 仓库），" +
				"并用 mygo build 打包出 .app 运行。",
		})
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		up, err := mygo.Updater.Check(ctx)
		cancel()
		if err != nil {
			a.reportError("检查更新", err.Error())
			return
		}
		if up == nil {
			_, _ = mygo.Dialog.Message(mygo.MessageOptions{
				Type:    mygo.MessageInfo,
				Title:   T("检查更新"),
				Message: "已是最新版本",
				Detail:  a.updateChannelNote(),
			})
			return
		}
		detail := strings.TrimSpace(up.Notes)
		if detail == "" {
			detail = "发布于 " + up.Date.Local().Format("2006-01-02")
		}
		res, err := mygo.Dialog.Message(mygo.MessageOptions{
			Type:          mygo.MessageInfo,
			Title:         T("发现新版本"),
			Message:       up.Version,
			Detail:        detail,
			Buttons:       []string{"下载并安装", "取消"},
			DefaultButton: 0,
		})
		if err != nil || res.Button != 0 {
			return
		}
		installCtx, cancelInstall := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancelInstall()
		if err := up.Install(installCtx, nil); err != nil {
			a.reportError("安装更新", err.Error())
			return
		}
		mygo.App.Relaunch()
	}()
}

const notificationUpdateID = "clashx-go-update"

// displayVersion is the version to show in the UI: the packaged app's
// version, falling back to the constant mirror of mygo.json in builds
// without bundle info (development builds).
func displayVersion() string {
	if v := mygo.App.Version(); v != "" {
		return v
	}
	return appVersion
}

// updateChannelNote is the detail line of the "up to date" dialog: the
// running version and the channel the check ran against.
func (a *app) updateChannelNote() string {
	channel := T("稳定版")
	if a.settings != nil && a.settings.updateChannel == "nightly" {
		channel = "Nightly"
	}
	return T("当前版本 ") + displayVersion() + " · " + T("更新渠道: ") + channel
}

// applyUpdateChannel points the updater at the channel saved in settings.
// mygo turns prerelease checking off at launch, so this runs at startup
// and again whenever the user switches channels.
func applyUpdateChannel(s *settings) {
	mygo.Updater.SetPrereleases(s != nil && s.updateChannel == "nightly")
}

// setUpdateChannel switches the update channel (设置页), persists it and
// re-arms the updater. Anything but "nightly" is the stable channel.
func (a *app) setUpdateChannel(channel string) {
	if channel != "nightly" {
		channel = "stable"
	}
	a.settings.updateChannel = channel
	applyUpdateChannel(a.settings)
	saveSettings(coreDir(), a.settings)
	if channel == "nightly" {
		a.showToast(T("已切换到 Nightly 渠道"), T("下次检查更新将包含 Nightly 版本。"))
	}
}

// autoUpdateCheckLoop checks the signed feed once after launch when the user
// enabled automatic checks, and posts a notification instead of a modal.
func (a *app) autoUpdateCheckLoop() {
	time.Sleep(20 * time.Second)
	if a.quitting || a.settings == nil || !a.settings.autoCheckUpd || !mygo.Updater.Enabled() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	up, err := mygo.Updater.Check(ctx)
	cancel()
	if err != nil || up == nil {
		return
	}
	n := mygo.NewNotification(mygo.NotificationOptions{
		ID:    notificationUpdateID,
		Title: T("ClashX Go 有新版本"),
		Body:  up.Version + " 可用，点击检查更新",
		Image: appIconPNG,
	})
	_ = n.Show()
}
