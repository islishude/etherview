package verify

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

const (
	maxStrictJSONDepth = 128
	maxStrictJSONNodes = 1 << 20
)

var (
	errJSONDuplicateKey = errors.New("JSON contains a duplicate object key")
	errJSONTooDeep      = errors.New("JSON nesting exceeds the limit")
	errJSONTooManyNodes = errors.New("JSON value count exceeds the limit")
)

// validateUniqueJSON walks one JSON value before map decoding so duplicate
// object keys cannot be silently resolved with Go's last-key-wins semantics.
func validateUniqueJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	nodes := 0
	if err := walkUniqueJSONValue(decoder, 0, &nodes); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON contains trailing values")
		}
		return err
	}
	return nil
}

func walkUniqueJSONValue(decoder *json.Decoder, depth int, nodes *int) error {
	if depth > maxStrictJSONDepth {
		return errJSONTooDeep
	}
	(*nodes)++
	if *nodes > maxStrictJSONNodes {
		return errJSONTooManyNodes
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, keyErr := decoder.Token()
			if keyErr != nil {
				return keyErr
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return errJSONDuplicateKey
			}
			seen[key] = struct{}{}
			if err := walkUniqueJSONValue(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, closeErr := decoder.Token()
		if closeErr != nil || closing != json.Delim('}') {
			if closeErr != nil {
				return closeErr
			}
			return errors.New("JSON object is not closed")
		}
	case '[':
		for decoder.More() {
			if err := walkUniqueJSONValue(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, closeErr := decoder.Token()
		if closeErr != nil || closing != json.Delim(']') {
			if closeErr != nil {
				return closeErr
			}
			return errors.New("JSON array is not closed")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}
