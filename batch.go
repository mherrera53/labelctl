package main

import (
	"fmt"
	"log"
	"strings"
)

// BatchJob describes a batch print operation.
type BatchJob struct {
	// For local template mode
	Template *LabelTemplate
	Preset   *LabelPreset

	// For backend API mode
	BackendTemplateID string // UUID of backend template
	Layout            string // "single" or "matrix_3x1"
	PresetName        string // preset name hint for backend

	// Common
	Rows    []map[string]string
	Copies  int
	Printer string
	Mode    string // "local" or "backend"
}

// BatchResult summarizes a completed batch print.
type BatchResult struct {
	TotalRows int      `json:"total_rows"`
	Printed   int      `json:"printed"`
	Errors    []string `json:"errors"`
	Mode      string   `json:"mode"`
	Bytes     int      `json:"bytes"`
}

// GenerateTSPL produces the complete TSPL2 command string for the batch.
// If mode is "backend", calls the API for each row. Otherwise uses local templates.
func (j *BatchJob) GenerateTSPL() (string, error) {
	var tspl string
	var err error
	if j.Mode == "backend" {
		tspl, err = j.generateViaBackend()
	} else {
		tspl, err = j.generateLocal(), nil
	}
	if err != nil {
		return "", err
	}
	return sanitizeTSPL(tspl), nil
}

// sanitizeTSPL cleans TSPL data to ensure the printer interprets it correctly:
// - Strips UTF-8 BOM
// - Normalizes line endings to \r\n
// - Strips leading whitespace before first command
func sanitizeTSPL(tspl string) string {
	// Strip UTF-8 BOM
	tspl = strings.TrimPrefix(tspl, "\xEF\xBB\xBF")
	// Strip leading whitespace/newlines
	tspl = strings.TrimLeft(tspl, " \t\n\r")
	// Normalize line endings: first remove \r, then replace \n with \r\n
	tspl = strings.ReplaceAll(tspl, "\r\n", "\n")
	tspl = strings.ReplaceAll(tspl, "\r", "\n")
	tspl = strings.ReplaceAll(tspl, "\n", "\r\n")
	// Ensure ends with \r\n
	if !strings.HasSuffix(tspl, "\r\n") {
		tspl += "\r\n"
	}
	return tspl
}

// generateViaBackend calls POST /pdfs/generate-tspl-commands for each row.
func (j *BatchJob) generateViaBackend() (string, error) {
	cfg := getConfig()
	if cfg.ApiURL == "" {
		return "", fmt.Errorf("API not configured (set api_url)")
	}
	hasAuth := cfg.ApiToken != "" || (cfg.ApiKey != "" && cfg.ApiSecret != "")
	if !hasAuth {
		return "", fmt.Errorf("API auth not configured (set api_token or api_key+api_secret)")
	}

	client := NewApiClient(cfg)
	var sb strings.Builder
	copies := j.Copies
	if copies < 1 {
		copies = 1
	}

	for i, row := range j.Rows {
		resp, err := client.GenerateTSPL(j.BackendTemplateID, row, copies, j.Layout, j.PresetName)
		if err != nil {
			return "", fmt.Errorf("row %d: %w", i+1, err)
		}
		sb.WriteString(resp.Commands)
		if !strings.HasSuffix(resp.Commands, "\r\n") {
			sb.WriteString("\r\n")
		}
	}

	return sb.String(), nil
}

// generateLocal produces TSPL2 using the local template engine.
func (j *BatchJob) generateLocal() string {
	var sb strings.Builder
	copies := j.Copies
	if copies < 1 {
		copies = 1
	}

	cols := j.Preset.Columns
	if cols < 1 {
		cols = 1
	}

	// Write header once
	sb.WriteString(generatePresetHeader(j.Preset))

	// Process rows in groups of `cols`
	for i := 0; i < len(j.Rows); i += cols {
		sb.WriteString("CLS\r\n")

		for c := 0; c < cols && (i+c) < len(j.Rows); c++ {
			row := j.Rows[i+c]
			colOffset := 0
			if c < len(j.Preset.ColOffsets) {
				colOffset = j.Preset.ColOffsets[c]
			}
			sb.WriteString(j.Template.Render(row, colOffset))
		}

		sb.WriteString(fmt.Sprintf("PRINT %d\r\n", copies))
	}

	return sb.String()
}

// Execute generates TSPL and sends it to the printer.
func (j *BatchJob) Execute() (*BatchResult, error) {
	tspl, err := j.GenerateTSPL()
	if err != nil {
		return &BatchResult{
			TotalRows: len(j.Rows),
			Errors:    []string{err.Error()},
			Mode:      j.Mode,
		}, err
	}

	result := &BatchResult{
		TotalRows: len(j.Rows),
		Errors:    []string{},
		Mode:      j.Mode,
		Bytes:     len(tspl),
	}

	printErr := sendToPrinterByName(tspl, j.Printer)
	if printErr != nil {
		result.Errors = append(result.Errors, printErr.Error())
		return result, printErr
	}

	result.Printed = len(j.Rows)
	log.Printf("[batch] Printed %d rows (%d bytes, mode=%s)", result.Printed, len(tspl), j.Mode)
	return result, nil
}

// sendToPrinterByName resolves a printer and sends raw data.
func sendToPrinterByName(tspl string, printerName string) error {
	allPrinters, _ := listAllPrinters()
	if printerName == "" {
		cfg := getConfig()
		printerName = cfg.DefaultPrinter
	}

	var targetPrinter *PrinterInfo
	if printerName == "" {
		if len(allPrinters) > 0 {
			targetPrinter = &allPrinters[0]
			printerName = targetPrinter.Name
		}
	} else {
		targetPrinter = findPrinter(printerName, allPrinters)
	}
	if printerName == "" {
		return fmt.Errorf("no printer found")
	}

	// Prepend TSPL initialization: ESC !R forces the printer into TSPL2 mode.
	// This prevents the printer from printing raw text when it's stuck in
	// another mode (text, hex dump, PCL, etc.)
	tsplInit := "\x1b!R\r\nSET CUTTER OFF\r\n"
	data := []byte(tsplInit + tspl)

	if targetPrinter != nil && (targetPrinter.Type == "network" || targetPrinter.Type == "manual" || targetPrinter.Type == "raw") {
		return networkRawPrint(targetPrinter.Address, data)
	}
	return rawPrint(printerName, data)
}
