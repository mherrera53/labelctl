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

// AppConfig is the persisted configuration for tsc-bridge.
type AppConfig struct {
	Port                int           `json:"port"`
	DefaultPrinter      string        `json:"default_printer"`
	DefaultPreset       string        `json:"default_preset"`
	CustomPresets       []LabelPreset `json:"custom_presets"`
	AutoStart           bool          `json:"auto_start"`
	NetworkScanEnabled  bool          `json:"network_scan_enabled"`
	NetworkScanInterval int           `json:"network_scan_interval"`
	ManualPrinters      []string      `json:"manual_printers"`
	ShareEnabled        bool          `json:"share_enabled"`
	SharePort           int           `json:"share_port"`
	SharePrinter        string        `json:"share_printer"`
}

var (
	appConfig   AppConfig
	configMu    sync.RWMutex
	configPath  string
)

func defaultConfig() AppConfig {
	return AppConfig{
		Port:                9271,
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

	log.Printf("[config] Loaded from %s (printer=%s, preset=%s)", configPath, appConfig.DefaultPrinter, appConfig.DefaultPreset)
}

// saveConfig persists the current config to disk.
func saveConfig() error {
	configMu.RLock()
	data, err := json.MarshalIndent(appConfig, "", "  ")
	configMu.RUnlock()
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
	// Deep copy custom presets
	c.CustomPresets = make([]LabelPreset, len(appConfig.CustomPresets))
	copy(c.CustomPresets, appConfig.CustomPresets)
	return c
}

// --- HTTP handlers ---

func handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonResponse(w, http.StatusOK, getConfig())

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
