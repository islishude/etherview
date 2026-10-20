package consensus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"strings"

	beacontypes "github.com/ethereum/go-ethereum/beacon/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto/kzg4844"
)

type BlobReference struct {
	Index            string `json:"index"`
	TransactionHash  string `json:"transaction_hash"`
	TransactionIndex string `json:"transaction_index"`
	VersionedHash    string `json:"versioned_hash"`
	Commitment       string `json:"commitment"`
	Size             string `json:"size"`
	SHA256           string `json:"sha256"`
	ObjectKey        string `json:"object_key"`
}
type Archive interface {
	Create(context.Context, string, []byte) (string, error)
	Read(context.Context, string, string) ([]byte, error)
}

func References(block *types.Block, observation Observation) ([]BlobReference, error) {
	refs := []BlobReference{}
	for _, tx := range block.Transactions() {
		for i, hash := range tx.BlobHashes() {
			index := len(refs)
			if index >= len(observation.Commitments) {
				return nil, failure("blob_commitment_count")
			}
			commitment := observation.Commitments[index]
			var kzg kzg4844.Commitment
			copy(kzg[:], common.FromHex(commitment))
			calculated := kzg4844.CalcBlobHashV1(sha256.New(), &kzg)
			if common.Hash(calculated) != hash {
				return nil, failure("blob_commitment_identity")
			}
			refs = append(refs, BlobReference{Index: strconv.Itoa(index), TransactionHash: tx.Hash().Hex(), TransactionIndex: strconv.Itoa(i), VersionedHash: hash.Hex(), Commitment: commitment, Size: "131072"})
		}
	}
	if len(refs) != len(observation.Commitments) {
		return nil, failure("blob_commitment_count")
	}
	return refs, nil
}
func (c *Client) ArchiveBlock(ctx context.Context, archive Archive, namespace string, observation Observation, refs []BlobReference) ([]BlobReference, error) {
	// Fetch one unique commitment at a time. The total per-worker body budget is independent of block size.
	saved := make(map[string]BlobReference)
	for i := range refs {
		ref := &refs[i]
		if prior, exists := saved[ref.VersionedHash]; exists {
			ref.ObjectKey, ref.SHA256 = prior.ObjectKey, prior.SHA256
			continue
		}
		stored, err := c.archiveReference(ctx, archive, namespace, observation, *ref)
		if err != nil {
			return nil, err
		}
		*ref = stored
		saved[ref.VersionedHash] = *ref
	}
	return refs, nil
}
func (c *Client) blob(ctx context.Context, observation Observation, ref BlobReference) ([]byte, error) {
	var response struct {
		Data []string `json:"data"`
	}
	err := c.get(ctx, "/eth/v1/beacon/blobs/"+observation.Root+"?versioned_hashes="+url.QueryEscape(ref.VersionedHash), &response)
	if err == nil {
		if len(response.Data) == 0 || len(response.Data) > 4096 {
			return nil, failure("blob_response_count")
		}
		// A versioned-hash filter may match repeated references in the block.
		// Associate by verified content, never by response position.
		var matched []byte
		for _, raw := range response.Data {
			data, err := validateBlob(raw, ref)
			if err != nil {
				return nil, err
			}
			if matched != nil && !bytes.Equal(matched, data) {
				return nil, failure("blob_duplicate_conflict")
			}
			matched = data
		}
		return matched, nil
	}
	var remote *Error
	if !errors.As(err, &remote) || (remote.Code != "beacon_not_found" && remote.Code != "beacon_unsupported_endpoint") || (observation.Fork != "deneb" && observation.Fork != "electra") {
		return nil, err
	}
	return c.sidecar(ctx, observation, ref)
}
func validateBlob(raw string, ref BlobReference) ([]byte, error) {
	if !fixedHex(raw, len(kzg4844.Blob{})) {
		return nil, failure("blob_encoding")
	}
	var blob kzg4844.Blob
	copy(blob[:], common.FromHex(raw))
	commitment, err := kzg4844.BlobToCommitment(&blob)
	if err != nil {
		return nil, failure("blob_field_encoding")
	}
	hash := kzg4844.CalcBlobHashV1(sha256.New(), &commitment)
	if !strings.EqualFold("0x"+hex.EncodeToString(commitment[:]), ref.Commitment) || !strings.EqualFold(common.Hash(hash).Hex(), ref.VersionedHash) {
		return nil, failure("blob_kzg_mismatch")
	}
	return blob[:], nil
}
func (c *Client) sidecar(ctx context.Context, observation Observation, ref BlobReference) ([]byte, error) {
	var response struct {
		Data []struct {
			Index      string `json:"index"`
			Blob       string `json:"blob"`
			Commitment string `json:"kzg_commitment"`
			Proof      string `json:"kzg_proof"`
			Header     struct {
				Message beacontypes.Header `json:"message"`
			} `json:"signed_block_header"`
			Branch []string `json:"kzg_commitment_inclusion_proof"`
		} `json:"data"`
	}
	if err := c.get(ctx, "/eth/v1/beacon/blob_sidecars/"+observation.Root+"?indices="+ref.Index, &response); err != nil {
		return nil, err
	}
	if len(response.Data) != 1 {
		return nil, failure("blob_response_count")
	}
	item := response.Data[0]
	root := item.Header.Message.Hash().Hex()
	if root != observation.Root || item.Index != ref.Index || !strings.EqualFold(item.Commitment, ref.Commitment) || !fixedHex(item.Proof, 48) {
		return nil, failure("blob_sidecar_identity")
	}
	data, err := validateBlob(item.Blob, ref)
	if err != nil {
		return nil, err
	}
	var blob kzg4844.Blob
	copy(blob[:], data)
	var commitment kzg4844.Commitment
	copy(commitment[:], common.FromHex(item.Commitment))
	var proof kzg4844.Proof
	copy(proof[:], common.FromHex(item.Proof))
	if kzg4844.VerifyBlobProof(&blob, commitment, proof) != nil {
		return nil, failure("blob_sidecar_proof")
	}
	index, err := quantity(ref.Index)
	if err != nil || index >= 4096 || len(item.Branch) != 17 {
		return nil, failure("blob_inclusion_proof")
	}
	var padded [64]byte
	copy(padded[:], commitment[:])
	leaf := sha256.Sum256(padded[:])
	position := uint64(27*8192) + index
	for _, sibling := range item.Branch {
		if !fixedHex(sibling, 32) {
			return nil, failure("blob_inclusion_proof")
		}
		var pair [64]byte
		if position&1 == 0 {
			copy(pair[:32], leaf[:])
			copy(pair[32:], common.FromHex(sibling))
		} else {
			copy(pair[:32], common.FromHex(sibling))
			copy(pair[32:], leaf[:])
		}
		leaf = sha256.Sum256(pair[:])
		position >>= 1
	}
	if position != 1 || common.Hash(leaf).Hex() != observation.Body {
		return nil, failure("blob_inclusion_proof")
	}
	return data, nil
}
