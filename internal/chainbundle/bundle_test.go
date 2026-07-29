package chainbundle_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/chainbundle/testfixture"
)

func TestFixtureDecodesKnownTransactionTypesAndPreservesRawAlignment(t *testing.T) {
	t.Parallel()
	bundle, err := testfixture.New(testfixture.Options{
		Number:             7,
		ExtraData:          []byte("known-types"),
		TransactionTypes:   []uint8{0, 1, 2, 3, 4},
		LogsPerTransaction: 1,
		Withdrawals: []*types.Withdrawal{{
			Index: 9, Validator: 11, Address: common.Address{19: 0x07}, Amount: 13,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := chainbundle.Validate(bundle); err != nil {
		t.Fatal(err)
	}
	if len(bundle.Block.Transactions()) != 5 ||
		len(bundle.Receipts) != 5 ||
		len(bundle.RawTransactions) != 5 ||
		len(bundle.RawReceipts) != 5 ||
		len(bundle.RawLogs) != 5 ||
		len(bundle.RawWithdrawals) != 1 {
		t.Fatalf("bundle alignment = tx:%d receipts:%d rawTx:%d rawReceipts:%d rawLogs:%d withdrawals:%d",
			len(bundle.Block.Transactions()),
			len(bundle.Receipts),
			len(bundle.RawTransactions),
			len(bundle.RawReceipts),
			len(bundle.RawLogs),
			len(bundle.RawWithdrawals),
		)
	}
	for index, transaction := range bundle.Block.Transactions() {
		if transaction.Type() != uint8(index) {
			t.Fatalf("transaction %d type = %d", index, transaction.Type())
		}
	}
}

func TestDecodeBlockRejectsUnknownTransactionTypeAtomically(t *testing.T) {
	t.Parallel()
	bundle, err := testfixture.New(testfixture.Options{
		Number:           1,
		TransactionTypes: []uint8{types.LegacyTxType},
	})
	if err != nil {
		t.Fatal(err)
	}
	rawBlock := mutateBlockTransaction(t, bundle.RawBlock, 0, func(fields map[string]json.RawMessage) {
		fields["type"] = json.RawMessage(`"0x7f"`)
	})
	decoded, err := chainbundle.DecodeBlock(rawBlock, nil)
	if !errors.Is(err, chainbundle.ErrUnsupportedTransactionType) || !chainbundle.IsPermanent(err) {
		t.Fatalf("DecodeBlock() error = %v", err)
	}
	if decoded.Block != nil || decoded.RawBlock != nil || decoded.Receipts != nil {
		t.Fatalf("DecodeBlock() exposed a partial bundle: %#v", decoded)
	}
}

func TestDecodeBlockRejectsDuplicateTransactionHashes(t *testing.T) {
	t.Parallel()
	bundle, err := testfixture.New(testfixture.Options{
		Number:           1,
		TransactionTypes: []uint8{types.LegacyTxType, types.LegacyTxType},
	})
	if err != nil {
		t.Fatal(err)
	}
	duplicate := bundle.Block.Transactions()[0]
	header := bundle.Block.Header()
	header.TxHash = types.DeriveSha(
		types.Transactions{duplicate, duplicate},
		trie.NewStackTrie(nil),
	)
	blockHash := header.Hash()
	rawBlock := mutateObject(t, bundle.RawBlock, func(fields map[string]json.RawMessage) {
		fields["hash"] = mustJSON(t, blockHash)
		fields["transactionsRoot"] = mustJSON(t, header.TxHash)
		transactions := make([]json.RawMessage, 2)
		for index := range transactions {
			transactions[index] = mutateObject(
				t,
				bundle.RawTransactions[0],
				func(transactionFields map[string]json.RawMessage) {
					transactionFields["blockHash"] = mustJSON(t, blockHash)
					transactionFields["transactionIndex"] = mustJSON(
						t,
						[]string{"0x0", "0x1"}[index],
					)
				},
			)
		}
		fields["transactions"] = mustJSON(t, transactions)
	})
	decoded, err := chainbundle.DecodeBlock(rawBlock, nil)
	if err == nil || decoded.Block != nil ||
		!strings.Contains(err.Error(), "duplicates another transaction") {
		t.Fatalf("DecodeBlock() = %#v, %v", decoded, err)
	}
}

func TestDecodeBlockRejectsUintOverflowsWithoutPanicking(t *testing.T) {
	t.Parallel()
	overflow := "0x1" + strings.Repeat("0", 64)
	tests := []struct {
		name            string
		transactionType uint8
		field           string
	}{
		{name: "blob tip cap", transactionType: types.BlobTxType, field: "maxPriorityFeePerGas"},
		{name: "blob fee cap", transactionType: types.BlobTxType, field: "maxFeePerGas"},
		{name: "blob fee per blob gas", transactionType: types.BlobTxType, field: "maxFeePerBlobGas"},
		{name: "blob value", transactionType: types.BlobTxType, field: "value"},
		{name: "set-code tip cap", transactionType: types.SetCodeTxType, field: "maxPriorityFeePerGas"},
		{name: "set-code fee cap", transactionType: types.SetCodeTxType, field: "maxFeePerGas"},
		{name: "set-code value", transactionType: types.SetCodeTxType, field: "value"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			bundle, err := testfixture.New(testfixture.Options{
				Number:           1,
				TransactionTypes: []uint8{test.transactionType},
			})
			if err != nil {
				t.Fatal(err)
			}
			rawBlock := mutateBlockTransaction(
				t,
				bundle.RawBlock,
				0,
				func(fields map[string]json.RawMessage) {
					fields[test.field] = mustJSON(t, overflow)
				},
			)
			assertDecodeBlockErrorWithoutPanic(t, rawBlock, "exceeds uint256")
		})
	}

	t.Run("set-code authorization chain ID", func(t *testing.T) {
		t.Parallel()
		bundle, err := testfixture.New(testfixture.Options{
			Number:           1,
			TransactionTypes: []uint8{types.SetCodeTxType},
		})
		if err != nil {
			t.Fatal(err)
		}
		rawBlock := mutateBlockTransaction(
			t,
			bundle.RawBlock,
			0,
			func(fields map[string]json.RawMessage) {
				fields["authorizationList"] = mustJSON(t, []map[string]any{{
					"chainId": overflow,
					"address": common.Address{19: 1},
					"nonce":   "0x0",
					"yParity": "0x0",
					"r":       "0x0",
					"s":       "0x0",
				}})
			},
		)
		assertDecodeBlockErrorWithoutPanic(t, rawBlock, "exceeds uint256")
	})

	t.Run("set-code authorization y parity does not alias", func(t *testing.T) {
		t.Parallel()
		bundle, err := testfixture.New(testfixture.Options{
			Number:           1,
			TransactionTypes: []uint8{types.SetCodeTxType},
		})
		if err != nil {
			t.Fatal(err)
		}
		rawBlock := mutateBlockTransaction(
			t,
			bundle.RawBlock,
			0,
			func(fields map[string]json.RawMessage) {
				fields["authorizationList"] = mustJSON(t, []map[string]any{{
					"chainId": "0x1",
					"address": common.Address{19: 1},
					"nonce":   "0x0",
					"yParity": "0x100",
					"r":       "0x0",
					"s":       "0x0",
				}})
			},
		)
		assertDecodeBlockErrorWithoutPanic(t, rawBlock, "exceeds uint8")
	})
}

func TestDecodeBlockRejectsMissingAndNonCanonicalInclusion(t *testing.T) {
	t.Parallel()
	bundle, err := testfixture.New(testfixture.Options{
		Number:           1,
		TransactionTypes: []uint8{types.LegacyTxType},
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(map[string]json.RawMessage)
	}{
		{name: "missing block hash", mutate: func(fields map[string]json.RawMessage) {
			delete(fields, "blockHash")
		}},
		{name: "null index", mutate: func(fields map[string]json.RawMessage) {
			fields["transactionIndex"] = json.RawMessage("null")
		}},
		{name: "uppercase quantity", mutate: func(fields map[string]json.RawMessage) {
			fields["blockNumber"] = json.RawMessage(`"0X1"`)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			rawBlock := mutateBlockTransaction(t, bundle.RawBlock, 0, test.mutate)
			if decoded, err := chainbundle.DecodeBlock(rawBlock, nil); err == nil || decoded.Block != nil {
				t.Fatalf("DecodeBlock() = %#v, %v", decoded, err)
			}
		})
	}
}

func TestDecodeBlockRejectsMissingRequiredWireFields(t *testing.T) {
	t.Parallel()
	bundle, err := testfixture.New(testfixture.Options{
		Number:           1,
		TransactionTypes: []uint8{types.LegacyTxType},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"miner", "mixHash", "nonce"} {
		t.Run("header "+name, func(t *testing.T) {
			t.Parallel()
			rawBlock := mutateObject(t, bundle.RawBlock, func(fields map[string]json.RawMessage) {
				delete(fields, name)
			})
			if decoded, err := chainbundle.DecodeBlock(rawBlock, nil); err == nil || decoded.Block != nil {
				t.Fatalf("DecodeBlock() = %#v, %v", decoded, err)
			}
		})
	}
	t.Run("transaction to", func(t *testing.T) {
		t.Parallel()
		rawBlock := mutateBlockTransaction(t, bundle.RawBlock, 0, func(fields map[string]json.RawMessage) {
			delete(fields, "to")
		})
		if decoded, err := chainbundle.DecodeBlock(rawBlock, nil); err == nil || decoded.Block != nil {
			t.Fatalf("DecodeBlock() = %#v, %v", decoded, err)
		}
	})
}

func TestDecodeReceiptRejectsAmbiguousAndNonCanonicalFields(t *testing.T) {
	t.Parallel()
	bundle, err := testfixture.New(testfixture.Options{
		Number:             1,
		TransactionTypes:   []uint8{types.LegacyTxType},
		LogsPerTransaction: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	transaction := bundle.Block.Transactions()[0]
	decode := func(raw json.RawMessage) error {
		t.Helper()
		_, _, _, err := chainbundle.DecodeReceipt(
			raw,
			transaction,
			bundle.Block.Hash(),
			bundle.Block.NumberU64(),
			0,
			0,
			bundle.Block.BaseFee(),
		)
		return err
	}
	if err := decode(bundle.RawReceipts[0]); err != nil {
		t.Fatalf("DecodeReceipt() rejected null contractAddress: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(map[string]json.RawMessage)
	}{
		{name: "missing contract address", mutate: func(fields map[string]json.RawMessage) {
			delete(fields, "contractAddress")
		}},
		{name: "invalid status", mutate: func(fields map[string]json.RawMessage) {
			fields["status"] = json.RawMessage(`"0x2"`)
		}},
		{name: "non-canonical topic", mutate: func(fields map[string]json.RawMessage) {
			var logs []json.RawMessage
			if err := json.Unmarshal(fields["logs"], &logs); err != nil {
				t.Fatal(err)
			}
			logs[0] = mutateObject(t, logs[0], func(logFields map[string]json.RawMessage) {
				var topics []json.RawMessage
				if err := json.Unmarshal(logFields["topics"], &topics); err != nil {
					t.Fatal(err)
				}
				var topic string
				if err := json.Unmarshal(topics[0], &topic); err != nil {
					t.Fatal(err)
				}
				topics[0] = mustJSON(t, "0X"+topic[2:])
				logFields["topics"] = mustJSON(t, topics)
			})
			fields["logs"] = mustJSON(t, logs)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			raw := mutateObject(t, bundle.RawReceipts[0], test.mutate)
			if err := decode(raw); err == nil {
				t.Fatal("DecodeReceipt() accepted invalid wire fields")
			}
		})
	}
}

func TestReceiptExecutionDerivedFieldsAreAuthenticated(t *testing.T) {
	t.Parallel()
	ordinary, err := testfixture.New(testfixture.Options{
		Number:           1,
		TransactionTypes: []uint8{types.LegacyTxType, types.DynamicFeeTxType},
	})
	if err != nil {
		t.Fatal(err)
	}
	blockOnly, err := chainbundle.DecodeBlock(ordinary.RawBlock, nil)
	if err != nil {
		t.Fatal(err)
	}
	poisonedAddress := mutateObject(
		t,
		ordinary.RawReceipts[0],
		func(fields map[string]json.RawMessage) {
			fields["contractAddress"] = mustJSON(t, common.Address{19: 0xff})
		},
	)
	if decoded, err := blockOnly.WithReceipts([]json.RawMessage{
		poisonedAddress,
		ordinary.RawReceipts[1],
	}); err == nil || decoded.Block != nil ||
		!strings.Contains(err.Error(), "non-creation transaction") {
		t.Fatalf("WithReceipts() accepted a poisoned contract address: %#v, %v", decoded, err)
	}

	poisonedGas := mutateObject(
		t,
		ordinary.RawReceipts[1],
		func(fields map[string]json.RawMessage) {
			fields["gasUsed"] = json.RawMessage(`"0x1"`)
		},
	)
	if decoded, err := blockOnly.WithReceipts([]json.RawMessage{
		ordinary.RawReceipts[0],
		poisonedGas,
	}); err == nil || decoded.Block != nil ||
		!strings.Contains(err.Error(), "cumulativeGasUsed delta") {
		t.Fatalf("WithReceipts() accepted poisoned gasUsed: %#v, %v", decoded, err)
	}

	creation, err := testfixture.New(testfixture.Options{
		Number:            2,
		TransactionTypes:  []uint8{types.LegacyTxType},
		ContractCreations: []bool{true},
	})
	if err != nil {
		t.Fatal(err)
	}
	creationBlock, err := chainbundle.DecodeBlock(creation.RawBlock, nil)
	if err != nil {
		t.Fatal(err)
	}
	wrongCreationAddress := mutateObject(
		t,
		creation.RawReceipts[0],
		func(fields map[string]json.RawMessage) {
			fields["contractAddress"] = mustJSON(t, common.Address{19: 0xee})
		},
	)
	if decoded, err := creationBlock.WithReceipts(
		[]json.RawMessage{wrongCreationAddress},
	); err == nil || decoded.Block != nil ||
		!strings.Contains(err.Error(), "top-level CREATE address") {
		t.Fatalf("WithReceipts() accepted a wrong CREATE address: %#v, %v", decoded, err)
	}
}

func TestReceiptEffectiveGasPriceIsAuthenticatedForTransactionTypesZeroThroughFour(t *testing.T) {
	t.Parallel()
	baseFee := big.NewInt(0)
	bundle, err := testfixture.New(testfixture.Options{
		Number:           3,
		BaseFee:          baseFee,
		TransactionTypes: []uint8{0, 1, 2, 3, 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	blockOnly, err := chainbundle.DecodeBlock(bundle.RawBlock, bundle.RawUncles)
	if err != nil {
		t.Fatal(err)
	}
	for index, transaction := range bundle.Block.Transactions() {
		t.Run(fmt.Sprintf("type %d", transaction.Type()), func(t *testing.T) {
			t.Parallel()
			expected, err := chainbundle.TransactionEffectiveGasPrice(
				transaction,
				bundle.Block.BaseFee(),
			)
			if err != nil {
				t.Fatal(err)
			}
			if bundle.Receipts[index].EffectiveGasPrice == nil ||
				bundle.Receipts[index].EffectiveGasPrice.Cmp(expected) != 0 {
				t.Fatalf(
					"fixture effectiveGasPrice = %v, want %s",
					bundle.Receipts[index].EffectiveGasPrice,
					expected,
				)
			}

			poisoned := cloneRawMessages(bundle.RawReceipts)
			poisoned[index] = mutateObject(
				t,
				poisoned[index],
				func(fields map[string]json.RawMessage) {
					fields["effectiveGasPrice"] = json.RawMessage(`"0x7"`)
				},
			)
			if decoded, err := blockOnly.WithReceipts(poisoned); err == nil ||
				decoded.Block != nil ||
				!strings.Contains(err.Error(), "effectiveGasPrice") {
				t.Fatalf(
					"WithReceipts() accepted poisoned type %d price: %#v, %v",
					transaction.Type(),
					decoded,
					err,
				)
			}
			if decoded, err := blockOnly.WithStoredReceipts(poisoned); err == nil ||
				decoded.Block != nil ||
				!strings.Contains(err.Error(), "effectiveGasPrice") {
				t.Fatalf(
					"WithStoredReceipts() accepted poisoned type %d price: %#v, %v",
					transaction.Type(),
					decoded,
					err,
				)
			}

			missing := cloneRawMessages(bundle.RawReceipts)
			missing[index] = mutateObject(
				t,
				missing[index],
				func(fields map[string]json.RawMessage) {
					delete(fields, "effectiveGasPrice")
				},
			)
			if decoded, err := blockOnly.WithReceipts(missing); err == nil ||
				decoded.Block != nil ||
				!strings.Contains(err.Error(), "effectiveGasPrice") {
				t.Fatalf(
					"WithReceipts() accepted missing type %d price: %#v, %v",
					transaction.Type(),
					decoded,
					err,
				)
			}

			stored, err := blockOnly.WithStoredReceipts(missing)
			if err != nil {
				t.Fatalf(
					"WithStoredReceipts() rejected missing type %d price: %v",
					transaction.Type(),
					err,
				)
			}
			if stored.Receipts[index].EffectiveGasPrice == nil ||
				stored.Receipts[index].EffectiveGasPrice.Cmp(expected) != 0 {
				t.Fatalf(
					"stored derived type %d price = %v, want %s",
					transaction.Type(),
					stored.Receipts[index].EffectiveGasPrice,
					expected,
				)
			}

			standalone, _, _, err := chainbundle.DecodeStoredReceipt(
				poisoned[index],
				transaction,
				bundle.Block.Hash(),
				bundle.Block.NumberU64(),
				uint64(index),
				0,
			)
			switch transaction.Type() {
			case types.LegacyTxType, types.AccessListTxType:
				if err == nil {
					t.Fatalf(
						"DecodeStoredReceipt() accepted poisoned type %d price",
						transaction.Type(),
					)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				if standalone.EffectiveGasPrice != nil {
					t.Fatalf(
						"standalone type %d exposed unauthenticated price %s",
						transaction.Type(),
						standalone.EffectiveGasPrice,
					)
				}
			}
		})
	}
}

func TestTransactionEffectiveGasPriceUsesFeeCapMinimumWithoutMutation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		baseFee *big.Int
		want    int64
	}{
		{name: "nil base fee", baseFee: nil, want: 2},
		{name: "base fee plus tip", baseFee: big.NewInt(0), want: 1},
		{name: "fee cap", baseFee: big.NewInt(10), want: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			bundle, err := testfixture.New(testfixture.Options{
				Number:           4,
				BaseFee:          test.baseFee,
				TransactionTypes: []uint8{2, 3, 4},
			})
			if err != nil {
				t.Fatal(err)
			}
			var originalBaseFee *big.Int
			if test.baseFee != nil {
				originalBaseFee = new(big.Int).Set(test.baseFee)
			}
			for index, transaction := range bundle.Block.Transactions() {
				price, err := chainbundle.TransactionEffectiveGasPrice(
					transaction,
					test.baseFee,
				)
				if err != nil {
					t.Fatal(err)
				}
				if price.Cmp(big.NewInt(test.want)) != 0 ||
					bundle.Receipts[index].EffectiveGasPrice.Cmp(price) != 0 {
					t.Fatalf(
						"type %d effectiveGasPrice = %s receipt=%s, want %d",
						transaction.Type(),
						price,
						bundle.Receipts[index].EffectiveGasPrice,
						test.want,
					)
				}
			}
			if test.baseFee != nil && test.baseFee.Cmp(originalBaseFee) != 0 {
				t.Fatalf("base fee mutated from %s to %s", originalBaseFee, test.baseFee)
			}
		})
	}
}

func TestStoredBlockPersistenceAndLegacyCompatibility(t *testing.T) {
	t.Parallel()
	bundle, err := testfixture.New(testfixture.Options{
		Number:           2,
		ExtraData:        []byte("stored"),
		TransactionTypes: []uint8{types.DynamicFeeTxType},
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := chainbundle.EncodeStoredBlock(bundle)
	if err != nil {
		t.Fatal(err)
	}
	fromEnvelope, err := chainbundle.DecodeStoredBlock(stored)
	if err != nil {
		t.Fatal(err)
	}
	fromEnvelope, err = fromEnvelope.WithReceipts(bundle.RawReceipts)
	if err != nil {
		t.Fatal(err)
	}
	if err := chainbundle.Validate(fromEnvelope); err != nil {
		t.Fatal(err)
	}

	legacyEmpty, err := chainbundle.DecodeStoredBlock(bundle.RawBlock)
	if err != nil {
		t.Fatal(err)
	}
	legacyEmpty, err = legacyEmpty.WithReceipts(bundle.RawReceipts)
	if err != nil {
		t.Fatal(err)
	}
	if legacyEmpty.Block.Hash() != bundle.Block.Hash() {
		t.Fatalf("legacy empty-uncles hash = %s, want %s", legacyEmpty.Block.Hash(), bundle.Block.Hash())
	}

	legacyPoW := mutateObject(t, bundle.RawBlock, func(fields map[string]json.RawMessage) {
		fields["uncles"] = mustJSON(t, []common.Hash{{31: 0x01}})
	})
	decoded, err := chainbundle.DecodeStoredBlock(legacyPoW)
	if !errors.Is(err, chainbundle.ErrStoredUncleHeadersUnavailable) || !chainbundle.IsPermanent(err) {
		t.Fatalf("DecodeStoredBlock() = %#v, %v", decoded, err)
	}
	if decoded.Block != nil {
		t.Fatal("legacy PoW decode exposed an unvalidated block")
	}

	legacyFormatCollision := mutateObject(t, bundle.RawBlock, func(fields map[string]json.RawMessage) {
		fields["format"] = mustJSON(t, "etherview.chainbundle.v1")
		fields["rawBlock"] = json.RawMessage(`{"provider":"extension"}`)
		fields["rawUncles"] = json.RawMessage(`[]`)
	})
	if decoded, err := chainbundle.DecodeStoredBlock(legacyFormatCollision); err != nil {
		t.Fatalf("DecodeStoredBlock() confused provider fields with an envelope: %v", err)
	} else if decoded.Block.Hash() != bundle.Block.Hash() {
		t.Fatalf("legacy block hash = %s, want %s", decoded.Block.Hash(), bundle.Block.Hash())
	} else if !bytes.Contains(decoded.RawBlock, []byte(`"provider"`)) {
		t.Fatalf("legacy block lost provider extension fields: %s", decoded.RawBlock)
	}

	shortLivedEnvelope := mustJSON(t, map[string]any{
		"format":    "etherview.chainbundle.v1",
		"rawBlock":  json.RawMessage(bundle.RawBlock),
		"rawUncles": []json.RawMessage{},
	})
	if decoded, err := chainbundle.DecodeStoredBlock(shortLivedEnvelope); err != nil {
		t.Fatalf("DecodeStoredBlock() rejected the short-lived exact envelope: %v", err)
	} else if decoded.Block.Hash() != bundle.Block.Hash() {
		t.Fatalf("short-lived envelope hash = %s, want %s", decoded.Block.Hash(), bundle.Block.Hash())
	}
}

func TestStoredLegacyReceiptAndEmptyWithdrawalsCompatibility(t *testing.T) {
	t.Parallel()
	bundle, err := testfixture.New(testfixture.Options{
		Number:             2,
		TransactionTypes:   []uint8{types.LegacyTxType},
		ContractCreations:  []bool{false},
		Withdrawals:        []*types.Withdrawal{},
		LogsPerTransaction: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyBlock := mutateObject(t, bundle.RawBlock, func(fields map[string]json.RawMessage) {
		delete(fields, "withdrawals")
	})
	if decoded, err := chainbundle.DecodeBlock(legacyBlock, nil); err == nil || decoded.Block != nil {
		t.Fatalf("fresh DecodeBlock() accepted missing withdrawals: %#v, %v", decoded, err)
	}
	decoded, err := chainbundle.DecodeStoredBlock(legacyBlock)
	if err != nil {
		t.Fatal(err)
	}
	legacyReceipt := mutateObject(t, bundle.RawReceipts[0], func(fields map[string]json.RawMessage) {
		delete(fields, "contractAddress")
	})
	if strict, err := decoded.WithReceipts([]json.RawMessage{legacyReceipt}); err == nil ||
		strict.Block != nil {
		t.Fatalf("fresh WithReceipts() accepted omitted contractAddress: %#v, %v", strict, err)
	}
	decoded, err = decoded.WithStoredReceipts([]json.RawMessage{legacyReceipt})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := chainbundle.DecodeStoredReceipt(
		legacyReceipt,
		decoded.Block.Transactions()[0],
		decoded.Block.Hash(),
		decoded.Block.NumberU64(),
		0,
		0,
	); err != nil {
		t.Fatalf("DecodeStoredReceipt() rejected an ordinary legacy row: %v", err)
	}
	if len(decoded.Block.Withdrawals()) != 0 ||
		bytes.Contains(decoded.RawBlock, []byte(`"withdrawals"`)) ||
		bytes.Contains(decoded.RawReceipts[0], []byte(`"contractAddress"`)) {
		t.Fatalf(
			"legacy raw shape was not preserved: block=%s receipt=%s",
			decoded.RawBlock,
			decoded.RawReceipts[0],
		)
	}
	if err := chainbundle.Validate(decoded); err != nil {
		t.Fatalf("Validate() rejected compatible legacy rows: %v", err)
	}
	if _, err := decoded.Clone(); err != nil {
		t.Fatalf("Clone() rejected compatible legacy rows: %v", err)
	}

	nonEmpty, err := testfixture.New(testfixture.Options{
		Number: 3,
		Withdrawals: []*types.Withdrawal{{
			Index: 1, Validator: 2, Address: common.Address{19: 3}, Amount: 4,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	missingNonEmpty := mutateObject(t, nonEmpty.RawBlock, func(fields map[string]json.RawMessage) {
		delete(fields, "withdrawals")
	})
	if decoded, err := chainbundle.DecodeStoredBlock(missingNonEmpty); err == nil ||
		decoded.Block != nil {
		t.Fatalf("DecodeStoredBlock() synthesized non-empty withdrawals: %#v, %v", decoded, err)
	}
}

func TestStoredLegacyContractCreationStillRequiresContractAddress(t *testing.T) {
	t.Parallel()
	bundle, err := testfixture.New(testfixture.Options{
		Number:            2,
		TransactionTypes:  []uint8{types.LegacyTxType},
		ContractCreations: []bool{true},
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := chainbundle.DecodeStoredBlock(bundle.RawBlock)
	if err != nil {
		t.Fatal(err)
	}
	legacyReceipt := mutateObject(t, bundle.RawReceipts[0], func(fields map[string]json.RawMessage) {
		delete(fields, "contractAddress")
	})
	if result, err := decoded.WithStoredReceipts(
		[]json.RawMessage{legacyReceipt},
	); err == nil || result.Block != nil {
		t.Fatalf("WithStoredReceipts() accepted a contract-creation omission: %#v, %v", result, err)
	}

	failed, err := testfixture.New(testfixture.Options{
		Number:             3,
		TransactionTypes:   []uint8{types.LegacyTxType},
		ContractCreations:  []bool{true},
		FailedTransactions: []bool{true},
	})
	if err != nil {
		t.Fatal(err)
	}
	failedBlock, err := chainbundle.DecodeStoredBlock(failed.RawBlock)
	if err != nil {
		t.Fatal(err)
	}
	failedReceipt := mutateObject(t, failed.RawReceipts[0], func(fields map[string]json.RawMessage) {
		delete(fields, "contractAddress")
	})
	if _, err := failedBlock.WithStoredReceipts(
		[]json.RawMessage{failedReceipt},
	); err != nil {
		t.Fatalf("WithStoredReceipts() rejected a failed legacy creation: %v", err)
	}
	if _, _, _, err := chainbundle.DecodeStoredReceipt(
		failedReceipt,
		failedBlock.Block.Transactions()[0],
		failedBlock.Block.Hash(),
		failedBlock.Block.NumberU64(),
		0,
		0,
	); err != nil {
		t.Fatalf("DecodeStoredReceipt() rejected a failed legacy creation: %v", err)
	}
}

func TestStoredPoWBlockKeepsTopLevelRPCFieldsAndRoundTripsUncles(t *testing.T) {
	t.Parallel()
	uncle := &types.Header{
		ParentHash:  common.Hash{31: 0x01},
		UncleHash:   types.EmptyUncleHash,
		Coinbase:    common.Address{19: 0x02},
		Root:        common.Hash{31: 0x03},
		TxHash:      types.EmptyTxsHash,
		ReceiptHash: types.EmptyReceiptsHash,
		Difficulty:  big.NewInt(1),
		Number:      big.NewInt(1),
		GasLimit:    30_000_000,
		Time:        1_700_000_001,
		Extra:       []byte("uncle"),
	}
	bundle, err := testfixture.New(testfixture.Options{
		Number:    2,
		Timestamp: 1_700_000_002,
		ExtraData: []byte("pow"),
		Uncles:    []*types.Header{uncle},
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := chainbundle.EncodeStoredBlock(bundle)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(stored, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["miner"]; !exists {
		t.Fatalf("stored PoW block lost top-level miner: %s", stored)
	}
	if _, exists := fields["rawBlock"]; exists {
		t.Fatalf("stored PoW block used SQL-incompatible outer envelope: %s", stored)
	}
	if _, exists := fields["_etherviewChainBundle"]; !exists {
		t.Fatalf("stored PoW block has no versioned uncle metadata: %s", stored)
	}
	decoded, err := chainbundle.DecodeStoredBlock(stored)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Block.Hash() != bundle.Block.Hash() ||
		len(decoded.Block.Uncles()) != 1 ||
		decoded.Block.Uncles()[0].Hash() != uncle.Hash() ||
		len(decoded.RawUncles) != 1 {
		t.Fatalf(
			"decoded PoW block = hash:%s uncles:%d raw:%d",
			decoded.Block.Hash(),
			len(decoded.Block.Uncles()),
			len(decoded.RawUncles),
		)
	}
}

func TestRawUnknownFieldsSurviveBundleAndStoredPersistence(t *testing.T) {
	t.Parallel()
	bundle, err := testfixture.New(testfixture.Options{Number: 3})
	if err != nil {
		t.Fatal(err)
	}
	rawBlock := mutateObject(t, bundle.RawBlock, func(fields map[string]json.RawMessage) {
		fields["futureBlock"] = json.RawMessage(`{"opaque":true}`)
	})
	decoded, err := chainbundle.DecodeBlock(rawBlock, nil)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = decoded.WithReceipts(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(decoded.RawBlock, []byte(`"futureBlock"`)) {
		t.Fatalf("raw block lost unknown field: %s", decoded.RawBlock)
	}
	stored, err := chainbundle.EncodeStoredBlock(decoded)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := chainbundle.DecodeStoredBlock(stored)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(roundTrip.RawBlock, []byte(`"futureBlock"`)) {
		t.Fatalf("stored raw block lost unknown field: %s", roundTrip.RawBlock)
	}
}

func TestStoredBlockRejectsReservedMetadataCollision(t *testing.T) {
	t.Parallel()
	bundle, err := testfixture.New(testfixture.Options{
		Number: 3,
		Uncles: []*types.Header{{
			UncleHash:   types.EmptyUncleHash,
			Root:        common.Hash{31: 1},
			TxHash:      types.EmptyTxsHash,
			ReceiptHash: types.EmptyReceiptsHash,
			Difficulty:  big.NewInt(1),
			Number:      big.NewInt(2),
			GasLimit:    30_000_000,
			Time:        1_700_000_002,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rawBlock := mutateObject(t, bundle.RawBlock, func(fields map[string]json.RawMessage) {
		fields["_etherviewChainBundle"] = json.RawMessage(`{"provider":"collision"}`)
	})
	bundle, err = chainbundle.DecodeBlock(rawBlock, bundle.RawUncles)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err = bundle.WithReceipts(nil)
	if err != nil {
		t.Fatal(err)
	}
	if stored, err := chainbundle.EncodeStoredBlock(bundle); err == nil ||
		!errors.Is(err, chainbundle.ErrReservedStoredMetadata) ||
		!chainbundle.IsPermanent(err) ||
		stored != nil {
		t.Fatalf("EncodeStoredBlock() = %s, %v", stored, err)
	}
}

func TestStoredBlockRejectsReservedMetadataCollisionWithoutUncles(t *testing.T) {
	t.Parallel()
	bundle, err := testfixture.New(testfixture.Options{Number: 3})
	if err != nil {
		t.Fatal(err)
	}
	rawBlock := mutateObject(t, bundle.RawBlock, func(fields map[string]json.RawMessage) {
		fields["_etherviewChainBundle"] = json.RawMessage(`{"provider":"collision"}`)
	})
	bundle, err = chainbundle.DecodeBlock(rawBlock, nil)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err = bundle.WithReceipts(nil)
	if err != nil {
		t.Fatal(err)
	}
	if stored, err := chainbundle.EncodeStoredBlock(bundle); err == nil ||
		!errors.Is(err, chainbundle.ErrReservedStoredMetadata) ||
		!chainbundle.IsPermanent(err) ||
		stored != nil {
		t.Fatalf("EncodeStoredBlock() = %s, %v", stored, err)
	}
}

func TestValidateRejectsRawAlignmentSubstitution(t *testing.T) {
	t.Parallel()
	bundle, err := testfixture.New(testfixture.Options{
		Number:           4,
		TransactionTypes: []uint8{types.LegacyTxType},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle.RawTransactions[0] = append(json.RawMessage(nil), bundle.RawTransactions[0]...)
	bundle.RawTransactions[0] = append(bundle.RawTransactions[0], ' ')
	if err := chainbundle.Validate(bundle); err == nil {
		t.Fatal("Validate() accepted substituted raw transaction bytes")
	}
}

func TestValidateParentRejectsUint64Wraparound(t *testing.T) {
	t.Parallel()
	parent, err := testfixture.New(testfixture.Options{
		Number:    math.MaxUint64,
		Timestamp: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := testfixture.New(testfixture.Options{
		Number:     0,
		ParentHash: parent.Block.Hash(),
		Timestamp:  2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := chainbundle.ValidateParent(child, parent); err == nil {
		t.Fatal("ValidateParent() accepted uint64 wraparound")
	}
}

func mutateBlockTransaction(
	t *testing.T,
	raw json.RawMessage,
	index int,
	mutate func(map[string]json.RawMessage),
) json.RawMessage {
	t.Helper()
	return mutateObject(t, raw, func(fields map[string]json.RawMessage) {
		var transactions []json.RawMessage
		if err := json.Unmarshal(fields["transactions"], &transactions); err != nil {
			t.Fatal(err)
		}
		transactions[index] = mutateObject(t, transactions[index], mutate)
		fields["transactions"] = mustJSON(t, transactions)
	})
}

func assertDecodeBlockErrorWithoutPanic(
	t *testing.T,
	rawBlock json.RawMessage,
	errorSubstring string,
) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("DecodeBlock() panicked: %v", recovered)
		}
	}()
	decoded, err := chainbundle.DecodeBlock(rawBlock, nil)
	if err == nil || decoded.Block != nil {
		t.Fatalf("DecodeBlock() = %#v, %v", decoded, err)
	}
	if !strings.Contains(err.Error(), errorSubstring) {
		t.Fatalf("DecodeBlock() error = %v, want %q", err, errorSubstring)
	}
}

func mutateObject(
	t *testing.T,
	raw json.RawMessage,
	mutate func(map[string]json.RawMessage),
) json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	mutate(fields)
	return mustJSON(t, fields)
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func cloneRawMessages(values []json.RawMessage) []json.RawMessage {
	cloned := make([]json.RawMessage, len(values))
	for index := range values {
		cloned[index] = append(json.RawMessage(nil), values[index]...)
	}
	return cloned
}
