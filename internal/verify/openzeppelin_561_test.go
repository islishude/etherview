package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestCanonicalOpenZeppelinSourcePathAcceptsPinnedHardhatAndStandardJSONNames(t *testing.T) {
	t.Parallel()
	const want = "proxy/utils/UUPSUpgradeable.sol"
	for _, name := range []string{
		"npm/@openzeppelin/contracts@5.6.1/proxy/utils/UUPSUpgradeable.sol",
		"@openzeppelin/contracts/proxy/utils/UUPSUpgradeable.sol",
		"node_modules/@openzeppelin/contracts/proxy/utils/UUPSUpgradeable.sol",
		"project/node_modules/@openzeppelin/contracts/proxy/utils/UUPSUpgradeable.sol",
		"contracts/proxy/utils/UUPSUpgradeable.sol",
		"npm\\@openzeppelin\\contracts@5.6.1\\proxy\\utils\\UUPSUpgradeable.sol",
	} {
		if got := canonicalOpenZeppelinSourcePath(name); got != want {
			t.Errorf("canonicalOpenZeppelinSourcePath(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestCanonicalOpenZeppelinSourcePathRejectsUnpinnedOrAmbiguousNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"npm/@openzeppelin/contracts@5.6.0/proxy/Proxy.sol",
		"npm/@openzeppelin/contracts@6.0.0/proxy/Proxy.sol",
		"npm/@openzeppelin/contracts-upgradeable@5.6.1/proxy/utils/UUPSUpgradeable.sol",
		"vendor/contracts/proxy/Proxy.sol",
		"evilcontracts/proxy/Proxy.sol",
		"contracts/../proxy/Proxy.sol",
		"contracts/proxy//Proxy.sol",
		"contracts/./proxy/Proxy.sol",
		"contracts/",
	} {
		if got := canonicalOpenZeppelinSourcePath(name); got != "" {
			t.Errorf("canonicalOpenZeppelinSourcePath(%q) = %q, want rejection", name, got)
		}
	}
}

func TestRecognizeOpenZeppelin561ArtifactRejectsUnauthenticatedSources(t *testing.T) {
	installSyntheticOpenZeppelinSources(t, oz561ERC1967Sources)
	target := common.HexToAddress("0x1000000000000000000000000000000000000001")

	assertRecognized := func(t *testing.T, outcome openZeppelinTestOutcome) {
		t.Helper()
		artifact, recognized := recognizeOpenZeppelin561Artifact(
			marshalOpenZeppelinTestOutcome(t, outcome), target, []byte{0x60, 0x00},
		)
		if !recognized || artifact.Kind != proxyArtifactERC1967 {
			t.Fatalf("recognized=%t artifact=%+v", recognized, artifact)
		}
	}

	assertRecognized(t, newERC1967TestOutcome(syntheticOpenZeppelinSources(oz561ERC1967Sources)))
	for _, test := range []struct {
		name   string
		mutate func(map[string]openZeppelinTestSource)
	}{
		{
			name: "tampered required source",
			mutate: func(sources map[string]openZeppelinTestSource) {
				path := "proxy/ERC1967/ERC1967Proxy.sol"
				sources[openZeppelinTestSourceName(path)] = openZeppelinTestSource{
					Content: "// source bytes were replaced\n",
				}
			},
		},
		{
			name: "missing required source",
			mutate: func(sources map[string]openZeppelinTestSource) {
				delete(sources, openZeppelinTestSourceName("proxy/Proxy.sol"))
			},
		},
		{
			name: "duplicate canonical alias",
			mutate: func(sources map[string]openZeppelinTestSource) {
				path := "proxy/Proxy.sol"
				sources["node_modules/@openzeppelin/contracts/"+path] = sources[openZeppelinTestSourceName(path)]
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			sources := syntheticOpenZeppelinSources(oz561ERC1967Sources)
			test.mutate(sources)
			artifact, recognized := recognizeOpenZeppelin561Artifact(
				marshalOpenZeppelinTestOutcome(t, newERC1967TestOutcome(sources)),
				target,
				[]byte{0x60, 0x00},
			)
			if recognized || artifact != (recognizedProxyArtifact{}) {
				t.Fatalf("recognized=%t artifact=%+v", recognized, artifact)
			}
		})
	}
}

func TestRecognizeOpenZeppelin561ArtifactRejectsInvalidImmutableEvidence(t *testing.T) {
	installSyntheticOpenZeppelinSources(t, oz561TransparentSources)
	target := common.HexToAddress("0x2000000000000000000000000000000000000002")
	admin := common.HexToAddress("0x3000000000000000000000000000000000000003")

	baseline, baselineRuntime := newTransparentTestOutcome(admin)
	artifact, recognized := recognizeOpenZeppelin561Artifact(
		marshalOpenZeppelinTestOutcome(t, baseline), target, baselineRuntime,
	)
	if !recognized || artifact.Kind != proxyArtifactTransparent ||
		artifact.RuntimeImmutable == nil || *artifact.RuntimeImmutable != admin {
		t.Fatalf("baseline recognized=%t artifact=%+v", recognized, artifact)
	}

	for _, test := range []struct {
		name   string
		mutate func(*openZeppelinTestOutcome, []byte)
	}{
		{
			name: "missing immutable reference",
			mutate: func(outcome *openZeppelinTestOutcome, _ []byte) {
				outcome.RuntimeCodeArtifacts.ImmutableReferences = map[string][]bytecodeRange{}
			},
		},
		{
			name: "forged immutable reference offset",
			mutate: func(outcome *openZeppelinTestOutcome, _ []byte) {
				outcome.RuntimeCodeArtifacts.ImmutableReferences[openZeppelinTestImmutableID] =
					[]bytecodeRange{{Start: openZeppelinTestImmutableOffset + 1, Length: common.HashLength}}
			},
		},
		{
			name: "missing immutable transformation",
			mutate: func(outcome *openZeppelinTestOutcome, _ []byte) {
				outcome.RuntimeMatch.Transformations = nil
			},
		},
		{
			name: "forged immutable transformation reason",
			mutate: func(outcome *openZeppelinTestOutcome, _ []byte) {
				outcome.RuntimeMatch.Transformations[0].Reason = "library"
			},
		},
		{
			name: "forged immutable value",
			mutate: func(outcome *openZeppelinTestOutcome, _ []byte) {
				forged := openZeppelinTestAddressWord(
					common.HexToAddress("0x4000000000000000000000000000000000000004"),
				)
				outcome.RuntimeMatch.Values.Immutables[openZeppelinTestImmutableID] =
					"0x" + hex.EncodeToString(forged)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			outcome, runtime := newTransparentTestOutcome(admin)
			test.mutate(&outcome, runtime)
			artifact, recognized := recognizeOpenZeppelin561Artifact(
				marshalOpenZeppelinTestOutcome(t, outcome), target, runtime,
			)
			if recognized || artifact != (recognizedProxyArtifact{}) {
				t.Fatalf("recognized=%t artifact=%+v", recognized, artifact)
			}
		})
	}
}

func TestRecognizeOpenZeppelin561ArtifactRejectsForgedUUPSIdentity(t *testing.T) {
	installSyntheticOpenZeppelinSources(t, oz561UUPSSources)
	target := common.HexToAddress("0x5000000000000000000000000000000000000005")

	baseline, baselineRuntime := newUUPSTestOutcome(target)
	artifact, recognized := recognizeOpenZeppelin561Artifact(
		marshalOpenZeppelinTestOutcome(t, baseline), target, baselineRuntime,
	)
	if !recognized || artifact.Kind != proxyArtifactUUPS ||
		artifact.RuntimeImmutable == nil || *artifact.RuntimeImmutable != target {
		t.Fatalf("baseline recognized=%t artifact=%+v", recognized, artifact)
	}

	for _, test := range []struct {
		name   string
		mutate func(*openZeppelinTestOutcome, []byte)
	}{
		{
			name: "missing OpenZeppelin base",
			mutate: func(outcome *openZeppelinTestOutcome, _ []byte) {
				outcome.CompilationArtifacts.LinearizedBases = nil
			},
		},
		{
			name: "forged OpenZeppelin base identity",
			mutate: func(outcome *openZeppelinTestOutcome, _ []byte) {
				outcome.CompilationArtifacts.LinearizedBases = []string{
					"@openzeppelin/contracts/proxy/utils/UUPSUpgradeable.sol:ForgedUUPSUpgradeable",
				}
			},
		},
		{
			name: "missing interface version ABI",
			mutate: func(outcome *openZeppelinTestOutcome, _ []byte) {
				outcome.ABI = append([]map[string]any(nil), outcome.ABI[:1]...)
				outcome.ABI = append(outcome.ABI, validUUPSTestABI()[2])
			},
		},
		{
			name: "forged upgrade ABI mutability",
			mutate: func(outcome *openZeppelinTestOutcome, _ []byte) {
				outcome.ABI[2]["stateMutability"] = "nonpayable"
			},
		},
		{
			name: "missing self immutable variable",
			mutate: func(outcome *openZeppelinTestOutcome, _ []byte) {
				delete(outcome.CompilationArtifacts.ImmutableVariables, openZeppelinTestImmutableID)
			},
		},
		{
			name: "forged self immutable",
			mutate: func(outcome *openZeppelinTestOutcome, runtime []byte) {
				forged := openZeppelinTestAddressWord(
					common.HexToAddress("0x6000000000000000000000000000000000000006"),
				)
				copy(runtime[openZeppelinTestImmutableOffset:], forged)
				outcome.RuntimeMatch.Values.Immutables[openZeppelinTestImmutableID] =
					"0x" + hex.EncodeToString(forged)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			outcome, runtime := newUUPSTestOutcome(target)
			test.mutate(&outcome, runtime)
			artifact, recognized := recognizeOpenZeppelin561Artifact(
				marshalOpenZeppelinTestOutcome(t, outcome), target, runtime,
			)
			if recognized || artifact != (recognizedProxyArtifact{}) {
				t.Fatalf("recognized=%t artifact=%+v", recognized, artifact)
			}
		})
	}
}

const (
	openZeppelinTestImmutableID     = "7"
	openZeppelinTestImmutableOffset = 8
)

type openZeppelinTestSource struct {
	Content string `json:"content"`
}

type openZeppelinTestCompilationArtifacts struct {
	LinearizedBases    []string          `json:"linearizedBaseContracts"`
	ImmutableVariables map[string]string `json:"immutableVariables"`
}

type openZeppelinTestRuntimeArtifacts struct {
	ImmutableReferences map[string][]bytecodeRange `json:"immutableReferences"`
}

type openZeppelinTestOutcome struct {
	FileName             string                               `json:"file_name"`
	ContractName         string                               `json:"contract_name"`
	ABI                  []map[string]any                     `json:"abi"`
	Sources              map[string]openZeppelinTestSource    `json:"sources"`
	CompilationArtifacts openZeppelinTestCompilationArtifacts `json:"compilation_artifacts"`
	RuntimeCodeArtifacts openZeppelinTestRuntimeArtifacts     `json:"runtime_code_artifacts"`
	RuntimeMatch         *VerificationMatchDetails            `json:"runtime_match"`
}

func newERC1967TestOutcome(sources map[string]openZeppelinTestSource) openZeppelinTestOutcome {
	return openZeppelinTestOutcome{
		FileName:     "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol",
		ContractName: "ERC1967Proxy",
		ABI:          []map[string]any{},
		Sources:      sources,
		CompilationArtifacts: openZeppelinTestCompilationArtifacts{
			ImmutableVariables: map[string]string{},
		},
		RuntimeCodeArtifacts: openZeppelinTestRuntimeArtifacts{
			ImmutableReferences: map[string][]bytecodeRange{},
		},
		RuntimeMatch: &VerificationMatchDetails{MatchType: VerificationMatchFull},
	}
}

func newTransparentTestOutcome(admin common.Address) (openZeppelinTestOutcome, []byte) {
	return newImmutableOpenZeppelinTestOutcome(
		"@openzeppelin/contracts/proxy/transparent/TransparentUpgradeableProxy.sol",
		"TransparentUpgradeableProxy",
		"@openzeppelin/contracts/proxy/transparent/TransparentUpgradeableProxy.sol:TransparentUpgradeableProxy:_admin",
		admin,
		syntheticOpenZeppelinSources(oz561TransparentSources),
	)
}

func newUUPSTestOutcome(target common.Address) (openZeppelinTestOutcome, []byte) {
	outcome, runtime := newImmutableOpenZeppelinTestOutcome(
		"src/TestUUPSImplementation.sol",
		"TestUUPSImplementation",
		"@openzeppelin/contracts/proxy/utils/UUPSUpgradeable.sol:UUPSUpgradeable:__self",
		target,
		syntheticOpenZeppelinSources(oz561UUPSSources),
	)
	outcome.CompilationArtifacts.LinearizedBases = []string{
		"src/TestUUPSImplementation.sol:TestUUPSImplementation",
		"@openzeppelin/contracts/proxy/utils/UUPSUpgradeable.sol:UUPSUpgradeable",
	}
	outcome.ABI = validUUPSTestABI()
	return outcome, runtime
}

func newImmutableOpenZeppelinTestOutcome(
	fileName string,
	contractName string,
	immutableIdentity string,
	immutable common.Address,
	sources map[string]openZeppelinTestSource,
) (openZeppelinTestOutcome, []byte) {
	word := openZeppelinTestAddressWord(immutable)
	runtime := make([]byte, 64)
	copy(runtime[openZeppelinTestImmutableOffset:], word)
	return openZeppelinTestOutcome{
		FileName:     fileName,
		ContractName: contractName,
		ABI:          []map[string]any{},
		Sources:      sources,
		CompilationArtifacts: openZeppelinTestCompilationArtifacts{
			ImmutableVariables: map[string]string{openZeppelinTestImmutableID: immutableIdentity},
		},
		RuntimeCodeArtifacts: openZeppelinTestRuntimeArtifacts{
			ImmutableReferences: map[string][]bytecodeRange{
				openZeppelinTestImmutableID: {{
					Start: openZeppelinTestImmutableOffset, Length: common.HashLength,
				}},
			},
		},
		RuntimeMatch: &VerificationMatchDetails{
			MatchType: VerificationMatchPartial,
			Transformations: []Transformation{{
				Type: "replace", Reason: "immutable",
				Offset: openZeppelinTestImmutableOffset, ID: openZeppelinTestImmutableID,
			}},
			Values: TransformationValues{Immutables: map[string]string{
				openZeppelinTestImmutableID: "0x" + hex.EncodeToString(word),
			}},
		},
	}, runtime
}

func validUUPSTestABI() []map[string]any {
	return []map[string]any{
		{
			"type": "function", "name": "proxiableUUID", "stateMutability": "view",
			"inputs": []any{}, "outputs": []any{map[string]any{"name": "", "type": "bytes32"}},
		},
		{
			"type": "function", "name": "UPGRADE_INTERFACE_VERSION", "stateMutability": "view",
			"inputs": []any{}, "outputs": []any{map[string]any{"name": "", "type": "string"}},
		},
		{
			"type": "function", "name": "upgradeToAndCall", "stateMutability": "payable",
			"inputs": []any{
				map[string]any{"name": "newImplementation", "type": "address"},
				map[string]any{"name": "data", "type": "bytes"},
			},
			"outputs": []any{},
		},
	}
}

func installSyntheticOpenZeppelinSources(t *testing.T, paths []string) {
	t.Helper()
	original := maps.Clone(openZeppelin561SourceSHA256)
	t.Cleanup(func() { openZeppelin561SourceSHA256 = original })
	for path, source := range syntheticOpenZeppelinSources(paths) {
		canonical := canonicalOpenZeppelinSourcePath(path)
		digest := sha256.Sum256([]byte(source.Content))
		openZeppelin561SourceSHA256[canonical] = hex.EncodeToString(digest[:])
	}
}

func syntheticOpenZeppelinSources(paths []string) map[string]openZeppelinTestSource {
	sources := make(map[string]openZeppelinTestSource, len(paths))
	for _, path := range paths {
		sources[openZeppelinTestSourceName(path)] = openZeppelinTestSource{
			Content: "// synthetic OpenZeppelin 5.6.1 fixture: " + path + "\n",
		}
	}
	return sources
}

func openZeppelinTestSourceName(path string) string {
	return "@openzeppelin/contracts/" + path
}

func openZeppelinTestAddressWord(address common.Address) []byte {
	word := make([]byte, common.HashLength)
	copy(word[common.HashLength-common.AddressLength:], address[:])
	return word
}

func marshalOpenZeppelinTestOutcome(t *testing.T, outcome openZeppelinTestOutcome) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(outcome)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestVerificationProxyReplayTargetKindSeparatesDirectUUPSProbe(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		artifact *recognizedProxyArtifact
		want     string
	}{
		{name: "ordinary proxy", artifact: nil, want: "proxy"},
		{name: "ERC1967 proxy", artifact: &recognizedProxyArtifact{Kind: proxyArtifactERC1967}, want: "proxy"},
		{name: "upgradeable beacon", artifact: &recognizedProxyArtifact{Kind: proxyArtifactUpgradeableBeacon}, want: "beacon"},
		{name: "UUPS implementation", artifact: &recognizedProxyArtifact{Kind: proxyArtifactUUPS}, want: "uups"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := verificationProxyReplayTargetKind(test.artifact); got != test.want {
				t.Fatalf("replay target kind=%q, want %q", got, test.want)
			}
		})
	}
}
