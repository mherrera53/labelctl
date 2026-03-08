# Architecture

This document describes the internal architecture of TSC Bridge for
contributors who want to understand the codebase, fix bugs, or write drivers.

## Overview

TSC Bridge is a single Go binary that embeds an HTML dashboard and runs three
subsystems concurrently:

1. **HTTP Server** -- serves the API and dashboard on localhost
2. **System Tray** -- native OS tray icon with context menu
3. **Native Window** -- platform-specific dashboard window (WKWebView on macOS,
   webview_go on Windows/Linux, browser fallback)

```
main.go
  |
  +-- tray.go           System tray lifecycle (fyne.io/systray)
  |
  +-- HTTP Server        net/http on 127.0.0.1:PORT
  |     |
  |     +-- /status      Bridge status
  |     +-- /printers    Printer enumeration
  |     +-- /print       Raw print job
  |     +-- /batch-pdf   PDF generation from template + rows
  |     +-- /batch-tspl  TSPL generation and printing
  |     +-- /dashboard   Embedded HTML (go:embed)
  |     +-- /output/     Serve generated files
  |     +-- /templates   Local template CRUD
  |     +-- ...          40+ endpoints
  |
  +-- webview_*.go       Native window (platform-specific)
  +-- config.go          Configuration management
  +-- auth.go            Backend authentication
```

## Source Organization

The project uses a flat package structure. All Go files are in `package main`.
Platform-specific code is separated by build tags.

### Core Files

| File | Responsibility |
|------|---------------|
| `main.go` | Entry point, HTTP router, server lifecycle |
| `config.go` | Read/write configuration, environment detection |
| `auth.go` | Token management, backend authentication |
| `api_client.go` | HTTP client for the backend API |
| `network.go` | Network utilities, device discovery |
| `tls.go` | Self-signed certificate generation |
| `crypto.go` | AES-256 encryption for stored credentials |

### Rendering Pipeline

| File | Responsibility |
|------|---------------|
| `pdf_renderer.go` | Renders label templates to PDF pages |
| `tspl_renderer.go` | Renders label templates to TSPL2 commands |
| `label_template.go` | Label template data structures and parsing |
| `presets.go` | Built-in label size presets |
| `batch.go` | Batch job management (Excel/CSV to labels) |
| `excel.go` | Excel file parsing |

### Platform Layer

| File | Platforms | Responsibility |
|------|-----------|---------------|
| `webview_darwin.go` | macOS | Native WKWebView via CGO |
| `webview.go` | Windows, Linux | webview_go library |
| `webview_stub.go` | Cross-build | Browser-only fallback |
| `printer_windows.go` | Windows | Win32 raw printing |
| `printer_other.go` | macOS, Linux | libusb + CUPS printing |
| `printer_crossbuild.go` | Cross-build | CUPS-only (no libusb) |
| `driver_darwin.go` | macOS | TSC driver detection via IOKit |
| `driver_windows.go` | Windows | TSC driver detection via Registry |
| `driver_other.go` | Linux | Stub |
| `dpi_darwin.go` | macOS | DPI query via CUPS PPD |
| `dpi_windows.go` | Windows | DPI query via WMI |
| `dpi_other.go` | Linux | Stub |
| `autostart_darwin.go` | macOS | LaunchAgent plist |
| `autostart_windows.go` | Windows | Registry run key |
| `autostart_other.go` | Linux | Stub |
| `icon_darwin.go` | macOS | Dock icon via NSImage |
| `icon_windows.go` | Windows | Taskbar icon |
| `icon_other.go` | Linux | No-op |

### Dashboard

The file `dashboard.html` (embedded via `go:embed`) contains the entire
dashboard UI: HTML, CSS, and JavaScript in a single file. It uses Bootstrap 5
and vanilla JavaScript.

The dashboard has five tabs:

1. **Dashboard** -- printer status, quick print, connection info
2. **Designer** -- interactive label designer with drag-and-drop
3. **Batch** -- import Excel, map columns, bulk print
4. **Templates** -- manage local and server-side templates
5. **Settings** -- printer selection, DPI, backend connection

## Build Tags

| Tag | Purpose |
|-----|---------|
| `darwin` | macOS-specific code (CGO required) |
| `windows` | Windows-specific code |
| `!darwin && !windows` | Linux/other platforms |
| `crossbuild` | Cross-compilation without platform SDK headers |

The `crossbuild` tag disables libusb and webview_go, producing a binary that
uses CUPS for printing and the system browser for the dashboard.

## Configuration

Configuration lives in `~/.tsc-bridge/config.json`. The bridge creates this
directory on first run. Key settings:

- `port` -- HTTP server port (default: 9638)
- `printer` -- default printer name
- `dpi` -- printer DPI (auto-detected or manual)
- `backend` -- backend API URL for template sync
- `whitelabel` -- custom branding
- `tls` -- TLS certificate settings
- `cors` -- allowed CORS origins

## Rendering Pipeline

When a print job arrives:

```
JSON request
    |
    v
Parse template (label_template.go)
    |
    v
Resolve variables (pdf_renderer.go: enrichRowForVariables)
    |
    +---> PDF path: RenderBulkPDF() -> gopdf -> .pdf file
    |
    +---> TSPL path: RenderBulkTSPL() -> TSPL2 commands -> rawPrint()
    |
    +---> [Future] ZPL path: RenderBulkZPL() -> ZPL commands -> rawPrint()
```

### Variable Resolution

Label templates use field names like `{nombre}` or `{product_code}`. When
rendering, the bridge matches these variables against the row data using:

1. Exact match: `row["nombre"]`
2. Suffix match: `row["gafete_nombre"]` matches variable `nombre`
3. Normalized match: dots and underscores are interchangeable

This is implemented in `enrichRowForVariables()` and
`enrichRowForPlaceholders()` in `pdf_renderer.go`.

## Driver Interface (planned)

The current renderers (`tspl_renderer.go`, `pdf_renderer.go`) are tightly
coupled to the main package. The planned driver architecture will extract a
clean interface:

```go
// Driver renders labels in a printer-specific language.
type Driver interface {
    // Name returns the driver identifier (e.g., "tspl", "zpl", "epl").
    Name() string

    // Languages returns the label languages this driver supports.
    Languages() []string

    // Render converts a parsed label template and row data into
    // printer-ready commands.
    Render(template *LabelTemplate, row map[string]string, opts RenderOpts) ([]byte, error)

    // Capabilities returns what this driver supports.
    Capabilities() DriverCapabilities
}

type DriverCapabilities struct {
    Barcodes    []string // Supported barcode types
    QRCode      bool
    Images      bool
    TrueType    bool     // TrueType font embedding
    Rotation    []int    // Supported rotation angles
    MaxDPI      int
}

type RenderOpts struct {
    DPI    int
    Copies int
    Mode   string // "print", "preview", "raster"
}
```

See [docs/DRIVERS.md](docs/DRIVERS.md) for the full driver development guide.

## Testing

Tests are in `*_test.go` files alongside the code they test:

```sh
go test ./...
```

Key test files:

- `config_test.go` -- configuration read/write, DPI round-trip
- `auth_test.go` -- authentication state management
- `dpi_test.go` -- DPI detection parsing

When writing a driver, use the test harness described in
[docs/DRIVERS.md](docs/DRIVERS.md) to validate output without a physical
printer.
