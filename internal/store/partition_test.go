package store

import (
	"database/sql"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestPartitionRangesAreFixedAlignedAndBounded(t *testing.T) {
	span, err := validatePartitionRequest(999_999, 1_000_001, DefaultPartitionSpan)
	if err != nil || span != DefaultPartitionSpan {
		t.Fatalf("validate boundary request: span=%d error=%v", span, err)
	}
	if got, want := partitionRangeStarts(999_999, 1_000_001, span), []uint64{0, 1_000_000}; !reflect.DeepEqual(got, want) {
		t.Fatalf("partition ranges = %v, want %v", got, want)
	}
	if _, err := validatePartitionRequest(0, 1, DefaultPartitionSpan/2); err == nil {
		t.Fatal("non-fixed partition span was accepted")
	}
	lastLower := (math.MaxUint64 / DefaultPartitionSpan) * DefaultPartitionSpan
	if _, err := validatePartitionRequest(lastLower, math.MaxUint64, DefaultPartitionSpan); err == nil {
		t.Fatal("unrepresentable final partition upper bound was accepted")
	}
}

func TestChainWritesRefreshSnapshotsAfterAdvisoryLockWait(t *testing.T) {
	if chainWriteIsolation != sql.LevelReadCommitted {
		t.Fatalf("chain write isolation = %v, want read committed", chainWriteIsolation)
	}
}

func TestPartitionNamesAndPostgresBoundsRemainDeterministic(t *testing.T) {
	lower := ((math.MaxUint64 - DefaultPartitionSpan) / DefaultPartitionSpan) * DefaultPartitionSpan
	upper := lower + DefaultPartitionSpan
	for _, spec := range blockPartitionSpecs {
		name := partitionName(spec, lower, upper)
		if len(name) > 63 {
			t.Fatalf("partition name %q is %d bytes, exceeds PostgreSQL limit", name, len(name))
		}
		if !strings.HasPrefix(name, "etherview_p_"+spec.NameCode+"_") {
			t.Fatalf("partition name %q does not include stable table code", name)
		}
	}
	got := normalizePartitionBound("FOR VALUES FROM ('1000000'::numeric) TO ('2000000'::numeric)")
	if want := normalizedPartitionBound(1_000_000, 2_000_000); got != want {
		t.Fatalf("normalized PostgreSQL bound = %q, want %q", got, want)
	}
}
