package main

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/png"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/smallnest/uix/components/button"
	"golang.org/x/image/draw"

	"clashx-go/core"
)

// appVersion mirrors mygo.json.
const appVersion = "0.1.0"

//go:embed assets/icon.png
var appIconPNG []byte

var (
	aboutIconOnce sync.Once
	aboutIcon     *ui.Bitmap
)

// aboutView renders the ClashX-Meta-style About page: icon, versions, build
// stamp and project links.
func aboutView(c *ui.Context) {
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
		dst := image.NewNRGBA(image.Rect(0, 0, 192, 192))
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
		aboutIcon = ui.NewBitmap(dst)
	})
	_, built := buildStamp()
	ui.Column(c).FillWidth().Padding(24).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Image(c, aboutIcon).Width(96).Height(96)
		ui.Text(c, "ClashX Go").FontSize(20).Bold()
		ui.Text(c, "版本: "+appVersion).FontSize(13)
		ui.Text(c, "Meta Core: "+core.CoreVersion).FontSize(13)
		if built != "" {
			ui.Text(c, built).FontSize(11).TextColor(c.Theme().TextMuted)
		}
		ui.Divider(c)
		ui.Link(c, "GitHub for Clash Meta: https://github.com/MetaCubeX/mihomo",
			"https://github.com/MetaCubeX/mihomo")
		ui.Link(c, "GitHub for ClashX Go: https://github.com/Sakurasan/ClashX-go",
			"https://github.com/Sakurasan/ClashX-go")
		ui.Row(c).FillWidth().Justify(ui.Center).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "配置目录: ~/.config/clashx-go").FontSize(12)
			button.Button(c, button.Props{
				Label:   "打开",
				Variant: button.Outline,
				OnClick: func() {
					_ = mygo.Shell.OpenPath(coreDir())
				},
			})
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
			Title:             "关于 ClashX Go",
			Width:             420,
			Height:            420,
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
