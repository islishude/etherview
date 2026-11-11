//go:build integration

package facilitator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/billing/x402wire"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	x402 "github.com/x402-foundation/x402/go/v2"
)

type blockedPendingJournal struct {
	settlementJournal
	entered, release, broadcast chan struct{}
	block                       atomic.Bool
	confirmed                   sync.Once
}

func (j *blockedPendingJournal) FacilitatorPending(ctx context.Context, network string) ([]dbgen.FacilitatorSettlement, error) {
	rows, err := j.settlementJournal.FacilitatorPending(ctx, network)
	if j.block.CompareAndSwap(true, false) {
		close(j.entered)
		// Hold only the test barrier beyond the monitor deadline if necessary;
		// elapsed wall time must not remove the admission/recovery overlap.
		<-j.release
	}
	return rows, err
}

func (j *blockedPendingJournal) FacilitatorConfirmBroadcast(ctx context.Context, id string, hash []byte) (int64, error) {
	count, err := j.settlementJournal.FacilitatorConfirmBroadcast(ctx, id, hash)
	if err == nil && count == 1 {
		j.confirmed.Do(func() { close(j.broadcast) })
	}
	return count, err
}

type countingSigner struct {
	digestSigner
	calls *atomic.Int64
}

func (s countingSigner) Sign(ctx context.Context, digest []byte) ([]byte, error) {
	s.calls.Add(1)
	return s.digestSigner.Sign(ctx, digest)
}

type settlementResult struct {
	response *x402.SettleResponse
	err      error
}

func TestSettlementWaitsForMonitorAndRetainsPendingTransaction(t *testing.T) {
	clearCredentialEnvironment(t)
	pool, url := journalDB(t)
	queries := dbgen.New(pool)
	cfg, mock := monitoredSettlementConfig(t, url, queries)
	service, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	// Replace the journal only after the startup monitors have joined.
	service.cancel()
	service.workers.Wait()
	journal := &blockedPendingJournal{settlementJournal: queries, entered: make(chan struct{}), release: make(chan struct{}), broadcast: make(chan struct{})}
	releaseMonitor := sync.OnceFunc(func() { close(journal.release) })
	defer releaseMonitor()
	journal.block.Store(true)
	service.queries = journal
	chain := service.chains[cfg.Chains[0].Network]
	chain.adapter.queries = journal
	var signatures atomic.Int64
	chain.adapter.signer = countingSigner{digestSigner: chain.adapter.signer, calls: &signatures}
	monitorCtx, stopMonitor := context.WithCancel(t.Context())
	service.cancel = stopMonitor
	service.workers.Add(1)
	go service.monitor(monitorCtx, chain)
	select {
	case <-journal.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("monitor did not enter its empty journal scan")
	}

	cancellations := make(chan context.CancelFunc, 1)
	handler := service.Handler()
	client := settlementTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		cancellations <- cancel
		handler.ServeHTTP(w, r.WithContext(ctx))
	}))
	submit := func(in paymentRequest) (<-chan settlementResult, context.CancelFunc) {
		t.Helper()
		result := make(chan settlementResult, 1)
		go func() {
			payload, _ := json.Marshal(in.PaymentPayload)
			requirement, _ := json.Marshal(in.PaymentRequirements)
			response, err := client.Settle(t.Context(), payload, requirement)
			result <- settlementResult{response, err}
		}()
		select {
		case cancel := <-cancellations:
			return result, cancel
		case <-time.After(5 * time.Second):
			t.Fatal("settlement handler did not start")
			return nil, nil
		}
	}
	waitForSubmission := func(in paymentRequest, result <-chan settlementResult) {
		t.Helper()
		select {
		case <-chain.submissionGate:
			// Restore the token while the monitor still owns the execution gate.
			chain.submissionGate <- struct{}{}
		case got := <-result:
			row, err := queries.FacilitatorGet(t.Context(), settlementIdentity(t, service, in))
			t.Fatalf("monitor refused settlement before admission: response=%+v error=%v journal_state=%s journal_response=%s journal_error=%v", got.response, got.err, row.State, row.Response, err)
		case <-time.After(5 * time.Second):
			t.Fatal("settlement did not acquire submission admission")
		}
	}

	// Cancellation behind maintenance must not reserve or permanently refuse an
	// authorization, and must release admission for the next submission.
	cancelled := signedTestRequest(t, cfg.Chains[0].Network)
	result, cancel := submit(cancelled)
	waitForSubmission(cancelled, result)
	cancel()
	got := <-result
	if !x402wire.IsFailure(got.err, x402wire.FailureSettlementUnknown) || got.response != nil || len(chain.submissionGate) != 0 {
		t.Fatalf("cancelled admission: response=%+v error=%v", got.response, got.err)
	}
	if _, err := queries.FacilitatorGet(t.Context(), settlementIdentity(t, service, cancelled)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cancelled admission mutated the journal: %v", err)
	}

	request := signedTestRequest(t, cfg.Chains[0].Network)
	result, cancel = submit(request)
	waitForSubmission(request, result)
	if _, err := queries.FacilitatorGet(t.Context(), settlementIdentity(t, service, request)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("waiting submission was reserved or refused: %v", err)
	}
	releaseMonitor()
	select {
	case <-journal.broadcast:
	case got := <-result:
		t.Fatalf("settlement did not broadcast: response=%+v error=%v", got.response, got.err)
	case <-time.After(5 * time.Second):
		t.Fatal("settlement did not broadcast after monitor released the gate")
	}
	// Cancel only the server operation after confirmed broadcast. Receipt lookup
	// still returns null, so detached publication must retain the signed hash.
	cancel()
	got = <-result
	if !x402wire.IsFailure(got.err, x402wire.FailureSettlementPending) || got.response == nil || got.response.Success {
		t.Fatalf("broadcast did not remain pending: response=%+v error=%v", got.response, got.err)
	}
	id := settlementIdentity(t, service, request)
	row, err := queries.FacilitatorGet(t.Context(), id)
	if err != nil || row.State != "signed" || row.BroadcastStatus != "confirmed" || len(row.RawTransaction) == 0 || len(row.Response) != 0 || got.response.Transaction != common.BytesToHash(row.TransactionHash).Hex() {
		t.Fatalf("pending journal lost original transaction: row=%+v error=%v", row, err)
	}
	if signatures.Load() != 1 || mock.sends.Load() != 1 || mock.nonces.Load() != 1 {
		t.Fatalf("unexpected submission work: signatures=%d broadcasts=%d nonces=%d", signatures.Load(), mock.sends.Load(), mock.nonces.Load())
	}
	service.Close()

	restarted, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	restarted.cancel()
	restarted.workers.Wait()
	adapter := restarted.chains[cfg.Chains[0].Network].adapter
	adapter.signer = countingSigner{digestSigner: adapter.signer, calls: &signatures}
	restartedCtx, stopRestarted := context.WithCancel(t.Context())
	restarted.cancel = stopRestarted
	restarted.workers.Add(1)
	go restarted.monitor(restartedCtx, restarted.chains[cfg.Chains[0].Network])
	replayed := settlementTestClient(t, restarted.Handler())
	mock.blocked.Store(false)
	payload, _ := json.Marshal(request.PaymentPayload)
	requirement, _ := json.Marshal(request.PaymentRequirements)
	for range 2 {
		response, err := replayed.Settle(t.Context(), payload, requirement)
		if err != nil || response == nil || !response.Success || response.Transaction != got.response.Transaction {
			t.Fatalf("reconciliation changed transaction: response=%+v error=%v", response, err)
		}
	}
	finished, err := queries.FacilitatorGet(t.Context(), id)
	if err != nil || finished.State != "succeeded" || !bytes.Equal(finished.RawTransaction, row.RawTransaction) || !bytes.Equal(finished.TransactionHash, row.TransactionHash) || finished.Nonce.Int.Cmp(row.Nonce.Int) != 0 || signatures.Load() != 1 || mock.sends.Load() != 1 || mock.nonces.Load() != 1 {
		t.Fatalf("recovery changed transaction or submitted again: row=%+v error=%v signatures=%d broadcasts=%d nonces=%d", finished, err, signatures.Load(), mock.sends.Load(), mock.nonces.Load())
	}
}

func settlementIdentity(t *testing.T, service *Service, in paymentRequest) string {
	t.Helper()
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	_, _, id, _, err := service.decode(httptest.NewRequest(http.MethodPost, "/settle", bytes.NewReader(body)))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func settlementTestClient(t *testing.T, handler http.Handler) *x402wire.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := x402wire.NewClient(x402wire.ClientOptions{BaseURL: server.URL, UnsafeAllowHTTP: true, AllowedCIDRs: []string{"127.0.0.0/8"}, Timeout: 10 * time.Second, MaxResponseBytes: 1 << 20, Headers: map[string]string{"Authorization": "Bearer " + strings.Repeat("t", 32)}})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func monitoredSettlementConfig(t *testing.T, url string, queries *dbgen.Queries) (Config, *journalRPC) {
	t.Helper()
	cfg := testConfig()
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	cfg.Database.URLFile = write("database", []byte(url))
	cfg.Auth.BearerTokenFile = write("token", []byte(strings.Repeat("t", 32)))
	key, err := os.ReadFile("../../preview/facilitator/keystore.json")
	if err != nil {
		t.Fatal(err)
	}
	signer := cfg.Signers["key"]
	signer.KeystoreFile = write("key", key)
	signer.PasswordFile = write("password", []byte("preview-only-password"))
	cfg.Signers["key"] = signer
	cfg.Chains[0].Tokens[0].Methods = []string{"eip3009"}
	mock := &journalRPC{t: t, chain: 31337, config: cfg.Chains[0], queries: queries, broadcast: make(chan struct{}), verifyEntered: make(chan struct{}), verifyRelease: make(chan struct{})}
	mock.blocked.Store(true)
	server := httptest.NewServer(http.HandlerFunc(mock.handler))
	t.Cleanup(server.Close)
	cfg.Chains[0].RPC.URLFile = write("rpc", []byte(server.URL))
	return cfg, mock
}
