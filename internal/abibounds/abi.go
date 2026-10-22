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

// Parse validates type complexity, including fixed-array expansion, before
// abi.JSON can recursively construct type names and reflection types.
func Parse(raw []byte) (abi.ABI, error) {
	if len(raw) > maxDocumentBytes || jsonstrict.Validate(raw, jsonstrict.Limits{MaxDepth: 64, MaxNodes: 131072}) != nil {
		return abi.ABI{}, ErrLimit
	}
	var entries []struct {
		Inputs  []abi.ArgumentMarshaling `json:"inputs"`
		Outputs []abi.ArgumentMarshaling `json:"outputs"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil || len(entries) > 1024 {
		return abi.ABI{}, ErrLimit
	}
	remaining := maxNodes
	for _, entry := range entries {
		for _, arguments := range [][]abi.ArgumentMarshaling{entry.Inputs, entry.Outputs} {
			for _, argument := range arguments {
				if !boundedType(argument, 0, 1, &remaining) {
					return abi.ABI{}, ErrLimit
				}
			}
		}
	}
	return abi.JSON(bytes.NewReader(raw))
}

func boundedType(argument abi.ArgumentMarshaling, depth, multiplier int, remaining *int) bool {
	if len(argument.Type) > 256 || depth > maxDepth {
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
			if depth > maxDepth {
				return false
			}
			if size != "" {
				count, err := strconv.Atoi(size)
				if err != nil || count < 0 || count > maxNodes || multiplier > maxNodes/max(1, count) {
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
		if !boundedType(component, depth+1, multiplier, remaining) {
			return false
		}
	}
	return true
}
