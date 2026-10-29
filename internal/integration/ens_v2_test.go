//go:build integration

package integration_test

import (
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/enssync"
	"github.com/islishude/etherview/internal/enssync/ensfixture"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
)

type ensV2RPC struct {
	*ensCollectorRPC
	fixture *ensfixture.Fixture
}

func (s *ensV2RPC) GetCode(address common.Address, block rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	if block.BlockHash == nil || !block.RequireCanonical || *block.BlockHash != s.block.Block.Hash() {
		return nil, errors.New("wrong state block")
	}
	return s.fixture.State.GetCode(address), nil
}
func (s *ensV2RPC) GetStorageAt(address common.Address, slot common.Hash, block rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	if block.BlockHash == nil || !block.RequireCanonical || *block.BlockHash != s.block.Block.Hash() {
		return nil, errors.New("wrong storage block")
	}
	return s.fixture.State.GetState(address, slot).Bytes(), nil
}
func (s *ensV2RPC) GetLogs(filter map[string]json.RawMessage) ([]*types.Log, error) {
	var hash common.Hash
	if json.Unmarshal(filter["blockHash"], &hash) != nil || hash != s.block.Block.Hash() || len(filter) != 1 {
		return nil, errors.New("wrong logs block")
	}
	result := []*types.Log{}
	for _, receipt := range s.block.Receipts {
		result = append(result, receipt.Logs...)
	}
	return result, nil
}

func TestENSV2RealContractsDiscoveryAndSharedPaths(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	execFixture(t, ctx, db, `INSERT INTO chains(chain_id) VALUES(1)`)
	real := ensfixture.New(t)
	migration := real.Migration()
	for _, entry := range []struct {
		label           string
		wrapped, locked bool
	}{{"v1plain", false, false}, {"v1wrapped", true, false}, {"v1locked", true, true}} {
		real.RegisterV1(migration, entry.label, entry.wrapped, entry.locked)
		real.MigrateV1(migration, entry.label, entry.wrapped, entry.locked)
	}
	wrapperProxy := common.BytesToAddress(real.Call(real.Registry, "getSubregistry", "v1locked"))
	expiry := uint64(1_900_000_000)
	// Instances and records exist before any parent links to them. Their factory
	// emits after proxy initialization, using the actual pinned upstream bytecode.
	real.Call(real.ChildContract(), "register", "wallet", real.Owner, common.Address{}, real.Resolver, big.NewInt(0), expiry)
	real.Call(real.ResolverContract(), "setAddress", []byte("\x06wallet\x05alice\x03eth\x00"), big.NewInt(60), real.Owner.Bytes())
	real.Call(real.Registry, "register", "alice", real.Owner, real.Child, real.Resolver, big.NewInt(0), expiry)
	real.Call(real.Registry, "register", "alias", real.Owner, real.Child, real.Resolver, big.NewInt(0), expiry)
	customResolver := testAddress(152098)
	real.Call(real.Registry, "register", "custom", real.Owner, common.Address{}, customResolver, big.NewInt(0), expiry)
	real.Call(real.Registry, "register", "reserved", common.Address{}, common.Address{}, common.Address{}, big.NewInt(0), expiry)
	real.Call(real.Registry, "register", "gone", real.Owner, common.Address{}, common.Address{}, big.NewInt(0), expiry)
	gone := new(big.Int).SetBytes(crypto.Keccak256([]byte("gone")))
	real.Call(real.Registry, "unregister", gone)
	alice := new(big.Int).SetBytes(crypto.Keccak256([]byte("alice")))
	// Changing roles regenerates token ID without changing the name or resource.
	real.Call(real.Registry, "grantRoles", alice, new(big.Int).Lsh(big.NewInt(1), 20), testAddress(152099))
	// Default records are record bundles linked to the DNS root, not a name.
	real.Call(real.ResolverContract(), "setAddress", []byte{0}, big.NewInt(60), real.Owner.Bytes())
	real.Call(real.ResolverContract(), "linkToRecord", []byte("\x06wallet\x05alias\x03eth\x00"), big.NewInt(1))
	// A cycle is retained as a graph fact but cannot recurse forever.
	wallet := new(big.Int).SetBytes(crypto.Keccak256([]byte("wallet")))
	real.Call(real.ChildContract(), "setSubregistry", wallet, real.Registry.Address)
	// A custom resolver can emit a standard-looking event without becoming a
	// trusted emitter merely because a reviewed registry links to it.
	for _, log := range real.Logs {
		if log.Address == real.Resolver {
			spoof := *log
			spoof.Address = customResolver
			real.Logs = append(real.Logs, &spoof)
		}
	}

	mainnet := core.DefaultGenesisBlock().ToBlock().Header()
	block, e := newIntegrationBundle(integrationBundleOptions{Number: 1, Timestamp: real.Config.Time, ParentHash: mainnet.Hash(), ExtraData: []byte("real-v2"), Transactions: []integrationTransactionOptions{{Type: types.DynamicFeeTxType, To: &real.Factory.Address, Logs: real.Logs}}})
	if e != nil {
		t.Fatal(e)
	}
	coreRepo, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = coreRepo.ConfigureIndex(ctx, "1", 1); err != nil {
		t.Fatal(err)
	}
	if _, err = coreRepo.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{block}); err != nil {
		t.Fatal(err)
	}
	if err = store.BindChainIdentity(ctx, db, "1", mainnet.Hash()); err != nil {
		t.Fatal(err)
	}
	service := &ensV2RPC{&ensCollectorRPC{genesis: mainnet, block: block}, real}
	server := rpc.NewServer()
	if e = server.RegisterName("eth", service); e != nil {
		t.Fatal(e)
	}
	client := rpc.DialInProc(server)
	t.Cleanup(client.Close)
	pool, e := ethrpc.NewPool([]ethrpc.Endpoint{{Name: "local-v2-fixture", Client: client, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true}}}, ethrpc.PoolOptions{})
	if e != nil {
		t.Fatal(e)
	}
	manifest := enssync.Manifest{TargetChainID: "1", SourceChainID: "1", Genesis: mainnet.Hash(), Policy: testHash(152001), Start: 1, StartHash: block.Block.Hash(), ParentName: "eth", CoinType: "60", Roots: []enssync.RegistryRoot{{Address: real.Registry.Address, Name: "eth"}}}
	for _, entry := range []struct {
		contract ensfixture.Contract
		profile  string
	}{{real.Registry, "v2_registry"}, {real.Factory, "v2_factory"}, {migration.Registry, "registry"}, {migration.Registrar, "registrar"}, {migration.Wrapper, "wrapper"}, {migration.Resolver, "resolver"}, {migration.Reverse, "reverse"}, {migration.DefaultReverse, "default_reverse"}} {
		manifest.Deployments = append(manifest.Deployments, enssync.Deployment{Address: entry.contract.Address, Profile: entry.profile, Number: 1, Hash: block.Block.Hash(), CodeHash: crypto.Keccak256Hash(entry.contract.Code)})
	}
	manifest.Implementations = []enssync.Implementation{{Address: real.ChildImplementation.Address, CodeHash: crypto.Keccak256Hash(real.ChildImplementation.Code), ProxyCodeHash: crypto.Keccak256Hash(real.State.GetCode(real.Child)), Profile: "v2_registry"}, {Address: real.ResolverImplementation.Address, CodeHash: crypto.Keccak256Hash(real.ResolverImplementation.Code), ProxyCodeHash: crypto.Keccak256Hash(real.State.GetCode(real.Resolver)), Profile: "v2_resolver"}}
	manifest.Implementations = append(manifest.Implementations, enssync.Implementation{Address: migration.WrapperImplementation.Address, CodeHash: crypto.Keccak256Hash(migration.WrapperImplementation.Code), ProxyCodeHash: crypto.Keccak256Hash(real.State.GetCode(wrapperProxy)), Profile: "v2_registry"})
	repo, _ := enssync.NewRepository(db)
	collector, e := enssync.NewCollector(repo, manifest, pool, enssync.CollectorOptions{})
	if e != nil {
		t.Fatal(e)
	}
	result, e := collector.Step(ctx, true)
	if e != nil || result.Scanned != 1 {
		t.Fatalf("collect real v2: %+v %v", result, e)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM ens_sync_deployments`, 11)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM ens_sync_candidates WHERE direction='forward' AND lookup_key IN ('v1plain.eth','v1wrapped.eth','v1locked.eth')`, 3)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM ens_sync_events WHERE emitter=$1`, 0, customResolver.Bytes())
	assertRowCount(t, ctx, db, `SELECT count(*) FROM ens_sync_candidates WHERE direction='forward' AND lookup_key='custom.eth'`, 1)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM ens_sync_projections WHERE kind='registration' AND payload->>'label'='custom' AND payload->>'resolver'=$1`, 1, strings.ToLower(customResolver.Hex()))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM ens_sync_deployments WHERE address IN ($1,$2)`, 0, real.ChildImplementation.Address.Bytes(), real.ResolverImplementation.Address.Bytes())
	assertRowCount(t, ctx, db, `SELECT count(*) FROM ens_sync_candidates WHERE direction='forward' AND lookup_key IN ('wallet.alice.eth','wallet.alias.eth','alice.eth','alias.eth')`, 4)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM ens_sync_projections WHERE kind='resolver_record' AND key LIKE '%/addr/60'`, 5)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM ens_sync_projections WHERE kind='name_link' AND key LIKE '%/link/%'`, 3)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM ens_sync_publications`, 0)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM ens_sync_projections WHERE kind='registration' AND payload->>'label'='reserved' AND payload->>'reserved'='true' AND payload->>'registered'='false'`, 1)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM (SELECT DISTINCT ON(key) payload FROM ens_sync_projections WHERE kind='registration' ORDER BY key,revision DESC) latest WHERE payload->>'label'='gone' AND payload->>'registered'='false'`, 1)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM (SELECT DISTINCT ON(key) payload FROM ens_sync_projections WHERE kind='lifecycle' ORDER BY key,revision DESC) latest WHERE payload->>'label_hash'=$1 AND payload ? 'resource'`, 2, crypto.Keccak256Hash([]byte("alice")).Hex())
	if _, e = collector.Step(ctx, true); e != nil {
		t.Fatal(e)
	}
}
