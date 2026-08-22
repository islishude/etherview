package verify

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/params"
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

func TestMatchCandidateReusesCreationAndRuntimeMatcher(t *testing.T) {
	t.Parallel()
	aux := solidityAuxdata(t, map[string]any{"ipfs": []byte{1, 2, 3}})
	modifiedAux := solidityAuxdata(t, map[string]any{"ipfs": []byte{4, 5, 6}})
	creation := append([]byte{0x60, 0x00, 0x56}, aux...)
	runtime := append([]byte{0x60, 0x01, 0x56}, aux...)
	original := candidateCompilerOutput(t, map[string]map[string]candidateOutputFixture{
		"Factory.sol": {"Child": {creation: creation, runtime: runtime}},
	})
	modified := candidateCompilerOutput(t, map[string]map[string]candidateOutputFixture{
		"Factory.sol": {"Child": {
			creation: append([]byte{0x60, 0x00, 0x56}, modifiedAux...),
			runtime:  append([]byte{0x60, 0x01, 0x56}, modifiedAux...),
		}},
	})
	candidates, err := ExtractCandidatesV2(
		original, modified, LanguageSolidity, "0.8.30+commit.73712a01",
	)
	if err != nil {
		t.Fatalf("extract candidates: %v", err)
	}
	match, ok, err := MatchCandidate(candidates[0], MatchInput{
		Creation: "0x" + hex.EncodeToString(creation),
		Runtime:  "0x" + hex.EncodeToString(runtime),
	}, true)
	if err != nil || !ok || match.Creation == nil || match.Runtime == nil {
		t.Fatalf("match = %+v, ok = %t, error = %v", match, ok, err)
	}
	if match.Candidate.FullyQualifiedName() != "Factory.sol:Child" ||
		match.Creation.MatchType != VerificationMatchFull ||
		match.Runtime.MatchType != VerificationMatchFull {
		t.Fatalf("unexpected match = %+v", match)
	}
	_, ok, err = MatchCandidate(candidates[0], MatchInput{
		Creation: "0x" + hex.EncodeToString(creation), Runtime: "0x6002",
	}, true)
	if err != nil || ok {
		t.Fatalf("runtime mismatch ok = %t, error = %v", ok, err)
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

func TestExtractCandidatesDerivesSolc058YulRuntimeFromStaticObjectWrapper(t *testing.T) {
	t.Parallel()
	output := solc058YulProxyCompilerOutput(t)
	candidates, err := extractCandidatesV2(
		output, output, LanguageYul, "0.5.8+commit.23d335f2", true,
	)
	if err != nil {
		t.Fatalf("extract solc 0.5.8 Yul candidate: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates=%d", len(candidates))
	}
	candidate := candidates[0]
	const runtime = "0x7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffe03601600081602082378035828234f58015156039578182fd5b8082525050506014600cf3"
	if candidate.FullyQualifiedName() != "deterministic-deployment-proxy.yul:Proxy" ||
		candidate.RuntimeBytecode != runtime || candidate.CreationBytecode == "" ||
		string(candidate.ABI) != "[]" || len(candidate.runtimeLinks) != 0 ||
		len(candidate.runtimeImmutables) != 0 {
		t.Fatalf("candidate=%+v", candidate)
	}
	results, err := VerifyCandidateArtifacts(
		candidates, BytecodePair{Runtime: runtime}, candidate.FullyQualifiedName(), true,
	)
	if err != nil || len(results) != 1 || results[0].Creation != nil ||
		results[0].Runtime == nil || results[0].Runtime.MatchType != VerificationMatchPartial {
		t.Fatalf("results=%+v error=%v", results, err)
	}
}

func TestYulRuntimeDerivationRejectsUnusableCompilerOutput(t *testing.T) {
	t.Parallel()
	t.Run("Solidity missing deployed bytecode", func(t *testing.T) {
		output := solc058YulProxyCompilerOutput(t)
		if _, err := extractCandidatesV2(
			output, output, LanguageSolidity, "0.5.8", true,
		); err == nil {
			t.Fatal("expected Solidity output without deployed bytecode to fail")
		}
	})
	t.Run("standalone Yul remains unchanged", func(t *testing.T) {
		output := solc058YulProxyCompilerOutput(t)
		if _, err := ExtractCandidatesV2(output, output, LanguageYul, "0.5.8"); err == nil {
			t.Fatal("standalone Yul unexpectedly derived a missing runtime")
		}
	})
	t.Run("Yul initcode reverts", func(t *testing.T) {
		output := yulCompilerOutput(t, []byte{0x60, 0x00, 0x60, 0x00, 0xfd})
		if _, err := extractCandidatesV2(
			output, output, LanguageYul, "0.5.8", true,
		); err == nil {
			t.Fatal("expected reverting Yul initcode to fail")
		}
	})
	t.Run("oversized Yul initcode", func(t *testing.T) {
		if _, err := deriveYulRuntime(make([]byte, params.MaxInitCodeSize+1), nil); err == nil {
			t.Fatal("expected oversized Yul initcode to fail")
		}
	})
	t.Run("unresolved Yul libraries", func(t *testing.T) {
		links := map[string]map[string][]bytecodeRange{
			"Proxy.yul": {
				"Library": []bytecodeRange{{Start: 0, Length: 1}},
			},
		}
		if _, err := deriveYulRuntime([]byte{0x00}, links); err == nil {
			t.Fatal("expected linked Yul initcode to fail")
		}
	})
	t.Run("dynamic constructor wrapper", func(t *testing.T) {
		if _, err := deriveYulRuntime([]byte{0x5b, 0x60, 0x00, 0x56}, nil); err == nil {
			t.Fatal("expected dynamic Yul initcode to fail")
		}
	})
}

type candidateOutputFixture struct {
	creation []byte
	runtime  []byte
}

func readCompilerJSONFixture(t *testing.T, path ...string) json.RawMessage {
	t.Helper()
	segments := append([]string{"testdata", "compiler"}, path...)
	value, err := os.ReadFile(filepath.Join(segments...))
	if err != nil {
		t.Fatal(err)
	}
	return json.RawMessage(value)
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

func solc058YulProxyCompilerOutput(t *testing.T) json.RawMessage {
	t.Helper()
	// Exact official emscripten-wasm32 solc-js 0.5.8 output for the upstream
	// deterministic-deployment-proxy Yul source and verifier output selection.
	output := json.RawMessage(`{"contracts":{"deterministic-deployment-proxy.yul":{"Proxy":{"evm":{"bytecode":{"linkReferences":{},"object":"604580600e600039806000f350fe7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffe03601600081602082378035828234f58015156039578182fd5b8082525050506014600cf3","opcodes":"PUSH1 0x45 DUP1 PUSH1 0xE PUSH1 0x0 CODECOPY DUP1 PUSH1 0x0 RETURN POP INVALID PUSH32 0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFE0 CALLDATASIZE ADD PUSH1 0x0 DUP2 PUSH1 0x20 DUP3 CALLDATACOPY DUP1 CALLDATALOAD DUP3 DUP3 CALLVALUE CREATE2 DUP1 ISZERO ISZERO PUSH1 0x39 JUMPI DUP2 DUP3 REVERT JUMPDEST DUP1 DUP3 MSTORE POP POP POP PUSH1 0x14 PUSH1 0xC RETURN ","sourceMap":""}}}}},"errors":[{"component":"general","formattedMessage":"Yul is still experimental. Please use the output with care.","message":"Yul is still experimental. Please use the output with care.","severity":"warning","type":"Warning"}]}`)
	if !json.Valid(output) {
		t.Fatal("solc 0.5.8 Yul fixture is invalid")
	}
	return output
}

func yulCompilerOutput(t *testing.T, creation []byte) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"contracts": map[string]any{
			"deterministic-deployment-proxy.yul": map[string]any{
				"Proxy": map[string]any{
					"evm": map[string]any{
						"bytecode": map[string]any{
							"object": hex.EncodeToString(creation), "sourceMap": "", "linkReferences": map[string]any{},
						},
					},
				},
			},
		},
		"errors": []map[string]string{{
			"severity": "warning", "type": "Warning",
			"message": "Yul is still experimental. Please use the output with care.",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
