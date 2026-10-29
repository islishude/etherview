//go:build integration

package integration_test

import (
	"encoding/json"
	"errors"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enscontract"
	"github.com/islishude/etherview/internal/ensread"
	"github.com/islishude/etherview/internal/enssync"
	"github.com/islishude/etherview/internal/query"
	"strconv"
	"testing"
	"time"
)

func TestENSRegistrationProfileIndependentPublication(t *testing.T) {
	f := newENSSyncFixture(t)
	ctx := t.Context()
	f.discover(t)
	if e := f.repo.RegisterPolicy(ctx, enssync.EffectivePolicy{TargetChainID: "31337", CoinType: "60", Digest: f.lookup.PolicyDigest, SourceID: f.source}); e != nil {
		t.Fatal(e)
	}
	readonly := &ensReadOnlyDB{Pool: f.db}
	reader, e := ensread.New(readonly, ensread.Options{ChainID: 31337, Policy: f.lookup.PolicyDigest, MaxBatch: 100, SnapshotTTL: time.Minute})
	if e != nil {
		t.Fatal(e)
	}
	detail, e := reader.NameDetail(ctx, "alice.eth")
	if e != nil || detail.State != "indexed" || detail.Registrant.State != "pending" {
		t.Fatalf("known before resolution %+v %v", detail, e)
	}
	unknown, e := reader.NameDetail(ctx, "unknown.eth")
	if e != nil || unknown.State != "not_indexed" {
		t.Fatalf("unknown %+v %v", unknown, e)
	}
	c, e := f.q.EnsureENSSyncCandidate(ctx, dbgen.EnsureENSSyncCandidateParams{SourceID: f.source, Direction: "profile", LookupKey: "alice.eth", CoinType: ensNumber("60"), OriginKind: "import", OriginIdentity: "batch-1"})
	if e != nil {
		t.Fatal(e)
	}
	forwardCandidate := f.candidate
	f.candidate = c.ID
	f.enqueue(t, 3)
	job := f.claim(t)
	input := f.publication(job)
	profile := enscontract.RegistrationProfile{RegistrationState: "expired", Registrant: enscontract.Field{State: "not_applicable"}, Controller: enscontract.Field{State: "not_applicable"}, ExpirationTime: enscontract.Field{State: "value", Value: "1700000000"}}
	input.ProfileData, _ = json.Marshal(profile)
	input.Address = nil
	input.Resolver = nil
	if _, e = f.repo.Publish(ctx, input); e != nil {
		t.Fatal(e)
	}
	detail, e = reader.NameDetail(ctx, "alice.eth")
	if e != nil || detail.RegistrationState != "expired" || detail.ResolvedAddress.State != "pending" || detail.ProfileObservation == nil || detail.ResolutionObservation != nil {
		t.Fatalf("profile independent %+v %v", detail, e)
	}
	profileCandidate := f.candidate
	f.candidate = forwardCandidate
	f.enqueue(t, 3)
	forwardJob := f.claim(t)
	forward := f.publication(forwardJob)
	forward.Outcome = "not_found"
	forward.Address = nil
	forward.Resolver = nil
	if _, e = f.repo.Publish(ctx, forward); e != nil {
		t.Fatal(e)
	}
	detail, e = reader.NameDetail(ctx, "alice.eth")
	if e != nil || detail.ResolvedAddress.State != "not_set" || detail.RegistrationState != "expired" {
		t.Fatalf("negative forward %+v %v", detail, e)
	}
	if _, e = f.q.InvalidateENSSyncCandidate(ctx, f.source, profileCandidate, 1); e != nil {
		t.Fatal(e)
	}
	detail, e = reader.NameDetail(ctx, "alice.eth")
	if e != nil || detail.Registrant.State != "pending" || detail.ResolvedAddress.State != "not_set" || detail.State != "indexed" {
		t.Fatalf("independent invalidation %+v %v", detail, e)
	}
	assertRowCount(t, ctx, f.db, `SELECT count(*) FROM search_catalog_documents WHERE source_kind='name' AND result_kind='name' AND result_key='alice.eth' AND valid_to_generation IS NULL`, 1)
	// The same retained publication cannot keep an event-only name in the
	// directory after its discovery block detaches. Operator imports are separate.
	execFixture(t, ctx, f.db, `DELETE FROM ens_sync_candidate_origins WHERE candidate_id=$1`, forwardCandidate)
	execFixture(t, ctx, f.db, `INSERT INTO ens_sync_candidate_origins(source_id,candidate_id,kind,identity) VALUES($1,$2,'event',$3)`, f.source, forwardCandidate, f.block.Hex()+"/0")
	detail, e = reader.NameDetail(ctx, "alice.eth")
	if e != nil || detail.State != "indexed" {
		t.Fatalf("event known %+v %v", detail, e)
	}
	execFixture(t, ctx, f.db, `DELETE FROM canonical_blocks WHERE chain_id=31337 AND block_hash=$1`, f.block.Bytes())
	detail, e = reader.NameDetail(ctx, "alice.eth")
	if e != nil || detail.State != "not_indexed" || detail.ProfileObservation != nil || detail.ResolutionObservation != nil {
		t.Fatalf("orphan name %+v %v", detail, e)
	}

}

func TestENSNameDirectoryKeepsAliasesAndRejectsChangedPagination(t *testing.T) {
	f := newENSSyncFixture(t)
	ctx := t.Context()
	policy := enssync.EffectivePolicy{TargetChainID: "31337", CoinType: "60", Digest: f.lookup.PolicyDigest, SourceID: f.source}
	if e := f.repo.RegisterPolicy(ctx, policy); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"alice.eth", "bob.eth"} {
		c, e := f.q.EnsureENSSyncCandidate(ctx, dbgen.EnsureENSSyncCandidateParams{SourceID: f.source, Direction: "forward", LookupKey: name, CoinType: ensNumber("60"), OriginKind: "import", OriginIdentity: "aliases"})
		if e != nil {
			t.Fatal(e)
		}
		f.candidate = c.ID
		f.enqueue(t, 3)
		input := f.publication(f.claim(t))
		input.Name = &name
		if _, e = f.repo.Publish(ctx, input); e != nil {
			t.Fatal(e)
		}
	}
	reader, e := query.NewPostgresReader(&ensReadOnlyDB{Pool: f.db}, query.Options{ChainID: 31337, NamePolicy: policy.Digest})
	if e != nil {
		t.Fatal(e)
	}
	c, e := f.q.EnsureENSSyncCandidate(ctx, dbgen.EnsureENSSyncCandidateParams{SourceID: f.source, Direction: "profile", LookupKey: "alice.eth", CoinType: ensNumber("60"), OriginKind: "import", OriginIdentity: "aliases-profile"})
	if e != nil {
		t.Fatal(e)
	}
	f.candidate = c.ID
	f.enqueue(t, 3)
	input := f.publication(f.claim(t))
	expiry := time.Now().Add(3 * time.Second).Unix()
	input.Address, input.Resolver = nil, nil
	input.ProfileData, _ = json.Marshal(enscontract.RegistrationProfile{RegistrationState: "registered", Registrant: enscontract.Field{State: "value", Value: "0x0000000000000000000000000000000000000001"}, Controller: enscontract.Field{State: "multi_account"}, ExpirationTime: enscontract.Field{State: "value", Value: strconv.FormatInt(expiry, 10)}})
	if _, e = f.repo.Publish(ctx, input); e != nil {
		t.Fatal(e)
	}
	_, expiringCursor, e := reader.Search(ctx, "eth", "", 1)
	if e != nil || expiringCursor == "" {
		t.Fatalf("expiry cursor %v", e)
	}
	time.Sleep(time.Until(time.Unix(expiry, 0)) + 10*time.Millisecond)
	if _, _, e = reader.Search(ctx, "eth", expiringCursor, 1); !errors.Is(e, query.ErrInvalidCursor) {
		t.Fatalf("registration expiry cursor %v", e)
	}
	first, cursor, e := reader.Search(ctx, "eth", "", 1)
	if e != nil || len(first) != 1 || cursor == "" || first[0].Key != "alice.eth" {
		t.Fatalf("first %+v %q %v", first, cursor, e)
	}
	next, _, e := reader.Search(ctx, "eth", cursor, 1)
	if e != nil || len(next) != 1 || next[0].Key != "bob.eth" || first[0].NameDetails == nil || next[0].NameDetails == nil || *first[0].NameDetails.ResolvedAddress.Value != *next[0].NameDetails.ResolvedAddress.Value {
		t.Fatalf("distinct aliases %+v %v", next, e)
	}
	if _, e = f.repo.ImportCandidates(ctx, enssync.OperationScope{SourceID: f.source, ChainID: "31337", Policy: policy.Digest}, "new-directory", []string{"unresolved.eth"}); e != nil {
		t.Fatal(e)
	}
	if _, _, e = reader.Search(ctx, "eth", cursor, 1); !errors.Is(e, query.ErrInvalidCursor) {
		t.Fatalf("changed snapshot %v", e)
	}
	all, _, e := reader.Search(ctx, "eth", "", 10)
	if e != nil || len(all) != 3 || all[2].Key != "unresolved.eth" || all[2].NameDetails.ResolvedAddress.State != "pending" {
		t.Fatalf("unresolved directory %+v %v", all, e)
	}
}
