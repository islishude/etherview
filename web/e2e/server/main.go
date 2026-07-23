package main

import (
	"encoding/json"
	"log"
	"maps"
	"net/http"
	"time"

	"github.com/islishude/etherview/web"
)

const (
	testAddress            = "0x1111111111111111111111111111111111111111"
	testHash               = "0x1111111111111111111111111111111111111111111111111111111111111111"
	secondHash             = "0x2222222222222222222222222222222222222222222222222222222222222222"
	orphanHash             = "0x3333333333333333333333333333333333333333333333333333333333333333"
	testTransactionHash    = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	secondTransactionHash  = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	pendingTransactionHash = "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	parentHash             = "0x0000000000000000000000000000000000000000000000000000000000000000"
	blockCursor            = "blocks/snapshot + page=2"
	transactionCursor      = "transactions/snapshot?generation=7 + page=2&exact=true/#"
	searchCursor           = "search/snapshot + page=2"
	testVerificationJobID  = "123e4567-e89b-42d3-a456-426614174000"
	testReadAPIKey         = "ev_e2e_read"
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
				"trace": true, "mempool": true, "historical_state": true,
				"verification": false, "nft_metadata": true, "pricing": false,
				"sourcify": false,
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
	mux.HandleFunc("GET /api/v1/addresses/{address}/nfts", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("address") != testAddress {
			writeNotFound(response)
			return
		}
		writeEnvelopeMeta(response, []any{map[string]any{
			"chain_id": "1", "owner": testAddress, "token_address": testAddress,
			"token_id": "1", "balance": "1", "confidence": "rpc_exact",
		}}, map[string]any{"coverage_end": "2"})
	})
	mux.HandleFunc("GET /api/v1/tokens", func(response http.ResponseWriter, _ *http.Request) {
		writeEnvelope(response, []any{tokenContract()})
	})
	mux.HandleFunc("GET /api/v1/tokens/{address}", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("address") != testAddress {
			writeNotFound(response)
			return
		}
		writeEnvelope(response, tokenContract())
	})
	mux.HandleFunc("GET /api/v1/tokens/{address}/transfers", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("address") != testAddress {
			writeNotFound(response)
			return
		}
		writeEnvelope(response, []any{map[string]any{
			"chain_id": "1", "block_number": "2", "block_hash": secondHash,
			"log_index": "0", "sub_index": "0", "transaction_hash": testTransactionHash,
			"token_address": testAddress, "standard": "erc721", "kind": "transfer",
			"from": testAddress, "to": testAddress, "token_id": "1", "amount": "1",
			"confidence": "verified",
		}})
	})
	mux.HandleFunc("GET /api/v1/nfts/{address}/{token_id}", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("address") != testAddress || request.PathValue("token_id") != "1" {
			writeNotFound(response)
			return
		}
		writeEnvelope(response, map[string]any{
			"chain_id": "1", "token_address": testAddress, "token_id": "1",
			"owner": testAddress, "balance": "1", "confidence": "rpc_exact",
			"snapshot": map[string]any{
				"chain_id": "1", "block_number": "2", "block_hash": secondHash,
			},
		})
	})
	mux.HandleFunc("GET /api/v1/pending", func(response http.ResponseWriter, _ *http.Request) {
		writeEnvelopeMeta(response, []any{map[string]any{
			"hash": pendingTransactionHash, "from": testAddress, "to": testAddress,
			"nonce": "9007199254740993", "value": "900719925474099312345",
			"gas": "21000", "max_fee_per_gas": "30000000000",
			"max_priority_fee_per_gas": "1000000000", "type": "2", "input": "0x",
			"first_seen_at": "2026-07-23T00:00:00Z",
			"last_seen_at":  "2026-07-23T00:00:01Z",
			"expires_at":    "2099-01-01T00:00:00Z", "endpoint": "pending-primary",
		}}, map[string]any{
			"snapshot_id": "7", "snapshot_at": "2026-07-23T00:00:01Z",
			"expires_at": "2099-01-01T00:00:00Z", "endpoint": "pending-primary",
			"capability": "complete", "transaction_count": "1",
		})
	})
	mux.HandleFunc("GET /api/v1/stats/blocks", func(response http.ResponseWriter, request *http.Request) {
		fromBlock := request.URL.Query().Get("from_block")
		toBlock := request.URL.Query().Get("to_block")
		if fromBlock == "" || toBlock == "" {
			writeNotFound(response)
			return
		}
		writeEnvelopeMeta(response, []any{blockStat()}, map[string]any{
			"coverage_start": fromBlock, "coverage_end": toBlock,
		})
	})
	mux.HandleFunc("GET /api/v1/stats/summary", func(response http.ResponseWriter, request *http.Request) {
		fromBlock := request.URL.Query().Get("from_block")
		toBlock := request.URL.Query().Get("to_block")
		if fromBlock == "" || toBlock == "" {
			writeNotFound(response)
			return
		}
		writeEnvelope(response, map[string]any{
			"chain_id": "1", "from_block": fromBlock, "to_block": toBlock,
			"snapshot": map[string]any{
				"chain_id": "1", "block_number": "2", "block_hash": secondHash,
			},
			"block_count": "3", "transaction_count": "5", "gas_used": "105000",
			"burned_wei": "900719925474099312345", "blob_burned_wei": "0",
			"token_event_count": "1", "token_transfer_count": "1",
			"nft_transfer_count": "1", "average_tps": "0.138888888888888889",
			"completeness": map[string]bool{"core": true, "stats": true, "token": true},
		})
	})
	mux.HandleFunc("GET /api/v1/verification/jobs/{id}", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("id") != testVerificationJobID {
			writeNotFound(response)
			return
		}
		if !authorized(request) {
			writeUnauthorized(response)
			return
		}
		writeEnvelope(response, map[string]any{
			"id": testVerificationJobID, "status": "succeeded",
			"result_kind": "exact", "runtime_match": "exact",
			"creation_match": "exact", "published": true,
			"created_at": "2026-07-23T00:00:00Z",
			"updated_at": "2026-07-23T00:00:01Z",
		})
	})
	mux.HandleFunc("GET /api/v1/contracts/{address}/verification", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("address") != testAddress ||
			request.URL.Query().Get("code_hash") != testHash {
			writeNotFound(response)
			return
		}
		if !authorized(request) {
			writeUnauthorized(response)
			return
		}
		writeEnvelope(response, map[string]any{
			"chain_id": "1", "address": testAddress, "code_hash": testHash,
			"valid_from_block": "1", "language": "solidity",
			"compiler_version": "0.8.30", "match_kind": "exact",
			"contract_name": "ExampleCollectible",
			"abi":           []any{map[string]any{"type": "function", "name": "ownerOf"}},
			"sources": map[string]any{
				"ExampleCollectible.sol": map[string]any{"content": "contract ExampleCollectible {}"},
			},
			"settings":   map[string]any{"optimizer": map[string]any{"enabled": true}},
			"created_at": "2026-07-23T00:00:01Z",
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

func tokenContract() map[string]any {
	return map[string]any{
		"chain_id": "1", "address": testAddress, "code_hash": testHash,
		"standard": "erc721", "confidence": "verified",
		"name": "Example Collectible", "symbol": "ECO", "total_supply": "9007199254740993",
		"metadata_state": "complete", "observed_block_number": "2",
		"observed_block_hash": secondHash, "updated_at": "2026-07-23T00:00:01Z",
	}
}

func blockStat() map[string]any {
	return map[string]any{
		"chain_id": "1", "block_number": "2", "block_hash": secondHash,
		"transaction_count": "2", "gas_used": "42000", "gas_limit": "30000000",
		"base_fee_per_gas": "1000000000", "burned_wei": "42000000000000",
		"blob_gas_used": "0", "excess_blob_gas": "0", "blob_burned_wei": "0",
		"block_timestamp": "1784764800", "block_interval_seconds": "12",
		"transactions_per_second": "0.166666666666666667",
		"token_event_count":       "1", "token_transfer_count": "1", "nft_transfer_count": "1",
		"computed_at": "2026-07-23T00:00:01Z",
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
	maps.Copy(meta, extraMeta)
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

func authorized(request *http.Request) bool {
	return request.Header.Get("X-API-Key") == testReadAPIKey
}

func writeUnauthorized(response http.ResponseWriter) {
	response.WriteHeader(http.StatusUnauthorized)
	writeJSON(response, map[string]any{
		"error": map[string]any{
			"code": "unauthorized", "message": "API key required", "request_id": "e2e-request",
		},
	})
}

func writeJSON(response http.ResponseWriter, value any) {
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(value); err != nil {
		panic(err)
	}
}
