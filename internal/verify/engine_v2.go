package verify

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

type VerificationMatchType string

const (
	VerificationMatchFull    VerificationMatchType = "full"
	VerificationMatchPartial VerificationMatchType = "partial"
)

type Transformation struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
	Offset uint64 `json:"offset"`
	ID     string `json:"id,omitempty"`
}

type TransformationValues struct {
	CBORAuxdata          map[string]string `json:"cborAuxdata,omitempty"`
	ConstructorArguments string            `json:"constructorArguments,omitempty"`
	Libraries            map[string]string `json:"libraries,omitempty"`
	Immutables           map[string]string `json:"immutables,omitempty"`
}

type VerificationMatchDetails struct {
	MatchType       VerificationMatchType `json:"match_type"`
	Transformations []Transformation      `json:"transformations"`
	Values          TransformationValues  `json:"values"`
}

type AuxdataValue struct {
	Offset uint64 `json:"offset"`
	Value  string `json:"value"`
}

// LocateAuxdata compares two equal-shape compiler outputs and authenticates
// every changed metadata range using the bounded CBOR decoder.
func LocateAuxdata(original, modified []byte, language Language) (map[string]AuxdataValue, error) {
	if len(original) != len(modified) {
		return nil, errors.New("dual compilation changed bytecode length")
	}
	if len(original) > maxMatcherBytecodeBytes {
		return nil, errors.New("compiled bytecode exceeds matcher limit")
	}
	different := make([]bool, len(original))
	hasDifference := false
	for index := range original {
		different[index] = original[index] != modified[index]
		hasDifference = hasDifference || different[index]
	}
	if !hasDifference {
		return map[string]AuxdataValue{}, nil
	}
	type located struct {
		start int
		end   int
	}
	var candidates []located
	for end := 3; end <= len(original); end++ {
		firstStart, firstOK := auxdataRangeEndingAt(original, end, language)
		secondStart, secondOK := auxdataRangeEndingAt(modified, end, language)
		if !firstOK || !secondOK || firstStart != secondStart {
			continue
		}
		containsDifference := false
		for index := firstStart; index < end; index++ {
			if different[index] {
				containsDifference = true
				break
			}
		}
		if containsDifference {
			candidates = append(candidates, located{start: firstStart, end: end})
		}
	}
	// Prefer the widest authenticated range when a valid CBOR value happens to
	// be nested inside another candidate. Independent ranges remain intact.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].start == candidates[j].start {
			return candidates[i].end > candidates[j].end
		}
		return candidates[i].start < candidates[j].start
	})
	selected := make([]located, 0, len(candidates))
	for _, candidate := range candidates {
		if len(selected) > 0 && candidate.start < selected[len(selected)-1].end {
			if candidate.end <= selected[len(selected)-1].end {
				continue
			}
			return nil, errors.New("dual compilation produced overlapping auxdata")
		}
		selected = append(selected, candidate)
	}
	for index, changed := range different {
		if !changed {
			continue
		}
		covered := false
		for _, candidate := range selected {
			if index >= candidate.start && index < candidate.end {
				covered = true
				break
			}
		}
		if !covered {
			return nil, errors.New("dual compilation changed non-auxdata bytecode")
		}
	}
	result := make(map[string]AuxdataValue, len(selected))
	for index, candidate := range selected {
		result[fmt.Sprintf("%d", index+1)] = AuxdataValue{
			Offset: uint64(candidate.start),
			Value:  "0x" + hex.EncodeToString(original[candidate.start:candidate.end]),
		}
	}
	return result, nil
}

func auxdataRangeEndingAt(code []byte, end int, language Language) (int, bool) {
	if end < 3 {
		return 0, false
	}
	encodedLength := int(binary.BigEndian.Uint16(code[end-2 : end]))
	var start int
	switch language {
	case LanguageSolidity, LanguageYul:
		if encodedLength == 0 || encodedLength > maxCompilerFooterBytes || encodedLength+2 > end {
			return 0, false
		}
		start = end - encodedLength - 2
		var value map[string]json.RawMessage
		if !validCompleteCBOR(code[start:end-2]) ||
			matcherCBORMode.Unmarshal(code[start:end-2], &value) != nil || len(value) == 0 {
			return 0, false
		}
	default:
		return 0, false
	}
	return start, true
}

type codeTransformationInput struct {
	Auxdata             map[string]AuxdataValue
	LinkReferences      map[string]map[string][]bytecodeRange
	ImmutableReferences map[string][]bytecodeRange
	ABI                 json.RawMessage
	Creation            bool
}

func matchTransformedCode(
	deployed []byte,
	compiled []byte,
	input codeTransformationInput,
) (*VerificationMatchDetails, error) {
	if len(deployed) < len(compiled) {
		return nil, nil
	}
	if !input.Creation && len(deployed) != len(compiled) {
		return nil, nil
	}
	ranges, err := collectTransformationRanges(input)
	if err != nil {
		return nil, err
	}
	if err := validateRanges(ranges, len(compiled)); err != nil {
		return nil, err
	}
	transformed := append([]byte(nil), compiled...)
	values := TransformationValues{
		CBORAuxdata: make(map[string]string),
		Libraries:   make(map[string]string),
		Immutables:  make(map[string]string),
	}
	var transformations []Transformation
	auxdataChanged := false
	auxIDs := sortedAuxdataIDs(input.Auxdata)
	for _, id := range auxIDs {
		auxdata := input.Auxdata[id]
		expected, decodeErr := decodeBytecode(auxdata.Value)
		if decodeErr != nil || uint64(len(expected)) > uint64(len(compiled))-auxdata.Offset {
			return nil, errCompilerOutputMalformed
		}
		start, end := int(auxdata.Offset), int(auxdata.Offset)+len(expected)
		actual := deployed[start:end]
		if !bytes.Equal(actual, expected) {
			auxdataChanged = true
			copy(transformed[start:end], actual)
			transformations = append(transformations, Transformation{
				Type: "replace", Reason: "cborAuxdata", Offset: auxdata.Offset, ID: id,
			})
			values.CBORAuxdata[id] = "0x" + hex.EncodeToString(actual)
		}
	}
	for _, source := range sortedKeys(input.LinkReferences) {
		for _, library := range sortedKeys(input.LinkReferences[source]) {
			id := source + ":" + library
			actual, applyErr := applyConsistentRanges(
				transformed, deployed, input.LinkReferences[source][library],
				"library", id, &transformations,
			)
			if applyErr != nil {
				return nil, applyErr
			}
			if actual != nil {
				values.Libraries[id] = "0x" + hex.EncodeToString(actual)
			}
		}
	}
	for _, id := range sortedKeys(input.ImmutableReferences) {
		actual, applyErr := applyConsistentRanges(
			transformed, deployed, input.ImmutableReferences[id],
			"immutable", id, &transformations,
		)
		if applyErr != nil {
			return nil, applyErr
		}
		if actual != nil {
			values.Immutables[id] = "0x" + hex.EncodeToString(actual)
		}
	}
	if input.Creation {
		arguments := deployed[len(compiled):]
		if len(arguments) > 0 {
			if err := validateConstructorArguments(input.ABI, arguments); err != nil {
				return nil, nil
			}
			transformed = append(transformed, arguments...)
			transformations = append(transformations, Transformation{
				Type: "insert", Reason: "constructorArguments", Offset: uint64(len(compiled)),
			})
			values.ConstructorArguments = "0x" + hex.EncodeToString(arguments)
		} else if err := validateConstructorArguments(input.ABI, nil); err != nil {
			return nil, nil
		}
	}
	if !bytes.Equal(deployed, transformed) {
		return nil, nil
	}
	sort.Slice(transformations, func(i, j int) bool {
		if transformations[i].Offset != transformations[j].Offset {
			return transformations[i].Offset < transformations[j].Offset
		}
		if transformations[i].Reason != transformations[j].Reason {
			return transformations[i].Reason < transformations[j].Reason
		}
		return transformations[i].ID < transformations[j].ID
	})
	matchType := VerificationMatchPartial
	if len(input.Auxdata) > 0 && !auxdataChanged {
		matchType = VerificationMatchFull
	}
	if len(values.CBORAuxdata) == 0 {
		values.CBORAuxdata = nil
	}
	if len(values.Libraries) == 0 {
		values.Libraries = nil
	}
	if len(values.Immutables) == 0 {
		values.Immutables = nil
	}
	return &VerificationMatchDetails{
		MatchType: matchType, Transformations: transformations, Values: values,
	}, nil
}

func collectTransformationRanges(input codeTransformationInput) ([]bytecodeRange, error) {
	ranges := make([]bytecodeRange, 0)
	for id, auxdata := range input.Auxdata {
		value, err := decodeBytecode(auxdata.Value)
		if id == "" || err != nil || len(value) == 0 {
			return nil, errCompilerOutputMalformed
		}
		ranges = append(ranges, bytecodeRange{Start: auxdata.Offset, Length: uint64(len(value))})
	}
	for source, libraries := range input.LinkReferences {
		if source == "" || len(libraries) == 0 {
			return nil, errCompilerOutputMalformed
		}
		for library, references := range libraries {
			if library == "" || len(references) == 0 {
				return nil, errCompilerOutputMalformed
			}
			for _, reference := range references {
				if reference.Length != 20 {
					return nil, errCompilerOutputMalformed
				}
				ranges = append(ranges, reference)
			}
		}
	}
	for id, references := range input.ImmutableReferences {
		if id == "" || len(references) == 0 {
			return nil, errCompilerOutputMalformed
		}
		length := references[0].Length
		for _, reference := range references {
			if reference.Length != length {
				return nil, errCompilerOutputMalformed
			}
			ranges = append(ranges, reference)
		}
	}
	return ranges, nil
}

func applyConsistentRanges(
	transformed []byte,
	deployed []byte,
	ranges []bytecodeRange,
	reason string,
	id string,
	transformations *[]Transformation,
) ([]byte, error) {
	var value []byte
	for _, span := range ranges {
		start, end := int(span.Start), int(span.Start+span.Length)
		if start < 0 || end < start || end > len(transformed) || end > len(deployed) {
			return nil, errCompilerOutputMalformed
		}
		actual := deployed[start:end]
		if value == nil {
			value = append([]byte(nil), actual...)
		} else if !bytes.Equal(value, actual) {
			return nil, errors.New("repeated transformation values are inconsistent")
		}
		copy(transformed[start:end], actual)
		*transformations = append(*transformations, Transformation{
			Type: "replace", Reason: reason, Offset: span.Start, ID: id,
		})
	}
	return value, nil
}

func validateConstructorArguments(rawABI json.RawMessage, arguments []byte) error {
	if !jsonArray(rawABI) {
		if len(arguments) == 0 {
			return nil
		}
		return errors.New("constructor arguments exist without an ABI")
	}
	parsed, err := abi.JSON(strings.NewReader(string(rawABI)))
	if err != nil {
		return errors.New("compiled contract ABI is invalid")
	}
	if parsed.Constructor.Inputs == nil {
		if len(arguments) == 0 {
			return nil
		}
		return errors.New("constructor arguments exist without a constructor")
	}
	values, err := parsed.Constructor.Inputs.Unpack(arguments)
	if err != nil {
		return errors.New("constructor arguments do not decode")
	}
	encoded, err := parsed.Constructor.Inputs.Pack(values...)
	if err != nil || !bytes.Equal(encoded, arguments) {
		return errors.New("constructor arguments are not canonically encoded")
	}
	return nil
}

func sortedAuxdataIDs(values map[string]AuxdataValue) []string {
	return sortedKeys(values)
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

type Blueprint struct {
	Version  uint8
	Data     []byte
	Initcode []byte
}

func ParseBlueprint(bytecode []byte) (Blueprint, bool, error) {
	if len(bytecode) < 2 || bytecode[0] != 0xfe || bytecode[1] != 0x71 {
		return Blueprint{}, false, nil
	}
	if len(bytecode) < 3 {
		return Blueprint{}, true, errors.New("ERC-5202 preamble is truncated")
	}
	versionByte := bytecode[2]
	lengthBytes := int(versionByte & 0x03)
	if lengthBytes == 3 {
		return Blueprint{}, true, errors.New("ERC-5202 continuation version is unsupported")
	}
	if len(bytecode) < 3+lengthBytes {
		return Blueprint{}, true, errors.New("ERC-5202 data length is truncated")
	}
	dataLength := 0
	for _, value := range bytecode[3 : 3+lengthBytes] {
		dataLength = dataLength<<8 | int(value)
	}
	dataStart := 3 + lengthBytes
	if dataLength > len(bytecode)-dataStart {
		return Blueprint{}, true, errors.New("ERC-5202 data section is truncated")
	}
	initcode := bytecode[dataStart+dataLength:]
	if len(initcode) == 0 {
		return Blueprint{}, true, errors.New("ERC-5202 initcode is empty")
	}
	return Blueprint{
		Version:  versionByte >> 2,
		Data:     append([]byte(nil), bytecode[dataStart:dataStart+dataLength]...),
		Initcode: append([]byte(nil), initcode...),
	}, true, nil
}

type RankedCandidate struct {
	FullyQualifiedName string
	Creation           *VerificationMatchDetails
	Runtime            *VerificationMatchDetails
	Hint               bool
}

func SortCandidates(candidates []RankedCandidate, requireRuntime bool) []RankedCandidate {
	filtered := candidates[:0]
	for _, candidate := range candidates {
		if requireRuntime && candidate.Runtime == nil {
			continue
		}
		if candidate.Creation == nil && candidate.Runtime == nil {
			continue
		}
		filtered = append(filtered, candidate)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		left, right := candidateRank(filtered[i]), candidateRank(filtered[j])
		if left != right {
			return left > right
		}
		if filtered[i].Hint != filtered[j].Hint {
			return filtered[i].Hint
		}
		return filtered[i].FullyQualifiedName < filtered[j].FullyQualifiedName
	})
	return filtered
}

func candidateRank(candidate RankedCandidate) int {
	creationFull := candidate.Creation != nil && candidate.Creation.MatchType == VerificationMatchFull
	runtimeFull := candidate.Runtime != nil && candidate.Runtime.MatchType == VerificationMatchFull
	switch {
	case creationFull && runtimeFull:
		return 4
	case creationFull:
		return 3
	case runtimeFull:
		return 2
	default:
		return 1
	}
}
