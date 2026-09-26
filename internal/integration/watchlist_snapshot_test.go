//go:build integration

package integration_test

import (
	"context"
	"errors"
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
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/watchlist"
	"github.com/jackc/pgx/v5"
)

func TestWatchlistExportSnapshotDuringReorg(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repo, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	stamp := uint64(time.Now().Add(-time.Hour).Unix())
	genesis, _ := testfixture.New(testfixture.Options{Number: 0, Timestamp: stamp})
	commitCanonical(t, ctx, repo, genesis)
	original, _ := testfixture.New(testfixture.Options{Number: 1, ParentHash: genesis.Block.Hash(), Timestamp: stamp + 10, TransactionTypes: []uint8{types.LegacyTxType}})
	commitCanonical(t, ctx, repo, original)
	user := uuid.NewString()
	execFixture(t, ctx, db, `INSERT INTO users(id,chain_id,address,role,status,created_at,updated_at) VALUES($1,1,decode(repeat('77',20),'hex'),'user','active',now(),now())`, user)
	service, _ := watchlist.New(db, 1)
	request := gen.AddressExportRequest{Address: common.Address{19: 2}.Hex(), Kind: "transaction", Direction: "both", From: time.Unix(int64(stamp), 0), To: time.Unix(int64(stamp+20), 0)}
	if file, err := service.Export(ctx, user, request); !errors.Is(err, watchlist.ErrUnavailable) || len(file.Bytes) != 0 {
		t.Fatalf("missing Core coverage returned a file: %v", err)
	}
	execFixture(t, ctx, db, `INSERT INTO core_coverage_ranges(chain_id,range_start,range_end) VALUES(1,0,1)`)
	replacement, _ := testfixture.New(testfixture.Options{Number: 1, ParentHash: genesis.Block.Hash(), Timestamp: stamp + 10, ExtraData: []byte("replacement"), TransactionTypes: []uint8{types.LegacyTxType}})
	var applied bool
	observed := &watchSnapshotDatabase{Database: db, test: t, afterSnapshot: func() {
		ref := mustBlockRef(t, replacement)
		err := repo.ApplyReorg(ctx, "1", store.Reorg{Ancestor: mustBlockRef(t, genesis), Detached: []store.BlockRef{mustBlockRef(t, original)}, Attached: []chainbundle.Bundle{replacement}, Checkpoint: store.NewCoreCheckpoint(ref), Reason: "export snapshot regression"})
		if err != nil {
			t.Fatal(err)
		}
		applied = true
	}}
	snapshotted, _ := watchlist.New(observed, 1)
	file, err := snapshotted.Export(ctx, user, request)
	if err != nil || !applied || file.BlockHash != original.Block.Hash().Hex() || !strings.Contains(string(file.Bytes), original.Block.Hash().Hex()) || strings.Contains(string(file.Bytes), replacement.Block.Hash().Hex()) {
		t.Fatalf("mixed reorg snapshot: hash=%s applied=%t error=%v", file.BlockHash, applied, err)
	}
	current, err := service.Export(ctx, user, request)
	if err != nil || current.BlockHash != replacement.Block.Hash().Hex() || strings.Contains(string(current.Bytes), original.Block.Hash().Hex()) {
		t.Fatalf("new snapshot did not see reorg: hash=%s error=%v", current.BlockHash, err)
	}
	deadline, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer cancel()
	if file, err := service.Export(deadline, user, request); err == nil || len(file.Bytes) != 0 {
		t.Fatalf("expired export returned a file: %v", err)
	}
}

// The real writer transaction remains open while another connection commits the
// reorg, immediately after the snapshot's first query has established visibility.
type watchSnapshotDatabase struct {
	dbaccess.Database
	test          *testing.T
	afterSnapshot func()
}

func (d *watchSnapshotDatabase) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 15*time.Second {
		d.test.Fatal("export database work lacks its 15-second deadline")
	}
	tx, err := d.Database.BeginTx(ctx, options)
	if err != nil || options.IsoLevel != pgx.RepeatableRead {
		return tx, err
	}
	if options.AccessMode != pgx.ReadOnly {
		d.test.Fatal("export snapshot permits writes")
	}
	return &watchSnapshotTransaction{Tx: tx, afterSnapshot: d.afterSnapshot}, nil
}

type watchSnapshotTransaction struct {
	pgx.Tx
	once          sync.Once
	afterSnapshot func()
}

func (t *watchSnapshotTransaction) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	return watchSnapshotRow{Row: t.Tx.QueryRow(ctx, query, args...), afterScan: func() { t.once.Do(t.afterSnapshot) }}
}

type watchSnapshotRow struct {
	pgx.Row
	afterScan func()
}

func (r watchSnapshotRow) Scan(dest ...any) error {
	if err := r.Row.Scan(dest...); err != nil {
		return err
	}
	r.afterScan()
	return nil
}
