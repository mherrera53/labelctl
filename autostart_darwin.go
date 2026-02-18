//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const launchAgentLabel = "com.tsc-bridge"

func launchAgentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
}

func isAutoStartEnabled() bool {
	if _, err := os.Stat(launchAgentPath()); os.IsNotExist(err) {
		return false
	}
	out, err := exec.Command("launchctl", "list").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), launchAgentLabel)
}

func setAutoStart(enable bool) error {
	plistPath := launchAgentPath()

	if !enable {
		exec.Command("launchctl", "unload", plistPath).Run()
		os.Remove(plistPath)
		return nil
	}

	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find binary: %v", err)
	}
	binPath, _ = filepath.EvalSymlinks(binPath)

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/tsc-bridge.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/tsc-bridge.err</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin</string>
    </dict>
</dict>
</plist>`, launchAgentLabel, binPath)

	os.MkdirAll(filepath.Dir(plistPath), 0755)
	if err := os.WriteFile(plistPath, []byte(plist), 0644); err != nil {
		return fmt.Errorf("cannot write plist: %v", err)
	}

	if err := exec.Command("launchctl", "load", plistPath).Run(); err != nil {
		return fmt.Errorf("launchctl load failed: %v", err)
	}

	return nil
}
