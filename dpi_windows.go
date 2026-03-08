//go:build windows

package main

import (
	"log"
	"os/exec"
	"strconv"
	"strings"
)

// queryDriverDPI queries the Windows print driver for a printer's DPI via PowerShell WMI.
func queryDriverDPI(printerName string) (int, bool) {
	// Use WMI to query the printer's print capabilities
	psCmd := `Get-CimInstance -ClassName Win32_Printer -Filter "Name='` + printerName + `'" | Select-Object -ExpandProperty HorizontalResolution`

	cmd := exec.Command("powershell", "-NoProfile", "-Command", psCmd)
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		log.Printf("[dpi] PowerShell WMI query for %s failed: %v", printerName, err)
		return 0, false
	}

	dpiStr := strings.TrimSpace(string(out))
	if dpiStr == "" {
		return 0, false
	}

	dpi, err := strconv.Atoi(dpiStr)
	if err != nil || dpi <= 0 {
		return 0, false
	}

	log.Printf("[dpi] Windows driver reports %s DPI=%d", printerName, dpi)
	return dpi, true
}
