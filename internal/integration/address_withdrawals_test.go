//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/httpapi"
	"github.com/islishude/etherview/internal/query"
)

func TestAddressWithdrawalsUseNumericIndexOrderingAcrossStablePages(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	execFixture(t, ctx, db, `INSERT INTO chains (chain_id) VALUES (1)`)

	address := common.HexToAddress("0x1111111111111111111111111111111111111111")
	other := common.HexToAddress("0x2222222222222222222222222222222222222222")
	block10 := insertWithdrawalHistoryBlock(t, ctx, db, 10, 10, true)
	block11 := insertWithdrawalHistoryBlock(t, ctx, db, 11, 11, true)
	block12 := insertWithdrawalHistoryBlock(t, ctx, db, 12, 12, true)
	orphan12 := insertWithdrawalHistoryBlock(t, ctx, db, 12, 99, false)
	insertWithdrawalHistoryRow(t, ctx, db, 10, block10, 2, address, 102, 2)
	insertWithdrawalHistoryRow(t, ctx, db, 11, block11, 9, address, 109, 1)
	insertWithdrawalHistoryRow(t, ctx, db, 12, block12, 10, address, 110, 3_200_000_000)
	insertWithdrawalHistoryRow(t, ctx, db, 12, block12, 100, other, 200, 1)
	insertWithdrawalHistoryRow(t, ctx, db, 12, orphan12, 999, address, 999, 1)

	reader, err := query.NewPostgresReader(db, query.Options{ChainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	first, cursor, err := reader.AddressWithdrawals(ctx, address.Hex(), "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].Index != "10" || first[1].Index != "9" || cursor == "" {
		t.Fatalf("first page=%+v cursor=%q", first, cursor)
	}

	block13 := insertWithdrawalHistoryBlock(t, ctx, db, 13, 13, true)
	insertWithdrawalHistoryRow(t, ctx, db, 13, block13, 11, address, 111, 1)
	second, next, err := reader.AddressWithdrawals(ctx, address.Hex(), cursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].Index != "2" || next != "" {
		t.Fatalf("second page=%+v next=%q", second, next)
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM canonical_blocks WHERE chain_id = 1 AND number IN (12, 13)`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reader.AddressWithdrawals(ctx, address.Hex(), cursor, 2); err == nil || err != httpapi.ErrInvalidCursor {
		t.Fatalf("detached snapshot cursor error=%v, want invalid cursor", err)
	}
}

func insertWithdrawalHistoryBlock(
	t *testing.T, ctx context.Context, db *sql.DB, number uint64, identity byte, canonical bool,
) common.Hash {
	t.Helper()
	hash := common.BytesToHash([]byte{identity})
	execFixture(t, ctx, db, `
		INSERT INTO blocks (chain_id, number, hash, parent_hash, timestamp, raw)
		VALUES (1, $1::numeric, $2, $3, $4::numeric, '{}'::jsonb)`,
		fmt.Sprint(number), hash.Bytes(), common.Hash{}.Bytes(), fmt.Sprint(1_700_000_000+number))
	if canonical {
		execFixture(t, ctx, db, `
			INSERT INTO canonical_blocks (chain_id, number, block_hash)
			VALUES (1, $1::numeric, $2)`, fmt.Sprint(number), hash.Bytes())
	}
	return hash
}

func insertWithdrawalHistoryRow(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	number uint64,
	blockHash common.Hash,
	index uint64,
	address common.Address,
	validator uint64,
	amount uint64,
) {
	t.Helper()
	execFixture(t, ctx, db, `
		INSERT INTO withdrawals (
			chain_id, block_number, block_hash, withdrawal_index,
			validator_index, address, amount, raw
		) VALUES (1, $1::numeric, $2, $3::numeric, $4::numeric, $5, $6::numeric, '{}'::jsonb)`,
		fmt.Sprint(number), blockHash.Bytes(), fmt.Sprint(index), fmt.Sprint(validator), address.Bytes(), fmt.Sprint(amount))
}
