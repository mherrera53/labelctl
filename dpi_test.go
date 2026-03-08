package main

import (
	"testing"
)

func TestParseDPIFromTSCResponse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantDPI  int
		wantOK   bool
	}{
		{
			name:    "simple 203 DPI",
			input:   "203 DPI",
			wantDPI: 203,
			wantOK:  true,
		},
		{
			name:    "simple 300 DPI",
			input:   "300 DPI",
			wantDPI: 300,
			wantOK:  true,
		},
		{
			name:    "resolution format 203x203",
			input:   "Resolution: 203x203",
			wantDPI: 203,
			wantOK:  true,
		},
		{
			name:    "resolution format 300x300",
			input:   "Resolution: 300X300",
			wantDPI: 300,
			wantOK:  true,
		},
		{
			name:    "embedded in longer response",
			input:   "TSC TDP-244 Pro\nFirmware: V1.2\n203 DPI\nRAM: 32MB",
			wantDPI: 203,
			wantOK:  true,
		},
		{
			name:    "resolution lowercase",
			input:   "resolution: 300x300",
			wantDPI: 300,
			wantOK:  true,
		},
		{
			name:    "no DPI info",
			input:   "TSC TDP-244 Pro\nFirmware: V1.2",
			wantDPI: 0,
			wantOK:  false,
		},
		{
			name:    "empty response",
			input:   "",
			wantDPI: 0,
			wantOK:  false,
		},
		{
			name:    "DPI with extra whitespace",
			input:   "  203  DPI  ",
			wantDPI: 203,
			wantOK:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dpi, ok := parseDPIFromTSCResponse(tt.input)
			if ok != tt.wantOK {
				t.Errorf("parseDPIFromTSCResponse(%q) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
			if dpi != tt.wantDPI {
				t.Errorf("parseDPIFromTSCResponse(%q) dpi = %d, want %d", tt.input, dpi, tt.wantDPI)
			}
		})
	}
}

func TestDetectDPIChain(t *testing.T) {
	// Setup: clean config
	configMu.Lock()
	origConfig := appConfig
	appConfig = defaultConfig()
	configMu.Unlock()

	defer func() {
		configMu.Lock()
		appConfig = origConfig
		configMu.Unlock()
	}()

	t.Run("cached value returns immediately", func(t *testing.T) {
		// Pre-populate cache
		configMu.Lock()
		appConfig.PrinterDPI["CachedPrinter"] = PrinterDPIEntry{DPI: 300, Source: "manual"}
		configMu.Unlock()

		printer := PrinterInfo{Name: "CachedPrinter", Type: "usb"}
		dpi := DetectPrinterDPI(printer)
		if dpi != 300 {
			t.Errorf("expected cached DPI 300, got %d", dpi)
		}
	})

	t.Run("unknown printer returns defaultDPI", func(t *testing.T) {
		printer := PrinterInfo{Name: "UnknownPrinter-XYZ-12345", Type: "usb"}
		dpi := DetectPrinterDPI(printer)
		if dpi != defaultDPI {
			t.Errorf("expected default DPI %d, got %d", defaultDPI, dpi)
		}
	})

	t.Run("GetPrinterDPI returns cached value", func(t *testing.T) {
		configMu.Lock()
		appConfig.PrinterDPI["TestGetDPI"] = PrinterDPIEntry{DPI: 300, Source: "driver"}
		configMu.Unlock()

		dpi := GetPrinterDPI("TestGetDPI")
		if dpi != 300 {
			t.Errorf("expected 300, got %d", dpi)
		}
	})

	t.Run("GetPrinterDPI returns default for unknown", func(t *testing.T) {
		dpi := GetPrinterDPI("NonExistentPrinter-99999")
		if dpi != defaultDPI {
			t.Errorf("expected default DPI %d, got %d", defaultDPI, dpi)
		}
	})
}
