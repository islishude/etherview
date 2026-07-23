// Command runtimefixture serves a deterministic Ethereum JSON-RPC fixture for
// the Docker Compose runtime parity smoke test. It is built into a test-only
// image target and is never copied into the production Etherview image.
package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	chainID = "0x1"

	zeroHash        = "0x0000000000000000000000000000000000000000000000000000000000000000"
	genesisHash     = "0x0000000000000000000000000000000000000000000000000000000000000100"
	blockOneHash    = "0x0000000000000000000000000000000000000000000000000000000000000101"
	blockTwoHash    = "0x0000000000000000000000000000000000000000000000000000000000000102"
	transactionHash = "0x0000000000000000000000000000000000000000000000000000000000000201"
	pendingHash     = "0x0000000000000000000000000000000000000000000000000000000000000301"

	fromAddress    = "0x00000000000000000000000000000000000000a1"
	toAddress      = "0x00000000000000000000000000000000000000b2"
	pendingAddress = "0x00000000000000000000000000000000000000c3"
)

type rpcRequest struct {
	JSONRPC string            `json:"jsonrpc"`
	ID      json.RawMessage   `json:"id"`
	Method  string            `json:"method"`
	Params  []json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type fixture struct {
	blocksByNumber map[string]map[string]any
	blocksByHash   map[string]map[string]any
	receipts       map[string][]map[string]any
	head           atomic.Uint64
}

func main() {
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	var err error
	switch command {
	case "serve":
		err = serve(os.Args[2:])
	case "healthcheck":
		err = healthcheck(os.Args[2:])
	case "advance":
		err = advance(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", command)
	}
	if err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func serve(arguments []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	address := flags.String("address", "0.0.0.0:8545", "HTTP listen address")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	handler := newFixture()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{\"status\":\"ready\"}\n")
	})
	mux.Handle("POST /", handler)
	mux.Handle("POST /advance", handler)
	server := &http.Server{
		Addr:              *address,
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() {
		log.Printf("runtime fixture listening on %s", *address)
		done <- server.ListenAndServe()
	}()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

func healthcheck(arguments []string) error {
	flags := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	rawURL := flags.String("url", "http://127.0.0.1:8545/health", "fixture health URL")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(*rawURL)
	if err != nil {
		return fmt.Errorf("request fixture health: %w", err)
	}
	defer response.Body.Close() //nolint:errcheck
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("fixture health returned HTTP %d", response.StatusCode)
	}
	return nil
}

func advance(arguments []string) error {
	flags := flag.NewFlagSet("advance", flag.ContinueOnError)
	rawURL := flags.String("url", "http://127.0.0.1:8545/advance", "fixture advance URL")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPost, *rawURL, nil)
	if err != nil {
		return fmt.Errorf("create fixture advance request: %w", err)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("advance fixture head: %w", err)
	}
	defer response.Body.Close() //nolint:errcheck
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("fixture advance returned HTTP %d", response.StatusCode)
	}
	return nil
}

func newFixture() *fixture {
	genesis := minedBlock("0x0", genesisHash, zeroHash, "0x6553f100", nil)
	transaction := minedTransaction()
	blockOne := minedBlock("0x1", blockOneHash, genesisHash, "0x6553f10c", []map[string]any{transaction})
	blockTwo := minedBlock("0x2", blockTwoHash, blockOneHash, "0x6553f118", nil)
	receipt := map[string]any{
		"transactionHash":   transactionHash,
		"transactionIndex":  "0x0",
		"blockHash":         blockOneHash,
		"blockNumber":       "0x1",
		"from":              fromAddress,
		"to":                toAddress,
		"cumulativeGasUsed": "0x5208",
		"gasUsed":           "0x5208",
		"logs":              []any{},
		"logsBloom":         "0x",
		"status":            "0x1",
		"type":              "0x0",
		"effectiveGasPrice": "0x3b9aca00",
	}
	result := &fixture{
		blocksByNumber: map[string]map[string]any{"0x0": genesis, "0x1": blockOne, "0x2": blockTwo},
		blocksByHash: map[string]map[string]any{
			strings.ToLower(genesisHash):  genesis,
			strings.ToLower(blockOneHash): blockOne,
			strings.ToLower(blockTwoHash): blockTwo,
		},
		receipts: map[string][]map[string]any{
			"0x0":                         {},
			"0x1":                         {receipt},
			"0x2":                         {},
			strings.ToLower(genesisHash):  {},
			strings.ToLower(blockOneHash): {receipt},
			strings.ToLower(blockTwoHash): {},
		},
	}
	result.head.Store(1)
	return result
}

func minedBlock(number, hash, parentHash, timestamp string, transactions []map[string]any) map[string]any {
	if transactions == nil {
		transactions = []map[string]any{}
	}
	gasUsed := "0x0"
	if len(transactions) > 0 {
		gasUsed = "0x5208"
	}
	return map[string]any{
		"number":           number,
		"hash":             hash,
		"parentHash":       parentHash,
		"nonce":            "0x0000000000000000",
		"sha3Uncles":       zeroHash,
		"logsBloom":        "0x",
		"transactionsRoot": zeroHash,
		"stateRoot":        zeroHash,
		"receiptsRoot":     zeroHash,
		"miner":            fromAddress,
		"difficulty":       "0x0",
		"totalDifficulty":  "0x0",
		"extraData":        "0x",
		"size":             "0x1",
		"gasLimit":         "0x1c9c380",
		"gasUsed":          gasUsed,
		"timestamp":        timestamp,
		"baseFeePerGas":    "0x3b9aca00",
		"transactions":     transactions,
		"uncles":           []any{},
		"withdrawals":      []any{},
	}
}

func minedTransaction() map[string]any {
	return map[string]any{
		"hash":             transactionHash,
		"type":             "0x0",
		"blockHash":        blockOneHash,
		"blockNumber":      "0x1",
		"transactionIndex": "0x0",
		"from":             fromAddress,
		"to":               toAddress,
		"nonce":            "0x0",
		"gas":              "0x5208",
		"gasPrice":         "0x3b9aca00",
		"value":            "0x5",
		"input":            "0x1234",
		"chainId":          chainID,
		"v":                "0x25",
		"r":                "0x1",
		"s":                "0x2",
	}
}

func pendingBlock(parentHash string) map[string]any {
	return map[string]any{
		"parentHash":       parentHash,
		"sha3Uncles":       zeroHash,
		"transactionsRoot": zeroHash,
		"stateRoot":        zeroHash,
		"receiptsRoot":     zeroHash,
		"extraData":        "0x",
		"gasLimit":         "0x1c9c380",
		"gasUsed":          "0x0",
		"timestamp":        "0x6553f118",
		"transactions": []map[string]any{{
			"hash":                 pendingHash,
			"type":                 "0x2",
			"from":                 pendingAddress,
			"to":                   toAddress,
			"nonce":                "0x7",
			"gas":                  "0x5208",
			"maxPriorityFeePerGas": "0x3b9aca00",
			"maxFeePerGas":         "0x77359400",
			"value":                "0x9",
			"input":                "0x",
			"chainId":              chainID,
			"yParity":              "0x0",
			"r":                    "0x3",
			"s":                    "0x4",
		}},
		"uncles": []any{},
	}
}

func (f *fixture) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/advance" {
		f.head.Store(2)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{\"head\":\"0x2\"}\n")
		return
	}
	request.Body = http.MaxBytesReader(w, request.Body, 1<<20)
	data, err := io.ReadAll(request.Body)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	data = bytes.TrimSpace(data)
	w.Header().Set("Content-Type", "application/json")
	if len(data) > 0 && data[0] == '[' {
		var batch []rpcRequest
		if err := json.Unmarshal(data, &batch); err != nil || len(batch) == 0 {
			writeRPCResponse(w, errorResponse(nil, -32600, "invalid request"))
			return
		}
		responses := make([]map[string]any, len(batch))
		for index := range batch {
			responses[index] = f.response(batch[index])
		}
		writeRPCResponse(w, responses)
		return
	}
	var rpcRequest rpcRequest
	if err := json.Unmarshal(data, &rpcRequest); err != nil {
		writeRPCResponse(w, errorResponse(nil, -32700, "parse error"))
		return
	}
	writeRPCResponse(w, f.response(rpcRequest))
}

func (f *fixture) response(request rpcRequest) map[string]any {
	if request.JSONRPC != "2.0" || len(request.ID) == 0 || request.Method == "" {
		return errorResponse(request.ID, -32600, "invalid request")
	}
	result, rpcErr := f.call(request.Method, request.Params)
	if rpcErr != nil {
		return map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": rpcErr}
	}
	return map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}
}

func (f *fixture) call(method string, params []json.RawMessage) (any, *rpcError) {
	switch method {
	case "eth_chainId":
		return chainID, nil
	case "eth_blockNumber":
		return f.headTag(), nil
	case "rpc_modules":
		return map[string]string{"debug": "1.0", "eth": "1.0", "txpool": "1.0"}, nil
	case "eth_getBlockByNumber":
		tag, ok := stringParam(params, 0)
		if !ok {
			return nil, invalidParams()
		}
		if tag == "pending" {
			parentHash := blockOneHash
			if f.head.Load() >= 2 {
				parentHash = blockTwoHash
			}
			return pendingBlock(parentHash), nil
		}
		switch tag {
		case "latest", "safe", "finalized":
			tag = f.headTag()
		}
		if tag == "0x2" && f.head.Load() < 2 {
			return nil, nil
		}
		block := f.blocksByNumber[tag]
		if block == nil {
			return nil, nil
		}
		full, _ := boolParam(params, 1)
		return blockResponse(block, full), nil
	case "eth_getBlockByHash":
		hash, ok := stringParam(params, 0)
		if !ok {
			return nil, invalidParams()
		}
		if strings.EqualFold(hash, blockTwoHash) && f.head.Load() < 2 {
			return nil, nil
		}
		block := f.blocksByHash[strings.ToLower(hash)]
		if block == nil {
			return nil, nil
		}
		full, _ := boolParam(params, 1)
		return blockResponse(block, full), nil
	case "eth_getBlockReceipts":
		identity, ok := stringParam(params, 0)
		if !ok {
			return nil, invalidParams()
		}
		if (identity == "0x2" || strings.EqualFold(identity, blockTwoHash)) && f.head.Load() < 2 {
			return nil, nil
		}
		receipts, exists := f.receipts[strings.ToLower(identity)]
		if !exists {
			return nil, nil
		}
		return receipts, nil
	case "eth_getTransactionReceipt":
		hash, ok := stringParam(params, 0)
		if !ok {
			return nil, invalidParams()
		}
		if strings.EqualFold(hash, transactionHash) {
			return f.receipts[strings.ToLower(blockOneHash)][0], nil
		}
		return nil, nil
	case "eth_getBalance":
		if !validAddressParam(params, 0) || !f.validExactBlockSelector(params, 1) || len(params) != 2 {
			return nil, invalidParams()
		}
		return "0x5", nil
	case "eth_getTransactionCount":
		if !validAddressParam(params, 0) || !f.validExactBlockSelector(params, 1) || len(params) != 2 {
			return nil, invalidParams()
		}
		return "0x1", nil
	case "eth_getCode":
		if !validAddressParam(params, 0) || !f.validExactBlockSelector(params, 1) || len(params) != 2 {
			return nil, invalidParams()
		}
		return "0x", nil
	case "eth_getStorageAt":
		if !validAddressParam(params, 0) || !validStringParam(params, 1) ||
			!f.validExactBlockSelector(params, 2) || len(params) != 3 {
			return nil, invalidParams()
		}
		return zeroHash, nil
	case "eth_call":
		if !validObjectParam(params, 0) || !f.validExactBlockSelector(params, 1) || len(params) != 2 {
			return nil, invalidParams()
		}
		return "0x", nil
	case "debug_traceTransaction":
		hash, ok := stringParam(params, 0)
		if !ok || !strings.EqualFold(hash, transactionHash) {
			return nil, invalidParams()
		}
		return map[string]any{
			"type": "CALL", "from": fromAddress, "to": toAddress,
			"value": "0x5", "gas": "0x5208", "gasUsed": "0x5208",
			"input": "0x1234", "output": "0x",
		}, nil
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found"}
	}
}

func blockResponse(block map[string]any, full bool) map[string]any {
	copy := make(map[string]any, len(block))
	for name, value := range block {
		copy[name] = value
	}
	if full {
		return copy
	}
	transactions, _ := block["transactions"].([]map[string]any)
	hashes := make([]string, len(transactions))
	for index := range transactions {
		hashes[index], _ = transactions[index]["hash"].(string)
	}
	copy["transactions"] = hashes
	return copy
}

func (f *fixture) headTag() string {
	return fmt.Sprintf("0x%x", f.head.Load())
}

func stringParam(params []json.RawMessage, index int) (string, bool) {
	if index >= len(params) {
		return "", false
	}
	var value string
	if err := json.Unmarshal(params[index], &value); err != nil {
		return "", false
	}
	return value, true
}

func boolParam(params []json.RawMessage, index int) (bool, bool) {
	if index >= len(params) {
		return false, false
	}
	var value bool
	if err := json.Unmarshal(params[index], &value); err != nil {
		return false, false
	}
	return value, true
}

func validAddressParam(params []json.RawMessage, index int) bool {
	value, ok := stringParam(params, index)
	if !ok || len(value) != 42 || !strings.HasPrefix(value, "0x") {
		return false
	}
	_, err := hex.DecodeString(value[2:])
	return err == nil
}

func validStringParam(params []json.RawMessage, index int) bool {
	_, ok := stringParam(params, index)
	return ok
}

func validObjectParam(params []json.RawMessage, index int) bool {
	if index >= len(params) {
		return false
	}
	var value map[string]json.RawMessage
	return json.Unmarshal(params[index], &value) == nil && value != nil
}

func (f *fixture) validExactBlockSelector(params []json.RawMessage, index int) bool {
	if index >= len(params) {
		return false
	}
	var selector map[string]json.RawMessage
	if err := json.Unmarshal(params[index], &selector); err != nil || len(selector) != 2 {
		return false
	}
	var hash string
	if err := json.Unmarshal(selector["blockHash"], &hash); err != nil {
		return false
	}
	var requireCanonical bool
	if err := json.Unmarshal(selector["requireCanonical"], &requireCanonical); err != nil || !requireCanonical {
		return false
	}
	if strings.EqualFold(hash, blockTwoHash) && f.head.Load() < 2 {
		return false
	}
	_, exists := f.blocksByHash[strings.ToLower(hash)]
	return exists
}

func invalidParams() *rpcError {
	return &rpcError{Code: -32602, Message: "invalid params"}
}

func errorResponse(id json.RawMessage, code int, message string) map[string]any {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   &rpcError{Code: code, Message: message},
	}
}

func writeRPCResponse(w http.ResponseWriter, value any) {
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("encode response: %v", err)
	}
}
