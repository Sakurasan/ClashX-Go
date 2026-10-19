package main

import (
	"bytes"
	"embed"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// The tray title cannot show two lines, so — like ClashX's StatusItemView —
// we compose the whole status item (icon + upload above download)
// into one template bitmap and set it as the tray icon.

//go:embed assets/menu_icon@2x.png
var catAsset embed.FS

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

var (
	glyphOnce   sync.Once
	glyphImg    *image.NRGBA
	glyphBounds image.Rectangle
)

// loadGlyph picks the status mark: the user's menuImage.png override when
// present, otherwise ClashX Meta's own cat silhouette. Both are cropped to
// their opaque bounds so the glyph, not its canvas, fills the icon box.
func loadGlyph() {
	if data, err := os.ReadFile(filepath.Join(coreDir(), "menuImage.png")); err == nil {
		if img, box, ok := decodeCropped(data); ok {
			glyphImg, glyphBounds = img, box
			return
		}
	}
	data, err := catAsset.ReadFile("assets/menu_icon@2x.png")
	if err != nil {
		return
	}
	if img, box, ok := decodeCropped(data); ok {
		glyphImg, glyphBounds = img, box
	}
}

func decodeCropped(data []byte) (*image.NRGBA, image.Rectangle, bool) {
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, image.Rectangle{}, false
	}
	n := image.NewNRGBA(src.Bounds())
	draw.Draw(n, n.Bounds(), src, src.Bounds().Min, draw.Src)
	min := image.Point{X: 1 << 30, Y: 1 << 30}
	max := image.Point{}
	for y := n.Bounds().Min.Y; y < n.Bounds().Max.Y; y++ {
		for x := n.Bounds().Min.X; x < n.Bounds().Max.X; x++ {
			if n.NRGBAAt(x, y).A == 0 {
				continue
			}
			if x < min.X {
				min.X = x
			}
			if y < min.Y {
				min.Y = y
			}
			if x > max.X {
				max.X = x
			}
			if y > max.Y {
				max.Y = y
			}
		}
	}
	if min.X > max.X {
		return nil, image.Rectangle{}, false
	}
	return n, image.Rect(min.X, min.Y, max.X+1, max.Y+1), true
}

// drawGlyph paints the status mark (menuImage.png, the embedded cat, or —
// only if both fail to decode — the built-in paw), tinted with ink and
// fitted into box.
func drawGlyph(dst *image.RGBA, box image.Rectangle, ink color.NRGBA) {
	glyphOnce.Do(loadGlyph)
	if glyphImg == nil {
		drawPaw(dst, box, ink)
		return
	}
	scale := math.Min(float64(box.Dx())/float64(glyphBounds.Dx()),
		float64(box.Dy())/float64(glyphBounds.Dy()))
	w := max(1, int(float64(glyphBounds.Dx())*scale+0.5))
	h := max(1, int(float64(glyphBounds.Dy())*scale+0.5))
	tinted := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(tinted, tinted.Bounds(), glyphImg, glyphBounds, draw.Src, nil)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			_, _, _, a := tinted.At(x, y).RGBA()
			// Source coverage times the ink's opacity: the icon dims with
			// the rest of the status item when no proxy is active.
			alpha := uint8(uint32(a>>8) * uint32(ink.A) / 255)
			tinted.SetNRGBA(x, y, color.NRGBA{R: ink.R, G: ink.G, B: ink.B, A: alpha})
		}
	}
	x0 := box.Min.X + (box.Dx()-w)/2
	y0 := box.Min.Y + (box.Dy()-h)/2
	draw.Draw(dst, image.Rect(x0, y0, x0+w, y0+h), tinted, tinted.Bounds().Min, draw.Over)
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
// ("88.8M/s"), so the icon and arrows never shift in the menu bar as the
// numbers change; only the digits move inside their column. When neither
// system proxy nor TUN is active the whole item dims, matching ClashX Meta.
func traySpeedIcon(up, down string, active bool) []byte {
	f := trayFont()
	up, down = strings.TrimLeft(up, " "), strings.TrimLeft(down, " ")
	const h = 48 // full menu-bar height at 2x, no vertical margin
	ringW, gap, pad := 46, 2, 2
	arrowW := max(font.MeasureString(f, "↑").Ceil(), font.MeasureString(f, "↓").Ceil())
	textW := font.MeasureString(f, widestDigits("88.8M/s")).Ceil()
	width := ringW + gap + arrowW + 2 + textW + pad
	img := image.NewRGBA(image.Rect(0, 0, width, h))
	textX := ringW + gap + arrowW + 2
	alpha := uint8(255)
	if !active {
		// ClashX Meta dims the whole status item (icon and both speed
		// lines share one disabled colour) while no proxy is active.
		alpha = 110
	}
	drawGlyph(img, image.Rect(0, 0, ringW, h), color.NRGBA{A: alpha})
	ink := image.NewUniform(color.NRGBA{A: alpha})
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
