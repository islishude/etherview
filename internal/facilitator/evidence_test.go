package facilitator

import (
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	x402 "github.com/x402-foundation/x402/go/v2"
	exactclient "github.com/x402-foundation/x402/go/v2/mechanisms/evm/exact/client"
	evmsigners "github.com/x402-foundation/x402/go/v2/signers/evm"
)

func proofRequest(t *testing.T, method string) paymentRequest {
	t.Helper()
	req := testRequest().PaymentRequirements
	req.Extra["assetTransferMethod"] = method
	req.Extra["name"] = "Local USD"
	req.Extra["version"] = "1"
	signer, err := evmsigners.NewClientSignerFromPrivateKey("0x" + strings.Repeat("01", 32))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := exactclient.NewExactEvmScheme(signer, nil).CreatePaymentPayload(t.Context(), req, x402.PaymentPayloadContext{})
	if err != nil {
		t.Fatal(err)
	}
	payload.Accepted = req
	return paymentRequest{X402Version: 2, PaymentPayload: payload, PaymentRequirements: req}
}

func TestExternalEvidenceBindsAuthorizationAndCanonicalTransfer(t *testing.T) {
	for _, method := range []string{"eip3009", "permit2"} {
		t.Run(method, func(t *testing.T) {
			in := proofRequest(t, method)
			proof, err := proofFor(in)
			if err != nil {
				t.Fatal(err)
			}
			chain, _ := networkID(in.PaymentRequirements.Network)
			key, err := crypto.HexToECDSA(strings.Repeat("02", 32))
			if err != nil {
				t.Fatal(err)
			}
			tx, err := types.SignTx(types.NewTx(&types.DynamicFeeTx{ChainID: chain, To: &proof.target, Gas: 100000, GasFeeCap: big.NewInt(2), GasTipCap: big.NewInt(1), Data: proof.calldata[0]}), types.LatestSignerForChainID(chain), key)
			if err != nil {
				t.Fatal(err)
			}
			header := &types.Header{Number: big.NewInt(1), Time: uint64(time.Now().Unix() + 1), GasLimit: 30000000, BaseFee: big.NewInt(1), Difficulty: big.NewInt(0)}
			transfer := &types.Log{Address: proof.token, TxHash: tx.Hash(), BlockHash: header.Hash(), BlockNumber: 1, Topics: []common.Hash{transferTopic, common.BytesToHash(proof.payer.Bytes()), common.BytesToHash(proof.recipient.Bytes())}, Data: common.LeftPadBytes(proof.amount.Bytes(), 32)}
			event := &types.Log{Address: proof.token, TxHash: tx.Hash(), BlockHash: header.Hash(), BlockNumber: 1, Index: 1, Topics: []common.Hash{authorizationUsedTopic, common.BytesToHash(proof.payer.Bytes()), proof.nonceBytes}}
			if method == "permit2" {
				event.Address = proof.target
				event.Topics = []common.Hash{settledTopic}
			}
			receipt := &types.Receipt{Type: 2, Status: 1, TxHash: tx.Hash(), BlockNumber: big.NewInt(1), BlockHash: header.Hash(), Logs: []*types.Log{transfer, event}, CumulativeGasUsed: 100000, GasUsed: 100000}
			noncanonical := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					t.Error("bad RPC")
					return
				}
				var result any
				switch request.Method {
				case "eth_getTransactionByHash":
					body, _ := tx.MarshalJSON()
					var wire map[string]any
					_ = json.Unmarshal(body, &wire)
					wire["blockHash"] = header.Hash()
					wire["blockNumber"] = "0x1"
					wire["transactionIndex"] = "0x0"
					result = wire
				case "eth_getTransactionReceipt":
					result = receipt
				case "eth_blockNumber":
					result = hexutil.Uint64(1)
				case "eth_getBlockByNumber":
					copy := *header
					if noncanonical {
						copy.Extra = []byte{1}
					}
					result = &copy
				default:
					t.Error("unexpected RPC", request.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
			}))
			defer server.Close()
			client, err := dialRPC(t.Context(), server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			ch := &chainRuntime{adapter: &chainClient{client: client, config: ChainConfig{Confirmations: 1}}}
			evidence, err := externalEvidence(t.Context(), ch, in, proof, tx.Hash())
			if err != nil || evidence == nil || evidence.Transaction != tx.Hash().Hex() || evidence.Source != "external" {
				t.Fatal(evidence, err)
			}
			receipt.Logs = []*types.Log{transfer}
			if e, err := externalEvidence(t.Context(), ch, in, proof, tx.Hash()); err != nil || e != nil {
				t.Fatal("similar Transfer without authorization accepted", e, err)
			}
			receipt.Logs = []*types.Log{transfer, event}
			transfer.Removed = true
			if e, err := externalEvidence(t.Context(), ch, in, proof, tx.Hash()); err != nil || e != nil {
				t.Fatal("removed transfer accepted", e, err)
			}
			transfer.Removed = false
			in.PaymentRequirements.Amount = "101"
			if e, err := externalEvidence(t.Context(), ch, in, proof, tx.Hash()); err != nil || e != nil {
				t.Fatal("wrong amount accepted", e, err)
			}
			in.PaymentRequirements.Amount = "100"
			noncanonical = true
			if e, err := externalEvidence(t.Context(), ch, in, proof, tx.Hash()); err == nil || e != nil {
				t.Fatal("orphan accepted", e, err)
			}
		})
	}
}
