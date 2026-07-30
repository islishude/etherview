package verify

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestPrepareStandardJSONSolidityMultiFileExactSelection(t *testing.T) {
	t.Parallel()
	input := json.RawMessage(`{
		"language":"Solidity",
		"sources":{
			"contracts/A.sol":{"content":"contract A {}"},
			"contracts/B.sol":{"content":"contract B {}"}
		},
		"settings":{
			"optimizer":{"enabled":true,"runs":200},
			"outputSelection":{"*":{"*":["abi"]}}
		}
	}`)
	prepared, err := PrepareStandardJSON(
		input, LanguageSolidity, "0.8.30", "contracts/A.sol:A", 64<<10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(prepared) || bytes.Contains(prepared, []byte(`"urls"`)) {
		t.Fatalf("prepared input is invalid: %s", prepared)
	}
	var document struct {
		Language string `json:"language"`
		Settings struct {
			OutputSelection map[string]map[string][]string `json:"outputSelection"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(prepared, &document); err != nil {
		t.Fatal(err)
	}
	if document.Language != "Solidity" {
		t.Fatalf("language = %q", document.Language)
	}
	outputs := document.Settings.OutputSelection["contracts/A.sol"]["A"]
	for _, required := range solidityRequiredOutputs {
		if !containsString(outputs, required) {
			t.Fatalf("required output %q is absent from %#v", required, outputs)
		}
	}
	again, err := PrepareStandardJSON(
		prepared, LanguageSolidity, "0.8.30", "contracts/A.sol:A", 64<<10,
	)
	if err != nil || !bytes.Equal(prepared, again) {
		t.Fatalf("preparation is not idempotent: error=%v", err)
	}
}

func TestPrepareStandardJSONRejectsUnsupportedLanguageAndIndirectInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		input      string
		language   Language
		identifier string
	}{
		{
			name:       "removed Vyper language",
			input:      `{"language":"Vyper","sources":{"A.vy":{"content":""}},"settings":{}}`,
			language:   Language("vyper"),
			identifier: "A.vy:A",
		},
		{
			name:       "language mismatch",
			input:      `{"language":"Yul","sources":{"A.yul":{"content":"object \"A\" {}"}},"settings":{}}`,
			language:   LanguageSolidity,
			identifier: "A.yul:A",
		},
		{
			name:       "remote source",
			input:      `{"language":"Solidity","sources":{"A.sol":{"urls":["https://example.invalid/A.sol"]}},"settings":{}}`,
			language:   LanguageSolidity,
			identifier: "A.sol:A",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := PrepareStandardJSON(
				json.RawMessage(test.input), test.language, "0.8.30",
				test.identifier, 64<<10,
			); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestPrepareStandardJSONBoundsWholeNormalizedInput(t *testing.T) {
	t.Parallel()
	input := json.RawMessage(`{
		"language":"Solidity",
		"sources":{"A.sol":{"content":"contract A {}"}},
		"settings":{}
	}`)
	prepared, err := PrepareStandardJSON(
		input, LanguageSolidity, "0.8.30", "A.sol:A", 64<<10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareStandardJSON(
		input, LanguageSolidity, "0.8.30", "A.sol:A", len(prepared)-1,
	); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("normalized size error = %v", err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
