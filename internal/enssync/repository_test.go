package enssync

import (
	"math/big"
	"testing"
	"time"

	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enscontract"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestSourcePublicationFreshnessIsNotRetention(t *testing.T) {
	now := time.Unix(1000, 0)
	generation, revision, outcome, name := int64(1), int64(2), "resolved", "alice.eth"
	row := dbgen.ReadENSSyncCandidateRow{SourceState: "active", Revision: revision,
		RequestedGeneration: generation, MinimumGeneration: generation, Generation: &generation, SourceRevision: &revision,
		HealthyUntil: pgtype.Timestamptz{Time: now.Add(time.Minute), Valid: true},
		TargetHash:   []byte{1}, ScannedHash: []byte{1}, Canonical: true, CoverageCanonical: true,
		PublicationID: 3, Outcome: &outcome, Name: &name,
		ValidUntil:  pgtype.Timestamptz{Time: now.Add(2 * time.Minute), Valid: true},
		RetainUntil: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true}}
	result := readResult(row, now)
	if result.State != enscontract.Resolved || !result.ValidUntil.Equal(row.ValidUntil.Time) {
		t.Fatalf("observation freshness must cap display: %+v", result)
	}
	lagging := row
	lagging.ScannedHash = nil
	lagging.CoverageCanonical = false
	lagging.HealthyUntil.Time = now
	if got := readResult(lagging, now); got.State != enscontract.Resolved {
		t.Fatalf("coverage hid exact observation: %+v", got)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*dbgen.ReadENSSyncCandidateRow)
		code   enscontract.Code
	}{
		{"expired observation", func(r *dbgen.ReadENSSyncCandidateRow) { r.ValidUntil.Time = now }, enscontract.Stale},
		{"retired publication", func(r *dbgen.ReadENSSyncCandidateRow) { r.RetainUntil.Time = now }, enscontract.Stale},
		{"observation detached", func(r *dbgen.ReadENSSyncCandidateRow) { r.Canonical = false }, enscontract.ResolutionPending},
		{"projection changed", func(r *dbgen.ReadENSSyncCandidateRow) { r.Revision++ }, enscontract.ResolutionPending},
		{"replay", func(r *dbgen.ReadENSSyncCandidateRow) { r.RequestedGeneration++; r.MinimumGeneration++ }, enscontract.ResolutionPending},
		{"unknown implementation", func(r *dbgen.ReadENSSyncCandidateRow) { r.SourceState = "unsupported" }, enscontract.UnsupportedSource},
		{"finalized conflict", func(r *dbgen.ReadENSSyncCandidateRow) { r.SourceState = "halted" }, enscontract.UnsupportedSource},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := row
			tc.mutate(&changed)
			got := readResult(changed, now)
			if got.State != enscontract.Unavailable || got.Code != tc.code || got.Name != "" || got.PublicationID != 0 {
				t.Fatalf("invalid label leaked: %+v", got)
			}
		})
	}
}

func TestENSSyncExactNumericInput(t *testing.T) {
	for _, value := range []string{"0", "60", "9007199254740993", "115792089237316195423570985008687907853269984665640564039457584007913129639935"} {
		n, ok := unsignedNumeric(value)
		if !ok || n.Int.String() != value {
			t.Fatalf("lost exact value %s", value)
		}
	}
	for _, value := range []string{"", "-1", "+1", "01", "1e2", "1.0", "NaN", "115792089237316195423570985008687907853269984665640564039457584007913129639936"} {
		if _, ok := unsignedNumeric(value); ok {
			t.Fatalf("accepted %q", value)
		}
	}
}

func TestNumericIdentityIncludesPostgresExponent(t *testing.T) {
	for _, tc := range []struct {
		coefficient string
		exponent    int32
		value       string
		different   string
	}{
		{"6", 1, "60", "6"}, {"1", 2, "100", "10"}, {"123", -1, "12", "123"}, {"2147483648", 0, "2147483648", "21474836480"},
	} {
		coefficient, _ := new(big.Int).SetString(tc.coefficient, 10)
		encoded := pgtype.Numeric{Int: coefficient, Exp: tc.exponent, Valid: true}
		value, _ := unsignedNumeric(tc.value)
		different, _ := unsignedNumeric(tc.different)
		want := tc.exponent >= 0
		if equalUnsignedNumeric(encoded, value) != want || equalUnsignedNumeric(encoded, different) {
			t.Fatalf("incorrect numeric identity for %+v", tc)
		}
	}
}
