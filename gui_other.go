//go:build !darwin && !windows

package main

// openGUI falls back to opening the URL in the default browser on Linux/other.
func openGUI(url string) {
	openBrowser(url)
}
