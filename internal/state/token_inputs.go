package state

import (
	"context"
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/ethrpc"
)

const tokenCandidateBytes = 1 + common.AddressLength + common.HashLength
const standardERC20 catalogStandard = 3

// Each entry contains the token standard, contract address and uint256 ID.
// The owner and block reference are shared by every read in a batch.
func tokenCandidates(candidates []parsedNFTCandidate, indexes []int) []byte {
	data := make([]byte, len(indexes)*tokenCandidateBytes)
	for i, index := range indexes {
		candidate := candidates[index]
		row := data[i*tokenCandidateBytes : (i+1)*tokenCandidateBytes]
		row[0] = byte(candidate.standard)
		copy(row[1:21], candidate.address[:])
		if candidate.tokenID != nil {
			candidate.tokenID.FillBytes(row[21:])
		}
	}
	return data
}

type tokenResult struct {
	Value    hexutil.Bytes
	Reverted bool
}

type tokenRevert struct{}

func (tokenRevert) Error() string  { return "execution reverted" }
func (tokenRevert) ErrorCode() int { return 3 }

func fetchTokenInputs(ctx context.Context, endpoint *ethrpc.Endpoint, ref CanonicalRef, owner common.Address, arg []byte) ([]tokenResult, error) {
	if len(arg) == 0 || len(arg)%tokenCandidateBytes != 0 || len(arg) > tokenCandidateBytes*maxBalanceCandidates {
		return nil, errors.New("invalid token input batch")
	}
	values := make([]tokenResult, len(arg)/tokenCandidateBytes)
	batch := make([]rpc.BatchElem, len(values))
	for i := range values {
		row := arg[i*tokenCandidateBytes : (i+1)*tokenCandidateBytes]
		address := common.BytesToAddress(row[1:21])
		tokenID := new(big.Int).SetBytes(row[21:])
		var data []byte
		switch catalogStandard(row[0]) {
		case standardERC721:
			data = erc721OwnerCallData(tokenID)
		case standardERC1155:
			data = erc1155BalanceCallData(owner, tokenID)
		case standardERC20:
			data = append(append([]byte{}, erc20BalanceOfSelector...), common.LeftPadBytes(owner[:], 32)...)
		default:
			return nil, errors.New("invalid token input standard")
		}
		batch[i] = rpc.BatchElem{Method: "eth_call", Args: []any{fixedNFTCallObject(address, data), canonicalSelector(ref)}, Result: &values[i].Value}
	}
	if err := endpoint.BatchCallContext(ctx, batch); err != nil {
		return nil, stateUnavailable(err)
	}
	for i, elem := range batch {
		if elem.Error != nil && nftExecutionReverted(elem.Error) && catalogStandard(arg[i*tokenCandidateBytes]) == standardERC721 {
			values[i] = tokenResult{Reverted: true}
		} else if elem.Error != nil || len(values[i].Value) != 32 {
			return nil, stateUnavailable(elem.Error)
		}
	}
	return values, nil
}
