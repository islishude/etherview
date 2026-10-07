package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func certificate(t *testing.T) (string, string, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ipfs.preview.test"}, DNSNames: []string{"ipfs.preview.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	if err = os.WriteFile(certPath, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private}), 0600); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(certPEM)
	return certPath, keyPath, roots
}
func freeAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := l.Addr().String()
	_ = l.Close()
	return address
}
func awaitReady(t *testing.T, address string) {
	t.Helper()
	client := &http.Client{Timeout: 100 * time.Millisecond}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://" + address + "/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("gateway did not become ready")
}
func TestTLSAndLifecycle(t *testing.T) {
	cert, key, roots := certificate(t)
	o := options{address: freeAddress(t), healthAddress: freeAddress(t), upstream: "http://127.0.0.1:1", cert: cert, key: key}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serve(ctx, o) }()
	awaitReady(t, o.healthAddress)
	for _, test := range []struct {
		name, host string
		roots      *x509.CertPool
		max        uint16
		ok         bool
	}{
		{"trusted", "ipfs.preview.test", roots, tls.VersionTLS13, true},
		{"tls12", "ipfs.preview.test", roots, tls.VersionTLS12, true},
		{"untrusted", "ipfs.preview.test", x509.NewCertPool(), tls.VersionTLS13, false},
		{"hostname", "wrong.invalid", roots, tls.VersionTLS13, false},
		{"tls11", "ipfs.preview.test", roots, tls.VersionTLS11, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: test.roots, ServerName: test.host, MinVersion: tls.VersionTLS10, MaxVersion: test.max}, ForceAttemptHTTP2: true}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: time.Second}
			resp, err := client.Get("https://" + o.address + "/api/v0/version")
			if !test.ok {
				if err == nil {
					_ = resp.Body.Close()
					t.Fatal("TLS accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != 404 || resp.ProtoMajor != 2 {
				t.Fatalf("response %d protocol %s", resp.StatusCode, resp.Proto)
			}
		})
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown hung")
	}
	conn, err := net.DialTimeout("tcp", o.address, 100*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		t.Fatal("listener survived shutdown")
	}
}
func TestCertificateFailureBeforeListen(t *testing.T) {
	cert, key, _ := certificate(t)
	_, otherKey, _ := certificate(t)
	for _, test := range []struct{ cert, key string }{{"missing", key}, {cert, "missing"}, {cert, otherKey}} {
		address := freeAddress(t)
		err := serve(context.Background(), options{address: address, healthAddress: freeAddress(t), upstream: "http://ipfs:8080", cert: test.cert, key: test.key})
		if err == nil || err.Error() != "invalid_tls_certificate" {
			t.Fatalf("unexpected error %v", err)
		}
		l, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatal("bound before certificate validation")
		}
		_ = l.Close()
	}
}
func TestUpstreamValidation(t *testing.T) {
	for _, raw := range []string{"https://ipfs:8080", "http://user:pass@ipfs:8080", "http://ipfs:8080/api", "http://ipfs:8080?key=secret", "http://ipfs:8080#x", "http:///", "%"} {
		if _, err := upstreamURL(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestGracefulDrain(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "first")
		_ = http.NewResponseController(w).Flush()
		select {
		case <-release:
			_, _ = io.WriteString(w, "last")
		case <-r.Context().Done():
		}
	}))
	defer func() { once.Do(func() { close(release) }); upstream.Close() }()
	cert, key, roots := certificate(t)
	o := options{address: freeAddress(t), healthAddress: freeAddress(t), upstream: upstream.URL, cert: cert, key: key}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serve(ctx, o) }()
	awaitReady(t, o.healthAddress)
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: "ipfs.preview.test"}, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	resp, err := client.Get("https://" + o.address + "/ipfs/cid")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	first := make([]byte, 5)
	if _, err = io.ReadFull(resp.Body, first); err != nil {
		t.Fatal(err)
	}
	cancel()
	// Wait for health withdrawal, keeping the upstream response active.
	healthClient := &http.Client{Timeout: 100 * time.Millisecond}
	defer healthClient.CloseIdleConnections()
	deadline := time.Now().Add(time.Second)
	for {
		health, healthErr := healthClient.Get("http://" + o.healthAddress + "/health")
		if healthErr != nil {
			break
		}
		_ = health.Body.Close()
		if health.StatusCode != 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("readiness not withdrawn")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-done:
		t.Fatalf("shutdown did not drain active request: %v", err)
	default:
	}
	once.Do(func() { close(release) })
	rest, err := io.ReadAll(resp.Body)
	if err != nil || string(rest) != "last" {
		t.Fatalf("drain: %q %v", rest, err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown hung")
	}
}
