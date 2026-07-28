package verify

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func validVerifyRequest() Request {
	runtimeBytecode := []byte{0x60, 0x01}
	return Request{
		ChainID:            1,
		Address:            "0x" + strings.Repeat("11", 20),
		CodeHash:           "0x" + hex.EncodeToString(keccak256Bytes(runtimeBytecode)),
		AtBlockHash:        "0x" + strings.Repeat("33", 32),
		Language:           LanguageSolidity,
		CompilerVersion:    "0.8.30",
		ContractIdentifier: "A.sol:A",
		StandardJSON:       json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}`),
		CreationBytecode:   "0x6001",
		RuntimeBytecode:    "0x" + hex.EncodeToString(runtimeBytecode),
	}
}

func verificationID(sequence int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012x", sequence)
}

func TestDecodeV2ResultFieldsRequiresRuntimeMatchForPublicationData(t *testing.T) {
	t.Parallel()
	outcome := json.RawMessage(`{
		"kind":"verification_success",
		"file_name":"A.sol",
		"contract_name":"A",
		"language":"solidity",
		"compiler_version":"0.8.30+commit.73712a01",
		"sources":{"A.sol":{"content":"contract A {}"}},
		"settings":{},
		"abi":[],
		"compilation_artifacts":{},
		"creation_code_artifacts":{},
		"runtime_code_artifacts":{},
		"creation_match":{"match_type":"partial","transformations":[],"values":{}},
		"runtime_match":{"match_type":"full","transformations":[],"values":{}},
		"libraries":{},
		"is_blueprint":false
	}`)
	fields, err := decodeV2ResultFields("verification_success", outcome)
	if err != nil {
		t.Fatal(err)
	}
	if fields.RuntimeMatch != "full" || fields.MatchType != VerificationMatchFull {
		t.Fatalf("fields = %#v", fields)
	}
}

func TestDecodeV2ResultFieldsRejectsMalformedSuccess(t *testing.T) {
	t.Parallel()
	for _, outcome := range []json.RawMessage{
		json.RawMessage(`{"kind":"verification_success"}`),
		json.RawMessage(`{
			"kind":"verification_success",
			"file_name":"A.sol","contract_name":"A","language":"solidity",
			"compiler_version":"0.8.30","sources":{},"settings":{},
			"compilation_artifacts":{},"creation_code_artifacts":{},
			"runtime_code_artifacts":{},"libraries":{},
			"runtime_match":{"match_type":"mismatch","transformations":[],"values":{}},
			"is_blueprint":false
		}`),
	} {
		if _, err := decodeV2ResultFields("verification_success", outcome); err == nil {
			t.Fatalf("expected malformed outcome rejection: %s", outcome)
		}
	}
}

func TestVerificationRequestDigestV2SeparatesKinds(t *testing.T) {
	t.Parallel()
	first, _ := json.Marshal(SubmissionV2{
		Kind: JobSolidityStandardJSON, Language: LanguageSolidity,
		CompilerVersion: "0.8.30", StandardJSON: json.RawMessage(`{}`),
	})
	second, _ := json.Marshal(SubmissionV2{
		Kind: JobSolidityBatchStandardJSON, Language: LanguageSolidity,
		CompilerVersion: "0.8.30", StandardJSON: json.RawMessage(`{}`),
	})
	if string(first) == string(second) {
		t.Fatal("job kind did not affect durable request payload")
	}
}
