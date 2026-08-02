//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/store"
)

func TestProxyInteractionCoverageRangesMergeSplitAndRecover(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}

	genesis := testBundle(0, testHash(160_000), testHash(0), testHash(161_000), "proxy-coverage-genesis")
	blockOne := testBundle(1, testHash(160_001), genesis.Block.Hash(), testHash(161_001), "proxy-coverage-one")
	blockTwo := testBundle(2, testHash(160_002), blockOne.Block.Hash(), testHash(161_002), "proxy-coverage-two")
	blockThree := testBundle(3, testHash(160_003), blockTwo.Block.Hash(), testHash(161_003), "proxy-coverage-three")
	blockFour := testBundle(4, testHash(160_004), blockThree.Block.Hash(), testHash(161_004), "proxy-coverage-four")
	blockFive := testBundle(5, testHash(160_005), blockFour.Block.Hash(), testHash(161_005), "proxy-coverage-five")
	commitCanonical(t, ctx, repository, genesis)
	commitCanonical(t, ctx, repository, blockOne)
	commitCanonical(t, ctx, repository, blockTwo)
	commitCanonical(t, ctx, repository, blockThree)
	commitCanonical(t, ctx, repository, blockFour)
	commitCanonical(t, ctx, repository, blockFive)

	one, two := mustBlockRef(t, blockOne), mustBlockRef(t, blockTwo)
	three, four := mustBlockRef(t, blockThree), mustBlockRef(t, blockFour)
	five := mustBlockRef(t, blockFive)
	publishEmptyProxyVerificationCoverage(t, ctx, db, two)
	assertProxyInteractionCoverageRanges(t, ctx, db, [][2]store.BlockRef{{two, two}})
	publishEmptyProxyVerificationCoverage(t, ctx, db, four)
	assertProxyInteractionCoverageRanges(t, ctx, db, [][2]store.BlockRef{{two, two}, {four, four}})
	assertProxyInteractionCoverageContains(t, ctx, db, two, four, false)

	// Filling a missing middle block prepends the right island and merges it
	// with the left island without walking either interval.
	publishEmptyProxyVerificationCoverage(t, ctx, db, three)
	assertProxyInteractionCoverageRanges(t, ctx, db, [][2]store.BlockRef{{two, four}})
	publishEmptyProxyVerificationCoverage(t, ctx, db, one)
	assertProxyInteractionCoverageRanges(t, ctx, db, [][2]store.BlockRef{{one, four}})
	assertProxyInteractionCoverageContains(t, ctx, db, one, four, true)
	publishEmptyProxyVerificationCoverage(t, ctx, db, five)
	assertProxyInteractionCoverageRanges(t, ctx, db, [][2]store.BlockRef{{one, five}})
	assertProxyInteractionCoverageContains(t, ctx, db, one, five, true)

	// A replay removes only proxy@2. The middle hole must split the exact
	// interval immediately; republishing that generation merges it again.
	enqueueProxyVerificationCoverageReplay(t, ctx, db, two, "coverage-range-middle-replay")
	assertProxyInteractionCoverageRanges(t, ctx, db, [][2]store.BlockRef{{one, one}, {three, five}})
	assertProxyInteractionCoverageContains(t, ctx, db, one, five, false)
	assertProxyInteractionCoverageContains(t, ctx, db, three, five, true)
	publishPendingEmptyProxyVerificationCoverage(t, ctx, db, two)
	assertProxyInteractionCoverageRanges(t, ctx, db, [][2]store.BlockRef{{one, five}})
	assertProxyInteractionCoverageContains(t, ctx, db, one, five, true)

	wrongEnd := five
	wrongEnd.Hash = testHash(999_999)
	assertProxyInteractionCoverageContains(t, ctx, db, one, wrongEnd, false)
}

func TestProxyInteractionCoverageRangesRejectDetachedHashes(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}

	genesis := testBundle(0, testHash(162_000), testHash(0), testHash(163_000), "proxy-coverage-reorg-genesis")
	oldOne := testBundle(1, testHash(162_001), genesis.Block.Hash(), testHash(163_001), "proxy-coverage-old-one")
	oldTwo := testBundle(2, testHash(162_002), oldOne.Block.Hash(), testHash(163_002), "proxy-coverage-old-two")
	oldThree := testBundle(3, testHash(162_003), oldTwo.Block.Hash(), testHash(163_003), "proxy-coverage-old-three")
	newOne := testBundle(1, testHash(164_001), genesis.Block.Hash(), testHash(165_001), "proxy-coverage-new-one")
	newTwo := testBundle(2, testHash(164_002), newOne.Block.Hash(), testHash(165_002), "proxy-coverage-new-two")
	newThree := testBundle(3, testHash(164_003), newTwo.Block.Hash(), testHash(165_003), "proxy-coverage-new-three")
	commitCanonical(t, ctx, repository, genesis)
	commitCanonical(t, ctx, repository, oldOne)
	commitCanonical(t, ctx, repository, oldTwo)
	commitCanonical(t, ctx, repository, oldThree)

	oldOneRef, oldTwoRef := mustBlockRef(t, oldOne), mustBlockRef(t, oldTwo)
	oldThreeRef := mustBlockRef(t, oldThree)
	for _, block := range []store.BlockRef{oldOneRef, oldTwoRef, oldThreeRef} {
		publishEmptyProxyVerificationCoverage(t, ctx, db, block)
	}
	assertProxyInteractionCoverageRanges(t, ctx, db, [][2]store.BlockRef{{oldOneRef, oldThreeRef}})
	assertProxyInteractionCoverageContains(t, ctx, db, oldOneRef, oldThreeRef, true)

	applyDerivedReorg(
		t, ctx, repository, genesis,
		[]chainbundle.Bundle{oldOne, oldTwo, oldThree},
		[]chainbundle.Bundle{newOne, newTwo, newThree},
		"proxy coverage exact endpoint reorg",
	)
	assertProxyInteractionCoverageRanges(t, ctx, db, nil)
	assertProxyInteractionCoverageContains(t, ctx, db, oldOneRef, oldThreeRef, false)
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM proxy_interaction_covered_blocks
		WHERE chain_id = 1 AND block_hash IN ($1, $2, $3)`, 0,
		oldOneRef.Hash.Bytes(), oldTwoRef.Hash.Bytes(), oldThreeRef.Hash.Bytes(),
	)

	newOneRef, newTwoRef := mustBlockRef(t, newOne), mustBlockRef(t, newTwo)
	newThreeRef := mustBlockRef(t, newThree)
	for _, block := range []store.BlockRef{newThreeRef, newOneRef, newTwoRef} {
		publishEmptyProxyVerificationCoverage(t, ctx, db, block)
	}
	assertProxyInteractionCoverageRanges(t, ctx, db, [][2]store.BlockRef{{newOneRef, newThreeRef}})
	assertProxyInteractionCoverageContains(t, ctx, db, newOneRef, newThreeRef, true)
	assertProxyInteractionCoverageContains(t, ctx, db, oldOneRef, oldThreeRef, false)
}

func TestProxyInteractionCoverageMembershipDoesNotDependOnChainHeight(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	const syntheticChainID = 987654321
	statements := []string{
		`INSERT INTO chains (chain_id) VALUES ($1)`,
		`INSERT INTO blocks (chain_id, number, hash, parent_hash, timestamp, raw)
		 SELECT $1, height,
		        decode(lpad(to_hex(height + 1), 64, '0'), 'hex'),
		        decode(lpad(to_hex(height), 64, '0'), 'hex'),
		        height, '{}'::jsonb
		 FROM generate_series(0, 9999) AS height`,
		`INSERT INTO proxy_interaction_covered_blocks (
			 chain_id, block_number, block_hash
		 )
		 SELECT chain_id, number, hash
		 FROM blocks
		 WHERE chain_id = $1`,
		`INSERT INTO proxy_interaction_coverage_ranges (
			 chain_id, start_block, start_block_hash, end_block, end_block_hash
		 )
		 SELECT $1, 0,
		        decode(lpad(to_hex(1), 64, '0'), 'hex'),
		        9999,
		        decode(lpad(to_hex(10000), 64, '0'), 'hex')`,
	}
	for index, statement := range statements {
		if _, err := db.ExecContext(ctx, statement, syntheticChainID); err != nil {
			t.Fatalf("insert long synthetic coverage interval step %d: %v", index, err)
		}
	}
	var covered bool
	if err := db.QueryRowContext(ctx, `
		SELECT proxy_interaction_coverage_contains(
			$1, 0, decode(lpad(to_hex(1), 64, '0'), 'hex'),
			9999, decode(lpad(to_hex(10000), 64, '0'), 'hex')
		)`, syntheticChainID).Scan(&covered); err != nil {
		t.Fatal(err)
	}
	if !covered {
		t.Fatal("long exact interval was not covered")
	}

	var definition string
	if err := db.QueryRowContext(ctx, `
		SELECT pg_get_functiondef(
			'proxy_interaction_coverage_contains(numeric,numeric,bytea,numeric,bytea)'::regprocedure
		)`).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"generate_series", "canonical_blocks", "published_block_stage_results"} {
		if strings.Contains(definition, forbidden) {
			t.Fatalf("coverage membership function contains height-dependent source %q: %s", forbidden, definition)
		}
	}
	for _, required := range []string{
		"proxy_interaction_coverage_ranges",
		"ORDER BY candidate.start_block DESC",
		"LIMIT 1",
		"required_start.block_hash = target_start_hash",
		"required_end.block_hash = target_end_hash",
	} {
		if !strings.Contains(definition, required) {
			t.Fatalf("coverage membership function lacks %q: %s", required, definition)
		}
	}
}

func assertProxyInteractionCoverageRanges(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	want [][2]store.BlockRef,
) {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
		SELECT start_block::text, start_block_hash,
		       end_block::text, end_block_hash
		FROM proxy_interaction_coverage_ranges
		WHERE chain_id = 1
		ORDER BY start_block`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close() //nolint:errcheck
	index := 0
	for rows.Next() {
		if index >= len(want) {
			t.Fatalf("unexpected proxy interaction coverage range at index %d", index)
		}
		var startNumber, endNumber string
		var startHash, endHash []byte
		if err := rows.Scan(&startNumber, &startHash, &endNumber, &endHash); err != nil {
			t.Fatal(err)
		}
		expectedStart, expectedEnd := want[index][0], want[index][1]
		if startNumber != strconv.FormatUint(expectedStart.Number, 10) ||
			endNumber != strconv.FormatUint(expectedEnd.Number, 10) ||
			!bytes.Equal(startHash, expectedStart.Hash.Bytes()) ||
			!bytes.Equal(endHash, expectedEnd.Hash.Bytes()) {
			t.Fatalf(
				"coverage range %d=(%s,%x)-(%s,%x), want (%d,%x)-(%d,%x)",
				index, startNumber, startHash, endNumber, endHash,
				expectedStart.Number, expectedStart.Hash.Bytes(),
				expectedEnd.Number, expectedEnd.Hash.Bytes(),
			)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if index != len(want) {
		t.Fatalf("proxy interaction coverage range count=%d, want %d", index, len(want))
	}
}

func assertProxyInteractionCoverageContains(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	start, end store.BlockRef,
	want bool,
) {
	t.Helper()
	var got bool
	if err := db.QueryRowContext(ctx, `
		SELECT proxy_interaction_coverage_contains($1, $2, $3, $4, $5)`,
		1, start.Number, start.Hash.Bytes(), end.Number, end.Hash.Bytes(),
	).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf(
			"proxy interaction coverage (%d,%s)-(%d,%s)=%t, want %t",
			start.Number, start.Hash, end.Number, end.Hash, got, want,
		)
	}
}
