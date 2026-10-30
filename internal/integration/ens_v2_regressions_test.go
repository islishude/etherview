//go:build integration

package integration_test

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/chainbundle"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enssync"
	"github.com/islishude/etherview/internal/enssync/ensfixture"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Keep exact historical fixture state available when a later collection round
// verifies the deployment block as well as its current target.
type ensV2SnapshotRPC struct {
	headers map[common.Hash]*types.Header
	states  map[common.Hash]*state.StateDB
}

func (s *ensV2SnapshotRPC) ChainId() hexutil.Uint64 { return 1 }
func (s *ensV2SnapshotRPC) GetBlockByHash(hash common.Hash, _ bool) (*types.Header, error) {
	if h := s.headers[hash]; h != nil {
		return h, nil
	}
	return nil, errors.New("unknown fixture block")
}
func (s *ensV2SnapshotRPC) snapshot(block rpc.BlockNumberOrHash) (*state.StateDB, error) {
	if block.BlockHash != nil && block.RequireCanonical {
		if snapshot := s.states[*block.BlockHash]; snapshot != nil {
			return snapshot, nil
		}
	}
	return nil, errors.New("fixture state must use an exact canonical hash")
}
func (s *ensV2SnapshotRPC) GetCode(address common.Address, block rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	snapshot, err := s.snapshot(block)
	if err != nil {
		return nil, err
	}
	return snapshot.GetCode(address), nil
}
func (s *ensV2SnapshotRPC) GetStorageAt(address common.Address, slot common.Hash, block rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	snapshot, err := s.snapshot(block)
	if err != nil {
		return nil, err
	}
	return snapshot.GetState(address, slot).Bytes(), nil
}

type ensV2CollectionFixture struct {
	db        *pgxpool.Pool
	core      *store.PostgresRepository
	real      *ensfixture.Fixture
	rpc       *ensV2SnapshotRPC
	pool      *ethrpc.Pool
	manifest  enssync.Manifest
	repo      *enssync.Repository
	collector *enssync.Collector
	tip       *types.Header
	logCursor int
}

func newENSV2CollectionFixture(t *testing.T, real *ensfixture.Fixture, registries ...ensfixture.Contract) *ensV2CollectionFixture {
	t.Helper()
	db := newMigratedPostgres(t)
	execFixture(t, t.Context(), db, `INSERT INTO chains(chain_id) VALUES(1)`)
	genesis := core.DefaultGenesisBlock().ToBlock().Header()
	coreRepo, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = coreRepo.ConfigureIndex(t.Context(), "1", 1); err != nil {
		t.Fatal(err)
	}
	if err = store.BindChainIdentity(t.Context(), db, "1", genesis.Hash()); err != nil {
		t.Fatal(err)
	}
	service := &ensV2SnapshotRPC{headers: map[common.Hash]*types.Header{genesis.Hash(): genesis}, states: map[common.Hash]*state.StateDB{}}
	f := &ensV2CollectionFixture{db: db, core: coreRepo, real: real, rpc: service, tip: genesis}
	f.appendBlock(t)
	server := rpc.NewServer()
	if err = server.RegisterName("eth", service); err != nil {
		t.Fatal(err)
	}
	client := rpc.DialInProc(server)
	t.Cleanup(client.Close)
	f.pool, err = ethrpc.NewPool([]ethrpc.Endpoint{{Name: "v2-regression", Client: client, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true}}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.manifest = enssync.Manifest{TargetChainID: "1", SourceChainID: "1", Genesis: genesis.Hash(), Policy: testHash(153001), Start: 1, StartHash: f.tip.Hash(), ParentName: "eth", CoinType: "60", Roots: []enssync.RegistryRoot{{Address: real.Registry.Address, Name: "eth"}}}
	for _, entry := range []struct {
		contract ensfixture.Contract
		profile  string
	}{{real.Registry, "v2_registry"}, {real.Factory, "v2_factory"}} {
		f.manifest.Deployments = append(f.manifest.Deployments, enssync.Deployment{Address: entry.contract.Address, Profile: entry.profile, Number: 1, Hash: f.tip.Hash(), CodeHash: crypto.Keccak256Hash(entry.contract.Code)})
	}
	// Factory proxies carry instance-specific immutable runtime bytes. These
	// additional instances are explicitly reviewed for the graph regression.
	for _, registry := range registries {
		f.manifest.Deployments = append(f.manifest.Deployments, enssync.Deployment{
			Address: registry.Address, Profile: "v2_registry", Number: 1, Hash: f.tip.Hash(),
			CodeHash: crypto.Keccak256Hash(registry.Code), Implementation: real.ChildImplementation.Address,
			ImplementationCodeHash: crypto.Keccak256Hash(real.ChildImplementation.Code),
		})
	}
	f.manifest.Implementations = []enssync.Implementation{
		{Address: real.ChildImplementation.Address, CodeHash: crypto.Keccak256Hash(real.ChildImplementation.Code), ProxyCodeHash: crypto.Keccak256Hash(real.State.GetCode(real.Child)), Profile: "v2_registry"},
		{Address: real.ResolverImplementation.Address, CodeHash: crypto.Keccak256Hash(real.ResolverImplementation.Code), ProxyCodeHash: crypto.Keccak256Hash(real.State.GetCode(real.Resolver)), Profile: "v2_resolver"},
	}
	f.repo, err = enssync.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	f.restart(t)
	return f
}
func (f *ensV2CollectionFixture) appendBlock(t *testing.T) {
	t.Helper()
	number := f.tip.Number.Uint64() + 1
	block, err := newIntegrationBundle(integrationBundleOptions{Number: number, Timestamp: f.real.Config.Time + number, ParentHash: f.tip.Hash(), ExtraData: []byte(fmt.Sprintf("v2-regression-%d", number)), Transactions: []integrationTransactionOptions{{Type: types.DynamicFeeTxType, To: &f.real.Registry.Address, Logs: f.real.Logs[f.logCursor:]}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.core.CommitCanonicalSegment(t.Context(), "1", []chainbundle.Bundle{block}); err != nil {
		t.Fatal(err)
	}
	f.tip = block.Block.Header()
	f.logCursor = len(f.real.Logs)
	f.rpc.headers[f.tip.Hash()] = f.tip
	f.rpc.states[f.tip.Hash()] = f.real.State.Copy()
}
func (f *ensV2CollectionFixture) restart(t *testing.T) {
	t.Helper()
	var err error
	f.collector, err = enssync.NewCollector(f.repo, f.manifest, f.pool, enssync.CollectorOptions{})
	if err != nil {
		t.Fatal(err)
	}
}
func (f *ensV2CollectionFixture) step(t *testing.T) int64 {
	t.Helper()
	result, err := f.collector.Step(t.Context(), true)
	if err != nil {
		t.Fatalf("collect v2: %+v %v", result, err)
	}
	progress, err := dbgen.New(f.db).GetENSSyncProgress(t.Context(), result.SourceID)
	if err != nil || progress.State != "active" || common.BytesToHash(progress.ScannedHash) != f.tip.Hash() {
		t.Fatalf("collection did not remain active at tip: %+v %v", progress, err)
	}
	return result.SourceID
}

func TestENSV2UnknownSubregistryDoesNotHaltCollection(t *testing.T) {
	real := ensfixture.New(t)
	expiry := uint64(1_900_000_000)
	real.Call(real.ChildContract(), "register", "wallet", real.Owner, common.Address{}, common.Address{}, big.NewInt(0), expiry)
	real.Call(real.Registry, "register", "alice", real.Owner, real.Child, common.Address{}, big.NewInt(0), expiry)
	f := newENSV2CollectionFixture(t, real)
	f.step(t)
	assertRowCount(t, t.Context(), f.db, `SELECT count(*) FROM ens_sync_candidates WHERE direction='forward' AND lookup_key='wallet.alice.eth'`, 1)
	// A custom contract that returns true, without a reviewed registry identity.
	custom := real.DeployTestCode(common.FromHex("0x600a600c600039600a6000f3600160005260206000f3"))
	alice := new(big.Int).SetBytes(crypto.Keccak256([]byte("alice")))
	real.Call(real.Registry, "setSubregistry", alice, custom.Address)
	real.Call(real.ChildContract(), "register", "disconnected", real.Owner, common.Address{}, common.Address{}, big.NewInt(0), expiry)
	real.Call(real.Registry, "register", "unrelated", real.Owner, common.Address{}, common.Address{}, big.NewInt(0), expiry)
	real.Logs = append(real.Logs, ensCollectorLog(t, "v2_registry", "LabelRegistered", custom.Address, big.NewInt(1), crypto.Keccak256Hash([]byte("spoof")), "spoof", real.Owner, expiry, real.Owner))
	f.appendBlock(t)
	f.step(t)
	assertRowCount(t, t.Context(), f.db, `SELECT count(*) FROM ens_sync_projections WHERE kind='registration' AND payload->>'label'='alice' AND payload->>'subregistry'=$1`, 1, strings.ToLower(custom.Address.Hex()))
	assertRowCount(t, t.Context(), f.db, `SELECT count(*) FROM ens_sync_deployments WHERE address=$1`, 0, custom.Address.Bytes())
	assertRowCount(t, t.Context(), f.db, `SELECT count(*) FROM ens_sync_events WHERE emitter=$1`, 0, custom.Address.Bytes())
	assertRowCount(t, t.Context(), f.db, `SELECT count(*) FROM ens_sync_candidates WHERE direction='forward' AND lookup_key='unrelated.eth'`, 1)
	assertRowCount(t, t.Context(), f.db, `SELECT count(*) FROM ens_sync_candidates WHERE direction='forward' AND lookup_key LIKE 'spoof.%'`, 0)
	assertRowCount(t, t.Context(), f.db, `SELECT count(*) FROM ens_sync_candidates WHERE direction='forward' AND lookup_key='disconnected.alice.eth'`, 0)
	// Repairing a user-owned link is observed normally, including after restart.
	real.Call(real.Registry, "setSubregistry", alice, real.Child)
	real.Call(real.ChildContract(), "register", "recovered", real.Owner, common.Address{}, common.Address{}, big.NewInt(0), expiry)
	f.appendBlock(t)
	f.restart(t)
	f.step(t)
	assertRowCount(t, t.Context(), f.db, `SELECT count(*) FROM ens_sync_candidates WHERE direction='forward' AND lookup_key='recovered.alice.eth'`, 1)
	assertRowCount(t, t.Context(), f.db, `SELECT count(*) FROM ens_sync_candidates WHERE direction='forward' AND lookup_key='disconnected.alice.eth'`, 1)
}

func TestENSV2GraphBudgetCheckpointsAndResumes(t *testing.T) {
	real := ensfixture.New(t)
	expiry := uint64(1_900_000_000)
	roles, _ := new(big.Int).SetString(strings.Repeat("1", 64), 16)
	init, err := real.ChildImplementation.ABI.Pack("initialize", []struct {
		Account    common.Address
		RoleBitmap *big.Int
	}{{real.Owner, roles}})
	if err != nil {
		t.Fatal(err)
	}
	parent := real.Registry
	var registries []ensfixture.Contract
	suffix := "eth"
	for level := range 6 {
		child := common.BytesToAddress(real.Call(real.Factory, "deployProxy", real.ChildImplementation.Address, big.NewInt(int64(10+level)), init))
		label := fmt.Sprintf("level%d", level)
		real.Call(parent, "register", label, real.Owner, child, common.Address{}, big.NewInt(0), expiry)
		suffix = label + "." + suffix
		parent = ensfixture.Contract{Address: child, ABI: real.ChildImplementation.ABI, Code: real.State.GetCode(child)}
		registries = append(registries, parent)
	}
	for i := range 100 {
		real.Call(parent, "register", fmt.Sprintf("leaf%03d", i), real.Owner, common.Address{}, common.Address{}, big.NewInt(0), expiry)
	}
	f := newENSV2CollectionFixture(t, real, registries...)
	source := f.step(t)
	var after string
	var pending bool
	if err = f.db.QueryRow(t.Context(), `SELECT after_key,pending FROM ens_sync_graph_work WHERE source_id=$1`, source).Scan(&after, &pending); err != nil {
		t.Fatal(err)
	}
	if after == "" || !pending {
		t.Fatalf("graph budget did not checkpoint incomplete page: after=%q pending=%v", after, pending)
	}
	var count int
	if err = f.db.QueryRow(t.Context(), `SELECT count(*) FROM ens_sync_candidates WHERE direction='forward' AND lookup_key LIKE 'leaf%'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 || count >= 100 {
		t.Fatalf("expected bounded partial leaf discovery, got %d", count)
	}
	f.restart(t)
	for rounds := 0; pending && rounds < 8; rounds++ {
		f.step(t)
		if err = f.db.QueryRow(t.Context(), `SELECT pending FROM ens_sync_graph_work WHERE source_id=$1`, source).Scan(&pending); err != nil {
			t.Fatal(err)
		}
	}
	if pending {
		t.Fatal("graph failed to finish after bounded resume rounds")
	}
	assertRowCount(t, t.Context(), f.db, `SELECT count(*) FROM ens_sync_candidates WHERE direction='forward' AND lookup_key LIKE $1`, 100, "leaf%."+suffix)
	assertRowCount(t, t.Context(), f.db, `SELECT count(*) FROM ens_sync_candidates WHERE direction='profile' AND lookup_key LIKE $1`, 100, "leaf%."+suffix)
	// A clean idle round must neither replay origins nor create duplicate names.
	f.step(t)
	assertRowCount(t, t.Context(), f.db, `SELECT count(*) FROM ens_sync_candidate_origins o JOIN ens_sync_candidates c ON c.source_id=o.source_id AND c.id=o.candidate_id WHERE c.direction='forward' AND c.lookup_key LIKE $1`, 100, "leaf%."+suffix)
}
