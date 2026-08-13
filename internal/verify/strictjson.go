package verify

import "github.com/islishude/etherview/internal/jsonstrict"

const (
	maxStrictJSONDepth = 128
	maxStrictJSONNodes = 1 << 20
)

var errJSONDuplicateKey = jsonstrict.ErrDuplicateKey

// validateUniqueJSON validates one hostile JSON value before typed decoding.
// Besides duplicate keys, it rejects invalid UTF-8 and unpaired UTF-16
// surrogate escapes so distinct wire inputs cannot normalize into one durable
// verification request.
func validateUniqueJSON(raw []byte) error {
	return jsonstrict.Validate(raw, jsonstrict.Limits{
		MaxDepth: maxStrictJSONDepth, MaxNodes: maxStrictJSONNodes,
	})
}

// ValidateUniqueJSON exposes the verification boundary's larger structural
// budgets to HTTP handlers without applying a compiler-input schema.
func ValidateUniqueJSON(raw []byte) error {
	return validateUniqueJSON(raw)
}
