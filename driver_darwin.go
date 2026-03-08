//go:build darwin

package main

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
)

// detectTSCDrivers checks for TSC drivers, USB devices, and registered printers on macOS.
func detectTSCDrivers() DriverStatus {
	status := DriverStatus{
		DownloadURL: tscDriverURLMac,
	}

	// 1. Check for TSC PPDs (driver files)
	ppds := detectTSCPPDs()
	if len(ppds) > 0 {
		status.DriversInstalled = true
		status.DriverNames = ppds
	}

	// 2. Enumerate USB devices with TSC vendor ID (0x1203)
	status.USBDevices = enumerateTSCUSB()

	// 3. Check registered CUPS printers
	status.RegisteredPrinters = detectTSCCUPSPrinters()

	// Determine if setup is needed
	hasUSB := len(status.USBDevices) > 0
	hasRegistered := len(status.RegisteredPrinters) > 0

	if hasUSB && !hasRegistered {
		status.NeedsSetup = true
		status.CanAutoInstall = true
		status.Instructions = "Impresora TSC detectada por USB pero no registrada en el sistema. Puede registrarse automaticamente en modo RAW (sin driver necesario)."
	} else if !hasUSB && !hasRegistered {
		status.NeedsSetup = false
		status.Instructions = "No se detectaron impresoras TSC por USB. Conecte la impresora e intente de nuevo."
	}

	return status
}

// detectTSCPPDs checks for TSC PPD files and available CUPS drivers.
func detectTSCPPDs() []string {
	var drivers []string

	// Check lpinfo for available TSC PPDs
	out, err := exec.Command("lpinfo", "-m").Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			lower := strings.ToLower(line)
			if strings.Contains(lower, "tsc") {
				fields := strings.Fields(line)
				if len(fields) > 0 {
					drivers = append(drivers, strings.TrimSpace(line))
				}
			}
		}
	}

	return drivers
}

// enumerateTSCUSB checks if any TSC USB device (vendor 0x1203) is connected.
// Uses the existing libusb C code for the known PID, plus system_profiler for discovery.
func enumerateTSCUSB() []USBDevice {
	var devices []USBDevice

	// Quick check with known PID via existing C code
	if usbDeviceExists() {
		devices = append(devices, USBDevice{
			VendorID:  0x1203,
			ProductID: 0x0133,
			Name:      "TSC TDP-244 Plus",
		})
	}

	// Also check system_profiler for other TSC models
	out, err := exec.Command("system_profiler", "SPUSBDataType", "-detailLevel", "mini").Output()
	if err == nil {
		lines := strings.Split(string(out), "\n")
		for i, line := range lines {
			lower := strings.ToLower(line)
			if strings.Contains(lower, "tsc") || strings.Contains(lower, "vendor id: 0x1203") {
				// Try to extract a meaningful name
				name := strings.TrimSpace(line)
				if strings.Contains(lower, "vendor id") {
					// Look backwards for the device name
					for j := i - 1; j >= 0 && j >= i-5; j-- {
						trimmed := strings.TrimSpace(lines[j])
						if trimmed != "" && !strings.Contains(trimmed, ":") {
							name = trimmed
							break
						}
					}
				}
				// Avoid duplicating the known device
				isDup := false
				for _, d := range devices {
					if d.Name == name || (d.VendorID == 0x1203 && d.ProductID == 0x0133) {
						isDup = true
						break
					}
				}
				if !isDup && name != "" {
					devices = append(devices, USBDevice{
						VendorID: 0x1203,
						Name:     name,
					})
				}
			}
		}
	}

	return devices
}

// usbDeviceExists wraps the existing C call to check for the known TSC device.
func usbDeviceExists() bool {
	// Use the C function from printer_other.go
	return C_usb_device_exists()
}

// detectTSCCUPSPrinters lists CUPS printers that appear to be TSC.
func detectTSCCUPSPrinters() []string {
	var printers []string
	out, err := exec.Command("lpstat", "-a").Output()
	if err != nil {
		return printers
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		name := fields[0]
		lower := strings.ToLower(name)
		for _, kw := range []string{"tsc", "tdp", "ttp", "te2", "te3"} {
			if strings.Contains(lower, kw) {
				printers = append(printers, name)
				break
			}
		}
	}
	return printers
}

// runDriverSetup executes a driver setup action on macOS.
func runDriverSetup(action string) (map[string]any, error) {
	switch action {
	case "register":
		return registerTSCPrinterCUPS()
	case "download":
		return map[string]any{
			"status":      "redirect",
			"download_url": tscDriverURLMac,
			"message":     "Descargue los drivers desde el sitio oficial de TSC",
		}, nil
	case "full-setup":
		// Register in RAW mode (no driver needed for TSPL)
		return registerTSCPrinterCUPS()
	default:
		return nil, fmt.Errorf("unknown action: %s", action)
	}
}

// registerTSCPrinterCUPS registers a TSC USB printer in CUPS using raw mode.
// This does NOT require a driver — CUPS sends data as-is to the printer.
func registerTSCPrinterCUPS() (map[string]any, error) {
	// Discover USB URI
	uri := discoverTSCURI()
	if uri == "" {
		return nil, fmt.Errorf("no se encontro URI de impresora TSC. Verifique la conexion USB")
	}

	printerName := "TSC-TDP-244-Plus"
	log.Printf("[driver] Registering TSC printer: name=%s uri=%s", printerName, uri)

	// Register with CUPS in raw mode (no PPD needed)
	// -E enables the printer and accepts jobs
	// -m raw means no filter — data is sent as-is (perfect for TSPL)
	cmd := exec.Command("lpadmin", "-p", printerName, "-E", "-v", uri, "-m", "raw")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Try "everywhere" model as fallback
		cmd2 := exec.Command("lpadmin", "-p", printerName, "-E", "-v", uri, "-m", "everywhere")
		output2, err2 := cmd2.CombinedOutput()
		if err2 != nil {
			return nil, fmt.Errorf("lpadmin failed: %v — %s / fallback: %v — %s", err, string(output), err2, string(output2))
		}
	}

	// Enable the printer
	exec.Command("cupsenable", printerName).Run()
	exec.Command("cupsaccept", printerName).Run()

	log.Printf("[driver] TSC printer registered successfully: %s", printerName)
	return map[string]any{
		"status":  "registered",
		"printer": printerName,
		"uri":     uri,
		"mode":    "raw",
		"message": "Impresora TSC registrada en modo RAW. Lista para imprimir TSPL.",
	}, nil
}

// discoverTSCURI finds the USB URI for a connected TSC printer.
func discoverTSCURI() string {
	out, err := exec.Command("lpinfo", "-v").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "tsc") && strings.Contains(lower, "usb://") {
			fields := strings.Fields(line)
			for _, f := range fields {
				if strings.HasPrefix(f, "usb://") {
					return f
				}
			}
		}
	}
	// Fallback: look for vendor 1203
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "usb://") && strings.Contains(line, "1203") {
			fields := strings.Fields(line)
			for _, f := range fields {
				if strings.HasPrefix(f, "usb://") {
					return f
				}
			}
		}
	}
	return ""
}
