package main

import (
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"
)

func TestGenerateCertificate(t *testing.T) {
	certificate, caPEM, err := generateCertificate(time.Now())
	if err != nil {
		t.Fatalf("generateCertificate() error = %v", err)
	}
	block, _ := pem.Decode(caPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatal("generated CA is not a PEM certificate")
	}
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}
	if !ca.IsCA {
		t.Fatal("generated CA certificate is not a CA")
	}
	if len(certificate.Certificate) == 0 {
		t.Fatal("generated TLS certificate chain is empty")
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		t.Fatalf("ParseCertificate() leaf error = %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	for _, name := range []string{"localhost", "127.0.0.1"} {
		if _, err := leaf.Verify(x509.VerifyOptions{
			DNSName:   name,
			Roots:     roots,
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}); err != nil {
			t.Fatalf("Verify(%q) error = %v", name, err)
		}
	}
	if leaf.PublicKeyAlgorithm != x509.ECDSA {
		t.Fatalf("leaf public key algorithm = %v, want ECDSA", leaf.PublicKeyAlgorithm)
	}
}
