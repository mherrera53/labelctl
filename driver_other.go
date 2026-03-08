//go:build !darwin && !windows

package main

import "fmt"

// detectTSCDrivers stub for unsupported platforms.
func detectTSCDrivers() DriverStatus {
	return DriverStatus{
		Instructions: "Deteccion de drivers TSC no soportada en esta plataforma.",
	}
}

// runDriverSetup stub for unsupported platforms.
func runDriverSetup(action string) (map[string]any, error) {
	return nil, fmt.Errorf("driver setup not supported on this platform")
}
