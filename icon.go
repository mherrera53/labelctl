package main

import (
	"bytes"
	"encoding/binary"
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

// generateICO creates a Windows .ico file with multiple sizes embedded as PNG.
func generateICO(sizes []int) []byte {
	type entry struct {
		data   []byte
		width  int
		height int
	}
	entries := make([]entry, len(sizes))
	for i, sz := range sizes {
		entries[i] = entry{data: generateAppIcon(sz), width: sz, height: sz}
	}

	var buf bytes.Buffer
	// ICO header: reserved(2) + type(2) + count(2)
	binary.Write(&buf, binary.LittleEndian, uint16(0))           // reserved
	binary.Write(&buf, binary.LittleEndian, uint16(1))           // type = ICO
	binary.Write(&buf, binary.LittleEndian, uint16(len(entries))) // count

	// Calculate offsets: header(6) + entries(16 each) + data
	dataOffset := 6 + 16*len(entries)
	for _, e := range entries {
		w := uint8(e.width)
		h := uint8(e.height)
		if e.width >= 256 {
			w = 0 // 0 means 256 in ICO format
		}
		if e.height >= 256 {
			h = 0
		}
		buf.WriteByte(w)                                                        // width
		buf.WriteByte(h)                                                        // height
		buf.WriteByte(0)                                                        // color palette
		buf.WriteByte(0)                                                        // reserved
		binary.Write(&buf, binary.LittleEndian, uint16(1))                      // color planes
		binary.Write(&buf, binary.LittleEndian, uint16(32))                     // bits per pixel
		binary.Write(&buf, binary.LittleEndian, uint32(len(e.data)))            // size
		binary.Write(&buf, binary.LittleEndian, uint32(dataOffset)) // offset
		dataOffset += len(e.data)
	}
	for _, e := range entries {
		buf.Write(e.data)
	}
	return buf.Bytes()
}

func generateAppIcon(size int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	s := float64(size)

	// For tray icons (small sizes), use macOS template style: black on transparent
	// macOS template icons: black pixels = visible, alpha = shape mask
	isTray := size <= 64
	if isTray {
		black := color.RGBA{0, 0, 0, 255}
		transparent := color.RGBA{0, 0, 0, 0}

		// Printer body — chunky recognizable silhouette
		iconFillRoundedRect(img, i(s*0.08), i(s*0.20), i(s*0.92), i(s*0.62), imax(1, i(s*0.05)), black)

		// Paper input tray on top
		iconFillRect(img, i(s*0.20), i(s*0.10), i(s*0.80), i(s*0.22), black)

		// Paper slot cutout (transparent hole in body)
		iconFillRect(img, i(s*0.18), i(s*0.28), i(s*0.82), i(s*0.50), transparent)

		// Two text lines inside slot
		iconFillRect(img, i(s*0.24), i(s*0.32), i(s*0.64), i(s*0.37), black)
		iconFillRect(img, i(s*0.24), i(s*0.40), i(s*0.54), i(s*0.44), black)

		// Paper/label output below printer
		iconFillRoundedRect(img, i(s*0.14), i(s*0.58), i(s*0.86), i(s*0.90), imax(1, i(s*0.03)), black)

		// Barcode lines on paper
		bw := imax(1, i(s*0.04))
		for j := 0; j < 6; j++ {
			w := bw
			if j%2 == 0 {
				w = bw + imax(1, i(s*0.02))
			}
			bx := i(s*0.24) + j*i(s*0.08)
			iconFillRect(img, bx, i(s*0.66), bx+w, i(s*0.80), transparent)
		}

		// Status LED dot
		iconFillCircle(img, i(s*0.82), i(s*0.16), imax(1, i(s*0.06)), black)
	} else {
		// Full color icon for app/dock — professional dark theme
		bg := color.RGBA{13, 17, 23, 255}          // GitHub dark #0D1117
		bgInner := color.RGBA{22, 27, 34, 255}     // #161B22
		surface := color.RGBA{33, 38, 45, 255}     // #21262D
		surfaceHL := color.RGBA{48, 54, 61, 255}   // #30363D
		accent := color.RGBA{110, 118, 255, 255}   // Indigo blue
		accentDim := color.RGBA{110, 118, 255, 50} // Faint accent
		accentGlow := color.RGBA{110, 118, 255, 25}
		green := color.RGBA{63, 185, 80, 255}       // #3FB950
		greenGlow := color.RGBA{63, 185, 80, 50}    // LED glow
		blue := color.RGBA{88, 166, 255, 255}       // #58A6FF
		paper := color.RGBA{240, 246, 252, 255}     // #F0F6FC
		paperShadow := color.RGBA{200, 210, 224, 255}
		dark := color.RGBA{1, 4, 9, 255}            // Near black
		highlight := color.RGBA{255, 255, 255, 15}  // Glossy highlight

		// 1. Background
		iconFillAll(img, bg)

		// 2. Inner squircle with subtle gradient (lighter at top)
		r := i(s * 0.16)
		iconFillRoundedRect(img, i(s*0.04), i(s*0.04), i(s*0.96), i(s*0.96), r, bgInner)

		// Subtle top highlight gradient on background
		for row := i(s * 0.04); row < i(s*0.25); row++ {
			alpha := uint8(8 - 8*float64(row-i(s*0.04))/float64(i(s*0.21)))
			if alpha > 0 {
				gradC := color.RGBA{255, 255, 255, alpha}
				for x := i(s * 0.04); x < i(s*0.96); x++ {
					if iconInRoundedRect(x, row, i(s*0.04), i(s*0.04), i(s*0.96), i(s*0.96), r) {
						iconBlend(img, x, row, gradC)
					}
				}
			}
		}

		// 3. Paper input tray on top of printer
		tx1, ty1 := i(s*0.24), i(s*0.16)
		tx2, ty2 := i(s*0.76), i(s*0.30)
		iconFillRoundedRect(img, tx1, ty1, tx2, ty2, i(s*0.03), surfaceHL)
		iconStrokeRoundedRect(img, tx1, ty1, tx2, ty2, i(s*0.03), accent, imax(1, i(s*0.003)))

		// Paper edges visible in input tray
		iconFillRect(img, i(s*0.30), i(s*0.19), i(s*0.70), i(s*0.20), paper)
		iconFillRect(img, i(s*0.30), i(s*0.21), i(s*0.70), i(s*0.215), paperShadow)

		// 4. Printer body
		bx1, by1 := i(s*0.12), i(s*0.28)
		bx2, by2 := i(s*0.88), i(s*0.66)
		br := i(s * 0.05)
		iconFillRoundedRect(img, bx1, by1, bx2, by2, br, surface)
		iconStrokeRoundedRect(img, bx1, by1, bx2, by2, br, accent, imax(1, i(s*0.004)))

		// Top highlight on printer body (glossy effect)
		iconFillRect(img, bx1+i(s*0.03), by1+1, bx2-i(s*0.03), by1+i(s*0.025), highlight)

		// 5. Display/paper slot (dark inset in body)
		sx1, sy1 := i(s*0.20), i(s*0.34)
		sx2, sy2 := i(s*0.78), i(s*0.52)
		sr := i(s * 0.02)
		iconFillRoundedRect(img, sx1, sy1, sx2, sy2, sr, dark)
		iconStrokeRoundedRect(img, sx1, sy1, sx2, sy2, sr, accent, imax(1, i(s*0.002)))

		// Text lines in slot (simulating a display)
		lw := imax(1, i(s*0.003))
		iconFillRect(img, i(s*0.25), i(s*0.38), i(s*0.62), i(s*0.38)+lw*2, accentDim)
		iconFillRect(img, i(s*0.25), i(s*0.42), i(s*0.55), i(s*0.42)+lw*2, accentDim)
		iconFillRect(img, i(s*0.25), i(s*0.46), i(s*0.48), i(s*0.46)+lw*2, accentDim)

		// 6. Control panel area (right side of body)
		// Status LED with glow
		ledX, ledY := i(s*0.76), i(s*0.36)
		ledR := imax(2, i(s*0.025))
		iconFillCircle(img, ledX, ledY, ledR*3, accentGlow)
		iconFillCircle(img, ledX, ledY, ledR*2, greenGlow)
		iconFillCircle(img, ledX, ledY, ledR, green)

		// Small buttons on right side
		btnC := surfaceHL
		iconFillRoundedRect(img, i(s*0.72), i(s*0.44), i(s*0.80), i(s*0.47), imax(1, i(s*0.01)), btnC)
		iconFillRoundedRect(img, i(s*0.72), i(s*0.49), i(s*0.80), i(s*0.52), imax(1, i(s*0.01)), btnC)

		// 7. Paper/label output — the hero element
		px1, py1 := i(s*0.16), i(s*0.64)
		px2, py2 := i(s*0.84), i(s*0.88)
		pr := i(s * 0.02)

		// Paper shadow
		iconFillRoundedRect(img, px1+i(s*0.01), py1+i(s*0.01), px2+i(s*0.01), py2+i(s*0.01), pr, color.RGBA{0, 0, 0, 40})

		// Paper body
		iconFillRoundedRect(img, px1, py1, px2, py2, pr, paper)

		// Barcode on paper — detailed pattern
		barcodeY := i(s * 0.69)
		barcodeH := i(s * 0.09)
		barWidths := []int{3, 1, 2, 1, 3, 1, 1, 2, 1, 3, 1, 2, 3, 1, 1, 2, 1, 3, 1, 2}
		bx := i(s * 0.24)
		gap := imax(1, i(s*0.004))
		bUnit := imax(1, i(s*0.005))
		for _, w := range barWidths {
			barW := w * bUnit
			iconFillRect(img, bx, barcodeY, bx+barW, barcodeY+barcodeH, dark)
			bx += barW + gap
			if bx > i(s*0.76) {
				break
			}
		}

		// Text line placeholder under barcode
		iconFillRect(img, i(s*0.24), i(s*0.80), i(s*0.58), i(s*0.82), paperShadow)
		iconFillRect(img, i(s*0.24), i(s*0.84), i(s*0.44), i(s*0.855), paperShadow)

		// 8. WiFi signal arcs (top right)
		wcx, wcy := i(s*0.82), i(s*0.12)
		wt := imax(2, i(s*0.008))
		iconDrawArc(img, wcx, wcy, i(s*0.04), -math.Pi*0.80, -math.Pi*0.20, blue, wt)
		iconDrawArc(img, wcx, wcy, i(s*0.075), -math.Pi*0.80, -math.Pi*0.20, blue, wt)
		iconDrawArc(img, wcx, wcy, i(s*0.11), -math.Pi*0.80, -math.Pi*0.20, blue, imax(1, wt-1))
		iconFillCircle(img, wcx, wcy, imax(2, i(s*0.015)), blue)

		// 9. Subtle accent glow at bottom
		for row := i(s * 0.88); row < i(s*0.96); row++ {
			alpha := uint8(12 * (1 - float64(row-i(s*0.88))/float64(i(s*0.08))))
			if alpha > 0 {
				gradC := color.RGBA{110, 118, 255, alpha}
				for x := i(s * 0.20); x < i(s*0.80); x++ {
					if iconInRoundedRect(x, row, i(s*0.04), i(s*0.04), i(s*0.96), i(s*0.96), r) {
						iconBlend(img, x, row, gradC)
					}
				}
			}
		}
	}

	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

func i(f float64) int { return int(f) }

func imax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

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
	if c.A == 0 {
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
