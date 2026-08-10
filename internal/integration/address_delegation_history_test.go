//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/query"
)

func TestAddressDelegationHistoryUsesNumericPositionOrderingAcrossPages(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	execFixture(t, ctx, db, `INSERT INTO chains (chain_id) VALUES (1)`)

	block9 := delegationHistoryBlock(t, ctx, db, 9, 9, true)
	block10 := delegationHistoryBlock(t, ctx, db, 10, 10, true)
	block11 := delegationHistoryBlock(t, ctx, db, 11, 11, true)
	orphan11 := delegationHistoryBlock(t, ctx, db, 11, 99, false)
	authority := common.HexToAddress("0x1111111111111111111111111111111111111111")
	delegateA := common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	delegateB := common.HexToAddress("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	delegateC := common.HexToAddress("0xcccccccccccccccccccccccccccccccccccccccc")

	insertOrderedDelegationHistoryEvent(t, ctx, db, 9, block9, 0, 0, authority, delegateA, "applied", true)
	insertOrderedDelegationHistoryEvent(t, ctx, db, 10, block10, 9, 0, authority, common.Address{}, "applied", true)
	insertOrderedDelegationHistoryEvent(t, ctx, db, 10, block10, 10, 0, authority, delegateB, "applied", true)
	insertOrderedDelegationHistoryEvent(t, ctx, db, 11, block11, 10, 9, authority, delegateC, "applied", true)
	insertOrderedDelegationHistoryEvent(t, ctx, db, 11, block11, 10, 10, authority, common.Address{}, "applied", true)
	insertOrderedDelegationHistoryEvent(t, ctx, db, 11, block11, 10, 11, authority, delegateA, "skipped", true)
	insertOrderedDelegationHistoryEvent(t, ctx, db, 11, orphan11, 12, 0, authority, delegateA, "applied", false)

	reader, err := catalog.NewPostgres(db, catalog.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var items []catalog.DelegationHistoryItem
	cursor := ""
	for {
		page, pageErr := reader.AddressDelegations(ctx, catalog.AddressDelegationRequest{
			ChainID: "1", Address: authority.Hex(), Cursor: cursor, Limit: 2,
		})
		if pageErr != nil {
			t.Fatal(pageErr)
		}
		items = append(items, page.Items...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	wantPositions := []string{"11/10/10", "11/10/9", "10/10/0", "10/9/0", "9/0/0"}
	wantKinds := []string{"cleared", "redelegated", "delegated", "cleared", "delegated"}
	if len(items) != len(wantPositions) {
		t.Fatalf("history item count=%d, want %d: %+v", len(items), len(wantPositions), items)
	}
	for index, item := range items {
		position := item.BlockNumber + "/" + item.TransactionIndex + "/" + item.AuthorizationIndex
		if position != wantPositions[index] || item.Kind != wantKinds[index] {
			t.Fatalf("history[%d]=%s %s, want %s %s", index, position, item.Kind, wantPositions[index], wantKinds[index])
		}
	}
	if items[1].PreviousDelegate == nil || *items[1].PreviousDelegate != delegateB.Hex() {
		t.Fatalf("redelegation previous delegate=%v, want %s", items[1].PreviousDelegate, delegateB.Hex())
	}
}

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

func insertOrderedDelegationHistoryEvent(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	number uint64,
	blockHash common.Hash,
	transactionIndex int,
	authorizationIndex int,
	authority common.Address,
	delegate common.Address,
	status string,
	canonical bool,
) {
	t.Helper()
	transactionHash := common.BytesToHash([]byte{byte(number), byte(transactionIndex), blockHash[common.HashLength-1]})
	execFixture(t, ctx, db, `
		INSERT INTO transactions (chain_id, hash, tx_type, raw)
		VALUES (1, $1, 4, '{}'::jsonb)
		ON CONFLICT DO NOTHING`, transactionHash.Bytes())
	execFixture(t, ctx, db, `
		INSERT INTO transaction_inclusions (
			chain_id, block_number, block_hash, tx_index, tx_hash, raw
		) VALUES (1, $1::numeric, $2, $3, $4, '{}'::jsonb)
		ON CONFLICT DO NOTHING`, fmt.Sprint(number), blockHash.Bytes(), transactionIndex, transactionHash.Bytes())
	skipReason := any(nil)
	if status != "applied" {
		skipReason = "nonce_mismatch"
	}
	execFixture(t, ctx, db, `
		INSERT INTO eip7702_authorizations (
			chain_id, block_number, block_hash, transaction_hash, transaction_index,
			authorization_index, authorization_chain_id, authorization_nonce,
			delegate_address, y_parity, r, s, authority, signature_status,
			application_status, skip_reason, canonical
		) VALUES (1, $1::numeric, $2, $3, $4, $5, 1, 0, $6, 0, $7, $7,
			$8, 'valid', $9, $10, $11)`,
		fmt.Sprint(number), blockHash.Bytes(), transactionHash.Bytes(), transactionIndex,
		authorizationIndex, delegate.Bytes(), common.HexToHash("0x01").Bytes(), authority.Bytes(),
		status, skipReason, canonical)
}
