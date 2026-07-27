// Package enrich contains asynchronous, block-scoped enrichment primitives.
// It deliberately has no dependency on the core indexer so slow or unavailable
// optional capabilities cannot delay the core checkpoint.
package enrich

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func ParseAddress(value string) (common.Address, error) {
	decoded, err := decodeFixedHex(value, common.AddressLength)
	if err != nil {
		return common.Address{}, fmt.Errorf("parse address: %w", err)
	}
	return common.BytesToAddress(decoded), nil
}

func ParseWord(value string) (common.Hash, error) {
	decoded, err := decodeFixedHex(value, common.HashLength)
	if err != nil {
		return common.Hash{}, fmt.Errorf("parse word: %w", err)
	}
	return common.BytesToHash(decoded), nil
}

func WordFromBytes(value []byte) (common.Hash, error) {
	if len(value) != common.HashLength {
		return common.Hash{}, fmt.Errorf("word must be 32 bytes, got %d", len(value))
	}
	return common.BytesToHash(value), nil
}

// AddressFromWord decodes the ABI/storage representation of an address and
// rejects non-zero high bytes rather than silently truncating them.
func AddressFromWord(word common.Hash) (common.Address, error) {
	for _, value := range word[:12] {
		if value != 0 {
			return common.Address{}, errors.New("address word has non-zero high bytes")
		}
	}
	return common.BytesToAddress(word[12:]), nil
}

func decodeFixedHex(value string, size int) ([]byte, error) {
	if !strings.HasPrefix(value, "0x") {
		return nil, errors.New("value must start with 0x")
	}
	if len(value) != 2+size*2 {
		return nil, fmt.Errorf("value must be %d bytes", size)
	}
	decoded, err := hex.DecodeString(value[2:])
	if err != nil {
		return nil, fmt.Errorf("invalid hex: %w", err)
	}
	return decoded, nil
}

func decodeDataHex(value string) ([]byte, error) {
	if !strings.HasPrefix(value, "0x") {
		return nil, errors.New("data must start with 0x")
	}
	if len(value)%2 != 0 {
		return nil, errors.New("data must contain an even number of hex digits")
	}
	decoded, err := hex.DecodeString(value[2:])
	if err != nil {
		return nil, fmt.Errorf("invalid data hex: %w", err)
	}
	return decoded, nil
}

func signatureHash(signature string) common.Hash {
	return crypto.Keccak256Hash([]byte(signature))
}

// SignatureHash returns the Ethereum Keccak-256 hash used for event topics and
// error identifiers.
func SignatureHash(signature string) common.Hash { return signatureHash(signature) }

// SignatureSelector returns the first four bytes of an Ethereum ABI signature.
func SignatureSelector(signature string) [4]byte {
	hash := signatureHash(signature)
	return [4]byte(hash[:4])
}

type Confidence string

const (
	// ConfidenceVerified is reserved for ABI material verified against deployed
	// bytecode. Callers cannot obtain it from signature-database registration.
	ConfidenceVerified Confidence = "verified"
	ConfidenceHigh     Confidence = "high"
	ConfidenceInferred Confidence = "inferred"
	ConfidenceGuess    Confidence = "guess"
)

func confidenceRank(value Confidence) int {
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
