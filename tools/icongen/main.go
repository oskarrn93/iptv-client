// Renders the app icon: a TV with a play symbol on a blue squircle.
// Regenerate with: go run ./tools/icongen build/appicon.png
// Wails derives the Windows .ico and macOS .icns from build/appicon.png.
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const (
	size = 1024
	ss   = 4 // supersampling per axis
)

// Signed distance to a rounded rectangle centred at (cx, cy).
func roundRect(x, y, cx, cy, hw, hh, r float64) float64 {
	qx := math.Abs(x-cx) - hw + r
	qy := math.Abs(y-cy) - hh + r
	ox, oy := math.Max(qx, 0), math.Max(qy, 0)
	return math.Hypot(ox, oy) + math.Min(math.Max(qx, qy), 0) - r
}

// Signed distance to a triangle (a, b, c), negative inside.
func triangle(px, py float64, p [3][2]float64) float64 {
	d := math.Inf(1)
	sign := 0.0
	for i := 0; i < 3; i++ {
		a, b := p[i], p[(i+1)%3]
		ex, ey := b[0]-a[0], b[1]-a[1]
		wx, wy := px-a[0], py-a[1]
		t := math.Max(0, math.Min(1, (wx*ex+wy*ey)/(ex*ex+ey*ey)))
		dx, dy := wx-ex*t, wy-ey*t
		d = math.Min(d, math.Hypot(dx, dy))
		cross := ex*wy - ey*wx
		if i == 0 {
			sign = math.Copysign(1, cross)
		} else if math.Copysign(1, cross) != sign {
			sign = 0
		}
	}
	if sign != 0 {
		return -d
	}
	return d
}

type rgba struct{ r, g, b, a float64 }

func over(dst, src rgba) rgba {
	a := src.a + dst.a*(1-src.a)
	if a == 0 {
		return rgba{}
	}
	mix := func(s, d float64) float64 { return (s*src.a + d*dst.a*(1-src.a)) / a }
	return rgba{mix(src.r, dst.r), mix(src.g, dst.g), mix(src.b, dst.b), a}
}

func sample(x, y float64) rgba {
	var c rgba
	// Background squircle with a vertical gradient (macOS-style margin).
	if roundRect(x, y, 512, 512, 412, 412, 185) <= 0 {
		t := (y - 100) / 824
		c = rgba{0.18 + 0.10*t, 0.45 - 0.17*t, 0.95 - 0.25*t, 1}
	}
	white := rgba{1, 1, 1, 1}
	// TV body outline.
	body := roundRect(x, y, 512, 540, 270, 195, 60)
	if body <= 0 && body >= -38 {
		c = over(c, white)
	}
	// Antenna legs.
	for _, s := range []float64{-1, 1} {
		ax, ay := 512+s*110, 255.0
		tx, ty := 512.0, 345.0
		ex, ey := tx-ax, ty-ay
		t := math.Max(0, math.Min(1, ((x-ax)*ex+(y-ay)*ey)/(ex*ex+ey*ey)))
		if math.Hypot(x-ax-ex*t, y-ay-ey*t) <= 19 {
			c = over(c, white)
		}
	}
	// Play triangle.
	if triangle(x, y, [3][2]float64{{455, 445}, {455, 635}, {615, 540}}) <= 0 {
		c = over(c, white)
	}
	return c
}

func main() {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					c := sample(float64(px)+(float64(sx)+0.5)/ss, float64(py)+(float64(sy)+0.5)/ss)
					r, g, b, a = r+c.r*c.a, g+c.g*c.a, b+c.b*c.a, a+c.a
				}
			}
			if a == 0 {
				continue
			}
			n := float64(ss * ss)
			img.SetNRGBA(px, py, color.NRGBA{
				R: uint8(math.Round(r / a * 255)), G: uint8(math.Round(g / a * 255)),
				B: uint8(math.Round(b / a * 255)), A: uint8(math.Round(a / n * 255)),
			})
		}
	}
	f, err := os.Create(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}
