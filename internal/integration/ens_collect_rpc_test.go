//go:build integration

package integration_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/chainbundle"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enssync"
	"github.com/islishude/etherview/internal/ethrpc"
)

type ensCollectorRPC struct {
	mu                             sync.Mutex
	genesis                        *types.Header
	block                          chainbundle.Bundle
	calls                          int
	logCalls                       int
	conflict, upgrade, badIdentity bool
}

func (s *ensCollectorRPC) ChainId() hexutil.Uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.badIdentity {
		return 2
	}
	return 1
}
func (s *ensCollectorRPC) GetBlockByNumber(number string, _ bool) (*types.Header, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	switch number {
	case "0x0":
		return s.genesis, nil
	case "0x1", "finalized":
		h := s.block.Block.Header()
		if s.conflict {
			h.Extra = []byte("finalized-conflict")
		}
		return h, nil
	default:
		return nil, errors.New("private RPC unavailable")
	}
}
func (s *ensCollectorRPC) GetBlockByHash(hash common.Hash, _ bool) (*types.Header, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.genesis.Hash() == hash {
		return s.genesis, nil
	}
	h := s.block.Block.Header()
	if s.conflict {
		h.Extra = []byte("finalized-conflict")
	}
	if h.Hash() != hash {
		return nil, errors.New("wrong hash")
	}
	return h, nil
}
func (s *ensCollectorRPC) GetCode(_ common.Address, block rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if block.BlockHash == nil || !block.RequireCanonical || *block.BlockHash != s.block.Block.Hash() {
		return nil, errors.New("state must be exact")
	}
	if s.upgrade {
		return []byte{1}, nil
	}
	return []byte{0x60, 0, 0x00}, nil
}
func (s *ensCollectorRPC) GetLogs(filter map[string]json.RawMessage) ([]*types.Log, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.logCalls++
	var hash common.Hash
	if json.Unmarshal(filter["blockHash"], &hash) != nil || hash != s.block.Block.Hash() || len(filter) != 2 {
		return nil, errors.New("logs must be exact")
	}
	logs := []*types.Log{}
	for _, receipt := range s.block.Receipts {
		logs = append(logs, receipt.Logs...)
	}
	return logs, nil
}

func TestENSCollectorRoundOutlivesIndividualRequestBudget(t *testing.T) {
	f := newENSCollectorFixture(t)
	mainnet := f.genesis.Block.Header()
	state := &ensCollectorRPC{genesis: mainnet, block: f.original}
	server := rpc.NewServer()
	if err := server.RegisterName("eth", state); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
			server.ServeHTTP(w, r)
		}
	}))
	defer upstream.Close()
	client, err := rpc.DialOptions(t.Context(), upstream.URL, rpc.WithHTTPClient(&http.Client{Timeout: 2 * time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{Name: "history", Client: client, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true}}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	m := f.manifest
	m.Genesis = mainnet.Hash()
	m.Start, m.StartHash = 1, f.original.Block.Hash()
	m.Deployments = append([]enssync.Deployment(nil), m.Deployments...)
	for i := range m.Deployments {
		m.Deployments[i].Number, m.Deployments[i].Hash = 1, m.StartHash
	}
	repo, _ := enssync.NewRepository(f.db)
	collector, err := enssync.NewCollector(repo, m, pool, enssync.CollectorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Advance the core head while the bounded historical round is in flight.
	// Its pinned block remains canonical and must still commit.
	advanced := make(chan error, 1)
	go func() {
		time.Sleep(3 * time.Second)
		next, e := newIntegrationBundle(integrationBundleOptions{Number: 2, ParentHash: f.original.Block.Hash(), ExtraData: []byte("ens-next-head")})
		if e == nil {
			_, e = f.core.CommitCanonicalSegment(t.Context(), "1", []chainbundle.Bundle{next})
		}
		advanced <- e
	}()
	started := time.Now()
	result, err := collector.Step(t.Context(), true)
	if e := <-advanced; e != nil {
		t.Fatal(e)
	}
	if err != nil || result.Scanned != 1 {
		t.Fatalf("bounded calls must commit after a longer collection round: %+v %v", result, err)
	}
	if time.Since(started) <= 8*time.Second {
		t.Fatal("regression did not exercise the former whole-round deadline")
	}
	assertRowCount(t, t.Context(), f.db, `SELECT count(*) FROM ens_sync_events`, 5)
}

func TestENSCollectorCurrentChainPinnedStateAndCoreGap(t *testing.T) {
	f := newENSCollectorFixture(t)
	ctx := t.Context()
	mainnet := f.genesis.Block.Header()
	state := &ensCollectorRPC{genesis: mainnet, block: f.original}
	server := rpc.NewServer()
	if e := server.RegisterName("eth", state); e != nil {
		t.Fatal(e)
	}
	client := rpc.DialInProc(server)
	t.Cleanup(client.Close)
	pool, e := ethrpc.NewPool([]ethrpc.Endpoint{{Name: "history", Client: client, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true}}}, ethrpc.PoolOptions{})
	if e != nil {
		t.Fatal(e)
	}
	m := f.manifest
	m.Genesis = mainnet.Hash()
	m.Start = 1
	m.StartHash = f.original.Block.Hash()
	m.Policy = testHash(151001)
	m.Deployments = append([]enssync.Deployment(nil), m.Deployments...)
	for i := range m.Deployments {
		m.Deployments[i].Number = 1
		m.Deployments[i].Hash = m.StartHash
	}
	repo, _ := enssync.NewRepository(f.db)
	collector, e := enssync.NewCollector(repo, m, pool, enssync.CollectorOptions{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = collector.Step(ctx, false); e != nil {
		t.Fatal(e)
	}
	if state.calls != 0 {
		t.Fatal("disabled collector called RPC")
	}
	r, e := collector.Step(ctx, true)
	if e != nil || r.Scanned != 1 {
		t.Fatalf("official %+v %v", r, e)
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_events`, 5)
	if state.logCalls != 0 {
		t.Fatal("ENS acquired logs independently of core sync")
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM pg_class WHERE oid IN ('ens_sync_blocks'::regclass,'ens_sync_events'::regclass) AND relkind='v'`, 2)
	if n, err := dbgen.New(f.db).PutENSSyncBlock(ctx, dbgen.PutENSSyncBlockParams{SourceID: r.SourceID, Number: ensNumber("2"), Hash: testHash(199999).Bytes(), ParentHash: f.original.Block.Hash().Bytes(), BlockTime: ensTime(time.Now()), Canonical: true}); err != nil || n != 0 {
		t.Fatalf("ENS fabricated a block: %d %v", n, err)
	}

	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_sync_candidates WHERE direction='forward' AND lookup_key='alice.test'`, 1)
	// A core coverage gap cannot be repaired by fetching logs from this RPC.
	execFixture(t, ctx, f.db, `DELETE FROM core_coverage_ranges WHERE chain_id=1`)
	if _, err := collector.Step(ctx, true); !errors.Is(err, enssync.ErrCoverageGap) {
		t.Fatalf("missing core coverage: %v", err)
	}
}
