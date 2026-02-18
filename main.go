package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const version = "2.0.0"

var startTime = time.Now()

// corsMiddleware adds CORS headers to every response.
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next(w, r)
	}
}

// jsonResponse writes a JSON response with the given status code.
func jsonResponse(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// listAllPrinters merges local (USB/CUPS/Spooler) and network printers.
func listAllPrinters() ([]PrinterInfo, error) {
	local, err := listLocalPrinters()
	if err != nil {
		return nil, err
	}
	network := getNetworkPrinters()
	all := make([]PrinterInfo, 0, len(local)+len(network))
	all = append(all, local...)
	all = append(all, network...)
	return all, nil
}

// printerNames extracts plain name strings for backward compatibility.
func printerNames(printers []PrinterInfo) []string {
	names := make([]string, len(printers))
	for i, p := range printers {
		names[i] = p.Name
	}
	return names
}

// findPrinter looks up a printer by name in the combined list.
func findPrinter(name string, printers []PrinterInfo) *PrinterInfo {
	for i := range printers {
		if printers[i].Name == name {
			return &printers[i]
		}
	}
	return nil
}

// handleStatus returns agent status info.
func handleStatus(w http.ResponseWriter, r *http.Request) {
	printers, _ := listAllPrinters()
	cfg := getConfig()
	httpsPort := cfg.Port + 1
	jsonResponse(w, http.StatusOK, map[string]any{
		"status":          "ok",
		"version":         version,
		"os":              runtime.GOOS,
		"uptime":          time.Since(startTime).Truncate(time.Second).String(),
		"port":            cfg.Port,
		"https_port":      httpsPort,
		"hostname":        defaultHostname,
		"dashboard":       fmt.Sprintf("https://%s:%d/", defaultHostname, httpsPort),
		"printers":        printers,
		"names":           printerNames(printers),
		"default_printer": cfg.DefaultPrinter,
		"default_preset":  cfg.DefaultPreset,
		"share":           getShareStatus(),
	})
}

// handlePrinters returns the list of detected TSC printers.
func handlePrinters(w http.ResponseWriter, r *http.Request) {
	printers, err := listAllPrinters()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{
		"printers": printers,
		"names":    printerNames(printers),
	})
}

// handleDiscover forces a network rescan and returns discovered printers.
func handleDiscover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	network := refreshNetworkPrinters()
	jsonResponse(w, http.StatusOK, map[string]any{
		"network_printers": network,
		"count":            len(network),
	})
}

// handleAutoStart queries or toggles OS auto-start at login.
func handleAutoStart(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonResponse(w, http.StatusOK, map[string]any{
			"enabled": isAutoStartEnabled(),
			"os":      runtime.GOOS,
		})

	case http.MethodPost:
		var req struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		if err := setAutoStart(req.Enabled); err != nil {
			jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		configMu.Lock()
		appConfig.AutoStart = req.Enabled
		configMu.Unlock()
		saveConfig()
		jsonResponse(w, http.StatusOK, map[string]any{
			"enabled": isAutoStartEnabled(),
			"os":      runtime.GOOS,
		})

	default:
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// handleDownload serves the tsc-bridge binary for the requested OS.
// GET /download?os=mac|windows
func handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	reqOS := r.URL.Query().Get("os")
	if reqOS == "" {
		reqOS = runtime.GOOS
	}

	var binPath, filename string
	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)

	switch reqOS {
	case "darwin", "mac":
		binPath = filepath.Join(exeDir, "tsc-bridge-mac")
		filename = "tsc-bridge-mac"
		// Also check current binary if it's macOS
		if _, err := os.Stat(binPath); os.IsNotExist(err) && runtime.GOOS == "darwin" {
			binPath = exePath
		}
	case "windows", "win":
		binPath = filepath.Join(exeDir, "tsc-bridge.exe")
		filename = "tsc-bridge.exe"
	default:
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "os must be mac or windows"})
		return
	}

	f, err := os.Open(binPath)
	if err != nil {
		jsonResponse(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("binary not found for %s: %v", reqOS, err),
		})
		return
	}
	defer f.Close()

	stat, _ := f.Stat()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))
	io.Copy(w, f)
}

// handleShare toggles or queries USB printer sharing over the network.
func handleShare(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonResponse(w, http.StatusOK, getShareStatus())

	case http.MethodPost:
		var req struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}

		// Update config
		configMu.Lock()
		appConfig.ShareEnabled = req.Enabled
		configMu.Unlock()
		saveConfig()

		toggleShare(req.Enabled)
		jsonResponse(w, http.StatusOK, getShareStatus())

	default:
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// handlePrint receives raw TSPL2 commands and sends them to a TSC printer.
// Optional query params:
//   - printer: target printer name
//   - preset: preset ID to wrap TSPL with SIZE/DIRECTION/GAP header
func handlePrint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
		})
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil || len(body) == 0 {
		jsonResponse(w, http.StatusBadRequest, map[string]string{
			"error": "empty body",
		})
		return
	}

	// Determine target printer
	printerName := r.URL.Query().Get("printer")
	if printerName == "" {
		cfg := getConfig()
		printerName = cfg.DefaultPrinter
	}

	allPrinters, _ := listAllPrinters()
	var targetPrinter *PrinterInfo

	if printerName == "" {
		if len(allPrinters) == 0 {
			jsonResponse(w, http.StatusServiceUnavailable, map[string]string{
				"error": "no TSC printer found",
			})
			return
		}
		targetPrinter = &allPrinters[0]
		printerName = targetPrinter.Name
	} else {
		targetPrinter = findPrinter(printerName, allPrinters)
	}

	// Apply preset header if requested
	presetID := r.URL.Query().Get("preset")
	if presetID != "" {
		cfg := getConfig()
		preset := getPresetByID(presetID, cfg.CustomPresets)
		if preset != nil {
			header := generatePresetHeader(preset)
			cleaned := stripSetupCommands(string(body))
			body = []byte(header + cleaned)
			log.Printf("[print] Applied preset %s header (%d bytes total)", presetID, len(body))
		} else {
			log.Printf("[print] Preset %s not found, sending raw", presetID)
		}
	}

	// Route print by type
	var printErr error
	if targetPrinter != nil && targetPrinter.Type == "network" {
		printErr = networkRawPrint(targetPrinter.Address, body)
	} else {
		printErr = rawPrint(printerName, body)
	}

	if printErr != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("print failed: %v", printErr),
		})
		return
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"status":  "printed",
		"printer": printerName,
		"bytes":   len(body),
		"preset":  presetID,
	})
}

// handleTestPrint generates a test label using the active or specified preset.
func handleTestPrint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	cfg := getConfig()
	presetID := r.URL.Query().Get("preset")
	if presetID == "" {
		presetID = cfg.DefaultPreset
	}

	preset := getPresetByID(presetID, cfg.CustomPresets)
	if preset == nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "preset not found: " + presetID})
		return
	}

	// Generate test TSPL
	tspl := generatePresetHeader(preset)
	for i := 0; i < preset.Columns; i++ {
		x := 8
		if i < len(preset.ColOffsets) {
			x = preset.ColOffsets[i]
		}
		tspl += fmt.Sprintf("TEXT %d,16,\"3\",0,1,1,\"TEST COL %d\"\r\n", x, i+1)
		tspl += fmt.Sprintf("BARCODE %d,48,\"128\",40,1,0,2,2,\"12345%d\"\r\n", x, i)
	}
	tspl += "PRINT 1\r\n"

	printerName := r.URL.Query().Get("printer")
	if printerName == "" {
		printerName = cfg.DefaultPrinter
	}

	allPrinters, _ := listAllPrinters()
	var targetPrinter *PrinterInfo

	if printerName == "" {
		if len(allPrinters) > 0 {
			targetPrinter = &allPrinters[0]
			printerName = targetPrinter.Name
		}
	} else {
		targetPrinter = findPrinter(printerName, allPrinters)
	}
	if printerName == "" {
		jsonResponse(w, http.StatusServiceUnavailable, map[string]string{"error": "no printer found"})
		return
	}

	var printErr error
	if targetPrinter != nil && targetPrinter.Type == "network" {
		printErr = networkRawPrint(targetPrinter.Address, []byte(tspl))
	} else {
		printErr = rawPrint(printerName, []byte(tspl))
	}
	if printErr != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("test print failed: %v", printErr),
		})
		return
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"status":   "printed",
		"printer":  printerName,
		"preset":   presetID,
		"bytes":    len(tspl),
		"commands": tspl,
	})
}

// stripSetupCommands removes TSPL setup commands from a raw command string
// so they can be replaced by the preset header.
func stripSetupCommands(tspl string) string {
	setupPrefixes := []string{
		"SIZE ", "GAP ", "DIRECTION ", "SPEED ", "DENSITY ",
		"REFERENCE ", "CODEPAGE ", "SET TEAR ", "SHIFT ", "CLS",
	}
	var lines []string
	for _, line := range strings.Split(tspl, "\r\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		isSetup := false
		for _, prefix := range setupPrefixes {
			if strings.HasPrefix(strings.ToUpper(trimmed), prefix) {
				isSetup = true
				break
			}
		}
		if !isSetup {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}

// serviceRunning checks if a tsc-bridge service is already listening.
func serviceRunning(addr string) bool {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get("http://" + addr + "/status")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func main() {
	// Load configuration
	initConfig()

	port := os.Getenv("TSC_BRIDGE_PORT")
	if port == "" {
		cfg := getConfig()
		port = fmt.Sprintf("%d", cfg.Port)
	}

	httpAddr := "127.0.0.1:" + port
	dashURL := fmt.Sprintf("http://%s/", httpAddr)

	// --dashboard / -d: open native GUI window
	if hasFlag("--dashboard") || hasFlag("-d") {
		if serviceRunning(httpAddr) {
			log.Printf("Service already running on %s — opening GUI only", httpAddr)
			openGUI(dashURL)
			return
		}
		// No service running — start servers then open GUI
		log.Printf("No service detected — starting servers + GUI")
		startServers(port)
		go startNetworkScanner()
		go startShareServer()
		time.Sleep(300 * time.Millisecond)
		openGUI(dashURL)
		return
	}

	// Service mode: start servers and block forever
	startServers(port)
	go startNetworkScanner()
	go startShareServer()
	log.Printf("tsc-bridge v%s started (os=%s)", version, runtime.GOOS)
	log.Printf("Dashboard: %s", dashURL)
	log.Printf("API (HTTP): http://%s/", httpAddr)
	select {}
}

func startServers(port string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", corsMiddleware(handleDashboard))
	mux.HandleFunc("/status", corsMiddleware(handleStatus))
	mux.HandleFunc("/printers", corsMiddleware(handlePrinters))
	mux.HandleFunc("/print", corsMiddleware(handlePrint))
	mux.HandleFunc("/test-print", corsMiddleware(handleTestPrint))
	mux.HandleFunc("/config", corsMiddleware(handleConfig))
	mux.HandleFunc("/discover", corsMiddleware(handleDiscover))
	mux.HandleFunc("/share", corsMiddleware(handleShare))
	mux.HandleFunc("/autostart", corsMiddleware(handleAutoStart))
	mux.HandleFunc("/download", corsMiddleware(handleDownload))
	mux.HandleFunc("/presets", corsMiddleware(handlePresets))
	mux.HandleFunc("/presets/", corsMiddleware(handlePresets))

	// HTTP server
	httpAddr := "127.0.0.1:" + port
	go func() {
		log.Printf("HTTP  server on %s", httpAddr)
		if err := http.ListenAndServe(httpAddr, mux); err != nil {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// HTTPS server with auto-generated certs
	httpsPort := fmt.Sprintf("%d", portInt(port)+1)
	httpsAddr := "127.0.0.1:" + httpsPort
	certFile, keyFile, _, err := ensureCerts(defaultHostname)
	if err != nil {
		log.Printf("[tls] Could not generate certs: %v — HTTPS disabled", err)
		return
	}
	tlsCert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		log.Printf("[tls] Could not load certs: %v — HTTPS disabled", err)
		return
	}
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	}
	httpsServer := &http.Server{
		Addr:      httpsAddr,
		Handler:   mux,
		TLSConfig: tlsConfig,
	}
	go func() {
		log.Printf("HTTPS server on %s (https://%s:%s/)", httpsAddr, defaultHostname, httpsPort)
		if err := httpsServer.ListenAndServeTLS("", ""); err != nil {
			log.Printf("[tls] HTTPS server error: %v", err)
		}
	}()
}

func portInt(s string) int {
	n, _ := strconv.Atoi(s)
	if n == 0 {
		return 9271
	}
	return n
}

// hasFlag checks if a CLI flag is present in os.Args.
func hasFlag(flag string) bool {
	for _, arg := range os.Args[1:] {
		if arg == flag {
			return true
		}
	}
	return false
}
