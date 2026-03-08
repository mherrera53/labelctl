package main

import "testing"

func TestIsAuthConfigured(t *testing.T) {
	configMu.Lock()
	appConfig.ApiURL = ""
	appConfig.ApiKey = ""
	appConfig.ApiSecret = ""
	appConfig.ApiToken = ""
	configMu.Unlock()

	if IsAuthConfigured() {
		t.Error("should be false with no credentials")
	}

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
	if !state.Configured {
		t.Error("configured should be true")
	}
	if state.WhitelabelName != "TestCo" {
		t.Errorf("wl name = %q, want TestCo", state.WhitelabelName)
	}
}
