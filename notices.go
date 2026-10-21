package main

import (
	"time"

	"github.com/egoist/mygo"
)

// subscriptionNoticeLoop warns once a day when the subscription's quota is
// nearly used up or it expires within three days.
func (a *app) subscriptionNoticeLoop() {
	var lastQuota, lastExpiry time.Time
	for {
		time.Sleep(30 * time.Minute)
		if a.quitting || a.settings == nil || a.settings.noNotify {
			continue
		}
		info := a.ctrl.State().SubInfo
		if info == nil || info.Total <= 0 {
			continue
		}
		now := time.Now()
		if used := info.Upload + info.Download; info.Total-used < info.Total/20 &&
			now.Sub(lastQuota) > 24*time.Hour {
			lastQuota = now
			a.notify("clashx-go-quota", "订阅流量即将用尽",
				"剩余 "+humanBytes(info.Total-used)+"（共 "+humanBytes(info.Total)+"）")
		}
		if !info.Expire.IsZero() && info.Expire.Sub(now) < 72*time.Hour &&
			now.Sub(lastExpiry) > 24*time.Hour {
			lastExpiry = now
			a.notify("clashx-go-expiry", "订阅即将到期",
				"到期时间 "+info.Expire.Local().Format("2006-01-02"))
		}
	}
}

func (a *app) notify(id, title, body string) {
	n := mygo.NewNotification(mygo.NotificationOptions{ID: id, Title: title, Body: body})
	_ = n.Show()
}
