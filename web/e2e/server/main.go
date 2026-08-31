package main

import (
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	webui "github.com/islishude/etherview/web"
)

const (
	testAddress                = "0x1111111111111111111111111111111111111111"
	unverifiedAddress          = "0x1212121212121212121212121212121212121212"
	testEOA                    = "0x2222222222222222222222222222222222222222"
	delegatedAddress           = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"
	clearedDelegationAddress   = "0x7777777777777777777777777777777777777777"
	delegatedDelegate          = "0x5FbDB2315678afecb367f032d93F642f64180aa3"
	uupsProxyAddress           = "0x3000000000000000000000000000000000000003"
	uupsImplementation         = "0x4000000000000000000000000000000000000004"
	beaconProxyAddress         = "0x5000000000000000000000000000000000000005"
	beaconImplementation       = "0x6000000000000000000000000000000000000006"
	cloneAddress               = "0x7000000000000000000000000000000000000007"
	cloneImplementation        = "0x8000000000000000000000000000000000000008"
	cwiaAddress                = "0xa00000000000000000000000000000000000000a"
	cwiaReadOnlyAddress        = "0xc00000000000000000000000000000000000000c"
	cwiaUnverifiedAddress      = "0xe00000000000000000000000000000000000000e"
	cwiaImplementation         = "0xb00000000000000000000000000000000000000b"
	cwiaCodeHashImplementation = "0xf00000000000000000000000000000000000000f"
	proxyAdminAddress          = "0x9000000000000000000000000000000000000009"
	upgradeableBeacon          = "0x2000000000000000000000000000000000000020"
	transparentImplementation  = "0x3000000000000000000000000000000000000030"
	oldImplementation          = "0x4000000000000000000000000000000000000040"
	diamondAddress             = "0xd000000000000000000000000000000000000000"
	diamondWriteFacet          = "0xd100000000000000000000000000000000000001"
	diamondLoupeFacet          = "0xd200000000000000000000000000000000000002"
	diamondInitAddress         = "0xd300000000000000000000000000000000000003"
	testHash                   = "0x1111111111111111111111111111111111111111111111111111111111111111"
	secondHash                 = "0x2222222222222222222222222222222222222222222222222222222222222222"
	orphanHash                 = "0x3333333333333333333333333333333333333333333333333333333333333333"
	testTransactionHash        = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	compoundTransactionHash    = "0xabababababababababababababababababababababababababababababababab"
	secondTransactionHash      = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	pendingTransactionHash     = "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	failedTxHash               = "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	delegationTransactionHash  = "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	clearingTransactionHash    = "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	parentHash                 = "0x0000000000000000000000000000000000000000000000000000000000000000"
	blockCursor                = "blocks/snapshot + page=2"
	transactionCursor          = "transactions/snapshot?generation=7 + page=2&exact=true/#"
	searchCursor               = "search/snapshot + page=2"
	testVerificationJobID      = "123e4567-e89b-42d3-a456-426614174000"
	testReadAPIKey             = "ev_e2e_read"
)

func main() {
	mux := http.NewServeMux()
	homeStreams := &homeStreamHub{streams: make(map[string]*homeTestStream)}
	registerCoreHandlers(mux, homeStreams)
	registerResourceHandlers(mux)
	registerContractHandlers(mux)
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
	result := block("2", secondHash, testHash, true, "safe")
	result["withdrawals"] = []any{map[string]any{
		"index": "10", "validator_index": "110", "address": testAddress, "amount": "3200000000",
	}}
	return result
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

func compoundCalldata() string {
	return "0xe967f546" +
		"0000000000000000000000004444444444444444444444444444444444444444" +
		"000000000000000000000000000000000000000000000000000000000000002a" +
		"0000000000000000000000000000000000000000000000000000000000000060" +
		"0000000000000000000000000000000000000000000000000000000000000002" +
		"0000000000000000000000000000000000000000000000000000000000000001" +
		"0000000000000000000000000000000000000000000000000000000000000002" +
		"0000000000000000000000000000000000000000000000000000000000000003" +
		"0000000000000000000000000000000000000000000000000000000000000004"
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

func (stream *homeTestStream) current() homeTestUpdate {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.updateLocked()
}

func (stream *homeTestStream) subscribe() (<-chan homeTestUpdate, func()) {
	return stream.subscribeWithCurrent(true)
}

func (stream *homeTestStream) subscribeFuture() (<-chan homeTestUpdate, func()) {
	return stream.subscribeWithCurrent(false)
}

func (stream *homeTestStream) subscribeWithCurrent(current bool) (<-chan homeTestUpdate, func()) {
	stream.mu.Lock()
	stream.nextID++
	id := stream.nextID
	channel := make(chan homeTestUpdate, 1)
	if current {
		channel <- stream.updateLocked()
	}
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
	switch strings.ToLower(address) {
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
	case cwiaAddress, cwiaReadOnlyAddress:
		contractName = "SoladyLegacyCWIA"
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
	case "0x5fbdb2315678afecb367f032d93f642f64180aa3":
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
	case cwiaImplementation, cwiaCodeHashImplementation:
		contractName = "MyAccount"
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
	artifact := map[string]any{
		"kind": "verification_success", "resolution": "exact_address",
		"verification_origin": "submitted", "derived_children": []any{},
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
	}
	if strings.EqualFold(address, cwiaCodeHashImplementation) {
		artifact["resolution"] = "code_hash"
		artifact["source"].(map[string]any)["address"] = cwiaImplementation
	}
	if strings.EqualFold(address, testAddress) {
		artifact["verification_origin"] = "factory_derived"
		artifact["derived_from"] = map[string]any{
			"creator_address": uupsProxyAddress, "created_address": testAddress,
			"transaction_hash": testTransactionHash, "trace_path": "0.1",
			"call_type": "CREATE2", "block_number": "100", "block_hash": secondHash,
			"parent_file_name": "Factory.sol", "parent_contract_name": "Factory",
		}
		artifact["derived_children"] = []any{map[string]any{
			"address": uupsImplementation, "transaction_hash": secondTransactionHash,
			"trace_path": "0.2", "call_type": "CREATE", "block_number": "101",
			"block_hash": testHash, "status": "matched", "auto_verified": true,
			"file_name": "Child.sol", "contract_name": "Child",
		}}
	}
	return artifact, true
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
