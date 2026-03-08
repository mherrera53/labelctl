package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/code128"
	"github.com/boombuler/barcode/ean"
	"github.com/signintech/gopdf"
	goqrcode "github.com/skip2/go-qrcode"
)

const mmToPt = 2.835 // 1mm = 2.835 PDF points

// ════════════════════════════════════════════════════
// Schema types
// ════════════════════════════════════════════════════

// PdfmeSchema represents a parsed pdfme template schema (v4 or v5).
type PdfmeSchema struct {
	Schemas [][]PdfmeField `json:"schemas"` // pages → fields
	BasePdf PdfmeBasePdf   `json:"basePdf"`
}

// PdfmeBasePdf is the page dimensions and optional background PDF.
type PdfmeBasePdf struct {
	Width      float64   `json:"width"`  // mm
	Height     float64   `json:"height"` // mm
	Padding    []float64 `json:"padding"`
	BackgroundPdf string // base64-encoded PDF to use as page background (decoded from basePdf string)
}

// PdfmeField is a single field in the schema.
type PdfmeField struct {
	Name              string          `json:"name"`
	Type              string          `json:"type"`
	Position          PdfmePos        `json:"position"`
	Width             float64         `json:"width"`  // mm
	Height            float64         `json:"height"` // mm
	FontSize          float64         `json:"fontSize,omitempty"`
	FontName          string          `json:"fontName,omitempty"`
	FontWeight        string          `json:"fontWeight,omitempty"` // "bold"
	Alignment         string          `json:"alignment,omitempty"`
	VerticalAlignment string          `json:"verticalAlignment,omitempty"` // top | middle | bottom
	FontColor         string          `json:"fontColor,omitempty"`
	BackgroundColor   string          `json:"backgroundColor,omitempty"`
	BorderWidth       jsonFloat       `json:"borderWidth,omitempty"`
	BorderColor       string          `json:"borderColor,omitempty"`
	LineHeight        float64         `json:"lineHeight,omitempty"`
	Content           string          `json:"content,omitempty"`
	Text              string          `json:"text,omitempty"`
	Variables         []string        `json:"variables,omitempty"`
	DynamicFontSize   *DynFontSize    `json:"dynamicFontSize,omitempty"`
	Rotate            float64         `json:"rotate,omitempty"`
	Opacity           float64         `json:"opacity,omitempty"`
	Color             string          `json:"color,omitempty"` // for line type
	// Table-specific
	Head                 []string        `json:"head,omitempty"`
	HeadWidthPercentages []float64       `json:"headWidthPercentages,omitempty"`
	ShowHead             *bool           `json:"showHead,omitempty"`
	TableStyles          json.RawMessage `json:"tableStyles,omitempty"`
	HeadStyles           json.RawMessage `json:"headStyles,omitempty"`
	BodyStyles           json.RawMessage `json:"bodyStyles,omitempty"`
	// Rectangle-specific
	Radius float64 `json:"radius,omitempty"`
	// QR-specific: color finder patterns differently (custom extension, compatible with readers)
	QrFinderColor string `json:"qrFinderColor,omitempty"`
	// Text decoration
	CharacterSpacing float64 `json:"characterSpacing,omitempty"`
	Underline        bool    `json:"underline,omitempty"`
	Strikethrough    bool    `json:"strikethrough,omitempty"`
	// Layout
	Padding jsonPadding `json:"padding,omitempty"`
}

// DynFontSize controls auto-sizing for text fields.
type DynFontSize struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
	Fit string  `json:"fit"` // "vertical" | "horizontal"
}

// jsonFloat handles borderWidth being either a number or an object.
type jsonFloat float64

func (jf *jsonFloat) UnmarshalJSON(b []byte) error {
	var f float64
	if err := json.Unmarshal(b, &f); err == nil {
		*jf = jsonFloat(f)
		return nil
	}
	// borderWidth can be {top,right,bottom,left} — take top as representative
	var obj map[string]float64
	if err := json.Unmarshal(b, &obj); err == nil {
		if v, ok := obj["top"]; ok {
			*jf = jsonFloat(v)
		}
		return nil
	}
	*jf = 0
	return nil
}

// jsonPadding handles padding as a number or [top,right,bottom,left] array.
type jsonPadding struct {
	Top, Right, Bottom, Left float64
}

func (jp *jsonPadding) UnmarshalJSON(b []byte) error {
	var n float64
	if err := json.Unmarshal(b, &n); err == nil {
		jp.Top, jp.Right, jp.Bottom, jp.Left = n, n, n, n
		return nil
	}
	var arr []float64
	if err := json.Unmarshal(b, &arr); err == nil {
		switch len(arr) {
		case 1:
			jp.Top, jp.Right, jp.Bottom, jp.Left = arr[0], arr[0], arr[0], arr[0]
		case 2:
			jp.Top, jp.Bottom = arr[0], arr[0]
			jp.Right, jp.Left = arr[1], arr[1]
		case 3:
			jp.Top = arr[0]
			jp.Right, jp.Left = arr[1], arr[1]
			jp.Bottom = arr[2]
		case 4:
			jp.Top, jp.Right, jp.Bottom, jp.Left = arr[0], arr[1], arr[2], arr[3]
		}
		return nil
	}
	return nil
}

// PdfmePos is x,y in mm.
type PdfmePos struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// TableStyle holds parsed table style properties.
type TableStyle struct {
	FontSize        float64 `json:"fontSize"`
	FontColor       string  `json:"fontColor"`
	FontName        string  `json:"fontName"`
	BackgroundColor string  `json:"backgroundColor"`
	Alignment       string  `json:"alignment"`
	BorderColor     string  `json:"borderColor"`
}

var (
	imgCache   = map[string]string{}
	imgCacheMu sync.Mutex
)

// ════════════════════════════════════════════════════
// Schema parsing — v4 and v5 with all basePdf formats
// ════════════════════════════════════════════════════

// ParsePdfmeSchema parses raw JSON into PdfmeSchema.
// Supports:
//   - v5: schemas: [[{name:"...", type:"...", ...}, ...]]
//   - v4: schemas: [{"fieldName": {type:"...", ...}, ...}]
//   - basePdf: {width, height, padding} | "BLANK" | "data:application/pdf;base64,..." | "JVBERi0x..."
//   - Multi-page: multiple entries in schemas array
func ParsePdfmeSchema(raw json.RawMessage) (*PdfmeSchema, error) {
	// First extract basePdf (can be object, string, or missing)
	basePdf := parseBasePdf(raw)

	// Try v5 format: schemas is array of arrays
	var v5 struct {
		Schemas [][]PdfmeField `json:"schemas"`
	}
	if err := json.Unmarshal(raw, &v5); err == nil && len(v5.Schemas) > 0 && len(v5.Schemas[0]) > 0 {
		schema := &PdfmeSchema{Schemas: v5.Schemas, BasePdf: basePdf}
		applyBasePdfDefaults(schema)
		log.Printf("[pdf] Parsed v5 schema: %d pages, %d fields on page 0", len(schema.Schemas), len(schema.Schemas[0]))
		return schema, nil
	}

	// Try v4 format: schemas is array of keyed objects.
	// IMPORTANT: Must preserve JSON key order — it defines the z-order (layer order).
	// Go maps lose insertion order, so we use json.Decoder to iterate keys in order.
	var v4raw struct {
		Schemas []json.RawMessage `json:"schemas"`
	}
	if err := json.Unmarshal(raw, &v4raw); err != nil || len(v4raw.Schemas) == 0 {
		return nil, fmt.Errorf("parse pdfme schema: no valid schemas found")
	}

	schema := &PdfmeSchema{BasePdf: basePdf}
	schema.Schemas = make([][]PdfmeField, len(v4raw.Schemas))
	for i, pageRaw := range v4raw.Schemas {
		schema.Schemas[i] = parseV4PageOrdered(pageRaw, i)
	}

	applyBasePdfDefaults(schema)
	totalFields := 0
	for _, p := range schema.Schemas {
		totalFields += len(p)
	}
	log.Printf("[pdf] Parsed v4 schema: %d pages, %d total fields", len(schema.Schemas), totalFields)
	return schema, nil
}

// parseV4PageOrdered parses a single v4 schema page, preserving JSON key order (= z-order).
// Two-phase approach:
//   Phase 1: Use json.Decoder (Token for keys, Decode to skip values) to extract key order.
//   Phase 2: Unmarshal into map for reliable field parsing.
func parseV4PageOrdered(pageRaw json.RawMessage, pageIndex int) []PdfmeField {
	// Phase 1: Extract ordered keys
	dec := json.NewDecoder(bytes.NewReader(pageRaw))
	t, err := dec.Token() // opening {
	if err != nil || t != json.Delim('{') {
		log.Printf("[pdf] Warning: page %d is not a JSON object", pageIndex)
		return nil
	}

	var keyOrder []string
	for dec.More() {
		// Read key
		t, err := dec.Token()
		if err != nil {
			break
		}
		key, ok := t.(string)
		if !ok {
			break
		}
		keyOrder = append(keyOrder, key)
		// Skip value (Decode consumes one complete JSON value)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			log.Printf("[pdf] Warning: could not skip value for key %q on page %d: %v", key, pageIndex, err)
			break
		}
	}

	// Phase 2: Unmarshal full map for reliable field values
	var fieldMap map[string]json.RawMessage
	if err := json.Unmarshal(pageRaw, &fieldMap); err != nil {
		log.Printf("[pdf] Warning: could not unmarshal page %d as map: %v", pageIndex, err)
		return nil
	}

	// Build fields in key order (= z-order from JSON)
	var fields []PdfmeField
	for _, key := range keyOrder {
		if strings.HasPrefix(key, "_") {
			continue
		}
		raw, ok := fieldMap[key]
		if !ok {
			continue
		}
		var field PdfmeField
		if err := json.Unmarshal(raw, &field); err != nil {
			log.Printf("[pdf] Warning: skip field %q on page %d: %v", key, pageIndex, err)
			continue
		}
		field.Name = key
		fields = append(fields, field)
	}

	log.Printf("[pdf] Page %d: parsed %d fields in z-order (keys: %d, map entries: %d)",
		pageIndex, len(fields), len(keyOrder), len(fieldMap))

	return fields
}

// parseBasePdf handles all basePdf formats: object, "BLANK", base64 PDF string.
func parseBasePdf(raw json.RawMessage) PdfmeBasePdf {
	var wrapper struct {
		BasePdf json.RawMessage `json:"basePdf"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil || wrapper.BasePdf == nil {
		return PdfmeBasePdf{} // will get defaults
	}

	// Try as dimension object
	var bp PdfmeBasePdf
	if err := json.Unmarshal(wrapper.BasePdf, &bp); err == nil && (bp.Width > 0 || bp.Height > 0) {
		return bp
	}

	// Try as string ("BLANK" or base64 PDF)
	var s string
	if err := json.Unmarshal(wrapper.BasePdf, &s); err == nil {
		if s == "BLANK" || s == "" {
			return PdfmeBasePdf{Width: 210, Height: 297} // A4
		}
		// base64 PDF — store it for use as background, default page size to A4
		return PdfmeBasePdf{Width: 210, Height: 297, BackgroundPdf: s}
	}

	// Also check for pageConfig (ISI custom format)
	var pcWrapper struct {
		PageConfig struct {
			Width       float64 `json:"width"`
			Height      float64 `json:"height"`
			Size        string  `json:"size"`
			Orientation string  `json:"orientation"`
		} `json:"pageConfig"`
	}
	if err := json.Unmarshal(raw, &pcWrapper); err == nil && pcWrapper.PageConfig.Width > 0 {
		w := pcWrapper.PageConfig.Width
		h := pcWrapper.PageConfig.Height
		// Some templates use cm instead of mm (width=21 instead of 210)
		if w < 100 {
			w *= 10
			h *= 10
		}
		return PdfmeBasePdf{Width: w, Height: h}
	}

	return PdfmeBasePdf{} // will get defaults
}

func applyBasePdfDefaults(schema *PdfmeSchema) {
	if schema.BasePdf.Width == 0 {
		schema.BasePdf.Width = 210 // A4 default
	}
	if schema.BasePdf.Height == 0 {
		schema.BasePdf.Height = 297 // A4 default
	}
}

// ════════════════════════════════════════════════════
// Bulk PDF rendering — multi-page template support
// ════════════════════════════════════════════════════

// RenderBulkPDF generates a multi-page PDF from rows of data using a pdfme schema.
// For multi-page templates, each row produces N pages (one per schema page).
func RenderBulkPDF(schema *PdfmeSchema, rows []map[string]string, outputPath string) error {
	pdf := gopdf.GoPdf{}

	pageW := schema.BasePdf.Width * mmToPt
	pageH := schema.BasePdf.Height * mmToPt

	pdf.Start(gopdf.Config{
		PageSize: gopdf.Rect{W: pageW, H: pageH},
	})

	// Reset font registry for this render
	fontRegistryMu.Lock()
	fontRegistry = map[string]bool{}
	fontRegistryMu.Unlock()

	// Load default fonts
	fontPath, boldFontPath := findFonts()
	fontLoaded := false
	if fontPath != "" {
		if err := pdf.AddTTFFont("default", fontPath); err != nil {
			log.Printf("[pdf] Warning: could not load font %s: %v", fontPath, err)
		} else {
			fontLoaded = true
			fontRegistry["default"] = true
			log.Printf("[pdf] Font loaded: %s", fontPath)
		}
	}
	if boldFontPath != "" {
		if err := pdf.AddTTFFont("bold", boldFontPath); err != nil {
			log.Printf("[pdf] Warning: could not load bold font %s: %v", boldFontPath, err)
		} else {
			fontRegistry["bold"] = true
			log.Printf("[pdf] Bold font loaded: %s", boldFontPath)
		}
	}
	if !fontLoaded {
		log.Printf("[pdf] WARNING: No font loaded — text fields will be empty.")
	}

	// Pre-load named fonts referenced in schema fields
	loadedFontNames := map[string]bool{}
	for _, page := range schema.Schemas {
		for _, field := range page {
			if field.FontName != "" && !loadedFontNames[field.FontName] {
				loadedFontNames[field.FontName] = true
				loadNamedFont(&pdf, field.FontName)
			}
		}
	}

	// Import background PDF template if present (basePdf as base64 PDF)
	bgTplID := -1
	var bgTmpFile string
	if schema.BasePdf.BackgroundPdf != "" {
		bgTmpFile = decodeBackgroundPdf(schema.BasePdf.BackgroundPdf)
		if bgTmpFile != "" {
			bgTplID = pdf.ImportPage(bgTmpFile, 1, "/MediaBox")
			log.Printf("[pdf] Background PDF imported from base64 (tplID=%d)", bgTplID)
		}
	}
	defer func() {
		if bgTmpFile != "" {
			os.Remove(bgTmpFile)
		}
	}()

	for ri, row := range rows {
		if ri == 0 {
			log.Printf("[pdf] === Row 0 data keys: %v", mapKeys(row))
		}

		// Render ALL pages from the template for each row
		for pi, pageFields := range schema.Schemas {
			pdf.AddPage()

			// Draw background PDF on every page
			if bgTplID >= 0 {
				pdf.UseImportedTemplate(bgTplID, 0, 0, pageW, pageH)
			}

			for _, field := range pageFields {
				value := resolveFieldValue(field, row)
				if ri == 0 && pi == 0 {
					log.Printf("[pdf] Field %q type=%s text=%q vars=%v → value=%q",
						field.Name, field.Type, truncate(field.Text, 50), field.Variables, truncate(value, 60))
				}

				x := field.Position.X * mmToPt
				y := field.Position.Y * mmToPt
				w := field.Width * mmToPt
				h := field.Height * mmToPt

				// Apply opacity by blending colors with white (gopdf SetTransparency is unreliable).
				// On white paper, blended color == transparent color visually.
				if field.Opacity > 0 && field.Opacity < 1 {
					op := field.Opacity
					field.Color = blendColorWithWhite(field.Color, op)
					field.FontColor = blendColorWithWhite(field.FontColor, op)
					field.BorderColor = blendColorWithWhite(field.BorderColor, op)
					field.BackgroundColor = blendColorWithWhite(field.BackgroundColor, op)
				}

				// Apply rotation around field center
				hasRotation := field.Rotate != 0
				if hasRotation {
					cx := x + w/2
					cy := y + h/2
					pdf.Rotate(field.Rotate, cx, cy)
				}

				// Render background/border first (for any type)
				if field.BackgroundColor != "" || float64(field.BorderWidth) > 0 {
					renderFieldBackground(&pdf, field, x, y, w, h)
				}

				switch field.Type {
				case "text", "multiVariableText":
					if value == "" {
						goto fieldDone
					}
					renderTextField(&pdf, field, value, x, y, w, h, fontLoaded)
				case "qrcode":
					log.Printf("[pdf] QR CASE: field=%q value=%q content=%q opacity=%.2f w=%.1f h=%.1f",
						field.Name, value, field.Content, field.Opacity, w, h)
					if value == "" {
						value = field.Content
					}
					if value == "" {
						value = field.Name
						log.Printf("[pdf] QR using field name as placeholder: %q", value)
					}
					renderQRField(&pdf, field, value, x, y, w, h)
					log.Printf("[pdf] QR rendered OK: field=%q", field.Name)
				case "image":
					renderImageField(&pdf, field, row, x, y, w, h)
				case "barcode", "code128", "code39", "ean13", "ean8":
					if value == "" {
						goto fieldDone
					}
					renderBarcodeField(&pdf, value, x, y, w, h)
				case "table":
					renderTableField(&pdf, field, row, x, y, w, h, fontLoaded)
				case "line":
					renderLineField(&pdf, field, x, y, w, h)
				case "rectangle":
					renderRectangleField(&pdf, field, x, y, w, h)
				case "ellipse":
					renderEllipseField(&pdf, field, x, y, w, h)
				}

			fieldDone:
				// Clear rotation
				if hasRotation {
					pdf.RotateReset()
				}
			}
		}
	}

	return pdf.WritePdf(outputPath)
}

// ════════════════════════════════════════════════════
// Field rendering
// ════════════════════════════════════════════════════

func renderFieldBackground(pdf *gopdf.GoPdf, field PdfmeField, x, y, w, h float64) {
	if field.BackgroundColor != "" {
		r, g, b, _ := parseColor(field.BackgroundColor)
		pdf.SetFillColor(r, g, b)
		pdf.RectFromUpperLeftWithStyle(x, y, w, h, "F")
	}
	// borderWidth is in mm, SetLineWidth expects pt
	bw := float64(field.BorderWidth) * mmToPt
	if bw > 0 {
		color := field.BorderColor
		if color == "" {
			color = "#000000"
		}
		r, g, b, _ := parseColor(color)
		pdf.SetStrokeColor(r, g, b)
		pdf.SetLineWidth(bw)
		pdf.RectFromUpperLeftWithStyle(x, y, w, h, "D")
	}
}

func renderTextField(pdf *gopdf.GoPdf, field PdfmeField, value string, x, y, w, h float64, fontLoaded bool) {
	if !fontLoaded {
		return
	}

	// Apply padding — shrink effective area
	px, py, pw, ph := x, y, w, h
	pad := field.Padding
	if pad.Top > 0 || pad.Right > 0 || pad.Bottom > 0 || pad.Left > 0 {
		padT := pad.Top * mmToPt
		padR := pad.Right * mmToPt
		padB := pad.Bottom * mmToPt
		padL := pad.Left * mmToPt
		px += padL
		py += padT
		pw -= padL + padR
		ph -= padT + padB
		if pw < 0 {
			pw = 0
		}
		if ph < 0 {
			ph = 0
		}
	}

	// Resolve font family FIRST (needed for dynamic font size measurement)
	fontFamily := "default"
	boldFamily := "bold"
	if field.FontName != "" {
		fam, _ := loadNamedFont(pdf, field.FontName)
		fontFamily = fam
		boldFamily = fam + "_bold"
	}

	// Active font = bold or regular
	activeFontFamily := fontFamily
	if field.FontWeight == "bold" {
		activeFontFamily = boldFamily
	}

	// Line height multiplier
	lh := field.LineHeight
	if lh <= 0 {
		lh = 1.4
	}

	fontSize := field.FontSize
	if fontSize == 0 {
		fontSize = 10
	}

	// Dynamic font size: try to fit text within bounds (uses correct font for measuring)
	if field.DynamicFontSize != nil && field.DynamicFontSize.Max > 0 {
		fontSize = calculateDynamicFontSize(pdf, value, pw, ph, field.DynamicFontSize, activeFontFamily, lh)
	}

	// Set the active font with style flags
	// gopdf style flags: Regular=0, Bold=2, Underline=4
	styleFlag := gopdf.Regular
	if field.Underline {
		styleFlag |= gopdf.Underline
	}

	if styleFlag != gopdf.Regular {
		if err := pdf.SetFontWithStyle(activeFontFamily, styleFlag, fontSize); err != nil {
			pdf.SetFont(activeFontFamily, "", fontSize)
		}
	} else {
		if err := pdf.SetFont(activeFontFamily, "", fontSize); err != nil {
			pdf.SetFont("default", "", fontSize)
		}
	}

	// Character spacing
	if field.CharacterSpacing != 0 {
		pdf.SetCharSpacing(field.CharacterSpacing)
	}

	// Font color
	if field.FontColor != "" {
		r, g, b, _ := parseColor(field.FontColor)
		pdf.SetTextColor(r, g, b)
	} else {
		pdf.SetTextColor(0, 0, 0)
	}

	// lineSpacingPt: vertical space per line in PDF points
	// fontSize is in points, lineHeight is a multiplier → result is in points
	lineSpacingPt := fontSize * lh

	// Word-wrap text into lines that fit within the available width
	lines := wrapTextByWord(pdf, value, pw)
	totalTextH := float64(len(lines)) * lineSpacingPt

	// Vertical alignment (start position)
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
	// default "top": textY = py
	}

	pdf.SetX(px)
	pdf.SetY(textY)

	for li, line := range lines {
		if li > 0 {
			textY += lineSpacingPt
			pdf.SetY(textY)
		}
		// Don't render lines that overflow the padded area
		if textY > py+ph {
			break
		}

		// Horizontal alignment (start position)
		lineX := px
		textW, _ := pdf.MeasureTextWidth(line)
		switch field.Alignment {
		case "center":
			if textW < pw {
				lineX = px + (pw-textW)/2
			}
		case "right":
			if textW < pw {
				lineX = px + pw - textW
			}
		// default "left": lineX = px
		}
		pdf.SetX(lineX)
		pdf.CellWithOption(&gopdf.Rect{W: pw, H: lineSpacingPt}, line, gopdf.CellOption{})

		// Strikethrough — draw a line through the middle of the text
		if field.Strikethrough {
			stY := textY + lineSpacingPt*0.4
			drawW := textW
			if drawW > pw {
				drawW = pw
			}
			r, g, b, _ := parseColor(field.FontColor)
			pdf.SetStrokeColor(r, g, b)
			pdf.SetLineWidth(fontSize * 0.05)
			pdf.Line(lineX, stY, lineX+drawW, stY)
		}
	}

	// Reset character spacing
	if field.CharacterSpacing != 0 {
		pdf.SetCharSpacing(0)
	}
}

// wrapTextByWord splits text into lines that fit within maxWidth (in PDF points).
// Respects existing \n line breaks. Wraps by word boundary (space).
// The font must already be set on pdf before calling.
func wrapTextByWord(pdf *gopdf.GoPdf, text string, maxWidth float64) []string {
	if maxWidth <= 0 {
		return []string{text}
	}

	var result []string
	paragraphs := strings.Split(text, "\n")

	for _, para := range paragraphs {
		if para == "" {
			result = append(result, "")
			continue
		}

		words := strings.Fields(para)
		if len(words) == 0 {
			result = append(result, "")
			continue
		}

		currentLine := words[0]
		for i := 1; i < len(words); i++ {
			candidate := currentLine + " " + words[i]
			candidateW, _ := pdf.MeasureTextWidth(candidate)
			if candidateW <= maxWidth {
				currentLine = candidate
			} else {
				result = append(result, currentLine)
				currentLine = words[i]
				// If a single word is wider than maxWidth, it still gets its own line
			}
		}
		result = append(result, currentLine)
	}

	return result
}

// countWrappedLines counts how many lines the text would occupy after word wrapping.
// The font must already be set on pdf before calling.
func countWrappedLines(pdf *gopdf.GoPdf, text string, maxWidth float64) int {
	lines := wrapTextByWord(pdf, text, maxWidth)
	return len(lines)
}

// calculateDynamicFontSize finds the largest font size (between min and max) that fits text within w×h.
// fontFamily: the gopdf font name to use for measuring.
// lineHeight: the line-height multiplier from the field (e.g. 1.4).
// w, h: available space in PDF points.
func calculateDynamicFontSize(pdf *gopdf.GoPdf, text string, w, h float64, dfs *DynFontSize, fontFamily string, lineHeight float64) float64 {
	if lineHeight <= 0 {
		lineHeight = 1.4
	}
	if fontFamily == "" {
		fontFamily = "default"
	}

	for size := dfs.Max; size >= dfs.Min; size -= 0.5 {
		if err := pdf.SetFont(fontFamily, "", size); err != nil {
			continue
		}

		lineSpacingPt := size * lineHeight

		if dfs.Fit == "horizontal" {
			// For horizontal fit, each paragraph must fit in one line (no wrapping)
			paragraphs := strings.Split(text, "\n")
			fits := true
			for _, para := range paragraphs {
				paraW, _ := pdf.MeasureTextWidth(para)
				if paraW > w {
					fits = false
					break
				}
			}
			if fits {
				return size
			}
		} else {
			// "vertical" — word-wrap and check total height fits
			totalLines := countWrappedLines(pdf, text, w)
			totalH := float64(totalLines) * lineSpacingPt
			if totalH <= h {
				return size
			}
		}
	}
	return dfs.Min
}

func renderQRField(pdf *gopdf.GoPdf, field PdfmeField, value string, x, y, w, h float64) {
	log.Printf("[pdf] QR RENDER: field=%q value=%q pos=(%.1f,%.1f) size=(%.1f x %.1f)", field.Name, value, x, y, w, h)
	qr, err := goqrcode.New(value, goqrcode.Medium)
	if err != nil {
		log.Printf("[pdf] QR error: %v", err)
		return
	}
	qr.DisableBorder = true
	bitmap := qr.Bitmap()
	size := len(bitmap)
	if size == 0 {
		log.Printf("[pdf] QR bitmap empty for %q", value)
		return
	}
	log.Printf("[pdf] QR bitmap size=%d modules, moduleW=%.2f moduleH=%.2f", size, w/float64(size), h/float64(size))

	// Colors
	fgColor := field.Color
	if fgColor == "" {
		fgColor = "#000000"
	}
	finderColor := field.QrFinderColor
	if finderColor == "" {
		finderColor = fgColor // same as foreground by default
	}

	moduleW := w / float64(size)
	moduleH := h / float64(size)

	// isFinderModule returns true if (row,col) is inside one of the 3 finder patterns (7x7 each).
	// Finder patterns include the 7x7 area plus the 1-module separator border around them.
	isFinderModule := func(row, col int) bool {
		// Top-left: rows 0-6, cols 0-6
		if row <= 6 && col <= 6 {
			return true
		}
		// Top-right: rows 0-6, cols (size-7) to (size-1)
		if row <= 6 && col >= size-7 {
			return true
		}
		// Bottom-left: rows (size-7) to (size-1), cols 0-6
		if row >= size-7 && col <= 6 {
			return true
		}
		return false
	}

	// Draw foreground modules
	fgR, fgG, fgB, _ := parseColor(fgColor)
	fpR, fpG, fpB, _ := parseColor(finderColor)

	for row := 0; row < size; row++ {
		for col := 0; col < size; col++ {
			if !bitmap[row][col] {
				continue
			}
			mx := x + float64(col)*moduleW
			my := y + float64(row)*moduleH

			if isFinderModule(row, col) {
				pdf.SetFillColor(fpR, fpG, fpB)
			} else {
				pdf.SetFillColor(fgR, fgG, fgB)
			}
			pdf.RectFromUpperLeftWithStyle(mx, my, moduleW, moduleH, "F")
		}
	}
}

func renderBarcodeField(pdf *gopdf.GoPdf, value string, x, y, w, h float64) {
	var bc barcode.Barcode
	var err error

	bc, err = code128.Encode(value)
	if err != nil {
		bc, err = ean.Encode(value)
		if err != nil {
			log.Printf("[pdf] barcode error for %q: %v", value, err)
			return
		}
	}

	imgW := int(w / mmToPt * 10)
	imgH := int(h / mmToPt * 10)
	if imgW < 100 {
		imgW = 100
	}
	if imgH < 30 {
		imgH = 30
	}
	bc, err = barcode.Scale(bc, imgW, imgH)
	if err != nil {
		return
	}

	tmpFile, err := os.CreateTemp("", "bc-*.png")
	if err != nil {
		return
	}
	defer os.Remove(tmpFile.Name())

	if err := png.Encode(tmpFile, bc); err != nil {
		return
	}
	tmpFile.Close()

	pdf.Image(tmpFile.Name(), x, y, &gopdf.Rect{W: w, H: h})
}

func renderImageField(pdf *gopdf.GoPdf, field PdfmeField, row map[string]string, x, y, w, h float64) {
	// Resolve content: check row data first (for dynamic images), then static content
	content := ""
	// Try row variable (mapped data)
	if val, ok := row[field.Name]; ok && val != "" {
		content = val
	}
	// Try variables array
	if content == "" {
		for _, v := range field.Variables {
			if val, ok := row[v]; ok && val != "" {
				content = val
				break
			}
		}
	}
	// Fallback to static content from template (base64 logos, etc.)
	if content == "" {
		content = field.Content
	}
	if content == "" {
		return
	}

	// Handle base64 inline images (data URI or raw base64)
	if strings.HasPrefix(content, "data:image/") {
		localPath := saveBase64Image(content)
		if localPath != "" {
			defer os.Remove(localPath)
			pdf.Image(localPath, x, y, &gopdf.Rect{W: w, H: h})
		}
		return
	}

	// Handle raw base64 (no data: prefix but starts with base64 chars)
	if len(content) > 100 && !strings.HasPrefix(content, "http") && !strings.Contains(content[:20], "/") {
		// Likely raw base64 — try to decode
		localPath := saveBase64Image("data:image/png;base64," + content)
		if localPath != "" {
			defer os.Remove(localPath)
			pdf.Image(localPath, x, y, &gopdf.Rect{W: w, H: h})
		}
		return
	}

	// Handle HTTP URLs
	if strings.HasPrefix(content, "http") {
		localPath := getCachedImage(content)
		if localPath != "" {
			pdf.Image(localPath, x, y, &gopdf.Rect{W: w, H: h})
		}
		return
	}

	// Handle local file paths
	if _, err := os.Stat(content); err == nil {
		pdf.Image(content, x, y, &gopdf.Rect{W: w, H: h})
	}
}

func renderLineField(pdf *gopdf.GoPdf, field PdfmeField, x, y, w, h float64) {
	color := field.Color
	if color == "" {
		color = field.FontColor
	}
	if color == "" {
		color = "#000000"
	}
	r, g, b, _ := parseColor(color)

	// pdfme renders lines as thin filled rectangles, not stroked paths.
	// Using fill instead of stroke ensures SetTransparency applies correctly
	// (gopdf transparency affects fill operations reliably).
	pdf.SetFillColor(r, g, b)
	pdf.RectFromUpperLeftWithStyle(x, y, w, h, "F")
}

func renderEllipseField(pdf *gopdf.GoPdf, field PdfmeField, x, y, w, h float64) {
	// Fill
	if field.BackgroundColor != "" {
		r, g, b, _ := parseColor(field.BackgroundColor)
		pdf.SetFillColor(r, g, b)
	}
	// Stroke — borderWidth is in mm, SetLineWidth expects pt
	bw := float64(field.BorderWidth) * mmToPt
	if bw > 0 {
		color := field.BorderColor
		if color == "" {
			color = field.Color
		}
		if color == "" {
			color = "#000000"
		}
		r, g, b, _ := parseColor(color)
		pdf.SetStrokeColor(r, g, b)
		pdf.SetLineWidth(bw)
	}
	// Oval uses x1,y1,x2,y2 (bounding box corners)
	pdf.Oval(x, y, x+w, y+h)
}

func renderRectangleField(pdf *gopdf.GoPdf, field PdfmeField, x, y, w, h float64) {
	style := ""
	// pdfme uses "color" as the fill color for rectangles; also check backgroundColor
	fillColor := field.Color
	if fillColor == "" {
		fillColor = field.BackgroundColor
	}
	if fillColor != "" {
		r, g, b, _ := parseColor(fillColor)
		pdf.SetFillColor(r, g, b)
		style = "F"
	}
	// borderWidth is in mm, SetLineWidth expects pt
	bw := float64(field.BorderWidth) * mmToPt
	if bw > 0 {
		color := field.BorderColor
		if color == "" {
			color = "#000000"
		}
		r, g, b, _ := parseColor(color)
		pdf.SetStrokeColor(r, g, b)
		pdf.SetLineWidth(bw)
		if style == "F" {
			style = "FD"
		} else {
			style = "D"
		}
	}
	if style != "" {
		pdf.RectFromUpperLeftWithStyle(x, y, w, h, style)
	}
}

func renderTableField(pdf *gopdf.GoPdf, field PdfmeField, row map[string]string, x, y, w, h float64, fontLoaded bool) {
	if !fontLoaded {
		return
	}

	// Parse head styles
	headFS := 10.0
	headBg := "#2980ba"
	headFontColor := "#ffffff"
	if field.HeadStyles != nil {
		var hs TableStyle
		if err := json.Unmarshal(field.HeadStyles, &hs); err == nil {
			if hs.FontSize > 0 {
				headFS = hs.FontSize
			}
			if hs.BackgroundColor != "" {
				headBg = hs.BackgroundColor
			}
			if hs.FontColor != "" {
				headFontColor = hs.FontColor
			}
		}
	}

	// Parse body styles
	bodyFS := 9.0
	bodyFontColor := "#000000"
	bodyBg := ""
	bodyAltBg := ""
	if field.BodyStyles != nil {
		var bs struct {
			TableStyle
			AlternateBackgroundColor string `json:"alternateBackgroundColor"`
		}
		if err := json.Unmarshal(field.BodyStyles, &bs); err == nil {
			if bs.FontSize > 0 {
				bodyFS = bs.FontSize
			}
			if bs.FontColor != "" {
				bodyFontColor = bs.FontColor
			}
			if bs.BackgroundColor != "" {
				bodyBg = bs.BackgroundColor
			}
			bodyAltBg = bs.AlternateBackgroundColor
		}
	}

	// Calculate column widths
	numCols := len(field.Head)
	if numCols == 0 {
		return
	}
	colWidths := make([]float64, numCols)
	if len(field.HeadWidthPercentages) == numCols {
		for i, pct := range field.HeadWidthPercentages {
			colWidths[i] = w * pct / 100
		}
	} else {
		eachW := w / float64(numCols)
		for i := range colWidths {
			colWidths[i] = eachW
		}
	}

	rowHeight := headFS * 2.5

	// Render header
	showHead := field.ShowHead == nil || *field.ShowHead
	curY := y
	if showHead {
		curX := x
		for ci, header := range field.Head {
			// Header background
			r, g, b := hexToRGB(headBg)
			pdf.SetFillColor(r, g, b)
			pdf.RectFromUpperLeftWithStyle(curX, curY, colWidths[ci], rowHeight, "F")

			// Header text
			pdf.SetFont("default", "", headFS)
			r, g, b = hexToRGB(headFontColor)
			pdf.SetTextColor(r, g, b)
			pdf.SetX(curX + 4)
			pdf.SetY(curY + (rowHeight-headFS)/2)
			pdf.CellWithOption(&gopdf.Rect{W: colWidths[ci] - 8, H: rowHeight}, header, gopdf.CellOption{})

			curX += colWidths[ci]
		}
		curY += rowHeight
	}

	// Resolve table body data
	bodyData := resolveTableBody(field, row)
	bodyRowH := bodyFS * 2.2

	for ri, dataRow := range bodyData {
		if curY+bodyRowH > y+h {
			break // don't overflow
		}
		curX := x
		// Alternate background
		bg := bodyBg
		if ri%2 == 1 && bodyAltBg != "" {
			bg = bodyAltBg
		}
		for ci := 0; ci < numCols; ci++ {
			cellVal := ""
			if ci < len(dataRow) {
				cellVal = dataRow[ci]
			}

			if bg != "" {
				r, g, b := hexToRGB(bg)
				pdf.SetFillColor(r, g, b)
				pdf.RectFromUpperLeftWithStyle(curX, curY, colWidths[ci], bodyRowH, "F")
			}

			pdf.SetFont("default", "", bodyFS)
			r, g, b := hexToRGB(bodyFontColor)
			pdf.SetTextColor(r, g, b)
			pdf.SetX(curX + 4)
			pdf.SetY(curY + (bodyRowH-bodyFS)/2)
			pdf.CellWithOption(&gopdf.Rect{W: colWidths[ci] - 8, H: bodyRowH}, cellVal, gopdf.CellOption{})

			curX += colWidths[ci]
		}
		curY += bodyRowH
	}
}

// resolveTableBody gets the table body data from field content or row variables.
func resolveTableBody(field PdfmeField, row map[string]string) [][]string {
	// Try to get body from row variable (e.g. {medicamentos_tabla})
	for _, v := range field.Variables {
		if val, ok := row[v]; ok && val != "" {
			return parseTableContent(val)
		}
	}
	if val, ok := row[field.Name]; ok && val != "" {
		return parseTableContent(val)
	}
	// Fallback: use Content
	if field.Content != "" {
		return parseTableContent(field.Content)
	}
	return nil
}

// parseTableContent parses a JSON 2D array string into rows of cells.
func parseTableContent(s string) [][]string {
	s = strings.TrimSpace(s)
	// Try as JSON 2D array: [["a","b"],["c","d"]]
	var rows [][]string
	if err := json.Unmarshal([]byte(s), &rows); err == nil {
		return rows
	}
	// Try as single row
	var row []string
	if err := json.Unmarshal([]byte(s), &row); err == nil {
		return [][]string{row}
	}
	return nil
}

// ════════════════════════════════════════════════════
// Image utilities
// ════════════════════════════════════════════════════

// decodeBackgroundPdf decodes a base64-encoded PDF string (with or without data URI prefix)
// to a temporary file and returns the path. Caller must remove the file when done.
func decodeBackgroundPdf(b64 string) string {
	data := b64
	// Strip data URI prefix if present
	if strings.Contains(data, ",") {
		parts := strings.SplitN(data, ",", 2)
		data = parts[1]
	}
	// Strip whitespace/newlines
	data = strings.ReplaceAll(data, "\n", "")
	data = strings.ReplaceAll(data, "\r", "")
	data = strings.ReplaceAll(data, " ", "")

	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(data)
		if err != nil {
			log.Printf("[pdf] Warning: could not decode background PDF base64: %v", err)
			return ""
		}
	}
	// Verify it looks like a PDF
	if len(decoded) < 5 || string(decoded[:5]) != "%PDF-" {
		log.Printf("[pdf] Warning: decoded background is not a valid PDF (header: %q)", string(decoded[:min(10, len(decoded))]))
		return ""
	}

	tmpFile, err := os.CreateTemp("", "bg-*.pdf")
	if err != nil {
		return ""
	}
	if _, err := tmpFile.Write(decoded); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return ""
	}
	tmpFile.Close()
	return tmpFile.Name()
}

func saveBase64Image(dataURI string) string {
	// Parse "data:image/png;base64,iVBOR..."
	parts := strings.SplitN(dataURI, ",", 2)
	if len(parts) != 2 {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		// Try RawStdEncoding (no padding)
		decoded, err = base64.RawStdEncoding.DecodeString(parts[1])
		if err != nil {
			return ""
		}
	}

	ext := ".png"
	if strings.Contains(parts[0], "jpeg") || strings.Contains(parts[0], "jpg") {
		ext = ".jpg"
	}

	tmpFile, err := os.CreateTemp("", "b64img-*"+ext)
	if err != nil {
		return ""
	}
	if _, err := tmpFile.Write(decoded); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return ""
	}
	tmpFile.Close()
	return tmpFile.Name()
}

func getCachedImage(url string) string {
	imgCacheMu.Lock()
	defer imgCacheMu.Unlock()

	if path, ok := imgCache[url]; ok {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	resp, err := http.Get(url)
	if err != nil {
		log.Printf("[pdf] image download error: %v", err)
		return ""
	}
	defer resp.Body.Close()

	cacheDir := filepath.Join(configDir(), "cache", "images")
	os.MkdirAll(cacheDir, 0755)

	ext := ".png"
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "jpeg") || strings.Contains(ct, "jpg") {
		ext = ".jpg"
	}

	tmpFile, err := os.CreateTemp(cacheDir, "img-*"+ext)
	if err != nil {
		return ""
	}
	defer tmpFile.Close()

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return ""
	}

	imgCache[url] = tmpFile.Name()
	return tmpFile.Name()
}

// ════════════════════════════════════════════════════
// Value resolution — all field types
// ════════════════════════════════════════════════════

// resolveFieldValue gets the display value for a field from row data.
//
// multiVariableText: uses field.Text as template, replaces {var} with row values.
// text with variables: same as multiVariableText (ISI custom format).
// qrcode/barcode: looks up row[field.Name], then interpolates {placeholder} in Content.
func resolveFieldValue(field PdfmeField, row map[string]string) string {
	// multiVariableText or text with variables array → template interpolation
	if field.Type == "multiVariableText" || (field.Type == "text" && len(field.Variables) > 0) {
		return resolveMultiVariableText(field, row)
	}

	// Direct lookup by field name
	if val, ok := row[field.Name]; ok && val != "" {
		return val
	}

	// Try normalized field name (dots ↔ underscores)
	normalized := strings.ReplaceAll(field.Name, ".", "_")
	dotted := strings.ReplaceAll(field.Name, "_", ".")
	if val, ok := row[normalized]; ok && val != "" {
		return val
	}
	if val, ok := row[dotted]; ok && val != "" {
		return val
	}

	// Try variables array as alternative keys
	for _, varName := range field.Variables {
		if val, ok := row[varName]; ok && val != "" {
			return val
		}
	}

	// Fallback: Content might have a {variable} placeholder
	if field.Content != "" {
		// Enrich row with suffix-matched data for placeholder interpolation
		enriched := enrichRowForPlaceholders(field.Content, row)
		resolved := interpolatePlaceholders(field.Content, enriched)
		if resolved != field.Content && resolved != "" {
			return resolved
		}
		// Static content (no placeholders or nothing resolved)
		if !strings.Contains(field.Content, "{") {
			return field.Content
		}
	}

	return ""
}

// enrichRowForPlaceholders extracts {placeholder} names from text and tries to
// find matching row keys by suffix matching (e.g. {token} matches row["gafete.token"]).
func enrichRowForPlaceholders(text string, row map[string]string) map[string]string {
	enriched := make(map[string]string, len(row))
	for k, v := range row {
		enriched[k] = v
	}

	// Extract placeholder names from text
	remaining := text
	for {
		start := strings.Index(remaining, "{")
		if start == -1 {
			break
		}
		end := strings.Index(remaining[start:], "}")
		if end == -1 {
			break
		}
		placeholder := remaining[start+1 : start+end]
		remaining = remaining[start+end+1:]

		// Skip JSON-like patterns
		if strings.ContainsAny(placeholder, "\":") {
			continue
		}
		if _, found := enriched[placeholder]; found {
			continue
		}

		// Suffix match: find row key ending with separator + placeholder
		for k, v := range row {
			if strings.HasSuffix(k, "_"+placeholder) || strings.HasSuffix(k, "."+placeholder) {
				enriched[placeholder] = v
				break
			}
		}
	}

	return enriched
}

// resolveMultiVariableText handles pdfme multiVariableText and text-with-variables fields.
func resolveMultiVariableText(field PdfmeField, row map[string]string) string {
	template := field.Text
	if template == "" {
		template = field.Content
	}
	if template == "" {
		return ""
	}

	// Build enriched data map: resolve variables that don't directly match row keys.
	// pdfme multiVariableText uses short variable names (e.g. "nombre") in the template,
	// but dashboard maps data to field names (e.g. "gafete_nombre" or "gafete.nombre").
	// We need to bridge this gap by suffix-matching variables to row keys.
	enrichedRow := enrichRowForVariables(field, row)

	// Detect if template is JSON content (e.g. '{"gafete.nombre":"[gafete.nombre]"}')
	// rather than a text template (e.g. "{gafete.nombre} {gafete.apellido}")
	trimmed := strings.TrimSpace(template)
	if len(trimmed) > 2 && trimmed[0] == '{' && trimmed[1] == '"' {
		// This is JSON content, not a text template.
		if len(field.Variables) > 0 {
			var parts []string
			for _, varName := range field.Variables {
				if val, ok := enrichedRow[varName]; ok && val != "" {
					parts = append(parts, val)
				}
			}
			return strings.Join(parts, " ")
		}
		return ""
	}

	// Process Handlebars conditionals: {{#if var}}text{{/if}}, {{#unless var}}text{{/unless}}
	template = processConditionals(template, enrichedRow)

	// Interpolate {variable} placeholders in template with row data
	result := interpolatePlaceholders(template, enrichedRow)
	return strings.TrimSpace(result)
}

// enrichRowForVariables creates an enriched data map that bridges the gap between
// pdfme variable names (short: "nombre") and dashboard row keys (full: "gafete_nombre").
//
// For each variable in field.Variables, if the variable doesn't exist in the row,
// try to find a matching row key by:
//  1. Field name direct match (for single-variable fields)
//  2. Suffix matching: row key ending with _varName or .varName
//  3. Normalized matching: underscores ↔ dots
func enrichRowForVariables(field PdfmeField, row map[string]string) map[string]string {
	enriched := make(map[string]string, len(row)+len(field.Variables))
	for k, v := range row {
		enriched[k] = v
	}

	for _, varName := range field.Variables {
		if _, found := enriched[varName]; found {
			continue // already have a direct match
		}

		// Single-variable field: use the field's own data
		if len(field.Variables) == 1 {
			if val, ok := row[field.Name]; ok && val != "" {
				enriched[varName] = val
				continue
			}
		}

		// Suffix match: find row key ending with separator + varName
		for k, v := range row {
			if strings.HasSuffix(k, "_"+varName) || strings.HasSuffix(k, "."+varName) {
				enriched[varName] = v
				break
			}
		}

		// Normalized match: try with dots ↔ underscores
		if _, found := enriched[varName]; !found {
			normalized := strings.ReplaceAll(field.Name, ".", "_")
			dotted := strings.ReplaceAll(field.Name, "_", ".")
			if val, ok := row[normalized]; ok && len(field.Variables) == 1 {
				enriched[varName] = val
			} else if val, ok := row[dotted]; ok && len(field.Variables) == 1 {
				enriched[varName] = val
			}
		}
	}

	return enriched
}

// processConditionals handles Handlebars-style conditionals in text templates.
// Supports: {{#if var}}text{{/if}}, {{#unless var}}text{{/unless}}, {{else}}
var (
	reIf     = regexp.MustCompile(`(?s)\{\{#if\s+(\S+?)\}\}(.*?)(?:\{\{else\}\}(.*?))?\{\{/if\}\}`)
	reUnless = regexp.MustCompile(`(?s)\{\{#unless\s+(\S+?)\}\}(.*?)(?:\{\{else\}\}(.*?))?\{\{/unless\}\}`)
)

func processConditionals(text string, row map[string]string) string {
	// Process {{#if var}}...{{else}}...{{/if}}
	result := reIf.ReplaceAllStringFunc(text, func(match string) string {
		sub := reIf.FindStringSubmatch(match)
		if len(sub) < 3 {
			return ""
		}
		varName := sub[1]
		trueBranch := sub[2]
		falseBranch := ""
		if len(sub) >= 4 {
			falseBranch = sub[3]
		}
		val, exists := row[varName]
		if exists && val != "" {
			return trueBranch
		}
		return falseBranch
	})

	// Process {{#unless var}}...{{else}}...{{/unless}}
	result = reUnless.ReplaceAllStringFunc(result, func(match string) string {
		sub := reUnless.FindStringSubmatch(match)
		if len(sub) < 3 {
			return ""
		}
		varName := sub[1]
		trueBranch := sub[2]
		falseBranch := ""
		if len(sub) >= 4 {
			falseBranch = sub[3]
		}
		val, exists := row[varName]
		if !exists || val == "" {
			return trueBranch
		}
		return falseBranch
	})

	return result
}

// interpolatePlaceholders replaces {key} placeholders with values from data map.
// Unreplaced placeholders are removed.
func interpolatePlaceholders(text string, data map[string]string) string {
	result := text
	for key, val := range data {
		result = strings.ReplaceAll(result, "{"+key+"}", val)
	}
	// Remove any unreplaced {placeholder} patterns (but not JSON-like patterns)
	for {
		start := strings.Index(result, "{")
		if start == -1 {
			break
		}
		end := strings.Index(result[start:], "}")
		if end == -1 {
			break
		}
		inner := result[start+1 : start+end]
		// Don't remove if it looks like JSON (contains quotes or colons)
		if strings.ContainsAny(inner, "\":") {
			break
		}
		result = result[:start] + result[start+end+1:]
	}
	return result
}

// ════════════════════════════════════════════════════
// Font discovery
// ════════════════════════════════════════════════════

// ════════════════════════════════════════════════════
// Font system — discovery, loading, registry
// ════════════════════════════════════════════════════

var (
	fontRegistry   = map[string]bool{} // tracks loaded font names in gopdf
	fontRegistryMu sync.Mutex
)

// findFonts returns paths for regular and bold fonts.
func findFonts() (regular string, bold string) {
	regularCandidates := []string{
		"/System/Library/Fonts/Supplemental/Arial.ttf",
		"/Library/Fonts/Arial.ttf",
		"/System/Library/Fonts/SFNSText.ttf",
		`C:\Windows\Fonts\arial.ttf`,
		`C:\Windows\Fonts\segoeui.ttf`,
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
		"/usr/share/fonts/TTF/DejaVuSans.ttf",
		"/System/Library/Fonts/Helvetica.ttc",
	}
	boldCandidates := []string{
		"/System/Library/Fonts/Supplemental/Arial Bold.ttf",
		"/Library/Fonts/Arial Bold.ttf",
		`C:\Windows\Fonts\arialbd.ttf`,
		`C:\Windows\Fonts\segoeuib.ttf`,
		"/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
		"/usr/share/fonts/TTF/DejaVuSans-Bold.ttf",
	}

	for _, p := range regularCandidates {
		if _, err := os.Stat(p); err == nil {
			regular = p
			break
		}
	}
	for _, p := range boldCandidates {
		if _, err := os.Stat(p); err == nil {
			bold = p
			break
		}
	}
	return
}

// findDefaultFont returns path for the default regular font (backward compat).
func findDefaultFont() string {
	r, _ := findFonts()
	return r
}

// systemFontDirs returns directories where fonts are installed.
func systemFontDirs() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/System/Library/Fonts/",
			"/System/Library/Fonts/Supplemental/",
			"/Library/Fonts/",
			filepath.Join(os.Getenv("HOME"), "Library/Fonts/"),
		}
	case "windows":
		return []string{`C:\Windows\Fonts\`}
	default: // linux
		return []string{
			"/usr/share/fonts/",
			"/usr/share/fonts/truetype/",
			"/usr/share/fonts/TTF/",
			"/usr/local/share/fonts/",
			filepath.Join(os.Getenv("HOME"), ".fonts/"),
		}
	}
}

// findFontByName searches system font directories for a font matching the given name.
// Returns paths for regular and bold variants. Name matching is case-insensitive.
func findFontByName(name string) (regular string, bold string) {
	if name == "" {
		return "", ""
	}
	nameLower := strings.ToLower(name)
	// Common name → file mappings
	nameVariations := []string{
		name,
		strings.ReplaceAll(name, " ", ""),
		strings.ReplaceAll(name, " ", "-"),
	}

	for _, dir := range systemFontDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			fname := entry.Name()
			fnameLower := strings.ToLower(fname)
			// Only .ttf and .otf (skip .ttc for now — gopdf has limited .ttc support)
			if !strings.HasSuffix(fnameLower, ".ttf") && !strings.HasSuffix(fnameLower, ".otf") {
				continue
			}
			base := strings.TrimSuffix(strings.TrimSuffix(fnameLower, ".ttf"), ".otf")

			for _, variation := range nameVariations {
				vl := strings.ToLower(variation)
				// Exact match or prefix match
				if base == vl || base == vl+"-regular" || base == vl+"regular" {
					regular = filepath.Join(dir, fname)
				}
				if base == vl+"-bold" || base == vl+"bold" || base == vl+" bold" {
					bold = filepath.Join(dir, fname)
				}
			}
		}
	}

	// Also try with the lowercase name directly
	if regular == "" {
		for _, dir := range systemFontDirs() {
			for _, ext := range []string{".ttf", ".otf"} {
				for _, variation := range nameVariations {
					candidates := []string{
						filepath.Join(dir, variation+ext),
						filepath.Join(dir, variation+"-Regular"+ext),
					}
					for _, c := range candidates {
						if _, err := os.Stat(c); err == nil {
							regular = c
							break
						}
					}
					if regular != "" {
						break
					}
				}
				if regular != "" {
					break
				}
			}
			if regular != "" {
				break
			}
		}
	}

	// If we found regular but not bold, check with Bold suffix
	if regular != "" && bold == "" {
		dir := filepath.Dir(regular)
		base := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(regular), ".ttf"), ".otf")
		base = strings.TrimSuffix(base, "-Regular")
		base = strings.TrimSuffix(base, "Regular")
		base = strings.TrimSuffix(base, "-regular")
		for _, ext := range []string{".ttf", ".otf"} {
			candidates := []string{
				filepath.Join(dir, base+"-Bold"+ext),
				filepath.Join(dir, base+"Bold"+ext),
				filepath.Join(dir, base+" Bold"+ext),
			}
			for _, c := range candidates {
				if _, err := os.Stat(c); err == nil {
					bold = c
					break
				}
			}
			if bold != "" {
				break
			}
		}
	}

	if regular != "" {
		log.Printf("[pdf] Found system font %q: regular=%s bold=%s", nameLower, regular, bold)
	}
	return
}

// loadNamedFont loads a font by name into gopdf if not already loaded.
// Returns the gopdf font family name to use.
func loadNamedFont(pdf *gopdf.GoPdf, name string) (fontFamily string, hasBold bool) {
	if name == "" || name == "default" {
		return "default", fontRegistry["bold"]
	}

	fontRegistryMu.Lock()
	defer fontRegistryMu.Unlock()

	famName := "font_" + strings.ToLower(strings.ReplaceAll(name, " ", "_"))
	famBold := famName + "_bold"

	if fontRegistry[famName] {
		return famName, fontRegistry[famBold]
	}

	regular, bold := findFontByName(name)
	if regular == "" {
		return "default", fontRegistry["bold"]
	}

	if err := pdf.AddTTFFont(famName, regular); err != nil {
		log.Printf("[pdf] Warning: could not load font %q (%s): %v", name, regular, err)
		return "default", fontRegistry["bold"]
	}
	fontRegistry[famName] = true
	log.Printf("[pdf] Loaded named font %q as %q from %s", name, famName, regular)

	if bold != "" {
		if err := pdf.AddTTFFont(famBold, bold); err == nil {
			fontRegistry[famBold] = true
			hasBold = true
		}
	}
	return famName, hasBold
}

// ════════════════════════════════════════════════════
// Utility functions
// ════════════════════════════════════════════════════

func mapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ════════════════════════════════════════════════════
// Color parsing — hex, rgb(), rgba(), hsl(), hsla(), named
// ════════════════════════════════════════════════════

// blendColorWithWhite applies opacity by blending a color with white.
// On white paper, this produces the same visual result as PDF transparency.
// Returns a hex color string (e.g. "#ebebeb").
func blendColorWithWhite(colorStr string, opacity float64) string {
	if colorStr == "" || opacity >= 1 || opacity <= 0 {
		return colorStr
	}
	r, g, b, _ := parseColor(colorStr)
	rr := int(math.Round(255*(1-opacity) + float64(r)*opacity))
	gg := int(math.Round(255*(1-opacity) + float64(g)*opacity))
	bb := int(math.Round(255*(1-opacity) + float64(b)*opacity))
	return fmt.Sprintf("#%02x%02x%02x", rr, gg, bb)
}

// parseColor parses any CSS color format and returns r, g, b (0-255) and alpha (0.0-1.0).
// Supports: #RGB, #RRGGBB, #RRGGBBAA, rgb(r,g,b), rgba(r,g,b,a), hsl(h,s%,l%), hsla(h,s%,l%,a), named colors.
func parseColor(color string) (r, g, b uint8, alpha float64) {
	alpha = 1.0
	color = strings.TrimSpace(color)
	if color == "" {
		return 0, 0, 0, 1.0
	}

	// Named colors
	if named, ok := namedColors[strings.ToLower(color)]; ok {
		return named[0], named[1], named[2], 1.0
	}

	// Hex: #RGB, #RRGGBB, #RRGGBBAA
	if strings.HasPrefix(color, "#") {
		hex := color[1:]
		switch len(hex) {
		case 3:
			hex = string(hex[0]) + string(hex[0]) + string(hex[1]) + string(hex[1]) + string(hex[2]) + string(hex[2])
		case 4:
			hex = string(hex[0]) + string(hex[0]) + string(hex[1]) + string(hex[1]) + string(hex[2]) + string(hex[2]) + string(hex[3]) + string(hex[3])
		}
		if len(hex) >= 6 {
			rv, _ := strconv.ParseUint(hex[0:2], 16, 8)
			gv, _ := strconv.ParseUint(hex[2:4], 16, 8)
			bv, _ := strconv.ParseUint(hex[4:6], 16, 8)
			r, g, b = uint8(rv), uint8(gv), uint8(bv)
			if len(hex) == 8 {
				av, _ := strconv.ParseUint(hex[6:8], 16, 8)
				alpha = float64(av) / 255.0
			}
			return
		}
		return 0, 0, 0, 1.0
	}

	// rgb(r,g,b) or rgba(r,g,b,a)
	if strings.HasPrefix(color, "rgb") {
		nums := extractNumbers(color)
		if len(nums) >= 3 {
			r = clampUint8(nums[0])
			g = clampUint8(nums[1])
			b = clampUint8(nums[2])
			if len(nums) >= 4 {
				alpha = clampFloat(nums[3], 0, 1)
			}
			return
		}
		return 0, 0, 0, 1.0
	}

	// hsl(h,s%,l%) or hsla(h,s%,l%,a)
	if strings.HasPrefix(color, "hsl") {
		nums := extractNumbers(color)
		if len(nums) >= 3 {
			h := math.Mod(nums[0], 360)
			if h < 0 {
				h += 360
			}
			s := clampFloat(nums[1], 0, 100) / 100
			l := clampFloat(nums[2], 0, 100) / 100
			r, g, b = hslToRGB(h, s, l)
			if len(nums) >= 4 {
				alpha = clampFloat(nums[3], 0, 1)
			}
			return
		}
		return 0, 0, 0, 1.0
	}

	return 0, 0, 0, 1.0
}

// hexToRGB is a convenience wrapper around parseColor (ignores alpha).
func hexToRGB(color string) (uint8, uint8, uint8) {
	r, g, b, _ := parseColor(color)
	return r, g, b
}

var colorNumRe = regexp.MustCompile(`[\d.]+`)

func extractNumbers(s string) []float64 {
	matches := colorNumRe.FindAllString(s, -1)
	nums := make([]float64, 0, len(matches))
	for _, m := range matches {
		if v, err := strconv.ParseFloat(m, 64); err == nil {
			nums = append(nums, v)
		}
	}
	return nums
}

func clampUint8(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func clampFloat(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// hslToRGB converts HSL (h in 0-360, s and l in 0-1) to RGB.
func hslToRGB(h, s, l float64) (uint8, uint8, uint8) {
	if s == 0 {
		v := clampUint8(l * 255)
		return v, v, v
	}
	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q
	hk := h / 360
	tr := hk + 1.0/3.0
	tg := hk
	tb := hk - 1.0/3.0
	return clampUint8(hueToRGB(p, q, tr) * 255), clampUint8(hueToRGB(p, q, tg) * 255), clampUint8(hueToRGB(p, q, tb) * 255)
}

func hueToRGB(p, q, t float64) float64 {
	if t < 0 {
		t += 1
	}
	if t > 1 {
		t -= 1
	}
	if t < 1.0/6.0 {
		return p + (q-p)*6*t
	}
	if t < 0.5 {
		return q
	}
	if t < 2.0/3.0 {
		return p + (q-p)*(2.0/3.0-t)*6
	}
	return p
}

var namedColors = map[string][3]uint8{
	"black":       {0, 0, 0},
	"white":       {255, 255, 255},
	"red":         {255, 0, 0},
	"green":       {0, 128, 0},
	"blue":        {0, 0, 255},
	"yellow":      {255, 255, 0},
	"cyan":        {0, 255, 255},
	"magenta":     {255, 0, 255},
	"gray":        {128, 128, 128},
	"grey":        {128, 128, 128},
	"orange":      {255, 165, 0},
	"purple":      {128, 0, 128},
	"pink":        {255, 192, 203},
	"brown":       {165, 42, 42},
	"navy":        {0, 0, 128},
	"teal":        {0, 128, 128},
	"maroon":      {128, 0, 0},
	"olive":       {128, 128, 0},
	"silver":      {192, 192, 192},
	"lime":        {0, 255, 0},
	"aqua":        {0, 255, 255},
	"fuchsia":     {255, 0, 255},
	"transparent": {0, 0, 0},
}

// Ensure strconv is used
var _ = strconv.ParseUint
