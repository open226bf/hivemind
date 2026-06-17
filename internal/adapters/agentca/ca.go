// Package agentca is a minimal internal certificate authority for the agent
// connection mode. Hivemind signs a client certificate for each enrolled agent
// and a server certificate for the agent-hub TLS listener, both chaining to the
// same CA — so the tunnel is mutually authenticated (the agent proves identity
// with its client cert; the server is verified against the CA).
package agentca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"
)

// CA holds the signing certificate and key.
type CA struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certPEM []byte
	keyPEM  []byte
}

// Generate creates a fresh self-signed CA (ECDSA P-256, 10-year validity).
func Generate() (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          randSerial(),
		Subject:               pkix.Name{CommonName: "Hivemind Agent CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	return load(der, key)
}

// Load rebuilds a CA from its persisted PEM material.
func Load(certPEM, keyPEM []byte) (*CA, error) {
	blk, _ := pem.Decode(certPEM)
	if blk == nil {
		return nil, fmt.Errorf("ca: invalid cert PEM")
	}
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return nil, err
	}
	kblk, _ := pem.Decode(keyPEM)
	if kblk == nil {
		return nil, fmt.Errorf("ca: invalid key PEM")
	}
	key, err := x509.ParseECPrivateKey(kblk.Bytes)
	if err != nil {
		return nil, err
	}
	return &CA{cert: cert, key: key, certPEM: certPEM, keyPEM: keyPEM}, nil
}

func load(certDER []byte, key *ecdsa.PrivateKey) (*CA, error) {
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return &CA{
		cert:    cert,
		key:     key,
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}),
		keyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	}, nil
}

// CertPEM returns the CA certificate (safe to distribute).
func (c *CA) CertPEM() []byte { return c.certPEM }

// KeyPEM returns the CA private key (sensitive — stored encrypted).
func (c *CA) KeyPEM() []byte { return c.keyPEM }

// Pool returns a cert pool trusting this CA, for verifying peer certs.
func (c *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(c.cert)
	return p
}

// IssueClient signs a client certificate with the given common name (the agent's
// cluster id) and returns its PEM material plus the serial (for revocation).
func (c *CA) IssueClient(commonName string, ttl time.Duration) (certPEM, keyPEM []byte, serial string, err error) {
	return c.issue(commonName, ttl, x509.ExtKeyUsageClientAuth, nil)
}

// IssueServerTLS signs a server certificate for the hub listener (SANs = hosts)
// and returns it as a tls.Certificate.
func (c *CA) IssueServerTLS(hosts []string, ttl time.Duration) (tls.Certificate, error) {
	certPEM, keyPEM, _, err := c.issue("hivemind-agent-hub", ttl, x509.ExtKeyUsageServerAuth, hosts)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(certPEM, keyPEM)
}

func (c *CA) issue(cn string, ttl time.Duration, eku x509.ExtKeyUsage, hosts []string) (certPEM, keyPEM []byte, serial string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, "", err
	}
	sn := randSerial()
	tmpl := &x509.Certificate{
		SerialNumber: sn,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(ttl),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{eku},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		return nil, nil, "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, "", err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, sn.String(), nil
}

func randSerial() *big.Int {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, _ := rand.Int(rand.Reader, limit)
	return n
}
