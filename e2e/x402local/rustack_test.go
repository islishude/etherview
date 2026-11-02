//go:build runtimee2e && rustacke2e

package x402locale2e

import (
	"context"
	"encoding/asn1"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/islishude/etherview/internal/billing/x402wire"
	"github.com/islishude/etherview/internal/testcompose"
	"gopkg.in/yaml.v3"
)

const rustackRevision = "ffc10378c64280a78bf6b73fff51612bbba0eb66"

func TestRustackDualAnvil(t *testing.T) {
	for _, mode := range []string{"monolith", "distributed"} {
		if !t.Run(mode, func(t *testing.T) { runRustackMode(t, mode) }) {
			return
		}
	}
}

type rustackHarness struct {
	project                  *testcompose.Project
	client                   *http.Client
	origin, csrf, apiService string
	chains                   [2]*ethclient.Client
	signer                   common.Address
	dropBroadcast            atomic.Bool
}

func (h *rustackHarness) run(t *testing.T, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	if _, err := h.project.Run(ctx, args...); err != nil {
		t.Fatal(err)
	}
}

func setupRustack(t *testing.T, mode string) *rustackHarness {
	t.Helper()
	root := repositoryRoot(t)
	artifacts := t.TempDir()
	h := &rustackHarness{origin: "http://127.0.0.1:" + strconv.Itoa(freePort(t)), apiService: "api"}
	if mode == "monolith" {
		h.apiService = "etherview"
	}
	h.project = testcompose.NewQuiet(root, testcompose.UniqueProjectName("t27-"+mode), "e2e/x402local/compose.yaml", "e2e/x402local/rustack.compose.yaml")
	h.project.ProjectDirectory = filepath.Join(root, "e2e/x402local")
	h.project.Profiles = []string{mode}
	h.project.Env = map[string]string{
		"ETHERVIEW_IMAGE": os.Getenv("IMAGE"), "T27_ARTIFACTS": artifacts,
		"X402_LOCAL_PORT":       strings.TrimPrefix(h.origin, "http://127.0.0.1:"),
		"T27_ANVIL_TWO_PORT":    strconv.Itoa(freePort(t)),
		"X402_LOCAL_PUBLIC_URL": h.origin, "X402_LOCAL_ANVIL_PORT": "0", "X402_LOCAL_POSTGRES_PORT": "0",
	}
	if h.project.Env["ETHERVIEW_IMAGE"] == "" {
		h.project.Env["ETHERVIEW_IMAGE"] = "etherview:local"
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if t.Failed() {
			t.Log(h.project.Logs(ctx))
		}
		if err := h.project.Down(ctx); err != nil {
			t.Error(err)
		}
	})
	h.run(t, "up", "-d", "--no-build", "--wait", "rustack", "anvil", "anvil-two")
	binding, err := h.project.Port(t.Context(), "rustack", 4566)
	if err != nil {
		t.Fatal(err)
	}
	arn, address := verifyRustackKMS(t, "http://"+binding)
	h.signer = address
	for i, service := range []string{"anvil", "anvil-two"} {
		binding, err = h.project.Port(t.Context(), service, 8545)
		if err != nil {
			t.Fatal(err)
		}
		h.chains[i], err = ethclient.DialContext(t.Context(), "http://"+binding)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(h.chains[i].Close)
		id, e := h.chains[i].ChainID(t.Context())
		if e != nil || id.Int64() != int64(31337+i) {
			t.Fatalf("chain identity: %v %v", id, e)
		}
		if e = h.chains[i].Client().CallContext(t.Context(), nil, "anvil_setBalance", address.Hex(), "0x56bc75e2d63100000"); e != nil {
			t.Fatal(e)
		}
	}
	writeRustackConfig(t, root, artifacts, arn, address)
	h.installProxy(t, artifacts)
	h.run(t, "build", "x402-fixture")
	h.run(t, "up", "-d", "--no-build", "--wait", "--wait-timeout", "180")
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	h.client = &http.Client{Jar: jar, Timeout: 40 * time.Second}
	t.Cleanup(h.client.CloseIdleConnections)
	key, err := crypto.HexToECDSA(ownerKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	h.csrf = signIn(t, h.client, h.origin, key, crypto.PubkeyToAddress(key.PublicKey))
	return h
}

// This preflight exercises the pinned emulator over HTTP before any settlement.
// The production signer independently parses the same SPKI and recovers every
// transaction signature during the end-to-end flow.
func verifyRustackKMS(t *testing.T, endpoint string) (string, common.Address) {
	t.Helper()
	client := kms.New(kms.Options{Region: "us-east-1", BaseEndpoint: aws.String(endpoint), Credentials: credentials.NewStaticCredentialsProvider("t27-local", "t27-local-only", "")})
	created, err := client.CreateKey(t.Context(), &kms.CreateKeyInput{KeySpec: kmstypes.KeySpecEccSecgP256k1, KeyUsage: kmstypes.KeyUsageTypeSignVerify})
	if err != nil {
		t.Fatalf("Rustack %s CreateKey compatibility: %v", rustackRevision, err)
	}
	arn := aws.ToString(created.KeyMetadata.Arn)
	pub, err := client.GetPublicKey(t.Context(), &kms.GetPublicKeyInput{KeyId: aws.String(arn)})
	if err != nil {
		t.Fatal(err)
	}
	if pub.KeySpec != kmstypes.KeySpecEccSecgP256k1 || pub.KeyUsage != kmstypes.KeyUsageTypeSignVerify || aws.ToString(pub.KeyId) != arn {
		t.Fatalf("unexpected KMS metadata: %+v", pub)
	}
	var spki struct {
		Algorithm struct{ Algorithm, Parameters asn1.ObjectIdentifier }
		Public    asn1.BitString
	}
	rest, err := asn1.Unmarshal(pub.PublicKey, &spki)
	if err != nil || len(rest) != 0 || !spki.Algorithm.Algorithm.Equal(asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1}) || !spki.Algorithm.Parameters.Equal(asn1.ObjectIdentifier{1, 3, 132, 0, 10}) || spki.Public.BitLength != 520 {
		t.Fatal("invalid secp256k1 SPKI", err)
	}
	public, err := crypto.UnmarshalPubkey(spki.Public.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	digest := crypto.Keccak256([]byte("Etherview P60-T27 KMS preflight"))
	signed, err := client.Sign(t.Context(), &kms.SignInput{KeyId: aws.String(arn), Message: digest, MessageType: kmstypes.MessageTypeDigest, SigningAlgorithm: kmstypes.SigningAlgorithmSpecEcdsaSha256})
	if err != nil {
		t.Fatal(err)
	}
	var rs struct{ R, S *big.Int }
	rest, err = asn1.Unmarshal(signed.Signature, &rs)
	n := crypto.S256().Params().N
	if err != nil || len(rest) != 0 || rs.R == nil || rs.S == nil || rs.R.Sign() <= 0 || rs.S.Sign() <= 0 || rs.R.Cmp(n) >= 0 || rs.S.Cmp(n) >= 0 || aws.ToString(signed.KeyId) != arn {
		t.Fatal("invalid KMS DER signature", err)
	}
	if rs.S.Cmp(new(big.Int).Rsh(new(big.Int).Set(n), 1)) > 0 {
		rs.S.Sub(n, rs.S)
	}
	sig := make([]byte, 65)
	rs.R.FillBytes(sig[:32])
	rs.S.FillBytes(sig[32:64])
	address := crypto.PubkeyToAddress(*public)
	for v := range 2 {
		sig[64] = byte(v)
		recovered, e := crypto.SigToPub(digest, sig)
		if e == nil && crypto.PubkeyToAddress(*recovered) == address {
			t.Logf("Rustack %s: SDK CreateKey/GetPublicKey/Sign DIGEST, SPKI and recovery passed", rustackRevision)
			return arn, address
		}
	}
	t.Fatal("KMS signature did not recover expected address")
	return "", common.Address{}
}

func writeRustackConfig(t *testing.T, root, directory, arn string, address common.Address) {
	t.Helper()
	read := func(name string) map[string]any {
		data, err := os.ReadFile(filepath.Join(root, "e2e/x402local", name))
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]any
		if err = yaml.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	write := func(name string, value map[string]any) {
		data, err := yaml.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(directory, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	config := read("config.yaml")
	billing := config["billing"].(map[string]any)
	chains := billing["payment_chains"].([]any)
	second := read("config.yaml")["billing"].(map[string]any)["payment_chains"].([]any)[0].(map[string]any)
	second["network"] = "eip155:31338"
	second["name"] = "Anvil Two"
	second["tokens"].([]any)[0].(map[string]any)["id"] = "anvil-two"
	billing["payment_chains"] = append(chains, second)
	write("config.yaml", config)
	facilitator := read("facilitator.yaml")
	facilitator["signers"] = map[string]any{"preview": map[string]any{"type": "aws_kms", "address": address.Hex(), "key_arn": arn, "region": "us-east-1"}}
	second = read("facilitator.yaml")["chains"].([]any)[0].(map[string]any)
	second["network"] = "eip155:31338"
	second["rpc_url_file"] = "/run/secrets/x402-anvil-two"
	facilitator["chains"] = append(facilitator["chains"].([]any), second)
	write("facilitator.yaml", facilitator)
	if err := os.WriteFile(filepath.Join(directory, "anvil-two-rpc"), []byte("http://anvil-two:8545\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func runRustackMode(t *testing.T, mode string) {
	h := setupRustack(t, mode)
	codec, err := x402wire.NewCodec(x402wire.DefaultMaxHeaderBytes)
	if err != nil {
		t.Fatal(err)
	}
	key, err := crypto.HexToECDSA(ownerKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	owner := crypto.PubkeyToAddress(key.PublicKey)
	apiKey := createUserAPIKey(t, h.client, h.origin, h.csrf, "t27")
	for i, chain := range h.chains {
		option := []string{"default", "anvil-two"}[i]
		for _, method := range []string{"eip3009", "permit2"} {
			if method == "permit2" {
				approveExact(t, chain, key, owner, big.NewInt(100))
			}
			payment := authorizeTopupOption(t, h.client, chain, codec, h.origin, h.csrf, owner, method, "100", option)
			checkTopupResponse(t, postTopupPayment(t, h.client, h.origin, h.csrf, payment.intentID, payment.header), method)
			assertRustackReplay(t, h, payment)
			if method == "permit2" && tokenUint(t, chain, "allowance", owner, common.HexToAddress(permit2Address)).Sign() != 0 {
				t.Fatal("Permit2 allowance not consumed")
			}
		}
	}
	// A single unavailable chain must not stop the independent network.
	h.run(t, "stop", "anvil-two")
	topup(t, h.client, h.chains[0], codec, h.origin, h.csrf, owner, "eip3009")
	h.run(t, "up", "-d", "--no-build", "--no-deps", "--wait", "anvil-two")
	if tokenUint(t, h.chains[1], "balanceOf", common.HexToAddress(recipientAddress)).String() != "200" {
		t.Fatal("Anvil restart lost persisted transfers")
	}
	// An unavailable KMS must not publish a payment or credit; resume the same
	// emulator process so its in-memory signing identity is retained.
	pending := authorizeTopup(t, h.client, h.chains[0], codec, h.origin, h.csrf, owner, "eip3009", "100")
	h.run(t, "pause", "rustack")
	response := postTopupPayment(t, h.client, h.origin, h.csrf, pending.intentID, pending.header)
	_ = response.Body.Close()
	h.run(t, "unpause", "rustack")
	if response.StatusCode == http.StatusOK {
		t.Fatal("payment succeeded with KMS unavailable")
	}
	if account := billingAccount(t, h.client, h.origin); account.TotalCreditAtomic != "500" {
		t.Fatalf("KMS outage credited account: %+v", account)
	}
	topup(t, h.client, h.chains[0], codec, h.origin, h.csrf, owner, "eip3009")
	topupWithDelayedMining(t, h.client, h.chains[0], codec, h.origin, h.csrf, owner, "eip3009")
	consumeBalance(t, h.client, h.origin, apiKey, owner)
	assertRustackRecovery(t, h, codec, owner)
	reconcileRustack(t, h)
	t.Logf("%s: both methods on both chains, replay, exact allowance, KMS recovery, isolated chain outage, delayed mining and restart passed", mode)
}

func assertRustackReplay(t *testing.T, h *rustackHarness, payment topupPayment) {
	t.Helper()
	response := postTopupPayment(t, h.client, h.origin, h.csrf, payment.intentID, payment.header)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("replay status=%d", response.StatusCode)
	}
}
