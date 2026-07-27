package ethrpc

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

var (
	ErrInvalidQuantity = errors.New("invalid Ethereum JSON-RPC quantity")
	ErrInvalidData     = errors.New("invalid Ethereum JSON-RPC data")
)

// ParseQuantity retains Etherview's lowercase canonical wire check while
// returning the standard arbitrary-precision integer used by go-ethereum.
func ParseQuantity(value string) (*big.Int, error) {
	if len(value) < 3 || !strings.HasPrefix(value, "0x") {
		return nil, fmt.Errorf("%w: value must start with 0x and contain a digit", ErrInvalidQuantity)
	}
	digits := value[2:]
	if len(digits) > 1 && digits[0] == '0' {
		return nil, fmt.Errorf("%w: value has a leading zero", ErrInvalidQuantity)
	}
	for _, digit := range digits {
		if !isLowerHex(digit) {
			return nil, fmt.Errorf("%w: value contains a non-lowercase-hex digit", ErrInvalidQuantity)
		}
	}
	decoded, err := hexutil.DecodeBig(value)
	if err != nil || hexutil.EncodeBig(decoded) != value {
		return nil, fmt.Errorf("%w: value is not canonical", ErrInvalidQuantity)
	}
	return decoded, nil
}

func QuantityFromUint64(value uint64) hexutil.Uint64 {
	return hexutil.Uint64(value)
}

// ParseData accepts uppercase hexadecimal digits for compatibility with the
// previous boundary, but requires the exact lowercase 0x prefix and an even
// number of digits.
func ParseData(value string) (hexutil.Bytes, error) {
	if !strings.HasPrefix(value, "0x") || (len(value)-2)%2 != 0 {
		return nil, fmt.Errorf("%w: value must use 0x and an even number of digits", ErrInvalidData)
	}
	for _, digit := range value[2:] {
		if !isHex(digit) {
			return nil, fmt.Errorf("%w: value contains a non-hex digit", ErrInvalidData)
		}
	}
	decoded, err := hexutil.Decode(value)
	if err != nil {
		return nil, fmt.Errorf("%w: value is not canonical data", ErrInvalidData)
	}
	return hexutil.Bytes(decoded), nil
}

func DataFromBytes(value []byte) hexutil.Bytes {
	return hexutil.Bytes(common.CopyBytes(value))
}

func ParseHash(value string) (common.Hash, error) {
	data, err := ParseData(value)
	if err != nil {
		return common.Hash{}, fmt.Errorf("invalid hash: %w", err)
	}
	if len(data) != common.HashLength {
		return common.Hash{}, fmt.Errorf("invalid hash: expected 32 bytes, got %d", len(data))
	}
	var hash common.Hash
	copy(hash[:], data)
	return hash, nil
}

func ParseAddress(value string) (common.Address, error) {
	data, err := ParseData(value)
	if err != nil {
		return common.Address{}, fmt.Errorf("invalid address: %w", err)
	}
	if len(data) != common.AddressLength {
		return common.Address{}, fmt.Errorf("invalid address: expected 20 bytes, got %d", len(data))
	}
	var address common.Address
	copy(address[:], data)
	return address, nil
}

func EqualData(left, right string) bool {
	leftData, leftErr := ParseData(left)
	rightData, rightErr := ParseData(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftData, rightData)
}

func isLowerHex(value rune) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f'
}

func isHex(value rune) bool {
	return isLowerHex(value) || value >= 'A' && value <= 'F'
}
