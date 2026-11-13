package defi

import (
	"context"
	"math/big"
	"slices"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/deficontract"
)

func observeCurve(ctx context.Context, s *State, d Deployment, c Candidate, p *deficontract.Position) error {
	registry := d.Address("metaregistry")
	pool, e := s.address(ctx, registry, "get_pool_from_lp_token(address)", addrWord(c.Contract))
	if e != nil {
		return e
	}
	if pool == (common.Address{}) {
		return deficontract.ErrUnsupported
	}
	lp, e := s.address(ctx, registry, "get_lp_token(address)", addrWord(pool))
	if e != nil {
		return e
	}
	if lp != c.Contract {
		return deficontract.ErrUnsupported
	}
	amount, e := s.uint(ctx, lp, "balanceOf(address)", addrWord(c.Owner))
	if e != nil {
		return e
	}
	p.ReceiptAmount = amount.String()
	p.Pool = pool.Hex()
	if amount.Sign() == 0 {
		p.State = "closed"
		return nil
	}
	assets, e := curveAssets(ctx, s, d, pool, amount, map[common.Address]bool{}, 0)
	if e != nil {
		return e
	}
	p.Assets = assets
	p.Limitations = append(p.Limitations, "reserve_share_not_redemption_quote", "fees_included_in_reserves")
	return nil
}
func curveAssets(ctx context.Context, s *State, d Deployment, pool common.Address, amount *big.Int, visited map[common.Address]bool, depth int) ([]deficontract.Asset, error) {
	if depth >= 4 || visited[pool] {
		return nil, deficontract.ErrUnsupported
	}
	visited[pool] = true
	defer delete(visited, pool)
	if e := s.verifyCurvePool(ctx, d, pool); e != nil {
		return nil, e
	}

	registry := d.Address("metaregistry")
	lp, e := s.address(ctx, registry, "get_lp_token(address)", addrWord(pool))
	if e != nil {
		return nil, e
	}
	total, e := s.uint(ctx, lp, "totalSupply()")
	if e != nil {
		return nil, e
	}
	coins, e := s.call(ctx, registry, "get_coins(address)", addrWord(pool))
	if e != nil {
		return nil, e
	}
	balances, e := s.call(ctx, registry, "get_balances(address)", addrWord(pool))
	if e != nil {
		return nil, e
	}
	if len(coins) != 8*32 || len(balances) != 8*32 {
		return nil, deficontract.ErrUnsupported
	}
	base, e := s.address(ctx, registry, "get_base_pool(address)", addrWord(pool))
	if e != nil {
		return nil, e
	}
	baseLP := common.Address{}
	if base != (common.Address{}) {
		baseLP, e = s.address(ctx, registry, "get_lp_token(address)", addrWord(base))
		if e != nil {
			return nil, e
		}
	}
	assets := []deficontract.Asset{}
	expanded := false
	seen := map[common.Address]bool{}
	for i := range 8 {
		a, e := addressWord(coins, i)
		if e != nil {
			return nil, e
		}
		if a == (common.Address{}) {
			continue
		}
		if seen[a] {
			return nil, deficontract.ErrIntegrity
		}
		seen[a] = true
		reserve, _ := word(balances, i)
		share, e := Share(amount, reserve, total)
		if e != nil {
			return nil, e
		}
		if a == baseLP {
			inner, e := curveAssets(ctx, s, d, base, share, visited, depth+1)
			if e != nil {
				return nil, e
			}
			assets = append(assets, inner...)
			expanded = true
		} else {
			if a == common.HexToAddress("0xEeeeeEeeeEeEeeEeEeEeeEEEeeeeEeeeeeeeEEeE") {
				a = common.Address{}
			}
			assets = append(assets, s.asset(ctx, a, share))
		}
	}
	if len(assets) == 0 || (base != (common.Address{}) && !expanded) {
		return nil, deficontract.ErrIntegrity
	}
	return assets, nil
}

func (s *State) verifyCurvePool(ctx context.Context, d Deployment, pool common.Address) error {
	// Registry membership alone does not identify pool accounting semantics.
	var code string
	if s.RPC.CallContext(ctx, &code, "eth_getCode", pool, rpc.BlockNumberOrHashWithHash(s.Hash, true)) != nil {
		return deficontract.ErrUnavailable
	}
	if len(code) > 2+49152*2 {
		return deficontract.ErrIntegrity
	}
	b, e := hexutil.Decode(code)
	if e != nil {
		return deficontract.ErrIntegrity
	}
	if !slices.Contains(d.PoolCodeHashes, crypto.Keccak256Hash(b)) {
		return deficontract.ErrUnsupported
	}
	return nil
}
