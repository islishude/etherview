package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/islishude/etherview/internal/api/gen"
)

func parseLimit(w http.ResponseWriter, r *http.Request, defaultValue int) (int, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return defaultValue, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > maximumPageSize {
		writeError(w, r, http.StatusBadRequest, "invalid_limit", fmt.Sprintf("limit must be between 1 and %d", maximumPageSize), nil)
		return 0, false
	}
	return value, true
}

func validBlockID(value string) bool {
	if hashPattern.MatchString(value) {
		return true
	}
	if strings.HasPrefix(value, "0x") {
		if len(value) <= 2 {
			return false
		}
		_, err := strconv.ParseUint(value[2:], 16, 64)
		return err == nil
	}
	_, err := strconv.ParseUint(value, 10, 64)
	return err == nil
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, details map[string]any) {
	var detailsPointer *map[string]any
	if details != nil {
		detailsPointer = &details
	}
	writeJSON(w, status, gen.ErrorResponse{Error: gen.APIError{
		Code: code, Message: message, Details: detailsPointer, RequestId: requestIDFrom(r.Context()),
	}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func quantity(value uint64) gen.Quantity { return strconv.FormatUint(value, 10) }

func saturatingSub(left, right uint64) uint64 {
	if right >= left {
		return 0
	}
	return left - right
}

func contains(values []string, value string) bool {
	return slices.Contains(values, value)
}

func addVary(header http.Header, value string) {
	values := header.Values("Vary")
	for _, existing := range values {
		for token := range strings.SplitSeq(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(token), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}

// EncodeCursor provides a stable, versioned opaque cursor helper for stores.
func EncodeCursor(value any) (string, error) {
	payload, err := json.Marshal(struct {
		Version int `json:"v"`
		Value   any `json:"value"`
	}{Version: 1, Value: value})
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	if len(encoded) > maximumOpaqueCursorLength {
		return "", errors.New("cursor exceeds maximum length")
	}
	return encoded, nil
}

// DecodeCursor rejects malformed or unsupported cursor versions.
func DecodeCursor(cursor string, target any) error {
	if len(cursor) == 0 || len(cursor) > maximumOpaqueCursorLength {
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
