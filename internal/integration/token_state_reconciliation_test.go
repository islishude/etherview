//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
	"github.com/islishude/etherview/internal/state"
	"github.com/islishude/etherview/internal/store"
)

func TestExactNFTObservationsRejectConcurrentConflictsAndPreserveIdenticalWrites(t *testing.T) {
	db := newMigratedPostgres(t)
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	genesis := testBundle(0, testHash(110_000), testHash(0), testHash(111_000), "immutable-nft-genesis")
	tip := testBundle(1, testHash(110_001), testHash(110_000), testHash(111_001), "immutable-nft-tip")
	commitCanonical(t, ctx, repository, genesis)
	commitCanonical(t, ctx, repository, tip)
	reference := mustBlockRef(t, tip)
	snapshot := catalog.Snapshot{
		ChainID: "1", BlockNumber: fmt.Sprint(reference.Number), BlockHash: reference.Hash.String(),
	}
	canonical := state.PostgresCanonicalSource{DB: db, ChainID: "1"}
	newReconciler := func(caller ethrpc.Caller) *state.NFTReconciler {
		reconciler, reconcileErr := state.NewNFTReconciler(db, newNFTStatePool(t, caller), canonical)
		if reconcileErr != nil {
			t.Fatal(reconcileErr)
		}
		return reconciler
	}

	contract721 := testAddress(1_721)
	firstOwner, conflictingOwner := testAddress(1_001), testAddress(1_002)
	blockedOwner := newGatedExactNFTCaller(&exactNFTCaller{owner: conflictingOwner})
	conflictingReconciler := newReconciler(blockedOwner)
	type ownerResult struct {
		observation catalog.NFTOwnerObservation
		err         error
	}
	conflictingResult := make(chan ownerResult, 1)
	go func() {
		observation, ownerErr := conflictingReconciler.Owner(ctx, snapshot, contract721.String(), "42")
		conflictingResult <- ownerResult{observation: observation, err: ownerErr}
	}()
	blockedOwner.waitUntilStarted(t, ctx)

	firstReconciler := newReconciler(&exactNFTCaller{owner: firstOwner})
	firstObservation, err := firstReconciler.Owner(ctx, snapshot, contract721.String(), "42")
	if err != nil || !firstObservation.Exists {
		t.Fatalf("persist first exact ERC-721 owner=%+v error=%v", firstObservation, err)
	}
	blockedOwner.releaseCall()
	second := <-conflictingResult
	if !errors.Is(second.err, state.ErrExactNFTObservationConflict) {
		t.Fatalf("conflicting ERC-721 observation=%+v error=%v", second.observation, second.err)
	}

	var storedOwner []byte
	if err := db.QueryRowContext(ctx, `
		SELECT owner_address FROM erc721_owner_reconciliations
		WHERE chain_id = 1 AND token_address = $1 AND token_id = 42 AND block_hash = $2`,
		mustBytes(t, contract721), mustBytes(t, reference.Hash),
	).Scan(&storedOwner); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(storedOwner, mustBytes(t, firstOwner)) {
		t.Fatalf("stored ERC-721 owner=%x, want first immutable owner", storedOwner)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE erc721_owner_reconciliations SET owner_address = $1
		WHERE chain_id = 1 AND token_address = $2 AND token_id = 42 AND block_hash = $3`,
		mustBytes(t, conflictingOwner), mustBytes(t, contract721), mustBytes(t, reference.Hash),
	); err == nil {
		t.Fatal("direct mutation of an exact ERC-721 observation succeeded")
	}

	contract1155, balanceOwner := testAddress(2_115), testAddress(2_001)
	blockedBalance := newGatedExactNFTCaller(&exactNFTCaller{erc1155Balance: "9"})
	conflictingBalanceReconciler := newReconciler(blockedBalance)
	type balanceResult struct {
		observations []catalog.NFTBalanceObservation
		err          error
	}
	conflictingBalance := make(chan balanceResult, 1)
	candidate := []catalog.NFTBalanceCandidate{{
		Standard: "erc1155", TokenAddress: contract1155.String(), TokenID: "7",
	}}
	go func() {
		observations, balanceErr := conflictingBalanceReconciler.Balances(ctx, snapshot, balanceOwner.String(), candidate)
		conflictingBalance <- balanceResult{observations: observations, err: balanceErr}
	}()
	blockedBalance.waitUntilStarted(t, ctx)
	firstBalanceReconciler := newReconciler(&exactNFTCaller{erc1155Balance: "7"})
	firstBalances, err := firstBalanceReconciler.Balances(ctx, snapshot, balanceOwner.String(), candidate)
	if err != nil || len(firstBalances) != 1 || firstBalances[0].Balance != "7" {
		t.Fatalf("persist first exact ERC-1155 balance=%+v error=%v", firstBalances, err)
	}
	blockedBalance.releaseCall()
	secondBalance := <-conflictingBalance
	if !errors.Is(secondBalance.err, state.ErrExactNFTObservationConflict) {
		t.Fatalf("conflicting ERC-1155 observations=%+v error=%v", secondBalance.observations, secondBalance.err)
	}
	var storedBalance string
	if err := db.QueryRowContext(ctx, `
		SELECT balance::text FROM erc1155_balance_reconciliations
		WHERE chain_id = 1 AND token_address = $1 AND token_id = 7
		  AND owner_address = $2 AND block_hash = $3`,
		mustBytes(t, contract1155), mustBytes(t, balanceOwner), mustBytes(t, reference.Hash),
	).Scan(&storedBalance); err != nil {
		t.Fatal(err)
	}
	if storedBalance != "7" {
		t.Fatalf("stored ERC-1155 balance=%s, want first immutable balance", storedBalance)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE erc1155_balance_reconciliations SET balance = 9
		WHERE chain_id = 1 AND token_address = $1 AND token_id = 7
		  AND owner_address = $2 AND block_hash = $3`,
		mustBytes(t, contract1155), mustBytes(t, balanceOwner), mustBytes(t, reference.Hash),
	); err == nil {
		t.Fatal("direct mutation of an exact ERC-1155 observation succeeded")
	}

	// Force two identical observations to miss the cache before either write.
	// The second INSERT must take the conditional no-op path without changing
	// the first observation's audit timestamp.
	identicalTokenID := "43"
	blockedIdentical := newGatedExactNFTCaller(&exactNFTCaller{owner: firstOwner})
	identicalReconciler := newReconciler(blockedIdentical)
	identicalResult := make(chan ownerResult, 1)
	go func() {
		observation, ownerErr := identicalReconciler.Owner(ctx, snapshot, contract721.String(), identicalTokenID)
		identicalResult <- ownerResult{observation: observation, err: ownerErr}
	}()
	blockedIdentical.waitUntilStarted(t, ctx)
	if _, err := firstReconciler.Owner(ctx, snapshot, contract721.String(), identicalTokenID); err != nil {
		t.Fatalf("persist first identical ERC-721 observation: %v", err)
	}
	var firstObservedAt time.Time
	if err := db.QueryRowContext(ctx, `
		SELECT observed_at FROM erc721_owner_reconciliations
		WHERE chain_id = 1 AND token_address = $1 AND token_id = 43 AND block_hash = $2`,
		mustBytes(t, contract721), mustBytes(t, reference.Hash),
	).Scan(&firstObservedAt); err != nil {
		t.Fatal(err)
	}
	blockedIdentical.releaseCall()
	identical := <-identicalResult
	if identical.err != nil || !identical.observation.Exists {
		t.Fatalf("second identical ERC-721 observation=%+v error=%v", identical.observation, identical.err)
	}
	var secondObservedAt time.Time
	if err := db.QueryRowContext(ctx, `
		SELECT observed_at FROM erc721_owner_reconciliations
		WHERE chain_id = 1 AND token_address = $1 AND token_id = 43 AND block_hash = $2`,
		mustBytes(t, contract721), mustBytes(t, reference.Hash),
	).Scan(&secondObservedAt); err != nil {
		t.Fatal(err)
	}
	if !secondObservedAt.Equal(firstObservedAt) {
		t.Fatalf("identical observation changed observed_at from %s to %s", firstObservedAt, secondObservedAt)
	}
}

func TestTokenObservationsAndExactNFTStateSurviveRealPostgresReorg(t *testing.T) {
	db := newMigratedPostgres(t)
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	genesis := testBundle(0, testHash(80_000), testHash(0), testHash(81_000), "token-genesis")
	oldTip := testBundle(1, testHash(80_001), testHash(80_000), testHash(81_001), "token-old")
	replacement := testBundle(1, testHash(90_001), testHash(80_000), testHash(91_001), "token-new")
	thirdTip := testBundle(1, testHash(100_001), testHash(80_000), testHash(101_001), "token-third")
	commitCanonical(t, ctx, repository, genesis)
	commitCanonical(t, ctx, repository, oldTip)
	markTokenStageComplete(t, ctx, db, oldTip)

	contract, codeHash := testAddress(700), testHash(70_000)
	insertTokenObservation(t, ctx, db, genesis, contract, codeHash, "Genesis observation")
	insertTokenObservation(t, ctx, db, oldTip, contract, codeHash, "Old-tip observation")
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM token_contracts
		WHERE chain_id = 1 AND address = $1 AND code_hash = $2`,
		2, mustBytes(t, contract), mustBytes(t, codeHash),
	)

	reader, err := catalog.NewPostgres(db, catalog.Options{})
	if err != nil {
		t.Fatal(err)
	}
	current, err := reader.TokenContract(ctx, "1", contract.String())
	if err != nil || current.Name == nil || *current.Name != "Old-tip observation" || current.ObservedBlockNumber != "1" {
		t.Fatalf("old-tip token observation=%+v error=%v", current, err)
	}

	applyDerivedReorg(t, ctx, repository, genesis, []ethrpc.Bundle{oldTip}, []ethrpc.Bundle{replacement}, "token observation reorg")
	markTokenStageComplete(t, ctx, db, replacement)
	current, err = reader.TokenContract(ctx, "1", contract.String())
	if err != nil || current.Name == nil || *current.Name != "Genesis observation" || current.ObservedBlockNumber != "0" {
		t.Fatalf("post-reorg token observation=%+v error=%v", current, err)
	}

	owner := testAddress(900)
	erc721, erc1155 := testAddress(721), testAddress(1155)
	rpc := &exactNFTCaller{owner: owner, erc1155Balance: "7"}
	pool := newNFTStatePool(t, rpc)
	canonical := state.PostgresCanonicalSource{DB: db, ChainID: "1"}
	reconciler, err := state.NewNFTReconciler(db, pool, canonical)
	if err != nil {
		t.Fatal(err)
	}
	reference := mustBlockRef(t, replacement)
	snapshot := catalog.Snapshot{
		ChainID: "1", BlockNumber: fmt.Sprint(reference.Number), BlockHash: reference.Hash.String(),
	}
	ownership, err := reconciler.Owner(ctx, snapshot, erc721.String(), "42")
	if err != nil || !ownership.Exists || ownership.Confidence != catalog.NFTStateConfidenceRPCExact {
		t.Fatalf("exact owner=%+v error=%v", ownership, err)
	}
	balances, err := reconciler.Balances(ctx, snapshot, owner.String(), []catalog.NFTBalanceCandidate{
		{Standard: "erc721", TokenAddress: erc721.String(), TokenID: "42"},
		{Standard: "erc1155", TokenAddress: erc1155.String(), TokenID: "9"},
	})
	if err != nil || len(balances) != 2 || balances[0].Balance != "1" || balances[1].Balance != "7" ||
		balances[0].Confidence != catalog.NFTStateConfidenceRPCExact || balances[1].Confidence != catalog.NFTStateConfidenceRPCExact {
		t.Fatalf("exact balances=%+v error=%v", balances, err)
	}
	if rpc.calls != 2 {
		t.Fatalf("exact NFT RPC calls=%d, want ownerOf plus balanceOf", rpc.calls)
	}
	for _, selector := range rpc.selectors {
		if selector["blockHash"] != reference.Hash.String() || selector["requireCanonical"] != true {
			t.Fatalf("NFT selector=%#v", selector)
		}
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM erc721_owner_reconciliations WHERE block_hash = $1`, 1, mustBytes(t, reference.Hash))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM erc1155_balance_reconciliations WHERE block_hash = $1`, 1, mustBytes(t, reference.Hash))

	// A fresh process can serve the exact canonical snapshot from PostgreSQL;
	// it does not need the RPC endpoint again.
	failing := &exactNFTCaller{err: errors.New("RPC must not be called for cached exact state")}
	cached, err := state.NewNFTReconciler(db, newNFTStatePool(t, failing), canonical)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cached.Owner(ctx, snapshot, erc721.String(), "42"); err != nil {
		t.Fatalf("cached owner: %v", err)
	}
	if _, err := cached.Balances(ctx, snapshot, owner.String(), []catalog.NFTBalanceCandidate{
		{Standard: "erc721", TokenAddress: erc721.String(), TokenID: "42"},
		{Standard: "erc1155", TokenAddress: erc1155.String(), TokenID: "9"},
	}); err != nil {
		t.Fatalf("cached balances: %v", err)
	}
	if failing.calls != 0 {
		t.Fatalf("cached reconciliation made %d RPC calls", failing.calls)
	}

	applyDerivedReorg(t, ctx, repository, genesis, []ethrpc.Bundle{replacement}, []ethrpc.Bundle{thirdTip}, "orphan exact NFT observations")
	if _, err := cached.Owner(ctx, snapshot, erc721.String(), "42"); !errors.Is(err, httpapi.ErrUnavailable) {
		t.Fatalf("orphan cached owner error=%v, want unavailable after fresh RPC failure", err)
	}
	if failing.calls != 1 {
		t.Fatalf("orphan exact observation was reused; RPC calls=%d", failing.calls)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM erc721_owner_reconciliations WHERE block_hash = $1`, 1, mustBytes(t, reference.Hash))
}

func markTokenStageComplete(t *testing.T, ctx context.Context, db *sql.DB, block ethrpc.Bundle) {
	t.Helper()
	reference := mustBlockRef(t, block)
	word, err := enrich.ParseWord(reference.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.TokenStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
	}); err != nil {
		t.Fatal(err)
	}
	lease, found, err := queue.Claim(ctx, "token-state-fixture", []enrich.StageID{enrich.TokenStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim token state stage=%+v found=%t err=%v", lease, found, err)
	}
	processor, err := enrich.NewPostgresTokenProcessor(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.ProcessLease(ctx, lease, queue); err != nil {
		t.Fatalf("publish token state stage: %v", err)
	}
}

func insertTokenObservation(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block ethrpc.Bundle,
	contract ethrpc.Address,
	codeHash ethrpc.Hash,
	name string,
) {
	t.Helper()
	reference := mustBlockRef(t, block)
	execFixture(t, ctx, db, `
		INSERT INTO token_contracts (
			chain_id, address, code_hash, standard, confidence,
			name, symbol, decimals, total_supply, metadata_state,
			observed_block_number, observed_block_hash
		) VALUES (
			1, $1, $2, 'erc20', 'high',
			$3, 'TOK', 18, 1000, 'complete',
			$4, $5
		)`,
		mustBytes(t, contract), mustBytes(t, codeHash), name,
		reference.Number, mustBytes(t, reference.Hash),
	)
}

type exactNFTCaller struct {
	owner          ethrpc.Address
	erc1155Balance string
	err            error
	calls          int
	selectors      []map[string]any
}

type gatedExactNFTCaller struct {
	delegate *exactNFTCaller
	started  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func newGatedExactNFTCaller(delegate *exactNFTCaller) *gatedExactNFTCaller {
	return &gatedExactNFTCaller{
		delegate: delegate,
		started:  make(chan struct{}),
		release:  make(chan struct{}),
	}
}

func (caller *gatedExactNFTCaller) Call(ctx context.Context, method string, params []any, result any) error {
	caller.once.Do(func() { close(caller.started) })
	select {
	case <-caller.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return caller.delegate.Call(ctx, method, params, result)
}

func (caller *gatedExactNFTCaller) waitUntilStarted(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-caller.started:
	case <-ctx.Done():
		t.Fatalf("wait for gated exact NFT call: %v", ctx.Err())
	}
}

func (caller *gatedExactNFTCaller) releaseCall() { close(caller.release) }

func (caller *exactNFTCaller) Call(_ context.Context, method string, params []any, result any) error {
	caller.calls++
	if caller.err != nil {
		return caller.err
	}
	if method != "eth_call" || len(params) != 2 {
		return fmt.Errorf("unexpected exact NFT call %q %#v", method, params)
	}
	selector, ok := params[1].(map[string]any)
	if !ok {
		return fmt.Errorf("unexpected exact NFT selector %#v", params[1])
	}
	caller.selectors = append(caller.selectors, selector)
	request := params[0].(map[string]any)
	data, err := ethrpc.ParseData(request["data"].(string))
	if err != nil {
		return err
	}
	input, _ := data.Bytes()
	output := make([]byte, 32)
	switch fmt.Sprintf("%x", input[:4]) {
	case "6352211e":
		ownerBytes, _ := caller.owner.Bytes()
		copy(output[12:], ownerBytes)
	case "00fdd58e":
		balance, ok := new(big.Int).SetString(caller.erc1155Balance, 10)
		if !ok {
			return errors.New("invalid fixture ERC-1155 balance")
		}
		balance.FillBytes(output)
	default:
		return fmt.Errorf("unexpected exact NFT selector 0x%x", input[:4])
	}
	destination, ok := result.(*ethrpc.Data)
	if !ok {
		return fmt.Errorf("unexpected exact NFT result %T", result)
	}
	*destination = ethrpc.DataFromBytes(output)
	return nil
}

func newNFTStatePool(t *testing.T, caller ethrpc.Caller) *ethrpc.Pool {
	t.Helper()
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "exact-nft-state", Client: caller,
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return pool
}
