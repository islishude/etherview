package consensus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto/kzg4844"
	"github.com/islishude/etherview/internal/config"
)

func TestArchiveReplayAuthenticatesRetainedObjects(t *testing.T) {
	var blob kzg4844.Blob
	blob[31] = 7
	commitment, err := kzg4844.BlobToCommitment(&blob)
	if err != nil {
		t.Fatal(err)
	}
	hash := common.Hash(kzg4844.CalcBlobHashV1(sha256.New(), &commitment)).Hex()
	sum := sha256.Sum256(blob[:])
	digest := "0x" + hex.EncodeToString(sum[:])
	ref := BlobReference{Index: "0", TransactionHash: common.Hash{1}.Hex(), TransactionIndex: "0", VersionedHash: hash, Commitment: "0x" + hex.EncodeToString(commitment[:]), Size: "131072"}
	retained := ref
	retained.SHA256 = digest
	retained.ObjectKey = blobObjectKey("1/genesis", hash, digest)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("intact archive contacted upstream")
		http.NotFound(w, r)
	}))
	defer server.Close()
	cfg := config.DefaultConsensusConfig()
	cfg.URL = server.URL
	client := NewClient(cfg)
	for _, mode := range []string{"valid", "wrong-namespace", "wrong-reference", "wrong-digest", "wrong-kzg"} {
		t.Run(mode, func(t *testing.T) {
			prior := retained
			stored := bytes.Clone(blob[:])
			archive := &replayArchive{data: stored}
			switch mode {
			case "wrong-namespace":
				prior.ObjectKey = blobObjectKey("2/other", hash, digest)
			case "wrong-reference":
				prior.TransactionHash = common.Hash{9}.Hex()
			case "wrong-digest":
				archive.data[31] = 8
			case "wrong-kzg":
				archive.data[31] = 8
				sum := sha256.Sum256(archive.data)
				prior.SHA256 = "0x" + hex.EncodeToString(sum[:])
				prior.ObjectKey = blobObjectKey("1/genesis", hash, prior.SHA256)
			}
			refs, err := RestoreReferences([]BlobReference{ref}, []BlobReference{prior}, "1/genesis")
			if err == nil {
				_, err = client.ArchiveBlock(t.Context(), archive, "1/genesis", Observation{Root: common.Hash{3}.Hex(), Fork: "fulu"}, refs)
			}
			if mode == "valid" {
				if err != nil || archive.reads != 1 {
					t.Fatalf("reads=%d err=%v", archive.reads, err)
				}
			} else if err == nil {
				t.Fatal("invalid retained evidence accepted")
			}
		})
	}
}

type replayArchive struct {
	data  []byte
	reads int
}

func (a *replayArchive) Read(context.Context, string, string) ([]byte, error) {
	a.reads++
	return bytes.Clone(a.data), nil
}
func (*replayArchive) Create(context.Context, string, []byte) (string, error) {
	return "", errors.New("unexpected archive write")
}
