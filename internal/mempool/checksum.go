package mempool

import (
	"github.com/ethereum/go-ethereum/common"
)

func checksumAddress(address common.Address) string {
	return address.Hex()
}
