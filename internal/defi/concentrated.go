package defi

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/deficontract"
)

func observeV3(ctx context.Context, s *State, d Deployment, c Candidate, p *deficontract.Position) error {
	owned, e := nftOwner(ctx, s, c, d.Address("position_manager"))
	if e != nil {
		return e
	}
	if !owned {
		p.State = "closed"
		return nil
	}
	id, _ := tokenID(c.TokenID)
	raw, e := s.call(ctx, c.Contract, "positions(uint256)", uintWord(id))
	if e != nil {
		return e
	}
	if len(raw) != 12*32 {
		return deficontract.ErrIntegrity
	}
	t0, e := addressWord(raw, 2)
	if e != nil {
		return e
	}
	t1, e := addressWord(raw, 3)
	if e != nil {
		return e
	}
	fee, _ := word(raw, 4)
	if fee.BitLen() > 24 {
		return deficontract.ErrIntegrity
	}
	f := uint32(fee.Uint64())
	p.Fee = &f
	lower, e := signedWord(raw, 5)
	if e != nil {
		return e
	}
	upper, e := signedWord(raw, 6)
	if e != nil {
		return e
	}
	liquidity, _ := word(raw, 7)
	last0, _ := word(raw, 8)
	last1, _ := word(raw, 9)
	owed0, _ := word(raw, 10)
	owed1, _ := word(raw, 11)
	if !uintBits(liquidity, 128) || !uintBits(owed0, 128) || !uintBits(owed1, 128) {
		return deficontract.ErrIntegrity
	}
	factory, e := s.address(ctx, c.Contract, "factory()")
	if e != nil {
		return e
	}
	if factory != d.Address("factory") {
		return deficontract.ErrUnsupported
	}
	pool, e := s.address(ctx, factory, "getPool(address,address,uint24)", addrWord(t0), addrWord(t1), uintWord(fee))
	if e != nil {
		return e
	}
	if pool == (common.Address{}) {
		return deficontract.ErrUnsupported
	}
	p.Pool = pool.Hex()
	state, e := s.call(ctx, pool, "slot0()")
	if e != nil {
		return e
	}
	if len(state) != 7*32 {
		return deficontract.ErrIntegrity
	}
	price, _ := word(state, 0)
	tick, e := signedWord(state, 1)
	if e != nil {
		return e
	}
	if e = setLiquidity(p, liquidity, price, lower, upper, tick, t0, t1); e != nil {
		return e
	}
	lo, e := s.call(ctx, pool, "ticks(int24)", intWord(lower))
	if e != nil {
		return e
	}
	hi, e := s.call(ctx, pool, "ticks(int24)", intWord(upper))
	if e != nil {
		return e
	}
	if len(lo) != 8*32 || len(hi) != 8*32 {
		return deficontract.ErrIntegrity
	}
	for i, a := range []common.Address{t0, t1} {
		sig := "feeGrowthGlobal0X128()"
		if i == 1 {
			sig = "feeGrowthGlobal1X128()"
		}
		global, e := s.uint(ctx, pool, sig)
		if e != nil {
			return e
		}
		below, _ := word(lo, 2+i)
		above, _ := word(hi, 2+i)
		if tick < lower {
			below = modSub(global, below)
		}
		if tick >= upper {
			above = modSub(global, above)
		}
		inside := modSub(modSub(global, below), above)
		last := last0
		owed := owed0
		if i == 1 {
			last = last1
			owed = owed1
		}
		delta, e := feeDelta(inside, last, liquidity)
		if e != nil {
			return e
		}
		p.UnsettledFees = append(p.UnsettledFees, asset(a, delta))
		// Recorded owed may include withdrawn principal. Keep it separate from fees.
		p.Claimable = append(p.Claimable, asset(a, owed))
	}
	p.Limitations = append(p.Limitations, "recorded_claimable_may_include_principal")
	if liquidity.Sign() == 0 && owed0.Sign() == 0 && owed1.Sign() == 0 {
		p.State = "closed"
	}
	return nil
}
func modSub(a, b *big.Int) *big.Int { return new(big.Int).Mod(new(big.Int).Sub(a, b), q256) }
func setLiquidity(p *deficontract.Position, l, price *big.Int, lower, upper, tick int32, t0, t1 common.Address) error {
	a, b, e := LiquidityAmounts(l, price, lower, upper)
	if e != nil {
		return e
	}
	if tick < -887272 || tick > 887272 {
		return deficontract.ErrIntegrity
	}
	p.Liquidity = l.String()
	p.Range = &deficontract.Range{LowerTick: lower, UpperTick: upper, CurrentTick: tick, SqrtPriceX96: price.String(), InRange: tick >= lower && tick < upper}
	p.Assets = append(p.Assets, asset(t0, a), asset(t1, b))
	return nil
}
func observeV4(ctx context.Context, s *State, d Deployment, c Candidate, p *deficontract.Position) error {
	owned, e := nftOwner(ctx, s, c, d.Address("position_manager"))
	if e != nil {
		return e
	}
	if !owned {
		p.State = "closed"
		return nil
	}
	manager, e := s.address(ctx, c.Contract, "poolManager()")
	if e != nil {
		return e
	}
	if manager != d.Address("pool_manager") {
		return deficontract.ErrUnsupported
	}
	viewManager, e := s.address(ctx, d.Address("state_view"), "poolManager()")
	if e != nil {
		return e
	}
	if viewManager != manager {
		return deficontract.ErrUnsupported
	}
	id, _ := tokenID(c.TokenID)
	raw, e := s.call(ctx, c.Contract, "getPoolAndPositionInfo(uint256)", uintWord(id))
	if e != nil {
		return e
	}
	if len(raw) != 6*32 {
		return deficontract.ErrIntegrity
	}
	t0, e := addressWord(raw, 0)
	if e != nil {
		return e
	}
	t1, e := addressWord(raw, 1)
	if e != nil {
		return e
	}
	fee, _ := word(raw, 2)
	if fee.BitLen() > 24 {
		return deficontract.ErrIntegrity
	}
	spacing, e := signedWord(raw, 3)
	if e != nil || spacing <= 0 {
		return deficontract.ErrIntegrity
	}
	hook, e := addressWord(raw, 4)
	if e != nil {
		return e
	}
	info, _ := word(raw, 5)
	lower := packedTick(info, 8)
	upper := packedTick(info, 32)
	poolID := crypto.Keccak256Hash(raw[:5*32])
	p.Pool = manager.Hex()
	p.PoolID = poolID.Hex()
	p.Hook = hook.Hex()
	p.TickSpacing = &spacing
	if hook != (common.Address{}) {
		p.State = "unsupported"
		p.Limitations = append(p.Limitations, "hook_accounting_not_supported")
		return nil
	}
	state, e := s.call(ctx, d.Address("state_view"), "getSlot0(bytes32)", poolID[:])
	if e != nil {
		return e
	}
	if len(state) != 128 {
		return deficontract.ErrIntegrity
	}
	price, _ := word(state, 0)
	tick, e := signedWord(state, 1)
	if e != nil {
		return e
	}
	lpfee, _ := word(state, 3)
	if lpfee.BitLen() > 24 {
		return deficontract.ErrIntegrity
	}
	f := uint32(lpfee.Uint64())
	p.Fee = &f
	positionKey := crypto.Keccak256Hash(c.Contract[:], intWord(lower)[29:], intWord(upper)[29:], uintWord(id))
	position, e := s.call(ctx, d.Address("state_view"), "getPositionInfo(bytes32,bytes32)", poolID[:], positionKey[:])
	if e != nil {
		return e
	}
	if len(position) != 96 {
		return deficontract.ErrIntegrity
	}
	liquidity, _ := word(position, 0)
	if e = setLiquidity(p, liquidity, price, lower, upper, tick, t0, t1); e != nil {
		return e
	}
	growth, e := s.call(ctx, d.Address("state_view"), "getFeeGrowthInside(bytes32,int24,int24)", poolID[:], intWord(lower), intWord(upper))
	if e != nil {
		return e
	}
	if len(growth) != 64 {
		return deficontract.ErrIntegrity
	}
	for i, a := range []common.Address{t0, t1} {
		current, _ := word(growth, i)
		last, _ := word(position, i+1)
		amount, e := feeDelta(current, last, liquidity)
		if e != nil {
			return e
		}
		p.UnsettledFees = append(p.UnsettledFees, asset(a, amount))
	}
	if liquidity.Sign() == 0 {
		p.State = "closed"
	}
	return nil
}
func packedTick(v *big.Int, shift uint) int32 {
	n := new(big.Int).Rsh(new(big.Int).Set(v), shift).Uint64() & 0xffffff
	if n&0x800000 != 0 {
		return int32(n) - 1<<24
	}
	return int32(n)
}
