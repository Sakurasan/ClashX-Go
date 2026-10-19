package main

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// checkForUpdates asks the signed update feed (configured in mygo.json and
// published by `mygo build`) for a newer release, then installs it and
// relaunches. Builds without update configuration say so instead of
// silently failing.
func (a *app) checkForUpdates() {
	if !mygo.Updater.Enabled() {
		_, _ = mygo.Dialog.Message(mygo.MessageOptions{
			Type:    mygo.MessageInfo,
			Title:   "检查更新",
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
				Title:   "检查更新",
				Message: "已是最新版本",
				Detail:  appVersion,
			})
			return
		}
		detail := strings.TrimSpace(up.Notes)
		if detail == "" {
			detail = "发布于 " + up.Date.Local().Format("2006-01-02")
		}
		res, err := mygo.Dialog.Message(mygo.MessageOptions{
			Type:          mygo.MessageInfo,
			Title:         "发现新版本",
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
