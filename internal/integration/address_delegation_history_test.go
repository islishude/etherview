//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/query"
)

func TestAddressDelegationHistoryExistenceUsesCanonicalAppliedRowsAtReference(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	execFixture(t, ctx, db, `INSERT INTO chains (chain_id) VALUES (1)`)

	referenceHash := delegationHistoryBlock(t, ctx, db, 2, 2, true)
	delegationHistoryBlock(t, ctx, db, 1, 1, true)
	delegationHistoryBlock(t, ctx, db, 3, 3, true)
	delegationHistoryBlock(t, ctx, db, 2, 22, false)

	cleared := common.HexToAddress("0x1111111111111111111111111111111111111111")
	skippedOnly := common.HexToAddress("0x2222222222222222222222222222222222222222")
	orphanOnly := common.HexToAddress("0x3333333333333333333333333333333333333333")
	futureOnly := common.HexToAddress("0x4444444444444444444444444444444444444444")
	insertDelegationHistoryAuthorization(t, ctx, db, 1, 1, cleared, common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), "applied", true)
	insertDelegationHistoryAuthorization(t, ctx, db, 2, 2, cleared, common.Address{}, "applied", true)
	insertDelegationHistoryAuthorization(t, ctx, db, 2, 2, skippedOnly, common.HexToAddress("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), "skipped", true)
	insertDelegationHistoryAuthorization(t, ctx, db, 2, 22, orphanOnly, common.HexToAddress("0xcccccccccccccccccccccccccccccccccccccccc"), "applied", false)
	insertDelegationHistoryAuthorization(t, ctx, db, 3, 3, futureOnly, common.HexToAddress("0xdddddddddddddddddddddddddddddddddddddddd"), "applied", true)

	reader, err := query.NewPostgresReader(db, query.Options{ChainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		address common.Address
		want    bool
	}{
		{name: "clearing retains history", address: cleared, want: true},
		{name: "skipped authorization is excluded", address: skippedOnly},
		{name: "orphan authorization is excluded", address: orphanOnly},
		{name: "future authorization is excluded", address: futureOnly},
	} {
		t.Run(test.name, func(t *testing.T) {
			hasHistory, err := reader.HasAddressDelegationHistory(ctx, test.address.Hex(), 2, referenceHash)
			if err != nil {
				t.Fatal(err)
			}
			if hasHistory != test.want {
				t.Fatalf("hasHistory=%v, want %v", hasHistory, test.want)
			}
		})
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM canonical_blocks WHERE chain_id = 1 AND number = 2`); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.HasAddressDelegationHistory(ctx, cleared.Hex(), 2, referenceHash); err == nil {
		t.Fatal("detached reference unexpectedly returned a delegation history result")
	}
}

func delegationHistoryBlock(
	t *testing.T, ctx context.Context, db *sql.DB, number uint64, identity byte, canonical bool,
) common.Hash {
	t.Helper()
	hash := common.BytesToHash([]byte{identity})
	transactionHash := common.BytesToHash([]byte{identity, 0xff})
	execFixture(t, ctx, db, `
		INSERT INTO blocks (chain_id, number, hash, parent_hash, timestamp, raw)
		VALUES (1, $1::numeric, $2, $3, $1::numeric, '{}'::jsonb)`,
		fmt.Sprint(number), hash.Bytes(), common.Hash{}.Bytes())
	execFixture(t, ctx, db, `
		INSERT INTO transactions (chain_id, hash, tx_type, raw)
		VALUES (1, $1, 4, '{}'::jsonb)`, transactionHash.Bytes())
	execFixture(t, ctx, db, `
		INSERT INTO transaction_inclusions (
			chain_id, block_number, block_hash, tx_index, tx_hash, raw
		) VALUES (1, $1::numeric, $2, 0, $3, '{}'::jsonb)`,
		fmt.Sprint(number), hash.Bytes(), transactionHash.Bytes())
	if canonical {
		execFixture(t, ctx, db, `
			INSERT INTO canonical_blocks (chain_id, number, block_hash)
			VALUES (1, $1::numeric, $2)`, fmt.Sprint(number), hash.Bytes())
	}
	return hash
}

func insertDelegationHistoryAuthorization(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	number uint64,
	identity byte,
	authority common.Address,
	delegate common.Address,
	status string,
	canonical bool,
) {
	t.Helper()
	hash := common.BytesToHash([]byte{identity})
	transactionHash := common.BytesToHash([]byte{identity, 0xff})
	skipReason := any(nil)
	if status != "applied" {
		skipReason = "nonce_mismatch"
	}
	authorizationIndex := int(authority[common.AddressLength-1])
	execFixture(t, ctx, db, `
		INSERT INTO eip7702_authorizations (
			chain_id, block_number, block_hash, transaction_hash, transaction_index,
			authorization_index, authorization_chain_id, authorization_nonce,
			delegate_address, y_parity, r, s, authority, signature_status,
			application_status, skip_reason, canonical
		) VALUES (1, $1::numeric, $2, $3, 0, $4, 1, 0, $5, 0, $6, $6,
			$7, 'valid', $8, $9, $10)`,
		fmt.Sprint(number), hash.Bytes(), transactionHash.Bytes(), authorizationIndex,
		delegate.Bytes(), common.HexToHash("0x01").Bytes(), authority.Bytes(), status, skipReason, canonical)
}
