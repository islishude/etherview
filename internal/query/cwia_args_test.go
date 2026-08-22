package query

import (
	"encoding/json"
	"testing"

	"github.com/islishude/etherview/internal/cwiaargs"
	dbgen "github.com/islishude/etherview/internal/db/gen"
)

func TestSelectCWIAAnalysisPrefersExactAndRequiresConsensus(t *testing.T) {
	t.Parallel()
	first := testCWIAAnalysis(t, "owner")
	second := testCWIAAnalysis(t, "account")
	firstRaw, err := cwiaargs.MarshalAnalysis(first)
	if err != nil {
		t.Fatal(err)
	}
	secondRaw, err := cwiaargs.MarshalAnalysis(second)
	if err != nil {
		t.Fatal(err)
	}

	selected, resolution := selectCWIAAnalysis([]dbgen.GetCWIAImplementationAnalysesRow{
		{Exact: true, Analysis: firstRaw},
		{Exact: false, Analysis: secondRaw},
	})
	if resolution != cwiaargs.ResolutionExactAddress ||
		cwiaargs.AnalysisDigest(selected) != cwiaargs.AnalysisDigest(first) {
		t.Fatalf("selected=%+v resolution=%q", selected, resolution)
	}

	conflict, resolution := selectCWIAAnalysis([]dbgen.GetCWIAImplementationAnalysesRow{
		{Analysis: json.RawMessage(firstRaw)}, {Analysis: string(secondRaw)},
	})
	if resolution != cwiaargs.ResolutionCodeHash || conflict.Status != cwiaargs.AnalysisStatusInvalid ||
		conflict.Reason != cwiaargs.ReasonConflict {
		t.Fatalf("conflict=%+v resolution=%q", conflict, resolution)
	}
}

func TestSelectCWIAAnalysisFailsClosedForMissingMalformedAndExcessiveRows(t *testing.T) {
	t.Parallel()
	missing, resolution := selectCWIAAnalysis(nil)
	if resolution != "" || missing.Status != cwiaargs.AnalysisStatusMissing {
		t.Fatalf("missing=%+v resolution=%q", missing, resolution)
	}
	malformed, _ := selectCWIAAnalysis([]dbgen.GetCWIAImplementationAnalysesRow{{Analysis: []byte(`{"status":`)}})
	if malformed.Status != cwiaargs.AnalysisStatusInvalid || malformed.Reason != cwiaargs.ReasonMalformed {
		t.Fatalf("malformed=%+v", malformed)
	}
	rows := make([]dbgen.GetCWIAImplementationAnalysesRow, 17)
	for index := range rows {
		rows[index].Analysis = map[string]any{"status": "ignored"}
	}
	limited, _ := selectCWIAAnalysis(rows)
	if limited.Status != cwiaargs.AnalysisStatusInvalid || limited.Reason != cwiaargs.ReasonLimit {
		t.Fatalf("limited=%+v", limited)
	}
}

func testCWIAAnalysis(t *testing.T, name string) cwiaargs.Analysis {
	t.Helper()
	schema, err := cwiaargs.FinalizeSchema([]cwiaargs.Field{{
		Name: name, Type: "address", Offset: 0, Role: cwiaargs.FieldRoleValue,
		Getters: []string{name + "()"}, Size: cwiaargs.FixedSize(20),
	}})
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := cwiaargs.DerivedAnalysis(schema)
	if err != nil {
		t.Fatal(err)
	}
	return analysis
}
