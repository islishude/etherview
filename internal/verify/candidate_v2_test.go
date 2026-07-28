package verify

import (
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestExtractAndVerifyAllCandidates(t *testing.T) {
	t.Parallel()
	firstAux := solidityAuxdata(t, map[string]any{"ipfs": []byte{1, 2, 3}})
	secondAux := solidityAuxdata(t, map[string]any{"ipfs": []byte{4, 5, 6}})
	creation := append([]byte{0x60, 0x00, 0x56}, firstAux...)
	runtime := append([]byte{0x60, 0x01, 0x56}, firstAux...)
	modifiedCreation := append([]byte{0x60, 0x00, 0x56}, secondAux...)
	modifiedRuntime := append([]byte{0x60, 0x01, 0x56}, secondAux...)
	originalOutput := candidateCompilerOutput(t, map[string]map[string]candidateOutputFixture{
		"B.sol": {
			"B": {creation: creation, runtime: runtime},
		},
		"A.sol": {
			"A": {creation: creation, runtime: runtime},
		},
	})
	modifiedOutput := candidateCompilerOutput(t, map[string]map[string]candidateOutputFixture{
		"B.sol": {
			"B": {creation: modifiedCreation, runtime: modifiedRuntime},
		},
		"A.sol": {
			"A": {creation: modifiedCreation, runtime: modifiedRuntime},
		},
	})
	candidates, err := ExtractCandidatesV2(
		originalOutput, modifiedOutput, LanguageSolidity, "v0.8.30+commit.73712a01",
	)
	if err != nil {
		t.Fatalf("extract candidates: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates = %d", len(candidates))
	}
	results, err := VerifyCandidateArtifacts(candidates, BytecodePair{
		Creation: "0x" + hex.EncodeToString(creation),
		Runtime:  "0x" + hex.EncodeToString(runtime),
	}, "B", true)
	if err != nil {
		t.Fatalf("verify candidates: %v", err)
	}
	if len(results) != 2 || results[0].Candidate.FullyQualifiedName() != "B.sol:B" {
		t.Fatalf("results = %#v", results)
	}
	if results[0].Creation == nil || results[0].Runtime == nil ||
		results[0].Creation.MatchType != VerificationMatchFull ||
		results[0].Runtime.MatchType != VerificationMatchFull {
		t.Fatalf("unexpected match: %#v", results[0])
	}
	if results[0].Candidate.CompilerVersion != "0.8.30+commit.73712a01" {
		t.Fatalf("version = %q", results[0].Candidate.CompilerVersion)
	}
}

func TestExtractCandidatesRequiresStableDualCompilationShape(t *testing.T) {
	t.Parallel()
	aux := solidityAuxdata(t, map[string]any{"solc": []byte{0, 8, 30}})
	first := candidateCompilerOutput(t, map[string]map[string]candidateOutputFixture{
		"A.sol": {"A": {creation: append([]byte{0x60}, aux...), runtime: append([]byte{0x61}, aux...)}},
	})
	second := candidateCompilerOutput(t, map[string]map[string]candidateOutputFixture{
		"B.sol": {"B": {creation: append([]byte{0x60}, aux...), runtime: append([]byte{0x61}, aux...)}},
	})
	if _, err := ExtractCandidatesV2(first, second, LanguageSolidity, "0.8.30"); err == nil {
		t.Fatal("expected candidate-set mismatch")
	}
}

func TestExtractCandidatesFromChecksumPinnedSolidityFixture(t *testing.T) {
	t.Parallel()
	candidates, err := ExtractCandidatesV2(
		readCompilerJSONFixture(t, "solidity", "output.linked.ipfs.json"),
		readCompilerJSONFixture(t, "solidity", "output.linked.ipfs-comment.json"),
		LanguageSolidity,
		"0.8.30+commit.73712a01",
	)
	if err != nil {
		t.Fatalf("extract pinned fixture candidates: %v", err)
	}
	if len(candidates) != 3 {
		t.Fatalf("pinned fixture candidates = %d, want 3", len(candidates))
	}
	var target *CandidateArtifact
	for index := range candidates {
		if candidates[index].FullyQualifiedName() == "contracts/Target.sol:Target" {
			target = &candidates[index]
			break
		}
	}
	if target == nil || len(target.creationAuxdata) == 0 || len(target.runtimeAuxdata) == 0 {
		t.Fatal("pinned Target fixture has no dual-compile auxdata")
	}
}

func TestExtractCandidatesFromChecksumPinnedVyperFixture(t *testing.T) {
	t.Parallel()
	candidates, err := ExtractCandidatesV2(
		readCompilerJSONFixture(t, "vyper", "output.metadata.json"),
		readCompilerJSONFixture(t, "vyper", "output.metadata-comment.json"),
		LanguageVyper,
		"0.4.3+commit.bff19ea2",
	)
	if err != nil {
		t.Fatalf("extract pinned Vyper fixture candidates: %v", err)
	}
	if len(candidates) != 1 ||
		candidates[0].FullyQualifiedName() != "contracts/Target.vy:Target" {
		t.Fatalf("pinned Vyper candidates = %#v", candidates)
	}
	if len(candidates[0].creationAuxdata) == 0 || len(candidates[0].runtimeAuxdata) != 0 {
		t.Fatalf(
			"Vyper auxdata counts creation=%d runtime=%d",
			len(candidates[0].creationAuxdata),
			len(candidates[0].runtimeAuxdata),
		)
	}
	matches, err := VerifyCandidateArtifacts(candidates, BytecodePair{
		Creation: candidates[0].CreationBytecode,
		Runtime:  candidates[0].RuntimeBytecode,
	}, "", false)
	if err != nil || len(matches) != 1 ||
		matches[0].Creation != nil ||
		matches[0].Runtime == nil ||
		matches[0].Runtime.MatchType != VerificationMatchPartial {
		t.Fatalf("Vyper fixture match = %#v, error = %v", matches, err)
	}
}

type candidateOutputFixture struct {
	creation []byte
	runtime  []byte
}

func candidateCompilerOutput(
	t *testing.T,
	fixtures map[string]map[string]candidateOutputFixture,
) json.RawMessage {
	t.Helper()
	contracts := make(map[string]any, len(fixtures))
	for file, byName := range fixtures {
		encodedContracts := make(map[string]any, len(byName))
		for name, fixture := range byName {
			encodedContracts[name] = map[string]any{
				"abi": []any{},
				"evm": map[string]any{
					"bytecode": map[string]any{
						"object":         hex.EncodeToString(fixture.creation),
						"sourceMap":      "0:1:0",
						"linkReferences": map[string]any{},
					},
					"deployedBytecode": map[string]any{
						"object":              hex.EncodeToString(fixture.runtime),
						"sourceMap":           "0:1:0",
						"linkReferences":      map[string]any{},
						"immutableReferences": map[string]any{},
					},
				},
			}
		}
		contracts[file] = encodedContracts
	}
	encoded, err := json.Marshal(map[string]any{"contracts": contracts})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
