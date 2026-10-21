package main

import "github.com/egoist/mygo/ui"

// appleTheme keeps MyGo's system-tuned neutrals and typography but replaces
// the indigo component palette with macOS system colors: system blue,
// system red and 6pt control corners. The settings, dialogs and About page
// all use it so they look like one native app.
func appleTheme(dark bool) *ui.Theme {
	t := ui.LightTheme()
	if dark {
		t = ui.DarkTheme()
	}
	t.Radius = 6
	if dark {
		t.Accent = ui.Hex("#0A84FF")
		t.AccentHover = ui.Hex("#409CFF")
		t.AccentPressed = ui.Hex("#0060DF")
		t.AccentText = ui.Hex("#FFFFFF")
		t.Danger = ui.Hex("#FF453A")
		t.Selection = ui.RGBA(10, 132, 255, 0.30)
		t.Focus = ui.RGBA(10, 132, 255, 0.55)
		return t
	}
	t.Accent = ui.Hex("#007AFF")
	t.AccentHover = ui.Hex("#0A84FF")
	t.AccentPressed = ui.Hex("#0062CC")
	t.AccentText = ui.Hex("#FFFFFF")
	t.Danger = ui.Hex("#FF3B30")
	t.Selection = ui.RGBA(0, 122, 255, 0.22)
	t.Focus = ui.RGBA(0, 122, 255, 0.45)
	return t
}

type nativeButtonKind int

const (
	buttonSecondary nativeButtonKind = iota
	buttonPrimary
	buttonDestructive
)

// nativeButton draws a push button in the macOS style: 13pt label, 6pt
// corners, a compact 24pt height, system blue for the default action and a
// quiet gray face with a hairline for secondary and destructive actions.
func nativeButton(c *ui.Context, label string, kind nativeButtonKind, fn func()) {
	t := c.Theme()
	b := ui.ButtonBase(c).Label(label).Padding(2, 12).Radius(6)
	// A secondary button sits on the window or on a group box: the brighter
	// hover face and a light shadow keep its edge visible on both.
	face, hover, pressed := t.SurfaceHover, t.SurfacePressed, t.SurfacePressed
	fg, border := t.Text, t.Border
	switch kind {
	case buttonPrimary:
		face, hover, pressed = t.Accent, t.AccentHover, t.AccentPressed
		fg, border = t.AccentText, ui.Color{}
	case buttonDestructive:
		fg = t.Danger
	}
	b.TextColor(fg)
	if border.A > 0 {
		b.Border(1, border)
		b.Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.10))
	}
	b.Draw(func(pp *ui.Painter, r ui.Rect) {
		bg := face
		switch {
		case b.Pressed():
			bg = pressed
		case b.Hovered():
			bg = hover
		}
		pp.Fill(r, bg, 6)
	})
	b.Children(func() {
		text := ui.Text(c, label).FontSize(13).SingleLine()
		if kind == buttonPrimary {
			text.FontWeight(500)
		}
	})
	if b.Clicked() && fn != nil {
		fn()
	}
}
