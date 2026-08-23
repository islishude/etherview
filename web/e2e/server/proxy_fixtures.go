package main

import (
	"fmt"
	"strings"
)

func contractProxy(address string) (map[string]any, bool) {
	normalizedAddress := strings.ToLower(address)
	if normalizedAddress == diamondAddress {
		return diamondProxyDetail(), true
	}
	var mechanism, pattern, implementation, bindingID, proxyArtifactKind string
	var management map[string]any
	var admin map[string]any
	var beacon map[string]any
	var immutableArgs string
	switch normalizedAddress {
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
	case cwiaAddress:
		mechanism, pattern = "cwia", "clone"
		implementation = cwiaImplementation
		bindingID = "018f3b52-0b3d-7bf1-b65f-6f214827cb45"
		immutableArgs = cwiaFixtureImmutableArgs()
	case cwiaReadOnlyAddress:
		mechanism, pattern = "cwia", "clone"
		implementation = cwiaImplementation
		bindingID = "018f3b52-0b3d-7bf1-b65f-6f214827cb46"
		immutableArgs = cwiaFixtureImmutableArgs()
	case cwiaUnverifiedAddress:
		mechanism, pattern = "cwia", "clone"
		implementation = cwiaCodeHashImplementation
		immutableArgs = cwiaFixtureImmutableArgs()
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
		delete(proxyIdentity, "artifact_resolution")
	}
	status, confidence := "verified", "verified"
	if normalizedAddress == cwiaUnverifiedAddress {
		status, confidence = "detected_unverified", "high"
	}
	implementationIdentity := contractIdentity(implementation, implementationKind)
	if normalizedAddress == cwiaUnverifiedAddress {
		implementationIdentity["verification_state"] = "unverified"
		implementationIdentity["artifact_resolution"] = "code_hash"
	}
	detail := map[string]any{
		"address": address, "status": status, "snapshot": contractSnapshot(),
		"mechanism": mechanism, "pattern": pattern,
		"evidence_state": "exact", "confidence": confidence,
		"proxy":          proxyIdentity,
		"implementation": implementationIdentity,
		"evidence": []any{map[string]any{
			"source": "runtime_code", "subject": "proxy", "result": "authoritative",
			"address": address, "code_hash": testHash,
		}},
	}
	if bindingID != "" {
		detail["binding_id"] = bindingID
	}
	interaction := map[string]any{
		"mechanism": mechanism, "pattern": pattern,
		"proxy":          proxyIdentity,
		"implementation": implementationIdentity,
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
	if mechanism == "cwia" {
		switch normalizedAddress {
		case cwiaAddress:
			detail["immutable_args_decoding"] = cwiaFixtureDecoding("exact_address")
		case cwiaReadOnlyAddress:
			detail["immutable_args_decoding"] = map[string]any{
				"status": "schema_unavailable", "reason": "ast_unavailable", "arguments": []any{},
			}
		default:
			detail["immutable_args_decoding"] = cwiaFixtureDecoding("code_hash")
		}
	}
	return detail, true
}

func cwiaFixtureImmutableArgs() string {
	return "0x" + testEOA[2:] + fmt.Sprintf("%064x", 0x2a) + "000b68656c6c6f2c776f726c64"
}

func cwiaFixtureDecoding(resolution string) map[string]any {
	return map[string]any{
		"status": "decoded", "schema_resolution": resolution,
		"schema": map[string]any{
			"version": 2, "source": "solidity_ast", "encoding": "solady-cwia-offsets",
			"helper_sha256": "0xbc97b0d077a3c5d5603808caeeb3fe572dcb2448c5536b66316d1b6b129cfca3",
			"sha256":        orphanHash,
			"fields": []any{
				map[string]any{"name": "owner", "type": "address", "offset": 0, "role": "value", "getters": []any{"owner()"}, "size": map[string]any{"kind": "fixed", "bytes": 20}},
				map[string]any{"name": "number", "type": "uint256", "offset": 20, "role": "value", "getters": []any{"number()"}, "size": map[string]any{"kind": "fixed", "bytes": 32}},
				map[string]any{"name": "data_length", "type": "uint16", "offset": 52, "role": "length", "getters": []any{"data()"}, "size": map[string]any{"kind": "fixed", "bytes": 2}},
				map[string]any{"name": "data", "type": "bytes", "offset": 54, "role": "value", "getters": []any{"data()"}, "size": map[string]any{"kind": "field", "field": "data_length", "multiplier": 1}},
			},
		},
		"arguments": []any{
			map[string]any{"name": "owner", "type": "address", "offset": 0, "length": 20, "value": testEOA},
			map[string]any{"name": "number", "type": "uint256", "offset": 20, "length": 32, "value": "42"},
			map[string]any{"name": "data_length", "type": "uint16", "offset": 52, "length": 2, "value": "11"},
			map[string]any{"name": "data", "type": "bytes", "offset": 54, "length": 11, "value": "0x68656c6c6f2c776f726c64"},
		},
	}
}

func contractIdentity(address, artifactKind string) map[string]any {
	identity := map[string]any{
		"address": address, "code_hash": testHash, "verification_state": "verified",
		"artifact_resolution": "exact_address",
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
