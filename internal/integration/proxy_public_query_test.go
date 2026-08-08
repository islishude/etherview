//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/etherscan"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
	"github.com/islishude/etherview/internal/query"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/verify"
)

func TestProxyPublicQueryReadinessBoundaries(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	reader, err := query.NewPostgresReader(db, query.Options{ChainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	address := testAddress(9_900)

	if _, err := reader.Proxy(ctx, address.Hex()); !errors.Is(err, httpapi.ErrNotReady) {
		t.Fatalf("proxy detail without a canonical tip error=%v, want ErrNotReady", err)
	}
	if _, err := reader.ProxyUpgrades(ctx, address.Hex(), "", 20); !errors.Is(err, httpapi.ErrNotReady) {
		t.Fatalf("proxy upgrades without a canonical tip error=%v, want ErrNotReady", err)
	}
	if _, err := reader.ProxyInitializations(ctx, address.Hex(), "", 20); !errors.Is(err, httpapi.ErrNotReady) {
		t.Fatalf("proxy initializations without a canonical tip error=%v, want ErrNotReady", err)
	}

	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(99_000), testHash(0), testHash(99_100), "proxy-query-readiness")
	commitCanonical(t, ctx, repository, genesis)

	detail, err := reader.Proxy(ctx, address.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != query.ProxyStatusUnavailable || detail.Snapshot.Number != "0" ||
		common.HexToHash(detail.Snapshot.Hash) != genesis.Block.Hash() {
		t.Fatalf("proxy detail without proxy@2 publication=%+v", detail)
	}
	if _, err := reader.ProxyUpgrades(ctx, address.Hex(), "", 20); !errors.Is(err, httpapi.ErrNotReady) {
		t.Fatalf("proxy upgrades without proxy@2 publication error=%v, want ErrNotReady", err)
	}
	if _, err := reader.ProxyInitializations(ctx, address.Hex(), "", 20); !errors.Is(err, httpapi.ErrNotReady) {
		t.Fatalf("proxy initializations without proxy@2 publication error=%v, want ErrNotReady", err)
	}
}

func TestProxyPublicQueryHistoryCursorReplacementAndClone(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(99_200), testHash(0), testHash(99_300), "proxy-query-genesis")
	commitCanonical(t, ctx, repository, genesis)

	proxy := integrationContractAddress(0)
	implementationOne := testAddress(9_901)
	implementationTwo := testAddress(9_902)
	implementationThree := testAddress(9_903)
	fakeImplementation := testAddress(9_904)
	clone := testAddress(9_920)
	cloneImplementation := testAddress(9_921)
	cloneImplementationCode := []byte{0x60, 0x55}
	cloneCode := cloneRuntime(cloneImplementation)

	blockOne := proxyCreationBundle(
		t, 1, testHash(99_201), genesis.Block.Hash(), testHash(99_301), proxy,
	)
	blockTwo := proxyUpgradeBundle(
		t, 2, testHash(99_202), blockOne.Block.Hash(), testHash(99_302), proxy, implementationTwo,
	)
	blockThree := proxyUpgradeBundle(
		t, 3, testHash(99_203), blockTwo.Block.Hash(), testHash(99_303), proxy, implementationThree,
	)
	blockFour, err := newIntegrationBundle(integrationBundleOptions{
		Number: 4, ParentHash: blockThree.Block.Hash(), ExtraData: []byte("proxy-code-replacement"),
		Transactions: []integrationTransactionOptions{{
			Type: types.DynamicFeeTxType, To: &proxy, Data: []byte{0x04},
			Logs: []*types.Log{
				{Address: proxy, Topics: []common.Hash{
					enrich.SignatureHash("Upgraded(address)"), common.BytesToHash(fakeImplementation.Bytes()),
				}},
				{Address: proxy, Topics: []common.Hash{enrich.SignatureHash("Initialized(uint64)")}, Data: initializedVersionWord(7)},
			},
		}},
		Withdrawals: []*types.Withdrawal{},
		RawExtra:    map[string]any{"integrationVariant": "proxy-code-replacement"},
	})
	if err != nil {
		t.Fatal(err)
	}
	blockFive, err := newIntegrationBundle(integrationBundleOptions{
		Number: 5, ParentHash: blockFour.Block.Hash(), ExtraData: []byte("clone-history"),
		Transactions: []integrationTransactionOptions{{
			Type: types.DynamicFeeTxType, To: &clone, Data: []byte{0x05},
			Logs: []*types.Log{
				{Address: clone, Topics: []common.Hash{enrich.SignatureHash("Initialized(uint64)")}, Data: initializedVersionWord(9)},
				{Address: clone, Topics: []common.Hash{
					enrich.SignatureHash("Upgraded(address)"), common.BytesToHash(fakeImplementation.Bytes()),
				}},
			},
		}},
		Withdrawals: []*types.Withdrawal{},
		RawExtra:    map[string]any{"integrationVariant": "clone-history"},
	})
	if err != nil {
		t.Fatal(err)
	}

	states := map[string]map[string]proxyContractState{
		blockOne.Block.Hash().String(): {
			proxy.String():             {code: []byte{0x60, 0x01}, implementation: &implementationOne},
			implementationOne.String(): {code: []byte{0x60, 0x11}},
		},
		blockTwo.Block.Hash().String(): {
			proxy.String():             {code: []byte{0x60, 0x01}, implementation: &implementationTwo},
			implementationTwo.String(): {code: []byte{0x60, 0x22}},
		},
		blockThree.Block.Hash().String(): {
			proxy.String():               {code: []byte{0x60, 0x01}, implementation: &implementationThree},
			implementationThree.String(): {code: []byte{0x60, 0x33}},
		},
		blockFour.Block.Hash().String(): {
			proxy.String():              {code: []byte{0x60, 0x44}, beaconImplementation: &proxy},
			fakeImplementation.String(): {code: []byte{0x60, 0x45}},
		},
		blockFive.Block.Hash().String(): {
			clone.String():               {code: cloneCode, beaconImplementation: &clone},
			cloneImplementation.String(): {code: cloneImplementationCode},
			fakeImplementation.String():  {code: []byte{0x60, 0x45}},
		},
	}
	var callMu sync.Mutex
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{
		proxyStateEndpoint(t, "proxy-query-a", states, nil, &callMu, make(map[string][]string)),
		proxyStateEndpoint(t, "proxy-query-b", states, nil, &callMu, make(map[string][]string)),
	}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewPostgresProxyProcessorWithOptions(
		db, pool, enrich.ProxyLimits{}, enrich.ProxyDetectionOptions{
			Enabled: true, SafeEnabled: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{
		ID: "proxy-public-query", LeaseDuration: 2 * time.Second, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	jobs := make(map[uint64]enrich.EnqueueResult)
	for _, fixture := range []struct {
		number uint64
		bundle chainbundle.Bundle
	}{
		{number: 1, bundle: blockOne},
		{number: 2, bundle: blockTwo},
		{number: 3, bundle: blockThree},
	} {
		commitCanonical(t, ctx, repository, fixture.bundle)
		jobs[fixture.number] = runDurableProxyBlock(t, ctx, db, queue, worker, fixture.bundle)
	}

	reader, err := query.NewPostgresReader(db, query.Options{ChainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := reader.Proxy(ctx, proxy.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != query.ProxyStatusDetectedUnverified || detail.Implementation == nil ||
		common.HexToAddress(detail.Implementation.Address) != implementationThree ||
		len(detail.DetectionV2) == 0 {
		t.Fatalf("current positive proxy detail=%+v", detail)
	}

	firstPage, err := reader.ProxyUpgrades(ctx, proxy.Hex(), "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstPage.Items) != 1 || firstPage.NextCursor == "" ||
		common.HexToAddress(firstPage.Items[0].NewImplementation.Address) != implementationThree {
		t.Fatalf("first proxy upgrade page=%+v", firstPage)
	}
	secondPage, err := reader.ProxyUpgrades(ctx, proxy.Hex(), firstPage.NextCursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondPage.Items) != 1 || firstPage.Snapshot != secondPage.Snapshot ||
		common.HexToAddress(secondPage.Items[0].NewImplementation.Address) != implementationTwo {
		t.Fatalf("second proxy upgrade page=%+v first_snapshot=%+v", secondPage, firstPage.Snapshot)
	}

	commitCanonical(t, ctx, repository, blockFour)
	runDurableProxyBlock(t, ctx, db, queue, worker, blockFour)
	afterNewHead, err := reader.ProxyUpgrades(ctx, proxy.Hex(), firstPage.NextCursor, 1)
	if err != nil {
		t.Fatalf("snapshot-bounded cursor after a newer head: %v", err)
	}
	if afterNewHead.Snapshot != firstPage.Snapshot || len(afterNewHead.Items) != 1 ||
		common.HexToAddress(afterNewHead.Items[0].NewImplementation.Address) != implementationTwo {
		t.Fatalf("snapshot-bounded page after newer head=%+v want_snapshot=%+v", afterNewHead, firstPage.Snapshot)
	}

	replayed, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.ProxyStage, ChainID: "1", BlockHash: blockTwo.Block.Hash(), BlockNumber: 2,
		Replay: enrich.ReplaySource{Kind: "proxy-public-query", Key: jobs[2].Job.ID},
	})
	if err != nil || !replayed.Replayed {
		t.Fatalf("enqueue snapshot-contained replay: result=%+v error=%v", replayed, err)
	}
	if _, err := reader.ProxyUpgrades(ctx, proxy.Hex(), firstPage.NextCursor, 1); !errors.Is(err, query.ErrInvalidCursor) {
		t.Fatalf("cursor after snapshot-contained requested replay error=%v, want ErrInvalidCursor", err)
	}
	processOne(t, ctx, worker)

	replaced, err := reader.Proxy(ctx, proxy.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Status != query.ProxyStatusNotDetected || replaced.Proxy != nil || len(replaced.Evidence) == 0 ||
		len(replaced.DetectionV2) == 0 {
		t.Fatalf("proxy detail after code replacement=%+v", replaced)
	}
	initializations, err := reader.ProxyInitializations(ctx, proxy.Hex(), "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(initializations.Items) == 0 || initializations.Items[0].Version != "7" ||
		initializations.Items[0].BlockNumber != "4" ||
		common.HexToAddress(initializations.Items[0].Implementation.Address) != proxy {
		t.Fatalf("standalone initialization after proxy code replacement=%+v", initializations)
	}
	upgradesAfterReplacement, err := reader.ProxyUpgrades(ctx, proxy.Hex(), "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(upgradesAfterReplacement.Items) != 2 {
		t.Fatalf("upgrade history after standalone fake event=%+v", upgradesAfterReplacement)
	}
	for _, upgrade := range upgradesAfterReplacement.Items {
		if upgrade.BlockNumber == "4" || common.HexToAddress(upgrade.NewImplementation.Address) == fakeImplementation {
			t.Fatalf("standalone fake Upgraded event became public: %+v", upgradesAfterReplacement)
		}
	}

	commitCanonical(t, ctx, repository, blockFive)
	blockFiveRef := mustBlockRef(t, blockFive)
	publishProxyVerificationInteractionCoverage(t, ctx, db, blockFiveRef, proxyVerificationCodeChange{
		address: clone,
		after:   cloneCode,
	})
	runDurableProxyBlock(t, ctx, db, queue, worker, blockFive)
	cloneImplementationHash := crypto.Keccak256Hash(cloneImplementationCode)
	insertProxyVerificationSource(
		t, ctx, db, cloneImplementation, cloneImplementationHash, "CloneImplementation",
	)
	var cloneInteractionCovered bool
	if err := db.QueryRowContext(ctx, `
		SELECT proxy_interaction_coverage_contains(1, 5, $1, 5, $1)`,
		blockFiveRef.Hash.Bytes(),
	).Scan(&cloneInteractionCovered); err != nil {
		t.Fatalf("query Clone interaction coverage: %v", err)
	}
	if !cloneInteractionCovered {
		t.Fatal("Clone interaction coverage at block 5 is incomplete")
	}
	unboundClone, err := reader.Proxy(ctx, clone.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if unboundClone.Status != query.ProxyStatusDetectedUnverified ||
		unboundClone.Pattern != "clone" || unboundClone.EvidenceState != "exact" ||
		unboundClone.Proxy == nil || unboundClone.Proxy.Verified ||
		unboundClone.Implementation == nil || !unboundClone.Implementation.Verified ||
		common.HexToAddress(unboundClone.Implementation.Address) != cloneImplementation {
		t.Fatalf("exact unbound Clone detail=%+v", unboundClone)
	}
	bindingID := bindCloneProxy(t, ctx, db, clone, cloneImplementation)

	cloneDetail, err := reader.Proxy(ctx, clone.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if cloneDetail.Status != query.ProxyStatusVerified || cloneDetail.Pattern != "clone" ||
		cloneDetail.Mechanism != "eip1167" || cloneDetail.StandardVersion != "" ||
		cloneDetail.EvidenceState != "exact" || cloneDetail.BindingID != bindingID ||
		cloneDetail.Proxy == nil || cloneDetail.Proxy.Verified || cloneDetail.Implementation == nil ||
		!cloneDetail.Implementation.Verified ||
		common.HexToAddress(cloneDetail.Implementation.Address) != cloneImplementation {
		t.Fatalf("exact verified Clone detail=%+v", cloneDetail)
	}
	cloneUpgrades, err := reader.ProxyUpgrades(ctx, clone.Hex(), "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if cloneUpgrades.Coverage.State != query.ProxyCoverageComplete || len(cloneUpgrades.Items) != 0 {
		t.Fatalf("Clone upgrade history=%+v", cloneUpgrades)
	}
	cloneInitializations, err := reader.ProxyInitializations(ctx, clone.Hex(), "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(cloneInitializations.Items) != 1 || cloneInitializations.Items[0].Version != "9" ||
		common.HexToAddress(cloneInitializations.Items[0].Implementation.Address) != cloneImplementation ||
		!cloneInitializations.Items[0].Implementation.Verified {
		t.Fatalf("Clone initialization history=%+v", cloneInitializations)
	}
}

func bindCloneProxy(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	clone common.Address,
	implementation common.Address,
) string {
	t.Helper()
	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := verify.NewService(repository, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := etherscan.NewPostgresBackend(db, etherscan.PostgresOptions{
		ChainID: 1, Verification: service,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := backend.Execute(ctx, etherscan.Request{
		Module: "contract", Action: "verifyproxycontract",
		Values: url.Values{
			"address":                {clone.Hex()},
			"expectedimplementation": {implementation.Hex()},
		},
	})
	if err != nil {
		t.Fatalf("submit exact Clone binding: %v", err)
	}
	bindingID, ok := result.(string)
	if !ok || bindingID == "" {
		t.Fatalf("Clone binding submission result=%#v", result)
	}
	completeProxyVerification(t, ctx, repository, bindingID)
	return bindingID
}
