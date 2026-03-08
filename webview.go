package main

import (
	"fmt"
	"log"
	"sync"
)

var (
	webviewActive bool
	webviewMu     sync.Mutex
	dashboardURL  string
)

// initWebview prepares the webview subsystem.
// For now, uses browser fallback. Will be upgraded to native webview
// when the build toolchain supports github.com/webview/webview with CGO.
func initWebview(dashURL string) {
	webviewMu.Lock()
	defer webviewMu.Unlock()
	dashboardURL = dashURL
	log.Printf("[webview] initialized with URL: %s", dashURL)
}

// showDashboard opens the dashboard in the native window (or browser fallback).
// This is the primary entry point that tray.go should call instead of
// openBrowser() directly, allowing a future swap to a native webview.
func showDashboard(dashURL string) {
	webviewMu.Lock()
	active := webviewActive
	webviewMu.Unlock()

	if dashURL == "" {
		dashURL = dashboardURL
	}

	if !active {
		log.Printf("[webview] native webview not available, opening in browser: %s", dashURL)
		openBrowser(dashURL)
		return
	}

	// When native webview is available, navigate the existing window
	// instead of opening a new browser tab.
	log.Printf("[webview] navigating native window to: %s", dashURL)
	openBrowser(dashURL)
}

// setWebviewTitle updates the window title with whitelabel branding.
// Currently logs the computed title; will call w.SetTitle() once
// a native webview backend is integrated.
func setWebviewTitle() {
	cfg := getConfig()
	title := "TSC Bridge"
	if cfg.Whitelabel.Name != "" {
		title = fmt.Sprintf("TSC Bridge — %s", cfg.Whitelabel.Name)
	}
	log.Printf("[webview] title set: %s", title)
	// When native webview is available: w.SetTitle(title)
}

// isWebviewActive reports whether a native webview window is currently open.
func isWebviewActive() bool {
	webviewMu.Lock()
	defer webviewMu.Unlock()
	return webviewActive
}

// destroyWebview tears down the native webview window if one is active.
// Safe to call even when no native window exists.
func destroyWebview() {
	webviewMu.Lock()
	defer webviewMu.Unlock()
	if webviewActive {
		log.Printf("[webview] destroying native window")
		webviewActive = false
		// When native webview is available: w.Destroy()
	}
}
