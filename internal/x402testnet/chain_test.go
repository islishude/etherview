package x402testnet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

const testBlockHash = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

type fakeSettlementChain struct {
	chainIDResult     string
	transactionResult *chainTransaction
	receiptResult     *chainReceipt
	chainIDErr        error
	transactionErr    error
	receiptErr        error
	chainIDCalls      int
	transactionCalls  int
	receiptCalls      int
}

func (chain *fakeSettlementChain) chainID(
	context.Context,
) (string, error) {
	chain.chainIDCalls++
	return chain.chainIDResult, chain.chainIDErr
}

func (chain *fakeSettlementChain) transaction(
	context.Context,
	string,
) (*chainTransaction, error) {
	chain.transactionCalls++
	return chain.transactionResult, chain.transactionErr
}

func (chain *fakeSettlementChain) receipt(
	context.Context,
	string,
) (*chainReceipt, error) {
	chain.receiptCalls++
	return chain.receiptResult, chain.receiptErr
}

func TestCheckChainIDRequiresBaseSepolia(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		result   string
		expected uint64
		rpcErr   error
		wantCode string
	}{
		{
			name: "base sepolia", result: "0x14a34",
			expected: baseSepoliaChainID,
		},
		{
			name: "wrong chain", result: "0x1",
			expected: baseSepoliaChainID, wantCode: "chain_id_mismatch",
		},
		{
			name: "non canonical response", result: "0x014a34",
			expected: baseSepoliaChainID, wantCode: "chain_response_invalid",
		},
		{
			name: "uppercase response", result: "0xA",
			expected: baseSepoliaChainID, wantCode: "chain_response_invalid",
		},
		{
			name: "rpc secret is redacted", result: "0x14a34",
			expected: baseSepoliaChainID,
			rpcErr:   errors.New("https://secret@rpc.invalid/token"),
			wantCode: "chain_unavailable",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			chain := &fakeSettlementChain{
				chainIDResult: test.result,
				chainIDErr:    test.rpcErr,
			}
			err := checkChainID(
				context.Background(),
				chain,
				test.expected,
			)
			if got := ErrorCode(err); got != test.wantCode {
				t.Fatalf("ErrorCode() = %q, want %q", got, test.wantCode)
			}
		})
	}
}

func TestRPCWireHexRequiresCanonicalLowercase(t *testing.T) {
	t.Parallel()
	if quantity, ok := parseHexQuantity("0xa"); !ok ||
		quantity.Uint64() != 10 {
		t.Fatal("canonical lowercase quantity was rejected")
	}
	if _, ok := parseHexQuantity("0xA"); ok {
		t.Fatal("uppercase quantity was accepted")
	}
	if _, ok := parseTransactionHash(
		"0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	); ok {
		t.Fatal("uppercase transaction hash was accepted")
	}
	if _, ok := parseRPCAddress(
		"0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	); ok {
		t.Fatal("uppercase RPC address was accepted")
	}
	if _, ok := decodeFixedHex(
		"0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		32,
	); ok {
		t.Fatal("uppercase RPC data word was accepted")
	}
}

func TestVerifyChainAcceptsSuccessfulReceiptAndAggregateTransfers(t *testing.T) {
	t.Parallel()
	expected := validChainExpectation(t)
	chain := validFakeChain()
	chain.receiptResult.Logs = []chainLog{
		transferLog("400", testPayer, testRecipient),
		transferLog("600", testPayer, testRecipient),
		transferLog("7", testRecipient, testPayer),
	}
	evidence, err := verifyChain(context.Background(), chain, expected)
	if err != nil {
		t.Fatalf("verifyChain() error = %v", err)
	}
	if evidence.TransactionHash != testTxHash ||
		evidence.BlockHash != testBlockHash ||
		evidence.BlockNumber != "16" ||
		evidence.TransferCount != 2 {
		t.Fatalf("unexpected evidence: %+v", evidence)
	}
	if chain.chainIDCalls != 1 ||
		chain.transactionCalls != 1 ||
		chain.receiptCalls != 1 {
		t.Fatalf(
			"unexpected calls: chain=%d tx=%d receipt=%d",
			chain.chainIDCalls,
			chain.transactionCalls,
			chain.receiptCalls,
		)
	}
}

func TestVerifyChainRejectsFailedReceiptAndTransferMismatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		mutate   func(*fakeSettlementChain)
		wantCode string
	}{
		{
			name: "failed receipt",
			mutate: func(chain *fakeSettlementChain) {
				chain.receiptResult.Status = "0x0"
			},
			wantCode: "chain_receipt_failed",
		},
		{
			name: "amount mismatch",
			mutate: func(chain *fakeSettlementChain) {
				chain.receiptResult.Logs[0].Data = dataWord("999")
			},
			wantCode: "chain_transfer_mismatch",
		},
		{
			name: "recipient mismatch",
			mutate: func(chain *fakeSettlementChain) {
				chain.receiptResult.Logs[0].Topics[2] = addressTopic(testPayer)
			},
			wantCode: "chain_transfer_mismatch",
		},
		{
			name: "transaction mismatch",
			mutate: func(chain *fakeSettlementChain) {
				chain.transactionResult.Hash = testBlockHash
			},
			wantCode: "chain_transaction_mismatch",
		},
		{
			name: "settlement target mismatch",
			mutate: func(chain *fakeSettlementChain) {
				wrong := testRecipient
				chain.transactionResult.To = &wrong
			},
			wantCode: "chain_transaction_mismatch",
		},
		{
			name: "settlement value mismatch",
			mutate: func(chain *fakeSettlementChain) {
				chain.transactionResult.Value = "0x1"
			},
			wantCode: "chain_transaction_mismatch",
		},
		{
			name: "settlement calldata mismatch",
			mutate: func(chain *fakeSettlementChain) {
				input, ok := decodeVariableRPCData(
					chain.transactionResult.Input,
					maxSettlementInputBytes,
				)
				if !ok {
					t.Fatal("decode test calldata")
				}
				input[4] ^= 0xff
				chain.transactionResult.Input =
					"0x" + hex.EncodeToString(input)
			},
			wantCode: "chain_transaction_mismatch",
		},
		{
			name: "receipt block mismatch",
			mutate: func(chain *fakeSettlementChain) {
				chain.receiptResult.BlockNumber = "0x11"
			},
			wantCode: "chain_receipt_mismatch",
		},
		{
			name: "pending transaction",
			mutate: func(chain *fakeSettlementChain) {
				chain.transactionResult.BlockHash = nil
			},
			wantCode: "chain_transaction_pending",
		},
		{
			name: "uppercase log address",
			mutate: func(chain *fakeSettlementChain) {
				chain.receiptResult.Logs[0].Address =
					"0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
			},
			wantCode: "chain_response_invalid",
		},
		{
			name: "uppercase log topic",
			mutate: func(chain *fakeSettlementChain) {
				chain.receiptResult.Logs[0].Topics[0] =
					"0xDDF252AD1BE2C89B69C2B068FC378DAA952BA7F163C4A11628F55A4DF523B3EF"
			},
			wantCode: "chain_response_invalid",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			chain := validFakeChain()
			test.mutate(chain)
			_, err := verifyChain(
				context.Background(),
				chain,
				validChainExpectation(t),
			)
			if got := ErrorCode(err); got != test.wantCode {
				t.Fatalf("ErrorCode() = %q, want %q", got, test.wantCode)
			}
		})
	}
}

func TestVerifyChainRejectsMalformedTransferLog(t *testing.T) {
	t.Parallel()
	chain := validFakeChain()
	chain.receiptResult.Logs[0].Topics[1] = "0x01"
	_, err := verifyChain(
		context.Background(),
		chain,
		validChainExpectation(t),
	)
	if got := ErrorCode(err); got != "chain_response_invalid" {
		t.Fatalf("ErrorCode() = %q", got)
	}
}

func TestChainHTTPClientIsRestricted(t *testing.T) {
	t.Parallel()
	if validChainRPCURL("http://rpc.example") ||
		validChainRPCURL("https://user:secret@rpc.example") ||
		validChainRPCURL("https://rpc.example/#fragment") ||
		!validChainRPCURL("https://rpc.example/v1/token") {
		t.Fatal("unexpected RPC URL validation")
	}
	client := newChainHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("chain RPC transport retained proxy discovery")
	}
	if transport.MaxResponseHeaderBytes !=
		maxChainRPCResponseHeaderBytes ||
		transport.MaxConnsPerHost != 2 ||
		transport.MaxIdleConns != 2 ||
		transport.MaxIdleConnsPerHost != 1 {
		t.Fatalf(
			"unexpected transport bounds: headers=%d conns=%d idle=%d idle_host=%d",
			transport.MaxResponseHeaderBytes,
			transport.MaxConnsPerHost,
			transport.MaxIdleConns,
			transport.MaxIdleConnsPerHost,
		)
	}
	request, err := http.NewRequest(
		http.MethodPost,
		"https://rpc.example",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(request, nil); !errors.Is(
		err,
		http.ErrUseLastResponse,
	) {
		t.Fatalf("CheckRedirect() error = %v", err)
	}
}

func validChainExpectation(t *testing.T) chainExpectation {
	t.Helper()
	callData := testSettlementCallData()
	result, ok := parseChainExpectation(ChainOptions{
		RPCURL:  "https://rpc.example/v1/token",
		ChainID: baseSepoliaChainID, TransactionHash: testTxHash,
		Asset: testAsset, AmountAtomic: "1000",
		Recipient: testRecipient, Payer: testPayer,
		CallDataPrefixBytes:  len(callData),
		CallDataPrefixSHA256: sha256.Sum256(callData),
	})
	if !ok {
		t.Fatal("valid chain expectation did not parse")
	}
	return result
}

func validFakeChain() *fakeSettlementChain {
	blockHash := testBlockHash
	blockNumber := "0x10"
	to := testAsset
	input := append(testSettlementCallData(), 0xde, 0xad, 0xbe, 0xef)
	return &fakeSettlementChain{
		chainIDResult: "0x14a34",
		transactionResult: &chainTransaction{
			Hash: testTxHash, BlockHash: &blockHash,
			BlockNumber: &blockNumber, ChainID: "0x14a34",
			To: &to, Value: "0x0",
			Input: "0x" + hex.EncodeToString(input),
		},
		receiptResult: &chainReceipt{
			TransactionHash: testTxHash, BlockHash: testBlockHash,
			BlockNumber: "0x10", Status: "0x1",
			Logs: []chainLog{
				transferLog("1000", testPayer, testRecipient),
			},
		},
	}
}

func testSettlementCallData() []byte {
	result := make([]byte, expectedSettlementCallBytes)
	for index := range result {
		result[index] = byte(index)
	}
	return result
}

func transferLog(amount, from, to string) chainLog {
	return chainLog{
		Address: testAsset,
		Topics: []string{
			erc20TransferTopic,
			addressTopic(from),
			addressTopic(to),
		},
		Data:      dataWord(amount),
		BlockHash: testBlockHash, BlockNumber: "0x10",
		TransactionHash: testTxHash,
	}
}

func addressTopic(address string) string {
	return "0x000000000000000000000000" + address[2:]
}

func dataWord(amount string) string {
	value, ok := parseAmount(amount)
	if !ok {
		panic("invalid test amount")
	}
	return fmt.Sprintf("0x%064x", value)
}
