package main

import (
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/code128"
	"github.com/boombuler/barcode/ean"
	"github.com/signintech/gopdf"
	goqrcode "github.com/skip2/go-qrcode"
)

const mmToPt = 2.835 // 1mm = 2.835 PDF points

// PdfmeSchema represents a parsed pdfme template schema.
type PdfmeSchema struct {
	Schemas [][]PdfmeField `json:"schemas"`
	BasePdf PdfmeBasePdf   `json:"basePdf"`
}

// PdfmeBasePdf is the page dimensions.
type PdfmeBasePdf struct {
	Width   float64   `json:"width"`  // mm
	Height  float64   `json:"height"` // mm
	Padding []float64 `json:"padding"`
}

// PdfmeField is a single field in the schema.
type PdfmeField struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Position  PdfmePos `json:"position"`
	Width     float64  `json:"width"`  // mm
	Height    float64  `json:"height"` // mm
	FontSize  float64  `json:"fontSize,omitempty"`
	FontName  string   `json:"fontName,omitempty"`
	Alignment string   `json:"alignment,omitempty"`
	FontColor string   `json:"fontColor,omitempty"`
	Content   string   `json:"content,omitempty"`
}

// PdfmePos is x,y in mm.
type PdfmePos struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

var (
	imgCache   = map[string]string{}
	imgCacheMu sync.Mutex
)

// ParsePdfmeSchema parses raw JSON into PdfmeSchema.
func ParsePdfmeSchema(raw json.RawMessage) (*PdfmeSchema, error) {
	var schema PdfmeSchema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("parse pdfme schema: %w", err)
	}
	if schema.BasePdf.Width == 0 {
		schema.BasePdf.Width = 76
	}
	if schema.BasePdf.Height == 0 {
		schema.BasePdf.Height = 50
	}
	return &schema, nil
}

// RenderBulkPDF generates a multi-page PDF from rows of data using a pdfme schema.
func RenderBulkPDF(schema *PdfmeSchema, rows []map[string]string, outputPath string) error {
	pdf := gopdf.GoPdf{}

	pageW := schema.BasePdf.Width * mmToPt
	pageH := schema.BasePdf.Height * mmToPt

	pdf.Start(gopdf.Config{
		PageSize: gopdf.Rect{W: pageW, H: pageH},
	})

	fontPath := findDefaultFont()
	if fontPath != "" {
		if err := pdf.AddTTFFont("default", fontPath); err != nil {
			log.Printf("[pdf] Warning: could not load font %s: %v", fontPath, err)
		}
	}

	fields := []PdfmeField{}
	if len(schema.Schemas) > 0 {
		fields = schema.Schemas[0]
	}

	for _, row := range rows {
		pdf.AddPage()

		for _, field := range fields {
			value := row[field.Name]
			if value == "" {
				value = field.Content
			}
			if value == "" {
				continue
			}

			x := field.Position.X * mmToPt
			y := field.Position.Y * mmToPt
			w := field.Width * mmToPt
			h := field.Height * mmToPt

			switch field.Type {
			case "text", "multiVariableText":
				renderTextField(&pdf, field, value, x, y, w, h, fontPath)
			case "qrcode":
				renderQRField(&pdf, value, x, y, w, h)
			case "image":
				renderImageField(&pdf, value, x, y, w, h)
			case "barcode", "code128":
				renderBarcodeField(&pdf, value, x, y, w, h)
			}
		}
	}

	return pdf.WritePdf(outputPath)
}

func renderTextField(pdf *gopdf.GoPdf, field PdfmeField, value string, x, y, w, h float64, fontPath string) {
	fontSize := field.FontSize
	if fontSize == 0 {
		fontSize = 10
	}
	if fontPath == "" {
		return
	}
	if err := pdf.SetFont("default", "", fontSize); err != nil {
		log.Printf("[pdf] font error: %v", err)
		return
	}

	if field.FontColor != "" {
		r, g, b := hexToRGB(field.FontColor)
		pdf.SetTextColor(r, g, b)
	} else {
		pdf.SetTextColor(0, 0, 0)
	}

	pdf.SetX(x)
	pdf.SetY(y)

	switch field.Alignment {
	case "center":
		textW, _ := pdf.MeasureTextWidth(value)
		if textW < w {
			pdf.SetX(x + (w-textW)/2)
		}
	case "right":
		textW, _ := pdf.MeasureTextWidth(value)
		if textW < w {
			pdf.SetX(x + w - textW)
		}
	}

	pdf.CellWithOption(&gopdf.Rect{W: w, H: h}, value, gopdf.CellOption{})
}

func renderQRField(pdf *gopdf.GoPdf, value string, x, y, w, h float64) {
	qr, err := goqrcode.New(value, goqrcode.Medium)
	if err != nil {
		log.Printf("[pdf] QR error: %v", err)
		return
	}

	tmpFile, err := os.CreateTemp("", "qr-*.png")
	if err != nil {
		return
	}
	defer os.Remove(tmpFile.Name())

	size := int(w / mmToPt * 10)
	if size < 100 {
		size = 100
	}
	qr.DisableBorder = true
	if err := qr.WriteFile(size, tmpFile.Name()); err != nil {
		return
	}

	pdf.Image(tmpFile.Name(), x, y, &gopdf.Rect{W: w, H: h})
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

func renderImageField(pdf *gopdf.GoPdf, value string, x, y, w, h float64) {
	if !strings.HasPrefix(value, "http") {
		return
	}
	localPath := getCachedImage(value)
	if localPath == "" {
		return
	}
	pdf.Image(localPath, x, y, &gopdf.Rect{W: w, H: h})
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

func findDefaultFont() string {
	candidates := []string{
		"/System/Library/Fonts/Helvetica.ttc",
		"/System/Library/Fonts/SFNSText.ttf",
		"/Library/Fonts/Arial.ttf",
		"/System/Library/Fonts/Supplemental/Arial.ttf",
		`C:\Windows\Fonts\arial.ttf`,
		`C:\Windows\Fonts\segoeui.ttf`,
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
		"/usr/share/fonts/TTF/DejaVuSans.ttf",
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func hexToRGB(hex string) (uint8, uint8, uint8) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 0, 0, 0
	}
	var r, g, b uint8
	fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b)
	return r, g, b
}
