package chainbundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

var (
	// ErrUnsupportedTransactionType is returned before a partial block bundle is
	// exposed when the linked go-ethereum version cannot decode a transaction
	// envelope. Upgrading the binary is required; retrying the same source
	// response cannot make the transaction supported.
	ErrUnsupportedTransactionType error = permanentError("unsupported Ethereum transaction type")
	// ErrStoredUncleHeadersUnavailable identifies legacy persisted PoW blocks
	// that retained only uncle hashes. The full headers cannot be reconstructed
	// or root-validated without an explicit RPC-backed repair.
	ErrStoredUncleHeadersUnavailable error = permanentError("stored block does not contain required uncle headers")
	// ErrReservedStoredMetadata prevents an RPC extension from being
	// overwritten by Etherview's PoW uncle persistence metadata.
	ErrReservedStoredMetadata error = permanentError("block contains reserved chain bundle metadata")
	ErrInvalidWireValue             = errors.New("invalid Ethereum JSON-RPC value")
)

type permanentError string

func (e permanentError) Error() string   { return string(e) }
func (e permanentError) Permanent() bool { return true }

// IsPermanent reports whether retrying an unchanged source response cannot
// succeed without changing the running binary or its configuration.
func IsPermanent(err error) bool {
	var target interface{ Permanent() bool }
	return errors.As(err, &target) && target.Permanent()
}

// Bundle keeps the reviewed go-ethereum projection aligned with the exact RPC
// bytes from which it was decoded. Persistence must write the Raw* fields and
// must never recreate them by marshaling the typed values.
type Bundle struct {
	Block           *types.Block
	Receipts        types.Receipts
	RawBlock        json.RawMessage
	RawTransactions []json.RawMessage
	RawReceipts     []json.RawMessage
	RawLogs         [][]json.RawMessage
	RawUncles       []json.RawMessage
	RawWithdrawals  []json.RawMessage

	// legacyStoredBlockShape is set only while decoding block rows written by the
	// pre-chainbundle persistence codec. It must never be enabled for fresh RPC
	// responses because those remain subject to the complete wire contract.
	legacyStoredBlockShape bool
	// storedReceiptShape permits the legacy receipt omissions documented by
	// WithStoredReceipts without weakening fresh RPC validation.
	storedReceiptShape bool
}

func (b Bundle) Number() (uint64, error) {
	if b.Block == nil || b.Block.Number() == nil {
		return 0, errors.New("block number is null")
	}
	number := b.Block.Number()
	if !number.IsUint64() {
		return 0, errors.New("block number exceeds uint64")
	}
	return number.Uint64(), nil
}

func (b Bundle) BlockHash() (common.Hash, error) {
	if b.Block == nil {
		return common.Hash{}, errors.New("block is nil")
	}
	return b.Block.Hash(), nil
}

// Clone re-decodes the preserved raw payloads, producing independent raw and
// typed slices while retaining the already validated uncle headers.
func (b Bundle) Clone() (Bundle, error) {
	if err := Validate(b); err != nil {
		return Bundle{}, err
	}
	var clone Bundle
	var err error
	if b.legacyStoredBlockShape {
		clone, err = decodeBlock(b.RawBlock, b.RawUncles, true)
	} else {
		clone, err = DecodeBlock(b.RawBlock, b.RawUncles)
	}
	if err != nil {
		return Bundle{}, err
	}
	if b.storedReceiptShape {
		return clone.withReceipts(b.RawReceipts, true)
	}
	return clone.WithReceipts(b.RawReceipts)
}

// Validate verifies both the typed facts and every raw-alignment slice by
// decoding the exact source payload again. A caller cannot substitute a typed
// value or a regenerated raw child without detection.
func Validate(bundle Bundle) error {
	if bundle.Block == nil {
		return validation("block", "must not be nil")
	}
	var decoded Bundle
	var err error
	if bundle.legacyStoredBlockShape {
		decoded, err = decodeBlock(bundle.RawBlock, bundle.RawUncles, true)
	} else {
		decoded, err = DecodeBlock(bundle.RawBlock, bundle.RawUncles)
	}
	if err != nil {
		return err
	}
	decoded, err = decoded.withReceipts(
		bundle.RawReceipts,
		bundle.storedReceiptShape,
	)
	if err != nil {
		return err
	}
	if decoded.Block.Hash() != bundle.Block.Hash() {
		return validation("block", "typed header does not match raw block")
	}
	if !sameBody(decoded.Block, bundle.Block) {
		return validation("block", "typed body does not match raw block")
	}
	if !reflect.DeepEqual(decoded.Receipts, bundle.Receipts) {
		return validation("receipts", "typed receipts do not match raw receipts")
	}
	if !sameRawSlice(decoded.RawTransactions, bundle.RawTransactions) {
		return validation("rawTransactions", "does not align with raw block")
	}
	if !sameRawSlice(decoded.RawReceipts, bundle.RawReceipts) {
		return validation("rawReceipts", "does not align with typed receipts")
	}
	if !sameNestedRawSlice(decoded.RawLogs, bundle.RawLogs) {
		return validation("rawLogs", "does not align with raw receipts")
	}
	if !sameRawSlice(decoded.RawUncles, bundle.RawUncles) {
		return validation("rawUncles", "does not align with raw block")
	}
	if !sameRawSlice(decoded.RawWithdrawals, bundle.RawWithdrawals) {
		return validation("rawWithdrawals", "does not align with raw block")
	}
	return nil
}

func ValidateParent(child, parent Bundle) error {
	childNumber, err := child.Number()
	if err != nil {
		return err
	}
	parentNumber, err := parent.Number()
	if err != nil {
		return err
	}
	if parentNumber == math.MaxUint64 || childNumber != parentNumber+1 {
		return validation("block.number", fmt.Sprintf("child %d does not immediately follow parent %d", childNumber, parentNumber))
	}
	if child.Block.ParentHash() != parent.Block.Hash() {
		return validation("block.parentHash", "does not match supplied parent")
	}
	return nil
}

type ValidationError struct {
	Path    string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Path, e.Message)
}

func IsValidationError(err error) bool {
	var target *ValidationError
	return errors.As(err, &target)
}

func validation(path, message string) error {
	return &ValidationError{Path: path, Message: message}
}

func sameBody(left, right *types.Block) bool {
	if left == nil || right == nil ||
		left.Hash() != right.Hash() ||
		len(left.Transactions()) != len(right.Transactions()) ||
		len(left.Uncles()) != len(right.Uncles()) ||
		len(left.Withdrawals()) != len(right.Withdrawals()) {
		return false
	}
	for index := range left.Transactions() {
		if left.Transactions()[index].Hash() != right.Transactions()[index].Hash() {
			return false
		}
	}
	for index := range left.Uncles() {
		if left.Uncles()[index].Hash() != right.Uncles()[index].Hash() {
			return false
		}
	}
	return reflect.DeepEqual(left.Withdrawals(), right.Withdrawals())
}

func sameRawSlice(left, right []json.RawMessage) bool {
	if (left == nil) != (right == nil) || len(left) != len(right) {
		return false
	}
	for index := range left {
		if !bytes.Equal(left[index], right[index]) {
			return false
		}
	}
	return true
}

func sameNestedRawSlice(left, right [][]json.RawMessage) bool {
	if (left == nil) != (right == nil) || len(left) != len(right) {
		return false
	}
	for index := range left {
		if !sameRawSlice(left[index], right[index]) {
			return false
		}
	}
	return true
}
