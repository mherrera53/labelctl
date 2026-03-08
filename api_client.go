package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// BackendTemplate is a PDF template from the anysubscriptions backend.
type BackendTemplate struct {
	ID                string `json:"id"`          // UUID
	Name              string `json:"name"`
	Description       string `json:"description"`
	Categoria         string `json:"categoria"`
	Icon              string `json:"icon"`
	ThermalPrintable  int    `json:"thermal_printable"`
	ConfigAdicional   string `json:"config_adicional,omitempty"` // JSON string
}

// FieldInfo describes a template field with its name and type (legacy, kept for compat).
type FieldInfo struct {
	Name string `json:"name"`
	Type string `json:"type"` // e.g. "multiVariableText", "qrcode", "image", "text"
}

// FieldDetail describes a template field with full pdfme layout information.
type FieldDetail struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	X         float64  `json:"x"`
	Y         float64  `json:"y"`
	Width     float64  `json:"width"`
	Height    float64  `json:"height"`
	FontSize  float64  `json:"font_size,omitempty"`
	Alignment string   `json:"alignment,omitempty"`
	Variables []string `json:"variables,omitempty"` // variable names for mapping (multiVariableText or {placeholder})
}

// BackendTemplateDetail includes the pdfme content and thermal config.
type BackendTemplateDetail struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	Categoria        string          `json:"categoria"`
	ThermalPrintable int             `json:"thermal_printable"`
	Fields           []string        `json:"fields"`              // field names (backward compat)
	FieldsTyped      []FieldDetail   `json:"fields_typed"`        // fields with types + positions
	Schema           json.RawMessage `json:"schema,omitempty"`    // raw pdfme content for preview
	ThermalConfig    *ThermalConfig  `json:"thermal_config,omitempty"`
}

// ThermalConfig is the thermal printing configuration from config_adicional.
type ThermalConfig struct {
	Layout string       `json:"layout"` // "single" or "matrix_3x1"
	Matrix *MatrixConfig `json:"matrix,omitempty"`
}

// MatrixConfig defines multi-column label layout.
type MatrixConfig struct {
	Columns      int   `json:"columns"`
	ColOffsets   []int `json:"col_offsets"`    // dots
	TotalWidthMm int  `json:"total_width_mm"`
}

// TsplResponse is the response from the backend TSPL generation endpoint.
type TsplResponse struct {
	Commands      string   `json:"commands"`
	CommandsArray []string `json:"commands_array"`
	SizeBytes     int      `json:"size_bytes"`
	Copies        int      `json:"copies"`
	Layout        string   `json:"layout"`
}

// ApiClient communicates with the anysubscriptions backend.
type ApiClient struct {
	baseURL    string
	bearerVal  string // "KEY:SECRET" or "eyJ..." JWT
	wlID       string // White Label ID header value
	httpClient *http.Client
}

// NewApiClient creates a client from the current config.
// Supports two auth modes:
//   - API Key:Secret → Bearer KEY:SECRET (detected by presence of api_key + api_secret)
//   - JWT Token → Bearer eyJ... (fallback to api_token)
func NewApiClient(cfg AppConfig) *ApiClient {
	bearer := cfg.ApiToken
	if cfg.ApiKey != "" && cfg.ApiSecret != "" {
		bearer = cfg.ApiKey + ":" + cfg.ApiSecret
	}

	wl := "20" // default ISI Hospital
	if cfg.ApiWhiteLabel > 0 {
		wl = fmt.Sprintf("%d", cfg.ApiWhiteLabel)
	}

	return &ApiClient{
		baseURL:   cfg.ApiURL,
		bearerVal: bearer,
		wlID:      wl,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *ApiClient) doGet(path string) ([]byte, error) {
	url := c.baseURL + path
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.bearerVal)
	req.Header.Set("X-Any-Wl", c.wlID)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func (c *ApiClient) doPost(path string, payload any) ([]byte, error) {
	url := c.baseURL + path
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.bearerVal)
	req.Header.Set("X-Any-Wl", c.wlID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

// TestConnection verifies the backend is reachable and authenticated.
func (c *ApiClient) TestConnection() error {
	// Use pdf-templates/all as health check since /status may not need auth
	body, err := c.doGet("/pdf-templates/all?limit=1")
	if err != nil {
		return err
	}
	var resp struct {
		Status int `json:"status"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("invalid response: %w", err)
	}
	if resp.Status != 1 {
		return fmt.Errorf("API returned status %d", resp.Status)
	}
	return nil
}

// BrandInfo contains branding information from the backend.
type BrandInfo struct {
	BrandName      string `json:"brandName"`
	BrandLogo      string `json:"brandLogo"`
	BrandURL       string `json:"brandUrl"`
	StoreName      string `json:"storeName"`
	StoreLogo      string `json:"storeLogo"`
	WLName         string `json:"whiteLabelName"`
	WLLogo         string `json:"whiteLabelLogo"`
	WLDomain       string `json:"whiteLabelDomain"`
	PrimaryColor   string `json:"primary"`
	SecondaryColor string `json:"secondary"`
}

// FetchBrandInfo returns brand/whitelabel info for the authenticated user.
// GET /users/brand-info
func (c *ApiClient) FetchBrandInfo() (*BrandInfo, error) {
	body, err := c.doGet("/users/brand-info")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Status int `json:"status"`
		Data   struct {
			BrandName  string `json:"brandName"`
			BrandLogo  string `json:"brandLogo"`
			BrandURL   string `json:"brandUrl"`
			StoreName  string `json:"storeName"`
			StoreLogo  string `json:"storeLogo"`
			WLName     string `json:"whiteLabelName"`
			WLLogo     string `json:"whiteLabelLogo"`
			WLDomain   string `json:"whiteLabelDomain"`
			Colors     struct {
				Primary   string `json:"primary"`
				Secondary string `json:"secondary"`
			} `json:"colors"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse brand info: %w", err)
	}
	d := resp.Data
	return &BrandInfo{
		BrandName:      d.BrandName,
		BrandLogo:      d.BrandLogo,
		BrandURL:       d.BrandURL,
		StoreName:      d.StoreName,
		StoreLogo:      d.StoreLogo,
		WLName:         d.WLName,
		WLLogo:         d.WLLogo,
		WLDomain:       d.WLDomain,
		PrimaryColor:   d.Colors.Primary,
		SecondaryColor: d.Colors.Secondary,
	}, nil
}

// FetchTemplates returns all PDF templates from the backend.
// GET /pdf-templates/all
func (c *ApiClient) FetchTemplates() ([]BackendTemplate, error) {
	body, err := c.doGet("/pdf-templates/all?limit=100")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Status int `json:"status"`
		Data   struct {
			Templates []BackendTemplate `json:"templates"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return resp.Data.Templates, nil
}

// FetchTemplateFields returns the field names for a template.
// GET /pdf-templates/{uuid}/fields
func (c *ApiClient) FetchTemplateFields(templateID string) ([]string, error) {
	body, err := c.doGet("/pdf-templates/" + templateID + "/fields")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Status int `json:"status"`
		Data   struct {
			Fields []string `json:"fields"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		// Fallback: try parsing as direct array
		var resp2 struct {
			Status int      `json:"status"`
			Data   []string `json:"data"`
		}
		if err2 := json.Unmarshal(body, &resp2); err2 != nil {
			return nil, fmt.Errorf("parse fields: %w", err)
		}
		return resp2.Data, nil
	}
	return resp.Data.Fields, nil
}

// FetchTemplateDetail returns a template with its schema content.
// GET /pdf-templates/{uuid}
//
// Uses two-pass unmarshal: pass 1 extracts metadata/fields (order irrelevant),
// pass 2 preserves raw JSON bytes for the schema so key order (= z-order in PDF)
// is maintained. Without this, Go's map[string]any alphabetizes keys and
// guilloche patterns render ON TOP of text fields.
func (c *ApiClient) FetchTemplateDetail(templateID string) (*BackendTemplateDetail, error) {
	body, err := c.doGet("/pdf-templates/" + templateID)
	if err != nil {
		return nil, err
	}

	// Pass 1: Extract metadata and fields (order doesn't matter for these)
	var resp struct {
		Status int `json:"status"`
		Data   struct {
			ID               string `json:"id"`
			Name             string `json:"name"`
			Description      string `json:"description"`
			Categoria        string `json:"categoria"`
			ThermalPrintable int    `json:"thermal_printable"`
			ConfigAdicional  any    `json:"config_adicional"`
			Content          any    `json:"content"` // pdfme schema — used only for field extraction
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}

	detail := &BackendTemplateDetail{
		ID:               resp.Data.ID,
		Name:             resp.Data.Name,
		Description:      resp.Data.Description,
		Categoria:        resp.Data.Categoria,
		ThermalPrintable: resp.Data.ThermalPrintable,
	}

	// Extract fields from pdfme schema content (uses map[string]any, order irrelevant)
	if resp.Data.Content != nil {
		detail.Fields, detail.FieldsTyped = extractPdfmeFieldsDetailed(resp.Data.Content)
	}

	// Pass 2: Preserve raw JSON bytes for schema (ORDER MATTERS for z-order!)
	// json.RawMessage keeps the original byte sequence without re-marshaling through map[string]any
	var rawResp struct {
		Data struct {
			Content json.RawMessage `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &rawResp); err == nil && len(rawResp.Data.Content) > 0 {
		detail.Schema = rawResp.Data.Content // Direct assignment, NO re-marshal!
	}

	// Extract thermal_config from config_adicional
	detail.ThermalConfig = extractThermalConfig(resp.Data.ConfigAdicional)

	return detail, nil
}

// GenerateTSPL calls the backend to generate TSPL commands from a template + data.
// POST /pdfs/generate-tspl-commands
func (c *ApiClient) GenerateTSPL(templateID string, data map[string]string, copies int, layout string, preset string) (*TsplResponse, error) {
	payload := map[string]any{
		"template_id": templateID,
		"data":        data,
		"copies":      copies,
	}
	if layout != "" {
		payload["layout"] = layout
	}
	if preset != "" {
		payload["preset"] = preset
	}

	body, err := c.doPost("/pdfs/generate-tspl-commands", payload)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Status int          `json:"status"`
		Data   TsplResponse `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse tspl response: %w", err)
	}
	if resp.Status != 1 {
		return nil, fmt.Errorf("TSPL generation failed (status %d)", resp.Status)
	}
	return &resp.Data, nil
}

// extractPdfmeFieldsDetailed reads field names, types, positions, dimensions, and variables from a pdfme schema.
// Supports both v4 (keyed objects) and v5 (array with name property) formats.
// Variables are extracted so the dashboard can map at the variable level (not field name level).
func extractPdfmeFieldsDetailed(content any) ([]string, []FieldDetail) {
	contentMap, ok := content.(map[string]any)
	if !ok {
		return nil, nil
	}

	schemas, ok := contentMap["schemas"].([]any)
	if !ok || len(schemas) == 0 {
		return nil, nil
	}

	// Only these types are mappable data fields (skip line, rectangle, image, etc.)
	mappableTypes := map[string]bool{"text": true, "multiVariableText": true, "qrcode": true, "barcode": true}

	seen := map[string]bool{}
	var names []string
	var detailed []FieldDetail

	for _, page := range schemas {
		switch p := page.(type) {
		case []any:
			// v5 format: array of objects with "name", "type", "position", "width", "height"
			for _, elem := range p {
				obj, ok := elem.(map[string]any)
				if !ok {
					continue
				}
				name, _ := obj["name"].(string)
				fieldType, _ := obj["type"].(string)
				if name == "" || seen[name] {
					continue
				}
				if !mappableTypes[fieldType] {
					continue
				}
				seen[name] = true
				names = append(names, name)

				fd := FieldDetail{Name: name}
				fd.Type, _ = obj["type"].(string)
				fd.Width, _ = obj["width"].(float64)
				fd.Height, _ = obj["height"].(float64)
				fd.FontSize, _ = obj["fontSize"].(float64)
				fd.Alignment, _ = obj["alignment"].(string)

				if pos, ok := obj["position"].(map[string]any); ok {
					fd.X, _ = pos["x"].(float64)
					fd.Y, _ = pos["y"].(float64)
				}

				fd.Variables = extractFieldVariables(obj)
				detailed = append(detailed, fd)
			}
		case map[string]any:
			// v4 format: keyed objects where value may have "type"
			for key, val := range p {
				if seen[key] {
					continue
				}

				fd := FieldDetail{Name: key}
				if obj, ok := val.(map[string]any); ok {
					fd.Type, _ = obj["type"].(string)
					if !mappableTypes[fd.Type] {
						continue
					}
					fd.Width, _ = obj["width"].(float64)
					fd.Height, _ = obj["height"].(float64)
					fd.FontSize, _ = obj["fontSize"].(float64)
					fd.Alignment, _ = obj["alignment"].(string)
					if pos, ok := obj["position"].(map[string]any); ok {
						fd.X, _ = pos["x"].(float64)
						fd.Y, _ = pos["y"].(float64)
					}
					fd.Variables = extractFieldVariables(obj)
				} else if !mappableTypes[fd.Type] {
					continue
				}

				seen[key] = true
				names = append(names, key)
				detailed = append(detailed, fd)
			}
		}
	}
	return names, detailed
}

// extractFieldVariables extracts the mappable variable names from a pdfme field object.
// Handles:
//   - multiVariableText/text with "variables" array (e.g. ["gafete.nombre", "gafete.apellido"])
//   - qrcode/barcode/text with {placeholder} in content (e.g. "{gafete.token}")
//   - table with body as variable string (e.g. "{medicamentos_tabla}")
func extractFieldVariables(obj map[string]any) []string {
	// Any field type can have an explicit variables array (multiVariableText, text with ISI custom vars, table)
	if vars, ok := obj["variables"].([]any); ok && len(vars) > 0 {
		var result []string
		for _, v := range vars {
			if s, ok := v.(string); ok && s != "" {
				result = append(result, s)
			}
		}
		if len(result) > 0 {
			return result
		}
	}

	// Check text template for {placeholder} patterns (e.g. text: "{gafete.nombre}")
	if text, ok := obj["text"].(string); ok && text != "" {
		vars := extractAllPlaceholders(text)
		if len(vars) > 0 {
			return vars
		}
	}

	// Check content for single {placeholder} (qrcode, barcode, text without variables array)
	if content, ok := obj["content"].(string); ok && content != "" {
		if ph := extractSinglePlaceholder(content); ph != "" {
			return []string{ph}
		}
	}

	// Check table body for variable reference
	if body, ok := obj["body"].(string); ok && body != "" {
		if ph := extractSinglePlaceholder(body); ph != "" {
			return []string{ph}
		}
	}

	return nil
}

// extractAllPlaceholders finds all {varName} patterns in a text string.
func extractAllPlaceholders(text string) []string {
	var result []string
	seen := map[string]bool{}
	remaining := text
	for {
		start := strings.Index(remaining, "{")
		if start == -1 {
			break
		}
		end := strings.Index(remaining[start:], "}")
		if end == -1 {
			break
		}
		inner := remaining[start+1 : start+end]
		remaining = remaining[start+end+1:]
		// Skip JSON-like patterns and Handlebars conditionals
		if strings.ContainsAny(inner, "\":# ") {
			continue
		}
		if inner != "" && !seen[inner] {
			seen[inner] = true
			result = append(result, inner)
		}
	}
	return result
}

// extractSinglePlaceholder returns the variable name if the string is exactly "{varName}".
func extractSinglePlaceholder(s string) string {
	s = strings.TrimSpace(s)
	if len(s) < 3 || s[0] != '{' || s[len(s)-1] != '}' {
		return ""
	}
	inner := s[1 : len(s)-1]
	// Must not contain spaces, braces, or colons (which would indicate JSON)
	if strings.ContainsAny(inner, " {}:\"") {
		return ""
	}
	return inner
}

// extractThermalConfig parses config_adicional to get thermal_config.
func extractThermalConfig(configAdicional any) *ThermalConfig {
	if configAdicional == nil {
		return nil
	}

	var cfgMap map[string]any

	switch v := configAdicional.(type) {
	case map[string]any:
		cfgMap = v
	case string:
		if v == "" {
			return nil
		}
		if err := json.Unmarshal([]byte(v), &cfgMap); err != nil {
			return nil
		}
	default:
		return nil
	}

	tcRaw, ok := cfgMap["thermal_config"]
	if !ok {
		return nil
	}

	tcMap, ok := tcRaw.(map[string]any)
	if !ok {
		return nil
	}

	tc := &ThermalConfig{}
	if layout, ok := tcMap["layout"].(string); ok {
		tc.Layout = layout
	}

	if matrixRaw, ok := tcMap["matrix"].(map[string]any); ok {
		tc.Matrix = &MatrixConfig{}
		if cols, ok := matrixRaw["columns"].(float64); ok {
			tc.Matrix.Columns = int(cols)
		}
		if tw, ok := matrixRaw["total_width_mm"].(float64); ok {
			tc.Matrix.TotalWidthMm = int(tw)
		}
		if offsets, ok := matrixRaw["col_offsets"].([]any); ok {
			for _, o := range offsets {
				if v, ok := o.(float64); ok {
					tc.Matrix.ColOffsets = append(tc.Matrix.ColOffsets, int(v))
				}
			}
		}
	}

	return tc
}
