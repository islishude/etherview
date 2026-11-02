package paymentoption

import (
	"math/big"
	"testing"
)

func validOption() Option {
	d := uint8(6)
	return Option{ID: "usd", Network: "eip155:31337", Name: "USD", Asset: "0x1111111111111111111111111111111111111111", Recipient: "0x2222222222222222222222222222222222222222", AssetDecimals: &d, AssetTransferMethods: []string{"eip3009", "permit2"}, AssetEIP712Name: "USD", AssetEIP712Version: "1", MinimumTopupAmountAtomic: "1", MaximumTopupAmountAtomic: new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)).String(), CreditRateNumerator: "1", CreditRateDenominator: "1"}
}
func TestExactCredits(t *testing.T) {
	for _, tc := range []struct {
		name                                 string
		decimals                             uint8
		numerator, denominator, amount, want string
	}{
		{"whole token", 0, "1000000", "1", "2", "2000000"},
		{"six decimals", 6, "3", "1", "1234567", "3703701"},
		{"eighteen decimals", 18, "2", "1000000000000", "1234567000000000000", "2469134"},
		{"full precision metadata", 255, "1", "1", "1", "1"},
		{"fractional credit", 18, "1", "1000000000000", "1000000000001", ""},
		{"zero credit", 18, "1", "1000000000000", "1", ""},
		{"overflow", 6, "2", "1", validOption().MaximumTopupAmountAtomic, ""},
		{"intermediate exceeds uint256", 6, "2", "2", validOption().MaximumTopupAmountAtomic, validOption().MaximumTopupAmountAtomic},
		{"noncanonical", 6, "1", "1", "01", ""},
		{"negative", 6, "1", "1", "-1", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := validOption()
			o.AssetDecimals = &tc.decimals
			o.CreditRateNumerator = tc.numerator
			o.CreditRateDenominator = tc.denominator
			got, err := o.Credit(tc.amount)
			if got != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
func TestRequiredPolicy(t *testing.T) {
	for _, mutate := range []func(*Option){func(o *Option) { o.AssetDecimals = nil }, func(o *Option) { o.CreditRateNumerator = "0" }, func(o *Option) { o.CreditRateDenominator = "0" }, func(o *Option) { o.MinimumTopupAmountAtomic = "2"; o.MaximumTopupAmountAtomic = "1" }, func(o *Option) { o.AssetTransferMethods = []string{"permit2", "permit2"} }} {
		o := validOption()
		mutate(&o)
		if o.Validate() == nil {
			t.Fatal("accepted invalid policy")
		}
	}
}
