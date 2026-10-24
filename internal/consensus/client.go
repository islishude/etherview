// Package consensus implements bounded, root-pinned Beacon observations.
package consensus

import (
	"bytes"
	"context"

	"encoding/hex"
	"encoding/json"
	"io"
	"math/bits"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	beacontypes "github.com/ethereum/go-ethereum/beacon/types"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/config"
)

type Error struct {
	Code  string
	Retry bool
}

func (e *Error) Error() string  { return e.Code }
func failure(code string) error { return &Error{Code: code} }

type Client struct {
	cfg  config.ConsensusConfig
	http *http.Client
}

func NewClient(cfg config.ConsensusConfig) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	return &Client{cfg: cfg, http: &http.Client{Transport: transport, Timeout: cfg.RequestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) get(ctx context.Context, path string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.cfg.URL, "/")+path, nil)
	if err != nil {
		return failure("invalid_endpoint")
	}
	for k, v := range c.cfg.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return &Error{Code: "beacon_transport", Retry: true}
	}
	defer func() { _ = response.Body.Close() }()
	switch {
	case response.StatusCode == 404:
		return failure("beacon_not_found")
	case response.StatusCode == 405 || response.StatusCode == 501:
		return failure("beacon_unsupported_endpoint")
	case response.StatusCode == 429 || response.StatusCode >= 500:
		return &Error{Code: "beacon_unavailable", Retry: true}
	case response.StatusCode != 200:
		return failure("beacon_rejected")
	}
	if response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity" {
		return failure("beacon_encoding")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, c.cfg.MaxResponseBytes+1))
	if err != nil {
		return &Error{Code: "beacon_transport", Retry: true}
	}
	if int64(len(raw)) > c.cfg.MaxResponseBytes {
		return failure("beacon_response_limit")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if dec.Decode(target) != nil {
		return failure("beacon_malformed")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return failure("beacon_malformed")
	}
	return nil
}

type Network struct {
	BodySpec map[string]any `json:"-"`
	Root     string         `json:"root"`
	Version  string         `json:"genesis_fork_version"`
	Genesis  uint64         `json:"genesis"`
	Millis   uint64         `json:"slot_millis"`
	Slots    uint64         `json:"slots"`
	ChainID  uint64         `json:"chain_id"`
}

func (c *Client) Network(ctx context.Context, chain uint64) (Network, error) {
	var genesis struct {
		Data struct {
			Time    string `json:"genesis_time"`
			Root    string `json:"genesis_validators_root"`
			Version string `json:"genesis_fork_version"`
		}
	}
	var deposit struct {
		Data struct {
			Chain string `json:"chain_id"`
		}
	}
	if err := c.get(ctx, "/eth/v1/beacon/genesis", &genesis); err != nil {
		return Network{}, err
	}
	// Spec contains non-string entries (e.g. BLOB_SCHEDULE); extract only the required constants.
	var values struct{ Data map[string]json.RawMessage }
	if err := c.get(ctx, "/eth/v1/config/spec", &values); err != nil {
		return Network{}, err
	}
	var slots string
	millis, durationErr := slotDuration(values.Data)
	if durationErr != nil || json.Unmarshal(values.Data["SLOTS_PER_EPOCH"], &slots) != nil {
		return Network{}, failure("beacon_spec")
	}
	if err := c.get(ctx, "/eth/v1/config/deposit_contract", &deposit); err != nil {
		return Network{}, err
	}
	id, e1 := quantity(deposit.Data.Chain)
	start, e2 := quantity(genesis.Data.Time)
	epoch, e4 := quantity(slots)
	if e1 != nil || e2 != nil || e4 != nil || id != chain || epoch == 0 || (!fixedHex(genesis.Data.Root, 32) || !fixedHex(genesis.Data.Version, 4)) {
		return Network{}, failure("beacon_network_mismatch")
	}
	spec, err := bodySpec(values.Data)
	if err != nil {
		return Network{}, err
	}
	return Network{Root: strings.ToLower(genesis.Data.Root), Version: strings.ToLower(genesis.Data.Version), Genesis: start, Millis: millis, Slots: epoch, ChainID: id, BodySpec: spec}, nil
}

func slotDuration(values map[string]json.RawMessage) (uint64, error) {
	var duration uint64
	for _, key := range []string{"SLOT_DURATION_MS", "SECONDS_PER_SLOT"} {
		raw, exists := values[key]
		if !exists {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return 0, failure("beacon_spec")
		}
		n, err := quantity(value)
		if err != nil || n == 0 {
			return 0, failure("beacon_spec")
		}
		if key == "SECONDS_PER_SLOT" {
			high, low := bits.Mul64(n, 1000)
			if high != 0 {
				return 0, failure("beacon_spec")
			}
			n = low
		}
		if duration != 0 && duration != n {
			return 0, failure("beacon_spec")
		}
		duration = n
	}
	if duration == 0 {
		return 0, failure("beacon_spec")
	}
	return duration, nil
}

type headerResponse struct {
	Data struct {
		Root   string `json:"root"`
		Header struct {
			Message beacontypes.Header `json:"message"`
		} `json:"header"`
	}
}
type Observation struct {
	// Authenticated wire evidence is transient; request publication owns its retention.
	requestBody     json.RawMessage
	requestBid      json.RawMessage
	Slot            uint64            `json:"slot"`
	Epoch           uint64            `json:"epoch"`
	Proposer        uint64            `json:"proposer"`
	Root            string            `json:"root"`
	Parent          string            `json:"parent_root"`
	State           string            `json:"state_root"`
	Body            string            `json:"body_root"`
	Fork            string            `json:"fork"`
	Graffiti        string            `json:"graffiti"`
	Operations      map[string]string `json:"operations"`
	Commitments     []string          `json:"commitments"`
	ExecutionHash   string            `json:"execution_hash"`
	ExecutionParent string            `json:"execution_parent"`
	SlotConflict    bool              `json:"slot_conflict"`
}

func (c *Client) Header(ctx context.Context, id string) (beacontypes.Header, string, error) {
	var result headerResponse
	if err := c.get(ctx, "/eth/v1/beacon/headers/"+url.PathEscape(id), &result); err != nil {
		return beacontypes.Header{}, "", err
	}
	root := result.Data.Header.Message.Hash().Hex()
	if !fixedHex(result.Data.Root, 32) || !strings.EqualFold(root, result.Data.Root) {
		return beacontypes.Header{}, "", failure("beacon_header_identity")
	}
	return result.Data.Header.Message, root, nil
}
func (c *Client) Observe(ctx context.Context, network Network, block *types.Block) (Observation, error) {
	if block.Difficulty().Sign() != 0 || block.Time() < network.Genesis {
		return Observation{}, failure("not_applicable")
	}
	high, low := bits.Mul64(block.Time()-network.Genesis, 1000)
	if network.Millis == 0 || high >= network.Millis {
		return Observation{}, failure("beacon_spec")
	}
	slot, _ := bits.Div64(high, low, network.Millis)

	header, root, err := c.Header(ctx, strconv.FormatUint(slot, 10))
	if err != nil {
		return Observation{}, err
	}
	observed, err := c.Block(ctx, header, root, network)
	if err != nil {
		return Observation{}, err
	}
	if !strings.EqualFold(observed.ExecutionHash, block.Hash().Hex()) || !strings.EqualFold(observed.ExecutionParent, block.ParentHash().Hex()) {
		return Observation{}, &Error{Code: "execution_identity_mismatch", Retry: true}
	}
	observed.SlotConflict = block.SlotNumber() != nil && *block.SlotNumber() != observed.Slot
	return observed, nil
}
func (c *Client) Block(ctx context.Context, header beacontypes.Header, root string, network Network) (Observation, error) {
	var envelope struct {
		Version string `json:"version"`
		Data    struct {
			Message struct {
				Slot     string          `json:"slot"`
				Proposer string          `json:"proposer_index"`
				Parent   string          `json:"parent_root"`
				State    string          `json:"state_root"`
				Body     json.RawMessage `json:"body"`
			} `json:"message"`
		}
	}
	if err := c.get(ctx, "/eth/v2/beacon/blocks/"+root, &envelope); err != nil {
		return Observation{}, err
	}
	m := envelope.Data.Message
	// The header body_root is not in the full block message.
	if m.Slot != strconv.FormatUint(header.Slot, 10) || m.Proposer != strconv.FormatUint(header.ProposerIndex, 10) || m.Parent != header.ParentRoot.Hex() || m.State != header.StateRoot.Hex() {
		return Observation{}, failure("beacon_block_identity")
	}
	if err := verifyBody(m.Body, envelope.Version, header.BodyRoot, network.BodySpec); err != nil {
		return Observation{}, err
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(m.Body, &body) != nil {
		return Observation{}, failure("beacon_malformed")
	}
	slot, proposer := header.Slot, header.ProposerIndex
	result := Observation{Slot: slot, Epoch: slot / network.Slots, Proposer: proposer, Root: root, Parent: header.ParentRoot.Hex(), State: header.StateRoot.Hex(), Body: header.BodyRoot.Hex(), Fork: envelope.Version, Operations: map[string]string{}, Commitments: []string{}}
	if json.Unmarshal(body["graffiti"], &result.Graffiti) != nil || !fixedHex(result.Graffiti, 32) {
		return Observation{}, failure("beacon_graffiti")
	}
	for _, key := range []string{"attestations", "proposer_slashings", "attester_slashings", "deposits", "voluntary_exits", "bls_to_execution_changes", "payload_attestations"} {
		if raw, ok := body[key]; ok {
			var items []json.RawMessage
			if string(raw) == "null" || json.Unmarshal(raw, &items) != nil || len(items) > 8192 {
				return Observation{}, failure("beacon_operations")
			}
			result.Operations[key] = strconv.Itoa(len(items))
		}
	}
	for _, key := range []string{"execution_requests", "parent_execution_requests"} {
		if raw, ok := body[key]; ok {
			var requests map[string][]json.RawMessage
			if json.Unmarshal(raw, &requests) != nil {
				return Observation{}, failure("beacon_requests")
			}
			for _, kind := range []string{"deposits", "withdrawals", "consolidations"} {
				if items, exists := requests[kind]; exists {
					result.Operations[key+"_"+kind] = strconv.Itoa(len(items))
				}
			}
		}
	}
	var payload struct {
		Hash   string `json:"block_hash"`
		Parent string `json:"parent_hash"`
	}
	if envelope.Version == "gloas" {
		var bid struct {
			Message struct {
				Hash        string   `json:"block_hash"`
				Parent      string   `json:"parent_block_hash"`
				Slot        string   `json:"slot"`
				Commitments []string `json:"blob_kzg_commitments"`
			} `json:"message"`
		}
		if json.Unmarshal(body["signed_execution_payload_bid"], &bid) != nil || bid.Message.Commitments == nil || bid.Message.Slot != strconv.FormatUint(header.Slot, 10) {
			return Observation{}, failure("beacon_payload")
		}
		payload.Hash, payload.Parent = bid.Message.Hash, bid.Message.Parent
		result.Commitments = bid.Message.Commitments
	} else {
		if json.Unmarshal(body["execution_payload"], &payload) != nil {
			return Observation{}, failure("beacon_payload")
		}
		if envelope.Version != "bellatrix" && envelope.Version != "capella" {
			if _, ok := body["blob_kzg_commitments"]; !ok {
				return Observation{}, failure("beacon_commitments")
			}
		}
		if raw, ok := body["blob_kzg_commitments"]; ok {
			if json.Unmarshal(raw, &result.Commitments) != nil || result.Commitments == nil {
				return Observation{}, failure("beacon_commitments")
			}
		}
	}
	if !fixedHex(payload.Hash, 32) || !fixedHex(payload.Parent, 32) || len(result.Commitments) > 4096 {
		return Observation{}, failure("beacon_payload")
	}
	for _, commitment := range result.Commitments {
		if !fixedHex(commitment, 48) {
			return Observation{}, failure("beacon_commitments")
		}
	}
	result.ExecutionHash, result.ExecutionParent = strings.ToLower(payload.Hash), strings.ToLower(payload.Parent)
	result.requestBody = body["execution_requests"]
	result.requestBid = body["signed_execution_payload_bid"]
	return result, nil
}

func quantity(v string) (uint64, error) {
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil || strconv.FormatUint(n, 10) != v {
		return 0, failure("beacon_quantity")
	}
	return n, nil
}
func fixedHex(v string, n int) bool {
	if len(v) != 2+n*2 || !strings.HasPrefix(v, "0x") {
		return false
	}
	_, err := hex.DecodeString(v[2:])
	return err == nil
}
