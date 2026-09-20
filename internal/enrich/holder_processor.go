package enrich

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	dbgen "github.com/islishude/etherview/internal/db/gen"
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
	db   dbaccess.Database
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

func NewPostgresHolderProcessor(db dbaccess.Database, pool *ethrpc.Pool) (*PostgresHolderProcessor, error) {
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
		return runStageTransaction(ctx, processor.db, job, func(context.Context, pgx.Tx) (StageResult, error) {
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
	return runStageTransaction(ctx, processor.db, job, func(ctx context.Context, tx pgx.Tx) (StageResult, error) {
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
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(processor.db).HolderSourcePrerequisites(ctx, queryValue0, queryValue1, job.BlockHash[:])
		if err != nil {
			return err
		}
		configuredStart = queryRow.ConfigurationConfiguredStart
		canonical = queryRow.Canonical
		tokenComplete = queryRow.TokenComplete
		proxyTerminal = queryRow.ProxyTerminal
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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
	rows, err := func() ([]dbgen.HolderAffectedTokensRow, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return nil, err
		}
		return dbgen.New(processor.db).HolderAffectedTokens(ctx, queryValue0, queryValue1, job.BlockHash[:])
	}()
	if err != nil {
		return nil, false, fmt.Errorf("query holder affected tokens: %w", err)
	}
	var tokens []common.Address
	var fullReconciliations []bool
	for _, storedRow := range rows {
		var encoded []byte
		var full bool
		{
			encoded = storedRow.TokenAddress
			full = storedRow.FullReconciliation
		}
		if len(encoded) != common.AddressLength {

			return nil, false, Permanent(errors.New("holder token address has invalid length"))
		}
		tokens = append(tokens, common.BytesToAddress(encoded))
		fullReconciliations = append(fullReconciliations, full)
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
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(processor.db).HolderTokenIdentity(ctx, queryValue0, token[:], queryValue1)
		if err != nil {
			return err
		}
		standard = queryRow.Standard
		confidence = queryRow.Confidence
		return nil
	}()
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
		err := func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(job.ChainID); err != nil {
				return err
			}
			var queryValue1 pgtype.Numeric
			if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
				return err
			}
			queryRow, err := dbgen.New(processor.db).HolderPreviousSnapshot(ctx, queryValue0, token[:], queryValue1)
			if err != nil {
				return err
			}
			previousBlock = queryRow.SnapshotBlockNumber
			state = queryRow.State
			countText = queryRow.SnapshotHolderCount
			totalSupplyText = queryRow.SnapshotTotalSupply
			sumText = queryRow.SnapshotReconciledBalanceSum
			return nil
		}()
		if errors.Is(err, pgx.ErrNoRows) || err == nil && state != "complete" {
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
			if err := func() error {
				var queryValue0 pgtype.Numeric
				if err := queryValue0.Scan(job.ChainID); err != nil {
					return err
				}
				var queryValue1 pgtype.Numeric
				if err := queryValue1.Scan(previousBlock); err != nil {
					return err
				}
				var queryValue2 pgtype.Numeric
				if err := queryValue2.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
					return err
				}
				queryRow, err := dbgen.New(processor.db).HolderHasUnreconciledEvents(ctx, dbgen.HolderHasUnreconciledEventsParams{ChainID: queryValue0, TokenAddress: token[:], PreviousBlock: queryValue1, BlockNumber: queryValue2})
				if err != nil {
					return err
				}
				gap = queryRow
				return nil
			}(); err != nil {
				return holderTokenInput{}, fmt.Errorf("read holder reconciliation gap: %w", err)
			}
			full = gap
		}
	}
	if full {
		previousSum, previousCount = new(big.Int), 0
	}
	holders, err := processor.readHolderCandidates(ctx, job, token, full)
	if err != nil {
		return holderTokenInput{}, err
	}

	previousBalances := make([]*big.Int, len(holders))
	if !full {
		for index, holder := range holders {
			var balanceText string
			err := func() error {
				var queryValue0 pgtype.Numeric
				if err := queryValue0.Scan(job.ChainID); err != nil {
					return err
				}
				var queryValue1 pgtype.Numeric
				if err := queryValue1.Scan(previousBlock); err != nil {
					return err
				}
				queryRow, err := dbgen.New(processor.db).HolderPreviousBalance(ctx, dbgen.HolderPreviousBalanceParams{ChainID: queryValue0, TokenAddress: token[:], HolderAddress: holder[:], BlockNumber: queryValue1})
				if err != nil {
					return err
				}
				balanceText = queryRow
				return nil
			}()
			if errors.Is(err, pgx.ErrNoRows) {
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
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(processor.db).HolderEventSupply(ctx, queryValue0, token[:], queryValue1)
		if err != nil {
			return err
		}
		supplyText = queryRow
		return nil
	}(); err != nil {
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
	tx pgx.Tx,
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
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return err
		}
		return dbgen.New(tx).HolderDeleteBlockOutput(ctx, queryValue0, queryValue1, job.BlockHash[:])
	}(); err != nil {
		return StageResult{}, fmt.Errorf("delete holder replay output: %w", err)
	}
	available := 0
	for _, reconciliation := range reconciliations {
		if err := func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(job.ChainID); err != nil {
				return err
			}
			var queryValue1 pgtype.Numeric
			if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
				return err
			}
			var queryValue2 pgtype.Numeric
			if err := queryValue2.Scan(strconv.FormatUint(reconciliation.holderCount, 10)); err != nil {
				return err
			}
			var queryValue3 pgtype.Numeric
			if err := queryValue3.Scan(reconciliation.totalSupply.String()); err != nil {
				return err
			}
			var queryValue4 pgtype.Numeric
			if err := queryValue4.Scan(reconciliation.balanceSum.String()); err != nil {
				return err
			}
			return dbgen.New(tx).HolderInsertSnapshot(ctx, dbgen.HolderInsertSnapshotParams{ChainID: queryValue0, TokenAddress: reconciliation.token[:], BlockNumber: queryValue1, BlockHash: job.BlockHash[:], State: reconciliation.state, HolderCount: queryValue2, TotalSupply: queryValue3, ReconciledBalanceSum: queryValue4})
		}(); err != nil {
			return StageResult{}, fmt.Errorf("persist holder snapshot: %w", err)
		}
		for _, balance := range reconciliation.balances {
			if err := func() error {
				var queryValue0 pgtype.Numeric
				if err := queryValue0.Scan(job.ChainID); err != nil {
					return err
				}
				var queryValue1 pgtype.Numeric
				if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
					return err
				}
				var queryValue2 pgtype.Numeric
				if err := queryValue2.Scan(balance.balance.String()); err != nil {
					return err
				}
				return dbgen.New(tx).HolderInsertBalance(ctx, dbgen.HolderInsertBalanceParams{ChainID: queryValue0, TokenAddress: reconciliation.token[:], HolderAddress: balance.holder[:], BlockNumber: queryValue1, BlockHash: job.BlockHash[:], Balance: queryValue2})
			}(); err != nil {
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
