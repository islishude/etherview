package enrich

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestDecodeBlockTraceResultsBindsCanonicalTransactionOrder(t *testing.T) {
	t.Parallel()
	first, second := uintWord(501), uintWord(502)
	results, err := decodeBlockTraceResults(
		repeatedBlockTraceResponse(t, json.RawMessage(`{"ok":true}`), first, second),
		[]common.Hash{first, second},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || string(results[0].result) != `{"ok":true}` ||
		string(results[1].result) != `{"ok":true}` || results[0].err != nil || results[1].err != nil {
		t.Fatalf("results=%+v", results)
	}
}

func TestDecodeBlockTraceResultsRejectsMalformedEnvelopes(t *testing.T) {
	t.Parallel()
	first, second := uintWord(511), uintWord(512)
	valid := json.RawMessage(`{"ok":true}`)
	for _, test := range []struct {
		name string
		raw  json.RawMessage
	}{
		{name: "null", raw: json.RawMessage(`null`)},
		{name: "object", raw: json.RawMessage(`{}`)},
		{name: "missing transaction", raw: repeatedBlockTraceResponse(t, valid, first)},
		{name: "reordered", raw: repeatedBlockTraceResponse(t, valid, second, first)},
		{name: "duplicate", raw: repeatedBlockTraceResponse(t, valid, first, first)},
		{name: "missing hash", raw: json.RawMessage(`[{"result":{}},{"txHash":"` + second.String() + `","result":{}}]`)},
		{name: "invalid hash", raw: json.RawMessage(`[{"txHash":"0x01","result":{}},{"txHash":"` + second.String() + `","result":{}}]`)},
		{name: "result and error", raw: json.RawMessage(`[{"txHash":"` + first.String() + `","result":{},"error":"failed"},{"txHash":"` + second.String() + `","result":{}}]`)},
		{name: "result and null error", raw: json.RawMessage(`[{"txHash":"` + first.String() + `","result":{},"error":null},{"txHash":"` + second.String() + `","result":{}}]`)},
		{name: "missing result and error", raw: json.RawMessage(`[{"txHash":"` + first.String() + `"},{"txHash":"` + second.String() + `","result":{}}]`)},
		{name: "null result", raw: json.RawMessage(`[{"txHash":"` + first.String() + `","result":null},{"txHash":"` + second.String() + `","result":{}}]`)},
		{name: "empty error", raw: json.RawMessage(`[{"txHash":"` + first.String() + `","error":""},{"txHash":"` + second.String() + `","result":{}}]`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := decodeBlockTraceResults(test.raw, []common.Hash{first, second}); err == nil {
				t.Fatal("accepted malformed block trace response")
			}
		})
	}
}

func TestDecodeBlockTraceResultsSanitizesItemErrors(t *testing.T) {
	t.Parallel()
	hash := uintWord(521)
	for _, test := range []struct {
		name    string
		message string
		want    error
	}{
		{name: "history unavailable", message: "secret missing trie node detail", want: errBlockTraceHistoryUnavailable},
		{name: "retryable", message: "secret tracer timeout detail", want: errBlockTraceTransactionFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			raw := blockTraceResponse(t, []common.Hash{hash}, []json.RawMessage{nil}, map[int]string{0: test.message})
			results, err := decodeBlockTraceResults(raw, []common.Hash{hash})
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || !errors.Is(results[0].err, test.want) || strings.Contains(results[0].err.Error(), "secret") {
				t.Fatalf("results=%+v", results)
			}
		})
	}
}
