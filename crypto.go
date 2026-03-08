package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

const encPrefix = "enc:" // prefix for encrypted values in config.json

var (
	machineKey     []byte
	machineKeyOnce sync.Once
)

// getMachineID returns a stable machine identifier.
// macOS: IOPlatformUUID from ioreg
// Windows: MachineGuid from registry
// Linux: /etc/machine-id
func getMachineID() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
		if err != nil {
			return "", fmt.Errorf("ioreg: %w", err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "IOPlatformUUID") {
				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 {
					uuid := strings.TrimSpace(parts[1])
					uuid = strings.Trim(uuid, `"`)
					return uuid, nil
				}
			}
		}
		return "", fmt.Errorf("IOPlatformUUID not found")

	case "windows":
		cmd := exec.Command("reg", "query",
			`HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Cryptography`,
			"/v", "MachineGuid")
		hideWindowCmd(cmd)
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("registry: %w", err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "MachineGuid") {
				fields := strings.Fields(line)
				if len(fields) >= 3 {
					return fields[len(fields)-1], nil
				}
			}
		}
		return "", fmt.Errorf("MachineGuid not found")

	default: // Linux and others
		data, err := os.ReadFile("/etc/machine-id")
		if err != nil {
			return "", fmt.Errorf("machine-id: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}
}

// deriveKey creates a 32-byte AES key from the machine ID using SHA-256.
// The salt ensures different apps on the same machine get different keys.
func deriveKey() []byte {
	machineKeyOnce.Do(func() {
		machineID, err := getMachineID()
		if err != nil {
			log.Printf("[crypto] WARNING: could not get machine ID: %v — using fallback", err)
			machineID = "tsc-bridge-fallback-key"
		}
		// Salt with app identifier
		salted := "tsc-bridge:v3:" + machineID
		hash := sha256.Sum256([]byte(salted))
		machineKey = hash[:]
	})
	return machineKey
}

// encryptString encrypts plaintext using AES-256-GCM with the machine key.
// Returns base64-encoded ciphertext prefixed with "enc:".
func encryptString(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	key := deriveKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aes cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(ciphertext), nil
}

// decryptString decrypts an "enc:"-prefixed base64 string using AES-256-GCM.
// If the value doesn't have the prefix, it's returned as-is (plaintext migration).
func decryptString(encrypted string) (string, error) {
	if encrypted == "" {
		return "", nil
	}

	// Not encrypted — return as-is (supports migrating from plaintext configs)
	if !strings.HasPrefix(encrypted, encPrefix) {
		return encrypted, nil
	}

	encoded := strings.TrimPrefix(encrypted, encPrefix)
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}

	key := deriveKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aes cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("gcm: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}

	return string(plaintext), nil
}

// isEncrypted checks if a string value is already encrypted.
func isEncrypted(value string) bool {
	return strings.HasPrefix(value, encPrefix)
}
