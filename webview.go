//go:build !darwin && !crossbuild

package main

import (
	"fmt"
	"log"
	"sync"

	webview "github.com/webview/webview_go"
)

var (
	wv             webview.WebView
	wvMu           sync.Mutex
	wvDashboardURL string
)

// initWebview creates the native webview window.
// On Windows/Linux, uses webview_go library.
func initWebview(dashURL string) {
	wvMu.Lock()
	defer wvMu.Unlock()
	wvDashboardURL = dashURL

	w := webview.New(false)
	if w == nil {
		log.Printf("[webview] failed to create native window — browser fallback active")
		return
	}

	cfg := getConfig()
	title := "TSC Bridge"
	if cfg.Whitelabel.Name != "" {
		title = fmt.Sprintf("TSC Bridge — %s", cfg.Whitelabel.Name)
	}

	w.SetTitle(title)
	w.SetSize(1100, 750, webview.HintNone)
	w.Navigate(dashURL)

	wv = w
	log.Printf("[webview] native window created: %s", dashURL)
}

// showDashboard opens or refocuses the native webview window.
func showDashboard(dashURL string) {
	wvMu.Lock()
	w := wv
	wvMu.Unlock()

	if dashURL == "" {
		dashURL = wvDashboardURL
	}

	if w == nil {
		log.Printf("[webview] no native window — opening browser: %s", dashURL)
		openBrowser(dashURL)
		return
	}

	w.Dispatch(func() {
		w.Navigate(dashURL)
	})
}

// setWebviewTitle updates the window title with whitelabel branding.
func setWebviewTitle() {
	wvMu.Lock()
	w := wv
	wvMu.Unlock()

	cfg := getConfig()
	title := "TSC Bridge"
	if cfg.Whitelabel.Name != "" {
		title = fmt.Sprintf("TSC Bridge — %s", cfg.Whitelabel.Name)
	}

	if w != nil {
		w.Dispatch(func() {
			w.SetTitle(title)
		})
	}
}

// isWebviewActive reports whether a native webview window exists.
func isWebviewActive() bool {
	wvMu.Lock()
	defer wvMu.Unlock()
	return wv != nil
}

// destroyWebview tears down the native webview window.
func destroyWebview() {
	wvMu.Lock()
	defer wvMu.Unlock()
	if wv != nil {
		log.Printf("[webview] destroying native window")
		wv.Destroy()
		wv = nil
	}
}
