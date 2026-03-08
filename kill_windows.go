//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
)

// killOldInstances kills any other tsc-bridge.exe processes on Windows.
// Excludes the current process by PID.
func killOldInstances() {
	myPID := os.Getpid()
	filter := fmt.Sprintf("PID ne %d", myPID)

	log.Printf("[kill] Killing old tsc-bridge.exe instances (excluding PID %d)", myPID)
	cmd := exec.Command("taskkill", "/IM", "tsc-bridge.exe", "/F", "/FI", filter)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[kill] taskkill: %v — %s", err, string(out))
	} else {
		log.Printf("[kill] taskkill: %s", string(out))
	}
}
