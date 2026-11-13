package defi

import (
	"context"
	"errors"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/deficontract"
)

type Candidate struct {
	Contract common.Address `json:"contract"`
	Owner    common.Address `json:"owner"`
	TokenID  string         `json:"token_id,omitempty"`
	Pool     common.Address `json:"pool,omitempty"`
	Kind     string         `json:"kind"`
}

func (c Candidate) Key() string {
	return strings.ToLower(c.Contract.Hex()) + ":" + c.TokenID + ":" + strings.ToLower(c.Owner.Hex())
}
func Observe(ctx context.Context, s *State, d Deployment, c Candidate, number string) (deficontract.Position, error) {
	p := deficontract.Position{ID: c.Key(), Protocol: d.Protocol, Deployment: d.ID, Owner: c.Owner.Hex(), Contract: c.Contract.Hex(), TokenID: c.TokenID, Kind: c.Kind, State: "available", Assets: []deficontract.Asset{}, Debts: []deficontract.Asset{}, Claimable: []deficontract.Asset{}, UnsettledFees: []deficontract.Asset{}, Limitations: []string{}, BlockNumber: number, BlockHash: s.Hash.Hex()}
	if c.Owner == (common.Address{}) || c.Contract == (common.Address{}) {
		return p, deficontract.ErrInput
	}
	var e error
	switch d.Protocol {
	case deficontract.UniswapV2:
		e = observeV2(ctx, s, d, c, &p)
	case deficontract.UniswapV3:
		e = observeV3(ctx, s, d, c, &p)
	case deficontract.UniswapV4:
		e = observeV4(ctx, s, d, c, &p)
	case deficontract.AaveV3:
		e = observeAave(ctx, s, d, c, &p)
	case deficontract.Curve:
		e = observeCurve(ctx, s, d, c, &p)
	case deficontract.Lido:
		e = observeLido(ctx, s, d, c, &p)
	default:
		e = deficontract.ErrUnsupported
	}
	return p, e
}
func observeV2(ctx context.Context, s *State, d Deployment, c Candidate, p *deficontract.Position) error {
	factory, e := s.address(ctx, c.Contract, "factory()")
	if e != nil {
		return e
	}
	if factory != d.Address("factory") {
		return deficontract.ErrUnsupported
	}
	t0, e := s.address(ctx, c.Contract, "token0()")
	if e != nil {
		return e
	}
	t1, e := s.address(ctx, c.Contract, "token1()")
	if e != nil {
		return e
	}
	pair, e := s.address(ctx, factory, "getPair(address,address)", addrWord(t0), addrWord(t1))
	if e != nil {
		return e
	}
	if pair != c.Contract {
		return deficontract.ErrUnsupported
	}
	balance, e := s.uint(ctx, c.Contract, "balanceOf(address)", addrWord(c.Owner))
	if e != nil {
		return e
	}
	p.ReceiptAmount = balance.String()
	p.Pool = c.Contract.Hex()
	if balance.Sign() == 0 {
		p.State = "closed"
		return nil
	}
	total, e := s.uint(ctx, c.Contract, "totalSupply()")
	if e != nil {
		return e
	}
	raw, e := s.call(ctx, c.Contract, "getReserves()")
	if e != nil {
		return e
	}
	if len(raw) != 96 {
		return deficontract.ErrIntegrity
	}
	for i, a := range []common.Address{t0, t1} {
		reserve, _ := word(raw, i)
		if reserve.BitLen() > 112 {
			return deficontract.ErrIntegrity
		}
		amount, e := Share(balance, reserve, total)
		if e != nil {
			return e
		}
		p.Assets = append(p.Assets, s.asset(ctx, a, amount))
	}
	p.Limitations = append(p.Limitations, "reserve_share_not_redemption_quote", "fees_included_in_reserves")
	return nil
}
func tokenID(text string) (*big.Int, error) {
	v, ok := new(big.Int).SetString(text, 10)
	if !ok || !uintBits(v, 256) || v.String() != text {
		return nil, deficontract.ErrInput
	}
	return v, nil
}
func nftOwner(ctx context.Context, s *State, c Candidate, expected common.Address) (bool, error) {
	if c.Contract != expected {
		return false, deficontract.ErrUnsupported
	}
	id, e := tokenID(c.TokenID)
	if e != nil {
		return false, e
	}
	owner, e := s.address(ctx, c.Contract, "ownerOf(uint256)", uintWord(id))
	if errors.Is(e, errReverted) {
		return false, nil
	}
	return owner == c.Owner, e
}
func observeAave(ctx context.Context, s *State, d Deployment, c Candidate, p *deficontract.Position) error {
	if e := s.verifyReceipt(ctx, d, c.Contract); e != nil {
		return e
	}

	underlying, e := s.address(ctx, c.Contract, "UNDERLYING_ASSET_ADDRESS()")
	if e != nil {
		return e
	}
	pool, e := s.address(ctx, c.Contract, "POOL()")
	if e != nil {
		return e
	}
	if pool != d.Address("pool") {
		return deficontract.ErrUnsupported
	}
	tokens, e := s.call(ctx, d.Address("data_provider"), "getReserveTokensAddresses(address)", addrWord(underlying))
	if e != nil {
		return e
	}
	if len(tokens) != 96 {
		return deficontract.ErrIntegrity
	}
	a, e := addressWord(tokens, 0)
	if e != nil {
		return e
	}
	stable, e := addressWord(tokens, 1)
	if e != nil {
		return e
	}
	variable, e := addressWord(tokens, 2)
	if e != nil {
		return e
	}
	if c.Contract != a && c.Contract != stable && c.Contract != variable {
		return deficontract.ErrUnsupported
	}
	amount, e := s.uint(ctx, c.Contract, "balanceOf(address)", addrWord(c.Owner))
	if e != nil {
		return e
	}
	p.ReceiptAmount = amount.String()
	p.Pool = pool.Hex()
	if amount.Sign() == 0 {
		p.State = "closed"
		return nil
	}
	if c.Contract == a {
		p.Kind = "supply"
		p.Assets = append(p.Assets, s.asset(ctx, underlying, amount))
		raw, e := s.call(ctx, d.Address("data_provider"), "getUserReserveData(address,address)", addrWord(underlying), addrWord(c.Owner))
		if e != nil {
			return e
		}
		if len(raw) != 9*32 {
			return deficontract.ErrUnsupported
		}
		enabled, _ := word(raw, 8)
		if enabled.BitLen() > 1 {
			return deficontract.ErrIntegrity
		}
		flag := enabled.Sign() != 0
		p.Collateral = &flag
	} else {
		p.Kind = "borrow"
		p.Debts = append(p.Debts, s.asset(ctx, underlying, amount))
	}
	return nil
}
func observeLido(ctx context.Context, s *State, d Deployment, c Candidate, p *deficontract.Position) error {
	steth, wsteth, queue := d.Address("steth"), d.Address("wsteth"), d.Address("withdrawal_queue")
	switch c.Contract {
	case steth, wsteth:
		amount, e := s.uint(ctx, c.Contract, "balanceOf(address)", addrWord(c.Owner))
		if e != nil {
			return e
		}
		p.ReceiptAmount = amount.String()
		p.Kind = "staking"
		if amount.Sign() == 0 {
			p.State = "closed"
			return nil
		}
		if c.Contract == wsteth {
			amount, e = s.uint(ctx, wsteth, "getStETHByWstETH(uint256)", uintWord(amount))
			if e != nil {
				return e
			}
		}
		p.Assets = append(p.Assets, s.asset(ctx, steth, amount))
		return nil
	case queue:
		id, e := tokenID(c.TokenID)
		if e != nil {
			return e
		}
		p.Kind = "withdrawal"
		// A one-element ABI uint256[]: offset, length, element.
		raw, e := s.call(ctx, queue, "getWithdrawalStatus(uint256[])", uintWord(big.NewInt(32)), uintWord(big.NewInt(1)), uintWord(id))
		if e != nil {
			return e
		}
		if len(raw) != 8*32 {
			return deficontract.ErrIntegrity
		}
		offset, _ := word(raw, 0)
		count, _ := word(raw, 1)
		if offset.Cmp(big.NewInt(32)) != 0 || count.Cmp(big.NewInt(1)) != 0 {
			return deficontract.ErrIntegrity
		}
		amount, _ := word(raw, 2)
		owner, e := addressWord(raw, 4)
		if e != nil {
			return e
		}
		finalized, _ := word(raw, 6)
		claimed, _ := word(raw, 7)
		if finalized.BitLen() > 1 || claimed.BitLen() > 1 {
			return deficontract.ErrIntegrity
		}
		if owner != c.Owner || claimed.Sign() != 0 {
			p.State = "closed"
			return nil
		}
		p.ReceiptAmount = amount.String()
		if finalized.Sign() == 0 {
			p.State = "pending"
			p.Assets = append(p.Assets, s.asset(ctx, steth, amount))
			p.Limitations = append(p.Limitations, "withdrawal_request_not_guaranteed_eth")
			return nil
		}
		last, e := s.uint(ctx, queue, "getLastCheckpointIndex()")
		if e != nil {
			return e
		}
		hints, e := s.call(ctx, queue, "findCheckpointHints(uint256[],uint256,uint256)", uintWord(big.NewInt(96)), uintWord(big.NewInt(1)), uintWord(last), uintWord(big.NewInt(1)), uintWord(id))
		if e != nil {
			return e
		}
		if !singleUintArray(hints) {
			return deficontract.ErrIntegrity
		}
		hint, _ := word(hints, 2)
		result, e := s.call(ctx, queue, "getClaimableEther(uint256[],uint256[])", uintWord(big.NewInt(64)), uintWord(big.NewInt(128)), uintWord(big.NewInt(1)), uintWord(id), uintWord(big.NewInt(1)), uintWord(hint))
		if e != nil {
			return e
		}
		if !singleUintArray(result) {
			return deficontract.ErrIntegrity
		}
		claim, _ := word(result, 2)
		p.State = "claimable"
		p.Claimable = append(p.Claimable, s.asset(ctx, common.Address{}, claim))
		return nil
	}
	return deficontract.ErrUnsupported
}

func singleUintArray(raw []byte) bool {
	if len(raw) != 96 {
		return false
	}
	offset, _ := word(raw, 0)
	count, _ := word(raw, 1)
	return offset.Cmp(big.NewInt(32)) == 0 && count.Cmp(big.NewInt(1)) == 0
}

func (s *State) verifyReceipt(ctx context.Context, d Deployment, address common.Address) error {
	// Pool registry membership does not authenticate an independently upgradeable receipt implementation.
	receipt := Contract{}
	for _, known := range d.ReceiptContracts {
		if known.Address == address {
			receipt = known
			break
		}
	}
	if receipt.Address == (common.Address{}) {
		return deficontract.ErrUnsupported
	}
	return s.Verify(ctx, Deployment{Contracts: []Contract{receipt}})

}
