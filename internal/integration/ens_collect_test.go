//go:build integration

package integration_test

import (
	"bytes"
	"errors"
	"math/big"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/chainbundle"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enscontract"
	"github.com/islishude/etherview/internal/enssync"
	"github.com/islishude/etherview/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ensCollectorFixture struct {
	db                *pgxpool.Pool
	core              *store.PostgresRepository
	manifest          enssync.Manifest
	genesis, original chainbundle.Bundle
	collector         *enssync.Collector
	owner             common.Address
}

func ensCollectorLog(t *testing.T, profile, name string, address common.Address, values ...any) *types.Log {
	t.Helper()
	raw, e := os.ReadFile("../enssync/abi/" + profile + ".json")
	if e != nil {
		t.Fatal(e)
	}
	contract, e := abi.JSON(strings.NewReader(string(raw)))
	if e != nil {
		t.Fatal(e)
	}
	event := contract.Events[name]
	log := &types.Log{Address: address, Topics: []common.Hash{event.ID}}
	plain := []any{}
	for i, arg := range event.Inputs {
		if arg.Indexed {
			b, e := (event.Inputs[i : i+1]).Pack(values[i])
			if e != nil {
				t.Fatal(e)
			}
			log.Topics = append(log.Topics, common.BytesToHash(b))
		} else {
			plain = append(plain, values[i])
		}
	}
	log.Data, e = event.Inputs.NonIndexed().Pack(plain...)
	if e != nil {
		t.Fatal(e)
	}
	return log
}
func newENSCollectorFixture(t *testing.T) *ensCollectorFixture {
	t.Helper()
	ctx := t.Context()
	db := newMigratedPostgres(t)
	core, e := store.NewPostgresRepository(db)
	if e != nil {
		t.Fatal(e)
	}
	if e = core.ConfigureIndex(ctx, "1", 0); e != nil {
		t.Fatal(e)
	}
	genesis := testBundle(0, testHash(150001), common.Hash{}, testHash(150002), "ens-collector-genesis")
	controller, resolver, owner := testAddress(150010), testAddress(150011), testAddress(150012)
	label := crypto.Keccak256Hash([]byte("alice"))
	parent := crypto.Keccak256Hash(make([]byte, 32), crypto.Keccak256([]byte("test")))
	node := crypto.Keccak256Hash(parent[:], label[:])
	logs := []*types.Log{
		ensCollectorLog(t, "controller_v3", "NameRegistered", controller, "alice", label, owner, big.NewInt(1), big.NewInt(0), big.NewInt(1900000000)),
		ensCollectorLog(t, "resolver", "AddrChanged", resolver, node, owner),
		ensCollectorLog(t, "resolver", "VersionChanged", resolver, node, uint64(2)),
		ensCollectorLog(t, "resolver", "AddrChanged", resolver, node, common.Address{}),
		ensCollectorLog(t, "controller_v3", "NameRenewed", controller, "alice", label, big.NewInt(1), big.NewInt(2000000000)),
	}
	original, e := newIntegrationBundle(integrationBundleOptions{Number: 1, ParentHash: genesis.Block.Hash(), ExtraData: []byte("ens-collect"), Transactions: []integrationTransactionOptions{{Type: types.DynamicFeeTxType, To: &controller, Logs: logs}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = core.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{genesis, original}); e != nil {
		t.Fatal(e)
	}
	if e = store.BindChainIdentity(ctx, db, "1", genesis.Block.Hash()); e != nil {
		t.Fatal(e)
	}
	// These are importer-owned authenticated evidence rows; the importer itself
	// has independent state-root verification tests. No registration is invented.
	execFixture(t, ctx, db, `INSERT INTO genesis_state_imports(chain_id,block_hash,state_root,document_sha256,state,account_count,imported_at) VALUES(1,$1,$2,$3,'complete',2,now())`, genesis.Block.Hash().Bytes(), genesis.Block.Root().Bytes(), testHash(150020).Bytes())
	code := []byte{0x60, 0, 0x00}
	codeHash := crypto.Keccak256Hash(code)
	for _, a := range []common.Address{controller, resolver} {
		execFixture(t, ctx, db, `INSERT INTO genesis_account_observations(chain_id,address,block_hash,balance,nonce,code_hash,code,storage_root) VALUES(1,$1,$2,0,0,$3,$4,$5)`, a.Bytes(), genesis.Block.Hash().Bytes(), codeHash.Bytes(), code, types.EmptyRootHash.Bytes())
	}
	m := enssync.Manifest{TargetChainID: "1", SourceChainID: "1", Genesis: genesis.Block.Hash(), Namespace: enscontract.Custom, Policy: testHash(150021), StartHash: genesis.Block.Hash(), ParentName: "test", CoinType: "60", Deployments: []enssync.Deployment{{Address: controller, Profile: "controller_v3", Hash: genesis.Block.Hash(), CodeHash: codeHash}, {Address: resolver, Profile: "resolver", Hash: genesis.Block.Hash(), CodeHash: codeHash}}}
	repo, e := enssync.NewRepository(db)
	if e != nil {
		t.Fatal(e)
	}
	collector, e := enssync.NewCollector(repo, m, nil, enssync.CollectorOptions{MaxBlocks: 1})
	if e != nil {
		t.Fatal(e)
	}
	return &ensCollectorFixture{db, core, m, genesis, original, collector, owner}
}
func TestENSCollectorAutonomousDiscoveryRestartAndRewind(t *testing.T) {
	f := newENSCollectorFixture(t)
	ctx := t.Context()
	first, e := f.collector.Step(ctx, true)
	if e != nil || first.Scanned != 1 {
		t.Fatalf("first %+v %v", first, e)
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_genesis_baselines`, 2)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_events`, 0)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_candidates WHERE direction='primary'`, 2)
	second, e := f.collector.Step(ctx, true)
	if e != nil || second.Scanned != 1 {
		t.Fatalf("second %+v %v", second, e)
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_events`, 5)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_candidates WHERE direction='forward' AND lookup_key='alice.test'`, 1)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_publications`, 0)
	q := dbgen.New(f.db)
	var latest []byte
	if e = f.db.QueryRow(ctx, `SELECT payload FROM ens_sync_projections WHERE kind='registration' ORDER BY revision DESC LIMIT 1`).Scan(&latest); e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(latest, []byte(`"expires": "2000000000"`)) || !bytes.Contains(latest, []byte(strings.ToLower(f.owner.Hex()))) {
		t.Fatalf("renewal lost owner: %s", latest)
	}
	if e = f.db.QueryRow(ctx, `SELECT payload FROM ens_sync_projections WHERE kind='resolver_record' AND key LIKE '%/addr/60' ORDER BY revision DESC LIMIT 1`).Scan(&latest); e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(latest, []byte(`"version": "2"`)) || !bytes.Contains(latest, []byte(`"deleted": true`)) {
		t.Fatalf("ordered version/deletion: %s", latest)
	}
	repo, _ := enssync.NewRepository(f.db)
	restarted, e := enssync.NewCollector(repo, f.manifest, nil, enssync.CollectorOptions{MaxBlocks: 1})
	if e != nil {
		t.Fatal(e)
	}
	before, e := q.GetENSSyncProgress(ctx, first.SourceID)
	if e != nil {
		t.Fatal(e)
	}
	for range 3 {
		r, e := restarted.Step(ctx, true)
		if e != nil || r.Scanned != 0 {
			t.Fatalf("replay %+v %v", r, e)
		}
	}
	after, _ := q.GetENSSyncProgress(ctx, first.SourceID)
	if before.ProjectionRevision != after.ProjectionRevision {
		t.Fatal("restart duplicated projections")
	}
	replacement := testBundle(1, testHash(150030), f.genesis.Block.Hash(), testHash(150031), "ens-replacement")
	if e = f.core.ApplyReorg(ctx, "1", store.Reorg{Ancestor: mustBlockRef(t, f.genesis), Detached: []store.BlockRef{mustBlockRef(t, f.original)}, Attached: []chainbundle.Bundle{replacement}, Checkpoint: store.NewCoreCheckpoint(mustBlockRef(t, replacement)), Reason: "disabled ENS collector"}); e != nil {
		t.Fatal(e)
	}
	if r, e := restarted.Step(ctx, false); e != nil || r.SourceID != 0 {
		t.Fatalf("disabled %+v %v", r, e)
	}
	r, e := restarted.Step(ctx, true)
	if e != nil || !r.Rewound {
		t.Fatalf("rewind %+v %v", r, e)
	}
	r, e = restarted.Step(ctx, true)
	if e != nil || r.Scanned != 1 {
		t.Fatalf("recover %+v %v", r, e)
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_events`, 5)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_projections p JOIN ens_sync_blocks b ON b.source_id=p.source_id AND b.hash=p.block_hash WHERE b.canonical AND p.kind<>'lifecycle'`, 0)
	if e = f.core.ApplyReorg(ctx, "1", store.Reorg{Ancestor: mustBlockRef(t, f.genesis), Detached: []store.BlockRef{mustBlockRef(t, replacement)}, Attached: []chainbundle.Bundle{f.original}, Checkpoint: store.NewCoreCheckpoint(mustBlockRef(t, f.original)), Reason: "reattach exact former hash"}); e != nil {
		t.Fatal(e)
	}
	for range 2 {
		if _, e = restarted.Step(ctx, true); e != nil {
			t.Fatal(e)
		}
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_events`, 5)
	final, _ := q.GetENSSyncProgress(ctx, first.SourceID)
	if final.Revision <= before.Revision || !bytes.Equal(final.ScannedHash, f.original.Block.Hash().Bytes()) {
		t.Fatal("reattachment failed")
	}
}
func TestENSCollectorMissingHistoryAndDeployment(t *testing.T) {
	f := newENSCollectorFixture(t)
	ctx := t.Context()
	execFixture(t, ctx, f.db, `DELETE FROM core_coverage_ranges WHERE chain_id=1`)
	if _, e := f.collector.Step(ctx, true); !errors.Is(e, enssync.ErrCoverageGap) {
		t.Fatalf("missing history: %v", e)
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_events`, 0)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_progress WHERE failure_code='coverage_gap' AND scanned_hash IS NULL`, 1)
	execFixture(t, ctx, f.db, `INSERT INTO core_coverage_ranges(chain_id,range_start,range_end) VALUES(1,0,1)`)
	changed := f.manifest
	changed.Policy = testHash(150099)
	changed.Deployments = append([]enssync.Deployment(nil), changed.Deployments...)
	changed.Deployments[0].CodeHash = testHash(150098)
	repo, _ := enssync.NewRepository(f.db)
	collector, e := enssync.NewCollector(repo, changed, nil, enssync.CollectorOptions{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = collector.Step(ctx, true); !errors.Is(e, enssync.ErrUnsupportedDeployment) {
		t.Fatal(e)
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_progress WHERE state='unsupported'`, 1)
}
func TestENSCollectorConcurrentCursor(t *testing.T) {
	f := newENSCollectorFixture(t)
	var wait sync.WaitGroup
	failures := make(chan error, 2)
	for range 2 {
		wait.Go(func() { _, e := f.collector.Step(t.Context(), true); failures <- e })
	}
	wait.Wait()
	close(failures)
	for e := range failures {
		if e != nil && !errors.Is(e, enssync.ErrFenceLost) {
			t.Fatal(e)
		}
	}
	for range 2 {
		if _, e := f.collector.Step(t.Context(), true); e != nil {
			t.Fatal(e)
		}
	}
	assertRowCount(t, t.Context(), f.db, `SELECT count(*) FROM ens_sync_events`, 5)
}
