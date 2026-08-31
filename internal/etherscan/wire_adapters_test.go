package etherscan

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/chainbundle/testfixture"
)

func TestDynamicEffectiveGasPriceRequiresAuthenticatedBlockContext(t *testing.T) {
	t.Parallel()
	bundle, err := testfixture.New(testfixture.Options{
		Number:           9,
		BaseFee:          big.NewInt(0),
		TransactionTypes: []uint8{types.DynamicFeeTxType},
	})
	if err != nil {
		t.Fatal(err)
	}
	transaction := bundle.Block.Transactions()[0]
	blockNumber := bundle.Block.Number()

	standalone, err := decodeStoredReceipt(
		bundle.RawReceipts[0],
		transaction,
		bundle.Block.Hash(),
		blockNumber,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if standalone.EffectiveGasPrice != nil {
		t.Fatalf(
			"standalone receipt exposed unauthenticated effectiveGasPrice %s",
			standalone.EffectiveGasPrice,
		)
	}
	if price, err := effectiveGasPrice(transaction, standalone); err == nil ||
		price != nil ||
		!strings.Contains(err.Error(), "no authenticated effective gas price") {
		t.Fatalf("effectiveGasPrice() = %v, %v", price, err)
	}

	authenticated, err := decodeStoredReceiptWithBlockContext(
		bundle.RawReceipts[0],
		transaction,
		bundle.Block.Hash(),
		blockNumber,
		0,
		bundle.Block.BaseFee(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if authenticated.EffectiveGasPrice == nil ||
		authenticated.EffectiveGasPrice.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf(
			"authenticated effectiveGasPrice = %v, want 1",
			authenticated.EffectiveGasPrice,
		)
	}

	poisoned := mutateReceiptField(
		t,
		bundle.RawReceipts[0],
		"effectiveGasPrice",
		json.RawMessage(`"0x7"`),
	)
	if receipt, err := decodeStoredReceiptWithBlockContext(
		poisoned,
		transaction,
		bundle.Block.Hash(),
		blockNumber,
		0,
		bundle.Block.BaseFee(),
	); err == nil || receipt != nil {
		t.Fatalf(
			"authenticated decode accepted poisoned effectiveGasPrice: %#v, %v",
			receipt,
			err,
		)
	}
}

func TestAccountScanAuthenticatesDynamicEffectiveGasPriceFromStoredHeader(t *testing.T) {
	t.Parallel()
	bundle, err := testfixture.New(testfixture.Options{
		Number:           9,
		BaseFee:          big.NewInt(0),
		TransactionTypes: []uint8{types.DynamicFeeTxType},
	})
	if err != nil {
		t.Fatal(err)
	}
	transaction := bundle.Block.Transactions()[0]
	sender, err := types.Sender(
		types.LatestSignerForChainID(transaction.ChainId()),
		transaction,
	)
	if err != nil {
		t.Fatal(err)
	}
	missing := deleteReceiptField(
		t,
		bundle.RawReceipts[0],
		"effectiveGasPrice",
	)
	poisoned := mutateReceiptField(
		t,
		bundle.RawReceipts[0],
		"effectiveGasPrice",
		json.RawMessage(`"0x7"`),
	)

	for _, test := range []struct {
		name      string
		receipt   json.RawMessage
		wantError bool
	}{
		{name: "legacy omission derived", receipt: missing},
		{name: "poisoned value rejected", receipt: poisoned, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			db := fakeDatabase(
				t,
				completeCoreCoverageExpectation("0", "", "9"),
				sqlExpectation{
					contains: "FROM transaction_inclusions AS inclusion",
					columns:  fakeColumns(9),
					rows: [][]driver.Value{{
						[]byte(bundle.RawTransactions[0]),
						[]byte(test.receipt),
						strconv.FormatUint(bundle.Block.Time(), 10),
						hexutil.EncodeBig(bundle.Block.BaseFee()),
						"9",
						bundle.Block.Hash().Bytes(),
						int64(0),
						transaction.Hash().Bytes(),
						"9",
					}},
				},
			)
			backend := testPostgresBackend(t, db, PostgresOptions{ChainID: 1})
			result, err := backend.Execute(
				context.Background(),
				Request{
					Module: "account",
					Action: "txlist",
					Values: url.Values{"address": {sender.Hex()}},
				},
			)
			if test.wantError {
				if err == nil || !strings.Contains(err.Error(), "effectiveGasPrice") {
					t.Fatalf("Execute() = %#v, %v", result, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			transactions, ok := result.([]accountTransaction)
			if !ok || len(transactions) != 1 ||
				transactions[0].GasPrice != "1" {
				t.Fatalf("Execute() = %#v", result)
			}
		})
	}
}

func mutateReceiptField(
	t *testing.T,
	raw json.RawMessage,
	name string,
	value json.RawMessage,
) json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	fields[name] = value
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func deleteReceiptField(
	t *testing.T,
	raw json.RawMessage,
	name string,
) json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, name)
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
