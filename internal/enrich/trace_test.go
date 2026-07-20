package enrich

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const (
	traceAddress1 = "0x0000000000000000000000000000000000000001"
	traceAddress2 = "0x0000000000000000000000000000000000000002"
	traceAddress3 = "0x0000000000000000000000000000000000000003"
	traceAddress4 = "0x0000000000000000000000000000000000000004"
	traceAddress5 = "0x0000000000000000000000000000000000000005"
)

func traceTestIdentity() TraceIdentity {
	return TraceIdentity{
		BlockHash:        traceTestWord(12),
		BlockNumber:      12,
		TransactionHash:  traceTestWord(120),
		TransactionIndex: 3,
	}
}

func traceTestWord(value uint64) Word {
	var word Word
	for index := 0; index < 8; index++ {
		word[31-index] = byte(value)
		value >>= 8
	}
	return word
}

func traceAPIIdentityFields(identity TraceIdentity) map[string]any {
	return map[string]any{
		"blockHash":           identity.BlockHash.String(),
		"blockNumber":         identity.BlockNumber,
		"transactionHash":     identity.TransactionHash.String(),
		"transactionPosition": identity.TransactionIndex,
	}
}

func mergeTraceFixture(identity TraceIdentity, fields map[string]any) map[string]any {
	result := traceAPIIdentityFields(identity)
	for key, value := range fields {
		result[key] = value
	}
	return result
}

func marshalTraceFixture(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestNormalizeCallTracerHandlesNestedRevertsAndCallKinds(t *testing.T) {
	t.Parallel()
	data := []byte(`{
      "type":"CALL","from":"` + traceAddress1 + `","to":"` + traceAddress2 + `",
      "value":"0x5","gas":"0x100","gasUsed":"0x80","input":"0x1234","output":"0x",
      "calls":[
        {"type":"DELEGATECALL","from":"` + traceAddress2 + `","to":"` + traceAddress3 + `",
         "value":"0x0","gas":"0x40","gasUsed":"0x20","input":"0x","output":"0xdeadbeef",
         "error":"execution reverted","revertReason":"custom error 0xdeadbeef",
         "calls":[{"type":"SELFDESTRUCT","from":"` + traceAddress3 + `","to":"` + traceAddress5 + `","value":"0x1"}]},
        {"type":"CREATE2","from":"` + traceAddress2 + `","to":"` + traceAddress4 + `",
         "value":"0x0","gas":"0x20","gasUsed":"0x10","input":"0x6000","output":"0x6000"}
      ]
    }`)
	trace, err := NormalizeCallTracer(data, DefaultTraceLimits())
	if err != nil {
		t.Fatal(err)
	}
	if trace.State != TraceComplete || trace.Source != TraceCallTracer || len(trace.Frames) != 4 {
		t.Fatalf("trace=%+v", trace)
	}
	delegate, destroyed, created := trace.Frames[1], trace.Frames[2], trace.Frames[3]
	if delegate.Type != "DELEGATECALL" || !delegate.DirectReverted || !delegate.Reverted ||
		delegate.RevertReason != "custom error 0xdeadbeef" || string(delegate.Output) != string([]byte{0xde, 0xad, 0xbe, 0xef}) {
		t.Fatalf("delegate=%+v", delegate)
	}
	if destroyed.Type != "SELFDESTRUCT" || destroyed.DirectReverted || !destroyed.Reverted || destroyed.ParentIndex != 1 {
		t.Fatalf("destroyed=%+v", destroyed)
	}
	if created.Type != "CREATE2" || created.Reverted || created.ParentIndex != 0 {
		t.Fatalf("created=%+v", created)
	}
}

func TestNormalizeTraceAPIValidatesIdentityAndNormalizesTree(t *testing.T) {
	t.Parallel()
	identity := traceTestIdentity()
	frames := []map[string]any{
		mergeTraceFixture(identity, map[string]any{
			"type": "create", "traceAddress": []uint64{1}, "subtraces": 0,
			"action": map[string]any{"creationMethod": "create2", "from": traceAddress2, "value": "0x0", "gas": "0x20", "init": "0x6000"},
			"result": map[string]any{"address": traceAddress4, "gasUsed": "0x10", "code": "0x6000"},
		}),
		mergeTraceFixture(identity, map[string]any{
			"type": "call", "traceAddress": []uint64{}, "subtraces": 3, "error": "Reverted",
			"action": map[string]any{"callType": "call", "from": traceAddress1, "to": traceAddress2, "value": "0x5", "gas": "0x100", "input": "0x1234"},
		}),
		mergeTraceFixture(identity, map[string]any{
			"type": "suicide", "traceAddress": []uint64{2}, "subtraces": 0,
			"action": map[string]any{"address": traceAddress4, "refundAddress": traceAddress5, "balance": "0x1"},
		}),
		mergeTraceFixture(identity, map[string]any{
			"type": "call", "traceAddress": []uint64{0}, "subtraces": 0,
			"action": map[string]any{"callType": "delegatecall", "from": traceAddress2, "to": traceAddress3, "value": "0x0", "gas": "0x40", "input": "0x"},
			"result": map[string]any{"gasUsed": "0x20", "output": "0xdeadbeef"},
		}),
	}
	trace, err := NormalizeTraceAPI(marshalTraceFixture(t, frames), DefaultTraceLimits(), identity)
	if err != nil {
		t.Fatal(err)
	}
	if trace.Source != TraceAPI || len(trace.Frames) != 4 || len(trace.Frames[0].TraceAddress) != 0 {
		t.Fatalf("trace=%+v", trace)
	}
	if trace.Frames[1].Type != "DELEGATECALL" || trace.Frames[2].Type != "CREATE2" || trace.Frames[3].Type != "SELFDESTRUCT" {
		t.Fatalf("frames=%+v", trace.Frames)
	}
	for _, frame := range trace.Frames[1:] {
		if frame.ParentIndex != 0 || !frame.Reverted {
			t.Fatalf("descendant did not inherit root revert: %+v", frame)
		}
	}
}

func TestNormalizeTraceAPIRejectsEmptyAndInconsistentResponses(t *testing.T) {
	t.Parallel()
	identity := traceTestIdentity()
	root := mergeTraceFixture(identity, map[string]any{
		"type": "call", "traceAddress": []uint64{}, "subtraces": 0,
		"action": map[string]any{"callType": "call", "from": traceAddress1, "to": traceAddress2, "value": "0x0", "gas": "0x1", "input": "0x"},
		"result": map[string]any{"gasUsed": "0x1", "output": "0x"},
	})
	if _, err := NormalizeTraceAPI([]byte(`[]`), DefaultTraceLimits(), identity); err == nil {
		t.Fatal("accepted an empty trace_transaction response as a complete transaction trace")
	}
	mismatch := mergeTraceFixture(identity, root)
	mismatch["transactionHash"] = traceTestWord(121).String()
	if _, err := NormalizeTraceAPI(marshalTraceFixture(t, []any{mismatch}), DefaultTraceLimits(), identity); err == nil {
		t.Fatal("accepted a trace response for another transaction")
	}
	root["subtraces"] = 1
	if _, err := NormalizeTraceAPI(marshalTraceFixture(t, []any{root}), DefaultTraceLimits(), identity); err == nil {
		t.Fatal("accepted an incomplete trace tree")
	}
	root["subtraces"] = 0
	root["blockNumber"] = "0xc"
	root["transactionPosition"] = "3"
	if _, err := NormalizeTraceAPI(marshalTraceFixture(t, []any{root}), DefaultTraceLimits(), identity); err != nil {
		t.Fatalf("rejected supported trace identity number encodings: %v", err)
	}
}

func TestTraceLimitsAndMalformedData(t *testing.T) {
	t.Parallel()
	validRoot := []byte(`{"type":"CALL","from":"` + traceAddress1 + `","to":"` + traceAddress2 + `","value":"0x0","gas":"0x1","gasUsed":"0x1","input":"0x","output":"0x"}`)
	tests := []struct {
		name   string
		data   []byte
		change func(*TraceLimits)
	}{
		{name: "payload", data: validRoot, change: func(limits *TraceLimits) { limits.MaxPayloadBytes = len(validRoot) - 1 }},
		{name: "frames", data: []byte(`{"type":"CALL","from":"` + traceAddress1 + `","to":"` + traceAddress2 + `","value":"0x0","gas":"0x1","gasUsed":"0x1","input":"0x","output":"0x","calls":[{"type":"CALL","from":"` + traceAddress2 + `","to":"` + traceAddress3 + `","value":"0x0","gas":"0x1","gasUsed":"0x1","input":"0x","output":"0x"}]}`), change: func(limits *TraceLimits) { limits.MaxFrames = 1 }},
		{name: "depth", data: []byte(`{"type":"CALL","from":"` + traceAddress1 + `","to":"` + traceAddress2 + `","value":"0x0","gas":"0x1","gasUsed":"0x1","input":"0x","output":"0x","calls":[{"type":"CALL","from":"` + traceAddress2 + `","to":"` + traceAddress3 + `","value":"0x0","gas":"0x1","gasUsed":"0x1","input":"0x","output":"0x","calls":[{"type":"CALL","from":"` + traceAddress3 + `","to":"` + traceAddress4 + `","value":"0x0","gas":"0x1","gasUsed":"0x1","input":"0x","output":"0x"}]}]}`), change: func(limits *TraceLimits) { limits.MaxDepth = 1 }},
		{name: "data", data: []byte(`{"type":"CALL","from":"` + traceAddress1 + `","to":"` + traceAddress2 + `","value":"0x0","gas":"0x1","gasUsed":"0x1","input":"0x1234","output":"0x"}`), change: func(limits *TraceLimits) { limits.MaxDataBytes = 1 }},
		{name: "text", data: []byte(`{"type":"CALL","from":"` + traceAddress1 + `","to":"` + traceAddress2 + `","value":"0x0","gas":"0x1","gasUsed":"0x1","input":"0x","output":"0x","error":"execution reverted"}`), change: func(limits *TraceLimits) { limits.MaxTextBytes = 4 }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			limits := DefaultTraceLimits()
			test.change(&limits)
			if _, err := NormalizeCallTracer(test.data, limits); !errors.Is(err, ErrTraceLimit) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	if _, err := NormalizeCallTracer([]byte(`{"type":"CALL","from":"`+traceAddress1+`","to":"`+traceAddress2+`","input":"0x1"}`), DefaultTraceLimits()); err == nil {
		t.Fatal("accepted odd-length call data")
	}
	oversized := DefaultTraceLimits()
	oversized.MaxTextBytes = 4
	if _, err := NormalizeCallTracer([]byte(`{"type":"CALL","from":"`+traceAddress1+`","to":"`+traceAddress2+`","error":"`+strings.Repeat("x", 5)+`"}`), oversized); !errors.Is(err, ErrTraceLimit) {
		t.Fatalf("oversized error text err=%v", err)
	}
	unavailable := UnavailableTrace(TraceAPI, "method not supported")
	if unavailable.State != TraceUnavailable || unavailable.Reason == "" {
		t.Fatalf("unavailable=%+v", unavailable)
	}
}
