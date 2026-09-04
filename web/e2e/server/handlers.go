package main

import (
	"fmt"
	"net/http"
	"time"
)

func registerCoreHandlers(mux *http.ServeMux, homeStreams *homeStreamHub) {
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
				"sourcify": false, "user_operations": true,
			},
		})
	})
	mux.HandleFunc("GET /api/v1/home", func(response http.ResponseWriter, request *http.Request) {
		update := homeStreams.stream(homeStreamSession(request)).current()
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write(update.payload)
	})
	mux.HandleFunc("GET /api/v1/events", func(response http.ResponseWriter, request *http.Request) {
		stream := homeStreams.stream(homeStreamSession(request))
		channel, unsubscribe := stream.subscribeFuture()
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
		for {
			select {
			case update, open := <-channel:
				if !open {
					return
				}
				if _, err := fmt.Fprintf(
					response, "id: %d\nevent: head\ndata: {\"number\":\"%d\"}\n\n",
					update.id, update.id,
				); err != nil {
					return
				}
				flusher.Flush()
			case <-request.Context().Done():
				return
			}
		}
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
			item := contractCreationTransaction(secondTransactionHash, testHash, "1", "finalized")
			item["method"] = "Contract Creation"
			writeEnvelope(response, []any{item})
			return
		}
		item := transaction(testTransactionHash, secondHash, "2", "safe")
		item["method"] = "valueWithAnIntentionallyLongMethodName"
		item["method_signature"] = "valueWithAnIntentionallyLongMethodName(uint256,address)"
		writeEnvelopeMeta(
			response,
			[]any{item},
			map[string]any{"next_cursor": transactionCursor},
		)
	})
	mux.HandleFunc("GET /api/v1/transactions/{hash}", func(response http.ResponseWriter, request *http.Request) {
		switch request.PathValue("hash") {
		case testTransactionHash:
			writeEnvelope(response, includedTransactionDetail(transaction(testTransactionHash, secondHash, "2", "safe")))
		case compoundTransactionHash:
			item := transaction(compoundTransactionHash, secondHash, "2", "safe")
			item["input"] = compoundCalldata()
			writeEnvelope(response, includedTransactionDetail(item))
		case secondTransactionHash:
			writeEnvelope(response, includedTransactionDetail(contractCreationTransaction(secondTransactionHash, testHash, "1", "finalized")))
		case failedTxHash:
			item := transaction(failedTxHash, testHash, "1", "finalized")
			item["status"] = "failed"
			item["input"] = "0x85bb7d69"
			writeEnvelope(response, includedTransactionDetail(item))
		case delegationTransactionHash:
			writeEnvelope(response, includedTransactionDetail(setCodeTransaction(delegationTransactionHash, 1, delegatedCalldata())))
		case clearingTransactionHash:
			writeEnvelope(response, includedTransactionDetail(setCodeTransaction(clearingTransactionHash, 2, "0x55241077")))
		default:
			writeNotFound(response)
		}
	})
	mux.HandleFunc("GET /api/v1/user-operations", func(response http.ResponseWriter, _ *http.Request) {
		writeEnvelopeMeta(response, []any{userOperationSummary(nil)}, map[string]any{
			"coverage_start": "0", "coverage_end": "2",
		})
	})
	mux.HandleFunc("GET /api/v1/user-operations/{hash}", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("hash") != userOperationHash {
			writeNotFound(response)
			return
		}
		writeEnvelope(response, userOperationDetail())
	})
	mux.HandleFunc("GET /api/v1/transactions/{hash}/user-operations", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("hash") != testTransactionHash {
			writeEnvelope(response, []any{})
			return
		}
		writeEnvelopeMeta(response, []any{userOperationSummary(nil)}, map[string]any{
			"coverage_start": "0", "coverage_end": "2",
		})
	})
	mux.HandleFunc("GET /api/v1/transactions/{hash}/failure", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("hash") != failedTxHash {
			writeNotFound(response)
			return
		}
		writeEnvelope(response, map[string]any{
			"chain_id": "1", "block_number": "1", "block_hash": testHash,
			"transaction_hash": failedTxHash, "transaction_index": "0", "state": "complete",
			"error": "execution reverted", "revert_data": "0x85bb7d69",
			"execution": map[string]any{
				"context_address": testAddress, "address": testAddress,
				"code_hash": testHash, "resolution": "direct",
			},
			"decoding": map[string]any{
				"status": "decoded", "error_name": "TransferRejected",
				"signature": "TransferRejected(address,uint256,(address,uint256),uint256[],uint8[3][])",
				"arguments": []any{
					map[string]any{"name": "sender", "type": "address", "value": testAddress, "components": []any{}},
					map[string]any{"name": "amount", "type": "uint256", "value": "42", "components": []any{}},
					map[string]any{
						"name": "pair", "type": "tuple", "value": []any{testAddress, "7"},
						"components": []any{
							map[string]any{"name": "account", "type": "address", "components": []any{}},
							map[string]any{"name": "value", "type": "uint256", "components": []any{}},
						},
					},
					map[string]any{"name": "values", "type": "uint256[]", "value": []any{"1", "2"}, "components": []any{}},
					map[string]any{"name": "items", "type": "uint8[3][]", "value": []any{[]any{"3", "4", "5"}}, "components": []any{}},
				},
				"candidates": []any{"TransferRejected(address,uint256,(address,uint256),uint256[],uint8[3][])"},
				"abi_source": map[string]any{"kind": "exact_address", "address": testAddress, "code_hash": testHash},
			},
		})
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
					"code_hash": testHash, "resolution": "direct", "evidence_source": "prestate_tracer",
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
		case compoundTransactionHash:
			writeEnvelope(response, map[string]any{
				"chain_id": "1", "block_number": "2", "block_hash": secondHash,
				"transaction_hash": compoundTransactionHash, "transaction_index": "0", "state": "complete",
				"input": compoundCalldata(),
				"execution": map[string]any{
					"context_address": testAddress, "address": testAddress,
					"code_hash": testHash, "resolution": "direct", "evidence_source": "prestate_tracer",
				},
				"decoding": map[string]any{
					"status": "decoded", "function_name": "configure",
					"signature": "configure((address,uint256),uint8[2][])",
					"inputs": []any{
						map[string]any{
							"name": "config", "type": "tuple", "internal_type": "struct Fixture.Config",
							"value": []any{"0x4444444444444444444444444444444444444444", "42"},
							"components": []any{
								map[string]any{"name": "owner", "type": "address", "components": []any{}},
								map[string]any{"name": "amount", "type": "uint256", "components": []any{}},
							},
						},
						map[string]any{
							"name": "pairs", "type": "uint8[2][]",
							"value": []any{[]any{"1", "2"}, []any{"3", "4"}}, "components": []any{},
						},
					},
					"candidates": []any{"configure((address,uint256),uint8[2][])"},
					"abi_source": map[string]any{
						"kind": "exact_address", "address": testAddress, "code_hash": testHash,
					},
					"confidence": "verified",
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
					"evidence_source": "root_trace_code_observation",
				},
				"decoding": map[string]any{
					"status": "decoded", "function_name": "setValue", "signature": "setValue(uint256)",
					"inputs": []any{map[string]any{
						"name": "value", "type": "uint256", "value": "42", "components": []any{},
					}},
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
					"context_address": delegatedAddress, "resolution": "empty", "evidence_source": "prestate_tracer",
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
}
func registerResourceHandlers(mux *http.ServeMux) {
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
		case cwiaUnverifiedAddress:
			writeEnvelope(response, map[string]any{
				"address": cwiaUnverifiedAddress, "type": "contract", "balance": "0", "nonce": "1",
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
		item := transaction(testTransactionHash, secondHash, "2", "finalized")
		item["method"] = "valueWithAnIntentionallyLongMethodName"
		item["method_signature"] = "valueWithAnIntentionallyLongMethodName(uint256,address)"
		failedItem := transaction(failedTxHash, testHash, "1", "finalized")
		failedItem["status"] = "failed"
		failedItem["method"] = "disperseToken"
		writeEnvelope(response, []any{item, failedItem})
	})
	mux.HandleFunc("GET /api/v1/addresses/{address}/user-operations", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("address") != testEOA {
			writeEnvelope(response, []any{})
			return
		}
		writeEnvelopeMeta(response, []any{userOperationSummary([]string{"sender"})}, map[string]any{
			"coverage_start": "0", "coverage_end": "2",
		})
	})
	mux.HandleFunc("GET /api/v1/addresses/{address}/withdrawals", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("address") != testAddress {
			writeEnvelope(response, []any{})
			return
		}
		writeEnvelope(response, []any{
			map[string]any{
				"index": "10", "validator_index": "110", "address": testAddress,
				"amount": "3200000000", "block_number": "2", "block_hash": secondHash,
				"block_timestamp": "2026-01-01T00:00:00Z",
			},
			map[string]any{
				"index": "2", "validator_index": "102", "address": testAddress,
				"amount": "1", "block_number": "1", "block_hash": testHash,
				"block_timestamp": "2025-12-31T00:00:00Z",
			},
		})
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
		if request.PathValue("address") == testHolderToken {
			writeEnvelope(response, holderTokenContract())
			return
		}
		if request.PathValue("address") != testAddress {
			writeNotFound(response)
			return
		}
		writeEnvelope(response, tokenContract())
	})
	mux.HandleFunc("GET /api/v1/tokens/{address}/transfers", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("address") == testHolderToken {
			writeEnvelope(response, []any{})
			return
		}
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
	mux.HandleFunc("GET /api/v1/tokens/{address}/holders", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("address") != testHolderToken {
			writeNotFound(response)
			return
		}
		writeEnvelopeMeta(response, []any{map[string]any{
			"chain_id": "1", "token_address": testHolderToken, "holder_address": testEOA,
			"balance": "7000000", "confidence": "rpc_exact",
			"observed_block_number": "2", "observed_block_hash": secondHash,
		}}, map[string]any{
			"snapshot_block_number": "2", "snapshot_block_hash": secondHash,
			"coverage_start": "0", "coverage_end": "2", "holder_count": "1",
			"total_supply": "7000000", "reconciled_balance_sum": "7000000",
		})
	})
	mux.HandleFunc("GET /api/v1/tokens/{address}/holders/count", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("address") != testHolderToken {
			writeNotFound(response)
			return
		}
		writeEnvelopeMeta(response, map[string]any{
			"chain_id": "1", "token_address": testHolderToken, "holder_count": "1",
		}, map[string]any{
			"snapshot_block_number": "2", "snapshot_block_hash": secondHash,
			"coverage_start": "0", "coverage_end": "2", "holder_count": "1",
			"total_supply": "7000000", "reconciled_balance_sum": "7000000",
		})
	})
	mux.HandleFunc("GET /api/v1/nfts/{address}/{token_id}", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("address") != testAddress ||
			request.PathValue("token_id") != "1" && request.PathValue("token_id") != "2" {
			writeNotFound(response)
			return
		}
		writeEnvelope(response, map[string]any{
			"chain_id": "1", "token_address": testAddress, "token_id": request.PathValue("token_id"),
			"owner": testAddress, "balance": "1", "confidence": "rpc_exact",
			"snapshot": map[string]any{
				"chain_id": "1", "block_number": "2", "block_hash": secondHash,
			},
		})
	})
	mux.HandleFunc("GET /api/v1/nfts/{address}/{token_id}/metadata", func(response http.ResponseWriter, request *http.Request) {
		if request.PathValue("address") != testAddress ||
			request.PathValue("token_id") != "1" && request.PathValue("token_id") != "2" {
			writeNotFound(response)
			return
		}
		if request.PathValue("token_id") == "2" {
			writeEnvelope(response, map[string]any{
				"chain_id": "1", "token_address": testAddress, "token_id": "2",
				"state": "pending",
				"observation": map[string]any{
					"chain_id": "1", "block_number": "3", "block_hash": orphanHash,
				},
				"content_observation": map[string]any{
					"chain_id": "1", "block_number": "2", "block_hash": secondHash,
				},
				"content_stale": true,
				"name":          "Prior Collectible #2", "name_truncated": false,
				"description": "Prior canonical metadata remains visible.", "description_truncated": false,
				"attributes":              []any{map[string]any{"trait_type": "Version", "value": "1"}},
				"omitted_attribute_count": 0,
				"image": map[string]any{
					"state": "available", "url": "https://media.example.invalid/nft.png?token=fixture", "source_scheme": "https",
				},
			})
			return
		}
		writeEnvelope(response, map[string]any{
			"chain_id": "1", "token_address": testAddress, "token_id": "1",
			"state": "available",
			"observation": map[string]any{
				"chain_id": "1", "block_number": "2", "block_hash": secondHash,
			},
			"content_observation": map[string]any{
				"chain_id": "1", "block_number": "2", "block_hash": secondHash,
			},
			"content_stale": false,
			"name":          "Example Collectible #1", "name_truncated": false,
			"description": "Plain fixture metadata; no image is embedded.", "description_truncated": false,
			"attributes": []any{map[string]any{
				"trait_type": "Level", "value": "9007199254740993", "display_type": "number",
			}},
			"omitted_attribute_count": 1,
			"image": map[string]any{
				"state": "available", "url": "https://media.example.invalid/nft.png?token=fixture", "source_scheme": "https",
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
}
func registerContractHandlers(mux *http.ServeMux) {
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
		if query == userOperationHash {
			writeEnvelope(response, []any{map[string]any{
				"kind": "user_operation", "key": userOperationHash,
				"label": "UserOperation · " + testEOA, "rank": 95, "canonical": true,
			}})
			return
		}
		writeEnvelope(response, []any{})
	})
}
