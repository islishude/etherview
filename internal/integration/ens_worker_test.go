//go:build integration

package integration_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/chainbundle"
	ensresolver "github.com/islishude/etherview/internal/ens"
	"github.com/islishude/etherview/internal/enscontract"
	"github.com/islishude/etherview/internal/enssync"
	"github.com/islishude/etherview/internal/ethrpc"
)

const ensWorkerABI = `[{"type":"function","name":"resolve","inputs":[{"name":"name","type":"bytes"},{"name":"data","type":"bytes"}],"outputs":[{"type":"bytes"},{"type":"address"}]},{"type":"function","name":"reverse","inputs":[{"name":"address","type":"bytes"},{"name":"coinType","type":"uint256"}],"outputs":[{"type":"string"},{"type":"address"},{"type":"address"}]}]`

type ensWorkerRPC struct {
	*ensCollectorRPC
	abi               abi.ABI
	address, resolver common.Address
	fail              bool
	noRecord          bool
	stateCalls        int
}

func (s *ensWorkerRPC) Call(request map[string]json.RawMessage, block rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stateCalls++
	if block.BlockHash == nil || *block.BlockHash != s.block.Block.Hash() || !block.RequireCanonical {
		return nil, errors.New("wrong exact block")
	}
	if s.fail {
		return nil, errors.New("private upstream failure must not persist")
	}
	var input hexutil.Bytes
	if json.Unmarshal(request["data"], &input) != nil || len(input) < 4 {
		return nil, errors.New("invalid call")
	}
	method, e := s.abi.MethodById(input[:4])
	if e != nil {
		return nil, e
	}
	if method.Name == "reverse" {
		if s.noRecord {
			return method.Outputs.Pack("", common.Address{}, common.Address{})
		}
		return method.Outputs.Pack("alice.test", s.resolver, s.resolver)
	}
	if s.noRecord {
		return method.Outputs.Pack(make([]byte, 32), s.resolver)
	}
	return method.Outputs.Pack(common.LeftPadBytes(s.address.Bytes(), 32), s.resolver)
}
func TestENSWorkerAutonomousResolutionAndTerminalBudget(t *testing.T) {
	f := newENSCollectorFixture(t)
	ctx := t.Context()
	f.manifest.Deployments[1].Profile = "universal"
	repo, _ := enssync.NewRepository(f.db)
	collector, e := enssync.NewCollector(repo, f.manifest, nil, enssync.CollectorOptions{})
	if e != nil {
		t.Fatal(e)
	}
	result, e := collector.Step(ctx, true)
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := abi.JSON(strings.NewReader(ensWorkerABI))
	if e != nil {
		t.Fatal(e)
	}
	service := &ensWorkerRPC{ensCollectorRPC: &ensCollectorRPC{genesis: f.genesis.Block.Header(), block: f.original}, abi: parsed, address: f.owner, resolver: f.manifest.Deployments[1].Address}
	server := rpc.NewServer()
	if e = server.RegisterName("eth", service); e != nil {
		t.Fatal(e)
	}
	client := rpc.DialInProc(server)
	t.Cleanup(client.Close)
	pool, e := ethrpc.NewPool([]ethrpc.Endpoint{{Name: "worker-fixture", Client: client, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true}}}, ethrpc.PoolOptions{})
	if e != nil {
		t.Fatal(e)
	}
	normalizer, e := ensresolver.NewNormalizer()
	if e != nil {
		t.Fatal(e)
	}
	resolver, e := ensresolver.NewResolver(normalizer, nil, 4)
	if e != nil {
		t.Fatal(e)
	}
	worker, e := enssync.NewWorker(repo, enssync.WorkerOptions{SourceID: result.SourceID, Manifest: f.manifest, Registry: f.manifest.Deployments[0].Address, UniversalResolver: service.resolver, Pool: pool, Resolver: resolver, FailureDelay: time.Second, MaxAttempts: 2})
	if e != nil {
		t.Fatal(e)
	}
	if e = worker.Schedule(ctx, 100); e != nil {
		t.Fatal(e)
	}
	for range 10 {
		worked, e := worker.Step(ctx)
		if !worked {
			if e != nil {
				t.Fatal(e)
			}
			break
		}
		if e != nil {
			var typed *ensresolver.ResolutionError
			if !errors.As(e, &typed) || typed.Code != ensresolver.CodeForwardMismatch {
				t.Fatal(e)
			}
		}
	}
	got, e := repo.Lookup(ctx, enscontract.Lookup{SourceID: result.SourceID, TargetChainID: "1", PolicyDigest: f.manifest.Policy, Direction: enscontract.Forward, Key: "alice.test", CoinType: "60"})
	if e != nil || got.State != enscontract.Resolved || got.Address != f.owner {
		t.Fatalf("forward %+v %v", got, e)
	}
	got, e = repo.Lookup(ctx, enscontract.Lookup{SourceID: result.SourceID, TargetChainID: "1", PolicyDigest: f.manifest.Policy, Direction: enscontract.Primary, Key: strings.ToLower(f.owner.Hex()), CoinType: "60"})
	if e != nil || got.State != enscontract.Resolved {
		t.Fatalf("primary %+v %v", got, e)
	}
	// A refresh is background work. Failed generations retain attempt/deadline
	// history and cannot be recreated just because the scheduler runs again.
	execFixture(t, ctx, f.db, `UPDATE ens_sync_candidates SET refresh_at=now()-interval '1 second' WHERE direction='forward'`)
	if e = worker.Schedule(ctx, 100); e != nil {
		t.Fatal(e)
	}
	service.mu.Lock()
	service.fail = true
	service.mu.Unlock()
	_, _ = worker.Step(ctx)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_jobs j JOIN ens_sync_candidates c ON c.id=j.candidate_id WHERE c.direction='forward' AND j.state='queued' AND j.attempts=1`, 1)
	advanced := testBundle(2, testHash(160110), f.original.Block.Hash(), testHash(160111), "retry-target-advance")
	if _, e = f.core.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{advanced}); e != nil {
		t.Fatal(e)
	}
	if _, e = collector.Step(ctx, true); e != nil {
		t.Fatal(e)
	}
	service.mu.Lock()
	service.block = advanced
	service.mu.Unlock()
	if e = worker.Schedule(ctx, 100); e != nil {
		t.Fatal(e)
	}
	for range 8 {
		_, _ = worker.Step(ctx)
		execFixture(t, ctx, f.db, `UPDATE ens_sync_jobs SET retry_at=now()-interval '1 second' WHERE state='queued'`)
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_jobs j JOIN ens_sync_candidates c ON c.id=j.candidate_id WHERE c.direction='forward' AND j.state='failed' AND j.attempts=2`, 1)
	for range 3 {
		if e = worker.Schedule(ctx, 100); e != nil {
			t.Fatal(e)
		}
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_jobs j JOIN ens_sync_candidates c ON c.id=j.candidate_id WHERE c.direction='forward'`, 3)
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_jobs WHERE failure_code LIKE '%private%'`, 0)
}

func TestENSWorkerStandardRefreshKeepsValidLabelAndAvoidsRPC(t *testing.T) {
	f := newENSCollectorFixture(t)
	ctx := t.Context()
	ur, registry := testAddress(160100), testAddress(160101)
	for _, d := range []struct {
		address common.Address
		profile string
	}{{ur, "universal"}, {registry, "registry"}} {
		execFixture(t, ctx, f.db, `INSERT INTO genesis_account_observations(chain_id,address,block_hash,balance,nonce,code_hash,code,storage_root) SELECT chain_id,$1,block_hash,balance,nonce,code_hash,code,storage_root FROM genesis_account_observations LIMIT 1`, d.address.Bytes())
		f.manifest.Deployments = append(f.manifest.Deployments, enssync.Deployment{Address: d.address, Profile: d.profile, Hash: f.genesis.Block.Hash(), CodeHash: f.manifest.Deployments[0].CodeHash})
	}
	node, _ := ensresolver.Namehash("alice.test")
	block, e := newIntegrationBundle(integrationBundleOptions{Number: 2, ParentHash: f.original.Block.Hash(), ExtraData: []byte("ens-standard"), Transactions: []integrationTransactionOptions{{Type: types.DynamicFeeTxType, To: &ur, Logs: []*types.Log{ensCollectorLog(t, "resolver", "AddrChanged", f.manifest.Deployments[1].Address, node, f.owner)}}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.core.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{block}); e != nil {
		t.Fatal(e)
	}
	repo, _ := enssync.NewRepository(f.db)
	collector, e := enssync.NewCollector(repo, f.manifest, nil, enssync.CollectorOptions{})
	if e != nil {
		t.Fatal(e)
	}
	result, e := collector.Step(ctx, true)
	if e != nil {
		t.Fatal(e)
	}
	parsed, _ := abi.JSON(strings.NewReader(ensWorkerABI))
	service := &ensWorkerRPC{ensCollectorRPC: &ensCollectorRPC{genesis: f.genesis.Block.Header(), block: block}, abi: parsed, address: f.owner, resolver: f.manifest.Deployments[1].Address}
	server := rpc.NewServer()
	if e = server.RegisterName("eth", service); e != nil {
		t.Fatal(e)
	}
	client := rpc.DialInProc(server)
	t.Cleanup(client.Close)
	pool, _ := ethrpc.NewPool([]ethrpc.Endpoint{{Name: "standard-fixture", Client: client, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true}}}, ethrpc.PoolOptions{})
	normalizer, _ := ensresolver.NewNormalizer()
	resolver, _ := ensresolver.NewResolver(normalizer, nil, 4)
	worker, e := enssync.NewWorker(repo, enssync.WorkerOptions{SourceID: result.SourceID, Manifest: f.manifest, Registry: registry, UniversalResolver: ur, Pool: pool, Resolver: resolver})
	if e != nil {
		t.Fatal(e)
	}
	drain := func() {
		t.Helper()
		for range 20 {
			worked, e := worker.Step(ctx)
			if e != nil {
				var typed *ensresolver.ResolutionError
				if !errors.As(e, &typed) || typed.Code != ensresolver.CodeForwardMismatch {
					t.Fatal(e)
				}
			}
			if !worked {
				break
			}
		}
	}
	if e = worker.Schedule(ctx, 100); e != nil {
		t.Fatal(e)
	}
	drain()
	lookup := enscontract.Lookup{SourceID: result.SourceID, TargetChainID: "1", PolicyDigest: f.manifest.Policy, Direction: enscontract.Forward, Key: "alice.test", CoinType: "60"}
	got, e := repo.Lookup(ctx, lookup)
	if e != nil || got.State != enscontract.Resolved {
		t.Fatal(got, e)
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_observations WHERE evidence='projection'`, 1)
	execFixture(t, ctx, f.db, `UPDATE ens_sync_candidates SET refresh_at=now()-interval '1 second' WHERE direction='forward'`)
	if e = worker.Schedule(ctx, 100); e != nil {
		t.Fatal(e)
	}
	got, e = repo.Lookup(ctx, lookup)
	if e != nil || got.State != enscontract.Resolved {
		t.Fatalf("refresh hid still-valid result: %+v %v", got, e)
	}
	service.mu.Lock()
	before := service.stateCalls
	service.fail = true
	service.mu.Unlock()
	drain()
	service.mu.Lock()
	after := service.stateCalls
	service.mu.Unlock()
	if after != before {
		t.Fatalf("standard refresh called RPC: %d -> %d", before, after)
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_observations WHERE evidence='projection'`, 2)
	// A record change invalidates a retained publication before any scheduler or
	// bounded fanout has run. The next job must obtain fresh exact-block evidence.
	changed, e := newIntegrationBundle(integrationBundleOptions{Number: 3, ParentHash: block.Block.Hash(), ExtraData: []byte("ens-standard-change"), Transactions: []integrationTransactionOptions{{Type: types.DynamicFeeTxType, To: &ur, Logs: []*types.Log{ensCollectorLog(t, "resolver", "AddrChanged", f.manifest.Deployments[1].Address, node, testAddress(160102))}}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.core.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{changed}); e != nil {
		t.Fatal(e)
	}
	if _, e = collector.Step(ctx, true); e != nil {
		t.Fatal(e)
	}
	got, e = repo.Lookup(ctx, lookup)
	if e != nil || got.State != enscontract.Unavailable || got.Name != "" {
		t.Fatalf("changed dependency retained label: %+v %v", got, e)
	}
}

func TestENSWorkerNegativeRecordsWithdrawBeforeRefresh(t *testing.T) {
	for _, direction := range []enscontract.Direction{enscontract.Forward, enscontract.Primary} {
		t.Run(string(direction), func(t *testing.T) {
			f := newENSCollectorFixture(t)
			ctx := t.Context()
			ur, registry, reverse := testAddress(160200), testAddress(160201), testAddress(160202)
			for _, d := range []struct {
				address common.Address
				profile string
			}{{ur, "universal"}, {registry, "registry"}, {reverse, "default_reverse"}} {
				execFixture(t, ctx, f.db, `INSERT INTO genesis_account_observations(chain_id,address,block_hash,balance,nonce,code_hash,code,storage_root) SELECT chain_id,$1,block_hash,balance,nonce,code_hash,code,storage_root FROM genesis_account_observations LIMIT 1`, d.address.Bytes())
				f.manifest.Deployments = append(f.manifest.Deployments, enssync.Deployment{Address: d.address, Profile: d.profile, Hash: f.genesis.Block.Hash(), CodeHash: f.manifest.Deployments[0].CodeHash})
			}
			// Configured future deployments cannot supply dependency markers yet.
			f.manifest.Deployments = append(f.manifest.Deployments, enssync.Deployment{Address: testAddress(160203), Profile: "default_reverse", Number: 10, Hash: testHash(160204), CodeHash: f.manifest.Deployments[0].CodeHash})
			repo, _ := enssync.NewRepository(f.db)
			collector, e := enssync.NewCollector(repo, f.manifest, nil, enssync.CollectorOptions{})
			if e != nil {
				t.Fatal(e)
			}
			result, e := collector.Step(ctx, true)
			if e != nil {
				t.Fatal(e)
			}
			parsed, _ := abi.JSON(strings.NewReader(ensWorkerABI))
			service := &ensWorkerRPC{ensCollectorRPC: &ensCollectorRPC{genesis: f.genesis.Block.Header(), block: f.original}, abi: parsed, address: f.owner, resolver: f.manifest.Deployments[1].Address, noRecord: true}
			server := rpc.NewServer()
			if e = server.RegisterName("eth", service); e != nil {
				t.Fatal(e)
			}
			client := rpc.DialInProc(server)
			t.Cleanup(client.Close)
			pool, e := ethrpc.NewPool([]ethrpc.Endpoint{{Name: "negative-fixture", Client: client, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true}}}, ethrpc.PoolOptions{})
			if e != nil {
				t.Fatal(e)
			}
			normalizer, _ := ensresolver.NewNormalizer()
			resolver, _ := ensresolver.NewResolver(normalizer, nil, 4)
			worker, e := enssync.NewWorker(repo, enssync.WorkerOptions{SourceID: result.SourceID, Manifest: f.manifest, Registry: registry, UniversalResolver: ur, Pool: pool, Resolver: resolver})
			if e != nil {
				t.Fatal(e)
			}
			if e = worker.Schedule(ctx, 100); e != nil {
				t.Fatal(e)
			}
			for range 20 {
				worked, e := worker.Step(ctx)
				if e != nil {
					t.Fatal(e)
				}
				if !worked {
					break
				}
			}
			key := "alice.test"
			if direction == enscontract.Primary {
				key = strings.ToLower(f.owner.Hex())
			}
			lookup := enscontract.Lookup{SourceID: result.SourceID, TargetChainID: "1", PolicyDigest: f.manifest.Policy, Direction: direction, Key: key, CoinType: "60"}
			got, e := repo.Lookup(ctx, lookup)
			if e != nil || got.State != enscontract.NotFound {
				t.Fatalf("negative publication: %+v %v", got, e)
			}
			node, _ := ensresolver.Namehash("alice.test")
			event := ensCollectorLog(t, "resolver", "AddrChanged", service.resolver, node, f.owner)
			if direction == enscontract.Primary {
				event = ensCollectorLog(t, "default_reverse", "NameForAddrChanged", reverse, f.owner, "alice.test")
			}
			changed, e := newIntegrationBundle(integrationBundleOptions{Number: 2, ParentHash: f.original.Block.Hash(), ExtraData: []byte("negative-record-changed"), Transactions: []integrationTransactionOptions{{Type: types.DynamicFeeTxType, To: &ur, Logs: []*types.Log{event}}}})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.core.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{changed}); e != nil {
				t.Fatal(e)
			}
			if _, e = collector.Step(ctx, true); e != nil {
				t.Fatal(e)
			}
			// No scheduler or resolver has run since the record changed. In particular,
			// this old negative must no longer authorize an official-to-custom fallback.
			got, e = repo.Lookup(ctx, lookup)
			if e != nil || got.State != enscontract.Unavailable {
				t.Fatalf("changed record retained negative publication: %+v %v", got, e)
			}
		})
	}
}
