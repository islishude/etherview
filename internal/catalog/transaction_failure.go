package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	gethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enrich"
)

// TransactionFailure returns the canonical root failure without assembling
// the complete public trace tree. Custom errors remain bound to the exact
// transaction-time execution identity; Solidity builtins never require one.
func (catalog *Postgres) TransactionFailure(
	ctx context.Context,
	chainID, transactionHashText string,
) (TransactionFailure, error) {
	if err := validateChainID(chainID); err != nil {
		return TransactionFailure{}, err
	}
	transactionHash, err := decodeFixedHex(transactionHashText, common.HashLength)
	if err != nil {
		return TransactionFailure{}, ErrInvalidInput
	}
	tx, err := catalog.beginRead(ctx)
	if err != nil {
		return TransactionFailure{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	identity, blockHash, err := catalog.resolveTraceIdentity(ctx, tx, chainID, transactionHash)
	if err != nil {
		return TransactionFailure{}, err
	}
	var receiptStatus sql.NullString
	if err := tx.QueryRowContext(ctx, dbgen.CatalogTransactionFailureReceiptStatus, chainID, identity.BlockNumber, blockHash, transactionHash).Scan(&receiptStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TransactionFailure{}, ErrCorruptData
		}
		return TransactionFailure{}, fmt.Errorf("read transaction failure receipt status: %w", err)
	}
	if !receiptStatus.Valid {
		return TransactionFailure{}, ErrNotApplicable
	}
	switch receiptStatus.String {
	case "0x0":
	case "0x1":
		return TransactionFailure{}, ErrNotApplicable
	default:
		return TransactionFailure{}, ErrCorruptData
	}

	root, err := catalog.scanTraceFrame(tx.QueryRowContext(ctx, dbgen.CatalogTransactionFailureRoot, chainID, identity.BlockNumber, blockHash, transactionHash))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TransactionFailure{}, ErrCorruptData
		}
		return TransactionFailure{}, fmt.Errorf("read transaction failure root: %w", err)
	}
	if root.pathText != "" || root.parentText.Valid || root.frame.Depth != 0 ||
		!root.frame.DirectReverted || !root.frame.Reverted || root.frame.Error == nil ||
		strings.TrimSpace(*root.frame.Error) == "" {
		return TransactionFailure{}, ErrCorruptData
	}

	var revertData []byte
	if root.frame.Output != nil {
		revertData, err = decodeTraceData(*root.frame.Output)
		if err != nil {
			return TransactionFailure{}, ErrCorruptData
		}
	}
	decoded := enrich.NewABIRegistry().DecodeBuiltinRevert(revertData)
	blockNumber, err := strconv.ParseUint(identity.BlockNumber, 10, 64)
	if err != nil {
		return TransactionFailure{}, ErrCorruptData
	}
	if callLikeTraceType(root.frame.CallType) && root.frame.Execution != nil &&
		root.frame.Execution.Address != "" && root.frame.Execution.CodeHash != "" {
		targetBytes, decodeErr := decodeFixedHex(root.frame.Execution.Address, common.AddressLength)
		if decodeErr != nil {
			return TransactionFailure{}, ErrCorruptData
		}
		identity, candidates, loadErr := loadLogABICandidates(
			ctx, tx, chainID, blockNumber, blockHash, common.BytesToAddress(targetBytes),
		)
		if loadErr != nil {
			return TransactionFailure{}, loadErr
		}
		if len(candidates) > 0 && identity.CodeHash.Hex() != root.frame.Execution.CodeHash {
			return TransactionFailure{}, ErrCorruptData
		}
		contractABI := make([]logABICandidate, 0, len(candidates))
		for _, candidate := range candidates {
			if candidate.source != enrich.ABISourceSignatureDatabase {
				contractABI = append(contractABI, candidate)
			}
		}
		if len(contractABI) > 0 && len(contractABI) <= maxReadTimeLogABICandidates {
			registry, registryErr := traceRegistryForCandidates(identity, contractABI)
			if registryErr != nil {
				if errors.Is(registryErr, ErrCorruptData) {
					return TransactionFailure{}, registryErr
				}
			} else {
				decoded = registry.DecodeRevert(identity, revertData)
			}
		}
	}
	decoding, err := transactionFailureDecoding(decoded, revertData)
	if err != nil {
		return TransactionFailure{}, err
	}
	result := TransactionFailure{
		Identity: TransactionResourceIdentity{
			ChainID: identity.ChainID, BlockNumber: identity.BlockNumber, BlockHash: identity.BlockHash,
			TransactionHash: identity.TransactionHash, TransactionIndex: identity.TransactionIndex,
			State: StageComplete,
		},
		Error: *root.frame.Error, Execution: root.frame.Execution, Decoding: decoding,
	}
	if root.frame.Output != nil {
		value := *root.frame.Output
		result.RevertData = &value
	}
	if err := commitRead(tx); err != nil {
		return TransactionFailure{}, err
	}
	return result, nil
}

func transactionFailureDecoding(
	decoded enrich.DecodeResult,
	revertData []byte,
) (TransactionFailureDecoding, error) {
	result := TransactionFailureDecoding{
		Status: string(decoded.Status), ErrorName: decoded.Name, Signature: decoded.Signature,
		Arguments:  transactionCalldataInputs(decoded.Arguments),
		Candidates: append([]string(nil), decoded.Candidates...),
		ABISource:  publicDecodeSource(decoded), Confidence: string(decoded.Confidence), Warning: decoded.Warning,
	}
	if decoded.Status == enrich.DecodeAmbiguous {
		result.ErrorName, result.Signature = "", ""
		result.Arguments, result.ABISource = []TransactionCalldataInput{}, nil
	}
	if decoded.Status == enrich.DecodeDecoded && decoded.Source == enrich.ABISourceBuiltin {
		reason, err := gethabi.UnpackRevert(revertData)
		if err != nil {
			return TransactionFailureDecoding{}, ErrCorruptData
		}
		result.Reason = &reason
	}
	return result, nil
}
