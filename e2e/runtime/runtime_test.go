//go:build runtimee2e

package runtimee2e

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/holiman/uint256"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/loadtest"
	"github.com/islishude/etherview/internal/testcompose"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	runtimeTimeout            = 15 * time.Minute
	waitTimeout               = 3 * time.Minute
	runtimeServerWriteTimeout = 2 * time.Second

	// NoCBOR.sol is the repository's reviewed Solidity 0.8.30 fixture from
	// internal/verify/testdata/compiler/solidity/output.no-cbor.json. Deploying
	// it exercises receipt-authenticated contract creation without introducing
	// another compiler or network dependency into this test.
	noCBORCreationBytecode   = "0x6080604052348015600e575f5ffd5b50603e80601a5f395ff3fe6080604052348015600e575f5ffd5b50600436106026575f3560e01c80633fa4f24514602a575b5f5ffd5b600760405190815260200160405180910390f3"
	noCBORRuntimeBytecode    = "0x6080604052348015600e575f5ffd5b50600436106026575f3560e01c80633fa4f24514602a575b5f5ffd5b600760405190815260200160405180910390f3"
	nativeTransferTarget     = "0x00000000000000000000000000000000000000F0"
	runtimeCompoundSignature = "configure((address,uint256),uint8[2][])"
	runtimeCompoundABIEntry  = `{"type":"function","name":"configure","inputs":[{"name":"config","type":"tuple","internalType":"struct Fixture.Config","components":[{"name":"owner","type":"address"},{"name":"amount","type":"uint256"}]},{"name":"pairs","type":"uint8[2][]"}],"outputs":[]}`
	runtimeENSName           = "runtime.custom"
	runtimeENSRegistry       = "0xE000000000000000000000000000000000000001"
	runtimeENSResolver       = "0xE000000000000000000000000000000000000002"
	runtimeCompoundCalldata  = "0xe967f546" +
		"0000000000000000000000004444444444444444444444444444444444444444" +
		"000000000000000000000000000000000000000000000000000000000000002a" +
		"0000000000000000000000000000000000000000000000000000000000000060" +
		"0000000000000000000000000000000000000000000000000000000000000002" +
		"0000000000000000000000000000000000000000000000000000000000000001" +
		"0000000000000000000000000000000000000000000000000000000000000002" +
		"0000000000000000000000000000000000000000000000000000000000000003" +
		"0000000000000000000000000000000000000000000000000000000000000004"
)

type fixture struct {
	genesisHash            string
	accounts               []string
	authorityKey           *ecdsa.PrivateKey
	sponsorKey             *ecdsa.PrivateKey
	skippedKey             *ecdsa.PrivateKey
	authority              string
	sponsor                string
	skippedAuthority       string
	nativeHash             string
	pendingHash            string
	pendingReplacementHash string
	pendingNonce           uint64
	creationHash           string
	failedHash             string
	compoundHash           string
	contractAddress        string
	nftCreationHash        string
	nftAddress             string
	delegateA              string
	delegateB              string
	orphanDelegationHash   string
	delegationHash         string
	delegationAuths        []types.SetCodeAuthorization
	redelegationHash       string
	redelegationAuth       types.SetCodeAuthorization
	delegatedCallHash      string
	clearingHash           string
	clearingAuth           types.SetCodeAuthorization
	entryPoint             string
	entryPointCreationHash string
	userOperationHash      string
	userOperationTxHash    string
	blockOneHash           string
	orphanHash             string
	finalHash              string
	finalHeight            uint64
	snapshotID             string
}

type durableSnapshot struct {
	GenesisHash          string
	Canonical            string
	BlockCount           int64
	TransactionCount     int64
	ReceiptCount         int64
	CompleteStages       int64
	FailedStages         int64
	UnpublishedOutbox    int64
	Checkpoint           string
	OrphanCount          int64
	Rollup               string
	Statistics           string
	Authorizations       int64
	OrphanAuthorizations int64
	ENSObservations      int64
	UserOperations       int64
}

type apiSnapshot struct {
	ChainID                   string
	IndexedBlock              string
	LatestBlock               string
	CoreReady                 bool
	BackfillComplete          bool
	Features                  map[string]bool
	BlockHashes               []string
	TransactionHashes         []string
	FromType                  string
	ContractType              string
	CreationAddress           string
	FailedStatus              string
	FailureState              string
	FailureDecodingStatus     string
	FailureError              string
	TraceState                string
	CalldataInput             string
	CalldataResolution        string
	CalldataStatus            string
	CompoundCalldataSignature string
	CompoundCalldataShape     string
	NativeMethod              string
	NativeCalldataResolution  string
	NativeCalldataStatus      string
	ChartAvailable            bool
	SPA                       bool
	SSE                       bool
	AuthorityType             string
	HasDelegationHistory      bool
	DelegationStatus          string
	DelegationHistory         string
	AuthorizationOutcomes     string
	DelegatedResolution       string
	DelegatedExecutionAddress string
	ClearingResolution        string
	ClearingStatus            string
	ENSName                   string
	ENSNameSource             string
	EtherscanAdvanced         string
	EtherscanFunding          string
	EtherscanBlockCounts      string
	EtherscanNFTHoldings      string
	UserOperationHash         string
}

type harness struct {
	t             *testing.T
	root          string
	mode          string
	phase         string
	project       *testcompose.Project
	apiService    string
	baseURL       string
	rpc           *rpc.Client
	db            *pgxpool.Pool
	http          *http.Client
	artifacts     string
	fixture       fixture
	baseTimestamp uint64
	rpcProxy      *receiptProxy
	ensRPC        *ensMainnetRPC
	secrets       []string
}

func TestProductionComposeRuntimeE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), runtimeTimeout)
	defer cancel()
	root := repositoryRoot(t)
	baseTimestamp := uint64(time.Now().UTC().Truncate(time.Hour).Unix())

	results := make(map[string]modeResult, 2)
	for _, mode := range []string{"monolith", "distributed"} {
		if !t.Run(mode, func(t *testing.T) {
			results[mode] = runMode(t, ctx, root, mode, baseTimestamp)
		}) {
			return
		}
	}
	if !reflect.DeepEqual(results["monolith"].durable, results["distributed"].durable) {
		t.Fatalf("durable parity mismatch\nmonolith: %#v\ndistributed: %#v",
			results["monolith"].durable, results["distributed"].durable)
	}
	if !reflect.DeepEqual(results["monolith"].api, results["distributed"].api) {
		t.Fatalf("public API parity mismatch\nmonolith: %#v\ndistributed: %#v",
			results["monolith"].api, results["distributed"].api)
	}
}

func TestNormalizeAnvilReceipts(t *testing.T) {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"result": []any{
			map[string]any{
				"transactionHash": "0x01",
				"blobGasPrice":    "0x1",
			},
			map[string]any{
				"transactionHash": "0x02",
				"blobGasPrice":    "0x2",
				"blobGasUsed":     "0x3",
			},
		},
	}
	if got := normalizeAnvilReceipts(payload); got != 1 {
		t.Fatalf("normalized receipts = %d, want 1", got)
	}
	receipts := payload["result"].([]any)
	first := receipts[0].(map[string]any)
	if _, present := first["blobGasPrice"]; present {
		t.Fatal("orphan blobGasPrice was not removed")
	}
	second := receipts[1].(map[string]any)
	if second["blobGasPrice"] != "0x2" || second["blobGasUsed"] != "0x3" {
		t.Fatalf("complete blob fee observation changed: %#v", second)
	}
}

func TestNormalizeAnvilClearedDelegations(t *testing.T) {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"result": map[string]any{
			"pre": map[string]any{
				"0x0000000000000000000000000000000000000001": map[string]any{
					"nonce": "0x2", "code": "0xef01000000000000000000000000000000000000000002",
				},
				"0x0000000000000000000000000000000000000003": map[string]any{
					"nonce": "0x2", "code": "0x6000",
				},
			},
			"post": map[string]any{
				"0x0000000000000000000000000000000000000001": map[string]any{"nonce": "0x3"},
				"0x0000000000000000000000000000000000000003": map[string]any{"nonce": "0x3"},
			},
		},
	}
	if got := normalizeAnvilClearedDelegations(payload); got != 1 {
		t.Fatalf("normalized cleared delegations = %d, want 1", got)
	}
	result := payload["result"].(map[string]any)
	post := result["post"].(map[string]any)
	cleared := post["0x0000000000000000000000000000000000000001"].(map[string]any)
	if cleared["code"] != "0x" {
		t.Fatalf("cleared delegation post-state = %#v", cleared)
	}
	ordinary := post["0x0000000000000000000000000000000000000003"].(map[string]any)
	if _, present := ordinary["code"]; present {
		t.Fatalf("ordinary account post-state was changed: %#v", ordinary)
	}
}

func TestRuntimeTLSKeyPairPermissions(t *testing.T) {
	certificateFile, keyFile, _ := writeRuntimeTLSKeyPair(t)
	for _, filename := range []string{certificateFile, keyFile} {
		info, err := os.Stat(filename)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o444 {
			t.Fatalf("%s mode = %#o, want 0444", filepath.Base(filename), got)
		}
	}
}

type diagnosticExecutor struct {
	psOutput  []byte
	psErr     error
	logOutput []byte
	logErr    error
}

func (e diagnosticExecutor) Run(_ context.Context, command testcompose.Command) ([]byte, error) {
	for _, argument := range command.Args {
		switch argument {
		case "ps":
			return e.psOutput, e.psErr
		case "logs":
			return e.logOutput, e.logErr
		}
	}
	return nil, fmt.Errorf("unexpected diagnostic command: %v", command.Args)
}

func TestCaptureRuntimeDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name            string
		logErr          error
		wantSummaryText string
	}{
		{name: "complete", wantSummaryText: "compose_logs_error=none"},
		{
			name:            "partial logs",
			logErr:          errors.New("logs unavailable"),
			wantSummaryText: `compose_logs_error=compose project "runtime": logs unavailable`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			artifacts := t.TempDir()
			project := testcompose.NewQuiet("/repo", "runtime", "compose.yaml")
			project.Compose = "compose"
			project.Executor = diagnosticExecutor{
				psOutput:  []byte("NAME STATUS\napi restarting\n"),
				logOutput: []byte("api | partial service log diagnostic-secret\n"),
				logErr:    test.logErr,
			}
			h := &harness{
				t: t, mode: "distributed", phase: "process-native TLS",
				project: project, artifacts: artifacts, secrets: []string{"diagnostic-secret"},
			}

			if got := h.captureDiagnostics(t.Context(), true); got != "NAME STATUS\napi restarting" {
				t.Fatalf("terminal Compose state = %q", got)
			}
			assertArtifactContents(t, artifacts, "compose-ps.txt", "api restarting")
			logs := assertArtifactContents(t, artifacts, "compose.log", "partial service log [REDACTED]")
			if strings.Contains(logs, "diagnostic-secret") {
				t.Fatalf("Compose diagnostics retained a configured secret: %q", logs)
			}
			summary := assertArtifactContents(
				t,
				artifacts,
				"failure-summary.txt",
				"status=failed\nmode=distributed\nphase=process-native TLS\nproject=runtime",
			)
			if !strings.Contains(summary, test.wantSummaryText) {
				t.Fatalf("failure summary = %q, want %q", summary, test.wantSummaryText)
			}
			if strings.Contains(summary, "partial service log") {
				t.Fatalf("failure summary included full service output: %q", summary)
			}
		})
	}
}

func assertArtifactContents(
	t *testing.T,
	directory, name, want string,
) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(directory, name))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(contents); !strings.Contains(got, want) {
		t.Fatalf("%s = %q, want substring %q", name, got, want)
	}
	return string(contents)
}

func runMode(t *testing.T, ctx context.Context, root, mode string, baseTimestamp uint64) modeResult {
	t.Helper()
	artifacts, err := os.MkdirTemp("", "etherview-runtime-e2e-"+mode+"-")
	if err != nil {
		t.Fatal(err)
	}
	project := testcompose.NewQuiet(
		root,
		testcompose.UniqueProjectName("etherview-runtime-"+mode),
		"compose.yaml",
		"e2e/runtime/compose.yaml",
	)
	project.Profiles = []string{mode}
	project.Env = runtimeEnvironment(root, baseTimestamp, true)
	h := &harness{
		t: t, root: root, mode: mode, project: project,
		apiService: map[string]string{"monolith": "etherview", "distributed": "api"}[mode],
		http:       &http.Client{Timeout: 5 * time.Second}, artifacts: artifacts,
		baseTimestamp: baseTimestamp, phase: "initialization",
	}
	t.Cleanup(func() { h.cleanup() })

	h.enterPhase("deterministic fixture")
	{
		if err := h.project.Up(ctx, "runtime-fixture"); err != nil {
			t.Fatal(err)
		}
		binding, err := h.project.Port(ctx, "runtime-fixture", 8545)
		if err != nil {
			t.Fatal(err)
		}
		h.rpc, err = rpc.DialContext(ctx, "http://"+binding)
		if err != nil {
			t.Fatal(err)
		}
		h.initializeFixture(ctx)
		h.configureUserOperationEnvironment()
		h.rpcProxy = startReceiptProxy(t, "http://"+binding)
		h.rpcProxy.ensAddress = common.HexToAddress(h.fixture.authority)
		h.ensRPC = startENSMainnetRPC(t)
		h.project.Env["ETHERVIEW_RUNTIME_RPC_URL"] = h.rpcProxy.containerURL()
		h.project.Env["ETHERVIEW_RUNTIME_ENS_RPC_URL"] = h.ensRPC.containerURL()
		h.project.Env["ETHERVIEW_CHAIN_GENESIS_HASH"] = h.fixture.genesisHash
	}

	h.enterPhase("fresh production topology")
	{
		h.startTopology(ctx)
		h.connectDatabase(ctx)
		h.resolveAPI(ctx)
		h.waitReady(ctx)
		h.waitCanonical(ctx, 1, h.fixture.blockOneHash)
		h.assertUserOperation(ctx)
		if h.rpcProxy.normalized.Load() == 0 {
			t.Fatal("RPC fixture adapter did not normalize Anvil's incomplete blob fee observation")
		}
		if mode == "distributed" {
			h.stopOneWorkerReplica(ctx, "sync")
			h.stopOneWorkerReplica(ctx, "enrich")
		}
	}

	h.enterPhase("pending API exact hash")
	{
		h.waitPending(ctx, h.fixture.pendingHash)
		h.fixture.pendingReplacementHash = h.sendTransaction(ctx, map[string]any{
			"from": h.fixture.accounts[0], "to": h.fixture.accounts[3],
			"nonce": hexutil.EncodeUint64(h.fixture.pendingNonce),
			"value": "0xa", "gas": "0x5208", "gasPrice": "0x77359400",
		})
		h.waitReplacedAndPending(ctx, h.fixture.pendingHash, h.fixture.pendingReplacementHash)
		h.rpcCall(ctx, &h.fixture.snapshotID, "evm_snapshot")
		if h.fixture.snapshotID == "" {
			t.Fatal("fixture returned empty replacement snapshot ID")
		}
	}

	var firstRollup string
	h.enterPhase("first branch publication")
	{
		h.fixture.orphanDelegationHash = h.sendSetCodeTransaction(ctx, []types.SetCodeAuthorization{
			h.signAuthorization(h.fixture.authorityKey, common.HexToAddress(h.fixture.delegateA), 0),
		}, common.FromHex("0x3fa4f245"))
		h.mine(ctx, h.baseTimestamp+2)
		h.fixture.orphanHash = h.latestBlock(ctx).Hash
		h.waitCanonical(ctx, 2, h.fixture.orphanHash)
		h.waitIncludedTransaction(ctx, h.fixture.pendingReplacementHash)
		orphanReceipt := h.waitReceipt(ctx, h.fixture.orphanDelegationHash)
		if orphanReceipt.Status != "0x1" {
			t.Fatalf("orphan delegation receipt status = %s, want 0x1", orphanReceipt.Status)
		}
		h.assertCurrentDelegation(ctx, h.fixture.delegateA)
		firstRollup = h.waitRollup(ctx, 2, "")
	}

	h.enterPhase("competing-hash reorg and contract")
	{
		var reverted bool
		h.rpcCall(ctx, &reverted, "evm_revert", h.fixture.snapshotID)
		if !reverted {
			t.Fatal("Anvil rejected deterministic snapshot revert")
		}
		h.fixture.creationHash = h.sendTransaction(ctx, map[string]any{
			"from": h.fixture.accounts[0], "data": noCBORCreationBytecode,
			"gas": "0x7a120", "gasPrice": "0x3b9aca00",
		})
		h.fixture.nftCreationHash = h.sendEtherscanNFTDeployment(ctx)
		h.fixture.delegationAuths = []types.SetCodeAuthorization{
			h.signAuthorization(h.fixture.authorityKey, common.HexToAddress(h.fixture.delegateB), 0),
			h.signAuthorization(h.fixture.skippedKey, common.HexToAddress(h.fixture.delegateA), 1),
		}
		h.fixture.delegationHash = h.sendSetCodeTransaction(
			ctx, h.fixture.delegationAuths, common.FromHex("0x3fa4f245"),
		)
		h.mine(ctx, h.baseTimestamp+12)
		replacement := h.latestBlock(ctx)
		if replacement.Number != "0x2" {
			t.Fatalf("replacement height = %s, want 0x2", replacement.Number)
		}
		if strings.EqualFold(replacement.Hash, h.fixture.orphanHash) {
			t.Fatalf("replacement block reused orphan hash %s", replacement.Hash)
		}
		receipt := h.waitReceipt(ctx, h.fixture.creationHash)
		if receipt.Status != "0x1" || receipt.ContractAddress == "" {
			t.Fatalf("creation receipt = %#v", receipt)
		}
		h.fixture.contractAddress = receipt.ContractAddress
		h.captureEtherscanNFTDeployment(ctx)
		delegationReceipt := h.waitReceipt(ctx, h.fixture.delegationHash)
		if delegationReceipt.Status != "0x1" {
			t.Fatalf("canonical delegation receipt status = %s, want 0x1", delegationReceipt.Status)
		}
		h.waitCanonical(ctx, 2, replacement.Hash)
		h.assertCurrentDelegation(ctx, h.fixture.delegateB)
		h.assertCanonicalDelegationHistory(ctx, []string{"delegated"}, []string{h.fixture.delegationHash})
		h.insertRuntimeCompoundSignature(ctx)
		h.fixture.failedHash = h.sendTransaction(ctx, map[string]any{
			"from": h.fixture.accounts[0], "to": h.fixture.contractAddress,
			"data": "0xffffffff", "gas": "0x186a0", "gasPrice": "0x3b9aca00",
		})
		h.fixture.compoundHash = h.sendTransaction(ctx, map[string]any{
			"from": h.fixture.accounts[0], "to": h.fixture.contractAddress,
			"data": runtimeCompoundCalldata, "gas": "0x30d40", "gasPrice": "0x3b9aca00",
		})
		h.fixture.redelegationAuth = h.signAuthorization(
			h.fixture.authorityKey, common.HexToAddress(h.fixture.delegateA), 1,
		)
		h.fixture.redelegationHash = h.sendSetCodeTransaction(
			ctx, []types.SetCodeAuthorization{h.fixture.redelegationAuth}, common.FromHex("0x3fa4f245"),
		)
		h.fixture.delegatedCallHash = h.sendDynamicFeeTransaction(
			ctx,
			common.HexToAddress(h.fixture.authority),
			common.FromHex("0x3fa4f245"),
		)
		h.mine(ctx, h.baseTimestamp+13)
		delegatedBlock := h.latestBlock(ctx)
		delegatedHeight := mustDecodeUint64(t, delegatedBlock.Number)
		h.waitCanonical(ctx, delegatedHeight, delegatedBlock.Hash)
		failedReceipt := h.waitReceipt(ctx, h.fixture.failedHash)
		if failedReceipt.Status != "0x0" {
			t.Fatalf("failed call receipt status = %s, want 0x0", failedReceipt.Status)
		}
		compoundReceipt := h.waitReceipt(ctx, h.fixture.compoundHash)
		if compoundReceipt.Status != "0x0" {
			t.Fatalf("compound calldata receipt status = %s, want 0x0", compoundReceipt.Status)
		}
		for name, hash := range map[string]string{
			"redelegation":   h.fixture.redelegationHash,
			"delegated call": h.fixture.delegatedCallHash,
		} {
			if transactionReceipt := h.waitReceipt(ctx, hash); transactionReceipt.Status != "0x1" {
				t.Fatalf("%s receipt status = %s, want 0x1", name, transactionReceipt.Status)
			}
		}
		h.assertCurrentDelegation(ctx, h.fixture.delegateA)

		h.fixture.clearingAuth = h.signAuthorization(h.fixture.authorityKey, common.Address{}, 2)
		h.fixture.clearingHash = h.sendSetCodeTransaction(
			ctx, []types.SetCodeAuthorization{h.fixture.clearingAuth}, common.FromHex("0x55241077"),
		)
		h.mine(ctx, h.baseTimestamp+14)
		finalBlock := h.latestBlock(ctx)
		h.fixture.finalHash = finalBlock.Hash
		h.fixture.finalHeight = mustDecodeUint64(t, finalBlock.Number)
		h.waitCanonical(ctx, h.fixture.finalHeight, h.fixture.finalHash)
		if h.rpcProxy.clearedDelegation.Load() == 0 {
			t.Fatal("RPC fixture adapter did not normalize Anvil's cleared delegation post-state")
		}
		if clearingReceipt := h.waitReceipt(ctx, h.fixture.clearingHash); clearingReceipt.Status != "0x1" {
			t.Fatalf("clearing receipt status = %s, want 0x1", clearingReceipt.Status)
		}
		h.assertCurrentDelegation(ctx, "")
		h.assertCanonicalDelegationHistory(
			ctx,
			[]string{"cleared", "redelegated", "delegated"},
			[]string{h.fixture.clearingHash, h.fixture.redelegationHash, h.fixture.delegationHash},
		)
		h.waitReorgClosure(ctx)
		changed := h.waitRollup(ctx, h.fixture.finalHeight, firstRollup)
		t.Logf("reorg rollup changed from %s to %s", firstRollup, changed)
	}

	h.enterPhase("RPC outage recovery")
	{
		before := h.canonicalHash(ctx, h.fixture.finalHeight)
		h.compose(ctx, "pause", "runtime-fixture")
		response := h.requireHTTPStatus(ctx, "/api/v1/status", http.StatusOK)
		_ = response.Body.Close()
		if after := h.canonicalHash(ctx, h.fixture.finalHeight); after != before {
			t.Fatalf("canonical hash changed during RPC outage: %s -> %s", before, after)
		}
		h.compose(ctx, "unpause", "runtime-fixture")
		h.waitReady(ctx)
	}

	h.enterPhase("PostgreSQL outage recovery")
	{
		h.compose(ctx, "pause", "postgres")
		h.waitDatabaseUnavailable(ctx)
		h.compose(ctx, "unpause", "postgres")
		h.waitReady(ctx)
		h.waitCanonical(ctx, h.fixture.finalHeight, h.fixture.finalHash)
	}

	h.enterPhase("process restart")
	{
		h.compose(ctx, "restart", h.apiService)
		// A service published on host port 0 may receive a new ephemeral port
		// when Compose restarts it, so refresh the externally reachable URL.
		h.resolveAPI(ctx)
		h.waitReady(ctx)
		h.waitCanonical(ctx, h.fixture.finalHeight, h.fixture.finalHash)
	}

	h.enterPhase("API, SSE, SPA, and bounded load")
	{
		h.validateSSE(ctx)
		report, err := loadtest.Run(ctx, loadtest.Config{
			BaseURL: h.baseURL,
			Paths:   []string{"/api/v1/config", "/api/v1/status", "/api/v1/blocks?limit=20", "/api/v1/transactions?limit=20"},
			Rate:    40, Duration: 3 * time.Second, Concurrency: 16,
			RequestTimeout: runtimeServerWriteTimeout, MaximumP95: time.Second,
			MaximumErrorRate: 0, MinimumThroughputRatio: 0.8, MaximumLag: 0,
			Profile: "p70-runtime-" + mode, Revision: "working-tree",
			Dataset:  "deterministic-competing-hash-contract-runtime",
			Hardware: "production-compose", RPCBehavior: "deterministic-anvil-with-outages",
		})
		if err != nil {
			t.Fatalf("bounded load: %v (report=%+v)", err, report)
		}
		encoded, _ := json.MarshalIndent(report, "", "  ")
		h.writeArtifact(mode+"-load.json", encoded)
	}
	if h.rpcProxy.blockTraceCalls.Load() == 0 {
		t.Fatal("RPC fixture adapter did not observe debug_traceBlockByHash")
	}
	if calls := h.rpcProxy.transactionTraces.Load(); calls != 0 {
		t.Fatalf("RPC fixture adapter observed %d debug_traceTransaction calls, want 0", calls)
	}
	h.assertOperationalLogs(ctx)
	h.assertENS(ctx)

	result := modeResult{
		durable: h.captureDurable(ctx),
		api:     h.captureAPI(ctx),
	}
	h.writeJSONArtifact(mode+"-durable.json", result.durable)
	h.writeJSONArtifact(mode+"-api.json", result.api)
	if mode == "distributed" {
		h.enterPhase("process-native TLS")
		h.validateProcessNativeTLS(ctx)
	}
	return result
}

func (h *harness) assertOperationalLogs(ctx context.Context) {
	services := []string{"etherview"}
	if h.mode == "distributed" {
		services = []string{"enrich", "trace"}
	}
	arguments := append([]string{"logs", "--no-color"}, services...)
	output, err := h.project.Run(ctx, arguments...)
	if err != nil {
		h.t.Fatalf("read operational logs: %v", err)
	}
	logs := string(output)
	for _, expected := range []string{
		`"event":"runtime_ready"`,
		`"event":"enrichment_job_transitioned"`,
		`"job":{"id":`,
		`"stage":{"name":"trace","version":3}`,
		`"stage":{"name":"state_diff","version":3}`,
		`"stage":{"name":"userop","version":1}`,
		`"block":{"number":"` + strconv.FormatUint(h.fixture.finalHeight, 10) + `","hash":"` + strings.ToLower(h.fixture.finalHash) + `"}`,
		`"summary":{`,
	} {
		if !strings.Contains(logs, expected) {
			h.t.Fatalf("operational logs missing %q", expected)
		}
	}
	for _, forbidden := range []string{"error_msg", "provider-secret", "database-url-redacted"} {
		if strings.Contains(logs, forbidden) {
			h.t.Fatalf("operational logs contain forbidden field/value %q", forbidden)
		}
	}
}

func runtimeEnvironment(root string, baseTimestamp uint64, userOperations bool) map[string]string {
	return map[string]string{
		"ETHERVIEW_IMAGE":                 valueOrDefault("IMAGE", "etherview:local"),
		"ETHERVIEW_RUNTIME_FIXTURE_IMAGE": valueOrDefault("ETHERVIEW_RUNTIME_FIXTURE_IMAGE", "ghcr.io/foundry-rs/foundry:v1.7.1"),
		"ANVIL_ARGS": valueOrDefault(
			"ANVIL_ARGS",
			fmt.Sprintf(
				"--timestamp %d --hardfork prague --mnemonic-seed-unsafe 424242",
				baseTimestamp,
			),
		),
		"ETHERVIEW_CONFIG_FILE":                     filepath.Join(root, "deploy/config.example.yaml"),
		"ETHERVIEW_RPC_URLS":                        "http://runtime-fixture:8545",
		"ETHERVIEW_CHAIN_ID":                        "1",
		"ETHERVIEW_CHAIN_GENESIS_HASH":              "",
		"ETHERVIEW_ADAPTER_NAMESPACE":               "runtime-e2e",
		"ETHERVIEW_RUNTIME_SERVER_WRITE_TIMEOUT":    runtimeServerWriteTimeout.String(),
		"ETHERVIEW_RUNTIME_FEATURE_USER_OPERATIONS": strconv.FormatBool(userOperations),
		"ETHERVIEW_PORT":                            "0",
		"ETHERVIEW_METRICS_PORT":                    "0",
		"POSTGRES_PASSWORD":                         "etherview-runtime-e2e",
	}
}

func (h *harness) initializeFixture(ctx context.Context) {
	var chainID string
	h.rpcCall(ctx, &chainID, "eth_chainId")
	if chainID != "0x1" {
		h.t.Fatalf("fixture chain ID = %s, want 0x1", chainID)
	}
	var genesis rpcBlock
	h.rpcCall(ctx, &genesis, "eth_getBlockByNumber", "0x0", false)
	if !common.IsHexHash(genesis.Hash) {
		h.t.Fatalf("invalid genesis hash %q", genesis.Hash)
	}
	h.fixture.genesisHash = genesis.Hash
	h.rpcCall(ctx, &h.fixture.accounts, "eth_accounts")
	if len(h.fixture.accounts) < 4 {
		h.t.Fatalf("fixture returned %d accounts, want at least 4", len(h.fixture.accounts))
	}
	h.fixture.authorityKey = deterministicRuntimeKey(h.t, "etherview-runtime-eip7702-authority")
	h.fixture.sponsorKey = deterministicRuntimeKey(h.t, "etherview-runtime-eip7702-sponsor")
	h.fixture.skippedKey = deterministicRuntimeKey(h.t, "etherview-runtime-eip7702-skipped")
	h.fixture.authority = crypto.PubkeyToAddress(h.fixture.authorityKey.PublicKey).Hex()
	h.fixture.sponsor = crypto.PubkeyToAddress(h.fixture.sponsorKey.PublicKey).Hex()
	h.fixture.skippedAuthority = crypto.PubkeyToAddress(h.fixture.skippedKey.PublicKey).Hex()
	for _, funded := range []string{h.fixture.authority, h.fixture.sponsor} {
		var accepted any
		h.rpcCall(ctx, &accepted, "anvil_setBalance", funded, "0x56bc75e2d63100000")
		var balance string
		h.rpcCall(ctx, &balance, "eth_getBalance", funded, "latest")
		if balance != "0x56bc75e2d63100000" {
			h.t.Fatalf("EIP-7702 fixture balance for %s = %s", funded, balance)
		}
	}
	h.sendUserOperationEntryPointDeployment(ctx)
	h.fixture.nativeHash = h.sendTransaction(ctx, map[string]any{
		"from": h.fixture.accounts[0], "to": nativeTransferTarget,
		"value": "0xb", "gas": "0x5208", "gasPrice": "0x3b9aca00",
	})
	delegateAHash := h.sendTransaction(ctx, map[string]any{
		"from": h.fixture.accounts[0], "data": noCBORCreationBytecode,
		"gas": "0x7a120", "gasPrice": "0x3b9aca00",
	})
	delegateBHash := h.sendTransaction(ctx, map[string]any{
		"from": h.fixture.accounts[0], "data": noCBORCreationBytecode,
		"gas": "0x7a120", "gasPrice": "0x3b9aca00",
	})
	h.sendUserOperationBundle(ctx)
	h.mine(ctx, h.baseTimestamp+1)
	h.captureInitialContractDeployments(ctx, delegateAHash, delegateBHash)
	h.captureUserOperationFixture(ctx)
	h.fixture.blockOneHash = h.latestBlock(ctx).Hash
	var pendingNonce hexutil.Uint64
	h.rpcCall(ctx, &pendingNonce, "eth_getTransactionCount", h.fixture.accounts[0], "latest")
	h.fixture.pendingNonce = uint64(pendingNonce)
	h.fixture.pendingHash = h.sendTransaction(ctx, map[string]any{
		"from": h.fixture.accounts[0], "to": h.fixture.accounts[2],
		"nonce": hexutil.EncodeUint64(h.fixture.pendingNonce),
		"value": "0x9", "gas": "0x5208", "gasPrice": "0x3b9aca00",
	})
	h.rpcCall(ctx, &h.fixture.snapshotID, "evm_snapshot")
	if h.fixture.snapshotID == "" {
		h.t.Fatal("fixture returned empty snapshot ID")
	}
}

func (h *harness) startTopology(ctx context.Context) {
	if h.mode == "distributed" {
		h.compose(ctx, "up", "-d", "--no-recreate", "--wait", "--wait-timeout", "90", "api")
		h.connectDatabase(ctx)
		waitFor(h.t, ctx, "config-only role identity bind", func() (bool, string, error) {
			var identity string
			err := h.db.QueryRow(ctx, `
				SELECT chain_id::text || ':' || encode(genesis_hash, 'hex')
				FROM chains WHERE chain_id = 1
			`).Scan(&identity)
			want := "1:" + strings.TrimPrefix(h.fixture.genesisHash, "0x")
			return err == nil && identity == want, identity, err
		})
		h.compose(ctx, "up", "-d", "--no-recreate", "--wait", "--wait-timeout", "90", "--remove-orphans",
			"--scale", "sync=2", "--scale", "enrich=2")
		return
	}
	h.compose(ctx, "up", "-d", "--no-recreate", "--wait", "--wait-timeout", "90", "--remove-orphans")
}

func (h *harness) connectDatabase(ctx context.Context) {
	if h.db != nil {
		return
	}
	binding, err := h.project.Port(ctx, "postgres", 5432)
	if err != nil {
		h.t.Fatal(err)
	}
	url := "postgres://etherview:etherview-runtime-e2e@" + binding + "/etherview?sslmode=disable"
	h.db, err = pgxpool.New(ctx, url)
	if err != nil {
		h.t.Fatal(err)
	}
	waitFor(h.t, ctx, "PostgreSQL connection", func() (bool, string, error) {
		err := h.db.Ping(ctx)
		return err == nil, fmt.Sprint(err), err
	})
}

func (h *harness) insertRuntimeCompoundSignature(ctx context.Context) {
	h.t.Helper()
	selector := common.FromHex(runtimeCompoundCalldata[:10])
	if len(selector) != 4 {
		h.t.Fatalf("compound calldata selector length = %d", len(selector))
	}
	if _, err := h.db.Exec(ctx, `
		INSERT INTO abi_signature_candidates (kind, identifier, signature, abi_entry)
		VALUES ('function', $1, $2, $3::jsonb)
		ON CONFLICT DO NOTHING`, selector, runtimeCompoundSignature, []byte(runtimeCompoundABIEntry)); err != nil {
		h.t.Fatalf("insert compound ABI signature candidate: %v", err)
	}
}

func (h *harness) resolveAPI(ctx context.Context) {
	binding, err := h.project.Port(ctx, h.apiService, 8080)
	if err != nil {
		h.t.Fatal(err)
	}
	h.baseURL = "http://" + binding
}

func (h *harness) validateProcessNativeTLS(ctx context.Context) {
	h.t.Helper()
	certificateFile, keyFile, roots := writeRuntimeTLSKeyPair(h.t)
	overrideFile := writeRuntimeTLSComposeOverride(
		h.t,
		h.apiService,
		certificateFile,
		keyFile,
	)
	project := testcompose.NewQuiet(
		h.root,
		h.project.Name,
		"compose.yaml",
		"e2e/runtime/compose.yaml",
		overrideFile,
	)
	project.Profiles = append([]string(nil), h.project.Profiles...)
	project.Env = make(map[string]string, len(h.project.Env))
	maps.Copy(project.Env, h.project.Env)
	if _, err := project.Run(ctx, "up", "-d", "--no-deps", "--force-recreate", h.apiService); err != nil {
		h.t.Fatal(err)
	}
	binding, err := project.Port(ctx, h.apiService, 8080)
	if err != nil {
		h.t.Fatal(err)
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			ForceAttemptHTTP2: true,
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				RootCAs:    roots,
			},
		},
	}
	waitFor(h.t, ctx, "process-native HTTPS readiness", func() (bool, string, error) {
		request, requestErr := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			"https://"+binding+"/health/ready",
			nil,
		)
		if requestErr != nil {
			return false, "", requestErr
		}
		response, requestErr := client.Do(request)
		if requestErr != nil {
			return false, requestErr.Error(), nil
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusOK {
			return false, response.Status, nil
		}
		if response.ProtoMajor != 2 || response.TLS == nil ||
			response.TLS.Version < tls.VersionTLS12 {
			return false, fmt.Sprintf("protocol=%s tls=%#v", response.Proto, response.TLS), nil
		}
		return true, response.Proto, nil
	})
}

func writeRuntimeTLSComposeOverride(
	t *testing.T,
	service, certificateFile, keyFile string,
) string {
	t.Helper()
	override := map[string]any{
		"services": map[string]any{
			service: map[string]any{
				"environment": map[string]string{
					"ETHERVIEW_SERVER_TLS_CERT_FILE": "/run/etherview-tls/tls.crt",
					"ETHERVIEW_SERVER_TLS_KEY_FILE":  "/run/etherview-tls/tls.key",
				},
				"volumes": []map[string]any{
					{
						"type":      "bind",
						"source":    certificateFile,
						"target":    "/run/etherview-tls/tls.crt",
						"read_only": true,
					},
					{
						"type":      "bind",
						"source":    keyFile,
						"target":    "/run/etherview-tls/tls.key",
						"read_only": true,
					},
				},
			},
		},
	}
	payload, err := json.Marshal(override)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "compose.runtime-tls.json")
	if err := os.WriteFile(filename, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return filename
}

func writeRuntimeTLSKeyPair(t *testing.T) (string, string, *x509.CertPool) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
	}
	certificateDER, err := x509.CreateCertificate(
		rand.Reader,
		template,
		template,
		&privateKey.PublicKey,
		privateKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})
	directory := t.TempDir()
	certificateFile := filepath.Join(directory, "tls.crt")
	keyFile := filepath.Join(directory, "tls.key")
	// The production image runs as UID 65532. Native Linux bind mounts preserve
	// the host file owner and mode, so owner-only files created by the test
	// runner are unreadable in the container. The private 0700 temporary
	// directory protects the host copies; expose only these two files to the
	// API container as immutable read-only mounts.
	if err := os.WriteFile(certificateFile, certificatePEM, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, privateKeyPEM, 0o444); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificatePEM) {
		t.Fatal("append runtime TLS certificate")
	}
	return certificateFile, keyFile, roots
}

func (h *harness) waitReady(ctx context.Context) {
	waitFor(h.t, ctx, h.mode+" API readiness", func() (bool, string, error) {
		response, err := h.request(ctx, "/health/ready")
		if err != nil {
			return false, err.Error(), nil
		}
		defer func() { _ = response.Body.Close() }()
		return response.StatusCode == http.StatusOK, response.Status, nil
	})
}

func (h *harness) waitCanonical(ctx context.Context, height uint64, hash string) {
	expected := strings.ToLower(strings.TrimPrefix(hash, "0x"))
	heightArgument := int64(height)
	expectedStages := expectedRuntimeStageCount(h.project.Env)
	waitFor(h.t, ctx, fmt.Sprintf("canonical block %d with %d complete stages", height, expectedStages), func() (bool, string, error) {
		var canonical string
		var checkpoint int64
		var complete, active, failed, unpublished int64
		var failures string
		err := h.db.QueryRow(ctx, `
			SELECT
				COALESCE((SELECT encode(block_hash, 'hex') FROM canonical_blocks
					WHERE chain_id = 1 AND number = $1), ''),
				COALESCE((SELECT contiguous_through::bigint FROM index_checkpoints
					WHERE chain_id = 1 AND stage = 'core'), -1),
				(SELECT count(*) FROM published_block_stage_results
					WHERE chain_id = 1 AND block_number = $1 AND block_hash = decode($2, 'hex')
					  AND state = 'complete' AND (stage, stage_version) IN (
					    ('proxy',$3::integer),('abi',$4::integer),('token',1),
					    ('stats',3),('trace',$5::integer),('state_diff',$6::integer),('userop',1))),
				(SELECT count(*) FROM durable_jobs WHERE status IN ('queued','leased')),
				(SELECT count(*) FROM published_block_stage_results
					WHERE chain_id = 1 AND block_number = $1 AND block_hash = decode($2, 'hex')
					  AND state <> 'complete' AND (stage, stage_version) IN (
					    ('proxy',$3::integer),('abi',$4::integer),('token',1),
					    ('stats',3),('trace',$5::integer),('state_diff',$6::integer),('userop',1))),
				(SELECT count(*) FROM transactional_outbox WHERE published_at IS NULL),
				COALESCE((SELECT string_agg(stage || ':' || left(last_error, 512), ';' ORDER BY stage)
					FROM published_block_stage_results
					WHERE chain_id = 1 AND block_number = $1 AND block_hash = decode($2, 'hex')
					  AND state <> 'complete'), '')
		`, heightArgument, expected, enrich.ProxyStage.Version, enrich.ABIStage.Version,
			enrich.TraceStage.Version, enrich.StateDiffStage.Version).
			Scan(&canonical, &checkpoint, &complete, &active, &failed, &unpublished, &failures)
		state := fmt.Sprintf("hash=%s checkpoint=%d complete=%d active=%d failed=%d outbox=%d failures=%s",
			canonical, checkpoint, complete, active, failed, unpublished, failures)
		if err == nil && failures != "" {
			err = errors.New(failures)
		}
		return err == nil && canonical == expected && checkpoint == int64(height) &&
			complete == expectedStages && active == 0 && failed == 0 && unpublished == 0, state, err
	})
}

func (h *harness) waitPending(ctx context.Context, expectedHash string) {
	waitFor(h.t, ctx, "exact pending transaction hash", func() (bool, string, error) {
		var response gen.PendingTransactionListResponse
		err := h.getJSON(ctx, "/api/v1/pending?limit=10", &response)
		hashes := make([]string, 0, len(response.Data))
		for _, transaction := range response.Data {
			hashes = append(hashes, string(transaction.Hash))
		}
		return err == nil && len(hashes) == 1 && strings.EqualFold(hashes[0], expectedHash),
			strings.Join(hashes, ","), err
	})
}

func (h *harness) waitReplacedAndPending(ctx context.Context, oldHash, newHash string) {
	waitFor(h.t, ctx, "direct mempool replacement detail", func() (bool, string, error) {
		var oldResponse, newResponse gen.TransactionResponse
		if err := h.getJSON(ctx, "/api/v1/transactions/"+oldHash, &oldResponse); err != nil {
			return false, "old detail unavailable", err
		}
		if err := h.getJSON(ctx, "/api/v1/transactions/"+newHash, &newResponse); err != nil {
			return false, "new detail unavailable", err
		}
		replaced, oldErr := oldResponse.Data.AsReplacedTransactionDetail()
		pending, newErr := newResponse.Data.AsPendingTransactionDetail()
		matches := oldErr == nil && newErr == nil &&
			replaced.Kind == gen.ReplacedTransactionDetailKindReplaced &&
			strings.EqualFold(string(replaced.ReplacementHash), newHash) &&
			pending.Kind == gen.PendingTransactionDetailKindPending &&
			pending.Transaction.ReplacesHash != nil &&
			strings.EqualFold(string(*pending.Transaction.ReplacesHash), oldHash)
		state := fmt.Sprintf("old_kind=%s successor=%s new_kind=%s predecessor=%v",
			replaced.Kind, replaced.ReplacementHash, pending.Kind, pending.Transaction.ReplacesHash)
		return matches, state, errors.Join(oldErr, newErr)
	})
}

func (h *harness) waitIncludedTransaction(ctx context.Context, hash string) {
	waitFor(h.t, ctx, "included replacement transaction", func() (bool, string, error) {
		var response gen.TransactionResponse
		if err := h.getJSON(ctx, "/api/v1/transactions/"+hash, &response); err != nil {
			return false, "detail unavailable", err
		}
		included, err := response.Data.AsIncludedTransactionDetail()
		if err != nil {
			return false, "included detail decode failed", err
		}
		status := ""
		if included.Transaction.Status != nil {
			status = string(*included.Transaction.Status)
		}
		matches := included.Kind == gen.IncludedTransactionDetailKindIncluded &&
			strings.EqualFold(string(included.Transaction.Hash), hash) && status == string(gen.TransactionStatusSuccess)
		return matches, fmt.Sprintf("kind=%s hash=%s status=%s", included.Kind, included.Transaction.Hash, status), nil
	})
}

func (h *harness) requireIncludedTransaction(response gen.TransactionResponse) gen.Transaction {
	h.t.Helper()
	included, err := response.Data.AsIncludedTransactionDetail()
	if err != nil || included.Kind != gen.IncludedTransactionDetailKindIncluded {
		h.t.Fatalf("transaction detail is not included: kind=%s error=%v", included.Kind, err)
	}
	return included.Transaction
}

func (h *harness) waitReorgClosure(ctx context.Context) {
	oldHash := strings.TrimPrefix(h.fixture.orphanHash, "0x")
	newHash := strings.TrimPrefix(h.canonicalHash(ctx, 2), "0x")
	waitFor(h.t, ctx, "competing-hash orphan retention and journal detach", func() (bool, string, error) {
		var blocks, detached int64
		var canonical string
		err := h.db.QueryRow(ctx, `
			SELECT
				(SELECT count(*) FROM blocks WHERE chain_id = 1 AND number = 2),
				COALESCE((SELECT encode(block_hash, 'hex') FROM canonical_blocks
					WHERE chain_id = 1 AND number = 2), ''),
				(SELECT count(*) FROM block_journals
					WHERE chain_id = 1 AND block_hash = decode($1, 'hex') AND canonical = false)
		`, oldHash).Scan(&blocks, &canonical, &detached)
		state := fmt.Sprintf("blocks=%d canonical=%s detached_journals=%d", blocks, canonical, detached)
		return err == nil && blocks == 2 && canonical == strings.ToLower(newHash) && detached > 0, state, err
	})
}

func (h *harness) waitRollup(ctx context.Context, height uint64, differentFrom string) string {
	var result string
	waitFor(h.t, ctx, fmt.Sprintf("hourly rollup through block %d", height), func() (bool, string, error) {
		var dirty int64
		err := h.db.QueryRow(ctx, `
			SELECT
				COALESCE((SELECT source_generation::text || ':' || from_block::text || ':' ||
					to_block::text || ':' || transaction_count::text || ':' ||
					failed_transaction_count::text || ':' || contract_creation_count::text
					FROM chart_hourly_rollups WHERE chain_id = 1
					ORDER BY bucket_start DESC LIMIT 1), ''),
				(SELECT count(*) FROM chart_rollup_dirty_hours WHERE chain_id = 1)
		`).Scan(&result, &dirty)
		parts := strings.Split(result, ":")
		throughExpected := len(parts) >= 3 && parts[2] == strconv.FormatUint(height, 10)
		changed := differentFrom == "" || result != differentFrom
		return err == nil && result != "" && dirty == 0 && throughExpected && changed,
			fmt.Sprintf("rollup=%s dirty=%d", result, dirty), err
	})
	return result
}

func (h *harness) stopOneWorkerReplica(ctx context.Context, service string) {
	output, err := h.project.Run(ctx, "ps", "-q", service)
	if err != nil {
		h.t.Fatal(err)
	}
	containers := strings.Fields(string(output))
	if len(containers) != 2 {
		h.t.Fatalf("%s running replicas = %d, want 2", service, len(containers))
	}
	runHostCommand(h.t, ctx, h.root, dockerCommand(), "container", "stop", "--time", "10", containers[0])
	waitFor(h.t, ctx, service+" surviving replica", func() (bool, string, error) {
		output, err := h.project.Run(ctx, "ps", "-q", service)
		count := len(strings.Fields(string(output)))
		return err == nil && count == 1, strconv.Itoa(count), err
	})
}

func (h *harness) waitDatabaseUnavailable(ctx context.Context) {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		requestCtx, cancel := context.WithTimeout(ctx, time.Second)
		response, err := h.request(requestCtx, "/api/v1/status")
		cancel()
		if err != nil {
			return
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	h.t.Fatal("database-backed status remained successful while PostgreSQL was paused")
}

func (h *harness) validateSSE(ctx context.Context) {
	streamCtx, cancel := context.WithTimeout(ctx, 10*runtimeServerWriteTimeout)
	defer cancel()
	var after int64
	if err := h.db.QueryRow(ctx, `
		SELECT COALESCE(MAX(id), 0)
		FROM runtime_events
		WHERE chain_id = 1`).Scan(&after); err != nil {
		h.t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(streamCtx, http.MethodGet, h.baseURL+"/api/v1/events", nil)
	if err != nil {
		h.t.Fatal(err)
	}
	request.Header.Set("Last-Event-ID", strconv.FormatInt(after, 10))
	streamClient := *h.http
	// The shared API client has a whole-request timeout for ordinary responses.
	// SSE is bounded by streamCtx instead so the idle-deadline regression can
	// intentionally outlive that ordinary client budget.
	streamClient.Timeout = 0
	response, err := streamClient.Do(request)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK ||
		response.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" {
		h.t.Fatalf("SSE status=%d headers=%v", response.StatusCode, response.Header)
	}
	// The ordinary-response write deadline and bounded-load request deadline use
	// the same fixture constant. Stay idle beyond that whole-response deadline,
	// then commit a durable event. The SSE frame must still arrive because
	// streaming applies the timeout per write.
	time.Sleep(3 * runtimeServerWriteTimeout)
	var eventID int64
	if err := h.db.QueryRow(ctx, `
		INSERT INTO runtime_events (chain_id, event_type, payload)
		VALUES (1, 'status', '{"ready":true,"test":"per_write_deadline"}'::jsonb)
		RETURNING id`).Scan(&eventID); err != nil {
		h.t.Fatal(err)
	}
	scanner := bufio.NewScanner(response.Body)
	var receivedID int64
	for scanner.Scan() {
		line := scanner.Text()
		if raw, ok := strings.CutPrefix(line, "id: "); ok {
			receivedID, err = strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
			if err != nil {
				h.t.Fatal(err)
			}
		}
		if line == "" && receivedID == eventID {
			return
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		h.t.Fatalf("read SSE: %v", err)
	}
	h.t.Fatal("SSE did not replay a durable event")
}

func (h *harness) assertENS(ctx context.Context) {
	var config gen.PublicConfigResponse
	h.mustGetJSON(ctx, "/api/v1/config", &config)
	if !config.Data.Features["ens"] {
		h.t.Fatal("runtime public config did not enable ENS")
	}
	var names gen.AddressNamePageResponse
	h.mustGetJSON(ctx, "/api/v1/address-names?addresses="+url.QueryEscape(h.fixture.authority), &names)
	if len(names.Data.Items) != 1 || names.Data.Items[0].State != gen.AddressNameLookupStateResolved ||
		names.Data.Items[0].PrimaryName == nil || names.Data.Items[0].PrimaryName.Name != runtimeENSName ||
		names.Data.Items[0].PrimaryName.Source != gen.PrimaryNameSourceCustomEns || names.Data.Snapshot == "" {
		h.t.Fatalf("runtime ENS address names = %#v", names.Data)
	}
	var search gen.SearchResponse
	h.mustGetJSON(ctx, "/api/v1/search?q="+url.QueryEscape(runtimeENSName), &search)
	if len(search.Data) != 1 || !strings.EqualFold(search.Data[0].Key, h.fixture.authority) ||
		search.Data[0].NameSource == nil || *search.Data[0].NameSource != gen.SearchResultNameSourceCustomEns {
		h.t.Fatalf("runtime ENS search = %#v", search.Data)
	}
	if h.rpcProxy.ensCalls.Load() < 4 {
		h.t.Fatalf("custom ENS RPC calls = %d, want deployment, reverse, and forward calls", h.rpcProxy.ensCalls.Load())
	}
}

func (h *harness) captureDurable(ctx context.Context) durableSnapshot {
	var snapshot durableSnapshot
	err := h.db.QueryRow(ctx, `
		SELECT
			COALESCE((SELECT encode(genesis_hash, 'hex') FROM chains WHERE chain_id = 1), ''),
			COALESCE((SELECT string_agg(number::text || ':' || encode(block_hash, 'hex'), ',' ORDER BY number)
				FROM canonical_blocks WHERE chain_id = 1), ''),
			(SELECT count(*) FROM blocks WHERE chain_id = 1),
			(SELECT count(*) FROM transactions WHERE chain_id = 1),
			(SELECT count(*) FROM receipts WHERE chain_id = 1),
			(SELECT count(*) FROM published_block_stage_results WHERE chain_id = 1 AND state = 'complete'),
			(SELECT count(*) FROM published_block_stage_results WHERE chain_id = 1 AND state IN ('failed','unavailable')),
			(SELECT count(*) FROM transactional_outbox WHERE published_at IS NULL),
			COALESCE((SELECT contiguous_through::text || ':' || encode(block_hash, 'hex')
				FROM index_checkpoints WHERE chain_id = 1 AND stage = 'core'), ''),
			(SELECT count(*) FROM blocks AS block WHERE block.chain_id = 1 AND NOT EXISTS (
				SELECT 1 FROM canonical_blocks AS canonical
				WHERE canonical.chain_id = block.chain_id AND canonical.number = block.number
				  AND canonical.block_hash = block.hash)),
			COALESCE((SELECT from_block::text || ':' || to_block::text || ':' ||
				transaction_count::text || ':' || failed_transaction_count::text || ':' ||
				contract_creation_count::text || ':' || execution_gas_fee_wei::text
				FROM chart_hourly_rollups WHERE chain_id = 1 ORDER BY bucket_start DESC LIMIT 1), ''),
			COALESCE((SELECT count(*)::text || ':' || sum(transaction_count)::text || ':' ||
				sum(failed_transaction_count)::text || ':' || sum(contract_creation_count)::text
				FROM block_statistics WHERE chain_id = 1), ''),
			(SELECT count(*) FROM eip7702_authorizations WHERE chain_id = 1),
			(SELECT count(*) FROM eip7702_authorizations
				WHERE chain_id = 1 AND transaction_hash = decode($1, 'hex')),
			(SELECT count(*) FROM ens_name_observations WHERE chain_id = 1),
			(SELECT count(*) FROM erc4337_user_operations WHERE chain_id = 1)
	`, strings.TrimPrefix(h.fixture.orphanDelegationHash, "0x")).Scan(
		&snapshot.GenesisHash, &snapshot.Canonical, &snapshot.BlockCount,
		&snapshot.TransactionCount, &snapshot.ReceiptCount, &snapshot.CompleteStages,
		&snapshot.FailedStages, &snapshot.UnpublishedOutbox, &snapshot.Checkpoint,
		&snapshot.OrphanCount, &snapshot.Rollup, &snapshot.Statistics,
		&snapshot.Authorizations, &snapshot.OrphanAuthorizations, &snapshot.ENSObservations,
		&snapshot.UserOperations,
	)
	if err != nil {
		h.t.Fatal(err)
	}
	if snapshot.Authorizations != 5 || snapshot.OrphanAuthorizations != 1 {
		h.t.Fatalf(
			"EIP-7702 durable authorization counts = total:%d orphan:%d, want total:5 orphan:1",
			snapshot.Authorizations, snapshot.OrphanAuthorizations,
		)
	}
	if snapshot.ENSObservations < 2 {
		h.t.Fatalf("ENS durable observations = %d, want forward and primary facts", snapshot.ENSObservations)
	}
	if snapshot.UserOperations != 1 {
		h.t.Fatalf("durable UserOperations = %d, want 1", snapshot.UserOperations)
	}
	return snapshot
}

func (h *harness) captureAPI(ctx context.Context) apiSnapshot {
	var config gen.PublicConfigResponse
	h.mustGetJSON(ctx, "/api/v1/config", &config)
	var status gen.StatusResponse
	h.mustGetJSON(ctx, "/api/v1/status", &status)
	var blocks gen.BlockListResponse
	h.mustGetJSON(ctx, "/api/v1/blocks?limit=20", &blocks)
	var transactions gen.TransactionListResponse
	h.mustGetJSON(ctx, "/api/v1/transactions?limit=20", &transactions)
	var userOperation gen.UserOperationResponse
	h.mustGetJSON(ctx, "/api/v1/user-operations/"+h.fixture.userOperationHash, &userOperation)
	etherscanAdvanced, etherscanFunding, etherscanCounts := h.captureEtherscanExpansion(ctx)
	etherscanNFTHoldings := h.captureEtherscanNFTHoldings(ctx)
	var from gen.AddressResponse
	h.mustGetJSON(ctx, "/api/v1/addresses/"+h.fixture.accounts[0], &from)
	var contract gen.AddressResponse
	h.mustGetJSON(ctx, "/api/v1/addresses/"+h.fixture.contractAddress, &contract)
	var ensNames gen.AddressNamePageResponse
	h.mustGetJSON(ctx, "/api/v1/address-names?addresses="+url.QueryEscape(h.fixture.authority), &ensNames)
	ensName, ensSource := "", ""
	if len(ensNames.Data.Items) == 1 && ensNames.Data.Items[0].PrimaryName != nil {
		ensName = ensNames.Data.Items[0].PrimaryName.Name
		ensSource = string(ensNames.Data.Items[0].PrimaryName.Source)
	}
	var creation gen.TransactionResponse
	h.mustGetJSON(ctx, "/api/v1/transactions/"+h.fixture.creationHash, &creation)
	var failed gen.TransactionResponse
	h.mustGetJSON(ctx, "/api/v1/transactions/"+h.fixture.failedHash, &failed)
	creationTransaction := h.requireIncludedTransaction(creation)
	failedTransaction := h.requireIncludedTransaction(failed)
	var trace gen.TransactionTraceResponse
	h.mustGetJSON(ctx, "/api/v1/transactions/"+h.fixture.nativeHash+"/trace", &trace)
	var calldata gen.TransactionCalldataResponse
	h.mustGetJSON(ctx, "/api/v1/transactions/"+h.fixture.failedHash+"/calldata", &calldata)
	var failure gen.TransactionFailureResponse
	h.mustGetJSON(ctx, "/api/v1/transactions/"+h.fixture.failedHash+"/failure", &failure)
	if failure.Data.State != gen.TransactionFailureStateComplete ||
		failure.Data.Decoding.Status != gen.TransactionFailureDecodingStatusUnknown ||
		failure.Data.Error == "" || failure.Data.TransactionHash != gen.Hash(strings.ToLower(h.fixture.failedHash)) ||
		failedTransaction.BlockHash == nil || failure.Data.BlockHash != *failedTransaction.BlockHash ||
		failedTransaction.TransactionIndex == nil ||
		failure.Data.TransactionIndex != gen.Quantity(strconv.Itoa(*failedTransaction.TransactionIndex)) {
		h.t.Fatalf("transaction failure = %#v", failure.Data)
	}
	expectedRuntimeHash := crypto.Keccak256Hash(common.FromHex(noCBORRuntimeBytecode)).Hex()
	if string(calldata.Data.Execution.Resolution) != "direct" ||
		!strings.EqualFold(calldata.Data.Execution.ContextAddress, h.fixture.contractAddress) ||
		calldata.Data.Execution.Address == nil ||
		!strings.EqualFold(string(*calldata.Data.Execution.Address), h.fixture.contractAddress) ||
		calldata.Data.Execution.CodeHash == nil ||
		!strings.EqualFold(string(*calldata.Data.Execution.CodeHash), expectedRuntimeHash) ||
		calldata.Data.Decoding.Status != gen.TransactionCalldataDecodingStatusUnknown {
		h.t.Fatalf("transaction calldata = %#v", calldata.Data)
	}
	var compoundCalldata gen.TransactionCalldataResponse
	h.mustGetJSON(ctx, "/api/v1/transactions/"+h.fixture.compoundHash+"/calldata", &compoundCalldata)
	compoundInputs := compoundCalldata.Data.Decoding.Inputs
	if compoundCalldata.Data.Input != runtimeCompoundCalldata ||
		compoundCalldata.Data.Execution.Resolution != gen.TransactionExecutionResolutionDirect ||
		compoundCalldata.Data.Decoding.Status != gen.TransactionCalldataDecodingStatusDecoded ||
		compoundCalldata.Data.Decoding.Signature == nil ||
		*compoundCalldata.Data.Decoding.Signature != runtimeCompoundSignature ||
		len(compoundInputs) != 2 || compoundInputs[0].InternalType == nil ||
		*compoundInputs[0].InternalType != "struct Fixture.Config" ||
		len(compoundInputs[0].Components) != 2 || compoundInputs[0].Components[0].Name != "owner" ||
		compoundInputs[0].Components[1].Name != "amount" ||
		compoundInputs[1].Components == nil || len(compoundInputs[1].Components) != 0 {
		h.t.Fatalf("compound transaction calldata = %#v", compoundCalldata.Data)
	}
	compoundShape := fmt.Sprintf(
		"%s:%s:%s:%d|%s:%d",
		compoundInputs[0].Name, compoundInputs[0].Type, *compoundInputs[0].InternalType,
		len(compoundInputs[0].Components), compoundInputs[1].Type, len(compoundInputs[1].Components),
	)
	var nativeCalldata gen.TransactionCalldataResponse
	h.mustGetJSON(ctx, "/api/v1/transactions/"+h.fixture.nativeHash+"/calldata", &nativeCalldata)
	if nativeCalldata.Data.Input != "0x" ||
		nativeCalldata.Data.Execution.Resolution != gen.TransactionExecutionResolutionEmpty ||
		!strings.EqualFold(string(nativeCalldata.Data.Execution.ContextAddress), nativeTransferTarget) ||
		nativeCalldata.Data.Execution.Address != nil || nativeCalldata.Data.Execution.CodeHash != nil ||
		nativeCalldata.Data.Decoding.Status != gen.TransactionCalldataDecodingStatusNotApplicable {
		h.t.Fatalf("native transfer calldata = %#v", nativeCalldata.Data)
	}
	nativeMethod := ""
	for _, transaction := range transactions.Data {
		if strings.EqualFold(string(transaction.Hash), h.fixture.nativeHash) && transaction.Method != nil {
			nativeMethod = *transaction.Method
			break
		}
	}
	if nativeMethod != "Native Transfer" {
		h.t.Fatalf("native transfer list method = %q", nativeMethod)
	}
	eip7702 := h.captureEIP7702API(ctx)
	chart := h.requireHTTPStatus(ctx, "/api/v1/stats/charts/overview", http.StatusOK)
	_ = chart.Body.Close()
	spa := h.requireHTTPStatus(ctx, "/", http.StatusOK)
	body, _ := io.ReadAll(io.LimitReader(spa.Body, 1<<20))
	_ = spa.Body.Close()

	blockHashes := make([]string, 0, len(blocks.Data))
	for _, block := range blocks.Data {
		blockHashes = append(blockHashes, string(block.Hash))
	}
	transactionHashes := make([]string, 0, len(transactions.Data))
	for _, transaction := range transactions.Data {
		transactionHashes = append(transactionHashes, string(transaction.Hash))
	}
	sort.Strings(blockHashes)
	sort.Strings(transactionHashes)
	failedStatus := ""
	if failedTransaction.Status != nil {
		failedStatus = string(*failedTransaction.Status)
	}
	creationAddress := ""
	if creationTransaction.ContractAddress != nil {
		creationAddress = string(*creationTransaction.ContractAddress)
	}
	return apiSnapshot{
		ChainID: config.Data.ChainId, IndexedBlock: status.Data.IndexedBlock,
		LatestBlock: status.Data.LatestBlock, CoreReady: status.Data.CoreReady,
		BackfillComplete: status.Data.BackfillComplete, Features: config.Data.Features,
		BlockHashes: blockHashes, TransactionHashes: transactionHashes,
		FromType: string(from.Data.Type), ContractType: string(contract.Data.Type),
		CreationAddress: creationAddress, FailedStatus: failedStatus,
		FailureState:          string(failure.Data.State),
		FailureDecodingStatus: string(failure.Data.Decoding.Status), FailureError: failure.Data.Error,
		TraceState: string(trace.Data.State), CalldataInput: calldata.Data.Input,
		CalldataResolution:        string(calldata.Data.Execution.Resolution),
		CalldataStatus:            string(calldata.Data.Decoding.Status),
		CompoundCalldataSignature: runtimeCompoundSignature,
		CompoundCalldataShape:     compoundShape,
		NativeMethod:              nativeMethod, NativeCalldataResolution: string(nativeCalldata.Data.Execution.Resolution),
		NativeCalldataStatus: string(nativeCalldata.Data.Decoding.Status), ChartAvailable: true,
		SPA: bytes.Contains(body, []byte("<div id=\"root\">")), SSE: true,
		AuthorityType:             eip7702.authorityType,
		HasDelegationHistory:      eip7702.hasDelegationHistory,
		DelegationStatus:          eip7702.delegationStatus,
		DelegationHistory:         eip7702.delegationHistory,
		AuthorizationOutcomes:     eip7702.authorizationOutcomes,
		DelegatedResolution:       eip7702.delegatedResolution,
		DelegatedExecutionAddress: eip7702.delegatedExecutionAddress,
		ClearingResolution:        eip7702.clearingResolution,
		ClearingStatus:            eip7702.clearingStatus,
		ENSName:                   ensName,
		ENSNameSource:             ensSource,
		EtherscanAdvanced:         etherscanAdvanced,
		EtherscanFunding:          etherscanFunding,
		EtherscanBlockCounts:      etherscanCounts,
		EtherscanNFTHoldings:      etherscanNFTHoldings,
		UserOperationHash:         string(userOperation.Data.Hash),
	}
}

func (h *harness) captureEIP7702API(ctx context.Context) eip7702APISnapshot {
	h.t.Helper()
	var authority gen.AddressResponse
	h.mustGetJSON(ctx, "/api/v1/addresses/"+h.fixture.authority, &authority)
	if string(authority.Data.Type) != "eoa" || !authority.Data.HasDelegationHistory {
		h.t.Fatalf("cleared EIP-7702 authority = %#v", authority.Data)
	}
	var binding gen.DelegationBindingResponse
	h.mustGetJSON(ctx, "/api/v1/addresses/"+h.fixture.authority+"/delegation", &binding)
	if binding.Data.Status != gen.DelegationBindingStatusNotDelegated || binding.Data.Delegate != nil {
		h.t.Fatalf("final EIP-7702 binding = %#v", binding.Data)
	}

	history := make([]gen.DelegationHistoryItem, 0, 3)
	cursor := ""
	for {
		path := "/api/v1/addresses/" + h.fixture.authority + "/delegations?limit=1"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page gen.DelegationHistoryResponse
		h.mustGetJSON(ctx, path, &page)
		history = append(history, page.Data...)
		if page.Meta.NextCursor == nil {
			break
		}
		cursor = string(*page.Meta.NextCursor)
		if len(history) > 3 {
			h.t.Fatalf("delegation history cursor did not terminate: %#v", history)
		}
	}
	expectedKinds := []string{"cleared", "redelegated", "delegated"}
	expectedHashes := []string{h.fixture.clearingHash, h.fixture.redelegationHash, h.fixture.delegationHash}
	if len(history) != len(expectedKinds) {
		h.t.Fatalf("final delegation history = %#v", history)
	}
	historySummary := make([]string, 0, len(history))
	for index, item := range history {
		if string(item.Kind) != expectedKinds[index] ||
			!strings.EqualFold(string(item.TransactionHash), expectedHashes[index]) ||
			strings.EqualFold(string(item.TransactionHash), h.fixture.orphanDelegationHash) {
			h.t.Fatalf("final delegation history[%d] = %#v", index, item)
		}
		historySummary = append(historySummary, string(item.Kind)+":"+string(item.TransactionHash))
	}
	if history[0].PreviousDelegate != nil || history[1].PreviousDelegate == nil ||
		!strings.EqualFold(string(*history[1].PreviousDelegate), h.fixture.delegateB) ||
		history[2].PreviousDelegate != nil {
		h.t.Fatalf("delegation history previous delegates = %#v", history)
	}

	var delegation gen.TransactionAuthorizationResponse
	h.mustGetJSON(ctx, "/api/v1/transactions/"+h.fixture.delegationHash+"/authorizations?limit=100", &delegation)
	if delegation.Data.State != gen.TransactionAuthorizationsStateComplete || len(delegation.Data.Items) != 2 {
		h.t.Fatalf("initial transaction authorizations = %#v", delegation.Data)
	}
	applied := delegation.Data.Items[0]
	skipped := delegation.Data.Items[1]
	assertRuntimeAuthorization(
		h.t, applied, h.fixture.delegationAuths[0], "0",
		gen.EIP7702AuthorizationApplicationStatusApplied, "",
	)
	assertRuntimeAuthorization(
		h.t, skipped, h.fixture.delegationAuths[1], "1",
		gen.EIP7702AuthorizationApplicationStatusSkipped, "nonce_mismatch",
	)

	for _, expected := range []struct {
		hash          string
		authorization types.SetCodeAuthorization
	}{
		{hash: h.fixture.redelegationHash, authorization: h.fixture.redelegationAuth},
		{hash: h.fixture.clearingHash, authorization: h.fixture.clearingAuth},
	} {
		var response gen.TransactionAuthorizationResponse
		h.mustGetJSON(ctx, "/api/v1/transactions/"+expected.hash+"/authorizations?limit=100", &response)
		if response.Data.State != gen.TransactionAuthorizationsStateComplete || len(response.Data.Items) != 1 {
			h.t.Fatalf("transaction %s authorizations = %#v", expected.hash, response.Data)
		}
		assertRuntimeAuthorization(
			h.t, response.Data.Items[0], expected.authorization, "0",
			gen.EIP7702AuthorizationApplicationStatusApplied, "",
		)
	}

	var setCodeTransaction gen.TransactionResponse
	h.mustGetJSON(ctx, "/api/v1/transactions/"+h.fixture.delegationHash, &setCodeTransaction)
	setCodeIncluded := h.requireIncludedTransaction(setCodeTransaction)
	if setCodeIncluded.Type == nil || *setCodeIncluded.Type != "4" {
		h.t.Fatalf("EIP-7702 transaction type = %#v", setCodeIncluded.Type)
	}

	delegatedCalls := []struct {
		hash           string
		delegate       string
		evidenceSource gen.TransactionExecutionEvidenceSource
	}{
		{
			hash:           h.fixture.delegationHash,
			delegate:       h.fixture.delegateB,
			evidenceSource: gen.TransactionExecutionEvidenceSourceRootTraceCodeObservation,
		},
		{
			hash:           h.fixture.redelegationHash,
			delegate:       h.fixture.delegateA,
			evidenceSource: gen.TransactionExecutionEvidenceSourceRootTraceCodeObservation,
		},
		{
			hash:           h.fixture.delegatedCallHash,
			delegate:       h.fixture.delegateA,
			evidenceSource: gen.TransactionExecutionEvidenceSourcePrestateTracer,
		},
	}
	for _, call := range delegatedCalls {
		var response gen.TransactionCalldataResponse
		h.mustGetJSON(ctx, "/api/v1/transactions/"+call.hash+"/calldata", &response)
		if response.Data.Execution.Resolution != gen.TransactionExecutionResolutionEip7702Delegate ||
			response.Data.Execution.EvidenceSource != call.evidenceSource ||
			!strings.EqualFold(string(response.Data.Execution.ContextAddress), h.fixture.authority) ||
			response.Data.Execution.Address == nil ||
			!strings.EqualFold(string(*response.Data.Execution.Address), call.delegate) {
			h.t.Fatalf("delegated calldata %s = %#v", call.hash, response.Data)
		}
	}
	var clearing gen.TransactionCalldataResponse
	h.mustGetJSON(ctx, "/api/v1/transactions/"+h.fixture.clearingHash+"/calldata", &clearing)
	if clearing.Data.Input != "0x55241077" || string(clearing.Data.Execution.Resolution) != "empty" ||
		clearing.Data.Decoding.Status != gen.TransactionCalldataDecodingStatusNotApplicable ||
		clearing.Data.Execution.Address != nil || clearing.Data.Execution.CodeHash != nil {
		h.t.Fatalf("clearing calldata = %#v", clearing.Data)
	}

	return eip7702APISnapshot{
		authorityType:             string(authority.Data.Type),
		hasDelegationHistory:      authority.Data.HasDelegationHistory,
		delegationStatus:          string(binding.Data.Status),
		delegationHistory:         strings.Join(historySummary, ","),
		authorizationOutcomes:     string(applied.ApplicationStatus) + "," + string(skipped.ApplicationStatus) + ":" + string(*skipped.SkipReason),
		delegatedResolution:       string(gen.TraceExecutionResolutionEip7702Delegate),
		delegatedExecutionAddress: h.fixture.delegateA,
		clearingResolution:        string(clearing.Data.Execution.Resolution),
		clearingStatus:            string(clearing.Data.Decoding.Status),
	}
}

func assertRuntimeAuthorization(
	t *testing.T,
	actual gen.EIP7702Authorization,
	expected types.SetCodeAuthorization,
	index string,
	applicationStatus gen.EIP7702AuthorizationApplicationStatus,
	skipReason string,
) {
	t.Helper()
	authority, err := expected.Authority()
	if err != nil {
		t.Fatalf("recover expected authorization authority: %v", err)
	}
	expectedR := common.BytesToHash(expected.R.Bytes()).Hex()
	expectedS := common.BytesToHash(expected.S.Bytes()).Hex()
	if string(actual.Index) != index || string(actual.ChainId) != expected.ChainID.ToBig().String() ||
		string(actual.Nonce) != strconv.FormatUint(expected.Nonce, 10) ||
		!strings.EqualFold(string(actual.Delegate), expected.Address.Hex()) ||
		actual.Authority == nil || !strings.EqualFold(string(*actual.Authority), authority.Hex()) ||
		actual.YParity != int(expected.V) || !strings.EqualFold(string(actual.R), expectedR) ||
		!strings.EqualFold(string(actual.S), expectedS) ||
		actual.SignatureStatus != gen.EIP7702AuthorizationSignatureStatusValid ||
		actual.ApplicationStatus != applicationStatus {
		t.Fatalf("authorization = %#v, want exact signed tuple %#v", actual, expected)
	}
	if skipReason == "" && actual.SkipReason != nil {
		t.Fatalf("authorization skip reason = %q, want absent", *actual.SkipReason)
	}
	if skipReason != "" && (actual.SkipReason == nil || string(*actual.SkipReason) != skipReason) {
		t.Fatalf("authorization skip reason = %#v, want %q", actual.SkipReason, skipReason)
	}
}

type receiptProxy struct {
	upstream          string
	listener          net.Listener
	server            *http.Server
	client            *http.Client
	normalized        atomic.Uint64
	clearedDelegation atomic.Uint64
	blockTraceCalls   atomic.Uint64
	transactionTraces atomic.Uint64
	ensAddress        common.Address
	ensCalls          atomic.Uint64
}

func startReceiptProxy(t *testing.T, upstream string) *receiptProxy {
	t.Helper()
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := &receiptProxy{
		upstream: upstream, listener: listener,
		client: &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
	proxy.server = &http.Server{
		Handler:           proxy,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	go func() {
		_ = proxy.server.Serve(listener)
	}()
	return proxy
}

func (p *receiptProxy) containerURL() string {
	port := p.listener.Addr().(*net.TCPAddr).Port
	return fmt.Sprintf("http://host.docker.internal:%d", port)
}

func (p *receiptProxy) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "JSON-RPC requires POST", http.StatusMethodNotAllowed)
		return
	}
	const maximumBody = 16 << 20
	body, err := io.ReadAll(io.LimitReader(request.Body, maximumBody+1))
	if err != nil || len(body) > maximumBody {
		http.Error(writer, "bounded JSON-RPC request required", http.StatusBadRequest)
		return
	}
	if response, handled := p.ensResponse(body); handled {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(response)
		return
	}
	requestMethod := rpcRequestMethod(body)
	switch requestMethod {
	case "debug_traceBlockByHash":
		p.blockTraceCalls.Add(1)
	case "debug_traceTransaction":
		p.transactionTraces.Add(1)
	}
	upstreamRequest, err := http.NewRequestWithContext(
		request.Context(), http.MethodPost, p.upstream, bytes.NewReader(body),
	)
	if err != nil {
		http.Error(writer, "create upstream JSON-RPC request", http.StatusBadGateway)
		return
	}
	upstreamRequest.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(upstreamRequest)
	if err != nil {
		http.Error(writer, "upstream JSON-RPC unavailable", http.StatusBadGateway)
		return
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maximumBody+1))
	if err != nil || len(responseBody) > maximumBody {
		http.Error(writer, "bounded JSON-RPC response required", http.StatusBadGateway)
		return
	}
	if response.StatusCode != http.StatusOK {
		writer.WriteHeader(response.StatusCode)
		_, _ = writer.Write(responseBody)
		return
	}

	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		http.Error(writer, "invalid upstream JSON-RPC response", http.StatusBadGateway)
		return
	}
	p.normalized.Add(normalizeAnvilReceipts(payload))
	p.clearedDelegation.Add(normalizeAnvilClearedDelegations(payload))
	encoded, err := json.Marshal(payload)
	if err != nil {
		http.Error(writer, "encode normalized JSON-RPC response", http.StatusBadGateway)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_, _ = writer.Write(encoded)
}

type runtimeJSONRPCRequest struct {
	JSONRPC string            `json:"jsonrpc"`
	ID      json.RawMessage   `json:"id"`
	Method  string            `json:"method"`
	Params  []json.RawMessage `json:"params"`
}

func (p *receiptProxy) ensResponse(body []byte) ([]byte, bool) {
	var request runtimeJSONRPCRequest
	if json.Unmarshal(body, &request) != nil || len(request.ID) == 0 {
		return nil, false
	}
	switch request.Method {
	case "eth_getCode":
		if len(request.Params) < 1 {
			return nil, false
		}
		var address string
		if json.Unmarshal(request.Params[0], &address) != nil ||
			(!strings.EqualFold(address, runtimeENSRegistry) && !strings.EqualFold(address, runtimeENSResolver)) {
			return nil, false
		}
		p.ensCalls.Add(1)
		return runtimeRPCResult(request.ID, "0x6000"), true
	case "eth_call":
		if len(request.Params) < 1 {
			return nil, false
		}
		var call struct {
			To   string `json:"to"`
			Data string `json:"data"`
		}
		if json.Unmarshal(request.Params[0], &call) != nil || !strings.EqualFold(call.To, runtimeENSResolver) {
			return nil, false
		}
		input := common.FromHex(call.Data)
		if len(input) < 4 {
			return runtimeRPCError(request.ID, -32602, "invalid ENS calldata", ""), true
		}
		p.ensCalls.Add(1)
		var output []byte
		var err error
		switch {
		case bytes.Equal(input[:4], runtimeENSABI.Methods["resolve"].ID):
			inner, packErr := runtimeLegacyAddressABI.Methods["addr"].Outputs.Pack(p.ensAddress)
			if packErr != nil {
				panic(packErr)
			}
			output, err = runtimeENSABI.Methods["resolve"].Outputs.Pack(
				inner, common.HexToAddress(runtimeENSResolver),
			)
		case bytes.Equal(input[:4], runtimeENSABI.Methods["reverse"].ID):
			output, err = runtimeENSABI.Methods["reverse"].Outputs.Pack(
				runtimeENSName, common.HexToAddress(runtimeENSResolver), common.HexToAddress(runtimeENSRegistry),
			)
		default:
			return runtimeRPCError(request.ID, 3, "execution reverted", "0x"), true
		}
		if err != nil {
			panic(err)
		}
		return runtimeRPCResult(request.ID, hexutil.Encode(output)), true
	default:
		return nil, false
	}
}

type ensMainnetRPC struct {
	listener net.Listener
	server   *http.Server
	header   map[string]any
}

func startENSMainnetRPC(t *testing.T) *ensMainnetRPC {
	t.Helper()
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	header := core.DefaultGenesisBlock().ToBlock().Header()
	encoded, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if json.Unmarshal(encoded, &wire) != nil {
		t.Fatal("decode Mainnet genesis fixture")
	}
	wire["hash"] = header.Hash().Hex()
	wire["number"] = "0x0"
	fixture := &ensMainnetRPC{listener: listener, header: wire}
	fixture.server = &http.Server{
		Handler: fixture, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
	}
	go func() { _ = fixture.server.Serve(listener) }()
	return fixture
}

func (fixture *ensMainnetRPC) containerURL() string {
	return fmt.Sprintf("http://host.docker.internal:%d", fixture.listener.Addr().(*net.TCPAddr).Port)
}

func (fixture *ensMainnetRPC) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "JSON-RPC requires POST", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
	if err != nil {
		http.Error(writer, "read JSON-RPC", http.StatusBadRequest)
		return
	}
	var rpcRequest runtimeJSONRPCRequest
	if json.Unmarshal(body, &rpcRequest) != nil {
		http.Error(writer, "invalid JSON-RPC", http.StatusBadRequest)
		return
	}
	var response []byte
	switch rpcRequest.Method {
	case "eth_chainId":
		response = runtimeRPCResult(rpcRequest.ID, "0x1")
	case "eth_getBlockByNumber", "eth_getBlockByHash":
		response = runtimeRPCResult(rpcRequest.ID, fixture.header)
	case "eth_call":
		definition := runtimeENSABI.Errors["ResolverNotFound"]
		arguments, packErr := definition.Inputs.Pack([]byte(runtimeENSName))
		if packErr != nil {
			panic(packErr)
		}
		data := append(append([]byte(nil), definition.ID[:4]...), arguments...)
		response = runtimeRPCError(rpcRequest.ID, 3, "execution reverted", hexutil.Encode(data))
	default:
		response = runtimeRPCError(rpcRequest.ID, -32601, "method not found", "")
	}
	writer.Header().Set("Content-Type", "application/json")
	_, _ = writer.Write(response)
}

func runtimeRPCResult(id json.RawMessage, result any) []byte {
	encoded, _ := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  any             `json:"result"`
	}{JSONRPC: "2.0", ID: id, Result: result})
	return encoded
}

func runtimeRPCError(id json.RawMessage, code int, message, data string) []byte {
	type rpcError struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    string `json:"data,omitempty"`
	}
	encoded, _ := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   rpcError        `json:"error"`
	}{JSONRPC: "2.0", ID: id, Error: rpcError{Code: code, Message: message, Data: data}})
	return encoded
}

func mustRuntimeENSABI(definition string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(definition))
	if err != nil {
		panic(err)
	}
	return parsed
}

var runtimeENSABI = mustRuntimeENSABI(`[
  {"type":"function","name":"resolve","stateMutability":"view","inputs":[{"name":"name","type":"bytes"},{"name":"data","type":"bytes"}],"outputs":[{"name":"result","type":"bytes"},{"name":"resolver","type":"address"}]},
  {"type":"function","name":"reverse","stateMutability":"view","inputs":[{"name":"lookupAddress","type":"bytes"},{"name":"coinType","type":"uint256"}],"outputs":[{"name":"primary","type":"string"},{"name":"resolver","type":"address"},{"name":"reverseResolver","type":"address"}]},
  {"type":"error","name":"ResolverNotFound","inputs":[{"name":"name","type":"bytes"}]}
]`)

var runtimeLegacyAddressABI = mustRuntimeENSABI(`[
  {"type":"function","name":"addr","stateMutability":"view","inputs":[{"name":"node","type":"bytes32"}],"outputs":[{"name":"address","type":"address"}]}
]`)

func rpcRequestMethod(body []byte) string {
	var request struct {
		Method string `json:"method"`
	}
	if json.Unmarshal(body, &request) != nil {
		return ""
	}
	return request.Method
}

func normalizeAnvilClearedDelegations(value any) uint64 {
	switch typed := value.(type) {
	case []any:
		var normalized uint64
		for _, item := range typed {
			normalized += normalizeAnvilClearedDelegations(item)
		}
		return normalized
	case map[string]any:
		var normalized uint64
		pre, hasPre := typed["pre"].(map[string]any)
		post, hasPost := typed["post"].(map[string]any)
		if hasPre && hasPost {
			for address, preValue := range pre {
				preAccount, preOK := preValue.(map[string]any)
				postAccount, postOK := post[address].(map[string]any)
				code, hasCode := preAccount["code"].(string)
				_, postHasCode := postAccount["code"]
				_, postHasNonce := postAccount["nonce"]
				if preOK && postOK && hasCode && strings.HasPrefix(strings.ToLower(code), "0xef0100") &&
					!postHasCode && postHasNonce {
					// Anvil v1.7.1 omits code when a Prague authorization clears
					// delegation. geth's diffMode contract uses an explicit empty
					// value for a changed scalar, so normalize only this fixture gap.
					postAccount["code"] = "0x"
					normalized++
				}
			}
		}
		for _, item := range typed {
			normalized += normalizeAnvilClearedDelegations(item)
		}
		return normalized
	default:
		return 0
	}
}

func normalizeAnvilReceipts(value any) uint64 {
	switch typed := value.(type) {
	case []any:
		var normalized uint64
		for _, item := range typed {
			normalized += normalizeAnvilReceipts(item)
		}
		return normalized
	case map[string]any:
		var normalized uint64
		if _, receipt := typed["transactionHash"]; receipt {
			if _, hasPrice := typed["blobGasPrice"]; hasPrice {
				if _, hasUsed := typed["blobGasUsed"]; !hasUsed {
					delete(typed, "blobGasPrice")
					normalized++
				}
			}
		}
		for _, item := range typed {
			normalized += normalizeAnvilReceipts(item)
		}
		return normalized
	default:
		return 0
	}
}

func deterministicRuntimeKey(t *testing.T, seed string) *ecdsa.PrivateKey {
	t.Helper()
	key, err := crypto.ToECDSA(crypto.Keccak256([]byte(seed)))
	if err != nil {
		t.Fatalf("derive deterministic runtime key: %v", err)
	}
	return key
}

func (h *harness) signAuthorization(
	key *ecdsa.PrivateKey,
	delegate common.Address,
	nonce uint64,
) types.SetCodeAuthorization {
	h.t.Helper()
	authorization, err := types.SignSetCode(key, types.SetCodeAuthorization{
		ChainID: *uint256.NewInt(1),
		Address: delegate,
		Nonce:   nonce,
	})
	if err != nil {
		h.t.Fatalf("sign EIP-7702 authorization: %v", err)
	}
	return authorization
}

func (h *harness) sendSetCodeTransaction(
	ctx context.Context,
	authorizations []types.SetCodeAuthorization,
	data []byte,
) string {
	h.t.Helper()
	transaction := types.NewTx(&types.SetCodeTx{
		ChainID:    uint256.NewInt(1),
		Nonce:      h.pendingNonce(ctx, h.fixture.sponsor),
		GasTipCap:  uint256.NewInt(1_000_000_000),
		GasFeeCap:  uint256.NewInt(10_000_000_000),
		Gas:        500_000,
		To:         common.HexToAddress(h.fixture.authority),
		Value:      uint256.NewInt(0),
		Data:       data,
		AccessList: types.AccessList{},
		AuthList:   authorizations,
	})
	return h.sendRawTransaction(ctx, transaction, h.fixture.sponsorKey)
}

func (h *harness) sendDynamicFeeTransaction(
	ctx context.Context,
	to common.Address,
	data []byte,
) string {
	h.t.Helper()
	transaction := types.NewTx(&types.DynamicFeeTx{
		ChainID:   big.NewInt(1),
		Nonce:     h.pendingNonce(ctx, h.fixture.sponsor),
		GasTipCap: big.NewInt(1_000_000_000),
		GasFeeCap: big.NewInt(10_000_000_000),
		Gas:       500_000,
		To:        &to,
		Value:     big.NewInt(0),
		Data:      data,
	})
	return h.sendRawTransaction(ctx, transaction, h.fixture.sponsorKey)
}

func (h *harness) sendRawTransaction(
	ctx context.Context,
	transaction *types.Transaction,
	key *ecdsa.PrivateKey,
) string {
	h.t.Helper()
	signed, err := types.SignTx(transaction, types.LatestSignerForChainID(big.NewInt(1)), key)
	if err != nil {
		h.t.Fatalf("sign raw transaction: %v", err)
	}
	payload, err := signed.MarshalBinary()
	if err != nil {
		h.t.Fatalf("encode raw transaction: %v", err)
	}
	var hash string
	h.rpcCall(ctx, &hash, "eth_sendRawTransaction", hexutil.Encode(payload))
	if !common.IsHexHash(hash) {
		h.t.Fatalf("invalid raw transaction hash %q", hash)
	}
	return hash
}

func (h *harness) pendingNonce(ctx context.Context, address string) uint64 {
	h.t.Helper()
	var nonce hexutil.Uint64
	h.rpcCall(ctx, &nonce, "eth_getTransactionCount", address, "pending")
	return uint64(nonce)
}

func (h *harness) sendTransaction(ctx context.Context, transaction map[string]any) string {
	var hash string
	h.rpcCall(ctx, &hash, "eth_sendTransaction", transaction)
	if !common.IsHexHash(hash) {
		h.t.Fatalf("invalid transaction hash %q", hash)
	}
	return hash
}

func (h *harness) assertCurrentDelegation(ctx context.Context, delegate string) {
	h.t.Helper()
	var response gen.DelegationBindingResponse
	h.mustGetJSON(ctx, "/api/v1/addresses/"+h.fixture.authority+"/delegation", &response)
	if delegate == "" {
		if response.Data.Status != gen.DelegationBindingStatusNotDelegated || response.Data.Delegate != nil {
			h.t.Fatalf("cleared delegation binding = %#v", response.Data)
		}
		return
	}
	if response.Data.Status != gen.DelegationBindingStatusDelegated || response.Data.Delegate == nil ||
		!strings.EqualFold(string(*response.Data.Delegate), delegate) {
		h.t.Fatalf("delegation binding = %#v, want delegate %s", response.Data, delegate)
	}
}

func (h *harness) assertCanonicalDelegationHistory(
	ctx context.Context,
	kinds []string,
	hashes []string,
) {
	h.t.Helper()
	var response gen.DelegationHistoryResponse
	h.mustGetJSON(ctx, "/api/v1/addresses/"+h.fixture.authority+"/delegations?limit=100", &response)
	if len(response.Data) != len(kinds) || len(kinds) != len(hashes) {
		h.t.Fatalf("delegation history = %#v, want kinds=%v hashes=%v", response.Data, kinds, hashes)
	}
	for index, item := range response.Data {
		if string(item.Kind) != kinds[index] || !strings.EqualFold(string(item.TransactionHash), hashes[index]) {
			h.t.Fatalf("delegation history[%d] = %#v, want kind=%s hash=%s", index, item, kinds[index], hashes[index])
		}
		if strings.EqualFold(string(item.TransactionHash), h.fixture.orphanDelegationHash) {
			h.t.Fatalf("orphan authorization leaked into canonical history: %#v", item)
		}
	}
}

func (h *harness) mine(ctx context.Context, timestamp uint64) {
	var accepted any
	h.rpcCall(ctx, &accepted, "evm_setNextBlockTimestamp", hexutil.EncodeUint64(timestamp))
	h.rpcCall(ctx, &accepted, "evm_mine")
}

func (h *harness) latestBlock(ctx context.Context) rpcBlock {
	var block rpcBlock
	h.rpcCall(ctx, &block, "eth_getBlockByNumber", "latest", false)
	if !common.IsHexHash(block.Hash) {
		h.t.Fatalf("invalid latest block %#v", block)
	}
	return block
}

func (h *harness) waitReceipt(ctx context.Context, hash string) rpcReceipt {
	var receipt rpcReceipt
	waitFor(h.t, ctx, "receipt "+hash, func() (bool, string, error) {
		err := h.rpc.CallContext(ctx, &receipt, "eth_getTransactionReceipt", hash)
		return err == nil && receipt.Status != "", fmt.Sprintf("%+v", receipt), err
	})
	return receipt
}

func (h *harness) rpcCall(ctx context.Context, result any, method string, arguments ...any) {
	h.t.Helper()
	if err := h.rpc.CallContext(ctx, result, method, arguments...); err != nil {
		h.t.Fatalf("RPC %s: %v", method, err)
	}
}

func (h *harness) canonicalHash(ctx context.Context, height uint64) string {
	var hash []byte
	if err := h.db.QueryRow(ctx, `
		SELECT block_hash FROM canonical_blocks WHERE chain_id = 1 AND number = $1
	`, int64(height)).Scan(&hash); err != nil {
		h.t.Fatal(err)
	}
	return "0x" + hex.EncodeToString(hash)
}

func (h *harness) compose(ctx context.Context, arguments ...string) {
	h.t.Helper()
	if _, err := h.project.Run(ctx, arguments...); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) request(ctx context.Context, path string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	return h.http.Do(request)
}

func (h *harness) requireHTTPStatus(ctx context.Context, path string, expected int) *http.Response {
	h.t.Helper()
	response, err := h.request(ctx, path)
	if err != nil {
		h.t.Fatalf("GET %s: %v", path, err)
	}
	if response.StatusCode != expected {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
		h.t.Fatalf("GET %s status=%d want=%d body=%s", path, response.StatusCode, expected, body)
	}
	return response
}

func (h *harness) getJSON(ctx context.Context, path string, output any) error {
	response, err := h.request(ctx, path)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("GET %s status=%d body=%s", path, response.StatusCode, body)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(output)
}

func (h *harness) mustGetJSON(ctx context.Context, path string, output any) {
	h.t.Helper()
	if err := h.getJSON(ctx, path, output); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) writeJSONArtifact(name string, value any) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		h.t.Errorf("encode artifact %s: %v", name, err)
		return
	}
	h.writeArtifact(name, encoded)
}

func (h *harness) writeArtifact(name string, contents []byte) {
	if err := os.WriteFile(filepath.Join(h.artifacts, name), h.redact(contents), 0o600); err != nil {
		h.t.Errorf("write artifact %s: %v", name, err)
	}
}

func (h *harness) redact(contents []byte) []byte {
	redacted := append([]byte(nil), contents...)
	for _, secret := range h.secrets {
		if secret != "" {
			redacted = bytes.ReplaceAll(redacted, []byte(secret), []byte("[REDACTED]"))
		}
	}
	return redacted
}

func (h *harness) enterPhase(phase string) {
	h.phase = phase
	h.t.Logf("phase: %s", phase)
}

func (h *harness) captureDiagnostics(ctx context.Context, failed bool) string {
	psOutput, psErr := h.project.Run(ctx, "ps", "--all")
	logOutput, logErr := h.project.Run(ctx, "logs", "--no-color", "--timestamps")
	h.writeArtifact("compose-ps.txt", psOutput)
	h.writeArtifact("compose.log", logOutput)

	status := "retained"
	if failed {
		status = "failed"
	}
	summary := fmt.Sprintf(
		"status=%s\nmode=%s\nphase=%s\nproject=%s\ncompose_ps_error=%s\ncompose_logs_error=%s\n",
		status,
		h.mode,
		h.phase,
		h.project.Name,
		diagnosticError(psErr),
		diagnosticError(logErr),
	)
	h.writeArtifact("failure-summary.txt", []byte(summary))

	ps := strings.TrimSpace(string(psOutput))
	if ps == "" {
		if psErr != nil {
			return "unavailable: " + diagnosticError(psErr)
		}
		return "(no containers)"
	}
	return ps
}

func diagnosticError(err error) string {
	if err == nil {
		return "none"
	}
	message := strings.TrimSpace(err.Error())
	if first, _, found := strings.Cut(message, "\n"); found {
		message = first
	}
	if message == "" {
		return "unknown error"
	}
	return message
}

func (h *harness) cleanup() {
	if h.rpc != nil {
		h.rpc.Close()
	}
	if h.db != nil {
		h.db.Close()
	}
	failed := h.t.Failed()
	keepArtifacts := strings.EqualFold(os.Getenv("RUNTIME_E2E_KEEP_ARTIFACTS"), "true")
	retainArtifacts := failed || keepArtifacts
	if retainArtifacts {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		ps := h.captureDiagnostics(ctx, failed)
		cancel()
		if failed {
			h.t.Logf(
				"runtime E2E failure: mode=%s phase=%s diagnostics=%s\ncompose ps:\n%s",
				h.mode,
				h.phase,
				h.artifacts,
				ps,
			)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	if err := h.project.Down(ctx); err != nil {
		h.t.Logf("cleanup Compose project: %v", err)
		if !retainArtifacts {
			h.phase = "cleanup"
			diagnosticCtx, diagnosticCancel := context.WithTimeout(context.Background(), 30*time.Second)
			ps := h.captureDiagnostics(diagnosticCtx, true)
			diagnosticCancel()
			h.t.Logf(
				"runtime E2E failure: mode=%s phase=%s diagnostics=%s\ncompose ps:\n%s",
				h.mode,
				h.phase,
				h.artifacts,
				ps,
			)
		}
		failed = true
	}
	cancel()
	if h.rpcProxy != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = h.rpcProxy.server.Shutdown(ctx)
		cancel()
	}
	if h.ensRPC != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = h.ensRPC.server.Shutdown(ctx)
		cancel()
	}
	if failed || keepArtifacts {
		h.t.Logf("runtime E2E artifacts retained at %s", h.artifacts)
		return
	}
	if err := os.RemoveAll(h.artifacts); err != nil {
		h.t.Logf("remove runtime E2E artifacts: %v", err)
	}
}

func waitFor(t *testing.T, ctx context.Context, description string, check func() (bool, string, error)) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	var last string
	var lastErr error
	consecutiveErrors := 0
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			t.Fatalf("wait for %s: %v (last=%s, error=%v)", description, err, last, lastErr)
		}
		ok, state, err := check()
		last, lastErr = state, err
		if ok {
			return
		}
		if err != nil {
			consecutiveErrors++
			if consecutiveErrors >= 10 {
				t.Fatalf("wait for %s repeatedly failed (last=%s, error=%v)", description, last, lastErr)
			}
		} else {
			consecutiveErrors = 0
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s (last=%s, error=%v)", description, last, lastErr)
}

func runHostCommand(t *testing.T, ctx context.Context, directory, name string, arguments ...string) {
	t.Helper()
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf(
			"%s %s: %v\n%s",
			name,
			strings.Join(arguments, " "),
			err,
			strings.TrimSpace(string(output)),
		)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("cannot locate repository root")
		}
		directory = parent
	}
}

func dockerCommand() string {
	return valueOrDefault("DOCKER", "docker")
}

func valueOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func mustDecodeUint64(t *testing.T, value string) uint64 {
	t.Helper()
	number, err := hexutil.DecodeUint64(value)
	if err != nil {
		t.Fatalf("decode quantity %q: %v", value, err)
	}
	return number
}
