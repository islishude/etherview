package chainbundle

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/ethereum/go-ethereum/core/types"
)

const storedEnvelopeFormat = "etherview.chainbundle.v1"

const (
	storedMetadataField  = "_etherviewChainBundle"
	storedMetadataFormat = "etherview.chainbundle.uncles.v1"
)

type storedBlockMetadata struct {
	Format    string            `json:"format"`
	RawUncles []json.RawMessage `json:"rawUncles"`
}

// DecodeStoredHeader returns an authenticated header from every supported
// stored block shape without requiring legacy PoW uncle headers or decoding
// the block body.
func DecodeStoredHeader(raw json.RawMessage) (*types.Header, error) {
	fields, err := decodeObject(raw, "storedBlock")
	if err != nil {
		return nil, err
	}
	formatRaw, hasFormat := fields["format"]
	rawBlock, hasRawBlock := fields["rawBlock"]
	_, hasRawUncles := fields["rawUncles"]
	var format string
	envelope := len(fields) == 3 &&
		hasFormat &&
		hasRawBlock &&
		hasRawUncles &&
		json.Unmarshal(formatRaw, &format) == nil &&
		format == storedEnvelopeFormat
	if envelope {
		return DecodeHeader(rawBlock)
	}
	return DecodeHeader(raw)
}

// EncodeStoredBlock preserves the block's existing top-level JSON shape so
// established JSONB paths such as raw->>'miner' keep working. PoW-only uncle
// headers are attached under a reserved, versioned metadata field and removed
// again by DecodeStoredBlock before the RPC payload is exposed.
func EncodeStoredBlock(bundle Bundle) (json.RawMessage, error) {
	if err := Validate(bundle); err != nil {
		return nil, err
	}
	return encodeStoredBlock(bundle)
}

// EncodeOwnedStoredBlock encodes a bundle returned by Clone without repeating
// its full root decode and alignment check. It is intended only for the
// immediate persistence path that owns the cloned value.
func EncodeOwnedStoredBlock(bundle Bundle) (json.RawMessage, error) {
	if !bundle.owned {
		return nil, validation("block", "must be owned before persistence encoding")
	}
	return encodeStoredBlock(bundle)
}

func encodeStoredBlock(bundle Bundle) (json.RawMessage, error) {
	fields, err := decodeObject(bundle.RawBlock, "block")
	if err != nil {
		return nil, err
	}
	if _, exists := fields[storedMetadataField]; exists {
		return nil, fmt.Errorf(
			"%w: %q",
			ErrReservedStoredMetadata,
			storedMetadataField,
		)
	}
	if len(bundle.RawUncles) == 0 {
		return cloneRaw(bundle.RawBlock), nil
	}
	metadata, err := json.Marshal(storedBlockMetadata{
		Format:    storedMetadataFormat,
		RawUncles: cloneRawSlice(bundle.RawUncles),
	})
	if err != nil {
		return nil, fmt.Errorf("encode stored chain bundle metadata: %w", err)
	}
	fields[storedMetadataField] = metadata
	data, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encode stored chain bundle block: %w", err)
	}
	return data, nil
}

// DecodeStoredBlock accepts the current root-preserving metadata form, the
// short-lived envelope form emitted during development, and legacy blocks that
// had no uncles. A legacy PoW block with uncle hashes fails permanently because
// the missing headers cannot be reconstructed or root-validated.
func DecodeStoredBlock(raw json.RawMessage) (Bundle, error) {
	fields, err := decodeObject(raw, "storedBlock")
	if err != nil {
		return Bundle{}, err
	}
	formatRaw, hasFormat := fields["format"]
	_, hasRawBlock := fields["rawBlock"]
	var format string
	_, hasRawUncles := fields["rawUncles"]
	envelope := len(fields) == 3 &&
		hasFormat &&
		hasRawBlock &&
		hasRawUncles &&
		json.Unmarshal(formatRaw, &format) == nil &&
		format == storedEnvelopeFormat
	if envelope {
		rawBlock, exists := fields["rawBlock"]
		if !exists || len(bytes.TrimSpace(rawBlock)) == 0 || isNull(rawBlock) {
			return Bundle{}, fmt.Errorf("%w: stored block envelope has no rawBlock", ErrInvalidWireValue)
		}
		rawUncles, err := requiredArray(fields, "rawUncles", "storedBlock.rawUncles")
		if err != nil {
			return Bundle{}, err
		}
		return DecodeBlock(rawBlock, rawUncles)
	}

	if metadataRaw, exists := fields[storedMetadataField]; exists {
		metadataFields, err := decodeObject(metadataRaw, "storedBlock.metadata")
		if err == nil {
			var metadataFormat string
			if json.Unmarshal(metadataFields["format"], &metadataFormat) == nil &&
				metadataFormat == storedMetadataFormat {
				rawUncles, err := requiredArray(
					metadataFields,
					"rawUncles",
					"storedBlock.metadata.rawUncles",
				)
				if err != nil {
					return Bundle{}, err
				}
				delete(fields, storedMetadataField)
				rawBlock, err := json.Marshal(fields)
				if err != nil {
					return Bundle{}, fmt.Errorf("decode stored chain bundle block: %w", err)
				}
				return DecodeBlock(rawBlock, rawUncles)
			}
		}
	}

	hashes, err := UncleHashes(raw)
	if err != nil {
		return Bundle{}, err
	}
	if len(hashes) != 0 {
		return Bundle{}, ErrStoredUncleHeadersUnavailable
	}
	bundle, err := decodeBlock(raw, nil, true)
	if err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}
