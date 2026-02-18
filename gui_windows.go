//go:build windows

package main

import (
	"github.com/jchv/go-webview2"
)

// openGUI opens a native Edge WebView2 window with the dashboard.
func openGUI(url string) {
	setAppIcon()
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  "TSC Bridge",
			Width:  1060,
			Height: 720,
		},
	})
	if w == nil {
		// WebView2 not available — fall back to default browser
		openBrowser(url)
		return
	}
	defer w.Destroy()
	w.Navigate(url)
	w.Run()
}
