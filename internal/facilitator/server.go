package facilitator

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/jsonstrict"
	x402 "github.com/x402-foundation/x402/go/v2"
	"github.com/x402-foundation/x402/go/v2/mechanisms/evm"
)

type paymentRequest struct {
	X402Version         int                      `json:"x402Version"`
	PaymentPayload      x402.PaymentPayload      `json:"paymentPayload"`
	PaymentRequirements x402.PaymentRequirements `json:"paymentRequirements"`
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ready := s.active.Load()
		for _, ch := range s.chains {
			ready = ready && ch.healthy.Load()
		}
		if !ready {
			http.Error(w, "not_ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	authenticated := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			given := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare([]byte(given), s.token) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /supported", authenticated(s.supported))
	mux.HandleFunc("POST /verify", authenticated(func(w http.ResponseWriter, r *http.Request) { s.payment(w, r, false) }))
	mux.HandleFunc("POST /settle", authenticated(func(w http.ResponseWriter, r *http.Request) { s.payment(w, r, true) }))
	mux.HandleFunc("POST /progress", authenticated(s.progress))
	mux.HandleFunc("GET /metrics", authenticated(s.metrics))
	return mux
}
func (s *Service) supported(w http.ResponseWriter, _ *http.Request) {
	out := x402.SupportedResponse{Kinds: []x402.SupportedKind{}, Extensions: []string{}, Signers: map[string][]string{"eip155:*": {}}}
	seen := map[string]bool{}
	for _, cfg := range s.config.Chains {
		ch := s.chains[cfg.Network]
		out.Kinds = append(out.Kinds, x402.SupportedKind{X402Version: 2, Scheme: "exact", Network: cfg.Network})
		address := ch.adapter.signer.Address().Hex()
		if !seen[address] {
			out.Signers["eip155:*"] = append(out.Signers["eip155:*"], address)
			seen[address] = true
		}
	}
	respond(w, out)
}
func respond(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Service) decode(r *http.Request) (paymentRequest, *chainRuntime, string, string, error) {
	var in paymentRequest
	bad := errors.New("invalid_payload")
	body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 || jsonstrict.Validate(body, jsonstrict.Limits{MaxDepth: 16, MaxNodes: 4096, SafeIntegersOnly: true}) != nil {
		return in, nil, "", "", bad
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	d.UseNumber()
	if d.Decode(&in) != nil || in.X402Version != 2 || in.PaymentPayload.X402Version != 2 {
		return in, nil, "", "", bad
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		return in, nil, "", "", bad
	}
	req := in.PaymentRequirements
	ch := s.chains[req.Network]
	if ch == nil || req.MaxTimeoutSeconds <= 0 || req.MaxTimeoutSeconds > 3600 || req.Scheme != "exact" || !validAddress(req.Asset) || !validAddress(req.PayTo) || len(in.PaymentPayload.Extensions) > 0 {
		return in, nil, "", "", bad
	}
	accepted, _ := canonicalJSON(in.PaymentPayload.Accepted)
	requirements, _ := canonicalJSON(req)
	if !bytes.Equal(accepted, requirements) {
		return in, nil, "", "", bad
	}
	amount, ok := new(big.Int).SetString(req.Amount, 10)
	if !ok || amount.Sign() <= 0 || amount.BitLen() > 256 || amount.String() != req.Amount {
		return in, nil, "", "", bad
	}
	method, _ := req.Extra["assetTransferMethod"].(string)
	flow, _ := req.Extra["paymentFlow"].(string)
	if flow != "authorization" {
		return in, nil, "", "", bad
	}
	allowed := false
	for _, t := range ch.adapter.config.Tokens {
		if strings.EqualFold(t.Address, req.Asset) {
			for _, m := range t.Methods {
				allowed = allowed || m == method
			}
		}
	}
	if !allowed {
		return in, nil, "", "", bad
	}
	allowed = false
	for _, a := range ch.adapter.config.Recipients {
		allowed = allowed || strings.EqualFold(a, req.PayTo)
	}
	if !allowed {
		return in, nil, "", "", bad
	}
	var payer, nonce, signature string
	// The SDK dispatches by payload shape. Keep that dispatch identical to
	// the configured method and the authorization identity recorded below.
	if evm.IsEIP3009Payload(in.PaymentPayload.Payload) != (method == "eip3009") ||
		evm.IsPermit2Payload(in.PaymentPayload.Payload) != (method == "permit2") {
		return in, nil, "", "", bad
	}
	switch method {
	case "eip3009":
		p, e := evm.PayloadFromMap(in.PaymentPayload.Payload)
		if e != nil {
			return in, nil, "", "", bad
		}
		payer = p.Authorization.From
		nonce = p.Authorization.Nonce
		signature = p.Signature
		b, e := hex.DecodeString(strings.TrimPrefix(nonce, "0x"))
		if e != nil || len(b) != 32 {
			return in, nil, "", "", bad
		}
		nonce = hex.EncodeToString(b)
	case "permit2":
		p, e := evm.Permit2PayloadFromMap(in.PaymentPayload.Payload)
		if e != nil {
			return in, nil, "", "", bad
		}
		payer = p.Permit2Authorization.From
		nonce = p.Permit2Authorization.Nonce
		signature = p.Signature
		n, ok := new(big.Int).SetString(nonce, 10)
		if !ok || n.Sign() < 0 || n.BitLen() > 256 {
			return in, nil, "", "", bad
		}
		nonce = n.String()
	default:
		return in, nil, "", "", bad
	}
	sig, e := hex.DecodeString(strings.TrimPrefix(signature, "0x"))
	if e != nil || len(sig) != 65 || !validAddress(payer) {
		return in, nil, "", "", bad
	}
	// Permit2 nonce space is per owner across all tokens; EIP-3009 is per token.
	scope := strings.ToLower(common.HexToAddress(req.Asset).Hex())
	if method == "permit2" {
		scope = strings.ToLower(evm.PERMIT2Address)
	}
	identity := req.Network + ":" + method + ":" + scope + ":" + strings.ToLower(payer) + ":" + nonce
	return in, ch, identity, common.HexToAddress(payer).Hex(), nil
}

func (s *Service) payment(w http.ResponseWriter, r *http.Request, settle bool) {
	in, ch, id, payer, err := s.decode(r)
	if err != nil {
		http.Error(w, "invalid_payload", http.StatusBadRequest)
		return
	}
	result, err := s.executePayment(r.Context(), in, ch, id, payer, settle)
	if err != nil {
		status := http.StatusServiceUnavailable
		code := "journal_unavailable"
		if failure, ok := errors.AsType[*operationError](err); ok {
			code = failure.code
		}
		if code == "authorization_conflict" {
			status = http.StatusConflict
		}
		http.Error(w, code, status)
		return
	}
	respond(w, result)
}

func (s *Service) metrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	for _, cfg := range s.config.Chains {
		ch := s.chains[cfg.Network]
		health := 0
		if ch.healthy.Load() {
			health = 1
		}
		low := 0
		if ch.insufficientFunds.Load() {
			low = 1
		}
		pending, err := s.queries.FacilitatorPendingCount(ctx, cfg.Network)
		if err != nil {
			http.Error(w, "metrics_unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprintf(w, "etherview_facilitator_chain_healthy{network=%q} %d\netherview_facilitator_pending{network=%q} %d\netherview_facilitator_success_total{network=%q} %d\netherview_facilitator_failure_total{network=%q} %d\netherview_facilitator_low_balance{network=%q} %d\n", cfg.Network, health, cfg.Network, pending, cfg.Network, ch.successes.Load(), cfg.Network, ch.failures.Load(), cfg.Network, low)
	}
}
