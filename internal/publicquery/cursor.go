package publicquery

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

const MaximumOpaqueCursorLength = 1024

func EncodeCursor(value any) (string, error) {
	payload, err := json.Marshal(struct {
		Version int `json:"v"`
		Value   any `json:"value"`
	}{Version: 1, Value: value})
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	if len(encoded) > MaximumOpaqueCursorLength {
		return "", errors.New("cursor exceeds maximum length")
	}
	return encoded, nil
}

func DecodeCursor(cursor string, target any) error {
	if len(cursor) == 0 || len(cursor) > MaximumOpaqueCursorLength {
		return errors.New("invalid cursor length")
	}
	payload, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return errors.New("invalid cursor encoding")
	}
	var envelope struct {
		Version int             `json:"v"`
		Value   json.RawMessage `json:"value"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil || envelope.Version != 1 {
		return errors.New("invalid cursor payload")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("invalid cursor payload")
	}
	if target == nil || len(envelope.Value) == 0 || string(envelope.Value) == "null" {
		return errors.New("cursor target is required")
	}
	decoder = json.NewDecoder(strings.NewReader(string(envelope.Value)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid cursor value")
	}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("invalid cursor value")
	}
	return nil
}
