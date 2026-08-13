package verify

import (
	"encoding/json"
	"strings"
	"testing"
)

func validGeasSubmission() SubmissionV2 {
	return SubmissionV2{
		Kind:            JobAddress,
		Language:        LanguageGeas,
		CompilerVersion: "v0.3.3",
		Geas: &GeasRequest{
			Sources: map[string]string{
				"system/main.eas":  "push 1\n",
				"system/ctor.eas":  `#bytes code: assemble("main.eas")`,
				"common/value.eas": "#define VALUE = 1\n",
			},
			RuntimeEntrypoint:  "system/main.eas",
			CreationEntrypoint: "system/ctor.eas",
		},
		Target: &VerificationTarget{
			ChainID: 1, Address: "0x" + strings.Repeat("11", 20),
			CodeHash:    "0x" + strings.Repeat("22", 32),
			AtBlockHash: "0x" + strings.Repeat("33", 32),
		},
		Bytecodes: []BytecodePair{{Creation: "0x6001", Runtime: "0x6001"}},
	}
}

func TestPrepareGeasAddressSubmission(t *testing.T) {
	t.Parallel()
	request := validGeasSubmission()
	request.CompilerDigest = strings.Repeat("aa", 32)
	request.ExecutorDigest = strings.Repeat("bb", 32)
	request.ExecutorKind = "caller-controlled"
	request.ExecutionPolicy = "caller-controlled"
	request.CompilerPlatform = "caller-controlled"
	request.CatalogGenerationID = 99
	service := &Service{maxInputBytes: 5 << 20}
	if err := service.prepareV2(t.Context(), &request); err != nil {
		t.Fatal(err)
	}
	if request.CompilerVersion != GeasCompilerVersion || request.ContractNameHint != "main" {
		t.Fatalf("normalized request = %+v", request)
	}
	if request.CompilerDigest != "" || request.ExecutorDigest != "" || request.ExecutorKind != "" ||
		request.ExecutionPolicy != "" || request.CompilerPlatform != "" || request.CatalogGenerationID != 0 {
		t.Fatalf("caller provenance survived: %+v", request)
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"runtime_entrypoint":"system/main.eas"`) ||
		strings.Contains(string(payload), "caller-controlled") {
		t.Fatalf("durable payload = %s", payload)
	}
}

func TestPrepareGeasRejectsUnsafeRequestShapes(t *testing.T) {
	t.Parallel()
	tests := map[string]func(*SubmissionV2){
		"wrong version":    func(request *SubmissionV2) { request.CompilerVersion = "0.3.2" },
		"standalone kind":  func(request *SubmissionV2) { request.Kind = JobSolidityMultipart },
		"missing runtime":  func(request *SubmissionV2) { request.Geas.RuntimeEntrypoint = "missing.eas" },
		"absolute runtime": func(request *SubmissionV2) { request.Geas.RuntimeEntrypoint = "/system/main.eas" },
		"traversing source": func(request *SubmissionV2) {
			request.Geas.Sources["../main.eas"] = "push 2"
		},
		"backslash source": func(request *SubmissionV2) {
			request.Geas.Sources[`system\\other.eas`] = "push 2"
		},
		"noncanonical source": func(request *SubmissionV2) {
			request.Geas.Sources["system/../main.eas"] = "push 2"
		},
		"control source": func(request *SubmissionV2) {
			request.Geas.Sources["system/bad\nname.eas"] = "push 2"
		},
		"control contract name": func(request *SubmissionV2) {
			request.ContractNameHint = "system\ncontract"
		},
		"standard json":          func(request *SubmissionV2) { request.StandardJSON = json.RawMessage(`{}`) },
		"missing target runtime": func(request *SubmissionV2) { request.Bytecodes[0].Runtime = "" },
	}
	service := &Service{maxInputBytes: 5 << 20}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			request := validGeasSubmission()
			mutate(&request)
			if err := service.prepareV2(t.Context(), &request); err == nil {
				t.Fatal("unsafe Geas request was accepted")
			}
		})
	}
}

func TestPrepareSolidityRejectsGeasPayload(t *testing.T) {
	t.Parallel()
	request := validGeasSubmission()
	request.Language = LanguageSolidity
	request.CompilerVersion = "0.8.30"
	if err := (&Service{maxInputBytes: 5 << 20}).prepareV2(t.Context(), &request); err == nil {
		t.Fatal("Solidity request accepted Geas payload")
	}
}
