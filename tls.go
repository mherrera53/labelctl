package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
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

	// Check if certs already exist and are valid
	if _, e := os.Stat(certFile); e == nil {
		if _, e := os.Stat(keyFile); e == nil {
			log.Printf("[tls] Certificates found in %s", dir)
			return certFile, keyFile, caFile, nil
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
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
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
		NotAfter:    time.Now().Add(10 * 365 * 24 * time.Hour),
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
