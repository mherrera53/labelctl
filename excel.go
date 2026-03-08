package main

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
)

// ExcelData holds the parsed contents of an Excel file.
type ExcelData struct {
	Headers []string            `json:"headers"`
	Rows    []map[string]string `json:"rows"`
}

// ParseExcel reads the first sheet of an xlsx file and returns headers + rows.
// Row 1 is treated as headers (variable names). Rows 2+ are data.
func ParseExcel(fileBytes []byte) (*ExcelData, error) {
	f, err := excelize.OpenReader(bytes.NewReader(fileBytes))
	if err != nil {
		return nil, fmt.Errorf("open excel: %w", err)
	}
	defer f.Close()

	sheetName := f.GetSheetName(0)
	if sheetName == "" {
		return nil, fmt.Errorf("no sheets found")
	}

	rows, err := f.GetRows(sheetName)
	if err != nil {
		return nil, fmt.Errorf("read rows: %w", err)
	}
	if len(rows) < 1 {
		return nil, fmt.Errorf("empty sheet")
	}

	// Row 0 = headers
	headers := make([]string, len(rows[0]))
	for i, h := range rows[0] {
		headers[i] = strings.TrimSpace(h)
	}

	// Rows 1+ = data
	data := make([]map[string]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		record := make(map[string]string, len(headers))
		for i, header := range headers {
			if header == "" {
				continue
			}
			val := ""
			if i < len(row) {
				val = strings.TrimSpace(row[i])
			}
			record[header] = val
		}
		// Skip completely empty rows
		empty := true
		for _, v := range record {
			if v != "" {
				empty = false
				break
			}
		}
		if !empty {
			data = append(data, record)
		}
	}

	return &ExcelData{
		Headers: headers,
		Rows:    data,
	}, nil
}
