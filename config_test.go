package main

import (
	"encoding/json"
	"testing"
)

func TestPrinterDPIConfigRoundTrip(t *testing.T) {
	cfg := defaultConfig()
	cfg.PrinterDPI["TSC-TDP-244"] = PrinterDPIEntry{DPI: 203, Source: "driver"}
	cfg.PrinterDPI["TSC-TE310"] = PrinterDPIEntry{DPI: 300, Source: "tspl_probe"}

	// Marshal to JSON
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Unmarshal back
	var decoded AppConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Verify round-trip
	if len(decoded.PrinterDPI) != 2 {
		t.Fatalf("expected 2 PrinterDPI entries, got %d", len(decoded.PrinterDPI))
	}

	entry, ok := decoded.PrinterDPI["TSC-TDP-244"]
	if !ok {
		t.Fatal("missing TSC-TDP-244 entry")
	}
	if entry.DPI != 203 {
		t.Errorf("expected DPI 203, got %d", entry.DPI)
	}
	if entry.Source != "driver" {
		t.Errorf("expected source 'driver', got %q", entry.Source)
	}

	entry2, ok := decoded.PrinterDPI["TSC-TE310"]
	if !ok {
		t.Fatal("missing TSC-TE310 entry")
	}
	if entry2.DPI != 300 {
		t.Errorf("expected DPI 300, got %d", entry2.DPI)
	}
	if entry2.Source != "tspl_probe" {
		t.Errorf("expected source 'tspl_probe', got %q", entry2.Source)
	}
}

func TestSafeConfigHidesDPISource(t *testing.T) {
	// Set up global config with DPI entries that have sources
	configMu.Lock()
	appConfig = defaultConfig()
	appConfig.PrinterDPI["TestPrinter"] = PrinterDPIEntry{DPI: 300, Source: "manual"}
	appConfig.PrinterDPI["TestPrinter2"] = PrinterDPIEntry{DPI: 203, Source: "driver"}
	configMu.Unlock()

	safe := safeConfigForClient()

	// printer_dpi should be a flat map[string]int (no source field)
	dpiRaw, ok := safe["printer_dpi"]
	if !ok {
		t.Fatal("printer_dpi not found in safe config")
	}

	dpiMap, ok := dpiRaw.(map[string]int)
	if !ok {
		t.Fatalf("printer_dpi is not map[string]int, got %T", dpiRaw)
	}

	if dpiMap["TestPrinter"] != 300 {
		t.Errorf("expected TestPrinter DPI 300, got %d", dpiMap["TestPrinter"])
	}
	if dpiMap["TestPrinter2"] != 203 {
		t.Errorf("expected TestPrinter2 DPI 203, got %d", dpiMap["TestPrinter2"])
	}

	// Verify source is not exposed — marshal to JSON and check
	data, err := json.Marshal(dpiMap)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	jsonStr := string(data)
	if contains(jsonStr, "source") {
		t.Errorf("safe config should not expose 'source', got: %s", jsonStr)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
