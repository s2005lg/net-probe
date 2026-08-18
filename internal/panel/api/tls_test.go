package api

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateSelfSigned(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	if err := GenerateSelfSigned(certPath, keyPath); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := os.Stat(certPath); err != nil {
		t.Fatalf("cert stat: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("key stat: %v", err)
	}

	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		t.Fatalf("key pair: %v", err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("no cert PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	if len(cert.DNSNames) != 1 || cert.DNSNames[0] != "net-probe-panel" {
		t.Fatalf("dns names = %v", cert.DNSNames)
	}
}
