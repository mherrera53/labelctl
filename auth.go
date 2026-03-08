package main

import (
	"encoding/json"
	"log"
	"net/http"
)

type AuthState struct {
	Configured     bool   `json:"configured"`
	Connected      bool   `json:"connected"`
	ApiURL         string `json:"api_url"`
	WhitelabelName string `json:"whitelabel_name"`
	WhitelabelID   int    `json:"whitelabel_id"`
	LogoURL        string `json:"logo_url,omitempty"`
	Error          string `json:"error,omitempty"`
}

func IsAuthConfigured() bool {
	cfg := getConfig()
	if cfg.ApiURL == "" {
		return false
	}
	return (cfg.ApiKey != "" && cfg.ApiSecret != "") || cfg.ApiToken != ""
}

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

// handleAuthState — GET /auth/state
func handleAuthState(w http.ResponseWriter, r *http.Request) {
	state := GetAuthState()
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

// handleAuthLogin — POST /auth/login { api_url, api_key, api_secret, wl_id? }
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

	// Test connection with temporary config
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

	// Fetch brand info (name, logo, colors) from backend
	brand, err := client.FetchBrandInfo()
	if err != nil {
		log.Printf("[auth] brand fetch failed (non-fatal): %v", err)
	} else if brand != nil {
		configMu.Lock()
		name := brand.BrandName
		if name == "" {
			name = brand.WLName
		}
		logo := brand.BrandLogo
		if logo == "" {
			logo = brand.WLLogo
		}
		if name != "" {
			appConfig.Whitelabel.Name = name
		}
		if logo != "" {
			appConfig.Whitelabel.LogoURL = logo
		}
		if brand.PrimaryColor != "" {
			appConfig.Whitelabel.PrimaryColor = brand.PrimaryColor
		}
		if brand.SecondaryColor != "" {
			appConfig.Whitelabel.AccentColor = brand.SecondaryColor
		}
		configMu.Unlock()
		log.Printf("[auth] brand detected: %s (logo=%s)", name, logo)
	}

	if err := saveConfig(); err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
		return
	}

	log.Printf("[auth] login successful for %s (wl=%d)", req.ApiURL, req.WlID)
	jsonResponse(w, http.StatusOK, map[string]string{"status": "connected"})
}

// handleAuthLogout — POST /auth/logout
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
