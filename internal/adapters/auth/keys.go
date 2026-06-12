package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
)

// LoadOrGenerateKey loads an Ed25519 private key from a PKCS#8 PEM file. When
// privPath is empty it generates an ephemeral key (development only) and reports
// generated=true so the caller can warn that tokens won't survive a restart.
func LoadOrGenerateKey(privPath string) (key ed25519.PrivateKey, generated bool, err error) {
	if privPath == "" {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, false, fmt.Errorf("generate ephemeral key: %w", err)
		}
		return priv, true, nil
	}

	pemBytes, err := os.ReadFile(privPath)
	if err != nil {
		return nil, false, fmt.Errorf("read private key: %w", err)
	}

	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, false, errors.New("no PEM block found in private key file")
	}
	if block.Type != "PRIVATE KEY" {
		return nil, false, fmt.Errorf("unsupported PEM block type %q (expected PKCS#8 \"PRIVATE KEY\")", block.Type)
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, false, fmt.Errorf("parse PKCS8 key: %w", err)
	}
	priv, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, false, fmt.Errorf("private key is not Ed25519 (got %T)", parsed)
	}
	return priv, false, nil
}

// MarshalPrivateKeyPEM encodes an Ed25519 private key as a PKCS#8 PEM block.
// Useful for generating a key file to persist across restarts.
func MarshalPrivateKeyPEM(key ed25519.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal pkcs8: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}
