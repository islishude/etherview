//go:build previewmetadatae2e

package previewmetadatae2e

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/cookiejar"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/billing/x402wire"
	x402 "github.com/x402-foundation/x402/go/v2"
	exactclient "github.com/x402-foundation/x402/go/v2/mechanisms/evm/exact/client"
	evmsigners "github.com/x402-foundation/x402/go/v2/signers/evm"
)

const holdingsCredit = "1000000"

type holdingsUser struct {
	h      *harness
	client http.Client
	csrf   string
	token  string
}

// Use a real user-owned read key and settled local top-up so holdings queries
// exercise Preview's prepaid boundary instead of the operator-key bypass.
func (h *harness) fundHoldingsUser(ctx context.Context) *holdingsUser {
	h.t.Helper()
	user := &holdingsUser{h: h, client: *h.http}
	var err error
	user.client.Jar, err = cookiejar.New(nil)
	if err != nil {
		h.t.Fatal(err)
	}
	var challenge gen.AuthChallengeResponse
	user.request(ctx, "/api/v1/auth/challenge", map[string]string{"address": h.developer.Hex()}, "", http.StatusCreated, &challenge)
	key, err := crypto.HexToECDSA(previewSigningKey)
	if err != nil {
		h.t.Fatal(err)
	}
	signature, err := crypto.Sign(accounts.TextHash([]byte(challenge.Data.Message)), key)
	if err != nil {
		h.t.Fatal(err)
	}
	signature[64] += 27
	var session gen.AuthSessionResponse
	user.request(ctx, "/api/v1/auth/verify", map[string]string{
		"challenge_id": challenge.Data.ChallengeId.String(), "signature": hexutil.Encode(signature),
	}, "", http.StatusCreated, &session)
	if !session.Data.Authenticated || session.Data.CsrfToken == nil {
		h.t.Fatal("holdings user was not authenticated")
	}
	user.csrf = *session.Data.CsrfToken
	var issued gen.UserAPIKeyIssuedResponse
	user.request(ctx, "/api/v1/users/me/api-keys", map[string]any{
		"name": "preview-metadata-holdings", "scopes": []string{"api:read"},
	}, "", http.StatusCreated, &issued)
	if issued.Data.Token == "" {
		h.t.Fatal("holdings user key is empty")
	}
	user.token = issued.Data.Token
	var intent gen.BillingTopupIntentResponse
	user.request(ctx, "/api/v1/billing/topup-intents", gen.BillingTopupIntentCreateRequest{AmountAtomic: holdingsCredit, PaymentOptionId: "default"}, "", http.StatusCreated, &intent)
	path := "/api/v1/billing/topup-intents/" + intent.Data.Id.String() + "/pay"
	headers := user.request(ctx, path, nil, "", http.StatusPaymentRequired, nil)
	codec, err := x402wire.NewCodec(x402wire.DefaultMaxHeaderBytes)
	if err != nil {
		h.t.Fatal(err)
	}
	requirements, err := codec.DecodePaymentRequiredAll(headers)
	if err != nil {
		h.t.Fatal(err)
	}
	var selected x402wire.Requirement
	for _, requirement := range requirements {
		if requirement.TransferMethod() == "eip3009" {
			selected = requirement
		}
	}
	if selected.TransferMethod() != "eip3009" {
		h.t.Fatal("holdings top-up requires EIP-3009")
	}
	signer, err := evmsigners.NewClientSignerFromPrivateKey(previewSigningKey)
	if err != nil {
		h.t.Fatal(err)
	}
	payload, err := exactclient.NewExactEvmScheme(signer, nil).CreatePaymentPayload(ctx, selected.SDK(), x402.PaymentPayloadContext{})
	if err != nil {
		h.t.Fatal(err)
	}
	payload.Accepted = selected.SDK()
	resource := selected.Resource()
	payload.Resource = &resource
	encoded, err := json.Marshal(payload)
	if err != nil {
		h.t.Fatal(err)
	}
	var receipt gen.BillingTopupReceiptResponse
	// Submit once. An uncertain settlement must fail this test, never repay.
	user.request(ctx, path, nil, base64.StdEncoding.EncodeToString(encoded), http.StatusOK, &receipt)
	if receipt.Data.Intent.State != "credited" || receipt.Data.Account.TotalCreditAtomic != holdingsCredit {
		h.t.Fatal("holdings top-up was not credited")
	}
	return user
}

func (h *harness) createHoldingsOperatorKey(ctx context.Context) string {
	h.t.Helper()
	output, err := h.project.Run(ctx, "exec", "-T", "api",
		"/etherview", "admin", "api-key", "create", "--config=/etc/etherview/config.yaml",
		"--name=preview-metadata-operator", "--scope=api:read", "--rate=100", "--burst=200")
	if err != nil {
		h.t.Fatal(err)
	}
	var issued struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(output, &issued); err != nil || issued.Token == "" {
		h.t.Fatalf("decode Preview operator key: %v", err)
	}
	return issued.Token
}

func (u *holdingsUser) account(ctx context.Context) gen.BillingAccount {
	u.h.t.Helper()
	var account gen.BillingAccountResponse
	u.request(ctx, "/api/v1/billing/account", nil, "", http.StatusOK, &account)
	return account.Data
}

func (u *holdingsUser) assertDebited(ctx context.Context, expected int64) gen.BillingAccount {
	u.h.t.Helper()
	account := u.account(ctx)
	credit, _ := new(big.Int).SetString(holdingsCredit, 10)
	debit := big.NewInt(expected)
	remaining := new(big.Int).Sub(credit, debit)
	if expected <= 0 || account.TotalDebitAtomic != debit.String() || account.AvailableAtomic != remaining.String() ||
		account.ReservedAtomic != "0" || account.TotalCreditAtomic != holdingsCredit {
		u.h.t.Fatal("holdings requests did not consume settled user credit")
	}
	u.h.t.Logf("user key holdings: credit=%s debit=%s available=%s", holdingsCredit, debit, remaining)
	return account
}

func (u *holdingsUser) assertUnchanged(ctx context.Context, before gen.BillingAccount) {
	u.h.t.Helper()
	after := u.account(ctx)
	if after.TotalCreditAtomic != before.TotalCreditAtomic || after.TotalDebitAtomic != before.TotalDebitAtomic ||
		after.AvailableAtomic != before.AvailableAtomic || after.ReservedAtomic != before.ReservedAtomic {
		u.h.t.Fatal("operator holdings requests changed the user billing account")
	}
	u.h.t.Log("operator key holdings: no user credit, debit, balance or reservation changes")
}

func (u *holdingsUser) request(ctx context.Context, path string, body any, payment string, status int, result any) http.Header {
	u.h.t.Helper()
	var encoded bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&encoded).Encode(body); err != nil {
			u.h.t.Fatal(err)
		}
	}
	method := http.MethodPost
	if path == "/api/v1/billing/account" {
		method = http.MethodGet
	}
	request, err := http.NewRequestWithContext(ctx, method, u.h.apiURL+path, &encoded)
	if err != nil {
		u.h.t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", u.h.apiURL)
	request.Header.Set("X-CSRF-Token", u.csrf)
	if payment != "" {
		request.Header.Set(x402wire.PaymentSignatureHeader, payment)
	}
	response, err := u.client.Do(request)
	if err != nil {
		u.h.t.Fatal(err)
	}
	defer response.Body.Close() //nolint:errcheck
	if response.StatusCode != status {
		u.h.t.Fatalf("%s %s: status=%d want=%d", method, path, response.StatusCode, status)
	}
	if result != nil {
		if err := json.NewDecoder(response.Body).Decode(result); err != nil {
			u.h.t.Fatal(err)
		}
	}
	return response.Header
}
