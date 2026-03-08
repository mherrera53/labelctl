//go:build !windows && crossbuild

package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// C_usb_device_exists always returns false in crossbuild (no libusb).
func C_usb_device_exists() bool { return false }

// listLocalPrinters lists CUPS printers via lpstat (no USB detection).
func listLocalPrinters() ([]PrinterInfo, error) {
	var printers []PrinterInfo

	// Parse CUPS printer status
	statusMap := make(map[string]string)
	if out, err := exec.Command("lpstat", "-p").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "printer ") && !strings.HasPrefix(line, "la impresora ") && !strings.HasPrefix(line, "impresora ") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			var name string
			if strings.HasPrefix(line, "la impresora ") || strings.HasPrefix(line, "impresora ") {
				for _, f := range fields {
					if f != "la" && f != "impresora" {
						name = f
						break
					}
				}
			} else {
				name = fields[1]
			}
			if name == "" {
				continue
			}
			lower := strings.ToLower(line)
			if strings.Contains(lower, "desactivad") || strings.Contains(lower, "disabled") {
				statusMap[name] = "disabled"
			} else if strings.Contains(lower, "imprimiendo") || strings.Contains(lower, "printing") {
				statusMap[name] = "printing"
			} else {
				statusMap[name] = "idle"
			}
		}
	}

	out, err := exec.Command("lpstat", "-a").Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			name := strings.Fields(line)[0]
			status := statusMap[name]
			online := status != "disabled"
			printers = append(printers, PrinterInfo{
				Name:   name,
				Type:   "cups",
				Online: online,
				Status: status,
			})
		}
	}

	return printers, nil
}

// rawPrint sends data via CUPS lp command (no USB direct in crossbuild).
func rawPrint(printerName string, data []byte) error {
	if strings.HasPrefix(printerName, "(simulated)") {
		fmt.Printf("[simulate] Would print %d bytes to %s\n", len(data), printerName)
		return nil
	}

	cupsName := strings.TrimSuffix(printerName, "-USB")
	cmd := exec.Command("lp", "-d", cupsName, "-o", "raw")
	cmd.Stdin = strings.NewReader(string(data))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("lp -d %s failed: %v — %s", cupsName, err, string(output))
	}
	fmt.Printf("[print-cups] Sent %d bytes via lp to %s\n", len(data), cupsName)
	return nil
}
