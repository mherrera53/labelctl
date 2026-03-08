//go:build darwin

package main

import (
	"log"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var reLPResolution = regexp.MustCompile(`(?i)resolution.*?(\d{3})`)

// queryDriverDPI queries the CUPS driver for a printer's DPI on macOS.
// Uses lpoptions -p <name> -l to list available options and find Resolution.
func queryDriverDPI(printerName string) (int, bool) {
	// Normalize printer name for CUPS (spaces become dashes)
	cupsName := strings.ReplaceAll(printerName, " ", "-")

	out, err := exec.Command("lpoptions", "-p", cupsName, "-l").Output()
	if err != nil {
		log.Printf("[dpi] lpoptions for %s failed: %v", cupsName, err)
		return 0, false
	}

	response := string(out)

	// Look for Resolution option line, e.g.:
	//   Resolution/Resolution: *203dpi 300dpi
	//   Resolution/Output Resolution: 203x203dpi *300x300dpi
	for _, line := range strings.Split(response, "\n") {
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "resolution") {
			continue
		}

		// Find the default value (marked with *)
		parts := strings.SplitN(line, ":", 2)
		if len(parts) < 2 {
			continue
		}
		options := strings.Fields(parts[1])
		for _, opt := range options {
			if strings.HasPrefix(opt, "*") {
				// Extract numeric DPI from the default option, e.g. "*203dpi" or "*300x300dpi"
				numStr := strings.TrimPrefix(opt, "*")
				numStr = strings.Split(numStr, "x")[0]
				numStr = strings.Split(numStr, "d")[0]
				numStr = strings.Split(numStr, "D")[0]
				if dpi, err := strconv.Atoi(numStr); err == nil && dpi > 0 {
					log.Printf("[dpi] CUPS driver reports %s DPI=%d", cupsName, dpi)
					return dpi, true
				}
			}
		}

		// Fallback: try regex on the whole line
		if m := reLPResolution.FindStringSubmatch(line); len(m) > 1 {
			if dpi, err := strconv.Atoi(m[1]); err == nil && dpi > 0 {
				log.Printf("[dpi] CUPS driver (regex) reports %s DPI=%d", cupsName, dpi)
				return dpi, true
			}
		}
	}

	return 0, false
}
