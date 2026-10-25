//go:build runtimee2e

package runtimee2e

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/testcompose"
)

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
	files := []string{"deploy/compose.yaml", "e2e/runtime/compose.yaml", "e2e/runtime/verification.compose.yaml"}
	if extraCompose != "" {
		files = append(files, extraCompose)
	}
	project := testcompose.NewQuiet(root, testcompose.UniqueProjectName("etherview-"+suite+"-"+mode), files...)
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
