package verify

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestPrepareVerifierStandardJSONOwnsAllCandidateOutputs(t *testing.T) {
	t.Parallel()
	input := json.RawMessage(`{
		"language":"Solidity",
		"sources":{
			"B.sol":{"content":"contract B {}"},
			"A.sol":{"content":"contract A {}","keccak256":"0x1111111111111111111111111111111111111111111111111111111111111111"}
		},
		"settings":{"outputSelection":{"*":{"*":["ir","ast"]}}}
	}`)
	prepared, err := PrepareVerifierStandardJSON(input, LanguageSolidity, "0.8.30+commit.73712a01", 1<<20)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(prepared, &document); err != nil {
		t.Fatal(err)
	}
	settings := document["settings"].(map[string]any)
	selection := settings["outputSelection"].(map[string]any)
	if len(selection) != 1 {
		t.Fatalf("selection = %#v", selection)
	}
	all := selection["*"].(map[string]any)
	outputs := all["*"].([]any)
	found := map[string]bool{}
	for _, output := range outputs {
		found[output.(string)] = true
	}
	for _, required := range []string{
		"abi", "devdoc", "userdoc", "storageLayout", "evm.bytecode.sourceMap",
		"evm.deployedBytecode.immutableReferences", "evm.methodIdentifiers",
	} {
		if !found[required] {
			t.Fatalf("required output %q is missing", required)
		}
	}
	modified, err := PerturbVerifierSources(prepared, 1<<20)
	if err != nil {
		t.Fatalf("perturb: %v", err)
	}
	var second map[string]any
	if err := json.Unmarshal(modified, &second); err != nil {
		t.Fatal(err)
	}
	sources := second["sources"].(map[string]any)
	for name, raw := range sources {
		source := raw.(map[string]any)
		if _, exists := source["keccak256"]; exists {
			t.Fatalf("%s retained stale checksum", name)
		}
		content := source["content"].(string)
		if content[len(content)-1] != ' ' {
			t.Fatalf("%s was not perturbed", name)
		}
	}
}

func TestPrepareVerifierStandardJSONSupportsYulAndRejectsIndirectSources(t *testing.T) {
	t.Parallel()
	yul := json.RawMessage(`{
		"language":"Yul",
		"sources":{"main.yul":{"content":"object \"A\" { code {} }"}},
		"settings":{}
	}`)
	if _, err := PrepareVerifierStandardJSON(yul, LanguageYul, "0.8.30+commit.73712a01", 1<<20); err != nil {
		t.Fatalf("prepare Yul: %v", err)
	}
	indirect := json.RawMessage(`{
		"language":"Solidity",
		"sources":{"A.sol":{"urls":["https://example.com/A.sol"]}}
	}`)
	if _, err := PrepareVerifierStandardJSON(indirect, LanguageSolidity, "0.8.30", 1<<20); err == nil {
		t.Fatal("expected URL source rejection")
	}
	duplicate := json.RawMessage(`{
		"language":"Solidity",
		"sources":{"A.sol":{"content":"a"},"A.sol":{"content":"b"}}
	}`)
	if _, err := PrepareVerifierStandardJSON(duplicate, LanguageSolidity, "0.8.30", 1<<20); err == nil {
		t.Fatal("expected duplicate-key rejection")
	}
}

func TestBuildMultipartStandardJSONVariants(t *testing.T) {
	t.Parallel()
	runs := 200
	variants, err := BuildMultipartStandardJSON(MultipartRequest{
		Language: LanguageSolidity,
		Sources: map[string]string{
			"B.sol": "contract B {}",
			"A.sol": "contract A {}",
		},
		OptimizationRuns: &runs,
	}, "0.8.30+commit.73712a01", 1<<20)
	if err != nil {
		t.Fatalf("build multipart: %v", err)
	}
	if len(variants) != 3 {
		t.Fatalf("variants = %d", len(variants))
	}
	var hashes []string
	for _, variant := range variants {
		var document map[string]any
		if err := json.Unmarshal(variant, &document); err != nil {
			t.Fatal(err)
		}
		settings := document["settings"].(map[string]any)
		metadata := settings["metadata"].(map[string]any)
		hashes = append(hashes, metadata["bytecodeHash"].(string))
	}
	if !reflect.DeepEqual(hashes, []string{"ipfs", "none", "bzzr1"}) {
		t.Fatalf("hash variants = %v", hashes)
	}
}
