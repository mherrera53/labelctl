package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// LabelField defines a single field in a label template.
type LabelField struct {
	Name      string `json:"name"`       // variable name, e.g. "descripcion"
	Type      string `json:"type"`       // "text", "barcode", "qrcode"
	X         int    `json:"x"`          // x offset in dots (relative to column)
	Y         int    `json:"y"`          // y offset in dots
	Font      string `json:"font"`       // TSPL font: "1"-"5" or "0" for barcode
	FontSize  int    `json:"font_size"`  // width multiplier for text
	Height    int    `json:"height"`     // barcode/qr height in dots
	CellWidth int    `json:"cell_width"` // barcode narrow bar width
}

// LabelTemplate defines a reusable TSPL2 label template with variable placeholders.
type LabelTemplate struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	PresetID string       `json:"preset_id"` // which LabelPreset to use for SIZE/GAP
	Fields   []LabelField `json:"fields"`
	Builtin  bool         `json:"builtin"`
}

var (
	customTemplates []LabelTemplate
	templatesMu     sync.RWMutex
)

// Built-in templates for common use cases.
var builtinTemplates = []LabelTemplate{
	{
		ID:       "basic-barcode",
		Name:     "Texto + Barcode",
		PresetID: "single-30x22",
		Fields: []LabelField{
			{Name: "descripcion", Type: "text", X: 8, Y: 8, Font: "3", FontSize: 1, Height: 0, CellWidth: 0},
			{Name: "codigo", Type: "barcode", X: 8, Y: 48, Font: "0", FontSize: 0, Height: 40, CellWidth: 2},
		},
		Builtin: true,
	},
	{
		ID:       "basic-qr",
		Name:     "Texto + QR",
		PresetID: "single-30x22",
		Fields: []LabelField{
			{Name: "descripcion", Type: "text", X: 8, Y: 8, Font: "3", FontSize: 1, Height: 0, CellWidth: 0},
			{Name: "codigo", Type: "qrcode", X: 8, Y: 48, Font: "0", FontSize: 0, Height: 4, CellWidth: 0},
		},
		Builtin: true,
	},
	{
		ID:       "product-label",
		Name:     "Producto (desc + presentacion + barcode)",
		PresetID: "matrix-3x1-30x22",
		Fields: []LabelField{
			{Name: "descripcion", Type: "text", X: 0, Y: 8, Font: "2", FontSize: 1, Height: 0, CellWidth: 0},
			{Name: "presentacion", Type: "text", X: 0, Y: 32, Font: "1", FontSize: 1, Height: 0, CellWidth: 0},
			{Name: "codigo", Type: "barcode", X: 0, Y: 56, Font: "0", FontSize: 0, Height: 40, CellWidth: 2},
			{Name: "codigo", Type: "text", X: 0, Y: 100, Font: "1", FontSize: 1, Height: 0, CellWidth: 0},
		},
		Builtin: true,
	},
}

// templatesPath returns the path to the templates JSON file.
func templatesPath() string {
	return filepath.Join(configDir(), "templates.json")
}

// LoadTemplates reads custom templates from disk.
func LoadTemplates() {
	templatesMu.Lock()
	defer templatesMu.Unlock()

	data, err := os.ReadFile(templatesPath())
	if err != nil {
		customTemplates = []LabelTemplate{}
		return
	}
	if err := json.Unmarshal(data, &customTemplates); err != nil {
		log.Printf("[templates] Error parsing templates: %v", err)
		customTemplates = []LabelTemplate{}
	}
}

// SaveTemplates writes custom templates to disk.
func SaveTemplates() error {
	templatesMu.RLock()
	data, err := json.MarshalIndent(customTemplates, "", "  ")
	templatesMu.RUnlock()
	if err != nil {
		return err
	}
	dir := filepath.Dir(templatesPath())
	os.MkdirAll(dir, 0755)
	return os.WriteFile(templatesPath(), data, 0644)
}

// GetAllTemplates returns built-in + custom templates.
func GetAllTemplates() []LabelTemplate {
	templatesMu.RLock()
	defer templatesMu.RUnlock()
	all := make([]LabelTemplate, 0, len(builtinTemplates)+len(customTemplates))
	all = append(all, builtinTemplates...)
	all = append(all, customTemplates...)
	return all
}

// GetTemplate returns a template by ID.
func GetTemplate(id string) *LabelTemplate {
	for i := range builtinTemplates {
		if builtinTemplates[i].ID == id {
			return &builtinTemplates[i]
		}
	}
	templatesMu.RLock()
	defer templatesMu.RUnlock()
	for i := range customTemplates {
		if customTemplates[i].ID == id {
			return &customTemplates[i]
		}
	}
	return nil
}

// SaveTemplate creates or updates a custom template.
func SaveTemplate(t LabelTemplate) error {
	// Prevent overwriting builtins
	for _, bt := range builtinTemplates {
		if bt.ID == t.ID {
			return fmt.Errorf("cannot overwrite built-in template")
		}
	}
	t.Builtin = false
	templatesMu.Lock()
	found := false
	for i, ct := range customTemplates {
		if ct.ID == t.ID {
			customTemplates[i] = t
			found = true
			break
		}
	}
	if !found {
		customTemplates = append(customTemplates, t)
	}
	templatesMu.Unlock()
	return SaveTemplates()
}

// DeleteTemplate removes a custom template by ID.
func DeleteTemplate(id string) error {
	for _, bt := range builtinTemplates {
		if bt.ID == id {
			return fmt.Errorf("cannot delete built-in template")
		}
	}
	templatesMu.Lock()
	filtered := customTemplates[:0]
	deleted := false
	for _, ct := range customTemplates {
		if ct.ID == id {
			deleted = true
			continue
		}
		filtered = append(filtered, ct)
	}
	customTemplates = filtered
	templatesMu.Unlock()
	if !deleted {
		return fmt.Errorf("template not found")
	}
	return SaveTemplates()
}

// RequiredVars returns the unique variable names used by a template.
func (t *LabelTemplate) RequiredVars() []string {
	seen := map[string]bool{}
	var vars []string
	for _, f := range t.Fields {
		if !seen[f.Name] {
			seen[f.Name] = true
			vars = append(vars, f.Name)
		}
	}
	return vars
}

// RenderField generates TSPL2 commands for a single field with data substitution.
// colOffset is the x offset for multi-column presets.
func RenderField(f LabelField, data map[string]string, colOffset int) string {
	value := data[f.Name]
	if value == "" {
		value = f.Name // fallback: show variable name
	}
	x := f.X + colOffset

	switch f.Type {
	case "barcode":
		h := f.Height
		if h == 0 {
			h = 40
		}
		cw := f.CellWidth
		if cw == 0 {
			cw = 2
		}
		return fmt.Sprintf("BARCODE %d,%d,\"128\",%d,1,0,%d,%d,\"%s\"\r\n",
			x, f.Y, h, cw, cw, value)

	case "qrcode":
		cellSize := f.Height
		if cellSize == 0 {
			cellSize = 4
		}
		return fmt.Sprintf("QRCODE %d,%d,L,%d,A,0,\"%s\"\r\n",
			x, f.Y, cellSize, value)

	default: // "text"
		font := f.Font
		if font == "" {
			font = "2"
		}
		size := f.FontSize
		if size == 0 {
			size = 1
		}
		return fmt.Sprintf("TEXT %d,%d,\"%s\",0,%d,%d,\"%s\"\r\n",
			x, f.Y, font, size, size, escTSPL(value))
	}
}

// Render generates complete TSPL2 field commands for one label using the given data.
// colOffset allows multi-column placement.
func (t *LabelTemplate) Render(data map[string]string, colOffset int) string {
	var sb strings.Builder
	for _, f := range t.Fields {
		sb.WriteString(RenderField(f, data, colOffset))
	}
	return sb.String()
}

// escTSPL escapes characters that could break TSPL string literals.
func escTSPL(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "'")
	// Limit length to avoid overflowing label
	if len(s) > 60 {
		s = s[:57] + "..."
	}
	return s
}
