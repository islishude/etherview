// Package abibounds bounds hostile ABI types and value expansion before geth
// constructs reflection types or allocates decoded values.
package abibounds

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/islishude/etherview/internal/jsonstrict"
)

const (
	maxDocumentBytes = 16 << 20
	maxDepth         = 16
	maxNodes         = 16384
	maxValueBytes    = 1 << 20
)

var ErrLimit = errors.New("ABI exceeds decoding bounds")

// Limits describes one caller's admission budget. Type expansion is checked
// before reflection construction; value expansion is checked before Unpack.
type Limits struct {
	MaxDocumentBytes int
	MaxEntries       int
	MaxArguments     int
	MaxTypeBytes     int
	MaxDepth         int
	MaxNodes         int
	MaxDataBytes     int
	MaxValueBytes    int
	MaxDynamicBytes  int
	MaxArrayElements int
}

func DefaultLimits() Limits {
	return Limits{
		MaxDocumentBytes: maxDocumentBytes, MaxEntries: 1024, MaxArguments: maxNodes, MaxTypeBytes: 256,
		MaxDepth: maxDepth, MaxNodes: maxNodes, MaxDataBytes: maxDocumentBytes,
		MaxValueBytes: maxValueBytes, MaxDynamicBytes: maxValueBytes, MaxArrayElements: maxNodes,
	}
}

func (limits Limits) valid() bool {
	return limits.MaxDocumentBytes > 2 && limits.MaxEntries > 0 && limits.MaxArguments > 0 && limits.MaxTypeBytes > 0 &&
		limits.MaxDepth > 0 && limits.MaxNodes > 0 && limits.MaxDataBytes > 0 &&
		limits.MaxValueBytes > 0 && limits.MaxDynamicBytes > 0 && limits.MaxArrayElements > 0
}

// ParseEntry applies the document bounds before constructing a singleton ABI.
func ParseEntry(raw []byte, limits Limits) (abi.ABI, error) {
	if !limits.valid() || len(raw) > limits.MaxDocumentBytes-2 {
		return abi.ABI{}, ErrLimit
	}
	document := make([]byte, 0, len(raw)+2)
	document = append(document, '[')
	document = append(document, raw...)
	document = append(document, ']')
	return ParseWithLimits(document, limits)
}

// Parse validates type complexity, including fixed-array expansion, before
// abi.JSON can recursively construct type names and reflection types.
func Parse(raw []byte) (abi.ABI, error) {
	return ParseWithLimits(raw, DefaultLimits())
}

func ParseWithLimits(raw []byte, limits Limits) (abi.ABI, error) {
	if !limits.valid() || len(raw) > limits.MaxDocumentBytes || jsonstrict.Validate(raw, jsonstrict.Limits{MaxDepth: 64, MaxNodes: 131072}) != nil {
		return abi.ABI{}, ErrLimit
	}
	var entries []struct {
		Inputs  []abi.ArgumentMarshaling `json:"inputs"`
		Outputs []abi.ArgumentMarshaling `json:"outputs"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil || len(entries) > limits.MaxEntries {
		return abi.ABI{}, ErrLimit
	}
	remaining := limits.MaxNodes
	for _, entry := range entries {
		for _, arguments := range [][]abi.ArgumentMarshaling{entry.Inputs, entry.Outputs} {
			if len(arguments) > limits.MaxArguments {
				return abi.ABI{}, ErrLimit
			}
			for _, argument := range arguments {
				if !boundedType(argument, 0, 1, &remaining, limits) {
					return abi.ABI{}, ErrLimit
				}
			}
		}
	}
	return abi.JSON(bytes.NewReader(raw))
}

func boundedType(argument abi.ArgumentMarshaling, depth, multiplier int, remaining *int, limits Limits) bool {
	if len(argument.Type) > limits.MaxTypeBytes || depth > limits.MaxDepth || len(argument.Components) > limits.MaxArguments {
		return false
	}
	base, suffix, _ := strings.Cut(argument.Type, "[")
	if strings.Contains(argument.Type, "[") {
		suffix = "[" + suffix
		for suffix != "" {
			if !strings.HasPrefix(suffix, "[") {
				return false
			}
			size, tail, ok := strings.Cut(suffix[1:], "]")
			if !ok {
				return false
			}
			depth++
			if depth > limits.MaxDepth {
				return false
			}
			if size != "" {
				count, err := strconv.Atoi(size)
				if err != nil || count < 0 || count > limits.MaxArrayElements || multiplier > limits.MaxNodes/max(1, count) {
					return false
				}
				multiplier *= max(1, count)
			}
			suffix = tail
		}
	}
	if multiplier > *remaining {
		return false
	}
	*remaining -= multiplier
	if base != "tuple" && len(argument.Components) != 0 {
		return false
	}
	for _, component := range argument.Components {
		if !boundedType(component, depth+1, multiplier, remaining, limits) {
			return false
		}
	}
	return true
}
