//go:build runtimee2e

package runtimee2e

import (
	"context"
	"encoding/json"
	"maps"
	"math/big"
	"net/url"
	"strconv"
	"strings"
)

func (h *harness) captureEtherscanExpansion(ctx context.Context) (string, string, string) {
	h.t.Helper()
	advancedRaw := h.etherscanResult(ctx, url.Values{
		"module": {"account"}, "action": {"txlist"},
		"from": {h.fixture.accounts[0]}, "to": {nativeTransferTarget},
		"fromto_opr": {"and"}, "startblock": {"0"}, "endblock": {"latest"},
	})
	var advanced []struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(advancedRaw, &advanced); err != nil {
		h.t.Fatal(err)
	}
	found := false
	for _, transaction := range advanced {
		if strings.EqualFold(transaction.Hash, h.fixture.nativeHash) {
			found = true
			break
		}
	}
	if !found {
		h.t.Fatalf("advanced Etherscan txlist omitted native transfer %s: %s", h.fixture.nativeHash, advancedRaw)
	}

	fundingRaw := h.etherscanResult(ctx, url.Values{
		"module": {"account"}, "action": {"fundedby"}, "address": {nativeTransferTarget},
	})
	var funding struct {
		Block      string `json:"block"`
		FundingTxn string `json:"fundingTxn"`
		Value      string `json:"value"`
	}
	if err := json.Unmarshal(fundingRaw, &funding); err != nil {
		h.t.Fatal(err)
	}
	if !strings.EqualFold(funding.FundingTxn, h.fixture.nativeHash) || funding.Value != "11" {
		h.t.Fatalf("Etherscan fundedby=%+v, want native transfer %s value 11", funding, h.fixture.nativeHash)
	}

	countsRaw := h.etherscanResult(ctx, url.Values{
		"module": {"block"}, "action": {"getblocktxnscount"},
		"blockno": {strconv.FormatUint(h.fixture.finalHeight, 10)},
	})
	var counts struct {
		Block        string `json:"block"`
		Transactions string `json:"txsCount"`
		Internal     string `json:"internalTxsCount"`
	}
	if err := json.Unmarshal(countsRaw, &counts); err != nil {
		h.t.Fatal(err)
	}
	transactionCount, ok := new(big.Int).SetString(counts.Transactions, 10)
	if !ok || transactionCount.Sign() <= 0 || counts.Block != strconv.FormatUint(h.fixture.finalHeight, 10) {
		h.t.Fatalf("Etherscan block counts=%+v", counts)
	}
	return strings.ToLower(h.fixture.nativeHash),
		funding.Block + ":" + funding.Value + ":" + strings.ToLower(funding.FundingTxn),
		counts.Block + ":" + counts.Transactions + ":" + counts.Internal
}

func (h *harness) etherscanResult(ctx context.Context, values url.Values) json.RawMessage {
	h.t.Helper()
	values = maps.Clone(values)
	values.Set("chainid", "1")
	var envelope struct {
		Status  string          `json:"status"`
		Message string          `json:"message"`
		Result  json.RawMessage `json:"result"`
	}
	h.mustGetJSON(ctx, "/v2/api?"+values.Encode(), &envelope)
	if envelope.Status != "1" || envelope.Message != "OK" {
		h.t.Fatalf("Etherscan %s.%s failed: status=%s message=%s result=%s",
			values.Get("module"), values.Get("action"), envelope.Status, envelope.Message, envelope.Result)
	}
	return envelope.Result
}
