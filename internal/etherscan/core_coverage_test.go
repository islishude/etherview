package etherscan

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestCanonicalCoreRangeRequiresOneInclusiveCoverageInterval(t *testing.T) {
	t.Parallel()
	end20, end30 := "20", "30"
	tests := []struct {
		name        string
		start       string
		end         *string
		expectation sqlExpectation
		wantTip     string
		want        error
	}{
		{
			name:  "exact island slice",
			start: "10", end: &end20,
			expectation: coreCoverageExpectation("10", "20", "20", "0", "10", "20"),
			wantTip:     "20",
		},
		{
			name:  "upper bound clamps to tip",
			start: "10", end: &end30,
			expectation: coreCoverageExpectation("10", "30", "20", "10", "10", "20"),
			wantTip:     "20",
		},
		{
			name:  "gap crossing has no containing interval",
			start: "5", end: &end20,
			expectation: coreCoverageExpectation("5", "20", "20", "0", nil, nil),
			want:        ErrCoreUnavailable,
		},
		{
			name:  "future-only range is empty at the boundary",
			start: "21", end: &end30,
			expectation: coreCoverageExpectation("21", "30", "20", "0", nil, nil),
			wantTip:     "20", want: ErrNotFound,
		},
		{
			name:  "missing tip is unavailable",
			start: "0", end: nil,
			expectation: sqlExpectation{
				contains: "FROM core_coverage_ranges AS candidate", columns: fakeColumns(4),
			},
			want: ErrCoreUnavailable,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			backend := testPostgresBackend(t, fakeDatabase(t, test.expectation), PostgresOptions{ChainID: 1})
			tip, err := backend.requireCanonicalCoreRange(context.Background(), backend.db, test.start, test.end)
			if tip != test.wantTip || !errors.Is(err, test.want) {
				t.Fatalf("tip=%q error=%v, want tip=%q error=%v", tip, err, test.wantTip, test.want)
			}
		})
	}
}

func TestCanonicalCoreRangeSQLProvesSingleContainingRange(t *testing.T) {
	t.Parallel()
	query := compactSQL(canonicalCoreRangeSQL)
	for _, required := range []string{
		"LEAST(COALESCE($3::numeric, tip.number), tip.number) AS range_end",
		"candidate.range_start <= requested.range_start",
		"candidate.range_end >= requested.range_end",
		"candidate.chain_id = configuration.chain_id",
	} {
		if !strings.Contains(query, compactSQL(required)) {
			t.Fatalf("canonical core coverage query does not contain %q: %s", compactSQL(required), query)
		}
	}
}

func TestCompatibilityRangeAndTimeActionsRejectIncompleteCoreCoverage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, module, action string
		values               url.Values
		expectation          sqlExpectation
	}{
		{
			name: "transaction range crosses gap", module: "account", action: "txlist",
			values:      url.Values{"address": {testSender}, "startblock": {"5"}, "endblock": {"20"}},
			expectation: coreCoverageExpectation("5", "20", "20", "0", nil, nil),
		},
		{
			name: "log range crosses gap", module: "logs", action: "getLogs",
			values:      url.Values{"fromBlock": {"5"}, "toBlock": {"20"}},
			expectation: coreCoverageExpectation("5", "20", "20", "0", nil, nil),
		},
		{
			name: "mined history starts after genesis", module: "account", action: "getminedblocks",
			values:      url.Values{"address": {testSender}},
			expectation: coreCoverageExpectation("0", "", "20", "10", nil, nil),
		},
		{
			name: "time lookup starts after genesis", module: "block", action: "getblocknobytime",
			values:      url.Values{"timestamp": {"100"}, "closest": {"before"}},
			expectation: coreCoverageExpectation("0", "", "20", "10", nil, nil),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			backend := testPostgresBackend(t, fakeDatabase(t, test.expectation), PostgresOptions{ChainID: 1})
			_, err := backend.Execute(context.Background(), Request{
				Module: test.module, Action: test.action, Values: test.values,
			})
			if !errors.Is(err, ErrCoreUnavailable) {
				t.Fatalf("error=%v, want core unavailable", err)
			}
		})
	}
}

func TestStageRangeRejectsMissingCoreBeforeEnrichmentProof(t *testing.T) {
	t.Parallel()
	db := fakeDatabase(t, coreCoverageExpectation("0", "", "20", "0", nil, nil))
	backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
	_, err := backend.requireCanonicalStageRange(
		context.Background(), db, tokenStage, "0", nil, ErrTokenUnavailable,
	)
	if !errors.Is(err, ErrCoreUnavailable) || errors.Is(err, ErrTokenUnavailable) {
		t.Fatalf("error=%v, want core unavailable before token-stage proof", err)
	}
}

func TestMissingInternalTransactionHashRequiresGlobalCoreCoverage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		coverage sqlExpectation
		want     error
	}{
		{
			name:     "gap is unavailable",
			coverage: coreCoverageExpectation("0", "", "20", "0", nil, nil),
			want:     ErrCoreUnavailable,
		},
		{
			name:     "complete history proves absence",
			coverage: completeCoreCoverageExpectation("0", "", "20"),
			want:     ErrNotFound,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			db := fakeDatabase(t,
				sqlExpectation{
					contains: "FROM transaction_inclusions AS inclusion JOIN canonical_blocks AS canonical",
					columns:  fakeColumns(1),
				},
				test.coverage,
			)
			backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
			_, err := backend.Execute(context.Background(), Request{
				Module: "account", Action: "txlistinternal",
				Values: url.Values{"txhash": {testHash(99)}},
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v, want %v", err, test.want)
			}
		})
	}
}

func TestMissingTransactionStatusRequiresGlobalCoreCoverage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		coverage sqlExpectation
		want     error
	}{
		{
			name:     "gap is unavailable",
			coverage: coreCoverageExpectation("0", "", "20", "0", nil, nil),
			want:     ErrCoreUnavailable,
		},
		{
			name:     "complete history proves absence",
			coverage: completeCoreCoverageExpectation("0", "", "20"),
			want:     ErrNotFound,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			db := fakeDatabase(t,
				sqlExpectation{
					contains: "FROM receipts AS receipt JOIN canonical_blocks AS canonical",
					columns:  fakeColumns(5),
				},
				test.coverage,
			)
			backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
			_, err := backend.Execute(context.Background(), Request{
				Module: "transaction", Action: "getstatus",
				Values: url.Values{"txhash": {testHash(99)}},
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v, want %v", err, test.want)
			}
		})
	}
}

func TestBlockCountdownNeverSamplesAcrossCoverageIslands(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		expectation sqlExpectation
		want        error
	}{
		{
			name: "missing tip coverage is unavailable",
			expectation: sqlExpectation{
				contains: "tip_coverage AS", columns: fakeColumns(8),
			},
			want: ErrCoreUnavailable,
		},
		{
			name: "single block tip island cannot estimate",
			expectation: sqlExpectation{
				contains: "tip_coverage AS", columns: fakeColumns(8),
				rows: [][]driver.Value{{"2", "102", "2", "102", "1", "0", "2", "2"}},
			},
			want: ErrEstimateUnavailable,
		},
		{
			name: "missing canonical height is rejected",
			expectation: sqlExpectation{
				contains: "tip_coverage AS", columns: fakeColumns(8),
				rows: [][]driver.Value{{"2", "102", "0", "100", "2", "0", "0", "2"}},
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			backend := testPostgresBackend(t, fakeDatabase(t, test.expectation), PostgresOptions{ChainID: 1})
			_, err := backend.Execute(context.Background(), Request{
				Module: "block", Action: "getblockcountdown", Values: url.Values{"blockno": {"4"}},
			})
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("error=%v, want %v", err, test.want)
			}
			if test.want == nil && (err == nil || !strings.Contains(err.Error(), "not continuous")) {
				t.Fatalf("error=%v, want continuity failure", err)
			}
		})
	}
}

func completeCoreCoverageExpectation(start, end, tip string) sqlExpectation {
	return coreCoverageExpectation(start, end, tip, "0", "0", tip)
}

func coreCoverageExpectation(start, end, tip string, configured, coveredStart, coveredEnd driver.Value) sqlExpectation {
	return sqlExpectation{
		contains: "FROM core_coverage_ranges AS candidate",
		columns:  fakeColumns(4),
		rows:     [][]driver.Value{{tip, configured, coveredStart, coveredEnd}},
		check: func(arguments []driver.NamedValue) error {
			if len(arguments) != 3 || arguments[0].Value != "1" || arguments[1].Value != start {
				return fmt.Errorf("core coverage arguments=%v", arguments)
			}
			if end == "" && arguments[2].Value != nil || end != "" && arguments[2].Value != end {
				return fmt.Errorf("core coverage end argument=%v want=%q", arguments[2].Value, end)
			}
			return nil
		},
	}
}
