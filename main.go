package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const version = "2.3.0"

var startTime = time.Now()

func init() {
	// Lock the main goroutine to the OS thread.
	// Required by systray for the GUI message loop.
	runtime.LockOSThread()

	// Ensure at least 4 OS threads are available for goroutines.
	// Critical on Windows: systray.Run() blocks the main thread
	// in CGO calls — the HTTP server goroutine needs its own thread to run.
	if runtime.GOMAXPROCS(0) < 4 {
		runtime.GOMAXPROCS(4)
	}
}

// setupFileLogging redirects log output to a file in the config directory.
// Critical on Windows with -H windowsgui where there is no console.
func setupFileLogging() {
	dir := configDir()
	os.MkdirAll(dir, 0755)
	logPath := filepath.Join(dir, "tsc-bridge.log")

	// Truncate if too large (> 1MB)
	if info, err := os.Stat(logPath); err == nil && info.Size() > 1024*1024 {
		os.Remove(logPath)
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	log.SetOutput(f)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
}

// waitForServer polls the HTTP server until it responds or times out.
func waitForServer(addr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://" + addr + "/status")
		if err == nil {
			resp.Body.Close()
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// corsMiddleware adds CORS headers to every response.
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")

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
	if targetPrinter != nil && (targetPrinter.Type == "network" || targetPrinter.Type == "manual" || targetPrinter.Type == "raw") {
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
	if targetPrinter != nil && (targetPrinter.Type == "network" || targetPrinter.Type == "manual" || targetPrinter.Type == "raw") {
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

// handleManualPrinters handles GET/DELETE for manually configured printers.
func handleManualPrinters(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := getConfig()
		jsonResponse(w, http.StatusOK, map[string]any{
			"manual_printers": cfg.ManualPrinters,
		})

	case http.MethodDelete:
		ip := r.URL.Query().Get("ip")
		if ip == "" {
			jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "ip parameter required"})
			return
		}
		configMu.Lock()
		filtered := make([]string, 0, len(appConfig.ManualPrinters))
		removed := false
		for _, addr := range appConfig.ManualPrinters {
			if addr == ip {
				removed = true
				continue
			}
			filtered = append(filtered, addr)
		}
		appConfig.ManualPrinters = filtered
		configMu.Unlock()

		if !removed {
			jsonResponse(w, http.StatusNotFound, map[string]string{"error": "ip not found in manual printers"})
			return
		}
		if err := saveConfig(); err != nil {
			jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "save failed: " + err.Error()})
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{
			"status":          "removed",
			"manual_printers": filtered,
		})

	default:
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
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
	// File logging — critical on Windows where -H windowsgui hides all output
	setupFileLogging()
	log.Printf("=== tsc-bridge v%s starting (os=%s, args=%v) ===", version, runtime.GOOS, os.Args)

	// Catch panics — log to file and show MessageBox on Windows
	defer func() {
		if r := recover(); r != nil {
			msg := fmt.Sprintf("PANIC: %v", r)
			log.Print(msg)
			buf := make([]byte, 4096)
			n := runtime.Stack(buf, false)
			log.Printf("Stack:\n%s", buf[:n])
			showError("TSC Bridge — Error fatal", msg)
		}
	}()

	// Load configuration
	initConfig()
	LoadTemplates()

	port := os.Getenv("TSC_BRIDGE_PORT")
	if port == "" {
		cfg := getConfig()
		port = fmt.Sprintf("%d", cfg.Port)
	}

	httpAddr := "127.0.0.1:" + port
	dashURL := fmt.Sprintf("http://%s/", httpAddr)

	headless := hasFlag("--headless") || hasFlag("--service")

	// If another instance is already running...
	if serviceRunning(httpAddr) {
		if headless {
			log.Printf("Service already running on %s — exiting", httpAddr)
			return
		}
		log.Printf("Service already running on %s — opening dashboard in browser", httpAddr)
		openBrowser(dashURL)
		return
	}

	// Start HTTP/HTTPS servers + background services
	if err := startServers(port); err != nil {
		// Port in use — kill zombie instances and retry once
		log.Printf("First bind attempt failed: %v — killing old instances and retrying", err)
		killOldInstances()
		time.Sleep(2 * time.Second)

		if err2 := startServers(port); err2 != nil {
			msg := fmt.Sprintf("No se pudo iniciar en el puerto %s.\n\n%v\n\nCierre todas las instancias de tsc-bridge e intente de nuevo.", port, err2)
			log.Printf("ERROR: %s", msg)
			showError("TSC Bridge — Error", msg)
			return
		}
		log.Printf("Retry succeeded after killing old instances")
	}
	go startNetworkScanner()
	go startShareServer()

	// Verify HTTP server is actually accepting connections before proceeding.
	// Critical on Windows: systray.Run() enters a Win32 message loop that can
	// starve goroutines if the HTTP serve goroutine hasn't been scheduled yet.
	if !waitForServer(httpAddr, 5*time.Second) {
		log.Printf("WARNING: HTTP server bound but slow to accept — continuing anyway")
	}
	log.Printf("tsc-bridge v%s ready on %s", version, httpAddr)

	// Run system tray — blocks until user clicks "Salir"
	// autoOpen=true when NOT headless → spawns dashboard window on first run
	log.Printf("Starting system tray (headless=%v)", headless)
	runTray(dashURL, !headless)
}

func startServers(port string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", corsMiddleware(handleDashboard))
	mux.HandleFunc("/status", corsMiddleware(handleStatus))
	mux.HandleFunc("/printers", corsMiddleware(handlePrinters))
	mux.HandleFunc("/print", corsMiddleware(handlePrint))
	mux.HandleFunc("/test-print", corsMiddleware(handleTestPrint))
	mux.HandleFunc("/config", corsMiddleware(handleConfig))
	mux.HandleFunc("/whitelabel", corsMiddleware(handleWhitelabel))
	mux.HandleFunc("/discover", corsMiddleware(handleDiscover))
	mux.HandleFunc("/share", corsMiddleware(handleShare))
	mux.HandleFunc("/autostart", corsMiddleware(handleAutoStart))
	mux.HandleFunc("/download", corsMiddleware(handleDownload))
	mux.HandleFunc("/presets", corsMiddleware(handlePresets))
	mux.HandleFunc("/presets/", corsMiddleware(handlePresets))
	mux.HandleFunc("/manual-printers", corsMiddleware(handleManualPrinters))

	// Driver detection & setup
	mux.HandleFunc("/driver/status", corsMiddleware(handleDriverStatus))
	mux.HandleFunc("/driver/setup", corsMiddleware(handleDriverSetup))
	mux.HandleFunc("/driver/progress", corsMiddleware(handleDriverProgress))

	// Batch printing routes
	mux.HandleFunc("/api/test", corsMiddleware(handleApiTest))
	mux.HandleFunc("/api/templates", corsMiddleware(handleApiTemplates))
	mux.HandleFunc("/excel/upload", corsMiddleware(handleExcelUpload))
	mux.HandleFunc("/templates", corsMiddleware(handleLabelTemplates))
	mux.HandleFunc("/templates/", corsMiddleware(handleLabelTemplateByID))
	mux.HandleFunc("/batch-print", corsMiddleware(handleBatchPrint))
	mux.HandleFunc("/batch-preview", corsMiddleware(handleBatchPreview))
	mux.HandleFunc("/batch-pdf", corsMiddleware(handleBatchPdf))

	// HTTP server — bind synchronously so we catch port-in-use errors immediately
	httpAddr := "127.0.0.1:" + port
	httpLn, err := net.Listen("tcp", httpAddr)
	if err != nil {
		return fmt.Errorf("HTTP bind %s: %w", httpAddr, err)
	}

	// Start HTTP server on a DEDICATED OS thread.
	// Critical on Windows: the main thread will be blocked by systray.Run()
	// in a CGO call. Without its own thread, the HTTP goroutine would be
	// starved and never serve requests.
	httpReady := make(chan struct{})
	go func() {
		runtime.LockOSThread() // pin this goroutine to its own OS thread
		close(httpReady)       // signal: thread is alive, about to serve
		log.Printf("HTTP server on %s (dedicated thread)", httpAddr)
		if err := http.Serve(httpLn, mux); err != nil {
			log.Printf("HTTP server error: %v", err)
		}
	}()
	<-httpReady // wait until the HTTP goroutine has its own thread

	// HTTPS server with auto-generated certs (also on dedicated thread)
	httpsPort := fmt.Sprintf("%d", portInt(port)+1)
	httpsAddr := "127.0.0.1:" + httpsPort
	certFile, keyFile, _, err := ensureCerts(defaultHostname)
	if err != nil {
		log.Printf("[tls] Could not generate certs: %v — HTTPS disabled", err)
		return nil // HTTP is running, HTTPS is optional
	}
	tlsCert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		log.Printf("[tls] Could not load certs: %v — HTTPS disabled", err)
		return nil
	}
	httpsLn, err := net.Listen("tcp", httpsAddr)
	if err != nil {
		log.Printf("[tls] Could not bind %s: %v — HTTPS disabled", httpsAddr, err)
		return nil
	}
	tlsListener := tls.NewListener(httpsLn, &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	})
	httpsReady := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		close(httpsReady)
		log.Printf("HTTPS server on %s (https://%s:%s/)", httpsAddr, defaultHostname, httpsPort)
		if err := http.Serve(tlsListener, mux); err != nil {
			log.Printf("[tls] HTTPS server error: %v", err)
		}
	}()
	<-httpsReady

	return nil
}

func portInt(s string) int {
	n, _ := strconv.Atoi(s)
	if n == 0 {
		return 9638
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

// --- Batch printing handlers ---

// handleApiTest tests the connection to the backend API.
func handleApiTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	cfg := getConfig()
	if cfg.ApiURL == "" {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "api_url not configured"})
		return
	}
	client := NewApiClient(cfg)
	if err := client.TestConnection(); err != nil {
		jsonResponse(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	// Count available templates
	templates, _ := client.FetchTemplates()
	thermalCount := 0
	for _, t := range templates {
		if t.Categoria == "etiqueta" || t.ThermalPrintable == 1 {
			thermalCount++
		}
	}
	jsonResponse(w, http.StatusOK, map[string]any{
		"status":           "connected",
		"api_url":          cfg.ApiURL,
		"wl":               cfg.ApiWhiteLabel,
		"total_templates":  len(templates),
		"thermal_templates": thermalCount,
	})
}

// handleApiTemplates fetches PDF templates from the backend.
// GET /api/templates — list all
// GET /api/templates?id=UUID — get detail + fields
func handleApiTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	cfg := getConfig()
	if cfg.ApiURL == "" {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "api_url not configured"})
		return
	}
	client := NewApiClient(cfg)

	// If specific ID requested, return detail
	templateID := r.URL.Query().Get("id")
	if templateID != "" {
		detail, err := client.FetchTemplateDetail(templateID)
		if err != nil {
			jsonResponse(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		jsonResponse(w, http.StatusOK, detail)
		return
	}

	// List all templates
	templates, err := client.FetchTemplates()
	if err != nil {
		jsonResponse(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"templates": templates})
}

// handleExcelUpload parses an uploaded xlsx file.
func handleExcelUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// Limit upload to 10MB
	r.ParseMultipartForm(10 << 20)
	file, _, err := r.FormFile("file")
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "file required: " + err.Error()})
		return
	}
	defer file.Close()

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "read file: " + err.Error()})
		return
	}

	data, err := ParseExcel(fileBytes)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "parse error: " + err.Error()})
		return
	}

	jsonResponse(w, http.StatusOK, data)
}

// handleLabelTemplates handles GET (list) and POST (create) for label templates.
func handleLabelTemplates(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonResponse(w, http.StatusOK, map[string]any{"templates": GetAllTemplates()})

	case http.MethodPost:
		var t LabelTemplate
		if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
			jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		if t.ID == "" || t.Name == "" {
			jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "id and name are required"})
			return
		}
		if err := SaveTemplate(t); err != nil {
			jsonResponse(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "saved", "id": t.ID})

	default:
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// handleLabelTemplateByID handles GET/PUT/DELETE for a specific template.
func handleLabelTemplateByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/templates/")
	if id == "" {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "template id required"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		t := GetTemplate(id)
		if t == nil {
			jsonResponse(w, http.StatusNotFound, map[string]string{"error": "template not found"})
			return
		}
		jsonResponse(w, http.StatusOK, t)

	case http.MethodPut:
		var t LabelTemplate
		if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
			jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		t.ID = id
		if err := SaveTemplate(t); err != nil {
			jsonResponse(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "updated"})

	case http.MethodDelete:
		if err := DeleteTemplate(id); err != nil {
			status := http.StatusNotFound
			if strings.Contains(err.Error(), "built-in") {
				status = http.StatusForbidden
			}
			jsonResponse(w, status, map[string]string{"error": err.Error()})
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "deleted"})

	default:
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// buildBatchJob creates a BatchJob from a request, supporting both backend and local modes.
// mode "backend": template_id is a UUID, TSPL generated server-side via API
// mode "local": template_id is a local template ID, TSPL generated locally
func buildBatchJob(req struct {
	TemplateID string              `json:"template_id"`
	PresetID   string              `json:"preset_id"`
	Layout     string              `json:"layout"`
	Rows       []map[string]string `json:"rows"`
	Copies     int                 `json:"copies"`
	Printer    string              `json:"printer"`
	Mode       string              `json:"mode"` // "backend" or "local", auto-detected if empty
}) (*BatchJob, error) {
	if len(req.Rows) == 0 {
		return nil, fmt.Errorf("no rows provided")
	}

	mode := req.Mode
	// Auto-detect: UUIDs have dashes, local IDs don't
	if mode == "" {
		if strings.Contains(req.TemplateID, "-") && len(req.TemplateID) > 30 {
			mode = "backend"
		} else {
			mode = "local"
		}
	}

	job := &BatchJob{
		Rows:    req.Rows,
		Copies:  req.Copies,
		Printer: req.Printer,
		Mode:    mode,
	}

	if mode == "backend" {
		job.BackendTemplateID = req.TemplateID
		job.Layout = req.Layout
		job.PresetName = req.PresetID
		return job, nil
	}

	// Local mode: resolve template and preset
	tmpl := GetTemplate(req.TemplateID)
	if tmpl == nil {
		return nil, fmt.Errorf("local template not found: %s", req.TemplateID)
	}
	job.Template = tmpl

	presetID := req.PresetID
	if presetID == "" {
		presetID = tmpl.PresetID
	}
	cfg := getConfig()
	preset := getPresetByID(presetID, cfg.CustomPresets)
	if preset == nil {
		return nil, fmt.Errorf("preset not found: %s", presetID)
	}
	job.Preset = preset
	return job, nil
}

// handleBatchPrint executes a batch print job.
func handleBatchPrint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req struct {
		TemplateID string              `json:"template_id"`
		PresetID   string              `json:"preset_id"`
		Layout     string              `json:"layout"`
		Rows       []map[string]string `json:"rows"`
		Copies     int                 `json:"copies"`
		Printer    string              `json:"printer"`
		Mode       string              `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	job, err := buildBatchJob(req)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	result, err := job.Execute()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]any{
			"error":  err.Error(),
			"result": result,
		})
		return
	}
	jsonResponse(w, http.StatusOK, result)
}

// handleBatchPreview returns the generated TSPL2 without printing.
func handleBatchPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req struct {
		TemplateID string              `json:"template_id"`
		PresetID   string              `json:"preset_id"`
		Layout     string              `json:"layout"`
		Rows       []map[string]string `json:"rows"`
		Copies     int                 `json:"copies"`
		Printer    string              `json:"printer"`
		Mode       string              `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	job, err := buildBatchJob(req)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	tspl, err := job.GenerateTSPL()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"tspl":  tspl,
		"rows":  len(req.Rows),
		"bytes": len(tspl),
		"mode":  job.Mode,
	})
}

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

	outputDir := filepath.Join(configDir(), "output")
	os.MkdirAll(outputDir, 0755)
	outputPath := filepath.Join(outputDir, fmt.Sprintf("batch_%d.pdf", time.Now().UnixMilli()))

	if err := RenderBulkPDF(schema, mappedRows, outputPath); err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "render PDF: " + err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="batch_%d.pdf"`, len(mappedRows)))
	http.ServeFile(w, r, outputPath)
}
