package enssync

import (
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enscontract"
	"testing"
	"time"
)

func TestEffectiveFallbackRequiresDefinitiveNegative(t *testing.T) {
	policy := dbgen.EnsEffectivePolicy{Mode: "official_first", OfficialSourceID: optionalID(1), CustomSourceID: optionalID(2)}
	custom := enscontract.Result{State: enscontract.Resolved, Source: enscontract.Custom, Name: "alice.test", ValidUntil: time.Now().Add(time.Hour)}
	for _, code := range []enscontract.Code{enscontract.NotIndexed, enscontract.SyncPending, enscontract.ResolutionPending, enscontract.Stale, enscontract.UnsupportedSource} {
		official := enscontract.Result{State: enscontract.Unavailable, Code: code}
		got := selectEffective(policy, official, custom)
		if got.State != enscontract.Unavailable || got.Code != code || got.Name != "" {
			t.Fatalf("unsafe fallback %+v", got)
		}
	}
	deadline := time.Now().Add(time.Minute)
	got := selectEffective(policy, enscontract.Result{State: enscontract.NotFound, Source: enscontract.Official, ValidUntil: deadline}, custom)
	if got.Source != enscontract.Custom || !got.ValidUntil.Equal(deadline) {
		t.Fatal("negative freshness must cap fallback", got)
	}
	policy.Mode = "custom_only"
	got = selectEffective(policy, enscontract.Result{State: enscontract.Unavailable}, custom)
	if got.State != enscontract.Resolved {
		t.Fatal(got)
	}
}
