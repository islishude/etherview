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

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/ethrpc"
)

var (
	TraceStage             = StageID{Name: "trace", Version: 3}
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
	index      uint64
	hash       common.Hash
	from       common.Address
	to         *common.Address
	value      string
	input      []byte
	trace      NormalizedTrace
	executions map[common.Address]executionCodeResolution
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
	logs    int
	logData int
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
		if err := budget.add(&budget.logs, len(frame.Logs), budget.limits.MaxBlockLogs, "block log count"); err != nil {
			return err
		}
		for _, log := range frame.Logs {
			if err := budget.add(&budget.logData, len(log.Data), budget.limits.MaxBlockLogDataBytes, "block log data bytes"); err != nil {
				return err
			}
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
		var to *common.Address
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
	resolutionRows, err := processor.db.QueryContext(ctx, traceExecutionResolutionsSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		StateDiffStage.Name, StateDiffStage.Version,
	)
	if err != nil {
		return nil, false, fmt.Errorf("query trace execution-code resolutions: %w", err)
	}
	defer resolutionRows.Close() //nolint:errcheck
	byTransaction := make(map[common.Hash]map[common.Address]executionCodeResolution)
	for resolutionRows.Next() {
		var transactionHash, contextAddress, executionAddress, codeHash []byte
		var resolution, evidenceSource string
		if err := resolutionRows.Scan(
			&transactionHash, &contextAddress, &executionAddress, &codeHash,
			&resolution, &evidenceSource,
		); err != nil {
			return nil, false, fmt.Errorf("scan trace execution-code resolution: %w", err)
		}
		if len(transactionHash) != common.HashLength || len(contextAddress) != common.AddressLength ||
			(len(executionAddress) != 0 && len(executionAddress) != common.AddressLength) ||
			(len(codeHash) != 0 && len(codeHash) != common.HashLength) {
			return nil, false, Permanent(errors.New("trace execution-code resolution identity is invalid"))
		}
		transaction := common.BytesToHash(transactionHash)
		contextAddressValue := common.BytesToAddress(contextAddress)
		item := executionCodeResolution{
			context: contextAddressValue, resolution: resolution, evidenceSource: evidenceSource,
		}
		if len(executionAddress) != 0 {
			value := common.BytesToAddress(executionAddress)
			item.execution = &value
		}
		if len(codeHash) != 0 {
			value := common.BytesToHash(codeHash)
			item.codeHash = &value
		}
		if byTransaction[transaction] == nil {
			byTransaction[transaction] = make(map[common.Address]executionCodeResolution)
		}
		byTransaction[transaction][contextAddressValue] = item
	}
	if err := resolutionRows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate trace execution-code resolutions: %w", err)
	}
	for index := range result {
		result[index].executions = byTransaction[result[index].hash]
	}
	return result, true, nil
}

func (processor *TraceRPCProcessor) fetch(ctx context.Context, endpoint *ethrpc.Endpoint, job Job, transactions []traceTransaction) (string, error) {
	debugStatus := endpoint.Capabilities.Status(ethrpc.CapabilityDebugTrace)
	parityStatus := endpoint.Capabilities.Status(ethrpc.CapabilityParityTrace)
	budget := newTraceBlockBudget(processor.limits)
	if debugStatus != ethrpc.AvailabilityUnavailable {
		err := processor.fetchCallTracer(ctx, endpoint.Client, job.BlockHash, transactions, budget)
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
	caller rpcCaller,
	blockHash common.Hash,
	transactions []traceTransaction,
	budget *traceBlockBudget,
) error {
	var raw json.RawMessage
	err := caller.CallContext(ctx, &raw, debugTraceBlockByHashMethod, blockHash, callTracerOptions(true))
	if err != nil && traceLogConfigUnsupported(err) {
		raw = nil
		err = caller.CallContext(ctx, &raw, debugTraceBlockByHashMethod, blockHash, callTracerOptions(false))
	}
	if err != nil {
		return sanitizeTraceRPCError(err)
	}
	if err := budget.addPayload(len(raw)); err != nil {
		return Permanent(fmt.Errorf("account callTracer block response: %w", err))
	}
	expected := make([]common.Hash, len(transactions))
	for index := range transactions {
		expected[index] = transactions[index].hash
	}
	results, err := decodeBlockTraceResults(raw, expected)
	if err != nil {
		return Permanent(fmt.Errorf("decode callTracer block response: %w", err))
	}
	for index := range transactions {
		if results[index].err != nil {
			return results[index].err
		}
		trace, err := NormalizeCallTracer(results[index].result, processor.limits)
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
		applyTraceExecutionResolutions(&transactions[index])
	}
	return nil
}

func callTracerOptions(withLog bool) map[string]any {
	return map[string]any{
		"tracer":       "callTracer",
		"tracerConfig": map[string]any{"onlyTopCall": false, "withLog": withLog},
	}
}

func traceLogConfigUnsupported(err error) bool {
	var rpcError rpc.Error
	return errors.As(err, &rpcError) && rpcError.ErrorCode() == -32602
}

func (processor *TraceRPCProcessor) fetchTraceAPI(
	ctx context.Context,
	caller rpcCaller,
	job Job,
	transactions []traceTransaction,
	budget *traceBlockBudget,
) error {
	for index := range transactions {
		var raw json.RawMessage
		if err := caller.CallContext(ctx, &raw, "trace_transaction", transactions[index].hash.String()); err != nil {
			return sanitizeTraceRPCError(err)
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
		applyTraceExecutionResolutions(&transactions[index])
	}
	return nil
}

func applyTraceExecutionResolutions(transaction *traceTransaction) {
	if transaction == nil {
		return
	}
	for index := range transaction.trace.Frames {
		frame := &transaction.trace.Frames[index]
		if frame.Type == "CREATE" || frame.Type == "CREATE2" {
			frame.ExecutionResolution = "not_applicable"
			continue
		}
		frame.ExecutionResolution = "unavailable"
		if frame.CodeAddress == nil {
			continue
		}
		resolution, exists := transaction.executions[*frame.CodeAddress]
		if !exists {
			continue
		}
		frame.ExecutionResolution = resolution.resolution
		if resolution.execution != nil {
			value := *resolution.execution
			frame.ExecutionAddress = &value
		}
		if resolution.codeHash != nil {
			value := *resolution.codeHash
			frame.ExecutionCodeHash = &value
		}
	}
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
		if _, err := tx.ExecContext(ctx, deleteTraceLogAttributionsSQL,
			job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		); err != nil {
			return StageResult{}, fmt.Errorf("clear previous trace log attribution: %w", err)
		}
		if _, err := tx.ExecContext(ctx, deleteTraceBlockSQL,
			job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		); err != nil {
			return StageResult{}, fmt.Errorf("clear previous normalized trace: %w", err)
		}
	}
	frames := 0
	attributedLogs, fallbackLogs := 0, 0
	for _, transaction := range transactions {
		for _, frame := range transaction.trace.Frames {
			if err := persistTraceFrame(ctx, tx, job, transaction, frame); err != nil {
				return StageResult{}, err
			}
			frames++
		}
		attributions, fallback, err := loadTraceLogAttributions(ctx, tx, job, transaction)
		if err != nil {
			return StageResult{}, err
		}
		for _, attribution := range attributions {
			if err := persistTraceLogAttribution(ctx, tx, job, transaction, attribution); err != nil {
				return StageResult{}, err
			}
			attributedLogs++
		}
		fallbackLogs += fallback
	}
	details := map[string]string{
		"outcome": outcome, "source": source,
		"transactions": strconv.Itoa(len(transactions)), "frames": strconv.Itoa(frames),
		"attributed_logs": strconv.Itoa(attributedLogs), "fallback_logs": strconv.Itoa(fallbackLogs),
	}
	creationTargets := successfulCreationTargets(transactions)
	details["creation_targets"] = strconv.Itoa(creationTargets)
	// Trace is optional and never blocks the first proxy/ABI pass. Every complete
	// trace publication, including an empty replacement, refreshes Proxy before
	// ABI: a newer generation can withdraw an earlier CREATE/CREATE2 proof used
	// to authenticate immutable-args Clones. The source-generation key makes the
	// invalidation bounded and prevents a replay loop.
	if canonical {
		proxyRequeued, err := resetTerminalDependentStageTx(ctx, tx, job, ProxyStage)
		if err != nil {
			return StageResult{}, err
		}
		details["proxy_requeued"] = strconv.FormatBool(proxyRequeued)
		abiRequeued, err := resetTerminalDependentStageTx(ctx, tx, job, ABIStage)
		if err != nil {
			return StageResult{}, err
		}
		details["abi_requeued"] = strconv.FormatBool(abiRequeued)
	}
	return StageResult{State: ResultComplete, Details: details}, nil
}

func successfulCreationTargets(transactions []traceTransaction) int {
	targets := 0
	for _, transaction := range transactions {
		for _, frame := range transaction.trace.Frames {
			if (frame.Type == "CREATE" || frame.Type == "CREATE2") &&
				!frame.Reverted && frame.To != nil {
				targets++
			}
		}
	}
	return targets
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
	var executionAddress, executionCodeHash any
	if frame.ExecutionAddress != nil {
		executionAddress = frame.ExecutionAddress[:]
	}
	if frame.ExecutionCodeHash != nil {
		executionCodeHash = frame.ExecutionCodeHash[:]
	}
	if frame.ExecutionResolution == "" {
		frame.ExecutionResolution = "unavailable"
	}
	_, err = tx.ExecContext(ctx, insertTraceFrameSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		transaction.hash[:], transaction.index, tracePath, parentPath, len(frame.TraceAddress),
		frame.Type, from, to, created, value, gas, gasUsed, nullableBytes(frame.Input),
		nullableBytes(frame.Output), traceError, frame.DirectReverted, frame.Reverted,
		executionAddress, executionCodeHash, frame.ExecutionResolution,
	)
	if err != nil {
		return fmt.Errorf("persist normalized trace frame: %w", err)
	}
	return nil
}

type traceLogAttribution struct {
	logIndex         uint64
	tracePath        string
	callType         string
	executionAddress common.Address
}

func loadTraceLogAttributions(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	transaction traceTransaction,
) ([]traceLogAttribution, int, error) {
	rows, err := tx.QueryContext(ctx, traceReceiptLogsSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:], transaction.hash[:],
	)
	if err != nil {
		return nil, 0, fmt.Errorf("query receipt logs for trace attribution: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	expected := make(map[uint64]types.Log)
	for rows.Next() {
		var index int64
		var raw []byte
		if err := rows.Scan(&index, &raw); err != nil {
			return nil, 0, fmt.Errorf("scan receipt log for trace attribution: %w", err)
		}
		if index < 0 {
			return nil, 0, Permanent(errors.New("stored receipt log index is negative"))
		}
		var decoded types.Log
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, 0, Permanent(errors.New("stored receipt log is malformed"))
		}
		if uint64(decoded.Index) != uint64(index) || decoded.TxHash != transaction.hash ||
			decoded.BlockHash != job.BlockHash || decoded.BlockNumber != job.BlockNumber {
			return nil, 0, Permanent(errors.New("stored receipt log identity is inconsistent"))
		}
		expected[uint64(index)] = decoded
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate receipt logs for trace attribution: %w", err)
	}
	captured := 0
	for _, frame := range transaction.trace.Frames {
		captured += len(frame.Logs)
	}
	if captured == 0 {
		return nil, len(expected), nil
	}
	if captured != len(expected) {
		return nil, 0, Permanent(errors.New("callTracer returned a partial receipt-log set"))
	}
	result := make([]traceLogAttribution, 0, captured)
	fallback := 0
	seen := make(map[uint64]struct{}, captured)
	for _, frame := range transaction.trace.Frames {
		if len(frame.Logs) == 0 {
			continue
		}
		if frame.Type == "STATICCALL" || frame.Type == "SELFDESTRUCT" || frame.To == nil {
			return nil, 0, Permanent(errors.New("callTracer attached a log to an invalid execution frame"))
		}
		for _, traced := range frame.Logs {
			if _, duplicate := seen[traced.Index]; duplicate {
				return nil, 0, Permanent(errors.New("callTracer returned a duplicate log index"))
			}
			seen[traced.Index] = struct{}{}
			stored, exists := expected[traced.Index]
			if !exists || stored.Address != traced.Address || !bytes.Equal(stored.Data, traced.Data) ||
				len(stored.Topics) != len(traced.Topics) {
				return nil, 0, Permanent(errors.New("callTracer log does not match the canonical receipt"))
			}
			for index := range stored.Topics {
				if stored.Topics[index] != traced.Topics[index] {
					return nil, 0, Permanent(errors.New("callTracer log topics do not match the canonical receipt"))
				}
			}
			if frame.To == nil || stored.Address != *frame.To {
				return nil, 0, Permanent(errors.New("callTracer log emitter does not match the execution context"))
			}
			executionAddress := frame.ExecutionAddress
			creation := frame.Type == "CREATE" || frame.Type == "CREATE2"
			if creation && frame.To != nil {
				executionAddress = frame.To
			}
			addressExact := frame.ExecutionResolution == "unavailable" && executionAddress != nil
			codeExact := frame.ExecutionCodeHash != nil &&
				(frame.ExecutionResolution == "direct" || frame.ExecutionResolution == "eip7702_delegate")
			if executionAddress == nil || !creation && !addressExact && !codeExact {
				fallback++
				continue
			}
			result = append(result, traceLogAttribution{
				logIndex: traced.Index, tracePath: tracePathKey(frame.TraceAddress),
				callType: frame.Type, executionAddress: *executionAddress,
			})
		}
	}
	return result, fallback, nil
}

func persistTraceLogAttribution(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	transaction traceTransaction,
	attribution traceLogAttribution,
) error {
	if _, err := tx.ExecContext(ctx, insertTraceLogAttributionSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		transaction.hash[:], attribution.logIndex, attribution.tracePath,
		attribution.callType, attribution.executionAddress[:],
	); err != nil {
		return fmt.Errorf("persist trace log attribution: %w", err)
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
	return quantity.String(), nil
}

func nullableBytes(value []byte) any {
	if value == nil {
		return nil
	}
	return value
}

func traceCapabilityUnavailable(err error) bool {
	if errors.Is(err, errTraceRPCUnavailable) || errors.Is(err, errBlockTraceHistoryUnavailable) ||
		ethrpc.IsMethodNotFound(err) {
		return true
	}
	var rpcError rpc.Error
	if !errors.As(err, &rpcError) {
		return false
	}
	message := strings.ToLower(rpcError.Error())
	return strings.Contains(message, "pruned") || strings.Contains(message, "historical state") ||
		strings.Contains(message, "missing trie")
}

func traceAdapterFallback(err error) bool {
	return ethrpc.IsMethodNotFound(err) || traceCapabilityUnavailable(err)
}

func sanitizeTraceRPCError(err error) error {
	if traceCapabilityUnavailable(err) {
		return errTraceRPCUnavailable
	}
	return ethrpc.SanitizeError(err)
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

const traceExecutionResolutionsSQL = `
SELECT resolution.transaction_hash, resolution.context_address,
       resolution.execution_address, resolution.execution_code_hash,
       resolution.resolution, resolution.evidence_source
FROM transaction_execution_code_resolutions AS resolution
WHERE resolution.chain_id = $1::numeric
  AND resolution.block_number = $2::numeric
  AND resolution.block_hash = $3
  AND resolution.canonical
  AND EXISTS (
      SELECT 1
      FROM published_block_stage_results AS published
      WHERE published.chain_id = resolution.chain_id
        AND published.block_number = resolution.block_number
        AND published.block_hash = resolution.block_hash
        AND published.stage = $4
        AND published.stage_version = $5
        AND published.state = 'complete'
  )
ORDER BY resolution.transaction_index, resolution.context_address`

const deleteTraceBlockSQL = `
DELETE FROM normalized_traces
WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3`

const deleteTraceLogAttributionsSQL = `
DELETE FROM trace_log_attributions
WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3`

const insertTraceFrameSQL = `
INSERT INTO normalized_traces (
    chain_id, block_number, block_hash, transaction_hash, transaction_index,
    trace_path, parent_path, depth, call_type, from_address, to_address,
    created_address, value, gas, gas_used, input, output, error,
    direct_reverted, reverted, execution_address, execution_code_hash,
    execution_resolution, canonical
) VALUES (
    $1::numeric, $2::numeric, $3, $4, $5, $6, $7, $8, $9, $10, $11,
    $12, $13::numeric, $14::numeric, $15::numeric, $16, $17, $18, $19, $20,
    $21, $22, $23, true
)`

const traceReceiptLogsSQL = `
SELECT log_index, raw
FROM logs
WHERE chain_id = $1::numeric
  AND block_number = $2::numeric
  AND block_hash = $3
  AND tx_hash = $4
ORDER BY log_index`

const insertTraceLogAttributionSQL = `
INSERT INTO trace_log_attributions (
    chain_id, block_number, block_hash, transaction_hash, log_index,
    trace_path, call_type, execution_address, canonical
) VALUES (
    $1::numeric, $2::numeric, $3, $4, $5, $6, $7, $8, TRUE
)`
