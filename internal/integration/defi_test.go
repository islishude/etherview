//go:build integration

package integration_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/chainbundle"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/deficontract"
	"github.com/islishude/etherview/internal/defiread"
	"github.com/islishude/etherview/internal/store"
	"github.com/jackc/pgx/v5/pgtype"
)

func defiNumeric(s string) pgtype.Numeric { var n pgtype.Numeric; _ = n.Scan(s); return n }
func TestDeFiPublicationCursorAndCanonicalFences(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repository, e := store.NewPostgresRepository(db)
	if e != nil {
		t.Fatal(e)
	}
	if e = repository.ConfigureIndex(ctx, "1", 0); e != nil {
		t.Fatal(e)
	}
	owner, token := testAddress(71), testAddress(72)
	block := derivedTokenBundle(t, 0, testHash(771), testHash(0), testHash(772), "defi", token, testAddress(0), owner, 10)
	if _, e = repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{block}); e != nil {
		t.Fatal(e)
	}
	q := dbgen.New(db)
	chain := defiNumeric("1")
	source := "source"
	digest := strings.Repeat("ab", 32)
	if e = q.RegisterDeFiSource(ctx, dbgen.RegisterDeFiSourceParams{ID: source, ChainID: chain, Deployment: "v2", Protocol: "uniswap-v2", ManifestDigest: digest}); e != nil {
		t.Fatal(e)
	}
	reader := &defiread.Reader{DB: db, Digest: digest, Sources: []defiread.Source{{ID: source, Deployment: "v2", Protocol: deficontract.UniswapV2}}}
	req := deficontract.Request{Owner: owner.Hex(), Limit: 1}
	page, e := reader.Positions(ctx, req)
	if e != nil || len(page.Positions) != 0 || page.Deployments[5].State == "complete" {
		t.Fatal(page, e)
	}
	attachment, e := q.DeFiCanonicalBlock(ctx, chain, defiNumeric("0"), block.Block.Hash().Bytes())
	if e != nil {
		t.Fatal(e)
	}
	lease := uuid.NewString()
	if _, e = q.ClaimDeFiSource(ctx, &lease, source); e != nil {
		t.Fatal(e)
	}
	contender := uuid.NewString()
	if _, e = q.ClaimDeFiSource(ctx, &contender, source); e == nil {
		t.Fatal("second owner claimed active lease")
	}
	run := uuid.NewString()
	if e = q.InsertDeFiRun(ctx, dbgen.InsertDeFiRunParams{ID: run, SourceID: source, BlockNumber: defiNumeric("0"), BlockHash: block.Block.Hash().Bytes(), Attachment: attachment, CoverageStart: defiNumeric("0"), ScanNumber: defiNumeric("0"), ScanIndex: -1, State: "observations"}); e != nil {
		t.Fatal(e)
	}
	for _, contract := range []common.Address{token, testAddress(73)} {
		key := strings.ToLower(contract.Hex()) + "::" + strings.ToLower(owner.Hex())
		p := deficontract.Position{ID: key, Protocol: deficontract.UniswapV2, Deployment: "v2", Owner: owner.Hex(), Contract: contract.Hex(), Kind: "liquidity", State: "available", Assets: []deficontract.Asset{{Kind: "erc20", Address: token.Hex(), Amount: "9007199254740993"}}, Debts: []deficontract.Asset{}, Claimable: []deficontract.Asset{}, UnsettledFees: []deficontract.Asset{}, Limitations: []string{}, BlockNumber: "0", BlockHash: block.Block.Hash().Hex()}
		raw, _ := json.Marshal(p)
		if e = q.InsertDeFiCandidate(ctx, dbgen.InsertDeFiCandidateParams{RunID: run, Key: key, Owner: owner.Bytes(), Candidate: []byte(`{}`)}); e != nil {
			t.Fatal(e)
		}
		if e = q.InsertDeFiObservation(ctx, dbgen.InsertDeFiObservationParams{RunID: run, Key: key, Owner: owner.Bytes(), State: p.State, Position: raw}); e != nil {
			t.Fatal(e)
		}
		p.Assets[0].Amount = "1"
		conflict, _ := json.Marshal(p)
		if e = q.InsertDeFiObservation(ctx, dbgen.InsertDeFiObservationParams{RunID: run, Key: key, Owner: owner.Bytes(), State: p.State, Position: conflict}); e == nil {
			t.Fatal("different exact observation overwrote first")
		}
	}
	page, e = reader.Positions(ctx, req)
	if e != nil || len(page.Positions) != 0 {
		t.Fatal("unpublished observations visible", page, e)
	}
	e = dbaccess.WithTransaction(ctx, db, func(tx *dbgen.Queries) error {
		if _, err := tx.FenceDeFiSource(ctx, source, &lease); err != nil {
			return err
		}
		_, err := tx.AdvanceDeFiRun(ctx, dbgen.AdvanceDeFiRunParams{ID: run, State: "complete", ScanNumber: defiNumeric("0")})
		return err
	})
	if e != nil {
		t.Fatal(e)
	}
	page, e = reader.Positions(ctx, req)
	if e != nil || len(page.Positions) != 1 || page.NextCursor == "" || len(page.NextCursor) > 1024 {
		t.Fatal(page, e)
	}
	req.Cursor = page.NextCursor
	next, e := reader.Positions(ctx, req)
	if e != nil || len(next.Positions) != 1 || next.Positions[0].ID == page.Positions[0].ID {
		t.Fatal(next, e)
	}
	// API traversal cannot cross owner identity.
	other := req
	other.Owner = testAddress(74).Hex()
	if _, e = reader.Positions(ctx, other); !errors.Is(e, deficontract.ErrCursor) {
		t.Fatal(e)
	}
	// A detach/reattach of the same hash changes attachment identity immediately.
	if _, e = db.Exec(ctx, `UPDATE canonical_blocks SET updated_at=updated_at+interval '1 second' WHERE chain_id=1 AND number=0`); e != nil {
		t.Fatal(e)
	}
	if _, e = reader.Positions(ctx, req); !errors.Is(e, deficontract.ErrCursor) {
		t.Fatal("reattached cursor accepted", e)
	}
	req.Cursor = ""
	page, e = reader.Positions(ctx, req)
	if e != nil || len(page.Positions) != 0 {
		t.Fatal("invalidated publication leaked", page, e)
	}
	if _, e = db.Exec(ctx, `UPDATE defi_sources SET lease_until=$1 WHERE id=$2`, time.Now().Add(-time.Minute), source); e != nil {
		t.Fatal(e)
	}
	if _, e = q.FenceDeFiSource(ctx, source, &lease); e == nil {
		t.Fatal("expired lease accepted")
	}
	if _, e = q.ClaimDeFiSource(ctx, &contender, source); e != nil {
		t.Fatal(e)
	}
	if _, e = q.FenceDeFiSource(ctx, source, &lease); e == nil {
		t.Fatal("stale owner accepted after takeover")
	}
}
