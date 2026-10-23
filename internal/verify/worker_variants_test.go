package verify

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestWorkerKeepsMatchedVariantInputAndCandidatesTogether(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		kind         JobKind
		firstFails   bool
		bytecodes    []BytecodePair
		wantVariants []string
	}{
		{
			name: "address after compilation failure", kind: JobAddress, firstFails: true,
			bytecodes: []BytecodePair{{Creation: "0x6002", Runtime: "0x6002"}}, wantVariants: []string{"none"},
		},
		{
			name: "address after nonmatching compilation", kind: JobAddress,
			bytecodes: []BytecodePair{{Creation: "0x6002", Runtime: "0x6002"}}, wantVariants: []string{"none"},
		},
		{
			name: "standalone after compilation failure", kind: JobSolidityMultipart, firstFails: true,
			bytecodes: []BytecodePair{{Creation: "0x6002", Runtime: "0x6002"}}, wantVariants: []string{"none"},
		},
		{
			name: "batch matches different variants", kind: JobSolidityBatchMultipart,
			bytecodes: []BytecodePair{
				{Creation: "0x6001", Runtime: "0x6001"},
				{Creation: "0x6002", Runtime: "0x6002"},
			},
			wantVariants: []string{"ipfs", "none"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := SubmissionV2{
				Kind: test.kind, Language: LanguageSolidity, CompilerVersion: "0.8.30",
				Multipart: &MultipartRequest{
					Language: LanguageSolidity,
					Sources:  map[string]string{"A.sol": "contract A {} contract Child {}"},
				},
				Bytecodes: test.bytecodes,
			}
			if test.kind == JobAddress {
				runtime, err := decodeBytecode(test.bytecodes[0].Runtime)
				if err != nil {
					t.Fatal(err)
				}
				request.Target = &VerificationTarget{
					ChainID: 1, Address: "0x" + strings.Repeat("11", 20),
					CodeHash: "0x" + hex.EncodeToString(keccak256Bytes(runtime)), AtBlockHash: "0x" + strings.Repeat("33", 32),
					CreationBytecode: test.bytecodes[0].Creation, RuntimeBytecode: test.bytecodes[0].Runtime,
				}
			}
			if err := (&Service{maxInputBytes: 5 << 20}).prepareV2(t.Context(), &request); err != nil {
				t.Fatal(err)
			}
			compilationFailure := json.RawMessage(`{"errors":[{"severity":"error","message":"variant cannot compile"}]}`)
			compiler := &variantTestCompiler{
				provenance: testSolcJSProvenance(),
				outputs: map[string]json.RawMessage{
					"ipfs": candidateCompilerOutput(t, map[string]map[string]candidateOutputFixture{
						"A.sol": {
							"A":     {creation: []byte{0x60, 0x01}, runtime: []byte{0x60, 0x01}},
							"Child": {creation: []byte{0x60, 0xff}, runtime: []byte{0x60, 0xff}},
						},
					}),
					"none": candidateCompilerOutput(t, map[string]map[string]candidateOutputFixture{
						"A.sol": {
							"A":     {creation: []byte{0x60, 0x02}, runtime: []byte{0x60, 0x02}},
							"Child": {creation: []byte{0x60, 0xff}, runtime: []byte{0x60, 0xff}},
						},
					}),
					"bzzr1": compilationFailure,
				},
			}
			if test.firstFails {
				compiler.outputs["ipfs"] = compilationFailure
			}
			repository := &verifyMemoryRepository{
				claimFound: true,
				lease: VerificationLease{
					Job:   VerificationJob{ID: verificationID(68), Kind: test.kind, Status: JobRunning, RequestV2: &request},
					Token: "variant-lease",
				},
			}
			found, err := newVerifyTestWorker(t, repository, compiler).ProcessOne(t.Context())
			if err != nil || !found || len(repository.failures) != 0 || len(repository.payloads) != 1 {
				t.Fatalf("found=%t error=%v failures=%v outcomes=%v", found, err, repository.failures, repository.outcomes)
			}
			if compiler.compileCalls != 2*len(request.StandardJSONVariants) {
				t.Errorf("compile calls=%d, want dual compilation for every variant", compiler.compileCalls)
			}
			var outcome variantOutcome
			if err := json.Unmarshal(repository.payloads[0], &outcome); err != nil {
				t.Fatal(err)
			}
			results := []variantOutcome{outcome}
			if test.kind == JobSolidityBatchMultipart {
				if outcome.Kind != "batch_results" {
					t.Fatalf("outcome kind=%q", outcome.Kind)
				}
				results = outcome.Results
			}
			if len(results) != len(test.wantVariants) {
				t.Fatalf("results=%d, want=%d", len(results), len(test.wantVariants))
			}
			for index, result := range results {
				if result.Kind != "verification_success" || result.Settings.Metadata.BytecodeHash != test.wantVariants[index] {
					t.Errorf("result %d kind=%q metadata hash=%q, want successful variant %q", index,
						result.Kind, result.Settings.Metadata.BytecodeHash, test.wantVariants[index])
				}
			}
			if test.kind != JobAddress {
				if len(repository.compilations) != 0 {
					t.Fatal("standalone or batch verification authenticated a compilation unit")
				}
				return
			}
			if len(repository.compilations) != 1 {
				t.Fatalf("authenticated compilation units=%d", len(repository.compilations))
			}
			unit := repository.compilations[0]
			if !bytes.Equal(unit.StandardJSON, request.StandardJSONVariants[1]) {
				t.Errorf("authenticated input=%s, want the matched none variant", unit.StandardJSON)
			}
			if len(unit.Candidates) != 2 || unit.Candidates[0].FullyQualifiedName() != "A.sol:A" ||
				unit.Candidates[1].FullyQualifiedName() != "A.sol:Child" {
				t.Errorf("authenticated unit lost its full candidate set: %+v", unit.Candidates)
			}
		})
	}
}

type variantOutcome struct {
	Kind     string `json:"kind"`
	Settings struct {
		Metadata struct {
			BytecodeHash string `json:"bytecodeHash"`
		} `json:"metadata"`
	} `json:"settings"`
	Results []variantOutcome `json:"results"`
}

type variantTestCompiler struct {
	verifyTestCompiler
	outputs map[string]json.RawMessage
}

func (compiler *variantTestCompiler) CompilePinned(
	_ context.Context,
	_ Language,
	_ string,
	_ CompilerProvenance,
	input []byte,
) ([]byte, error) {
	compiler.compileCalls++
	var document variantOutcome
	if err := json.Unmarshal(input, &document); err != nil {
		return nil, err
	}
	output, exists := compiler.outputs[document.Settings.Metadata.BytecodeHash]
	if !exists {
		return nil, fmt.Errorf("unexpected compilation variant %q", document.Settings.Metadata.BytecodeHash)
	}
	return bytes.Clone(output), nil
}
