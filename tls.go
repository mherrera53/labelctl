package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

const defaultHostname = "myprinter.com"

// certDir returns the directory for TLS certificates.
func certDir() string {
	return filepath.Join(configDir(), "certs")
}

// ensureCerts generates a CA + server certificate if they don't exist.
// Returns paths to cert, key, and CA files.
func ensureCerts(hostname string) (certFile, keyFile, caFile string, err error) {
	dir := certDir()
	os.MkdirAll(dir, 0755)

	caFile = filepath.Join(dir, "ca.pem")
	caKeyFile := filepath.Join(dir, "ca-key.pem")
	certFile = filepath.Join(dir, "server.pem")
	keyFile = filepath.Join(dir, "server-key.pem")

	// Check if certs already exist and are standards-compliant
	if _, e := os.Stat(certFile); e == nil {
		if _, e := os.Stat(keyFile); e == nil {
			if certIsCompliant(certFile) {
				log.Printf("[tls] Certificates found in %s", dir)
				return certFile, keyFile, caFile, nil
			}
			log.Printf("[tls] Existing certificate is non-compliant (>398 days) — regenerating")
			// Remove old certs and marker to force regeneration + CA re-install
			os.Remove(filepath.Join(dir, ".ca-installed"))
		}
	}

	log.Printf("[tls] Generating certificates for %s ...", hostname)

	// --- Generate CA ---
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", "", err
	}

	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"TSC Bridge"},
			CommonName:   "TSC Bridge Local CA",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return "", "", "", err
	}

	if err := writePEM(caFile, "CERTIFICATE", caCertDER); err != nil {
		return "", "", "", err
	}
	if err := writeECKeyPEM(caKeyFile, caKey); err != nil {
		return "", "", "", err
	}

	// --- Generate Server Certificate signed by CA ---
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", "", err
	}

	caParsed, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		return "", "", "", err
	}

	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			Organization: []string{"TSC Bridge"},
			CommonName:   hostname,
		},
		DNSNames:    []string{hostname, "localhost", "tsc-bridge", "myprinter.com"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(397 * 24 * time.Hour), // Max 398 days for Apple/Chrome compliance
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	serverCertDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caParsed, &serverKey.PublicKey, caKey)
	if err != nil {
		return "", "", "", err
	}

	if err := writePEM(certFile, "CERTIFICATE", serverCertDER); err != nil {
		return "", "", "", err
	}
	if err := writeECKeyPEM(keyFile, serverKey); err != nil {
		return "", "", "", err
	}

	log.Printf("[tls] Certificates generated in %s", dir)
	return certFile, keyFile, caFile, nil
}

func writePEM(file string, pemType string, data []byte) error {
	f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: pemType, Bytes: data})
}

func writeECKeyPEM(file string, key *ecdsa.PrivateKey) error {
	f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	return pem.Encode(f, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
}

// loadCertWithCA loads the server certificate + key and appends the CA cert to the chain.
// This ensures the TLS server sends the full chain so clients can verify trust.
func loadCertWithCA(certFile, keyFile, caFile string) (tls.Certificate, error) {
	tlsCert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return tlsCert, err
	}
	// Append CA cert to the chain
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		log.Printf("[tls] Could not read CA file for chain: %v — serving without chain", err)
		return tlsCert, nil
	}
	caBlock, _ := pem.Decode(caPEM)
	if caBlock != nil {
		tlsCert.Certificate = append(tlsCert.Certificate, caBlock.Bytes)
	}
	return tlsCert, nil
}

// certIsCompliant checks if an existing server certificate has <=398 day validity (Apple/Chrome requirement).
func certIsCompliant(certFile string) bool {
	data, err := os.ReadFile(certFile)
	if err != nil {
		return false
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	validity := cert.NotAfter.Sub(cert.NotBefore)
	return validity <= 399*24*time.Hour
}

// installCACert installs the CA certificate into the OS trust store.
// On Windows: uses certutil to add to Root store (triggers UAC prompt).
// On macOS: uses security add-trusted-cert to add to login keychain.
// Skips silently if already installed (marker file).
func installCACert(caFile string) {
	marker := filepath.Join(filepath.Dir(caFile), ".ca-installed")
	if _, err := os.Stat(marker); err == nil {
		log.Printf("[tls] CA already installed (marker exists)")
		return
	}

	switch runtime.GOOS {
	case "windows":
		// certutil -addstore Root <ca.pem> — adds to Trusted Root CAs
		// This triggers a UAC elevation prompt on Windows
		cmd := exec.Command("certutil", "-addstore", "Root", caFile)
		out, err := cmd.CombinedOutput()
		if err != nil {
			log.Printf("[tls] Failed to install CA on Windows: %v — %s", err, string(out))
			log.Printf("[tls] Users can manually install: certutil -addstore Root \"%s\"", caFile)
			return
		}
		log.Printf("[tls] CA certificate installed in Windows trust store")

	case "darwin":
		// Add CA to login keychain as trusted root — prompts for keychain password
		cmd := exec.Command("security", "add-trusted-cert", "-r", "trustRoot",
			"-k", filepath.Join(os.Getenv("HOME"), "Library", "Keychains", "login.keychain-db"),
			caFile)
		out, err := cmd.CombinedOutput()
		if err != nil {
			log.Printf("[tls] Failed to install CA on macOS: %v — %s", err, string(out))
			log.Printf("[tls] Install manually: security add-trusted-cert -r trustRoot -k ~/Library/Keychains/login.keychain-db \"%s\"", caFile)
			return
		}
		log.Printf("[tls] CA certificate installed in macOS login keychain")

	case "linux":
		// Copy CA to system trust store and update
		destDir := "/usr/local/share/ca-certificates"
		dest := filepath.Join(destDir, "tsc-bridge-ca.crt")
		cpCmd := exec.Command("sudo", "cp", caFile, dest)
		if out, err := cpCmd.CombinedOutput(); err != nil {
			log.Printf("[tls] Failed to copy CA on Linux: %v — %s", err, string(out))
			log.Printf("[tls] Install manually: sudo cp \"%s\" %s && sudo update-ca-certificates", caFile, dest)
			return
		}
		updCmd := exec.Command("sudo", "update-ca-certificates")
		if out, err := updCmd.CombinedOutput(); err != nil {
			log.Printf("[tls] Failed to update CA store on Linux: %v — %s", err, string(out))
			return
		}
		log.Printf("[tls] CA certificate installed in Linux trust store")

	default:
		log.Printf("[tls] Auto CA install not supported on %s — manually trust %s", runtime.GOOS, caFile)
		return
	}

	// Write marker so we don't re-install on every start
	os.WriteFile(marker, []byte("installed"), 0644)
}
