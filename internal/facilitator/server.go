package facilitator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/jsonstrict"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	x402 "github.com/x402-foundation/x402/go/v2"
	"github.com/x402-foundation/x402/go/v2/mechanisms/evm"
	exact "github.com/x402-foundation/x402/go/v2/mechanisms/evm/exact/facilitator"
)

type paymentRequest struct {
	X402Version         int                      `json:"x402Version"`
	PaymentPayload      x402.PaymentPayload      `json:"paymentPayload"`
	PaymentRequirements x402.PaymentRequirements `json:"paymentRequirements"`
}

type chainRuntime struct {
	adapter           *chainClient
	scheme            *exact.ExactEvmScheme
	gate              chan struct{}
	verifyGate        chan struct{}
	healthy           atomic.Bool
	successes         atomic.Uint64
	failures          atomic.Uint64
	insufficientFunds atomic.Bool
}

// Service owns its RPC clients, singleton connection and bounded per-chain queues.
type Service struct {
	config  Config
	token   []byte
	pool    *pgxpool.Pool
	queries *dbgen.Queries
	lock    *pgx.Conn
	lockMu  sync.Mutex
	active  atomic.Bool
	chains  map[string]*chainRuntime
	cancel  context.CancelFunc
	workers sync.WaitGroup
}

func New(ctx context.Context, c Config) (*Service, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	secret, err := secretFile(c.DatabaseURLFile, 8192)
	if err != nil {
		return nil, err
	}
	token, err := secretFile(c.BearerTokenFile, 4096)
	if err != nil {
		return nil, err
	}
	token = bytes.TrimSpace(token)
	if len(token) < 32 {
		return nil, errors.New("invalid_bearer_token")
	}
	s := &Service{config: c, token: token, chains: map[string]*chainRuntime{}}
	success := false
	defer func() {
		if !success {
			s.Close()
		}
	}()
	s.pool, err = pgxpool.New(ctx, strings.TrimSpace(string(secret)))
	if err != nil {
		return nil, errors.New("database_unavailable")
	}
	s.queries = dbgen.New(s.pool)
	s.lock, err = pgx.Connect(ctx, strings.TrimSpace(string(secret)))
	clear(secret)
	if err != nil {
		return nil, errors.New("database_unavailable")
	}
	acquired, err := dbgen.New(s.lock).FacilitatorAcquireLock(ctx)
	if err != nil || !acquired {
		return nil, errors.New("facilitator_already_active")
	}
	signers := map[string]digestSigner{}
	for id, sc := range c.Signers {
		signer, e := loadSigner(ctx, sc)
		if e != nil {
			return nil, e
		}
		signers[id] = signer
	}
	for _, ch := range c.Chains {
		if _, e := s.queries.FacilitatorPending(ctx, ch.Network); e != nil {
			return nil, errors.New("journal_unavailable")
		}
		raw, e := rpcURL(ch.RPCURLFile, c.UnsafeLocalHTTP)
		if e != nil {
			return nil, e
		}
		client, e := dialRPC(ctx, raw)
		if e != nil {
			return nil, errors.New("rpc_unavailable")
		}
		adapter := &chainClient{client: client, signer: signers[ch.Signer], config: ch, queries: s.queries, guard: s.guard}
		runtime := &chainRuntime{adapter: adapter, gate: make(chan struct{}, 1), verifyGate: make(chan struct{}, 8)}
		s.chains[ch.Network] = runtime
		id, e := client.ChainID(ctx)
		expected, _ := networkID(ch.Network)
		if e != nil || id == nil || id.Cmp(expected) != 0 {
			return nil, errors.New("chain_identity_mismatch")
		}
		addresses := map[string]bool{}
		for _, t := range ch.Tokens {
			addresses[t.Address] = true
			for _, m := range t.Methods {
				if m == "permit2" {
					addresses[evm.PERMIT2Address] = true
					addresses[evm.X402ExactPermit2ProxyAddress] = true
				}
			}
		}
		for a := range addresses {
			code, e := adapter.GetCode(ctx, a)
			if e != nil || len(code) == 0 {
				return nil, errors.New("required_contract_missing")
			}
		}
		runtime.scheme = exact.NewExactEvmScheme(adapter, &exact.ExactEvmSchemeConfig{SimulateInSettle: true})
		runtime.scheme.SetPendingSettlementStore(pendingJournal{queries: s.queries})
		runtime.healthy.Store(true)
	}
	s.active.Store(true)
	workerCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	for _, ch := range s.chains {
		s.workers.Add(1)
		go s.monitor(workerCtx, ch)
	}
	success = true
	return s, nil
}

func (s *Service) Close() {
	s.active.Store(false)
	if s.cancel != nil {
		s.cancel()
	}
	s.workers.Wait()
	for _, ch := range s.chains {
		ch.adapter.client.Close()
	}
	if s.lock != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.lock.Close(ctx)
	}
	if s.pool != nil {
		s.pool.Close()
	}
	clear(s.token)
}
func (s *Service) guard(ctx context.Context) error {
	if !s.active.Load() {
		return errors.New("facilitator_inactive")
	}
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	ping, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.lock.Ping(ping); err != nil {
		s.active.Store(false)
		return errors.New("facilitator_lock_lost")
	}
	return nil
}
func (s *Service) monitor(ctx context.Context, ch *chainRuntime) {
	defer s.workers.Done()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check, cancel := context.WithTimeout(ctx, time.Second)
			id, err := ch.adapter.GetChainID(check)
			expected, _ := networkID(ch.adapter.config.Network)
			healthy := err == nil && id != nil && id.Cmp(expected) == 0 && s.guard(check) == nil
			ch.healthy.Store(healthy)
			if healthy {
				balance, e := ch.adapter.client.BalanceAt(check, ch.adapter.signer.Address(), nil)
				if e == nil {
					fee, _ := new(big.Int).SetString(ch.adapter.config.MaxFeePerGas, 10)
					ch.insufficientFunds.Store(balance.Cmp(fee.Mul(fee, new(big.Int).SetUint64(ch.adapter.config.MaxGas))) < 0)
				}
			}
			cancel()
			if !healthy {
				continue
			}
			select {
			case ch.gate <- struct{}{}:
				work, done := context.WithTimeout(ctx, time.Second)
				rows, e := s.queries.FacilitatorPending(work, ch.adapter.config.Network)
				if e == nil {
					for _, row := range rows {
						var request paymentRequest
						if json.Unmarshal(row.RequestBody, &request) == nil {
							_, _ = s.reconcile(work, ch, row, request)
						}
					}
				}
				done()
				<-ch.gate
			default:
			}
		}
	}
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
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if !ch.healthy.Load() || s.guard(ctx) != nil {
		http.Error(w, "chain_unavailable", http.StatusServiceUnavailable)
		return
	}
	if !settle {
		select {
		case ch.verifyGate <- struct{}{}:
			defer func() { <-ch.verifyGate }()
		default:
			http.Error(w, "chain_busy", http.StatusServiceUnavailable)
			return
		}
		result, e := ch.scheme.Verify(ctx, in.PaymentPayload, in.PaymentRequirements, nil)
		if e != nil {
			respond(w, x402.VerifyResponse{IsValid: false, InvalidReason: "verification_failed"})
			return
		}
		if !result.IsValid {
			result.InvalidReason = "invalid_payload"
			result.InvalidMessage = ""
		}
		respond(w, result)
		return
	}
	select {
	case ch.gate <- struct{}{}:
		defer func() { <-ch.gate }()
	default:
		http.Error(w, "chain_busy", http.StatusServiceUnavailable)
		return
	}
	body, _ := canonicalJSON(in)
	digest := sha256.Sum256(body)
	row, e := s.queries.FacilitatorGet(ctx, id)
	if e == nil {
		if !bytes.Equal(row.RequestDigest, digest[:]) {
			http.Error(w, "authorization_conflict", http.StatusConflict)
			return
		}
		response, e := s.reconcile(ctx, ch, row, in)
		if e != nil {
			http.Error(w, "journal_unavailable", http.StatusServiceUnavailable)
			return
		}
		respond(w, response)
		return
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		http.Error(w, "journal_unavailable", http.StatusServiceUnavailable)
		return
	}
	pending, e := s.queries.FacilitatorPendingCount(ctx, in.PaymentRequirements.Network)
	if e != nil || pending != 0 {
		http.Error(w, "chain_pending", http.StatusServiceUnavailable)
		return
	}
	verified, e := ch.scheme.Verify(ctx, in.PaymentPayload, in.PaymentRequirements, nil)
	if e != nil || !verified.IsValid {
		respond(w, x402.SettleResponse{Success: false, ErrorReason: "verification_failed", Network: x402.Network(in.PaymentRequirements.Network)})
		return
	}
	count, e := s.queries.FacilitatorReserve(ctx, dbgen.FacilitatorReserveParams{AuthorizationID: id, RequestDigest: digest[:], RequestBody: body, Network: in.PaymentRequirements.Network, SignerAddress: ch.adapter.signer.Address().Bytes()})
	if e != nil || count != 1 {
		http.Error(w, "reservation_failed", http.StatusServiceUnavailable)
		return
	}
	ctx = context.WithValue(ctx, authorizationContext{}, id)
	_, _ = ch.scheme.Settle(ctx, in.PaymentPayload, in.PaymentRequirements, nil)
	// Never trust an SDK error to indicate whether broadcast occurred. The journal owns it.
	finish, done := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer done()
	row, e = s.queries.FacilitatorGet(finish, id)
	if e != nil {
		http.Error(w, "journal_unavailable", http.StatusServiceUnavailable)
		return
	}
	if len(row.TransactionHash) == 0 {
		response := x402.SettleResponse{Success: false, ErrorReason: "settlement_rejected", Network: x402.Network(in.PaymentRequirements.Network), Payer: payer}
		encoded, _ := json.Marshal(response)
		if _, e = s.queries.FacilitatorFinish(finish, id, "failed", encoded); e != nil {
			http.Error(w, "journal_unavailable", http.StatusServiceUnavailable)
			return
		}
		ch.failures.Add(1)
		respond(w, response)
		return
	}
	response, e := s.reconcile(finish, ch, row, in)
	if e != nil {
		http.Error(w, "journal_unavailable", http.StatusServiceUnavailable)
		return
	}
	respond(w, response)
}

func (s *Service) reconcile(ctx context.Context, ch *chainRuntime, row dbgen.FacilitatorSettlement, in paymentRequest) (*x402.SettleResponse, error) {
	if len(row.Response) > 0 {
		var response x402.SettleResponse
		if json.Unmarshal(row.Response, &response) != nil {
			return nil, errors.New("invalid_journal")
		}
		return &response, nil
	}
	response := &x402.SettleResponse{Success: false, ErrorReason: "settlement_rejected", Network: x402.Network(row.Network), Amount: in.PaymentRequirements.Amount}
	var payer string
	if p, e := evm.PayloadFromMap(in.PaymentPayload.Payload); e == nil {
		payer = p.Authorization.From
	}
	if p, e := evm.Permit2PayloadFromMap(in.PaymentPayload.Payload); e == nil && p.Permit2Authorization.From != "" {
		payer = p.Permit2Authorization.From
	}
	response.Payer = common.HexToAddress(payer).Hex()
	if len(row.TransactionHash) == 0 {
		encoded, _ := json.Marshal(response)
		_, err := s.queries.FacilitatorFinish(ctx, row.AuthorizationID, "failed", encoded)
		return response, err
	}
	response.Transaction = common.BytesToHash(row.TransactionHash).Hex()
	response.ErrorReason = "settlement_pending"
	receipt, err := ch.adapter.WaitForTransactionReceipt(ctx, response.Transaction)
	if err != nil {
		return response, nil
	}
	response.ErrorReason = "settlement_rejected"
	topic := crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	amount, _ := new(big.Int).SetString(in.PaymentRequirements.Amount, 10)
	if receipt.Status == 1 {
		for _, log := range receipt.Logs {
			if log.Address == common.HexToAddress(in.PaymentRequirements.Asset) && len(log.Topics) == 3 && log.Topics[0] == topic && log.Topics[1] == common.BytesToHash(common.HexToAddress(payer).Bytes()) && log.Topics[2] == common.BytesToHash(common.HexToAddress(in.PaymentRequirements.PayTo).Bytes()) && len(log.Data) == 32 && new(big.Int).SetBytes(log.Data).Cmp(amount) == 0 {
				response.Success = true
				response.ErrorReason = ""
				break
			}
		}
	}
	if !response.Success && receipt.Status == 1 {
		response.ErrorReason = "settlement_pending"
		return response, nil
	}
	if !response.Success {
		response.Transaction = ""
	}
	state := "failed"
	if response.Success {
		state = "succeeded"
	}
	encoded, _ := json.Marshal(response)
	count, err := s.queries.FacilitatorFinish(ctx, row.AuthorizationID, state, encoded)
	if err != nil {
		return nil, err
	}
	if count > 0 {
		if response.Success {
			ch.successes.Add(1)
		} else {
			ch.failures.Add(1)
		}
	}
	return response, nil
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
