// Package etherscanops owns stable identities and billing metadata for the
// explicitly supported Etherscan V2 compatibility actions.
package etherscanops

import "slices"

const DefaultMaxResponseBytes int64 = 8 << 20

type Spec struct {
	ID               string
	Module           string
	Action           string
	BillingEligible  bool
	MaxResponseBytes int64
}

var catalog = []Spec{
	billable("account", "balance"),
	billable("account", "balancemulti"),
	billable("account", "txlist"),
	billable("account", "txlistinternal"),
	billable("account", "tokentx"),
	billable("account", "tokennfttx"),
	billable("account", "token1155tx"),
	billable("account", "tokenbalance"),
	billable("account", "getminedblocks"),
	billable("contract", "getabi"),
	billable("contract", "getsourcecode"),
	billable("contract", "getcontractcreation"),
	nonBillable("contract", "verifysourcecode"),
	nonBillable("contract", "checkverifystatus"),
	nonBillable("contract", "verifyproxycontract"),
	nonBillable("contract", "checkproxyverification"),
	billable("transaction", "getstatus"),
	billable("transaction", "gettxreceiptstatus"),
	billable("logs", "getLogs"),
	billable("block", "getblocknobytime"),
	billable("block", "getblockcountdown"),
	nonBillable("stats", "ethsupply"),
	billable("stats", "ethprice"),
	billable("stats", "tokensupply"),
	billable("token", "tokensupply"),
	billable("token", "tokenbalance"),
	billable("token", "tokeninfo"),
	nonBillable("token", "tokenholderlist"),
}

func billable(module, action string) Spec {
	return Spec{
		ID:     "etherscan." + module + "." + action,
		Module: module, Action: action, BillingEligible: true,
		MaxResponseBytes: DefaultMaxResponseBytes,
	}
}

func nonBillable(module, action string) Spec {
	return Spec{ID: "etherscan." + module + "." + action, Module: module, Action: action}
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
