package consensus

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	dynssz "github.com/pk910/dynamic-ssz"
)

// ExecutionRequest is an exact protocol record. Sequence is local to its type;
// Index is the deposit contract's global index and is a different identity.
type ExecutionRequest struct {
	Type                  string `json:"request_type"`
	Sequence              string `json:"sequence_index"`
	Pubkey                string `json:"pubkey,omitempty"`
	WithdrawalCredentials string `json:"withdrawal_credentials,omitempty"`
	Amount                string `json:"amount,omitempty"`
	Signature             string `json:"signature,omitempty"`
	Index                 string `json:"index,omitempty"`
	SourceAddress         string `json:"source_address,omitempty"`
	ValidatorPubkey       string `json:"validator_pubkey,omitempty"`
	SourcePubkey          string `json:"source_pubkey,omitempty"`
	TargetPubkey          string `json:"target_pubkey,omitempty"`
}

type RequestSet struct {
	Counts       map[string]string  `json:"counts"`
	Total        string             `json:"total"`
	BlockHash    string             `json:"block_hash"`
	ParentHash   string             `json:"parent_hash"`
	BeaconRoot   string             `json:"beacon_root"`
	Fork         string             `json:"fork"`
	RequestsHash string             `json:"requests_hash"`
	Items        []ExecutionRequest `json:"items"`
}

var requestNames = []string{"deposits", "withdrawals", "consolidations", "builder_deposits", "builder_exits"}
var requestFields = [][]string{
	{"pubkey", "withdrawal_credentials", "amount", "signature", "index"},
	{"source_address", "validator_pubkey", "amount"},
	{"source_address", "source_pubkey", "target_pubkey"},
	{"pubkey", "withdrawal_credentials", "amount", "signature"},
	{"source_address", "pubkey"},
}
var requestLimits = []int{8192, 16, 2, 64, 16}
var requestLimitKeys = []string{"MAX_DEPOSIT_REQUESTS_PER_PAYLOAD", "MAX_WITHDRAWAL_REQUESTS_PER_PAYLOAD", "MAX_CONSOLIDATION_REQUESTS_PER_PAYLOAD", "MAX_BUILDER_DEPOSIT_REQUESTS_PER_PAYLOAD", "MAX_BUILDER_EXIT_REQUESTS_PER_PAYLOAD"}

// ObserveRequests always binds requests to the exact indexed execution header.
// It is independent of the consensus and blob jobs: envelope outages cannot
// revoke those capabilities.
func (c *Client) ObserveRequests(ctx context.Context, network Network, block *types.Block) (set RequestSet, err error) {
	defer func() {
		if recover() != nil {
			set = RequestSet{}
			err = failure("beacon_requests_invalid")
		}
	}()
	if block.RequestsHash() == nil {
		return RequestSet{}, failure("not_applicable")
	}
	observed, err := c.Observe(ctx, network, block)
	if err != nil {
		return RequestSet{}, err
	}
	raw := observed.requestBody
	switch observed.Fork {
	case "electra", "fulu":
	case "gloas":
		var bid gloas.SignedExecutionPayloadBid
		if json.Unmarshal(observed.requestBid, &bid) != nil || bid.Message == nil {
			return RequestSet{}, failure("beacon_requests_identity")
		}
		var envelope struct {
			Version string                                `json:"version"`
			Data    *gloas.SignedExecutionPayloadEnvelope `json:"data"`
		}
		// Retain raw request keys too: protocol decoders intentionally ignore unknown fields.
		var wire struct {
			Version string `json:"version"`
			Data    struct {
				Message struct {
					Requests json.RawMessage `json:"execution_requests"`
				} `json:"message"`
			} `json:"data"`
		}
		var response json.RawMessage
		if err := c.get(ctx, "/eth/v1/beacon/execution_payload_envelopes/"+observed.Root, &response); err != nil {
			return RequestSet{}, err
		}
		if json.Unmarshal(response, &wire) != nil || wire.Version != "gloas" {
			return RequestSet{}, failure("beacon_requests_invalid")
		}
		raw = wire.Data.Message.Requests
		if _, _, err := decodeRequests(raw, observed.Fork, network.BodySpec); err != nil {
			return RequestSet{}, err
		}
		if json.Unmarshal(response, &envelope) != nil || envelope.Version != "gloas" || envelope.Data == nil || envelope.Data.Message == nil {
			return RequestSet{}, failure("beacon_requests_invalid")
		}
		message := envelope.Data.Message
		if message.Payload == nil || message.ExecutionRequests == nil || common.Hash(message.BeaconBlockRoot).Hex() != observed.Root || common.Hash(message.ParentBeaconBlockRoot).Hex() != observed.Parent || message.BuilderIndex != bid.Message.BuilderIndex || common.Hash(message.Payload.BlockHash) != block.Hash() || common.Hash(message.Payload.ParentHash) != block.ParentHash() || common.Hash(bid.Message.ParentBlockRoot).Hex() != observed.Parent {
			return RequestSet{}, failure("beacon_requests_identity")
		}
		root, err := requestRoot(message.ExecutionRequests, network.BodySpec)
		if err != nil || root != common.Hash(bid.Message.ExecutionRequestsRoot) {
			return RequestSet{}, failure("beacon_requests_identity")
		}
	default:
		return RequestSet{}, failure("unsupported_fork")
	}
	items, encoded, err := decodeRequests(raw, observed.Fork, network.BodySpec)
	if err != nil {
		return RequestSet{}, err
	}
	hash := types.CalcRequestsHash(encoded)
	if hash != *block.RequestsHash() {
		return RequestSet{}, failure("requests_hash_mismatch")
	}
	counts := requestCounts(items)
	return RequestSet{Counts: counts, Total: strconv.Itoa(len(items)), BlockHash: block.Hash().Hex(), ParentHash: block.ParentHash().Hex(), BeaconRoot: observed.Root, Fork: observed.Fork, RequestsHash: hash.Hex(), Items: items}, nil
}

func requestRoot(value any, spec map[string]any) (root common.Hash, err error) {
	defer func() {
		if recover() != nil {
			err = failure("beacon_requests_invalid")
		}
	}()
	hash, err := dynssz.NewDynSsz(spec, dynssz.WithNoFastHash()).HashTreeRoot(value)
	return common.Hash(hash), err
}

func decodeRequests(raw json.RawMessage, fork string, spec map[string]any) ([]ExecutionRequest, [][]byte, error) {
	var groups map[string]json.RawMessage
	count := 3
	if fork == "gloas" {
		count = 5
	} else if fork != "electra" && fork != "fulu" {
		return nil, nil, failure("unsupported_fork")
	}
	if json.Unmarshal(raw, &groups) != nil || len(groups) != count {
		return nil, nil, failure("beacon_requests_invalid")
	}
	items := []ExecutionRequest{}
	encoded := make([][]byte, 0, count)
	for kind, name := range requestNames[:count] {
		group, ok := groups[name]
		decoder := json.NewDecoder(bytes.NewReader(group))
		token, err := decoder.Token()
		if !ok || err != nil || token != json.Delim('[') {
			return nil, nil, failure("beacon_requests_invalid")
		}
		limit := requestLimits[kind]
		if n, ok := spec[requestLimitKeys[kind]].(uint64); ok {
			if n == 0 || n > 8192 {
				return nil, nil, failure("beacon_requests_limit")
			}
			limit = int(n)
		}
		data := []byte{byte(kind)}
		for i := 0; decoder.More(); i++ {
			// Admit each record before decoding or allocating its protocol structure.
			if i >= limit {
				return nil, nil, failure("beacon_requests_limit")
			}
			var record json.RawMessage
			if decoder.Decode(&record) != nil || len(record) > 2048 {
				return nil, nil, failure("beacon_requests_invalid")
			}
			normalized, bytes, err := decodeRequest(kind, record)
			if err != nil {
				return nil, nil, err
			}
			normalized.Type = fmt.Sprintf("0x%02x", kind)
			normalized.Sequence = strconv.Itoa(i)
			items = append(items, normalized)
			data = append(data, bytes...)
		}
		encoded = append(encoded, data)
	}
	return items, encoded, nil
}

func decodeRequest(kind int, raw json.RawMessage) (ExecutionRequest, []byte, error) {
	var fields map[string]string
	if json.Unmarshal(raw, &fields) != nil || len(fields) != len(requestFields[kind]) {
		return ExecutionRequest{}, nil, failure("beacon_requests_invalid")
	}
	encoded := []byte{}
	for _, key := range requestFields[kind] {
		value, ok := fields[key]
		if !ok {
			return ExecutionRequest{}, nil, failure("beacon_requests_invalid")
		}
		if key == "amount" || key == "index" {
			n, err := quantity(value)
			if err != nil {
				return ExecutionRequest{}, nil, failure("beacon_requests_invalid")
			}
			encoded = binary.LittleEndian.AppendUint64(encoded, n)
		} else {
			size := 48
			switch key {
			case "source_address":
				size = 20
			case "withdrawal_credentials":
				size = 32
			case "signature":
				size = 96
			}
			if !fixedHex(value, size) {
				return ExecutionRequest{}, nil, failure("beacon_requests_invalid")
			}
			bytes, _ := hex.DecodeString(value[2:])
			encoded = append(encoded, bytes...)
			fields[key] = "0x" + hex.EncodeToString(bytes)
		}
	}
	normalized, _ := json.Marshal(fields)
	var result ExecutionRequest
	if json.Unmarshal(normalized, &result) != nil {
		return ExecutionRequest{}, nil, failure("beacon_requests_invalid")
	}
	return result, encoded, nil
}

// ValidateRequestSet rechecks retained protocol bytes before a new publication.
func ValidateRequestSet(set RequestSet, block *types.Block) error {
	counts := requestCounts(set.Items)
	if set.Total != strconv.Itoa(len(set.Items)) || len(set.Counts) != 5 {
		return failure("beacon_requests_invalid")
	}
	for key, n := range counts {
		if set.Counts[key] != n {
			return failure("beacon_requests_invalid")
		}
	}
	if block.RequestsHash() == nil || set.BlockHash != block.Hash().Hex() || set.ParentHash != block.ParentHash().Hex() || !fixedHex(set.BeaconRoot, 32) || set.RequestsHash != block.RequestsHash().Hex() {
		return failure("requests_identity_mismatch")
	}
	count := 3
	if set.Fork == "gloas" {
		count = 5
	} else if set.Fork != "electra" && set.Fork != "fulu" {
		return failure("unsupported_fork")
	}
	groups := make([][]byte, count)
	sequences := make([]int, count)
	for i := range groups {
		groups[i] = []byte{byte(i)}
	}
	previous := -1
	for _, item := range set.Items {
		kind := -1
		for i := 0; i < count; i++ {
			if item.Type == fmt.Sprintf("0x%02x", i) {
				kind = i
			}
		}
		if kind < previous || kind < 0 || item.Sequence != strconv.Itoa(sequences[kind]) || sequences[kind] >= 8192 {
			return failure("beacon_requests_invalid")
		}
		fields := map[string]string{}
		raw, _ := json.Marshal(item)
		_ = json.Unmarshal(raw, &fields)
		delete(fields, "request_type")
		delete(fields, "sequence_index")
		raw, _ = json.Marshal(fields)
		_, data, err := decodeRequest(kind, raw)
		if err != nil {
			return err
		}
		groups[kind] = append(groups[kind], data...)
		sequences[kind]++
		previous = kind
	}
	if types.CalcRequestsHash(groups) != *block.RequestsHash() {
		return failure("requests_hash_mismatch")
	}
	return nil
}

func requestCounts(items []ExecutionRequest) map[string]string {
	counts := map[string]string{"0x00": "0", "0x01": "0", "0x02": "0", "0x03": "0", "0x04": "0"}
	for _, item := range items {
		n, _ := strconv.Atoi(counts[item.Type])
		counts[item.Type] = strconv.Itoa(n + 1)
	}
	return counts
}
