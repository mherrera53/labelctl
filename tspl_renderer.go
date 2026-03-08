package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"math"
	"net/http"
	"os"
	"strings"

	goqrcode "github.com/skip2/go-qrcode"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// ════════════════════════════════════════════════════
// TSPL2 Renderer — Converts pdfme schemas to TSPL2
// commands for TSC TDP-244 Pro (203 DPI) and compatible TSPL2 printers.
//
// Two rendering modes:
//   - Native: uses TSPL2 commands (TEXT, BAR, BOX, QRCODE, BARCODE, DIAGONAL, BITMAP)
//   - Raster: renders entire page as monochrome bitmap (maximum fidelity, larger payload)
//
// The output includes the ESC !R init sequence so the printer switches to TSPL2 mode.
// IMPORTANT: do NOT run sanitizeTSPL() on the output — it corrupts binary BITMAP data.
// ════════════════════════════════════════════════════

const defaultDPI = 203 // TSC TDP-244 Pro = 203 DPI (8 dots/mm)

// TSPL2 built-in monospace font metrics (width × height in dots at 203 DPI).
// Can be scaled with multipliers 1–10.
var tsplFontMetrics = []struct {
	Name   string
	Width  int // char width in dots
	Height int // char height in dots
}{
	{"1", 8, 12},
	{"2", 12, 20},
	{"3", 16, 24},
	{"4", 24, 32},
	{"5", 32, 48},
}

// ════════════════════════════════════════════════════
// Unit conversions
// ════════════════════════════════════════════════════

func mmToDots(mm float64, dpi int) int {
	return int(math.Round(mm * float64(dpi) / 25.4))
}

func ptToDots(pt float64, dpi int) int {
	return int(math.Round(pt * float64(dpi) / 72.0))
}

// ════════════════════════════════════════════════════
// Visibility checks (thermal = monochrome binary)
// ════════════════════════════════════════════════════

// isColorDarkEnough returns true if a color would print visibly on thermal paper.
func isColorDarkEnough(colorStr string) bool {
	if colorStr == "" {
		return true // default = black
	}
	r, g, b, a := parseColor(colorStr)
	if a < 0.3 {
		return false
	}
	lum := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
	return lum < 200
}

// shouldRenderTSPL returns false for fields invisible on thermal (e.g. 15% opacity guilloche).
// shouldRenderTSPL filters decorative elements that don't translate to thermal printing.
// Same logic as PHP PdfmeToTsplConverter::isDecorativeElement (inverted: true = render).
func shouldRenderTSPL(field PdfmeField) bool {
	// Low opacity elements are decorative (guilloche patterns are typically ~0.08 opacity)
	if field.Opacity > 0 && field.Opacity < 0.5 {
		return false
	}

	// Rotated lines = diagonals, no TSPL native support
	if field.Type == "line" && (field.Rotate > 1.0 || field.Rotate < -1.0) {
		return false
	}

	// Ellipses are decorative flourishes
	if field.Type == "ellipse" {
		return false
	}

	return true
}

// emitThermalGuilloche generates a decorative border pattern for thermal labels.
// Same logic as PHP PdfmeToTsplConverter::emitThermalGuilloche.
func emitThermalGuilloche(buf *bytes.Buffer, labelWmm, labelHmm float64, dpi int) {
	w := mmToDots(labelWmm, dpi)
	h := mmToDots(labelHmm, dpi)
	m := 4 // margin in dots

	// Double frame
	buf.WriteString(fmt.Sprintf("BOX %d,%d,%d,%d,1\r\n", m, m, w-m, h-m))
	buf.WriteString(fmt.Sprintf("BOX %d,%d,%d,%d,1\r\n", m+3, m+3, w-m-3, h-m-3))

	// Corner tick marks
	tickLen := 20
	tickOff := m + 6

	// Top-left
	buf.WriteString(fmt.Sprintf("BAR %d,%d,%d,1\r\n", tickOff, tickOff, tickLen))
	buf.WriteString(fmt.Sprintf("BAR %d,%d,1,%d\r\n", tickOff, tickOff, tickLen))
	// Top-right
	tr := w - m - 6
	buf.WriteString(fmt.Sprintf("BAR %d,%d,%d,1\r\n", tr-tickLen, tickOff, tickLen))
	buf.WriteString(fmt.Sprintf("BAR %d,%d,1,%d\r\n", tr, tickOff, tickLen))
	// Bottom-left
	bl := h - m - 6
	buf.WriteString(fmt.Sprintf("BAR %d,%d,%d,1\r\n", tickOff, bl, tickLen))
	buf.WriteString(fmt.Sprintf("BAR %d,%d,1,%d\r\n", tickOff, bl-tickLen, tickLen))
	// Bottom-right
	buf.WriteString(fmt.Sprintf("BAR %d,%d,%d,1\r\n", tr-tickLen, bl, tickLen))
	buf.WriteString(fmt.Sprintf("BAR %d,%d,1,%d\r\n", tr, bl-tickLen, tickLen))

	// Dashed lines along top/bottom edges
	lineStart := m + 8
	lineEnd := w - m - 8
	for y := m + 1; y < m+3; y++ {
		for x := lineStart; x < lineEnd; x += 16 {
			dw := 8
			if lineEnd-x < dw {
				dw = lineEnd - x
			}
			buf.WriteString(fmt.Sprintf("BAR %d,%d,%d,1\r\n", x, y, dw))
		}
	}
	bottomY := h - m - 2
	for y := bottomY; y < bottomY+2; y++ {
		for x := lineStart; x < lineEnd; x += 16 {
			dw := 8
			if lineEnd-x < dw {
				dw = lineEnd - x
			}
			buf.WriteString(fmt.Sprintf("BAR %d,%d,%d,1\r\n", x, y, dw))
		}
	}

	// Dashed lines along left/right edges
	vStart := m + 8
	vEnd := h - m - 8
	for x := m + 1; x < m+3; x++ {
		for y := vStart; y < vEnd; y += 16 {
			dh := 8
			if vEnd-y < dh {
				dh = vEnd - y
			}
			buf.WriteString(fmt.Sprintf("BAR %d,%d,1,%d\r\n", x, y, dh))
		}
	}
	rightX := w - m - 2
	for x := rightX; x < rightX+2; x++ {
		for y := vStart; y < vEnd; y += 16 {
			dh := 8
			if vEnd-y < dh {
				dh = vEnd - y
			}
			buf.WriteString(fmt.Sprintf("BAR %d,%d,1,%d\r\n", x, y, dh))
		}
	}
}

// ════════════════════════════════════════════════════
// Font sizing
// ════════════════════════════════════════════════════

type tsplFontChoice struct {
	Font  string
	Mult  int
	CharW int // effective char width in dots
	CharH int // effective char height in dots
}

// pickTSPLFont returns the largest font+multiplier that fits within targetH dots.
func pickTSPLFont(targetH int) tsplFontChoice {
	var best tsplFontChoice
	for _, f := range tsplFontMetrics {
		for mult := 1; mult <= 10; mult++ {
			h := f.Height * mult
			if h <= targetH && h > best.CharH {
				best = tsplFontChoice{Font: f.Name, Mult: mult, CharW: f.Width * mult, CharH: h}
			}
		}
	}
	if best.Font == "" {
		best = tsplFontChoice{Font: "1", Mult: 1, CharW: 8, CharH: 12}
	}
	return best
}

// pickTSPLFontForSize converts a pdfme fontSize (points) to the best TSPL font.
func pickTSPLFontForSize(fontSize float64, dpi int) tsplFontChoice {
	targetH := ptToDots(fontSize, dpi)
	if targetH < 12 {
		targetH = 12
	}
	return pickTSPLFont(targetH)
}

// pickTSPLFontDynamic finds the largest font+mult that lets text fit inside w×h dots.
// fit: "horizontal" = text must fit in one line (shrinks font until no wrapping needed).
// fit: "vertical" (default) = word-wrap allowed, total height must fit within h.
func pickTSPLFontDynamic(text string, w, h int, minPt, maxPt float64, fit string, dpi int) tsplFontChoice {
	maxH := ptToDots(maxPt, dpi)
	minH := ptToDots(minPt, dpi)
	if minH < 12 {
		minH = 12
	}

	isHorizontal := fit == "horizontal"

	// Try from largest to smallest
	for fi := len(tsplFontMetrics) - 1; fi >= 0; fi-- {
		f := tsplFontMetrics[fi]
		for mult := 10; mult >= 1; mult-- {
			charH := f.Height * mult
			charW := f.Width * mult
			if charH > maxH || charH < minH {
				continue
			}

			if isHorizontal {
				// Each paragraph must fit in one line (no wrapping)
				fits := true
				for _, para := range strings.Split(text, "\n") {
					textW := len(para) * charW
					if textW > w {
						fits = false
						break
					}
				}
				if fits {
					return tsplFontChoice{Font: f.Name, Mult: mult, CharW: charW, CharH: charH}
				}
			} else {
				// Vertical: word-wrap and check total height fits
				lines := tsplWordWrap(text, w, charW)
				totalH := len(lines) * charH
				if totalH <= h {
					return tsplFontChoice{Font: f.Name, Mult: mult, CharW: charW, CharH: charH}
				}
			}
		}
	}
	return pickTSPLFont(minH)
}

// tsplWordWrap wraps text to fit within maxWidth dots using monospace charWidth.
func tsplWordWrap(text string, maxWidthDots, charWidthDots int) []string {
	if charWidthDots <= 0 {
		return []string{text}
	}
	maxChars := maxWidthDots / charWidthDots
	if maxChars < 1 {
		maxChars = 1
	}

	var result []string
	for _, para := range strings.Split(text, "\n") {
		if para == "" {
			result = append(result, "")
			continue
		}
		words := strings.Fields(para)
		if len(words) == 0 {
			result = append(result, "")
			continue
		}
		cur := words[0]
		for i := 1; i < len(words); i++ {
			candidate := cur + " " + words[i]
			if len(candidate) <= maxChars {
				cur = candidate
			} else {
				result = append(result, cur)
				cur = words[i]
			}
		}
		result = append(result, cur)
	}
	return result
}

// ════════════════════════════════════════════════════
// Rotation helper: pdfme degrees → TSPL 0/90/180/270
// ════════════════════════════════════════════════════

func tsplRotation(degrees float64) int {
	if degrees == 0 {
		return 0
	}
	r := math.Mod(degrees, 360)
	if r < 0 {
		r += 360
	}
	if r >= 315 || r < 45 {
		return 0
	} else if r >= 45 && r < 135 {
		return 90
	} else if r >= 135 && r < 225 {
		return 180
	}
	return 270
}

// ════════════════════════════════════════════════════
// TSPL string escaping
// ════════════════════════════════════════════════════

// escTSPLStr escapes a string for TSPL TEXT commands (truncates at 200 chars for safety).
func escTSPLStr(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "'")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	if len(s) > 200 {
		s = s[:197] + "..."
	}
	return s
}

// escTSPLData escapes a string for TSPL data commands (QRCODE, BARCODE) without truncation.
func escTSPLData(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "'")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}

// ════════════════════════════════════════════════════
// Main entry: RenderBulkTSPL (native commands mode)
// ════════════════════════════════════════════════════

// RenderBulkTSPL generates TSPL2 commands from a pdfme schema + data rows.
// Returns raw bytes ready to send to the printer (includes ESC !R init).
// Do NOT pass through sanitizeTSPL — may contain binary BITMAP data.
func RenderBulkTSPL(schema *PdfmeSchema, rows []map[string]string, dpi int, copies int) []byte {
	if dpi <= 0 {
		dpi = defaultDPI
	}
	if copies < 1 {
		copies = 1
	}

	var buf bytes.Buffer

	// ── ESC !R: Force printer into TSPL2 mode ──
	buf.Write([]byte{0x1b, 0x21, 0x52})
	buf.WriteString("\r\n")

	// ── Label setup from schema dimensions ──
	labelW := schema.BasePdf.Width
	labelH := schema.BasePdf.Height

	buf.WriteString(fmt.Sprintf("SIZE %.1f mm, %.1f mm\r\n", labelW, labelH))
	buf.WriteString("GAP 3 mm, 0 mm\r\n")
	buf.WriteString("DIRECTION 0,0\r\n")
	buf.WriteString("SPEED 4\r\n")
	buf.WriteString("DENSITY 10\r\n")
	buf.WriteString("SET CUTTER OFF\r\n")
	buf.WriteString("SET TEAR ON\r\n")

	hasBitmap := schemaHasImages(schema)
	if !hasBitmap {
		buf.WriteString("CODEPAGE UTF-8\r\n")
	}

	// Pre-scan: count decorative elements to decide if thermal guilloche is needed
	decorativeCount := 0
	for _, pageFields := range schema.Schemas {
		for _, field := range pageFields {
			if !shouldRenderTSPL(field) {
				decorativeCount++
			}
		}
	}

	for ri, row := range rows {
		for pi, pageFields := range schema.Schemas {
			buf.WriteString("CLS\r\n")

			// Emit thermal guilloche as background if decorative elements were filtered
			if decorativeCount > 5 {
				emitThermalGuilloche(&buf, labelW, labelH, dpi)
			}

			for _, field := range pageFields {
				if !shouldRenderTSPL(field) {
					continue
				}

				value := resolveFieldValue(field, row)
				x := mmToDots(field.Position.X, dpi)
				y := mmToDots(field.Position.Y, dpi)
				w := mmToDots(field.Width, dpi)
				h := mmToDots(field.Height, dpi)

				if ri == 0 && pi == 0 {
					log.Printf("[tspl] Field %q type=%s value=%q x=%d y=%d w=%d h=%d",
						field.Name, field.Type, truncate(value, 40), x, y, w, h)
				}

				switch field.Type {
				case "text", "multiVariableText":
					if value != "" {
						tsplRenderText(&buf, field, value, x, y, w, h, dpi)
					}
				case "qrcode":
					if value == "" {
						value = field.Content
					}
					if value != "" {
						tsplRenderQR(&buf, field, value, x, y, w, h, dpi)
					}
				case "barcode", "code128", "code39", "ean13", "ean8":
					if value != "" {
						tsplRenderBarcode(&buf, field, value, x, y, w, h, dpi)
					}
				case "line":
					tsplRenderLine(&buf, field, x, y, w, h, dpi)
				case "rectangle":
					tsplRenderRectangle(&buf, field, x, y, w, h, dpi)
				case "ellipse":
					tsplRenderEllipse(&buf, field, x, y, w, h, dpi)
				case "image":
					tsplRenderImage(&buf, field, row, x, y, w, h, dpi)
				}
			}

			buf.WriteString(fmt.Sprintf("PRINT %d\r\n", copies))
		}
	}

	log.Printf("[tspl] Generated %d bytes for %d rows (mode=native)", buf.Len(), len(rows))
	return buf.Bytes()
}

// renderSinglePageTSPL generates TSPL2 commands for a single page from one row.
// Returns raw bytes ready to send to the printer (includes ESC !R init + header).
// pageIndex selects which page within the pdfme schema to render.
func renderSinglePageTSPL(schema *PdfmeSchema, row map[string]string, pageIndex int, dpi int, copies int) []byte {
	if dpi <= 0 {
		dpi = defaultDPI
	}
	if copies < 1 {
		copies = 1
	}
	if pageIndex >= len(schema.Schemas) {
		return nil
	}

	var buf bytes.Buffer

	// ── ESC !R: Force printer into TSPL2 mode ──
	buf.Write([]byte{0x1b, 0x21, 0x52})
	buf.WriteString("\r\n")

	// ── Label setup from schema dimensions ──
	labelW := schema.BasePdf.Width
	labelH := schema.BasePdf.Height

	buf.WriteString(fmt.Sprintf("SIZE %.1f mm, %.1f mm\r\n", labelW, labelH))
	buf.WriteString("GAP 3 mm, 0 mm\r\n")
	buf.WriteString("DIRECTION 0,0\r\n")
	buf.WriteString("SPEED 4\r\n")
	buf.WriteString("DENSITY 10\r\n")
	buf.WriteString("SET CUTTER OFF\r\n")
	buf.WriteString("SET TEAR ON\r\n")

	hasBitmap := false
	for _, f := range schema.Schemas[pageIndex] {
		if f.Type == "image" {
			hasBitmap = true
			break
		}
	}
	if !hasBitmap {
		buf.WriteString("CODEPAGE UTF-8\r\n")
	}

	// Decorative count for this page
	decorativeCount := 0
	for _, field := range schema.Schemas[pageIndex] {
		if !shouldRenderTSPL(field) {
			decorativeCount++
		}
	}

	buf.WriteString("CLS\r\n")

	if decorativeCount > 5 {
		emitThermalGuilloche(&buf, labelW, labelH, dpi)
	}

	for _, field := range schema.Schemas[pageIndex] {
		if !shouldRenderTSPL(field) {
			continue
		}

		value := resolveFieldValue(field, row)
		x := mmToDots(field.Position.X, dpi)
		y := mmToDots(field.Position.Y, dpi)
		w := mmToDots(field.Width, dpi)
		h := mmToDots(field.Height, dpi)

		switch field.Type {
		case "text", "multiVariableText":
			if value != "" {
				tsplRenderText(&buf, field, value, x, y, w, h, dpi)
			}
		case "qrcode":
			if value == "" {
				value = field.Content
			}
			if value != "" {
				tsplRenderQR(&buf, field, value, x, y, w, h, dpi)
			}
		case "barcode", "code128", "code39", "ean13", "ean8":
			if value != "" {
				tsplRenderBarcode(&buf, field, value, x, y, w, h, dpi)
			}
		case "line":
			tsplRenderLine(&buf, field, x, y, w, h, dpi)
		case "rectangle":
			tsplRenderRectangle(&buf, field, x, y, w, h, dpi)
		case "ellipse":
			tsplRenderEllipse(&buf, field, x, y, w, h, dpi)
		case "image":
			tsplRenderImage(&buf, field, row, x, y, w, h, dpi)
		}
	}

	buf.WriteString(fmt.Sprintf("PRINT %d\r\n", copies))
	return buf.Bytes()
}

// schemaHasImages checks if any field in the schema is an image type.
func schemaHasImages(schema *PdfmeSchema) bool {
	for _, page := range schema.Schemas {
		for _, f := range page {
			if f.Type == "image" {
				return true
			}
		}
	}
	return false
}

// ════════════════════════════════════════════════════
// Text rendering
// ════════════════════════════════════════════════════

func tsplRenderText(buf *bytes.Buffer, field PdfmeField, value string, x, y, w, h, dpi int) {
	if field.FontColor != "" && !isColorDarkEnough(field.FontColor) {
		return
	}

	fontSize := field.FontSize
	if fontSize == 0 {
		fontSize = 10
	}

	var fc tsplFontChoice
	if field.DynamicFontSize != nil && field.DynamicFontSize.Max > 0 {
		fc = pickTSPLFontDynamic(value, w, h, field.DynamicFontSize.Min, field.DynamicFontSize.Max, field.DynamicFontSize.Fit, dpi)
	} else {
		fc = pickTSPLFontForSize(fontSize, dpi)
	}

	rotation := tsplRotation(field.Rotate)

	// Padding
	padT := mmToDots(field.Padding.Top, dpi)
	padR := mmToDots(field.Padding.Right, dpi)
	padB := mmToDots(field.Padding.Bottom, dpi)
	padL := mmToDots(field.Padding.Left, dpi)
	px := x + padL
	py := y + padT
	pw := w - padL - padR
	ph := h - padT - padB
	if pw < fc.CharW {
		pw = fc.CharW
	}
	if ph < fc.CharH {
		ph = fc.CharH
	}

	// Word wrap
	lines := tsplWordWrap(value, pw, fc.CharW)

	// Vertical alignment
	totalTextH := len(lines) * fc.CharH
	textY := py
	switch field.VerticalAlignment {
	case "middle":
		if totalTextH < ph {
			textY = py + (ph-totalTextH)/2
		}
	case "bottom":
		if totalTextH < ph {
			textY = py + ph - totalTextH
		}
	}

	for li, line := range lines {
		lineY := textY + li*fc.CharH
		if lineY+fc.CharH > y+h {
			break
		}

		lineX := px
		lineWDots := len(line) * fc.CharW

		switch field.Alignment {
		case "center":
			if lineWDots < pw {
				lineX = px + (pw-lineWDots)/2
			}
		case "right":
			if lineWDots < pw {
				lineX = px + pw - lineWDots
			}
		}

		escaped := escTSPLStr(line)
		buf.WriteString(fmt.Sprintf("TEXT %d,%d,\"%s\",%d,%d,%d,\"%s\"\r\n",
			lineX, lineY, fc.Font, rotation, fc.Mult, fc.Mult, escaped))

		// Poor man's bold: double-print with 1-dot X offset (TSPL fonts have no bold variant)
		if field.FontWeight == "bold" {
			buf.WriteString(fmt.Sprintf("TEXT %d,%d,\"%s\",%d,%d,%d,\"%s\"\r\n",
				lineX+1, lineY, fc.Font, rotation, fc.Mult, fc.Mult, escaped))
		}
	}
}

// ════════════════════════════════════════════════════
// QR code
// ════════════════════════════════════════════════════

func tsplRenderQR(buf *bytes.Buffer, field PdfmeField, value string, x, y, w, h, dpi int) {
	qr, err := goqrcode.New(value, goqrcode.Medium)
	if err != nil {
		log.Printf("[tspl] QR error: %v", err)
		return
	}
	qr.DisableBorder = true
	bitmap := qr.Bitmap()
	modules := len(bitmap)
	if modules == 0 {
		return
	}

	dim := w
	if h < w {
		dim = h
	}
	cellSize := dim / modules
	if cellSize < 1 {
		cellSize = 1
	}
	if cellSize > 10 {
		cellSize = 10
	}

	// Center within field
	qrTotal := cellSize * modules
	qrX := x + (w-qrTotal)/2
	qrY := y + (h-qrTotal)/2
	if qrX < 0 {
		qrX = 0
	}
	if qrY < 0 {
		qrY = 0
	}

	rotation := tsplRotation(field.Rotate)

	buf.WriteString(fmt.Sprintf("QRCODE %d,%d,M,%d,A,%d,\"%s\"\r\n",
		qrX, qrY, cellSize, rotation, escTSPLData(value)))
}

// ════════════════════════════════════════════════════
// Barcode
// ════════════════════════════════════════════════════

func tsplRenderBarcode(buf *bytes.Buffer, field PdfmeField, value string, x, y, w, h, dpi int) {
	if h < 20 {
		h = 20
	}

	btype := "128"
	switch field.Type {
	case "code39":
		btype = "39"
	case "ean13":
		btype = "EAN13"
	case "ean8":
		btype = "EAN8"
	}

	// Fit narrow bar width to field width
	totalMods := len(value)*11 + 35
	narrow := w / totalMods
	if narrow < 1 {
		narrow = 1
	}
	if narrow > 4 {
		narrow = 4
	}

	rotation := tsplRotation(field.Rotate)

	buf.WriteString(fmt.Sprintf("BARCODE %d,%d,\"%s\",%d,1,%d,%d,%d,\"%s\"\r\n",
		x, y, btype, h, rotation, narrow, narrow, escTSPLData(value)))
}

// ════════════════════════════════════════════════════
// Line
// ════════════════════════════════════════════════════

func tsplRenderLine(buf *bytes.Buffer, field PdfmeField, x, y, w, h, dpi int) {
	col := field.Color
	if col == "" {
		col = field.FontColor
	}
	if col != "" && !isColorDarkEnough(col) {
		return
	}

	thickness := h
	if thickness < 1 {
		thickness = 1
	}

	if field.Rotate == 0 {
		// Horizontal: BAR x,y,width,height
		buf.WriteString(fmt.Sprintf("BAR %d,%d,%d,%d\r\n", x, y, w, thickness))
		return
	}

	// Rotated: DIAGONAL x1,y1,x2,y2,thickness
	cx := float64(x) + float64(w)/2
	cy := float64(y) + float64(h)/2
	halfW := float64(w) / 2
	rad := field.Rotate * math.Pi / 180

	x1 := int(math.Round(cx - halfW*math.Cos(rad)))
	y1 := int(math.Round(cy - halfW*math.Sin(rad)))
	x2 := int(math.Round(cx + halfW*math.Cos(rad)))
	y2 := int(math.Round(cy + halfW*math.Sin(rad)))

	// Clamp to non-negative (TDP-244 requirement)
	if x1 < 0 {
		x1 = 0
	}
	if y1 < 0 {
		y1 = 0
	}
	if x2 < 0 {
		x2 = 0
	}
	if y2 < 0 {
		y2 = 0
	}

	buf.WriteString(fmt.Sprintf("DIAGONAL %d,%d,%d,%d,%d\r\n", x1, y1, x2, y2, thickness))
}

// ════════════════════════════════════════════════════
// Rectangle
// ════════════════════════════════════════════════════

func tsplRenderRectangle(buf *bytes.Buffer, field PdfmeField, x, y, w, h, dpi int) {
	fillColor := field.Color
	if fillColor == "" {
		fillColor = field.BackgroundColor
	}

	hasFill := fillColor != "" && isColorDarkEnough(fillColor)
	hasBorder := float64(field.BorderWidth) > 0

	if hasFill {
		// BAR = filled black rectangle
		buf.WriteString(fmt.Sprintf("BAR %d,%d,%d,%d\r\n", x, y, w, h))
	}

	if hasBorder && !hasFill {
		bw := mmToDots(float64(field.BorderWidth), dpi)
		if bw < 1 {
			bw = 1
		}
		// BOX x,y,x_end,y_end,thickness
		buf.WriteString(fmt.Sprintf("BOX %d,%d,%d,%d,%d\r\n", x, y, x+w, y+h, bw))
	}
}

// ════════════════════════════════════════════════════
// Ellipse
// ════════════════════════════════════════════════════

func tsplRenderEllipse(buf *bytes.Buffer, field PdfmeField, x, y, w, h, dpi int) {
	thickness := 1
	if float64(field.BorderWidth) > 0 {
		thickness = mmToDots(float64(field.BorderWidth), dpi)
		if thickness < 1 {
			thickness = 1
		}
	}
	// ELLIPSE x,y,width,height,thickness
	buf.WriteString(fmt.Sprintf("ELLIPSE %d,%d,%d,%d,%d\r\n", x, y, w, h, thickness))
}

// ════════════════════════════════════════════════════
// Image → monochrome BITMAP
// ════════════════════════════════════════════════════

func tsplRenderImage(buf *bytes.Buffer, field PdfmeField, row map[string]string, x, y, w, h, dpi int) {
	content := ""
	if val, ok := row[field.Name]; ok && val != "" {
		content = val
	}
	if content == "" {
		for _, v := range field.Variables {
			if val, ok := row[v]; ok && val != "" {
				content = val
				break
			}
		}
	}
	if content == "" {
		content = field.Content
	}
	if content == "" {
		return
	}

	img := decodeImageForTSPL(content)
	if img == nil {
		log.Printf("[tspl] Could not decode image for field %q", field.Name)
		return
	}

	// Scale to target size
	scaled := scaleImageNearest(img, w, h)

	// Convert to monochrome and write BITMAP command
	tsplWriteBitmap(buf, scaled, x, y)
}

// decodeImageForTSPL decodes an image from data URI, raw base64, URL, or file path.
func decodeImageForTSPL(content string) image.Image {
	// Data URI: data:image/png;base64,iVBOR...
	if strings.HasPrefix(content, "data:image/") {
		idx := strings.Index(content, ",")
		if idx < 0 {
			return nil
		}
		data, err := base64.StdEncoding.DecodeString(content[idx+1:])
		if err != nil {
			data, err = base64.RawStdEncoding.DecodeString(content[idx+1:])
			if err != nil {
				return nil
			}
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return nil
		}
		return img
	}

	// Raw base64 (long string, not a URL or path)
	if len(content) > 100 && !strings.HasPrefix(content, "http") && !strings.Contains(content[:20], "/") {
		data, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			data, err = base64.RawStdEncoding.DecodeString(content)
			if err != nil {
				return nil
			}
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return nil
		}
		return img
	}

	// HTTP URL
	if strings.HasPrefix(content, "http") {
		resp, err := http.Get(content)
		if err != nil {
			return nil
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil
		}
		img, _, err := image.Decode(resp.Body)
		if err != nil {
			return nil
		}
		return img
	}

	// Local file
	f, err := os.Open(content)
	if err != nil {
		return nil
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil
	}
	return img
}

// scaleImageNearest resizes using nearest-neighbor (fast, good for logos/text).
func scaleImageNearest(src image.Image, targetW, targetH int) image.Image {
	srcB := src.Bounds()
	srcW, srcH := srcB.Dx(), srcB.Dy()
	if srcW == targetW && srcH == targetH {
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	for dy := 0; dy < targetH; dy++ {
		for dx := 0; dx < targetW; dx++ {
			sx := dx * srcW / targetW
			sy := dy * srcH / targetH
			dst.Set(dx, dy, src.At(srcB.Min.X+sx, srcB.Min.Y+sy))
		}
	}
	return dst
}

// tsplWriteBitmap converts an image to monochrome 1-bit and writes TSPL BITMAP command.
// TSPL format: BITMAP x,y,widthBytes,height,mode,<raw binary>
// Bit convention: 1 = black (print), 0 = white (no print).
func tsplWriteBitmap(buf *bytes.Buffer, img image.Image, x, y int) {
	bounds := img.Bounds()
	imgW, imgH := bounds.Dx(), bounds.Dy()
	if imgW == 0 || imgH == 0 {
		return
	}

	widthBytes := (imgW + 7) / 8
	data := make([]byte, widthBytes*imgH)

	for row := 0; row < imgH; row++ {
		for col := 0; col < imgW; col++ {
			r, g, b, a := img.At(bounds.Min.X+col, bounds.Min.Y+row).RGBA()
			r8, g8, b8, a8 := r>>8, g>>8, b>>8, a>>8
			lum := 0.299*float64(r8) + 0.587*float64(g8) + 0.114*float64(b8)
			if lum < 128 && a8 > 128 {
				idx := row*widthBytes + col/8
				bit := uint(7 - col%8)
				data[idx] |= 1 << bit
			}
		}
	}

	// BITMAP x,y,widthBytes,height,mode,data
	buf.WriteString(fmt.Sprintf("BITMAP %d,%d,%d,%d,0,", x, y, widthBytes, imgH))
	buf.Write(data)
	buf.WriteString("\r\n")

	log.Printf("[tspl] BITMAP %dx%d (%d bytes) at %d,%d", imgW, imgH, len(data), x, y)
}

// ════════════════════════════════════════════════════
// Full raster mode: render entire page as bitmap
// ════════════════════════════════════════════════════

// RenderBulkTSPLRaster renders each page as a full monochrome bitmap.
// Maximum fidelity — reproduces all visual elements exactly.
// Larger payload and slower printing than native mode.
func RenderBulkTSPLRaster(schema *PdfmeSchema, rows []map[string]string, dpi int, copies int) []byte {
	if dpi <= 0 {
		dpi = defaultDPI
	}
	if copies < 1 {
		copies = 1
	}

	var buf bytes.Buffer

	// ESC !R init
	buf.Write([]byte{0x1b, 0x21, 0x52})
	buf.WriteString("\r\n")

	labelW := schema.BasePdf.Width
	labelH := schema.BasePdf.Height

	buf.WriteString(fmt.Sprintf("SIZE %.1f mm, %.1f mm\r\n", labelW, labelH))
	buf.WriteString("GAP 3 mm, 0 mm\r\n")
	buf.WriteString("DIRECTION 0,0\r\n")
	buf.WriteString("SPEED 3\r\n")
	buf.WriteString("DENSITY 10\r\n")
	buf.WriteString("SET CUTTER OFF\r\n")
	buf.WriteString("SET TEAR ON\r\n")

	for _, row := range rows {
		for pi := range schema.Schemas {
			buf.WriteString("CLS\r\n")
			img := rasterizePage(schema, row, pi, dpi)
			tsplWriteBitmap(&buf, img, 0, 0)
			buf.WriteString(fmt.Sprintf("PRINT %d\r\n", copies))
		}
	}

	log.Printf("[tspl-raster] Generated %d bytes for %d rows", buf.Len(), len(rows))
	return buf.Bytes()
}

// rasterizePage renders a single schema page as a monochrome *image.Gray.
func rasterizePage(schema *PdfmeSchema, row map[string]string, pageIndex int, dpi int) image.Image {
	w := mmToDots(schema.BasePdf.Width, dpi)
	h := mmToDots(schema.BasePdf.Height, dpi)

	img := image.NewGray(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)

	if pageIndex >= len(schema.Schemas) {
		return img
	}

	black := color.Gray{Y: 0}

	for _, field := range schema.Schemas[pageIndex] {
		if field.Opacity > 0 && field.Opacity < 0.25 {
			continue
		}

		value := resolveFieldValue(field, row)
		fx := mmToDots(field.Position.X, dpi)
		fy := mmToDots(field.Position.Y, dpi)
		fw := mmToDots(field.Width, dpi)
		fh := mmToDots(field.Height, dpi)

		switch field.Type {
		case "line":
			rasterLine(img, field, fx, fy, fw, fh, black)
		case "rectangle":
			rasterRect(img, field, fx, fy, fw, fh, black)
		case "qrcode":
			if value == "" {
				value = field.Content
			}
			if value != "" {
				rasterQR(img, value, fx, fy, fw, fh, black)
			}
		case "image":
			rasterImage(img, field, row, fx, fy, fw, fh)
		case "text", "multiVariableText":
			if value == "" {
				value = field.Content
			}
			if value != "" {
				rasterText(img, value, field, fx, fy, fw, fh, black, dpi)
			}
		}
	}

	return img
}

func rasterLine(img *image.Gray, field PdfmeField, x, y, w, h int, c color.Gray) {
	col := field.Color
	if col == "" {
		col = field.FontColor
	}
	if col != "" && !isColorDarkEnough(col) {
		return
	}

	bounds := img.Bounds()
	thick := h
	if thick < 1 {
		thick = 1
	}

	if field.Rotate == 0 {
		for dy := 0; dy < thick; dy++ {
			py := y + dy
			if py < 0 || py >= bounds.Max.Y {
				continue
			}
			for dx := 0; dx < w; dx++ {
				px := x + dx
				if px >= 0 && px < bounds.Max.X {
					img.SetGray(px, py, c)
				}
			}
		}
		return
	}

	// Rotated line
	cx := float64(x) + float64(w)/2
	cy := float64(y) + float64(h)/2
	halfW := float64(w) / 2
	rad := field.Rotate * math.Pi / 180

	x1 := cx - halfW*math.Cos(rad)
	y1 := cy - halfW*math.Sin(rad)
	x2 := cx + halfW*math.Cos(rad)
	y2 := cy + halfW*math.Sin(rad)

	steps := int(math.Max(math.Abs(x2-x1), math.Abs(y2-y1))) + 1
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		px := x1 + t*(x2-x1)
		py := y1 + t*(y2-y1)
		for d := -thick / 2; d <= thick/2; d++ {
			ix := int(math.Round(px + float64(d)*math.Sin(rad)))
			iy := int(math.Round(py - float64(d)*math.Cos(rad)))
			if ix >= 0 && ix < bounds.Max.X && iy >= 0 && iy < bounds.Max.Y {
				img.SetGray(ix, iy, c)
			}
		}
	}
}

// rasterText draws text onto the monochrome raster image using basicfont.
// Uses field.FontSize (in points) to determine scale, with word-wrapping.
func rasterText(img *image.Gray, text string, field PdfmeField, x, y, w, h int, c color.Gray, dpi int) {
	col := field.FontColor
	if col == "" {
		col = field.Color
	}
	if col != "" && !isColorDarkEnough(col) {
		return
	}

	// Determine scale from pdfme font size (in points), NOT field height
	baseFontH := 13 // basicfont.Face7x13 pixel height
	baseFontW := 7  // basicfont.Face7x13 character width

	fontSize := field.FontSize
	if fontSize == 0 && field.DynamicFontSize != nil {
		fontSize = field.DynamicFontSize.Max
	}
	if fontSize == 0 {
		fontSize = 13 // default ~13pt
	}

	targetH := ptToDots(fontSize, dpi)
	scale := targetH / baseFontH
	if scale < 1 {
		scale = 1
	}
	if scale > 6 {
		scale = 6
	}

	face := basicfont.Face7x13
	bounds := img.Bounds()
	charW := baseFontW * scale

	// Word-wrap: split text into lines that fit within field width
	maxCharsPerLine := w / charW
	if maxCharsPerLine < 1 {
		maxCharsPerLine = 1
	}
	lines := wrapTextLines(text, maxCharsPerLine)

	lineH := (baseFontH + 2) * scale // line height with spacing
	totalTextH := len(lines) * lineH

	// Vertical alignment
	startY := y
	valign := strings.ToLower(field.VerticalAlignment)
	if valign == "middle" || valign == "" {
		startY = y + (h-totalTextH)/2
	} else if valign == "bottom" {
		startY = y + h - totalTextH
	}

	for lineIdx, line := range lines {
		if line == "" {
			continue
		}
		lineY := startY + lineIdx*lineH
		if lineY+lineH < y || lineY > y+h {
			continue // skip lines outside field
		}

		// Measure line width
		adv := font.MeasureString(face, line)
		textW := adv.Ceil()
		if textW == 0 {
			continue
		}

		// Render line at 1x into temp image
		tmpImg := image.NewGray(image.Rect(0, 0, textW, baseFontH+2))
		draw.Draw(tmpImg, tmpImg.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)

		d := &font.Drawer{
			Dst:  tmpImg,
			Src:  image.NewUniform(c),
			Face: face,
			Dot:  fixed.P(0, baseFontH),
		}
		d.DrawString(line)

		scaledW := textW * scale

		// Horizontal alignment
		ox := x
		align := strings.ToLower(field.Alignment)
		if align == "center" {
			ox = x + (w-scaledW)/2
		} else if align == "right" {
			ox = x + w - scaledW
		}

		// Blit scaled pixels, clipped to field bounds
		for sy := 0; sy < baseFontH+2; sy++ {
			for sx := 0; sx < textW; sx++ {
				pixel := tmpImg.GrayAt(sx, sy)
				if pixel.Y < 128 { // dark pixel
					for dy := 0; dy < scale; dy++ {
						for dx := 0; dx < scale; dx++ {
							px := ox + sx*scale + dx
							py := lineY + sy*scale + dy
							if px >= x && px < x+w && py >= y && py < y+h &&
								px >= 0 && px < bounds.Max.X && py >= 0 && py < bounds.Max.Y {
								img.SetGray(px, py, c)
							}
						}
					}
				}
			}
		}
	}
}

// wrapTextLines splits text into lines that fit within maxChars characters.
func wrapTextLines(text string, maxChars int) []string {
	if len(text) <= maxChars {
		return []string{text}
	}
	words := strings.Fields(text)
	var lines []string
	current := ""
	for _, word := range words {
		if current == "" {
			current = word
		} else if len(current)+1+len(word) <= maxChars {
			current += " " + word
		} else {
			lines = append(lines, current)
			current = word
		}
		// Break long words
		for len(current) > maxChars {
			lines = append(lines, current[:maxChars])
			current = current[maxChars:]
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func rasterRect(img *image.Gray, field PdfmeField, x, y, w, h int, c color.Gray) {
	fillColor := field.Color
	if fillColor == "" {
		fillColor = field.BackgroundColor
	}
	bounds := img.Bounds()

	if fillColor != "" && isColorDarkEnough(fillColor) {
		for dy := 0; dy < h; dy++ {
			py := y + dy
			if py < 0 || py >= bounds.Max.Y {
				continue
			}
			for dx := 0; dx < w; dx++ {
				px := x + dx
				if px >= 0 && px < bounds.Max.X {
					img.SetGray(px, py, c)
				}
			}
		}
	}

	bw := int(math.Round(float64(field.BorderWidth)))
	if bw > 0 {
		for dx := 0; dx < w; dx++ {
			px := x + dx
			if px < 0 || px >= bounds.Max.X {
				continue
			}
			for t := 0; t < bw; t++ {
				if y+t >= 0 && y+t < bounds.Max.Y {
					img.SetGray(px, y+t, c)
				}
				if y+h-1-t >= 0 && y+h-1-t < bounds.Max.Y {
					img.SetGray(px, y+h-1-t, c)
				}
			}
		}
		for dy := 0; dy < h; dy++ {
			py := y + dy
			if py < 0 || py >= bounds.Max.Y {
				continue
			}
			for t := 0; t < bw; t++ {
				if x+t >= 0 && x+t < bounds.Max.X {
					img.SetGray(x+t, py, c)
				}
				if x+w-1-t >= 0 && x+w-1-t < bounds.Max.X {
					img.SetGray(x+w-1-t, py, c)
				}
			}
		}
	}
}

func rasterQR(img *image.Gray, value string, x, y, w, h int, c color.Gray) {
	qr, err := goqrcode.New(value, goqrcode.Medium)
	if err != nil {
		return
	}
	qr.DisableBorder = true
	bmap := qr.Bitmap()
	modules := len(bmap)
	if modules == 0 {
		return
	}

	dim := w
	if h < w {
		dim = h
	}
	cell := dim / modules
	if cell < 1 {
		cell = 1
	}

	bounds := img.Bounds()
	ox := x + (w-cell*modules)/2
	oy := y + (h-cell*modules)/2

	for mr := 0; mr < modules; mr++ {
		for mc := 0; mc < modules; mc++ {
			if !bmap[mr][mc] {
				continue
			}
			for dy := 0; dy < cell; dy++ {
				for dx := 0; dx < cell; dx++ {
					px := ox + mc*cell + dx
					py := oy + mr*cell + dy
					if px >= 0 && px < bounds.Max.X && py >= 0 && py < bounds.Max.Y {
						img.SetGray(px, py, c)
					}
				}
			}
		}
	}
}

func rasterImage(img *image.Gray, field PdfmeField, row map[string]string, x, y, w, h int) {
	content := ""
	if val, ok := row[field.Name]; ok && val != "" {
		content = val
	}
	if content == "" {
		for _, v := range field.Variables {
			if val, ok := row[v]; ok && val != "" {
				content = val
				break
			}
		}
	}
	if content == "" {
		content = field.Content
	}
	if content == "" {
		return
	}

	src := decodeImageForTSPL(content)
	if src == nil {
		return
	}

	bounds := img.Bounds()
	srcB := src.Bounds()
	srcW, srcH := srcB.Dx(), srcB.Dy()

	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			px, py := x+dx, y+dy
			if px < 0 || px >= bounds.Max.X || py < 0 || py >= bounds.Max.Y {
				continue
			}
			sx := dx * srcW / w
			sy := dy * srcH / h
			r, g, b, a := src.At(srcB.Min.X+sx, srcB.Min.Y+sy).RGBA()
			lum := 0.299*float64(r>>8) + 0.587*float64(g>>8) + 0.114*float64(b>>8)
			if lum < 128 && (a>>8) > 128 {
				img.SetGray(px, py, color.Gray{Y: 0})
			}
		}
	}
}
