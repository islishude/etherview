package catalog

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/crypto/sha3"
)

const (
	cursorVersion        = 1
	maximumCursorBytes   = 4096
	maximumDecimalDigits = 78
)

var maximumUint256 = func() *big.Int {
	maximum := new(big.Int).Lsh(big.NewInt(1), 256)
	return maximum.Sub(maximum, big.NewInt(1))
}()

type tokenListCursor struct {
	Version        int    `json:"v"`
	ChainID        string `json:"chain_id"`
	SnapshotNumber string `json:"snapshot_number"`
	SnapshotHash   string `json:"snapshot_hash"`
	AfterAddress   string `json:"after_address"`
}

type tokenEventCursor struct {
	Version        int    `json:"v"`
	ChainID        string `json:"chain_id"`
	TokenAddress   string `json:"token_address"`
	SnapshotNumber string `json:"snapshot_number"`
	SnapshotHash   string `json:"snapshot_hash"`
	BlockNumber    string `json:"block_number"`
	BlockHash      string `json:"block_hash"`
	LogIndex       string `json:"log_index"`
	SubIndex       string `json:"sub_index"`
}

type nftBalanceCursor struct {
	Version        int    `json:"v"`
	ChainID        string `json:"chain_id"`
	Owner          string `json:"owner"`
	SnapshotNumber string `json:"snapshot_number"`
	SnapshotHash   string `json:"snapshot_hash"`
	TokenAddress   string `json:"token_address"`
	TokenID        string `json:"token_id"`
}

func encodeCursor(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode catalog cursor: %w", err)
	}
	if len(encoded) > maximumCursorBytes {
		return "", ErrInvalidCursor
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeCursor(encoded string, target any) error {
	if encoded == "" || len(encoded) > base64.RawURLEncoding.EncodedLen(maximumCursorBytes) {
		return ErrInvalidCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) == 0 || len(raw) > maximumCursorBytes {
		return ErrInvalidCursor
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrInvalidCursor
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrInvalidCursor
	}
	return nil
}

func decodeFixedHex(value string, size int) ([]byte, error) {
	if len(value) != 2+size*2 || !strings.HasPrefix(value, "0x") {
		return nil, ErrInvalidInput
	}
	decoded, err := hex.DecodeString(value[2:])
	if err != nil {
		return nil, ErrInvalidInput
	}
	return decoded, nil
}

func lowerHex(value []byte, size int) (string, error) {
	if len(value) != size {
		return "", ErrCorruptData
	}
	return "0x" + hex.EncodeToString(value), nil
}

func checksumAddressBytes(address []byte) (string, error) {
	if len(address) != 20 {
		return "", ErrCorruptData
	}
	lower := hex.EncodeToString(address)
	digest := sha3.NewLegacyKeccak256()
	_, _ = digest.Write([]byte(lower))
	hashed := digest.Sum(nil)
	result := []byte(lower)
	for index, character := range result {
		nibble := hashed[index/2]
		if index%2 == 0 {
			nibble >>= 4
		} else {
			nibble &= 0x0f
		}
		if character >= 'a' && character <= 'f' && nibble >= 8 {
			result[index] -= 'a' - 'A'
		}
	}
	return "0x" + string(result), nil
}

func checksumInputAddress(value string) ([]byte, string, error) {
	decoded, err := decodeFixedHex(value, 20)
	if err != nil {
		return nil, "", err
	}
	checksummed, err := checksumAddressBytes(decoded)
	if err != nil {
		return nil, "", err
	}
	return decoded, checksummed, nil
}

func optionalChecksumAddress(value []byte) (*string, error) {
	if value == nil {
		return nil, nil
	}
	checksummed, err := checksumAddressBytes(value)
	if err != nil {
		return nil, err
	}
	return &checksummed, nil
}

func canonicalUint256(value string) bool {
	if !canonicalDecimal(value, maximumDecimalDigits) {
		return false
	}
	parsed := new(big.Int)
	if _, ok := parsed.SetString(value, 10); !ok {
		return false
	}
	return parsed.Cmp(maximumUint256) <= 0
}

func canonicalDecimal(value string, maximumDigits int) bool {
	if value == "" || len(value) > maximumDigits || len(value) > 1 && value[0] == '0' {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func decimalRangeSize(from, to string, maximum int) (int, error) {
	if !canonicalUint256(from) || !canonicalUint256(to) {
		return 0, ErrInvalidInput
	}
	left, right := new(big.Int), new(big.Int)
	left.SetString(from, 10)
	right.SetString(to, 10)
	if right.Cmp(left) < 0 {
		return 0, ErrInvalidInput
	}
	difference := new(big.Int).Sub(right, left)
	difference.Add(difference, big.NewInt(1))
	if !difference.IsInt64() || difference.Int64() > int64(maximum) {
		return 0, ErrLimitExceeded
	}
	return int(difference.Int64()), nil
}

func canonicalInt64(value string) bool {
	if !canonicalDecimal(value, 19) {
		return false
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	return err == nil && parsed >= 0
}

func canonicalInt32(value string) bool {
	if !canonicalDecimal(value, 10) {
		return false
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	return err == nil && parsed >= 0
}

func parseTracePath(value string) ([]uint32, error) {
	if value == "" {
		return []uint32{}, nil
	}
	parts := strings.Split(value, ".")
	if len(parts) > 128 {
		return nil, ErrCorruptData
	}
	path := make([]uint32, len(parts))
	for index, part := range parts {
		if !canonicalDecimal(part, 10) {
			return nil, ErrCorruptData
		}
		parsed, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return nil, ErrCorruptData
		}
		path[index] = uint32(parsed)
	}
	return path, nil
}

func compareTracePaths(left, right []uint32) int {
	return slices.Compare(left, right)
}
