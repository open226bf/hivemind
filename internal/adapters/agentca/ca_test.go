package agentca

import (
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"
)

func TestIssueClient_ChainsToCA(t *testing.T) {
	ca, err := Generate()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	certPEM, keyPEM, serial, err := ca.IssueClient("cluster-123", time.Hour)
	if err != nil {
		t.Fatalf("issue client: %v", err)
	}
	if serial == "" || len(keyPEM) == 0 {
		t.Fatal("expected serial and key")
	}

	blk, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatalf("parse client cert: %v", err)
	}
	if cert.Subject.CommonName != "cluster-123" {
		t.Fatalf("CN = %q, want cluster-123", cert.Subject.CommonName)
	}

	// Verifies against the CA pool with client-auth usage.
	if _, err := cert.Verify(x509.VerifyOptions{
		Roots:     ca.Pool(),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Fatalf("client cert should chain to CA: %v", err)
	}
}

func TestLoadRoundTrip(t *testing.T) {
	ca, err := Generate()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	again, err := Load(ca.CertPEM(), ca.KeyPEM())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// A cert issued by the reloaded CA still verifies against the original pool.
	certPEM, _, _, err := again.IssueClient("x", time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	blk, _ := pem.Decode(certPEM)
	cert, _ := x509.ParseCertificate(blk.Bytes)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: ca.Pool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("verify after reload: %v", err)
	}
}

func TestIssueServerTLS(t *testing.T) {
	ca, err := Generate()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := ca.IssueServerTLS([]string{"localhost", "127.0.0.1"}, time.Hour); err != nil {
		t.Fatalf("issue server tls: %v", err)
	}
}
