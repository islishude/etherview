// Package enrich contains asynchronous, block-scoped enrichment primitives.
// It deliberately has no dependency on the core indexer so slow or unavailable
// optional capabilities cannot delay the core checkpoint.
package enrich

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/sha3"
)

// Address and Word are fixed-width EVM values. Fixed arrays make it impossible
// to accidentally accept a short topic or storage value at package boundaries.
type Address [20]byte
type Word [32]byte

func ParseAddress(value string) (Address, error) {
	var address Address
	decoded, err := decodeFixedHex(value, len(address))
	if err != nil {
		return address, fmt.Errorf("parse address: %w", err)
	}
	copy(address[:], decoded)
	return address, nil
}

func ParseWord(value string) (Word, error) {
	var word Word
	decoded, err := decodeFixedHex(value, len(word))
	if err != nil {
		return word, fmt.Errorf("parse word: %w", err)
	}
	copy(word[:], decoded)
	return word, nil
}

func WordFromBytes(value []byte) (Word, error) {
	var word Word
	if len(value) != len(word) {
		return word, fmt.Errorf("word must be 32 bytes, got %d", len(value))
	}
	copy(word[:], value)
	return word, nil
}

func (a Address) String() string { return "0x" + hex.EncodeToString(a[:]) }
func (w Word) String() string    { return "0x" + hex.EncodeToString(w[:]) }
func (w Word) IsZero() bool      { return w == Word{} }

// AddressFromWord decodes the ABI/storage representation of an address and
// rejects non-zero high bytes rather than silently truncating them.
func AddressFromWord(word Word) (Address, error) {
	var address Address
	for _, value := range word[:12] {
		if value != 0 {
			return address, errors.New("address word has non-zero high bytes")
		}
	}
	copy(address[:], word[12:])
	return address, nil
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

func signatureHash(signature string) Word {
	hasher := sha3.NewLegacyKeccak256()
	_, _ = hasher.Write([]byte(signature))
	var result Word
	hasher.Sum(result[:0])
	return result
}

// SignatureHash returns the Ethereum Keccak-256 hash used for event topics and
// error identifiers.
func SignatureHash(signature string) Word { return signatureHash(signature) }

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
