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
		OS             string `json:"os"`
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

	binaryPath = filepath.Join(distDir, binaryName)
	if _, err := os.Stat(binaryPath); err != nil {
		exe, _ := os.Executable()
		binaryPath = exe
		binaryName = filepath.Base(exe)
	}

	if _, err := os.Stat(binaryPath); err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "binary not found: " + binaryName})
		return
	}

	// Generate config with encrypted secrets
	encKey, _ := encryptString(req.ApiKey)
	encSecret, _ := encryptString(req.ApiSecret)

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
		ApiKey:              encKey,
		ApiSecret:           encSecret,
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
