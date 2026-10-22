package consensus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	beacontypes "github.com/ethereum/go-ethereum/beacon/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto/kzg4844"
	"github.com/holiman/uint256"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/consensus/testfixture"
)

func TestRootPinnedObservationAcrossForks(t *testing.T) {
	for _, fork := range []string{"bellatrix", "capella", "deneb", "electra", "fulu", "gloas"} {
		t.Run(fork, func(t *testing.T) {
			timestamp, millis := uint64(124), uint64(12000)
			if fork == "gloas" {
				timestamp, millis = 101, 500
			}
			block := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(1), Time: timestamp, Difficulty: big.NewInt(0)})
			header := beacontypes.Header{Slot: 2, ProposerIndex: 9007199254740993, ParentRoot: common.Hash{1}, StateRoot: common.Hash{2}, BodyRoot: common.Hash{3}}
			header, envelope, err := testfixture.Block(fork, block, header, []string{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			root := header.Hash().Hex()
			calls := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/eth/v1/beacon/headers/2":
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"root": root, "header": map[string]any{"message": map[string]any{"slot": "2", "proposer_index": "9007199254740993", "parent_root": header.ParentRoot.Hex(), "state_root": header.StateRoot.Hex(), "body_root": header.BodyRoot.Hex()}}}})
				case "/eth/v2/beacon/blocks/" + root:
					_ = json.NewEncoder(w).Encode(envelope)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			cfg := config.DefaultConsensusConfig()
			cfg.URL = server.URL
			got, err := NewClient(cfg).Observe(t.Context(), Network{Genesis: 100, Millis: millis, Slots: 32}, block)
			if err != nil || got.Slot != 2 || got.Root != root || got.Proposer != header.ProposerIndex || len(calls) != 2 {
				t.Fatalf("observation=%+v err=%v calls=%v", got, err, calls)
			}
		})
	}
}

func TestSlotDurationNormalization(t *testing.T) {
	for _, test := range []struct {
		spec string
		want uint64
	}{
		{`{"SECONDS_PER_SLOT":"12"}`, 12000},
		{`{"SLOT_DURATION_MS":"12000"}`, 12000},
		{`{"SECONDS_PER_SLOT":"12","SLOT_DURATION_MS":"12000"}`, 12000},
		{`{"SLOT_DURATION_MS":"500"}`, 500},
		{`{"SECONDS_PER_SLOT":"12","SLOT_DURATION_MS":"6000"}`, 0},
		{`{"SLOT_DURATION_MS":"0"}`, 0},
		{`{"SLOT_DURATION_MS":12000}`, 0},
		{`{"SECONDS_PER_SLOT":"18446744073709551615"}`, 0},
		{`{}`, 0},
	} {
		t.Run(test.spec, func(t *testing.T) {
			var values map[string]json.RawMessage
			if err := json.Unmarshal([]byte(test.spec), &values); err != nil {
				t.Fatal(err)
			}
			got, err := slotDuration(values)
			if got != test.want || (err != nil) != (test.want == 0) {
				t.Fatalf("duration=%d err=%v", got, err)
			}
		})
	}
}
func TestBeaconTransportBoundsAndRedaction(t *testing.T) {
	for _, test := range []struct {
		name, body, code string
		status           int
		retry            bool
	}{{"missing", "secret", "beacon_not_found", 404, false}, {"retry", "secret", "beacon_unavailable", 503, true}, {"auth", "secret", "beacon_rejected", 401, false}, {"trailing", "{} {}", "beacon_malformed", 200, false}, {"oversize", strings.Repeat("x", 1025), "beacon_response_limit", 200, false}} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			cfg := config.DefaultConsensusConfig()
			cfg.URL = server.URL
			cfg.MaxResponseBytes = 1024
			var value any
			err := NewClient(cfg).get(t.Context(), "/", &value)
			var remote *Error
			if !errors.As(err, &remote) || remote.Code != test.code || remote.Retry != test.retry || strings.Contains(err.Error(), "secret") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
func TestBlobCommitmentValidationAndDuplicateReferences(t *testing.T) {
	var blob kzg4844.Blob
	blob[31] = 7
	commitment, err := kzg4844.BlobToCommitment(&blob)
	if err != nil {
		t.Fatal(err)
	}
	hash := common.Hash(kzg4844.CalcBlobHashV1(sha256.New(), &commitment))
	encoded := "0x" + hex.EncodeToString(commitment[:])
	raw := "0x" + hex.EncodeToString(blob[:])
	tx := types.NewTx(&types.BlobTx{ChainID: uint256.NewInt(1), GasTipCap: uint256.NewInt(1), GasFeeCap: uint256.NewInt(1), Value: uint256.NewInt(0), BlobFeeCap: uint256.NewInt(1), BlobHashes: []common.Hash{hash, hash}})
	block := types.NewBlockWithHeader(&types.Header{}).WithBody(types.Body{Transactions: []*types.Transaction{tx}})
	observation := Observation{Commitments: []string{encoded, encoded}, Root: common.Hash{1}.Hex(), Fork: "fulu"}
	refs, err := References(block, observation)
	if err != nil || len(refs) != 2 || refs[1].TransactionIndex != "1" {
		t.Fatalf("refs=%+v err=%v", refs, err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("versioned_hashes") != hash.Hex() {
			t.Error("wrong requested hash")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []string{raw, raw}})
	}))
	defer server.Close()
	cfg := config.DefaultConsensusConfig()
	cfg.URL = server.URL
	archive := &testArchive{}
	result, err := NewClient(cfg).ArchiveBlock(t.Context(), archive, "1/genesis", observation, refs)
	if err != nil || calls != 1 || archive.writes != 1 || result[0].ObjectKey != result[1].ObjectKey {
		t.Fatalf("calls=%d writes=%d err=%v", calls, archive.writes, err)
	}
	refs[0].VersionedHash = common.Hash{2}.Hex()
	if _, err := validateBlob(raw, refs[0]); err == nil {
		t.Fatal("accepted incorrect versioned hash")
	}
	if _, err := validateBlob("0x"+strings.Repeat("ff", len(blob)), refs[1]); err == nil {
		t.Fatal("accepted non-field bytes")
	}
}

type testArchive struct{ writes int }

func (a *testArchive) Create(_ context.Context, _ string, data []byte) (string, error) {
	a.writes++
	sum := sha256.Sum256(data)
	return "0x" + hex.EncodeToString(sum[:]), nil
}
func (*testArchive) Read(context.Context, string, string) ([]byte, error) {
	return nil, errors.New("unexpected read")
}

func TestBeaconRedirectNeverForwardsCredentials(t *testing.T) {
	var received atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { received.Store(true) }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private" {
			t.Error("configured authentication missing")
		}
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	cfg := config.DefaultConsensusConfig()
	cfg.URL = origin.URL
	cfg.Headers = map[string]string{"Authorization": "Bearer private"}
	var response any
	err := NewClient(cfg).get(t.Context(), "/", &response)
	if err == nil || received.Load() || strings.Contains(err.Error(), origin.URL) || strings.Contains(err.Error(), "private") {
		t.Fatalf("redirect result=%v reached=%v", err, received.Load())
	}
}
