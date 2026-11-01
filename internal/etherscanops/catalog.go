// Package etherscanops owns stable identities and billing metadata for the
// explicitly supported Etherscan V2 compatibility actions.
package etherscanops

import (
	"fmt"
	"math/big"
	"slices"
)

const DefaultMaxResponseBytes int64 = 8 << 20

type Spec struct {
	ID               string
	Module           string
	Action           string
	BillingEligible  bool
	ComplexityWeight uint64
	CostReason       string
	Classification   string
	MaxResponseBytes int64
}

var catalog = []Spec{
	billable("blob", "getblob", 5, "fixed-size large blob artifact with archive integrity validation"),
	billable("account", "balance", 2, "exact state or bounded composite lookup"),
	billable("account", "balancemulti", 40, "up to 20 exact account balances"),
	billable("account", "txlist", 5, "paged records or large artifact"),
	billable("account", "txlistinternal", 10, "derived joins, coverage checks or batch lookup"),
	billable("account", "tokentx", 10, "derived joins, coverage checks or batch lookup"),
	billable("account", "tokennfttx", 10, "derived joins, coverage checks or batch lookup"),
	billable("account", "token1155tx", 10, "derived joins, coverage checks or batch lookup"),
	billable("account", "tokenbalance", 2, "exact state or bounded composite lookup"),
	billable("account", "getminedblocks", 5, "paged records or large artifact"),
	billable("account", "txsBeaconWithdrawal", 5, "paged records or large artifact"),
	billable("account", "addresstokenbalance", 100, "asset candidates and batch state reconciliation"),
	billable("account", "addresstokennftbalance", 100, "asset candidates and batch state reconciliation"),
	billable("account", "addresstokennftinventory", 100, "asset candidates and batch state reconciliation"),
	billable("account", "fundedby", 10, "derived joins, coverage checks or batch lookup"),
	billable("contract", "getabi", 1, "single record or published aggregate"),
	billable("contract", "getsourcecode", 5, "paged records or large artifact"),
	billable("contract", "getcontractcreation", 10, "derived joins, coverage checks or batch lookup"),
	nonBillable("verification", "contract", "verifysourcecode"),
	nonBillable("verification", "contract", "checkverifystatus"),
	nonBillable("verification", "contract", "verifyproxycontract"),
	nonBillable("verification", "contract", "checkproxyverification"),
	billable("transaction", "getstatus", 1, "single record or published aggregate"),
	billable("transaction", "gettxreceiptstatus", 1, "single record or published aggregate"),
	billable("logs", "getLogs", 50, "log scan over up to 100000 blocks"),
	billable("block", "getblocknobytime", 2, "exact state or bounded composite lookup"),
	billable("block", "getblockcountdown", 2, "exact state or bounded composite lookup"),
	billable("block", "getblocktxnscount", 1, "single record or published aggregate"),
	nonBillable("unavailable", "stats", "ethsupply"),
	billable("stats", "ethprice", 1, "single record or published aggregate"),
	billable("stats", "tokensupply", 2, "exact state or bounded composite lookup"),
	billable("token", "tokensupply", 2, "exact state or bounded composite lookup"),
	billable("token", "tokenbalance", 2, "exact state or bounded composite lookup"),
	billable("token", "tokeninfo", 2, "exact state or bounded composite lookup"),
	billable("token", "tokenholderlist", 5, "paged records or large artifact"),
	billable("token", "tokenholdercount", 1, "single record or published aggregate"),
}

func billable(module, action string, weight uint64, reason string) Spec {
	return Spec{
		ID:     "etherview." + module + "." + action,
		Module: module, Action: action, BillingEligible: true,
		MaxResponseBytes: DefaultMaxResponseBytes,
		ComplexityWeight: weight, CostReason: reason, Classification: "query",
	}
}

func nonBillable(classification, module, action string) Spec {
	return Spec{ID: "etherview." + module + "." + action, Module: module, Action: action, Classification: classification}
}

func Lookup(id string) (Spec, bool) {
	for _, spec := range catalog {
		if spec.ID == id {
			return spec, true
		}
	}
	return Spec{}, false
}

func LookupAction(module, action string) (Spec, bool) {
	for _, spec := range catalog {
		if spec.Module == module && spec.Action == action {
			return spec, true
		}
	}
	return Spec{}, false
}

func All() []Spec { return slices.Clone(catalog) }

func EligibleIDs() []string {
	result := make([]string, 0, len(catalog))
	for _, spec := range catalog {
		if spec.BillingEligible {
			result = append(result, spec.ID)
		}
	}
	slices.Sort(result)
	return result
}

// Prices derives the complete price schedule. There is no free-query fallback.
func Prices(base string) (map[string]string, error) {
	value, ok := new(big.Int).SetString(base, 10)
	if !ok || value.Sign() <= 0 || value.String() != base || value.BitLen() > 256 {
		return nil, fmt.Errorf("billing.base_price_atomic must be a canonical positive uint256")
	}
	prices := make(map[string]string)
	for _, spec := range catalog {
		if spec.Classification != "query" {
			if spec.BillingEligible || (spec.Classification != "verification" && spec.Classification != "unavailable") {
				return nil, fmt.Errorf("billing operation %q has invalid classification", spec.ID)
			}
			continue
		}
		if !spec.BillingEligible || spec.ComplexityWeight == 0 || spec.ComplexityWeight > 2147483647 || spec.CostReason == "" {
			return nil, fmt.Errorf("billing operation %q requires a positive complexity weight and reason", spec.ID)
		}
		amount := new(big.Int).Mul(value, new(big.Int).SetUint64(spec.ComplexityWeight))
		if amount.BitLen() > 256 {
			return nil, fmt.Errorf("billing operation %q price exceeds uint256", spec.ID)
		}
		if _, duplicate := prices[spec.ID]; duplicate {
			return nil, fmt.Errorf("duplicate billing operation %q", spec.ID)
		}
		prices[spec.ID] = amount.String()
	}
	return prices, nil
}
