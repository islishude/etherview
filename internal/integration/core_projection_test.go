//go:build integration

package integration_test

import (
	"context"
	"math/big"
	"strings"
	"testing"

	pgxpool "github.com/jackc/pgx/v5/pgxpool"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/chainbundle/testfixture"
	"github.com/islishude/etherview/internal/query"
	"github.com/islishude/etherview/internal/store"
)

// This tests stored projection boundaries, not execution-header authentication.
func TestCoreBlockSlotProjection(t *testing.T) {
	db, reader, bundle := coreProjectionFixture(t)
	for _, value := range []struct{ raw, want string }{
		{`null`, ""}, {`"0x0"`, "0"}, {`"0xffffffffffffffff"`, "18446744073709551615"},
	} {
		if _, err := db.Exec(t.Context(), `UPDATE blocks SET raw = jsonb_set(raw, '{slotNumber}', $1::jsonb) WHERE chain_id = 1 AND hash = $2`, value.raw, bundle.Block.Hash().Bytes()); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"0", bundle.Block.Hash().Hex()} {
			block, err := reader.Block(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			if value.want == "" {
				if block.SlotNumber != nil {
					t.Fatal("null slot must be omitted")
				}
			} else if block.SlotNumber == nil || *block.SlotNumber != value.want {
				t.Fatalf("slot = %v, want %s", block.SlotNumber, value.want)
			}
		}
		blocks, _, err := reader.Blocks(t.Context(), "", 10)
		if err != nil {
			t.Fatal(err)
		}
		if value.want != "" && (len(blocks) != 1 || blocks[0].SlotNumber == nil || *blocks[0].SlotNumber != value.want) {
			t.Fatalf("block list = %+v", blocks)
		}
	}
	if _, err := db.Exec(t.Context(), `UPDATE blocks SET raw = jsonb_set(raw, '{slotNumber}', '42'::jsonb) WHERE chain_id = 1 AND hash = $1`, bundle.Block.Hash().Bytes()); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Block(t.Context(), "0"); err == nil {
		t.Fatal("numeric slot accepted")
	}
}

func TestCorePublicProjectionUsesNormalizedRowsAndFailsClosedOnDrift(t *testing.T) {
	t.Run("projects exact block and transaction fields", func(t *testing.T) {
		db, reader, bundle := coreProjectionFixture(t)
		blocks, _, err := reader.Blocks(context.Background(), "", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(blocks) != 1 || blocks[0].TransactionCount != 2 ||
			blocks[0].Withdrawals == nil || len(*blocks[0].Withdrawals) != 2 ||
			(*blocks[0].Withdrawals)[1].Index != "8" {
			t.Fatalf("block projection = %+v", blocks)
		}
		transactions, _, err := reader.Transactions(context.Background(), "", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(transactions) != 2 || transactions[0].BlockTimestamp == nil ||
			transactions[0].BlockTimestamp.Unix() != int64(bundle.Block.Time()) ||
			transactions[0].BaseFeePerGas == nil {
			t.Fatalf("transaction projection = %+v", transactions)
		}
		_ = db
	})

	t.Run("rejects raw transaction count drift", func(t *testing.T) {
		db, reader, bundle := coreProjectionFixture(t)
		if _, err := db.Exec(context.Background(), `
			UPDATE blocks
			SET raw = jsonb_set(raw, '{transactions}', '[]'::jsonb)
			WHERE chain_id = 1 AND hash = $1
		`, bundle.Block.Hash().Bytes()); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.Block(context.Background(), bundle.Block.Hash().Hex()); err == nil ||
			!strings.Contains(err.Error(), "normalized inclusions") {
			t.Fatalf("transaction-count drift error = %v", err)
		}
	})

	t.Run("rejects normalized withdrawal loss", func(t *testing.T) {
		db, reader, bundle := coreProjectionFixture(t)
		if _, err := db.Exec(context.Background(), `
			DELETE FROM withdrawals
			WHERE chain_id = 1 AND block_hash = $1 AND withdrawal_index = 8
		`, bundle.Block.Hash().Bytes()); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.Block(context.Background(), bundle.Block.Hash().Hex()); err == nil ||
			!strings.Contains(err.Error(), "withdrawal count") {
			t.Fatalf("withdrawal drift error = %v", err)
		}
	})
}

func coreProjectionFixture(t *testing.T) (*pgxpool.Pool, *query.PostgresReader, chainbundle.Bundle) {
	t.Helper()
	db := newMigratedPostgres(t)
	bundle, err := testfixture.New(testfixture.Options{
		Number:           0,
		BaseFee:          big.NewInt(1),
		TransactionTypes: []uint8{types.DynamicFeeTxType, types.DynamicFeeTxType},
		Withdrawals: []*types.Withdrawal{
			{Index: 7, Validator: 70, Address: common.HexToAddress("0x01"), Amount: 700},
			{Index: 8, Validator: 80, Address: common.HexToAddress("0x02"), Amount: 800},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BindChainIdentity(context.Background(), db, "1", bundle.Block.Hash()); err != nil {
		t.Fatal(err)
	}
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ConfigureIndex(context.Background(), "1", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CommitCanonicalSegment(
		context.Background(), "1", []chainbundle.Bundle{bundle},
	); err != nil {
		t.Fatal(err)
	}
	reader, err := query.NewPostgresReader(db, query.Options{ChainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	return db, reader, bundle
}
