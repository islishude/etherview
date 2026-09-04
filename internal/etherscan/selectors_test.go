package etherscan

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/islishude/etherview/internal/etherscanops"
)

func TestExpandedCompatibilityInventoryCounts(t *testing.T) {
	t.Parallel()
	registered := 0
	for _, actions := range supported {
		registered += len(actions)
	}
	if registered != 35 {
		t.Fatalf("registered actions=%d, want 35", registered)
	}
	if eligible := len(etherscanops.EligibleIDs()); eligible != 30 {
		t.Fatalf("billing-eligible actions=%d, want 30", eligible)
	}
}

func TestExpandedSelectorModes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, action string
		values       url.Values
		valid        bool
	}{
		{name: "normal legacy", action: "txlist", values: url.Values{"address": {testSender}}, valid: true},
		{name: "normal directional", action: "txlist", values: url.Values{"from": {testSender}, "to": {testRecipient}, "fromto_opr": {"or"}}, valid: true},
		{name: "normal mixed", action: "txlist", values: url.Values{"address": {testSender}, "from": {testRecipient}, "fromto_opr": {"and"}}},
		{name: "normal missing operator", action: "txlist", values: url.Values{"from": {testSender}}},
		{name: "internal hash", action: "txlistinternal", values: url.Values{"txhash": {testHash(1)}}, valid: true},
		{name: "internal range", action: "txlistinternal", values: url.Values{"startblock": {"1"}, "endblock": {"latest"}}, valid: true},
		{name: "internal directional", action: "txlistinternal", values: url.Values{"to": {testRecipient}, "fromto_opr": {"and"}}, valid: true},
		{name: "internal open range", action: "txlistinternal", values: url.Values{"startblock": {"1"}}, valid: true},
		{name: "internal end only", action: "txlistinternal", values: url.Values{"endblock": {"2"}}},
		{name: "token legacy", action: "tokentx", values: url.Values{"address": {testSender}, "contractaddress": {testContract}}, valid: true},
		{name: "token contract", action: "tokentx", values: url.Values{"contractaddress": {testContract}}, valid: true},
		{name: "token directional", action: "tokentx", values: url.Values{"from": {testSender}, "fromto_opr": {"or"}}, valid: true},
		{name: "token empty", action: "tokentx", values: url.Values{}},
		{name: "token mixed", action: "tokentx", values: url.Values{"address": {testSender}, "to": {testRecipient}, "fromto_opr": {"and"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := validateValues(test.values, supported["account"][test.action])
			if (err == nil) != test.valid {
				t.Fatalf("validation error=%v, valid=%t", err, test.valid)
			}
		})
	}
}

func TestNewActionsRejectUnknownAndSortParameters(t *testing.T) {
	t.Parallel()
	if err := validateValues(url.Values{
		"address": {testSender}, "unknown": {"1"},
	}, supported["account"]["fundedby"]); err == nil {
		t.Fatal("strict fundedby action accepted an unknown parameter")
	}
	if err := validateValues(url.Values{
		"address": {testSender}, "sort": {"asc"},
	}, supported["account"]["addresstokenbalance"]); err == nil {
		t.Fatal("holding action accepted sort")
	}
}

func TestExpandedBillingCanonicalization(t *testing.T) {
	t.Parallel()
	values := url.Values{
		"chainid": {"1"}, "module": {"account"}, "action": {"txlist"},
		"from":       {"0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		"to":         {"0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"},
		"fromto_opr": {"OR"}, "endblock": {"latest"},
	}
	canonical, err := canonicalBillableValues(http.MethodGet, "account", "txlist", values, supported["account"]["txlist"])
	if err != nil {
		t.Fatal(err)
	}
	if canonical.Get("from") != "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" ||
		canonical.Get("to") != "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" ||
		canonical.Get("fromto_opr") != "or" || canonical.Get("startblock") != "0" ||
		canonical.Get("endblock") != "" || canonical.Get("page") != "1" ||
		canonical.Get("offset") != "100" || canonical.Get("sort") != "asc" {
		t.Fatalf("canonical values=%s", canonical.Encode())
	}

	holding := url.Values{
		"chainid": {"1"}, "module": {"account"}, "action": {"addresstokenbalance"},
		"address": {testSender}, "page": {"11"}, "offset": {"100"},
	}
	_, err = canonicalBillableValues(http.MethodGet, "account", "addresstokenbalance", holding, supported["account"]["addresstokenbalance"])
	if !errors.Is(err, ErrHoldingWindowUnavailable) {
		t.Fatalf("deep holding window error=%v", err)
	}

	internal := url.Values{
		"chainid": {"1"}, "module": {"account"}, "action": {"txlistinternal"},
		"startblock": {"10"}, "endblock": {"latest"},
	}
	canonical, err = canonicalBillableValues(http.MethodGet, "account", "txlistinternal", internal, supported["account"]["txlistinternal"])
	if err != nil || canonical.Get("startblock") != "10" || canonical.Get("endblock") != "" {
		t.Fatalf("canonical internal range=%s error=%v", canonical.Encode(), err)
	}
	if _, err := internalTransactionSelector(canonical); err != nil {
		t.Fatalf("canonical internal range is not executable: %v", err)
	}
}
