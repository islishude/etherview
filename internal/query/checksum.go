package query

import (
	"github.com/islishude/etherview/internal/ethrpc"
)

// ChecksumAddress returns the EIP-55 mixed-case form of an Ethereum address.
func ChecksumAddress(value string) (string, error) {
	address, err := ethrpc.ParseAddress(value)
	if err != nil {
		return "", err
	}
	return address.Hex(), nil
}
