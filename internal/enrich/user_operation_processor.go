package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/chainbundle"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/erc4337"
	"github.com/islishude/etherview/internal/stagecontract"
)

var UserOperationStage = stagecontract.UserOperation

type PostgresUserOperationProcessor struct {
	db       dbaccess.Database
	registry erc4337.Registry
}

func NewPostgresUserOperationProcessor(db dbaccess.Database, registry erc4337.Registry) (*PostgresUserOperationProcessor, error) {
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
	return runStageTransaction(ctx, processor.db, job, func(ctx context.Context, tx pgx.Tx) (StageResult, error) {
		return processor.persistBlock(ctx, tx, job, operations)
	})
}

func loadUserOperationBundle(ctx context.Context, db dbaccess.Database, job Job) (chainbundle.Bundle, error) {
	var rawBlock []byte
	blockNumber := strconv.FormatUint(job.BlockNumber, 10)
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(blockNumber); err != nil {
			return err
		}
		queryRow, err := dbgen.New(db).ERC4337SourceBlock(ctx, queryValue0, queryValue1, job.BlockHash[:])
		if err != nil {
			return err
		}
		rawBlock = queryRow
		return nil
	}(); err != nil {
		return chainbundle.Bundle{}, fmt.Errorf("query UserOperation source block: %w", err)
	}
	bundle, err := chainbundle.DecodeStoredBlock(json.RawMessage(rawBlock))
	if err != nil {
		return chainbundle.Bundle{}, Permanent(fmt.Errorf("decode UserOperation source block: %w", err))
	}
	rows, err := func() ([][]byte, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(blockNumber); err != nil {
			return nil, err
		}
		return dbgen.New(db).ERC4337SourceReceipts(ctx, queryValue0, queryValue1, job.BlockHash[:])
	}()
	if err != nil {
		return chainbundle.Bundle{}, fmt.Errorf("query UserOperation source receipts: %w", err)
	}

	rawReceipts := make([]json.RawMessage, 0, len(bundle.Block.Transactions()))
	for _, storedRow := range rows {
		var raw []byte
		{
			raw = storedRow
		}
		rawReceipts = append(rawReceipts, json.RawMessage(raw))
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
	tx pgx.Tx,
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

	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(blockNumber); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).ERC4337RemoveCoveredBlock(ctx, queryValue0, digest[:], queryValue1)
		if err != nil {
			return err
		}
		_ = queryRow
		return nil
	}(); err != nil {
		return StageResult{}, fmt.Errorf("remove prior UserOperation coverage: %w", err)
	}
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(blockNumber); err != nil {
			return err
		}
		return dbgen.New(tx).ERC4337DeleteBlockOutput(ctx, dbgen.ERC4337DeleteBlockOutputParams{ChainID: queryValue0, ConfigurationDigest: digest[:], BlockNumber: queryValue1, BlockHash: job.BlockHash[:]})
	}(); err != nil {
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

		if err := func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(job.ChainID); err != nil {
				return err
			}
			var queryValue1 pgtype.Numeric
			if err := queryValue1.Scan(blockNumber); err != nil {
				return err
			}
			queryRow, err := dbgen.New(tx).ERC4337AddCoveredBlock(ctx, dbgen.ERC4337AddCoveredBlockParams{ChainID: queryValue0, ConfigurationDigest: digest[:], BlockNumber: queryValue1, BlockHash: job.BlockHash[:], DurableJobID: identity.jobID, JobGeneration: identity.generation})
			if err != nil {
				return err
			}
			_ = queryRow
			return nil
		}(); err != nil {
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
	tx pgx.Tx,
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
	var chain pgtype.Numeric
	if err := chain.Scan(job.ChainID); err != nil {
		return err
	}
	params := dbgen.ERC4337InsertUserOperationParams{
		ChainID: chain, ConfigurationDigest: digest, BlockNumber: userOperationNumeric(new(big.Int).SetUint64(job.BlockNumber)), BlockHash: job.BlockHash[:],
		TransactionHash: operation.TransactionHash[:], TransactionIndex: int64(operation.TransactionIndex), OperationIndex: int64(operation.OperationIndex), EventLogIndex: int64(operation.EventLogIndex), UserOpHash: operation.Hash[:],
		EntryPoint: operation.EntryPoint[:], EntryPointVersion: string(operation.Version), Sender: operation.Request.Sender[:],
		Nonce: userOperationNumeric(operation.Request.Nonce), NonceKey: userOperationNumeric(nonceKey), NonceSequence: userOperationNumeric(nonceSequence),
		Bundler: operation.Bundler[:], Beneficiary: operation.Beneficiary[:], InitKind: string(operation.Request.InitKind), Factory: addressBytes(operation.Request.Factory), Paymaster: addressBytes(operation.Request.Paymaster), Aggregator: addressBytes(operation.Request.Aggregator),
		Success: operation.Success, ActualGasCost: userOperationNumeric(operation.ActualGasCost), ActualGasUsed: userOperationNumeric(operation.ActualGasUsed), CallGasLimit: userOperationNumeric(operation.Request.CallGasLimit), VerificationGasLimit: userOperationNumeric(operation.Request.VerificationGasLimit), PreVerificationGas: userOperationNumeric(operation.Request.PreVerificationGas), MaxFeePerGas: userOperationNumeric(operation.Request.MaxFeePerGas), MaxPriorityFeePerGas: userOperationNumeric(operation.Request.MaxPriorityFeePerGas),
		PaymasterVerificationGasLimit: optionalBigString(operation.Request.PaymasterVerificationGasLimit), PaymasterPostOpGasLimit: optionalBigString(operation.Request.PaymasterPostOpGasLimit),
		InitCode: nonNilBytes(operation.Request.InitCode), FactoryData: nonNilBytes(operation.Request.FactoryData), CallData: nonNilBytes(operation.Request.CallData), PaymasterAndData: nonNilBytes(operation.Request.PaymasterAndData), PaymasterData: nonNilBytes(operation.Request.PaymasterData), PaymasterSignature: nonNilBytes(operation.Request.PaymasterSignature), Signature: nonNilBytes(operation.Request.Signature), AccountGasLimits: nilIfEmpty(operation.Request.AccountGasLimits), GasFees: nilIfEmpty(operation.Request.GasFees), AggregatedSignature: nonNilBytes(operation.Request.AggregatedSignature),
	}
	if err := dbgen.New(tx).ERC4337InsertUserOperation(ctx, params); err != nil {
		return fmt.Errorf("persist UserOperation %s: %w", operation.Hash, err)
	}
	for _, event := range operation.Events {
		if err := persistUserOperationEvent(ctx, tx, job, digest, operation, event); err != nil {
			return err
		}
	}
	for _, participant := range operationParticipants(operation) {
		if err := func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(job.ChainID); err != nil {
				return err
			}
			var queryValue1 pgtype.Numeric
			if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
				return err
			}
			return dbgen.New(tx).ERC4337InsertUserOperationParticipant(ctx, dbgen.ERC4337InsertUserOperationParticipantParams{ChainID: queryValue0, ConfigurationDigest: digest, BlockNumber: queryValue1, BlockHash: job.BlockHash[:], TransactionHash: operation.TransactionHash[:], OperationIndex: int64(operation.OperationIndex), Address: participant.address[:], Role: participant.role})
		}(); err != nil {
			return fmt.Errorf("persist UserOperation participant: %w", err)
		}
	}
	return nil
}

func persistUserOperationEvent(
	ctx context.Context,
	tx pgx.Tx,
	job Job,
	digest []byte,
	operation erc4337.Operation,
	event erc4337.ProtocolEvent,
) error {
	if event.LogIndex > math.MaxInt64 {
		return Permanent(errors.New("UserOperation event index exceeds PostgreSQL BIGINT"))
	}
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return err
		}
		return dbgen.New(tx).ERC4337InsertUserOperationEvent(ctx, dbgen.ERC4337InsertUserOperationEventParams{ChainID: queryValue0, ConfigurationDigest: digest, BlockNumber: queryValue1, BlockHash: job.BlockHash[:], TransactionHash: operation.TransactionHash[:], OperationIndex: int64(operation.OperationIndex), LogIndex: int64(event.LogIndex), EventKind: string(event.Kind), Sender: event.Sender[:], Nonce: optionalBigString(event.Nonce), RelatedAddress: addressBytes(event.RelatedAddress), Paymaster: addressBytes(event.Paymaster), RawData: nonNilBytes(event.RawData), Reason: event.Reason, PanicCode: optionalBigString(event.PanicCode)})
	}()
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

func addressBytes(value *common.Address) []byte {
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

func nilIfEmpty(value []byte) []byte {
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

func userOperationNumeric(value *big.Int) pgtype.Numeric {
	if value == nil {
		return pgtype.Numeric{}
	}
	return pgtype.Numeric{Int: value, Valid: true}
}
