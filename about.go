package main

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/png"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"golang.org/x/image/draw"

	"clashx-go/core"
)

// appVersion mirrors mygo.json.
const appVersion = "0.2.1"

//go:embed assets/icon.png
var appIconPNG []byte

var (
	aboutIconOnce sync.Once
	aboutIcon     *ui.Bitmap
)

// aboutView renders the ClashX-Meta-style About page: icon, versions, build
// stamp and project links.
func aboutView(c *ui.Context) {
	c.SetTheme(appleTheme(c.Theme().Dark))
	aboutIconOnce.Do(func() {
		// ClashX Meta's full-resolution app icon, downscaled with a good
		// filter to a 2x bitmap so the 96pt logo stays crisp on Retina
		// (the 32px menu-bar glyph made it look blurry).
		src, err := png.Decode(bytes.NewReader(appIconPNG))
		if err != nil {
			img := image.NewRGBA(image.Rect(0, 0, 192, 192))
			drawGlyph(img, img.Rect, color.NRGBA{R: 37, G: 99, B: 235, A: 255})
			aboutIcon = ui.NewBitmap(img)
			return
		}
		dst := image.NewNRGBA(image.Rect(0, 0, 256, 256))
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
		aboutIcon = ui.NewBitmap(dst)
	})
	t := c.Theme()
	rev, built := buildStamp()
	ui.Column(c).Fill().Padding(28, 36).Children(func() {
		ui.Column(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Image(c, aboutIcon).Width(128).Height(128)
			ui.Text(c, "ClashX Go").FontSize(24).FontWeight(600).Margin(14, 0, 0, 0)
			ui.Text(c, T("版本: ")+appVersion).FontSize(12).TextColor(t.TextMuted).Margin(4, 0, 0, 0)
			ui.Row(c).Margin(10, 0, 0, 0).Padding(3, 10).Radius(11).
				Background(t.Text.Alpha(0.06)).Children(func() {
				ui.Text(c, "Meta Core "+core.CoreVersion).FontSize(11).TextColor(t.TextMuted)
			})
			if rev != "" && built != "" {
				ui.Text(c, built).FontSize(10).TextColor(t.TextMuted).Margin(6, 0, 0, 0)
			}
			ui.Box(c).FillWidth().Height(1).Background(t.Border).Margin(20, 6, 18, 6)
			ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
				ui.Link(c, "mihomo", "https://github.com/MetaCubeX/mihomo").FontSize(12)
				ui.Text(c, "·").FontSize(11).TextColor(t.TextMuted)
				ui.Link(c, "ClashX Go", "https://github.com/Sakurasan/ClashX-go").FontSize(12)
				ui.Text(c, "·").FontSize(11).TextColor(t.TextMuted)
				ui.Link(c, "AGPL-3.0", "https://github.com/Sakurasan/ClashX-go/blob/main/LICENSE").FontSize(12)
			})
		})
		ui.Spacer(c)
		ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(5).Children(func() {
			ui.Text(c, "~/.config/clashx-go").FontSize(10).TextColor(t.TextMuted)
			settingsAction(c, T("打开配置文件夹"), func() {
				_ = mygo.Shell.OpenPath(coreDir())
			})
			ui.Text(c, "© "+strconv.Itoa(time.Now().Year())+" Sakurasan").
				FontSize(10).TextColor(t.TextMuted).Margin(4, 0, 0, 0)
		})
	})
}

// buildStamp returns the short VCS revision and build time embedded by the
// Go toolchain, empty when built outside a checkout.
func buildStamp() (rev, line string) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", ""
	}
	var when string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			when = s.Value
		}
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if when != "" {
		if t, err := time.Parse(time.RFC3339, when); err == nil {
			when = t.Format("2006-01-02 15:04")
		}
	}
	line = strings.TrimSpace(appVersion + " (" + rev + ") " + when)
	return rev, line
}

// showAbout opens (or re-shows) the About window.
func (a *app) showAbout() {
	if a.aboutWin == nil {
		a.aboutWin = mygo.NewWindow(mygo.WindowOptions{
			Title:             T("关于 ClashX Go"),
			Width:             400,
			Height:            460,
			UseContentSize:    true,
			Hidden:            true,
			DisableResize:     true,
			DisableMinimize:   true,
			DisableMaximize:   true,
			DisableFullScreen: true,
			Content:           ui.View(aboutView),
		})
		a.aboutWin.OnClose(func(e *mygo.CloseEvent) {
			if !a.quitting {
				e.PreventDefault()
				a.aboutWin.Hide()
			}
		})
	}
	a.aboutWin.Center()
	a.aboutWin.Show()
}
