//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func startupShortcutPath() string {
	return filepath.Join(os.Getenv("APPDATA"),
		"Microsoft", "Windows", "Start Menu", "Programs", "Startup",
		"TSC Bridge.lnk")
}

func isAutoStartEnabled() bool {
	_, err := os.Stat(startupShortcutPath())
	return err == nil
}

func setAutoStart(enable bool) error {
	lnk := startupShortcutPath()

	if !enable {
		os.Remove(lnk)
		return nil
	}

	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find binary: %v", err)
	}
	binPath, _ = filepath.EvalSymlinks(binPath)
	binDir := filepath.Dir(binPath)
	iconPath := filepath.Join(configDir(), "icon.ico")

	ps := fmt.Sprintf(
		`$ws = New-Object -ComObject WScript.Shell; `+
			`$s = $ws.CreateShortcut('%s'); `+
			`$s.TargetPath = '%s'; `+
			`$s.WorkingDirectory = '%s'; `+
			`$s.Description = 'TSC Bridge'; `+
			`$s.WindowStyle = 7; `+
			`if (Test-Path '%s') { $s.IconLocation = '%s,0' }; `+
			`$s.Save()`,
		lnk, binPath, binDir, iconPath, iconPath)

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps)
	hideWindow(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("shortcut creation failed: %v — %s", err, string(out))
	}
	return nil
}
