package query

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/publicquery"
)

type userOperationCursor struct {
	Kind                   string `json:"kind"`
	ChainID                string `json:"chain_id"`
	ConfigurationDigest    string `json:"configuration_digest"`
	IndexStart             uint64 `json:"index_start"`
	SnapshotNumber         uint64 `json:"snapshot_number"`
	SnapshotHash           string `json:"snapshot_hash"`
	Address                string `json:"address,omitempty"`
	TransactionHash        string `json:"transaction_hash,omitempty"`
	BeforeBlockNumber      uint64 `json:"before_block_number,omitempty"`
	BeforeTransactionIndex uint64 `json:"before_transaction_index,omitempty"`
	BeforeOperationIndex   uint64 `json:"before_operation_index,omitempty"`
	BeforeUserOpHash       string `json:"before_user_op_hash,omitempty"`
	AfterOperationIndex    uint64 `json:"after_operation_index,omitempty"`
}

type userOperationSummaryRow struct {
	userOpHash, entryPoint, sender              []byte
	entryPointVersion                           string
	nonce, nonceKey, nonceSequence              string
	success                                     bool
	actualGasCost, actualGasUsed                string
	transactionHash                             []byte
	transactionIndex, operationIndex            int64
	eventLogIndex                               int64
	blockNumber                                 string
	blockHash                                   []byte
	blockTimestamp, safeNumber, finalizedNumber string
	bundler, beneficiary                        []byte
	initKind                                    string
	factory, paymaster, aggregator              []byte
	participatingRoles                          []byte
}

type userOperationDetailRow struct {
	summary                                                userOperationSummaryRow
	callGasLimit, verificationGasLimit, preVerificationGas string
	maxFeePerGas, maxPriorityFeePerGas                     string
	paymasterVerificationGasLimit, paymasterPostOpGasLimit string
	initCode, factoryData, callData                        []byte
	paymasterAndData, paymasterData, paymasterSignature    []byte
	signature, accountGasLimits, gasFees                   []byte
	aggregatedSignature                                    []byte
}

type userOperationEventRow struct {
	kind, nonce, panicCode string
	logIndex               int64
	sender, relatedAddress []byte
	paymaster, rawData     []byte
	reason                 *string
}

func (r *PostgresReader) UserOperations(
	ctx context.Context,
	encodedCursor string,
	limit int,
) (publicquery.UserOperationPage, error) {
	if err := r.validateUserOperationRequest(limit); err != nil {
		return publicquery.UserOperationPage{}, err
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return publicquery.UserOperationPage{}, fmt.Errorf("begin UserOperation page: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	cursor, err := r.prepareUserOperationCursor(ctx, tx, "global", "", "", encodedCursor)
	if err != nil {
		return publicquery.UserOperationPage{}, err
	}
	rows, err := tx.QueryContext(ctx, dbgen.ERC4337ListUserOperations,
		r.chainID, r.userOperationDigest, strconv.FormatUint(cursor.IndexStart, 10),
		strconv.FormatUint(cursor.SnapshotNumber, 10), encodedCursor != "",
		strconv.FormatUint(cursor.BeforeBlockNumber, 10), int64(cursor.BeforeTransactionIndex),
		int64(cursor.BeforeOperationIndex), cursorBoundaryHash(cursor.BeforeUserOpHash), limit+1,
	)
	if err != nil {
		return publicquery.UserOperationPage{}, fmt.Errorf("query UserOperations: %w", err)
	}
	items, boundaries, err := scanUserOperationRows(rows)
	if err != nil {
		return publicquery.UserOperationPage{}, err
	}
	page, err := userOperationPage(items, boundaries, cursor, limit)
	if err != nil {
		return publicquery.UserOperationPage{}, err
	}
	if err := tx.Commit(); err != nil {
		return publicquery.UserOperationPage{}, fmt.Errorf("commit UserOperation page: %w", err)
	}
	return page, nil
}

func (r *PostgresReader) AddressUserOperations(
	ctx context.Context,
	rawAddress, encodedCursor string,
	limit int,
) (publicquery.UserOperationPage, error) {
	if err := r.validateUserOperationRequest(limit); err != nil {
		return publicquery.UserOperationPage{}, err
	}
	address, err := ethrpc.ParseAddress(rawAddress)
	if err != nil {
		return publicquery.UserOperationPage{}, fmt.Errorf("%w: invalid UserOperation participant address", publicquery.ErrInvalidInput)
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return publicquery.UserOperationPage{}, fmt.Errorf("begin address UserOperation page: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	cursor, err := r.prepareUserOperationCursor(ctx, tx, "address", address.Hex(), "", encodedCursor)
	if err != nil {
		return publicquery.UserOperationPage{}, err
	}
	rows, err := tx.QueryContext(ctx, dbgen.ERC4337ListAddressUserOperations,
		address.Bytes(), r.chainID, r.userOperationDigest, strconv.FormatUint(cursor.IndexStart, 10),
		strconv.FormatUint(cursor.SnapshotNumber, 10), encodedCursor != "",
		strconv.FormatUint(cursor.BeforeBlockNumber, 10), int64(cursor.BeforeTransactionIndex),
		int64(cursor.BeforeOperationIndex), cursorBoundaryHash(cursor.BeforeUserOpHash), limit+1,
	)
	if err != nil {
		return publicquery.UserOperationPage{}, fmt.Errorf("query address UserOperations: %w", err)
	}
	items, boundaries, err := scanUserOperationRows(rows)
	if err != nil {
		return publicquery.UserOperationPage{}, err
	}
	page, err := userOperationPage(items, boundaries, cursor, limit)
	if err != nil {
		return publicquery.UserOperationPage{}, err
	}
	if err := tx.Commit(); err != nil {
		return publicquery.UserOperationPage{}, fmt.Errorf("commit address UserOperation page: %w", err)
	}
	return page, nil
}

func (r *PostgresReader) TransactionUserOperations(
	ctx context.Context,
	rawHash, encodedCursor string,
	limit int,
) (publicquery.UserOperationPage, error) {
	if err := r.validateUserOperationRequest(limit); err != nil {
		return publicquery.UserOperationPage{}, err
	}
	hash, err := ethrpc.ParseHash(rawHash)
	if err != nil {
		return publicquery.UserOperationPage{}, fmt.Errorf("%w: invalid UserOperation bundle transaction hash", publicquery.ErrInvalidInput)
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return publicquery.UserOperationPage{}, fmt.Errorf("begin transaction UserOperation page: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	cursor, err := r.prepareUserOperationCursor(ctx, tx, "transaction", "", strings.ToLower(hash.Hex()), encodedCursor)
	if err != nil {
		return publicquery.UserOperationPage{}, err
	}
	var transactionBlock string
	var transactionBlockHash []byte
	if err := tx.QueryRowContext(ctx, dbgen.ERC4337CanonicalTransactionBlock, r.chainID, hash[:]).Scan(
		&transactionBlock, &transactionBlockHash,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return publicquery.UserOperationPage{}, publicquery.ErrNotFound
		}
		return publicquery.UserOperationPage{}, fmt.Errorf("query UserOperation bundle transaction: %w", err)
	}
	blockNumber, err := parseDecimalUint64(transactionBlock)
	if err != nil || len(transactionBlockHash) != common.HashLength {
		return publicquery.UserOperationPage{}, errors.New("stored UserOperation bundle transaction identity is invalid")
	}
	if blockNumber >= cursor.IndexStart && blockNumber > cursor.SnapshotNumber {
		return publicquery.UserOperationPage{}, publicquery.ErrNotReady
	}
	rows, err := tx.QueryContext(ctx, dbgen.ERC4337ListTransactionUserOperations,
		r.chainID, r.userOperationDigest, hash[:], strconv.FormatUint(cursor.IndexStart, 10),
		strconv.FormatUint(cursor.SnapshotNumber, 10), encodedCursor != "",
		int64(cursor.AfterOperationIndex), limit+1,
	)
	if err != nil {
		return publicquery.UserOperationPage{}, fmt.Errorf("query transaction UserOperations: %w", err)
	}
	items, boundaries, err := scanUserOperationRows(rows)
	if err != nil {
		return publicquery.UserOperationPage{}, err
	}
	page, err := transactionUserOperationPage(items, boundaries, cursor, limit)
	if err != nil {
		return publicquery.UserOperationPage{}, err
	}
	if err := tx.Commit(); err != nil {
		return publicquery.UserOperationPage{}, fmt.Errorf("commit transaction UserOperation page: %w", err)
	}
	return page, nil
}

func (r *PostgresReader) UserOperation(ctx context.Context, rawHash string) (gen.UserOperationDetail, error) {
	if err := r.requireUserOperations(); err != nil {
		return gen.UserOperationDetail{}, err
	}
	hash, err := ethrpc.ParseHash(rawHash)
	if err != nil {
		return gen.UserOperationDetail{}, fmt.Errorf("%w: invalid userOpHash", publicquery.ErrInvalidInput)
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return gen.UserOperationDetail{}, fmt.Errorf("begin UserOperation detail: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	row := userOperationDetailRow{}
	targets := userOperationSummaryTargets(&row.summary)
	targets = append(targets,
		&row.callGasLimit, &row.verificationGasLimit, &row.preVerificationGas,
		&row.maxFeePerGas, &row.maxPriorityFeePerGas,
		&row.paymasterVerificationGasLimit, &row.paymasterPostOpGasLimit,
		&row.initCode, &row.factoryData, &row.callData, &row.paymasterAndData,
		&row.paymasterData, &row.paymasterSignature, &row.signature,
		&row.accountGasLimits, &row.gasFees, &row.aggregatedSignature,
	)
	err = tx.QueryRowContext(ctx, dbgen.ERC4337GetUserOperation, r.chainID, r.userOperationDigest, hash[:]).Scan(targets...)
	if errors.Is(err, sql.ErrNoRows) {
		snapshot, snapshotErr := r.currentUserOperationSnapshot(ctx, tx)
		if snapshotErr != nil {
			return gen.UserOperationDetail{}, snapshotErr
		}
		tip, tipErr := r.currentBlockCursor(ctx, tx)
		if tipErr != nil {
			return gen.UserOperationDetail{}, tipErr
		}
		if snapshot.SnapshotNumber < tip.SnapshotNumber {
			return gen.UserOperationDetail{}, publicquery.ErrNotReady
		}
		return gen.UserOperationDetail{}, publicquery.ErrNotFound
	}
	if err != nil {
		return gen.UserOperationDetail{}, fmt.Errorf("query UserOperation detail: %w", err)
	}
	summary, _, err := userOperationSummaryModel(row.summary)
	if err != nil {
		return gen.UserOperationDetail{}, err
	}
	detail, err := userOperationDetailModel(summary, row)
	if err != nil {
		return gen.UserOperationDetail{}, err
	}
	eventRows, err := tx.QueryContext(ctx, dbgen.ERC4337ListUserOperationEvents,
		r.chainID, r.userOperationDigest, row.summary.blockNumber, row.summary.blockHash,
		row.summary.transactionHash, row.summary.operationIndex,
	)
	if err != nil {
		return gen.UserOperationDetail{}, fmt.Errorf("query UserOperation events: %w", err)
	}
	detail.Events, err = scanUserOperationEvents(eventRows)
	if err != nil {
		return gen.UserOperationDetail{}, err
	}
	if err := tx.Commit(); err != nil {
		return gen.UserOperationDetail{}, fmt.Errorf("commit UserOperation detail: %w", err)
	}
	return detail, nil
}

func (r *PostgresReader) validateUserOperationRequest(limit int) error {
	if err := r.requireUserOperations(); err != nil {
		return err
	}
	if limit <= 0 || limit > 100 {
		return fmt.Errorf("%w: UserOperation limit is outside 1..100", publicquery.ErrInvalidInput)
	}
	return nil
}

func (r *PostgresReader) requireUserOperations() error {
	if r == nil || !r.userOperations || len(r.userOperationDigest) != 32 {
		return publicquery.NewCapabilityUnavailableError("user_operations", "unavailable", "not_configured")
	}
	return nil
}

func (r *PostgresReader) prepareUserOperationCursor(
	ctx context.Context,
	tx *sql.Tx,
	kind, address, transactionHash, encoded string,
) (userOperationCursor, error) {
	if encoded == "" {
		snapshot, err := r.currentUserOperationSnapshot(ctx, tx)
		if err != nil {
			return userOperationCursor{}, err
		}
		snapshot.Kind, snapshot.Address, snapshot.TransactionHash = kind, address, transactionHash
		return snapshot, nil
	}
	var cursor userOperationCursor
	if err := publicquery.DecodeCursor(encoded, &cursor); err != nil {
		return userOperationCursor{}, fmt.Errorf("%w: %v", publicquery.ErrInvalidCursor, err)
	}
	if cursor.Kind != kind || cursor.ChainID != r.chainID || cursor.ConfigurationDigest != hex.EncodeToString(r.userOperationDigest) ||
		cursor.IndexStart != r.userOperationStart || cursor.Address != address || cursor.TransactionHash != transactionHash ||
		cursor.SnapshotNumber < cursor.IndexStart {
		return userOperationCursor{}, publicquery.ErrInvalidCursor
	}
	snapshotHash, err := ethrpc.ParseHash(cursor.SnapshotHash)
	if err != nil {
		return userOperationCursor{}, publicquery.ErrInvalidCursor
	}
	var valid bool
	if err := tx.QueryRowContext(ctx, dbgen.ERC4337ValidateSnapshot,
		strconv.FormatUint(cursor.IndexStart, 10), strconv.FormatUint(cursor.SnapshotNumber, 10),
		snapshotHash[:], r.chainID, r.userOperationDigest,
	).Scan(&valid); err != nil {
		return userOperationCursor{}, fmt.Errorf("validate UserOperation snapshot: %w", err)
	}
	if !valid {
		return userOperationCursor{}, publicquery.ErrInvalidCursor
	}
	if kind != "transaction" {
		if cursor.BeforeBlockNumber > cursor.SnapshotNumber || cursor.BeforeTransactionIndex > math.MaxInt64 ||
			cursor.BeforeOperationIndex > math.MaxInt64 {
			return userOperationCursor{}, publicquery.ErrInvalidCursor
		}
		if _, err := ethrpc.ParseHash(cursor.BeforeUserOpHash); err != nil {
			return userOperationCursor{}, publicquery.ErrInvalidCursor
		}
	} else if cursor.AfterOperationIndex > math.MaxInt64 {
		return userOperationCursor{}, publicquery.ErrInvalidCursor
	}
	return cursor, nil
}

func (r *PostgresReader) currentUserOperationSnapshot(ctx context.Context, tx *sql.Tx) (userOperationCursor, error) {
	var number string
	var hash []byte
	if err := tx.QueryRowContext(ctx, dbgen.ERC4337CurrentSnapshot,
		strconv.FormatUint(r.userOperationStart, 10), r.chainID, r.userOperationDigest,
	).Scan(&number, &hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return userOperationCursor{}, publicquery.ErrNotReady
		}
		return userOperationCursor{}, fmt.Errorf("read UserOperation coverage snapshot: %w", err)
	}
	snapshotNumber, err := parseDecimalUint64(number)
	if err != nil || snapshotNumber < r.userOperationStart || len(hash) != common.HashLength {
		return userOperationCursor{}, errors.New("stored UserOperation coverage snapshot is invalid")
	}
	return userOperationCursor{
		ChainID: r.chainID, ConfigurationDigest: hex.EncodeToString(r.userOperationDigest),
		IndexStart: r.userOperationStart, SnapshotNumber: snapshotNumber,
		SnapshotHash: strings.ToLower(common.BytesToHash(hash).Hex()),
	}, nil
}

func scanUserOperationRows(rows *sql.Rows) ([]gen.UserOperationSummary, []userOperationCursor, error) {
	defer rows.Close() //nolint:errcheck
	var items []gen.UserOperationSummary
	var boundaries []userOperationCursor
	for rows.Next() {
		row := userOperationSummaryRow{}
		if err := rows.Scan(userOperationSummaryTargets(&row)...); err != nil {
			return nil, nil, fmt.Errorf("scan UserOperation summary: %w", err)
		}
		item, boundary, err := userOperationSummaryModel(row)
		if err != nil {
			return nil, nil, err
		}
		items, boundaries = append(items, item), append(boundaries, boundary)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate UserOperation summaries: %w", err)
	}
	return items, boundaries, nil
}

func userOperationSummaryTargets(row *userOperationSummaryRow) []any {
	return []any{
		&row.userOpHash, &row.entryPoint, &row.entryPointVersion, &row.sender,
		&row.nonce, &row.nonceKey, &row.nonceSequence, &row.success,
		&row.actualGasCost, &row.actualGasUsed, &row.transactionHash,
		&row.transactionIndex, &row.operationIndex, &row.eventLogIndex, &row.blockNumber,
		&row.blockHash, &row.blockTimestamp, &row.safeNumber, &row.finalizedNumber,
		&row.bundler, &row.beneficiary, &row.initKind, &row.factory,
		&row.paymaster, &row.aggregator, &row.participatingRoles,
	}
}

func userOperationSummaryModel(row userOperationSummaryRow) (gen.UserOperationSummary, userOperationCursor, error) {
	for name, value := range map[string][]byte{
		"userOpHash": row.userOpHash, "entryPoint": row.entryPoint, "sender": row.sender,
		"transactionHash": row.transactionHash, "blockHash": row.blockHash,
		"bundler": row.bundler, "beneficiary": row.beneficiary,
	} {
		expected := common.HashLength
		if name == "entryPoint" || name == "sender" || name == "bundler" || name == "beneficiary" {
			expected = common.AddressLength
		}
		if len(value) != expected {
			return gen.UserOperationSummary{}, userOperationCursor{}, fmt.Errorf("stored UserOperation %s has invalid length", name)
		}
	}
	quantities := []string{row.nonce, row.nonceKey, row.nonceSequence, row.actualGasCost, row.actualGasUsed}
	for _, quantity := range quantities {
		if !canonicalUint256(quantity) {
			return gen.UserOperationSummary{}, userOperationCursor{}, errors.New("stored UserOperation quantity is invalid")
		}
	}
	blockNumber, err := parseDecimalUint64(row.blockNumber)
	if err != nil || row.transactionIndex < 0 || row.operationIndex < 0 || row.eventLogIndex < 0 ||
		row.transactionIndex > math.MaxInt || row.operationIndex > math.MaxInt || row.eventLogIndex > math.MaxInt {
		return gen.UserOperationSummary{}, userOperationCursor{}, errors.New("stored UserOperation position is invalid")
	}
	timestamp, err := parseDecimalUint64(row.blockTimestamp)
	if err != nil || timestamp > math.MaxInt64 {
		return gen.UserOperationSummary{}, userOperationCursor{}, errors.New("stored UserOperation timestamp is invalid")
	}
	finality, err := classifyFinality(true, blockNumber,
		sql.NullString{String: row.safeNumber, Valid: row.safeNumber != ""},
		sql.NullString{String: row.finalizedNumber, Valid: row.finalizedNumber != ""},
	)
	if err != nil {
		return gen.UserOperationSummary{}, userOperationCursor{}, err
	}
	version := gen.UserOperationVersion(row.entryPointVersion)
	initKind := gen.UserOperationInitKind(row.initKind)
	if !version.Valid() || !initKind.Valid() {
		return gen.UserOperationSummary{}, userOperationCursor{}, errors.New("stored UserOperation version or init kind is invalid")
	}
	roles, err := decodeUserOperationRoles(row.participatingRoles)
	if err != nil {
		return gen.UserOperationSummary{}, userOperationCursor{}, err
	}
	model := gen.UserOperationSummary{
		Hash:       strings.ToLower(common.BytesToHash(row.userOpHash).Hex()),
		EntryPoint: common.BytesToAddress(row.entryPoint).Hex(), EntryPointVersion: version,
		Sender: common.BytesToAddress(row.sender).Hex(), Nonce: row.nonce,
		NonceKey: row.nonceKey, NonceSequence: row.nonceSequence,
		Success: row.success, ActualGasCost: row.actualGasCost, ActualGasUsed: row.actualGasUsed,
		TransactionHash:  strings.ToLower(common.BytesToHash(row.transactionHash).Hex()),
		TransactionIndex: int(row.transactionIndex), OperationIndex: int(row.operationIndex),
		EventLogIndex: int(row.eventLogIndex),
		BlockNumber:   row.blockNumber, BlockHash: strings.ToLower(common.BytesToHash(row.blockHash).Hex()),
		BlockTimestamp: time.Unix(int64(timestamp), 0).UTC(), Canonical: true, Finality: finality,
		Bundler: common.BytesToAddress(row.bundler).Hex(), Beneficiary: common.BytesToAddress(row.beneficiary).Hex(),
		InitKind: initKind,
	}
	model.Factory, err = optionalUserOperationAddress(row.factory)
	if err != nil {
		return gen.UserOperationSummary{}, userOperationCursor{}, err
	}
	model.Paymaster, err = optionalUserOperationAddress(row.paymaster)
	if err != nil {
		return gen.UserOperationSummary{}, userOperationCursor{}, err
	}
	model.Aggregator, err = optionalUserOperationAddress(row.aggregator)
	if err != nil {
		return gen.UserOperationSummary{}, userOperationCursor{}, err
	}
	if len(roles) > 0 {
		model.ParticipatingRoles = &roles
	}
	return model, userOperationCursor{
		BeforeBlockNumber: blockNumber, BeforeTransactionIndex: uint64(row.transactionIndex),
		BeforeOperationIndex: uint64(row.operationIndex), BeforeUserOpHash: model.Hash,
		AfterOperationIndex: uint64(row.operationIndex),
	}, nil
}

func userOperationPage(
	items []gen.UserOperationSummary,
	boundaries []userOperationCursor,
	cursor userOperationCursor,
	limit int,
) (publicquery.UserOperationPage, error) {
	page := publicquery.UserOperationPage{CoverageStart: cursor.IndexStart, CoverageEnd: cursor.SnapshotNumber}
	if len(items) > limit {
		items, boundaries = items[:limit], boundaries[:limit]
		next := cursor
		next.BeforeBlockNumber = boundaries[len(boundaries)-1].BeforeBlockNumber
		next.BeforeTransactionIndex = boundaries[len(boundaries)-1].BeforeTransactionIndex
		next.BeforeOperationIndex = boundaries[len(boundaries)-1].BeforeOperationIndex
		next.BeforeUserOpHash = boundaries[len(boundaries)-1].BeforeUserOpHash
		encoded, err := publicquery.EncodeCursor(next)
		if err != nil {
			return publicquery.UserOperationPage{}, err
		}
		page.NextCursor = encoded
	}
	page.Items = items
	return page, nil
}

func transactionUserOperationPage(
	items []gen.UserOperationSummary,
	boundaries []userOperationCursor,
	cursor userOperationCursor,
	limit int,
) (publicquery.UserOperationPage, error) {
	page := publicquery.UserOperationPage{CoverageStart: cursor.IndexStart, CoverageEnd: cursor.SnapshotNumber}
	if len(items) > limit {
		items, boundaries = items[:limit], boundaries[:limit]
		next := cursor
		next.AfterOperationIndex = boundaries[len(boundaries)-1].AfterOperationIndex
		encoded, err := publicquery.EncodeCursor(next)
		if err != nil {
			return publicquery.UserOperationPage{}, err
		}
		page.NextCursor = encoded
	}
	page.Items = items
	return page, nil
}

func userOperationDetailModel(summary gen.UserOperationSummary, row userOperationDetailRow) (gen.UserOperationDetail, error) {
	for _, quantity := range []string{
		row.callGasLimit, row.verificationGasLimit, row.preVerificationGas,
		row.maxFeePerGas, row.maxPriorityFeePerGas,
	} {
		if !canonicalUint256(quantity) {
			return gen.UserOperationDetail{}, errors.New("stored UserOperation request quantity is invalid")
		}
	}
	request := gen.UserOperationRequest{
		CallGasLimit: row.callGasLimit, VerificationGasLimit: row.verificationGasLimit,
		PreVerificationGas: row.preVerificationGas, MaxFeePerGas: row.maxFeePerGas,
		MaxPriorityFeePerGas: row.maxPriorityFeePerGas,
		InitCode:             hexutil.Encode(row.initCode), FactoryData: hexutil.Encode(row.factoryData),
		CallData: hexutil.Encode(row.callData), PaymasterAndData: hexutil.Encode(row.paymasterAndData),
		PaymasterData: hexutil.Encode(row.paymasterData), PaymasterSignature: hexutil.Encode(row.paymasterSignature),
		Signature: hexutil.Encode(row.signature), AggregatedSignature: hexutil.Encode(row.aggregatedSignature),
	}
	request.PaymasterVerificationGasLimit = optionalQuantity(row.paymasterVerificationGasLimit)
	request.PaymasterPostOpGasLimit = optionalQuantity(row.paymasterPostOpGasLimit)
	request.AccountGasLimits = optionalHash(row.accountGasLimits)
	request.GasFees = optionalHash(row.gasFees)
	return gen.UserOperationDetail{
		Hash: summary.Hash, EntryPoint: summary.EntryPoint, EntryPointVersion: summary.EntryPointVersion,
		Sender: summary.Sender, Nonce: summary.Nonce, NonceKey: summary.NonceKey,
		NonceSequence: summary.NonceSequence, Success: summary.Success,
		ActualGasCost: summary.ActualGasCost, ActualGasUsed: summary.ActualGasUsed,
		TransactionHash: summary.TransactionHash, TransactionIndex: summary.TransactionIndex,
		OperationIndex: summary.OperationIndex, EventLogIndex: summary.EventLogIndex,
		BlockNumber: summary.BlockNumber,
		BlockHash:   summary.BlockHash, BlockTimestamp: summary.BlockTimestamp,
		Canonical: summary.Canonical, Finality: summary.Finality,
		Bundler: summary.Bundler, Beneficiary: summary.Beneficiary,
		InitKind: summary.InitKind, Factory: summary.Factory, Paymaster: summary.Paymaster,
		Aggregator: summary.Aggregator, ParticipatingRoles: summary.ParticipatingRoles,
		Request: request, Events: []gen.UserOperationProtocolEvent{},
	}, nil
}

func scanUserOperationEvents(rows *sql.Rows) ([]gen.UserOperationProtocolEvent, error) {
	defer rows.Close() //nolint:errcheck
	events := make([]gen.UserOperationProtocolEvent, 0)
	for rows.Next() {
		row := userOperationEventRow{}
		if err := rows.Scan(
			&row.kind, &row.logIndex, &row.sender, &row.nonce, &row.relatedAddress,
			&row.paymaster, &row.rawData, &row.reason, &row.panicCode,
		); err != nil {
			return nil, fmt.Errorf("scan UserOperation event: %w", err)
		}
		kind := gen.UserOperationEventKind(row.kind)
		if !kind.Valid() || row.logIndex < 0 || row.logIndex > math.MaxInt || len(row.sender) != common.AddressLength {
			return nil, errors.New("stored UserOperation event identity is invalid")
		}
		relatedAddress, addressErr := optionalUserOperationAddress(row.relatedAddress)
		if addressErr != nil {
			return nil, addressErr
		}
		paymaster, addressErr := optionalUserOperationAddress(row.paymaster)
		if addressErr != nil {
			return nil, addressErr
		}
		event := gen.UserOperationProtocolEvent{
			Kind: kind, LogIndex: int(row.logIndex), Sender: common.BytesToAddress(row.sender).Hex(),
			RawData: hexutil.Encode(row.rawData), Reason: row.reason,
			RelatedAddress: relatedAddress, Paymaster: paymaster,
			Nonce: optionalQuantity(row.nonce), PanicCode: optionalQuantity(row.panicCode),
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate UserOperation events: %w", err)
	}
	return events, nil
}

func decodeUserOperationRoles(raw []byte) ([]gen.UserOperationRole, error) {
	var values []string
	if len(raw) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, errors.New("stored UserOperation participant roles are invalid")
	}
	roles := make([]gen.UserOperationRole, len(values))
	seen := make(map[gen.UserOperationRole]struct{}, len(values))
	for index, value := range values {
		role := gen.UserOperationRole(value)
		if !role.Valid() {
			return nil, errors.New("stored UserOperation participant role is unknown")
		}
		if _, exists := seen[role]; exists {
			return nil, errors.New("stored UserOperation participant role is duplicated")
		}
		seen[role], roles[index] = struct{}{}, role
	}
	return roles, nil
}

func canonicalUint256(value string) bool {
	integer, ok := new(big.Int).SetString(value, 10)
	return ok && integer.Sign() >= 0 && integer.BitLen() <= 256 && integer.String() == value
}

func optionalUserOperationAddress(value []byte) (*gen.Address, error) {
	if len(value) == 0 {
		return nil, nil
	}
	if len(value) != common.AddressLength {
		return nil, errors.New("stored optional UserOperation address is invalid")
	}
	address := gen.Address(common.BytesToAddress(value).Hex())
	return &address, nil
}

func optionalQuantity(value string) *gen.Quantity {
	if value == "" || !canonicalUint256(value) {
		return nil
	}
	quantity := gen.Quantity(value)
	return &quantity
}

func optionalHash(value []byte) *gen.Hash {
	if len(value) == 0 {
		return nil
	}
	if len(value) != common.HashLength {
		return nil
	}
	hash := gen.Hash(strings.ToLower(common.BytesToHash(value).Hex()))
	return &hash
}

func cursorBoundaryHash(value string) []byte {
	if value == "" {
		return make([]byte, common.HashLength)
	}
	hash, err := ethrpc.ParseHash(value)
	if err != nil {
		return make([]byte, common.HashLength)
	}
	return hash[:]
}
