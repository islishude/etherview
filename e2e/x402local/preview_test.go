//go:build runtimee2e && previewx402e2e

package x402locale2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/billing/x402wire"
	"github.com/islishude/etherview/internal/testcompose"
)

// The ordinary Preview files own the topology/config; only the test's ports,
// public origin, isolated compiler cache and eventual all-role command are overridden.
func TestPreviewX402(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	root := repositoryRoot(t)
	artifacts := t.TempDir()
	compilerDirectory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://127.0.0.1:" + strconv.Itoa(freePort(t))
	overlay := filepath.Join(artifacts, "ports.yaml")
	if err := os.WriteFile(overlay, []byte(fmt.Sprintf(`services:
  api:
    environment:
      ETHERVIEW_SERVER_PUBLIC_URL: %s
    ports:
      - "%s:8080"
`, origin, origin[len("http://"):])), 0600); err != nil {
		t.Fatal(err)
	}
	project := testcompose.NewQuiet(root, testcompose.UniqueProjectName("preview-x402"), "compose.yaml", overlay)
	project.Env = map[string]string{
		"PREVIEW_COMPILER_CACHE_DIR": compilerDirectory,
		"ETHERVIEW_IMAGE":            os.Getenv("IMAGE"),
		"ETHERVIEW_CONFIG_FILE":      filepath.Join(root, "preview/config.yaml"),
		"PREVIEW_NETWORK_DIR":        filepath.Join(artifacts, "network"),
		"PREVIEW_HTTPS_PORT":         "0", "GETH_HTTP_PORT": "0", "GETH_WS_PORT": "0",
		"LIGHTHOUSE_HTTP_PORT": "0", "IPFS_API_PORT": "0", "ETHERVIEW_METRICS_PORT": "0",
		"ETHERVIEW_PUSH_ENABLED": "false", "ETHERVIEW_TURNSTILE_ENABLED": "false",
	}
	if project.Env["ETHERVIEW_IMAGE"] == "" {
		project.Env["ETHERVIEW_IMAGE"] = "etherview:local"
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 90*time.Second)
		defer stop()
		if t.Failed() {
			output, _ := project.Run(cleanup, "logs", "--no-color", "api", "migration", "geth", "x402-facilitator")
			t.Log(string(output))
		}
		if err := project.Down(cleanup); err != nil {
			t.Error(err)
		} else if err := project.PreviewCompilerCache(cleanup, "clean"); err != nil {
			t.Error(err)
		}
	})
	if err := project.PreviewCompilerCache(ctx, "init"); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, "node", "--input-type=module", "-e", `import {prepareNetwork} from "./.github/scripts/preview-network.mjs"; prepareNetwork(process.argv[1]);`, project.Env["PREVIEW_NETWORK_DIR"])
	command.Dir = root
	command.Env = append(os.Environ(), "COMPOSE_PROJECT_NAME="+project.Name, "ETHERVIEW_GENESIS_FILE=", "GETH_GENESIS_FILE=")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("network: %v\n%s", err, output)
	}
	run := func(args ...string) {
		t.Helper()
		if output, err := project.Run(ctx, args...); err != nil {
			t.Fatalf("compose %v: %v\n%s", args, err, output)
		}
	}
	run("up", "-d", "--no-build", "--wait", "--wait-timeout", "180")
	run("run", "--rm", "archive-init")
	binding, err := project.Port(ctx, "geth", 8545)
	if err != nil {
		t.Fatal(err)
	}
	rpcClient, err := ethclient.Dial("http://" + binding)
	if err != nil {
		t.Fatal(err)
	}
	defer rpcClient.Close()
	// Settlement needs actual PoS block production, not just an open RPC.
	deadline := time.Now().Add(180 * time.Second)
	for {
		head, err := rpcClient.BlockNumber(ctx)
		if err == nil && head > 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Preview did not produce blocks")
		}
		time.Sleep(time.Second)
	}
	token := common.HexToAddress("0x8464135c8F25Da09e49BC8782676a84730C318bC")
	code, err := rpcClient.CodeAt(ctx, token, big.NewInt(0))
	if err != nil || len(code) == 0 {
		t.Fatalf("Local USD missing at genesis: %v", err)
	}
	ownerKey, err := crypto.HexToECDSA(ownerKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	owner := crypto.PubkeyToAddress(ownerKey.PublicKey)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 40 * time.Second}
	defer client.CloseIdleConnections()
	codec, err := x402wire.NewCodec(x402wire.DefaultMaxHeaderBytes)
	if err != nil {
		t.Fatal(err)
	}
	csrf := signIn(t, client, origin, ownerKey, owner)
	key := createUserAPIKey(t, client, origin, csrf, "preview")
	for index, mode := range []string{"split", "all"} {
		if index > 0 {
			run("stop", "sync", "enrich", "trace", "metadata", "maintenance")
			all := filepath.Join(artifacts, "all.yaml")
			if err := os.WriteFile(all, []byte(`services:
  api:
    command: [serve, --roles=all, --config=/etc/etherview/config.yaml]
    environment:
      ETHERVIEW_ROLES: all
      ETHERVIEW_CONSENSUS_BLOBS_ACCESS_KEY: preview-writer
      ETHERVIEW_CONSENSUS_BLOBS_SECRET_KEY: preview-write-dev-only
`), 0600); err != nil {
				t.Fatal(err)
			}
			project.Files = append(project.Files, all)
			run("up", "-d", "--no-build", "--no-deps", "--wait", "--wait-timeout", "180", "api")
		}
		first := topup(t, client, rpcClient, codec, origin, csrf, owner, "eip3009", "100")
		replay := postTopupPayment(t, client, origin, csrf, first.intentID, first.header)
		_ = replay.Body.Close()
		if replay.StatusCode != http.StatusConflict {
			t.Fatalf("%s replay=%d", mode, replay.StatusCode)
		}
		approval, err := tokenABI(t).Pack("approve", common.HexToAddress(permit2Address), big.NewInt(100))
		if err != nil {
			t.Fatal(err)
		}
		sendTransaction(t, rpcClient, ownerKey, owner, token, approval)
		topup(t, client, rpcClient, codec, origin, csrf, owner, "permit2", "100")
		if value := tokenUintAt(t, rpcClient, token, "allowance", owner, common.HexToAddress(permit2Address)); value.Sign() != 0 {
			t.Fatalf("remaining allowance=%s", value)
		}
		before := tokenUintAt(t, rpcClient, token, "balanceOf", owner)
		nonce, err := rpcClient.NonceAt(ctx, common.HexToAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8"), nil)
		if err != nil {
			t.Fatal(err)
		}
		run("up", "-d", "--no-build", "--no-deps", "--force-recreate", "--wait", "x402-facilitator")
		afterNonce, err := rpcClient.NonceAt(ctx, common.HexToAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8"), nil)
		if err != nil || nonce != afterNonce || before.Cmp(tokenUintAt(t, rpcClient, token, "balanceOf", owner)) != 0 {
			t.Fatal("facilitator recreation changed chain state")
		}
		run("stop", "x402-facilitator")
		run("up", "-d", "--no-build", "--no-deps", "--force-recreate", "--wait", "--wait-timeout", "180", "api")
		if err := consumeBalanceOnChain(client, origin, key, owner, "48815"); err != nil {
			t.Fatal(err)
		}
		account := billingAccount(t, client, origin)
		credit, debit := strconv.Itoa((index+1)*200), strconv.Itoa(index+1)
		recipient := tokenUintAt(t, rpcClient, token, "balanceOf", common.HexToAddress(recipientAddress))
		if account.TotalCreditAtomic != credit || account.TotalDebitAtomic != debit || account.ReservedAtomic != "0" || recipient.String() != credit || account.AvailableAtomic != strconv.Itoa((index+1)*199) {
			t.Fatalf("%s chain/ledger mismatch: %+v recipient=%s", mode, account, recipient)
		}
		t.Logf("%s: credit=%s debit=%s available=%s recipient=%s; replay, exact allowance, recreation and outage passed", mode, credit, debit, account.AvailableAtomic, recipient)
		run("start", "x402-facilitator")
	}
	// A broadcast on a paused PoS chain is uncertain, not rejected. Preserve
	// its immutable hash and reconcile after mining resumes without resending.
	run("stop", "validator")
	pending := authorizeTopup(t, client, rpcClient, codec, origin, csrf, owner, "eip3009", "1")
	response := postTopupPayment(t, client, origin, csrf, pending.intentID, pending.header)
	var settled struct {
		Success     bool   `json:"success"`
		ErrorReason string `json:"errorReason"`
		Transaction string `json:"transaction"`
	}
	// DecodePaymentResponse intentionally accepts successful receipts only.
	// Inspect the pending header separately, then check the persisted intent.
	raw, decodeErr := base64.StdEncoding.DecodeString(response.Header.Get(x402wire.PaymentResponseHeader))
	if decodeErr == nil {
		decodeErr = json.Unmarshal(raw, &settled)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusPaymentRequired || decodeErr != nil || settled.Success || settled.ErrorReason != "settlement_pending" || settled.Transaction == "" {
		t.Fatalf("paused-chain response: status=%d settlement=%+v error=%v", response.StatusCode, settled, decodeErr)
	}
	var intent gen.BillingTopupIntentResponse
	doJSON(t, client, http.MethodGet, origin+"/api/v1/billing/topup-intents/"+pending.intentID, origin, "", nil, http.StatusOK, &intent)
	if intent.Data.PaymentId == nil || intent.Data.TransactionHash == nil || *intent.Data.TransactionHash != settled.Transaction || string(intent.Data.State) != "settling" {
		t.Fatalf("pending payment was not retained: %+v", intent.Data)
	}
	if account := billingAccount(t, client, origin); account.TotalCreditAtomic != "400" {
		t.Fatalf("pending credit=%+v", account)
	}
	run("start", "validator")
	hash := common.HexToHash(settled.Transaction)
	deadline = time.Now().Add(30 * time.Second)
	for {
		receipt, err := rpcClient.TransactionReceipt(ctx, hash)
		if err == nil {
			if receipt.TxHash != hash || receipt.Status != types.ReceiptStatusSuccessful {
				t.Fatalf("pending receipt=%+v", receipt)
			}
			break
		}
		if !errors.Is(err, ethereum.NotFound) || time.Now().After(deadline) {
			t.Fatalf("pending receipt: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	run("exec", "-T", "api", "/etherview", "admin", "billing", "reconcile", "--config=/etc/etherview/config.yaml", "--id", intent.Data.PaymentId.String(), "--outcome", "settled", "--transaction-hash", hash.Hex())
	replay := postTopupPayment(t, client, origin, csrf, pending.intentID, pending.header)
	_ = replay.Body.Close()
	if replay.StatusCode != http.StatusConflict {
		t.Fatalf("reconciled replay=%d", replay.StatusCode)
	}
	account := billingAccount(t, client, origin)
	recipient := tokenUintAt(t, rpcClient, token, "balanceOf", common.HexToAddress(recipientAddress))
	if account.TotalCreditAtomic != "401" || account.TotalDebitAtomic != "2" || account.AvailableAtomic != "399" || recipient.String() != "401" {
		t.Fatalf("reconciled account=%+v recipient=%s", account, recipient)
	}
	t.Log("paused-chain pending hash, resumed receipt, one-shot reconciliation and replay passed")

}
