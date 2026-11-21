package enrich

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// TraceInputCollector acquires bounded raw responses for one immutable block.
// Supplementary prestate uses the endpoint recorded with the original diff.
type TraceInputCollector struct {
	DB                    dbaccess.Database
	Pool                  *ethrpc.Pool
	ChainID               string
	Timeout, PollInterval time.Duration
	Logger                *slog.Logger
}

type traceInput struct {
	Endpoint    string            `json:"endpoint"`
	Source      string            `json:"source"`
	Calls       map[string][]byte `json:"calls"`
	WithoutLogs bool              `json:"without_logs,omitempty"`
}

type traceCapture struct {
	input  *traceInput
	caller rpcCaller
}

func traceCallKey(method string, args []any) (string, error) {
	data, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return method + ":" + hex.EncodeToString(digest[:]), nil
}
func (c traceCapture) CallContext(ctx context.Context, result any, method string, args ...any) error {
	raw, ok := result.(*json.RawMessage)
	if !ok {
		return errors.New("trace_input_type")
	}
	err := c.caller.CallContext(ctx, raw, method, args...)
	if err != nil {
		if traceLogConfigUnsupported(err) {
			c.input.WithoutLogs = true
		}
		return err
	}
	key, err := traceCallKey(method, args)
	if err != nil {
		return err
	}
	c.input.Calls[key] = *raw
	return nil
}

type traceConfigUnsupported struct{}

func (traceConfigUnsupported) Error() string  { return "trace_config_unsupported" }
func (traceConfigUnsupported) ErrorCode() int { return -32602 }
func (input *traceInput) CallContext(_ context.Context, result any, method string, args ...any) error {
	key, err := traceCallKey(method, args)
	if err != nil {
		return err
	}
	raw, ok := result.(*json.RawMessage)
	data, found := input.Calls[key]
	if !found && input.WithoutLogs && method == debugTraceBlockByHashMethod {
		return traceConfigUnsupported{}
	}
	if !found || !ok {
		return Permanent(errors.New("trace_input_invalid"))
	}
	*raw = data
	return nil
}
func (*TraceInputCollector) Name() string { return "trace-inputs" }
func (c *TraceInputCollector) Run(ctx context.Context) error {
	ticker := time.NewTicker(c.PollInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		found, err := c.CollectOne(ctx)
		if found && err == nil {
			continue
		}
		if err != nil && ctx.Err() == nil && c.Logger != nil {
			c.Logger.WarnContext(ctx, "trace acquisition deferred", "error_code", "trace_acquisition_unavailable")
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
	return nil
}
func (c *TraceInputCollector) CollectOne(ctx context.Context) (bool, error) {
	var chain pgtype.Numeric
	if err := chain.Scan(c.ChainID); err != nil {
		return false, err
	}
	q := dbgen.New(c.DB)
	row, err := q.BlockInputClaim(ctx, (c.Timeout + time.Second).Milliseconds(), chain, []string{TraceStage.String(), StateDiffStage.String(), fullPrestateKind})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	fetchCtx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	input, fetchErr := c.collectBlock(fetchCtx, chain, row)
	finish := dbgen.BlockInputFinishParams{ChainID: chain, BlockNumber: row.BlockNumber, BlockHash: row.BlockHash, Kind: row.Kind, Generation: row.Generation, Attempts: row.Attempts}
	if fetchErr != nil {
		classified, _ := errors.AsType[stageError](fetchErr)
		switch {
		case classified.kind == "permanent":
			finish.ErrorCode = "trace_input_invalid"
		case classified.kind == "unavailable", traceCapabilityUnavailable(fetchErr):
			finish.ErrorCode = "trace_input_unavailable"
		case row.Attempts >= 4:
			finish.ErrorCode = "trace_acquisition_failed"
		default:
			return true, fetchErr
		}
		diagnostic := StageDiagnostic{Endpoint: input.Endpoint}
		if d, ok := errors.AsType[stageDiagnosticError](fetchErr); ok {
			diagnostic = d.diagnostic
			diagnostic.Endpoint = input.Endpoint
		}
		finish.Evidence, err = json.Marshal(diagnostic)
		if err != nil {
			return true, err
		}
	} else {
		finish.Evidence, err = json.Marshal(input)
		if err != nil {
			return true, err
		}
	}
	_, err = q.BlockInputFinish(ctx, finish)
	return true, err
}

const fullPrestateKind = "full_prestate@1"

func prestateOptions(diff bool) map[string]any {
	return map[string]any{"tracer": "prestateTracer", "tracerConfig": map[string]any{"diffMode": diff}}
}

func (c *TraceInputCollector) collectBlock(ctx context.Context, chain pgtype.Numeric, row dbgen.BlockInputClaimRow) (traceInput, error) {
	input := traceInput{Calls: make(map[string][]byte)}
	q := dbgen.New(c.DB)
	var ep *ethrpc.Endpoint
	var err error
	if row.Kind == fullPrestateKind {
		name, e := q.BlockInputTraceEndpoint(ctx, chain, row.BlockNumber, row.BlockHash)
		if e != nil {
			return input, e
		}
		ep, err = c.Pool.AcquireNamed(ethrpc.PurposeTrace, name)
	} else {
		ep, err = c.Pool.Acquire(ethrpc.PurposeTrace)
	}
	if err != nil {
		return input, Unavailable(errTraceRPCUnavailable)
	}
	input.Endpoint = ep.Name
	caller := traceCapture{input: &input, caller: ep}
	hash := common.BytesToHash(row.BlockHash)
	if row.Kind == TraceStage.String() {
		hashes, e := q.BlockInputTransactionHashes(ctx, chain, row.BlockNumber, row.BlockHash)
		if e != nil {
			return input, e
		}
		expected := make([]common.Hash, len(hashes))
		for i, h := range hashes {
			expected[i] = common.BytesToHash(h)
		}
		input.Source, err = collectCallTrace(ctx, caller, ep.Capabilities, hash, expected)
	} else {
		input.Source = "prestateTracer"
		if ep.Capabilities.Status(ethrpc.CapabilityDebugTrace) == ethrpc.AvailabilityUnavailable {
			return input, Unavailable(errStateDiffRPCAbsent)
		}
		var raw json.RawMessage
		err = caller.CallContext(ctx, &raw, debugTraceBlockByHashMethod, hash, prestateOptions(row.Kind != fullPrestateKind))
		if len(raw) > DefaultStateDiffLimits().MaxBlockPayloadBytes {
			return input, Permanent(errors.New("state_diff_payload_limit"))
		}
	}
	if err != nil {
		c.Pool.ReportFailure(ep.Name)
	} else {
		c.Pool.ReportSuccess(ep.Name)
	}
	return input, err
}

func collectCallTrace(ctx context.Context, caller traceCapture, capabilities ethrpc.CapabilityReport, hash common.Hash, transactions []common.Hash) (string, error) {
	budget := newTraceBlockBudget(DefaultTraceLimits())
	if capabilities.Status(ethrpc.CapabilityDebugTrace) != ethrpc.AvailabilityUnavailable {
		var raw json.RawMessage
		err := caller.CallContext(ctx, &raw, debugTraceBlockByHashMethod, hash, callTracerOptions(true))
		if traceLogConfigUnsupported(err) {
			err = caller.CallContext(ctx, &raw, debugTraceBlockByHashMethod, hash, callTracerOptions(false))
		}
		if err == nil {
			if e := budget.addPayload(len(raw)); e != nil {
				return "", Permanent(e)
			}
			// Inspect the envelope for a history-pruned item before choosing an
			// adapter. The trace worker owns frame normalization and identity checks.
			rows, e := decodeBlockTraceResults(raw, transactions)
			if e != nil {
				return "", Permanent(e)
			}
			for _, row := range rows {
				if row.err != nil {
					err = row.err
					break
				}
			}
			if !traceAdapterFallback(err) {
				return string(TraceCallTracer), nil
			}
		}
		if !traceAdapterFallback(err) {
			return "", err
		}
		clear(caller.input.Calls)
	}
	if capabilities.Status(ethrpc.CapabilityParityTrace) == ethrpc.AvailabilityUnavailable {
		return "", Unavailable(errTraceRPCUnavailable)
	}
	for i, tx := range transactions {
		var raw json.RawMessage
		if err := caller.CallContext(ctx, &raw, "trace_transaction", tx.Hex()); err != nil {
			return "", withStageDiagnostic(err, StageDiagnostic{Code: "rpc_request_failed", Phase: "trace_transaction", TransactionHash: tx, TransactionIndex: uint64(i), HasTransaction: true})
		}
		if err := budget.addPayload(len(raw)); err != nil {
			return "", Permanent(err)
		}
	}
	return string(TraceAPI), nil
}

func loadTraceInput(ctx context.Context, db dbaccess.Database, job Job, kind string) (*traceInput, error) {
	var chain, height pgtype.Numeric
	if err := chain.Scan(job.ChainID); err != nil {
		return nil, err
	}
	if err := height.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
		return nil, err
	}
	_, generation, err := durableJobGeneration(job)
	if err != nil {
		return nil, err
	}
	q := dbgen.New(db)
	key := dbgen.BlockInputReadParams{ChainID: chain, BlockNumber: height, BlockHash: job.BlockHash[:], Kind: kind}
	row, err := q.BlockInputRead(ctx, key)
	switch {
	case errors.Is(err, pgx.ErrNoRows), err == nil && row.ErrorCode != "" && generation > row.Generation:
		err = q.BlockInputSubmit(ctx, dbgen.BlockInputSubmitParams{ChainID: chain, BlockNumber: height, BlockHash: job.BlockHash[:], Kind: key.Kind, Generation: generation})
		if err != nil {
			return nil, err
		}
		return nil, errInputPending
	case err != nil:
		return nil, err
	case row.ErrorCode != "":
		var diag StageDiagnostic
		if json.Unmarshal(row.Evidence, &diag) != nil {
			return nil, Permanent(errors.New("trace_input_invalid"))
		}
		if row.ErrorCode == "trace_input_unavailable" {
			return nil, Unavailable(withStageDiagnostic(errTraceRPCUnavailable, diag))
		}
		return nil, Permanent(withStageDiagnostic(errors.New(row.ErrorCode), diag))
	case len(row.Evidence) == 0:
		return nil, errInputPending
	}
	var input traceInput
	if json.Unmarshal(row.Evidence, &input) != nil || input.Endpoint == "" {
		return nil, Permanent(errors.New("trace_input_invalid"))
	}
	return &input, nil
}

type storedStateDiff struct {
	primary *traceInput
	load    func() (*traceInput, error)
	loadErr error
}

func (s *storedStateDiff) CallContext(ctx context.Context, result any, method string, args ...any) error {
	key, err := traceCallKey(method, args)
	if err != nil {
		return err
	}
	if _, ok := s.primary.Calls[key]; ok {
		return s.primary.CallContext(ctx, result, method, args...)
	}
	extra, err := s.load()
	if err != nil {
		s.loadErr = err
		return err
	}
	if extra.Endpoint != s.primary.Endpoint {
		return Permanent(errors.New("state_diff_endpoint_mismatch"))
	}
	return extra.CallContext(ctx, result, method, args...)
}
