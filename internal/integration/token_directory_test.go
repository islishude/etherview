//go:build integration

package integration_test

import (
	"errors"
	"testing"

	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/store"
)

func TestTokenDirectoryOrdersLatestObservationsAndFencesSnapshot(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	var blocks []chainbundle.Bundle
	parent := testHash(0)
	for n := uint64(0); n <= 11; n++ {
		block := testBundle(n, testHash(380000+n), parent, testHash(381000+n), "token-directory")
		commitCanonical(t, ctx, repository, block)
		blocks = append(blocks, block)
		parent = block.Block.Hash()
	}
	insert := func(address, number uint64) {
		t.Helper()
		execFixture(t, ctx, db, `INSERT INTO token_contracts
   (chain_id, address, code_hash, standard, confidence, metadata_state, observed_block_number, observed_block_hash)
   VALUES (1, $1, $2, 'erc20', 'high', 'complete', $3, $4)`,
			mustBytes(t, testAddress(address)), mustBytes(t, testHash(382000)), number, mustBytes(t, blocks[number].Block.Hash()))
	}
	// 160 addresses prove the API itself has no 150-item display cap.
	for address := uint64(1); address <= 160; address++ {
		insert(address, 9)
	}
	insert(2, 10)
	insert(3, 10)
	insert(1, 11) // Unpublished newest observation must not displace block 9.
	markTokenStageComplete(t, ctx, db, blocks[9])
	markTokenStageComplete(t, ctx, db, blocks[10])
	reader, err := catalog.NewPostgres(db, catalog.Options{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := reader.TokenContracts(ctx, catalog.TokenListRequest{ChainID: "1", Limit: 1})
	if err != nil || len(first.Items) != 1 || first.Items[0].Address != testAddress(2).Hex() || first.Items[0].ObservedBlockNumber != "10" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	// A newer head must not expand or invalidate the frozen window.
	extra := testBundle(12, testHash(380012), parent, testHash(381012), "token-directory-new-head")
	commitCanonical(t, ctx, repository, extra)
	markTokenStageComplete(t, ctx, db, extra)
	cursor := first.NextCursor
	addresses := []string{first.Items[0].Address}
	for cursor != "" {
		page, err := reader.TokenContracts(ctx, catalog.TokenListRequest{ChainID: "1", Limit: 25, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			addresses = append(addresses, item.Address)
		}
		cursor = page.NextCursor
	}
	expected := []uint64{2, 3, 1}
	for address := uint64(4); address <= 160; address++ {
		expected = append(expected, address)
	}
	if len(addresses) != len(expected) {
		t.Fatalf("got %d addresses, want %d", len(addresses), len(expected))
	}
	for i, address := range expected {
		if addresses[i] != testAddress(address).Hex() {
			t.Fatalf("position %d=%s want %s", i, addresses[i], testAddress(address).Hex())
		}
	}
	markTokenStageComplete(t, ctx, db, blocks[11])
	if _, err := reader.TokenContracts(ctx, catalog.TokenListRequest{ChainID: "1", Cursor: first.NextCursor}); !errors.Is(err, catalog.ErrInvalidCursor) {
		t.Fatalf("late publication cursor error=%v", err)
	}
	fresh, err := reader.TokenContracts(ctx, catalog.TokenListRequest{ChainID: "1", Limit: 1})
	if err != nil || len(fresh.Items) != 1 || fresh.Items[0].ObservedBlockNumber != "11" {
		t.Fatalf("fresh=%+v err=%v", fresh, err)
	}
	// Replace blocks 11 and 12; retained orphan observations must disappear.
	replacement := testBundle(11, testHash(390011), blocks[10].Block.Hash(), testHash(391011), "token-directory-reorg")
	if err := repository.ApplyReorg(ctx, "1", store.Reorg{
		Ancestor:   mustBlockRef(t, blocks[10]),
		Detached:   []store.BlockRef{mustBlockRef(t, extra), mustBlockRef(t, blocks[11])},
		Attached:   []chainbundle.Bundle{replacement},
		Checkpoint: store.NewCoreCheckpoint(mustBlockRef(t, replacement)),
		Reason:     "token directory reorg",
	}); err != nil {
		t.Fatal(err)
	}
	afterReorg, err := reader.TokenContracts(ctx, catalog.TokenListRequest{ChainID: "1", Limit: 1})
	if err != nil || len(afterReorg.Items) != 1 || afterReorg.Items[0].Address != testAddress(2).Hex() {
		t.Fatalf("reorg=%+v err=%v", afterReorg, err)
	}
	if _, err := reader.TokenContracts(ctx, catalog.TokenListRequest{ChainID: "1", Cursor: fresh.NextCursor}); !errors.Is(err, catalog.ErrInvalidCursor) {
		t.Fatalf("orphan snapshot error=%v", err)
	}
}
