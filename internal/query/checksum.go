package query

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/islishude/etherview/internal/ethrpc"
	"golang.org/x/crypto/sha3"
)

// ChecksumAddress returns the EIP-55 mixed-case form of an Ethereum address.
func ChecksumAddress(value string) (string, error) {
	address, err := ethrpc.ParseAddress(value)
	if err != nil {
		return "", err
	}
	lower := strings.ToLower(address.String()[2:])
	hasher := sha3.NewLegacyKeccak256()
	if _, err := hasher.Write([]byte(lower)); err != nil {
		return "", fmt.Errorf("hash address: %w", err)
	}
	digest := hasher.Sum(nil)
	encodedDigest := hex.EncodeToString(digest)
	checksummed := []byte(lower)
	for index := range checksummed {
		if checksummed[index] >= 'a' && checksummed[index] <= 'f' && encodedDigest[index] >= '8' {
			checksummed[index] -= 'a' - 'A'
		}
	}
	return "0x" + string(checksummed), nil
}
