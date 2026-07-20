package etherscan

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/islishude/etherview/internal/ethrpc"
	"golang.org/x/crypto/sha3"
)

func decodeRawObject(raw []byte, destination any) error {
	if len(raw) == 0 || destination == nil {
		return errors.New("raw JSON and destination are required")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("raw JSON contains multiple values")
		}
		return err
	}
	return nil
}

func decimalQuantity(quantity ethrpc.Quantity) (string, error) {
	value, err := quantity.Big()
	if err != nil {
		return "", err
	}
	return value.String(), nil
}

func checksumAddress(address ethrpc.Address) (string, error) {
	lower := strings.ToLower(address.String()[2:])
	hasher := sha3.NewLegacyKeccak256()
	if _, err := hasher.Write([]byte(lower)); err != nil {
		return "", fmt.Errorf("hash address: %w", err)
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	checksummed := []byte(lower)
	for index := range checksummed {
		if checksummed[index] >= 'a' && checksummed[index] <= 'f' && digest[index] >= '8' {
			checksummed[index] -= 'a' - 'A'
		}
	}
	return "0x" + string(checksummed), nil
}

func parseAddressParameter(raw, name string) (ethrpc.Address, []byte, error) {
	address, err := ethrpc.ParseAddress(strings.TrimSpace(raw))
	if err != nil {
		return "", nil, invalidParameter("%s is not a valid address", name)
	}
	bytes, err := address.Bytes()
	if err != nil {
		return "", nil, invalidParameter("%s is not a valid address", name)
	}
	return address, bytes, nil
}

func parseHashParameter(raw, name string) (ethrpc.Hash, []byte, error) {
	hash, err := ethrpc.ParseHash(strings.TrimSpace(raw))
	if err != nil {
		return "", nil, invalidParameter("%s is not a valid hash", name)
	}
	bytes, err := hash.Bytes()
	if err != nil {
		return "", nil, invalidParameter("%s is not a valid hash", name)
	}
	return hash, bytes, nil
}

func hashFromBytes(raw []byte) (ethrpc.Hash, error) {
	if len(raw) != 32 {
		return "", fmt.Errorf("database hash has %d bytes, expected 32", len(raw))
	}
	return ethrpc.ParseHash("0x" + hex.EncodeToString(raw))
}

func addressFromBytes(raw []byte) (ethrpc.Address, error) {
	if len(raw) != 20 {
		return "", fmt.Errorf("database address has %d bytes, expected 20", len(raw))
	}
	return ethrpc.ParseAddress("0x" + hex.EncodeToString(raw))
}

func compactJSON(raw []byte) (string, error) {
	var value any
	if err := decodeRawObject(raw, &value); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
