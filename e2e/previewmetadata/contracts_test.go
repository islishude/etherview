//go:build previewmetadatae2e

package previewmetadatae2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/islishude/etherview/internal/api/gen"
)

type contractScenarioReport struct {
	Phase                     string `json:"phase"`
	Genesis                   string `json:"genesis"`
	Name                      string `json:"name"`
	NameOwner                 string `json:"nameOwner"`
	SmartAccount              string `json:"smartAccount"`
	UserOperationHash         string `json:"userOperationHash"`
	TransactionHash           string `json:"transactionHash"`
	RevertedUserOperationHash string `json:"revertedUserOperationHash"`
	RevertedTransactionHash   string `json:"revertedTransactionHash"`
}

func TestPreviewContracts(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 18*time.Minute)
	defer cancel()
	h := newHarness(t)
	t.Cleanup(h.cleanup)
	// Use the shipped configuration, with unrelated remote discovery disabled.
	data, err := os.ReadFile(filepath.Join(h.root, "preview/config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := strings.ReplaceAll(string(data), "  sourcify: true", "  sourcify: false")
	cfg = strings.ReplaceAll(cfg, "bytecode_db:\n  enabled: true", "bytecode_db:\n  enabled: false")
	cfg = strings.ReplaceAll(cfg, "resolution_freshness: 15m", "resolution_freshness: 1m")
	configFile := filepath.Join(h.artifacts, "contracts.config.yaml")
	// The bind-mounted fixture contains no secrets and must be readable by the image UID.
	if err = os.WriteFile(configFile, []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}
	h.project.Env["ETHERVIEW_CONFIG_FILE"] = configFile
	h.prepareNetwork(ctx)
	if _, err = h.project.Run(ctx, "up", "-d", "--no-build", "--wait", "--wait-timeout", "180"); err != nil {
		t.Fatal(err)
	}
	h.initializeArchive(ctx)
	h.connect(ctx)
	h.assertSlotDuration(ctx)
	waitFor(t, ctx, "real PoS block production", func() (bool, string, error) {
		var head hexutil.Uint64
		err := h.rpc.CallContext(ctx, &head, "eth_blockNumber")
		return uint64(head) > 1, fmt.Sprint(head), err
	})
	waitFor(t, ctx, "authenticated genesis import", func() (bool, string, error) {
		var state string
		err := h.db.QueryRow(ctx, "SELECT state FROM genesis_state_imports WHERE chain_id=48815").Scan(&state)
		return state == "complete", state, err
	})
	h.assertBundlerBoundary(ctx)
	for _, phase := range []string{"split", "all"} {
		if phase == "all" {
			h.contractsAllRoles(ctx)
		}
		rpcBinding, err := h.project.Port(ctx, "geth", 8545)
		if err != nil {
			t.Fatal(err)
		}
		bundlerBinding, err := h.project.Port(ctx, "bundler", 4337)
		if err != nil {
			t.Fatal(err)
		}
		reportPath := filepath.Join(h.artifacts, "contracts-"+phase+".json")
		command := exec.CommandContext(ctx, "node", "preview/contracts/scenario.mjs", "http://"+rpcBinding, "http://"+bundlerBinding, phase, reportPath)
		command.Dir = h.root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("contract scenario %s: %v\n%s", phase, err, output)
		}
		data, err := os.ReadFile(reportPath)
		if err != nil {
			t.Fatal(err)
		}
		var report contractScenarioReport
		if err = json.Unmarshal(data, &report); err != nil {
			t.Fatal(err)
		}
		h.assertContractAPI(ctx, report)
		h.assertContractsBrowser(ctx, reportPath)
		// Recreate containers against the same network and database, then reread all facts.
		if _, err = h.project.Run(ctx, "up", "-d", "--no-build", "--no-deps", "--force-recreate", "--wait", "--wait-timeout", "180", "bundler", "api", "preview-gateway"); err != nil {
			t.Fatal(err)
		}
		h.connect(ctx)
		h.assertContractAPI(ctx, report)
		h.assertBundlerBoundary(ctx)
	}
	if _, err := h.project.Run(ctx, "stop", "bundler"); err != nil {
		t.Fatal(err)
	}
	// The real PoS chain keeps producing blocks after Bundler shutdown. A probe
	// can observe core catch-up (503), so require readiness within the existing
	// bounded wait while the Bundler stays stopped, rather than at one instant.
	waitFor(t, ctx, "core readiness without Bundler", func() (bool, string, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.apiURL+"/health/ready", nil)
		if err != nil {
			return false, "", err
		}
		response, err := h.http.Do(request)
		if err != nil {
			return false, "", err
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
		closeErr := response.Body.Close()
		detail := fmt.Sprintf("HTTP %d: %s", response.StatusCode, body)
		if readErr != nil {
			return false, detail, readErr
		}
		if closeErr != nil {
			return false, detail, closeErr
		}
		switch response.StatusCode {
		case http.StatusOK:
			return true, detail, nil
		case http.StatusServiceUnavailable:
			return false, detail, nil
		default:
			return false, detail, fmt.Errorf("unexpected readiness status %d", response.StatusCode)
		}
	})
}

func (h *harness) contractsAllRoles(ctx context.Context) {
	if _, err := h.project.Run(ctx, "stop", "sync", "enrich", "trace", "metadata", "maintenance"); err != nil {
		h.t.Fatal(err)
	}
	override := filepath.Join(h.artifacts, "contracts-all.yaml")
	config := `services:
  api:
    volumes:
      - ${PREVIEW_NETWORK_DIR}/metadata/ens.json:/etc/etherview/ens/current.json:ro
    command: [serve, --roles=all, --config=/etc/etherview/config.yaml]
    environment:
      ETHERVIEW_ROLES: all
      ETHERVIEW_CONSENSUS_BLOBS_ACCESS_KEY: preview-writer
      ETHERVIEW_CONSENSUS_BLOBS_SECRET_KEY: preview-write-dev-only
`
	if err := os.WriteFile(override, []byte(config), 0600); err != nil {
		h.t.Fatal(err)
	}
	h.project.Files = append(h.project.Files, override)
	if _, err := h.project.Run(ctx, "up", "-d", "--no-build", "--no-deps", "--wait", "--wait-timeout", "180", "api"); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) assertContractAPI(ctx context.Context, report contractScenarioReport) {
	// Registration must reach a verified publication before the first name HTTP lookup.
	waitFor(h.t, ctx, "autonomous ENS publication", func() (bool, string, error) {
		var count int
		err := h.db.QueryRow(ctx, `SELECT count(*) FROM ens_effective_publications WHERE target_chain_id=48815 AND direction='forward' AND name=$1 AND ens_effective_publication_current(id)`, report.Name).Scan(&count)
		return count > 0, fmt.Sprint(count), err
	})
	for _, operation := range []struct {
		hash, transaction string
		success           bool
	}{{report.UserOperationHash, report.TransactionHash, true}, {report.RevertedUserOperationHash, report.RevertedTransactionHash, false}} {
		waitFor(h.t, ctx, "indexed real UserOperation", func() (bool, string, error) {
			var result struct {
				Data struct {
					Hash        string `json:"hash"`
					Success     bool   `json:"success"`
					Transaction string `json:"transaction_hash"`
					Version     string `json:"entry_point_version"`
				}
			}
			err := h.getJSON(ctx, h.apiURL+"/api/v1/user-operations/"+operation.hash, &result)
			return err == nil && strings.EqualFold(result.Data.Hash, operation.hash) && result.Data.Success == operation.success && strings.EqualFold(result.Data.Transaction, operation.transaction) && result.Data.Version == "0.9", fmt.Sprintf("%+v %v", result.Data, err), nil
		})
		var count int
		if err := h.db.QueryRow(ctx, "SELECT count(*) FROM published_erc4337_user_operations WHERE chain_id=48815 AND user_op_hash=decode($1,'hex')", strings.TrimPrefix(operation.hash, "0x")).Scan(&count); err != nil || count != 1 {
			h.t.Fatalf("operation publication count=%d: %v", count, err)
		}
	}
	waitFor(h.t, ctx, "local primary name API", func() (bool, string, error) {
		var result struct {
			Data struct {
				Items []struct {
					State   string `json:"state"`
					Primary *struct {
						Name string `json:"name"`
					} `json:"primary_name"`
				} `json:"items"`
			}
		}
		err := h.getJSON(ctx, h.apiURL+"/api/v1/address-names?addresses="+report.NameOwner, &result)
		if err != nil || len(result.Data.Items) != 1 {
			return false, fmt.Sprint(err), nil
		}
		item := result.Data.Items[0]
		return item.State == "resolved" && item.Primary != nil && item.Primary.Name == report.Name, fmt.Sprintf("%+v", item), nil
	})
	waitFor(h.t, ctx, "local forward name search", func() (bool, string, error) {
		var result struct {
			Data []struct {
				Key   string `json:"key"`
				Label string `json:"label"`
			} `json:"data"`
		}
		err := h.getJSON(ctx, h.apiURL+"/api/v1/search?q="+report.Name, &result)
		for _, item := range result.Data {
			if item.Key == report.Name && item.Label == report.Name {
				return true, "name directory published", nil
			}
		}
		return false, fmt.Sprintf("%+v %v", result, err), nil
	})
	waitFor(h.t, ctx, "name registration profile", func() (bool, string, error) {
		var response struct {
			Data struct {
				Registrant struct {
					State string
					Value string
				}
				ProfileObservation any `json:"profile_observation"`
			}
		}
		err := h.getJSON(ctx, h.apiURL+"/api/v1/names/"+report.Name, &response)
		return err == nil && response.Data.ProfileObservation != nil && response.Data.Registrant.State == "value" && strings.EqualFold(response.Data.Registrant.Value, report.NameOwner), fmt.Sprintf("%+v %v", response.Data, err), nil
	})

	waitFor(h.t, ctx, "name operation history", func() (bool, string, error) {
		var history gen.NameHistoryResponse
		err := h.getJSON(ctx, h.apiURL+"/api/v1/names/"+report.Name+"/history", &history)
		return err == nil && len(history.Data.Items) > 0 && history.Data.Snapshot != "", fmt.Sprintf("%+v %v", history.Data, err), nil
	})

	var total, sources int
	if err := h.db.QueryRow(ctx, "SELECT count(*),count(DISTINCT s.id) FROM ens_sync_observations o JOIN ens_sync_sources s ON s.id=o.source_id WHERE s.target_chain_id=48815").Scan(&total, &sources); err != nil || total == 0 || sources != 1 {
		h.t.Fatalf("ENS background observations total=%d sources=%d: %v", total, sources, err)
	}
}

func (h *harness) assertBundlerBoundary(ctx context.Context) {
	for _, method := range []string{"eth_supportedEntryPoints", "debug_bundler_clearState"} {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": []any{}})
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://bundler.etherview.localhost", bytes.NewReader(body))
		if err != nil {
			h.t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := h.http.Do(request)
		if err != nil {
			h.t.Fatal(err)
		}
		var result struct {
			Result []string `json:"result"`
			Error  *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		err = json.NewDecoder(response.Body).Decode(&result)
		if closeErr := response.Body.Close(); closeErr != nil {
			h.t.Fatal(closeErr)
		}
		if err != nil {
			h.t.Fatal(err)
		}
		if method == "eth_supportedEntryPoints" {
			if len(result.Result) != 1 || !strings.EqualFold(result.Result[0], "0x433709009b8330fda32311df1c2afa402ed8d009") {
				h.t.Fatalf("gateway EntryPoints: %+v", result)
			}
		} else if result.Error == nil {
			h.t.Fatal("gateway exposed Bundler debug method")
		}
	}
}

func (h *harness) assertContractsBrowser(ctx context.Context, report string) {
	binding, err := h.project.Port(ctx, "preview-gateway", 443)
	if err != nil {
		h.t.Fatal(err)
	}
	command := exec.CommandContext(ctx, "node", "web/scripts/preview-contracts-browser.mjs", binding, filepath.Join(h.root, ".local/preview-tls/gateway.crt"), report, h.artifacts)
	command.Dir = h.root
	if output, err := command.CombinedOutput(); err != nil {
		h.t.Fatalf("contracts browser: %v\n%s", err, output)
	}
}
