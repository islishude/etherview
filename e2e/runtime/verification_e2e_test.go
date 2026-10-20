//go:build runtimee2e && verificatione2e

package runtimee2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/testcompose"
)

const verificationE2ETimeout = 30 * time.Minute

// The group waits for both parallel children and their cleanups before the
// caller can compare snapshots or publish acceptance. Each child owns one slot.
func runVerificationTopologies[T any](t *testing.T, ctx context.Context, run func(*testing.T, string) T) ([2]T, bool) {
	t.Helper()
	var results [2]T
	var complete [2]bool
	if !t.Run("topologies", func(t *testing.T) {
		for index, mode := range []string{"monolith", "distributed"} {
			t.Run(mode, func(t *testing.T) {
				t.Parallel()
				started := time.Now()
				t.Cleanup(func() { t.Logf("topology %s including cleanup: %s", mode, time.Since(started)) })
				results[index] = run(t, mode)
				complete[index] = true
			})
		}
	}) {
		return results, false
	}
	if ctx.Err() != nil || !complete[0] || !complete[1] {
		t.Errorf("verification topology results incomplete or deadline exceeded")
		return results, false
	}
	if !reflect.DeepEqual(results[0], results[1]) {
		t.Errorf("verification topology parity mismatch\nmonolith: %#v\ndistributed: %#v", results[0], results[1])
		return results, false
	}
	return results, true
}

func startVerificationTopology(t *testing.T, ctx context.Context, root, mode, suite, extraCompose string, configure func(*harness)) *harness {
	t.Helper()
	artifacts, err := os.MkdirTemp("", "etherview-"+suite+"-e2e-"+mode+"-")
	if err != nil {
		t.Fatal(err)
	}
	project := testcompose.NewQuiet(
		root,
		testcompose.UniqueProjectName("etherview-"+suite+"-"+mode),
		"deploy/compose.yaml",
		"e2e/runtime/compose.yaml",
		"e2e/runtime/verification.compose.yaml",
		extraCompose,
	)
	project.Profiles = []string{mode}
	project.Env = runtimeEnvironment(root, uint64(time.Now().UTC().Truncate(time.Hour).Unix()), false)
	h := &harness{
		t: t, root: root, mode: mode, project: project,
		apiService: map[string]string{"monolith": "etherview", "distributed": "api"}[mode],
		http:       &http.Client{Timeout: 10 * time.Second},
		artifacts:  artifacts,
		phase:      "initialization",
	}
	t.Cleanup(func() {
		if !h.cleanup() && !t.Failed() {
			t.Error("verification topology failed or cleanup incomplete")
		}
	})

	if configure != nil {
		configure(h)
	}

	h.enterPhase("isolated Anvil and production topology")
	if err := project.Up(ctx, "runtime-fixture"); err != nil {
		t.Fatal(err)
	}
	binding, err := project.Port(ctx, "runtime-fixture", 8545)
	if err != nil {
		t.Fatal(err)
	}
	hostRPC := "http://" + binding
	h.rpc, err = rpc.DialContext(ctx, hostRPC)
	if err != nil {
		t.Fatal(err)
	}
	h.rpcProxy = startReceiptProxy(t, hostRPC)
	project.Env["ETHERVIEW_RUNTIME_RPC_URL"] = h.rpcProxy.containerURL()
	var genesis rpcBlock
	h.rpcCall(ctx, &genesis, "eth_getBlockByNumber", "0x0", false)
	h.fixture.genesisHash = genesis.Hash
	h.rpcCall(ctx, &h.fixture.accounts, "eth_accounts")
	h.mine(ctx, uint64(time.Now().Unix()))
	first := h.latestBlock(ctx)
	project.Env["ETHERVIEW_CHAIN_GENESIS_HASH"] = genesis.Hash
	h.startTopology(ctx)
	h.connectDatabase(ctx)
	h.resolveAPI(ctx)
	h.waitReady(ctx)
	h.waitCanonical(ctx, mustDecodeUint64(t, first.Number), first.Hash)

	return h
}

func createVerificationAPIKey(t *testing.T, ctx context.Context, h *harness) string {
	t.Helper()
	output, err := h.project.Run(ctx, "exec", "-T", h.apiService,
		"/etherview", "admin", "api-key", "create", "--config=/etc/etherview/config.yaml",
		"--name=verification-e2e", "--rate=100", "--burst=200")
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(output, &response); err != nil || response.Token == "" {
		t.Fatalf("decode production API-key output: %v", err)
	}
	return response.Token
}

func verificationEtherscanRequest(
	t *testing.T,
	ctx context.Context,
	h *harness,
	apiKey, method string,
	values url.Values,
) (json.RawMessage, error) {
	t.Helper()
	values.Set("chainid", "1")
	values.Set("module", "contract")
	var request *http.Request
	var err error
	if method == http.MethodPost {
		request, err = http.NewRequestWithContext(ctx, method, h.baseURL+"/v2/api", strings.NewReader(values.Encode()))
		if err == nil {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	} else {
		request, err = http.NewRequestWithContext(ctx, method, h.baseURL+"/v2/api?"+values.Encode(), nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-API-Key", apiKey)
	response, err := h.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close() //nolint:errcheck
	var envelope etherscanEnvelope
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return nil, err
	}
	var result string
	if response.StatusCode != http.StatusOK || envelope.Status != "1" {
		if err := json.Unmarshal(envelope.Result, &result); err != nil {
			return nil, fmt.Errorf("decode Etherscan error result: %w", err)
		}
		switch result {
		case "proxy implementation source code not verified":
			return nil, errHardhatImplementationUnverified
		case "proxy verification target unavailable":
			return nil, errHardhatProxyTargetUnavailable
		default:
			return nil, fmt.Errorf("Etherscan %s: status=%s message=%s result=%s",
				values.Get("action"), envelope.Status, envelope.Message, result)
		}
	}
	return envelope.Result, nil
}

func waitVerificationCanonicalTip(t *testing.T, ctx context.Context, h *harness) {
	t.Helper()
	latest := h.latestBlock(ctx)
	h.waitCanonical(ctx, mustDecodeUint64(t, latest.Number), latest.Hash)
}

func waitVerificationContractCode(t *testing.T, ctx context.Context, h *harness, address string) {
	t.Helper()
	waitFor(t, ctx, "canonical contract code "+address, func() (bool, string, error) {
		var count int64
		err := h.db.QueryRow(ctx, `
			SELECT count(*)
			FROM contract_code_observations AS observation
			JOIN canonical_blocks AS canonical
			  ON canonical.chain_id = observation.chain_id
			 AND canonical.number = observation.block_number
			 AND canonical.block_hash = observation.block_hash
			WHERE observation.chain_id = 1
			  AND observation.address = $1
			  AND observation.canonical
			  AND octet_length(observation.code) > 0`, common.HexToAddress(address).Bytes()).Scan(&count)
		return err == nil && count > 0, fmt.Sprintf("observations=%d", count), err
	})
}

func verificationImageID(t *testing.T, ctx context.Context) string {
	t.Helper()
	output, err := exec.CommandContext(ctx, dockerCommand(), "image", "inspect", "--format", "{{.Id}}", valueOrDefault("IMAGE", "etherview:local")).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(output))
}

// Only the parent calls this, after both children and all their cleanups finish.
func writeApplicationResult[T any](t *testing.T, ctx context.Context, root, suite, imageID string, results [2]T) {
	t.Helper()
	if ctx.Err() != nil || t.Failed() || verificationImageID(t, ctx) != imageID {
		t.Fatal("application evidence incomplete or image changed")
	}
	raw, err := json.Marshal(map[string]any{
		"schema": "etherview-application-result-v1", "role": suite,
		"native_linux": runtime.GOOS == "linux", "architecture": runtime.GOARCH,
		"image_id": imageID, "cleanup": true,
		"topologies": map[string]T{"monolith": results[0], "distributed": results[1]},
	})
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, ".local/production-results")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(directory, suite+".json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write(raw)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("write application evidence: %v; close: %v", writeErr, closeErr)
	}
}
