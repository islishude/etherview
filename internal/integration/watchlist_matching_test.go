//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/chainbundle/testfixture"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/watchlist"
)

func TestWatchlistMatchingSkipsUnrelatedAddresses(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repo, _ := store.NewPostgresRepository(db)
	stamp := uint64(time.Now().Add(-time.Hour).Unix())
	genesis, _ := testfixture.New(testfixture.Options{Number: 0, Timestamp: stamp})
	commitCanonical(t, ctx, repo, genesis)
	// 400 accounts, each with 100 distinct unrelated addresses. The old join
	// visited 80 million pairs and exceeded the worker's ten-second budget.
	execFixture(t, ctx, db, `INSERT INTO users(id,chain_id,address,role,status,created_at,updated_at) SELECT md5('user'||i)::uuid,1,decode(lpad(to_hex(i),40,'0'),'hex'),'user','active',now(),now() FROM generate_series(1,400) AS i`)
	execFixture(t, ctx, db, `INSERT INTO address_watches(id,user_id,chain_id,address,kinds,direction,start_number,start_hash) SELECT md5('watch'||i)::uuid,md5('user'||((i-1)/100+1))::uuid,1,decode(lpad(to_hex(i+100),40,'0'),'hex'),ARRAY['transaction'],'both',0,$1 FROM generate_series(1,40000) AS i`, genesis.Block.Hash().Bytes())
	block, err := testfixture.New(testfixture.Options{Number: 1, ParentHash: genesis.Block.Hash(), Timestamp: stamp + 10, TransactionTypes: make([]uint8, 2000)})
	if err != nil {
		t.Fatal(err)
	}
	commitCanonical(t, ctx, repo, block)
	execFixture(t, ctx, db, `ANALYZE address_watches; ANALYZE users; ANALYZE transaction_inclusions; ANALYZE receipts; ANALYZE blocks; ANALYZE canonical_blocks`)
	service, _ := watchlist.New(db, 1)
	started := time.Now()
	pages := drainWatchlistWork(t, service, 30)
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM watch_notifications`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("unrelated notifications=%d", count)
	}
	t.Logf("40,000 unrelated watches / 2,000 transactions: %d pages, %s", pages, time.Since(started))
}

func TestWatchlistMatchingAdvancesPastIneligibleFollowers(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repo, _ := store.NewPostgresRepository(db)
	stamp := uint64(time.Now().Add(-time.Hour).Unix())
	genesis, _ := testfixture.New(testfixture.Options{Number: 0, Timestamp: stamp})
	commitCanonical(t, ctx, repo, genesis)
	execFixture(t, ctx, db, `INSERT INTO users(id,chain_id,address,role,status,created_at,updated_at) SELECT md5('user'||i)::uuid,1,decode(lpad(to_hex(i),40,'0'),'hex'),'user',CASE WHEN i<=100 THEN 'disabled' ELSE 'active' END,now(),now() FROM generate_series(1,700) AS i`)
	// The first 450 ordered candidates must advance the cursor without delivery.
	// The final 250 eligible followers require more than one publication page.
	execFixture(t, ctx, db, `INSERT INTO address_watches(id,user_id,chain_id,address,kinds,direction,start_number,start_hash) SELECT lpad(to_hex(i),32,'0')::uuid,md5('user'||i)::uuid,1,decode(lpad('2',40,'0'),'hex'),CASE WHEN i BETWEEN 101 AND 450 THEN ARRAY['erc20'] ELSE ARRAY['transaction'] END,'both',0,$1 FROM generate_series(1,700) AS i`, genesis.Block.Hash().Bytes())
	block, err := testfixture.New(testfixture.Options{Number: 1, ParentHash: genesis.Block.Hash(), Timestamp: stamp + 10, TransactionTypes: []uint8{types.LegacyTxType}})
	if err != nil {
		t.Fatal(err)
	}
	commitCanonical(t, ctx, repo, block)
	service, _ := watchlist.New(db, 1)
	if busy, err := service.ProcessOne(ctx); err != nil || !busy {
		t.Fatalf("initial partial page: busy=%t error=%v", busy, err)
	}
	// Resume the persisted cursor through a fresh consumer instance.
	service, _ = watchlist.New(db, 1)
	drainWatchlistWork(t, service, 10)
	var count, eligible int
	if err := db.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE watch_id>lpad(to_hex(450),32,'0')::uuid) FROM watch_notifications`).Scan(&count, &eligible); err != nil {
		t.Fatal(err)
	}
	if count != 250 || eligible != 250 {
		t.Fatalf("notifications=%d eligible=%d", count, eligible)
	}
	// A replay must preserve identities and read state across the same boundaries.
	execFixture(t, ctx, db, `UPDATE watch_notifications SET read_at=now()`)
	execFixture(t, ctx, db, `UPDATE watch_notification_work SET generation=generation+1,after_source='',after_watch='00000000-0000-0000-0000-000000000000'`)
	drainWatchlistWork(t, service, 10)
	if err := db.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE read_at IS NOT NULL) FROM watch_notifications`).Scan(&count, &eligible); err != nil {
		t.Fatal(err)
	}
	if count != 250 || eligible != 250 {
		t.Fatalf("replay notifications=%d read=%d", count, eligible)
	}
}

func drainWatchlistWork(t *testing.T, service *watchlist.Service, limit int) int {
	t.Helper()
	for page := range limit {
		work, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		busy, err := service.ProcessOne(work)
		cancel()
		if err != nil {
			t.Fatalf("notification page %d: %v", page, err)
		}
		if !busy {
			return page
		}
	}
	t.Fatal("notification progress did not finish")
	return 0
}
