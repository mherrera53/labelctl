package main

import (
	"bytes"
	"fmt"
	"image"
	_ "image/png" // register PNG decoder
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// rasterizeUploadedPDF converts a PDF file into per-page monochrome PNG images.
// Tries pdftoppm first, falls back to Ghostscript.
// Returns the output directory and page count.
func rasterizeUploadedPDF(pdfPath string, dpi int) (outDir string, pageCount int, err error) {
	outDir = filepath.Dir(pdfPath)
	prefix := filepath.Join(outDir, "page")

	// Try pdftoppm first (from poppler-utils)
	cmd := exec.Command("pdftoppm", "-mono", "-r", fmt.Sprintf("%d", dpi), "-png", pdfPath, prefix)
	hideWindowCmd(cmd)
	if out, err := cmd.CombinedOutput(); err == nil {
		count := countPNGs(outDir)
		if count > 0 {
			log.Printf("[pdf-upload] pdftoppm rasterized %d pages at %d DPI", count, dpi)
			return outDir, count, nil
		}
		log.Printf("[pdf-upload] pdftoppm produced 0 pages, output: %s", string(out))
	} else {
		log.Printf("[pdf-upload] pdftoppm failed: %v, trying ghostscript", err)
	}

	// Fallback to Ghostscript
	gsCmd := exec.Command("gs",
		"-dBATCH", "-dNOPAUSE", "-dQUIET",
		"-sDEVICE=pngmono",
		fmt.Sprintf("-r%d", dpi),
		fmt.Sprintf("-sOutputFile=%s", filepath.Join(outDir, "page-%03d.png")),
		pdfPath,
	)
	hideWindowCmd(gsCmd)
	if out, err := gsCmd.CombinedOutput(); err != nil {
		return "", 0, fmt.Errorf("ghostscript failed: %v — %s", err, string(out))
	}

	count := countPNGs(outDir)
	if count == 0 {
		return "", 0, fmt.Errorf("no pages produced from PDF")
	}
	log.Printf("[pdf-upload] ghostscript rasterized %d pages at %d DPI", count, dpi)
	return outDir, count, nil
}

// countPNGs counts .png files in a directory.
func countPNGs(dir string) int {
	entries, _ := os.ReadDir(dir)
	count := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".png") {
			count++
		}
	}
	return count
}

// getUploadPagePath returns the path to a rasterized PNG page.
// Pages are numbered starting from 0.
func getUploadPagePath(uploadID string, pageIndex int) string {
	uploadsDir := filepath.Join(configDir(), "uploads", uploadID)

	// List PNG files sorted by name
	entries, err := os.ReadDir(uploadsDir)
	if err != nil {
		return ""
	}
	var pngs []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".png") {
			pngs = append(pngs, e.Name())
		}
	}
	sort.Strings(pngs)

	if pageIndex >= len(pngs) || pageIndex < 0 {
		return ""
	}
	return filepath.Join(uploadsDir, pngs[pageIndex])
}

// renderUploadedPageTSPL reads a rasterized PNG and generates TSPL bitmap commands.
func renderUploadedPageTSPL(uploadID string, pageIndex int, copies int) []byte {
	pngPath := getUploadPagePath(uploadID, pageIndex)
	if pngPath == "" {
		return nil
	}

	f, err := os.Open(pngPath)
	if err != nil {
		log.Printf("[pdf-upload] open page %d: %v", pageIndex, err)
		return nil
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		log.Printf("[pdf-upload] decode page %d: %v", pageIndex, err)
		return nil
	}

	bounds := img.Bounds()
	imgW := bounds.Dx()
	imgH := bounds.Dy()

	// Calculate label size in mm from image dimensions at defaultDPI
	labelWmm := float64(imgW) * 25.4 / float64(defaultDPI)
	labelHmm := float64(imgH) * 25.4 / float64(defaultDPI)

	var buf bytes.Buffer
	buf.Write([]byte{0x1b, 0x21, 0x52})
	buf.WriteString("\r\n")
	buf.WriteString(fmt.Sprintf("SIZE %.1f mm, %.1f mm\r\n", labelWmm, labelHmm))
	buf.WriteString("GAP 3 mm, 0 mm\r\n")
	buf.WriteString("DIRECTION 0,0\r\n")
	buf.WriteString("SPEED 3\r\n")
	buf.WriteString("DENSITY 10\r\n")
	buf.WriteString("SET CUTTER OFF\r\n")
	buf.WriteString("SET TEAR ON\r\n")
	buf.WriteString("CLS\r\n")

	tsplWriteBitmap(&buf, img, 0, 0)

	if copies < 1 {
		copies = 1
	}
	buf.WriteString(fmt.Sprintf("PRINT %d\r\n", copies))

	return buf.Bytes()
}

// startUploadCleanup starts a goroutine that periodically removes old upload directories.
func startUploadCleanup() {
	go func() {
		for {
			time.Sleep(30 * time.Minute)
			cleanupUploads()
		}
	}()
}

// cleanupUploads removes upload directories older than 1 hour.
func cleanupUploads() {
	uploadsDir := filepath.Join(configDir(), "uploads")
	entries, err := os.ReadDir(uploadsDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-1 * time.Hour)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			path := filepath.Join(uploadsDir, e.Name())
			os.RemoveAll(path)
			log.Printf("[pdf-upload] Cleaned up old upload: %s", e.Name())
		}
	}
}

// rasterizeUploadedPDFThumbnails creates low-DPI thumbnails for the print dialog.
func rasterizeUploadedPDFThumbnails(pdfPath string) (string, int, error) {
	return rasterizeUploadedPDF(pdfPath, 72)
}
