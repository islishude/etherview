// Command x402localfacilitator is a test-only exact-EVM facilitator for the
// disposable Anvil environment under e2e/x402local. It is built into a
// separate image and is never included in the production Etherview image.
package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	x402 "github.com/x402-foundation/x402/go/v2"
	"github.com/x402-foundation/x402/go/v2/mechanisms/evm"
)

const localSettlementABI = `[
  {"type":"function","name":"transferWithAuthorization","stateMutability":"nonpayable","inputs":[
    {"name":"from","type":"address"},{"name":"to","type":"address"},
    {"name":"value","type":"uint256"},{"name":"validAfter","type":"uint256"},
    {"name":"validBefore","type":"uint256"},{"name":"nonce","type":"bytes32"},
    {"name":"v","type":"uint8"},{"name":"r","type":"bytes32"},{"name":"s","type":"bytes32"}],"outputs":[]},
  {"type":"function","name":"settlePermit2","stateMutability":"nonpayable","inputs":[
    {"name":"from","type":"address"},{"name":"amount","type":"uint256"},
    {"name":"nonce","type":"uint256"},{"name":"deadline","type":"uint256"},
    {"name":"to","type":"address"},{"name":"validAfter","type":"uint256"},
    {"name":"signature","type":"bytes"}],"outputs":[]}
]`

type facilitatorRequest struct {
	X402Version         int                      `json:"x402Version"`
	PaymentPayload      x402.PaymentPayload      `json:"paymentPayload"`
	PaymentRequirements x402.PaymentRequirements `json:"paymentRequirements"`
}

type server struct {
	client  *ethclient.Client
	key     *ecdsa.PrivateKey
	address common.Address
	token   common.Address
	network string
	abi     abi.ABI
	mu      sync.Mutex
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--healthcheck" {
		client := &http.Client{Timeout: 2 * time.Second}
		response, err := client.Get("http://127.0.0.1:8081/healthz")
		if err != nil || response.StatusCode != http.StatusNoContent {
			os.Exit(1)
		}
		_ = response.Body.Close()
		return
	}
	rpcURL := requiredEnvironment("X402_LOCAL_RPC_URL")
	privateKey := requiredEnvironment("X402_LOCAL_FACILITATOR_PRIVATE_KEY")
	token := common.HexToAddress(requiredEnvironment("X402_LOCAL_TOKEN"))
	network := requiredEnvironment("X402_LOCAL_NETWORK")
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		log.Fatal("connect Anvil")
	}
	key, err := crypto.HexToECDSA(strings.TrimPrefix(privateKey, "0x"))
	if err != nil {
		log.Fatal("parse facilitator key")
	}
	parsedABI, err := abi.JSON(strings.NewReader(localSettlementABI))
	if err != nil {
		log.Fatal("parse local settlement ABI")
	}
	service := &server{
		client: client, key: key,
		address: crypto.PubkeyToAddress(key.PublicKey),
		token:   token, network: network, abi: parsedABI,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /supported", service.supported)
	mux.HandleFunc("POST /verify", service.verify)
	mux.HandleFunc("POST /settle", service.settle)
	httpServer := &http.Server{
		Addr: "0.0.0.0:8081", Handler: mux,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: 32 << 10,
	}
	log.Printf("test facilitator listening on %s", httpServer.Addr)
	log.Fatal(httpServer.ListenAndServe())
}

func (service *server) supported(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, x402.SupportedResponse{
		Kinds: []x402.SupportedKind{{
			X402Version: 2, Scheme: "exact", Network: service.network,
		}},
		Extensions: []string{},
		Signers:    map[string][]string{"eip155:*": {service.address.Hex()}},
	})
}

func (service *server) verify(writer http.ResponseWriter, request *http.Request) {
	value, payer, data, err := service.decode(request)
	if err != nil {
		log.Printf("verify rejected request: %v", err)
		writeJSON(writer, x402.VerifyResponse{
			IsValid: false, InvalidReason: "invalid_payload",
		})
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 8*time.Second)
	defer cancel()
	if _, err := service.client.CallContract(ctx, ethereum.CallMsg{
		From: service.address, To: &service.token, Data: data,
	}, nil); err != nil {
		log.Printf("verify simulation failed: %v", err)
		writeJSON(writer, x402.VerifyResponse{
			IsValid: false, InvalidReason: "simulation_failed", Payer: payer.Hex(),
		})
		return
	}
	_ = value
	writeJSON(writer, x402.VerifyResponse{IsValid: true, Payer: payer.Hex()})
}

func (service *server) settle(writer http.ResponseWriter, request *http.Request) {
	value, payer, data, err := service.decode(request)
	if err != nil {
		writeJSON(writer, x402.SettleResponse{
			Success: false, ErrorReason: "invalid_payload", Network: x402.Network(service.network),
		})
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 25*time.Second)
	defer cancel()
	hash, err := service.send(ctx, data)
	if err != nil {
		writeJSON(writer, x402.SettleResponse{
			Success: false, ErrorReason: "settlement_rejected", Payer: payer.Hex(),
			Network: x402.Network(service.network), Amount: value.String(),
		})
		return
	}
	writeJSON(writer, x402.SettleResponse{
		Success: true, Payer: payer.Hex(), Transaction: hash.Hex(),
		Network: x402.Network(service.network), Amount: value.String(),
	})
}

func (service *server) decode(request *http.Request) (*big.Int, common.Address, []byte, error) {
	const maxRequestBytes = 1 << 20
	body, err := io.ReadAll(io.LimitReader(request.Body, maxRequestBytes+1))
	if err != nil || len(body) > maxRequestBytes {
		return nil, common.Address{}, nil, errors.New("invalid request")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var input facilitatorRequest
	if err := decoder.Decode(&input); err != nil || input.X402Version != 2 ||
		input.PaymentPayload.X402Version != 2 {
		return nil, common.Address{}, nil, errors.New("invalid request")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, common.Address{}, nil, errors.New("invalid request")
	}
	if input.PaymentRequirements.Scheme != "exact" ||
		input.PaymentRequirements.Network != service.network ||
		!strings.EqualFold(input.PaymentRequirements.Asset, service.token.Hex()) ||
		input.PaymentRequirements.Amount == "" ||
		input.PaymentPayload.Accepted.Network != input.PaymentRequirements.Network ||
		!strings.EqualFold(input.PaymentPayload.Accepted.Asset, input.PaymentRequirements.Asset) ||
		input.PaymentPayload.Accepted.Amount != input.PaymentRequirements.Amount ||
		!strings.EqualFold(input.PaymentPayload.Accepted.PayTo, input.PaymentRequirements.PayTo) {
		return nil, common.Address{}, nil, errors.New("requirement mismatch")
	}
	amount, ok := new(big.Int).SetString(input.PaymentRequirements.Amount, 10)
	if !ok || amount.Sign() <= 0 {
		return nil, common.Address{}, nil, errors.New("invalid amount")
	}
	method, _ := input.PaymentRequirements.Extra["assetTransferMethod"].(string)
	flow, _ := input.PaymentRequirements.Extra["paymentFlow"].(string)
	if flow != "authorization" || (method != "eip3009" && method != "permit2") {
		return nil, common.Address{}, nil, errors.New("unsupported flow")
	}
	if method == "permit2" {
		return service.permit2Call(input.PaymentPayload, input.PaymentRequirements, amount)
	}
	return service.eip3009Call(input.PaymentPayload, input.PaymentRequirements, amount)
}

func (service *server) eip3009Call(
	payload x402.PaymentPayload,
	requirements x402.PaymentRequirements,
	amount *big.Int,
) (*big.Int, common.Address, []byte, error) {
	parsed, err := evm.PayloadFromMap(payload.Payload)
	if err != nil || parsed.Authorization.Value != amount.String() ||
		!strings.EqualFold(parsed.Authorization.To, requirements.PayTo) {
		return nil, common.Address{}, nil, errors.New("invalid EIP-3009 payload")
	}
	signature, err := decodeSignature(parsed.Signature)
	if err != nil {
		return nil, common.Address{}, nil, err
	}
	validAfter, okAfter := new(big.Int).SetString(parsed.Authorization.ValidAfter, 10)
	validBefore, okBefore := new(big.Int).SetString(parsed.Authorization.ValidBefore, 10)
	if !okAfter || !okBefore {
		return nil, common.Address{}, nil, errors.New("invalid authorization time")
	}
	nonceBytes, err := hex.DecodeString(strings.TrimPrefix(parsed.Authorization.Nonce, "0x"))
	if err != nil || len(nonceBytes) != 32 {
		return nil, common.Address{}, nil, errors.New("invalid authorization nonce")
	}
	var nonce, r, s [32]byte
	copy(nonce[:], nonceBytes)
	copy(r[:], signature[:32])
	copy(s[:], signature[32:64])
	data, err := service.abi.Pack(
		"transferWithAuthorization",
		common.HexToAddress(parsed.Authorization.From),
		common.HexToAddress(parsed.Authorization.To), amount, validAfter, validBefore,
		nonce, signature[64], r, s,
	)
	return amount, common.HexToAddress(parsed.Authorization.From), data, err
}

func (service *server) permit2Call(
	payload x402.PaymentPayload,
	requirements x402.PaymentRequirements,
	amount *big.Int,
) (*big.Int, common.Address, []byte, error) {
	parsed, err := evm.Permit2PayloadFromMap(payload.Payload)
	if err != nil || parsed.Permit2Authorization.Permitted.Amount != amount.String() ||
		!strings.EqualFold(parsed.Permit2Authorization.Permitted.Token, service.token.Hex()) ||
		!strings.EqualFold(parsed.Permit2Authorization.Spender, evm.X402ExactPermit2ProxyAddress) ||
		!strings.EqualFold(parsed.Permit2Authorization.Witness.To, requirements.PayTo) {
		return nil, common.Address{}, nil, errors.New("invalid Permit2 payload")
	}
	nonce, okNonce := new(big.Int).SetString(parsed.Permit2Authorization.Nonce, 10)
	deadline, okDeadline := new(big.Int).SetString(parsed.Permit2Authorization.Deadline, 10)
	validAfter, okAfter := new(big.Int).SetString(parsed.Permit2Authorization.Witness.ValidAfter, 10)
	signature, signatureErr := decodeSignature(parsed.Signature)
	if !okNonce || !okDeadline || !okAfter || signatureErr != nil {
		return nil, common.Address{}, nil, errors.New("invalid Permit2 authorization")
	}
	data, err := service.abi.Pack(
		"settlePermit2", common.HexToAddress(parsed.Permit2Authorization.From),
		amount, nonce, deadline, common.HexToAddress(parsed.Permit2Authorization.Witness.To),
		validAfter, signature,
	)
	return amount, common.HexToAddress(parsed.Permit2Authorization.From), data, err
}

func (service *server) send(ctx context.Context, data []byte) (common.Hash, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	chainID, err := service.client.ChainID(ctx)
	if err != nil {
		return common.Hash{}, err
	}
	nonce, err := service.client.PendingNonceAt(ctx, service.address)
	if err != nil {
		return common.Hash{}, err
	}
	tip, err := service.client.SuggestGasTipCap(ctx)
	if err != nil {
		return common.Hash{}, err
	}
	header, err := service.client.HeaderByNumber(ctx, nil)
	if err != nil {
		return common.Hash{}, err
	}
	fee := new(big.Int).Add(tip, big.NewInt(2_000_000_000))
	if header.BaseFee != nil {
		fee = new(big.Int).Add(tip, new(big.Int).Mul(header.BaseFee, big.NewInt(2)))
	}
	gas, err := service.client.EstimateGas(ctx, ethereum.CallMsg{
		From: service.address, To: &service.token, Data: data,
	})
	if err != nil {
		return common.Hash{}, err
	}
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID: chainID, Nonce: nonce, GasTipCap: tip, GasFeeCap: fee,
		Gas: gas + gas/5, To: &service.token, Data: data,
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), service.key)
	if err != nil {
		return common.Hash{}, err
	}
	if err := service.client.SendTransaction(ctx, signed); err != nil {
		return common.Hash{}, err
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		receipt, receiptErr := service.client.TransactionReceipt(ctx, signed.Hash())
		if receiptErr == nil {
			if receipt.Status != types.ReceiptStatusSuccessful {
				return common.Hash{}, errors.New("settlement reverted")
			}
			return signed.Hash(), nil
		}
		if !errors.Is(receiptErr, ethereum.NotFound) {
			return common.Hash{}, receiptErr
		}
		select {
		case <-ctx.Done():
			return common.Hash{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func decodeSignature(raw string) ([]byte, error) {
	value, err := hex.DecodeString(strings.TrimPrefix(raw, "0x"))
	if err != nil || len(value) != 65 {
		return nil, errors.New("invalid signature")
	}
	if value[64] >= 27 {
		value[64] -= 27
	}
	if value[64] > 1 {
		return nil, errors.New("invalid signature recovery ID")
	}
	return value, nil
}

func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(value)
}

func requiredEnvironment(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		log.Fatalf("%s is required", name)
	}
	return value
}
