package mempool

import (
	"encoding/hex"
	"errors"

	"github.com/islishude/etherview/internal/ethrpc"
	"golang.org/x/crypto/sha3"
)

func checksumAddress(address ethrpc.Address) (string, error) {
	decoded, err := address.Bytes()
	if err != nil || len(decoded) != 20 {
		return "", errors.New("invalid address")
	}
	lower := hex.EncodeToString(decoded)
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
