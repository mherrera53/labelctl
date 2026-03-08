package main

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// GET /native-file-dialog?type=excel|pdf|image|json
// Opens a native OS file picker and returns the file contents + metadata.
// This is needed because WKWebView does not support <input type="file">.
func handleNativeFileDialog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET only"})
		return
	}

	fileType := r.URL.Query().Get("type")
	filePath, err := openNativeFileDialog(fileType)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if filePath == "" {
		// User cancelled
		jsonResponse(w, http.StatusOK, map[string]any{"cancelled": true})
		return
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "read file: " + err.Error()})
		return
	}

	log.Printf("[file-dialog] User selected: %s (%d bytes)", filepath.Base(filePath), len(data))

	resp := map[string]any{
		"cancelled": false,
		"filename":  filepath.Base(filePath),
		"path":      filePath,
		"size":      len(data),
		"data":      base64.StdEncoding.EncodeToString(data),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// GET /native-download?file=batch_123.pdf&action=open|reveal
// Copies file from output dir to ~/Downloads and opens it (or reveals in Finder).
// Needed because WKWebView cannot download files.
func handleNativeDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET only"})
		return
	}

	filename := r.URL.Query().Get("file")
	if filename == "" || strings.Contains(filename, "..") || strings.Contains(filename, "/") {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid filename"})
		return
	}
	action := r.URL.Query().Get("action")
	if action == "" {
		action = "open"
	}

	srcPath := filepath.Join(configDir(), "output", filename)
	if _, err := os.Stat(srcPath); os.IsNotExist(err) {
		jsonResponse(w, http.StatusNotFound, map[string]string{"error": "file not found"})
		return
	}

	// Copy to ~/Downloads
	homeDir, _ := os.UserHomeDir()
	downloadsDir := filepath.Join(homeDir, "Downloads")
	destPath := filepath.Join(downloadsDir, filename)

	data, err := os.ReadFile(srcPath)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "read: " + err.Error()})
		return
	}
	if err := os.WriteFile(destPath, data, 0644); err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "write: " + err.Error()})
		return
	}

	log.Printf("[download] Copied %s to %s", filename, destPath)

	// Open or reveal
	switch runtime.GOOS {
	case "darwin":
		if action == "reveal" {
			exec.Command("open", "-R", destPath).Start()
		} else {
			exec.Command("open", destPath).Start()
		}
	case "windows":
		if action == "reveal" {
			exec.Command("explorer", "/select,", destPath).Start()
		} else {
			exec.Command("cmd", "/c", "start", "", destPath).Start()
		}
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"path":     destPath,
		"filename": filename,
		"action":   action,
	})
}

func openNativeFileDialog(fileType string) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return openFileDialogDarwin(fileType)
	default:
		return "", nil
	}
}

func openFileDialogDarwin(fileType string) (string, error) {
	// Build osascript to open a native file dialog
	var typeFilter string
	switch fileType {
	case "excel":
		typeFilter = `of type {"xlsx", "xls", "csv"}`
	case "pdf":
		typeFilter = `of type {"pdf"}`
	case "image":
		typeFilter = `of type {"png", "jpg", "jpeg", "gif", "bmp"}`
	case "json":
		typeFilter = `of type {"json"}`
	default:
		typeFilter = ""
	}

	script := `POSIX path of (choose file ` + typeFilter + ` with prompt "Seleccionar archivo")`
	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.Output()
	if err != nil {
		// User cancelled or error
		if strings.Contains(err.Error(), "exit status") {
			return "", nil // cancelled
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
