//go:build !darwin && !windows

package main

func isAutoStartEnabled() bool {
	return false
}

func setAutoStart(_ bool) error {
	return nil
}
