//go:build !darwin && !windows

package main

func setAppIcon() {
	// No-op on Linux/other platforms.
}
