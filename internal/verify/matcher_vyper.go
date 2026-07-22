package verify

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/fxamacker/cbor/v2"
)

type vyperVersion struct {
	Major uint64
	Minor uint64
	Patch uint64
}

type vyperAuxdata struct {
	Arity         int
	Integrity     []byte
	RuntimeSize   uint64
	DataSizes     []uint64
	ImmutableSize uint64
	Compiler      vyperVersion
}

func parseVyperImmutableLayout(layout json.RawMessage) (int, error) {
	fields, err := decodeRawJSONObject(layout)
	if err != nil {
		return 0, errCompilerOutputMalformed
	}
	rawCodeLayout, exists := fields["code_layout"]
	if !exists {
		return 0, nil
	}
	if !jsonObject(rawCodeLayout) {
		return 0, errCompilerOutputMalformed
	}
	root, err := decodeRawJSONObject(rawCodeLayout)
	if err != nil {
		return 0, errCompilerOutputMalformed
	}
	ranges := make([]bytecodeRange, 0, len(root))
	count := 0
	for name, raw := range root {
		if name == "" || collectVyperCodeLayout(raw, 1, &ranges, &count) != nil {
			return 0, errCompilerOutputMalformed
		}
	}
	if len(ranges) == 0 {
		return 0, nil
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Start < ranges[j].Start })
	var expected uint64
	for _, span := range ranges {
		if span.Start != expected || span.Length == 0 || span.Length > uint64(maxMatcherBytecodeBytes)-span.Start {
			return 0, errCompilerOutputMalformed
		}
		expected = span.Start + span.Length
	}
	if expected > uint64(maxMatcherBytecodeBytes) {
		return 0, errCompilerOutputMalformed
	}
	return int(expected), nil
}

func collectVyperCodeLayout(
	raw json.RawMessage,
	depth int,
	ranges *[]bytecodeRange,
	count *int,
) error {
	if depth > 8 {
		return errCompilerOutputMalformed
	}
	fields, err := decodeRawJSONObject(raw)
	if err != nil || len(fields) == 0 {
		return errCompilerOutputMalformed
	}
	_, hasOffset := fields["offset"]
	_, hasLength := fields["length"]
	_, hasType := fields["type"]
	if hasOffset || hasLength || hasType {
		if !hasOffset || !hasLength || !hasType || len(fields) != 3 {
			return errCompilerOutputMalformed
		}
		offset, err := canonicalJSONUint(fields["offset"])
		if err != nil {
			return err
		}
		length, err := canonicalJSONUint(fields["length"])
		if err != nil || length == 0 {
			return errCompilerOutputMalformed
		}
		var valueType string
		if json.Unmarshal(fields["type"], &valueType) != nil || strings.TrimSpace(valueType) == "" || len(valueType) > 256 {
			return errCompilerOutputMalformed
		}
		(*count)++
		if *count > maxMatcherRanges {
			return errCompilerOutputMalformed
		}
		*ranges = append(*ranges, bytecodeRange{Start: offset, Length: length})
		return nil
	}
	for name, child := range fields {
		if name == "" || collectVyperCodeLayout(child, depth+1, ranges, count) != nil {
			return errCompilerOutputMalformed
		}
	}
	return nil
}

func canonicalJSONUint(raw json.RawMessage) (uint64, error) {
	value := strings.TrimSpace(string(raw))
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return 0, errCompilerOutputMalformed
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, errCompilerOutputMalformed
	}
	return parsed, nil
}

func parseVyperVersion(value string) (vyperVersion, bool) {
	if value == "" || strings.TrimSpace(value) != value {
		return vyperVersion{}, false
	}
	value = strings.TrimPrefix(value, "v")
	firstDot := strings.IndexByte(value, '.')
	if firstDot <= 0 {
		return vyperVersion{}, false
	}
	secondRelative := strings.IndexByte(value[firstDot+1:], '.')
	if secondRelative <= 0 {
		return vyperVersion{}, false
	}
	secondDot := firstDot + 1 + secondRelative
	patchEnd := secondDot + 1
	for patchEnd < len(value) && value[patchEnd] >= '0' && value[patchEnd] <= '9' {
		patchEnd++
	}
	if patchEnd == secondDot+1 {
		return vyperVersion{}, false
	}
	for _, character := range value[patchEnd:] {
		if !(character >= '0' && character <= '9' || character >= 'A' && character <= 'Z' ||
			character >= 'a' && character <= 'z' || strings.ContainsRune("+-._", character)) {
			return vyperVersion{}, false
		}
	}
	major, errMajor := strconv.ParseUint(value[:firstDot], 10, 64)
	minor, errMinor := strconv.ParseUint(value[firstDot+1:secondDot], 10, 64)
	patch, errPatch := strconv.ParseUint(value[secondDot+1:patchEnd], 10, 64)
	if errMajor != nil || errMinor != nil || errPatch != nil {
		return vyperVersion{}, false
	}
	return vyperVersion{Major: major, Minor: minor, Patch: patch}, true
}

func (version vyperVersion) atLeast(major, minor, patch uint64) bool {
	if version.Major != major {
		return version.Major > major
	}
	if version.Minor != minor {
		return version.Minor > minor
	}
	return version.Patch >= patch
}

func decodeVyperAuxdata(bytecode []byte) (decodedFooter, vyperAuxdata, bool) {
	footer, values, ok := decodeInclusiveArrayFooter(bytecode)
	if !ok || (len(values) != 4 && len(values) != 5) {
		return decodedFooter{}, vyperAuxdata{}, false
	}
	auxdata := vyperAuxdata{Arity: len(values)}
	index := 0
	if len(values) == 5 {
		if matcherCBORMode.Unmarshal(values[0], &auxdata.Integrity) != nil || len(auxdata.Integrity) != 32 {
			return decodedFooter{}, vyperAuxdata{}, false
		}
		index++
	}
	if matcherCBORMode.Unmarshal(values[index], &auxdata.RuntimeSize) != nil ||
		matcherCBORMode.Unmarshal(values[index+1], &auxdata.DataSizes) != nil ||
		matcherCBORMode.Unmarshal(values[index+2], &auxdata.ImmutableSize) != nil {
		return decodedFooter{}, vyperAuxdata{}, false
	}
	compiler, ok := decodeVyperCompilerMap(values[index+3])
	if !ok {
		return decodedFooter{}, vyperAuxdata{}, false
	}
	auxdata.Compiler = compiler
	if (compiler.atLeast(0, 4, 1) && auxdata.Arity != 5) ||
		(!compiler.atLeast(0, 4, 1) && auxdata.Arity != 4) ||
		!compiler.atLeast(0, 3, 10) || auxdata.RuntimeSize > maxMatcherBytecodeBytes ||
		auxdata.ImmutableSize > maxMatcherBytecodeBytes || len(auxdata.DataSizes) > 1024 {
		return decodedFooter{}, vyperAuxdata{}, false
	}
	var dataTotal uint64
	for _, size := range auxdata.DataSizes {
		if size == 0 || size > auxdata.RuntimeSize-dataTotal {
			return decodedFooter{}, vyperAuxdata{}, false
		}
		dataTotal += size
	}
	return footer, auxdata, true
}

func decodeVyperCompilerMap(raw cbor.RawMessage) (vyperVersion, bool) {
	var compiler map[string]cbor.RawMessage
	if matcherCBORMode.Unmarshal(raw, &compiler) != nil || len(compiler) != 1 {
		return vyperVersion{}, false
	}
	rawVersion, exists := compiler["vyper"]
	if !exists {
		return vyperVersion{}, false
	}
	var parts []uint64
	if matcherCBORMode.Unmarshal(rawVersion, &parts) != nil || len(parts) != 3 {
		return vyperVersion{}, false
	}
	return vyperVersion{Major: parts[0], Minor: parts[1], Patch: parts[2]}, true
}

func decodeVyperExclusiveVersionFooter(bytecode []byte) (decodedFooter, vyperVersion, bool) {
	footer, ok := decodeExclusiveMapFooter(bytecode)
	if !ok {
		return decodedFooter{}, vyperVersion{}, false
	}
	version, ok := decodeVyperCompilerMap(cbor.RawMessage(footer.Payload))
	return footer, version, ok
}

func decodeVyperFixedVersionFooter(bytecode []byte) (decodedFooter, vyperVersion, bool) {
	const legacyFooterBytes = 11
	if len(bytecode) < legacyFooterBytes {
		return decodedFooter{}, vyperVersion{}, false
	}
	start := len(bytecode) - legacyFooterBytes
	payload := bytecode[start:]
	version, ok := decodeVyperCompilerMap(cbor.RawMessage(payload))
	if !ok {
		return decodedFooter{}, vyperVersion{}, false
	}
	return decodedFooter{
		Start: start, Payload: append([]byte(nil), payload...), Raw: append([]byte(nil), payload...),
	}, version, true
}

func vyperBytecodeMetadataEnabled(input json.RawMessage, version vyperVersion) (bool, error) {
	document, err := decodeRawJSONObject(input)
	if err != nil {
		return false, errors.New("Vyper Standard JSON is malformed")
	}
	settings, err := decodeRawJSONObject(document["settings"])
	if err != nil {
		return false, errors.New("Vyper Standard JSON settings are malformed")
	}
	if !version.atLeast(0, 3, 5) {
		// Older vyper-json versions ignore bytecodeMetadata and always emit
		// their fixed compiler signature.
		return true, nil
	}
	raw, exists := settings["bytecodeMetadata"]
	if !exists {
		return true, nil
	}
	var enabled bool
	trimmed := bytes.TrimSpace(raw)
	if !bytes.Equal(trimmed, []byte("true")) && !bytes.Equal(trimmed, []byte("false")) ||
		json.Unmarshal(trimmed, &enabled) != nil {
		return false, errors.New("Vyper bytecodeMetadata setting is malformed")
	}
	return enabled, nil
}

func matchVyperArtifact(request Request, artifact Artifact) (MatchResult, error) {
	version, ok := parseVyperVersion(request.CompilerVersion)
	if !ok {
		return MatchResult{}, errors.New("Vyper compiler version is not semantic")
	}
	if artifact.vyperVersionPresent && artifact.vyperVersion != version {
		return MatchResult{}, errCompilerVersionMalformed
	}
	metadataEnabled, err := vyperBytecodeMetadataEnabled(request.StandardJSON, version)
	if err != nil {
		return MatchResult{}, err
	}
	compiledCreation, err := decodeBytecode(artifact.CreationBytecode)
	if err != nil || len(compiledCreation) == 0 {
		return MatchResult{}, errCompiledCodeMalformed
	}
	compiledRuntime, err := decodeBytecode(artifact.RuntimeBytecode)
	if err != nil || len(compiledRuntime) == 0 {
		return MatchResult{}, errCompiledCodeMalformed
	}
	onchainCreation, err := decodeBytecode(request.CreationBytecode)
	if err != nil {
		return MatchResult{}, errOnchainCodeMalformed
	}
	onchainRuntime, err := decodeBytecode(request.RuntimeBytecode)
	if err != nil {
		return MatchResult{}, errOnchainCodeMalformed
	}

	creation := MatchMismatch
	immutableSize := artifact.vyperImmutableSize
	if version.atLeast(0, 3, 10) {
		creation, immutableSize, err = matchModernVyperCreation(
			onchainCreation,
			compiledCreation,
			compiledRuntime,
			artifact.vyperImmutableSize,
			artifact.vyperLayoutPresent,
			version,
			metadataEnabled,
		)
		if err != nil {
			return MatchResult{}, err
		}
	} else if bytes.Equal(onchainCreation, compiledCreation) {
		creation = MatchExact
	}

	runtime := matchVyperRuntime(
		onchainRuntime,
		compiledRuntime,
		immutableSize,
		version,
		metadataEnabled,
	)
	return MatchResult{Creation: creation, Runtime: runtime}, nil
}

func matchModernVyperCreation(
	onchain []byte,
	compiled []byte,
	compiledRuntime []byte,
	layoutImmutableSize int,
	layoutPresent bool,
	version vyperVersion,
	metadataEnabled bool,
) (MatchKind, int, error) {
	compiledFooter, compiledAux, compiledHasAux := decodeVyperAuxdata(compiled)
	if metadataEnabled {
		if !compiledHasAux || !validVyperAuxdata(compiledAux, version, len(compiledRuntime)) ||
			compiledAux.ImmutableSize > uint64(maxMatcherBytecodeBytes) {
			return MatchMismatch, 0, errCompilerOutputMalformed
		}
		if layoutPresent && compiledAux.ImmutableSize != uint64(layoutImmutableSize) {
			return MatchMismatch, 0, errCompilerOutputMalformed
		}
		layoutImmutableSize = int(compiledAux.ImmutableSize)
	} else {
		if compiledHasAux {
			return MatchMismatch, 0, errCompilerOutputMalformed
		}
		if !layoutPresent {
			// Official Vyper <=0.4.0 Standard JSON does not return layout.
			// With metadata disabled there is no authenticated immutable-size
			// declaration, so only a zero-length suffix can match below.
			layoutImmutableSize = 0
		}
		if bytes.Equal(onchain, compiled) {
			return MatchExact, layoutImmutableSize, nil
		}
		return MatchMismatch, layoutImmutableSize, nil
	}
	if bytes.Equal(onchain, compiled) {
		return MatchExact, layoutImmutableSize, nil
	}
	onchainFooter, onchainAux, ok := decodeVyperAuxdata(onchain)
	if !ok || !validVyperAuxdata(onchainAux, version, len(compiledRuntime)) ||
		onchainAux.ImmutableSize != uint64(layoutImmutableSize) ||
		compiledAux.Arity != 5 || onchainAux.Arity != 5 ||
		compiledFooter.Start != onchainFooter.Start ||
		bytes.Equal(compiledAux.Integrity, onchainAux.Integrity) ||
		!equalVyperAuxdataExceptIntegrity(compiledAux, onchainAux) {
		return MatchMismatch, layoutImmutableSize, nil
	}
	if bytes.Equal(compiled[:compiledFooter.Start], onchain[:onchainFooter.Start]) {
		return MatchMetadataOnly, layoutImmutableSize, nil
	}
	return MatchMismatch, layoutImmutableSize, nil
}

func validVyperAuxdata(auxdata vyperAuxdata, version vyperVersion, runtimeSize int) bool {
	return auxdata.Compiler == version && auxdata.RuntimeSize == uint64(runtimeSize) &&
		((version.atLeast(0, 4, 1) && auxdata.Arity == 5) ||
			(!version.atLeast(0, 4, 1) && auxdata.Arity == 4))
}

func equalVyperAuxdataExceptIntegrity(left, right vyperAuxdata) bool {
	if left.Arity != right.Arity || left.RuntimeSize != right.RuntimeSize ||
		left.ImmutableSize != right.ImmutableSize || left.Compiler != right.Compiler ||
		len(left.DataSizes) != len(right.DataSizes) {
		return false
	}
	for index := range left.DataSizes {
		if left.DataSizes[index] != right.DataSizes[index] {
			return false
		}
	}
	return true
}

func matchVyperRuntime(
	onchain []byte,
	compiled []byte,
	immutableSize int,
	version vyperVersion,
	metadataEnabled bool,
) MatchKind {
	if immutableSize < 0 || len(onchain) != len(compiled)+immutableSize {
		return MatchMismatch
	}
	onchainTemplate := onchain[:len(compiled)]
	if bytes.Equal(onchainTemplate, compiled) {
		return MatchExact
	}
	if !metadataEnabled || version.atLeast(0, 3, 10) || !version.atLeast(0, 3, 5) {
		return MatchMismatch
	}
	onchainFooter, onchainVersion, onchainOK := decodeVyperExclusiveVersionFooter(onchainTemplate)
	compiledFooter, compiledVersion, compiledOK := decodeVyperExclusiveVersionFooter(compiled)
	if !onchainOK || !compiledOK || onchainVersion != version || compiledVersion != version ||
		onchainFooter.Start != compiledFooter.Start || bytes.Equal(onchainFooter.Raw, compiledFooter.Raw) {
		return MatchMismatch
	}
	if bytes.Equal(onchainTemplate[:onchainFooter.Start], compiled[:compiledFooter.Start]) {
		return MatchMetadataOnly
	}
	return MatchMismatch
}
