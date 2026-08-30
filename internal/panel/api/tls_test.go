package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/s2005lg/net-probe/internal/panel/pki"
)

func TestTLSServerAllowsBrowserAndVerifiesAgentCertificate(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host := listener.Addr().String()
	manager, err := pki.Ensure(t.TempDir(), "https://"+host)
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}

	d, cfg := openTestDB(t)
	panel := New(d, cfg)
	mux := http.NewServeMux()
	mux.HandleFunc("/verified-agent", func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) != 1 {
			http.Error(w, "Agent certificate was not verified", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/", panel.Routes())
	srv := NewTLSServer(host, mux, manager.TLSConfig())
	srv.ErrorLog = log.New(io.Discard, "", 0)
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.ServeTLS(listener, manager.ServerCertFile, manager.ServerKeyFile)
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
		if err := <-serveErr; err != nil && err != http.ErrServerClosed {
			t.Errorf("serve: %v", err)
		}
	})

	rootCAs := certPool(t, manager.CACertFile)
	baseURL := "https://" + host
	browser := tlsHTTPClient(rootCAs, nil)
	resp, err := browser.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("browser request without client certificate: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("browser status = %d", resp.StatusCode)
	}

	agentCert := issueClientCertificate(t, manager)
	agent := tlsHTTPClient(rootCAs, func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
		return &agentCert, nil
	})
	resp, err = agent.Get(baseURL + "/verified-agent")
	if err != nil {
		t.Fatalf("Agent request: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("Agent status = %d", resp.StatusCode)
	}

	wrongManager, err := pki.Ensure(t.TempDir(), "https://"+host)
	if err != nil {
		t.Fatal(err)
	}
	wrongCert := issueClientCertificate(t, wrongManager)
	wrongAgent := tlsHTTPClient(rootCAs, func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
		return &wrongCert, nil
	})
	if _, err := wrongAgent.Get(baseURL + "/verified-agent"); err == nil {
		t.Fatal("server accepted Agent certificate from the wrong CA")
	}
}

func certPool(t *testing.T, path string) *x509.CertPool {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read CA certificate: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(body) {
		t.Fatal("append CA certificate")
	}
	return pool
}

func issueClientCertificate(t *testing.T, manager *pki.Manager) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM, _, err := manager.IssueAgent("123e4567-e89b-42d3-a456-426614174000", "node-1", csrDER, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}

func tlsHTTPClient(rootCAs *x509.CertPool, getClientCertificate func(*tls.CertificateRequestInfo) (*tls.Certificate, error)) *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			MinVersion:           tls.VersionTLS13,
			RootCAs:              rootCAs,
			GetClientCertificate: getClientCertificate,
		}},
	}
}
