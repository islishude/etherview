package ethrpc

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

var (
	ErrInvalidQuantity = errors.New("invalid Ethereum JSON-RPC quantity")
	ErrInvalidData     = errors.New("invalid Ethereum JSON-RPC data")
)

// Quantity is a canonical Ethereum JSON-RPC QUANTITY. It deliberately keeps
// the wire representation so callers never lose precision by passing through
// a float64 or a machine-sized integer.
type Quantity string

func ParseQuantity(value string) (Quantity, error) {
	if len(value) < 3 || !strings.HasPrefix(value, "0x") {
		return "", fmt.Errorf("%w: %q must start with 0x and contain a digit", ErrInvalidQuantity, value)
	}
	digits := value[2:]
	if len(digits) > 1 && digits[0] == '0' {
		return "", fmt.Errorf("%w: %q has a leading zero", ErrInvalidQuantity, value)
	}
	for _, r := range digits {
		if !isLowerHex(r) {
			return "", fmt.Errorf("%w: %q contains a non-lowercase-hex digit", ErrInvalidQuantity, value)
		}
	}
	return Quantity(value), nil
}

func QuantityFromUint64(value uint64) Quantity {
	return Quantity("0x" + strconv.FormatUint(value, 16))
}

func (q Quantity) String() string { return string(q) }

func (q Quantity) Big() (*big.Int, error) {
	if _, err := ParseQuantity(string(q)); err != nil {
		return nil, err
	}
	value, ok := new(big.Int).SetString(string(q)[2:], 16)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrInvalidQuantity, q)
	}
	return value, nil
}

func (q Quantity) Uint64() (uint64, error) {
	value, err := q.Big()
	if err != nil {
		return 0, err
	}
	if !value.IsUint64() {
		return 0, fmt.Errorf("%w: %q exceeds uint64", ErrInvalidQuantity, q)
	}
	return value.Uint64(), nil
}

func (q Quantity) MarshalJSON() ([]byte, error) {
	if _, err := ParseQuantity(string(q)); err != nil {
		return nil, err
	}
	return json.Marshal(string(q))
}

func (q *Quantity) UnmarshalJSON(data []byte) error {
	if q == nil {
		return errors.New("ethrpc.Quantity: UnmarshalJSON on nil receiver")
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("decode quantity: %w", err)
	}
	parsed, err := ParseQuantity(value)
	if err != nil {
		return err
	}
	*q = parsed
	return nil
}

// Data is an arbitrary-length Ethereum JSON-RPC DATA value.
type Data string

func ParseData(value string) (Data, error) {
	if !strings.HasPrefix(value, "0x") || (len(value)-2)%2 != 0 {
		return "", fmt.Errorf("%w: %q must have an even number of digits", ErrInvalidData, value)
	}
	for _, r := range value[2:] {
		if !isHex(r) {
			return "", fmt.Errorf("%w: %q contains a non-hex digit", ErrInvalidData, value)
		}
	}
	return Data(value), nil
}

func DataFromBytes(value []byte) Data { return Data("0x" + hex.EncodeToString(value)) }

func (d Data) String() string { return string(d) }

func (d Data) Bytes() ([]byte, error) {
	if _, err := ParseData(string(d)); err != nil {
		return nil, err
	}
	return hex.DecodeString(string(d)[2:])
}

func (d Data) MarshalJSON() ([]byte, error) {
	if _, err := ParseData(string(d)); err != nil {
		return nil, err
	}
	return json.Marshal(string(d))
}

func (d *Data) UnmarshalJSON(data []byte) error {
	if d == nil {
		return errors.New("ethrpc.Data: UnmarshalJSON on nil receiver")
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("decode data: %w", err)
	}
	parsed, err := ParseData(value)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

type Hash string

func ParseHash(value string) (Hash, error) {
	data, err := ParseData(value)
	if err != nil {
		return "", fmt.Errorf("invalid hash: %w", err)
	}
	if len(data) != 66 {
		return "", fmt.Errorf("invalid hash: expected 32 bytes, got %d", (len(data)-2)/2)
	}
	return Hash(data), nil
}

func (h Hash) String() string { return string(h) }

func (h Hash) Bytes() ([]byte, error) {
	if _, err := ParseHash(string(h)); err != nil {
		return nil, err
	}
	return hex.DecodeString(string(h)[2:])
}

func (h Hash) Equal(other Hash) bool { return strings.EqualFold(string(h), string(other)) }

func (h Hash) MarshalJSON() ([]byte, error) {
	if _, err := ParseHash(string(h)); err != nil {
		return nil, err
	}
	return json.Marshal(string(h))
}

func (h *Hash) UnmarshalJSON(data []byte) error {
	if h == nil {
		return errors.New("ethrpc.Hash: UnmarshalJSON on nil receiver")
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("decode hash: %w", err)
	}
	parsed, err := ParseHash(value)
	if err != nil {
		return err
	}
	*h = parsed
	return nil
}

type Address string

func ParseAddress(value string) (Address, error) {
	data, err := ParseData(value)
	if err != nil {
		return "", fmt.Errorf("invalid address: %w", err)
	}
	if len(data) != 42 {
		return "", fmt.Errorf("invalid address: expected 20 bytes, got %d", (len(data)-2)/2)
	}
	return Address(data), nil
}

func (a Address) String() string { return string(a) }

func (a Address) Bytes() ([]byte, error) {
	if _, err := ParseAddress(string(a)); err != nil {
		return nil, err
	}
	return hex.DecodeString(string(a)[2:])
}

func (a Address) Equal(other Address) bool { return strings.EqualFold(string(a), string(other)) }

func (a Address) MarshalJSON() ([]byte, error) {
	if _, err := ParseAddress(string(a)); err != nil {
		return nil, err
	}
	return json.Marshal(string(a))
}

func (a *Address) UnmarshalJSON(data []byte) error {
	if a == nil {
		return errors.New("ethrpc.Address: UnmarshalJSON on nil receiver")
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("decode address: %w", err)
	}
	parsed, err := ParseAddress(value)
	if err != nil {
		return err
	}
	*a = parsed
	return nil
}

func EqualData(left, right string) bool {
	leftData, leftErr := ParseData(left)
	rightData, rightErr := ParseData(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	leftBytes, _ := leftData.Bytes()
	rightBytes, _ := rightData.Bytes()
	return bytes.Equal(leftBytes, rightBytes)
}

func isLowerHex(r rune) bool { return r >= '0' && r <= '9' || r >= 'a' && r <= 'f' }

func isHex(r rune) bool { return isLowerHex(r) || r >= 'A' && r <= 'F' }
