# TSC Bridge v3.0 — Professional Dashboard + Bulk PDF

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Transform TSC Bridge into a plug-and-play professional app with whitelabel branding, auto-synced templates, and local bulk PDF generation.

**Architecture:** Eliminate hardcoded API credentials in favor of config.json (pre-filled at distribution time via Go microservice). Rewrite dashboard as tabbed app-like UI with dynamic whitelabel branding. Add local PDF renderer using gopdf to generate bulk PDFs from pdfme schemas without hitting the backend per-row.

**Tech Stack:** Go 1.25, gopdf, go-qrcode, boombuler/barcode, embedded HTML dashboard

---

### Task 1: Add Go dependencies for PDF rendering

**Files:**
- Modify: `go.mod`

**Step 1: Add dependencies**

Run:
```bash
cd /Users/mario/PARA/1_Proyectos/ISI_Hospital/Servicios/tsc-bridge
go get github.com/signintech/gopdf
go get github.com/skip2/go-qrcode
go get github.com/boombuler/barcode
```

**Step 2: Verify go.mod updated**

Run: `cat go.mod`
Expected: All three dependencies listed in require block.

**Step 3: Tidy**

Run: `go mod tidy`

**Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add gopdf, go-qrcode, barcode for local PDF rendering"
```

---

### Task 2: Refactor config.go — Remove hardcoded credentials, add Whitelabel

**Files:**
- Modify: `config.go`

**Step 1: Replace hardcoded constants and AppConfig struct**

Replace the `builtinApi*` constants and `AppConfig` struct (lines 14-46) with:

```go
// WhitelabelConfig stores branding info for the client.
type WhitelabelConfig struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	LogoURL      string `json:"logo_url,omitempty"`
	PrimaryColor string `json:"primary_color,omitempty"`
	AccentColor  string `json:"accent_color,omitempty"`
}

// AppConfig is the persisted configuration for tsc-bridge.
type AppConfig struct {
	Port                int              `json:"port"`
	DefaultPrinter      string           `json:"default_printer"`
	DefaultPreset       string           `json:"default_preset"`
	CustomPresets       []LabelPreset    `json:"custom_presets"`
	AutoStart           bool             `json:"auto_start"`
	NetworkScanEnabled  bool             `json:"network_scan_enabled"`
	NetworkScanInterval int              `json:"network_scan_interval"`
	ManualPrinters      []string         `json:"manual_printers"`
	ShareEnabled        bool             `json:"share_enabled"`
	SharePort           int              `json:"share_port"`
	SharePrinter        string           `json:"share_printer"`
	ApiURL              string           `json:"api_url"`
	ApiToken            string           `json:"api_token"`
	ApiKey              string           `json:"api_key"`
	ApiSecret           string           `json:"api_secret"`
	ApiWhiteLabel       int              `json:"api_wl"`
	Whitelabel          WhitelabelConfig `json:"whitelabel"`
}
```

Delete the `builtinApi*` constants block entirely.

**Step 2: Update defaultConfig()**

```go
func defaultConfig() AppConfig {
	return AppConfig{
		Port:                9638,
		DefaultPrinter:      "",
		DefaultPreset:       "matrix-3x1-30x22",
		CustomPresets:       []LabelPreset{},
		AutoStart:           true,
		NetworkScanEnabled:  true,
		NetworkScanInterval: 30,
		ManualPrinters:      []string{},
		ShareEnabled:        false,
		SharePort:           9100,
		SharePrinter:        "",
	}
}
```

**Step 3: Update getConfig() — remove forced overrides**

```go
func getConfig() AppConfig {
	configMu.RLock()
	defer configMu.RUnlock()
	c := appConfig
	c.CustomPresets = make([]LabelPreset, len(appConfig.CustomPresets))
	copy(c.CustomPresets, appConfig.CustomPresets)
	return c
}
```

**Step 4: Update safeConfigForClient() — expose whitelabel, hide secrets**

```go
func safeConfigForClient() map[string]any {
	cfg := getConfig()
	return map[string]any{
		"port":                  cfg.Port,
		"default_printer":      cfg.DefaultPrinter,
		"default_preset":       cfg.DefaultPreset,
		"custom_presets":       cfg.CustomPresets,
		"auto_start":          cfg.AutoStart,
		"network_scan_enabled": cfg.NetworkScanEnabled,
		"network_scan_interval": cfg.NetworkScanInterval,
		"manual_printers":     cfg.ManualPrinters,
		"share_enabled":       cfg.ShareEnabled,
		"share_port":          cfg.SharePort,
		"share_printer":       cfg.SharePrinter,
		"api_configured":      cfg.ApiURL != "" && (cfg.ApiKey != "" || cfg.ApiToken != ""),
		"api_url":             cfg.ApiURL,
		"whitelabel":          cfg.Whitelabel,
	}
}
```

**Step 5: Update handleConfig PUT — allow API fields from config.json**

In `handleConfig()` PUT case, after existing field updates (line ~193), add:

```go
		if incoming.ApiURL != "" {
			appConfig.ApiURL = incoming.ApiURL
		}
		if incoming.ApiKey != "" {
			appConfig.ApiKey = incoming.ApiKey
		}
		if incoming.ApiSecret != "" {
			appConfig.ApiSecret = incoming.ApiSecret
		}
		if incoming.ApiToken != "" {
			appConfig.ApiToken = incoming.ApiToken
		}
		if incoming.ApiWhiteLabel > 0 {
			appConfig.ApiWhiteLabel = incoming.ApiWhiteLabel
		}
		if incoming.Whitelabel.ID > 0 {
			appConfig.Whitelabel = incoming.Whitelabel
		}
```

**Step 6: Add handleWhitelabel endpoint**

```go
func handleWhitelabel(w http.ResponseWriter, r *http.Request) {
	cfg := getConfig()
	wl := cfg.Whitelabel
	if wl.Name == "" {
		wl.Name = "TSC Bridge"
	}
	jsonResponse(w, http.StatusOK, wl)
}
```

**Step 7: Verify compile**

Run: `go build -o /dev/null .`
Expected: Success (no errors)

**Step 8: Commit**

```bash
git add config.go
git commit -m "refactor: remove hardcoded API credentials, add whitelabel config"
```

---

### Task 3: Add /whitelabel route in main.go

**Files:**
- Modify: `main.go`

**Step 1: Register the new endpoint**

In `startServers()`, find the block of `mux.HandleFunc` calls. Add after the `/config` route:

```go
	mux.HandleFunc("/whitelabel", corsMiddleware(handleWhitelabel))
```

**Step 2: Verify compile**

Run: `go build -o /dev/null .`

**Step 3: Commit**

```bash
git add main.go
git commit -m "feat: add /whitelabel endpoint"
```

---

### Task 4: Create pdf_renderer.go — Local PDF generation from pdfme schema

**Files:**
- Create: `pdf_renderer.go`

**Step 1: Create the PDF renderer**

```go
package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
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
	Name      string       `json:"name"`
	Type      string       `json:"type"`
	Position  PdfmePos     `json:"position"`
	Width     float64      `json:"width"`  // mm
	Height    float64      `json:"height"` // mm
	FontSize  float64      `json:"fontSize,omitempty"`
	FontName  string       `json:"fontName,omitempty"`
	Alignment string       `json:"alignment,omitempty"`
	FontColor string       `json:"fontColor,omitempty"`
	Content   string       `json:"content,omitempty"` // default/static content
}

// PdfmePos is x,y in mm.
type PdfmePos struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// imageCache caches downloaded images.
var (
	imgCache   = map[string]string{} // url -> local path
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

	// Load a default font for text rendering
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

// renderTextField draws positioned text.
func renderTextField(pdf *gopdf.GoPdf, field PdfmeField, value string, x, y, w, h float64, fontPath string) {
	fontSize := field.FontSize
	if fontSize == 0 {
		fontSize = 10
	}
	// Scale: pdfme fontSize is in pt already
	if fontPath != "" {
		if err := pdf.SetFont("default", "", fontSize); err != nil {
			log.Printf("[pdf] font error: %v", err)
			return
		}
	} else {
		return // no font available
	}

	// Set color
	if field.FontColor != "" {
		r, g, b := hexToRGB(field.FontColor)
		pdf.SetTextColor(r, g, b)
	} else {
		pdf.SetTextColor(0, 0, 0)
	}

	// Handle alignment
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

	// Use CellWithOption for bounded text
	pdf.CellWithOption(&gopdf.Rect{W: w, H: h}, value, gopdf.CellOption{})
}

// renderQRField generates a QR code and embeds it.
func renderQRField(pdf *gopdf.GoPdf, value string, x, y, w, h float64) {
	qr, err := goqrcode.New(value, goqrcode.Medium)
	if err != nil {
		log.Printf("[pdf] QR error: %v", err)
		return
	}

	// Write QR to temp file
	tmpFile, err := os.CreateTemp("", "qr-*.png")
	if err != nil {
		return
	}
	defer os.Remove(tmpFile.Name())

	size := int(w / mmToPt * 10) // pixels
	if size < 100 {
		size = 100
	}
	qr.DisableBorder = true
	if err := qr.WriteFile(size, tmpFile.Name()); err != nil {
		return
	}

	pdf.Image(tmpFile.Name(), x, y, &gopdf.Rect{W: w, H: h})
}

// renderBarcodeField generates a barcode and embeds it.
func renderBarcodeField(pdf *gopdf.GoPdf, value string, x, y, w, h float64) {
	var bc barcode.Barcode
	var err error

	// Try Code128 first, then EAN
	bc, err = code128.Encode(value)
	if err != nil {
		bc, err = ean.Encode(value)
		if err != nil {
			log.Printf("[pdf] barcode error for %q: %v", value, err)
			return
		}
	}

	// Scale barcode to desired size
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

	// Write to temp file as PNG
	tmpFile, err := os.CreateTemp("", "bc-*.png")
	if err != nil {
		return
	}
	defer os.Remove(tmpFile.Name())

	if err := encodePNG(tmpFile, bc); err != nil {
		return
	}
	tmpFile.Close()

	pdf.Image(tmpFile.Name(), x, y, &gopdf.Rect{W: w, H: h})
}

// renderImageField downloads (or uses cached) image and embeds it.
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

// getCachedImage downloads an image URL and caches it locally.
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

// findDefaultFont returns a path to a system TTF font.
func findDefaultFont() string {
	candidates := []string{
		// macOS
		"/System/Library/Fonts/Helvetica.ttc",
		"/System/Library/Fonts/SFNSText.ttf",
		"/Library/Fonts/Arial.ttf",
		"/System/Library/Fonts/Supplemental/Arial.ttf",
		// Windows
		`C:\Windows\Fonts\arial.ttf`,
		`C:\Windows\Fonts\segoeui.ttf`,
		// Linux
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

// hexToRGB converts "#rrggbb" to r,g,b uint8.
func hexToRGB(hex string) (uint8, uint8, uint8) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 0, 0, 0
	}
	var r, g, b uint8
	fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b)
	return r, g, b
}

// encodePNG writes an image.Image as PNG to a writer.
func encodePNG(w io.Writer, img image.Image) error {
	// Use standard library png encoder
	// Import is in the import block
	return encodePNGImage(w, img)
}
```

Note: We need a small helper for PNG encoding. Add to imports: `"image/png"` and:

```go
func encodePNGImage(w io.Writer, img image.Image) error {
	return png.Encode(w, img)
}
```

**Step 2: Verify compile**

Run: `go build -o /dev/null .`

Fix any import issues (likely need to add `image/png`, remove unused `image/color`).

**Step 3: Commit**

```bash
git add pdf_renderer.go
git commit -m "feat: add local PDF renderer for pdfme schemas"
```

---

### Task 5: Add /batch-pdf endpoint in main.go

**Files:**
- Modify: `main.go`

**Step 1: Add handleBatchPdf handler**

Add this function (near `handleBatchPrint`):

```go
// handleBatchPdf generates a multi-page PDF from Excel rows + pdfme template.
func handleBatchPdf(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}

	var req struct {
		TemplateID string              `json:"template_id"`
		Rows       []map[string]string `json:"rows"`
		Mapping    map[string]string   `json:"mapping"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.TemplateID == "" || len(req.Rows) == 0 {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "template_id and rows required"})
		return
	}

	// Fetch template detail from backend
	cfg := getConfig()
	client := NewApiClient(cfg)
	detail, err := client.FetchTemplateDetail(req.TemplateID)
	if err != nil {
		jsonResponse(w, http.StatusBadGateway, map[string]string{"error": "fetch template: " + err.Error()})
		return
	}
	if detail.Schema == nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "template has no pdfme schema"})
		return
	}

	schema, err := ParsePdfmeSchema(detail.Schema)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "parse schema: " + err.Error()})
		return
	}

	// Apply column mapping if provided
	mappedRows := req.Rows
	if len(req.Mapping) > 0 {
		mappedRows = make([]map[string]string, len(req.Rows))
		for i, row := range req.Rows {
			mapped := make(map[string]string)
			for fieldName, colName := range req.Mapping {
				if val, ok := row[colName]; ok {
					mapped[fieldName] = val
				}
			}
			mappedRows[i] = mapped
		}
	}

	// Generate PDF
	outputDir := filepath.Join(configDir(), "output")
	os.MkdirAll(outputDir, 0755)
	outputPath := filepath.Join(outputDir, fmt.Sprintf("batch_%d.pdf", time.Now().UnixMilli()))

	if err := RenderBulkPDF(schema, mappedRows, outputPath); err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "render PDF: " + err.Error()})
		return
	}

	// Serve the file as download
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="batch_%d.pdf"`, len(mappedRows)))
	http.ServeFile(w, r, outputPath)
}
```

**Step 2: Register the route**

In `startServers()`, add:

```go
	mux.HandleFunc("/batch-pdf", corsMiddleware(handleBatchPdf))
```

**Step 3: Add missing imports to main.go**

Ensure `"time"` is in the imports of main.go (likely already there, verify).

**Step 4: Verify compile**

Run: `go build -o /dev/null .`

**Step 5: Commit**

```bash
git add main.go
git commit -m "feat: add /batch-pdf endpoint for bulk PDF generation"
```

---

### Task 6: Create download_server.go — ZIP distribution microservice

**Files:**
- Create: `download_server.go`

**Step 1: Create the download server**

```go
package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// handleBridgeDownload generates a ZIP containing the bridge binary + pre-filled config.
// Called from ISI Hospital frontend with client credentials.
// POST /bridge/download { "api_url", "api_key", "api_secret", "wl_id", "wl_name", "wl_logo_url", "wl_primary_color", "os": "windows"|"mac" }
func handleBridgeDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}

	var req struct {
		ApiURL         string `json:"api_url"`
		ApiKey         string `json:"api_key"`
		ApiSecret      string `json:"api_secret"`
		WlID           int    `json:"wl_id"`
		WlName         string `json:"wl_name"`
		WlLogoURL      string `json:"wl_logo_url"`
		WlPrimaryColor string `json:"wl_primary_color"`
		WlAccentColor  string `json:"wl_accent_color"`
		OS             string `json:"os"` // "windows" or "mac"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	if req.ApiKey == "" || req.ApiSecret == "" {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "api_key and api_secret required"})
		return
	}

	targetOS := strings.ToLower(req.OS)
	if targetOS == "" {
		targetOS = runtime.GOOS
	}

	// Find the binary to package
	distDir := filepath.Join(filepath.Dir(os.Args[0]), "dist")
	var binaryName, binaryPath string

	switch targetOS {
	case "windows":
		binaryName = "tsc-bridge.exe"
	default:
		binaryName = "tsc-bridge-mac"
	}

	// Look in dist/ first, then same directory as running binary
	binaryPath = filepath.Join(distDir, binaryName)
	if _, err := os.Stat(binaryPath); err != nil {
		// Try current executable
		exe, _ := os.Executable()
		binaryPath = exe
		binaryName = filepath.Base(exe)
	}

	if _, err := os.Stat(binaryPath); err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "binary not found: " + binaryName})
		return
	}

	// Generate config.json
	cfg := AppConfig{
		Port:                9638,
		DefaultPreset:       "matrix-3x1-30x22",
		AutoStart:           true,
		NetworkScanEnabled:  true,
		NetworkScanInterval: 30,
		ManualPrinters:      []string{},
		CustomPresets:       []LabelPreset{},
		SharePort:           9100,
		ApiURL:              req.ApiURL,
		ApiKey:              req.ApiKey,
		ApiSecret:           req.ApiSecret,
		ApiWhiteLabel:       req.WlID,
		Whitelabel: WhitelabelConfig{
			ID:           req.WlID,
			Name:         req.WlName,
			LogoURL:      req.WlLogoURL,
			PrimaryColor: req.WlPrimaryColor,
			AccentColor:  req.WlAccentColor,
		},
	}

	cfgJSON, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "config marshal: " + err.Error()})
		return
	}

	// Stream ZIP to response
	zipName := fmt.Sprintf("tsc-bridge-%s.zip", targetOS)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, zipName))

	zw := zip.NewWriter(w)
	defer zw.Close()

	// Add binary
	binaryFile, err := os.Open(binaryPath)
	if err != nil {
		log.Printf("[download] open binary: %v", err)
		return
	}
	defer binaryFile.Close()

	binaryStat, _ := binaryFile.Stat()
	header, _ := zip.FileInfoHeader(binaryStat)
	header.Name = binaryName
	header.Method = zip.Deflate

	bw, err := zw.CreateHeader(header)
	if err != nil {
		return
	}
	io.Copy(bw, binaryFile)

	// Add config.json
	cw, err := zw.Create("config.json")
	if err != nil {
		return
	}
	cw.Write(cfgJSON)

	log.Printf("[download] Generated %s for WL=%d (%s)", zipName, req.WlID, req.WlName)
}
```

**Step 2: Register the route in main.go**

In `startServers()`, add:

```go
	mux.HandleFunc("/bridge/download", corsMiddleware(handleBridgeDownload))
```

**Step 3: Verify compile**

Run: `go build -o /dev/null .`

**Step 4: Commit**

```bash
git add download_server.go main.go
git commit -m "feat: add /bridge/download endpoint for pre-configured ZIP distribution"
```

---

### Task 7: Rewrite dashboard.html — Tabbed professional UI with whitelabel branding

This is the largest task. The dashboard is a single embedded HTML file (~2200 lines). We rewrite it with:
- Tab navigation (Impresoras | Plantillas | Batch)
- Dynamic whitelabel branding (logo, colors from /whitelabel)
- PDF output option in batch wizard
- Clean, professional design

**Files:**
- Modify: `dashboard.html`

**Step 1: Rewrite the full dashboard**

This is a full rewrite of the embedded HTML. The new dashboard should have:

**HTML structure:**
```
header: logo + app name (from whitelabel) + version badge
nav: 3 tabs (Impresoras, Plantillas, Batch)
main: tab content panels
  - panel-printers: printer list, drivers, manual add, network scan, sharing, config
  - panel-templates: synced template grid with pdfme previews
  - panel-batch: wizard (upload > template > mapping > output choice > execute)
footer: status bar (connection, printer count)
```

**Key JS changes:**
- On load: fetch `/whitelabel` and apply branding (CSS variables, logo, name)
- Tab switching with history state
- Batch wizard step 4 adds output toggle: "Imprimir etiquetas (TSPL)" vs "Generar PDF"
- "Generar PDF" calls `POST /batch-pdf` and triggers browser download
- Template grid fetches from `/api/templates` and shows pdfme spatial preview

**CSS approach:**
- CSS custom properties for theming: `--primary`, `--accent`, `--bg`, `--surface`, `--text`
- Default light theme (professional, clean)
- Whitelabel overrides applied dynamically via JS

Due to the size of this file (~2000+ lines), this will be implemented as a complete rewrite of dashboard.html with the new tab structure and all existing functionality preserved but reorganized.

**Step 2: Verify by opening in browser**

Run the bridge, open `https://localhost:9639`, verify:
- Tabs switch correctly
- Printers load
- Templates sync from backend
- Batch wizard works through all steps
- PDF output option appears and downloads

**Step 3: Commit**

```bash
git add dashboard.html
git commit -m "feat: rewrite dashboard with tabbed UI, whitelabel branding, PDF batch output"
```

---

### Task 8: Build and verify end-to-end

**Files:** None (verification only)

**Step 1: Build macOS**

```bash
cd /Users/mario/PARA/1_Proyectos/ISI_Hospital/Servicios/tsc-bridge
go build -o dist/tsc-bridge-mac .
```

**Step 2: Build Windows**

```bash
CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc GOOS=windows GOARCH=amd64 go build -ldflags="-H windowsgui" -o dist/tsc-bridge.exe .
```

**Step 3: Test config.json loading**

Create a test config.json in `~/.tsc-bridge/config.json`:
```json
{
  "api_url": "https://api.anysubscriptions.com",
  "api_key": "TEST_KEY",
  "api_secret": "TEST_SECRET",
  "api_wl": 29,
  "whitelabel": {
    "id": 29,
    "name": "ISI Hospital Test",
    "primary_color": "#6366f1"
  },
  "port": 9638
}
```

Run: `./dist/tsc-bridge-mac`
Open: `https://localhost:9639`
Verify: Dashboard shows "ISI Hospital Test" branding, API connects.

**Step 4: Test batch PDF**

1. Upload an Excel file
2. Select a backend template
3. Map columns
4. Choose "Generar PDF"
5. Verify PDF downloads with correct content

**Step 5: Test /bridge/download**

```bash
curl -X POST https://localhost:9639/bridge/download \
  -H "Content-Type: application/json" \
  -d '{"api_key":"TEST","api_secret":"SEC","wl_id":29,"wl_name":"Test","os":"mac"}' \
  -o test-download.zip -k
unzip -l test-download.zip
```

Verify ZIP contains binary + config.json.

**Step 6: Final commit**

```bash
git add -A
git commit -m "feat: TSC Bridge v3.0 - professional dashboard, whitelabel, bulk PDF"
```

---

## Execution Order

| Task | Description | Dependencies |
|------|-------------|-------------|
| 1 | Add Go dependencies | None |
| 2 | Refactor config.go | None |
| 3 | Add /whitelabel route | Task 2 |
| 4 | Create pdf_renderer.go | Task 1 |
| 5 | Add /batch-pdf endpoint | Task 4 |
| 6 | Create download_server.go | Task 2 |
| 7 | Rewrite dashboard.html | Tasks 3, 5, 6 |
| 8 | Build and verify | All tasks |

Tasks 1, 2 can run in parallel.
Tasks 4, 6 can run in parallel (both depend on 1 or 2).
Task 7 depends on everything.
Task 8 is final verification.
