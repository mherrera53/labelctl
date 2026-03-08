package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
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

// handleAuthLoginPassword — POST /auth/login-password { api_url, wl_id, email, password }
// Authenticates via backend using email+password, generates API key, and stores credentials.
// Only store owners are allowed (staff users are rejected).
func handleAuthLoginPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		ApiURL   string `json:"api_url"`
		WlID     int    `json:"wl_id"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.ApiURL == "" || req.Email == "" || req.Password == "" {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "api_url, email, password required"})
		return
	}

	// Step 1: Authenticate via backend POST /users/authorize
	authResult, err := backendAuthorize(req.ApiURL, req.WlID, req.Email, req.Password)
	if err != nil {
		log.Printf("[auth] password login failed for %s: %v", req.Email, err)
		jsonResponse(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	// Step 2: Reject staff users — only store owners allowed
	if authResult.IsStaff {
		log.Printf("[auth] rejected staff login for %s", req.Email)
		jsonResponse(w, http.StatusForbidden, map[string]string{"error": "Solo el dueño de la tienda puede conectar el bridge. Los usuarios staff no tienen permiso."})
		return
	}

	// Step 3: Generate API key using the JWT token
	apiKey, apiSecret, err := backendGenerateApiKey(req.ApiURL, req.WlID, authResult.Token)
	if err != nil {
		log.Printf("[auth] API key generation failed for %s: %v", req.Email, err)
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "Login exitoso pero no se pudo generar API key: " + err.Error()})
		return
	}

	// Step 4: Test connection with the generated credentials
	testCfg := AppConfig{
		ApiURL:        req.ApiURL,
		ApiKey:        apiKey,
		ApiSecret:     apiSecret,
		ApiWhiteLabel: req.WlID,
	}
	client := NewApiClient(testCfg)
	if err := client.TestConnection(); err != nil {
		log.Printf("[auth] connection test failed after key generation: %v", err)
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "API key generada pero falló la conexión: " + err.Error()})
		return
	}

	// Step 5: Save credentials
	configMu.Lock()
	appConfig.ApiURL = req.ApiURL
	appConfig.ApiKey = apiKey
	appConfig.ApiSecret = apiSecret
	appConfig.ApiToken = ""
	if req.WlID > 0 {
		appConfig.ApiWhiteLabel = req.WlID
	}
	configMu.Unlock()

	// Fetch brand info
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
		log.Printf("[auth] brand detected: %s", name)
	}

	if err := saveConfig(); err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
		return
	}

	log.Printf("[auth] password login successful for %s (wl=%d)", req.Email, req.WlID)
	jsonResponse(w, http.StatusOK, map[string]string{"status": "connected"})
}

// authResult holds the result of a backend /users/authorize call.
type authResult struct {
	Token   string
	IsStaff bool
}

// backendAuthorize calls POST /users/authorize on the backend.
func backendAuthorize(apiURL string, wlID int, email, password string) (*authResult, error) {
	payload := map[string]any{
		"email":    email,
		"password": password,
		"staff":    0,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("POST", apiURL+"/users/authorize", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if wlID > 0 {
		req.Header.Set("X-Any-Wl", fmt.Sprintf("%d", wlID))
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("no se pudo contactar el servidor: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error leyendo respuesta: %w", err)
	}

	var result struct {
		Status  int    `json:"status"`
		Message string `json:"message"`
		Data    struct {
			Token string `json:"token"`
			User  struct {
				StaffID any `json:"staff_id"` // 0 or null for owners, >0 for staff
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("respuesta inválida del servidor")
	}

	if result.Status != 1 {
		msg := result.Message
		if msg == "" {
			msg = "credenciales inválidas"
		}
		return nil, fmt.Errorf("%s", msg)
	}

	if result.Data.Token == "" {
		return nil, fmt.Errorf("el servidor no devolvió un token")
	}

	// Check if staff: staff_id can be float64 (from JSON number) or string
	isStaff := false
	switch v := result.Data.User.StaffID.(type) {
	case float64:
		isStaff = v > 0
	case string:
		isStaff = v != "" && v != "0"
	}

	return &authResult{
		Token:   result.Data.Token,
		IsStaff: isStaff,
	}, nil
}

// backendGenerateApiKey calls POST /users/api/key with a JWT token.
func backendGenerateApiKey(apiURL string, wlID int, jwtToken string) (string, string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("POST", apiURL+"/users/api/key", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if wlID > 0 {
		req.Header.Set("X-Any-Wl", fmt.Sprintf("%d", wlID))
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("no se pudo generar API key: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("error leyendo respuesta: %w", err)
	}

	var result struct {
		Status int `json:"status"`
		Data   struct {
			Key    string `json:"key"`
			Secret string `json:"secret"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", "", fmt.Errorf("respuesta inválida")
	}
	if result.Status != 1 || result.Data.Key == "" {
		return "", "", fmt.Errorf("el servidor no generó API key")
	}

	return result.Data.Key, result.Data.Secret, nil
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
