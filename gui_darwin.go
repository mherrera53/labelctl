//go:build darwin

package main

import webview "github.com/webview/webview_go"

// openGUI opens a native WebKit window with the dashboard.
func openGUI(url string) {
	setAppIcon()
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("TSC Bridge")
	w.SetSize(1060, 720, webview.HintNone)
	w.Navigate(url)
	w.Run()
}
