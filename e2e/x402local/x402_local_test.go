//go:build runtimee2e

package x402locale2e

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/billing/x402wire"
	"github.com/islishude/etherview/internal/testcompose"
	x402 "github.com/x402-foundation/x402/go/v2"
	exactclient "github.com/x402-foundation/x402/go/v2/mechanisms/evm/exact/client"
	evmsigners "github.com/x402-foundation/x402/go/v2/signers/evm"
)

const (
	ownerKeyHex      = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
	tokenAddress     = "0x5FbDB2315678afecb367f032d93F642f64180aa3"
	recipientAddress = "0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC"
	permit2Address   = "0x000000000022D473030F116dDEE9F6B43aC78BA3"
	localTokenABI    = `[
      {"type":"function","name":"approve","stateMutability":"nonpayable","inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[{"name":"","type":"bool"}]},
      {"type":"function","name":"allowance","stateMutability":"view","inputs":[{"name":"owner","type":"address"},{"name":"spender","type":"address"}],"outputs":[{"name":"","type":"uint256"}]},
      {"type":"function","name":"balanceOf","stateMutability":"view","inputs":[{"name":"owner","type":"address"}],"outputs":[{"name":"","type":"uint256"}]}
    ]`
)

type modeResult struct {
	Credit    string
	Debit     string
	Available string
	Recipient string
}

func TestX402LocalPrepaidRuntime(t *testing.T) {
	root := repositoryRoot(t)
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
	defer cancel()
	results := make(map[string]modeResult, 2)
	modes := []string{"monolith", "distributed"}
	if selected := os.Getenv("X402_LOCAL_E2E_MODE"); selected == "monolith" || selected == "distributed" {
		modes = []string{selected}
	}
	for _, mode := range modes {
		if !t.Run(mode, func(t *testing.T) {
			results[mode] = runMode(t, ctx, root, mode)
		}) {
			return
		}
	}
	if len(modes) == 2 && results["monolith"] != results["distributed"] {
		t.Fatalf("billing topology parity mismatch monolith=%+v distributed=%+v", results["monolith"], results["distributed"])
	}
}

func runMode(t *testing.T, ctx context.Context, root, mode string) modeResult {
	t.Helper()
	apiPort := freePort(t)
	anvilPort := freePort(t)
	postgresPort := freePort(t)
	publicOrigin := "http://127.0.0.1:" + strconv.Itoa(apiPort)
	project := testcompose.NewQuiet(
		root,
		testcompose.UniqueProjectName("x402-local-"+mode),
		"e2e/x402local/compose.yaml",
	)
	project.Profiles = []string{mode}
	project.Env = map[string]string{
		"ETHERVIEW_IMAGE":          os.Getenv("IMAGE"),
		"X402_LOCAL_PORT":          strconv.Itoa(apiPort),
		"X402_LOCAL_ANVIL_PORT":    strconv.Itoa(anvilPort),
		"X402_LOCAL_POSTGRES_PORT": strconv.Itoa(postgresPort),
		"X402_LOCAL_PUBLIC_URL":    publicOrigin,
	}
	if project.Env["ETHERVIEW_IMAGE"] == "" {
		project.Env["ETHERVIEW_IMAGE"] = "etherview:local"
	}
	t.Cleanup(func() {
		cleanup, cancelCleanup := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancelCleanup()
		if err := project.Down(cleanup); err != nil {
			t.Errorf("tear down x402 local %s: %v", mode, err)
		}
	})
	if _, err := project.Run(ctx, "build", "x402-fixture", "x402-facilitator"); err != nil {
		t.Fatal(err)
	}
	if _, err := project.Run(ctx, "up", "-d", "--no-build", "--wait", "--wait-timeout", "180", "--remove-orphans"); err != nil {
		diagnostics, _ := project.Run(context.Background(), "logs", "--no-color", "metadata", "maintenance", "api")
		t.Fatalf("%v\n%s", err, diagnostics)
	}
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		diagnostics, _ := project.Run(context.Background(), "logs", "--no-color", "x402-facilitator")
		t.Logf("x402 local diagnostics:\n%s", diagnostics)
	})

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Jar: jar, Timeout: 20 * time.Second}
	ownerKey, err := crypto.HexToECDSA(ownerKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	owner := crypto.PubkeyToAddress(ownerKey.PublicKey)
	csrf := signIn(t, httpClient, publicOrigin, ownerKey, owner)
	firstKey := createUserAPIKey(t, httpClient, publicOrigin, csrf, "runtime-one")
	secondKey := createUserAPIKey(t, httpClient, publicOrigin, csrf, "runtime-two")

	rpcClient, err := ethclient.Dial("http://127.0.0.1:" + strconv.Itoa(anvilPort))
	if err != nil {
		t.Fatal(err)
	}
	defer rpcClient.Close()
	codec, err := x402wire.NewCodec(x402wire.DefaultMaxHeaderBytes)
	if err != nil {
		t.Fatal(err)
	}
	firstHeader := topup(t, httpClient, rpcClient, codec, publicOrigin, csrf, owner, "eip3009", "100")
	replay := postTopupPayment(t, httpClient, publicOrigin, csrf, firstHeader.intentID, firstHeader.header)
	if replay.StatusCode != http.StatusConflict {
		t.Fatalf("EIP-3009 replay status=%d", replay.StatusCode)
	}
	_ = replay.Body.Close()

	approveExact(t, rpcClient, ownerKey, owner, big.NewInt(100))
	topup(t, httpClient, rpcClient, codec, publicOrigin, csrf, owner, "permit2", "100")
	if allowance := tokenUint(t, rpcClient, "allowance", owner, common.HexToAddress(permit2Address)); allowance.Sign() != 0 {
		t.Fatalf("Permit2 exact allowance remained %s after settlement", allowance)
	}

	account := billingAccount(t, httpClient, publicOrigin)
	if account.TotalCreditAtomic != "200" || account.TotalDebitAtomic != "0" || account.AvailableAtomic != "200" {
		t.Fatalf("credited account=%+v", account)
	}
	consumeBalance(t, httpClient, publicOrigin, firstKey, owner)
	consumeBalance(t, httpClient, publicOrigin, secondKey, owner)
	account = billingAccount(t, httpClient, publicOrigin)
	if account.TotalDebitAtomic != "2" || account.AvailableAtomic != "198" {
		t.Fatalf("shared-key account=%+v", account)
	}

	logicalFailure(t, httpClient, publicOrigin, firstKey)
	if after := billingAccount(t, httpClient, publicOrigin); after.TotalDebitAtomic != "2" {
		t.Fatalf("logical failure was charged: %+v", after)
	}

	var wait sync.WaitGroup
	errorsByCall := make(chan error, 16)
	for index := range 16 {
		key := firstKey
		if index%2 == 1 {
			key = secondKey
		}
		wait.Go(func() { errorsByCall <- consumeBalanceError(httpClient, publicOrigin, key, owner) })
	}
	wait.Wait()
	close(errorsByCall)
	for callErr := range errorsByCall {
		if callErr != nil {
			t.Fatal(callErr)
		}
	}
	account = billingAccount(t, httpClient, publicOrigin)
	if account.TotalDebitAtomic != "18" || account.AvailableAtomic != "182" || account.ReservedAtomic != "0" {
		t.Fatalf("concurrent debit account=%+v", account)
	}

	operatorKey := createOperatorKey(t, ctx, project, mode)
	consumeBalance(t, httpClient, publicOrigin, operatorKey, owner)
	assertERC20Holdings(t, httpClient, publicOrigin, operatorKey, owner)
	account = billingAccount(t, httpClient, publicOrigin)
	if account.TotalDebitAtomic != "18" {
		t.Fatalf("operator bypass changed user balance: %+v", account)
	}

	recipient := tokenUint(t, rpcClient, "balanceOf", common.HexToAddress(recipientAddress))
	if recipient.String() != account.TotalCreditAtomic {
		t.Fatalf("chain/ledger mismatch recipient=%s credit=%s", recipient, account.TotalCreditAtomic)
	}
	return modeResult{
		Credit: account.TotalCreditAtomic, Debit: account.TotalDebitAtomic,
		Available: account.AvailableAtomic, Recipient: recipient.String(),
	}
}

func assertERC20Holdings(t *testing.T, client *http.Client, origin, key string, owner common.Address) {
	t.Helper()
	target := origin + "/v2/api?chainid=31337&module=account&action=addresstokenbalance&address=" +
		owner.Hex() + "&apikey=" + url.QueryEscape(key)
	deadline := time.Now().Add(30 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		response, err := client.Get(target)
		if err != nil {
			last = err.Error()
			time.Sleep(100 * time.Millisecond)
			continue
		}
		var envelope struct {
			Status string          `json:"status"`
			Result json.RawMessage `json:"result"`
		}
		decodeErr := json.NewDecoder(response.Body).Decode(&envelope)
		_ = response.Body.Close()
		if decodeErr == nil && response.StatusCode == http.StatusOK && envelope.Status == "1" {
			var holdings []struct {
				TokenAddress  string `json:"TokenAddress"`
				TokenQuantity string `json:"TokenQuantity"`
			}
			if err := json.Unmarshal(envelope.Result, &holdings); err == nil {
				for _, holding := range holdings {
					quantity, ok := new(big.Int).SetString(holding.TokenQuantity, 10)
					if strings.EqualFold(holding.TokenAddress, tokenAddress) && ok && quantity.Sign() > 0 {
						return
					}
				}
			}
		}
		last = fmt.Sprintf("status=%d envelope=%s result=%s", response.StatusCode, envelope.Status, envelope.Result)
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("ERC-20 compatibility holding did not become available: %s", last)
}

type topupPayment struct {
	intentID string
	header   string
}

func topup(
	t *testing.T,
	client *http.Client,
	rpcClient *ethclient.Client,
	codec *x402wire.Codec,
	origin, csrf string,
	owner common.Address,
	method, amount string,
) topupPayment {
	t.Helper()
	var intent gen.BillingTopupIntentResponse
	doJSON(t, client, http.MethodPost, origin+"/api/v1/billing/topup-intents", origin, csrf,
		map[string]string{"amount_atomic": amount}, http.StatusCreated, &intent)
	challenge := postTopupPayment(t, client, origin, csrf, intent.Data.Id.String(), "")
	if challenge.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("%s challenge status=%d", method, challenge.StatusCode)
	}
	requiredHeader := make(http.Header)
	requiredHeader.Set(x402wire.PaymentRequiredHeader, challenge.Header.Get(x402wire.PaymentRequiredHeader))
	_ = challenge.Body.Close()
	requirements, err := codec.DecodePaymentRequiredAll(requiredHeader)
	if err != nil {
		t.Fatal(err)
	}
	var selected x402wire.Requirement
	for _, requirement := range requirements {
		if requirement.TransferMethod() == method {
			selected = requirement
		}
	}
	if selected.TransferMethod() == "" {
		t.Fatalf("missing %s requirement", method)
	}
	if method == "permit2" {
		if allowance := tokenUint(t, rpcClient, "allowance", owner, common.HexToAddress(permit2Address)); allowance.String() != amount {
			t.Fatalf("Permit2 prepayment allowance=%s amount=%s", allowance, amount)
		}
	}
	signer, err := evmsigners.NewClientSignerFromPrivateKey(ownerKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := exactclient.NewExactEvmScheme(signer, nil).CreatePaymentPayload(t.Context(), selected.SDK())
	if err != nil {
		t.Fatal(err)
	}
	payload.Accepted = selected.SDK()
	resource := selected.Resource()
	payload.Resource = &resource
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	header := base64.StdEncoding.EncodeToString(encoded)
	response := postTopupPayment(t, client, origin, csrf, intent.Data.Id.String(), header)
	if response.StatusCode != http.StatusOK || response.Header.Get(x402wire.PaymentResponseHeader) == "" {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("%s top-up status=%d body=%s", method, response.StatusCode, body)
	}
	_ = response.Body.Close()
	return topupPayment{intentID: intent.Data.Id.String(), header: header}
}

func postTopupPayment(t *testing.T, client *http.Client, origin, csrf, intentID, payment string) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(
		t.Context(), http.MethodPost,
		origin+"/api/v1/billing/topup-intents/"+url.PathEscape(intentID)+"/pay", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", origin)
	request.Header.Set("X-CSRF-Token", csrf)
	if payment != "" {
		request.Header.Set(x402wire.PaymentSignatureHeader, payment)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func signIn(t *testing.T, client *http.Client, origin string, key *ecdsa.PrivateKey, address common.Address) string {
	t.Helper()
	var challenge gen.AuthChallengeResponse
	doJSON(t, client, http.MethodPost, origin+"/api/v1/auth/challenge", origin, "",
		map[string]string{"address": address.Hex()}, http.StatusCreated, &challenge)
	signature, err := crypto.Sign(accounts.TextHash([]byte(challenge.Data.Message)), key)
	if err != nil {
		t.Fatal(err)
	}
	signature[64] += 27
	var session gen.AuthSessionResponse
	doJSON(t, client, http.MethodPost, origin+"/api/v1/auth/verify", origin, "",
		map[string]string{
			"challenge_id": challenge.Data.ChallengeId.String(),
			"signature":    "0x" + fmt.Sprintf("%x", signature),
		}, http.StatusCreated, &session)
	if !session.Data.Authenticated || session.Data.CsrfToken == nil {
		t.Fatalf("SIWE session=%+v", session.Data)
	}
	return *session.Data.CsrfToken
}

func createUserAPIKey(t *testing.T, client *http.Client, origin, csrf, name string) string {
	t.Helper()
	var issued gen.UserAPIKeyIssuedResponse
	doJSON(t, client, http.MethodPost, origin+"/api/v1/users/me/api-keys", origin, csrf,
		map[string]any{"name": name, "scopes": []string{"api:read"}}, http.StatusCreated, &issued)
	return issued.Data.Token
}

func billingAccount(t *testing.T, client *http.Client, origin string) gen.BillingAccount {
	t.Helper()
	var account gen.BillingAccountResponse
	doJSON(t, client, http.MethodGet, origin+"/api/v1/billing/account", origin, "", nil, http.StatusOK, &account)
	return account.Data
}

func consumeBalance(t *testing.T, client *http.Client, origin, key string, owner common.Address) {
	t.Helper()
	if err := consumeBalanceError(client, origin, key, owner); err != nil {
		t.Fatal(err)
	}
}

func consumeBalanceError(client *http.Client, origin, key string, owner common.Address) error {
	target := origin + "/v2/api?chainid=31337&module=account&action=balance&address=" + owner.Hex() + "&apikey=" + url.QueryEscape(key)
	response, err := client.Get(target)
	if err != nil {
		return err
	}
	defer response.Body.Close() //nolint:errcheck
	var envelope struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK || envelope.Status != "1" {
		return fmt.Errorf("priced balance status=%d envelope=%s", response.StatusCode, envelope.Status)
	}
	return nil
}

func logicalFailure(t *testing.T, client *http.Client, origin, key string) {
	t.Helper()
	target := origin + "/v2/api?chainid=31337&module=contract&action=getabi&address=" + tokenAddress + "&apikey=" + url.QueryEscape(key)
	response, err := client.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close() //nolint:errcheck
	var envelope struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || envelope.Status != "0" {
		t.Fatalf("logical failure status=%d envelope=%s", response.StatusCode, envelope.Status)
	}
}

func createOperatorKey(t *testing.T, ctx context.Context, project *testcompose.Project, mode string) string {
	t.Helper()
	service := "etherview"
	if mode == "distributed" {
		service = "api"
	}
	output, err := project.Run(
		ctx, "exec", "-T", service, "/etherview", "admin", "api-key", "create",
		"--name", "x402-local-operator", "--rate", "100", "--burst", "200",
		"--scope", "api:read", "--config", "/etc/etherview/config.yaml",
	)
	if err != nil {
		t.Fatal(err)
	}
	var issued struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(output, &issued); err != nil || issued.Token == "" {
		t.Fatalf("operator key output=%s error=%v", output, err)
	}
	return issued.Token
}

func approveExact(t *testing.T, client *ethclient.Client, key *ecdsa.PrivateKey, owner common.Address, amount *big.Int) {
	t.Helper()
	parsed := tokenABI(t)
	data, err := parsed.Pack("approve", common.HexToAddress(permit2Address), amount)
	if err != nil {
		t.Fatal(err)
	}
	sendTransaction(t, client, key, owner, common.HexToAddress(tokenAddress), data)
}

func tokenUint(t *testing.T, client *ethclient.Client, method string, arguments ...any) *big.Int {
	t.Helper()
	parsed := tokenABI(t)
	data, err := parsed.Pack(method, arguments...)
	if err != nil {
		t.Fatal(err)
	}
	address := common.HexToAddress(tokenAddress)
	result, err := client.CallContract(t.Context(), ethereum.CallMsg{To: &address, Data: data}, nil)
	if err != nil {
		t.Fatal(err)
	}
	values, err := parsed.Unpack(method, result)
	if err != nil || len(values) != 1 {
		t.Fatalf("unpack %s values=%v error=%v", method, values, err)
	}
	value, ok := values[0].(*big.Int)
	if !ok {
		t.Fatalf("%s result type=%T", method, values[0])
	}
	return value
}

func sendTransaction(t *testing.T, client *ethclient.Client, key *ecdsa.PrivateKey, from, to common.Address, data []byte) {
	t.Helper()
	chainID, err := client.ChainID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := client.PendingNonceAt(t.Context(), from)
	if err != nil {
		t.Fatal(err)
	}
	tip, err := client.SuggestGasTipCap(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	header, err := client.HeaderByNumber(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	fee := new(big.Int).Add(tip, new(big.Int).Mul(header.BaseFee, big.NewInt(2)))
	gas, err := client.EstimateGas(t.Context(), ethereum.CallMsg{From: from, To: &to, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	transaction := types.NewTx(&types.DynamicFeeTx{
		ChainID: chainID, Nonce: nonce, GasTipCap: tip, GasFeeCap: fee,
		Gas: gas + gas/5, To: &to, Data: data,
	})
	signed, err := types.SignTx(transaction, types.LatestSignerForChainID(chainID), key)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SendTransaction(t.Context(), signed); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		receipt, receiptErr := client.TransactionReceipt(t.Context(), signed.Hash())
		if receiptErr == nil {
			if receipt.Status != types.ReceiptStatusSuccessful {
				t.Fatal("approval reverted")
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("approval receipt timeout")
}

func tokenABI(t *testing.T) abi.ABI {
	t.Helper()
	parsed, err := abi.JSON(strings.NewReader(localTokenABI))
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func doJSON(
	t *testing.T,
	client *http.Client,
	method, target, origin, csrf string,
	body any,
	want int,
	destination any,
) {
	t.Helper()
	var encoded bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&encoded).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	request, err := http.NewRequestWithContext(t.Context(), method, target, &encoded)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close() //nolint:errcheck
	if response.StatusCode != want {
		t.Fatalf("%s %s status=%d want=%d", method, target, response.StatusCode, want)
	}
	if destination != nil {
		if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
			t.Fatal(err)
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close() //nolint:errcheck
	return listener.Addr().(*net.TCPAddr).Port
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(directory, "/e2e/x402local")
}

var _ = x402.PaymentPayload{}
