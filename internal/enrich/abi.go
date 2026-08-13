package enrich

import (
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/common"
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
	constructors    []abiEntry
	functions       map[[4]byte][]abiEntry
	receive         []abiEntry
	fallback        []abiEntry
	errors          map[[4]byte][]abiEntry
	events          map[common.Hash][]abiEntry
	anonymousEvents []abiEntry
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

// RegisterJSON adds callable entries, constructors, events, and custom errors
// for an exact durable binding. Selectorless receive and fallback entries are
// kept separately so they can never collide with a four-byte selector.
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
			events:    make(map[common.Hash][]abiEntry),
		}
		registry.bindings[binding.Identity] = candidates
	}
	for _, entry := range entries {
		entry.sourceAddress = binding.SourceAddress
		entry.sourceCodeHash = binding.SourceCodeHash
		switch entry.kind {
		case ABIKindConstructor:
			candidates.constructors = appendUniqueABIEntry(candidates.constructors, entry)
		case ABIKindFunction:
			switch entry.selectorless {
			case "receive":
				candidates.receive = appendUniqueABIEntry(candidates.receive, entry)
			case "fallback":
				candidates.fallback = appendUniqueABIEntry(candidates.fallback, entry)
			default:
				candidates.functions[entry.selector] = appendUniqueABIEntry(candidates.functions[entry.selector], entry)
			}
		case ABIKindError:
			candidates.errors[entry.selector] = appendUniqueABIEntry(candidates.errors[entry.selector], entry)
		case ABIKindEvent:
			if entry.anonymous {
				candidates.anonymousEvents = appendUniqueABIEntry(candidates.anonymousEvents, entry)
			} else {
				candidates.events[entry.topic] = appendUniqueABIEntry(candidates.events[entry.topic], entry)
			}
		}
	}
	return nil
}

func (registry *ABIRegistry) DecodeConstructor(identity ABIIdentity, arguments []byte) DecodeResult {
	if registry == nil {
		return DecodeResult{Status: DecodeUnknown, Kind: ABIKindConstructor, Warning: "no ABI registry"}
	}
	if err := identity.validate(); err != nil {
		return DecodeResult{Status: DecodeUnknown, Kind: ABIKindConstructor, Warning: err.Error()}
	}
	registry.mu.RLock()
	candidates := registry.bindings[identity]
	var entries []abiEntry
	if candidates != nil {
		entries = append(entries, candidates.constructors...)
	}
	registry.mu.RUnlock()
	if len(entries) == 0 {
		return DecodeResult{Status: DecodeUnknown, Kind: ABIKindConstructor, Warning: "verified ABI has no constructor"}
	}
	return registry.decodeConstructors(entries, arguments)
}

func (registry *ABIRegistry) decodeConstructors(entries []abiEntry, payload []byte) DecodeResult {
	var decoded []decodedABICandidate
	var failures []string
	budget := newABIDecodeBudget(registry.limits)
	for _, entry := range entries {
		values, err := decodeABIValuesWithBudget(entry.types, payload, registry.limits, budget)
		if err != nil {
			failures = append(failures, entry.signature+": "+err.Error())
			if errors.Is(err, ErrABIDecodeLimit) {
				return DecodeResult{Status: DecodeMalformed, Kind: ABIKindConstructor, Candidates: uniqueSignatures(entries), Warning: strings.Join(failures, "; ")}
			}
			continue
		}
		arguments := make([]DecodedArgument, len(values))
		for index, value := range values {
			arguments[index] = DecodedArgument{Name: entry.inputs[index].Name, Type: entry.inputs[index].Type, Value: value}
		}
		decoded = append(decoded, decodedABICandidate{entry: entry, arguments: arguments})
	}
	if len(decoded) == 0 {
		return DecodeResult{Status: DecodeMalformed, Kind: ABIKindConstructor, Candidates: uniqueSignatures(entries), Warning: strings.Join(failures, "; ")}
	}
	result, _ := chooseDecodedABICandidate(ABIKindConstructor, decoded)
	return result
}

func appendUniqueABIEntry(entries []abiEntry, candidate abiEntry) []abiEntry {
	for index, existing := range entries {
		if existing.kind == candidate.kind && existing.signature == candidate.signature && existing.source == candidate.source &&
			existing.sourceAddress == candidate.sourceAddress && existing.sourceCodeHash == candidate.sourceCodeHash &&
			existing.anonymous == candidate.anonymous && existing.selectorless == candidate.selectorless {
			entries[index] = candidate
			return entries
		}
	}
	return append(entries, candidate)
}

func (registry *ABIRegistry) DecodeCalldata(identity ABIIdentity, input []byte) DecodeResult {
	result, _ := registry.decodeCalldataWithEntry(identity, input)
	return result
}

func (registry *ABIRegistry) decodeCalldataWithEntry(identity ABIIdentity, input []byte) (DecodeResult, *abiEntry) {
	if registry == nil {
		return DecodeResult{Status: DecodeUnknown, Kind: ABIKindFunction, Warning: "no ABI registry"}, nil
	}
	if err := identity.validate(); err != nil {
		return DecodeResult{Status: DecodeUnknown, Kind: ABIKindFunction, Warning: err.Error()}, nil
	}
	if len(input) == 0 {
		entries := registry.selectorlessEntries(identity, true)
		if len(entries) == 0 {
			return DecodeResult{Status: DecodeUnknown, Kind: ABIKindFunction, Warning: "empty calldata has no receive or fallback ABI entry"}, nil
		}
		return chooseSelectorlessABICandidate(entries)
	}
	if len(input) < 4 {
		entries := registry.selectorlessEntries(identity, false)
		if len(entries) != 0 {
			return chooseSelectorlessABICandidate(entries)
		}
		return DecodeResult{Status: DecodeMalformed, Kind: ABIKindFunction, Warning: "calldata has no complete selector and ABI declares no fallback entry"}, nil
	}
	var selector [4]byte
	copy(selector[:], input[:4])
	entries := registry.callableEntries(identity, ABIKindFunction, selector)
	result, selected := registry.decodeCallablesWithEntry(ABIKindFunction, selector, entries, input[4:])
	if result.Status != DecodeUnknown {
		return result, selected
	}
	fallback := registry.selectorlessEntries(identity, false)
	if len(fallback) != 0 {
		return chooseSelectorlessABICandidate(fallback)
	}
	return result, nil
}

func (registry *ABIRegistry) DecodeCall(identity ABIIdentity, input, output []byte, directReverted bool) CallDecodeResult {
	if directReverted {
		return CallDecodeResult{Input: registry.DecodeCalldata(identity, input), ReturnStatus: ReturnNotApplicable}
	}
	decoded, selected := registry.decodeCalldataWithEntry(identity, input)
	result := CallDecodeResult{Input: decoded, ReturnStatus: ReturnUnknown}
	if decoded.Status != DecodeDecoded || selected == nil {
		return result
	}
	if !selected.outputsKnown {
		result.ReturnStatus = ReturnUnavailable
		result.Warning = "selected ABI function does not declare outputs"
		return result
	}
	if len(selected.outputs) == 0 {
		if len(output) == 0 {
			result.ReturnStatus = ReturnEmpty
			result.Returns = []DecodedArgument{}
			return result
		}
		result.ReturnStatus = ReturnMalformed
		result.Warning = "function declares no outputs but returned data"
		return result
	}
	values, err := decodeABIValuesWithBudget(selected.outputTypes, output, registry.limits, newABIDecodeBudget(registry.limits))
	if err != nil {
		result.ReturnStatus = ReturnMalformed
		result.Warning = "decode function output: " + err.Error()
		return result
	}
	result.ReturnStatus = ReturnDecoded
	result.Returns = make([]DecodedArgument, len(values))
	for index, value := range values {
		result.Returns[index] = DecodedArgument{
			Name: selected.outputs[index].Name, Type: selected.outputs[index].Type, Value: value,
		}
	}
	return result
}

func (registry *ABIRegistry) selectorlessEntries(identity ABIIdentity, preferReceive bool) []abiEntry {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	candidates := registry.bindings[identity]
	if candidates == nil {
		return nil
	}
	if preferReceive && len(candidates.receive) != 0 {
		return append([]abiEntry(nil), candidates.receive...)
	}
	return append([]abiEntry(nil), candidates.fallback...)
}

func chooseSelectorlessABICandidate(entries []abiEntry) (DecodeResult, *abiEntry) {
	decoded := make([]decodedABICandidate, len(entries))
	for index := range entries {
		decoded[index] = decodedABICandidate{entry: entries[index], arguments: []DecodedArgument{}}
	}
	return chooseDecodedABICandidate(ABIKindFunction, decoded)
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
	if builtin := registry.decodeBuiltinRevert(selector, data[4:]); builtin != nil {
		return *builtin
	}
	entries := registry.callableEntries(identity, ABIKindError, selector)
	result, _ := registry.decodeCallablesWithEntry(ABIKindError, selector, entries, data[4:])
	return result
}

// DecodeBuiltinRevert decodes Solidity's protocol-stable Error and Panic
// payloads without requiring a contract identity. Custom errors deliberately
// remain bound to an exact ABI candidate.
func (registry *ABIRegistry) DecodeBuiltinRevert(data []byte) DecodeResult {
	if registry == nil {
		return DecodeResult{Status: DecodeUnknown, Kind: ABIKindError, Warning: "no ABI registry"}
	}
	if len(data) == 0 {
		return DecodeResult{Status: DecodeUnknown, Kind: ABIKindError, Warning: "empty revert data"}
	}
	if len(data) < 4 {
		return DecodeResult{Status: DecodeMalformed, Kind: ABIKindError, Warning: "revert data has no complete selector"}
	}
	var selector [4]byte
	copy(selector[:], data[:4])
	if builtin := registry.decodeBuiltinRevert(selector, data[4:]); builtin != nil {
		return *builtin
	}
	return DecodeResult{Status: DecodeUnknown, Kind: ABIKindError, Warning: "revert selector has no builtin ABI"}
}

func (registry *ABIRegistry) decodeBuiltinRevert(selector [4]byte, payload []byte) *DecodeResult {
	var entry abiEntry
	switch selector {
	case SignatureSelector("Error(string)"):
		entry = builtinEntry(ABIKindError, "Error", []abiParameter{{Name: "message", Type: "string"}}, registry.limits)
	case SignatureSelector("Panic(uint256)"):
		entry = builtinEntry(ABIKindError, "Panic", []abiParameter{{Name: "code", Type: "uint256"}}, registry.limits)
	default:
		return nil
	}
	result := registry.decodeCallables(ABIKindError, selector, []abiEntry{entry}, payload)
	return &result
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
	result, _ := registry.decodeCallablesWithEntry(kind, selector, entries, payload)
	return result
}

func (registry *ABIRegistry) decodeCallablesWithEntry(kind ABIKind, selector [4]byte, entries []abiEntry, payload []byte) (DecodeResult, *abiEntry) {
	identifier := "0x" + hex.EncodeToString(selector[:])
	if len(entries) == 0 {
		return DecodeResult{Status: DecodeUnknown, Kind: kind, Warning: "unknown ABI identifier " + identifier}, nil
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
				}, nil
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
		}, nil
	}
	return chooseDecodedABICandidate(kind, decoded)
}

type decodedABICandidate struct {
	entry     abiEntry
	arguments []DecodedArgument
}

func chooseDecodedABICandidate(kind ABIKind, decoded []decodedABICandidate) (DecodeResult, *abiEntry) {
	sort.SliceStable(decoded, func(left, right int) bool {
		return confidenceRank(decoded[left].entry.confidence()) > confidenceRank(decoded[right].entry.confidence())
	})
	bestRank := confidenceRank(decoded[0].entry.confidence())
	bestSignatures := make(map[string]struct{})
	for _, item := range decoded {
		if confidenceRank(item.entry.confidence()) != bestRank {
			break
		}
		bestSignatures[abiCandidateLabel(item.entry)] = struct{}{}
	}
	status := DecodeDecoded
	warning := ""
	if len(bestSignatures) > 1 {
		status = DecodeAmbiguous
		warning = "multiple ABI candidates with equal confidence decoded successfully"
	}
	selected := decoded[0]
	result := DecodeResult{
		Status:         status,
		Kind:           kind,
		Name:           selected.entry.name,
		Signature:      selected.entry.signature,
		Source:         selected.entry.source,
		SourceAddress:  selected.entry.sourceAddress,
		SourceCodeHash: selected.entry.sourceCodeHash,
		Confidence:     selected.entry.confidence(),
		Arguments:      selected.arguments,
		Candidates:     uniqueDecodedSignatures(decoded),
		Warning:        warning,
	}
	if status == DecodeAmbiguous {
		return result, nil
	}
	entry := selected.entry
	return result, &entry
}

func uniqueSignatures(entries []abiEntry) []string {
	seen := make(map[string]struct{}, len(entries))
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		label := abiCandidateLabel(entry)
		if _, exists := seen[label]; exists {
			continue
		}
		seen[label] = struct{}{}
		result = append(result, label)
	}
	sort.Strings(result)
	return result
}

func uniqueDecodedSignatures(entries []decodedABICandidate) []string {
	seen := make(map[string]struct{}, len(entries))
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		label := abiCandidateLabel(entry.entry)
		if _, exists := seen[label]; exists {
			continue
		}
		seen[label] = struct{}{}
		result = append(result, label)
	}
	sort.Strings(result)
	return result
}

func abiCandidateLabel(entry abiEntry) string {
	if entry.source == ABISourceDiamondFacet && entry.sourceAddress != (common.Address{}) {
		return entry.signature + " @ " + entry.sourceAddress.Hex()
	}
	return entry.signature
}

func (registry *ABIRegistry) DecodeLog(identity ABIIdentity, topics []common.Hash, data []byte) DecodeResult {
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
		if len(topics) > 0 {
			entries = append(entries, candidates.events[topics[0]]...)
		}
		entries = append(entries, candidates.anonymousEvents...)
	}
	registry.mu.RUnlock()
	if len(entries) == 0 {
		identifier := "anonymous log"
		if len(topics) > 0 {
			identifier = "event topic " + topics[0].String()
		}
		return DecodeResult{Status: DecodeUnknown, Kind: ABIKindEvent, Warning: "unknown " + identifier}
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
	result, _ := chooseDecodedABICandidate(ABIKindEvent, decoded)
	return result
}

func (registry *ABIRegistry) decodeEvent(entry abiEntry, topics []common.Hash, data []byte, budget *abiDecodeBudget) ([]DecodedArgument, error) {
	indexedCount := 0
	var nonIndexedTypes []*abiType
	for index, indexed := range entry.indexed {
		if indexed {
			indexedCount++
		} else {
			nonIndexedTypes = append(nonIndexedTypes, entry.types[index])
		}
	}
	signatureTopics := 1
	if entry.anonymous {
		signatureTopics = 0
	}
	if len(topics) != indexedCount+signatureTopics {
		return nil, fmt.Errorf("got %d indexed topics, want %d", len(topics)-signatureTopics, indexedCount)
	}
	nonIndexedValues, err := decodeABIValuesWithBudget(nonIndexedTypes, data, registry.limits, budget)
	if err != nil {
		return nil, fmt.Errorf("decode event data: %w", err)
	}
	arguments := make([]DecodedArgument, len(entry.inputs))
	topicIndex, dataIndex := signatureTopics, 0
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
