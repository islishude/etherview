package defi

import (
	"context"
	"encoding/json"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/deficontract"
)

type Instrument struct {
	Address, Pool common.Address
	Kind          string
	FailureCode   string
}

var transferTopic = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))

// Instruments enumerates at most twenty factory or registry entries per step.
func Instruments(ctx context.Context, s *State, d Deployment, offset uint64) ([]Instrument, uint64, bool, error) {
	items := []Instrument{}
	switch d.Protocol {
	case deficontract.UniswapV3, deficontract.UniswapV4:
		return []Instrument{{Address: d.Address("position_manager"), Kind: "liquidity-nft"}}, 1, true, nil
	case deficontract.Lido:
		return []Instrument{{Address: d.Address("steth"), Kind: "staking"}, {Address: d.Address("wsteth"), Kind: "staking"}, {Address: d.Address("withdrawal_queue"), Kind: "withdrawal"}}, 3, true, nil
	case deficontract.AaveV3:
		raw, e := s.call(ctx, d.Address("pool"), "getReservesList()")
		if e != nil {
			return nil, offset, false, e
		}
		if len(raw) < 64 {
			return nil, offset, false, deficontract.ErrIntegrity
		}
		ptr, _ := word(raw, 0)
		count, _ := word(raw, 1)
		if ptr.Cmp(big.NewInt(32)) != 0 || !count.IsUint64() || count.Uint64() > 1024 || len(raw) != 64+32*int(count.Uint64()) {
			return nil, offset, false, deficontract.ErrIntegrity
		}
		end := min(offset+5, count.Uint64())
		for i := offset; i < end; i++ {
			if !hasStepBudget(ctx) {
				if i == offset {
					return nil, offset, false, deficontract.ErrUnavailable
				}
				end = i
				break
			}
			reserve, e := addressWord(raw, int(i)+2)
			if e != nil {
				return nil, offset, false, e
			}
			tokens, e := s.call(ctx, d.Address("data_provider"), "getReserveTokensAddresses(address)", addrWord(reserve))
			if e != nil {
				return nil, offset, false, e
			}
			if len(tokens) != 96 {
				return nil, offset, false, deficontract.ErrIntegrity
			}
			for j := range 3 {
				a, e := addressWord(tokens, j)
				if e != nil {
					return nil, offset, false, e
				}
				if a != (common.Address{}) {
					item := Instrument{Address: a, Pool: d.Address("pool"), Kind: "lending"}
					if err := s.verifyReceipt(ctx, d, a); err != nil {
						item.FailureCode = failureCode(err)
					}
					items = append(items, item)
				}
			}
		}
		return items, end, end >= count.Uint64(), nil
	case deficontract.UniswapV2, deficontract.Curve:
		root := d.Address("factory")
		countSig, itemSig := "allPairsLength()", "allPairs(uint256)"
		if d.Protocol == deficontract.Curve {
			root = d.Address("metaregistry")
			countSig, itemSig = "pool_count()", "pool_list(uint256)"
		}
		count, e := s.uint(ctx, root, countSig)
		if e != nil {
			return nil, offset, false, e
		}
		if !count.IsUint64() || count.Uint64() > 10_000_000 {
			return nil, offset, false, deficontract.ErrUnsupported
		}
		end := min(offset+20, count.Uint64())
		for i := offset; i < end; i++ {
			if !hasStepBudget(ctx) {
				if i == offset {
					return nil, offset, false, deficontract.ErrUnavailable
				}
				end = i
				break
			}
			pool, e := s.address(ctx, root, itemSig, uintWord(new(big.Int).SetUint64(i)))
			if e != nil {
				return nil, offset, false, e
			}
			token := pool
			if d.Protocol == deficontract.Curve {
				token, e = s.address(ctx, root, "get_lp_token(address)", addrWord(pool))
				if e != nil {
					return nil, offset, false, e
				}
			}
			if token == (common.Address{}) || pool == (common.Address{}) {
				return nil, offset, false, deficontract.ErrIntegrity
			}
			item := Instrument{Address: token, Pool: pool, Kind: "liquidity"}
			if d.Protocol == deficontract.Curve {
				if err := s.verifyCurvePool(ctx, d, pool); err != nil {
					item.FailureCode = failureCode(err)
				}
			}
			items = append(items, item)
		}
		return items, end, end >= count.Uint64(), nil
	}
	return nil, offset, false, deficontract.ErrUnsupported
}
func Candidates(raw []byte, kind string, pool common.Address) ([]Candidate, *types.Log, error) {
	var log types.Log
	if json.Unmarshal(raw, &log) != nil || log.Removed || log.Address == (common.Address{}) || len(log.Topics) < 3 || log.Topics[0] != transferTopic {
		return nil, nil, deficontract.ErrIntegrity
	}
	nft := kind == "liquidity-nft" || kind == "withdrawal"
	if nft && (len(log.Topics) != 4 || len(log.Data) != 0) || !nft && (len(log.Topics) != 3 || len(log.Data) != 32) {
		return nil, nil, deficontract.ErrIntegrity
	}
	id := ""
	if nft {
		id = new(big.Int).SetBytes(log.Topics[3][:]).String()
	}
	result := []Candidate{}
	for _, topic := range log.Topics[1:3] {
		v := new(big.Int).SetBytes(topic[:])
		if v.BitLen() > 160 {
			return nil, nil, deficontract.ErrIntegrity
		}
		a := common.BigToAddress(v)
		if a != (common.Address{}) {
			result = append(result, Candidate{Contract: log.Address, Owner: a, TokenID: id, Pool: pool, Kind: kind})
		}
	}
	return result, &log, nil
}
