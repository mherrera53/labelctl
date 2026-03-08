//go:build windows

package main

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
)

// detectTSCDrivers checks for TSC drivers, USB devices, and registered printers on Windows.
func detectTSCDrivers() DriverStatus {
	status := DriverStatus{
		DownloadURL: tscDriverURLWindows,
	}

	// 1. Check installed printer drivers
	status.DriverNames = detectTSCWindowsDrivers()
	status.DriversInstalled = len(status.DriverNames) > 0

	// 2. Detect TSC USB devices via PnP
	status.USBDevices = detectTSCWindowsUSB()

	// 3. Check registered printers
	status.RegisteredPrinters = detectTSCWindowsPrinters()

	// Determine setup needs
	hasUSB := len(status.USBDevices) > 0
	hasRegistered := len(status.RegisteredPrinters) > 0
	hasDrivers := status.DriversInstalled

	if hasUSB && !hasRegistered && hasDrivers {
		status.NeedsSetup = true
		status.CanAutoInstall = true
		status.Instructions = "Driver TSC instalado y dispositivo detectado, pero no hay impresora registrada. Se puede registrar automaticamente."
	} else if hasUSB && !hasDrivers {
		status.NeedsSetup = true
		status.CanAutoInstall = false
		status.Instructions = "Dispositivo TSC detectado pero no hay driver instalado. Descargue el driver desde el sitio oficial de TSC."
	} else if !hasUSB && !hasRegistered {
		status.Instructions = "No se detectaron dispositivos TSC. Conecte la impresora e intente de nuevo."
	}

	return status
}

// detectTSCWindowsDrivers lists installed TSC printer drivers via PowerShell.
func detectTSCWindowsDrivers() []string {
	var drivers []string
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		"Get-PrinterDriver | Where-Object {$_.Name -like '*TSC*'} | Select-Object -ExpandProperty Name")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return drivers
	}
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name != "" {
			drivers = append(drivers, name)
		}
	}
	return drivers
}

// detectTSCWindowsUSB detects TSC USB devices via PnP.
func detectTSCWindowsUSB() []USBDevice {
	var devices []USBDevice
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		"Get-PnpDevice -Class Printer -Status OK -ErrorAction SilentlyContinue | Where-Object {$_.FriendlyName -like '*TSC*'} | Select-Object -ExpandProperty FriendlyName")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		// Fallback: check all USB devices for vendor 1203
		cmd2 := exec.Command("powershell", "-NoProfile", "-Command",
			"Get-PnpDevice -Status OK -ErrorAction SilentlyContinue | Where-Object {$_.InstanceId -like '*VID_1203*'} | Select-Object -ExpandProperty FriendlyName")
		hideWindow(cmd2)
		out, err = cmd2.Output()
		if err != nil {
			return devices
		}
	}
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name != "" {
			devices = append(devices, USBDevice{
				VendorID: 0x1203,
				Name:     name,
			})
		}
	}
	return devices
}

// detectTSCWindowsPrinters lists registered printers that appear to be TSC.
func detectTSCWindowsPrinters() []string {
	var printers []string
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		"Get-Printer | Where-Object {$_.Name -like '*TSC*' -or $_.DriverName -like '*TSC*'} | Select-Object -ExpandProperty Name")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return printers
	}
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name != "" {
			printers = append(printers, name)
		}
	}
	return printers
}

// runDriverSetup executes a driver setup action on Windows.
func runDriverSetup(action string) (map[string]any, error) {
	switch action {
	case "register":
		return registerTSCPrinterWindows()
	case "download":
		return map[string]any{
			"status":       "redirect",
			"download_url": tscDriverURLWindows,
			"message":      "Descargue los drivers desde el sitio oficial de TSC",
		}, nil
	case "full-setup":
		return registerTSCPrinterWindows()
	default:
		return nil, fmt.Errorf("unknown action: %s", action)
	}
}

// registerTSCPrinterWindows registers a TSC printer in the Windows spooler.
func registerTSCPrinterWindows() (map[string]any, error) {
	// Find an available TSC driver
	drivers := detectTSCWindowsDrivers()
	if len(drivers) == 0 {
		return nil, fmt.Errorf("no TSC driver installed. Descargue el driver desde: %s", tscDriverURLWindows)
	}

	driverName := drivers[0]
	printerName := "TSC-TDP-244-Plus"

	// Find available USB port
	portName := findTSCUSBPort()
	if portName == "" {
		portName = "USB001" // fallback
	}

	log.Printf("[driver] Registering TSC printer: name=%s driver=%s port=%s", printerName, driverName, portName)

	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		fmt.Sprintf(`Add-Printer -Name "%s" -DriverName "%s" -PortName "%s" -ErrorAction Stop`, printerName, driverName, portName))
	hideWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("Add-Printer failed: %v — %s", err, string(output))
	}

	log.Printf("[driver] TSC printer registered successfully: %s", printerName)
	return map[string]any{
		"status":  "registered",
		"printer": printerName,
		"driver":  driverName,
		"port":    portName,
		"message": "Impresora TSC registrada correctamente.",
	}, nil
}

// findTSCUSBPort finds the USB port where a TSC printer is connected.
func findTSCUSBPort() string {
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		"Get-PrinterPort | Where-Object {$_.Name -like 'USB*'} | Select-Object -First 1 -ExpandProperty Name")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
