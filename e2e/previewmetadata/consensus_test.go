//go:build previewmetadatae2e

package previewmetadatae2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/kzg4844"
	"github.com/holiman/uint256"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/consensus"
)

func TestPreviewConsensus(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 22*time.Minute)
	defer cancel()
	h := newHarness(t)
	t.Cleanup(h.cleanup)
	h.prepareNetwork(ctx)
	if _, err := h.project.Run(ctx, "up", "-d", "--no-build", "--wait", "--wait-timeout", "180"); err != nil {
		t.Fatal(err)
	}
	h.initializeArchive(ctx)
	h.connect(ctx)
	h.assertPublicConfig(ctx)
	h.assertAmsterdam(ctx, "0x1236")
	receipt, blob := h.sendBlob(ctx, 7)
	observation, refs := h.assertConsensusBlob(ctx, receipt, blob)
	h.assertArchivePermissions(ctx, refs[0], blob)
	h.assertPreviewRecovery(ctx, receipt, blob)
	h.assertAmsterdamMonolith(ctx)
	second, secondBlob := h.sendBlob(ctx, 9)
	h.assertConsensusBlob(ctx, second, secondBlob)
	h.assertArchiveOutageRecreate(ctx, second, []string{"api"}, "0x1239")
	h.assertBlobDownload(ctx, second.BlockHash, secondBlob)
	recovered, recoveredBlob := h.sendBlob(ctx, 11)
	h.assertConsensusBlob(ctx, recovered, recoveredBlob)
	h.t.Log("real blob publication, permissions, restart and both roles passed; waiting for finality")
	h.waitFinality(ctx)
	report := map[string]any{"execution_hash": receipt.BlockHash.Hex(), "beacon_root": observation.Root, "slot": observation.Slot, "blob_sha256": refs[0].SHA256, "monolith_execution_hash": second.BlockHash.Hex(), "lighthouse": "v8.3.0", "geth": "v1.17.8", "roles": []string{"distributed", "all"}, "finality": true, "restart": true, "archive_permissions": true, "execution_requests": true}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.artifacts, "consensus-report.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) sendBlob(ctx context.Context, marker byte) (rpcReceipt, []byte) {
	h.t.Helper()
	var blob kzg4844.Blob
	for i := 31; i < len(blob); i += 32 {
		blob[i] = marker
	}
	commitment, err := kzg4844.BlobToCommitment(&blob)
	if err != nil {
		h.t.Fatal(err)
	}
	proofs, err := kzg4844.ComputeCellProofs(&blob)
	if err != nil {
		h.t.Fatal(err)
	}
	sidecar := types.NewBlobTxSidecar(types.BlobSidecarVersion1, []kzg4844.Blob{blob}, []kzg4844.Commitment{commitment}, proofs)
	key, err := crypto.HexToECDSA(previewSigningKey)
	if err != nil {
		h.t.Fatal(err)
	}
	sender := crypto.PubkeyToAddress(key.PublicKey)
	var nonce hexutil.Uint64
	h.rpcCall(ctx, &nonce, "eth_getTransactionCount", sender, "pending")
	tx := types.NewTx(&types.BlobTx{ChainID: uint256.NewInt(48815), Nonce: uint64(nonce), GasTipCap: uint256.NewInt(1_000_000_000), GasFeeCap: uint256.NewInt(2_000_000_000), Gas: 21000, To: common.HexToAddress("0x1234"), Value: new(uint256.Int), BlobFeeCap: uint256.NewInt(1_000_000_000), BlobHashes: sidecar.BlobHashes(), Sidecar: sidecar})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(big.NewInt(48815)), key)
	if err != nil {
		h.t.Fatal(err)
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		h.t.Fatal(err)
	}
	var hash common.Hash
	h.rpcCall(ctx, &hash, "eth_sendRawTransaction", hexutil.Bytes(raw))
	if hash != signed.Hash() {
		h.t.Fatal("blob hash mismatch")
	}
	return h.waitReceipt(ctx, "real blob transaction", hash), blob[:]
}

func (h *harness) getJSON(ctx context.Context, url string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := h.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close() //nolint:errcheck
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP status %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 32<<20)).Decode(target)
}

func (h *harness) assertConsensusBlob(ctx context.Context, receipt rpcReceipt, blob []byte) (gen.ConsensusSummary, []consensus.BlobReference) {
	h.t.Helper()
	base := h.apiURL + "/api/v1/blocks/" + receipt.BlockHash.Hex()
	var observed gen.ConsensusResponse
	var archived gen.BlockBlobsResponse
	waitFor(h.t, ctx, "real Beacon and blob publication", func() (bool, string, error) {
		if err := h.getJSON(ctx, base+"/consensus", &observed); err != nil {
			return false, err.Error(), nil
		}
		if err := h.getJSON(ctx, base+"/blobs", &archived); err != nil {
			return false, err.Error(), nil
		}
		return observed.Data.State == gen.ConsensusStateComplete && archived.Data.State == gen.ConsensusStateComplete, fmt.Sprintf("consensus=%s code=%v blobs=%s code=%v", observed.Data.State, observed.Data.Code, archived.Data.State, archived.Data.Code), nil
	})
	if observed.Data.Root == nil || observed.Data.Slot == nil || observed.Data.Fork == nil || *observed.Data.Fork != "gloas" || len(archived.Data.Items) != 1 {
		h.t.Fatal("missing real consensus/blob fields")
	}
	var block struct{ Data gen.Block }
	if err := h.getJSON(ctx, base, &block); err != nil {
		h.t.Fatal(err)
	}
	if block.Data.SlotNumberSource == nil || *block.Data.SlotNumberSource != gen.Consensus || block.Data.SlotNumber == nil || *block.Data.SlotNumber != *observed.Data.Slot {
		h.t.Fatal("consensus slot not selected")
	}
	h.assertExecutionRequests(ctx, receipt, *observed.Data.Root)
	binding, err := h.project.Port(ctx, "lighthouse", 5052)
	if err != nil {
		h.t.Fatal(err)
	}
	var beacon struct {
		Data struct {
			Message struct {
				Slot string `json:"slot"`
				Body struct {
					Bid struct {
						Message struct {
							BlockHash   string   `json:"block_hash"`
							Commitments []string `json:"blob_kzg_commitments"`
						}
					} `json:"signed_execution_payload_bid"`
				}
			}
		}
	}
	if err := h.getJSON(ctx, "http://"+binding+"/eth/v2/beacon/blocks/"+*observed.Data.Root, &beacon); err != nil {
		h.t.Fatal(err)
	}
	if beacon.Data.Message.Slot != *observed.Data.Slot || !strings.EqualFold(beacon.Data.Message.Body.Bid.Message.BlockHash, receipt.BlockHash.Hex()) {
		h.t.Fatal("Beacon/execution identity mismatch")
	}
	var refsJSON []byte
	if err := h.db.QueryRow(ctx, "SELECT manifest FROM blob_manifests WHERE block_hash=$1 AND canonical", receipt.BlockHash.Bytes()).Scan(&refsJSON); err != nil {
		h.t.Fatal(err)
	}
	var refs []consensus.BlobReference
	if err := json.Unmarshal(refsJSON, &refs); err != nil {
		h.t.Fatal(err)
	}
	if len(refs) != 1 {
		h.t.Fatal("unexpected manifest count")
	}
	var input kzg4844.Blob
	copy(input[:], blob)
	commitment, err := kzg4844.BlobToCommitment(&input)
	if err != nil {
		h.t.Fatal(err)
	}
	versionedHash := kzg4844.CalcBlobHashV1(sha256.New(), &commitment)
	if refs[0].Commitment != hexutil.Encode(commitment[:]) || refs[0].VersionedHash != hexutil.Encode(versionedHash[:]) {
		h.t.Fatal("manifest KZG commitment or versioned hash differs from input")
	}
	sum := sha256.Sum256(blob)
	if refs[0].SHA256 != "0x"+hex.EncodeToString(sum[:]) && refs[0].SHA256 != hex.EncodeToString(sum[:]) {
		h.t.Fatal("manifest checksum mismatch")
	}
	if len(beacon.Data.Message.Body.Bid.Message.Commitments) != 1 || beacon.Data.Message.Body.Bid.Message.Commitments[0] != refs[0].Commitment {
		h.t.Fatal("Beacon commitment mismatch")
	}
	var full struct{ Data []string }
	if err := h.getJSON(ctx, "http://"+binding+"/eth/v1/beacon/blobs/"+*observed.Data.Root+"?versioned_hashes="+refs[0].VersionedHash, &full); err != nil {
		h.t.Fatal(err)
	}
	if len(full.Data) != 1 || !bytes.Equal(common.FromHex(full.Data[0]), blob) {
		h.t.Fatal("Lighthouse blob readback differs")
	}
	h.assertBlobDownload(ctx, receipt.BlockHash, blob)
	return observed.Data, refs
}

func (h *harness) assertBlobDownload(ctx context.Context, hash common.Hash, want []byte) {
	h.t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.apiURL+"/api/v1/blocks/"+hash.Hex()+"/blobs/0/content", nil)
	if err != nil {
		h.t.Fatal(err)
	}
	response, err := h.http.Do(request)
	if err != nil {
		h.t.Fatal(err)
	}
	defer response.Body.Close() //nolint:errcheck
	got, err := io.ReadAll(io.LimitReader(response.Body, 131073))
	if err != nil {
		h.t.Fatal(err)
	}
	if response.StatusCode != 200 || !bytes.Equal(got, want) {
		h.t.Fatalf("blob download: status=%d length=%d", response.StatusCode, len(got))
	}
}

func (h *harness) assertArchivePermissions(ctx context.Context, ref consensus.BlobReference, blob []byte) {
	h.t.Helper()
	binding, err := h.project.Port(ctx, "object-storage", 9000)
	if err != nil {
		h.t.Fatal(err)
	}
	client := s3.New(s3.Options{Region: "us-east-1", BaseEndpoint: aws.String("http://" + binding), UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider("preview-reader", "preview-read-dev-only", "")})
	read, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("etherview-preview-blobs"), Key: aws.String(ref.ObjectKey)})
	if err != nil {
		h.t.Fatal(err)
	}
	data, err := io.ReadAll(io.LimitReader(read.Body, 131073))
	closeErr := read.Body.Close()
	if err != nil || closeErr != nil || !bytes.Equal(data, blob) {
		h.t.Fatal("independent S3 readback differs")
	}
	_, err = client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String("etherview-preview-blobs"), Key: aws.String("must-not-write"), Body: bytes.NewReader(blob)})
	var remote smithy.APIError
	if !errors.As(err, &remote) || remote.ErrorCode() != "AccessDenied" {
		h.t.Fatalf("API credential write was not denied: %v", err)
	}
}

func (h *harness) assertArchiveOutageRecreate(ctx context.Context, receipt rpcReceipt, roles []string, recipient string) {
	h.t.Helper()
	if _, err := h.project.Run(ctx, "stop", "object-storage"); err != nil {
		h.t.Fatal(err)
	}
	// Do not use --no-deps: the regression is the Compose dependency graph.
	args := append([]string{"up", "-d", "--no-build", "--force-recreate", "--wait", "--wait-timeout", "90"}, roles...)
	if _, err := h.project.Run(ctx, args...); err != nil {
		h.t.Fatal(err)
	}
	running, err := h.project.Run(ctx, "ps", "--status", "running", "--services", "object-storage")
	if err != nil || strings.TrimSpace(string(running)) != "" {
		h.t.Fatalf("core recreation restarted the unavailable archive: %v %s", err, running)
	}
	h.connect(ctx)
	var historical gen.ConsensusResponse
	if err := h.getJSON(ctx, h.apiURL+"/api/v1/blocks/"+receipt.BlockHash.Hex()+"/consensus", &historical); err != nil || historical.Data.State != gen.ConsensusStateComplete {
		h.t.Fatalf("historical consensus unavailable after archive outage recreation: %v", err)
	}
	h.assertAmsterdam(ctx, recipient)
	if _, err := h.project.Run(ctx, "start", "object-storage"); err != nil {
		h.t.Fatal(err)
	}
	h.initializeArchive(ctx)
}

func (h *harness) assertPreviewRecovery(ctx context.Context, receipt rpcReceipt, blob []byte) {
	h.t.Helper()
	manifestPath := filepath.Join(h.project.Env["PREVIEW_NETWORK_DIR"], "manifest.json")
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		h.t.Fatal(err)
	}
	h.assertArchiveOutageRecreate(ctx, receipt, []string{"api", "enrich"}, "0x1237")
	// A failed provisioner must fail its caller while already started core roles
	// remain ready. Re-running the valid provisioner must preserve archived bytes.
	if _, err := h.project.Run(ctx, "run", "--rm", "--no-deps", "-e", "RUSTFS_SECRET_KEY=invalid-init-secret", "archive-init"); err == nil {
		h.t.Fatal("archive initialization failure was swallowed")
	}
	if _, err := h.project.Run(ctx, "exec", "-T", "api", "/etherview", "healthcheck"); err != nil {
		h.t.Fatal(err)
	}
	h.initializeArchive(ctx)
	waitFor(h.t, ctx, "archive restart", func() (bool, string, error) {
		var result gen.BlockBlobsResponse
		err := h.getJSON(ctx, h.apiURL+"/api/v1/blocks/"+receipt.BlockHash.Hex()+"/blobs", &result)
		return err == nil && result.Data.State == gen.ConsensusStateComplete, fmt.Sprint(err), nil
	})
	if _, err := h.project.Run(ctx, "stop", "validator", "lighthouse"); err != nil {
		h.t.Fatal(err)
	}
	// Stopping the sole consensus producer stops new blocks, not core readiness.
	if _, err := h.project.Run(ctx, "exec", "-T", "api", "/etherview", "healthcheck"); err != nil {
		h.t.Fatal(err)
	}
	h.assertBlobDownload(ctx, receipt.BlockHash, blob)
	if _, err := h.project.Run(ctx, "up", "-d", "--no-build", "--force-recreate", "--wait", "--wait-timeout", "180", "geth", "lighthouse", "validator", "object-storage", "api", "enrich"); err != nil {
		h.t.Fatal(err)
	}
	h.initializeArchive(ctx)
	h.connect(ctx)
	after, err := os.ReadFile(manifestPath)
	if err != nil || !bytes.Equal(before, after) {
		h.t.Fatal("restart changed network material")
	}
	h.assertAmsterdam(ctx, "0x1238")
	h.assertConsensusBlob(ctx, receipt, blob)
}

func (h *harness) waitFinality(ctx context.Context) {
	h.t.Helper()
	deadline := time.NewTimer(14 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(6 * time.Second)
	defer ticker.Stop()
	for {
		var home gen.HomeSnapshotResponse
		err := h.getJSON(ctx, h.apiURL+"/api/v1/home", &home)
		if err == nil && home.Data.Status.Consensus != nil && home.Data.Status.Consensus.Finalized != nil {
			epoch, parseErr := strconv.ParseUint(home.Data.Status.Consensus.Finalized.Epoch, 10, 64)
			if parseErr == nil && epoch > 0 && !home.Data.Status.Consensus.Stale {
				return
			}
		}
		select {
		case <-ctx.Done():
			h.t.Fatal(ctx.Err())
		case <-deadline.C:
			h.t.Fatal("real Beacon finality did not advance")
		case <-ticker.C:
		}
	}
}
