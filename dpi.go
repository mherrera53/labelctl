package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DPI detection regexes
var (
	reDPISimple     = regexp.MustCompile(`(\d{3})\s*DPI`)
	reDPIResolution = regexp.MustCompile(`[Rr]esolution[:\s]+(\d{3})\s*[xX]\s*(\d{3})`)
)

// parseDPIFromTSCResponse extracts DPI from a TSC printer response string.
// Supports formats like "203 DPI", "300 DPI", "Resolution: 203x203", etc.
func parseDPIFromTSCResponse(response string) (int, bool) {
	// Try "NNN DPI" format first
	if m := reDPISimple.FindStringSubmatch(response); len(m) > 1 {
		if dpi, err := strconv.Atoi(m[1]); err == nil && dpi > 0 {
			return dpi, true
		}
	}

	// Try "Resolution: NNNxNNN" format
	if m := reDPIResolution.FindStringSubmatch(response); len(m) > 1 {
		if dpi, err := strconv.Atoi(m[1]); err == nil && dpi > 0 {
			return dpi, true
		}
	}

	return 0, false
}

// GetPrinterDPI returns the cached DPI for a printer, or defaultDPI if unknown.
func GetPrinterDPI(printerName string) int {
	configMu.RLock()
	defer configMu.RUnlock()
	if entry, ok := appConfig.PrinterDPI[printerName]; ok {
		return entry.DPI
	}
	return defaultDPI
}

// SetPrinterDPI saves a DPI value for a printer to the config.
func SetPrinterDPI(printerName string, dpi int, source string) {
	configMu.Lock()
	if appConfig.PrinterDPI == nil {
		appConfig.PrinterDPI = map[string]PrinterDPIEntry{}
	}
	appConfig.PrinterDPI[printerName] = PrinterDPIEntry{DPI: dpi, Source: source}
	configMu.Unlock()

	if err := saveConfig(); err != nil {
		log.Printf("[dpi] Failed to save config after setting DPI for %s: %v", printerName, err)
	}
	log.Printf("[dpi] Set %s DPI=%d (source=%s)", printerName, dpi, source)
}

// probeTSPLForDPI sends a TSPL ~!I command to a network printer and parses DPI from the response.
func probeTSPLForDPI(addr string) (int, bool) {
	conn, err := net.DialTimeout("tcp", addr, probeTimeout)
	if err != nil {
		return 0, false
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(probeTimeout))

	// Send status/info command
	_, err = conn.Write([]byte("~!I\r\n"))
	if err != nil {
		return 0, false
	}

	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		return 0, false
	}

	return parseDPIFromTSCResponse(string(buf[:n]))
}

// DetectPrinterDPI runs the DPI detection chain for a single printer:
//  1. Config cache (already known)
//  2. OS driver query (lpoptions on macOS, WMI on Windows)
//  3. TSPL probe (network printers only — send ~!I and parse response)
//  4. Default (203 DPI)
func DetectPrinterDPI(printer PrinterInfo) int {
	// 1. Check config cache
	configMu.RLock()
	if entry, ok := appConfig.PrinterDPI[printer.Name]; ok {
		configMu.RUnlock()
		log.Printf("[dpi] %s: cached DPI=%d (source=%s)", printer.Name, entry.DPI, entry.Source)
		return entry.DPI
	}
	configMu.RUnlock()

	// 2. Try OS driver query
	if dpi, ok := queryDriverDPI(printer.Name); ok {
		SetPrinterDPI(printer.Name, dpi, "driver")
		return dpi
	}

	// 3. Try TSPL probe (only for network printers with an address)
	if printer.Address != "" {
		if dpi, ok := probeTSPLForDPI(printer.Address); ok {
			SetPrinterDPI(printer.Name, dpi, "tspl_probe")
			return dpi
		}
	}

	// 4. Default
	log.Printf("[dpi] %s: using default DPI=%d", printer.Name, defaultDPI)
	return defaultDPI
}

// DetectAllPrinterDPIs runs DPI detection for all known printers.
func DetectAllPrinterDPIs() {
	printers, err := listAllPrinters()
	if err != nil {
		log.Printf("[dpi] Failed to list printers: %v", err)
		return
	}

	log.Printf("[dpi] Detecting DPI for %d printer(s)...", len(printers))
	for _, p := range printers {
		DetectPrinterDPI(p)
	}
	log.Printf("[dpi] DPI detection complete")
}

// handleDPIDetect handles POST /dpi/detect?printer=name
func handleDPIDetect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	printerName := r.URL.Query().Get("printer")
	if printerName == "" {
		// Detect all
		DetectAllPrinterDPIs()
		configMu.RLock()
		flat := make(map[string]int, len(appConfig.PrinterDPI))
		for name, entry := range appConfig.PrinterDPI {
			flat[name] = entry.DPI
		}
		configMu.RUnlock()
		jsonResponse(w, http.StatusOK, map[string]any{
			"status":      "detected",
			"printer_dpi": flat,
			"default_dpi": defaultDPI,
		})
		return
	}

	// Find the specific printer
	printers, err := listAllPrinters()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "failed to list printers: " + err.Error()})
		return
	}

	printer := findPrinter(printerName, printers)
	if printer == nil {
		jsonResponse(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("printer %q not found", printerName)})
		return
	}

	dpi := DetectPrinterDPI(*printer)
	jsonResponse(w, http.StatusOK, map[string]any{
		"status":  "detected",
		"printer": printerName,
		"dpi":     dpi,
	})
}

// handleDPISet handles PUT /dpi?printer=name&dpi=300
func handleDPISet(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		// Return current DPI map
		configMu.RLock()
		flat := make(map[string]int, len(appConfig.PrinterDPI))
		for name, entry := range appConfig.PrinterDPI {
			flat[name] = entry.DPI
		}
		configMu.RUnlock()
		jsonResponse(w, http.StatusOK, map[string]any{
			"printer_dpi": flat,
			"default_dpi": defaultDPI,
		})
		return
	}

	if r.Method != http.MethodPut {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	printerName := r.URL.Query().Get("printer")
	dpiStr := r.URL.Query().Get("dpi")

	// Also accept JSON body
	if printerName == "" || dpiStr == "" {
		var body struct {
			Printer string `json:"printer"`
			DPI     int    `json:"dpi"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			if printerName == "" {
				printerName = body.Printer
			}
			if dpiStr == "" && body.DPI > 0 {
				dpiStr = strconv.Itoa(body.DPI)
			}
		}
	}

	if printerName == "" {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "printer name required"})
		return
	}
	if dpiStr == "" {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "dpi value required"})
		return
	}

	dpi, err := strconv.Atoi(dpiStr)
	if err != nil || dpi <= 0 {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid dpi value"})
		return
	}

	// Validate reasonable DPI range
	if dpi < 100 || dpi > 600 {
		jsonResponse(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("DPI %d out of range (100-600)", dpi),
		})
		return
	}

	SetPrinterDPI(printerName, dpi, "manual")
	jsonResponse(w, http.StatusOK, map[string]any{
		"status":  "saved",
		"printer": printerName,
		"dpi":     dpi,
		"source":  "manual",
	})
}

// stripPrinterName normalizes a printer name for comparison.
func stripPrinterName(name string) string {
	// Remove common suffixes/prefixes for matching
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, " ", "-")
	return strings.ToLower(name)
}
