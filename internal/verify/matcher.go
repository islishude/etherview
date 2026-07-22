package verify

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
)

const (
	maxMatcherRanges        = 4096
	maxMatcherBytecodeBytes = 64 << 20
)

var (
	errCompilerOutputMalformed  = errors.New("compiler output is malformed")
	errCompilerOutputDiagnostic = errors.New("compiler output contains an error diagnostic")
	errCompilerTargetMissing    = errors.New("compiler output target is missing")
	errCompiledCodeMalformed    = errors.New("compiler output bytecode is malformed")
	errOnchainCodeMalformed     = errors.New("on-chain bytecode is malformed")
	errCompilerVersionMalformed = errors.New("compiler version is invalid")
)

type bytecodeRange struct {
	Start  uint64 `json:"start"`
	Length uint64 `json:"length"`
}

type Artifact struct {
	CreationBytecode string
	RuntimeBytecode  string
	ABI              json.RawMessage
	Metadata         json.RawMessage
	Layout           json.RawMessage

	language            Language
	immutableReferences map[string][]bytecodeRange
	vyperImmutableSize  int
	vyperLayoutPresent  bool
	vyperVersion        vyperVersion
	vyperVersionPresent bool
}

// ExtractArtifact validates and selects one exact Solidity or Vyper Standard
// JSON compiler output target. Compiler-provided diagnostics and malformed
// fields never cross the stable error boundary.
func ExtractArtifact(
	output json.RawMessage,
	language Language,
	compilerVersion string,
	identifier string,
) (Artifact, error) {
	var parsedVyperVersion vyperVersion
	if language == LanguageVyper {
		var versionOK bool
		parsedVyperVersion, versionOK = parseVyperVersion(compilerVersion)
		if !versionOK {
			return Artifact{}, errCompilerVersionMalformed
		}
	}
	source, name, err := parseStandardJSONContractIdentifier(identifier, language)
	if err != nil {
		return Artifact{}, errCompilerTargetMissing
	}
	document, err := decodeRawJSONObject(output)
	if err != nil {
		return Artifact{}, errCompilerOutputMalformed
	}
	if err := validateCompilerDiagnostics(document["errors"]); err != nil {
		return Artifact{}, err
	}
	contracts, err := decodeRawJSONObject(document["contracts"])
	if err != nil {
		return Artifact{}, errCompilerOutputMalformed
	}
	sourceContracts, exists := contracts[source]
	if !exists {
		return Artifact{}, errCompilerTargetMissing
	}
	contractMap, err := decodeRawJSONObject(sourceContracts)
	if err != nil {
		return Artifact{}, errCompilerOutputMalformed
	}
	contractRaw, exists := contractMap[name]
	if !exists {
		return Artifact{}, errCompilerTargetMissing
	}
	contract, err := decodeRawJSONObject(contractRaw)
	if err != nil || !jsonArray(contract["abi"]) {
		return Artifact{}, errCompilerOutputMalformed
	}

	metadataRequired := language != LanguageVyper || parsedVyperVersion.atLeast(0, 3, 10)
	metadata, err := validateCompilerMetadata(contract["metadata"], language, metadataRequired)
	if err != nil {
		return Artifact{}, err
	}
	evm, err := decodeRawJSONObject(contract["evm"])
	if err != nil {
		return Artifact{}, errCompilerOutputMalformed
	}
	creation, creationLinks, creationImmutables, err := parseCompilerBytecode(
		evm["bytecode"],
		language == LanguageSolidity,
		false,
	)
	if err != nil {
		return Artifact{}, err
	}
	runtime, runtimeLinks, immutableReferences, err := parseCompilerBytecode(
		evm["deployedBytecode"],
		language == LanguageSolidity,
		language == LanguageSolidity,
	)
	if err != nil {
		return Artifact{}, err
	}
	if len(creationLinks) != 0 || len(runtimeLinks) != 0 || len(creationImmutables) != 0 ||
		(language == LanguageVyper && len(immutableReferences) != 0) {
		return Artifact{}, errCompiledCodeMalformed
	}

	artifact := Artifact{
		CreationBytecode:    creation,
		RuntimeBytecode:     runtime,
		ABI:                 append(json.RawMessage(nil), contract["abi"]...),
		Metadata:            metadata,
		language:            language,
		vyperVersion:        parsedVyperVersion,
		vyperVersionPresent: language == LanguageVyper,
	}
	switch language {
	case LanguageSolidity:
		compiledRuntime, decodeErr := decodeBytecode(runtime)
		if decodeErr != nil || len(compiledRuntime) == 0 {
			return Artifact{}, errCompiledCodeMalformed
		}
		if err := validateImmutableReferences(immutableReferences, compiledRuntime); err != nil {
			return Artifact{}, err
		}
		artifact.immutableReferences = immutableReferences
	case LanguageVyper:
		layout := contract["layout"]
		layoutRequired := parsedVyperVersion.atLeast(0, 4, 1)
		if len(layout) == 0 {
			if layoutRequired {
				return Artifact{}, errCompilerOutputMalformed
			}
		} else {
			if !jsonObject(layout) {
				return Artifact{}, errCompilerOutputMalformed
			}
			immutableSize, layoutErr := parseVyperImmutableLayout(layout)
			if layoutErr != nil {
				return Artifact{}, layoutErr
			}
			artifact.Layout = append(json.RawMessage(nil), layout...)
			artifact.vyperImmutableSize = immutableSize
			artifact.vyperLayoutPresent = true
		}
	default:
		return Artifact{}, errCompilerOutputMalformed
	}
	return artifact, nil
}

func validateCompilerDiagnostics(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if !jsonArray(raw) {
		return errCompilerOutputMalformed
	}
	var diagnostics []map[string]json.RawMessage
	if err := decodeStrictJSON(raw, &diagnostics); err != nil {
		return errCompilerOutputMalformed
	}
	for _, diagnostic := range diagnostics {
		var severity string
		if err := json.Unmarshal(diagnostic["severity"], &severity); err != nil || severity == "" {
			return errCompilerOutputMalformed
		}
		if strings.EqualFold(severity, "error") {
			return errCompilerOutputDiagnostic
		}
	}
	return nil
}

func validateCompilerMetadata(raw json.RawMessage, language Language, required bool) (json.RawMessage, error) {
	switch language {
	case LanguageSolidity:
		var encoded string
		if err := json.Unmarshal(raw, &encoded); err != nil || !jsonObject(json.RawMessage(encoded)) ||
			validateUniqueJSON([]byte(encoded)) != nil {
			return nil, errCompilerOutputMalformed
		}
		return json.RawMessage(append([]byte(nil), encoded...)), nil
	case LanguageVyper:
		if len(raw) == 0 && !required {
			return nil, nil
		}
		if !jsonObject(raw) {
			return nil, errCompilerOutputMalformed
		}
		return append(json.RawMessage(nil), raw...), nil
	default:
		return nil, errCompilerOutputMalformed
	}
}

func parseCompilerBytecode(
	raw json.RawMessage,
	requireLinkReferences bool,
	requireImmutableReferences bool,
) (string, map[string]map[string][]bytecodeRange, map[string][]bytecodeRange, error) {
	fields, err := decodeRawJSONObject(raw)
	if err != nil {
		return "", nil, nil, errCompilerOutputMalformed
	}
	var object string
	if err := json.Unmarshal(fields["object"], &object); err != nil || object == "" {
		return "", nil, nil, errCompiledCodeMalformed
	}
	byteLength, err := compilerBytecodeLength(object)
	if err != nil || byteLength == 0 || byteLength > maxMatcherBytecodeBytes {
		return "", nil, nil, errCompiledCodeMalformed
	}

	links := make(map[string]map[string][]bytecodeRange)
	if rawLinks, exists := fields["linkReferences"]; exists {
		if !jsonObject(rawLinks) || decodeStrictJSON(rawLinks, &links) != nil || links == nil {
			return "", nil, nil, errCompilerOutputMalformed
		}
		if err := validateLinkReferences(links, byteLength); err != nil {
			return "", nil, nil, err
		}
	} else if requireLinkReferences {
		return "", nil, nil, errCompilerOutputMalformed
	}

	immutableReferences := make(map[string][]bytecodeRange)
	if rawImmutables, exists := fields["immutableReferences"]; exists {
		if !jsonObject(rawImmutables) || decodeStrictJSON(rawImmutables, &immutableReferences) != nil || immutableReferences == nil {
			return "", nil, nil, errCompilerOutputMalformed
		}
	} else if requireImmutableReferences {
		return "", nil, nil, errCompilerOutputMalformed
	}
	if len(links) != 0 {
		return prefixHex(object), links, immutableReferences, nil
	}
	decoded, err := decodeBytecode(object)
	if err != nil || len(decoded) != byteLength {
		return "", nil, nil, errCompiledCodeMalformed
	}
	return prefixHex(object), links, immutableReferences, nil
}

func compilerBytecodeLength(value string) (int, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "0x")
	if value == "" || len(value)%2 != 0 {
		return 0, errCompiledCodeMalformed
	}
	return len(value) / 2, nil
}

func validateLinkReferences(references map[string]map[string][]bytecodeRange, byteLength int) error {
	all := make([]bytecodeRange, 0)
	for source, libraries := range references {
		if source == "" || libraries == nil {
			return errCompilerOutputMalformed
		}
		for library, ranges := range libraries {
			if library == "" || len(ranges) == 0 {
				return errCompilerOutputMalformed
			}
			for _, span := range ranges {
				if span.Length != 20 {
					return errCompilerOutputMalformed
				}
				all = append(all, span)
			}
		}
	}
	return validateRanges(all, byteLength)
}

func validateImmutableReferences(references map[string][]bytecodeRange, compiled []byte) error {
	all := make([]bytecodeRange, 0)
	for identifier, ranges := range references {
		if identifier == "" || len(ranges) == 0 {
			return errCompilerOutputMalformed
		}
		length := ranges[0].Length
		for _, span := range ranges {
			if span.Length != length {
				return errCompilerOutputMalformed
			}
			all = append(all, span)
		}
	}
	if err := validateRanges(all, len(compiled)); err != nil {
		return err
	}
	return nil
}

func validateRanges(ranges []bytecodeRange, byteLength int) error {
	if len(ranges) > maxMatcherRanges {
		return errCompilerOutputMalformed
	}
	sorted := append([]bytecodeRange(nil), ranges...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Start == sorted[j].Start {
			return sorted[i].Length < sorted[j].Length
		}
		return sorted[i].Start < sorted[j].Start
	})
	var previousEnd uint64
	for index, span := range sorted {
		if span.Length == 0 || span.Start > uint64(byteLength) ||
			span.Length > uint64(byteLength)-span.Start {
			return errCompilerOutputMalformed
		}
		end := span.Start + span.Length
		if index > 0 && span.Start < previousEnd {
			return errCompilerOutputMalformed
		}
		previousEnd = end
	}
	return nil
}

func decodeRawJSONObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if !jsonObject(raw) {
		return nil, errCompilerOutputMalformed
	}
	var object map[string]json.RawMessage
	if err := decodeStrictJSON(raw, &object); err != nil || object == nil {
		return nil, errCompilerOutputMalformed
	}
	return object, nil
}

func decodeStrictJSON(raw []byte, destination any) error {
	if err := validateUniqueJSON(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("JSON contains trailing values")
	}
	return nil
}

func MatchBytecode(onchain, compiled string) (MatchKind, error) {
	onchainBytes, err := decodeBytecode(onchain)
	if err != nil {
		return MatchMismatch, errOnchainCodeMalformed
	}
	compiledBytes, err := decodeBytecode(compiled)
	if err != nil {
		return MatchMismatch, errCompiledCodeMalformed
	}
	return matchSolidityBytes(onchainBytes, compiledBytes, nil, true), nil
}

func MatchArtifact(request Request, artifact Artifact) (MatchResult, error) {
	if artifact.language != "" && artifact.language != request.Language {
		return MatchResult{}, errors.New("compiler artifact language mismatch")
	}
	switch request.Language {
	case LanguageSolidity:
		metadataEnabled, err := solidityBytecodeMetadataEnabled(request.StandardJSON)
		if err != nil {
			return MatchResult{}, err
		}
		creation, err := matchSolidityBytecode(
			request.CreationBytecode,
			artifact.CreationBytecode,
			nil,
			metadataEnabled,
		)
		if err != nil {
			return MatchResult{}, err
		}
		runtime, err := matchSolidityBytecode(
			request.RuntimeBytecode,
			artifact.RuntimeBytecode,
			artifact.immutableReferences,
			metadataEnabled,
		)
		if err != nil {
			return MatchResult{}, err
		}
		return MatchResult{Creation: creation, Runtime: runtime}, nil
	case LanguageVyper:
		return matchVyperArtifact(request, artifact)
	default:
		return MatchResult{}, errors.New("unsupported compiler artifact language")
	}
}

func matchSolidityBytecode(
	onchain string,
	compiled string,
	references map[string][]bytecodeRange,
	metadataEnabled bool,
) (MatchKind, error) {
	onchainBytes, err := decodeBytecode(onchain)
	if err != nil {
		return MatchMismatch, errOnchainCodeMalformed
	}
	compiledBytes, err := decodeBytecode(compiled)
	if err != nil {
		return MatchMismatch, errCompiledCodeMalformed
	}
	if metadataEnabled {
		if footer, ok := decodeExclusiveMapFooter(compiledBytes); ok &&
			!rangesFitBefore(references, footer.Start) {
			return MatchMismatch, errCompilerOutputMalformed
		}
	}
	if !validOnchainImmutableReferences(references, onchainBytes, metadataEnabled) {
		return MatchMismatch, nil
	}
	return matchSolidityBytes(onchainBytes, compiledBytes, references, metadataEnabled), nil
}

func matchSolidityBytes(
	onchain []byte,
	compiled []byte,
	references map[string][]bytecodeRange,
	metadataEnabled bool,
) MatchKind {
	if len(onchain) == len(compiled) {
		normalizedOnchain := append([]byte(nil), onchain...)
		normalizedCompiled := append([]byte(nil), compiled...)
		maskBytecodeRanges(normalizedOnchain, references)
		maskBytecodeRanges(normalizedCompiled, references)
		if bytes.Equal(normalizedOnchain, normalizedCompiled) {
			return MatchExact
		}
	}
	if !metadataEnabled {
		return MatchMismatch
	}
	onchainFooter, onchainOK := decodeExclusiveMapFooter(onchain)
	compiledFooter, compiledOK := decodeExclusiveMapFooter(compiled)
	if !onchainOK || !compiledOK || bytes.Equal(onchainFooter.Raw, compiledFooter.Raw) ||
		onchainFooter.Start != compiledFooter.Start {
		return MatchMismatch
	}
	onchainCore := append([]byte(nil), onchain[:onchainFooter.Start]...)
	compiledCore := append([]byte(nil), compiled[:compiledFooter.Start]...)
	if !rangesFitBefore(references, len(onchainCore)) {
		return MatchMismatch
	}
	maskBytecodeRanges(onchainCore, references)
	maskBytecodeRanges(compiledCore, references)
	if bytes.Equal(onchainCore, compiledCore) {
		return MatchMetadataOnly
	}
	return MatchMismatch
}

func solidityBytecodeMetadataEnabled(input json.RawMessage) (bool, error) {
	document, err := decodeRawJSONObject(input)
	if err != nil {
		return false, errors.New("Solidity Standard JSON is malformed")
	}
	rawSettings, exists := document["settings"]
	if !exists {
		return true, nil
	}
	settings, err := decodeRawJSONObject(rawSettings)
	if err != nil {
		return false, errors.New("Solidity Standard JSON settings are malformed")
	}
	rawMetadata, exists := settings["metadata"]
	if !exists {
		return true, nil
	}
	metadata, err := decodeRawJSONObject(rawMetadata)
	if err != nil {
		return false, errors.New("Solidity metadata settings are malformed")
	}
	rawAppend, exists := metadata["appendCBOR"]
	if !exists {
		return true, nil
	}
	trimmed := bytes.TrimSpace(rawAppend)
	var enabled bool
	if !bytes.Equal(trimmed, []byte("true")) && !bytes.Equal(trimmed, []byte("false")) ||
		json.Unmarshal(trimmed, &enabled) != nil {
		return false, errors.New("Solidity appendCBOR setting is malformed")
	}
	return enabled, nil
}

func validOnchainImmutableReferences(
	references map[string][]bytecodeRange,
	onchain []byte,
	metadataEnabled bool,
) bool {
	for _, ranges := range references {
		var value []byte
		for _, span := range ranges {
			if span.Start > uint64(len(onchain)) || span.Length > uint64(len(onchain))-span.Start {
				return false
			}
			candidate := onchain[int(span.Start):int(span.Start+span.Length)]
			if value == nil {
				value = candidate
			} else if !bytes.Equal(value, candidate) {
				return false
			}
		}
	}
	if metadataEnabled {
		if footer, ok := decodeExclusiveMapFooter(onchain); ok {
			return rangesFitBefore(references, footer.Start)
		}
	}
	return true
}

func rangesFitBefore(references map[string][]bytecodeRange, boundary int) bool {
	for _, ranges := range references {
		for _, span := range ranges {
			if span.Start > uint64(boundary) || span.Length > uint64(boundary)-span.Start {
				return false
			}
		}
	}
	return true
}

func maskBytecodeRanges(bytecode []byte, references map[string][]bytecodeRange) {
	for _, ranges := range references {
		for _, span := range ranges {
			if span.Start > uint64(len(bytecode)) || span.Length > uint64(len(bytecode))-span.Start {
				continue
			}
			clear(bytecode[int(span.Start):int(span.Start+span.Length)])
		}
	}
}

func decodeBytecode(value string) ([]byte, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "0x")
	if len(value)%2 != 0 {
		return nil, errors.New("hex bytecode has odd length")
	}
	if len(value)/2 > maxMatcherBytecodeBytes {
		return nil, errors.New("hex bytecode exceeds the matcher limit")
	}
	if strings.ContainsAny(value, "_$") {
		return nil, errors.New("bytecode contains unresolved link placeholders")
	}
	return hex.DecodeString(value)
}

func prefixHex(value string) string {
	if strings.HasPrefix(value, "0x") {
		return value
	}
	return "0x" + value
}
