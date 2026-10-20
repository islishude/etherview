package consensus

import (
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	beacontypes "github.com/ethereum/go-ethereum/beacon/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/consensus/testfixture"
)

func TestBodyAuthenticationAcrossForksAndPresets(t *testing.T) {
	for _, fork := range []string{"bellatrix", "capella", "deneb", "electra", "fulu", "gloas"} {
		t.Run(fork, func(t *testing.T) {
			block := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(1), Time: 124, Difficulty: big.NewInt(0)})
			for _, spec := range []map[string]any{nil, {"SYNC_COMMITTEE_SIZE": uint64(32), "MAX_PROPOSER_SLASHINGS": uint64(4)}} {
				header, envelope, err := testfixture.Block(fork, block, beacontypes.Header{Slot: 2, ParentRoot: common.Hash{1}, StateRoot: common.Hash{2}}, []string{}, spec)
				if err != nil {
					t.Fatal(err)
				}
				message := envelope["data"].(map[string]any)["message"].(map[string]any)
				raw, _ := json.Marshal(message["body"])
				if err := verifyBody(raw, fork, header.BodyRoot, spec); err != nil {
					t.Fatal(err)
				}
				// Geth/ZRNT is an independent SSZ oracle for its supported mainnet forks.
				if spec == nil && fork != "bellatrix" && fork != "gloas" {
					encoded, _ := json.Marshal(message)
					reference, err := beacontypes.BlockFromJSON(fork, encoded)
					if err != nil {
						t.Fatal(err)
					}
					if reference.Header().BodyRoot != header.BodyRoot || reference.Root() != header.Hash() {
						t.Fatal("independent Beacon root mismatch")
					}
				}
				message["body"].(map[string]any)["graffiti"] = common.Hash{9}.Hex()
				altered, _ := json.Marshal(message["body"])
				if err := verifyBody(altered, fork, header.BodyRoot, spec); err == nil {
					t.Fatal("body tampering accepted")
				}
				if err := verifyBody(raw, "unknown", header.BodyRoot, spec); err == nil {
					t.Fatal("unknown fork accepted")
				}
				if err := verifyBody(json.RawMessage(`{}`), fork, header.BodyRoot, spec); err == nil {
					t.Fatal("missing structure accepted")
				}
			}
		})
	}
}

func TestObserveRejectsExecutionHashOutsidePinnedBody(t *testing.T) {
	header := beacontypes.Header{Slot: 2}
	original := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(1), Time: 124, Difficulty: big.NewInt(0)})
	indexed := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(1), Time: 124, Difficulty: big.NewInt(0), Extra: []byte("local")})
	header, envelope, err := testfixture.Block("capella", original, header, []string{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	message := envelope["data"].(map[string]any)["message"].(map[string]any)
	message["body"].(map[string]any)["execution_payload"].(map[string]any)["block_hash"] = indexed.Hash().Hex()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var response any = envelope
		if r.URL.Path == "/eth/v1/beacon/headers/2" {
			response = map[string]any{"data": map[string]any{"root": header.Hash().Hex(), "header": map[string]any{"message": map[string]string{"slot": "2", "proposer_index": "0", "parent_root": header.ParentRoot.Hex(), "state_root": header.StateRoot.Hex(), "body_root": header.BodyRoot.Hex()}}}}
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	cfg := config.DefaultConsensusConfig()
	cfg.URL = server.URL
	_, err = NewClient(cfg).Observe(t.Context(), Network{Genesis: 100, Millis: 12000, Slots: 32}, indexed)
	var remote *Error
	if !errors.As(err, &remote) || remote.Code != "beacon_block_identity" {
		t.Fatalf("forged association err=%v", err)
	}
}

func TestBodySpecRejectsUnboundedAndMalformedParameters(t *testing.T) {
	for _, raw := range []string{`{"SYNC_COMMITTEE_SIZE":"0"}`, `{"MAX_VALIDATORS_PER_COMMITTEE":"18446744073709551615"}`, `{"PTC_SIZE":512}`} {
		var values map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &values); err != nil {
			t.Fatal(err)
		}
		if _, err := bodySpec(values); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
