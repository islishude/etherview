//go:build runtimee2e && rustacke2e

package x402locale2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/billing/x402wire"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Only the facilitator uses this proxy; independent chain evidence bypasses it.
// An armed proxy broadcasts the exact bytes, then loses the response.
func (h *rustackHarness) installProxy(t *testing.T, artifacts string) {
	t.Helper()
	binding, err := h.project.Port(t.Context(), "anvil", 8545)
	if err != nil {
		t.Fatal(err)
	}
	upstream := "http://" + binding
	client := &http.Client{Timeout: 15 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, e := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if e != nil {
			http.Error(w, "request", http.StatusBadRequest)
			return
		}
		var request struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(body, &request) != nil {
			http.Error(w, "request", http.StatusBadRequest)
			return
		}
		req, e := http.NewRequestWithContext(r.Context(), http.MethodPost, upstream, bytes.NewReader(body))
		if e != nil {
			http.Error(w, "request", http.StatusBadRequest)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		response, e := client.Do(req)
		if e != nil {
			http.Error(w, "upstream", http.StatusServiceUnavailable)
			return
		}
		defer func() { _ = response.Body.Close() }()
		data, e := io.ReadAll(io.LimitReader(response.Body, 8<<20))
		if e != nil {
			http.Error(w, "upstream", http.StatusServiceUnavailable)
			return
		}
		if request.Method == "eth_sendRawTransaction" && h.dropBroadcast.Load() {
			http.Error(w, "lost broadcast response", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(data)
	}))
	_ = server.Listener.Close()
	server.Listener, err = net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	t.Cleanup(server.Close)
	endpoint := fmt.Sprintf("http://host.docker.internal:%d\n", server.Listener.Addr().(*net.TCPAddr).Port)
	if err = os.WriteFile(filepath.Join(artifacts, "anvil-one-rpc"), []byte(endpoint), 0644); err != nil {
		t.Fatal(err)
	}
}

func rustackDB(t *testing.T, h *rustackHarness) *pgxpool.Pool {
	t.Helper()
	binding, err := h.project.Port(t.Context(), "postgres", 5432)
	if err != nil {
		t.Fatal(err)
	}
	db, err := pgxpool.New(t.Context(), "postgres://etherview:etherview-x402-local@"+binding+"/etherview?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

func assertRustackRecovery(t *testing.T, h *rustackHarness, codec *x402wire.Codec, owner common.Address) {
	t.Helper()
	chain := h.chains[0]
	pending := authorizeTopup(t, h.client, chain, codec, h.origin, h.csrf, owner, "eip3009", "100")
	if err := chain.Client().CallContext(t.Context(), nil, "evm_setAutomine", false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = chain.Client().CallContext(ctx, nil, "evm_setAutomine", true)
	})
	h.dropBroadcast.Store(true)
	response := postTopupPayment(t, h.client, h.origin, h.csrf, pending.intentID, pending.header)
	_ = response.Body.Close()
	if response.StatusCode == http.StatusOK {
		t.Fatal("uncertain broadcast reported settled")
	}
	db := rustackDB(t, h)
	var id, broadcast string
	var hash, raw []byte
	var nonce uint64
	if err := db.QueryRow(t.Context(), `SELECT authorization_id, transaction_hash, raw_transaction, nonce::text::bigint, broadcast_status FROM facilitator_settlements WHERE state='signed'`).Scan(&id, &hash, &raw, &nonce, &broadcast); err != nil {
		t.Fatal(err)
	}
	if broadcast != "unknown" || len(hash) != 32 || len(raw) == 0 {
		t.Fatalf("missing unknown broadcast evidence: %s", broadcast)
	}
	tx, pendingOnChain, err := chain.TransactionByHash(t.Context(), common.BytesToHash(hash))
	if err != nil || !pendingOnChain || tx.Nonce() != nonce {
		t.Fatalf("broadcast never reached Anvil: pending=%v err=%v", pendingOnChain, err)
	}
	if account := billingAccount(t, h.client, h.origin); account.TotalCreditAtomic != "700" {
		t.Fatalf("unknown transaction credited: %+v", account)
	}
	h.run(t, "stop", "x402-facilitator")
	h.dropBroadcast.Store(false)
	if err = chain.Client().CallContext(t.Context(), nil, "evm_mine"); err != nil {
		t.Fatal(err)
	}
	if err = chain.Client().CallContext(t.Context(), nil, "evm_setAutomine", true); err != nil {
		t.Fatal(err)
	}
	h.run(t, "up", "-d", "--no-build", "--no-deps", "--wait", "x402-facilitator")
	deadline := time.Now().Add(30 * time.Second)
	for {
		var state string
		if err = db.QueryRow(t.Context(), `SELECT state FROM facilitator_settlements WHERE authorization_id=$1`, id).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("journal recovery state=%s", state)
		}
		time.Sleep(100 * time.Millisecond)
	}
	var afterRaw, afterHash []byte
	var afterNonce uint64
	if err = db.QueryRow(t.Context(), `SELECT raw_transaction,transaction_hash,nonce::text::bigint FROM facilitator_settlements WHERE authorization_id=$1`, id).Scan(&afterRaw, &afterHash, &afterNonce); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, afterRaw) || !bytes.Equal(hash, afterHash) || nonce != afterNonce {
		t.Fatal("restart replaced immutable transaction")
	}
	var intent gen.BillingTopupIntentResponse
	doJSON(t, h.client, http.MethodGet, h.origin+"/api/v1/billing/topup-intents/"+pending.intentID, h.origin, "", nil, http.StatusOK, &intent)
	if intent.Data.PaymentId == nil || intent.Data.TransactionHash == nil || *intent.Data.TransactionHash != common.BytesToHash(hash).Hex() {
		t.Fatalf("intent lost exact transaction: %+v", intent.Data)
	}
	h.run(t, "exec", "-T", h.apiService, "/etherview", "admin", "billing", "reconcile", "--config=/etc/etherview/config.yaml", "--id", intent.Data.PaymentId.String(), "--outcome", "settled", "--transaction-hash", common.BytesToHash(hash).Hex())
	assertRustackReplay(t, h, pending)
	t.Logf("uncertain broadcast recovered unchanged hash=%s nonce=%d; one-shot credit and replay passed", common.BytesToHash(hash), nonce)
}

func reconcileRustack(t *testing.T, h *rustackHarness) {
	t.Helper()
	db := rustackDB(t, h)
	rows, err := db.Query(t.Context(), `SELECT network,raw_transaction,transaction_hash,nonce::text::bigint FROM facilitator_settlements WHERE state='succeeded' ORDER BY network,nonce`)
	if err != nil {
		t.Fatal(err)
	}
	type record struct {
		network   string
		raw, hash []byte
		nonce     uint64
	}
	var records []record
	for rows.Next() {
		var r record
		if err = rows.Scan(&r.network, &r.raw, &r.hash, &r.nonce); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		records = append(records, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	var failedUnsigned, outstanding int
	if err = db.QueryRow(t.Context(), `SELECT count(*) FILTER (WHERE state='failed' AND raw_transaction IS NULL), count(*) FILTER (WHERE state IN ('reserved','signed') OR (state='failed' AND raw_transaction IS NOT NULL)) FROM facilitator_settlements`).Scan(&failedUnsigned, &outstanding); err != nil {
		t.Fatal(err)
	}
	if failedUnsigned != 1 || outstanding != 0 {
		t.Fatalf("KMS failure/recovery journal: unsigned failures=%d outstanding=%d", failedUnsigned, outstanding)
	}
	// The database cursor is closed before independent RPC calls.
	counts := map[string]uint64{}
	for _, r := range records {
		i := 0
		if r.network == "eip155:31338" {
			i = 1
		} else if r.network != "eip155:31337" {
			t.Fatal("unexpected network")
		}
		var tx types.Transaction
		if err = tx.UnmarshalBinary(r.raw); err != nil {
			t.Fatal(err)
		}
		hash := common.BytesToHash(r.hash)
		sender, e := types.Sender(types.LatestSignerForChainID(tx.ChainId()), &tx)
		if e != nil || sender != h.signer || tx.Hash() != hash || tx.Nonce() != r.nonce || r.nonce != counts[r.network] {
			t.Fatalf("journal signer/hash/nonce mismatch: %v", e)
		}
		receipt, e := h.chains[i].TransactionReceipt(t.Context(), hash)
		if e != nil || receipt.Status != types.ReceiptStatusSuccessful {
			t.Fatalf("receipt mismatch: %v", e)
		}
		transfers := 0
		for _, log := range receipt.Logs {
			if log.Address == common.HexToAddress(tokenAddress) && len(log.Topics) == 3 && log.Topics[0] == crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)")) && common.BytesToAddress(log.Topics[2].Bytes()) == common.HexToAddress(recipientAddress) {
				if common.BytesToHash(log.Data).Big().Uint64() != 100 {
					t.Fatal("transfer amount mismatch")
				}
				transfers++
			}
		}
		if transfers != 1 {
			t.Fatalf("expected one transfer, got %d", transfers)
		}
		counts[r.network]++
	}
	if len(records) != 8 || counts["eip155:31337"] != 6 || counts["eip155:31338"] != 2 {
		t.Fatalf("journal counts=%v", counts)
	}
	for i, chain := range h.chains {
		expected := []string{"600", "200"}[i]
		if tokenUint(t, chain, "balanceOf", common.HexToAddress(recipientAddress)).String() != expected {
			t.Fatal("token balance mismatch")
		}
		nonce, e := chain.NonceAt(t.Context(), h.signer, nil)
		if e != nil || nonce != counts[fmt.Sprintf("eip155:%d", 31337+i)] {
			t.Fatal("chain nonce mismatch", e)
		}
	}
	var credit, debit, reserved string
	if err = db.QueryRow(t.Context(), `SELECT total_credit_atomic::text,total_debit_atomic::text,reserved_atomic::text FROM billing_accounts`).Scan(&credit, &debit, &reserved); err != nil {
		t.Fatal(err)
	}
	var entries, unique int
	var sum string
	if err = db.QueryRow(t.Context(), `SELECT count(*),count(DISTINCT source_id),sum(amount_atomic)::text FROM billing_account_entries WHERE kind='topup' AND direction='credit'`).Scan(&entries, &unique, &sum); err != nil {
		t.Fatal(err)
	}
	var debitEntries int
	var debitSum string
	if err = db.QueryRow(t.Context(), `SELECT count(*),sum(amount_atomic)::text FROM billing_account_entries WHERE kind='usage' AND direction='debit'`).Scan(&debitEntries, &debitSum); err != nil {
		t.Fatal(err)
	}
	if debitEntries != 1 || debitSum != "2" {
		t.Fatalf("priced usage ledger mismatch: entries=%d sum=%s", debitEntries, debitSum)
	}
	account := billingAccount(t, h.client, h.origin)
	if credit != "800" || debit != "2" || reserved != "0" || entries != 8 || unique != 8 || sum != "800" || account.TotalCreditAtomic != credit || account.TotalDebitAtomic != debit || account.AvailableAtomic != "798" {
		t.Fatalf("writer/API/ledger mismatch: credit=%s debit=%s reserved=%s entries=%d unique=%d sum=%s account=%+v", credit, debit, reserved, entries, unique, sum, account)
	}
	t.Log("independent receipts, transfer logs, journal bytes/sender/nonces, 8 unique credits=800 and priced debit=2 reconciled")
}
