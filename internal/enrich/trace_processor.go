package enrich

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/islishude/etherview/internal/ethrpc"
)

var (
	TraceStage             = StageID{Name: "trace", Version: 1}
	errTraceRPCUnavailable = errors.New("trace RPC capability unavailable")
)

type TraceRPCProcessor struct {
	db     *sql.DB
	pool   *ethrpc.Pool
	limits TraceLimits
}

func NewTraceRPCProcessor(db *sql.DB, pool *ethrpc.Pool, limits TraceLimits) (*TraceRPCProcessor, error) {
	if db == nil || pool == nil {
		return nil, errors.New("trace processor requires a database and RPC pool")
	}
	if limits == (TraceLimits{}) {
		limits = DefaultTraceLimits()
	}
	if err := limits.validate(); err != nil {
		return nil, err
	}
	return &TraceRPCProcessor{db: db, pool: pool, limits: limits}, nil
}

func (*TraceRPCProcessor) Stage() StageID { return TraceStage }

func (processor *TraceRPCProcessor) ProcessLease(
	ctx context.Context,
	lease Lease,
	queue *PostgresJobQueue,
) (StageResult, error) {
	return processor.Process(ctx, bindStagePublication(lease.Job, lease, queue))
}

type traceTransaction struct {
	index uint64
	hash  Word
	from  Address
	to    *Address
	value string
	input []byte
	trace NormalizedTrace
}

// traceBlockBudget is shared by every RPC response processed for one block
// job, including work discarded before a same-endpoint adapter fallback. The
// per-transaction limits enforced by Normalize* remain independently active.
type traceBlockBudget struct {
	limits  TraceLimits
	payload int
	frames  int
	data    int
	text    int
}

func newTraceBlockBudget(limits TraceLimits) *traceBlockBudget {
	return &traceBlockBudget{limits: limits}
}

func (budget *traceBlockBudget) addPayload(size int) error {
	return budget.add(&budget.payload, size, budget.limits.MaxBlockPayloadBytes, "block payload bytes")
}

func (budget *traceBlockBudget) addTrace(trace NormalizedTrace) error {
	if err := budget.add(&budget.frames, len(trace.Frames), budget.limits.MaxBlockFrames, "block frame count"); err != nil {
		return err
	}
	for _, frame := range trace.Frames {
		if err := budget.add(&budget.data, len(frame.Input), budget.limits.MaxBlockDataBytes, "block input/output bytes"); err != nil {
			return err
		}
		if err := budget.add(&budget.data, len(frame.Output), budget.limits.MaxBlockDataBytes, "block input/output bytes"); err != nil {
			return err
		}
		if err := budget.add(&budget.text, len(frame.Error), budget.limits.MaxBlockTextBytes, "block error text bytes"); err != nil {
			return err
		}
		if err := budget.add(&budget.text, len(frame.RevertReason), budget.limits.MaxBlockTextBytes, "block error text bytes"); err != nil {
			return err
		}
	}
	return nil
}

func (*traceBlockBudget) add(used *int, size, limit int, name string) error {
	if size < 0 || *used > limit-size {
		return fmt.Errorf("%w: %s", ErrTraceLimit, name)
	}
	*used += size
	return nil
}

func (processor *TraceRPCProcessor) Process(ctx context.Context, job Job) (StageResult, error) {
	if processor == nil || processor.db == nil || processor.pool == nil {
		return StageResult{}, errors.New("process trace stage using unconfigured processor")
	}
	if err := job.Validate(); err != nil {
		return StageResult{}, Permanent(err)
	}
	if job.Stage != TraceStage {
		return StageResult{}, Permanent(fmt.Errorf("trace processor received stage %s", job.Stage))
	}
	transactions, canonical, err := processor.transactions(ctx, job)
	if err != nil {
		return StageResult{}, err
	}
	if !canonical {
		return processor.persist(ctx, job, nil, "", "stale_canonical_skipped")
	}
	if len(transactions) == 0 {
		return processor.persist(ctx, job, transactions, "none", "complete")
	}
	endpoint, err := processor.pool.Acquire(ethrpc.PurposeTrace)
	if err != nil {
		return StageResult{}, Unavailable(err)
	}
	source, err := processor.fetch(ctx, endpoint, job, transactions)
	if err != nil {
		processor.pool.ReportFailure(endpoint.Name)
		if traceCapabilityUnavailable(err) {
			return StageResult{}, Unavailable(err)
		}
		return StageResult{}, err
	}
	processor.pool.ReportSuccess(endpoint.Name)
	return processor.persist(ctx, job, transactions, source, "complete")
}

func (processor *TraceRPCProcessor) transactions(ctx context.Context, job Job) ([]traceTransaction, bool, error) {
	var canonical bool
	if err := processor.db.QueryRowContext(ctx, traceCanonicalSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
	).Scan(&canonical); err != nil {
		return nil, false, fmt.Errorf("check trace block canonicality: %w", err)
	}
	if !canonical {
		return nil, false, nil
	}
	rows, err := processor.db.QueryContext(ctx, traceTransactionsSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
	)
	if err != nil {
		return nil, false, fmt.Errorf("query trace transactions: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	var result []traceTransaction
	for rows.Next() {
		var index int64
		var hashBytes []byte
		var fromText, valueText, inputText string
		var toText sql.NullString
		if err := rows.Scan(&index, &hashBytes, &fromText, &toText, &valueText, &inputText); err != nil {
			return nil, false, fmt.Errorf("scan trace transaction: %w", err)
		}
		if index < 0 || len(hashBytes) != 32 {
			return nil, false, Permanent(errors.New("trace transaction identity is invalid"))
		}
		hash, err := WordFromBytes(hashBytes)
		if err != nil {
			return nil, false, Permanent(err)
		}
		from, err := ParseAddress(fromText)
		if err != nil {
			return nil, false, Permanent(fmt.Errorf("trace transaction from address: %w", err))
		}
		var to *Address
		if toText.Valid {
			address, err := ParseAddress(toText.String)
			if err != nil {
				return nil, false, Permanent(fmt.Errorf("trace transaction to address: %w", err))
			}
			to = addressPointer(address)
		}
		if err := validateTraceQuantity(valueText); err != nil {
			return nil, false, Permanent(fmt.Errorf("trace transaction value: %w", err))
		}
		input, err := optionalTraceData(inputText)
		if err != nil {
			return nil, false, Permanent(fmt.Errorf("trace transaction input: %w", err))
		}
		result = append(result, traceTransaction{
			index: uint64(index), hash: hash, from: from, to: to, value: valueText, input: input,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate trace transactions: %w", err)
	}
	return result, true, nil
}

func (processor *TraceRPCProcessor) fetch(ctx context.Context, endpoint *ethrpc.Endpoint, job Job, transactions []traceTransaction) (string, error) {
	debugStatus := endpoint.Capabilities.Status(ethrpc.CapabilityDebugTrace)
	parityStatus := endpoint.Capabilities.Status(ethrpc.CapabilityParityTrace)
	budget := newTraceBlockBudget(processor.limits)
	if debugStatus != ethrpc.AvailabilityUnavailable {
		err := processor.fetchCallTracer(ctx, endpoint.Client, transactions, budget)
		if err == nil {
			return string(TraceCallTracer), nil
		}
		if !traceAdapterFallback(err) || parityStatus == ethrpc.AvailabilityUnavailable {
			return "", err
		}
	}
	if parityStatus != ethrpc.AvailabilityUnavailable {
		if err := processor.fetchTraceAPI(ctx, endpoint.Client, job, transactions, budget); err != nil {
			return "", err
		}
		return string(TraceAPI), nil
	}
	return "", fmt.Errorf("%w: configured endpoint exposes neither debug nor trace module", errTraceRPCUnavailable)
}

func (processor *TraceRPCProcessor) fetchCallTracer(
	ctx context.Context,
	caller ethrpc.Caller,
	transactions []traceTransaction,
	budget *traceBlockBudget,
) error {
	for index := range transactions {
		var raw json.RawMessage
		err := caller.Call(ctx, "debug_traceTransaction", []any{
			transactions[index].hash.String(),
			map[string]any{"tracer": "callTracer", "tracerConfig": map[string]any{"onlyTopCall": false, "withLog": false}},
		}, &raw)
		if err != nil {
			return err
		}
		if err := budget.addPayload(len(raw)); err != nil {
			return Permanent(fmt.Errorf("account callTracer transaction %s: %w", transactions[index].hash, err))
		}
		trace, err := NormalizeCallTracer(raw, processor.limits)
		if err != nil {
			return Permanent(fmt.Errorf("normalize callTracer transaction %s: %w", transactions[index].hash, err))
		}
		if err := budget.addTrace(trace); err != nil {
			return Permanent(fmt.Errorf("account callTracer transaction %s: %w", transactions[index].hash, err))
		}
		if err := validateTransactionRoot(trace, transactions[index]); err != nil {
			return Permanent(fmt.Errorf("validate callTracer transaction %s: %w", transactions[index].hash, err))
		}
		transactions[index].trace = trace
	}
	return nil
}

func (processor *TraceRPCProcessor) fetchTraceAPI(
	ctx context.Context,
	caller ethrpc.Caller,
	job Job,
	transactions []traceTransaction,
	budget *traceBlockBudget,
) error {
	for index := range transactions {
		var raw json.RawMessage
		if err := caller.Call(ctx, "trace_transaction", []any{transactions[index].hash.String()}, &raw); err != nil {
			return err
		}
		if err := budget.addPayload(len(raw)); err != nil {
			return Permanent(fmt.Errorf("account trace_transaction %s: %w", transactions[index].hash, err))
		}
		trace, err := NormalizeTraceAPI(raw, processor.limits, TraceIdentity{
			BlockHash:        job.BlockHash,
			BlockNumber:      job.BlockNumber,
			TransactionHash:  transactions[index].hash,
			TransactionIndex: transactions[index].index,
		})
		if err != nil {
			return Permanent(fmt.Errorf("normalize trace_transaction %s: %w", transactions[index].hash, err))
		}
		if err := budget.addTrace(trace); err != nil {
			return Permanent(fmt.Errorf("account trace_transaction %s: %w", transactions[index].hash, err))
		}
		if err := validateTransactionRoot(trace, transactions[index]); err != nil {
			return Permanent(fmt.Errorf("validate trace_transaction %s: %w", transactions[index].hash, err))
		}
		transactions[index].trace = trace
	}
	return nil
}

func validateTransactionRoot(trace NormalizedTrace, transaction traceTransaction) error {
	if trace.State != TraceComplete || len(trace.Frames) == 0 {
		return errors.New("normalized transaction trace has no root frame")
	}
	root := trace.Frames[0]
	if root.Index != 0 || root.ParentIndex != -1 || len(root.TraceAddress) != 0 {
		return errors.New("normalized transaction trace root identity is invalid")
	}
	if root.From == nil || *root.From != transaction.from {
		return errors.New("trace root sender does not match canonical transaction")
	}
	if transaction.to == nil {
		if root.Type != "CREATE" {
			return errors.New("trace root type does not match contract-creation transaction")
		}
	} else {
		if root.Type != "CALL" || root.To == nil || *root.To != *transaction.to {
			return errors.New("trace root target does not match canonical transaction")
		}
	}
	rootValue, err := traceDecimal(root.Value)
	if err != nil {
		return fmt.Errorf("trace root value: %w", err)
	}
	transactionValue, err := traceDecimal(transaction.value)
	if err != nil {
		return fmt.Errorf("canonical transaction value: %w", err)
	}
	if rootValue == nil || transactionValue == nil || rootValue != transactionValue {
		return errors.New("trace root value does not match canonical transaction")
	}
	if !bytes.Equal(root.Input, transaction.input) {
		return errors.New("trace root input does not match canonical transaction")
	}
	return nil
}

func (processor *TraceRPCProcessor) persist(ctx context.Context, job Job, transactions []traceTransaction, source, outcome string) (StageResult, error) {
	return runStageTransaction(ctx, processor.db, job, func(ctx context.Context, tx *sql.Tx) (StageResult, error) {
		return processor.persistTx(ctx, tx, job, transactions, source, outcome)
	})
}

func (processor *TraceRPCProcessor) persistTx(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	transactions []traceTransaction,
	source string,
	outcome string,
) (StageResult, error) {
	canonical, err := lockCanonicalBlock(ctx, tx, job)
	if err != nil {
		return StageResult{}, err
	}
	if !canonical {
		outcome = "stale_canonical_skipped"
		transactions = nil
	}
	if canonical {
		if _, err := tx.ExecContext(ctx, deleteTraceBlockSQL,
			job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		); err != nil {
			return StageResult{}, fmt.Errorf("clear previous normalized trace: %w", err)
		}
	}
	frames := 0
	for _, transaction := range transactions {
		for _, frame := range transaction.trace.Frames {
			if err := persistTraceFrame(ctx, tx, job, transaction, frame); err != nil {
				return StageResult{}, err
			}
			frames++
		}
	}
	details := map[string]string{
		"outcome": outcome, "source": source,
		"transactions": strconv.Itoa(len(transactions)), "frames": strconv.Itoa(frames),
	}
	// Trace is optional and never blocks the first proxy/ABI pass. If normalized
	// CREATE/CREATE2 targets arrive later, reset only terminal downstream jobs
	// in this transaction. Removing proxy@1's result keeps abi@1 dependency-
	// blocked until the replay has incorporated those targets.
	if canonical {
		proxyRequeued, err := resetTerminalDependentStageTx(ctx, tx, job, ProxyStage)
		if err != nil {
			return StageResult{}, err
		}
		details["proxy_requeued"] = strconv.FormatBool(proxyRequeued)
		if proxyRequeued {
			abiRequeued, err := resetTerminalDependentStageTx(ctx, tx, job, ABIStage)
			if err != nil {
				return StageResult{}, err
			}
			details["abi_requeued"] = strconv.FormatBool(abiRequeued)
		}
	}
	return StageResult{State: ResultComplete, Details: details}, nil
}

func persistTraceFrame(ctx context.Context, tx *sql.Tx, job Job, transaction traceTransaction, frame CallFrame) error {
	if frame.Index < 0 || frame.ParentIndex >= frame.Index || frame.Type == "" || len(frame.Type) > 32 {
		return Permanent(errors.New("normalized trace frame identity is invalid"))
	}
	tracePath := tracePathKey(frame.TraceAddress)
	var parentPath any
	if len(frame.TraceAddress) > 0 {
		parentPath = tracePathKey(frame.TraceAddress[:len(frame.TraceAddress)-1])
	}
	var from, to, created any
	if frame.From != nil {
		from = frame.From[:]
	}
	if frame.To != nil {
		if frame.Type == "CREATE" || frame.Type == "CREATE2" {
			created = frame.To[:]
		} else {
			to = frame.To[:]
		}
	}
	value, err := traceDecimal(frame.Value)
	if err != nil {
		return Permanent(fmt.Errorf("trace value: %w", err))
	}
	gas, err := traceDecimal(frame.Gas)
	if err != nil {
		return Permanent(fmt.Errorf("trace gas: %w", err))
	}
	gasUsed, err := traceDecimal(frame.GasUsed)
	if err != nil {
		return Permanent(fmt.Errorf("trace gas used: %w", err))
	}
	var traceError any
	if frame.RevertReason != "" {
		traceError = frame.RevertReason
	} else if frame.Error != "" {
		traceError = frame.Error
	}
	_, err = tx.ExecContext(ctx, insertTraceFrameSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		transaction.hash[:], transaction.index, tracePath, parentPath, len(frame.TraceAddress),
		frame.Type, from, to, created, value, gas, gasUsed, nullableBytes(frame.Input),
		nullableBytes(frame.Output), traceError, frame.Reverted,
	)
	if err != nil {
		return fmt.Errorf("persist normalized trace frame: %w", err)
	}
	return nil
}

func traceDecimal(value string) (any, error) {
	if value == "" {
		return nil, nil
	}
	quantity, err := ethrpc.ParseQuantity(value)
	if err != nil {
		return nil, err
	}
	integer, err := quantity.Big()
	if err != nil {
		return nil, err
	}
	return integer.String(), nil
}

func nullableBytes(value []byte) any {
	if value == nil {
		return nil
	}
	return value
}

func traceCapabilityUnavailable(err error) bool {
	if errors.Is(err, errTraceRPCUnavailable) || ethrpc.IsMethodNotFound(err) {
		return true
	}
	var rpcError *ethrpc.RPCError
	if !errors.As(err, &rpcError) {
		return false
	}
	message := strings.ToLower(rpcError.Message)
	return strings.Contains(message, "pruned") || strings.Contains(message, "historical state") ||
		strings.Contains(message, "missing trie")
}

func traceAdapterFallback(err error) bool {
	return ethrpc.IsMethodNotFound(err) || traceCapabilityUnavailable(err)
}

const traceCanonicalSQL = `
SELECT EXISTS (
    SELECT 1 FROM canonical_blocks
    WHERE chain_id = $1::numeric AND number = $2::numeric AND block_hash = $3
)`

const traceTransactionsSQL = `
SELECT tx_index, tx_hash,
       raw->>'from', raw->>'to', raw->>'value', raw->>'input'
FROM transaction_inclusions
WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3
ORDER BY tx_index`

const deleteTraceBlockSQL = `
DELETE FROM normalized_traces
WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3`

const insertTraceFrameSQL = `
INSERT INTO normalized_traces (
    chain_id, block_number, block_hash, transaction_hash, transaction_index,
    trace_path, parent_path, depth, call_type, from_address, to_address,
    created_address, value, gas, gas_used, input, output, error, reverted, canonical
) VALUES (
    $1::numeric, $2::numeric, $3, $4, $5, $6, $7, $8, $9, $10, $11,
    $12, $13::numeric, $14::numeric, $15::numeric, $16, $17, $18, $19, true
)`
