# TSC Bridge v3.0 — Native Dashboard Design

**Date:** 2026-03-07
**Status:** Approved
**Repository:** ISI_Hospital/Servicios/tsc-bridge

---

## 1. Problem Statement

The current TSC Bridge v2.3.0 dashboard has significant issues:
- **Redundancy**: Printer selector in 5 places, preset selector in 5 places, status in 3 places
- **Browser-dependent**: Requires opening an external browser to access the dashboard
- **No authentication**: Anyone on the network can access the dashboard
- **Hardcoded DPI**: All TSPL generation assumes 203 DPI
- **No label designer**: Templates must be created externally (pdfme editor) and imported
- **No QR builder**: QR content (vCard, URL) is constructed manually in frontend code

## 2. Solution Overview

TSC Bridge v3.0 = **Go native app** with:
- **System tray** icon (always running, desatendido)
- **Webview window** (native, no browser) for full dashboard
- **Setup wizard** for first-run configuration
- **API key authentication** compatible with paygateway-api.com
- **DPI auto-detection** chain (driver -> TSPL probe -> manual)
- **Label Designer** with drag-and-drop, standards support, QR builder
- **Batch processor** with formula concatenation in field mapping
- **Zero redundancy** dashboard with 5 clean tabs

## 3. Architecture

```
+---------------------------------------------+
|           TSC Bridge v3 (Go binary)          |
|                                              |
|  +--------+  +--------+  +-----------+      |
|  |Systray |  |Webview |  | HTTP API  |      |
|  |(background)|        |  | (localhost)|     |
|  +--------+  +--------+  +-----------+      |
|      |           |             |             |
|  +---+-----------+-------------+----------+  |
|  |              Core Engine               |  |
|  |  +------+ +--------+ +-------------+  |  |
|  |  | Auth | |Printer | |TSPL Renderer|  |  |
|  |  |Mgr   | |Scanner | |(pdfme->TSPL)|  |  |
|  |  +------+ +--------+ +-------------+  |  |
|  |  +------+ +--------+ +-------------+  |  |
|  |  | DPI  | | Config | | Batch/Excel |  |  |
|  |  |Detect| | Store  | | Processor   |  |  |
|  |  +------+ +--------+ +-------------+  |  |
|  +----------------------------------------+  |
+----------------------------------------------+
```

### Three interfaces:
- **Systray**: Always active, context menu for quick actions
- **Webview**: Native window (Go webview library) for full dashboard
- **HTTP API**: For AnySubscription/ISI frontend to send print jobs

### Technology:
- **Go + github.com/webview/webview** for native window
- **getlantern/systray** for system tray (already in use)
- **//go:embed** for dashboard HTML (already in use)
- Single binary, ~15MB

## 4. Authentication

### API Key Model (compatible with paygateway-api.com)

The bridge authenticates using `accessKey` + `accessToken` from the `usuarioApi` table,
exactly as the existing JWT.php auth middleware works.

### Two setup paths:

**Path A: Pre-configured download (zero setup)**
1. Staff user downloads bridge from admin panel (already logged in)
2. Backend generates `config.json` with api_key, api_secret, whitelabel branding
3. Bridge starts fully configured

**Path B: Manual setup wizard**
1. User enters Store URL
2. User enters API Key + Secret (or logs in with user/pass to generate keys)
3. Bridge validates credentials against backend
4. Fetches whitelabel config, stores encrypted credentials

### Request authentication:
```
Authorization: Bearer {accessKey}:{accessToken}
X-WhiteLabel: {wl_id}
```

### API key limitations:
- Scoped to: /pdf-templates, /pdfs/generate-tspl-commands, /printer/*
- No access to orders, payments, clients
- Rate limiting per key

### Token refresh:
- API keys don't expire (persistent)
- Bridge validates connectivity on startup
- If credentials invalid, shows setup wizard

## 5. DPI Auto-Detection

### Detection chain (per printer):
```
1. Driver query (fast, ~10ms)
   - Windows: WMI PrinterInfo2 -> xRes/yRes
   - macOS: lpoptions -> Resolution attribute
   Success -> save + done

2. TSPL ~!I probe (~1s)
   - Send ~!I to printer, parse response for DPI
   Success -> save + done

3. Fallback: manual selection
   - User picks from [200, 203, 300, 600]
   - Saved per-printer
```

### Config storage:
```json
{
  "printer_dpi": {
    "TSC TE200": {"dpi": 203, "source": "tspl_probe"},
    "TSC TTP-345": {"dpi": 300, "source": "driver"}
  }
}
```

### HTTP API exposes DPI:
```json
GET /status
{
  "printers": [...],
  "printer_dpi": {"TSC TE200": 203, "TTP-345": 300},
  "default_dpi": 203
}
```

Frontend reads DPI from bridge and passes to backend when generating TSPL.

## 6. Dashboard (Webview) — 5 Tabs, Zero Redundancy

### Layout:
```
+--------------------------------------------------+
| [logo] Expomotriz              * TSC TE200   v3.0|
|--------------------------------------------------|
| [Impresoras] [Disenar] [Batch] [Templates] [Config]|
|--------------------------------------------------|
|                                                  |
|              (tab content)                       |
|                                                  |
+--------------------------------------------------+
```

### Global state (set once, used everywhere):
- Selected printer (in header, not per-tab)
- Active preset (in header)
- DPI (derived from selected printer)
- Auth state (in header badge)

### Tab 1: Impresoras
- Unified printer list (USB + network) with type badge and detected DPI
- Per-printer actions: [Set Default] [Test Print] [Share]
- Network scanner with manual IP
- USB sharing toggle
- Autostart toggle

### Tab 2: Disenar (Label Designer)
See Section 7.

### Tab 3: Batch
- 4-step wizard (upload Excel -> select template -> map columns -> execute)
- Uses global printer/preset selection (no duplicate selectors)
- Formula concatenation in mapping step (see Section 8)
- Output: Print TSPL or Generate PDF
- Progress bar with SSE streaming

### Tab 4: Templates
- Grid of templates fetched from backend API (authenticated)
- Local templates
- Per-template: [Preview] [Edit in Designer] [Print] [Use in Batch]
- Template preview as rasterized PNG at target DPI

### Tab 5: Config
- Account section: Store URL, connected user, API key status, logout/reconnect
- Printing section: Default printer, default preset, DPI per printer (editable)
- System section: HTTP port, autostart, printer sharing, network scan interval
- Downloads section: macOS / Windows installer links

## 7. Label Designer

### Canvas
- Real-scale rendering (mm -> px at screen DPI)
- Drag-and-drop elements on label surface
- Snap-to-grid optional
- Zoom controls

### Toolbar elements:
- **Text** (T): Single/multi-line, TSPL fonts 1-5 with multipliers
- **Barcode**: Code128, Code39, EAN13, EAN8, UPC-A, UPC-E, Code93, ITF14, NW7
- **QR Code**: With QR Builder (see Section 7.1)
- **Line**: Horizontal/vertical
- **Rectangle**: Outline or filled
- **Image**: Bitmap (for logos, converted to BITMAP TSPL command)

### Properties panel (right side):
- Position (x, y in mm)
- Size (width, height in mm)
- Font (TSPL font number + multiplier)
- Content (literal text or {variable} placeholder)
- Rotation (0, 90, 180, 270)
- Alignment (left, center, right)

### Standards selector:
- **None**: Free-form, no validation
- **Gafete/Credential**: Validates name present, vCard in QR, badge dimensions
- **GS1-128**: Validates Application Identifiers, GTIN check digits, required fields
- **ISO 15223 (Patient)**: Validates MRN, DOB, required identification fields

### TSPL Preview:
- Live-updating TSPL code as elements are moved/resized
- TSPL output adapted to selected printer's DPI
- Rasterize button: generates PNG preview at target DPI

### Export:
- Save locally (bridge template store)
- Upload to backend API (pdfme-compatible JSON format)
- Export as pdfme JSON

### 7.1 QR Builder

When a QR element is selected, the properties panel shows a QR content builder:

**Type: vCard**
```
Name:     [{nombre}     v]
Surname:  [{apellido}   v]
Phone:    [{telefono}   v]
Email:    [{correo}     v]
Company:  [{empresa}    v]
Title:    [{puesto}     v]
Address:  [(optional)   v]
```
Generates valid vCard 3.0:
```
BEGIN:VCARD
VERSION:3.0
FN:{nombre} {apellido}
N:{apellido};{nombre};
TEL;TYPE=CELL:{telefono}
EMAIL:{correo}
ORG:{empresa}
TITLE:{puesto}
END:VCARD
```

**Type: URL**
```
Pattern: [https://stl.lat/{codigo}]
```

**Type: WiFi**
```
SSID:     [{ssid}    ]
Password: [{pass}    ]
Security: [WPA v]
```
Generates: `WIFI:S:{ssid};T:WPA;P:{pass};;`

**Type: Free text**
```
Content: [{campo1}-{campo2}/{campo3}]
```

## 8. Batch Formula Concatenation

In the batch wizard Step 3 (Column Mapping), each template field can be mapped as:

### Mapping modes per field:

**Direct column:**
```
Template field: {nombre}
Mode: [Column v]
Value: [Col_A v]
```

**Formula (concatenation):**
```
Template field: {nombre_completo}
Mode: [Formula v]
Value: {Col_A} + " " + {Col_B}
```
Supported operators: `+` (concatenate), literal strings in quotes.

**QR vCard auto-build:**
```
Template field: {token}
Mode: [vCard Builder v]
  FN:    Col_A + " " + Col_B
  TEL:   Col_C
  EMAIL: Col_D
  ORG:   Col_E
  TITLE: Col_F
```
Automatically builds valid vCard 3.0 per row.

**QR URL pattern:**
```
Template field: {qr_url}
Mode: [URL Pattern v]
Value: https://example.com/{Col_G}
```

### Preview:
The mapping step shows a live preview of the first 4 rows with formulas applied,
so the user can verify before executing the batch.

## 9. System Tray

### Menu structure:
```
* TSC Bridge -- Expomotriz
--------------------------
  TSC TE200 (203 DPI)     [default]
  TSC TTP-345 (300 DPI)
--------------------------
  Abrir Dashboard
  Re-detectar impresoras
  Test Print
--------------------------
  Configurar...
  Salir
```

### Tray icon states:
- Green dot: Connected, printer available
- Yellow dot: Connected, no printer
- Red dot: Auth error or disconnected
- Gray dot: Starting up

### Behavior:
- Left-click: Opens webview dashboard
- Right-click: Context menu
- On first run without config: auto-opens setup wizard in webview

## 10. File Structure

```
tsc-bridge/
  main.go              # Entry: systray + webview + HTTP server
  auth.go              # API key auth, credential validation, login flow
  config.go            # AppConfig with printer_dpi, auth state, whitelabel
  dashboard.go         # //go:embed dashboard.html
  dashboard.html       # Redesigned dashboard (~2000 lines, 5 tabs)
  dpi.go               # DPI detection: driver query + TSPL probe + manual
  webview.go           # Webview window lifecycle (open, close, navigate)
  tray.go              # Systray menu, icon states, actions
  printer_darwin.go    # macOS printer detection (CUPS)
  printer_windows.go   # Windows printer detection (WMI/Spooler)
  printer_other.go     # Linux printer detection
  network.go           # Network scanner, manual IPs
  share.go             # USB printer sharing via TCP
  tspl_renderer.go     # pdfme -> TSPL engine (DPI-aware)
  batch.go             # Excel upload, batch processing, PDF generation
  presets.go           # Label size presets
  label_template.go    # Template storage and pdfme compatibility
  tls.go               # Self-signed certificate generation
  excel.go             # Excel parsing (excelize)
  driver.go            # Driver detection abstraction
  driver_darwin.go     # macOS CUPS driver queries
  driver_windows.go    # Windows WMI driver queries
  browser.go           # Open-in-browser fallback
  icon.go              # Tray icon assets
  go.mod
  go.sum
```

## 11. Migration from v2.3.0

### What stays:
- All Go backend logic (printing, network, sharing, presets, TLS, batch, TSPL renderer)
- Config structure (extended with new fields)
- HTTP API endpoints (all existing ones preserved)

### What changes:
- `main.go`: Add webview initialization, setup wizard flow
- `config.go`: Add `printer_dpi`, extend auth fields
- `dashboard.html`: Complete rewrite (remove redundancy, add 5-tab layout, designer, QR builder)
- New files: `dpi.go`, `webview.go`, `auth.go`

### Config migration:
- v2 configs auto-migrate (new fields get defaults)
- Existing api_key/api_secret preserved
- printer_dpi populated on first printer detection

## 12. Dependencies

### Go modules (new):
- `github.com/webview/webview` — Native webview window
- (systray already present via getlantern/systray)

### Existing (kept):
- `github.com/xuri/excelize/v2` — Excel parsing
- `github.com/getlantern/systray` — System tray
- Standard library for HTTP, TLS, crypto, image

### Build:
- Windows: requires CGO + WebView2 SDK
- macOS: WebKit is built-in (no extra deps)
- Cross-compile via GitHub Actions (already set up)
