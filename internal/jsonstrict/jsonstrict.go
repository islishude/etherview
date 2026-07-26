// Package jsonstrict validates JSON at hostile trust boundaries before typed
// decoding. The standard encoding/json package accepts duplicate object keys
// and, when decoding into interface values, may lose integer precision.
package jsonstrict

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"unicode/utf16"
	"unicode/utf8"
)

var (
	ErrDuplicateKey = errors.New("json_duplicate_key")
	ErrSyntax       = errors.New("json_invalid")
	ErrTrailingData = errors.New("json_trailing_data")
	ErrTooDeep      = errors.New("json_too_deep")
	ErrTooManyNodes = errors.New("json_too_many_nodes")
	ErrUnsafeNumber = errors.New("json_unsafe_number")
)

const (
	defaultMaxDepth = 32
	defaultMaxNodes = 4096
)

var maxSafeInteger = big.NewInt(1<<53 - 1)

// Limits bounds the amount and shape of JSON accepted by Validate. Zero
// MaxDepth or MaxNodes values select conservative defaults.
type Limits struct {
	MaxDepth         int
	MaxNodes         int
	SafeIntegersOnly bool
}

// Validate accepts exactly one JSON value, rejects duplicate object keys, and
// applies explicit depth and node budgets. When SafeIntegersOnly is set, every
// number must be a canonical decimal integer within JavaScript's exact integer
// range.
func Validate(data []byte, limits Limits) error {
	if !utf8.Valid(data) || !validUTF16Escapes(data) {
		return ErrSyntax
	}
	if limits.MaxDepth <= 0 {
		limits.MaxDepth = defaultMaxDepth
	}
	if limits.MaxNodes <= 0 {
		limits.MaxNodes = defaultMaxNodes
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	first, err := decoder.Token()
	if err != nil {
		return ErrSyntax
	}
	state := validationState{decoder: decoder, limits: limits}
	if err := state.value(first, 0); err != nil {
		return err
	}

	if _, err := decoder.Token(); err == io.EOF {
		return nil
	} else if err != nil {
		return ErrSyntax
	}
	return ErrTrailingData
}

// encoding/json replaces unpaired UTF-16 surrogate escapes with U+FFFD.
// Reject them on the wire so distinct hostile inputs cannot normalize into the
// same typed value or fingerprint.
func validUTF16Escapes(data []byte) bool {
	inString := false
	for index := 0; index < len(data); index++ {
		switch data[index] {
		case '"':
			inString = !inString
		case '\\':
			if !inString || index+1 >= len(data) {
				continue
			}
			index++
			if data[index] != 'u' || index+4 >= len(data) {
				continue
			}
			code, ok := parseHexWord(data[index+1 : index+5])
			if !ok {
				continue
			}
			index += 4
			if !utf16.IsSurrogate(rune(code)) {
				continue
			}
			if code < 0xd800 || code > 0xdbff ||
				index+6 >= len(data) ||
				data[index+1] != '\\' ||
				data[index+2] != 'u' {
				return false
			}
			low, ok := parseHexWord(data[index+3 : index+7])
			if !ok || low < 0xdc00 || low > 0xdfff {
				return false
			}
			index += 6
		}
	}
	return true
}

func parseHexWord(value []byte) (uint16, bool) {
	if len(value) != 4 {
		return 0, false
	}
	var result uint16
	for _, character := range value {
		result <<= 4
		switch {
		case character >= '0' && character <= '9':
			result |= uint16(character - '0')
		case character >= 'a' && character <= 'f':
			result |= uint16(character-'a') + 10
		case character >= 'A' && character <= 'F':
			result |= uint16(character-'A') + 10
		default:
			return 0, false
		}
	}
	return result, true
}

type validationState struct {
	decoder *json.Decoder
	limits  Limits
	nodes   int
}

func (s *validationState) value(token json.Token, depth int) error {
	s.nodes++
	if s.nodes > s.limits.MaxNodes {
		return ErrTooManyNodes
	}

	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			return s.object(depth + 1)
		case '[':
			return s.array(depth + 1)
		default:
			return ErrSyntax
		}
	case json.Number:
		if s.limits.SafeIntegersOnly && !safeCanonicalInteger(string(value)) {
			return ErrUnsafeNumber
		}
	case string, bool, nil:
	default:
		return ErrSyntax
	}
	return nil
}

func (s *validationState) object(depth int) error {
	if depth > s.limits.MaxDepth {
		return ErrTooDeep
	}

	keys := make(map[string]struct{})
	for s.decoder.More() {
		keyToken, err := s.decoder.Token()
		if err != nil {
			return ErrSyntax
		}
		key, ok := keyToken.(string)
		if !ok {
			return ErrSyntax
		}
		s.nodes++
		if s.nodes > s.limits.MaxNodes {
			return ErrTooManyNodes
		}
		if _, exists := keys[key]; exists {
			return ErrDuplicateKey
		}
		keys[key] = struct{}{}

		valueToken, err := s.decoder.Token()
		if err != nil {
			return ErrSyntax
		}
		if err := s.value(valueToken, depth); err != nil {
			return err
		}
	}
	end, err := s.decoder.Token()
	if err != nil || end != json.Delim('}') {
		return ErrSyntax
	}
	return nil
}

func (s *validationState) array(depth int) error {
	if depth > s.limits.MaxDepth {
		return ErrTooDeep
	}
	for s.decoder.More() {
		token, err := s.decoder.Token()
		if err != nil {
			return ErrSyntax
		}
		if err := s.value(token, depth); err != nil {
			return err
		}
	}
	end, err := s.decoder.Token()
	if err != nil || end != json.Delim(']') {
		return ErrSyntax
	}
	return nil
}

func safeCanonicalInteger(value string) bool {
	if value == "" {
		return false
	}
	if value == "0" {
		return true
	}

	start := 0
	switch value[0] {
	case '-':
		if len(value) == 1 || value[1] == '0' {
			return false
		}
		start = 1
	case '0':
		return false
	}
	for index := start; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}

	parsed := new(big.Int)
	if _, ok := parsed.SetString(value, 10); !ok {
		return false
	}
	parsed.Abs(parsed)
	return parsed.Cmp(maxSafeInteger) <= 0
}
