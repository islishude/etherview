//go:build runtimee2e

package runtimee2e

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/islishude/etherview/internal/api/gen"
)

// Only the worker receives the fixture's TLS root and source URL. The API
// accepts the import while the first remote lookup deliberately remains open.
func (h *harness) configureImportRecoverySource() *atomic.Int32 {
	certPath, keyPath, _ := writeRuntimeTLSKeyPair(h.t)
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		h.t.Fatal(err)
	}
	calls := new(atomic.Int32)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			select {
			case <-r.Context().Done():
			case <-h.t.Context().Done():
			}
			return
		}
		http.NotFound(w, r)
	}))
	_ = server.Listener.Close()
	server.Listener, err = net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		h.t.Fatal(err)
	}
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	h.t.Cleanup(server.Close)
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		h.t.Fatal(err)
	}
	// Keep bind mounts alive until the harness has removed its containers.
	rootPath := filepath.Join(h.artifacts, "import-root.pem")
	pem, err := os.ReadFile(certPath)
	if err != nil {
		h.t.Fatal(err)
	}
	if err = os.WriteFile(rootPath, pem, 0644); err != nil {
		h.t.Fatal(err)
	}
	override := map[string]any{"services": map[string]any{
		"api": map[string]any{"environment": map[string]string{"ETHERVIEW_FEATURE_SOURCIFY": "true"}},
		"verification": map[string]any{
			"environment": map[string]string{"ETHERVIEW_FEATURE_SOURCIFY": "true", "ETHERVIEW_SOURCIFY_BASE_URL": "https://host.docker.internal:" + port, "ETHERVIEW_SOURCIFY_UNSAFE_ALLOW_PRIVATE_NETWORKS": "true", "ETHERVIEW_LEASE_DURATION": "2s", "SSL_CERT_FILE": "/fixture/import-root.pem"},
			"volumes":     []map[string]any{{"type": "bind", "source": rootPath, "target": "/fixture/import-root.pem", "read_only": true}},
		},
	}}
	encoded, err := json.Marshal(override)
	if err != nil {
		h.t.Fatal(err)
	}
	path := filepath.Join(h.artifacts, "import-recovery.compose.json")
	if err = os.WriteFile(path, encoded, 0600); err != nil {
		h.t.Fatal(err)
	}
	h.project.Files = append(h.project.Files, path)
	return calls
}

func (h *harness) assertImportWorkerRecovery(ctx context.Context, session runtimeWatchSession, address string, calls *atomic.Int32) {
	data, _ := h.watchRequest(ctx, session, "POST", "/verification-jobs/sourcify-import", map[string]string{"address": address, "chain_id": "1"})
	var submitted gen.BrowserVerificationJobResponse
	if err := json.Unmarshal(data, &submitted); err != nil || submitted.Data.Id.String() == "00000000-0000-0000-0000-000000000000" {
		h.t.Fatalf("enqueue import: %s %v", data, err)
	}
	var owner string
	waitFor(h.t, ctx, "leased source lookup", func() (bool, string, error) {
		err := h.db.QueryRow(ctx, `SELECT COALESCE(leased_by,'') FROM verification_jobs WHERE id=$1::uuid`, submitted.Data.Id.String()).Scan(&owner)
		return owner != "" && calls.Load() > 0, owner, err
	})
	ids, err := h.project.Run(ctx, "ps", "-q", "verification")
	if err != nil {
		h.t.Fatal(err)
	}
	stopped := false
	for id := range strings.FieldsSeq(string(ids)) {
		if len(id) < 12 || !strings.HasPrefix(owner, id[:12]+"-") {
			continue
		}
		docker := os.Getenv("DOCKER")
		if docker == "" {
			docker = "docker"
		}
		output, err := exec.CommandContext(ctx, docker, "stop", "--time", "0", id).CombinedOutput()
		if err != nil {
			h.t.Fatalf("stop leased instance: %v %s", err, output)
		}
		stopped = true
		break
	}
	if !stopped {
		h.t.Fatalf("lease owner %s not found among test worker containers", owner)
	}
	waitFor(h.t, ctx, "surviving worker source takeover", func() (bool, string, error) {
		data, _ := h.watchRequest(ctx, session, "GET", "/verification-jobs/"+submitted.Data.Id.String(), nil)
		var result gen.BrowserVerificationJobResponse
		if err := json.Unmarshal(data, &result); err != nil {
			return false, "", err
		}
		if result.Data.Status != "failed" {
			return false, string(result.Data.Status), nil
		}
		if result.Data.ErrorCode == nil || *result.Data.ErrorCode != "sourcify_not_found" {
			return false, string(data), fmt.Errorf("unexpected import outcome")
		}
		return true, string(data), nil
	})
	var attempts int
	if err := h.db.QueryRow(ctx, `SELECT attempt_count FROM verification_jobs WHERE id=$1::uuid`, submitted.Data.Id.String()).Scan(&attempts); err != nil || attempts != 2 || calls.Load() != 2 {
		h.t.Fatalf("takeover attempts=%d calls=%d err=%v", attempts, calls.Load(), err)
	}
	// Restore the normal two-instance topology for the rest of the suite.
	h.compose(ctx, "up", "-d", "--no-recreate", "--wait", "--wait-timeout", "90", "--scale", "verification=2", "verification")
}
