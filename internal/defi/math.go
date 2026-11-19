package defi

import (
	"github.com/islishude/etherview/internal/deficontract"
	"math/big"
)

var q96 = new(big.Int).Lsh(big.NewInt(1), 96)
var q128 = new(big.Int).Lsh(big.NewInt(1), 128)
var q256 = new(big.Int).Lsh(big.NewInt(1), 256)

// Share computes the floor of a proportional reserve entitlement, not a swap quote.
func Share(balance, reserve, supply *big.Int) (*big.Int, error) {
	if !uintBits(balance, 256) || !uintBits(reserve, 256) || !uintBits(supply, 256) || supply.Sign() == 0 || balance.Cmp(supply) > 0 {
		return nil, deficontract.ErrIntegrity
	}
	return new(big.Int).Quo(new(big.Int).Mul(balance, reserve), supply), nil
}
func uintBits(v *big.Int, n int) bool { return v != nil && v.Sign() >= 0 && v.BitLen() <= n }

// SqrtAtTick derives the protocol's Q128.128 multipliers from the defining
// rational numbers. Each reciprocal power is rounded to nearest before the
// protocol's multiply/shift sequence. No floating point enters the calculation.
func SqrtAtTick(tick int32) (*big.Int, error) {
	if tick < -887272 || tick > 887272 {
		return nil, deficontract.ErrIntegrity
	}
	abs := int64(tick)
	if abs < 0 {
		abs = -abs
	}
	ratio := new(big.Int).Set(q128)
	for bit, factor := range tickFactors {
		if abs&(1<<bit) != 0 {
			ratio.Rsh(new(big.Int).Mul(ratio, factor), 128)
		}
	}
	if tick > 0 {
		ratio.Quo(new(big.Int).Sub(q256, big.NewInt(1)), ratio)
	}
	// Ceiling division by 2^32 matches the pool's tick boundary convention.
	ratio.Add(ratio, new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 32), big.NewInt(1)))
	return ratio.Rsh(ratio, 32), nil
}

var tickFactors = func() []*big.Int {
	factors := make([]*big.Int, 20)
	// Use 512 guard bits for repeated square roots and powers; derive Q128
	// factors by rounding once. The first factor is truncated by protocol design.
	precision := uint(640)
	scale := new(big.Int).Lsh(big.NewInt(1), precision)
	base := new(big.Int).Quo(new(big.Int).Mul(big.NewInt(10000), new(big.Int).Mul(scale, scale)), big.NewInt(10001))
	base.Sqrt(base)
	for i := range factors {
		rounded := new(big.Int).Set(base)
		if i > 0 {
			rounded.Add(rounded, new(big.Int).Lsh(big.NewInt(1), precision-129))
		}
		factors[i] = rounded.Rsh(rounded, precision-128)
		base.Rsh(new(big.Int).Mul(base, base), precision)
	}
	return factors
}()

func LiquidityAmounts(liquidity, price *big.Int, lower, upper int32) (*big.Int, *big.Int, error) {
	if !uintBits(liquidity, 128) || !uintBits(price, 160) || price.Sign() == 0 || lower >= upper {
		return nil, nil, deficontract.ErrIntegrity
	}
	a, e := SqrtAtTick(lower)
	if e != nil {
		return nil, nil, e
	}
	b, e := SqrtAtTick(upper)
	if e != nil {
		return nil, nil, e
	}
	p := new(big.Int).Set(price)
	if p.Cmp(a) < 0 {
		p.Set(a)
	}
	if p.Cmp(b) > 0 {
		p.Set(b)
	}
	amount0 := new(big.Int).Mul(liquidity, new(big.Int).Sub(b, p))
	amount0.Mul(amount0, q96)
	amount0.Quo(amount0, new(big.Int).Mul(p, b))
	amount1 := new(big.Int).Mul(liquidity, new(big.Int).Sub(p, a))
	amount1.Quo(amount1, q96)
	return amount0, amount1, nil
}
func feeDelta(current, previous, liquidity *big.Int) (*big.Int, error) {
	if !uintBits(current, 256) || !uintBits(previous, 256) || !uintBits(liquidity, 128) {
		return nil, deficontract.ErrIntegrity
	}
	delta := new(big.Int).Sub(current, previous)
	delta.Mod(delta, q256)
	delta.Mul(delta, liquidity)
	delta.Quo(delta, q128)
	if delta.BitLen() > 128 {
		return nil, deficontract.ErrIntegrity
	}
	return delta, nil
}
