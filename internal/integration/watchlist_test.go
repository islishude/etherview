//go:build integration

package integration_test

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/enrich"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/chainbundle/testfixture"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/watchlist"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestWatchlistDurableDeliveryReorgAndExport(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repo, e := store.NewPostgresRepository(db)
	if e != nil {
		t.Fatal(e)
	}
	timestamp := uint64(time.Now().Add(-time.Hour).Unix())
	genesis, e := testfixture.New(testfixture.Options{Number: 0, Timestamp: timestamp})
	if e != nil {
		t.Fatal(e)
	}
	commitCanonical(t, ctx, repo, genesis)
	user, other := uuid.NewString(), uuid.NewString()
	execFixture(t, ctx, db, `INSERT INTO users(id,chain_id,address,role,status,created_at,updated_at) VALUES ($1,1,decode(repeat('11',20),'hex'),'user','active',now(),now()),($2,1,decode(repeat('22',20),'hex'),'user','active',now(),now())`, user, other)
	service, e := watchlist.New(db, 1)
	if e != nil {
		t.Fatal(e)
	}
	address := common.Address{19: 2}.Hex()
	input := gen.WatchInput{Address: address, Label: "Private", Kinds: []gen.WatchInputKinds{"transaction", "erc20", "erc721", "erc1155"}, Direction: "both", Enabled: true}
	watch, e := service.Save(ctx, user, "", input)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = service.Save(ctx, user, "", input); !errors.Is(e, watchlist.ErrDuplicate) {
		t.Fatalf("duplicate: %v", e)
	}
	if e = service.Delete(ctx, other, watch.Id); !errors.Is(e, watchlist.ErrNotFound) {
		t.Fatalf("cross-user delete: %v", e)
	}
	block, e := testfixture.New(testfixture.Options{Number: 1, ParentHash: genesis.Block.Hash(), Timestamp: timestamp + 10, TransactionTypes: []uint8{types.LegacyTxType}, FailedTransactions: []bool{true}})
	if e != nil {
		t.Fatal(e)
	}
	commitCanonical(t, ctx, repo, block)
	// The durable task survives loss of the bounded public event ledger and a new service instance.
	execFixture(t, ctx, db, `DELETE FROM runtime_events`)
	service, e = watchlist.New(db, 1)
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() { _, e := service.ProcessOne(context.Background()); errs <- e })
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	page, e := service.Notifications(ctx, user, "", false)
	if e != nil || len(page.Items) != 1 || page.UnreadCount != "1" || *page.Items[0].Activity.Status != "failed" {
		t.Fatalf("page=%+v err=%v", page, e)
	}
	first := page.Items[0].Id
	if e = service.Read(ctx, other, first, false); !errors.Is(e, watchlist.ErrNotFound) {
		t.Fatalf("cross-user read: %v", e)
	}
	if e = service.Read(ctx, user, page.Watermark, true); e != nil {
		t.Fatal(e)
	}
	ancestor := mustBlockRef(t, genesis)
	old := mustBlockRef(t, block)
	replacement, e := testfixture.New(testfixture.Options{Number: 1, ParentHash: genesis.Block.Hash(), Timestamp: timestamp + 10, ExtraData: []byte("replacement")})
	if e != nil {
		t.Fatal(e)
	}
	replacementRef := mustBlockRef(t, replacement)
	if e = repo.ApplyReorg(ctx, "1", store.Reorg{Ancestor: ancestor, Detached: []store.BlockRef{old}, Attached: []chainbundle.Bundle{replacement}, Checkpoint: store.NewCoreCheckpoint(replacementRef), Reason: "test"}); e != nil {
		t.Fatal(e)
	}
	page, e = service.Notifications(ctx, user, "", false)
	if e != nil || len(page.Items) != 1 || page.Items[0].Canonical {
		t.Fatalf("reorg read=%+v %v", page, e)
	}
	if e = repo.ApplyReorg(ctx, "1", store.Reorg{Ancestor: ancestor, Detached: []store.BlockRef{replacementRef}, Attached: []chainbundle.Bundle{block}, Checkpoint: store.NewCoreCheckpoint(old), Reason: "reattach"}); e != nil {
		t.Fatal(e)
	}
	for range 4 {
		if _, e = service.ProcessOne(ctx); e != nil {
			t.Fatal(e)
		}
	}
	page, e = service.Notifications(ctx, user, "", false)
	if e != nil || len(page.Items) != 1 || page.Items[0].Id != first || !page.Items[0].Read || !page.Items[0].Canonical {
		t.Fatalf("reattach=%+v %v", page, e)
	}
	execFixture(t, ctx, db, `INSERT INTO core_coverage_ranges(chain_id,range_start,range_end) VALUES(1,0,1)`)
	inputExport := gen.AddressExportRequest{Address: address, Kind: "transaction", Direction: "both", From: time.Unix(int64(timestamp), 0), To: time.Unix(int64(timestamp+20), 0)}
	result, e := service.Export(ctx, user, inputExport)
	if e != nil {
		t.Fatal(e)
	}
	records, e := csv.NewReader(strings.NewReader(string(result.Bytes))).ReadAll()
	if e != nil || len(records) != 2 || records[1][13] != "failed" {
		t.Fatalf("csv=%q %v", result.Bytes, e)
	}
	inputExport.Kind = "erc20"
	if _, e = service.Export(ctx, user, inputExport); !errors.Is(e, watchlist.ErrUnavailable) {
		t.Fatalf("unpublished token export: %v", e)
	}
	if e = service.Delete(ctx, user, watch.Id); e != nil {
		t.Fatal(e)
	}
	page, e = service.Notifications(ctx, user, "", false)
	if e != nil || len(page.Items) != 1 {
		t.Fatalf("deleted watch history=%+v %v", page, e)
	}
}
func TestWatchlistConcurrentLimit(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repo, _ := store.NewPostgresRepository(db)
	genesis, _ := testfixture.New(testfixture.Options{Number: 0})
	commitCanonical(t, ctx, repo, genesis)
	user := uuid.NewString()
	execFixture(t, ctx, db, `INSERT INTO users(id,chain_id,address,role,status,created_at,updated_at) VALUES($1,1,decode(repeat('33',20),'hex'),'user','active',now(),now())`, user)
	service, _ := watchlist.New(db, 1)
	for i := 1; i <= 99; i++ {
		_, e := service.Save(ctx, user, "", gen.WatchInput{Address: fmt.Sprintf("0x%040x", i), Kinds: []gen.WatchInputKinds{"transaction"}, Direction: "both", Enabled: true})
		if e != nil {
			t.Fatal(e)
		}
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 100; i <= 101; i++ {
		wg.Go(func() {
			_, e := service.Save(ctx, user, "", gen.WatchInput{Address: fmt.Sprintf("0x%040x", i), Kinds: []gen.WatchInputKinds{"transaction"}, Direction: "both", Enabled: true})
			results <- e
		})
	}
	wg.Wait()
	close(results)
	good, limited := 0, 0
	for e := range results {
		if e == nil {
			good++
		} else if errors.Is(e, watchlist.ErrLimit) {
			limited++
		} else {
			t.Fatal(e)
		}
	}
	if good != 1 || limited != 1 {
		t.Fatalf("good=%d limited=%d", good, limited)
	}
}

func TestWatchlistDelayedTokenPublicationAndReplay(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("disabled=%t", disabled), func(t *testing.T) { testWatchlistTokenReplay(t, disabled) })
	}
}

func testWatchlistTokenReplay(t *testing.T, disabled bool) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repo, _ := store.NewPostgresRepository(db)
	stamp := uint64(time.Now().Add(-time.Hour).Unix())
	genesis, _ := testfixture.New(testfixture.Options{Number: 0, Timestamp: stamp})
	commitCanonical(t, ctx, repo, genesis)
	user := uuid.NewString()
	execFixture(t, ctx, db, `INSERT INTO users(id,chain_id,address,role,status,created_at,updated_at) VALUES($1,1,decode(repeat('44',20),'hex'),'user','active',now(),now())`, user)
	service, _ := watchlist.New(db, 1)
	watched := common.Address{19: 2}
	zero := common.Address{}
	_, err := service.Save(ctx, user, "", gen.WatchInput{Address: watched.Hex(), Kinds: []gen.WatchInputKinds{"transaction", "erc20", "erc721", "erc1155"}, Direction: "both", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	word := func(n int64) []byte { return common.LeftPadBytes(big.NewInt(n).Bytes(), 32) }
	topics := func(signature string, addresses ...common.Address) []common.Hash {
		out := []common.Hash{crypto.Keccak256Hash([]byte(signature))}
		for _, a := range addresses {
			out = append(out, common.BytesToHash(a.Bytes()))
		}
		return out
	}
	logs := []*types.Log{
		{Address: common.Address{19: 20}, Topics: topics("Transfer(address,address,uint256)", zero, watched), Data: word(123)},
		{Address: common.Address{19: 21}, Topics: append(topics("Transfer(address,address,uint256)", watched, watched), common.BytesToHash(word(7)))},
		{Address: common.Address{19: 22}, Topics: topics("TransferSingle(address,address,address,uint256,uint256)", watched, watched, zero), Data: append(word(8), word(9)...)},
	}
	array, _ := abi.NewType("uint256[]", "", nil)
	batch, err := (abi.Arguments{{Type: array}, {Type: array}}).Pack([]*big.Int{big.NewInt(1), big.NewInt(2)}, []*big.Int{big.NewInt(3), big.NewInt(4)})
	if err != nil {
		t.Fatal(err)
	}
	logs = append(logs, &types.Log{Address: common.Address{19: 22}, Topics: topics("TransferBatch(address,address,address,uint256[],uint256[])", watched, zero, watched), Data: batch})
	block, err := newIntegrationBundle(integrationBundleOptions{Number: 1, ParentHash: genesis.Block.Hash(), Timestamp: stamp + 10, Transactions: []integrationTransactionOptions{{Type: types.DynamicFeeTxType, To: &watched, Logs: logs}}})
	if err != nil {
		t.Fatal(err)
	}
	commitCanonical(t, ctx, repo, block)
	if _, err = service.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := service.Notifications(ctx, user, "", false)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("before publication=%+v %v", page, err)
	}
	markTokenStageComplete(t, ctx, db, block)
	if _, err = service.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	page, err = service.Notifications(ctx, user, "", false)
	if err != nil || len(page.Items) != 6 {
		t.Fatalf("published=%+v %v", page, err)
	}
	kinds := map[string]int{}
	for _, n := range page.Items {
		kinds[n.Activity.Kind]++
		if !n.Published {
			t.Fatal("unpublished result")
		}
	}
	if kinds["erc20"] != 1 || kinds["erc721"] != 1 || kinds["erc1155"] != 3 {
		t.Fatalf("kinds=%v", kinds)
	}
	if err = service.Read(ctx, user, page.Watermark, true); err != nil {
		t.Fatal(err)
	}
	if disabled {
		execFixture(t, ctx, db, `UPDATE users SET status='disabled' WHERE id=$1`, user)
	}
	queue, _ := enrich.NewPostgresJobQueue(db)
	hash, _ := enrich.ParseWord(block.Block.Hash().Hex())
	if _, err = queue.Enqueue(ctx, enrich.EnqueueRequest{Stage: enrich.TokenStage, ChainID: "1", BlockHash: hash, BlockNumber: 1, Replay: enrich.ReplaySource{Kind: "integration", Key: "watch-token-replay"}}); err != nil {
		t.Fatal(err)
	}
	page, err = service.Notifications(ctx, user, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range page.Items {
		if n.Activity.Kind != "transaction" && n.Published {
			t.Fatal("old generation visible")
		}
	}
	markTokenStageComplete(t, ctx, db, block)
	if _, err = service.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if disabled {
		if err := service.Cleanup(ctx); err != nil {
			t.Fatal(err)
		}
		execFixture(t, ctx, db, `UPDATE users SET status='active' WHERE id=$1`, user)
		if _, err := service.ProcessOne(ctx); err != nil {
			t.Fatal(err)
		}
	}
	page, err = service.Notifications(ctx, user, "", false)
	if err != nil || len(page.Items) != 6 || page.UnreadCount != "0" {
		t.Fatalf("replay=%+v %v", page, err)
	}
	// Notifications arriving after the read watermark remain unread; disabling and
	// re-enabling preserves existing history but excludes activity during the pause.
	for _, n := range page.Items {
		if !n.Published || !n.Read {
			t.Fatalf("replay state=%+v", n)
		}
	}
	execFixture(t, ctx, db, `INSERT INTO core_coverage_ranges(chain_id,range_start,range_end) VALUES(1,0,1)`)
	markTokenStageComplete(t, ctx, db, genesis)
	exported, err := service.Export(ctx, user, gen.AddressExportRequest{Address: watched.Hex(), Kind: "nft", Direction: "both", From: time.Unix(int64(stamp), 0), To: time.Unix(int64(stamp+20), 0)})
	if err != nil {
		t.Fatal(err)
	}
	csvRows, err := csv.NewReader(strings.NewReader(string(exported.Bytes))).ReadAll()
	if err != nil || len(csvRows) != 5 {
		t.Fatalf("NFT export rows=%d err=%v", len(csvRows), err)
	}
}

func TestWatchlistLeaseRecoveryWatermarkAndCSVBounds(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repo, _ := store.NewPostgresRepository(db)
	stamp := uint64(time.Now().Add(-time.Hour).Unix())
	genesis, _ := testfixture.New(testfixture.Options{Number: 0, Timestamp: stamp})
	commitCanonical(t, ctx, repo, genesis)
	user := uuid.NewString()
	execFixture(t, ctx, db, `INSERT INTO users(id,chain_id,address,role,status,created_at,updated_at) VALUES($1,1,decode(repeat('55',20),'hex'),'user','active',now(),now())`, user)
	service, _ := watchlist.New(db, 1)
	address := common.Address{19: 2}.Hex()
	input := gen.WatchInput{Address: address, Kinds: []gen.WatchInputKinds{"transaction"}, Direction: "both", Enabled: true}
	watch, err := service.Save(ctx, user, "", input)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := testfixture.New(testfixture.Options{Number: 1, ParentHash: genesis.Block.Hash(), Timestamp: stamp + 10, TransactionTypes: []uint8{types.LegacyTxType}})
	commitCanonical(t, ctx, repo, first)
	execFixture(t, ctx, db, `UPDATE watch_notification_work SET lease_token=$1,lease_until=now()+interval '1 minute'`, uuid.NewString())
	if found, err := service.ProcessOne(ctx); err != nil || found {
		t.Fatalf("stole live lease: %t %v", found, err)
	}
	execFixture(t, ctx, db, `UPDATE watch_notification_work SET lease_until=now()-interval '1 minute'`)
	if found, err := service.ProcessOne(ctx); err != nil || !found {
		t.Fatalf("expired lease not recovered: %t %v", found, err)
	}
	page, err := service.Notifications(ctx, user, "", false)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("initial: %+v %v", page, err)
	}
	second, _ := testfixture.New(testfixture.Options{Number: 2, ParentHash: first.Block.Hash(), Timestamp: stamp + 20, TransactionTypes: []uint8{types.LegacyTxType}})
	commitCanonical(t, ctx, repo, second)
	if _, err := service.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.Read(ctx, user, page.Watermark, true); err != nil {
		t.Fatal(err)
	}
	page, err = service.Notifications(ctx, user, "", true)
	if err != nil || page.UnreadCount != "1" || len(page.Items) != 1 {
		t.Fatalf("watermark: %+v %v", page, err)
	}
	input.Enabled = false
	if _, err = service.Save(ctx, user, watch.Id, input); err != nil {
		t.Fatal(err)
	}
	third, _ := testfixture.New(testfixture.Options{Number: 3, ParentHash: second.Block.Hash(), Timestamp: stamp + 30})
	commitCanonical(t, ctx, repo, third)
	input.Enabled = true
	watch, err = service.Save(ctx, user, watch.Id, input)
	if err != nil || watch.StartNumber != "3" {
		t.Fatalf("resume: %+v %v", watch, err)
	}
	// Export boundary fixture: normalized transaction/receipt facts deliberately
	// synthesized to exercise 10,000 vs 10,001 without RPC or float conversion.
	execFixture(t, ctx, db, `INSERT INTO core_coverage_ranges(chain_id,range_start,range_end) VALUES(1,0,3)`)
	execFixture(t, ctx, db, `INSERT INTO transactions(chain_id,hash,raw) SELECT 1,decode(md5(i::text)||md5(i::text),'hex'),'{}'::jsonb FROM generate_series(1,9999) AS i`)
	execFixture(t, ctx, db, `INSERT INTO transaction_inclusions(chain_id,block_number,block_hash,tx_index,tx_hash,raw) SELECT 1,1,$1,i,decode(md5(i::text)||md5(i::text),'hex'),jsonb_build_object('from',$2::text,'to',$2::text,'value','0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff') FROM generate_series(1,9999) AS i`, first.Block.Hash().Bytes(), strings.ToLower(address))
	execFixture(t, ctx, db, `INSERT INTO receipts(chain_id,block_number,block_hash,tx_index,tx_hash,raw) SELECT chain_id,block_number,block_hash,tx_index,tx_hash,'{"status":"0x1"}'::jsonb FROM transaction_inclusions WHERE block_number=1 AND tx_index>0`)
	request := gen.AddressExportRequest{Address: address, Kind: "transaction", Direction: "both", From: time.Unix(int64(stamp-20), 0), To: time.Unix(int64(stamp+11), 0)}
	exported, err := service.Export(ctx, user, request)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(exported.Bytes))).ReadAll()
	if err != nil || len(rows) != 10001 {
		t.Fatalf("export rows=%d %v", len(rows), err)
	}
	if !strings.Contains(string(exported.Bytes), "115792089237316195423570985008687907853269984665640564039457584007913129639935") {
		t.Fatal("uint256 lost precision")
	}
	request.To = time.Unix(int64(stamp+21), 0)
	if out, err := service.Export(ctx, user, request); !errors.Is(err, watchlist.ErrExportLimit) || len(out.Bytes) != 0 {
		t.Fatalf("10001 result bytes=%d error=%v", len(out.Bytes), err)
	}
	request.From = request.To
	request.To = request.From.Add(time.Minute)
	if out, err := service.Export(ctx, user, request); err != nil || strings.Count(string(out.Bytes), "\n") != 1 {
		t.Fatalf("empty export=%q %v", out.Bytes, err)
	}
	request.From = time.Unix(int64(stamp+10), 500)
	request.To = time.Unix(int64(stamp+11), 0)
	if out, err := service.Export(ctx, user, request); err != nil || strings.Count(string(out.Bytes), "\n") != 1 {
		t.Fatalf("fractional date boundary=%q %v", out.Bytes, err)
	}
	request.To = request.From.Add(32 * 24 * time.Hour)
	if _, err = service.Export(ctx, user, request); !errors.Is(err, watchlist.ErrInvalid) {
		t.Fatalf("32 day export: %v", err)
	}
}

func TestWatchlistExportAdmissionAcrossReplicas(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repo, _ := store.NewPostgresRepository(db)
	genesis, _ := testfixture.New(testfixture.Options{Number: 0})
	commitCanonical(t, ctx, repo, genesis)
	users := make([]pgtype.UUID, 5)
	for i := range users {
		users[i] = pgtype.UUID{Bytes: uuid.New(), Valid: true}
		execFixture(t, ctx, db, `INSERT INTO users(id,chain_id,address,role,status,created_at,updated_at) VALUES($1,1,$2,'user','active',now(),now())`, users[i], common.Address{19: byte(i + 1)}.Bytes())
	}
	var admitted int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, owner := range users {
		wg.Go(func() {
			e := dbaccess.WithTransaction(ctx, db, func(q *dbgen.Queries) error {
				if e := q.ExportLock(ctx); e != nil {
					return e
				}
				n, e := q.ExportAdmit(ctx, pgtype.UUID{Bytes: uuid.New(), Valid: true}, owner)
				mu.Lock()
				admitted += int(n)
				mu.Unlock()
				return e
			})
			if e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	if admitted != 4 {
		t.Fatalf("admitted=%d", admitted)
	}
	execFixture(t, ctx, db, `UPDATE address_export_admissions SET expires_at=now()-interval '1 second'`)
	q := dbgen.New(db)
	for i := range 5 {
		token := pgtype.UUID{Bytes: uuid.New(), Valid: true}
		n, e := q.ExportAdmit(ctx, token, users[4])
		if e != nil {
			t.Fatal(e)
		}
		if n == 0 {
			if i < 4 {
				t.Fatalf("premature limit at %d", i)
			}
			break
		}
		if e = q.ExportRelease(ctx, token); e != nil {
			t.Fatal(e)
		}
	}
	if n, e := q.ExportAdmit(ctx, pgtype.UUID{Bytes: uuid.New(), Valid: true}, users[4]); e != nil || n != 0 {
		t.Fatalf("rolling rate: n=%d err=%v", n, e)
	}
}
