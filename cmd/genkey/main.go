// Command genkey generates an Ed25519 private key in PKCS#8 PEM format,
// ready to be used as JWT_PRIVATE_KEY_PATH. It replaces `openssl genpkey`,
// which is unavailable on the LibreSSL build shipped with macOS.
//
// Usage:
//
//	go run ./cmd/genkey                  # print to stdout
//	go run ./cmd/genkey -out certs/private.pem
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/orange/hivemind/internal/adapters/auth"
)

func main() {
	out := flag.String("out", "", "output file path (default: stdout)")
	force := flag.Bool("force", false, "overwrite the output file if it already exists")
	flag.Parse()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fail("generate key: %v", err)
	}

	pemBytes, err := auth.MarshalPrivateKeyPEM(priv)
	if err != nil {
		fail("encode key: %v", err)
	}

	if *out == "" {
		os.Stdout.Write(pemBytes)
		return
	}

	if _, err := os.Stat(*out); err == nil && !*force {
		fail("%s already exists (use -force to overwrite)", *out)
	}
	if dir := filepath.Dir(*out); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fail("create dir: %v", err)
		}
	}
	if err := os.WriteFile(*out, pemBytes, 0o600); err != nil {
		fail("write key: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Ed25519 private key written to %s\n", *out)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "genkey: "+format+"\n", args...)
	os.Exit(1)
}
