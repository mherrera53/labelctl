# Driver Development Guide

This document explains how to write a printer driver for TSC Bridge. A driver
translates the universal label format into a printer-specific command language
(TSPL, ZPL, EPL, etc.).

## Overview

TSC Bridge uses a two-stage rendering pipeline:

1. **Parse**: The JSON label template is parsed into a `LabelTemplate` struct
2. **Render**: A driver converts the parsed template into printer commands

Currently, the project has two built-in renderers: TSPL (`tspl_renderer.go`)
and PDF (`pdf_renderer.go`). New drivers follow the same pattern.

## The Driver Interface

```go
// Driver renders labels in a printer-specific language.
type Driver interface {
    // Name returns a unique identifier for this driver (e.g., "zpl").
    Name() string

    // Languages returns the command languages this driver produces.
    Languages() []string

    // Render converts a label template and row data into printer commands.
    Render(schema *PdfmeSchema, row map[string]string, opts RenderOpts) ([]byte, error)

    // RenderBulk converts multiple rows into a single print job.
    RenderBulk(schema *PdfmeSchema, rows []map[string]string, opts RenderOpts) ([]byte, error)

    // Capabilities reports what features this driver supports.
    Capabilities() DriverCapabilities
}
```

### RenderOpts

```go
type RenderOpts struct {
    DPI       int    // Target DPI (203, 300, 600)
    Copies    int    // Number of copies per label
    Mode      string // "print", "preview", "raster"
    PrinterID string // Target printer identifier
}
```

### DriverCapabilities

```go
type DriverCapabilities struct {
    Barcodes     []string // e.g., ["128", "39", "ean13", "upca"]
    QRCode       bool
    Images       bool     // Raster image embedding
    TrueType     bool     // TrueType font support
    Rotation     []int    // Supported angles: [0, 90, 180, 270]
    MaxDPI       int      // Maximum supported DPI
    MultiPage    bool     // Multiple labels in one job
    CutSupport   bool     // Cutter commands
    VariableData bool     // Variable substitution in firmware
}
```

## Step-by-Step: Writing a ZPL Driver

This walkthrough creates a Zebra ZPL driver as a reference implementation.

### 1. Create the File

Create `zpl_renderer.go`:

```go
package main

import (
    "bytes"
    "fmt"
    "strings"
)

type ZPLDriver struct{}

func init() {
    RegisterDriver(&ZPLDriver{})
}

func (d *ZPLDriver) Name() string { return "zpl" }

func (d *ZPLDriver) Languages() []string { return []string{"ZPL", "ZPL II"} }
```

### 2. Implement Render

The `Render` method receives a parsed schema and a single row of data. It must
produce valid ZPL commands as a byte slice.

```go
func (d *ZPLDriver) Render(schema *PdfmeSchema, row map[string]string, opts RenderOpts) ([]byte, error) {
    if len(schema.Schemas) == 0 {
        return nil, fmt.Errorf("zpl: empty schema")
    }

    dpi := opts.DPI
    if dpi == 0 {
        dpi = 203
    }

    var buf bytes.Buffer

    // Label dimensions (mm to dots)
    wDots := mmToDots(schema.BasePdf.Width, dpi)
    hDots := mmToDots(schema.BasePdf.Height, dpi)

    buf.WriteString("^XA\n")                              // Start format
    buf.WriteString(fmt.Sprintf("^PW%d\n", wDots))        // Print width
    buf.WriteString(fmt.Sprintf("^LL%d\n", hDots))        // Label length

    // Render each field
    for _, field := range schema.Schemas[0] {
        value := resolveFieldValue(field, row)
        x := mmToDots(field.Position.X, dpi)
        y := mmToDots(field.Position.Y, dpi)

        switch field.Type {
        case "text", "multiVariableText":
            d.renderText(&buf, x, y, value, field, dpi)
        case "barcodes128":
            d.renderBarcode128(&buf, x, y, value, field, dpi)
        case "qrcode":
            d.renderQRCode(&buf, x, y, value, field, dpi)
        case "image":
            d.renderImage(&buf, x, y, value, field, dpi)
        case "line":
            d.renderLine(&buf, x, y, field, dpi)
        }
    }

    buf.WriteString(fmt.Sprintf("^PQ%d\n", opts.Copies))  // Print quantity
    buf.WriteString("^XZ\n")                               // End format

    return buf.Bytes(), nil
}
```

### 3. Implement Field Renderers

Each field type needs a rendering function. Here is text as an example:

```go
func (d *ZPLDriver) renderText(buf *bytes.Buffer, x, y int, value string, field PdfmeField, dpi int) {
    // ZPL font size approximation
    fontSize := field.FontSize
    if fontSize == 0 {
        fontSize = 10
    }
    fontH := int(float64(fontSize) * float64(dpi) / 72.0)
    fontW := fontH

    // Field origin
    buf.WriteString(fmt.Sprintf("^FO%d,%d\n", x, y))

    // Font selection (0 = default scalable font)
    buf.WriteString(fmt.Sprintf("^A0N,%d,%d\n", fontH, fontW))

    // Field data
    buf.WriteString(fmt.Sprintf("^FD%s^FS\n", zplEscape(value)))
}

func zplEscape(s string) string {
    // ZPL uses ~ as escape character
    s = strings.ReplaceAll(s, "~", "~~")
    s = strings.ReplaceAll(s, "^", "~^")
    return s
}
```

### 4. Implement RenderBulk

```go
func (d *ZPLDriver) RenderBulk(schema *PdfmeSchema, rows []map[string]string, opts RenderOpts) ([]byte, error) {
    var buf bytes.Buffer
    for _, row := range rows {
        label, err := d.Render(schema, row, opts)
        if err != nil {
            return nil, fmt.Errorf("zpl bulk row: %w", err)
        }
        buf.Write(label)
    }
    return buf.Bytes(), nil
}
```

### 5. Declare Capabilities

```go
func (d *ZPLDriver) Capabilities() DriverCapabilities {
    return DriverCapabilities{
        Barcodes:     []string{"128", "39", "ean13", "upca", "itf", "codabar"},
        QRCode:       true,
        Images:       true,
        TrueType:     true,
        Rotation:     []int{0, 90, 180, 270},
        MaxDPI:       600,
        MultiPage:    true,
        CutSupport:   true,
        VariableData: false,
    }
}
```

### 6. Write Tests

Create `zpl_renderer_test.go`:

```go
package main

import (
    "strings"
    "testing"
)

func TestZPLRenderText(t *testing.T) {
    schema := &PdfmeSchema{
        BasePdf: BasePdf{Width: 50, Height: 30},
        Schemas: [][]PdfmeField{{
            {
                Name:     "title",
                Type:     "text",
                Position: Position{X: 5, Y: 5},
                Width:    40,
                Height:   8,
                FontSize: 12,
            },
        }},
    }

    row := map[string]string{"title": "Hello World"}
    opts := RenderOpts{DPI: 203, Copies: 1}

    driver := &ZPLDriver{}
    out, err := driver.Render(schema, row, opts)
    if err != nil {
        t.Fatalf("render failed: %v", err)
    }

    result := string(out)

    if !strings.HasPrefix(result, "^XA") {
        t.Error("expected ZPL to start with ^XA")
    }
    if !strings.Contains(result, "Hello World") {
        t.Error("expected output to contain field data")
    }
    if !strings.HasSuffix(strings.TrimSpace(result), "^XZ") {
        t.Error("expected ZPL to end with ^XZ")
    }
}

func TestZPLRenderBarcode(t *testing.T) {
    schema := &PdfmeSchema{
        BasePdf: BasePdf{Width: 50, Height: 30},
        Schemas: [][]PdfmeField{{
            {
                Name:     "code",
                Type:     "barcodes128",
                Position: Position{X: 5, Y: 15},
                Width:    40,
                Height:   10,
            },
        }},
    }

    row := map[string]string{"code": "1234567890"}
    opts := RenderOpts{DPI: 203, Copies: 1}

    driver := &ZPLDriver{}
    out, err := driver.Render(schema, row, opts)
    if err != nil {
        t.Fatalf("render failed: %v", err)
    }

    if !strings.Contains(string(out), "1234567890") {
        t.Error("expected barcode data in output")
    }
}
```

### 7. Register the Driver

Driver registration happens in `init()` (already done in step 1). The
`RegisterDriver` function adds the driver to the global registry:

```go
var driverRegistry = make(map[string]Driver)

func RegisterDriver(d Driver) {
    driverRegistry[d.Name()] = d
}

func GetDriver(name string) (Driver, bool) {
    d, ok := driverRegistry[name]
    return d, ok
}
```

### 8. Document the Driver

Create `docs/drivers/zpl.md`:

```markdown
# ZPL Driver

The ZPL driver generates Zebra Programming Language II commands for Zebra
thermal printers.

## Supported Printers

- Zebra ZD420, ZD620 series
- Zebra ZT230, ZT410, ZT610 series
- Zebra GK420, GX420 series

## Supported Features

| Feature | Supported |
|---------|-----------|
| Text | Yes |
| Code 128 | Yes |
| Code 39 | Yes |
| QR Code | Yes |
| Images | Yes (GRF format) |
| Rotation | 0, 90, 180, 270 |
| Max DPI | 600 |

## ZPL-Specific Notes

- Font mapping: the driver maps generic font sizes to ZPL ^A0 scalable font
- Images are converted to GRF (Graphic Relief Format) for embedding
- The driver uses ^CI28 for UTF-8 character encoding
```

## Utility Functions

These functions from the existing codebase are available to all drivers:

| Function | File | Purpose |
|----------|------|---------|
| `ParsePdfmeSchema()` | `label_template.go` | Parse JSON schema |
| `resolveFieldValue()` | `pdf_renderer.go` | Resolve field value from row |
| `enrichRowForVariables()` | `pdf_renderer.go` | Match short variable names |
| `mmToDots()` | `tspl_renderer.go` | Convert millimeters to dots |
| `generateQRCode()` | `pdf_renderer.go` | Generate QR code image |
| `generateBarcode()` | `pdf_renderer.go` | Generate barcode image |

## Testing Without Hardware

Drivers should be testable without a physical printer. The test strategy:

1. **Unit tests**: Verify that `Render()` produces syntactically valid commands
2. **Golden files**: Compare output against known-good command sequences stored
   in `testdata/`
3. **Raster mode**: Use `Mode: "raster"` to generate a bitmap preview instead
   of sending to the printer

### Golden File Testing

```go
func TestZPLGolden(t *testing.T) {
    // ... render a label ...

    golden := filepath.Join("testdata", "zpl_basic.golden")
    if *update {
        os.WriteFile(golden, out, 0644)
        return
    }

    expected, err := os.ReadFile(golden)
    if err != nil {
        t.Fatalf("read golden: %v", err)
    }
    if !bytes.Equal(out, expected) {
        t.Errorf("output differs from golden file")
    }
}
```

## Checklist

Before submitting a driver PR:

- [ ] `Name()` returns a unique, lowercase identifier
- [ ] All field types handled (text, barcode, QR, image, line, rectangle)
- [ ] DPI scaling is correct for 203, 300, and 600 DPI
- [ ] `Capabilities()` accurately reflects supported features
- [ ] Unit tests cover all field types
- [ ] Golden file tests for at least one complete label
- [ ] Documentation in `docs/drivers/<name>.md`
- [ ] README driver table updated
- [ ] Tested on at least one physical printer model (document which one)
