//go:build !windows

package main

import (
	"log"
	"os/exec"
)

// killOldInstances kills any other tsc-bridge processes.
func killOldInstances() {
	log.Printf("[kill] Killing old tsc-bridge processes")
	exec.Command("pkill", "-9", "-f", "tsc-bridge").Run()
}

// hideWindowCmd is a no-op on non-Windows platforms.
func hideWindowCmd(_ *exec.Cmd) {}
