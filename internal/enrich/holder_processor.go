package enrich

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/stagecontract"
)

var HolderStage = stagecontract.Holder

const holderRPCBatchSize = 200

var (
	erc20BalanceOfSelector   = hexutil.Bytes{0x70, 0xa0, 0x82, 0x31}
	erc20TotalSupplySelector = hexutil.Bytes{0x18, 0x16, 0x0d, 0xdd}
)

type PostgresHolderProcessor struct {
	db   *sql.DB
	pool *ethrpc.Pool
}

type holderTokenInput struct {
	token            common.Address
	holders          []common.Address
	previousBalances []*big.Int
	previousSum      *big.Int
	previousCount    uint64
	full             bool
	eventSupply      *big.Int
	eventSupplyValid bool
}

type holderBalance struct {
	holder  common.Address
	balance *big.Int
}

type holderTokenReconciliation struct {
	token       common.Address
	balances    []holderBalance
	totalSupply *big.Int
	balanceSum  *big.Int
	holderCount uint64
	state       string
}

func NewPostgresHolderProcessor(db *sql.DB, pool *ethrpc.Pool) (*PostgresHolderProcessor, error) {
	if db == nil || pool == nil {
		return nil, errors.New("holder processor requires PostgreSQL and an RPC pool")
	}
	return &PostgresHolderProcessor{db: db, pool: pool}, nil
}

func (*PostgresHolderProcessor) Stage() StageID { return HolderStage }

func (processor *PostgresHolderProcessor) ProcessLease(
	ctx context.Context,
	lease Lease,
	queue *PostgresJobQueue,
) (StageResult, error) {
	return processor.Process(ctx, bindStagePublication(lease.Job, lease, queue))
}

func (processor *PostgresHolderProcessor) Process(ctx context.Context, job Job) (StageResult, error) {
	if processor == nil || processor.db == nil || processor.pool == nil {
		return StageResult{}, errors.New("process holder stage using incomplete processor")
	}
	if err := job.Validate(); err != nil {
		return StageResult{}, Permanent(err)
	}
	if job.Stage != HolderStage {
		return StageResult{}, Permanent(fmt.Errorf("holder processor received stage %s", job.Stage))
	}
	inputs, stale, err := processor.readInputs(ctx, job)
	if err != nil {
		return StageResult{}, err
	}
	if stale {
		return runStageTransaction(ctx, processor.db, job, func(context.Context, *sql.Tx) (StageResult, error) {
			return StageResult{State: ResultComplete, Details: map[string]string{"outcome": "stale_canonical_skipped"}}, nil
		})
	}
	reconciliations := make([]holderTokenReconciliation, 0, len(inputs))
	if len(inputs) > 0 {
		endpoint, acquireErr := processor.pool.Acquire(ethrpc.PurposeState)
		if acquireErr != nil {
			return StageResult{}, Unavailable(errors.New("holder exact-state RPC is unavailable"))
		}
		for _, input := range inputs {
			reconciliation, reconcileErr := reconcileHolderToken(ctx, endpoint, job, input)
			if reconcileErr != nil {
				if _, tokenUnavailable := errors.AsType[holderTokenUnavailableError](reconcileErr); tokenUnavailable {
					reconciliations = append(reconciliations, holderTokenReconciliation{
						token: input.token, balances: []holderBalance{}, totalSupply: new(big.Int),
						balanceSum: new(big.Int), state: "unavailable",
					})
					continue
				}
				processor.pool.ReportFailure(endpoint.Name)
				return StageResult{}, reconcileErr
			}
			reconciliations = append(reconciliations, reconciliation)
		}
		processor.pool.ReportSuccess(endpoint.Name)
	}
	return runStageTransaction(ctx, processor.db, job, func(ctx context.Context, tx *sql.Tx) (StageResult, error) {
		return persistHolderReconciliations(ctx, tx, job, reconciliations)
	})
}

type holderTokenUnavailableError struct{ reason string }

func (err holderTokenUnavailableError) Error() string { return err.reason }

func holderCallError(ctx context.Context, err error) error {
	if rpcError, ok := errors.AsType[rpc.Error](err); ok {
		message := strings.ToLower(rpcError.Error())
		if strings.Contains(message, "revert") || strings.Contains(message, "invalid opcode") {
			return holderTokenUnavailableError{reason: "ERC-20 holder state call is unavailable"}
		}
	}
	return exactStateRPCError(ctx, "eth_call", err)
}

func (processor *PostgresHolderProcessor) readInputs(
	ctx context.Context,
	job Job,
) ([]holderTokenInput, bool, error) {
	var configuredStart string
	var canonical, tokenComplete, proxyTerminal bool
	err := processor.db.QueryRowContext(
		ctx, dbgen.HolderSourcePrerequisites, job.ChainID,
		strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
	).Scan(&configuredStart, &canonical, &tokenComplete, &proxyTerminal)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, Permanent(errors.New("holder chain configuration is missing"))
	}
	if err != nil {
		return nil, false, fmt.Errorf("read holder prerequisites: %w", err)
	}
	if !canonical {
		return nil, true, nil
	}
	if configuredStart != "0" {
		return nil, false, Unavailable(errors.New("holder coverage does not start at genesis"))
	}
	if !tokenComplete || !proxyTerminal {
		return nil, false, errors.New("holder stage dependencies are not terminal")
	}
	rows, err := processor.db.QueryContext(
		ctx, dbgen.HolderAffectedTokens, job.ChainID,
		strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
	)
	if err != nil {
		return nil, false, fmt.Errorf("query holder affected tokens: %w", err)
	}
	var tokens []common.Address
	var fullReconciliations []bool
	for rows.Next() {
		var encoded []byte
		var full bool
		if err := rows.Scan(&encoded, &full); err != nil {
			_ = rows.Close()
			return nil, false, fmt.Errorf("scan holder affected token: %w", err)
		}
		if len(encoded) != common.AddressLength {
			_ = rows.Close()
			return nil, false, Permanent(errors.New("holder token address has invalid length"))
		}
		tokens = append(tokens, common.BytesToAddress(encoded))
		fullReconciliations = append(fullReconciliations, full)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate holder affected tokens: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, false, fmt.Errorf("close holder affected tokens: %w", err)
	}
	inputs := make([]holderTokenInput, 0, len(tokens))
	for index, token := range tokens {
		input, err := processor.readTokenInput(ctx, job, token, fullReconciliations[index])
		if errors.Is(err, errHolderTokenNotApplicable) {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		inputs = append(inputs, input)
	}
	return inputs, false, nil
}

var errHolderTokenNotApplicable = errors.New("holder token is not an authoritative ERC-20")

func (processor *PostgresHolderProcessor) readTokenInput(
	ctx context.Context,
	job Job,
	token common.Address,
	full bool,
) (holderTokenInput, error) {
	var standard, confidence string
	err := processor.db.QueryRowContext(
		ctx, dbgen.HolderTokenIdentity, job.ChainID, token[:], strconv.FormatUint(job.BlockNumber, 10),
	).Scan(&standard, &confidence)
	if err != nil {
		return holderTokenInput{}, fmt.Errorf("read holder token identity: %w", err)
	}
	if standard != string(TokenERC20) || confidence != string(ConfidenceHigh) && confidence != string(ConfidenceVerified) {
		return holderTokenInput{}, errHolderTokenNotApplicable
	}
	previousSum, previousCount := new(big.Int), uint64(0)
	previousBlock := "0"
	if !full {
		var state, countText, totalSupplyText, sumText string
		err := processor.db.QueryRowContext(
			ctx, dbgen.HolderPreviousSnapshot, job.ChainID, token[:], strconv.FormatUint(job.BlockNumber, 10),
		).Scan(&previousBlock, &state, &countText, &totalSupplyText, &sumText)
		if errors.Is(err, sql.ErrNoRows) || err == nil && state != "complete" {
			full = true
		} else if err != nil {
			return holderTokenInput{}, fmt.Errorf("read previous holder snapshot: %w", err)
		} else {
			count, parseErr := strconv.ParseUint(countText, 10, 64)
			sum, sumOK := new(big.Int).SetString(sumText, 10)
			if parseErr != nil || !sumOK || sum.Sign() < 0 || sum.BitLen() > 256 ||
				totalSupplyText != sumText {
				return holderTokenInput{}, Permanent(errors.New("previous holder snapshot is invalid"))
			}
			previousCount, previousSum = count, sum
			var gap bool
			if err := processor.db.QueryRowContext(
				ctx, dbgen.HolderHasUnreconciledEvents, job.ChainID, token[:],
				previousBlock, strconv.FormatUint(job.BlockNumber, 10),
			).Scan(&gap); err != nil {
				return holderTokenInput{}, fmt.Errorf("read holder reconciliation gap: %w", err)
			}
			full = gap
		}
	}
	query, arguments := dbgen.HolderTouchedCandidates, []any{
		job.ChainID, token[:], strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
	}
	if full {
		query = dbgen.HolderCandidates
		arguments = []any{job.ChainID, token[:], strconv.FormatUint(job.BlockNumber, 10)}
		previousSum, previousCount = new(big.Int), 0
	}
	rows, err := processor.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return holderTokenInput{}, fmt.Errorf("query holder candidates: %w", err)
	}
	holders := make([]common.Address, 0)
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			_ = rows.Close()
			return holderTokenInput{}, fmt.Errorf("scan holder candidate: %w", err)
		}
		if len(encoded) != common.AddressLength {
			_ = rows.Close()
			return holderTokenInput{}, Permanent(errors.New("holder candidate address has invalid length"))
		}
		holder := common.BytesToAddress(encoded)
		if holder == (common.Address{}) {
			_ = rows.Close()
			return holderTokenInput{}, Permanent(errors.New("holder candidate address is zero"))
		}
		holders = append(holders, holder)
	}
	if err := rows.Err(); err != nil {
		return holderTokenInput{}, fmt.Errorf("iterate holder candidates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return holderTokenInput{}, fmt.Errorf("close holder candidates: %w", err)
	}
	previousBalances := make([]*big.Int, len(holders))
	if !full {
		for index, holder := range holders {
			var balanceText string
			err := processor.db.QueryRowContext(
				ctx, dbgen.HolderPreviousBalance, job.ChainID, token[:], holder[:], previousBlock,
			).Scan(&balanceText)
			if errors.Is(err, sql.ErrNoRows) {
				previousBalances[index] = new(big.Int)
				continue
			}
			if err != nil {
				return holderTokenInput{}, fmt.Errorf("read previous holder balance: %w", err)
			}
			balance, ok := new(big.Int).SetString(balanceText, 10)
			if !ok || balance.Sign() < 0 || balance.BitLen() > 256 || balance.String() != balanceText {
				return holderTokenInput{}, Permanent(errors.New("previous holder balance is invalid"))
			}
			previousBalances[index] = balance
		}
	}
	var supplyText string
	if err := processor.db.QueryRowContext(
		ctx, dbgen.HolderEventSupply, job.ChainID, token[:], strconv.FormatUint(job.BlockNumber, 10),
	).Scan(&supplyText); err != nil {
		return holderTokenInput{}, fmt.Errorf("read holder event supply: %w", err)
	}
	eventSupply, ok := new(big.Int).SetString(supplyText, 10)
	eventSupplyValid := ok && eventSupply.Sign() >= 0 && eventSupply.BitLen() <= 256 && eventSupply.String() == supplyText
	if !eventSupplyValid {
		eventSupply = new(big.Int)
	}
	return holderTokenInput{
		token: token, holders: holders, previousBalances: previousBalances,
		previousSum: previousSum, previousCount: previousCount, full: full,
		eventSupply:      eventSupply,
		eventSupplyValid: eventSupplyValid,
	}, nil
}

func reconcileHolderToken(
	ctx context.Context,
	endpoint *ethrpc.Endpoint,
	job Job,
	input holderTokenInput,
) (holderTokenReconciliation, error) {
	block := rpc.BlockNumberOrHashWithHash(job.BlockHash, true)
	totalSupply, err := callHolderUint256(ctx, endpoint, input.token, erc20TotalSupplySelector, block)
	if err != nil {
		return holderTokenReconciliation{}, err
	}
	balances := make([]holderBalance, 0, len(input.holders))
	sum := new(big.Int)
	if input.previousSum != nil {
		sum.Set(input.previousSum)
	}
	count := input.previousCount
	for start := 0; start < len(input.holders); start += holderRPCBatchSize {
		end := min(start+holderRPCBatchSize, len(input.holders))
		batch, err := callHolderBalanceBatch(ctx, endpoint, input.token, input.holders[start:end], block)
		if err != nil {
			return holderTokenReconciliation{}, err
		}
		for _, balance := range batch {
			balances = append(balances, balance)
			previous := new(big.Int)
			if !input.full {
				previous = input.previousBalances[len(balances)-1]
				sum.Sub(sum, previous)
				if previous.Sign() > 0 {
					if count == 0 {
						return holderTokenReconciliation{}, Permanent(errors.New("previous holder count underflow"))
					}
					count--
				}
			}
			sum.Add(sum, balance.balance)
			if balance.balance.Sign() > 0 {
				count++
			}
		}
	}
	state := "complete"
	if !input.eventSupplyValid || sum.Cmp(totalSupply) != 0 || input.eventSupply.Cmp(totalSupply) != 0 {
		state = "unavailable"
	}
	return holderTokenReconciliation{
		token: input.token, balances: balances, totalSupply: totalSupply,
		balanceSum: sum, holderCount: count, state: state,
	}, nil
}

func callHolderUint256(
	ctx context.Context,
	endpoint *ethrpc.Endpoint,
	token common.Address,
	data hexutil.Bytes,
	block rpc.BlockNumberOrHash,
) (*big.Int, error) {
	var result hexutil.Bytes
	if err := endpoint.CallContext(ctx, &result, "eth_call", map[string]any{"to": token, "data": data}, block); err != nil {
		return nil, holderCallError(ctx, err)
	}
	if len(result) != 32 {
		return nil, holderTokenUnavailableError{reason: "ERC-20 holder state call returned malformed uint256"}
	}
	return new(big.Int).SetBytes(result), nil
}

func callHolderBalanceBatch(
	ctx context.Context,
	endpoint *ethrpc.Endpoint,
	token common.Address,
	holders []common.Address,
	block rpc.BlockNumberOrHash,
) ([]holderBalance, error) {
	results := make([]hexutil.Bytes, len(holders))
	elements := make([]rpc.BatchElem, len(holders))
	for index, holder := range holders {
		data := make(hexutil.Bytes, 36)
		copy(data, erc20BalanceOfSelector)
		copy(data[16:], holder[:])
		elements[index] = rpc.BatchElem{
			Method: "eth_call",
			Args:   []any{map[string]any{"to": token, "data": data}, block},
			Result: &results[index],
		}
	}
	if err := endpoint.BatchCallContext(ctx, elements); err != nil {
		return nil, holderCallError(ctx, err)
	}
	balances := make([]holderBalance, len(holders))
	for index := range elements {
		if elements[index].Error != nil {
			return nil, holderCallError(ctx, elements[index].Error)
		}
		if len(results[index]) != 32 {
			return nil, holderTokenUnavailableError{reason: "ERC-20 balanceOf returned malformed uint256"}
		}
		balances[index] = holderBalance{holder: holders[index], balance: new(big.Int).SetBytes(results[index])}
	}
	return balances, nil
}

func persistHolderReconciliations(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	reconciliations []holderTokenReconciliation,
) (StageResult, error) {
	canonical, err := lockCanonicalBlock(ctx, tx, job)
	if err != nil {
		return StageResult{}, err
	}
	if !canonical {
		return StageResult{State: ResultComplete, Details: map[string]string{"outcome": "stale_canonical_skipped"}}, nil
	}
	if _, err := tx.ExecContext(
		ctx, dbgen.HolderDeleteBlockOutput, job.ChainID,
		strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
	); err != nil {
		return StageResult{}, fmt.Errorf("delete holder replay output: %w", err)
	}
	available := 0
	for _, reconciliation := range reconciliations {
		if _, err := tx.ExecContext(
			ctx, dbgen.HolderInsertSnapshot, job.ChainID, reconciliation.token[:],
			strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:], reconciliation.state,
			strconv.FormatUint(reconciliation.holderCount, 10), reconciliation.totalSupply.String(),
			reconciliation.balanceSum.String(),
		); err != nil {
			return StageResult{}, fmt.Errorf("persist holder snapshot: %w", err)
		}
		for _, balance := range reconciliation.balances {
			if _, err := tx.ExecContext(
				ctx, dbgen.HolderInsertBalance, job.ChainID, reconciliation.token[:], balance.holder[:],
				strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:], balance.balance.String(),
			); err != nil {
				return StageResult{}, fmt.Errorf("persist holder balance: %w", err)
			}
		}
		if reconciliation.state == "complete" {
			available++
		}
	}
	return StageResult{State: ResultComplete, Details: map[string]string{
		"tokens": strconv.Itoa(len(reconciliations)), "available_tokens": strconv.Itoa(available),
	}}, nil
}
