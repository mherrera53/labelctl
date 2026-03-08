//go:build !darwin && !windows

package main

// queryDriverDPI is a stub for unsupported platforms.
func queryDriverDPI(printerName string) (int, bool) {
	return 0, false
}
