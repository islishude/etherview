package query

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/islishude/etherview/internal/cwiaargs"
	dbgen "github.com/islishude/etherview/internal/db/gen"
)

const (
	CWIAArgsDecoded           = cwiaargs.StatusDecoded
	CWIAArgsSchemaUnavailable = cwiaargs.StatusSchemaUnavailable
	CWIAArgsSchemaInvalid     = cwiaargs.StatusSchemaInvalid
	CWIAArgsDataInvalid       = cwiaargs.StatusDataInvalid

	CWIAArgsReasonASTUnavailable = cwiaargs.ReasonASTUnavailable
	CWIAArgsReasonMalformed      = cwiaargs.ReasonMalformed
	CWIAArgsReasonUnsupported    = cwiaargs.ReasonUnsupported
	CWIAArgsReasonAmbiguous      = cwiaargs.ReasonAmbiguous
	CWIAArgsReasonIncomplete     = cwiaargs.ReasonIncomplete
	CWIAArgsReasonConflict       = cwiaargs.ReasonConflict
	CWIAArgsReasonLimitExceeded  = cwiaargs.ReasonLimit
	CWIAArgsReasonLengthMismatch = cwiaargs.ReasonLengthMismatch
	CWIAArgsReasonNoncanonical   = cwiaargs.ReasonNoncanonical
)

type CWIAImmutableArgField = cwiaargs.Field
type CWIAImmutableArgSchema = cwiaargs.Schema
type CWIAImmutableArgValue = cwiaargs.Value
type CWIAImmutableArgsDecoding = cwiaargs.Decoding

func selectCWIAAnalysis(
	rows []dbgen.GetCWIAImplementationAnalysesRow,
) (cwiaargs.Analysis, string) {
	if len(rows) == 0 {
		return cwiaargs.UnavailableAnalysis(), ""
	}
	exact := rows[0].Exact
	selected := make([]dbgen.GetCWIAImplementationAnalysesRow, 0, len(rows))
	for _, row := range rows {
		if row.Exact == exact {
			selected = append(selected, row)
		}
	}
	resolution := cwiaargs.ResolutionCodeHash
	if exact {
		resolution = cwiaargs.ResolutionExactAddress
	}
	if len(selected) > 16 {
		return cwiaargs.InvalidAnalysis(cwiaargs.ReasonLimit), resolution
	}
	var canonical []byte
	var chosen cwiaargs.Analysis
	for _, row := range selected {
		raw, err := cwiaAnalysisBytes(row.Analysis)
		if err != nil {
			return cwiaargs.InvalidAnalysis(cwiaargs.ReasonMalformed), resolution
		}
		analysis, err := cwiaargs.ParseAnalysis(raw)
		if err != nil {
			return cwiaargs.InvalidAnalysis(cwiaargs.ReasonMalformed), resolution
		}
		normalized, err := cwiaargs.MarshalAnalysis(analysis)
		if err != nil {
			return cwiaargs.InvalidAnalysis(cwiaargs.ReasonMalformed), resolution
		}
		if canonical == nil {
			canonical = normalized
			chosen = analysis
			continue
		}
		if !bytes.Equal(canonical, normalized) {
			return cwiaargs.InvalidAnalysis(cwiaargs.ReasonConflict), resolution
		}
	}
	return chosen, resolution
}

func cwiaAnalysisBytes(value any) ([]byte, error) {
	switch typed := value.(type) {
	case []byte:
		return append([]byte(nil), typed...), nil
	case string:
		return []byte(typed), nil
	case json.RawMessage:
		return append([]byte(nil), typed...), nil
	case nil:
		return nil, errors.New("CWIA AST analysis is missing")
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return nil, errors.New("CWIA AST analysis cannot be encoded")
		}
		return encoded, nil
	}
}
