//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/chainbundle/testfixture"
	"github.com/islishude/etherview/internal/query"
	"github.com/islishude/etherview/internal/store"
)

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
		if _, err := db.ExecContext(context.Background(), `
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
		if _, err := db.ExecContext(context.Background(), `
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

func coreProjectionFixture(t *testing.T) (*sql.DB, *query.PostgresReader, chainbundle.Bundle) {
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
