// Package abicontract owns ABI provenance and confidence vocabulary shared by
// workers and public readers.
package abicontract

import "errors"

type Confidence string

const (
	ConfidenceVerified Confidence = "verified"
	ConfidenceHigh     Confidence = "high"
	ConfidenceInferred Confidence = "inferred"
	ConfidenceGuess    Confidence = "guess"
)

func ConfidenceRank(value Confidence) int {
	switch value {
	case ConfidenceVerified:
		return 4
	case ConfidenceHigh:
		return 3
	case ConfidenceInferred:
		return 2
	case ConfidenceGuess:
		return 1
	default:
		return 0
	}
}

type Source string

const (
	SourceVerified            Source = "verified"
	SourceCodeHash            Source = "code_hash"
	SourceProxyImplementation Source = "proxy_implementation"
	SourceDiamondFacet        Source = "diamond_facet"
	SourceSignatureDatabase   Source = "signature_database"
	SourceBuiltin             Source = "builtin"
)

func (source Source) Confidence() Confidence {
	switch source {
	case SourceVerified:
		return ConfidenceVerified
	case SourceCodeHash, SourceProxyImplementation, SourceDiamondFacet, SourceBuiltin:
		return ConfidenceHigh
	case SourceSignatureDatabase:
		return ConfidenceGuess
	default:
		return ""
	}
}

func (source Source) Persistent() bool {
	return source == SourceVerified || source == SourceCodeHash ||
		source == SourceProxyImplementation || source == SourceDiamondFacet ||
		source == SourceSignatureDatabase
}

func (source Source) Validate() error {
	if source.Confidence() == "" {
		return errors.New("ABI source is invalid")
	}
	return nil
}
