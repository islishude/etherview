//go:build integration

package integration_test

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/chainbundle/testfixture"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/watchlist"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPushSubscriptionsOwnershipLimitAndExpiry(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repo, _ := store.NewPostgresRepository(db)
	genesis, _ := testfixture.New(testfixture.Options{Number: 0})
	commitCanonical(t, ctx, repo, genesis)
	user, other, session, otherSession := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	execFixture(t, ctx, db, `INSERT INTO users(id,chain_id,address,role,status,created_at,updated_at) VALUES($1,1,decode(repeat('11',20),'hex'),'user','active',now(),now()),($2,1,decode(repeat('22',20),'hex'),'user','active',now(),now())`, user, other)
	for i, entry := range [][2]string{{user, session}, {other, otherSession}} {
		execFixture(t, ctx, db, `INSERT INTO user_sessions(id,user_id,token_digest,csrf_digest,created_at,expires_at,last_used_at) VALUES($1,$2,$3,$4,now(),now()+interval '1 hour',now())`, entry[1], entry[0], []byte(strings.Repeat(fmt.Sprint(i), 32)), []byte(strings.Repeat("c", 32)))
	}
	key, _ := ecdh.P256().GenerateKey(rand.Reader)
	input := gen.PushSubscriptionInput{Endpoint: "https://push.example/first", P256dh: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), Auth: base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("a", 16))), Language: "en"}
	service, _ := watchlist.New(db, 1, watchlist.PushOptions{Enabled: true})
	first, e := service.SavePushSubscription(ctx, user, session, input)
	if e != nil {
		t.Fatal(e)
	}
	again, e := service.SavePushSubscription(ctx, user, session, input)
	if e != nil || again.Id != first.Id {
		t.Fatal("registration not idempotent", e)
	}
	if e = service.DeletePushSubscription(ctx, other, otherSession, first.Id); !errors.Is(e, watchlist.ErrNotFound) {
		t.Fatal("cross-user delete", e)
	}
	if _, e = service.SavePushSubscription(ctx, user, otherSession, input); !errors.Is(e, watchlist.ErrNotFound) {
		t.Fatal("cross-session save", e)
	}
	listed, e := service.PushSubscriptions(ctx, user, session)
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(listed)
	if strings.Contains(string(encoded), input.Endpoint) || strings.Contains(string(encoded), input.Auth) || strings.Contains(string(encoded), input.P256dh) {
		t.Fatal("subscription response leaks endpoint/keys")
	}
	for i := 1; i < 10; i++ {
		input.Endpoint = fmt.Sprintf("https://push.example/%d", i)
		if _, e = service.SavePushSubscription(ctx, user, session, input); e != nil {
			t.Fatal(e)
		}
	}
	input.Endpoint = "https://push.example/overflow"
	if _, e = service.SavePushSubscription(ctx, user, session, input); !errors.Is(e, watchlist.ErrPushLimit) {
		t.Fatal("subscription limit", e)
	}
	execFixture(t, ctx, db, `UPDATE user_sessions SET revoked_at=now() WHERE id=$1`, session)
	if _, e = service.PushSubscriptions(ctx, user, session); !errors.Is(e, watchlist.ErrNotFound) {
		t.Fatal("revoked session reads", e)
	}
	if e = dbgen.New(db).PushCleanup(ctx); e != nil {
		t.Fatal(e)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM push_subscriptions`, 0)
	input.Endpoint = "https://push.example/other"
	if _, e = service.SavePushSubscription(ctx, other, otherSession, input); e != nil {
		t.Fatal(e)
	}
	execFixture(t, ctx, db, `DELETE FROM user_sessions WHERE id=$1`, otherSession)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM push_subscriptions`, 0)
}
func TestPushSingleSenderFailureAndRevocation(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repo, _ := store.NewPostgresRepository(db)
	stamp := uint64(time.Now().Unix())
	genesis, _ := testfixture.New(testfixture.Options{Number: 0, Timestamp: stamp})
	commitCanonical(t, ctx, repo, genesis)
	user := uuid.NewString()
	execFixture(t, ctx, db, `INSERT INTO users(id,chain_id,address,role,status,created_at,updated_at) VALUES($1,1,decode(repeat('11',20),'hex'),'user','active',now(),now())`, user)
	service, _ := watchlist.New(db, 1)
	_, e := service.Save(ctx, user, "", gen.WatchInput{Address: "0x0000000000000000000000000000000000000002", Label: "Private label", Kinds: []gen.WatchInputKinds{"transaction"}, Direction: "both", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	sender := startPushWorker(t, db, user)
	standby, _ := watchlist.NewPushWorker(db, 1, sender, nil)
	standbyCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = standby.Run(standbyCtx) }()
	t.Cleanup(func() { cancel(); <-done })
	parent := genesis.Block.Hash()
	for n := uint64(1); n <= 3; n++ {
		if n == 2 {
			sender.mu.Lock()
			sender.status = 503
			sender.mu.Unlock()
		}
		if n == 3 {
			execFixture(t, ctx, db, `UPDATE user_sessions SET revoked_at=now() WHERE user_id=$1`, user)
		}
		block, _ := testfixture.New(testfixture.Options{Number: n, ParentHash: parent, Timestamp: stamp + n, TransactionTypes: []uint8{types.LegacyTxType}})
		commitCanonical(t, ctx, repo, block)
		parent = block.Block.Hash()
		if n < 3 {
			waitPushCount(t, sender, int(n))
		}
		assertPushQuiet(t, sender, min(int(n), 2))
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	var payload map[string]any
	if e = json.Unmarshal(sender.payloads[0], &payload); e != nil {
		t.Fatal(e)
	}
	if payload["body"] != "Private label · transaction · in" {
		t.Fatalf("unexpected summary: %v", payload)
	}
	for _, key := range []string{"amount", "value", "activity"} {
		if _, ok := payload[key]; ok {
			t.Fatal("unexpected financial detail")
		}
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM pg_class WHERE relnamespace=current_schema()::regnamespace AND relname IN ('watch_notifications','watch_notification_work')`, 0)
}
func TestPushFollowerKeysetBounds(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repo, _ := store.NewPostgresRepository(db)
	genesis, _ := testfixture.New(testfixture.Options{Number: 0})
	commitCanonical(t, ctx, repo, genesis)
	execFixture(t, ctx, db, `INSERT INTO users(id,chain_id,address,role,status,created_at,updated_at) SELECT md5('user'||i)::uuid,1,decode(lpad(to_hex(i),40,'0'),'hex'),'user','active',now(),now() FROM generate_series(1,1100) i`)
	execFixture(t, ctx, db, `INSERT INTO address_watches(id,user_id,chain_id,address,kinds,direction,start_number,start_hash) SELECT md5('unrelated'||i)::uuid,md5('user'||((i-1)/100+1))::uuid,1,decode(lpad(to_hex(i+100),40,'0'),'hex'),ARRAY['transaction'],'both',0,$1 FROM generate_series(1,40000) i`, genesis.Block.Hash().Bytes())
	execFixture(t, ctx, db, `INSERT INTO address_watches(id,user_id,chain_id,address,kinds,direction,start_number,start_hash) SELECT lpad(to_hex(i),32,'0')::uuid,md5('user'||(i+400))::uuid,1,decode(lpad('2',40,'0'),'hex'),ARRAY['erc20'],'both',0,$1 FROM generate_series(1,700) i`, genesis.Block.Hash().Bytes())
	q := dbgen.New(db)
	numeric := pgtype.Numeric{}
	_ = numeric.Scan("1")
	address := make([]byte, 20)
	address[19] = 2
	cursor := pgtype.UUID{Valid: true}
	count := 0
	for range 5 {
		rows, e := q.PushFollowers(ctx, dbgen.PushFollowersParams{ChainID: numeric, FromAddress: address, ToAddress: address, AfterWatch: cursor})
		if e != nil {
			t.Fatal(e)
		}
		if len(rows) > 200 {
			t.Fatal("unbounded follower page")
		}
		count += len(rows)
		if len(rows) < 200 {
			break
		}
		cursor = rows[len(rows)-1].ID
	}
	if count != 700 {
		t.Fatalf("self-transfer candidates=%d want700", count)
	}
}

func TestPushRestartDropsBacklogAndGoneEndpoint(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repo, _ := store.NewPostgresRepository(db)
	stamp := uint64(time.Now().Unix())
	genesis, _ := testfixture.New(testfixture.Options{Number: 0, Timestamp: stamp})
	commitCanonical(t, ctx, repo, genesis)
	user := uuid.NewString()
	execFixture(t, ctx, db, `INSERT INTO users(id,chain_id,address,role,status,created_at,updated_at) VALUES($1,1,decode(repeat('11',20),'hex'),'user','active',now(),now())`, user)
	service, _ := watchlist.New(db, 1)
	_, e := service.Save(ctx, user, "", gen.WatchInput{Address: "0x0000000000000000000000000000000000000002", Kinds: []gen.WatchInputKinds{"transaction"}, Direction: "both", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	sender := startPushWorker(t, db, user)
	first, _ := testfixture.New(testfixture.Options{Number: 1, ParentHash: genesis.Block.Hash(), Timestamp: stamp + 1, TransactionTypes: []uint8{types.LegacyTxType}})
	commitCanonical(t, ctx, repo, first)
	waitPushCount(t, sender, 1)
	sender.stop()
	second, _ := testfixture.New(testfixture.Options{Number: 2, ParentHash: first.Block.Hash(), Timestamp: stamp + 2, TransactionTypes: []uint8{types.LegacyTxType}})
	commitCanonical(t, ctx, repo, second)
	runPushWorker(t, db, sender)
	assertPushQuiet(t, sender, 1)
	sender.mu.Lock()
	sender.status = 410
	sender.mu.Unlock()
	third, _ := testfixture.New(testfixture.Options{Number: 3, ParentHash: second.Block.Hash(), Timestamp: stamp + 3, TransactionTypes: []uint8{types.LegacyTxType}})
	commitCanonical(t, ctx, repo, third)
	waitPushCount(t, sender, 2)
	assertPushQuiet(t, sender, 2)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM push_subscriptions`, 0)
	fourth, _ := testfixture.New(testfixture.Options{Number: 4, ParentHash: third.Block.Hash(), Timestamp: stamp + 4, TransactionTypes: []uint8{types.LegacyTxType}})
	commitCanonical(t, ctx, repo, fourth)
	assertPushQuiet(t, sender, 2)
}

func TestPushLosingLeaderCancelsSendWithoutSnapshot(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repo, _ := store.NewPostgresRepository(db)
	genesis, _ := testfixture.New(testfixture.Options{Number: 0})
	commitCanonical(t, ctx, repo, genesis)
	user := uuid.NewString()
	execFixture(t, ctx, db, `INSERT INTO users(id,chain_id,address,role,status,created_at,updated_at) VALUES($1,1,decode(repeat('11',20),'hex'),'user','active',now(),now())`, user)
	service, _ := watchlist.New(db, 1)
	if _, e := service.Save(ctx, user, "", gen.WatchInput{Address: "0x0000000000000000000000000000000000000002", Kinds: []gen.WatchInputKinds{"transaction"}, Direction: "both", Enabled: true}); e != nil {
		t.Fatal(e)
	}
	sender := startPushWorker(t, db, user)
	sender.stop()
	started, cancelled := make(chan struct{}), make(chan struct{})
	sender.onSend = func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return ctx.Err()
	}
	workerConfig := db.Config()
	workerName := "push-snapshot-" + uuid.NewString()
	workerConfig.ConnConfig.RuntimeParams["application_name"] = workerName
	workerDB, err := pgxpool.NewWithConfig(ctx, workerConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(workerDB.Close)
	runPushWorker(t, workerDB, sender)
	block, _ := testfixture.New(testfixture.Options{Number: 1, ParentHash: genesis.Block.Hash(), TransactionTypes: []uint8{types.LegacyTxType}})
	commitCanonical(t, ctx, repo, block)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("send did not start")
	}
	// Other packages share this database with isolated schemas. Hold an unrelated
	// snapshot to prove that it cannot contaminate the worker-only assertion.
	unrelated, err := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unrelated.Rollback(ctx) }()
	if _, err := unrelated.Exec(ctx, `SELECT count(*) FROM users`); err != nil {
		t.Fatal(err)
	}
	const snapshots = `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND application_name=$1 AND (state='idle in transaction' OR backend_xmin IS NOT NULL)`
	assertRowCount(t, ctx, db, snapshots, 0, workerName)
	// A snapshot owned by the worker pool must still be detected.
	owned, err := workerDB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owned.Rollback(ctx) }()
	if _, err := owned.Exec(ctx, `SELECT count(*) FROM users`); err != nil {
		t.Fatal(err)
	}
	assertRowCount(t, ctx, db, snapshots, 1, workerName)
	if err := owned.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	assertRowCount(t, ctx, db, snapshots, 0, workerName)
	var terminated bool
	if e := db.QueryRow(ctx, `SELECT pg_terminate_backend(l.pid) FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid WHERE a.application_name=$1 AND locktype='advisory' AND classid=hashtext('etherview:browser-push')::oid AND objid=hashtext('1')::oid AND granted`, workerName).Scan(&terminated); e != nil || !terminated {
		t.Fatal("terminate leader", e)
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("lost lock did not cancel pending HTTP")
	}
	sender.stop()
}
