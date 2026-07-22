package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/islishude/etherview/internal/webui"
)

const (
	testAddress           = "0x1111111111111111111111111111111111111111"
	testHash              = "0x1111111111111111111111111111111111111111111111111111111111111111"
	secondHash            = "0x2222222222222222222222222222222222222222222222222222222222222222"
	orphanHash            = "0x3333333333333333333333333333333333333333333333333333333333333333"
	testTransactionHash   = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	secondTransactionHash = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	parentHash            = "0x0000000000000000000000000000000000000000000000000000000000000000"
	blockCursor           = "blocks/snapshot + page=2"
	transactionCursor     = "transactions/snapshot?generation=7 + page=2&exact=true/#"
	searchCursor          = "search/snapshot + page=2"
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
		writeEnvelopeMeta(response, map[string]any{
			"chain_id": "1", "core_ready": true, "latest_block": "2", "indexed_block": "2",
			"highest_covered_block": "2", "backfill_complete": true,
			"safe_block": "2", "finalized_block": "1", "lag": "0", "completeness": completeness(),
		}, map[string]any{"coverage_start": "0", "coverage_end": "2"})
	})
	mux.HandleFunc("GET /api/v1/blocks", func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("cursor") == blockCursor {
			writeEnvelope(response, []any{canonicalBlockOne()})
			return
		}
		writeEnvelopeMeta(response, []any{canonicalBlockTwo()}, map[string]any{"next_cursor": blockCursor})
	})
	mux.HandleFunc("GET /api/v1/blocks/{id}", func(response http.ResponseWriter, request *http.Request) {
		switch request.PathValue("id") {
		case "1", testHash:
			writeEnvelope(response, canonicalBlockOne())
		case "2", secondHash:
			writeEnvelope(response, canonicalBlockTwo())
		case orphanHash:
			writeEnvelope(response, orphanBlock())
		default:
			writeNotFound(response)
		}
	})
	mux.HandleFunc("GET /api/v1/transactions", func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("cursor") == transactionCursor {
			writeEnvelope(response, []any{transaction(secondTransactionHash, testHash, "1", "finalized")})
			return
		}
		writeEnvelopeMeta(
			response,
			[]any{transaction(testTransactionHash, secondHash, "2", "safe")},
			map[string]any{"next_cursor": transactionCursor},
		)
	})
	mux.HandleFunc("GET /api/v1/transactions/{hash}", func(response http.ResponseWriter, request *http.Request) {
		switch request.PathValue("hash") {
		case testTransactionHash:
			writeEnvelope(response, transaction(testTransactionHash, secondHash, "2", "safe"))
		case secondTransactionHash:
			writeEnvelope(response, transaction(secondTransactionHash, testHash, "1", "finalized"))
		default:
			writeNotFound(response)
		}
	})
	mux.HandleFunc("GET /api/v1/transactions/{hash}/trace", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("hash") != testTransactionHash && request.PathValue("hash") != secondTransactionHash {
			writeNotFound(response)
			return
		}
		writeEnvelope(response, map[string]any{
			"chain_id": "1", "block_number": "2", "block_hash": secondHash,
			"transaction_hash": request.PathValue("hash"), "transaction_index": "0",
			"state": "unavailable", "frames": []any{},
		})
	})
	mux.HandleFunc("GET /api/v1/addresses/{address}", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("address") != testAddress {
			writeNotFound(response)
			return
		}
		writeEnvelope(response, map[string]any{
			"address": testAddress, "type": "contract", "balance": "900719925474099312345", "nonce": "1",
			"code_hash": testHash, "at_block": secondHash, "completeness": completeness(),
		})
	})
	mux.HandleFunc("GET /api/v1/search", func(response http.ResponseWriter, request *http.Request) {
		query := request.URL.Query().Get("q")
		cursor := request.URL.Query().Get("cursor")
		if query == "activity" && cursor == searchCursor {
			writeEnvelope(response, []any{map[string]any{
				"kind": "block", "key": orphanHash, "label": "Retained orphan block #1",
				"rank": 90, "canonical": false,
			}})
			return
		}
		if query == "activity" {
			writeEnvelopeMeta(response, []any{map[string]any{
				"kind": "transaction", "key": testTransactionHash, "label": "Canonical transaction",
				"rank": 100, "canonical": true,
			}}, map[string]any{"next_cursor": searchCursor})
			return
		}
		if query == orphanHash {
			writeEnvelope(response, []any{map[string]any{
				"kind": "block", "key": orphanHash, "label": "Retained orphan block #1",
				"rank": 100, "canonical": false,
			}})
			return
		}
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

func canonicalBlockOne() map[string]any {
	return block("1", testHash, parentHash, true, "finalized", 1)
}

func canonicalBlockTwo() map[string]any {
	return block("2", secondHash, testHash, true, "safe", 1)
}

func orphanBlock() map[string]any {
	return block("1", orphanHash, parentHash, false, "orphan", 1)
}

func block(number, hash, parent string, canonical bool, finality string, transactionCount int) map[string]any {
	return map[string]any{
		"hash": hash, "number": number, "parent_hash": parent,
		"timestamp": "2026-01-01T00:00:00Z", "miner": testAddress,
		"transaction_count": transactionCount, "gas_used": "21000", "gas_limit": "30000000",
		"base_fee_per_gas": "1000000000", "canonical": canonical,
		"finality": finality, "completeness": completeness(),
	}
}

func transaction(hash, blockHash, blockNumber, finality string) map[string]any {
	return map[string]any{
		"hash": hash, "block_hash": blockHash, "block_number": blockNumber, "transaction_index": 0,
		"from": testAddress, "to": testAddress, "nonce": "1", "value": "900719925474099312345",
		"gas": "21000", "gas_price": "1000000000", "type": "2", "input": "0x",
		"status": "success", "canonical": true, "finality": finality, "completeness": completeness(),
	}
}

func completeness() map[string]string {
	return map[string]string{"core": "complete", "trace": "complete", "metadata": "complete", "state": "complete"}
}

func writeEnvelope(response http.ResponseWriter, data any) {
	writeEnvelopeMeta(response, data, nil)
}

func writeEnvelopeMeta(response http.ResponseWriter, data any, extraMeta map[string]any) {
	meta := map[string]any{"request_id": "e2e-request", "chain_id": "1"}
	for key, value := range extraMeta {
		meta[key] = value
	}
	writeJSON(response, map[string]any{
		"data": data,
		"meta": meta,
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
