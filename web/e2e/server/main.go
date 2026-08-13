package main

import (
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/islishude/etherview/web"
)

const (
	testAddress               = "0x1111111111111111111111111111111111111111"
	unverifiedAddress         = "0x1212121212121212121212121212121212121212"
	testEOA                   = "0x2222222222222222222222222222222222222222"
	delegatedAddress          = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"
	clearedDelegationAddress  = "0x7777777777777777777777777777777777777777"
	delegatedDelegate         = "0x5FbDB2315678afecb367f032d93F642f64180aa3"
	uupsProxyAddress          = "0x3000000000000000000000000000000000000003"
	uupsImplementation        = "0x4000000000000000000000000000000000000004"
	beaconProxyAddress        = "0x5000000000000000000000000000000000000005"
	beaconImplementation      = "0x6000000000000000000000000000000000000006"
	cloneAddress              = "0x7000000000000000000000000000000000000007"
	cloneImplementation       = "0x8000000000000000000000000000000000000008"
	proxyAdminAddress         = "0x9000000000000000000000000000000000000009"
	upgradeableBeacon         = "0x2000000000000000000000000000000000000020"
	transparentImplementation = "0x3000000000000000000000000000000000000030"
	oldImplementation         = "0x4000000000000000000000000000000000000040"
	diamondAddress            = "0xd000000000000000000000000000000000000000"
	diamondWriteFacet         = "0xd100000000000000000000000000000000000001"
	diamondLoupeFacet         = "0xd200000000000000000000000000000000000002"
	diamondInitAddress        = "0xd300000000000000000000000000000000000003"
	testHash                  = "0x1111111111111111111111111111111111111111111111111111111111111111"
	secondHash                = "0x2222222222222222222222222222222222222222222222222222222222222222"
	orphanHash                = "0x3333333333333333333333333333333333333333333333333333333333333333"
	testTransactionHash       = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	secondTransactionHash     = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	pendingTransactionHash    = "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	delegationTransactionHash = "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	clearingTransactionHash   = "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	parentHash                = "0x0000000000000000000000000000000000000000000000000000000000000000"
	blockCursor               = "blocks/snapshot + page=2"
	transactionCursor         = "transactions/snapshot?generation=7 + page=2&exact=true/#"
	searchCursor              = "search/snapshot + page=2"
	testVerificationJobID     = "123e4567-e89b-42d3-a456-426614174000"
	testReadAPIKey            = "ev_e2e_read"
)

func main() {
	mux := http.NewServeMux()
	homeStreams := &homeStreamHub{streams: make(map[string]*homeTestStream)}
	mux.HandleFunc("GET /health/live", func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(response, map[string]any{"status": "live"})
	})
	mux.HandleFunc("GET /api/v1/config", func(response http.ResponseWriter, _ *http.Request) {
		writeEnvelope(response, map[string]any{
			"chain_id": "1", "chain_name": "Ethereum", "native_symbol": "ETH",
			"native_name": "Ether", "native_decimals": 18,
			"wallet_add_chain": map[string]any{
				"chain_id": "1", "chain_name": "Ethereum",
				"native_currency": map[string]any{
					"name": "Ether", "symbol": "ETH", "decimals": 18,
				},
				"rpc_urls": []string{"http://localhost:8545"},
			},
			"features": map[string]bool{
				"trace": true, "mempool": true, "historical_state": true,
				"verification": false, "nft_metadata": true, "pricing": false,
				"sourcify": false,
			},
		})
	})
	mux.HandleFunc("GET /api/v1/home/stream", func(response http.ResponseWriter, request *http.Request) {
		stream := homeStreams.stream(homeStreamSession(request))
		channel, unsubscribe := stream.subscribe()
		defer unsubscribe()
		response.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		response.Header().Set("Cache-Control", "no-cache, no-transform")
		response.Header().Set("X-Accel-Buffering", "no")
		response.WriteHeader(http.StatusOK)
		flusher, ok := response.(http.Flusher)
		if !ok {
			return
		}
		flusher.Flush()
		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case update, open := <-channel:
				if !open {
					return
				}
				if _, err := fmt.Fprintf(
					response,
					"id: %d\nevent: snapshot\ndata: %s\n\n",
					update.id,
					update.payload,
				); err != nil {
					return
				}
				flusher.Flush()
			case <-heartbeat.C:
				if _, err := fmt.Fprint(response, ": heartbeat\n\n"); err != nil {
					return
				}
				flusher.Flush()
			case <-request.Context().Done():
				return
			}
		}
	})
	mux.HandleFunc("POST /__e2e/home/head", func(response http.ResponseWriter, request *http.Request) {
		session := homeStreamSession(request)
		if session == "" {
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		homeStreams.stream(session).advance()
		writeJSON(response, map[string]string{"status": "advanced"})
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
			writeEnvelope(response, includedTransactionDetail(transaction(testTransactionHash, secondHash, "2", "safe")))
		case secondTransactionHash:
			writeEnvelope(response, includedTransactionDetail(contractCreationTransaction(secondTransactionHash, testHash, "1", "finalized")))
		case delegationTransactionHash:
			writeEnvelope(response, includedTransactionDetail(setCodeTransaction(delegationTransactionHash, 1, delegatedCalldata())))
		case clearingTransactionHash:
			writeEnvelope(response, includedTransactionDetail(setCodeTransaction(clearingTransactionHash, 2, "0x55241077")))
		default:
			writeNotFound(response)
		}
	})
	mux.HandleFunc("GET /api/v1/transactions/{hash}/calldata", func(response http.ResponseWriter, request *http.Request) {
		switch request.PathValue("hash") {
		case testTransactionHash:
			writeEnvelope(response, map[string]any{
				"chain_id": "1", "block_number": "2", "block_hash": secondHash,
				"transaction_hash": request.PathValue("hash"), "transaction_index": "0", "state": "complete",
				"input": "0x3fa4f245",
				"execution": map[string]any{
					"context_address": testAddress, "address": testAddress,
					"code_hash": testHash, "resolution": "direct",
				},
				"decoding": map[string]any{
					"status": "decoded", "function_name": "value", "signature": "value()",
					"inputs": []any{}, "candidates": []any{"value()"},
					"abi_source": map[string]any{
						"kind": "proxy_implementation", "address": transparentImplementation, "code_hash": testHash,
					},
					"confidence": "high",
				},
			})
		case delegationTransactionHash:
			writeEnvelope(response, map[string]any{
				"chain_id": "1", "block_number": "2", "block_hash": secondHash,
				"transaction_hash": delegationTransactionHash, "transaction_index": "1", "state": "complete",
				"input": delegatedCalldata(),
				"execution": map[string]any{
					"context_address": delegatedAddress, "address": delegatedDelegate,
					"code_hash": testHash, "resolution": "eip7702_delegate",
				},
				"decoding": map[string]any{
					"status": "decoded", "function_name": "setValue", "signature": "setValue(uint256)",
					"inputs":     []any{map[string]any{"name": "value", "type": "uint256", "value": "42"}},
					"candidates": []any{"setValue(uint256)"},
					"abi_source": map[string]any{
						"kind": "exact_address", "address": delegatedDelegate, "code_hash": testHash,
					},
					"confidence": "verified",
				},
			})
		case clearingTransactionHash:
			writeEnvelope(response, map[string]any{
				"chain_id": "1", "block_number": "2", "block_hash": secondHash,
				"transaction_hash": clearingTransactionHash, "transaction_index": "2", "state": "complete",
				"input": "0x55241077",
				"execution": map[string]any{
					"context_address": delegatedAddress, "resolution": "empty",
				},
				"decoding": map[string]any{
					"status": "not_applicable", "inputs": []any{}, "candidates": []any{},
				},
			})
		default:
			writeNotFound(response)
		}
	})
	mux.HandleFunc("GET /api/v1/transactions/{hash}/authorizations", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("hash") == clearingTransactionHash {
			writeEnvelope(response, transactionAuthorizations(clearingTransactionHash, "2", []any{
				authorization("0", delegatedAddress, "0x0000000000000000000000000000000000000000", "2", "applied", ""),
			}))
			return
		}
		if request.PathValue("hash") != delegationTransactionHash {
			writeNotFound(response)
			return
		}
		if request.URL.Query().Get("cursor") == "authorization-next" {
			writeEnvelope(response, transactionAuthorizations(delegationTransactionHash, "1", []any{
				authorization("1", testEOA, delegatedDelegate, "9", "skipped", "nonce_mismatch"),
			}))
			return
		}
		writeEnvelopeMeta(response,
			transactionAuthorizations(delegationTransactionHash, "1", []any{
				authorization("0", delegatedAddress, delegatedDelegate, "0", "applied", ""),
			}),
			map[string]any{"next_cursor": "authorization-next"},
		)
	})
	mux.HandleFunc("GET /api/v1/transactions/{hash}/trace", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("hash") != testTransactionHash && request.PathValue("hash") != secondTransactionHash {
			writeNotFound(response)
			return
		}
		if request.PathValue("hash") == secondTransactionHash {
			writeEnvelope(response, map[string]any{
				"chain_id": "1", "block_number": "2", "block_hash": secondHash,
				"transaction_hash": request.PathValue("hash"), "transaction_index": "0",
				"state": "unavailable", "frames": []any{},
			})
			return
		}
		writeEnvelope(response, map[string]any{
			"chain_id": "1", "block_number": "2", "block_hash": secondHash,
			"transaction_hash": request.PathValue("hash"), "transaction_index": "0",
			"state": "complete", "frames": []any{
				map[string]any{
					"path": []any{}, "parent_path": []any{}, "depth": 0, "call_type": "CALL",
					"from": testEOA, "to": uupsProxyAddress, "value": "0", "gas": "100000", "gas_used": "50000",
					"input": "0x2e64cec1", "output": "0x000000000000000000000000000000000000000000000000000000000000002a",
					"direct_reverted": false, "reverted": false,
					"execution": map[string]any{
						"context_address": uupsProxyAddress, "address": uupsProxyAddress,
						"code_hash": testHash, "resolution": "direct",
					},
					"decoding": map[string]any{
						"kind": "function", "status": "decoded", "function_name": "retrieve", "signature": "retrieve()",
						"inputs": []any{}, "output_status": "decoded",
						"outputs":    []any{map[string]any{"name": "value", "type": "uint256", "value": "42"}},
						"candidates": []any{"retrieve()"},
						"abi_source": map[string]any{"kind": "proxy_implementation", "address": uupsImplementation, "code_hash": testHash},
						"confidence": "high",
					},
				},
				map[string]any{
					"path": []any{0}, "parent_path": []any{}, "depth": 1, "call_type": "DELEGATECALL",
					"from": uupsProxyAddress, "to": uupsImplementation, "value": "0", "gas": "40000", "gas_used": "12000",
					"input":  "0x8e4a23d60000000000000000000000002222222222222222222222222222222222222222",
					"output": "0x4e487b710000000000000000000000000000000000000000000000000000000000000011",
					"error":  "execution reverted", "direct_reverted": true, "reverted": true,
					"execution": map[string]any{
						"context_address": uupsProxyAddress, "address": uupsImplementation,
						"code_hash": testHash, "resolution": "direct",
					},
					"decoding": map[string]any{
						"kind": "function", "status": "decoded", "function_name": "setOwner", "signature": "setOwner(address)",
						"inputs":        []any{map[string]any{"name": "owner", "type": "address", "value": testEOA}},
						"output_status": "not_applicable", "outputs": []any{}, "candidates": []any{"setOwner(address)"},
						"revert": map[string]any{
							"status": "decoded", "error_name": "Panic", "signature": "Panic(uint256)",
							"arguments":  []any{map[string]any{"name": "code", "type": "uint256", "value": "17"}},
							"candidates": []any{"Panic(uint256)"}, "abi_source": map[string]any{"kind": "builtin"}, "confidence": "high",
						},
						"abi_source": map[string]any{"kind": "exact_address", "address": uupsImplementation, "code_hash": testHash},
						"confidence": "verified",
					},
				},
			},
		})
	})
	mux.HandleFunc("GET /api/v1/transactions/{hash}/internal-transactions", func(response http.ResponseWriter, request *http.Request) {
		switch request.PathValue("hash") {
		case testTransactionHash:
			writeEnvelope(response, map[string]any{
				"chain_id": "1", "block_number": "2", "block_hash": secondHash,
				"transaction_hash": request.PathValue("hash"), "transaction_index": "0",
				"state": "complete", "items": []any{map[string]any{
					"path": []any{1}, "depth": 1, "call_type": "CALL",
					"from": testEOA, "to": uupsProxyAddress, "value": "1250000000000000000",
				}},
			})
		case secondTransactionHash:
			writeEnvelope(response, map[string]any{
				"chain_id": "1", "block_number": "1", "block_hash": testHash,
				"transaction_hash": request.PathValue("hash"), "transaction_index": "0",
				"state": "complete", "items": []any{},
			})
		default:
			writeNotFound(response)
		}
	})
	mux.HandleFunc("GET /api/v1/transactions/{hash}/token-transfers", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("hash") != testTransactionHash && request.PathValue("hash") != secondTransactionHash &&
			request.PathValue("hash") != delegationTransactionHash && request.PathValue("hash") != clearingTransactionHash {
			writeNotFound(response)
			return
		}
		items := []any{}
		if request.PathValue("hash") == testTransactionHash {
			items = append(items, map[string]any{
				"chain_id": "1", "block_number": "2", "block_hash": secondHash,
				"log_index": "0", "sub_index": "0", "transaction_hash": testTransactionHash,
				"token_address": testAddress, "standard": "erc20", "kind": "transfer",
				"from": testEOA, "to": uupsProxyAddress, "amount": "1234500", "decimals": 6,
				"confidence": "verified",
			})
		}
		writeEnvelope(response, map[string]any{
			"chain_id": "1", "block_number": "2", "block_hash": secondHash,
			"transaction_hash": request.PathValue("hash"), "transaction_index": "0",
			"state": "complete", "items": items,
		})
	})
	mux.HandleFunc("GET /api/v1/transactions/{hash}/logs", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("hash") != testTransactionHash && request.PathValue("hash") != secondTransactionHash {
			writeNotFound(response)
			return
		}
		items := []any{}
		if request.PathValue("hash") == testTransactionHash {
			items = append(items, map[string]any{
				"address": uupsProxyAddress, "log_index": "7",
				"topics": []any{"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"},
				"data":   "0x000000000000000000000000000000000000000000000000000000000000002a",
				"decoding": map[string]any{
					"status": "decoded", "event_name": "ValueChanged", "signature": "ValueChanged(uint256)",
					"arguments":   []any{map[string]any{"name": "value", "type": "uint256", "indexed": false, "hashed": false, "value": "42"}},
					"candidates":  []any{"ValueChanged(uint256)"},
					"abi_source":  map[string]any{"kind": "exact_address", "address": uupsImplementation, "code_hash": testHash},
					"confidence":  "verified",
					"attribution": map[string]any{"mode": "exact_trace", "trace_path": []any{0}, "execution_address": uupsImplementation},
				},
			})
		}
		writeEnvelope(response, map[string]any{
			"chain_id": "1", "block_number": "2", "block_hash": secondHash,
			"transaction_hash": request.PathValue("hash"), "transaction_index": "0",
			"state": "complete", "items": items,
		})
	})
	mux.HandleFunc("GET /api/v1/transactions/{hash}/state-changes", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("hash") != testTransactionHash && request.PathValue("hash") != secondTransactionHash {
			writeNotFound(response)
			return
		}
		writeEnvelope(response, map[string]any{
			"chain_id": "1", "block_number": "2", "block_hash": secondHash,
			"transaction_hash": request.PathValue("hash"), "transaction_index": "0",
			"state": "complete", "items": []any{},
		})
	})
	mux.HandleFunc("GET /api/v1/addresses/{address}", func(response http.ResponseWriter, request *http.Request) {
		switch request.PathValue("address") {
		case testAddress:
			writeEnvelope(response, map[string]any{
				"address": testAddress, "type": "contract", "balance": "900719925474099312345", "nonce": "1",
				"code_hash": testHash, "at_block": secondHash, "completeness": completeness(),
				"has_delegation_history": false,
				"origin": map[string]any{
					"kind": "contract_creation", "state": "found",
					"source_address": testEOA, "transaction_hash": testTransactionHash,
				},
			})
		case unverifiedAddress:
			writeEnvelope(response, map[string]any{
				"address": unverifiedAddress, "type": "contract", "balance": "0", "nonce": "1",
				"code_hash": testHash, "at_block": secondHash, "completeness": completeness(),
				"has_delegation_history": false,
			})
		case testEOA:
			writeEnvelope(response, map[string]any{
				"address": testEOA, "type": "eoa", "balance": "0", "nonce": "0",
				"at_block": secondHash, "completeness": completeness(), "has_delegation_history": false,
			})
		case delegatedAddress:
			writeEnvelope(response, map[string]any{
				"address": delegatedAddress, "type": "delegated_eoa", "balance": "1000000000000000000", "nonce": "4",
				"code_hash": testHash, "at_block": secondHash, "completeness": completeness(),
				"has_delegation_history": true,
			})
		case clearedDelegationAddress:
			writeEnvelope(response, map[string]any{
				"address": clearedDelegationAddress, "type": "eoa", "balance": "0", "nonce": "5",
				"at_block": secondHash, "completeness": completeness(), "has_delegation_history": true,
			})
		default:
			if _, ok := contractArtifact(request.PathValue("address")); !ok {
				writeNotFound(response)
				return
			}
			writeEnvelope(response, map[string]any{
				"address": request.PathValue("address"), "type": "contract", "balance": "0", "nonce": "0",
				"code_hash": testHash, "at_block": secondHash, "completeness": completeness(),
				"has_delegation_history": false,
			})
		}
	})
	mux.HandleFunc("GET /api/v1/addresses/{address}/delegation", func(response http.ResponseWriter, request *http.Request) {
		switch request.PathValue("address") {
		case delegatedAddress:
			writeEnvelope(response, map[string]any{
				"authority": delegatedAddress, "status": "delegated", "chain_id": "1",
				"block_number": "3", "block_hash": testHash,
				"delegate": delegatedDelegate, "delegate_code_hash": testHash,
			})
		case clearedDelegationAddress:
			writeEnvelope(response, map[string]any{
				"authority": clearedDelegationAddress, "status": "not_delegated", "chain_id": "1",
				"block_number": "3", "block_hash": testHash,
			})
		default:
			writeNotFound(response)
		}
	})
	mux.HandleFunc("GET /api/v1/addresses/{address}/delegations", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("address") != delegatedAddress && request.PathValue("address") != clearedDelegationAddress {
			writeNotFound(response)
			return
		}
		if request.PathValue("address") == clearedDelegationAddress {
			writeEnvelope(response, []any{map[string]any{
				"authority": clearedDelegationAddress, "kind": "cleared",
				"delegate":          "0x0000000000000000000000000000000000000000",
				"previous_delegate": delegatedDelegate, "block_number": "3", "block_hash": testHash,
				"transaction_hash": secondTransactionHash, "transaction_index": "0", "authorization_index": "0",
			}})
			return
		}
		item := map[string]any{
			"authority": delegatedAddress, "kind": "redelegated", "delegate": delegatedDelegate,
			"previous_delegate": delegatedDelegate, "block_number": "3", "block_hash": testHash,
			"transaction_hash": secondTransactionHash, "transaction_index": "0", "authorization_index": "0",
		}
		if request.URL.Query().Get("cursor") == "delegation-next" {
			item["kind"] = "delegated"
			delete(item, "previous_delegate")
			item["block_number"] = "2"
			item["block_hash"] = secondHash
			item["transaction_hash"] = testTransactionHash
			writeEnvelope(response, []any{item})
			return
		}
		writeEnvelopeMeta(response, []any{item}, map[string]any{"next_cursor": "delegation-next"})
	})
	mux.HandleFunc("GET /api/v1/addresses/{address}/transactions", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("address") != testAddress && request.PathValue("address") != testEOA &&
			request.PathValue("address") != clearedDelegationAddress {
			writeNotFound(response)
			return
		}
		writeEnvelope(response, []any{transaction(testTransactionHash, secondHash, "2", "safe")})
	})
	mux.HandleFunc("GET /api/v1/addresses/{address}/internal-transactions", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("address") != testAddress {
			writeNotFound(response)
			return
		}
		writeEnvelope(response, []any{map[string]any{
			"block_number": "2", "block_hash": secondHash, "block_timestamp": "2026-01-01T00:00:00Z",
			"transaction_hash": testTransactionHash, "transaction_index": "0",
			"path": []int{0}, "depth": 1, "call_type": "call",
			"from": testAddress, "to": testAddress, "value": "1", "reverted": false,
		}})
	})
	mux.HandleFunc("GET /api/v1/addresses/{address}/erc20-transfers", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("address") != testAddress {
			writeNotFound(response)
			return
		}
		writeEnvelope(response, []any{addressTokenTransfer("erc20", "transfer")})
	})
	mux.HandleFunc("GET /api/v1/addresses/{address}/nft-transfers", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("address") != testAddress {
			writeNotFound(response)
			return
		}
		transfer := addressTokenTransfer("erc1155", "mint")
		transfer["from"] = nil
		transfer["token_id"] = "1"
		writeEnvelope(response, []any{transfer})
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
	mux.HandleFunc("GET /api/v1/addresses/{address}/erc20-balances", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("address") != testAddress {
			writeNotFound(response)
			return
		}
		writeEnvelopeMeta(response, []any{map[string]any{
			"chain_id": "1", "owner": testAddress, "token_address": testEOA,
			"balance": "1234500", "confidence": "rpc_exact",
			"name": "Example Token", "symbol": "EXT", "decimals": 4,
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
	mux.HandleFunc("GET /api/v1/stats/charts/overview", func(response http.ResponseWriter, _ *http.Request) {
		metrics := []string{
			"transactions", "failed-transactions", "average-tps",
			"erc20-transfers", "nft-transfers", "contract-creations",
			"blocks", "average-block-time", "gas-used", "gas-utilization",
			"average-base-fee", "execution-fees", "average-transaction-fee",
			"priority-fees", "burned-fees", "blob-gas-used",
			"average-blob-base-fee", "blob-burned-fees",
		}
		previews := make([]map[string]any, 0, len(metrics))
		for _, metric := range metrics {
			value := "42"
			if metric == "execution-fees" {
				value = "900719925474099312345"
			}
			previews = append(previews, map[string]any{
				"metric": metric, "current_value": value, "previous_value": "40",
				"change_percent": "5", "points": []map[string]any{chartPoint(value)},
			})
		}
		writeEnvelope(response, map[string]any{
			"generated_at": "2026-07-28T12:00:00Z",
			"snapshot": map[string]any{
				"chain_id": "1", "block_number": "100", "block_hash": secondHash,
			},
			"coverage": chartCoverage(), "metrics": previews, "pending": false,
		})
	})
	mux.HandleFunc("GET /api/v1/stats/charts/{metric}", func(response http.ResponseWriter, request *http.Request) {
		metric := request.PathValue("metric")
		if metric == "" || request.URL.Query().Get("from_time") == "" ||
			request.URL.Query().Get("to_time") == "" {
			writeNotFound(response)
			return
		}
		value := "900719925474099312345"
		writeEnvelope(response, map[string]any{
			"metric": metric, "interval": "day",
			"from_time": request.URL.Query().Get("from_time"),
			"to_time":   request.URL.Query().Get("to_time"),
			"points":    []map[string]any{chartPoint(value)},
			"summary": map[string]any{
				"current": value, "highest": value, "lowest": value,
				"total": value, "average": value,
			},
			"snapshot": map[string]any{
				"chain_id": "1", "block_number": "100", "block_hash": secondHash,
			},
			"coverage": chartCoverage(),
		})
	})
	mux.HandleFunc("GET /api/v1/verifier/jobs/{id}", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("id") != testVerificationJobID {
			writeNotFound(response)
			return
		}
		if !authorized(request) {
			writeUnauthorized(response)
			return
		}
		writeEnvelope(response, map[string]any{
			"id": testVerificationJobID, "kind": "address", "status": "succeeded",
			"outcome": map[string]any{
				"kind":      "verification_success",
				"file_name": "ExampleCollectible.sol", "contract_name": "ExampleCollectible",
				"language": "solidity", "compiler_version": "0.8.30",
				"settings": map[string]any{}, "sources": map[string]any{},
				"compilation_artifacts":   map[string]any{},
				"creation_code_artifacts": map[string]any{},
				"runtime_code_artifacts":  map[string]any{},
				"libraries":               map[string]any{}, "is_blueprint": false,
				"runtime_match": map[string]any{
					"match_type": "full", "transformations": []any{}, "values": map[string]any{},
				},
			},
			"created_at": "2026-07-23T00:00:00Z",
			"updated_at": "2026-07-23T00:00:01Z",
		})
	})
	mux.HandleFunc("GET /api/v1/contracts/{address}/verification", func(response http.ResponseWriter, request *http.Request) {
		if rejectAuthenticatedContractRead(response, request) {
			return
		}
		artifact, ok := contractArtifact(request.PathValue("address"))
		if !ok {
			writeNotFound(response)
			return
		}
		writeEnvelope(response, artifact)
	})
	mux.HandleFunc("GET /api/v1/contracts/{address}/proxy", func(response http.ResponseWriter, request *http.Request) {
		if rejectAuthenticatedContractRead(response, request) {
			return
		}
		detail, ok := contractProxy(request.PathValue("address"))
		if !ok {
			writeNotFound(response)
			return
		}
		writeEnvelope(response, detail)
	})
	mux.HandleFunc("GET /api/v1/contracts/{address}/proxy/upgrades", func(response http.ResponseWriter, request *http.Request) {
		if rejectAuthenticatedContractRead(response, request) {
			return
		}
		history, ok := proxyUpgradeHistory(request.PathValue("address"))
		if !ok {
			writeNotFound(response)
			return
		}
		writeEnvelope(response, history)
	})
	mux.HandleFunc("GET /api/v1/contracts/{address}/proxy/initializations", func(response http.ResponseWriter, request *http.Request) {
		if rejectAuthenticatedContractRead(response, request) {
			return
		}
		history, ok := proxyInitializationHistory(request.PathValue("address"))
		if !ok {
			writeNotFound(response)
			return
		}
		writeEnvelope(response, history)
	})
	mux.HandleFunc("GET /api/v1/contracts/{address}/proxy/diamond-cuts", func(response http.ResponseWriter, request *http.Request) {
		if rejectAuthenticatedContractRead(response, request) {
			return
		}
		history, ok := diamondCutHistory(request.PathValue("address"))
		if !ok {
			writeNotFound(response)
			return
		}
		writeEnvelope(response, history)
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

func chartCoverage() map[string]any {
	return map[string]any{
		"available_from": "2025-07-28T00:00:00Z",
		"available_to":   "2026-07-28T12:00:00Z",
		"complete":       true, "dirty_hours": "0",
		"backfill_state": "complete", "backfill_progress": "100",
	}
}

func chartPoint(value string) map[string]any {
	return map[string]any{
		"bucket_start": "2026-07-27T00:00:00Z",
		"bucket_end":   "2026-07-28T00:00:00Z",
		"value":        value, "partial": false, "from_block": "99", "to_block": "100",
	}
}

func canonicalBlockOne() map[string]any {
	return block("1", testHash, parentHash, true, "finalized")
}

func canonicalBlockTwo() map[string]any {
	return block("2", secondHash, testHash, true, "safe")
}

func orphanBlock() map[string]any {
	return block("1", orphanHash, parentHash, false, "orphan")
}

func block(number, hash, parent string, canonical bool, finality string) map[string]any {
	return map[string]any{
		"hash": hash, "number": number, "parent_hash": parent,
		"timestamp": "2026-01-01T00:00:00Z", "miner": testAddress,
		"transaction_count": 1, "gas_used": "21000", "gas_limit": "30000000",
		"base_fee_per_gas": "1000000000", "canonical": canonical,
		"finality": finality, "completeness": completeness(),
	}
}

func transaction(hash, blockHash, blockNumber, finality string) map[string]any {
	return map[string]any{
		"hash": hash, "block_hash": blockHash, "block_number": blockNumber, "transaction_index": 0,
		"block_timestamp": "2026-01-01T00:00:00Z",
		"from":            testAddress, "to": testAddress, "nonce": "1", "value": "900719925474099312345",
		"gas": "567028", "gas_used": "430551", "base_fee_per_gas": "112489733",
		"max_fee_per_gas": "151663696", "max_priority_fee_per_gas": "28319880",
		"type": "2", "input": "0x3fa4f245",
		"status": "success", "canonical": true, "finality": finality, "completeness": completeness(),
	}
}

func includedTransactionDetail(transaction map[string]any) map[string]any {
	return map[string]any{"kind": "included", "transaction": transaction}
}

func setCodeTransaction(hash string, transactionIndex int, input string) map[string]any {
	return map[string]any{
		"hash": hash, "block_hash": secondHash, "block_number": "2", "transaction_index": transactionIndex,
		"block_timestamp": "2026-01-01T00:00:00Z",
		"from":            testEOA, "to": delegatedAddress, "nonce": strconv.Itoa(transactionIndex), "value": "0",
		"gas": "100000", "gas_price": "1000000000", "type": "4", "input": input,
		"access_list": []any{}, "status": "success", "canonical": true, "finality": "safe",
		"completeness": completeness(),
	}
}

func delegatedCalldata() string {
	return "0x55241077000000000000000000000000000000000000000000000000000000000000002a"
}

func transactionAuthorizations(hash, transactionIndex string, items []any) map[string]any {
	return map[string]any{
		"chain_id": "1", "block_number": "2", "block_hash": secondHash,
		"transaction_hash": hash, "transaction_index": transactionIndex,
		"state": "complete", "items": items,
	}
}

func authorization(index, authority, delegate, nonce, status, skipReason string) map[string]any {
	item := map[string]any{
		"index": index, "chain_id": "1", "nonce": nonce, "delegate": delegate,
		"y_parity": 1, "r": testHash, "s": secondHash, "authority": authority,
		"signature_status": "valid", "application_status": status,
	}
	if skipReason != "" {
		item["skip_reason"] = skipReason
	}
	return item
}

func addressTokenTransfer(standard, kind string) map[string]any {
	transfer := map[string]any{
		"block_number": "2", "block_hash": secondHash, "block_timestamp": "2026-01-01T00:00:00Z",
		"transaction_hash": testTransactionHash, "transaction_index": "0",
		"log_index": "0", "sub_index": "0", "token_address": testAddress,
		"standard": standard, "kind": kind, "from": testAddress, "to": testAddress,
		"amount": "1", "confidence": "verified",
	}
	if standard == "erc20" {
		transfer["amount"] = "1234500"
		transfer["decimals"] = 6
	}
	return transfer
}

func contractCreationTransaction(hash, blockHash, blockNumber, finality string) map[string]any {
	result := transaction(hash, blockHash, blockNumber, finality)
	result["to"] = nil
	result["contract_address"] = testAddress
	result["type"] = "3"
	result["access_list"] = []any{}
	result["blob_base_fee_per_gas"] = "1000000"
	result["max_fee_per_blob_gas"] = "1000000000"
	result["blob_versioned_hashes"] = []any{testHash}
	return result
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

type homeStreamHub struct {
	mu      sync.Mutex
	streams map[string]*homeTestStream
}

func (hub *homeStreamHub) stream(session string) *homeTestStream {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	stream := hub.streams[session]
	if stream == nil {
		stream = &homeTestStream{
			eventID: 1, head: 2,
			subscribers: make(map[uint64]chan homeTestUpdate),
		}
		hub.streams[session] = stream
	}
	return stream
}

type homeTestUpdate struct {
	id      uint64
	payload []byte
}

type homeTestStream struct {
	mu          sync.Mutex
	eventID     uint64
	head        uint64
	nextID      uint64
	subscribers map[uint64]chan homeTestUpdate
}

func (stream *homeTestStream) subscribe() (<-chan homeTestUpdate, func()) {
	stream.mu.Lock()
	stream.nextID++
	id := stream.nextID
	channel := make(chan homeTestUpdate, 1)
	channel <- stream.updateLocked()
	stream.subscribers[id] = channel
	stream.mu.Unlock()
	return channel, func() {
		stream.mu.Lock()
		if current, exists := stream.subscribers[id]; exists && current == channel {
			delete(stream.subscribers, id)
			close(channel)
		}
		stream.mu.Unlock()
	}
}

func (stream *homeTestStream) advance() {
	stream.mu.Lock()
	stream.eventID++
	stream.head++
	update := stream.updateLocked()
	for id, subscriber := range stream.subscribers {
		select {
		case subscriber <- update:
		default:
			close(subscriber)
			delete(stream.subscribers, id)
		}
	}
	stream.mu.Unlock()
}

func (stream *homeTestStream) updateLocked() homeTestUpdate {
	number := strconv.FormatUint(stream.head, 10)
	hash := fmt.Sprintf("0x%064x", stream.head)
	parent := fmt.Sprintf("0x%064x", stream.head-1)
	transactionID := fmt.Sprintf("0x%064x", stream.head+1000)
	if stream.head == 2 {
		hash, parent, transactionID = secondHash, testHash, testTransactionHash
	}
	payload, err := json.Marshal(map[string]any{
		"data": map[string]any{
			"status": map[string]any{
				"chain_id": "1", "core_ready": true,
				"latest_block": number, "indexed_block": number,
				"highest_covered_block": number, "backfill_complete": true,
				"safe_block": number, "finalized_block": strconv.FormatUint(stream.head-1, 10),
				"lag": "0", "completeness": completeness(),
			},
			"blocks": []any{
				block(number, hash, parent, true, "latest"),
			},
			"transactions": []any{
				transaction(transactionID, hash, number, "latest"),
			},
		},
		"meta": map[string]any{
			"request_id": "e2e-home", "chain_id": "1",
			"coverage_start": "0", "coverage_end": number,
		},
	})
	if err != nil {
		panic(err)
	}
	return homeTestUpdate{id: stream.eventID, payload: payload}
}

func homeStreamSession(request *http.Request) string {
	cookie, err := request.Cookie("etherview_e2e_home")
	if err != nil {
		return ""
	}
	return cookie.Value
}

func contractArtifact(address string) (map[string]any, bool) {
	var contractName string
	var abi []any
	switch address {
	case testAddress:
		contractName = "TransparentUpgradeableProxy"
		abi = proxyContractABI()
	case uupsProxyAddress:
		contractName = "ERC1967Proxy"
		abi = proxyContractABI()
	case beaconProxyAddress:
		contractName = "BeaconProxy"
		abi = proxyContractABI()
	case cloneAddress:
		contractName = "MinimalClone"
		abi = proxyContractABI()
	case diamondAddress:
		contractName = "DiamondRouter"
		abi = []any{
			map[string]any{
				"type": "function", "name": "supportsInterface", "stateMutability": "view",
				"inputs":  []any{map[string]any{"name": "interfaceId", "type": "bytes4"}},
				"outputs": []any{map[string]any{"name": "supported", "type": "bool"}},
			},
		}
	case diamondWriteFacet:
		contractName = "DiamondWriteFacet"
		abi = implementationABI(false)
	case diamondLoupeFacet:
		contractName = "DiamondLoupeFacet"
		abi = diamondLoupeABI()
	case delegatedDelegate:
		contractName = "DelegatedDisperser"
		abi = []any{
			map[string]any{
				"type": "function", "name": "disperseToken", "stateMutability": "nonpayable",
				"inputs": []any{
					map[string]any{"name": "token", "type": "address"},
					map[string]any{"name": "requests", "type": "tuple[]", "components": []any{
						map[string]any{"name": "to", "type": "address"},
						map[string]any{"name": "value", "type": "uint256"},
					}},
				},
				"outputs": []any{},
			},
		}
	case transparentImplementation:
		contractName = "TransparentImplementationV2"
		abi = implementationABI(false)
	case uupsImplementation:
		contractName = "UUPSImplementationV2"
		abi = implementationABI(true)
	case beaconImplementation:
		contractName = "BeaconImplementationV2"
		abi = implementationABI(false)
	case cloneImplementation:
		contractName = "CloneImplementation"
		abi = implementationABI(false)
	case proxyAdminAddress:
		contractName = "ProxyAdmin"
		abi = []any{
			map[string]any{
				"type": "function", "name": "upgradeAndCall", "stateMutability": "payable",
				"inputs": []any{
					map[string]any{"name": "proxy", "type": "address"},
					map[string]any{"name": "implementation", "type": "address"},
					map[string]any{"name": "data", "type": "bytes"},
				},
				"outputs": []any{},
			},
		}
	case upgradeableBeacon:
		contractName = "UpgradeableBeacon"
		abi = []any{
			map[string]any{
				"type": "function", "name": "implementation", "stateMutability": "view",
				"inputs": []any{}, "outputs": []any{map[string]any{"name": "", "type": "address"}},
			},
			map[string]any{
				"type": "function", "name": "upgradeTo", "stateMutability": "nonpayable",
				"inputs":  []any{map[string]any{"name": "newImplementation", "type": "address"}},
				"outputs": []any{},
			},
		}
	default:
		return nil, false
	}

	fileName := contractName + ".sol"
	return map[string]any{
		"kind": "verification_success", "resolution": "exact_address",
		"target": map[string]any{
			"chain_id": "1", "address": address, "code_hash": testHash,
			"block_number": "100", "block_hash": secondHash,
		},
		"source": map[string]any{
			"address": address, "code_hash": testHash, "valid_from_block": "1",
			"created_at": "2026-08-02T00:00:01Z",
		},
		"language":         "solidity",
		"compiler_version": "0.8.30", "file_name": fileName,
		"contract_name": contractName, "is_blueprint": false, "abi": abi,
		"sources": map[string]any{
			fileName:            map[string]any{"content": "contract " + contractName + " {}"},
			"lib/ProxyBase.sol": map[string]any{"content": "abstract contract ProxyBase {}"},
		},
		"settings":                map[string]any{"optimizer": map[string]any{"enabled": true}},
		"compilation_artifacts":   map[string]any{},
		"creation_code_artifacts": map[string]any{},
		"runtime_code_artifacts":  map[string]any{},
		"libraries":               map[string]any{},
		"runtime_match": map[string]any{
			"match_type": "full", "transformations": []any{}, "values": map[string]any{},
		},
	}, true
}

func proxyContractABI() []any {
	return []any{
		map[string]any{
			"type": "function", "name": "proxyValue", "stateMutability": "view",
			"inputs": []any{}, "outputs": []any{map[string]any{"name": "value", "type": "uint256"}},
		},
		map[string]any{
			"type": "function", "name": "setProxyValue", "stateMutability": "nonpayable",
			"inputs":  []any{map[string]any{"name": "newValue", "type": "uint256"}},
			"outputs": []any{},
		},
	}
}

func implementationABI(uups bool) []any {
	abi := []any{
		map[string]any{
			"type": "function", "name": "value", "stateMutability": "view",
			"inputs": []any{}, "outputs": []any{map[string]any{"name": "", "type": "uint256"}},
		},
		map[string]any{
			"type": "function", "name": "setValue", "stateMutability": "nonpayable",
			"inputs":  []any{map[string]any{"name": "newValue", "type": "uint256"}},
			"outputs": []any{},
		},
	}
	if !uups {
		return abi
	}
	return append(abi,
		map[string]any{
			"type": "function", "name": "proxiableUUID", "stateMutability": "view",
			"inputs": []any{}, "outputs": []any{map[string]any{"name": "", "type": "bytes32"}},
		},
		map[string]any{
			"type": "function", "name": "upgradeToAndCall", "stateMutability": "payable",
			"inputs": []any{
				map[string]any{"name": "newImplementation", "type": "address"},
				map[string]any{"name": "data", "type": "bytes"},
			},
			"outputs": []any{},
		},
	)
}

func diamondLoupeABI() []any {
	return []any{
		map[string]any{
			"type": "function", "name": "facets", "stateMutability": "view",
			"inputs": []any{}, "outputs": []any{map[string]any{
				"name": "facets_", "type": "tuple[]", "components": []any{
					map[string]any{"name": "facetAddress", "type": "address"},
					map[string]any{"name": "functionSelectors", "type": "bytes4[]"},
				},
			}},
		},
		map[string]any{
			"type": "function", "name": "facetFunctionSelectors", "stateMutability": "view",
			"inputs":  []any{map[string]any{"name": "facet", "type": "address"}},
			"outputs": []any{map[string]any{"name": "functionSelectors_", "type": "bytes4[]"}},
		},
		map[string]any{
			"type": "function", "name": "facetAddresses", "stateMutability": "view",
			"inputs": []any{}, "outputs": []any{map[string]any{"name": "facetAddresses_", "type": "address[]"}},
		},
		map[string]any{
			"type": "function", "name": "facetAddress", "stateMutability": "view",
			"inputs":  []any{map[string]any{"name": "selector", "type": "bytes4"}},
			"outputs": []any{map[string]any{"name": "facetAddress_", "type": "address"}},
		},
		map[string]any{
			"type": "function", "name": "unregisteredLoupeHelper", "stateMutability": "view",
			"inputs": []any{}, "outputs": []any{map[string]any{"name": "value", "type": "uint256"}},
		},
	}
}

func contractProxy(address string) (map[string]any, bool) {
	if address == diamondAddress {
		return diamondProxyDetail(), true
	}
	var mechanism, pattern, implementation, bindingID, proxyArtifactKind string
	var management map[string]any
	var admin map[string]any
	var beacon map[string]any
	var immutableArgs string
	switch address {
	case testAddress:
		mechanism, pattern = "eip1967", "transparent"
		implementation = transparentImplementation
		bindingID = "018f3b52-0b3d-7bf1-b65f-6f214827cb41"
		proxyArtifactKind = "transparent_proxy"
		admin = contractIdentity(proxyAdminAddress, "proxy_admin")
		management = map[string]any{
			"kind": "proxy_admin", "target": admin, "affected_proxy_count": "1",
		}
	case uupsProxyAddress:
		mechanism, pattern = "eip1967", "uups"
		implementation = uupsImplementation
		bindingID = "018f3b52-0b3d-7bf1-b65f-6f214827cb42"
		proxyArtifactKind = "erc1967_proxy"
	case beaconProxyAddress:
		mechanism, pattern = "beacon", "beacon"
		implementation = beaconImplementation
		bindingID = "018f3b52-0b3d-7bf1-b65f-6f214827cb43"
		proxyArtifactKind = "beacon_proxy"
		beacon = contractIdentity(upgradeableBeacon, "upgradeable_beacon")
		management = map[string]any{
			"kind": "upgradeable_beacon", "target": beacon, "affected_proxy_count": "2",
		}
	case cloneAddress:
		mechanism, pattern = "eip1167", "clone"
		implementation = cloneImplementation
		bindingID = "018f3b52-0b3d-7bf1-b65f-6f214827cb44"
		immutableArgs = "0x1234"
	default:
		return nil, false
	}

	implementationKind := ""
	if pattern == "uups" {
		implementationKind = "uups_implementation"
	}
	proxyIdentity := contractIdentity(address, proxyArtifactKind)
	if pattern == "clone" {
		proxyIdentity["verification_state"] = "unverified"
	}
	detail := map[string]any{
		"address": address, "status": "verified", "snapshot": contractSnapshot(),
		"mechanism": mechanism, "pattern": pattern,
		"evidence_state": "exact", "confidence": "verified", "binding_id": bindingID,
		"proxy":          proxyIdentity,
		"implementation": contractIdentity(implementation, implementationKind),
		"evidence": []any{map[string]any{
			"source": "verified_artifact", "subject": "proxy", "result": "authoritative",
			"address": address, "code_hash": testHash,
		}},
	}
	interaction := map[string]any{
		"mechanism": mechanism, "pattern": pattern,
		"proxy":          proxyIdentity,
		"implementation": contractIdentity(implementation, implementationKind),
	}
	if beacon != nil {
		interaction["beacon"] = beacon
	}
	detail["implementation_interaction"] = interaction
	if pattern != "clone" {
		detail["standard_version"] = "5.6.1"
	}
	if admin != nil {
		detail["admin"] = admin
	}
	if beacon != nil {
		detail["beacon"] = beacon
	}
	if management != nil {
		detail["management"] = management
	}
	if immutableArgs != "" {
		detail["immutable_args"] = immutableArgs
	}
	return detail, true
}

func contractIdentity(address, artifactKind string) map[string]any {
	identity := map[string]any{
		"address": address, "code_hash": testHash, "verification_state": "verified",
	}
	if artifactKind != "" {
		identity["artifact_kind"] = artifactKind
		identity["standard_version"] = "5.6.1"
	}
	return identity
}

func contractSnapshot() map[string]any {
	return map[string]any{
		"chain_id": "1", "block_number": "42", "block_hash": testHash,
	}
}

func proxyUpgradeHistory(address string) (map[string]any, bool) {
	if address == diamondAddress {
		return nil, false
	}
	detail, ok := contractProxy(address)
	if !ok || detail["pattern"] == "clone" {
		return nil, false
	}
	implementation := detail["implementation"].(map[string]any)["address"].(string)
	changeType := "implementation"
	var management map[string]any
	switch detail["pattern"] {
	case "beacon":
		changeType = "beacon_implementation"
		management = map[string]any{
			"kind": "upgradeable_beacon",
			"target": map[string]any{
				"address": upgradeableBeacon, "code_hash": testHash, "verification_state": "verified",
			},
		}
	case "transparent":
		management = map[string]any{
			"kind": "proxy_admin",
			"target": map[string]any{
				"address": proxyAdminAddress, "code_hash": testHash, "verification_state": "verified",
			},
		}
	}
	item := map[string]any{
		"change_type": changeType, "evidence_type": "event",
		"old_implementation": map[string]any{
			"address": oldImplementation, "code_hash": secondHash, "verification_state": "verified",
		},
		"new_implementation": map[string]any{
			"address": implementation, "code_hash": testHash, "verification_state": "verified",
		},
		"block_number": "40", "block_hash": testHash,
		"block_timestamp":  "2026-08-02T00:00:00Z",
		"transaction_hash": testTransactionHash, "log_index": "0", "emitter_address": address,
	}
	if management != nil {
		item["management"] = management
	}
	return map[string]any{
		"proxy_address": address, "snapshot": contractSnapshot(),
		"coverage": map[string]any{"state": "complete", "from_block": "1", "to_block": "42"},
		"items":    []any{item},
	}, true
}

func proxyInitializationHistory(address string) (map[string]any, bool) {
	if address == diamondAddress {
		return nil, false
	}
	detail, ok := contractProxy(address)
	if !ok {
		return nil, false
	}
	implementation := detail["implementation"].(map[string]any)["address"].(string)
	return map[string]any{
		"contract_address": address, "snapshot": contractSnapshot(),
		"coverage": map[string]any{"state": "complete", "from_block": "1", "to_block": "42"},
		"items": []any{map[string]any{
			"version": "2", "block_number": "41", "block_hash": testHash,
			"block_timestamp":  "2026-08-02T00:01:00Z",
			"transaction_hash": testTransactionHash, "log_index": "1",
			"implementation": map[string]any{
				"address": implementation, "code_hash": testHash, "verification_state": "verified",
			},
		}},
	}, true
}

func diamondProxyDetail() map[string]any {
	writeSelectors := []any{"0x55241077"}
	loupeSelectors := []any{"0x7a0ed627", "0xadfca15e", "0x52ef6b2c", "0xcdffacc6"}
	facets := []any{
		map[string]any{
			"address": diamondWriteFacet, "role": "facet", "selectors": writeSelectors,
			"code_exists": true, "code_hash": testHash,
		},
		map[string]any{
			"address": diamondLoupeFacet, "role": "facet", "selectors": loupeSelectors,
			"code_exists": true, "code_hash": testHash,
		},
		map[string]any{
			"address": diamondAddress, "role": "immutable", "selectors": []any{"0x01ffc9a7"},
			"code_exists": true,
		},
	}
	selectorToFacet := map[string]any{
		"0x55241077": diamondWriteFacet,
		"0x7a0ed627": diamondLoupeFacet,
		"0xadfca15e": diamondLoupeFacet,
		"0x52ef6b2c": diamondLoupeFacet,
		"0xcdffacc6": diamondLoupeFacet,
		"0x01ffc9a7": diamondAddress,
	}
	outcome := map[string]any{
		"detector": "erc2535", "detector_version": "1.0.0", "priority": 150,
		"family": "erc2535", "variant": "diamond", "status": "confirmed", "confidence": "high",
		"proxy": diamondAddress, "implementation_path": []any{}, "canonical_proxy_shell": false,
		"implementation_has_code": false, "official_singleton": false, "singleton_changed": false,
		"targets": facets,
		"diamond": map[string]any{
			"completeness": "complete", "validation": "full", "facets": facets,
			"selector_to_facet":        selectorToFacet,
			"implementation_addresses": []any{diamondWriteFacet, diamondLoupeFacet},
			"standard_diamond_cut":     map[string]any{"status": "absent"},
			"loupe_interface_reported": true, "truncated": false,
		},
		"evidence": []any{
			map[string]any{"kind": "loupe-call", "description": "All four Loupe views agree at the snapshot block."},
			map[string]any{"kind": "facet-code", "description": "Every external facet has code at the snapshot block."},
		},
		"warnings": []any{}, "chain_id": "1", "block_number": "42", "block_hash": testHash,
	}
	return map[string]any{
		"address": diamondAddress, "status": "detected_unverified", "snapshot": contractSnapshot(),
		"implementation_addresses": []any{diamondWriteFacet, diamondLoupeFacet},
		"proxy_detection_v2": map[string]any{
			"status": "confirmed", "primary": outcome, "outcomes": []any{outcome},
			"conflicts": []any{}, "shadow_diff": map[string]any{
				"different": true, "reasons": []any{"v2_positive_legacy_not_detected"},
			},
		},
		"evidence": []any{},
	}
}

func diamondCutHistory(address string) (map[string]any, bool) {
	if address != diamondAddress {
		return nil, false
	}
	return map[string]any{
		"diamond_address": diamondAddress, "snapshot": contractSnapshot(),
		"coverage": map[string]any{"state": "complete", "from_block": "1", "to_block": "42"},
		"items": []any{map[string]any{
			"block_number": "1", "block_hash": testHash,
			"block_timestamp":  "2026-08-02T00:00:00Z",
			"transaction_hash": testTransactionHash, "transaction_index": "0", "log_index": "0",
			"init_address": diamondInitAddress, "init_calldata": "0x1234",
			"cuts": []any{
				map[string]any{
					"cut_index": 0, "action": "add", "facet_address": diamondWriteFacet,
					"selectors": []any{"0x55241077"},
				},
				map[string]any{
					"cut_index": 1, "action": "add", "facet_address": diamondLoupeFacet,
					"selectors": []any{"0x7a0ed627", "0xadfca15e", "0x52ef6b2c", "0xcdffacc6"},
				},
			},
		}},
	}, true
}

func rejectAuthenticatedContractRead(response http.ResponseWriter, request *http.Request) bool {
	for _, header := range []string{"X-API-Key", "Payment-Signature", "X-CSRF-Token"} {
		if request.Header.Get(header) == "" {
			continue
		}
		response.WriteHeader(http.StatusBadRequest)
		writeJSON(response, map[string]any{
			"error": map[string]any{
				"code": "unexpected_credentials", "message": "contract reads must be anonymous",
				"request_id": "e2e-request",
			},
		})
		return true
	}
	return false
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
