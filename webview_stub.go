//go:build !darwin && crossbuild

package main

import (
	"log"
	"sync"
)

var (
	wvMu           sync.Mutex
	wvDashboardURL string
)

// initWebview stores the dashboard URL (browser-only fallback for cross-compiled builds).
func initWebview(dashURL string) {
	wvMu.Lock()
	defer wvMu.Unlock()
	wvDashboardURL = dashURL
	log.Printf("[webview] cross-build mode — browser fallback active")
}

// showDashboard opens the dashboard in the system browser.
func showDashboard(dashURL string) {
	wvMu.Lock()
	url := dashURL
	if url == "" {
		url = wvDashboardURL
	}
	wvMu.Unlock()

	if url == "" {
		return
	}
	log.Printf("[webview] opening browser: %s", url)
	openBrowser(url)
}

// setWebviewTitle is a no-op in browser mode.
func setWebviewTitle() {}

// isWebviewActive always returns false in browser mode.
func isWebviewActive() bool { return false }

// destroyWebview is a no-op in browser mode.
func destroyWebview() {}
