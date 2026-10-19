package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"strings"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// The tray title cannot show two lines, so — like ClashX's StatusItemView —
// we compose the whole status item (paw icon + upload above download)
// into one template bitmap and set it as the tray icon.

// pawEllipse is one rotated ellipse of the paw-print mark in a 24x24 design
// space (the winning preview, batch 3 option 14).
type pawEllipse struct {
	cx, cy, rx, ry, rot float64
}

var (
	pawPad  = pawEllipse{12, 15.5, 5.5, 4.5, 0}
	pawToes = []pawEllipse{
		{5.2, 9.5, 2.1, 2.9, -18 * math.Pi / 180},
		{9.4, 5.8, 2.1, 3.0, -6 * math.Pi / 180},
		{14.6, 5.8, 2.1, 3.0, 6 * math.Pi / 180},
		{18.8, 9.5, 2.1, 2.9, 18 * math.Pi / 180},
	}
	// Opaque bounds of the mark in design space.
	pawX0, pawY0 = 3.0, 2.8
	pawW, pawH   = 18.0, 17.2
)

// drawPaw paints the paw mark tinted with ink, scaled to fit box and centred.
// Edges are supersampled 4x4 so the 2x bitmap stays smooth.
func drawPaw(dst *image.RGBA, box image.Rectangle, ink color.NRGBA) {
	scale := math.Min(float64(box.Dx())/pawW, float64(box.Dy())/pawH)
	w := max(1, int(pawW*scale+0.5))
	h := max(1, int(pawH*scale+0.5))
	mask := image.NewAlpha(image.Rect(0, 0, w, h))
	const ss = 4
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			cov := 0
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					px := pawX0 + (float64(x)+(float64(sx)+0.5)/ss)/scale
					py := pawY0 + (float64(y)+(float64(sy)+0.5)/ss)/scale
					if inPaw(px, py) {
						cov++
					}
				}
			}
			if cov > 0 {
				mask.SetAlpha(x, y, color.Alpha{A: uint8(cov * 255 / (ss * ss))})
			}
		}
	}
	x0 := box.Min.X + (box.Dx()-w)/2
	y0 := box.Min.Y + (box.Dy()-h)/2
	draw.DrawMask(dst, image.Rect(x0, y0, x0+w, y0+h),
		&image.Uniform{C: ink}, image.Point{}, mask, image.Point{}, draw.Over)
}

func inPaw(x, y float64) bool {
	if inEllipse(pawPad, x, y) {
		return true
	}
	for _, e := range pawToes {
		if inEllipse(e, x, y) {
			return true
		}
	}
	return false
}

func inEllipse(e pawEllipse, x, y float64) bool {
	dx, dy := x-e.cx, y-e.cy
	if e.rot != 0 {
		c, s := math.Cos(e.rot), math.Sin(e.rot)
		dx, dy = dx*c+dy*s, -dx*s+dy*c
	}
	dx, dy = dx/e.rx, dy/e.ry
	return dx*dx+dy*dy <= 1
}

var speedFace font.Face

func init() {
	// The real menu-bar font when available; basicfont keeps us working
	// if SFNS.ttf ever moves.
	if data, err := os.ReadFile("/System/Library/Fonts/SFNS.ttf"); err == nil {
		if f, err := opentype.Parse(data); err == nil {
			if face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 25, DPI: 72}); err == nil {
				speedFace = face
			}
		}
	}
}

func trayFont() font.Face {
	if speedFace != nil {
		return speedFace
	}
	return basicfont.Face7x13
}

// traySpeedIcon renders "↑up" above "↓down" with the arrows in one fixed
// column and the speeds left-aligned just after them. SFNS is proportional,
// so the %9s padding from humanSpeed is meaningless here — leading spaces
// are trimmed. The text column is reserved at the widest 3-digit readout
// ("88.8M/s"), so the paw and arrows never shift in the menu bar as the
// numbers change; only the digits move inside their column.
func traySpeedIcon(up, down string) []byte {
	f := trayFont()
	up, down = strings.TrimLeft(up, " "), strings.TrimLeft(down, " ")
	const h = 48 // full menu-bar height at 2x, no vertical margin
	ringW, gap, pad := 46, 2, 2
	arrowW := max(font.MeasureString(f, "↑").Ceil(), font.MeasureString(f, "↓").Ceil())
	textW := font.MeasureString(f, widestDigits("88.8M/s")).Ceil()
	width := ringW + gap + arrowW + 2 + textW + pad
	img := image.NewRGBA(image.Rect(0, 0, width, h))
	textX := ringW + gap + arrowW + 2
	black := color.NRGBA{A: 255}
	drawPaw(img, image.Rect(0, 0, ringW, h), black)
	ink := image.NewUniform(black)
	drawTrayText(img, f, ink, "↑", ringW+gap+arrowW, 20)
	drawTrayText(img, f, ink, "↓", ringW+gap+arrowW, 44)
	drawTrayTextLeft(img, f, ink, up, textX, 20)
	drawTrayTextLeft(img, f, ink, down, textX, 44)
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// widestDigits rewrites every digit as "8" (SFNS's widest numeral) so the
// measured width is the ceiling for any value of the same shape.
func widestDigits(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= '0' && c <= '9' {
			b[i] = '8'
		}
	}
	return string(b)
}

func drawTrayText(dst *image.RGBA, f font.Face, src *image.Uniform, s string, right, baseline int) {
	w := font.MeasureString(f, s).Ceil()
	drawTrayTextLeft(dst, f, src, s, right-w, baseline)
}

func drawTrayTextLeft(dst *image.RGBA, f font.Face, src *image.Uniform, s string, left, baseline int) {
	d := &font.Drawer{
		Dst:  dst,
		Src:  src,
		Face: f,
		Dot:  fixed.P(left, baseline),
	}
	d.DrawString(s)
}
