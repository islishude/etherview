package facilitator

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"gopkg.in/yaml.v3"
)

func configFixture(t *testing.T) Config {
	t.Helper()
	clearCredentialEnvironment(t)
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	c := testConfig()
	c.Database.URLFile = write("db", "postgres://localhost:1/unreachable")
	c.Auth.BearerTokenFile = write("token", strings.Repeat("s", 32))
	signer := c.Signers["key"]
	// CheckConfig must not decrypt this intentionally invalid keystore.
	signer.KeystoreFile = write("key", "not-an-encrypted-key")
	signer.PasswordFile = write("pass", "password")
	c.Signers["key"] = signer
	c.Chains[0].RPC = RPCConfig{URL: "https://localhost:1"}
	return c
}
func writeConfig(t *testing.T, c Config) string {
	t.Helper()
	b, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err = os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestConfigSourcesAndTransportIndependence(t *testing.T) {
	c := configFixture(t)
	if err := CheckConfig(c); err != nil {
		t.Fatal(err)
	}
	c.Chains[0].RPC.URL = "http://localhost:1"
	if c.Validate() == nil {
		t.Fatal("listener plaintext enabled RPC plaintext")
	}
	c.Chains[0].RPC.AllowInsecureHTTP = true
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Server.AllowInsecureHTTP = false
	if c.Validate() == nil {
		t.Fatal("RPC plaintext disabled listener TLS requirement")
	}
	c.Server.TLSCertFile = "certificate"
	c.Server.TLSKeyFile = "key"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Chains[0].RPC.URLFile = "other"
	if c.Validate() == nil {
		t.Fatal("accepted two sources")
	}
	c.Chains[0].RPC.URL = ""
	c.Chains[0].RPC.URLFile = ""
	if c.Validate() == nil {
		t.Fatal("accepted no source")
	}
}
func TestStrictConfigAndRedaction(t *testing.T) {
	c := configFixture(t)
	path := writeConfig(t, c)
	base, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"unknown":   string(base) + "marker_SECRET: token\n",
		"legacy":    string(base) + "unsafe_local_http: true\n",
		"duplicate": string(base) + "auth: {bearer_token_file: marker_SECRET}\n",
		"multiple":  string(base) + "---\n{}\n",
		"oversized": strings.Repeat("x", (1<<20)+1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadConfig(path)
			var cfgErr *ConfigError
			if !errors.As(err, &cfgErr) || strings.Contains(err.Error(), "marker_SECRET") {
				t.Fatalf("unsafe or missing error: %v", err)
			}
		})
	}
	c.Chains[0].RPC.URL = "https://marker_SECRET@%"
	if err := c.Validate(); err == nil || strings.Contains(err.Error(), "marker_SECRET") {
		t.Fatal("unsafe URL error")
	}
}
func TestBoundedFilesAndOfflineCheck(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	c := configFixture(t)
	c.Chains[0].RPC = RPCConfig{URL: server.URL, AllowInsecureHTTP: true}
	if err := CheckConfig(c); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("configuration check called RPC")
	}
	path := filepath.Join(t.TempDir(), "rpc")
	c.Chains[0].RPC = RPCConfig{URLFile: path}
	for _, body := range []string{"", strings.Repeat("x", 8193), "  ", "https://marker_SECRET@%"} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		err := CheckConfig(c)
		var ce *ConfigError
		if !errors.As(err, &ce) || ce.Field != "chains[0].rpc" && ce.Field != "chains[0].rpc.url_file" || strings.Contains(err.Error(), "marker_SECRET") {
			t.Fatalf("unsafe error: %v", err)
		}
	}
	if err := os.WriteFile(path, []byte("https://localhost:1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CheckConfig(c); err != nil {
		t.Fatal(err)
	}
}
func TestCloseIsReverseAndConcurrentIdempotent(t *testing.T) {
	var order []int
	s := &Service{token: []byte("secret")}
	for i := range 4 {
		s.cleanups = append(s.cleanups, func() { order = append(order, i) })
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(s.Close)
	}
	wg.Wait()
	if !reflect.DeepEqual(order, []int{3, 2, 1, 0}) {
		t.Fatal(order)
	}
	for _, v := range s.token {
		if v != 0 {
			t.Fatal("token retained")
		}
	}
}

func TestRepositoryFacilitatorConfigs(t *testing.T) {
	for _, path := range []string{"../../preview/facilitator/config.yaml", "../../e2e/x402local/facilitator.yaml", "../../deploy/facilitator/config.example.yaml"} {
		t.Run(path, func(t *testing.T) {
			c, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(path, "config.example") {
				return
			}
			for _, chain := range c.Chains {
				if chain.RPC.URL == "" || chain.RPC.URLFile != "" {
					t.Fatal("local RPC must use ordinary inline configuration")
				}
			}
		})
	}
}

func TestOfflineTLSAndKMSConfiguration(t *testing.T) {
	c := configFixture(t)
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	certificate := server.TLS.Certificates[0]
	key, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	c.Server.AllowInsecureHTTP = false
	c.Server.TLSCertFile = filepath.Join(t.TempDir(), "cert.pem")
	c.Server.TLSKeyFile = filepath.Join(t.TempDir(), "key.pem")
	if err = os.WriteFile(c.Server.TLSCertFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(c.Server.TLSKeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600); err != nil {
		t.Fatal(err)
	}
	c.Signers = map[string]SignerConfig{"key": {Type: "aws_kms", Address: c.Signers["key"].Address, Region: "us-east-1", KeyARN: "arn:aws:kms:us-east-1:123456789012:key/offline"}}
	t.Setenv("AWS_ENDPOINT_URL_KMS", server.URL)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	if err = CheckConfig(c); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("offline check contacted KMS")
	}
	if err = os.WriteFile(c.Server.TLSKeyFile, []byte("marker_SECRET"), 0600); err != nil {
		t.Fatal(err)
	}
	err = CheckConfig(c)
	var ce *ConfigError
	if !errors.As(err, &ce) || ce.Code != "invalid_tls" || strings.Contains(err.Error(), "marker_SECRET") {
		t.Fatal("unsafe TLS diagnostic", err)
	}
}
