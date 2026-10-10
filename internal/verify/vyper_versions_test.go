package verify

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVyperStableVersionMatrix(t *testing.T) {
	var profiles map[string]VyperCapabilities
	if err := json.Unmarshal(vyperCapabilitiesJSON, &profiles); err != nil {
		t.Fatal(err)
	}
	for version := range profiles {
		t.Run(version, func(t *testing.T) {
			directory := filepath.Join("testdata/compiler/vyper/versions", version)
			files, err := filepath.Glob(filepath.Join(directory, "*.output.json"))
			if err != nil || len(files) < 3 {
				t.Fatalf("missing version fixtures: %v", err)
			}
			for _, file := range files {
				t.Run(filepath.Base(file), func(t *testing.T) {
					name := strings.TrimSuffix(file, ".output.json")
					first, err := os.ReadFile(file)
					if err != nil {
						t.Fatal(err)
					}
					second, err := os.ReadFile(name + ".modified_output.json")
					if err != nil {
						t.Fatal(err)
					}
					candidates, err := ExtractCandidatesV2(first, second, LanguageVyper, version)
					if strings.HasPrefix(filepath.Base(name), "invalid") {
						if err == nil {
							t.Fatal("accepted invalid source")
						}
						return
					}
					if err != nil || len(candidates) != 1 {
						t.Fatalf("candidate extraction: %v", err)
					}
					candidate := candidates[0]
					pair := BytecodePair{Creation: candidate.CreationBytecode, Runtime: candidate.RuntimeBytecode}
					if strings.HasSuffix(name, "constructor") || strings.HasSuffix(name, "immutable") {
						word := strings.Repeat("0", 62) + "2a"
						pair.Creation += word
						if strings.HasSuffix(name, "immutable") {
							if len(candidate.runtimeImmutables) == 0 {
								t.Fatal("missing immutable layout")
							}
							pair.Runtime = pair.Runtime[:len(pair.Runtime)-64] + word
						}
					}
					matches, err := VerifyCandidateArtifacts(candidates, pair, "", true)
					if err != nil || len(matches) != 1 {
						t.Fatalf("verification: %v matches=%d", err, len(matches))
					}
					// A change outside declared metadata and immutable ranges must not match.
					pair.Runtime = "0xfe" + pair.Runtime[4:]
					matches, err = VerifyCandidateArtifacts(candidates, pair, "", true)
					if err == nil && len(matches) > 0 && matches[0].Runtime != nil {
						t.Fatal("accepted undeclared runtime difference")
					}
				})
			}
		})
	}
}

func TestVyperVersionSettings(t *testing.T) {
	input := json.RawMessage(`{"language":"Vyper","sources":{"A.vy":{"content":""}},"settings":{"optimize":"none"}}`)
	if _, err := PrepareVyperStandardJSON(input, "A.vy", "0.2.0", 1<<20); err == nil {
		t.Fatal("accepted ignored optimization setting")
	}
	if _, err := PrepareVyperStandardJSON(input, "A.vy", "0.3.7", 1<<20); err != nil {
		t.Fatal(err)
	}
	input = json.RawMessage(`{"language":"Vyper","sources":{"A.vy":{"content":""}},"settings":{"optimize":"codesize"}}`)
	if _, err := PrepareVyperStandardJSON(input, "A.vy", "0.3.7", 1<<20); err == nil {
		t.Fatal("accepted unavailable optimization mode")
	}
	if _, err := PrepareVyperStandardJSON(input, "A.vy", "0.3.10", 1<<20); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"0.2.6", "0.3.5", "0.4.3rc1", "0.1.0-beta.17"} {
		if _, ok := VyperVersionCapabilities(version); ok {
			t.Fatalf("unsupported release advertised: %s", version)
		}
	}
}

func TestVyperDynamicRuntimeMatrix(t *testing.T) {
	root := os.Getenv("ETHERVIEW_TEST_VYPER_MATRIX_ROOT")
	if root == "" {
		t.Skip("run make test-vyper-matrix for the WASM runtime matrix")
	}
	var profiles map[string]VyperCapabilities
	if err := json.Unmarshal(vyperCapabilitiesJSON, &profiles); err != nil {
		t.Fatal(err)
	}
	for version := range profiles {
		t.Run(version, func(t *testing.T) {
			runtimePath := filepath.Join(root, version)
			raw, err := os.ReadFile(filepath.Join(runtimePath, "package-manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			var manifest vyperWASMManifest
			if err := json.Unmarshal(raw, &manifest); err != nil {
				t.Fatal(err)
			}
			digest, err := decodeCatalogDigest(manifest.CompilerSHA256)
			if err != nil {
				t.Fatal(err)
			}
			compiler := newVyperTestCompiler(t)
			compiler.PackagePath = copyWASMTestTree(t, runtimePath)
			compiler.Version = version
			compiler.CompilerDigest = digest
			compiler.ManifestDigest = sha256.Sum256(raw)
			if err := compiler.ValidateRuntime(t.Context()); err != nil {
				t.Fatal(err)
			}
			fixtures := filepath.Join("testdata/compiler/vyper/versions", version)
			files, err := filepath.Glob(filepath.Join(fixtures, "*.input.json"))
			if err != nil || len(files) < 3 {
				t.Fatalf("incomplete matrix: %v", err)
			}
			for _, file := range files {
				name := strings.TrimSuffix(file, ".input.json")
				for _, variant := range []struct{ input, output string }{{"input", "output"}, {"modified", "modified_output"}} {
					t.Run(filepath.Base(name)+"/"+variant.input, func(t *testing.T) {
						input, err := os.ReadFile(name + "." + variant.input + ".json")
						if err != nil {
							t.Fatal(err)
						}
						actual, err := compiler.Compile(t.Context(), LanguageVyper, version, input)
						if err != nil {
							t.Fatal(err)
						}
						expected, err := os.ReadFile(name + "." + variant.output + ".json")
						if err != nil {
							t.Fatal(err)
						}
						assertVyperReference(t, actual, expected, strings.HasPrefix(filepath.Base(name), "invalid"))
					})
				}
			}
			compiler.ManifestDigest[0] ^= 1
			if _, err := compiler.run(t.Context(), []string{"--compile", "5242880", "33554432"}, []byte("{}")); err == nil {
				t.Fatal("executed changed runtime identity")
			}
		})
	}
}

func assertVyperReference(t *testing.T, actual, expected []byte, invalid bool) {
	t.Helper()
	type result struct {
		Contracts json.RawMessage `json:"contracts"`
		Errors    []struct {
			Severity string `json:"severity"`
			Type     string `json:"type"`
		} `json:"errors"`
	}
	var got, want result
	if json.Unmarshal(actual, &got) != nil || json.Unmarshal(expected, &want) != nil {
		t.Fatal("invalid compiler result")
	}
	if !invalid {
		if !equalJSONValue(got.Contracts, want.Contracts) {
			t.Fatal("compiler contracts differ from official reference")
		}
		return
	}
	for _, actualError := range got.Errors {
		for _, expectedError := range want.Errors {
			if actualError.Severity == "error" && actualError.Type == expectedError.Type {
				return
			}
		}
	}
	t.Fatal("compiler diagnostics differ from official reference")
}
