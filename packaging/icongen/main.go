// Command icongen draws Gaze's application icon.
//
// Pure standard library: no ImageMagick, no font rasteriser, no dependencies.
// Shapes are evaluated analytically with 3x3 supersampling per pixel, which
// gives clean antialiased edges when the 512px master is scaled down.
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const (
	size = 512 // master icon size
	ss   = 3   // supersample factor per axis
)

type rounded struct {
	x0, y0, x1, y1, r float64
	c                 color.RGBA
}

// covers reports whether (x, y) is inside the rounded rectangle.
func (r rounded) covers(x, y float64) bool {
	dx := math.Max(r.x0+r.r-x, math.Max(0, x-(r.x1-r.r)))
	dy := math.Max(r.y0+r.r-y, math.Max(0, y-(r.y1-r.r)))
	return dx*dx+dy*dy <= r.r*r.r
}

func lerp(a, b, t float64) float64 { return a + (b-a)*t }

// gradient colour across the canvas, top-left to bottom-right.
func gradientColor(x, y, w, h float64) color.RGBA {
	t := ((x/w + y/h) / 2)
	r := lerp(0x43, 0x7E, t)
	g := lerp(0x38, 0x22, t)
	b := lerp(0xCA, 0xCE, t)
	return color.RGBA{uint8(r), uint8(g), uint8(b), 0xFF}
}

func main() {
	bg := rounded{0, 0, size, size, 112, color.RGBA{}}
	caret := rounded{99, 154, 123, 204, 12, color.RGBA{0xFF, 0xC5, 0x3D, 0xFF}}
	lines := []rounded{
		{141, 162, 413, 196, 17, color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}},
		{141, 241, 381, 275, 17, color.RGBA{0xFF, 0xFF, 0xFF, 0xE6}},
		{141, 320, 325, 354, 17, color.RGBA{0xFF, 0xFF, 0xFF, 0xE6}},
	}

	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	inv := 1.0 / (ss * ss)

	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var rr, gg, bb, aa float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := float64(px) + (float64(sx)+0.5)/ss
					y := float64(py) + (float64(sy)+0.5)/ss

					var c color.RGBA
					if bg.covers(x, y) {
						c = gradientColor(x, y, size, size)
					}
					for _, l := range append([]rounded{caret}, lines...) {
						if l.covers(x, y) {
							c = l.c
							break
						}
					}
					// premultiplied accumulation keeps edges clean on transparency
					f := float64(c.A) / 255
					rr += float64(c.R) * f
					gg += float64(c.G) * f
					bb += float64(c.B) * f
					aa += f
				}
			}
			if aa == 0 {
				continue
			}
			img.SetNRGBA(px, py, color.NRGBA{
				R: uint8(math.Round(rr * inv / (aa * inv) * 1)),
				G: uint8(math.Round(gg * inv / (aa * inv) * 1)),
				B: uint8(math.Round(bb * inv / (aa * inv) * 1)),
				A: uint8(math.Round(aa * inv * 255)),
			})
		}
	}

	write("Icon.png", img)
	write("icon-preview.png", preview(img))
	fmt.Println("wrote Icon.png (512x512) and icon-preview.png")
}

func write(path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}

// preview renders the icon at several sizes on light and dark backgrounds so
// small-size legibility can be judged at a glance.
func preview(src *image.NRGBA) *image.NRGBA {
	sizes := []int{16, 24, 32, 48, 64, 128}
	const pad = 24
	light := color.NRGBA{0xF5, 0xF5, 0xF7, 0xFF}
	dark := color.NRGBA{0x1E, 0x1E, 0x24, 0xFF}

	w := pad
	for _, s := range sizes {
		w += s + pad
	}
	rowH := 128 + pad
	out := image.NewNRGBA(image.Rect(0, 0, w, rowH*2+pad+256+pad))

	fill(out, image.Rect(0, 0, w, rowH*2+pad+256+pad), light)

	for i, s := range sizes {
		x := pad
		for j := 0; j < i; j++ {
			x += sizes[j] + pad
		}
		scaled := scaled(src, s)
		blit(out, scaled, x, pad+(128-s)/2)
		blit(out, scaled, x, rowH+pad+(128-s)/2)
		fill(out, image.Rect(x, rowH, w, rowH+pad), dark)
	}

	// full-size rendering at the bottom, on light
	fill(out, image.Rect(0, rowH*2, w, rowH*2+pad), light)
	blit(out, scaled(src, 256), pad, rowH*2+pad/2)
	return out
}

func fill(dst *image.NRGBA, r image.Rectangle, c color.NRGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if image.Pt(x, y).In(dst.Rect) {
				dst.SetNRGBA(x, y, c)
			}
		}
	}
}

func blit(dst *image.NRGBA, src *image.NRGBA, x0, y0 int) {
	b := src.Bounds()
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			px := x0 + x
			py := y0 + y
			if !image.Pt(px, py).In(dst.Rect) {
				continue
			}
			s := src.NRGBAAt(b.Min.X+x, b.Min.Y+y)
			if s.A == 0 {
				continue
			}
			d := dst.NRGBAAt(px, py)
			a := float64(s.A) / 255
			dst.SetNRGBA(px, py, color.NRGBA{
				R: uint8(float64(s.R)*a + float64(d.R)*(1-a)),
				G: uint8(float64(s.G)*a + float64(d.G)*(1-a)),
				B: uint8(float64(s.B)*a + float64(d.B)*(1-a)),
				A: 0xFF,
			})
		}
	}
}

// scaled box-filters src down to an n x n square.
func scaled(src *image.NRGBA, n int) *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, n, n))
	b := src.Bounds()
	step := float64(b.Dx()) / float64(n)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			var rr, gg, bb, aa, cnt float64
			for sy := int(float64(y) * step); sy < int(float64(y+1)*step); sy++ {
				for sx := int(float64(x) * step); sx < int(float64(x+1)*step); sx++ {
					if !image.Pt(sx, sy).In(b) {
						continue
					}
					c := src.NRGBAAt(sx, sy)
					f := float64(c.A) / 255
					rr += float64(c.R) * f
					gg += float64(c.G) * f
					bb += float64(c.B) * f
					aa += f
					cnt++
				}
			}
			if cnt == 0 || aa == 0 {
				continue
			}
			out.SetNRGBA(x, y, color.NRGBA{
				R: uint8(math.Round(rr / aa)),
				G: uint8(math.Round(gg / aa)),
				B: uint8(math.Round(bb / aa)),
				A: uint8(math.Round(aa / cnt * 255)),
			})
		}
	}
	return out
}
