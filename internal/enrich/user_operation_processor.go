package enrich

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/erc4337"
	"github.com/islishude/etherview/internal/stagecontract"
)

var UserOperationStage = stagecontract.UserOperation

type PostgresUserOperationProcessor struct {
	db       *sql.DB
	registry erc4337.Registry
}

func NewPostgresUserOperationProcessor(db *sql.DB, registry erc4337.Registry) (*PostgresUserOperationProcessor, error) {
	if db == nil || len(registry.Entries()) == 0 {
		return nil, errors.New("UserOperation processor requires a database and EntryPoint registry")
	}
	return &PostgresUserOperationProcessor{db: db, registry: registry}, nil
}

func (*PostgresUserOperationProcessor) Stage() StageID { return UserOperationStage }

func (processor *PostgresUserOperationProcessor) ProcessLease(
	ctx context.Context,
	lease Lease,
	queue *PostgresJobQueue,
) (StageResult, error) {
	return processor.Process(ctx, bindStagePublication(lease.Job, lease, queue))
}

func (processor *PostgresUserOperationProcessor) Process(ctx context.Context, job Job) (StageResult, error) {
	if processor == nil || processor.db == nil {
		return StageResult{}, errors.New("process UserOperations using nil database")
	}
	if err := job.Validate(); err != nil {
		return StageResult{}, Permanent(err)
	}
	if job.Stage != UserOperationStage {
		return StageResult{}, Permanent(fmt.Errorf("UserOperation processor received stage %s", job.Stage))
	}
	bundle, err := loadUserOperationBundle(ctx, processor.db, job)
	if err != nil {
		return StageResult{}, err
	}
	chainID, ok := new(big.Int).SetString(job.ChainID, 10)
	if !ok || chainID.Sign() <= 0 {
		return StageResult{}, Permanent(errors.New("UserOperation job chain ID is invalid"))
	}
	operations, err := erc4337.DecodeBlock(processor.registry, chainID, bundle.Block, bundle.Receipts)
	if err != nil {
		return StageResult{}, Permanent(err)
	}
	return runStageTransaction(ctx, processor.db, job, func(ctx context.Context, tx *sql.Tx) (StageResult, error) {
		return processor.persistBlock(ctx, tx, job, operations)
	})
}

func loadUserOperationBundle(ctx context.Context, db *sql.DB, job Job) (chainbundle.Bundle, error) {
	var rawBlock []byte
	blockNumber := strconv.FormatUint(job.BlockNumber, 10)
	if err := db.QueryRowContext(ctx, dbgen.ERC4337SourceBlock, job.ChainID, blockNumber, job.BlockHash[:]).Scan(&rawBlock); err != nil {
		return chainbundle.Bundle{}, fmt.Errorf("query UserOperation source block: %w", err)
	}
	bundle, err := chainbundle.DecodeStoredBlock(json.RawMessage(rawBlock))
	if err != nil {
		return chainbundle.Bundle{}, Permanent(fmt.Errorf("decode UserOperation source block: %w", err))
	}
	rows, err := db.QueryContext(ctx, dbgen.ERC4337SourceReceipts, job.ChainID, blockNumber, job.BlockHash[:])
	if err != nil {
		return chainbundle.Bundle{}, fmt.Errorf("query UserOperation source receipts: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	rawReceipts := make([]json.RawMessage, 0, len(bundle.Block.Transactions()))
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return chainbundle.Bundle{}, fmt.Errorf("scan UserOperation source receipt: %w", err)
		}
		rawReceipts = append(rawReceipts, json.RawMessage(raw))
	}
	if err := rows.Err(); err != nil {
		return chainbundle.Bundle{}, fmt.Errorf("iterate UserOperation source receipts: %w", err)
	}
	bundle, err = bundle.WithStoredReceipts(rawReceipts)
	if err != nil {
		return chainbundle.Bundle{}, Permanent(fmt.Errorf("decode UserOperation source receipts: %w", err))
	}
	if bundle.Block.Hash() != job.BlockHash || bundle.Block.NumberU64() != job.BlockNumber {
		return chainbundle.Bundle{}, Permanent(errors.New("UserOperation source block identity mismatch"))
	}
	return bundle, nil
}

func (processor *PostgresUserOperationProcessor) persistBlock(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	operations []erc4337.Operation,
) (StageResult, error) {
	canonical, err := lockCanonicalBlock(ctx, tx, job)
	if err != nil {
		return StageResult{}, err
	}
	if !canonical {
		return StageResult{State: ResultComplete, Details: map[string]string{"outcome": "stale_canonical_skipped"}}, nil
	}
	digest := processor.registry.Digest()
	blockNumber := strconv.FormatUint(job.BlockNumber, 10)
	var removed bool
	if err := tx.QueryRowContext(ctx, dbgen.ERC4337RemoveCoveredBlock, job.ChainID, digest[:], blockNumber).Scan(&removed); err != nil {
		return StageResult{}, fmt.Errorf("remove prior UserOperation coverage: %w", err)
	}
	if _, err := tx.ExecContext(ctx, dbgen.ERC4337DeleteBlockOutput, job.ChainID, digest[:], blockNumber, job.BlockHash[:]); err != nil {
		return StageResult{}, fmt.Errorf("delete prior UserOperation block output: %w", err)
	}
	for _, operation := range operations {
		if err := persistUserOperation(ctx, tx, job, digest[:], operation); err != nil {
			return StageResult{}, err
		}
	}
	if job.publication != nil {
		identity, err := publicationIdentity(job.publication.lease)
		if err != nil {
			return StageResult{}, err
		}
		var added bool
		if err := tx.QueryRowContext(
			ctx, dbgen.ERC4337AddCoveredBlock,
			job.ChainID, digest[:], blockNumber, job.BlockHash[:], identity.jobID, identity.generation,
		).Scan(&added); err != nil {
			return StageResult{}, fmt.Errorf("publish UserOperation coverage: %w", err)
		}
	}
	return StageResult{State: ResultComplete, Details: map[string]string{
		"configuration_digest": processor.registry.DigestHex(),
		"user_operations":      strconv.Itoa(len(operations)),
	}}, nil
}

func persistUserOperation(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	digest []byte,
	operation erc4337.Operation,
) error {
	if operation.TransactionIndex > math.MaxInt64 || operation.OperationIndex > math.MaxInt64 ||
		operation.EventLogIndex > math.MaxInt64 {
		return Permanent(errors.New("UserOperation position exceeds PostgreSQL BIGINT"))
	}
	nonceKey := new(big.Int).Rsh(new(big.Int).Set(operation.Request.Nonce), 64)
	nonceSequence := new(big.Int).And(new(big.Int).Set(operation.Request.Nonce), new(big.Int).SetUint64(math.MaxUint64))
	args := []any{
		job.ChainID, digest, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		operation.TransactionHash[:], int64(operation.TransactionIndex), int64(operation.OperationIndex),
		int64(operation.EventLogIndex), operation.Hash[:],
		operation.EntryPoint[:], string(operation.Version), operation.Request.Sender[:],
		operation.Request.Nonce.String(), nonceKey.String(), nonceSequence.String(),
		operation.Bundler[:], operation.Beneficiary[:], string(operation.Request.InitKind),
		addressBytes(operation.Request.Factory), addressBytes(operation.Request.Paymaster), addressBytes(operation.Request.Aggregator),
		operation.Success, operation.ActualGasCost.String(), operation.ActualGasUsed.String(),
		operation.Request.CallGasLimit.String(), operation.Request.VerificationGasLimit.String(),
		operation.Request.PreVerificationGas.String(), operation.Request.MaxFeePerGas.String(),
		operation.Request.MaxPriorityFeePerGas.String(), optionalBigString(operation.Request.PaymasterVerificationGasLimit),
		optionalBigString(operation.Request.PaymasterPostOpGasLimit), nonNilBytes(operation.Request.InitCode),
		nonNilBytes(operation.Request.FactoryData), nonNilBytes(operation.Request.CallData), nonNilBytes(operation.Request.PaymasterAndData),
		nonNilBytes(operation.Request.PaymasterData), nonNilBytes(operation.Request.PaymasterSignature), nonNilBytes(operation.Request.Signature),
		nilIfEmpty(operation.Request.AccountGasLimits), nilIfEmpty(operation.Request.GasFees),
		nonNilBytes(operation.Request.AggregatedSignature),
	}
	if _, err := tx.ExecContext(ctx, dbgen.ERC4337InsertUserOperation, args...); err != nil {
		return fmt.Errorf("persist UserOperation %s: %w", operation.Hash, err)
	}
	for _, event := range operation.Events {
		if err := persistUserOperationEvent(ctx, tx, job, digest, operation, event); err != nil {
			return err
		}
	}
	for _, participant := range operationParticipants(operation) {
		if _, err := tx.ExecContext(
			ctx, dbgen.ERC4337InsertUserOperationParticipant,
			job.ChainID, digest, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
			operation.TransactionHash[:], int64(operation.OperationIndex), participant.address[:], participant.role,
		); err != nil {
			return fmt.Errorf("persist UserOperation participant: %w", err)
		}
	}
	return nil
}

func persistUserOperationEvent(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	digest []byte,
	operation erc4337.Operation,
	event erc4337.ProtocolEvent,
) error {
	if event.LogIndex > math.MaxInt64 {
		return Permanent(errors.New("UserOperation event index exceeds PostgreSQL BIGINT"))
	}
	_, err := tx.ExecContext(
		ctx, dbgen.ERC4337InsertUserOperationEvent,
		job.ChainID, digest, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		operation.TransactionHash[:], int64(operation.OperationIndex), int64(event.LogIndex), string(event.Kind),
		event.Sender[:], optionalBigString(event.Nonce), addressBytes(event.RelatedAddress), addressBytes(event.Paymaster),
		nonNilBytes(event.RawData), event.Reason, optionalBigString(event.PanicCode),
	)
	if err != nil {
		return fmt.Errorf("persist UserOperation protocol event: %w", err)
	}
	return nil
}

type userOperationParticipant struct {
	address common.Address
	role    string
}

func operationParticipants(operation erc4337.Operation) []userOperationParticipant {
	candidates := []userOperationParticipant{
		{operation.Request.Sender, "sender"}, {operation.EntryPoint, "entry_point"},
		{operation.Bundler, "bundler"}, {operation.Beneficiary, "beneficiary"},
	}
	for _, optional := range []struct {
		address *common.Address
		role    string
	}{
		{operation.Request.Factory, "factory"}, {operation.Request.Paymaster, "paymaster"},
		{operation.Request.Aggregator, "aggregator"},
	} {
		if optional.address != nil {
			candidates = append(candidates, userOperationParticipant{*optional.address, optional.role})
		}
	}
	for _, event := range operation.Events {
		if event.Kind == erc4337.EventEIP7702Initialized && event.RelatedAddress != nil {
			candidates = append(candidates, userOperationParticipant{*event.RelatedAddress, "eip7702_delegate"})
		}
	}
	seen := make(map[string]struct{}, len(candidates))
	participants := make([]userOperationParticipant, 0, len(candidates))
	for _, candidate := range candidates {
		key := candidate.address.Hex() + ":" + candidate.role
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		participants = append(participants, candidate)
	}
	return participants
}

func addressBytes(value *common.Address) any {
	if value == nil {
		return nil
	}
	return value[:]
}

func optionalBigString(value *big.Int) string {
	if value == nil {
		return ""
	}
	return value.String()
}

func nilIfEmpty(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func nonNilBytes(value []byte) []byte {
	if value == nil {
		return []byte{}
	}
	return value
}
