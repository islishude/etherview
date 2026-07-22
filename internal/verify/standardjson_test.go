package verify

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestPrepareStandardJSONSolidityMultiFileExactSelection(t *testing.T) {
	t.Parallel()
	input := json.RawMessage(`{
		"settings":{
			"optimizer":{"enabled":true},
			"metadata":{"appendCBOR":false,"bytecodeHash":"none"},
			"outputSelection":{"B.sol":{"B":["userdoc"]}}
		},
		"sources":{"B.sol":{"content":""},"A.sol":{"content":"contract A {}"}},
		"auxiliaryInput":{"smtlib2responses":{"fixture":"sat"}},
		"language":"Solidity"
	}`)
	original := append(json.RawMessage(nil), input...)
	prepared, err := PrepareStandardJSON(input, LanguageSolidity, "0.8.30", "A.sol:A", 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	document := decodePreparedStandardJSON(t, prepared)
	settings := document["settings"].(map[string]any)
	selection := settings["outputSelection"].(map[string]any)
	assertStandardJSONOutputs(t, selection, "A.sol", "A", solidityRequiredOutputs)
	if _, exists := selection["B.sol"]; exists {
		t.Fatal("preparation retained unrelated caller-selected outputs")
	}
	if _, exists := selection["*"]; exists {
		t.Fatal("preparation implicitly selected every source")
	}
	if _, exists := selection["C.sol"]; exists {
		t.Fatal("preparation implicitly selected an unrelated source")
	}
	optimizer := settings["optimizer"].(map[string]any)
	if enabled, ok := optimizer["enabled"].(bool); !ok || !enabled {
		t.Fatalf("caller compiler settings were not preserved: %#v", optimizer)
	}
	metadata := settings["metadata"].(map[string]any)
	if appendCBOR, ok := metadata["appendCBOR"].(bool); !ok || appendCBOR || metadata["bytecodeHash"] != "none" {
		t.Fatalf("caller metadata settings were not preserved: %#v", metadata)
	}
	if !reflect.DeepEqual(document["auxiliaryInput"], decodePreparedStandardJSON(t, input)["auxiliaryInput"]) {
		t.Fatal("Solidity auxiliaryInput was not preserved")
	}
	if !bytes.Equal(input, original) {
		t.Fatalf("caller input was mutated: %s", input)
	}
	again, err := PrepareStandardJSON(prepared, LanguageSolidity, "0.8.30", "A.sol:A", 64<<10)
	if err != nil || !bytes.Equal(prepared, again) {
		t.Fatalf("preparation is not idempotent: error=%v\nfirst=%s\nsecond=%s", err, prepared, again)
	}
	prepared[0] = '['
	if !bytes.Equal(input, original) {
		t.Fatal("prepared JSON aliases caller storage")
	}
}

func TestPrepareStandardJSONVyperInlineInputsAndSelections(t *testing.T) {
	t.Parallel()
	input := json.RawMessage(`{
		"language":"Vyper",
		"sources":{
			"contracts/Token.vy":{"content":""},
			"contracts/Other.vy":{"content":"@external\ndef ping(): pass"}
		},
		"interfaces":{
			"interfaces/Owned.vyi":{"content":""},
			"interfaces/ERC20.json":{"abi":[]}
		},
		"storage_layout_overrides":{
			"contracts/Token.vy":{
				"layouts/Token.json":{"supply":{"type":"uint256","slot":0,"n_slots":1}}
			}
		},
		"integrity":"sha256:fixture",
		"settings":{
			"optimize":"gas",
			"outputSelection":{
				"*":{"*":["devdoc"]},
				"contracts/Other.vy":{"*":["ast"]}
			}
		}
	}`)
	originalDocument := decodePreparedStandardJSON(t, input)
	prepared, err := PrepareStandardJSON(input, LanguageVyper, "0.4.3", "contracts/Token.vy:Token", 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	document := decodePreparedStandardJSON(t, prepared)
	for _, field := range []string{"interfaces", "storage_layout_overrides", "integrity"} {
		if !reflect.DeepEqual(document[field], originalDocument[field]) {
			t.Fatalf("top-level Vyper field %q changed:\nwant=%#v\ngot=%#v", field, originalDocument[field], document[field])
		}
	}
	settings := document["settings"].(map[string]any)
	searchPaths, ok := settings["search_paths"].([]any)
	if !ok || len(searchPaths) != 1 || searchPaths[0] != "." {
		t.Fatalf("search_paths was not pinned to the virtual root: %#v", settings["search_paths"])
	}
	selection := settings["outputSelection"].(map[string]any)
	assertFlatStandardJSONOutputs(t, selection, "contracts/Token.vy", vyperRequiredOutputs)
	for _, unrelated := range []string{"contracts/Other.vy", "*"} {
		if _, exists := selection[unrelated]; exists {
			t.Fatalf("preparation retained unrelated Vyper selector %q", unrelated)
		}
	}

	again, err := PrepareStandardJSON(prepared, LanguageVyper, "0.4.3", "contracts/Token.vy:Token", 64<<10)
	if err != nil || !bytes.Equal(prepared, again) {
		t.Fatalf("Vyper preparation is not idempotent: error=%v\nfirst=%s\nsecond=%s", err, prepared, again)
	}
}

func TestPrepareStandardJSONVyperFlatAndNestedSelection(t *testing.T) {
	t.Parallel()
	input := json.RawMessage(`{
		"language":"Vyper",
		"sources":{"Token.vy":{"content":""},"Other.vy":{"content":""},"Third.vy":{"content":""}},
		"settings":{"outputSelection":{"Other.vy":["ast"],"Third.vy":{"*":["devdoc"]}}}
	}`)
	prepared, err := PrepareStandardJSON(input, LanguageVyper, "0.4.3", "Token.vy:Token", 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	document := decodePreparedStandardJSON(t, prepared)
	selection := document["settings"].(map[string]any)["outputSelection"].(map[string]any)
	assertFlatStandardJSONOutputs(t, selection, "Token.vy", vyperRequiredOutputs)
	for _, unrelated := range []string{"Other.vy", "Third.vy", "*"} {
		if _, exists := selection[unrelated]; exists {
			t.Fatalf("preparation retained unrelated Vyper selector %q", unrelated)
		}
	}
}

func TestPrepareStandardJSONVyperVersionMatrix(t *testing.T) {
	t.Parallel()
	input := json.RawMessage(`{
		"language":"Vyper",
		"sources":{"A.vy":{"content":""},"B.vy":{"content":""}},
		"settings":{"outputSelection":{"*":["*"]}}
	}`)
	tests := []struct {
		version       string
		targetOutputs []string
		otherOutputs  []string
	}{
		{version: "0.3.4", targetOutputs: vyperLegacyRequiredOutputs, otherOutputs: []string{"userdoc"}},
		{version: "0.3.9", targetOutputs: vyperLegacyRequiredOutputs, otherOutputs: []string{"userdoc"}},
		{version: "0.3.10", targetOutputs: vyperV040RequiredOutputs, otherOutputs: []string{"userdoc"}},
		{version: "0.4.0", targetOutputs: vyperV040RequiredOutputs},
		{version: "0.4.1", targetOutputs: vyperRequiredOutputs},
		{version: "0.4.3+commit.bff19ea2", targetOutputs: vyperRequiredOutputs},
	}
	for _, test := range tests {
		t.Run(test.version, func(t *testing.T) {
			t.Parallel()
			prepared, err := PrepareStandardJSON(input, LanguageVyper, test.version, "A.vy:A", 64<<10)
			if err != nil {
				t.Fatal(err)
			}
			document := decodePreparedStandardJSON(t, prepared)
			selection := document["settings"].(map[string]any)["outputSelection"].(map[string]any)
			assertFlatStandardJSONOutputs(t, selection, "A.vy", test.targetOutputs)
			if test.otherOutputs == nil {
				if _, exists := selection["B.vy"]; exists {
					t.Fatalf("modern compiler selected unrelated source: %s", prepared)
				}
			} else {
				assertFlatStandardJSONOutputs(t, selection, "B.vy", test.otherOutputs)
			}
			again, err := PrepareStandardJSON(prepared, LanguageVyper, test.version, "A.vy:A", 64<<10)
			if err != nil || !bytes.Equal(prepared, again) {
				t.Fatalf("versioned preparation is not idempotent: error=%v\nfirst=%s\nsecond=%s", err, prepared, again)
			}
		})
	}
}

func TestPrepareStandardJSONRejectsDuplicateKeysAndNormalizesVyperInterfaces(t *testing.T) {
	t.Parallel()
	duplicate := json.RawMessage(`{
		"language":"Solidity",
		"sources":{"A.sol":{"content":"first","content":"second"}},
		"settings":{}
	}`)
	if _, err := PrepareStandardJSON(duplicate, LanguageSolidity, "0.8.30", "A.sol:A", 64<<10); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate-key error=%v", err)
	}

	input := json.RawMessage(`{
		"language":"Vyper",
		"sources":{"A.vy":{"content":""},"I.vyi":{"content":""}},
		"interfaces":{"ERC20.json":[]},
		"settings":{}
	}`)
	prepared, err := PrepareStandardJSON(input, LanguageVyper, "0.4.3", "A.vy:A", 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	document := decodePreparedStandardJSON(t, prepared)
	interfaces := document["interfaces"].(map[string]any)
	entry, ok := interfaces["ERC20.json"].(map[string]any)
	if !ok {
		t.Fatalf("bare ABI was not normalized: %#v", interfaces)
	}
	if abi, ok := entry["abi"].([]any); !ok || len(abi) != 0 {
		t.Fatalf("normalized ABI=%#v", entry["abi"])
	}
}

func TestPrepareStandardJSONVyperInterfaceVersionMatrix(t *testing.T) {
	t.Parallel()
	modernInput := json.RawMessage(`{
		"language":"Vyper",
		"sources":{"contracts/A.vy":{"content":""}},
		"interfaces":{"interfaces/IThing.vyi":{"content":"@external\ndef ping(): view"}},
		"settings":{}
	}`)
	for _, version := range []string{"0.4.0", "0.4.1", "0.4.3"} {
		version := version
		t.Run("modern-vyi-"+version, func(t *testing.T) {
			t.Parallel()
			prepared, err := PrepareStandardJSON(
				modernInput,
				LanguageVyper,
				version,
				"contracts/A.vy:A",
				64<<10,
			)
			if err != nil {
				t.Fatal(err)
			}
			interfaces := decodePreparedStandardJSON(t, prepared)["interfaces"].(map[string]any)
			entry, ok := interfaces["interfaces/IThing.vyi"].(map[string]any)
			if !ok || entry["content"] != "@external\ndef ping(): view" {
				t.Fatalf("prepared .vyi interface=%#v", interfaces["interfaces/IThing.vyi"])
			}
		})
	}

	legacyInput := json.RawMessage(`{
		"language":"Vyper",
		"sources":{"contracts/A.vy":{"content":""}},
		"interfaces":{
			"interfaces/IOwned.vy":{"content":"@external\ndef owner() -> address: view"},
			"package.json":{
				"manifest":"ethpm/3",
				"contractTypes":{
					"IERC20":{"abi":[{"type":"function","name":"totalSupply"}],"runtimeBytecode":{"bytecode":"0x"}},
					"IOwnable":{"abi":[]}
				}
			}
		},
		"settings":{}
	}`)
	for _, version := range []string{"0.3.4", "0.3.9", "0.3.10"} {
		version := version
		t.Run("legacy-ethpm-"+version, func(t *testing.T) {
			t.Parallel()
			prepared, err := PrepareStandardJSON(
				legacyInput,
				LanguageVyper,
				version,
				"contracts/A.vy:A",
				64<<10,
			)
			if err != nil {
				t.Fatal(err)
			}
			document := decodePreparedStandardJSON(t, prepared)
			manifest := document["interfaces"].(map[string]any)["package.json"].(map[string]any)
			if len(manifest) != 1 {
				t.Fatalf("ignored EthPM fields were not normalized away: %#v", manifest)
			}
			contractTypes := manifest["contractTypes"].(map[string]any)
			if len(contractTypes) != 2 {
				t.Fatalf("contractTypes=%#v", contractTypes)
			}
			for contractName, rawContractType := range contractTypes {
				contractType, ok := rawContractType.(map[string]any)
				if !ok || len(contractType) != 1 {
					t.Fatalf("contract type %q was not normalized to ABI only: %#v", contractName, rawContractType)
				}
				if _, ok := contractType["abi"].([]any); !ok {
					t.Fatalf("contract type %q ABI=%#v", contractName, contractType["abi"])
				}
			}
			again, err := PrepareStandardJSON(prepared, LanguageVyper, version, "contracts/A.vy:A", 64<<10)
			if err != nil || !bytes.Equal(prepared, again) {
				t.Fatalf("legacy interface preparation is not idempotent: error=%v\nfirst=%s\nsecond=%s", err, prepared, again)
			}
		})
	}
}

func TestPrepareStandardJSONVyperStorageOverrideVersionMatrix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		version  string
		override string
	}{
		{
			version:  "0.4.1",
			override: `{"supply":{"type":"uint256","slot":0,"n_slots":1}}`,
		},
		{
			version:  "0.4.2",
			override: `{"layouts/A.json":{"supply":{"type":"uint256","slot":0,"n_slots":1}}}`,
		},
		{
			version:  "0.4.3",
			override: `{"layouts/A.json":{"supply":{"type":"uint256","slot":0,"n_slots":1}}}`,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.version, func(t *testing.T) {
			t.Parallel()
			input := json.RawMessage(`{"language":"Vyper","sources":{"contracts/A.vy":{"content":""}},` +
				`"storage_layout_overrides":{"contracts/A.vy":` + test.override + `},"settings":{}}`)
			prepared, err := PrepareStandardJSON(input, LanguageVyper, test.version, "contracts/A.vy:A", 64<<10)
			if err != nil {
				t.Fatal(err)
			}
			again, err := PrepareStandardJSON(prepared, LanguageVyper, test.version, "contracts/A.vy:A", 64<<10)
			if err != nil || !bytes.Equal(prepared, again) {
				t.Fatalf("override preparation is not idempotent: error=%v\nfirst=%s\nsecond=%s", err, prepared, again)
			}
		})
	}
}

func TestPrepareStandardJSONBoundsVyperInterfaceExpansion(t *testing.T) {
	t.Parallel()
	t.Run("ABI entries", func(t *testing.T) {
		t.Parallel()
		abi := make([]any, maxStandardJSONOutputEntries+1)
		input, err := json.Marshal(map[string]any{
			"language": "Vyper",
			"sources":  map[string]any{"A.vy": map[string]any{"content": ""}},
			"interfaces": map[string]any{
				"I.json": abi,
			},
			"settings": map[string]any{},
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = PrepareStandardJSON(input, LanguageVyper, "0.4.3", "A.vy:A", 1<<20)
		if err == nil || !strings.Contains(err.Error(), "too many entries") {
			t.Fatalf("ABI expansion error=%v", err)
		}
	})

	t.Run("EthPM namespaces", func(t *testing.T) {
		t.Parallel()
		contractTypes := make(map[string]any, maxStandardJSONSources+1)
		for index := 0; index <= maxStandardJSONSources; index++ {
			contractTypes[fmt.Sprintf("I%d", index)] = map[string]any{"abi": []any{}}
		}
		input, err := json.Marshal(map[string]any{
			"language": "Vyper",
			"sources":  map[string]any{"A.vy": map[string]any{"content": ""}},
			"interfaces": map[string]any{
				"package.json": map[string]any{"contractTypes": contractTypes},
			},
			"settings": map[string]any{},
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = PrepareStandardJSON(input, LanguageVyper, "0.3.10", "A.vy:A", 1<<20)
		if err == nil || !strings.Contains(err.Error(), "bounded") {
			t.Fatalf("EthPM expansion error=%v", err)
		}
	})
}

func TestPrepareStandardJSONRejectsVersionIncompatibleVyperInputs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		version    string
		identifier string
		input      string
		want       string
	}{
		{
			name: "non semantic version", version: "nightly", identifier: "A.vy:A",
			input: `{"language":"Vyper","sources":{"A.vy":{"content":""}},"settings":{}}`, want: "semantic",
		},
		{
			name: "interface target", version: "0.4.3", identifier: "A.vyi:A",
			input: `{"language":"Vyper","sources":{"A.vyi":{"content":""}},"settings":{}}`, want: ".vy source",
		},
		{
			name: "legacy interface source", version: "0.3.10", identifier: "A.vy:A",
			input: `{"language":"Vyper","sources":{"A.vy":{"content":""},"I.vyi":{"content":""}},"settings":{}}`, want: "requires .vy",
		},
		{
			name: "legacy vyi interface", version: "0.3.10", identifier: "A.vy:A",
			input: `{"language":"Vyper","sources":{"A.vy":{"content":""}},"interfaces":{"I.vyi":{"content":""}},"settings":{}}`, want: "does not support .vyi",
		},
		{
			name: "modern EthPM interface", version: "0.4.0", identifier: "A.vy:A",
			input: `{"language":"Vyper","sources":{"A.vy":{"content":""}},"interfaces":{"I.json":{"contractTypes":{"I":{"abi":[]}}}},"settings":{}}`, want: "does not support EthPM",
		},
		{
			name: "modern vyi ABI", version: "0.4.3", identifier: "A.vy:A",
			input: `{"language":"Vyper","sources":{"A.vy":{"content":""}},"interfaces":{"I.vyi":{"abi":[]}},"settings":{}}`, want: ".json filename",
		},
		{
			name: "modern json content", version: "0.4.3", identifier: "A.vy:A",
			input: `{"language":"Vyper","sources":{"A.vy":{"content":""}},"interfaces":{"I.json":{"content":""}},"settings":{}}`, want: ".vy or .vyi",
		},
		{
			name: "legacy integrity", version: "0.3.10", identifier: "A.vy:A",
			input: `{"language":"Vyper","sources":{"A.vy":{"content":""}},"integrity":"fixture","settings":{}}`, want: "integrity",
		},
		{
			name: "legacy search path", version: "0.3.10", identifier: "A.vy:A",
			input: `{"language":"Vyper","sources":{"A.vy":{"content":""}},"settings":{"search_paths":["."]}}`, want: "0.4.0",
		},
		{
			name: "unsupported layout override", version: "0.4.0", identifier: "A.vy:A",
			input: `{"language":"Vyper","sources":{"A.vy":{"content":""}},"storage_layout_overrides":{"A.vy":{}},"settings":{}}`, want: "0.4.1",
		},
		{
			name: "modern override without inline file", version: "0.4.2", identifier: "A.vy:A",
			input: `{"language":"Vyper","sources":{"A.vy":{"content":""}},"storage_layout_overrides":{"A.vy":{}},"settings":{}}`, want: "one inline file",
		},
		{
			name: "non boolean metadata setting", version: "0.4.3", identifier: "A.vy:A",
			input: `{"language":"Vyper","sources":{"A.vy":{"content":""}},"settings":{"bytecodeMetadata":null}}`, want: "boolean",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := PrepareStandardJSON(json.RawMessage(test.input), LanguageVyper, test.version, test.identifier, 64<<10)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want substring %q", err, test.want)
			}
		})
	}
}

func TestPrepareStandardJSONRejectsMalformedAndIndirectInputs(t *testing.T) {
	t.Parallel()
	validSolidity := `{"language":"Solidity","sources":{"A.sol":{"content":""}},"settings":{}}`
	validVyper := `{"language":"Vyper","sources":{"A.vy":{"content":""}},"settings":{}}`
	tests := []struct {
		name       string
		input      string
		language   Language
		identifier string
		want       string
	}{
		{name: "unsupported request language", input: validSolidity, language: "Yul", identifier: "A.sol:A", want: "language"},
		{name: "language mismatch", input: validSolidity, language: LanguageVyper, identifier: "A.sol:A", want: "language"},
		{name: "empty sources", input: `{"language":"Solidity","sources":{},"settings":{}}`, language: LanguageSolidity, identifier: "A.sol:A", want: "sources"},
		{name: "sources array", input: `{"language":"Solidity","sources":[],"settings":{}}`, language: LanguageSolidity, identifier: "A.sol:A", want: "sources"},
		{name: "missing target", input: validSolidity, language: LanguageSolidity, identifier: "B.sol:B", want: "not present"},
		{name: "missing content", input: `{"language":"Solidity","sources":{"A.sol":{}},"settings":{}}`, language: LanguageSolidity, identifier: "A.sol:A", want: "inline content"},
		{name: "null content", input: `{"language":"Solidity","sources":{"A.sol":{"content":null}},"settings":{}}`, language: LanguageSolidity, identifier: "A.sol:A", want: "inline content"},
		{name: "URL alongside content", input: `{"language":"Solidity","sources":{"A.sol":{"content":"","urls":[]}},"settings":{}}`, language: LanguageSolidity, identifier: "A.sol:A", want: "no URLs"},
		{name: "file indirection", input: `{"language":"Solidity","sources":{"A.sol":{"content":"","file":"/tmp/A.sol"}},"settings":{}}`, language: LanguageSolidity, identifier: "A.sol:A", want: "unsupported"},
		{name: "bad checksum", input: `{"language":"Solidity","sources":{"A.sol":{"content":"","keccak256":"0x12"}},"settings":{}}`, language: LanguageSolidity, identifier: "A.sol:A", want: "checksum"},
		{name: "settings null", input: `{"language":"Solidity","sources":{"A.sol":{"content":""}},"settings":null}`, language: LanguageSolidity, identifier: "A.sol:A", want: "settings"},
		{name: "settings array", input: `{"language":"Solidity","sources":{"A.sol":{"content":""}},"settings":[]}`, language: LanguageSolidity, identifier: "A.sol:A", want: "settings"},
		{name: "malformed Solidity selection", input: `{"language":"Solidity","sources":{"A.sol":{"content":""}},"settings":{"outputSelection":{"*":["abi"]}}}`, language: LanguageSolidity, identifier: "A.sol:A", want: "outputSelection"},
		{name: "malformed Vyper selection", input: `{"language":"Vyper","sources":{"A.vy":{"content":""}},"settings":{"outputSelection":{"*":[1]}}}`, language: LanguageVyper, identifier: "A.vy:A", want: "outputSelection"},
		{name: "Vyper name mismatch", input: validVyper, language: LanguageVyper, identifier: "A.vy:B", want: "filename"},
		{name: "invalid contract name", input: validSolidity, language: LanguageSolidity, identifier: "A.sol:1A", want: "name"},
		{name: "wildcard source name", input: `{"language":"Solidity","sources":{"*":{"content":"contract A {}"}},"settings":{}}`, language: LanguageSolidity, identifier: "*:A", want: "source"},
		{name: "unknown top-level", input: `{"language":"Solidity","sources":{"A.sol":{"content":""}},"settings":{},"basePath":"/tmp"}`, language: LanguageSolidity, identifier: "A.sol:A", want: "top-level"},
		{name: "empty Vyper search paths", input: `{"language":"Vyper","sources":{"A.vy":{"content":""}},"settings":{"search_paths":[]}}`, language: LanguageVyper, identifier: "A.vy:A", want: "virtual root"},
		{name: "nonempty Vyper search path", input: `{"language":"Vyper","sources":{"A.vy":{"content":""}},"settings":{"search_paths":["/tmp"]}}`, language: LanguageVyper, identifier: "A.vy:A", want: "search paths"},
		{name: "Vyper interface URL", input: `{"language":"Vyper","sources":{"A.vy":{"content":""}},"interfaces":{"I.vy":{"content":"","urls":["https://example.invalid/I.vy"]}},"settings":{}}`, language: LanguageVyper, identifier: "A.vy:A", want: "no URLs"},
		{name: "Vyper interface both forms", input: `{"language":"Vyper","sources":{"A.vy":{"content":""}},"interfaces":{"I.vy":{"content":"","abi":[]}},"settings":{}}`, language: LanguageVyper, identifier: "A.vy:A", want: "exactly one"},
		{name: "Vyper override absent source", input: `{"language":"Vyper","sources":{"A.vy":{"content":""}},"storage_layout_overrides":{"B.vy":{}},"settings":{}}`, language: LanguageVyper, identifier: "A.vy:A", want: "not a source"},
		{name: "Solidity metadata null", input: `{"language":"Solidity","sources":{"A.sol":{"content":""}},"settings":{"metadata":null}}`, language: LanguageSolidity, identifier: "A.sol:A", want: "metadata setting"},
		{name: "Solidity appendCBOR null", input: `{"language":"Solidity","sources":{"A.sol":{"content":""}},"settings":{"metadata":{"appendCBOR":null}}}`, language: LanguageSolidity, identifier: "A.sol:A", want: "appendCBOR"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			compilerVersion := "0.8.30"
			if test.language == LanguageVyper {
				compilerVersion = "0.4.3"
			}
			_, err := PrepareStandardJSON(json.RawMessage(test.input), test.language, compilerVersion, test.identifier, 64<<10)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want substring %q", err, test.want)
			}
		})
	}
}

func TestPrepareStandardJSONRejectsNonCleanVyperPaths(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		version    string
		identifier string
		input      string
		want       string
	}{
		{
			name: "dot source", version: "0.4.3", identifier: "./A.vy:A",
			input: `{"language":"Vyper","sources":{"./A.vy":{"content":""}},"settings":{}}`, want: "source path",
		},
		{
			name: "parent source", version: "0.4.3", identifier: "../A.vy:A",
			input: `{"language":"Vyper","sources":{"../A.vy":{"content":""}},"settings":{}}`, want: "source path",
		},
		{
			name: "collapsing source", version: "0.4.3", identifier: "contracts/../A.vy:A",
			input: `{"language":"Vyper","sources":{"contracts/../A.vy":{"content":""}},"settings":{}}`, want: "source path",
		},
		{
			name: "absolute source", version: "0.4.3", identifier: "/A.vy:A",
			input: `{"language":"Vyper","sources":{"/A.vy":{"content":""}},"settings":{}}`, want: "source path",
		},
		{
			name: "backslash source", version: "0.4.3", identifier: `contracts\A.vy:A`,
			input: `{"language":"Vyper","sources":{"contracts\\A.vy":{"content":""}},"settings":{}}`, want: "source path",
		},
		{
			name: "interface alias", version: "0.4.3", identifier: "A.vy:A",
			input: `{"language":"Vyper","sources":{"A.vy":{"content":""}},"interfaces":{"interfaces/../I.vyi":{"content":""}},"settings":{}}`, want: "interface path",
		},
		{
			name: "override target alias", version: "0.4.1", identifier: "A.vy:A",
			input: `{"language":"Vyper","sources":{"A.vy":{"content":""}},"storage_layout_overrides":{"contracts/../A.vy":{}},"settings":{}}`, want: "target path",
		},
		{
			name: "override file alias", version: "0.4.2", identifier: "A.vy:A",
			input: `{"language":"Vyper","sources":{"A.vy":{"content":""}},"storage_layout_overrides":{"A.vy":{"layouts/../A.json":{}}},"settings":{}}`, want: "file path",
		},
		{
			name: "override file parent", version: "0.4.3", identifier: "A.vy:A",
			input: `{"language":"Vyper","sources":{"A.vy":{"content":""}},"storage_layout_overrides":{"A.vy":{"../A.json":{}}},"settings":{}}`, want: "file path",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := PrepareStandardJSON(
				json.RawMessage(test.input),
				LanguageVyper,
				test.version,
				test.identifier,
				64<<10,
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want substring %q", err, test.want)
			}
		})
	}
}

func TestPrepareStandardJSONDefaultsSettingsAndChecksNormalizedSize(t *testing.T) {
	t.Parallel()
	input := json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":""}}}`)
	prepared, err := PrepareStandardJSON(input, LanguageSolidity, "0.8.30", "A.sol:A", 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	document := decodePreparedStandardJSON(t, prepared)
	if _, ok := document["settings"].(map[string]any); !ok {
		t.Fatalf("settings were not defaulted: %s", prepared)
	}
	if _, err := PrepareStandardJSON(input, LanguageSolidity, "0.8.30", "A.sol:A", len(input)); err == nil || !strings.Contains(err.Error(), "normalized") {
		t.Fatalf("normalized size error=%v", err)
	}
}

func TestRequestValidationRejectsStructurallyInvalidStandardJSON(t *testing.T) {
	t.Parallel()
	request := standardJSONValidRequest()
	request.StandardJSON = json.RawMessage(`{"language":"Solidity","sources":[],"settings":{}}`)
	if err := request.Validate(64 << 10); err == nil || !strings.Contains(err.Error(), "sources") {
		t.Fatalf("validation error=%v", err)
	}
}

func TestVerificationServicePersistsPreparedStandardJSON(t *testing.T) {
	t.Parallel()
	repository := &standardJSONRecordingRepository{}
	service, err := NewService(repository, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	request := standardJSONValidRequest()
	request.StandardJSON = json.RawMessage(" \n " + string(request.StandardJSON) + " \n")
	original := append(json.RawMessage(nil), request.StandardJSON...)
	expected, err := PrepareStandardJSON(request.StandardJSON, request.Language, request.CompilerVersion, request.ContractIdentifier, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	job, created, err := service.Submit(context.Background(), request)
	if err != nil || !created || job.ID == "" {
		t.Fatalf("job=%#v created=%v error=%v", job, created, err)
	}
	if repository.calls != 1 || !bytes.Equal(repository.request.StandardJSON, expected) {
		t.Fatalf("repository request=%s calls=%d, want %s", repository.request.StandardJSON, repository.calls, expected)
	}
	if !bytes.Equal(request.StandardJSON, original) {
		t.Fatal("service mutated its caller's Standard JSON")
	}
}

func TestPostgresRepositoryCanonicalizesDirectSubmissionsBeforeDigesting(t *testing.T) {
	t.Parallel()
	repository := &PostgresRepository{options: RepositoryOptions{MaxRequestBytes: 64 << 10}}
	request := standardJSONValidRequest()
	original := append(json.RawMessage(nil), request.StandardJSON...)

	encoded, _, _, _, err := repository.encodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	var persisted Request
	if err := json.Unmarshal(encoded, &persisted); err != nil {
		t.Fatal(err)
	}
	expected, err := PrepareStandardJSON(
		request.StandardJSON,
		request.Language,
		request.CompilerVersion,
		request.ContractIdentifier,
		64<<10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(persisted.StandardJSON, expected) {
		t.Fatalf("persisted Standard JSON=%s, want %s", persisted.StandardJSON, expected)
	}
	request.StandardJSON = expected
	canonicalEncoded, _, _, _, err := repository.encodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, canonicalEncoded) {
		t.Fatalf("semantic duplicates produce different payloads:\nraw=%s\nprepared=%s", encoded, canonicalEncoded)
	}
	if bytes.Equal(original, expected) {
		t.Fatal("fixture was already prepared and did not exercise repository normalization")
	}
}

type standardJSONRecordingRepository struct {
	Repository
	request Request
	calls   int
}

func (repository *standardJSONRecordingRepository) Submit(
	_ context.Context,
	request Request,
	_ ...SubmissionOptions,
) (VerificationJob, bool, error) {
	repository.calls++
	repository.request = request
	return VerificationJob{ID: "00000000-0000-4000-8000-000000000001"}, true, nil
}

func standardJSONValidRequest() Request {
	runtimeBytecode := []byte{0x60, 0x01}
	return Request{
		ChainID:            1,
		Address:            "0x" + strings.Repeat("11", 20),
		CodeHash:           "0x" + hex.EncodeToString(keccak256Bytes(runtimeBytecode)),
		AtBlockHash:        "0x" + strings.Repeat("22", 32),
		Language:           LanguageSolidity,
		CompilerVersion:    "0.8.30",
		ContractIdentifier: "A.sol:A",
		StandardJSON:       json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":""}},"settings":{}}`),
		CreationBytecode:   "0x6001",
		RuntimeBytecode:    "0x6001",
	}
}

func decodePreparedStandardJSON(t *testing.T, input json.RawMessage) map[string]any {
	t.Helper()
	document, err := decodeStandardJSONObject(input)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func assertStandardJSONOutputs(
	t *testing.T,
	selection map[string]any,
	source string,
	contract string,
	want []string,
) {
	t.Helper()
	contracts, ok := selection[source].(map[string]any)
	if !ok {
		t.Fatalf("source selection %q=%#v", source, selection[source])
	}
	assertOutputList(t, contracts[contract], want)
}

func assertFlatStandardJSONOutputs(t *testing.T, selection map[string]any, source string, want []string) {
	t.Helper()
	assertOutputList(t, selection[source], want)
}

func assertOutputList(t *testing.T, value any, want []string) {
	t.Helper()
	values, ok := value.([]any)
	if !ok {
		t.Fatalf("outputs=%#v", value)
	}
	got := make([]string, len(values))
	for index, value := range values {
		var ok bool
		got[index], ok = value.(string)
		if !ok {
			t.Fatalf("output %d=%#v", index, value)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("outputs=%v, want %v", got, want)
	}
}

func TestPrepareStandardJSONSourceCountBound(t *testing.T) {
	t.Parallel()
	sources := make(map[string]any, maxStandardJSONSources+1)
	for index := 0; index <= maxStandardJSONSources; index++ {
		sources[fmt.Sprintf("S%d.sol", index)] = map[string]string{"content": ""}
	}
	input, err := json.Marshal(map[string]any{
		"language": "Solidity",
		"sources":  sources,
		"settings": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = PrepareStandardJSON(input, LanguageSolidity, "0.8.30", "S0.sol:S", 1<<20)
	if err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("source count error=%v", err)
	}
}

func TestVerificationServiceRejectsPreparedRequestOverWholeLimit(t *testing.T) {
	t.Parallel()
	request := standardJSONValidRequest()
	service, err := NewService(&standardJSONRecordingRepository{}, len(request.StandardJSON))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = service.Submit(context.Background(), request)
	var serviceError ServiceError
	if !errors.As(err, &serviceError) || serviceError.Code != ServiceInvalidRequest {
		t.Fatalf("service error=%#v", err)
	}
}
