package defi

import (
	"math/big"
	"testing"
)

func TestSqrtAtTickProtocolVectors(t *testing.T) {
	for _, tc := range []struct {
		tick int32
		want string
	}{
		{-887272, "4295128739"}, {-1, "79224201403219477170569942574"}, {0, "79228162514264337593543950336"}, {1, "79232123823359799118286999568"}, {887272, "1461446703485210103287273052203988822378723970342"},
	} {
		got, e := SqrtAtTick(tc.tick)
		if e != nil || got.String() != tc.want {
			t.Fatalf("tick %d: %v %v want %s", tc.tick, got, e, tc.want)
		}
	}
	for _, tick := range []int32{-887273, 887273} {
		if _, e := SqrtAtTick(tick); e == nil {
			t.Fatal("accepted out-of-range tick")
		}
	}
}
func TestLiquidityBoundariesAndPrecision(t *testing.T) {
	l := new(big.Int).Lsh(big.NewInt(1), 100)
	for _, tick := range []int32{-60, 0, 60} {
		price, _ := SqrtAtTick(tick)
		a, b, e := LiquidityAmounts(l, price, -60, 60)
		if e != nil {
			t.Fatal(e)
		}
		if tick == -60 && (a.Sign() == 0 || b.Sign() != 0) || tick == 60 && (a.Sign() != 0 || b.Sign() == 0) || tick == 0 && (a.Sign() == 0 || b.Sign() == 0) {
			t.Fatalf("boundary %d: %s %s", tick, a, b)
		}
	}
	if _, _, e := LiquidityAmounts(l, q96, 1, 1); e == nil {
		t.Fatal("accepted empty range")
	}
}
func TestShareAndFeeRounding(t *testing.T) {
	amount, e := Share(big.NewInt(3), big.NewInt(10), big.NewInt(7))
	if e != nil || amount.Int64() != 4 {
		t.Fatal(amount, e)
	}
	if _, e = Share(big.NewInt(1), big.NewInt(1), new(big.Int)); e == nil {
		t.Fatal("zero supply")
	}
	previous := new(big.Int).Sub(q256, q128)
	delta, e := feeDelta(new(big.Int), previous, big.NewInt(3))
	if e != nil || delta.Int64() != 3 {
		t.Fatal(delta, e)
	}
}
