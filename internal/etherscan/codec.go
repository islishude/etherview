package etherscan

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/ethrpc"
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

func checksumAddress(address common.Address) (string, error) {
	return address.Hex(), nil
}

func parseAddressParameter(raw, name string) (common.Address, []byte, error) {
	address, err := ethrpc.ParseAddress(strings.TrimSpace(raw))
	if err != nil {
		return common.Address{}, nil, invalidParameter("%s is not a valid address", name)
	}
	return address, address.Bytes(), nil
}

func parseHashParameter(raw, name string) (common.Hash, []byte, error) {
	hash, err := ethrpc.ParseHash(strings.TrimSpace(raw))
	if err != nil {
		return common.Hash{}, nil, invalidParameter("%s is not a valid hash", name)
	}
	return hash, hash.Bytes(), nil
}

func hashFromBytes(raw []byte) (common.Hash, error) {
	if len(raw) != common.HashLength {
		return common.Hash{}, fmt.Errorf("database hash has %d bytes, expected 32", len(raw))
	}
	return common.BytesToHash(raw), nil
}

func addressFromBytes(raw []byte) (common.Address, error) {
	if len(raw) != common.AddressLength {
		return common.Address{}, fmt.Errorf("database address has %d bytes, expected 20", len(raw))
	}
	return common.BytesToAddress(raw), nil
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
