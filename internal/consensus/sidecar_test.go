package consensus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	beacontypes "github.com/ethereum/go-ethereum/beacon/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto/kzg4844"
	"github.com/islishude/etherview/internal/config"
)

func TestLegacySidecarProofAndRootBinding(t *testing.T) {
	var blob kzg4844.Blob
	blob[31] = 13
	commitment, err := kzg4844.BlobToCommitment(&blob)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := kzg4844.ComputeBlobProof(&blob, commitment)
	if err != nil {
		t.Fatal(err)
	}
	hash := common.Hash(kzg4844.CalcBlobHashV1(sha256.New(), &commitment))
	var padded [64]byte
	copy(padded[:], commitment[:])
	leaf := sha256.Sum256(padded[:])
	branch := make([]string, 17)
	position := uint64(27 * 8192)
	for i := range branch {
		branch[i] = common.Hash{}.Hex()
		var pair [64]byte
		if position&1 == 0 {
			copy(pair[:32], leaf[:])
		} else {
			copy(pair[32:], leaf[:])
		}
		leaf = sha256.Sum256(pair[:])
		position >>= 1
	}
	header := beacontypes.Header{Slot: 2, BodyRoot: common.Hash(leaf)}
	observation := Observation{Root: header.Hash().Hex(), Body: header.BodyRoot.Hex(), Fork: "deneb"}
	ref := BlobReference{Index: "0", VersionedHash: hash.Hex(), Commitment: "0x" + hex.EncodeToString(commitment[:])}
	for _, kind := range []string{"valid", "proof", "root", "branch", "index", "missing", "fulu"} {
		t.Run(kind, func(t *testing.T) {
			obs := observation
			if kind == "fulu" {
				obs.Fork = "fulu"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/blobs/") {
					http.NotFound(w, r)
					return
				}
				if kind == "fulu" {
					t.Error("Fulu used obsolete sidecars")
				}
				item := map[string]any{"index": "0", "blob": "0x" + hex.EncodeToString(blob[:]), "kzg_commitment": ref.Commitment, "kzg_proof": "0x" + hex.EncodeToString(proof[:]), "signed_block_header": map[string]any{"message": map[string]any{"slot": "2", "proposer_index": "0", "parent_root": header.ParentRoot.Hex(), "state_root": header.StateRoot.Hex(), "body_root": header.BodyRoot.Hex()}}, "kzg_commitment_inclusion_proof": branch}
				switch kind {
				case "proof":
					item["kzg_proof"] = "0x" + strings.Repeat("00", 48)
				case "root":
					item["signed_block_header"] = map[string]any{"message": map[string]any{"slot": "3", "proposer_index": "0", "parent_root": header.ParentRoot.Hex(), "state_root": header.StateRoot.Hex(), "body_root": header.BodyRoot.Hex()}}
				case "branch":
					wrong := append([]string(nil), branch...)
					wrong[0] = common.Hash{1}.Hex()
					item["kzg_commitment_inclusion_proof"] = wrong
				case "index":
					item["index"] = "1"
				}
				data := []any{item}
				if kind == "missing" {
					data = nil
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
			}))
			defer server.Close()
			cfg := config.DefaultConsensusConfig()
			cfg.URL = server.URL
			data, err := NewClient(cfg).blob(t.Context(), obs, ref)
			if kind == "valid" {
				if err != nil || len(data) != len(blob) {
					t.Fatalf("valid length=%d err=%v", len(data), err)
				}
			} else if err == nil {
				t.Fatal("invalid sidecar accepted")
			}
		})
	}
}
func TestBeaconNetworkPrivateParametersAndIdentity(t *testing.T) {
	for _, chain := range []string{"42", "1"} {
		t.Run(chain, func(t *testing.T) {
			var modern atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var data any
				switch r.URL.Path {
				case "/eth/v1/beacon/genesis":
					data = map[string]string{"genesis_time": "100", "genesis_fork_version": "0x00000001", "genesis_validators_root": common.Hash{1}.Hex()}
				case "/eth/v1/config/spec":
					spec := map[string]any{"SECONDS_PER_SLOT": "3", "SLOTS_PER_EPOCH": "8", "BLOB_SCHEDULE": []any{}}
					if modern.Load() {
						delete(spec, "SECONDS_PER_SLOT")
						spec["SLOT_DURATION_MS"] = "3000"
					}
					data = spec
				case "/eth/v1/config/deposit_contract":
					data = map[string]string{"chain_id": chain}
				default:
					t.Error("unexpected URL")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
			}))
			defer server.Close()
			cfg := config.DefaultConsensusConfig()
			cfg.URL = server.URL
			network, err := NewClient(cfg).Network(t.Context(), 42)
			if chain == "42" {
				if err != nil || network.Millis != 3000 || network.Slots != 8 {
					t.Fatalf("network=%+v err=%v", network, err)
				}
				modern.Store(true)
				upgraded, err := NewClient(cfg).Network(t.Context(), 42)
				if err != nil || !reflect.DeepEqual(upgraded, network) {
					t.Fatalf("node upgrade changed network identity: %+v err=%v", upgraded, err)
				}
			} else if err == nil {
				t.Fatal("wrong chain accepted")
			}
		})
	}
}
