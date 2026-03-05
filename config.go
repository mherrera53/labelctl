package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

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

var (
	appConfig   AppConfig
	configMu    sync.RWMutex
	configPath  string
)

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

// configDir returns the platform-specific config directory.
func configDir() string {
	if runtime.GOOS == "windows" {
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			appdata = os.Getenv("LOCALAPPDATA")
		}
		return filepath.Join(appdata, "tsc-bridge")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tsc-bridge")
}

// initConfig loads or creates the config file.
func initConfig() {
	configPath = filepath.Join(configDir(), "config.json")
	appConfig = defaultConfig()

	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Printf("[config] No config file found, using defaults (%s)", configPath)
		return
	}

	if err := json.Unmarshal(data, &appConfig); err != nil {
		log.Printf("[config] Error parsing config: %v, using defaults", err)
		appConfig = defaultConfig()
		return
	}

	if appConfig.CustomPresets == nil {
		appConfig.CustomPresets = []LabelPreset{}
	}
	if appConfig.ManualPrinters == nil {
		appConfig.ManualPrinters = []string{}
	}

	// Decrypt secrets (supports plaintext migration — unencrypted values pass through)
	if appConfig.ApiKey != "" {
		if dec, err := decryptString(appConfig.ApiKey); err == nil {
			appConfig.ApiKey = dec
		} else {
			log.Printf("[config] Warning: could not decrypt api_key: %v", err)
		}
	}
	if appConfig.ApiSecret != "" {
		if dec, err := decryptString(appConfig.ApiSecret); err == nil {
			appConfig.ApiSecret = dec
		} else {
			log.Printf("[config] Warning: could not decrypt api_secret: %v", err)
		}
	}
	if appConfig.ApiToken != "" {
		if dec, err := decryptString(appConfig.ApiToken); err == nil {
			appConfig.ApiToken = dec
		} else {
			log.Printf("[config] Warning: could not decrypt api_token: %v", err)
		}
	}

	// Re-encrypt if loaded as plaintext (auto-migration)
	needsSave := false
	if appConfig.ApiKey != "" && !isEncrypted(appConfig.ApiKey) {
		needsSave = true
	}
	if appConfig.ApiSecret != "" && !isEncrypted(appConfig.ApiSecret) {
		needsSave = true
	}

	log.Printf("[config] Loaded from %s (printer=%s, preset=%s, api=%v)", configPath, appConfig.DefaultPrinter, appConfig.DefaultPreset, appConfig.ApiURL != "")

	if needsSave {
		log.Printf("[config] Migrating plaintext secrets to encrypted — re-saving config")
		saveConfig()
	}
}

// saveConfig persists the current config to disk with secrets encrypted.
func saveConfig() error {
	configMu.RLock()
	// Copy config and encrypt sensitive fields before serialization
	cfgCopy := appConfig
	configMu.RUnlock()

	if cfgCopy.ApiKey != "" {
		if enc, err := encryptString(cfgCopy.ApiKey); err == nil {
			cfgCopy.ApiKey = enc
		}
	}
	if cfgCopy.ApiSecret != "" {
		if enc, err := encryptString(cfgCopy.ApiSecret); err == nil {
			cfgCopy.ApiSecret = enc
		}
	}
	if cfgCopy.ApiToken != "" {
		if enc, err := encryptString(cfgCopy.ApiToken); err == nil {
			cfgCopy.ApiToken = enc
		}
	}

	data, err := json.MarshalIndent(cfgCopy, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0644)
}

// getConfig returns a copy of the current config.
func getConfig() AppConfig {
	configMu.RLock()
	defer configMu.RUnlock()
	c := appConfig
	c.CustomPresets = make([]LabelPreset, len(appConfig.CustomPresets))
	copy(c.CustomPresets, appConfig.CustomPresets)
	return c
}

// safeConfigForClient returns config without sensitive API fields.
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

// --- HTTP handlers ---

func handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonResponse(w, http.StatusOK, safeConfigForClient())

	case http.MethodPut:
		var incoming AppConfig
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		configMu.Lock()
		if incoming.Port > 0 {
			appConfig.Port = incoming.Port
		}
		appConfig.DefaultPrinter = incoming.DefaultPrinter
		if incoming.DefaultPreset != "" {
			appConfig.DefaultPreset = incoming.DefaultPreset
		}
		appConfig.AutoStart = incoming.AutoStart
		appConfig.NetworkScanEnabled = incoming.NetworkScanEnabled
		if incoming.NetworkScanInterval > 0 {
			appConfig.NetworkScanInterval = incoming.NetworkScanInterval
		}
		if incoming.ManualPrinters != nil {
			appConfig.ManualPrinters = incoming.ManualPrinters
		}
		appConfig.ShareEnabled = incoming.ShareEnabled
		if incoming.SharePort > 0 {
			appConfig.SharePort = incoming.SharePort
		}
		appConfig.SharePrinter = incoming.SharePrinter
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
		configMu.Unlock()

		if err := saveConfig(); err != nil {
			jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "save failed: " + err.Error()})
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "saved"})

	default:
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func handleWhitelabel(w http.ResponseWriter, r *http.Request) {
	cfg := getConfig()
	wl := cfg.Whitelabel
	if wl.Name == "" {
		wl.Name = "TSC Bridge"
	}
	jsonResponse(w, http.StatusOK, wl)
}

func handlePresets(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := getConfig()
		jsonResponse(w, http.StatusOK, map[string]any{
			"presets": getAllPresets(cfg.CustomPresets),
		})

	case http.MethodPost:
		var preset LabelPreset
		if err := json.NewDecoder(r.Body).Decode(&preset); err != nil {
			jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		if preset.ID == "" || preset.Name == "" {
			jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "id and name are required"})
			return
		}
		// Prevent overwriting built-in presets
		for _, bp := range builtinPresets {
			if bp.ID == preset.ID {
				jsonResponse(w, http.StatusConflict, map[string]string{"error": "cannot overwrite built-in preset"})
				return
			}
		}
		preset.Builtin = false
		configMu.Lock()
		// Replace if exists, otherwise append
		found := false
		for i, cp := range appConfig.CustomPresets {
			if cp.ID == preset.ID {
				appConfig.CustomPresets[i] = preset
				found = true
				break
			}
		}
		if !found {
			appConfig.CustomPresets = append(appConfig.CustomPresets, preset)
		}
		configMu.Unlock()
		if err := saveConfig(); err != nil {
			jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "save failed: " + err.Error()})
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "saved", "id": preset.ID})

	case http.MethodDelete:
		// Extract preset ID from path: /presets/my-preset-id
		path := strings.TrimPrefix(r.URL.Path, "/presets/")
		if path == "" || path == r.URL.Path {
			jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "preset id required in path"})
			return
		}
		// Prevent deleting built-in presets
		for _, bp := range builtinPresets {
			if bp.ID == path {
				jsonResponse(w, http.StatusForbidden, map[string]string{"error": "cannot delete built-in preset"})
				return
			}
		}
		configMu.Lock()
		filtered := appConfig.CustomPresets[:0]
		deleted := false
		for _, cp := range appConfig.CustomPresets {
			if cp.ID == path {
				deleted = true
				continue
			}
			filtered = append(filtered, cp)
		}
		appConfig.CustomPresets = filtered
		configMu.Unlock()
		if !deleted {
			jsonResponse(w, http.StatusNotFound, map[string]string{"error": "preset not found"})
			return
		}
		if err := saveConfig(); err != nil {
			jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "save failed: " + err.Error()})
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "deleted"})

	default:
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}
