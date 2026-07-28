//go:build integration

package integration_test

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/state"
)

func TestPostgresCanonicalStateTipUsesNumericHeightAndCurrentHash(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	execFixture(t, ctx, db, `INSERT INTO chains (chain_id) VALUES (1)`)
	parent := common.Hash{}
	hashes := map[uint64]common.Hash{}
	for _, number := range []uint64{99, 100, 169} {
		hash := common.BigToHash(new(big.Int).SetUint64(number + 10_000))
		hashes[number] = hash
		execFixture(t, ctx, db, `
			INSERT INTO blocks (chain_id, number, hash, parent_hash, timestamp, raw)
			VALUES (1, $1::numeric, $2, $3, $1::numeric, '{}'::jsonb)`,
			number, hash.Bytes(), parent.Bytes())
		execFixture(t, ctx, db, `
			INSERT INTO canonical_blocks (chain_id, number, block_hash)
			VALUES (1, $1::numeric, $2)`, number, hash.Bytes())
		parent = hash
	}

	source := state.PostgresCanonicalSource{DB: db, ChainID: "1"}
	tip, err := source.Tip(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tip.Number != 169 || tip.Hash != hashes[169] {
		t.Fatalf("numeric canonical tip = %+v, want 169/%s", tip, hashes[169])
	}

	replacement := common.BigToHash(new(big.Int).SetUint64(20_169))
	execFixture(t, ctx, db, `
		INSERT INTO blocks (chain_id, number, hash, parent_hash, timestamp, raw)
		VALUES (1, 169, $1, $2, 169, '{}'::jsonb)`,
		replacement.Bytes(), hashes[100].Bytes())
	execFixture(t, ctx, db, `
		UPDATE canonical_blocks SET block_hash = $1, updated_at = now()
		WHERE chain_id = 1 AND number = 169`, replacement.Bytes())

	tip, err = source.Tip(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tip.Number != 169 || tip.Hash != replacement {
		t.Fatalf("replacement canonical tip = %+v, want 169/%s", tip, replacement)
	}
	canonical, err := source.IsCanonical(ctx, state.CanonicalRef{Number: 169, Hash: hashes[169]})
	if err != nil {
		t.Fatal(err)
	}
	if canonical {
		t.Fatal("detached block remained canonical")
	}
}
