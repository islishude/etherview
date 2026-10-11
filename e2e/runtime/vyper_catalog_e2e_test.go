//go:build runtimee2e && hardhat3e2e

package runtimee2e

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The fixture serves common WASM packages verified against this production image.
// It uses a per-test signing key and CA; neither is a production trust root.
func configureVyperCatalogFixture(t *testing.T, h *harness) {
	t.Helper()
	directory := filepath.Join(h.root, ".local/vyper-releases")
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := fmt.Sprintf("https://host.docker.internal:%d", listener.Addr().(*net.TCPAddr).Port)
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"host.docker.internal"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	certificate, err := x509.CreateCertificate(rand.Reader, certTemplate, certTemplate, public, key)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(h.artifacts, "vyper-ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate}), 0o644); err != nil {
		t.Fatal(err)
	}
	var index struct {
		Versions []struct {
			Version string `json:"version"`
			Digest  string `json:"compiler_sha256"`
		} `json:"versions"`
	}
	raw, err := os.ReadFile(filepath.Join(h.root, "compiler/vyper/versions/index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &index); err != nil {
		t.Fatal(err)
	}
	evidence := readVyperMatrixEvidence(t, h.root)
	builds := make([]map[string]any, 0, len(index.Versions))
	for _, version := range index.Versions {
		name := "vyper-" + version.Version + "-emscripten-wasm32"
		descriptor, err := os.ReadFile(filepath.Join(directory, name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		descriptorHash := sha256.Sum256(descriptor)
		if evidence.Descriptors[name+".json"] != hex.EncodeToString(descriptorHash[:]) {
			t.Fatal("stale Vyper matrix descriptor")
		}
		var artifact map[string]any
		if err := json.Unmarshal(descriptor, &artifact); err != nil {
			t.Fatal(err)
		}
		archive, err := os.ReadFile(filepath.Join(directory, name+".tar.gz"))
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(archive)
		if artifact["sha256"] != hex.EncodeToString(hash[:]) || artifact["compiler_sha256"] != version.Digest {
			t.Fatal("Vyper release artifact changed")
		}
		matrix, ok := evidence.Matrix[version.Version]
		if !ok || matrix.PackageSHA256 != artifact["manifest_sha256"] || matrix.SharedSHA256 != artifact["shared_sha256"] {
			t.Fatal("Vyper package has no matching production matrix")
		}
		// The ephemeral catalog runs on this host only. Release signing requires
		// separate evidence and distinct host bindings from both Linux runners.
		artifacts := []map[string]any{{"platform": "emscripten-wasm32", "url": origin + "/" + name + ".tar.gz",
			"sha256": artifact["sha256"], "manifest_sha256": artifact["manifest_sha256"],
			"shared_sha256": artifact["shared_sha256"], "max_bytes": artifact["max_bytes"], "protocol": artifact["protocol"],
			"executor_digests": map[string]string{"linux-amd64": matrix.ExecutorSHA256, "linux-arm64": matrix.ExecutorSHA256}}}

		builds = append(builds, map[string]any{"version": version.Version, "compiler_sha256": version.Digest, "withdrawn": false, "runtimes": artifacts})
	}
	payload, err := json.Marshal(map[string]any{"schema": "etherview-vyper-catalog-v2", "expires_at": time.Now().Add(24 * time.Hour), "builds": builds})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(map[string]string{"payload": base64.StdEncoding.EncodeToString(payload), "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /catalog.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(envelope)
	})
	mux.Handle("GET /", http.FileServer(http.Dir(directory)))
	server := httptest.NewUnstartedServer(mux)
	_ = server.Listener.Close()
	server.Listener = listener
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{certificate}, PrivateKey: key}}}
	server.StartTLS()
	t.Cleanup(server.Close)
	environment := map[string]string{
		"SSL_CERT_FILE": "/etc/etherview/vyper-ca.pem",
		"ETHERVIEW_VERIFICATION_VYPER_CATALOG_URL":                      origin + "/catalog.json",
		"ETHERVIEW_VERIFICATION_VYPER_CATALOG_PUBLIC_KEY":               base64.StdEncoding.EncodeToString(public),
		"ETHERVIEW_VERIFICATION_ALLOWED_DOWNLOAD_ORIGINS":               "https://binaries.soliditylang.org," + origin,
		"ETHERVIEW_VERIFICATION_UNSAFE_ALLOW_PRIVATE_DOWNLOAD_NETWORKS": "true",
	}
	services := map[string]any{}
	for _, service := range []string{"api", "etherview"} {
		services[service] = map[string]any{"environment": environment, "volumes": []string{caPath + ":/etc/etherview/vyper-ca.pem:ro"}}
	}
	override, err := json.Marshal(map[string]any{"services": services})
	if err != nil {
		t.Fatal(err)
	}
	overridePath := filepath.Join(h.artifacts, "vyper-catalog.compose.json")
	if err := os.WriteFile(overridePath, override, 0o600); err != nil {
		t.Fatal(err)
	}
	h.project.Files = append(h.project.Files, overridePath)
}

type vyperMatrixCase struct {
	Cases          int    `json:"cases"`
	FixturesSHA256 string `json:"fixtures_sha256"`
	PackageSHA256  string `json:"package_sha256"`
	SharedSHA256   string `json:"shared_sha256"`
	ExecutorSHA256 string `json:"executor_sha256"`
}

type vyperMatrixEvidence struct {
	NativeLinux  bool                       `json:"native_linux"`
	ImageID      string                     `json:"image_id"`
	HostSHA256   string                     `json:"host_sha256"`
	SharedSHA256 string                     `json:"shared_sha256"`
	Descriptors  map[string]string          `json:"descriptors"`
	Matrix       map[string]vyperMatrixCase `json:"matrix"`
	Monolith     bool                       `json:"monolith"`
	Split        bool                       `json:"split"`
}

func readVyperMatrixEvidence(t *testing.T, root string) vyperMatrixEvidence {
	t.Helper()
	var evidence vyperMatrixEvidence
	raw, err := os.ReadFile(filepath.Join(root, ".local/vyper-releases", "matrix-"+runtime.GOARCH+".json"))
	if err != nil {
		t.Fatalf("run the production Vyper matrix before E2E: %v", err)
	}
	if err := json.Unmarshal(raw, &evidence); err != nil {
		t.Fatal(err)
	}
	image, err := exec.CommandContext(t.Context(), dockerCommand(), "image", "inspect", "--format", "{{.Id}}", valueOrDefault("IMAGE", "etherview:local")).Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(image)) != evidence.ImageID || len(evidence.Descriptors) != 26 || len(evidence.Matrix) != 26 {
		t.Fatal("incomplete or stale production Vyper matrix")
	}
	return evidence
}

func writeVyperReleaseAcceptance(t *testing.T, root string) {
	t.Helper()
	directory := filepath.Join(root, ".local/vyper-releases")
	platform := "linux-" + runtime.GOARCH
	evidence := readVyperMatrixEvidence(t, root)
	for name, expected := range evidence.Descriptors {
		raw, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != expected {
			t.Fatal("Vyper descriptor changed during E2E")
		}
	}
	evidence.Monolith, evidence.Split = true, true
	raw, err := json.Marshal(map[string]vyperMatrixEvidence{platform: evidence})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "acceptance-"+runtime.GOARCH+".json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
