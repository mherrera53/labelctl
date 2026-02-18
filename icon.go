package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"sync"
)

var (
	appIconPNG  []byte
	appIconOnce sync.Once
)

// getAppIconPNG returns a cached 256x256 PNG icon for the app.
func getAppIconPNG() []byte {
	appIconOnce.Do(func() {
		appIconPNG = generateAppIcon(256)
	})
	return appIconPNG
}

func generateAppIcon(size int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	s := float64(size)

	bg := color.RGBA{15, 17, 23, 255}
	bgLight := color.RGBA{22, 24, 34, 255}
	surface := color.RGBA{30, 34, 48, 255}
	accent := color.RGBA{108, 114, 255, 255}
	accentDim := color.RGBA{108, 114, 255, 80}
	green := color.RGBA{52, 211, 153, 255}
	blue := color.RGBA{96, 165, 250, 255}
	paper := color.RGBA{220, 224, 236, 255}

	// Fill background
	iconFillAll(img, bg)

	// Rounded background card
	iconFillRoundedRect(img, i(s*0.04), i(s*0.04), i(s*0.96), i(s*0.96), i(s*0.16), bgLight)

	// Printer body
	bx, by := i(s*0.14), i(s*0.28)
	bx2, by2 := i(s*0.86), i(s*0.68)
	iconFillRoundedRect(img, bx, by, bx2, by2, i(s*0.06), surface)
	iconStrokeRoundedRect(img, bx, by, bx2, by2, i(s*0.06), accent, 2)

	// Paper slot (inner dark area)
	iconFillRect(img, i(s*0.22), i(s*0.34), i(s*0.78), i(s*0.48), bg)
	iconStrokeRect(img, i(s*0.22), i(s*0.34), i(s*0.78), i(s*0.48), accent, 1)

	// Label lines inside slot
	iconFillRect(img, i(s*0.26), i(s*0.37), i(s*0.60), i(s*0.40), accentDim)
	iconFillRect(img, i(s*0.26), i(s*0.42), i(s*0.52), i(s*0.44), accentDim)

	// Paper output (label coming out bottom)
	iconFillRoundedRect(img, i(s*0.20), i(s*0.66), i(s*0.80), i(s*0.82), i(s*0.03), paper)

	// Barcode on paper
	barcodeY := i(s * 0.71)
	barcodeH := i(s * 0.07)
	for j := 0; j < 12; j++ {
		bw := 2
		if j%3 == 0 {
			bw = 3
		}
		bx := i(s*0.28) + j*i(s*0.035)
		iconFillRect(img, bx, barcodeY, bx+bw, barcodeY+barcodeH, bg)
	}

	// LED indicator
	iconFillCircle(img, i(s*0.76), i(s*0.36), i(s*0.025), green)

	// WiFi arcs (top-right)
	wcx, wcy := i(s*0.78), i(s*0.16)
	iconDrawArc(img, wcx, wcy, i(s*0.05), -math.Pi*0.75, -math.Pi*0.25, blue, 3)
	iconDrawArc(img, wcx, wcy, i(s*0.09), -math.Pi*0.75, -math.Pi*0.25, blue, 3)
	iconDrawArc(img, wcx, wcy, i(s*0.13), -math.Pi*0.75, -math.Pi*0.25, blue, 2)
	iconFillCircle(img, wcx, wcy, i(s*0.02), blue)

	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

func i(f float64) int { return int(f) }

func iconFillAll(img *image.RGBA, c color.RGBA) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func iconFillRect(img *image.RGBA, x1, y1, x2, y2 int, c color.RGBA) {
	for y := y1; y < y2; y++ {
		for x := x1; x < x2; x++ {
			iconBlend(img, x, y, c)
		}
	}
}

func iconStrokeRect(img *image.RGBA, x1, y1, x2, y2 int, c color.RGBA, w int) {
	for t := 0; t < w; t++ {
		for x := x1; x < x2; x++ {
			iconBlend(img, x, y1+t, c)
			iconBlend(img, x, y2-1-t, c)
		}
		for y := y1; y < y2; y++ {
			iconBlend(img, x1+t, y, c)
			iconBlend(img, x2-1-t, y, c)
		}
	}
}

func iconFillRoundedRect(img *image.RGBA, x1, y1, x2, y2, r int, c color.RGBA) {
	for y := y1; y < y2; y++ {
		for x := x1; x < x2; x++ {
			if iconInRoundedRect(x, y, x1, y1, x2, y2, r) {
				iconBlend(img, x, y, c)
			}
		}
	}
}

func iconStrokeRoundedRect(img *image.RGBA, x1, y1, x2, y2, r int, c color.RGBA, w int) {
	for y := y1; y < y2; y++ {
		for x := x1; x < x2; x++ {
			if iconInRoundedRect(x, y, x1, y1, x2, y2, r) && !iconInRoundedRect(x, y, x1+w, y1+w, x2-w, y2-w, r-w) {
				iconBlend(img, x, y, c)
			}
		}
	}
}

func iconInRoundedRect(x, y, x1, y1, x2, y2, r int) bool {
	if x < x1 || x >= x2 || y < y1 || y >= y2 {
		return false
	}
	// Check corners
	corners := [][2]int{{x1 + r, y1 + r}, {x2 - r, y1 + r}, {x1 + r, y2 - r}, {x2 - r, y2 - r}}
	for _, corner := range corners {
		cx, cy := corner[0], corner[1]
		inCornerRegion := false
		if x < x1+r && y < y1+r {
			inCornerRegion = true
		} else if x >= x2-r && y < y1+r {
			inCornerRegion = true
		} else if x < x1+r && y >= y2-r {
			inCornerRegion = true
		} else if x >= x2-r && y >= y2-r {
			inCornerRegion = true
		}
		if inCornerRegion {
			dx := float64(x - cx)
			dy := float64(y - cy)
			if dx*dx+dy*dy > float64(r*r) {
				return false
			}
		}
	}
	_ = corners
	return true
}

func iconFillCircle(img *image.RGBA, cx, cy, r int, c color.RGBA) {
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			dx, dy := float64(x-cx), float64(y-cy)
			if dx*dx+dy*dy <= float64(r*r) {
				iconBlend(img, x, y, c)
			}
		}
	}
}

func iconDrawArc(img *image.RGBA, cx, cy, r int, startAngle, endAngle float64, c color.RGBA, w int) {
	steps := r * 6
	if steps < 60 {
		steps = 60
	}
	for j := 0; j <= steps; j++ {
		t := startAngle + (endAngle-startAngle)*float64(j)/float64(steps)
		x := cx + int(float64(r)*math.Cos(t))
		y := cy + int(float64(r)*math.Sin(t))
		iconFillCircle(img, x, y, w/2, c)
	}
}

func iconBlend(img *image.RGBA, x, y int, c color.RGBA) {
	b := img.Bounds()
	if x < b.Min.X || x >= b.Max.X || y < b.Min.Y || y >= b.Max.Y {
		return
	}
	if c.A == 255 {
		img.SetRGBA(x, y, c)
		return
	}
	// Alpha blend
	bg := img.RGBAAt(x, y)
	a := float64(c.A) / 255.0
	img.SetRGBA(x, y, color.RGBA{
		R: uint8(float64(c.R)*a + float64(bg.R)*(1-a)),
		G: uint8(float64(c.G)*a + float64(bg.G)*(1-a)),
		B: uint8(float64(c.B)*a + float64(bg.B)*(1-a)),
		A: 255,
	})
}
