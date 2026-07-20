package enrich

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

type ABISource string

const (
	ABISourceVerified            ABISource = "verified"
	ABISourceProxyImplementation ABISource = "proxy_implementation"
	ABISourceSignatureDatabase   ABISource = "signature_database"
	ABISourceBuiltin             ABISource = "builtin"
)

func (source ABISource) confidence() Confidence {
	switch source {
	case ABISourceVerified:
		return ConfidenceVerified
	case ABISourceProxyImplementation, ABISourceBuiltin:
		return ConfidenceHigh
	case ABISourceSignatureDatabase:
		return ConfidenceGuess
	default:
		return ""
	}
}

func (source ABISource) persistent() bool {
	return source == ABISourceVerified || source == ABISourceProxyImplementation || source == ABISourceSignatureDatabase
}

// ABIIdentity is the exact target identity at which ABI material may be used.
// BlockHash deliberately participates in the identity: two forks at the same
// height must never share candidates through an in-process registry.
type ABIIdentity struct {
	ChainID     string
	Address     Address
	CodeHash    Word
	BlockNumber uint64
	BlockHash   Word
}

func (identity ABIIdentity) validate() error {
	chainID := new(big.Int)
	if identity.ChainID == "" {
		return errors.New("ABI identity chain ID is empty")
	}
	if _, ok := chainID.SetString(identity.ChainID, 10); !ok || chainID.Sign() < 0 {
		return fmt.Errorf("ABI identity chain ID %q is not an unsigned decimal", identity.ChainID)
	}
	if identity.CodeHash.IsZero() {
		return errors.New("ABI identity code hash is zero")
	}
	if identity.BlockHash.IsZero() {
		return errors.New("ABI identity block hash is zero")
	}
	return nil
}

// ABIBinding describes the durable provenance and validity interval for one
// candidate set. Confidence is derived from Source and is never caller-set.
type ABIBinding struct {
	Identity       ABIIdentity
	Source         ABISource
	SourceAddress  Address
	SourceCodeHash Word
	ValidFromBlock uint64
	ValidToBlock   *uint64
}

func (binding ABIBinding) validate() error {
	if err := binding.Identity.validate(); err != nil {
		return err
	}
	if !binding.Source.persistent() {
		return fmt.Errorf("ABI source %q cannot be registered as durable material", binding.Source)
	}
	if binding.SourceCodeHash.IsZero() {
		return errors.New("ABI binding source code hash is zero")
	}
	if binding.ValidToBlock != nil && *binding.ValidToBlock < binding.ValidFromBlock {
		return errors.New("ABI binding validity range is inverted")
	}
	if binding.Identity.BlockNumber < binding.ValidFromBlock || binding.ValidToBlock != nil && binding.Identity.BlockNumber > *binding.ValidToBlock {
		return errors.New("ABI identity block is outside binding validity range")
	}
	if binding.Source != ABISourceProxyImplementation &&
		(binding.SourceAddress != binding.Identity.Address || binding.SourceCodeHash != binding.Identity.CodeHash) {
		return errors.New("direct and signature ABI bindings must use the target source identity")
	}
	return nil
}

func (source ABISource) validate() error {
	if source.confidence() == "" {
		return fmt.Errorf("unknown ABI source %q", source)
	}
	return nil
}

type abiParameter struct {
	Name       string         `json:"name"`
	Type       string         `json:"type"`
	Indexed    bool           `json:"indexed,omitempty"`
	Components []abiParameter `json:"components,omitempty"`
}

type abiEntryJSON struct {
	Type      string         `json:"type"`
	Name      string         `json:"name"`
	Inputs    []abiParameter `json:"inputs"`
	Anonymous bool           `json:"anonymous,omitempty"`
}

type abiEntry struct {
	kind      ABIKind
	name      string
	signature string
	inputs    []abiParameter
	types     []*abiType
	indexed   []bool
	source    ABISource
	selector  [4]byte
	topic     Word
}

func (entry abiEntry) confidence() Confidence { return entry.source.confidence() }

type ABIKind string

const (
	ABIKindFunction ABIKind = "function"
	ABIKindEvent    ABIKind = "event"
	ABIKindError    ABIKind = "error"
)

type DecodeStatus string

const (
	DecodeDecoded   DecodeStatus = "decoded"
	DecodeUnknown   DecodeStatus = "unknown"
	DecodeMalformed DecodeStatus = "malformed"
	DecodeAmbiguous DecodeStatus = "ambiguous"
)

type DecodedArgument struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Indexed bool   `json:"indexed"`
	Hashed  bool   `json:"hashed"`
	Value   any    `json:"value"`
}

type DecodeResult struct {
	Status     DecodeStatus
	Kind       ABIKind
	Name       string
	Signature  string
	Source     ABISource
	Confidence Confidence
	Arguments  []DecodedArgument
	Candidates []string
	Warning    string
}

// DecodeLimits bound attacker-controlled dynamic values and candidate ABI
// documents. MaxArguments applies to a tuple parameter list, while
// MaxArrayElements independently bounds arrays. The decode-wide node, work,
// and byte limits are shared across selector candidates and count repeated
// traversal through aliased offsets. MaxDepth applies to tuple/array nesting.
type DecodeLimits struct {
	MaxArguments      int
	MaxArrayElements  int
	MaxDynamicBytes   int
	MaxDecodeNodes    int
	MaxDecodeWork     int
	MaxDecodedBytes   int
	MaxDepth          int
	MaxDocumentBytes  int
	MaxEntries        int
	MaxSignatureBytes int
}

func DefaultDecodeLimits() DecodeLimits {
	return DecodeLimits{
		MaxArguments:      256,
		MaxArrayElements:  4096,
		MaxDynamicBytes:   4 << 20,
		MaxDecodeNodes:    65_536,
		MaxDecodeWork:     1 << 20,
		MaxDecodedBytes:   16 << 20,
		MaxDepth:          16,
		MaxDocumentBytes:  4 << 20,
		MaxEntries:        4096,
		MaxSignatureBytes: 4096,
	}
}

func (limits DecodeLimits) validate() error {
	if limits.MaxArguments <= 0 || limits.MaxArrayElements <= 0 || limits.MaxDynamicBytes <= 0 ||
		limits.MaxDecodeNodes <= 0 || limits.MaxDecodeWork <= 0 || limits.MaxDecodedBytes <= 0 || limits.MaxDepth <= 0 ||
		limits.MaxDocumentBytes <= 2 || limits.MaxEntries <= 0 || limits.MaxSignatureBytes <= 0 {
		return errors.New("all ABI decode limits must be positive")
	}
	return nil
}

type abiTypeKind uint8

const (
	abiUint abiTypeKind = iota + 1
	abiInt
	abiBool
	abiAddress
	abiFixedBytes
	abiDynamicBytes
	abiString
	abiFunction
	abiArray
	abiTuple
)

type abiType struct {
	kind        abiTypeKind
	bits        int
	size        int
	element     *abiType
	arrayLength int // -1 denotes a dynamic array.
	components  []*abiType
}

func (value *abiType) dynamic() bool {
	switch value.kind {
	case abiDynamicBytes, abiString:
		return true
	case abiArray:
		return value.arrayLength < 0 || value.element.dynamic()
	case abiTuple:
		for _, component := range value.components {
			if component.dynamic() {
				return true
			}
		}
	}
	return false
}

func (value *abiType) staticSize() (int, error) {
	if value.dynamic() {
		return 0, errors.New("dynamic ABI type has no static size")
	}
	switch value.kind {
	case abiUint, abiInt, abiBool, abiAddress, abiFixedBytes, abiFunction:
		return 32, nil
	case abiArray:
		elementSize, err := value.element.staticSize()
		if err != nil {
			return 0, err
		}
		return checkedMultiply(elementSize, value.arrayLength)
	case abiTuple:
		total := 0
		for _, component := range value.components {
			size, err := component.staticSize()
			if err != nil {
				return 0, err
			}
			if total > int(^uint(0)>>1)-size {
				return 0, errors.New("ABI static tuple size overflows int")
			}
			total += size
		}
		return total, nil
	default:
		return 0, errors.New("unsupported ABI type")
	}
}

func parseABIType(parameter abiParameter, depth int, limit int) (*abiType, error) {
	if depth > limit {
		return nil, errors.New("ABI type nesting exceeds limit")
	}
	typeName := strings.TrimSpace(parameter.Type)
	if typeName == "" {
		return nil, errors.New("ABI parameter type is empty")
	}
	baseName, dimensions, err := splitArrayDimensions(typeName)
	if err != nil {
		return nil, err
	}
	var parsed *abiType
	switch {
	case baseName == "address":
		parsed = &abiType{kind: abiAddress}
	case baseName == "bool":
		parsed = &abiType{kind: abiBool}
	case baseName == "string":
		parsed = &abiType{kind: abiString}
	case baseName == "bytes":
		parsed = &abiType{kind: abiDynamicBytes}
	case baseName == "function":
		parsed = &abiType{kind: abiFunction}
	case baseName == "tuple":
		if len(parameter.Components) == 0 {
			return nil, errors.New("tuple ABI parameter has no components")
		}
		parsed = &abiType{kind: abiTuple, components: make([]*abiType, len(parameter.Components))}
		for index, component := range parameter.Components {
			componentType, err := parseABIType(component, depth+1, limit)
			if err != nil {
				return nil, fmt.Errorf("tuple component %d: %w", index, err)
			}
			parsed.components[index] = componentType
		}
	case strings.HasPrefix(baseName, "uint"):
		bits, err := parseIntegerBits(strings.TrimPrefix(baseName, "uint"))
		if err != nil {
			return nil, fmt.Errorf("invalid %s: %w", baseName, err)
		}
		parsed = &abiType{kind: abiUint, bits: bits}
	case strings.HasPrefix(baseName, "int"):
		bits, err := parseIntegerBits(strings.TrimPrefix(baseName, "int"))
		if err != nil {
			return nil, fmt.Errorf("invalid %s: %w", baseName, err)
		}
		parsed = &abiType{kind: abiInt, bits: bits}
	case strings.HasPrefix(baseName, "bytes"):
		size, err := strconv.Atoi(strings.TrimPrefix(baseName, "bytes"))
		if err != nil || size < 1 || size > 32 {
			return nil, fmt.Errorf("fixed bytes size must be between 1 and 32")
		}
		parsed = &abiType{kind: abiFixedBytes, size: size}
	default:
		return nil, fmt.Errorf("unsupported ABI type %q", baseName)
	}
	for _, dimension := range dimensions {
		parsed = &abiType{kind: abiArray, element: parsed, arrayLength: dimension}
	}
	return parsed, nil
}

func splitArrayDimensions(typeName string) (string, []int, error) {
	start := strings.IndexByte(typeName, '[')
	if start < 0 {
		return typeName, nil, nil
	}
	base := typeName[:start]
	remainder := typeName[start:]
	var dimensions []int
	for len(remainder) > 0 {
		if remainder[0] != '[' {
			return "", nil, fmt.Errorf("invalid array suffix %q", remainder)
		}
		end := strings.IndexByte(remainder, ']')
		if end < 0 {
			return "", nil, fmt.Errorf("unterminated array suffix %q", remainder)
		}
		lengthText := remainder[1:end]
		length := -1
		if lengthText != "" {
			parsed, err := strconv.Atoi(lengthText)
			if err != nil || parsed <= 0 {
				return "", nil, fmt.Errorf("invalid array length %q", lengthText)
			}
			length = parsed
		}
		dimensions = append(dimensions, length)
		remainder = remainder[end+1:]
	}
	return base, dimensions, nil
}

func parseIntegerBits(value string) (int, error) {
	if value == "" {
		return 256, nil
	}
	bits, err := strconv.Atoi(value)
	if err != nil || bits < 8 || bits > 256 || bits%8 != 0 {
		return 0, errors.New("integer width must be an 8-bit multiple between 8 and 256")
	}
	return bits, nil
}

func canonicalParameter(parameter abiParameter) (string, error) {
	typeName := parameter.Type
	base, dimensions, err := splitArrayDimensions(typeName)
	if err != nil {
		return "", err
	}
	switch base {
	case "uint":
		base = "uint256"
	case "int":
		base = "int256"
	case "tuple":
		components := make([]string, len(parameter.Components))
		for index, component := range parameter.Components {
			canonical, err := canonicalParameter(component)
			if err != nil {
				return "", err
			}
			components[index] = canonical
		}
		base = "(" + strings.Join(components, ",") + ")"
	}
	var result strings.Builder
	result.WriteString(base)
	for _, length := range dimensions {
		result.WriteByte('[')
		if length >= 0 {
			result.WriteString(strconv.Itoa(length))
		}
		result.WriteByte(']')
	}
	return result.String(), nil
}

func parseABIEntries(data []byte, source ABISource, limits DecodeLimits) ([]abiEntry, error) {
	if err := source.validate(); err != nil {
		return nil, err
	}
	if err := limits.validate(); err != nil {
		return nil, err
	}
	if len(data) > limits.MaxDocumentBytes {
		return nil, errors.New("ABI document exceeds byte limit")
	}
	var raw []abiEntryJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode ABI JSON: %w", err)
	}
	if len(raw) > limits.MaxEntries {
		return nil, errors.New("ABI document contains too many entries")
	}
	entries := make([]abiEntry, 0, len(raw))
	for index, item := range raw {
		kind := ABIKind(item.Type)
		if kind != ABIKindFunction && kind != ABIKindEvent && kind != ABIKindError {
			continue
		}
		if item.Name == "" {
			return nil, fmt.Errorf("ABI entry %d has no name", index)
		}
		if len(item.Inputs) > limits.MaxArguments {
			return nil, fmt.Errorf("ABI entry %s has too many inputs", item.Name)
		}
		if kind == ABIKindEvent && item.Anonymous {
			// Anonymous events have no signature topic and cannot be selected from
			// an isolated log without a contract-specific candidate set.
			continue
		}
		entry := abiEntry{
			kind:    kind,
			name:    item.Name,
			inputs:  item.Inputs,
			types:   make([]*abiType, len(item.Inputs)),
			indexed: make([]bool, len(item.Inputs)),
			source:  source,
		}
		canonicalInputs := make([]string, len(item.Inputs))
		for inputIndex, input := range item.Inputs {
			canonical, err := canonicalParameter(input)
			if err != nil {
				return nil, fmt.Errorf("ABI entry %s input %d: %w", item.Name, inputIndex, err)
			}
			parsedType, err := parseABIType(input, 1, limits.MaxDepth)
			if err != nil {
				return nil, fmt.Errorf("ABI entry %s input %d: %w", item.Name, inputIndex, err)
			}
			canonicalInputs[inputIndex] = canonical
			entry.types[inputIndex] = parsedType
			entry.indexed[inputIndex] = input.Indexed
		}
		entry.signature = item.Name + "(" + strings.Join(canonicalInputs, ",") + ")"
		if len(entry.signature) > limits.MaxSignatureBytes {
			return nil, fmt.Errorf("ABI entry %s signature exceeds byte limit", item.Name)
		}
		// Solidity's standard Error and Panic payloads are decoded locally. They
		// are not contract-specific ABI evidence, regardless of which durable
		// source document happened to repeat them.
		if kind == ABIKindError && isBuiltinErrorSignature(entry.signature) {
			continue
		}
		if kind == ABIKindEvent {
			entry.topic = signatureHash(entry.signature)
		} else {
			entry.selector = SignatureSelector(entry.signature)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func checkedMultiply(left, right int) (int, error) {
	if left < 0 || right < 0 {
		return 0, errors.New("cannot multiply a negative ABI size")
	}
	maxInt := int(^uint(0) >> 1)
	if left != 0 && right > maxInt/left {
		return 0, errors.New("ABI size overflows int")
	}
	return left * right, nil
}
