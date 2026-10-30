//go:build integration

package integration_test

import (
	"errors"
	"testing"
	"time"

	goens "github.com/ensdomains/go-ens/v4"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/enscontract"
	"github.com/islishude/etherview/internal/ensread"
	"github.com/islishude/etherview/internal/enssync"
	"github.com/islishude/etherview/internal/store"
)

func TestENSHistoryGroupingPaginationAndReorg(t *testing.T) {
	f := newENSCollectorFixture(t)
	ctx := t.Context()
	result, e := f.collector.Step(ctx, true)
	if e != nil {
		t.Fatal(e)
	}
	repo, _ := enssync.NewRepository(f.db)
	if e = repo.RegisterPolicy(ctx, enssync.EffectivePolicy{TargetChainID: "1", CoinType: "60", Digest: f.manifest.Policy, SourceID: result.SourceID}); e != nil {
		t.Fatal(e)
	}
	reader, e := ensread.New(&ensReadOnlyDB{Pool: f.db}, ensread.Options{ChainID: 1, Policy: f.manifest.Policy, MaxBatch: 100, SnapshotTTL: time.Minute})
	if e != nil {
		t.Fatal(e)
	}
	request := enscontract.HistoryRequest{Name: "alice.test", Limit: 1}
	partial, _, e := reader.NameHistory(ctx, request)
	if e != nil || partial.Coverage.State != "partial" || len(partial.Items) != 0 {
		t.Fatalf("partial %+v %v", partial, e)
	}
	if _, e = f.collector.Step(ctx, true); e != nil {
		t.Fatal(e)
	}
	first, next, e := reader.NameHistory(ctx, request)
	if e != nil || next != "" || len(first.Items) != 1 || first.Items[0].EventCount != "5" || first.Coverage.State != "complete" {
		t.Fatalf("history %+v %s %v", first, next, e)
	}
	item := first.Items[0]
	eventRequest := enscontract.HistoryEventsRequest{Name: request.Name, TransactionHash: item.TransactionHash, BlockHash: item.BlockHash, Snapshot: first.Snapshot, Limit: 2}
	indexes := []string{}
	for {
		page, cursor, e := reader.NameHistoryEvents(ctx, eventRequest)
		if e != nil {
			t.Fatal(e)
		}
		for _, event := range page.Items {
			indexes = append(indexes, event.LogIndex)
			if event.Emitter == "" || len(event.Fields) == 0 {
				t.Fatal("missing provenance")
			}
		}
		if cursor == "" {
			break
		}
		eventRequest.Cursor = cursor
	}
	if len(indexes) != 5 || indexes[0] != "0" || indexes[4] != "4" {
		t.Fatal(indexes)
	}
	wrong := eventRequest
	wrong.Name = "bob.test"
	if _, _, e = reader.NameHistoryEvents(ctx, wrong); !errors.Is(e, enscontract.ErrSnapshotInvalid) {
		t.Fatalf("cross name cursor: %v", e)
	}
	// A new block preserves a frozen traversal; its history groups are not mixed in.
	resolver := f.manifest.Deployments[1].Address
	// Deployment ordering is explicit in this fixture (controller, resolver).
	extra, e := newIntegrationBundle(integrationBundleOptions{Number: 2, ParentHash: f.original.Block.Hash(), ExtraData: []byte("history-next"), Transactions: []integrationTransactionOptions{{To: &resolver, Logs: []*types.Log{ensCollectorLog(t, "resolver", "AddrChanged", resolver, func() common.Hash { n, _ := goens.NameHash("alice.test"); return common.Hash(n) }(), f.owner)}}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.core.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{extra}); e != nil {
		t.Fatal(e)
	}
	lag, _, e := reader.NameHistory(ctx, request)
	if e != nil || lag.Coverage.State != "partial" {
		t.Fatalf("core lag %+v %v", lag, e)
	}
	if _, e = f.collector.Step(ctx, true); e != nil {
		t.Fatal(e)
	}
	latest, cursor, err := reader.NameHistory(ctx, request)
	if err != nil || len(latest.Items) != 1 || latest.Items[0].BlockNumber != "2" || cursor == "" {
		t.Fatalf("new transaction page %+v %s %v", latest, cursor, err)
	}
	older, tail, err := reader.NameHistory(ctx, enscontract.HistoryRequest{Name: request.Name, Limit: 1, Cursor: cursor})
	if err != nil || len(older.Items) != 1 || older.Items[0].BlockNumber != "1" || tail != "" {
		t.Fatalf("older page %+v %s %v", older, tail, err)
	}
	eventRequest.Cursor = ""
	if _, _, e = reader.NameHistoryEvents(ctx, eventRequest); e != nil {
		t.Fatalf("new head invalidated snapshot %v", e)
	}
	// Invalidation is checked against core canonicality before the ENS collector runs.
	replacement := testBundle(1, testHash(159030), f.genesis.Block.Hash(), testHash(159031), "history-replacement")
	if e = f.core.ApplyReorg(ctx, "1", store.Reorg{Ancestor: mustBlockRef(t, f.genesis), Detached: []store.BlockRef{mustBlockRef(t, extra), mustBlockRef(t, f.original)}, Attached: []chainbundle.Bundle{replacement}, Checkpoint: store.NewCoreCheckpoint(mustBlockRef(t, replacement)), Reason: "history test"}); e != nil {
		t.Fatal(e)
	}
	if _, _, e = reader.NameHistoryEvents(ctx, eventRequest); !errors.Is(e, enscontract.ErrSnapshotInvalid) {
		t.Fatalf("orphan snapshot survived %v", e)
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM ens_history_events`, 6)
	current, _, e := reader.NameHistory(ctx, request)
	if e != nil || len(current.Items) != 0 {
		t.Fatalf("orphan history visible %+v %v", current, e)
	}
	unknown, _, e := reader.NameHistory(ctx, enscontract.HistoryRequest{Name: "unknown.test", Limit: 25})
	if e != nil || unknown.State != "not_indexed" {
		t.Fatal(unknown, e)
	}
}
