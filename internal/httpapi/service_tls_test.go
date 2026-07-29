package httpapi

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/config"
)

func TestServiceServesHTTPSWithHTTP2AndGracefulShutdown(t *testing.T) {
	certificateFile, keyFile := writeTestTLSKeyPair(t)
	cfg := config.Default()
	cfg.Server.TLSCertFile = certificateFile
	cfg.Server.TLSKeyFile = keyFile
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(cfg, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	service.listen = func(_, _ string) (net.Listener, error) { return listener, nil }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			ForceAttemptHTTP2: true,
			TLSClientConfig: &tls.Config{
				MinVersion:         tls.VersionTLS12,
				InsecureSkipVerify: true, // The certificate is generated only for this local listener.
			},
		},
	}
	response, err := client.Get("https://" + listener.Addr().String() + "/health/live")
	if err != nil {
		cancel()
		t.Fatalf("HTTPS request: %v", err)
	}
	if err := response.Body.Close(); err != nil {
		cancel()
		t.Fatalf("close HTTPS response: %v", err)
	}
	if response.StatusCode != http.StatusNoContent || response.ProtoMajor != 2 {
		cancel()
		t.Fatalf("HTTPS response status=%d protocol=%s", response.StatusCode, response.Proto)
	}

	legacy := &tls.Config{
		InsecureSkipVerify: true, // The handshake must fail on protocol version before trust matters.
		MinVersion:         tls.VersionTLS11,
		MaxVersion:         tls.VersionTLS11,
	}
	if connection, err := tls.Dial("tcp", listener.Addr().String(), legacy); err == nil {
		if err := connection.Close(); err != nil {
			cancel()
			t.Fatalf("close legacy TLS connection: %v", err)
		}
		cancel()
		t.Fatal("TLS 1.1 handshake unexpectedly succeeded")
	}

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("service shutdown error = %v", err)
	}
}

func TestServiceRejectsInvalidTLSBeforeListening(t *testing.T) {
	directory := t.TempDir()
	certificateFile := filepath.Join(directory, "tls.crt")
	keyFile := filepath.Join(directory, "tls.key")
	if err := os.WriteFile(certificateFile, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Server.TLSCertFile = certificateFile
	cfg.Server.TLSKeyFile = keyFile
	service := NewService(cfg, http.NotFoundHandler())
	var listenCalls atomic.Int32
	service.listen = func(_, _ string) (net.Listener, error) {
		listenCalls.Add(1)
		return nil, errors.New("must not listen")
	}
	err := service.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "load API TLS key pair") {
		t.Fatalf("invalid TLS error = %v", err)
	}
	if listenCalls.Load() != 0 {
		t.Fatalf("listener called %d times", listenCalls.Load())
	}
}

func TestServiceRejectsPartialOrMismatchedTLSBeforeListening(t *testing.T) {
	certificateFile, _ := writeTestTLSKeyPair(t)
	_, unrelatedKeyFile := writeTestTLSKeyPair(t)
	for _, test := range []struct {
		name            string
		certificateFile string
		keyFile         string
		want            string
	}{
		{
			name:            "partial pair",
			certificateFile: certificateFile,
			want:            "must be configured together",
		},
		{
			name:            "mismatched pair",
			certificateFile: certificateFile,
			keyFile:         unrelatedKeyFile,
			want:            "load API TLS key pair",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Server.TLSCertFile = test.certificateFile
			cfg.Server.TLSKeyFile = test.keyFile
			service := NewService(cfg, http.NotFoundHandler())
			var listenCalls atomic.Int32
			service.listen = func(_, _ string) (net.Listener, error) {
				listenCalls.Add(1)
				return nil, errors.New("must not listen")
			}
			err := service.Run(context.Background())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("TLS error = %v, want %q", err, test.want)
			}
			if listenCalls.Load() != 0 {
				t.Fatalf("listener called %d times", listenCalls.Load())
			}
		})
	}
}

func writeTestTLSKeyPair(t *testing.T) (string, string) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	certificateFile := filepath.Join(directory, "tls.crt")
	keyFile := filepath.Join(directory, "tls.key")
	if err := os.WriteFile(certificateFile, pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: certificateDER,
	}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{
		Type: "PRIVATE KEY", Bytes: privateKeyDER,
	}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certificateFile, keyFile
}
