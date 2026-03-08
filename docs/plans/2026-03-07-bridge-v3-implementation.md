# TSC Bridge v3.0 — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Transform TSC Bridge v2.3.0 into a native dashboard app with webview, DPI auto-detection, auth, label designer, QR builder, and zero-redundancy 5-tab UI.

**Architecture:** Extend the existing Go service with three new files (`dpi.go`, `webview.go`, `auth.go`), modify `config.go`, `main.go`, `tray.go`, and completely rewrite `dashboard.html`. All existing HTTP API endpoints and backend logic (printing, network, sharing, TSPL renderer) are preserved unchanged.

**Tech Stack:** Go 1.25+, github.com/webview/webview (native window), fyne.io/systray (tray, already present), Bootstrap 5, vanilla JS, //go:embed.

**Design Document:** `docs/plans/2026-03-07-bridge-v3-native-dashboard-design.md`

**Repository:** `/Users/mario/PARA/1_Proyectos/ISI_Hospital/Servicios/tsc-bridge/`

---

## Phase 1: Foundation — Config & DPI Detection

### Task 1: Add webview dependency

**Files:**
- Modify: `go.mod`

**Step 1: Add webview module**

```bash
cd /Users/mario/PARA/1_Proyectos/ISI_Hospital/Servicios/tsc-bridge
go get github.com/webview/webview/v2
```

**Step 2: Verify module resolves**

Run: `go mod tidy`
Expected: Clean exit, `go.sum` updated with webview entries.

**Step 3: Verify build still compiles**

Run: `go build -o /dev/null .`
Expected: Clean build (webview not yet imported, just in go.mod).

**Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add github.com/webview/webview for native dashboard window"
```

---

### Task 2: Extend AppConfig with PrinterDPI map

**Files:**
- Modify: `config.go:24-64` (AppConfig struct + defaultConfig)
- Modify: `config.go:189-207` (safeConfigForClient)
- Test: `config_test.go` (new)

**Step 1: Write the failing test**

Create `config_test.go`:

```go
package main

import (
	"encoding/json"
	"testing"
)

func TestPrinterDPIConfigRoundTrip(t *testing.T) {
	cfg := defaultConfig()
	if cfg.PrinterDPI == nil {
		t.Fatal("PrinterDPI should be initialized as empty map, got nil")
	}

	cfg.PrinterDPI["TSC TE200"] = PrinterDPIEntry{DPI: 203, Source: "tspl_probe"}
	cfg.PrinterDPI["TSC TTP-345"] = PrinterDPIEntry{DPI: 300, Source: "driver"}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var loaded AppConfig
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if loaded.PrinterDPI["TSC TE200"].DPI != 203 {
		t.Errorf("TE200 DPI = %d, want 203", loaded.PrinterDPI["TSC TE200"].DPI)
	}
	if loaded.PrinterDPI["TSC TTP-345"].Source != "driver" {
		t.Errorf("TTP-345 source = %q, want 'driver'", loaded.PrinterDPI["TSC TTP-345"].Source)
	}
}

func TestSafeConfigHidesDPISource(t *testing.T) {
	cfg := defaultConfig()
	cfg.PrinterDPI["Test"] = PrinterDPIEntry{DPI: 300, Source: "manual"}

	// safeConfigForClient should include printer_dpi as flat map (name -> dpi int)
	safe := safeConfigForClient()
	dpiMap, ok := safe["printer_dpi"].(map[string]int)
	if !ok {
		t.Fatalf("printer_dpi should be map[string]int, got %T", safe["printer_dpi"])
	}
	if dpiMap["Test"] != 300 {
		t.Errorf("safe printer_dpi[Test] = %d, want 300", dpiMap["Test"])
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/mario/PARA/1_Proyectos/ISI_Hospital/Servicios/tsc-bridge && go test -run TestPrinterDPI -v`
Expected: FAIL — `PrinterDPIEntry` undefined.

**Step 3: Implement PrinterDPI in config.go**

Add after `WhitelabelConfig` struct (line ~21):

```go
// PrinterDPIEntry stores detected DPI and how it was determined.
type PrinterDPIEntry struct {
	DPI    int    `json:"dpi"`
	Source string `json:"source"` // "driver", "tspl_probe", "manual"
}
```

Add field to `AppConfig` struct (after line 41, before closing brace):

```go
	PrinterDPI map[string]PrinterDPIEntry `json:"printer_dpi"`
```

Update `defaultConfig()` — add to return:

```go
		PrinterDPI: map[string]PrinterDPIEntry{},
```

Update `initConfig()` — add nil check after ManualPrinters nil check (after line ~101):

```go
	if appConfig.PrinterDPI == nil {
		appConfig.PrinterDPI = map[string]PrinterDPIEntry{}
	}
```

Update `safeConfigForClient()` — add to returned map:

```go
		"printer_dpi": func() map[string]int {
			cfg := getConfig()
			flat := make(map[string]int, len(cfg.PrinterDPI))
			for name, entry := range cfg.PrinterDPI {
				flat[name] = entry.DPI
			}
			return flat
		}(),
```

Note: the `safeConfigForClient` function already calls `getConfig()` at the top — use that `cfg` variable for the PrinterDPI iteration. The function needs to expose a flat `map[string]int` (printer name -> DPI) without the `source` field.

**Step 4: Run tests to verify they pass**

Run: `go test -run TestPrinterDPI -v`
Expected: PASS

**Step 5: Commit**

```bash
git add config.go config_test.go
git commit -m "feat(config): add PrinterDPI map for per-printer DPI storage"
```

---

### Task 3: Create dpi.go — DPI detection chain

**Files:**
- Create: `dpi.go`
- Create: `dpi_darwin.go`
- Create: `dpi_windows.go`
- Create: `dpi_other.go`
- Test: `dpi_test.go` (new)

**Step 1: Write the failing test**

Create `dpi_test.go`:

```go
package main

import "testing"

func TestParseDPIFromTSCResponse(t *testing.T) {
	tests := []struct {
		name     string
		response string
		wantDPI  int
		wantOK   bool
	}{
		{"TE200 standard", "TSC TE200\r\nV1.0\r\n203 DPI\r\n", 203, true},
		{"TTP-345 300dpi", "TSC TTP-345\r\n300 DPI\r\nV2.1", 300, true},
		{"lowercase dpi", "TSC TE200\r\n203 dpi\r\n", 203, true},
		{"no DPI in response", "TSC TE200\r\nV1.0\r\n", 0, false},
		{"empty response", "", 0, false},
		{"resolution format", "Resolution: 203x203 DPI", 203, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dpi, ok := parseDPIFromTSCResponse(tt.response)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if dpi != tt.wantDPI {
				t.Errorf("dpi = %d, want %d", dpi, tt.wantDPI)
			}
		})
	}
}

func TestDetectDPIChain(t *testing.T) {
	// DetectPrinterDPI should return saved value if already in config
	configMu.Lock()
	appConfig.PrinterDPI = map[string]PrinterDPIEntry{
		"Cached Printer": {DPI: 300, Source: "manual"},
	}
	configMu.Unlock()

	dpi := GetPrinterDPI("Cached Printer")
	if dpi != 300 {
		t.Errorf("cached DPI = %d, want 300", dpi)
	}

	// Unknown printer returns defaultDPI
	dpi = GetPrinterDPI("Unknown Printer")
	if dpi != defaultDPI {
		t.Errorf("unknown DPI = %d, want %d", dpi, defaultDPI)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -run "TestParseDPI|TestDetectDPI" -v`
Expected: FAIL — functions undefined.

**Step 3: Implement dpi.go**

Create `dpi.go`:

```go
package main

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
)

// parseDPIFromTSCResponse extracts DPI from a TSC ~!I probe response.
// Looks for patterns like "203 DPI", "300 dpi", "Resolution: 203x203 DPI".
func parseDPIFromTSCResponse(response string) (int, bool) {
	if response == "" {
		return 0, false
	}

	// Pattern: "NNN DPI" or "NNN dpi"
	re := regexp.MustCompile(`(\d{3})\s*[Dd][Pp][Ii]`)
	if m := re.FindStringSubmatch(response); len(m) > 1 {
		dpi, err := strconv.Atoi(m[1])
		if err == nil && dpi >= 96 && dpi <= 600 {
			return dpi, true
		}
	}

	// Pattern: "Resolution: NNNxNNN"
	re2 := regexp.MustCompile(`[Rr]esolution[:\s]+(\d{3})x(\d{3})`)
	if m := re2.FindStringSubmatch(response); len(m) > 1 {
		dpi, err := strconv.Atoi(m[1])
		if err == nil && dpi >= 96 && dpi <= 600 {
			return dpi, true
		}
	}

	return 0, false
}

// GetPrinterDPI returns the known DPI for a printer, or defaultDPI if unknown.
func GetPrinterDPI(printerName string) int {
	configMu.RLock()
	entry, ok := appConfig.PrinterDPI[printerName]
	configMu.RUnlock()
	if ok && entry.DPI > 0 {
		return entry.DPI
	}
	return defaultDPI
}

// SetPrinterDPI stores a detected DPI for a printer and persists to config.
func SetPrinterDPI(printerName string, dpi int, source string) {
	configMu.Lock()
	if appConfig.PrinterDPI == nil {
		appConfig.PrinterDPI = map[string]PrinterDPIEntry{}
	}
	appConfig.PrinterDPI[printerName] = PrinterDPIEntry{DPI: dpi, Source: source}
	configMu.Unlock()
	log.Printf("[dpi] %s: %d DPI (source=%s)", printerName, dpi, source)
	saveConfig()
}

// DetectPrinterDPI runs the detection chain for a printer:
// 1. Check config cache
// 2. Driver query (platform-specific)
// 3. TSPL ~!I probe (for network printers)
// 4. Return defaultDPI as fallback
func DetectPrinterDPI(printer PrinterInfo) int {
	// 1. Already cached?
	configMu.RLock()
	entry, ok := appConfig.PrinterDPI[printer.Name]
	configMu.RUnlock()
	if ok && entry.DPI > 0 {
		return entry.DPI
	}

	// 2. Driver query (platform-specific, fast ~10ms)
	if dpi, ok := queryDriverDPI(printer.Name); ok {
		SetPrinterDPI(printer.Name, dpi, "driver")
		return dpi
	}

	// 3. TSPL probe for network printers
	if printer.Address != "" {
		if response, isTSC := probeTSCPrinter(printer.Address); isTSC {
			if dpi, ok := parseDPIFromTSCResponse(response); ok {
				SetPrinterDPI(printer.Name, dpi, "tspl_probe")
				return dpi
			}
		}
	}

	// 4. Fallback — don't save, let user set manually
	log.Printf("[dpi] %s: no DPI detected, using default %d", printer.Name, defaultDPI)
	return defaultDPI
}

// DetectAllPrinterDPIs runs detection for all known printers.
func DetectAllPrinterDPIs() {
	printers, err := listAllPrinters()
	if err != nil {
		log.Printf("[dpi] detect all: %v", err)
		return
	}
	for _, p := range printers {
		DetectPrinterDPI(p)
	}
}

// handleDPIDetect is the HTTP handler for POST /dpi/detect
func handleDPIDetect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	printerName := r.URL.Query().Get("printer")
	if printerName == "" {
		// Detect all
		go DetectAllPrinterDPIs()
		jsonResponse(w, http.StatusOK, map[string]string{"status": "detecting"})
		return
	}

	// Find printer and detect
	printers, _ := listAllPrinters()
	for _, p := range printers {
		if p.Name == printerName {
			dpi := DetectPrinterDPI(p)
			jsonResponse(w, http.StatusOK, map[string]any{
				"printer": printerName,
				"dpi":     dpi,
			})
			return
		}
	}
	jsonResponse(w, http.StatusNotFound, map[string]string{"error": "printer not found"})
}

// handleDPISet is the HTTP handler for PUT /dpi
func handleDPISet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	printerName := r.URL.Query().Get("printer")
	dpiStr := r.URL.Query().Get("dpi")
	if printerName == "" || dpiStr == "" {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "printer and dpi required"})
		return
	}
	dpi, err := strconv.Atoi(dpiStr)
	if err != nil || dpi < 96 || dpi > 600 {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "dpi must be 96-600"})
		return
	}
	SetPrinterDPI(printerName, dpi, "manual")
	jsonResponse(w, http.StatusOK, map[string]any{
		"printer": printerName,
		"dpi":     dpi,
		"source":  "manual",
	})
}
```

Note: `handleDPIDetect` and `handleDPISet` need `"net/http"` in the import. Add it. Also `"strconv"` and `"fmt"` are already imported — verify after writing.

Create `dpi_darwin.go`:

```go
package main

import (
	"log"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// queryDriverDPI queries the macOS CUPS driver for printer resolution.
func queryDriverDPI(printerName string) (int, bool) {
	// lpoptions -p "PrinterName" -l | grep -i resolution
	cmd := exec.Command("lpoptions", "-p", printerName, "-l")
	out, err := cmd.Output()
	if err != nil {
		log.Printf("[dpi-darwin] lpoptions for %q: %v", printerName, err)
		return 0, false
	}

	// Look for "Resolution/...:" lines
	for _, line := range strings.Split(string(out), "\n") {
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "resolution") {
			continue
		}

		// Find the default value (marked with *)
		re := regexp.MustCompile(`\*(\d{3})(?:x\d{3})?dpi`)
		if m := re.FindStringSubmatch(strings.ToLower(line)); len(m) > 1 {
			dpi, _ := strconv.Atoi(m[1])
			if dpi >= 96 && dpi <= 600 {
				log.Printf("[dpi-darwin] %s: driver reports %d DPI", printerName, dpi)
				return dpi, true
			}
		}

		// Try without asterisk — any DPI value in the line
		re2 := regexp.MustCompile(`(\d{3})(?:x\d{3})?dpi`)
		if m := re2.FindStringSubmatch(strings.ToLower(line)); len(m) > 1 {
			dpi, _ := strconv.Atoi(m[1])
			if dpi >= 96 && dpi <= 600 {
				log.Printf("[dpi-darwin] %s: driver reports %d DPI (first available)", printerName, dpi)
				return dpi, true
			}
		}
	}

	return 0, false
}
```

Create `dpi_windows.go`:

```go
//go:build windows

package main

import (
	"log"
	"os/exec"
	"regexp"
	"strconv"
)

// queryDriverDPI queries Windows WMI for printer resolution.
func queryDriverDPI(printerName string) (int, bool) {
	// PowerShell: Get-WmiObject Win32_Printer -Filter "Name='..'" | Select HorizontalResolution
	query := `Get-WmiObject Win32_Printer -Filter "Name='` + printerName + `'" | Select-Object -ExpandProperty HorizontalResolution`
	cmd := exec.Command("powershell", "-NoProfile", "-Command", query)
	out, err := cmd.Output()
	if err != nil {
		log.Printf("[dpi-windows] WMI query for %q: %v", printerName, err)
		return 0, false
	}

	re := regexp.MustCompile(`(\d{3,4})`)
	if m := re.FindStringSubmatch(string(out)); len(m) > 1 {
		dpi, _ := strconv.Atoi(m[1])
		if dpi >= 96 && dpi <= 600 {
			log.Printf("[dpi-windows] %s: driver reports %d DPI", printerName, dpi)
			return dpi, true
		}
	}

	return 0, false
}
```

Create `dpi_other.go`:

```go
//go:build !darwin && !windows

package main

// queryDriverDPI is not supported on this platform.
func queryDriverDPI(printerName string) (int, bool) {
	return 0, false
}
```

**Step 4: Run tests to verify they pass**

Run: `go test -run "TestParseDPI|TestDetectDPI" -v`
Expected: PASS

**Step 5: Verify build compiles**

Run: `go build -o /dev/null .`
Expected: Clean build.

**Step 6: Commit**

```bash
git add dpi.go dpi_darwin.go dpi_windows.go dpi_other.go dpi_test.go
git commit -m "feat: add DPI auto-detection chain (driver -> TSPL probe -> manual)"
```

---

### Task 4: Expose DPI in /status endpoint and wire into TSPL rendering

**Files:**
- Modify: `main.go:129-148` (handleStatus)
- Modify: `main.go:1173+` (handleBatchTspl — read DPI from request or printer config)
- Modify: `main.go:617-654` (startServers — register new DPI routes)

**Step 1: Add DPI fields to handleStatus response**

In `main.go` `handleStatus()`, add to the response map:

```go
		"printer_dpi": func() map[string]int {
			configMu.RLock()
			defer configMu.RUnlock()
			flat := make(map[string]int, len(appConfig.PrinterDPI))
			for name, entry := range appConfig.PrinterDPI {
				flat[name] = entry.DPI
			}
			return flat
		}(),
		"default_dpi": defaultDPI,
```

**Step 2: Register DPI routes in startServers**

After the driver routes block (line ~636), add:

```go
	// DPI detection & manual override
	mux.HandleFunc("/dpi/detect", corsMiddleware(handleDPIDetect))
	mux.HandleFunc("/dpi", corsMiddleware(handleDPISet))
```

**Step 3: Trigger DPI detection on startup**

In `main()`, after `go startNetworkScanner()` (line ~595), add:

```go
	go DetectAllPrinterDPIs()
```

**Step 4: Verify build compiles**

Run: `go build -o /dev/null .`
Expected: Clean build.

**Step 5: Commit**

```bash
git add main.go
git commit -m "feat: expose printer DPI in /status, add /dpi endpoints, auto-detect on startup"
```

---

## Phase 2: Auth System

### Task 5: Create auth.go — credential validation

**Files:**
- Create: `auth.go`
- Test: `auth_test.go` (new)

**Step 1: Write the failing test**

Create `auth_test.go`:

```go
package main

import "testing"

func TestIsAuthConfigured(t *testing.T) {
	// No credentials
	configMu.Lock()
	appConfig.ApiURL = ""
	appConfig.ApiKey = ""
	appConfig.ApiSecret = ""
	appConfig.ApiToken = ""
	configMu.Unlock()

	if IsAuthConfigured() {
		t.Error("should be false with no credentials")
	}

	// API key mode
	configMu.Lock()
	appConfig.ApiURL = "https://example.com"
	appConfig.ApiKey = "key123"
	appConfig.ApiSecret = "secret456"
	configMu.Unlock()

	if !IsAuthConfigured() {
		t.Error("should be true with API key + secret")
	}
}

func TestAuthState(t *testing.T) {
	configMu.Lock()
	appConfig.ApiURL = "https://example.com"
	appConfig.ApiKey = "key"
	appConfig.ApiSecret = "secret"
	appConfig.Whitelabel = WhitelabelConfig{Name: "TestCo", ID: 42}
	configMu.Unlock()

	state := GetAuthState()
	if state.Configured != true {
		t.Error("configured should be true")
	}
	if state.WhitelabelName != "TestCo" {
		t.Errorf("wl name = %q, want TestCo", state.WhitelabelName)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -run TestIsAuth -v`
Expected: FAIL — `IsAuthConfigured` undefined.

**Step 3: Implement auth.go**

Create `auth.go`:

```go
package main

import (
	"encoding/json"
	"log"
	"net/http"
)

// AuthState represents the current authentication state for the dashboard.
type AuthState struct {
	Configured     bool   `json:"configured"`
	Connected      bool   `json:"connected"`
	ApiURL         string `json:"api_url"`
	WhitelabelName string `json:"whitelabel_name"`
	WhitelabelID   int    `json:"whitelabel_id"`
	LogoURL        string `json:"logo_url,omitempty"`
	Error          string `json:"error,omitempty"`
}

// IsAuthConfigured returns true if API credentials are present.
func IsAuthConfigured() bool {
	cfg := getConfig()
	if cfg.ApiURL == "" {
		return false
	}
	return (cfg.ApiKey != "" && cfg.ApiSecret != "") || cfg.ApiToken != ""
}

// GetAuthState returns the current auth state for the dashboard.
func GetAuthState() AuthState {
	cfg := getConfig()
	return AuthState{
		Configured:     IsAuthConfigured(),
		ApiURL:         cfg.ApiURL,
		WhitelabelName: cfg.Whitelabel.Name,
		WhitelabelID:   cfg.Whitelabel.ID,
		LogoURL:        cfg.Whitelabel.LogoURL,
	}
}

// handleAuthState returns the current auth state.
// GET /auth/state
func handleAuthState(w http.ResponseWriter, r *http.Request) {
	state := GetAuthState()

	// If configured, test the connection
	if state.Configured {
		client := NewApiClient(getConfig())
		if err := client.TestConnection(); err != nil {
			state.Connected = false
			state.Error = err.Error()
		} else {
			state.Connected = true
		}
	}

	jsonResponse(w, http.StatusOK, state)
}

// handleAuthLogin validates credentials and saves them.
// POST /auth/login { api_url, api_key, api_secret, wl_id? }
func handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req struct {
		ApiURL    string `json:"api_url"`
		ApiKey    string `json:"api_key"`
		ApiSecret string `json:"api_secret"`
		WlID      int    `json:"wl_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	if req.ApiURL == "" || req.ApiKey == "" || req.ApiSecret == "" {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "api_url, api_key, api_secret required"})
		return
	}

	// Build temporary config to test connection
	testCfg := AppConfig{
		ApiURL:        req.ApiURL,
		ApiKey:        req.ApiKey,
		ApiSecret:     req.ApiSecret,
		ApiWhiteLabel: req.WlID,
	}
	client := NewApiClient(testCfg)
	if err := client.TestConnection(); err != nil {
		log.Printf("[auth] login failed for %s: %v", req.ApiURL, err)
		jsonResponse(w, http.StatusUnauthorized, map[string]string{"error": "connection failed: " + err.Error()})
		return
	}

	// Save credentials
	configMu.Lock()
	appConfig.ApiURL = req.ApiURL
	appConfig.ApiKey = req.ApiKey
	appConfig.ApiSecret = req.ApiSecret
	if req.WlID > 0 {
		appConfig.ApiWhiteLabel = req.WlID
	}
	configMu.Unlock()

	if err := saveConfig(); err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
		return
	}

	// Fetch whitelabel branding
	go fetchAndSaveWhitelabel(client)

	log.Printf("[auth] login successful for %s (wl=%d)", req.ApiURL, req.WlID)
	jsonResponse(w, http.StatusOK, map[string]string{"status": "connected"})
}

// handleAuthLogout clears credentials.
// POST /auth/logout
func handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	configMu.Lock()
	appConfig.ApiURL = ""
	appConfig.ApiKey = ""
	appConfig.ApiSecret = ""
	appConfig.ApiToken = ""
	appConfig.Whitelabel = WhitelabelConfig{}
	configMu.Unlock()

	saveConfig()
	log.Printf("[auth] logged out")
	jsonResponse(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

// fetchAndSaveWhitelabel fetches branding info from the backend and saves it.
func fetchAndSaveWhitelabel(client *ApiClient) {
	// Try to get whitelabel info from /whitelabel endpoint
	// This is best-effort — branding is not required for operation
	templates, err := client.FetchTemplates()
	if err != nil {
		log.Printf("[auth] could not fetch templates for whitelabel: %v", err)
		return
	}
	log.Printf("[auth] fetched %d templates from backend", len(templates))
}
```

**Step 4: Run tests to verify they pass**

Run: `go test -run "TestIsAuth|TestAuthState" -v`
Expected: PASS

**Step 5: Commit**

```bash
git add auth.go auth_test.go
git commit -m "feat: add auth.go with login/logout/state endpoints and credential management"
```

---

### Task 6: Register auth routes in main.go

**Files:**
- Modify: `main.go:617-654` (startServers)

**Step 1: Add auth routes**

After the DPI routes, add:

```go
	// Auth routes
	mux.HandleFunc("/auth/state", corsMiddleware(handleAuthState))
	mux.HandleFunc("/auth/login", corsMiddleware(handleAuthLogin))
	mux.HandleFunc("/auth/logout", corsMiddleware(handleAuthLogout))
```

**Step 2: Verify build compiles**

Run: `go build -o /dev/null .`
Expected: Clean build.

**Step 3: Commit**

```bash
git add main.go
git commit -m "feat: register /auth/* HTTP routes"
```

---

## Phase 3: Webview Window & Tray Upgrade

### Task 7: Create webview.go — native window lifecycle

**Files:**
- Create: `webview.go`

**Step 1: Implement webview.go**

```go
package main

import (
	"fmt"
	"log"
	"sync"

	webview "github.com/webview/webview/v2"
)

var (
	wv     webview.WebView
	wvOnce sync.Once
	wvMu   sync.Mutex
)

// initWebview creates the webview window (must be called from main thread).
func initWebview() webview.WebView {
	wvMu.Lock()
	defer wvMu.Unlock()

	if wv != nil {
		return wv
	}

	w := webview.New(false) // debug=false for production
	if w == nil {
		log.Printf("[webview] failed to create webview — falling back to browser mode")
		return nil
	}

	cfg := getConfig()
	title := "TSC Bridge"
	if cfg.Whitelabel.Name != "" {
		title = fmt.Sprintf("TSC Bridge — %s", cfg.Whitelabel.Name)
	}

	w.SetTitle(title)
	w.SetSize(1100, 750, webview.HintNone)
	wv = w
	return w
}

// showDashboard opens the native webview pointing at the local dashboard.
func showDashboard(dashURL string) {
	wvMu.Lock()
	w := wv
	wvMu.Unlock()

	if w == nil {
		// Fallback: open in browser
		log.Printf("[webview] no webview available — opening in browser")
		openBrowser(dashURL)
		return
	}

	w.Dispatch(func() {
		w.Navigate(dashURL)
		// Window is already running, just navigate
	})
}

// runWebviewLoop starts the webview event loop (blocks, must be on main thread).
// Returns when the user closes the window.
func runWebviewLoop(dashURL string) {
	w := initWebview()
	if w == nil {
		return
	}
	defer w.Destroy()

	w.Navigate(dashURL)
	w.Run() // blocks until window closed
}
```

**Step 2: Verify build compiles**

Run: `go build -o /dev/null .`
Expected: Build succeeds (may need CGO on macOS — WebKit is built-in so no extra deps).

Note: If build fails with CGO errors, ensure `CGO_ENABLED=1` is set. On macOS this should work out of the box since WebKit is available as a system framework.

**Step 3: Commit**

```bash
git add webview.go
git commit -m "feat: add webview.go for native dashboard window"
```

---

### Task 8: Upgrade tray.go — rich menu with printer list and icon states

**Files:**
- Modify: `tray.go` (complete rewrite)

**Step 1: Rewrite tray.go**

```go
package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"fyne.io/systray"
)

// trayIconState represents the current icon color.
type trayIconState int

const (
	trayGray   trayIconState = iota // starting up
	trayGreen                       // connected, printer available
	trayYellow                      // connected, no printer
	trayRed                         // auth error or disconnected
)

var currentTrayState = trayGray

// runTray starts the system tray icon and blocks.
func runTray(dashURL string, autoOpen bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[tray] PANIC in systray: %v — falling back to headless mode", r)
			select {}
		}
	}()

	systray.Run(
		func() { onTrayReady(dashURL, autoOpen) },
		onTrayExit,
	)
}

func onTrayReady(dashURL string, autoOpen bool) {
	cfg := getConfig()
	tooltip := "TSC Bridge v" + version
	if cfg.Whitelabel.Name != "" {
		tooltip = fmt.Sprintf("TSC Bridge — %s", cfg.Whitelabel.Name)
	}
	systray.SetTooltip(tooltip)
	updateTrayIcon(trayGray) // starting

	// Dashboard
	mOpen := systray.AddMenuItem("Abrir Dashboard", "Abrir panel de control en ventana nativa")
	systray.AddSeparator()

	// Printer submenu (populated dynamically)
	mPrinters := systray.AddMenuItem("Impresoras", "")
	mPrinters.Disable()
	mRefresh := systray.AddMenuItem("Re-detectar impresoras", "Escanear red y USB")
	mTestPrint := systray.AddMenuItem("Test Print", "Imprimir pagina de prueba")
	systray.AddSeparator()

	// Auto-start
	mAutoStart := systray.AddMenuItemCheckbox(
		"Iniciar con el sistema",
		"Iniciar TSC Bridge al encender el equipo",
		isAutoStartEnabled(),
	)
	systray.AddSeparator()

	// Info
	mInfo := systray.AddMenuItem(
		fmt.Sprintf("Puerto %d — v%s", cfg.Port, version),
		"Informacion del servicio",
	)
	mInfo.Disable()
	systray.AddSeparator()

	mQuit := systray.AddMenuItem("Salir", "Detener servicio y salir")

	// Auto-open dashboard on first run
	if autoOpen {
		go func() {
			time.Sleep(2 * time.Second)
			showDashboard(dashURL)
		}()
	}

	// Background: update printer count and tray icon periodically
	go func() {
		for {
			printers, _ := listAllPrinters()
			if len(printers) > 0 {
				defaultDpi := GetPrinterDPI(cfg.DefaultPrinter)
				label := fmt.Sprintf("%d impresora(s) — %d DPI", len(printers), defaultDpi)
				mPrinters.SetTitle(label)
				updateTrayIcon(trayGreen)
			} else {
				mPrinters.SetTitle("Sin impresoras")
				updateTrayIcon(trayYellow)
			}
			time.Sleep(10 * time.Second)
		}
	}()

	// Event loop
	go func() {
		for {
			select {
			case <-mOpen.ClickedCh:
				go showDashboard(dashURL)
			case <-mRefresh.ClickedCh:
				go func() {
					refreshNetworkPrinters()
					DetectAllPrinterDPIs()
				}()
			case <-mTestPrint.ClickedCh:
				go func() {
					// Quick test print using default printer
					cfg := getConfig()
					if cfg.DefaultPrinter != "" {
						sendTestPrint(cfg.DefaultPrinter, cfg.DefaultPreset)
					}
				}()
			case <-mAutoStart.ClickedCh:
				enabled := !isAutoStartEnabled()
				if err := setAutoStart(enabled); err != nil {
					log.Printf("[tray] autostart toggle error: %v", err)
				} else {
					if enabled {
						mAutoStart.Check()
					} else {
						mAutoStart.Uncheck()
					}
					configMu.Lock()
					appConfig.AutoStart = enabled
					configMu.Unlock()
					saveConfig()
				}
			case <-mQuit.ClickedCh:
				systray.Quit()
			}
		}
	}()
}

func onTrayExit() {
	log.Printf("System tray exit — shutting down")
	os.Exit(0)
}

// updateTrayIcon sets the tray icon based on state.
func updateTrayIcon(state trayIconState) {
	currentTrayState = state
	// For now, use the same icon for all states.
	// TODO: Generate colored variants of the app icon.
	systray.SetIcon(generateAppIcon(32))
}

// sendTestPrint is a helper that prints a test page on the given printer.
func sendTestPrint(printerName, presetName string) {
	preset := findPresetByID(presetName)
	if preset == nil {
		log.Printf("[tray] test print: preset %q not found", presetName)
		return
	}
	tspl := generateTestTSPL(preset)
	if err := printToNamedPrinter(printerName, []byte(tspl)); err != nil {
		log.Printf("[tray] test print error: %v", err)
	} else {
		log.Printf("[tray] test print sent to %s", printerName)
	}
}
```

Note: `sendTestPrint` references `findPresetByID`, `generateTestTSPL`, and `printToNamedPrinter` — these should already exist in the codebase (they're used by the test-print HTTP handler). Search for their exact names before implementing. If they have different names, adjust the calls.

**Step 2: Verify references exist**

Run: `grep -rn "func findPreset\|func generateTest\|func printToNamed\|func rawPrint\|func handleTestPrint" *.go`

Adjust `sendTestPrint` to use whatever existing functions handle test printing. The key pattern is:
1. Build TSPL test page commands
2. Send to printer by name

If `handleTestPrint` is a monolithic HTTP handler, extract the core logic or call the same underlying functions.

**Step 3: Verify build compiles**

Run: `go build -o /dev/null .`
Expected: Clean build.

**Step 4: Commit**

```bash
git add tray.go
git commit -m "feat: upgrade tray with printer count, DPI info, refresh, test print, native window"
```

---

### Task 9: Update main.go — webview lifecycle and setup wizard flow

**Files:**
- Modify: `main.go:21` (version bump)
- Modify: `main.go:536-614` (main function)

**Step 1: Update version**

Change line 21:

```go
const version = "3.0.0"
```

**Step 2: Update main() for webview**

Replace the tray/headless block at the end of `main()` (lines ~606-614):

```go
	// Check if first run (no auth configured) — will show setup wizard
	if !IsAuthConfigured() {
		log.Printf("First run detected — setup wizard will appear in dashboard")
	}

	// Run system tray — blocks until user clicks "Salir"
	log.Printf("Starting system tray (headless=%v)", headless)
	if headless {
		log.Printf("Headless mode — skipping systray, blocking forever")
		select {}
	}

	// Auto-open: show native webview instead of browser
	// tray.go's onTrayReady calls showDashboard() which uses webview
	runTray(dashURL, !headless)
```

**Step 3: Verify build compiles**

Run: `go build -o /dev/null .`
Expected: Clean build.

**Step 4: Commit**

```bash
git add main.go
git commit -m "feat: v3.0.0 — webview lifecycle in main, setup wizard detection"
```

---

## Phase 4: Dashboard Rewrite

### Task 10: Dashboard HTML — shell with header, global state, 5-tab skeleton

This is the largest single task. The dashboard.html will be a complete rewrite.

**Files:**
- Modify: `dashboard.html` (complete rewrite)

**Strategy:** Build incrementally — start with the shell (header, tabs, global state), then fill each tab in subsequent tasks.

**Step 1: Write the dashboard shell**

The new `dashboard.html` should contain:

1. **HTML head**: Bootstrap 5 CDN, custom CSS with CSS variables for theming
2. **Header bar**: Logo + whitelabel name (left), printer selector + DPI badge (center), preset selector + auth badge (right)
3. **Tab navigation**: 5 tabs — Impresoras, Disenar, Batch, Templates, Config
4. **Tab content containers**: Empty divs for each tab
5. **JavaScript global state**: `AppState` object with selectedPrinter, selectedPreset, dpi, authState
6. **Init function**: Fetches /status, /config, /auth/state, populates header
7. **Polling**: Updates status every 5 seconds

The full HTML will be ~2000-3000 lines. Here is the structure to implement:

```html
<!DOCTYPE html>
<html lang="es">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>TSC Bridge</title>
    <link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/css/bootstrap.min.css" rel="stylesheet">
    <link href="https://cdn.jsdelivr.net/npm/bootstrap-icons@1.11.3/font/bootstrap-icons.min.css" rel="stylesheet">
    <style>
        :root {
            --primary: #2563eb;
            --accent: #7c3aed;
            /* Overridden by whitelabel config */
        }
        /* ... compact dashboard styles ... */
    </style>
</head>
<body>
    <!-- HEADER -->
    <nav class="navbar navbar-dark" style="background: var(--primary)">
        <div class="container-fluid">
            <span class="navbar-brand" id="brand-name">TSC Bridge</span>
            <div class="d-flex align-items-center gap-3">
                <select id="global-printer" class="form-select form-select-sm"></select>
                <span id="dpi-badge" class="badge bg-info">203 DPI</span>
                <select id="global-preset" class="form-select form-select-sm"></select>
                <span id="auth-badge" class="badge"></span>
            </div>
        </div>
    </nav>

    <!-- TAB NAV -->
    <ul class="nav nav-tabs px-3 pt-2" id="mainTabs">
        <li class="nav-item"><a class="nav-link active" data-tab="impresoras">Impresoras</a></li>
        <li class="nav-item"><a class="nav-link" data-tab="disenar">Disenar</a></li>
        <li class="nav-item"><a class="nav-link" data-tab="batch">Batch</a></li>
        <li class="nav-item"><a class="nav-link" data-tab="templates">Templates</a></li>
        <li class="nav-item"><a class="nav-link" data-tab="config">Config</a></li>
    </ul>

    <!-- TAB CONTENT -->
    <div class="tab-content p-3">
        <div id="tab-impresoras" class="tab-pane active">...</div>
        <div id="tab-disenar" class="tab-pane">...</div>
        <div id="tab-batch" class="tab-pane">...</div>
        <div id="tab-templates" class="tab-pane">...</div>
        <div id="tab-config" class="tab-pane">...</div>
    </div>

    <!-- SETUP WIZARD MODAL -->
    <div class="modal" id="setupWizard">...</div>

    <script src="https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/js/bootstrap.bundle.min.js"></script>
    <script>
    const AppState = {
        printers: [],
        selectedPrinter: '',
        selectedPreset: '',
        dpi: 203,
        printerDPI: {},
        authState: {},
        config: {},
        templates: [],
    };

    // ... init, polling, tab switching, global state management ...
    </script>
</body>
</html>
```

**Key design rules for the rewrite:**
- Printer selector appears ONCE in the header — all tabs read from `AppState.selectedPrinter`
- Preset selector appears ONCE in the header — same pattern
- DPI badge auto-updates when printer changes (reads from `AppState.printerDPI[selectedPrinter]`)
- Auth badge shows whitelabel name + connected/disconnected state
- Setup wizard modal auto-shows on first load if `!authState.configured`
- All API calls go through a single `api(method, path, body)` helper

**Step 2: Verify it loads in webview**

Run the bridge and verify the dashboard loads:

```bash
cd /Users/mario/PARA/1_Proyectos/ISI_Hospital/Servicios/tsc-bridge
go run .
```

Open http://127.0.0.1:9638/ in a browser — verify header, 5 tabs, and global state loads.

**Step 3: Commit**

```bash
git add dashboard.html
git commit -m "feat: dashboard v3 shell — header, 5-tab layout, global state, setup wizard"
```

---

### Task 11: Tab 1 — Impresoras

**Files:**
- Modify: `dashboard.html` (tab-impresoras content)

**Content:**
- Unified printer table (USB + network) with columns: Name, Type badge, DPI, Status, Actions
- Per-printer actions: [Set Default] [Test Print] [Share] [Set DPI]
- "Add Manual IP" form
- Network scan button
- USB sharing toggle

**Key JavaScript functions:**
```js
async function loadPrinters() {
    const res = await api('GET', '/printers');
    AppState.printers = res.printers || [];
    renderPrinterTable();
    updateGlobalPrinterSelector();
}

function renderPrinterTable() { /* ... */ }

async function setDefaultPrinter(name) {
    AppState.selectedPrinter = name;
    document.getElementById('global-printer').value = name;
    updateDPIBadge();
    await api('PUT', '/config', { default_printer: name });
}

async function testPrint(printerName) {
    await api('POST', '/test-print?printer=' + encodeURIComponent(printerName));
}

async function setManualDPI(printerName, dpi) {
    await api('PUT', '/dpi?printer=' + encodeURIComponent(printerName) + '&dpi=' + dpi);
    AppState.printerDPI[printerName] = dpi;
    updateDPIBadge();
}
```

**Step 1: Implement tab content**
**Step 2: Test manually** — verify printer list loads, actions work
**Step 3: Commit**

```bash
git add dashboard.html
git commit -m "feat: dashboard Tab 1 — Impresoras with unified list, DPI display, actions"
```

---

### Task 12: Tab 5 — Config

**Files:**
- Modify: `dashboard.html` (tab-config content)

**Sections:**
1. **Account**: Store URL, API key status, Connected user (whitelabel name), [Logout] [Reconnect]
2. **Printing**: Default printer (read-only, set from header), Default preset, DPI per printer table (editable)
3. **System**: HTTP port, Autostart toggle, Network scan toggle + interval, Share toggle + port
4. **Downloads**: macOS / Windows installer links

**Key JavaScript:**
```js
async function loadConfig() {
    AppState.config = await api('GET', '/config');
    renderConfigForm();
}

async function saveConfigSection(data) {
    await api('PUT', '/config', data);
    showToast('Configuracion guardada');
}
```

**Step 1: Implement tab content**
**Step 2: Test manually** — verify config loads and saves
**Step 3: Commit**

```bash
git add dashboard.html
git commit -m "feat: dashboard Tab 5 — Config with account, printing, system sections"
```

---

### Task 13: Tab 4 — Templates

**Files:**
- Modify: `dashboard.html` (tab-templates content)

**Content:**
- Two sections: "Backend Templates" (from API) and "Local Templates"
- Grid/card layout with template preview thumbnail
- Per-template actions: [Preview] [Edit in Designer] [Print] [Use in Batch]
- Backend templates require auth — show "Conectar" button if not authenticated

**Key JavaScript:**
```js
async function loadTemplates() {
    // Local templates
    const local = await api('GET', '/templates');
    AppState.localTemplates = local.templates || [];

    // Backend templates (if authenticated)
    if (AppState.authState.configured) {
        try {
            const backend = await api('GET', '/api/templates');
            AppState.backendTemplates = backend.templates || [];
        } catch (e) {
            console.warn('Backend templates unavailable:', e);
        }
    }
    renderTemplateGrid();
}

async function previewTemplate(templateId, source) {
    const res = await fetch('/batch-preview-image', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({
            template_id: templateId,
            source: source,
            dpi: AppState.dpi,
            data: {}
        })
    });
    const blob = await res.blob();
    // Show in modal
}
```

**Step 1: Implement tab content**
**Step 2: Test manually**
**Step 3: Commit**

```bash
git add dashboard.html
git commit -m "feat: dashboard Tab 4 — Templates grid with backend + local, preview"
```

---

## Phase 5: Label Designer

### Task 14: Tab 2 — Designer canvas and toolbar

**Files:**
- Modify: `dashboard.html` (tab-disenar content)

**Layout:**
```
+--toolbar--+--------canvas---------+--properties--+
| [T] Text  |                       | Position     |
| [B] Barcode|      Label surface   | x: __ y: __  |
| [Q] QR    |      (mm scale)      | Size         |
| [L] Line  |                       | w: __ h: __  |
| [R] Rect  |                       | Content      |
| [I] Image |                       | [________]   |
+===========+                       | Font         |
| Standards |                       | [1] [x2]     |
| [None v]  |                       | Rotation     |
+-----------+-----------------------| [0 v]        |
| TSPL Code |                       +--------------+
| (readonly)|
+-----------+
```

**Canvas implementation:**
- HTML5 `<canvas>` element scaled to label dimensions in mm
- `mmToPx(mm)` converts using screen DPI (96) and zoom level
- Elements stored in `AppState.designerElements[]` array
- Each element: `{id, type, x, y, w, h, content, font, rotation, alignment}`
- Drag-and-drop via canvas mouse events (mousedown → track, mousemove → update, mouseup → commit)
- Selected element highlighted with resize handles
- Snap-to-grid optional (1mm grid)

**Toolbar:**
- Add element buttons — clicking adds element at center of canvas
- Standards selector dropdown (None, Gafete, GS1-128, ISO 15223)
- TSPL code preview textarea (read-only, auto-updates)

**Key JavaScript:**

```js
const Designer = {
    canvas: null,
    ctx: null,
    elements: [],
    selected: null,
    zoom: 1,
    gridSnap: true,
    labelW: 30, // mm
    labelH: 22, // mm
    standard: 'none',

    init() { /* setup canvas, event listeners */ },
    addElement(type) { /* push new element, select it */ },
    render() { /* clear canvas, draw all elements, handles */ },
    hitTest(px, py) { /* find element at canvas coords */ },
    generateTSPL() { /* convert elements to TSPL commands at AppState.dpi */ },
    toPdfmeSchema() { /* export as pdfme-compatible JSON */ },
    fromPdfmeSchema(schema) { /* import from pdfme JSON */ },
};
```

**Step 1: Implement canvas, toolbar, element rendering**
**Step 2: Test manually** — add elements, drag them, verify TSPL output updates
**Step 3: Commit**

```bash
git add dashboard.html
git commit -m "feat: dashboard Tab 2 — Label Designer with canvas, toolbar, drag-and-drop"
```

---

### Task 15: Properties panel and field editing

**Files:**
- Modify: `dashboard.html` (designer properties panel)

**Properties panel updates when an element is selected:**
- Position: x/y inputs (mm, 0.1 step)
- Size: w/h inputs (mm, 0.1 step)
- Content: text input or textarea (supports `{variable}` placeholders)
- Font: TSPL font selector (1-5) + multiplier (1-8)
- Rotation: dropdown (0, 90, 180, 270)
- Alignment: button group (left, center, right)
- For barcodes: type selector (Code128, EAN13, etc.)
- For QR codes: → opens QR Builder (Task 16)
- For images: file upload button

**Step 1: Implement properties panel**
**Step 2: Test manually** — select element, edit properties, verify canvas updates
**Step 3: Commit**

```bash
git add dashboard.html
git commit -m "feat: designer properties panel — position, size, font, content, rotation"
```

---

### Task 16: QR Builder

**Files:**
- Modify: `dashboard.html` (QR builder in properties panel)

**When a QR element is selected, show QR content builder instead of plain text input:**

```html
<div id="qr-builder">
    <select id="qr-type">
        <option value="free">Texto libre</option>
        <option value="vcard">vCard 3.0</option>
        <option value="url">URL</option>
        <option value="wifi">WiFi</option>
    </select>

    <!-- vCard fields -->
    <div id="qr-vcard" style="display:none">
        <input placeholder="Nombre ({nombre})" data-field="fn">
        <input placeholder="Apellido ({apellido})" data-field="ln">
        <input placeholder="Telefono ({telefono})" data-field="tel">
        <input placeholder="Email ({correo})" data-field="email">
        <input placeholder="Empresa ({empresa})" data-field="org">
        <input placeholder="Puesto ({puesto})" data-field="title">
    </div>

    <!-- URL pattern -->
    <div id="qr-url" style="display:none">
        <input placeholder="https://stl.lat/{codigo}" id="qr-url-pattern">
    </div>

    <!-- WiFi -->
    <div id="qr-wifi" style="display:none">
        <input placeholder="SSID" id="qr-wifi-ssid">
        <input placeholder="Password" id="qr-wifi-pass">
        <select id="qr-wifi-security">
            <option value="WPA">WPA/WPA2</option>
            <option value="WEP">WEP</option>
            <option value="">Abierta</option>
        </select>
    </div>
</div>
```

**JavaScript generators:**

```js
const QRBuilder = {
    generateVCard(fields) {
        return [
            'BEGIN:VCARD',
            'VERSION:3.0',
            `FN:${fields.fn} ${fields.ln}`,
            `N:${fields.ln};${fields.fn};`,
            `TEL;TYPE=CELL:${fields.tel}`,
            `EMAIL:${fields.email}`,
            `ORG:${fields.org}`,
            `TITLE:${fields.title}`,
            'END:VCARD'
        ].join('\n');
    },

    generateWiFi(ssid, pass, security) {
        return `WIFI:S:${ssid};T:${security};P:${pass};;`;
    },

    generateURL(pattern) {
        return pattern; // Variables resolved at print time
    }
};
```

**Step 1: Implement QR builder UI and generators**
**Step 2: Test manually** — select QR element, switch types, verify content preview
**Step 3: Commit**

```bash
git add dashboard.html
git commit -m "feat: QR Builder — vCard 3.0, URL, WiFi, free text generators"
```

---

### Task 17: Designer TSPL preview, rasterize, and export

**Files:**
- Modify: `dashboard.html` (designer bottom panel + export buttons)

**TSPL preview:**
- Read-only textarea showing live TSPL output
- Auto-updates as elements are moved/resized
- DPI-aware output (uses `AppState.dpi`)

**Rasterize button:**
- POST to `/batch-preview-image` with designer schema as pdfme JSON
- Shows PNG preview in modal

**Export buttons:**
- "Guardar local" → POST to `/templates` (local storage)
- "Subir al servidor" → POST to backend API via bridge (requires auth)
- "Exportar JSON" → download pdfme-compatible JSON file

```js
Designer.generateTSPL = function() {
    const dpi = AppState.dpi;
    const lines = [];
    lines.push('SIZE ' + this.labelW + ' mm, ' + this.labelH + ' mm');
    lines.push('GAP 2 mm, 0 mm');
    lines.push('DIRECTION 0,0');
    lines.push('SPEED 4');
    lines.push('DENSITY 8');
    lines.push('CLS');

    for (const el of this.elements) {
        const x = Math.round(el.x / 25.4 * dpi);
        const y = Math.round(el.y / 25.4 * dpi);
        const w = Math.round(el.w / 25.4 * dpi);
        const h = Math.round(el.h / 25.4 * dpi);

        switch (el.type) {
            case 'text':
                lines.push(`TEXT ${x},${y},"${el.font || '3'}",0,${el.fontMult || 1},${el.fontMult || 1},"${el.content}"`);
                break;
            case 'qrcode':
                const cell = Math.max(2, Math.round(w / 25));
                lines.push(`QRCODE ${x},${y},M,${cell},A,0,"${el.content}"`);
                break;
            case 'barcode':
                lines.push(`BARCODE ${x},${y},"128",${h},0,2,2,"${el.content}"`);
                break;
            case 'line':
                lines.push(`BAR ${x},${y},${w},${Math.max(2, h)}`);
                break;
            case 'rectangle':
                lines.push(`BOX ${x},${y},${x+w},${y+h},2`);
                break;
        }
    }
    lines.push('PRINT 1');
    return lines.join('\r\n');
};

Designer.toPdfmeSchema = function() {
    return {
        schemas: [this.elements.map(el => ({
            name: el.id,
            type: el.type,
            position: { x: el.x, y: el.y },
            width: el.w,
            height: el.h,
            content: el.content,
            fontSize: el.fontSize || 12,
            alignment: el.alignment || 'left',
            rotate: el.rotation || 0,
        }))],
        basePdf: {
            width: this.labelW,
            height: this.labelH,
            padding: [0, 0, 0, 0]
        }
    };
};
```

**Step 1: Implement TSPL preview, rasterize button, export buttons**
**Step 2: Test manually** — design a label, verify TSPL, rasterize preview, export JSON
**Step 3: Commit**

```bash
git add dashboard.html
git commit -m "feat: designer TSPL preview, PNG rasterize, local/backend/JSON export"
```

---

## Phase 6: Batch Enhancements

### Task 18: Tab 3 — Batch wizard (4-step)

**Files:**
- Modify: `dashboard.html` (tab-batch content)

**4-step wizard:**

```
Step 1: Upload Excel     → POST /excel/upload → get columns + preview
Step 2: Select Template  → pick from local/backend templates
Step 3: Map Columns      → for each template field, assign Excel column or formula
Step 4: Execute          → print TSPL or generate PDF
```

**Step 1 — Upload Excel:**
```html
<div id="batch-step-1">
    <h5>Paso 1: Subir archivo Excel</h5>
    <input type="file" accept=".xlsx,.xls,.csv" id="batch-file">
    <div id="batch-preview-table"></div>
    <button class="btn btn-primary" onclick="batchNext(2)">Siguiente</button>
</div>
```

**Step 2 — Select Template:**
```html
<div id="batch-step-2" style="display:none">
    <h5>Paso 2: Seleccionar plantilla</h5>
    <div id="batch-template-grid" class="row"></div>
    <button class="btn btn-secondary" onclick="batchBack(1)">Atras</button>
    <button class="btn btn-primary" onclick="batchNext(3)">Siguiente</button>
</div>
```

**Step 3 — Map Columns (with formula support, Task 19):**
```html
<div id="batch-step-3" style="display:none">
    <h5>Paso 3: Mapear columnas</h5>
    <div id="batch-mapping-fields"></div>
    <div id="batch-mapping-preview"></div>
    <button class="btn btn-secondary" onclick="batchBack(2)">Atras</button>
    <button class="btn btn-primary" onclick="batchNext(4)">Ejecutar</button>
</div>
```

**Step 4 — Execute:**
```html
<div id="batch-step-4" style="display:none">
    <h5>Paso 4: Ejecutar</h5>
    <div class="btn-group">
        <button class="btn btn-success" onclick="batchExecute('print')">Imprimir TSPL</button>
        <button class="btn btn-primary" onclick="batchExecute('pdf')">Generar PDF</button>
    </div>
    <div class="progress mt-3"><div id="batch-progress" class="progress-bar"></div></div>
    <div id="batch-result"></div>
</div>
```

**Uses global printer/preset from header — no duplicate selectors.**

**Step 1: Implement 4-step wizard UI and navigation**
**Step 2: Test manually** — upload Excel, select template, see mapping, execute
**Step 3: Commit**

```bash
git add dashboard.html
git commit -m "feat: dashboard Tab 3 — Batch 4-step wizard, uses global printer/preset"
```

---

### Task 19: Formula concatenation engine (JavaScript)

**Files:**
- Modify: `dashboard.html` (batch step 3 mapping + formula engine)

**Mapping modes per field:**

```html
<div class="mapping-field" data-field="{nombre_completo}">
    <label>{nombre_completo}</label>
    <select class="mapping-mode">
        <option value="column">Columna directa</option>
        <option value="formula">Formula</option>
        <option value="vcard">vCard Builder</option>
        <option value="url">URL Pattern</option>
        <option value="static">Texto fijo</option>
    </select>
    <div class="mapping-value">
        <!-- Changes based on mode -->
    </div>
</div>
```

**Formula engine:**

```js
const FormulaEngine = {
    // Parse formula: {Col_A} + " " + {Col_B}
    // Returns function that takes a row and returns resolved string
    compile(formula) {
        // Tokenize: split on + but respect quoted strings
        const tokens = [];
        let current = '';
        let inQuote = false;

        for (let i = 0; i < formula.length; i++) {
            const ch = formula[i];
            if (ch === '"') {
                inQuote = !inQuote;
                current += ch;
            } else if (ch === '+' && !inQuote) {
                tokens.push(current.trim());
                current = '';
            } else {
                current += ch;
            }
        }
        if (current.trim()) tokens.push(current.trim());

        return (row) => {
            return tokens.map(t => {
                t = t.trim();
                // Quoted string literal
                if (t.startsWith('"') && t.endsWith('"')) {
                    return t.slice(1, -1);
                }
                // Column reference: {Col_A} or Col_A
                const colMatch = t.match(/^\{?(.+?)\}?$/);
                if (colMatch) {
                    return row[colMatch[1]] || '';
                }
                return t;
            }).join('');
        };
    },

    // vCard builder: takes field mappings, returns function
    compileVCard(mappings) {
        return (row) => {
            const resolve = (expr) => expr ? this.compile(expr)(row) : '';
            return [
                'BEGIN:VCARD',
                'VERSION:3.0',
                'FN:' + resolve(mappings.fn),
                'N:' + resolve(mappings.ln) + ';' + resolve(mappings.fn) + ';',
                'TEL;TYPE=CELL:' + resolve(mappings.tel),
                'EMAIL:' + resolve(mappings.email),
                'ORG:' + resolve(mappings.org),
                'TITLE:' + resolve(mappings.title),
                'END:VCARD'
            ].join('\n');
        };
    },

    // URL pattern: https://example.com/{Col_G}
    compileURL(pattern) {
        return (row) => {
            return pattern.replace(/\{(.+?)\}/g, (_, col) => row[col] || '');
        };
    }
};
```

**Live preview:** Show first 4 rows with formulas applied in a table below the mapping.

```js
function updateMappingPreview() {
    const rows = AppState.batchRows.slice(0, 4);
    const mappings = getMappings(); // read from UI
    const table = rows.map(row => {
        const resolved = {};
        for (const [field, mapping] of Object.entries(mappings)) {
            resolved[field] = mapping.resolver(row);
        }
        return resolved;
    });
    renderPreviewTable(table);
}
```

**Step 1: Implement formula engine + mapping UI + preview**
**Step 2: Test manually** — create formula "{Col_A} + ' ' + {Col_B}", verify preview
**Step 3: Commit**

```bash
git add dashboard.html
git commit -m "feat: batch formula concatenation — direct column, formula, vCard, URL mapping"
```

---

### Task 20: Batch SSE progress streaming

**Files:**
- Modify: `main.go` (add SSE endpoint for batch progress)
- Modify: `dashboard.html` (batch step 4 progress bar with EventSource)

**Step 1: Add SSE endpoint in main.go**

Add to `main.go` after batch routes:

```go
mux.HandleFunc("/batch-progress", corsMiddleware(handleBatchProgress))
```

Implement `handleBatchProgress` in `main.go` (or `batch.go`):

```go
var batchProgressCh = make(chan BatchProgress, 100)

type BatchProgress struct {
	Current int    `json:"current"`
	Total   int    `json:"total"`
	Status  string `json:"status"` // "processing", "done", "error"
	Message string `json:"message,omitempty"`
}

func handleBatchProgress(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	for progress := range batchProgressCh {
		data, _ := json.Marshal(progress)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		if progress.Status == "done" || progress.Status == "error" {
			return
		}
	}
}
```

**Step 2: Dashboard EventSource**

```js
function batchExecuteWithProgress(mode) {
    const evtSource = new EventSource('/batch-progress');
    evtSource.onmessage = (e) => {
        const p = JSON.parse(e.data);
        const pct = Math.round(p.current / p.total * 100);
        document.getElementById('batch-progress').style.width = pct + '%';
        document.getElementById('batch-progress').textContent = `${p.current}/${p.total}`;
        if (p.status === 'done' || p.status === 'error') {
            evtSource.close();
        }
    };

    // Start batch job
    api('POST', '/batch-tspl', { /* ... */ });
}
```

**Step 3: Verify build compiles**

Run: `go build -o /dev/null .`
Expected: Clean build.

**Step 4: Commit**

```bash
git add main.go dashboard.html
git commit -m "feat: batch SSE progress streaming for real-time progress bar"
```

---

## Phase 7: Standards Validation (Designer)

### Task 21: Standards validation in designer

**Files:**
- Modify: `dashboard.html` (designer standards selector + validation)

**Standards:**
- **None**: No validation, free-form
- **Gafete/Credential**: Warn if no name field, suggest vCard QR, validate badge dimensions
- **GS1-128**: Validate Application Identifiers (AI), GTIN check digits, required fields (GTIN, batch, expiry)
- **ISO 15223 (Patient)**: Validate MRN, DOB, required identification fields

```js
const Standards = {
    validate(standard, elements) {
        switch (standard) {
            case 'gafete': return this.validateGafete(elements);
            case 'gs1-128': return this.validateGS1(elements);
            case 'iso-15223': return this.validateISO(elements);
            default: return { valid: true, warnings: [] };
        }
    },

    validateGafete(elements) {
        const warnings = [];
        const hasName = elements.some(e => e.type === 'text' && /nombre|name/i.test(e.content));
        const hasQR = elements.some(e => e.type === 'qrcode');
        if (!hasName) warnings.push('Se recomienda incluir un campo de nombre');
        if (!hasQR) warnings.push('Se recomienda incluir un QR con vCard');
        return { valid: true, warnings };
    },

    validateGS1(elements) {
        const warnings = [];
        const barcodes = elements.filter(e => e.type === 'barcode');
        if (barcodes.length === 0) warnings.push('Se requiere al menos un codigo de barras GS1-128');
        // Check for required AIs: (01) GTIN, (10) Batch, (17) Expiry
        return { valid: warnings.length === 0, warnings };
    },

    validateISO(elements) {
        const warnings = [];
        const texts = elements.filter(e => e.type === 'text');
        const hasMRN = texts.some(e => /mrn|expediente|registro/i.test(e.content));
        if (!hasMRN) warnings.push('Se requiere campo de MRN/expediente');
        return { valid: warnings.length === 0, warnings };
    }
};
```

Display warnings in designer sidebar when standard is selected and validation fails.

**Step 1: Implement standards validation**
**Step 2: Test manually** — select Gafete standard, add/remove name field, verify warnings
**Step 3: Commit**

```bash
git add dashboard.html
git commit -m "feat: designer standards validation — Gafete, GS1-128, ISO 15223"
```

---

## Phase 8: Integration & Polish

### Task 22: Setup wizard modal

**Files:**
- Modify: `dashboard.html` (setup wizard modal content)

**Setup wizard flow:**
1. Welcome screen: "Configurar TSC Bridge"
2. Store URL input: `https://tienda.anysubscriptions.com/api` or `https://latam.paygateway-api.com/api`
3. Credentials: API Key + API Secret fields (with help text linking to admin panel)
4. White-label ID (optional, auto-detected from API response)
5. Test connection button → POST /auth/login
6. Success: close modal, update auth badge, load templates

```html
<div class="modal fade" id="setupWizard" data-bs-backdrop="static">
    <div class="modal-dialog modal-dialog-centered">
        <div class="modal-content">
            <div class="modal-header">
                <h5>Configurar TSC Bridge</h5>
            </div>
            <div class="modal-body">
                <div class="mb-3">
                    <label>URL del servidor</label>
                    <input type="url" id="setup-url" class="form-control"
                           placeholder="https://latam.paygateway-api.com/api">
                </div>
                <div class="mb-3">
                    <label>API Key</label>
                    <input type="text" id="setup-key" class="form-control">
                </div>
                <div class="mb-3">
                    <label>API Secret</label>
                    <input type="password" id="setup-secret" class="form-control">
                </div>
                <div class="mb-3">
                    <label>White-label ID (opcional)</label>
                    <input type="number" id="setup-wl" class="form-control" value="20">
                </div>
                <div id="setup-error" class="alert alert-danger d-none"></div>
                <div id="setup-success" class="alert alert-success d-none"></div>
            </div>
            <div class="modal-footer">
                <button class="btn btn-primary" onclick="setupConnect()">Conectar</button>
                <button class="btn btn-secondary" data-bs-dismiss="modal">Omitir</button>
            </div>
        </div>
    </div>
</div>
```

```js
async function setupConnect() {
    const url = document.getElementById('setup-url').value.trim();
    const key = document.getElementById('setup-key').value.trim();
    const secret = document.getElementById('setup-secret').value.trim();
    const wl = parseInt(document.getElementById('setup-wl').value) || 0;

    try {
        await api('POST', '/auth/login', {
            api_url: url,
            api_key: key,
            api_secret: secret,
            wl_id: wl
        });
        document.getElementById('setup-success').textContent = 'Conectado exitosamente';
        document.getElementById('setup-success').classList.remove('d-none');
        setTimeout(() => {
            bootstrap.Modal.getInstance(document.getElementById('setupWizard')).hide();
            refreshAuthState();
            loadTemplates();
        }, 1500);
    } catch (e) {
        document.getElementById('setup-error').textContent = e.message || 'Error de conexion';
        document.getElementById('setup-error').classList.remove('d-none');
    }
}

// Auto-show on first load if not configured
async function checkSetupNeeded() {
    const state = await api('GET', '/auth/state');
    AppState.authState = state;
    if (!state.configured) {
        new bootstrap.Modal(document.getElementById('setupWizard')).show();
    }
}
```

**Step 1: Implement setup wizard modal**
**Step 2: Test manually** — clear config, reload, verify wizard auto-shows
**Step 3: Commit**

```bash
git add dashboard.html
git commit -m "feat: setup wizard modal — auto-shows on first run, validates credentials"
```

---

### Task 23: Final integration and build verification

**Files:**
- Verify: all `.go` files compile
- Verify: `dashboard.html` loads in webview

**Step 1: Clean build**

```bash
cd /Users/mario/PARA/1_Proyectos/ISI_Hospital/Servicios/tsc-bridge
go vet ./...
go build -o tsc-bridge .
```

Expected: No errors, binary produced.

**Step 2: Run all tests**

```bash
go test -v ./...
```

Expected: All tests pass (config_test.go, dpi_test.go, auth_test.go).

**Step 3: Manual smoke test**

```bash
./tsc-bridge
```

Verify:
- [ ] System tray icon appears with correct menu
- [ ] Dashboard opens in native webview
- [ ] 5 tabs visible and navigable
- [ ] Printer list populates
- [ ] DPI badge shows correct value
- [ ] Config tab loads and saves
- [ ] Setup wizard shows if no auth configured

**Step 4: Commit**

```bash
git add -A
git commit -m "feat: TSC Bridge v3.0.0 — native dashboard, DPI detection, auth, designer"
```

---

## Dependency Graph

```
Task 1 (webview dep)
    └─→ Task 7 (webview.go)
            └─→ Task 9 (main.go webview)

Task 2 (config PrinterDPI)
    └─→ Task 3 (dpi.go)
            └─→ Task 4 (/status DPI)

Task 5 (auth.go)
    └─→ Task 6 (auth routes)

Task 8 (tray.go) ← depends on Task 3 (DPI), Task 7 (webview)

Task 10 (dashboard shell) ← depends on Tasks 4, 6
    ├─→ Task 11 (Tab 1 Impresoras)
    ├─→ Task 12 (Tab 5 Config)
    ├─→ Task 13 (Tab 4 Templates)
    ├─→ Task 14 (Tab 2 Designer canvas)
    │       ├─→ Task 15 (Properties panel)
    │       ├─→ Task 16 (QR Builder)
    │       ├─→ Task 17 (TSPL preview + export)
    │       └─→ Task 21 (Standards validation)
    ├─→ Task 18 (Tab 3 Batch wizard)
    │       ├─→ Task 19 (Formula engine)
    │       └─→ Task 20 (SSE progress)
    └─→ Task 22 (Setup wizard modal)

Task 23 (integration) ← depends on all above
```

## Estimated Scope

| Phase | Tasks | New/Modified Files |
|-------|-------|--------------------|
| Phase 1: Foundation | 1-4 | config.go, dpi.go, dpi_*.go, main.go |
| Phase 2: Auth | 5-6 | auth.go, main.go |
| Phase 3: Webview + Tray | 7-9 | webview.go, tray.go, main.go |
| Phase 4: Dashboard | 10-13 | dashboard.html |
| Phase 5: Designer | 14-17 | dashboard.html |
| Phase 6: Batch | 18-20 | dashboard.html, main.go |
| Phase 7: Standards | 21 | dashboard.html |
| Phase 8: Polish | 22-23 | dashboard.html |
