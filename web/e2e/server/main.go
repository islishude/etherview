package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/islishude/etherview/internal/webui"
)

const (
	testAddress = "0x1111111111111111111111111111111111111111"
	testHash    = "0x1111111111111111111111111111111111111111111111111111111111111111"
	parentHash  = "0x0000000000000000000000000000000000000000000000000000000000000000"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(response, map[string]any{"status": "live"})
	})
	mux.HandleFunc("GET /api/v1/config", func(response http.ResponseWriter, _ *http.Request) {
		writeEnvelope(response, map[string]any{
			"chain_id": "1", "chain_name": "Ethereum", "native_symbol": "ETH",
			"native_name": "Ether", "native_decimals": 18,
			"features": map[string]bool{
				"trace": true, "mempool": false, "historical_state": true,
				"verification": true, "nft_metadata": true, "pricing": false,
			},
		})
	})
	mux.HandleFunc("GET /api/v1/status", func(response http.ResponseWriter, _ *http.Request) {
		writeEnvelope(response, map[string]any{
			"chain_id": "1", "core_ready": true, "latest_block": "1", "indexed_block": "1",
			"safe_block": "1", "finalized_block": "1", "lag": "0", "completeness": completeness(),
		})
	})
	mux.HandleFunc("GET /api/v1/blocks", func(response http.ResponseWriter, _ *http.Request) {
		writeEnvelope(response, []any{block()})
	})
	mux.HandleFunc("GET /api/v1/blocks/{id}", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("id") != "1" && request.PathValue("id") != testHash {
			writeNotFound(response)
			return
		}
		writeEnvelope(response, block())
	})
	mux.HandleFunc("GET /api/v1/transactions", func(response http.ResponseWriter, _ *http.Request) {
		writeEnvelope(response, []any{})
	})
	mux.HandleFunc("GET /api/v1/addresses/{address}", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("address") != testAddress {
			writeNotFound(response)
			return
		}
		writeEnvelope(response, map[string]any{
			"address": testAddress, "type": "contract", "balance": "0", "nonce": "1",
			"code_hash": testHash, "at_block": testHash, "completeness": completeness(),
		})
	})
	mux.HandleFunc("GET /api/v1/search", func(response http.ResponseWriter, _ *http.Request) {
		writeEnvelope(response, []any{})
	})
	mux.Handle("/", webui.NewHandler())

	server := &http.Server{
		Addr:              "127.0.0.1:4173",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("Etherview E2E server listening on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func block() map[string]any {
	return map[string]any{
		"hash": testHash, "number": "1", "parent_hash": parentHash,
		"timestamp": "2026-01-01T00:00:00Z", "miner": testAddress,
		"transaction_count": 0, "gas_used": "0", "gas_limit": "30000000",
		"base_fee_per_gas": "1000000000", "canonical": true,
		"finality": "finalized", "completeness": completeness(),
	}
}

func completeness() map[string]string {
	return map[string]string{"core": "complete", "trace": "complete", "metadata": "complete", "state": "complete"}
}

func writeEnvelope(response http.ResponseWriter, data any) {
	writeJSON(response, map[string]any{
		"data": data,
		"meta": map[string]string{"request_id": "e2e-request", "chain_id": "1"},
	})
}

func writeNotFound(response http.ResponseWriter) {
	response.WriteHeader(http.StatusNotFound)
	writeJSON(response, map[string]any{
		"error": map[string]any{
			"code": "not_found", "message": "resource not found", "request_id": "e2e-request",
		},
	})
}

func writeJSON(response http.ResponseWriter, value any) {
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(value); err != nil {
		panic(err)
	}
}
