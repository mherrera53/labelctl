package main

import (
	"crypto/tls"
	_ "embed"
	"log"
)

//go:embed certs/server.crt
var embeddedCert []byte

//go:embed certs/server.key
var embeddedKey []byte

// loadEmbeddedCert returns the embedded Let's Encrypt certificate for local.labelctl.dev.
// Returns nil if the embedded cert is missing or invalid.
func loadEmbeddedCert() *tls.Certificate {
	if len(embeddedCert) == 0 || len(embeddedKey) == 0 {
		return nil
	}
	cert, err := tls.X509KeyPair(embeddedCert, embeddedKey)
	if err != nil {
		log.Printf("[tls] Embedded cert invalid: %v — falling back to self-signed", err)
		return nil
	}
	log.Printf("[tls] Using embedded Let's Encrypt certificate for local.labelctl.dev")
	return &cert
}
