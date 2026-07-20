package metadata

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// validateDocument enforces an object-shaped, bounded JSON metadata document.
// The HTTP client already caps bytes; these limits prevent a small but deeply
// nested or high-cardinality object from becoming an expensive query payload.
func validateDocument(document []byte) error {
	if len(document) == 0 || int64(len(document)) > MaxDocumentBytes {
		return fmt.Errorf("metadata document must contain between 1 and %d bytes", MaxDocumentBytes)
	}
	if err := validatePostgresJSONText(document); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("decode metadata document: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return err
	}
	root, ok := value.(map[string]any)
	if !ok || root == nil {
		return errors.New("metadata document root must be an object")
	}
	nodes := 0
	if err := validateDocumentValue(root, 1, &nodes); err != nil {
		return err
	}
	for _, field := range []string{"name", "description", "image", "animation_url", "external_url", "background_color"} {
		fieldValue, exists := root[field]
		if !exists || fieldValue == nil {
			continue
		}
		if _, ok := fieldValue.(string); !ok {
			return fmt.Errorf("metadata field %q must be a string or null", field)
		}
	}
	if attributes, exists := root["attributes"]; exists && attributes != nil {
		if _, ok := attributes.([]any); !ok {
			return errors.New("metadata field \"attributes\" must be an array or null")
		}
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("decode metadata document suffix: %w", err)
	}
	return errors.New("metadata document contains multiple JSON values")
}

func validateDocumentValue(value any, depth int, nodes *int) error {
	*nodes++
	if *nodes > maxDocumentNodes {
		return fmt.Errorf("metadata document exceeds %d values", maxDocumentNodes)
	}
	if depth > maxDocumentDepth {
		return fmt.Errorf("metadata document exceeds nesting depth %d", maxDocumentDepth)
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if len(key) > maxDocumentStringSize {
				return errors.New("metadata document contains an oversized object key")
			}
			if err := validateDocumentValue(child, depth+1, nodes); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := validateDocumentValue(child, depth+1, nodes); err != nil {
				return err
			}
		}
	case string:
		if len(typed) > maxDocumentStringSize {
			return fmt.Errorf("metadata document string exceeds %d bytes", maxDocumentStringSize)
		}
	case json.Number:
		if err := validateJSONNumber(typed); err != nil {
			return err
		}
	case nil, bool:
		return nil
	default:
		return fmt.Errorf("metadata document contains unsupported JSON value %T", value)
	}
	return nil
}

// PostgreSQL jsonb rejects NUL escapes and malformed UTF-16 pairs even though
// encoding/json accepts some of them by substituting U+FFFD. Validate the wire
// representation before decoding so a successful fetch can always be stored.
func validatePostgresJSONText(document []byte) error {
	if !utf8.Valid(document) {
		return errors.New("metadata document is not valid UTF-8")
	}
	inString := false
	for index := 0; index < len(document); index++ {
		switch document[index] {
		case '"':
			inString = !inString
		case '\\':
			if !inString || index+1 >= len(document) {
				continue
			}
			index++
			if document[index] != 'u' || index+4 >= len(document) {
				continue
			}
			code, ok := parseHexWord(document[index+1 : index+5])
			if !ok {
				continue
			}
			if code == 0 {
				return errors.New("metadata document contains a PostgreSQL-incompatible NUL escape")
			}
			index += 4
			if utf16.IsSurrogate(rune(code)) {
				if code < 0xd800 || code > 0xdbff || index+6 >= len(document) ||
					document[index+1] != '\\' || document[index+2] != 'u' {
					return errors.New("metadata document contains an unpaired UTF-16 surrogate")
				}
				low, lowOK := parseHexWord(document[index+3 : index+7])
				if !lowOK || low < 0xdc00 || low > 0xdfff {
					return errors.New("metadata document contains an unpaired UTF-16 surrogate")
				}
				index += 6
			}
		}
	}
	return nil
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

func validateJSONNumber(number json.Number) error {
	value := number.String()
	if len(value) > maxJSONNumberBytes {
		return fmt.Errorf("metadata JSON number exceeds %d bytes", maxJSONNumberBytes)
	}
	if exponentAt := strings.IndexAny(value, "eE"); exponentAt >= 0 {
		exponentText := value[exponentAt+1:]
		if len(exponentText) > 6 {
			return errors.New("metadata JSON number exponent exceeds PostgreSQL bounds")
		}
		exponent, err := strconv.Atoi(exponentText)
		if err != nil || exponent < -maxJSONNumberExponent || exponent > maxJSONNumberExponent {
			return errors.New("metadata JSON number exponent exceeds PostgreSQL bounds")
		}
	}
	return nil
}
