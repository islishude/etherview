package consensus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

func blobObjectKey(namespace, versionedHash, digest string) string {
	return fmt.Sprintf("blobs/v2/%s/%s/%s.s2", namespace, strings.TrimPrefix(versionedHash, "0x"), strings.TrimPrefix(digest, "0x"))
}

// RestoreReferences reads private retained evidence, never publication authority.
// Every reference must still agree with the exact execution/Beacon block pair.
func RestoreReferences(refs, retained []BlobReference, namespace string) ([]BlobReference, error) {
	if len(refs) != len(retained) {
		return nil, failure("blob_manifest_conflict")
	}
	for i, old := range retained {
		expected := refs[i]
		expected.ObjectKey, expected.SHA256 = old.ObjectKey, old.SHA256
		if expected != old || !fixedHex(old.SHA256, 32) || old.ObjectKey != blobObjectKey(namespace, old.VersionedHash, old.SHA256) {
			return nil, failure("blob_manifest_conflict")
		}
		refs[i] = old
	}
	return refs, nil
}

func (c *Client) archiveReference(ctx context.Context, archive Archive, namespace string, observation Observation, ref BlobReference) (BlobReference, error) {
	if ref.ObjectKey != "" {
		data, err := archive.Read(ctx, ref.ObjectKey, ref.SHA256)
		if err == nil {
			sum := sha256.Sum256(data)
			if "0x"+hex.EncodeToString(sum[:]) != ref.SHA256 {
				return BlobReference{}, failure("blob_storage_integrity")
			}
			if _, err := validateBlob("0x"+hex.EncodeToString(data), ref); err != nil {
				return BlobReference{}, err
			}
			return ref, nil
		}
		// Missing objects may be repaired from the exact upstream block.
		// Create remains conditional: corrupt existing keys are never overwritten.
	}
	data, err := c.blob(ctx, observation, ref)
	if err != nil {
		return BlobReference{}, err
	}
	sum := sha256.Sum256(data)
	digest := "0x" + hex.EncodeToString(sum[:])
	ref.ObjectKey = blobObjectKey(namespace, ref.VersionedHash, digest)
	stored, err := archive.Create(ctx, ref.ObjectKey, data)
	if err != nil {
		return BlobReference{}, &Error{Code: "blob_storage_unavailable", Retry: true}
	}
	if stored != digest {
		return BlobReference{}, failure("blob_storage_integrity")
	}
	ref.SHA256 = digest
	return ref, nil
}
