package main

import (
	"encoding/json"
	"net/http"
	"runtime"
	"sync"
)

// DriverStatus describes the state of TSC drivers and USB devices on this machine.
type DriverStatus struct {
	DriversInstalled   bool        `json:"drivers_installed"`
	DriverNames        []string    `json:"driver_names,omitempty"`
	USBDevices         []USBDevice `json:"usb_devices,omitempty"`
	RegisteredPrinters []string    `json:"registered_printers,omitempty"`
	NeedsSetup         bool        `json:"needs_setup"`
	CanAutoInstall     bool        `json:"can_auto_install"`
	OS                 string      `json:"os"`
	Instructions       string      `json:"instructions,omitempty"`
	DownloadURL        string      `json:"download_url,omitempty"`
}

// USBDevice represents a detected TSC USB device.
type USBDevice struct {
	VendorID  int    `json:"vendor_id"`
	ProductID int    `json:"product_id"`
	Name      string `json:"name"`
}

// DriverSetupRequest is the payload for POST /driver/setup.
type DriverSetupRequest struct {
	Action string `json:"action"` // "register" | "download" | "full-setup"
}

// Download progress tracking
var (
	driverDownloadProgress int
	driverDownloadMu       sync.Mutex
)

// Official TSC driver download URLs
const (
	tscDriverURLMac     = "https://usca.tscprinters.com/en/downloads"
	tscDriverURLWindows = "https://usca.tscprinters.com/en/downloads"
)

// handleDriverStatus returns the current state of TSC drivers on this machine.
func handleDriverStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	status := detectTSCDrivers()
	status.OS = runtime.GOOS
	jsonResponse(w, http.StatusOK, status)
}

// handleDriverSetup runs a driver setup action.
func handleDriverSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req DriverSetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	result, err := runDriverSetup(req.Action)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{
			"error":  err.Error(),
			"action": req.Action,
		})
		return
	}
	jsonResponse(w, http.StatusOK, result)
}

// handleDriverProgress returns the current download progress (0-100).
func handleDriverProgress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	driverDownloadMu.Lock()
	p := driverDownloadProgress
	driverDownloadMu.Unlock()
	jsonResponse(w, http.StatusOK, map[string]any{"progress": p})
}
