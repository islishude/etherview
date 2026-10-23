// Package abicalldata owns bounded, canonical function and constructor argument
// validation. Callers retain ownership of ABI provenance and public statuses.
package abicalldata

import (
	"bytes"
	"encoding/json"

	gethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/islishude/etherview/internal/abibounds"
)

// DecodeError distinguishes validation failures without retaining ABI input or
// nested decoder errors. Callers map these categories to their domain errors.
type DecodeError string

func (err DecodeError) Error() string { return string(err) }

const (
	ErrInvalidABI         DecodeError = "ABI is invalid or exceeds type bounds"
	ErrMissingConstructor DecodeError = "arguments exist without a constructor"
	ErrArgumentLimit      DecodeError = "ABI arguments exceed decoding bounds"
	ErrInvalidArguments   DecodeError = "ABI arguments do not decode"
	ErrNonCanonical       DecodeError = "ABI arguments are not canonically encoded"
)

// Verified function matching admits the normalized entry envelope and bounds
// expanded values before geth allocates them. The enrichment decoder continues
// to enforce its own work, output and value-depth budgets independently.
// Constructors retain their existing, smaller verification budget.
func verifiedFunctionLimits() abibounds.Limits {
	return abibounds.Limits{
		MaxDocumentBytes: 4 << 20, MaxEntries: 4096, MaxArguments: 256, MaxTypeBytes: 4096,
		// Enrichment counts the outer parameter at depth one, while abibounds
		// counts it at zero.
		MaxDepth: 15, MaxNodes: 65_536, MaxDataBytes: 16 << 20,
		MaxValueBytes: 16 << 20, MaxDynamicBytes: 4 << 20, MaxArrayElements: 4096,
	}
}

// ValidateConstructor uses the same pre-allocation budget and exact encoding
// check for verification, enrichment and read-time projections.
func ValidateConstructor(document, arguments []byte) error {
	parsed, err := abibounds.Parse(document)
	if err != nil {
		return ErrInvalidABI
	}
	if parsed.Constructor.Inputs == nil && len(arguments) > 0 {
		return ErrMissingConstructor
	}
	return validateCanonicalArguments(parsed.Constructor.Inputs, arguments, abibounds.DefaultLimits())
}

func DecodeVerifiedFunction(entry json.RawMessage, calldata []byte) (string, bool) {
	if len(entry) == 0 || len(calldata) < 4 {
		return "", false
	}
	limits := verifiedFunctionLimits()
	parsed, err := abibounds.ParseEntry(entry, limits)
	if err != nil || len(parsed.Methods) != 1 {
		return "", false
	}
	method, err := parsed.MethodById(calldata[:4])
	if err != nil {
		return "", false
	}
	if err := validateCanonicalArguments(method.Inputs, calldata[4:], limits); err != nil {
		return "", false
	}
	return method.Sig, true
}

func validateCanonicalArguments(arguments gethabi.Arguments, data []byte, limits abibounds.Limits) error {
	if err := abibounds.CheckValuesWithLimits(arguments, data, limits); err != nil {
		return ErrArgumentLimit
	}
	values, err := arguments.Unpack(data)
	if err != nil {
		return ErrInvalidArguments
	}
	reencoded, err := arguments.Pack(values...)
	if err != nil || !bytes.Equal(reencoded, data) {
		return ErrNonCanonical
	}
	return nil
}
