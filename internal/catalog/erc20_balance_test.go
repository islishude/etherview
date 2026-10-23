package catalog

import (
	"errors"
	"strings"
	"testing"
)

func TestERC20BalancesRejectNonCanonicalDecimalObservations(t *testing.T) {
	t.Parallel()
	for _, balance := range []string{"+1", "01", "-0", "00"} {
		t.Run(balance, func(t *testing.T) {
			t.Parallel()
			token := bytesOf(0xcc, 20)
			state := &fakeERC20State{observations: []ERC20BalanceObservation{{
				Balance: balance, Confidence: NFTStateConfidenceRPCExact,
			}}}
			catalog, backend := openCatalogWithOptions(t, Options{ERC20State: state},
				snapshotStep("100", bytesOf(0xaa, 32)), stageStep("complete"),
				catalogQueryStep{contains: "FROM token_balance_deltas AS d", rows: catalogRows(1, []any{token})},
				catalogQueryStep{contains: "FROM token_contracts", rows: catalogRows(13, tokenRow(token, "erc20", "90"))},
			)
			page, err := catalog.ERC20Balances(t.Context(), ERC20BalanceRequest{
				ChainID: "1", Owner: "0x" + strings.Repeat("12", 20), Limit: 2,
			})
			if !errors.Is(err, ErrCorruptData) || len(page.Items) != 0 {
				t.Fatalf("balance=%q page=%+v error=%v, want corrupt data with no balances", balance, page, err)
			}
			assertCatalogConsumed(t, backend)
		})
	}
}
