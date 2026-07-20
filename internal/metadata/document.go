package metadata

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// validateDocument enforces an object-shaped, bounded JSON metadata document.
// The HTTP client already caps bytes; these limits prevent a small but deeply
// nested or high-cardinality object from becoming an expensive query payload.
func validateDocument(document []byte) error {
	if len(document) == 0 || int64(len(document)) > MaxDocumentBytes {
		return fmt.Errorf("metadata document must contain between 1 and %d bytes", MaxDocumentBytes)
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
	case nil, bool, json.Number:
		return nil
	default:
		return fmt.Errorf("metadata document contains unsupported JSON value %T", value)
	}
	return nil
}
