//go:build windows

package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
)

func setAppIcon() {
	// Save .ico to config directory for use in shortcuts
	iconPath := filepath.Join(configDir(), "icon.ico")
	if _, err := os.Stat(iconPath); err == nil {
		return // already exists
	}

	png := getAppIconPNG()
	ico := pngToICO(png)
	os.MkdirAll(configDir(), 0755)
	os.WriteFile(iconPath, ico, 0644)
}

// pngToICO wraps a PNG image in the ICO container format (Windows Vista+).
func pngToICO(png []byte) []byte {
	const headerSize = 6 + 16 // ICONDIR + 1 ICONDIRENTRY

	ico := make([]byte, headerSize+len(png))

	// ICONDIR
	binary.LittleEndian.PutUint16(ico[0:], 0)  // reserved
	binary.LittleEndian.PutUint16(ico[2:], 1)  // type: ICO
	binary.LittleEndian.PutUint16(ico[4:], 1)  // count: 1 image

	// ICONDIRENTRY
	ico[6] = 0  // width: 0 means 256
	ico[7] = 0  // height: 0 means 256
	ico[8] = 0  // color count
	ico[9] = 0  // reserved
	binary.LittleEndian.PutUint16(ico[10:], 1)                   // color planes
	binary.LittleEndian.PutUint16(ico[12:], 32)                  // bits per pixel
	binary.LittleEndian.PutUint32(ico[14:], uint32(len(png)))    // image data size
	binary.LittleEndian.PutUint32(ico[18:], uint32(headerSize))  // offset to data

	copy(ico[headerSize:], png)
	return ico
}
