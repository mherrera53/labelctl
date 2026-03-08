package main

import (
	"log"
	"os/exec"
	"runtime"
)

// openBrowser opens the given URL in the system's default browser.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// Use rundll32 instead of "cmd /c start" to avoid flashing a console window
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if runtime.GOOS == "windows" {
		hideWindowCmd(cmd)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("[browser] Failed to open %s: %v", url, err)
		return
	}
	go cmd.Wait()
}
