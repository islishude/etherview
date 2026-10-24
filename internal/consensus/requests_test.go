package consensus

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/attestantio/go-eth2-client/spec/electra"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	beacontypes "github.com/ethereum/go-ethereum/beacon/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/consensus/testfixture"
)

func nonemptyRequests() *gloas.ExecutionRequests {
	return &gloas.ExecutionRequests{
		Deposits:        []*electra.DepositRequest{{Pubkey: phase0.BLSPubKey{1}, WithdrawalCredentials: make([]byte, 32), Amount: phase0.Gwei(math.MaxUint64), Signature: phase0.BLSSignature{2}, Index: math.MaxUint64}, {Pubkey: phase0.BLSPubKey{3}, WithdrawalCredentials: make([]byte, 32), Amount: 1, Index: 0}},
		Withdrawals:     []*electra.WithdrawalRequest{{ValidatorPubkey: phase0.BLSPubKey{4}, Amount: 0}},
		Consolidations:  []*electra.ConsolidationRequest{{SourcePubkey: phase0.BLSPubKey{5}, TargetPubkey: phase0.BLSPubKey{6}}},
		BuilderDeposits: []*gloas.BuilderDepositRequest{{Pubkey: phase0.BLSPubKey{7}, WithdrawalCredentials: make([]byte, 32), Amount: phase0.Gwei(math.MaxUint64), Signature: phase0.BLSSignature{8}}},
		BuilderExits:    []*gloas.BuilderExitRequest{{Pubkey: phase0.BLSPubKey{9}}},
	}
}

func TestExecutionRequestsAcrossForks(t *testing.T) {
	for _, fork := range []string{"electra", "fulu", "gloas"} {
		for _, empty := range []bool{false, true} {
			t.Run(fork+"/"+map[bool]string{true: "empty", false: "nonempty"}[empty], func(t *testing.T) {
				requests := nonemptyRequests()
				if empty {
					requests = &gloas.ExecutionRequests{}
				}
				if fork != "gloas" {
					requests.BuilderDeposits = nil
					requests.BuilderExits = nil
				}
				commitment, err := testfixture.RequestsHash(requests)
				if err != nil {
					t.Fatal(err)
				}
				block := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(1), Time: 124, Difficulty: big.NewInt(0), RequestsHash: &commitment})
				header, body, envelope, err := testfixture.Requests(fork, block, beacontypes.Header{Slot: 2, ParentRoot: common.Hash{1}}, requests)
				if err != nil {
					t.Fatal(err)
				}
				var envelopeReads int
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var response any
					switch r.URL.Path {
					case "/eth/v1/beacon/headers/2":
						response = testfixture.HeaderResponse(header)
					case "/eth/v2/beacon/blocks/" + header.Hash().Hex():
						response = body
					case "/eth/v1/beacon/execution_payload_envelopes/" + header.Hash().Hex():
						envelopeReads++
						response = envelope
					default:
						t.Errorf("unexpected path %s", r.URL.Path)
						http.NotFound(w, r)
						return
					}
					_ = json.NewEncoder(w).Encode(response)
				}))
				defer server.Close()
				cfg := config.DefaultConsensusConfig()
				cfg.URL = server.URL
				set, err := NewClient(cfg).ObserveRequests(t.Context(), Network{Genesis: 100, Millis: 12000, Slots: 32}, block)
				if err != nil {
					t.Fatal(err)
				}
				if err := ValidateRequestSet(set, block); err != nil {
					t.Fatal(err)
				}
				want := 4
				if fork == "gloas" {
					want = 6
				}
				if empty {
					want = 0
				}
				if len(set.Items) != want || (fork == "gloas" && envelopeReads != 1) || (fork != "gloas" && envelopeReads != 0) {
					t.Fatalf("items=%+v reads=%d", set.Items, envelopeReads)
				}
				if !empty && (set.Items[0].Amount != "18446744073709551615" || set.Items[0].Index != "18446744073709551615" || set.Items[1].Sequence != "1") {
					t.Fatal("lost uint64 precision or order")
				}
				if !empty {
					set.Items[0].Amount = "1"
					if ValidateRequestSet(set, block) == nil {
						t.Fatal("retained tampering accepted")
					}
				}
			})
		}
	}
}

func TestRequestsRejectMalformedAndExcessWork(t *testing.T) {
	good, _ := json.Marshal(nonemptyRequests())
	for _, mutate := range []func(map[string]json.RawMessage){
		func(m map[string]json.RawMessage) { m["unknown"] = json.RawMessage(`[]`) },
		func(m map[string]json.RawMessage) { delete(m, "deposits") },
		func(m map[string]json.RawMessage) { m["builder_exits"] = json.RawMessage(`null`) },
		func(m map[string]json.RawMessage) {
			m["deposits"] = json.RawMessage(strings.ReplaceAll(string(m["deposits"]), "18446744073709551615", "18446744073709551616"))
		},
		func(m map[string]json.RawMessage) { m["withdrawals"] = json.RawMessage(`[{}]`) },
		func(m map[string]json.RawMessage) {
			m["deposits"] = json.RawMessage(strings.ReplaceAll(string(m["deposits"]), "18446744073709551615", strings.Repeat("1", 2049)))
		},
		func(m map[string]json.RawMessage) {
			m["withdrawals"] = json.RawMessage("[" + strings.TrimSuffix(strings.Repeat(string(m["withdrawals"])[1:len(m["withdrawals"])-1]+",", 17), ",") + "]")
		},
		func(m map[string]json.RawMessage) {
			m["withdrawals"] = json.RawMessage(`[ {"source_address":"0x00","validator_pubkey":"0x00","amount":"0"} ]`)
		},
	} {
		var m map[string]json.RawMessage
		_ = json.Unmarshal(good, &m)
		mutate(m)
		raw, _ := json.Marshal(m)
		if _, _, err := decodeRequests(raw, "gloas", nil); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if _, _, err := decodeRequests(good, "unknown", nil); err == nil {
		t.Fatal("unknown fork accepted")
	}
	if _, _, err := decodeRequests(good, "gloas", map[string]any{"MAX_DEPOSIT_REQUESTS_PER_PAYLOAD": uint64(1)}); err == nil {
		t.Fatal("private preset exceeded")
	}
}

func TestGloasRequestsRejectWrongEnvelopeAndUnavailable(t *testing.T) {
	requests := nonemptyRequests()
	commitment, err := testfixture.RequestsHash(requests)
	if err != nil {
		t.Fatal(err)
	}
	block := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(1), Time: 124, Difficulty: big.NewInt(0), RequestsHash: &commitment})
	for _, scenario := range []string{"root", "parent_root", "builder", "hash", "parent_hash", "request_root", "missing", "unsupported", "temporary"} {
		t.Run(scenario, func(t *testing.T) {
			header, body, envelope, err := testfixture.Requests("gloas", block, beacontypes.Header{Slot: 2, ParentRoot: common.Hash{1}}, requests)
			if err != nil {
				t.Fatal(err)
			}
			signed := envelope["data"].(*gloas.SignedExecutionPayloadEnvelope)
			switch scenario {
			case "root":
				signed.Message.BeaconBlockRoot[0] ^= 1
			case "parent_root":
				signed.Message.ParentBeaconBlockRoot[0] ^= 1
			case "builder":
				signed.Message.BuilderIndex++
			case "hash":
				signed.Message.Payload.BlockHash[0] ^= 1
			case "parent_hash":
				signed.Message.Payload.ParentHash[0] ^= 1
			case "request_root":
				signed.Message.ExecutionRequests = nonemptyRequests()
				signed.Message.ExecutionRequests.Deposits[0].Amount = 1
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				response := any(body)
				if strings.Contains(r.URL.Path, "/headers/") {
					response = testfixture.HeaderResponse(header)
				} else if strings.Contains(r.URL.Path, "execution_payload_envelopes") {
					switch scenario {
					case "missing":
						http.NotFound(w, r)
						return
					case "unsupported":
						w.WriteHeader(501)
						return
					case "temporary":
						w.WriteHeader(503)
						return
					}
					response = envelope
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			cfg := config.DefaultConsensusConfig()
			cfg.URL = server.URL
			_, err = NewClient(cfg).ObserveRequests(t.Context(), Network{Genesis: 100, Millis: 12000, Slots: 32}, block)
			var remote *Error
			if !errors.As(err, &remote) {
				t.Fatalf("error=%v", err)
			}
			if scenario != "missing" && scenario != "unsupported" && scenario != "temporary" && remote.Code != "beacon_requests_identity" {
				t.Fatalf("identity mismatch code=%s", remote.Code)
			}
			if (scenario == "temporary") != remote.Retry {
				t.Fatalf("retry=%v", remote.Retry)
			}
		})
	}
}

func TestRequestsHeaderCommitmentMismatchAndPreActivation(t *testing.T) {
	requests := nonemptyRequests()
	wrong := common.Hash{99}
	block := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(1), Time: 124, Difficulty: big.NewInt(0), RequestsHash: &wrong})
	header, body, envelope, err := testfixture.Requests("gloas", block, beacontypes.Header{Slot: 2}, requests)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := any(body)
		if strings.Contains(r.URL.Path, "/headers/") {
			response = testfixture.HeaderResponse(header)
		} else if strings.Contains(r.URL.Path, "execution_payload_envelopes") {
			response = envelope
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	cfg := config.DefaultConsensusConfig()
	cfg.URL = server.URL
	_, err = NewClient(cfg).ObserveRequests(t.Context(), Network{Genesis: 100, Millis: 12000, Slots: 32}, block)
	var remote *Error
	if !errors.As(err, &remote) || remote.Code != "requests_hash_mismatch" {
		t.Fatalf("commitment err=%v", err)
	}
	before := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(0), Difficulty: big.NewInt(1)})
	_, err = NewClient(cfg).ObserveRequests(t.Context(), Network{}, before)
	if !errors.As(err, &remote) || remote.Code != "not_applicable" {
		t.Fatalf("preactivation=%v", err)
	}
}
