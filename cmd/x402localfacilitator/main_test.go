package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	x402 "github.com/x402-foundation/x402/go/v2"
)

type receiptRPCError struct {
	code    int
	message string
}

func TestSettlementBroadcastBoundary(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		reason    string
		broadcast bool
	}{
		{"success", "", true},
		{"indexing", "", true},
		{"estimate rejection", "settlement_rejected", false},
		{"reverted", "settlement_rejected", true},
		{"broadcast uncertainty", "settlement_pending", true},
		{"receipt failure", "settlement_pending", true},
		{"receipt mismatch", "settlement_pending", true},
		{"receipt timeout", "settlement_pending", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var sends, receipts atomic.Uint32
			var broadcastHash atomic.Value
			rpcServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input struct {
					ID     json.RawMessage   `json:"id"`
					Method string            `json:"method"`
					Params []json.RawMessage `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
					return
				}
				var result any
				var rpcError any
				switch input.Method {
				case "eth_chainId":
					result = "0xbeaf"
				case "eth_getTransactionCount":
					result = "0x0"
				case "eth_maxPriorityFeePerGas":
					result = "0x1"
				case "eth_getBlockByNumber":
					result = &types.Header{Number: big.NewInt(1), Difficulty: big.NewInt(0), BaseFee: big.NewInt(1), GasLimit: 30_000_000}
				case "eth_estimateGas":
					result = "0x186a0"
					if scenario.name == "estimate rejection" {
						rpcError = map[string]any{"code": -32000, "message": "execution reverted"}
					}
				case "eth_sendRawTransaction":
					sends.Add(1)
					var raw hexutil.Bytes
					if err := json.Unmarshal(input.Params[0], &raw); err != nil {
						t.Error(err)
						return
					}
					var tx types.Transaction
					if err := tx.UnmarshalBinary(raw); err != nil {
						t.Error(err)
						return
					}
					if tx.GasTipCap().Cmp(big.NewInt(1_000_000_000)) < 0 {
						t.Error("tip below Geth builder floor")
					}
					broadcastHash.Store(tx.Hash())
					result = tx.Hash()
					if scenario.name == "broadcast uncertainty" {
						rpcError = map[string]any{"code": -32000, "message": "upstream unavailable"}
					}
				case "eth_getTransactionReceipt":
					attempt := receipts.Add(1)
					hash := broadcastHash.Load().(common.Hash)
					status := uint64(types.ReceiptStatusSuccessful)
					if scenario.name == "reverted" {
						status = types.ReceiptStatusFailed
					}
					if scenario.name == "receipt mismatch" {
						hash = common.HexToHash("0x1234")
					}
					result = &types.Receipt{TxHash: hash, Status: status, Logs: []*types.Log{}, BlockNumber: big.NewInt(1)}
					if scenario.name == "receipt timeout" {
						result = nil
					}
					if scenario.name == "receipt failure" {
						rpcError = map[string]any{"code": -32001, "message": "upstream unavailable"}
					}
					if scenario.name == "indexing" && attempt == 1 {
						rpcError = map[string]any{"code": -32000, "message": "transaction indexing is in progress"}
					}
				default:
					t.Errorf("unexpected RPC %s", input.Method)
				}
				response := map[string]any{"jsonrpc": "2.0", "id": input.ID}
				if rpcError != nil {
					response["error"] = rpcError
				} else {
					response["result"] = result
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Error(err)
				}
			}))
			defer rpcServer.Close()
			client, err := ethclient.Dial(rpcServer.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			key, err := crypto.GenerateKey()
			if err != nil {
				t.Fatal(err)
			}
			codec, err := abi.JSON(strings.NewReader(localSettlementABI))
			if err != nil {
				t.Fatal(err)
			}
			service := &server{client: client, key: key, address: crypto.PubkeyToAddress(key.PublicKey), token: common.HexToAddress("0x1234"), network: "eip155:48815", abi: codec}
			requirements := x402.PaymentRequirements{Scheme: "exact", Network: service.network, Asset: service.token.Hex(), Amount: "100", PayTo: service.address.Hex(), Extra: map[string]any{"assetTransferMethod": "eip3009", "paymentFlow": "authorization"}}
			payload := x402.PaymentPayload{X402Version: 2, Accepted: requirements, Payload: map[string]any{
				"signature":     "0x" + strings.Repeat("01", 64) + "00",
				"authorization": map[string]string{"from": service.address.Hex(), "to": requirements.PayTo, "value": "100", "validAfter": "1", "validBefore": "9999999999", "nonce": "0x" + strings.Repeat("00", 32)},
			}}
			body, err := json.Marshal(facilitatorRequest{X402Version: 2, PaymentPayload: payload, PaymentRequirements: requirements})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/settle", bytes.NewReader(body))
			writer := httptest.NewRecorder()
			service.settle(writer, request)
			var response x402.SettleResponse
			if err := json.Unmarshal(writer.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.ErrorReason != scenario.reason || response.Success != (scenario.reason == "") {
				t.Fatalf("response=%+v", response)
			}
			if sends.Load() > 1 || (sends.Load() == 1) != scenario.broadcast {
				t.Fatalf("broadcast count=%d", sends.Load())
			}
			if scenario.broadcast && scenario.reason != "settlement_rejected" {
				if response.Transaction != broadcastHash.Load().(common.Hash).Hex() {
					t.Fatalf("lost or changed broadcast hash: %+v", response)
				}
			} else if response.Transaction != "" {
				t.Fatalf("rejected settlement has hash: %+v", response)
			}
		})
	}
}

func (e receiptRPCError) Error() string  { return e.message }
func (e receiptRPCError) ErrorCode() int { return e.code }

func TestReceiptPending(t *testing.T) {
	for _, test := range []struct {
		err     error
		pending bool
	}{
		{ethereum.NotFound, true},
		{receiptRPCError{-32000, "transaction indexing is in progress"}, true},
		{receiptRPCError{-32001, "transaction indexing is in progress"}, false},
		{receiptRPCError{-32000, "execution reverted"}, false},
		{errors.New("transaction indexing is in progress"), false},
	} {
		if receiptPending(test.err) != test.pending {
			t.Fatalf("pending(%v) != %v", test.err, test.pending)
		}
	}
}
