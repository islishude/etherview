package verify

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	solidityFixtureIdentifier = "contracts/Target.sol:Target"
	solidityNoCBORIdentifier  = "contracts/NoCBOR.sol:NoCBOR"
	vyperFixtureIdentifier    = "contracts/Target.vy:Target"
	vyperMatrixIdentifier     = "contracts/Matrix.vy:Matrix"
)

func TestSolidityCompilerFixturesMatchExactAndMetadataOnly(t *testing.T) {
	t.Parallel()

	baseOutput := readCompilerJSONFixture(t, "solidity", "output.linked.ipfs.json")
	baseArtifact := extractFixtureArtifact(
		t,
		baseOutput,
		LanguageSolidity,
		"0.8.30",
		solidityFixtureIdentifier,
	)

	t.Run("linked exact with independently populated immutables", func(t *testing.T) {
		t.Parallel()
		result, err := MatchArtifact(Request{
			Language:           LanguageSolidity,
			CompilerVersion:    "0.8.30",
			ContractIdentifier: solidityFixtureIdentifier,
			StandardJSON:       readCompilerJSONFixture(t, "solidity", "input.linked.ipfs.json"),
			CreationBytecode:   readCompilerHexFixture(t, "solidity", "creation.compiled.linked.ipfs.hex"),
			RuntimeBytecode:    readCompilerHexFixture(t, "solidity", "runtime.onchain.linked.ipfs.hex"),
		}, baseArtifact)
		if err != nil {
			t.Fatal(err)
		}
		assertMatchResult(t, result, MatchExact, MatchExact)
	})

	t.Run("comment-only compiler metadata", func(t *testing.T) {
		t.Parallel()
		result, err := MatchArtifact(Request{
			Language:           LanguageSolidity,
			CompilerVersion:    "0.8.30",
			ContractIdentifier: solidityFixtureIdentifier,
			StandardJSON:       readCompilerJSONFixture(t, "solidity", "input.linked.ipfs-comment.json"),
			CreationBytecode:   readCompilerHexFixture(t, "solidity", "creation.compiled.linked.ipfs-comment.hex"),
			RuntimeBytecode:    readCompilerHexFixture(t, "solidity", "runtime.onchain.linked.ipfs-comment.hex"),
		}, baseArtifact)
		if err != nil {
			t.Fatal(err)
		}
		assertMatchResult(t, result, MatchMetadataOnly, MatchMetadataOnly)
	})

	t.Run("bzzr1 length shift changes the creation core", func(t *testing.T) {
		t.Parallel()
		bzzr1Artifact := extractFixtureArtifact(
			t,
			readCompilerJSONFixture(t, "solidity", "output.linked.bzzr1.json"),
			LanguageSolidity,
			"0.8.30",
			solidityFixtureIdentifier,
		)
		result, err := MatchArtifact(Request{
			Language:           LanguageSolidity,
			CompilerVersion:    "0.8.30",
			ContractIdentifier: solidityFixtureIdentifier,
			StandardJSON:       readCompilerJSONFixture(t, "solidity", "input.linked.bzzr1.json"),
			CreationBytecode:   bzzr1Artifact.CreationBytecode,
			RuntimeBytecode:    readCompilerHexFixture(t, "solidity", "runtime.onchain.linked.bzzr1.hex"),
		}, baseArtifact)
		if err != nil {
			t.Fatal(err)
		}
		assertMatchResult(t, result, MatchMismatch, MatchMetadataOnly)
	})
}

func TestSolidityCompilerFixtureRejectsUnlinkedAndNonExactTargets(t *testing.T) {
	t.Parallel()

	unlinked := readCompilerJSONFixture(t, "solidity", "output.unlinked.ipfs.json")
	if _, err := extractArtifactWithVersion(
		unlinked,
		LanguageSolidity,
		"0.8.30",
		solidityFixtureIdentifier,
	); !errors.Is(err, errCompiledCodeMalformed) {
		t.Fatalf("unlinked artifact error = %v, want %v", err, errCompiledCodeMalformed)
	}

	linked := readCompilerJSONFixture(t, "solidity", "output.linked.ipfs.json")
	target := extractFixtureArtifact(t, linked, LanguageSolidity, "0.8.30", solidityFixtureIdentifier)
	other := extractFixtureArtifact(t, linked, LanguageSolidity, "0.8.30", "contracts/Scale.sol:Scale")
	if target.CreationBytecode == other.CreationBytecode || target.RuntimeBytecode == other.RuntimeBytecode {
		t.Fatal("exact target selection returned another contract artifact")
	}
	for _, identifier := range []string{
		"contracts/Missing.sol:Target",
		"contracts/Target.sol:Missing",
	} {
		if _, err := extractArtifactWithVersion(linked, LanguageSolidity, "0.8.30", identifier); !errors.Is(err, errCompilerTargetMissing) {
			t.Fatalf("identifier %q error = %v, want %v", identifier, err, errCompilerTargetMissing)
		}
	}
}

func TestSolidityCompilerFixtureWithoutCBORMatchesOnlyExactly(t *testing.T) {
	t.Parallel()

	input := readCompilerJSONFixture(t, "solidity", "input.no-cbor.json")
	artifact := extractFixtureArtifact(
		t,
		readCompilerJSONFixture(t, "solidity", "output.no-cbor.json"),
		LanguageSolidity,
		"0.8.30",
		solidityNoCBORIdentifier,
	)
	exactRequest := Request{
		Language:           LanguageSolidity,
		CompilerVersion:    "0.8.30",
		ContractIdentifier: solidityNoCBORIdentifier,
		StandardJSON:       input,
		CreationBytecode:   artifact.CreationBytecode,
		RuntimeBytecode:    artifact.RuntimeBytecode,
	}
	result, err := MatchArtifact(exactRequest, artifact)
	if err != nil {
		t.Fatal(err)
	}
	assertMatchResult(t, result, MatchExact, MatchExact)

	compiledRuntime, err := decodeBytecode(artifact.RuntimeBytecode)
	if err != nil {
		t.Fatal(err)
	}
	compiledWithExecutableSuffix := appendExclusiveTestFooter(
		compiledRuntime,
		[]byte{0xa1, 0x61, 'x', 0x01},
	)
	onchainWithExecutableSuffix := appendExclusiveTestFooter(
		compiledRuntime,
		[]byte{0xa1, 0x61, 'x', 0x02},
	)
	artifact.RuntimeBytecode = "0x" + hex.EncodeToString(compiledWithExecutableSuffix)
	exactRequest.RuntimeBytecode = "0x" + hex.EncodeToString(onchainWithExecutableSuffix)
	result, err = MatchArtifact(exactRequest, artifact)
	if err != nil {
		t.Fatal(err)
	}
	assertMatchResult(t, result, MatchExact, MatchMismatch)

	artifact.immutableReferences = map[string][]bytecodeRange{
		"1": {{Start: uint64(len(compiledRuntime) + 3), Length: 1}},
	}
	result, err = MatchArtifact(exactRequest, artifact)
	if err != nil {
		t.Fatal(err)
	}
	assertMatchResult(t, result, MatchExact, MatchExact)
}

func TestSolidityCompilerFixtureImmutableReferencesAreStrict(t *testing.T) {
	t.Parallel()

	output := readCompilerJSONFixture(t, "solidity", "output.linked.ipfs.json")
	artifact := extractFixtureArtifact(t, output, LanguageSolidity, "0.8.30", solidityFixtureIdentifier)
	compiledRuntime, err := decodeBytecode(artifact.RuntimeBytecode)
	if err != nil {
		t.Fatal(err)
	}
	footer, ok := decodeExclusiveMapFooter(compiledRuntime)
	if !ok {
		t.Fatal("official Solidity fixture has no strict terminal metadata footer")
	}

	tests := []struct {
		name       string
		references any
	}{
		{
			name: "out of bounds",
			references: map[string]any{
				"37": []any{map[string]any{"start": len(compiledRuntime) - 1, "length": 32}},
			},
		},
		{
			name: "overlap",
			references: map[string]any{
				"37": []any{map[string]any{"start": 72, "length": 32}},
				"39": []any{map[string]any{"start": 80, "length": 32}},
			},
		},
		{
			name: "one identifier has inconsistent widths",
			references: map[string]any{
				"37": []any{
					map[string]any{"start": 72, "length": 32},
					map[string]any{"start": 130, "length": 20},
				},
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			malformed := replaceFixtureTargetField(
				t,
				output,
				[]string{"evm", "deployedBytecode", "immutableReferences"},
				test.references,
			)
			if _, err := extractArtifactWithVersion(
				malformed,
				LanguageSolidity,
				"0.8.30",
				solidityFixtureIdentifier,
			); !errors.Is(err, errCompilerOutputMalformed) {
				t.Fatalf("error = %v, want %v", err, errCompilerOutputMalformed)
			}
		})
	}

	t.Run("immutable reference overlaps enabled terminal compiler metadata", func(t *testing.T) {
		t.Parallel()
		malformed := replaceFixtureTargetField(
			t,
			output,
			[]string{"evm", "deployedBytecode", "immutableReferences"},
			map[string]any{
				"37": []any{map[string]any{"start": footer.Start, "length": 1}},
			},
		)
		malformedArtifact, err := extractArtifactWithVersion(
			malformed,
			LanguageSolidity,
			"0.8.30",
			solidityFixtureIdentifier,
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = MatchArtifact(Request{
			Language:           LanguageSolidity,
			CompilerVersion:    "0.8.30",
			ContractIdentifier: solidityFixtureIdentifier,
			StandardJSON:       readCompilerJSONFixture(t, "solidity", "input.linked.ipfs.json"),
			CreationBytecode:   malformedArtifact.CreationBytecode,
			RuntimeBytecode:    malformedArtifact.RuntimeBytecode,
		}, malformedArtifact)
		if !errors.Is(err, errCompilerOutputMalformed) {
			t.Fatalf("error = %v, want %v", err, errCompilerOutputMalformed)
		}
	})

	t.Run("one immutable identifier cannot resolve to two values", func(t *testing.T) {
		t.Parallel()
		onchain, err := decodeBytecode(readCompilerHexFixture(t, "solidity", "runtime.onchain.linked.ipfs.hex"))
		if err != nil {
			t.Fatal(err)
		}
		ranges := artifact.immutableReferences["37"]
		if len(ranges) != 2 {
			t.Fatalf("fixture immutable range count = %d, want 2", len(ranges))
		}
		onchain[int(ranges[0].Start)] ^= 0xff
		result, err := MatchArtifact(Request{
			Language:           LanguageSolidity,
			CompilerVersion:    "0.8.30",
			ContractIdentifier: solidityFixtureIdentifier,
			StandardJSON:       readCompilerJSONFixture(t, "solidity", "input.linked.ipfs.json"),
			CreationBytecode:   artifact.CreationBytecode,
			RuntimeBytecode:    "0x" + hex.EncodeToString(onchain),
		}, artifact)
		if err != nil {
			t.Fatal(err)
		}
		assertMatchResult(t, result, MatchExact, MatchMismatch)
	})
}

func TestVyperCompilerFixturesMatchExactMetadataOnlyAndNoMetadata(t *testing.T) {
	t.Parallel()

	baseArtifact := extractFixtureArtifact(
		t,
		readCompilerJSONFixture(t, "vyper", "output.metadata.json"),
		LanguageVyper,
		"0.4.3",
		vyperFixtureIdentifier,
	)

	tests := []struct {
		name            string
		input           string
		output          string
		creation        string
		wantCreation    MatchKind
		wantRuntime     MatchKind
		compareWithBase bool
	}{
		{
			name:         "exact",
			input:        "input.metadata.json",
			output:       "output.metadata.json",
			creation:     "creation.compiled.metadata.hex",
			wantCreation: MatchExact,
			wantRuntime:  MatchExact,
		},
		{
			name:            "comment-only integrity metadata",
			input:           "input.metadata-comment.json",
			creation:        "creation.compiled.metadata-comment.hex",
			wantCreation:    MatchMetadataOnly,
			wantRuntime:     MatchExact,
			compareWithBase: true,
		},
		{
			name:         "metadata disabled",
			input:        "input.no-metadata.json",
			output:       "output.no-metadata.json",
			creation:     "creation.compiled.no-metadata.hex",
			wantCreation: MatchExact,
			wantRuntime:  MatchExact,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			artifact := baseArtifact
			if !test.compareWithBase {
				artifact = extractFixtureArtifact(
					t,
					readCompilerJSONFixture(t, "vyper", test.output),
					LanguageVyper,
					"0.4.3",
					vyperFixtureIdentifier,
				)
			}
			result, err := MatchArtifact(Request{
				Language:           LanguageVyper,
				CompilerVersion:    "0.4.3",
				ContractIdentifier: vyperFixtureIdentifier,
				StandardJSON:       readCompilerJSONFixture(t, "vyper", test.input),
				CreationBytecode:   readCompilerHexFixture(t, "vyper", test.creation),
				RuntimeBytecode:    readCompilerHexFixture(t, "vyper", "runtime.onchain.synthetic.hex"),
			}, artifact)
			if err != nil {
				t.Fatal(err)
			}
			assertMatchResult(t, result, test.wantCreation, test.wantRuntime)
		})
	}
}

func TestOfficialVyperStandardJSONVersionMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		version      string
		auxArity     int
		wantLayout   bool
		wantWarnings bool
	}{
		{version: "0.3.10", auxArity: 4, wantWarnings: true},
		{version: "0.4.0", auxArity: 4},
		{version: "0.4.1", auxArity: 5, wantLayout: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.version, func(t *testing.T) {
			t.Parallel()
			input := readCompilerJSONFixture(t, "vyper-version-matrix", "input-"+test.version+".json")
			output := readCompilerJSONFixture(t, "vyper-version-matrix", "output-"+test.version+".json")
			if test.wantWarnings {
				document, err := decodeRawJSONObject(output)
				if err != nil {
					t.Fatal(err)
				}
				var diagnostics []map[string]json.RawMessage
				if err := decodeStrictJSON(document["errors"], &diagnostics); err != nil || len(diagnostics) == 0 {
					t.Fatal("official output does not contain the expected warning diagnostics")
				}
				for _, diagnostic := range diagnostics {
					var severity string
					if err := json.Unmarshal(diagnostic["severity"], &severity); err != nil || severity != "warning" {
						t.Fatal("official output contains a non-warning diagnostic")
					}
				}
			}
			artifact := extractFixtureArtifact(
				t,
				output,
				LanguageVyper,
				test.version,
				vyperMatrixIdentifier,
			)
			if !jsonObject(artifact.Metadata) {
				t.Fatal("official Vyper metadata was not retained as an object")
			}
			if artifact.vyperLayoutPresent != test.wantLayout {
				t.Fatalf("layout present = %v, want %v", artifact.vyperLayoutPresent, test.wantLayout)
			}

			creation, err := decodeBytecode(artifact.CreationBytecode)
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := decodeBytecode(artifact.RuntimeBytecode)
			if err != nil {
				t.Fatal(err)
			}
			footer, auxdata, ok := decodeVyperAuxdata(creation)
			version, versionOK := parseVyperVersion(test.version)
			if !ok || !versionOK || footer.Start <= 0 || auxdata.Arity != test.auxArity ||
				auxdata.RuntimeSize != uint64(len(runtime)) || auxdata.ImmutableSize != 0 ||
				auxdata.Compiler != version {
				t.Fatalf("footer=%+v auxdata=%+v version=%+v ok=%v versionOK=%v", footer, auxdata, version, ok, versionOK)
			}

			result, err := MatchArtifact(Request{
				Language:           LanguageVyper,
				CompilerVersion:    test.version,
				ContractIdentifier: vyperMatrixIdentifier,
				StandardJSON:       input,
				CreationBytecode:   artifact.CreationBytecode,
				RuntimeBytecode:    artifact.RuntimeBytecode,
			}, artifact)
			if err != nil {
				t.Fatal(err)
			}
			assertMatchResult(t, result, MatchExact, MatchExact)
		})
	}
}

func TestVyperCompilerFixtureRejectsAuxdataAndLayoutContradictions(t *testing.T) {
	t.Parallel()

	output := readCompilerJSONFixture(t, "vyper", "output.metadata.json")
	input := readCompilerJSONFixture(t, "vyper", "input.metadata.json")
	creation := readCompilerHexFixture(t, "vyper", "creation.compiled.metadata.hex")
	runtime := readCompilerHexFixture(t, "vyper", "runtime.onchain.synthetic.hex")

	for _, test := range []struct {
		name   string
		layout any
	}{
		{
			name: "gap",
			layout: map[string]any{"code_layout": map[string]any{
				"owner": map[string]any{"offset": 0, "length": 32, "type": "address"},
				"seed":  map[string]any{"offset": 33, "length": 32, "type": "uint256"},
			}},
		},
		{
			name: "overlap",
			layout: map[string]any{"code_layout": map[string]any{
				"owner": map[string]any{"offset": 0, "length": 32, "type": "address"},
				"seed":  map[string]any{"offset": 16, "length": 32, "type": "uint256"},
			}},
		},
		{
			name: "partial leaf",
			layout: map[string]any{"code_layout": map[string]any{
				"owner": map[string]any{"offset": 0, "length": 32},
			}},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			malformed := replaceFixtureTargetField(t, output, []string{"layout"}, test.layout)
			if _, err := extractArtifactWithVersion(
				malformed,
				LanguageVyper,
				"0.4.3",
				vyperFixtureIdentifier,
			); !errors.Is(err, errCompilerOutputMalformed) {
				t.Fatalf("error = %v, want %v", err, errCompilerOutputMalformed)
			}
		})
	}

	t.Run("auxdata immutable size disagrees with layout", func(t *testing.T) {
		t.Parallel()
		oneWordLayout := map[string]any{"code_layout": map[string]any{
			"owner": map[string]any{"offset": 0, "length": 32, "type": "address"},
		}}
		artifact := extractFixtureArtifact(
			t,
			replaceFixtureTargetField(t, output, []string{"layout"}, oneWordLayout),
			LanguageVyper,
			"0.4.3",
			vyperFixtureIdentifier,
		)
		_, err := MatchArtifact(Request{
			Language:           LanguageVyper,
			CompilerVersion:    "0.4.3",
			ContractIdentifier: vyperFixtureIdentifier,
			StandardJSON:       input,
			CreationBytecode:   creation,
			RuntimeBytecode:    runtime,
		}, artifact)
		if !errors.Is(err, errCompilerOutputMalformed) {
			t.Fatalf("error = %v, want %v", err, errCompilerOutputMalformed)
		}
	})

	t.Run("runtime immutable suffix has the wrong size", func(t *testing.T) {
		t.Parallel()
		artifact := extractFixtureArtifact(t, output, LanguageVyper, "0.4.3", vyperFixtureIdentifier)
		result, err := MatchArtifact(Request{
			Language:           LanguageVyper,
			CompilerVersion:    "0.4.3",
			ContractIdentifier: vyperFixtureIdentifier,
			StandardJSON:       input,
			CreationBytecode:   creation,
			RuntimeBytecode:    runtime[:len(runtime)-2],
		}, artifact)
		if err != nil {
			t.Fatal(err)
		}
		assertMatchResult(t, result, MatchExact, MatchMismatch)
	})

	t.Run("malformed on-chain creation auxdata is not metadata-only", func(t *testing.T) {
		t.Parallel()
		artifact := extractFixtureArtifact(t, output, LanguageVyper, "0.4.3", vyperFixtureIdentifier)
		malformedCreation, err := decodeBytecode(
			readCompilerHexFixture(t, "vyper", "creation.compiled.metadata-comment.hex"),
		)
		if err != nil {
			t.Fatal(err)
		}
		malformedCreation[len(malformedCreation)-1]++
		result, err := MatchArtifact(Request{
			Language:           LanguageVyper,
			CompilerVersion:    "0.4.3",
			ContractIdentifier: vyperFixtureIdentifier,
			StandardJSON:       readCompilerJSONFixture(t, "vyper", "input.metadata-comment.json"),
			CreationBytecode:   "0x" + hex.EncodeToString(malformedCreation),
			RuntimeBytecode:    runtime,
		}, artifact)
		if err != nil {
			t.Fatal(err)
		}
		assertMatchResult(t, result, MatchMismatch, MatchExact)
	})
}

func TestOfficialLegacyVyperFooterBoundaries(t *testing.T) {
	t.Parallel()

	t.Run("0.3.4 fixed map without a length field", func(t *testing.T) {
		t.Parallel()
		_, runtime := readLegacyVyperOutput(t, "0.3.4")
		execution, err := decodeBytecode(readCompilerHexFixture(t, "legacy-vyper", "runtime-execution-0.3.4.hex"))
		if err != nil {
			t.Fatal(err)
		}
		runtimeBytes, err := decodeBytecode(runtime)
		if err != nil {
			t.Fatal(err)
		}
		assertLegacyVyperExact(t, "0.3.4", runtime)
		footer, version, ok := decodeVyperFixedVersionFooter(runtimeBytes)
		if !ok || footer.Start != len(execution) || version != (vyperVersion{Major: 0, Minor: 3, Patch: 4}) {
			t.Fatalf("footer=%+v version=%+v ok=%v", footer, version, ok)
		}
		if _, _, ok := decodeVyperExclusiveVersionFooter(runtimeBytes); ok {
			t.Fatal("0.3.4 fixed footer was accepted as an exclusive-length footer")
		}
	})

	t.Run("0.3.9 map with exclusive payload length", func(t *testing.T) {
		t.Parallel()
		_, runtime := readLegacyVyperOutput(t, "0.3.9")
		execution, err := decodeBytecode(readCompilerHexFixture(t, "legacy-vyper", "runtime-execution-0.3.9.hex"))
		if err != nil {
			t.Fatal(err)
		}
		runtimeBytes, err := decodeBytecode(runtime)
		if err != nil {
			t.Fatal(err)
		}
		assertLegacyVyperExact(t, "0.3.9", runtime)
		footer, version, ok := decodeVyperExclusiveVersionFooter(runtimeBytes)
		if !ok || footer.Start != len(execution) || version != (vyperVersion{Major: 0, Minor: 3, Patch: 9}) {
			t.Fatalf("footer=%+v version=%+v ok=%v", footer, version, ok)
		}
		if _, _, ok := decodeVyperFixedVersionFooter(runtimeBytes); ok {
			t.Fatal("0.3.9 exclusive-length footer was accepted as a fixed footer")
		}
	})

	t.Run("0.3.10 creation tuple with inclusive total length", func(t *testing.T) {
		t.Parallel()
		creation, runtime := readLegacyVyperOutput(t, "0.3.10")
		creationBytes, err := decodeBytecode(creation)
		if err != nil {
			t.Fatal(err)
		}
		runtimeBytes, err := decodeBytecode(runtime)
		if err != nil {
			t.Fatal(err)
		}
		assertLegacyVyperExact(t, "0.3.10", runtime)
		footer, auxdata, ok := decodeVyperAuxdata(creationBytes)
		if !ok || footer.Start != len(creationBytes)-18 || auxdata.Arity != 4 ||
			auxdata.RuntimeSize != uint64(len(runtimeBytes)) || auxdata.ImmutableSize != 0 ||
			auxdata.Compiler != (vyperVersion{Major: 0, Minor: 3, Patch: 10}) {
			t.Fatalf("footer=%+v auxdata=%+v ok=%v", footer, auxdata, ok)
		}
		if _, _, ok := decodeVyperAuxdata(runtimeBytes); ok {
			t.Fatal("0.3.10 runtime unexpectedly carried creation auxdata")
		}
	})
}

func TestCompilerMetadataCBORRejectsMalformedTerminalFooters(t *testing.T) {
	t.Parallel()

	core := []byte{0x60, 0x01}
	compiled := appendExclusiveTestFooter(core, []byte{0xa1, 0x61, 'x', 0x01})
	tests := []struct {
		name     string
		bytecode []byte
	}{
		{
			name:     "duplicate map key",
			bytecode: appendExclusiveTestFooter(core, []byte{0xa2, 0x61, 'x', 0x01, 0x61, 'x', 0x02}),
		},
		{
			name: "nested duplicate map key",
			bytecode: appendExclusiveTestFooter(core, []byte{
				0xa1, 0x61, 'x', 0xa2, 0x61, 'a', 0x01, 0x61, 'a', 0x02,
			}),
		},
		{
			name:     "indefinite map",
			bytecode: appendExclusiveTestFooter(core, []byte{0xbf, 0x61, 'x', 0x01, 0xff}),
		},
		{
			name:     "tagged map",
			bytecode: appendExclusiveTestFooter(core, []byte{0xc1, 0xa1, 0x61, 'x', 0x01}),
		},
		{
			name:     "empty map",
			bytecode: appendExclusiveTestFooter(core, []byte{0xa0}),
		},
		{
			name:     "truncated payload length",
			bytecode: append(append([]byte(nil), compiled...), 0x00),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, ok := decodeExclusiveMapFooter(test.bytecode); ok {
				t.Fatal("malformed terminal footer was decoded")
			}
			kind, err := MatchBytecode("0x"+hex.EncodeToString(test.bytecode), "0x"+hex.EncodeToString(compiled))
			if err != nil {
				t.Fatal(err)
			}
			if kind != MatchMismatch {
				t.Fatalf("match kind = %s, want %s", kind, MatchMismatch)
			}
		})
	}
}

func extractFixtureArtifact(
	t *testing.T,
	output json.RawMessage,
	language Language,
	compilerVersion string,
	identifier string,
) Artifact {
	t.Helper()
	artifact, err := extractArtifactWithVersion(output, language, compilerVersion, identifier)
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func extractArtifactWithVersion(
	output json.RawMessage,
	language Language,
	compilerVersion string,
	identifier string,
) (Artifact, error) {
	return ExtractArtifact(output, language, compilerVersion, identifier)
}

func readCompilerJSONFixture(t *testing.T, path ...string) json.RawMessage {
	t.Helper()
	return json.RawMessage(readCompilerFixture(t, path...))
}

func readCompilerHexFixture(t *testing.T, path ...string) string {
	t.Helper()
	return strings.TrimSpace(string(readCompilerFixture(t, path...)))
}

func readCompilerFixture(t *testing.T, path ...string) []byte {
	t.Helper()
	parts := append([]string{"testdata", "compiler"}, path...)
	contents, err := os.ReadFile(filepath.Join(parts...))
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func readLegacyVyperOutput(t *testing.T, version string) (string, string) {
	t.Helper()
	fields := strings.Fields(string(readCompilerFixture(t, "legacy-vyper", "output-"+version+".txt")))
	if len(fields) != 2 {
		t.Fatalf("legacy Vyper %s output fields = %d, want 2", version, len(fields))
	}
	return fields[0], fields[1]
}

func assertLegacyVyperExact(t *testing.T, compilerVersion, runtime string) {
	t.Helper()
	creation, fixtureRuntime := readLegacyVyperOutput(t, compilerVersion)
	if runtime != fixtureRuntime {
		t.Fatal("legacy runtime argument does not match the official compiler output")
	}
	version, ok := parseVyperVersion(compilerVersion)
	if !ok {
		t.Fatalf("legacy compiler version %q is malformed", compilerVersion)
	}
	result, err := MatchArtifact(Request{
		Language:         LanguageVyper,
		CompilerVersion:  compilerVersion,
		StandardJSON:     json.RawMessage(`{"settings":{}}`),
		CreationBytecode: creation,
		RuntimeBytecode:  runtime,
	}, Artifact{
		CreationBytecode:    creation,
		RuntimeBytecode:     runtime,
		language:            LanguageVyper,
		vyperLayoutPresent:  false,
		vyperVersion:        version,
		vyperVersionPresent: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertMatchResult(t, result, MatchExact, MatchExact)
}

func replaceFixtureTargetField(
	t *testing.T,
	output json.RawMessage,
	fieldPath []string,
	replacement any,
) json.RawMessage {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(output, &document); err != nil {
		t.Fatal(err)
	}
	contracts := mustFixtureObject(t, document["contracts"])
	sourceContracts, exists := contracts["contracts/Target.sol"]
	if !exists {
		sourceContracts, exists = contracts["contracts/Target.vy"]
	}
	if !exists {
		t.Fatal("fixture target source is missing")
	}
	current := mustFixtureObject(t, sourceContracts)
	current = mustFixtureObject(t, current["Target"])
	for _, key := range fieldPath[:len(fieldPath)-1] {
		current = mustFixtureObject(t, current[key])
	}
	current[fieldPath[len(fieldPath)-1]] = replacement
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func mustFixtureObject(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("fixture value has type %T, want object", value)
	}
	return object
}

func appendExclusiveTestFooter(core, payload []byte) []byte {
	result := make([]byte, 0, len(core)+len(payload)+2)
	result = append(result, core...)
	result = append(result, payload...)
	var length [2]byte
	binary.BigEndian.PutUint16(length[:], uint16(len(payload)))
	return append(result, length[:]...)
}

func assertMatchResult(t *testing.T, result MatchResult, creation, runtime MatchKind) {
	t.Helper()
	if result.Creation != creation || result.Runtime != runtime {
		t.Fatalf("match result = %+v, want creation=%s runtime=%s", result, creation, runtime)
	}
}
