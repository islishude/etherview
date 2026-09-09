package wasmcompiler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSolcModernDifferential(t *testing.T) {
	path, err := filepath.Abs("../../compiler/node_modules/solc/soljson.js")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skip("reference solc not installed")
	}
	if err != nil {
		t.Fatal(err)
	}
	_, err = Extract(context.Background(), raw, sha256.Sum256(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		"pragma solidity ^0.8.0; contract A { function value() external pure returns(uint) {return 42;} }",
		"pragma solidity ^0.8.0; contract {",
		"pragma solidity ^0.8.0; import 'absent.sol'; contract A {}",
		"pragma solidity ^0.8.0; contract A { function f(uint x) public pure { assert(x > 0); } }",
	} {
		t.Run(source, func(t *testing.T) {
			settings := map[string]any{"outputSelection": map[string]any{"*": map[string]any{"*": []string{"abi", "evm.bytecode.object"}}}}
			smt := strings.Contains(source, "assert(")
			if smt {
				settings["modelChecker"] = map[string]any{"engine": "bmc", "solvers": []string{"smtlib2"}}
			}
			input, _ := json.Marshal(map[string]any{"language": "Solidity", "sources": map[string]any{"A.sol": map[string]string{"content": source}}, "settings": settings})
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			got, err := runSolcHelper(t, ctx, path, "0.8.36+commit.8a079791", input, 64<<20)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, "node", "../../compiler/wasm/reference.cjs", path)
			cmd.Stdin = bytes.NewReader(input)
			ref, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			var response struct{ Output any }
			if err = json.Unmarshal(ref, &response); err != nil {
				t.Fatal(err)
			}
			var actual any
			if err = json.Unmarshal(got, &actual); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, response.Output) {
				t.Fatalf("output mismatch: %s", got)
			}
			if smt {
				var queries struct {
					Requested struct {
						Queries map[string]string `json:"smtlib2queries"`
					} `json:"auxiliaryInputRequested"`
				}
				if json.Unmarshal(got, &queries) != nil || len(queries.Requested.Queries) == 0 {
					t.Fatal("SMT fixture did not request a solver query")
				}
			}
		})
	}
}

func TestSolcLegacyStart(t *testing.T) {
	root := os.Getenv("SOLC_BASELINE_DIR")
	if root == "" {
		t.Skip("requires compiler baseline")
	}
	raw, err := os.ReadFile(filepath.Join(root, "solc-emscripten-wasm32-v0.4.0+commit.acd334c9.js"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Extract(context.Background(), raw, sha256.Sum256(raw))
	if err != nil {
		t.Fatal(err)
	}
	_, err = runSolcHelper(t, context.Background(), filepath.Join(root, "solc-emscripten-wasm32-v0.4.0+commit.acd334c9.js"), "0.4.0+commit.acd334c9", []byte(`{"language":"Solidity","sources":{"C.sol":{"content":"contract C {}"}}}`), 64<<20)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSolcCatalogDifferential(t *testing.T) {
	root := os.Getenv("SOLC_BASELINE_DIR")
	if root == "" {
		t.Skip("requires authenticated compiler baseline")
	}
	data, err := os.ReadFile("../../compiler/wasm/solc-baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Builds []struct{ Path, LongVersion, SHA256 string }
	}
	if err = json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	for _, build := range catalog.Builds {
		t.Run(build.LongVersion, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, build.Path))
			if err != nil {
				t.Fatal(err)
			}
			digest, err := hex.DecodeString(strings.TrimPrefix(build.SHA256, "0x"))
			if err != nil || len(digest) != 32 {
				t.Fatal("invalid catalog digest")
			}
			_, err = Extract(context.Background(), raw, [32]byte(digest))
			if err != nil {
				t.Fatal(err)
			}
			input := []byte(`{"language":"Solidity","sources":{"C.sol":{"content":"contract C { function value() public returns(uint) { return 42; } }"}},"settings":{"outputSelection":{"*":{"*":["abi","evm.bytecode.object"]}}}}`)

			switch os.Getenv("SOLC_TEST_CASE") {
			case "invalid":
				input = []byte(`{"language":"Solidity","sources":{"C.sol":{"content":"contract {"}}}`)
			case "missing":
				input = []byte(`{"language":"Solidity","sources":{"C.sol":{"content":"import 'absent.sol'; contract C {}"}}}`)
			case "yul":
				input = []byte(`{"language":"Yul","sources":{"C.yul":{"content":"object \"C\" { code { mstore(0, 42) return(0, 32) } }"}},"settings":{"outputSelection":{"*":{"*":["evm.bytecode.object"]}}}}`)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "node", "../../compiler/wasm/reference.cjs", filepath.Join(root, build.Path))
			cmd.Stdin = bytes.NewReader(input)
			ref, refErr := cmd.Output()
			var expected struct {
				Version          string
				Output           any
				ReferenceFailure string
				UnexpectedStdout bool
			}
			if err = json.Unmarshal(ref, &expected); err != nil {
				t.Fatalf("reference unavailable: %v %v", refErr, err)
			}
			got, err := runSolcHelper(t, ctx, filepath.Join(root, build.Path), build.LongVersion, input, 64<<20)
			if expected.ReferenceFailure != "" || expected.UnexpectedStdout {
				if err == nil {
					t.Fatal("reference fails execution but candidate did not")
				}
				return
			}
			if normalizeVersion(expected.Version) != normalizeVersion(build.LongVersion) {
				if !errors.Is(err, ErrVersion) {
					t.Fatalf("expected version rejection, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if refErr != nil {
				t.Fatal(refErr)
			}
			var actual any
			if err = json.Unmarshal(got, &actual); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, expected.Output) {
				_ = os.WriteFile(filepath.Join(t.TempDir(), "actual.json"), got, 0o600)
				t.Fatal(jsonDifference("$", actual, expected.Output))
			}
		})
	}
}

func jsonDifference(path string, a, b any) string {
	if reflect.DeepEqual(a, b) {
		return ""
	}
	switch x := a.(type) {
	case map[string]any:
		if y, ok := b.(map[string]any); ok {
			for k, v := range x {
				other, exists := y[k]
				if !exists {
					return path + "." + k + " unexpected field"
				}
				if d := jsonDifference(path+"."+k, v, other); d != "" {
					return d
				}
			}
			for k := range y {
				if _, ok := x[k]; !ok {
					return path + "." + k + " missing field"
				}
			}
		}
	case []any:
		if y, ok := b.([]any); ok {
			if len(x) != len(y) {
				return fmt.Sprintf("%s length %d != %d", path, len(x), len(y))
			}
			for i, v := range x {
				if d := jsonDifference(fmt.Sprintf("%s[%d]", path, i), v, y[i]); d != "" {
					return d
				}
			}
		}
	}
	return fmt.Sprintf("%s: %v (%T) != %v (%T)", path, a, a, b, b)
}

func TestSolcGoldenFixtures(t *testing.T) {
	root := os.Getenv("SOLC_BASELINE_DIR")
	if root == "" {
		t.Skip("requires authenticated compiler baseline")
	}
	raw, err := os.ReadFile(filepath.Join(root, "solc-emscripten-wasm32-v0.8.30+commit.73712a01.js"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Extract(context.Background(), raw, sha256.Sum256(raw))
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := filepath.Glob("../verify/testdata/compiler/solidity/input.*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 5 {
		t.Fatalf("expected five real Solidity fixtures, got %d", len(inputs))
	}
	for _, path := range inputs {
		t.Run(filepath.Base(path), func(t *testing.T) {
			input, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			reference, err := os.ReadFile(filepath.Join(filepath.Dir(path), strings.Replace(filepath.Base(path), "input.", "output.", 1)))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			out, err := runSolcHelper(t, ctx, filepath.Join(root, "solc-emscripten-wasm32-v0.8.30+commit.73712a01.js"), "0.8.30+commit.73712a01", input, 64<<20)
			if err != nil {
				t.Fatal(err)
			}
			var actual, expected any
			if err = json.Unmarshal(out, &actual); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(reference, &expected); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatal(jsonDifference("$", actual, expected))
			}
		})
	}
}
