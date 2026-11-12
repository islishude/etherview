package verify

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestAdmissionBoundsPerturbedInputBeforeStorage(t *testing.T) {
	repository := &verifyMemoryRepository{}
	input := json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}`)
	prepared, err := PrepareVerifierStandardJSON(input, LanguageSolidity, "0.8.30", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	service, _ := NewService(repository, len(prepared))
	_, _, err = service.SubmitV2(t.Context(), SubmissionV2{Kind: JobSolidityStandardJSON, Language: LanguageSolidity, CompilerVersion: "0.8.30", StandardJSON: input, Bytecodes: []BytecodePair{{Runtime: "0x6001"}}})
	var typed ServiceError
	if !errors.As(err, &typed) || typed.Code != ServiceInvalidRequest || repository.submits != 0 {
		t.Fatalf("err=%v submits=%d", err, repository.submits)
	}
}

func TestCompactInputRejectsSourceChangingVariants(t *testing.T) {
	input := json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}`)
	changed := json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract B {}"}},"settings":{}}`)
	if _, _, err := encodeSubmission(SubmissionV2{StandardJSON: input, StandardJSONVariants: []json.RawMessage{changed}}); err == nil {
		t.Fatal("different variant sources discarded")
	}
}

func TestCompactInputPreservesBaseCompilerBytes(t *testing.T) {
	input := json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}`)
	payload, _, err := encodeSubmission(SubmissionV2{StandardJSON: input, StandardJSONVariants: []json.RawMessage{input}})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeSubmission(payload)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded.StandardJSONVariants[0]) != string(input) {
		t.Fatal("base compiler evidence bytes changed")
	}
}
