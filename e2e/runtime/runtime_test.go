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
	"math/big"
	"net"
	"net/http"
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

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/loadtest"
	"github.com/islishude/etherview/internal/testcompose"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	runtimeTimeout = 15 * time.Minute
	waitTimeout    = 3 * time.Minute

	// NoCBOR.sol is the repository's reviewed Solidity 0.8.30 fixture from
	// internal/verify/testdata/compiler/solidity/output.no-cbor.json. Deploying
	// it exercises receipt-authenticated contract creation without introducing
	// another compiler or network dependency into this test.
	noCBORCreationBytecode = "0x6080604052348015600e575f5ffd5b50603e80601a5f395ff3fe6080604052348015600e575f5ffd5b50600436106026575f3560e01c80633fa4f24514602a575b5f5ffd5b600760405190815260200160405180910390f3"
)

type fixture struct {
	genesisHash     string
	accounts        []string
	nativeHash      string
	pendingHash     string
	creationHash    string
	failedHash      string
	contractAddress string
	blockOneHash    string
	orphanHash      string
	finalHash       string
	finalHeight     uint64
	snapshotID      string
}

type durableSnapshot struct {
	GenesisHash       string
	Canonical         string
	BlockCount        int64
	TransactionCount  int64
	ReceiptCount      int64
	CompleteStages    int64
	FailedStages      int64
	UnpublishedOutbox int64
	Checkpoint        string
	OrphanCount       int64
	Rollup            string
	Statistics        string
}

type apiSnapshot struct {
	ChainID           string
	IndexedBlock      string
	LatestBlock       string
	CoreReady         bool
	BackfillComplete  bool
	Features          map[string]bool
	BlockHashes       []string
	TransactionHashes []string
	FromType          string
	ContractType      string
	CreationAddress   string
	FailedStatus      string
	TraceState        string
	ChartAvailable    bool
	SPA               bool
	SSE               bool
}

type modeResult struct {
	durable durableSnapshot
	api     apiSnapshot
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
				logOutput: []byte("api | partial service log\n"),
				logErr:    test.logErr,
			}
			h := &harness{
				t: t, mode: "distributed", phase: "process-native TLS",
				project: project, artifacts: artifacts,
			}

			if got := h.captureDiagnostics(t.Context(), true); got != "NAME STATUS\napi restarting" {
				t.Fatalf("terminal Compose state = %q", got)
			}
			assertArtifactContents(t, artifacts, "compose-ps.txt", "api restarting")
			assertArtifactContents(t, artifacts, "compose.log", "partial service log")
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
	project.Env = runtimeEnvironment(root, baseTimestamp)
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
		h.rpcProxy = startReceiptProxy(t, "http://"+binding)
		h.project.Env["ETHERVIEW_RUNTIME_RPC_URL"] = h.rpcProxy.containerURL()
		h.initializeFixture(ctx)
		h.project.Env["ETHERVIEW_CHAIN_GENESIS_HASH"] = h.fixture.genesisHash
	}

	h.enterPhase("fresh production topology")
	{
		h.startTopology(ctx)
		h.connectDatabase(ctx)
		h.resolveAPI(ctx)
		h.waitReady(ctx)
		h.waitCanonical(ctx, 1, h.fixture.blockOneHash)
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
	}

	var firstRollup string
	h.enterPhase("first branch publication")
	{
		h.mine(ctx, h.baseTimestamp+2)
		h.fixture.orphanHash = h.latestBlock(ctx).Hash
		h.waitCanonical(ctx, 2, h.fixture.orphanHash)
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
		h.fixture.failedHash = h.sendTransaction(ctx, map[string]any{
			"from": h.fixture.accounts[0], "to": h.fixture.contractAddress,
			"data": "0xffffffff", "gas": "0x186a0", "gasPrice": "0x3b9aca00",
		})
		h.mine(ctx, h.baseTimestamp+13)
		finalBlock := h.latestBlock(ctx)
		h.fixture.finalHash = finalBlock.Hash
		h.fixture.finalHeight = mustDecodeUint64(t, finalBlock.Number)
		h.waitCanonical(ctx, h.fixture.finalHeight, h.fixture.finalHash)
		failedReceipt := h.waitReceipt(ctx, h.fixture.failedHash)
		if failedReceipt.Status != "0x0" {
			t.Fatalf("failed call receipt status = %s, want 0x0", failedReceipt.Status)
		}
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
			RequestTimeout: 2 * time.Second, MaximumP95: time.Second,
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

func runtimeEnvironment(root string, baseTimestamp uint64) map[string]string {
	return map[string]string{
		"ETHERVIEW_IMAGE":                 valueOrDefault("IMAGE", "etherview:local"),
		"ETHERVIEW_RUNTIME_FIXTURE_IMAGE": valueOrDefault("ETHERVIEW_RUNTIME_FIXTURE_IMAGE", "ghcr.io/foundry-rs/foundry:stable"),
		"ANVIL_ARGS": valueOrDefault(
			"ANVIL_ARGS",
			fmt.Sprintf(
				"--timestamp %d --hardfork shanghai --mnemonic-seed-unsafe 424242",
				baseTimestamp,
			),
		),
		"ETHERVIEW_CONFIG_FILE":        filepath.Join(root, "deploy/config.example.yaml"),
		"ETHERVIEW_RPC_URLS":           "http://runtime-fixture:8545",
		"ETHERVIEW_CHAIN_ID":           "1",
		"ETHERVIEW_CHAIN_GENESIS_HASH": "",
		"ETHERVIEW_ADAPTER_NAMESPACE":  "runtime-e2e",
		"ETHERVIEW_PORT":               "0",
		"ETHERVIEW_METRICS_PORT":       "0",
		"POSTGRES_PASSWORD":            "etherview-runtime-e2e",
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
	h.fixture.nativeHash = h.sendTransaction(ctx, map[string]any{
		"from": h.fixture.accounts[0], "to": h.fixture.accounts[1],
		"value": "0x5", "gas": "0x5208", "gasPrice": "0x3b9aca00",
	})
	h.mine(ctx, h.baseTimestamp+1)
	h.fixture.blockOneHash = h.latestBlock(ctx).Hash
	h.fixture.pendingHash = h.sendTransaction(ctx, map[string]any{
		"from": h.fixture.accounts[0], "to": h.fixture.accounts[2],
		"value": "0x9", "gas": "0x5208", "gasPrice": "0x3b9aca00",
	})
	h.rpcCall(ctx, &h.fixture.snapshotID, "evm_snapshot")
	if h.fixture.snapshotID == "" {
		h.t.Fatal("fixture returned empty snapshot ID")
	}
}

func (h *harness) startTopology(ctx context.Context) {
	if h.mode == "distributed" {
		h.compose(ctx, "up", "-d", "--wait", "--wait-timeout", "90", "api")
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
		h.compose(ctx, "up", "-d", "--wait", "--wait-timeout", "90", "--remove-orphans",
			"--scale", "sync=2", "--scale", "enrich=2")
		return
	}
	h.compose(ctx, "up", "-d", "--wait", "--wait-timeout", "90", "--remove-orphans")
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
	for key, value := range h.project.Env {
		project.Env[key] = value
	}
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
	waitFor(h.t, ctx, fmt.Sprintf("canonical block %d with six complete stages", height), func() (bool, string, error) {
		var canonical string
		var checkpoint int64
		var complete, active, failed, unpublished int64
		err := h.db.QueryRow(ctx, `
			SELECT
				COALESCE((SELECT encode(block_hash, 'hex') FROM canonical_blocks
					WHERE chain_id = 1 AND number = $1), ''),
				COALESCE((SELECT contiguous_through::bigint FROM index_checkpoints
					WHERE chain_id = 1 AND stage = 'core'), -1),
				(SELECT count(*) FROM published_block_stage_results
					WHERE chain_id = 1 AND block_number = $1 AND block_hash = decode($2, 'hex')
					  AND state = 'complete' AND (stage, stage_version) IN (
					    ('proxy',1),('abi',1),('token',1),('stats',3),('trace',1),('state_diff',1))),
				(SELECT count(*) FROM durable_jobs WHERE status IN ('queued','leased')),
				(SELECT count(*) FROM published_block_stage_results
					WHERE chain_id = 1 AND block_number = $1 AND block_hash = decode($2, 'hex')
					  AND state <> 'complete' AND (stage, stage_version) IN (
					    ('proxy',1),('abi',1),('token',1),('stats',3),('trace',1),('state_diff',1))),
				(SELECT count(*) FROM transactional_outbox WHERE published_at IS NULL)
		`, heightArgument, expected).Scan(&canonical, &checkpoint, &complete, &active, &failed, &unpublished)
		state := fmt.Sprintf("hash=%s checkpoint=%d complete=%d active=%d failed=%d outbox=%d",
			canonical, checkpoint, complete, active, failed, unpublished)
		return err == nil && canonical == expected && checkpoint == int64(height) &&
			complete == 6 && active == 0 && failed == 0 && unpublished == 0, state, err
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
	streamCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(streamCtx, http.MethodGet, h.baseURL+"/api/v1/events", nil)
	if err != nil {
		h.t.Fatal(err)
	}
	request.Header.Set("Last-Event-ID", "0")
	response, err := h.http.Do(request)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK ||
		response.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" {
		h.t.Fatalf("SSE status=%d headers=%v", response.StatusCode, response.Header)
	}
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "event: ") {
			return
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		h.t.Fatalf("read SSE: %v", err)
	}
	h.t.Fatal("SSE did not replay a durable event")
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
				FROM block_statistics WHERE chain_id = 1), '')
	`).Scan(
		&snapshot.GenesisHash, &snapshot.Canonical, &snapshot.BlockCount,
		&snapshot.TransactionCount, &snapshot.ReceiptCount, &snapshot.CompleteStages,
		&snapshot.FailedStages, &snapshot.UnpublishedOutbox, &snapshot.Checkpoint,
		&snapshot.OrphanCount, &snapshot.Rollup, &snapshot.Statistics,
	)
	if err != nil {
		h.t.Fatal(err)
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
	var from gen.AddressResponse
	h.mustGetJSON(ctx, "/api/v1/addresses/"+h.fixture.accounts[0], &from)
	var contract gen.AddressResponse
	h.mustGetJSON(ctx, "/api/v1/addresses/"+h.fixture.contractAddress, &contract)
	var creation gen.TransactionResponse
	h.mustGetJSON(ctx, "/api/v1/transactions/"+h.fixture.creationHash, &creation)
	var failed gen.TransactionResponse
	h.mustGetJSON(ctx, "/api/v1/transactions/"+h.fixture.failedHash, &failed)
	var trace gen.TransactionTraceResponse
	h.mustGetJSON(ctx, "/api/v1/transactions/"+h.fixture.nativeHash+"/trace", &trace)
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
	if failed.Data.Status != nil {
		failedStatus = string(*failed.Data.Status)
	}
	creationAddress := ""
	if creation.Data.ContractAddress != nil {
		creationAddress = string(*creation.Data.ContractAddress)
	}
	return apiSnapshot{
		ChainID: config.Data.ChainId, IndexedBlock: status.Data.IndexedBlock,
		LatestBlock: status.Data.LatestBlock, CoreReady: status.Data.CoreReady,
		BackfillComplete: status.Data.BackfillComplete, Features: config.Data.Features,
		BlockHashes: blockHashes, TransactionHashes: transactionHashes,
		FromType: string(from.Data.Type), ContractType: string(contract.Data.Type),
		CreationAddress: creationAddress, FailedStatus: failedStatus,
		TraceState: string(trace.Data.State), ChartAvailable: true,
		SPA: bytes.Contains(body, []byte("<div id=\"root\">")), SSE: true,
	}
}

type rpcBlock struct {
	Hash   string `json:"hash"`
	Number string `json:"number"`
}

type rpcReceipt struct {
	Status          string `json:"status"`
	ContractAddress string `json:"contractAddress"`
}

type receiptProxy struct {
	upstream   string
	listener   net.Listener
	server     *http.Server
	client     *http.Client
	normalized atomic.Uint64
}

func startReceiptProxy(t *testing.T, upstream string) *receiptProxy {
	t.Helper()
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := &receiptProxy{
		upstream: upstream,
		listener: listener,
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
	encoded, err := json.Marshal(payload)
	if err != nil {
		http.Error(writer, "encode normalized JSON-RPC response", http.StatusBadGateway)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_, _ = writer.Write(encoded)
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

func (h *harness) sendTransaction(ctx context.Context, transaction map[string]any) string {
	var hash string
	h.rpcCall(ctx, &hash, "eth_sendTransaction", transaction)
	if !common.IsHexHash(hash) {
		h.t.Fatalf("invalid transaction hash %q", hash)
	}
	return hash
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
	if err := os.WriteFile(filepath.Join(h.artifacts, name), contents, 0o600); err != nil {
		h.t.Errorf("write artifact %s: %v", name, err)
	}
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
