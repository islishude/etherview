package enrich

import (
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ABIRegistry indexes ABI entries by an exact chain/address/code/block
// identity. It is safe to decode while new bindings are being added, and it
// cannot return candidates registered for a different range or fork.
type ABIRegistry struct {
	mu       sync.RWMutex
	limits   DecodeLimits
	bindings map[ABIIdentity]*abiCandidateSet
}

type abiCandidateSet struct {
	functions map[[4]byte][]abiEntry
	errors    map[[4]byte][]abiEntry
	events    map[Word][]abiEntry
}

func NewABIRegistry() *ABIRegistry {
	return &ABIRegistry{
		limits:   DefaultDecodeLimits(),
		bindings: make(map[ABIIdentity]*abiCandidateSet),
	}
}

func NewABIRegistryWithLimits(limits DecodeLimits) (*ABIRegistry, error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	registry := NewABIRegistry()
	registry.limits = limits
	return registry, nil
}

// RegisterJSON adds only functions, non-anonymous events, and custom errors for
// an exact durable binding.
// Constructors, fallback functions, and receive functions have no selector and
// are intentionally ignored.
func (registry *ABIRegistry) RegisterJSON(binding ABIBinding, data []byte) error {
	if registry == nil {
		return errors.New("register ABI on nil registry")
	}
	if err := binding.validate(); err != nil {
		return err
	}
	entries, err := parseABIEntries(data, binding.Source, registry.limits)
	if err != nil {
		return err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	candidates := registry.bindings[binding.Identity]
	if candidates == nil {
		candidates = &abiCandidateSet{
			functions: make(map[[4]byte][]abiEntry),
			errors:    make(map[[4]byte][]abiEntry),
			events:    make(map[Word][]abiEntry),
		}
		registry.bindings[binding.Identity] = candidates
	}
	for _, entry := range entries {
		switch entry.kind {
		case ABIKindFunction:
			candidates.functions[entry.selector] = appendUniqueABIEntry(candidates.functions[entry.selector], entry)
		case ABIKindError:
			candidates.errors[entry.selector] = appendUniqueABIEntry(candidates.errors[entry.selector], entry)
		case ABIKindEvent:
			candidates.events[entry.topic] = appendUniqueABIEntry(candidates.events[entry.topic], entry)
		}
	}
	return nil
}

func appendUniqueABIEntry(entries []abiEntry, candidate abiEntry) []abiEntry {
	for index, existing := range entries {
		if existing.kind == candidate.kind && existing.signature == candidate.signature && existing.source == candidate.source {
			entries[index] = candidate
			return entries
		}
	}
	return append(entries, candidate)
}

func (registry *ABIRegistry) DecodeCalldata(identity ABIIdentity, input []byte) DecodeResult {
	if registry == nil {
		return DecodeResult{Status: DecodeUnknown, Kind: ABIKindFunction, Warning: "no ABI registry"}
	}
	if err := identity.validate(); err != nil {
		return DecodeResult{Status: DecodeUnknown, Kind: ABIKindFunction, Warning: err.Error()}
	}
	if len(input) < 4 {
		status := DecodeMalformed
		if len(input) == 0 {
			status = DecodeUnknown
		}
		return DecodeResult{Status: status, Kind: ABIKindFunction, Warning: "calldata has no complete selector"}
	}
	var selector [4]byte
	copy(selector[:], input[:4])
	entries := registry.callableEntries(identity, ABIKindFunction, selector)
	return registry.decodeCallables(ABIKindFunction, selector, entries, input[4:])
}

func (registry *ABIRegistry) DecodeRevert(identity ABIIdentity, data []byte) DecodeResult {
	if registry == nil {
		return DecodeResult{Status: DecodeUnknown, Kind: ABIKindError, Warning: "no ABI registry"}
	}
	if err := identity.validate(); err != nil {
		return DecodeResult{Status: DecodeUnknown, Kind: ABIKindError, Warning: err.Error()}
	}
	if len(data) == 0 {
		return DecodeResult{Status: DecodeUnknown, Kind: ABIKindError, Warning: "empty revert data"}
	}
	if len(data) < 4 {
		return DecodeResult{Status: DecodeMalformed, Kind: ABIKindError, Warning: "revert data has no complete selector"}
	}
	var selector [4]byte
	copy(selector[:], data[:4])
	if selector == SignatureSelector("Error(string)") {
		entry := builtinEntry(ABIKindError, "Error", []abiParameter{{Name: "message", Type: "string"}}, registry.limits)
		return registry.decodeCallables(ABIKindError, selector, []abiEntry{entry}, data[4:])
	}
	if selector == SignatureSelector("Panic(uint256)") {
		entry := builtinEntry(ABIKindError, "Panic", []abiParameter{{Name: "code", Type: "uint256"}}, registry.limits)
		return registry.decodeCallables(ABIKindError, selector, []abiEntry{entry}, data[4:])
	}
	entries := registry.callableEntries(identity, ABIKindError, selector)
	return registry.decodeCallables(ABIKindError, selector, entries, data[4:])
}

func builtinEntry(kind ABIKind, name string, inputs []abiParameter, limits DecodeLimits) abiEntry {
	entry := abiEntry{kind: kind, name: name, inputs: inputs, source: ABISourceBuiltin}
	canonical := make([]string, len(inputs))
	entry.types = make([]*abiType, len(inputs))
	for index, input := range inputs {
		canonical[index], _ = canonicalParameter(input)
		entry.types[index], _ = parseABIType(input, 1, limits.MaxDepth)
	}
	entry.signature = name + "(" + strings.Join(canonical, ",") + ")"
	entry.selector = SignatureSelector(entry.signature)
	return entry
}

func isBuiltinErrorSignature(signature string) bool {
	return signature == "Error(string)" || signature == "Panic(uint256)"
}

func isBuiltinErrorSelector(selector [4]byte) bool {
	return selector == SignatureSelector("Error(string)") || selector == SignatureSelector("Panic(uint256)")
}

func (registry *ABIRegistry) callableEntries(identity ABIIdentity, kind ABIKind, selector [4]byte) []abiEntry {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	candidates := registry.bindings[identity]
	if candidates == nil {
		return nil
	}
	var source []abiEntry
	if kind == ABIKindFunction {
		source = candidates.functions[selector]
	} else {
		source = candidates.errors[selector]
	}
	return append([]abiEntry(nil), source...)
}

func (registry *ABIRegistry) decodeCallables(kind ABIKind, selector [4]byte, entries []abiEntry, payload []byte) DecodeResult {
	identifier := "0x" + hex.EncodeToString(selector[:])
	if len(entries) == 0 {
		return DecodeResult{Status: DecodeUnknown, Kind: kind, Warning: "unknown ABI identifier " + identifier}
	}
	var decoded []decodedABICandidate
	failures := make([]string, 0, len(entries))
	budget := newABIDecodeBudget(registry.limits)
	for _, entry := range entries {
		values, err := decodeABIValuesWithBudget(entry.types, payload, registry.limits, budget)
		if err != nil {
			failures = append(failures, entry.signature+": "+err.Error())
			if errors.Is(err, ErrABIDecodeLimit) {
				return DecodeResult{
					Status: DecodeMalformed, Kind: kind,
					Candidates: uniqueSignatures(entries), Warning: strings.Join(failures, "; "),
				}
			}
			continue
		}
		arguments := make([]DecodedArgument, len(values))
		for index, value := range values {
			arguments[index] = DecodedArgument{
				Name:  entry.inputs[index].Name,
				Type:  entry.inputs[index].Type,
				Value: value,
			}
		}
		decoded = append(decoded, decodedABICandidate{entry: entry, arguments: arguments})
	}
	if len(decoded) == 0 {
		return DecodeResult{
			Status:     DecodeMalformed,
			Kind:       kind,
			Candidates: uniqueSignatures(entries),
			Warning:    strings.Join(failures, "; "),
		}
	}
	return chooseDecodedABICandidate(kind, decoded)
}

type decodedABICandidate struct {
	entry     abiEntry
	arguments []DecodedArgument
}

func chooseDecodedABICandidate(kind ABIKind, decoded []decodedABICandidate) DecodeResult {
	sort.SliceStable(decoded, func(left, right int) bool {
		return confidenceRank(decoded[left].entry.confidence()) > confidenceRank(decoded[right].entry.confidence())
	})
	bestRank := confidenceRank(decoded[0].entry.confidence())
	bestSignatures := make(map[string]struct{})
	for _, item := range decoded {
		if confidenceRank(item.entry.confidence()) != bestRank {
			break
		}
		bestSignatures[item.entry.signature] = struct{}{}
	}
	status := DecodeDecoded
	warning := ""
	if len(bestSignatures) > 1 {
		status = DecodeAmbiguous
		warning = "multiple ABI candidates with equal confidence decoded successfully"
	}
	selected := decoded[0]
	return DecodeResult{
		Status:     status,
		Kind:       kind,
		Name:       selected.entry.name,
		Signature:  selected.entry.signature,
		Source:     selected.entry.source,
		Confidence: selected.entry.confidence(),
		Arguments:  selected.arguments,
		Candidates: uniqueDecodedSignatures(decoded),
		Warning:    warning,
	}
}

func uniqueSignatures(entries []abiEntry) []string {
	seen := make(map[string]struct{}, len(entries))
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		if _, exists := seen[entry.signature]; exists {
			continue
		}
		seen[entry.signature] = struct{}{}
		result = append(result, entry.signature)
	}
	sort.Strings(result)
	return result
}

func uniqueDecodedSignatures(entries []decodedABICandidate) []string {
	seen := make(map[string]struct{}, len(entries))
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		if _, exists := seen[entry.entry.signature]; exists {
			continue
		}
		seen[entry.entry.signature] = struct{}{}
		result = append(result, entry.entry.signature)
	}
	sort.Strings(result)
	return result
}

func (registry *ABIRegistry) DecodeLog(identity ABIIdentity, topics []Word, data []byte) DecodeResult {
	if len(topics) == 0 {
		return DecodeResult{Status: DecodeUnknown, Kind: ABIKindEvent, Warning: "log has no signature topic"}
	}
	if registry == nil {
		return DecodeResult{Status: DecodeUnknown, Kind: ABIKindEvent, Warning: "no ABI registry"}
	}
	if err := identity.validate(); err != nil {
		return DecodeResult{Status: DecodeUnknown, Kind: ABIKindEvent, Warning: err.Error()}
	}
	registry.mu.RLock()
	candidates := registry.bindings[identity]
	var entries []abiEntry
	if candidates != nil {
		entries = append(entries, candidates.events[topics[0]]...)
	}
	registry.mu.RUnlock()
	if len(entries) == 0 {
		return DecodeResult{Status: DecodeUnknown, Kind: ABIKindEvent, Warning: "unknown event topic " + topics[0].String()}
	}
	var decoded []decodedABICandidate
	var failures []string
	budget := newABIDecodeBudget(registry.limits)
	for _, entry := range entries {
		arguments, err := registry.decodeEvent(entry, topics, data, budget)
		if err != nil {
			failures = append(failures, entry.signature+": "+err.Error())
			if errors.Is(err, ErrABIDecodeLimit) {
				return DecodeResult{
					Status: DecodeMalformed, Kind: ABIKindEvent,
					Candidates: uniqueSignatures(entries), Warning: strings.Join(failures, "; "),
				}
			}
			continue
		}
		decoded = append(decoded, decodedABICandidate{entry: entry, arguments: arguments})
	}
	if len(decoded) == 0 {
		return DecodeResult{
			Status:     DecodeMalformed,
			Kind:       ABIKindEvent,
			Candidates: uniqueSignatures(entries),
			Warning:    strings.Join(failures, "; "),
		}
	}
	return chooseDecodedABICandidate(ABIKindEvent, decoded)
}

func (registry *ABIRegistry) decodeEvent(entry abiEntry, topics []Word, data []byte, budget *abiDecodeBudget) ([]DecodedArgument, error) {
	indexedCount := 0
	var nonIndexedTypes []*abiType
	for index, indexed := range entry.indexed {
		if indexed {
			indexedCount++
		} else {
			nonIndexedTypes = append(nonIndexedTypes, entry.types[index])
		}
	}
	if len(topics) != indexedCount+1 {
		return nil, fmt.Errorf("got %d indexed topics, want %d", len(topics)-1, indexedCount)
	}
	nonIndexedValues, err := decodeABIValuesWithBudget(nonIndexedTypes, data, registry.limits, budget)
	if err != nil {
		return nil, fmt.Errorf("decode event data: %w", err)
	}
	arguments := make([]DecodedArgument, len(entry.inputs))
	topicIndex, dataIndex := 1, 0
	for index, input := range entry.inputs {
		argument := DecodedArgument{Name: input.Name, Type: input.Type, Indexed: input.Indexed}
		if !input.Indexed {
			argument.Value = nonIndexedValues[dataIndex]
			dataIndex++
			arguments[index] = argument
			continue
		}
		if indexedValueIsHashed(entry.types[index]) {
			argument.Hashed = true
			argument.Value = topics[topicIndex].String()
		} else {
			decoder := abiDecoder{data: topics[topicIndex][:], limits: registry.limits, budget: budget}
			value, err := decoder.decodeStatic(entry.types[index], 0, 1)
			if err != nil {
				return nil, fmt.Errorf("decode indexed input %d: %w", index, err)
			}
			argument.Value = value
		}
		topicIndex++
		arguments[index] = argument
	}
	return arguments, nil
}

func indexedValueIsHashed(valueType *abiType) bool {
	switch valueType.kind {
	case abiDynamicBytes, abiString, abiArray, abiTuple:
		return true
	default:
		return false
	}
}
