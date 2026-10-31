package etherscanops

import (
	"math/big"
	"testing"
)

func TestComplexityPrices(t *testing.T) {
	prices, err := Prices("3")
	if err != nil || len(prices) != 30 {
		t.Fatalf("prices=%v err=%v", prices, err)
	}
	expected := map[string]string{
		"contract.getabi": "3", "account.balance": "6", "account.txlist": "15",
		"account.txlistinternal": "30", "account.balancemulti": "120", "logs.getLogs": "150",
		"account.addresstokenbalance": "300",
	}
	for id, want := range expected {
		if prices["etherscan."+id] != want {
			t.Fatalf("%s: %s want %s", id, prices["etherscan."+id], want)
		}
	}
	for _, pair := range [][2]string{{"account.tokenbalance", "token.tokenbalance"}, {"stats.tokensupply", "token.tokensupply"}} {
		if prices["etherscan."+pair[0]] != prices["etherscan."+pair[1]] {
			t.Fatalf("alias prices differ: %v", pair)
		}
	}
}

func TestPriceBounds(t *testing.T) {
	maximum := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	for _, value := range []string{"", "0", "01", "-1", "1.5", maximum.String()} {
		if _, err := Prices(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	if _, err := Prices(maximum.Div(maximum, big.NewInt(100)).String()); err != nil {
		t.Fatal(err)
	}
}

func TestMissingQueryPolicyRejected(t *testing.T) {
	original := catalog
	defer func() { catalog = original }()
	for _, spec := range []Spec{
		{ID: "etherscan.new.query", Classification: "query", BillingEligible: true, CostReason: "lookup"},
		{ID: "etherscan.new.query", Classification: "query", BillingEligible: true, ComplexityWeight: 1},
		{ID: "etherscan.new.query"},
	} {
		catalog = append(append([]Spec{}, original...), spec)
		if _, err := Prices("1"); err == nil {
			t.Fatalf("accepted missing policy: %+v", spec)
		}
	}
}
