package main

import "fmt"

// LabelPreset defines a reusable label layout configuration.
type LabelPreset struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Columns     int     `json:"columns"`       // 1 = single, 3 = matrix
	LabelWidth  float64 `json:"label_width"`   // mm per label
	LabelHeight float64 `json:"label_height"`  // mm per label
	GapRow      float64 `json:"gap_row"`       // mm gap between rows
	GapCol      int     `json:"gap_col"`       // dots gap between columns
	TotalWidth  float64 `json:"total_width"`   // mm SIZE width total
	Direction   string  `json:"direction"`     // "0,0" | "1,0"
	Speed       int     `json:"speed"`
	Density     int     `json:"density"`
	ColOffsets  []int   `json:"col_offsets"`   // x positions in dots per column
	Builtin     bool    `json:"builtin"`       // true = cannot be deleted
}

// builtinPresets are the factory-shipped label presets.
var builtinPresets = []LabelPreset{
	{
		ID:          "single-40x25",
		Name:        "Individual 40x25mm",
		Columns:     1,
		LabelWidth:  40,
		LabelHeight: 25,
		GapRow:      3,
		GapCol:      0,
		TotalWidth:  40,
		Direction:   "0,0",
		Speed:       4,
		Density:     8,
		ColOffsets:  []int{8},
		Builtin:     true,
	},
	{
		ID:          "single-30x22",
		Name:        "Individual 30x22mm",
		Columns:     1,
		LabelWidth:  30,
		LabelHeight: 22,
		GapRow:      3,
		GapCol:      0,
		TotalWidth:  30,
		Direction:   "0,0",
		Speed:       4,
		Density:     8,
		ColOffsets:  []int{8},
		Builtin:     true,
	},
	{
		ID:          "matrix-3x1-30x22",
		Name:        "3 por fila 30x22mm (96mm total)",
		Columns:     3,
		LabelWidth:  30,
		LabelHeight: 22,
		GapRow:      3,
		GapCol:      272,
		TotalWidth:  96,
		Direction:   "0,0",
		Speed:       4,
		Density:     8,
		ColOffsets:  []int{8, 280, 552},
		Builtin:     true,
	},
	{
		ID:          "single-76x51",
		Name:        "Individual 3\"x2\" (76x51mm)",
		Columns:     1,
		LabelWidth:  76.2,
		LabelHeight: 50.8,
		GapRow:      3,
		GapCol:      0,
		TotalWidth:  76.2,
		Direction:   "0,0",
		Speed:       4,
		Density:     8,
		ColOffsets:  []int{8},
		Builtin:     true,
	},
}

// getPresetByID returns a preset by ID from built-in + custom presets.
func getPresetByID(id string, custom []LabelPreset) *LabelPreset {
	for i := range builtinPresets {
		if builtinPresets[i].ID == id {
			return &builtinPresets[i]
		}
	}
	for i := range custom {
		if custom[i].ID == id {
			return &custom[i]
		}
	}
	return nil
}

// getAllPresets returns built-in presets + custom presets from config.
func getAllPresets(custom []LabelPreset) []LabelPreset {
	all := make([]LabelPreset, 0, len(builtinPresets)+len(custom))
	all = append(all, builtinPresets...)
	all = append(all, custom...)
	return all
}

// generatePresetHeader returns TSPL2 setup commands for a preset.
func generatePresetHeader(p *LabelPreset) string {
	cmds := ""
	cmds += "SIZE " + fmtFloat(p.TotalWidth) + " mm, " + fmtFloat(p.LabelHeight) + " mm\r\n"
	cmds += "GAP " + fmtFloat(p.GapRow) + " mm, 0 mm\r\n"
	cmds += "DIRECTION " + p.Direction + "\r\n"
	cmds += "SPEED " + itoa(p.Speed) + "\r\n"
	cmds += "DENSITY " + itoa(p.Density) + "\r\n"
	cmds += "SET TEAR ON\r\n"
	cmds += "CLS\r\n"
	return cmds
}

func fmtFloat(f float64) string {
	s := ""
	if f == float64(int(f)) {
		s = itoa(int(f))
	} else {
		s = fmt.Sprintf("%.1f", f)
	}
	return s
}

func itoa(i int) string {
	return fmt.Sprintf("%d", i)
}
