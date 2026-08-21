package enrich

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/db/gen"
)

var (
	TokenStage = StageID{Name: "token", Version: 1}
	// stats@3 adds receipt-authenticated execution fees, priority fees, failed
	// transactions, and successful top-level creations. Its new durable
	// identity prevents stats@1 or stats@2 completions from claiming the
	// expanded persisted contract.
	StatsStage = StageID{Name: "stats", Version: 3}
)

type PostgresTokenProcessor struct {
	db       *sql.DB
	detector TokenDetector
}

func NewPostgresTokenProcessor(db *sql.DB) (*PostgresTokenProcessor, error) {
	if db == nil {
		return nil, errors.New("token processor requires a database")
	}
	return &PostgresTokenProcessor{db: db}, nil
}

func NewPostgresTokenProcessorWithDetector(db *sql.DB, detector TokenDetector) (*PostgresTokenProcessor, error) {
	if db == nil || detector == nil {
		return nil, errors.New("token processor requires a database and detector")
	}
	return &PostgresTokenProcessor{db: db, detector: detector}, nil
}

func (*PostgresTokenProcessor) Stage() StageID { return TokenStage }

func (processor *PostgresTokenProcessor) ProcessLease(
	ctx context.Context,
	lease Lease,
	queue *PostgresJobQueue,
) (StageResult, error) {
	return processor.Process(ctx, bindStagePublication(lease.Job, lease, queue))
}

func (processor *PostgresTokenProcessor) Process(ctx context.Context, job Job) (StageResult, error) {
	if processor == nil || processor.db == nil {
		return StageResult{}, errors.New("process token stage using nil database")
	}
	if err := job.Validate(); err != nil {
		return StageResult{}, Permanent(err)
	}
	if job.Stage != TokenStage {
		return StageResult{}, Permanent(fmt.Errorf("token processor received stage %s", job.Stage))
	}
	if processor.detector != nil {
		return processor.processDetected(ctx, job)
	}
	return runStageTransaction(ctx, processor.db, job, func(ctx context.Context, tx *sql.Tx) (StageResult, error) {
		return processor.processTokenTx(ctx, tx, job)
	})
}

func (processor *PostgresTokenProcessor) processTokenTx(ctx context.Context, tx *sql.Tx, job Job) (StageResult, error) {
	canonical, err := lockCanonicalBlock(ctx, tx, job)
	if err != nil {
		return StageResult{}, err
	}
	if !canonical {
		return StageResult{
			State: ResultComplete, Details: map[string]string{"outcome": "stale_canonical_skipped"},
		}, nil
	}

	rows, err := tx.QueryContext(ctx, dbgen.EnrichLegacyTokenLogs, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:])
	if err != nil {
		return StageResult{}, fmt.Errorf("query token logs: %w", err)
	}
	storedLogs, err := readStoredTokenLogs(rows, job)
	if err != nil {
		return StageResult{}, err
	}
	parsedEvents, malformedLogs := 0, 0
	for _, stored := range storedLogs {
		result := ParseTokenLog(stored.log)
		if result.Status == TokenMalformed {
			malformedLogs++
			continue
		}
		if result.Status != TokenParsed {
			continue
		}
		standard, confidence, known, err := detectedToken(ctx, tx, job, stored.log.Contract)
		if err != nil {
			return StageResult{}, err
		}
		for _, event := range result.Events {
			if event.Standard == TokenERC721Or1155 {
				if !known || standard != TokenERC721 && standard != TokenERC1155 {
					continue
				}
				event.Standard = standard
			}
			if known && event.Standard != standard {
				// A log layout that conflicts with a positively detected standard is
				// not accepted as a token event.
				continue
			}
			if known {
				event.Confidence = confidence
			} else {
				event.Confidence = ConfidenceGuess
			}
			if err := persistTokenEvent(ctx, tx, job, stored.transactionHash, stored.raw, event); err != nil {
				return StageResult{}, err
			}
			parsedEvents++
		}
	}
	return StageResult{
		State: ResultComplete,
		Details: map[string]string{
			"events": strconv.Itoa(parsedEvents), "malformed_logs": strconv.Itoa(malformedLogs),
		},
	}, nil
}

func (processor *PostgresTokenProcessor) processDetected(ctx context.Context, job Job) (StageResult, error) {
	canonical, err := processor.tokenBlockCanonical(ctx, job)
	if err != nil {
		return StageResult{}, err
	}
	if !canonical {
		return processor.persistDetectedTokenBlock(ctx, job, nil)
	}
	evidence, err := processor.collectTokenEvidence(ctx, job)
	if err != nil {
		return StageResult{}, err
	}
	addresses := sortedTokenAddresses(evidence)
	detections := make(map[common.Address]TokenDetection, len(addresses))
	if blockDetector, ok := processor.detector.(TokenBlockDetector); ok {
		detections, err = blockDetector.DetectBlock(ctx, job, evidence)
		if err != nil {
			return StageResult{}, err
		}
	} else {
		for _, address := range addresses {
			detection, detectErr := processor.detector.Detect(ctx, TokenDetectionRequest{
				Job: job, Address: address, Evidence: evidence[address],
			})
			if detectErr != nil {
				return StageResult{}, detectErr
			}
			detections[address] = detection
		}
	}
	if len(detections) != len(addresses) {
		return StageResult{}, Permanent(errors.New("token detector returned an incomplete block result"))
	}
	for _, address := range addresses {
		detection, ok := detections[address]
		if !ok {
			return StageResult{}, Permanent(errors.New("token detector omitted a block address"))
		}
		if err := detection.validate(); err != nil {
			return StageResult{}, Permanent(fmt.Errorf("token detector returned invalid result: %w", err))
		}
	}
	return processor.persistDetectedTokenBlock(ctx, job, detections)
}

func (processor *PostgresTokenProcessor) tokenBlockCanonical(ctx context.Context, job Job) (bool, error) {
	var canonical bool
	if err := processor.db.QueryRowContext(ctx, dbgen.EnrichLegacyTokenCanonical, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:]).Scan(&canonical); err != nil {
		return false, fmt.Errorf("check token block canonicality: %w", err)
	}
	return canonical, nil
}

func (processor *PostgresTokenProcessor) collectTokenEvidence(ctx context.Context, job Job) (map[common.Address]TokenLogEvidence, error) {
	rows, err := processor.db.QueryContext(ctx, dbgen.EnrichLegacyTokenLogs, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:])
	if err != nil {
		return nil, fmt.Errorf("query token detection logs: %w", err)
	}
	storedLogs, err := readStoredTokenLogs(rows, job)
	if err != nil {
		return nil, err
	}
	evidence := make(map[common.Address]TokenLogEvidence)
	for _, stored := range storedLogs {
		parsed := ParseTokenLog(stored.log)
		if parsed.Status != TokenParsed {
			continue
		}
		current := evidence[stored.log.Contract]
		for _, event := range parsed.Events {
			switch event.Standard {
			case TokenERC20:
				current.ERC20 = true
			case TokenERC721:
				current.ERC721 = true
			case TokenERC1155:
				current.ERC1155 = true
			case TokenERC721Or1155:
				current.ERC721Or1155 = true
			}
		}
		evidence[stored.log.Contract] = current
	}
	return evidence, nil
}

func (processor *PostgresTokenProcessor) persistDetectedTokenBlock(ctx context.Context, job Job, detections map[common.Address]TokenDetection) (StageResult, error) {
	return runStageTransaction(ctx, processor.db, job, func(ctx context.Context, tx *sql.Tx) (StageResult, error) {
		return processor.persistDetectedTokenBlockTx(ctx, tx, job, detections)
	})
}

func (processor *PostgresTokenProcessor) persistDetectedTokenBlockTx(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	detections map[common.Address]TokenDetection,
) (StageResult, error) {
	canonical, err := lockCanonicalBlock(ctx, tx, job)
	if err != nil {
		return StageResult{}, err
	}
	if !canonical {
		return StageResult{
			State: ResultComplete, Details: map[string]string{"outcome": "stale_canonical_skipped"},
		}, nil
	}
	for _, address := range sortedTokenAddresses(detections) {
		detection := detections[address]
		if err := persistTokenContract(ctx, tx, job, address, detection); err != nil {
			return StageResult{}, err
		}
	}
	rows, err := tx.QueryContext(ctx, dbgen.EnrichLegacyTokenLogs, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:])
	if err != nil {
		return StageResult{}, fmt.Errorf("query detected token logs: %w", err)
	}
	storedLogs, err := readStoredTokenLogs(rows, job)
	if err != nil {
		return StageResult{}, err
	}
	parsedEvents, malformedLogs := 0, 0
	for _, stored := range storedLogs {
		result := ParseTokenLog(stored.log)
		if result.Status == TokenMalformed {
			malformedLogs++
			continue
		}
		if result.Status != TokenParsed {
			continue
		}
		detection, detected := detections[stored.log.Contract]
		if !detected {
			return StageResult{}, Permanent(errors.New("parsed token address was not detected"))
		}
		known := detection.Standard != TokenStandardUnknown
		for _, event := range result.Events {
			if event.Standard == TokenERC721Or1155 {
				if !known || detection.Standard != TokenERC721 && detection.Standard != TokenERC1155 {
					continue
				}
				event.Standard = detection.Standard
			}
			if known && event.Standard != detection.Standard {
				continue
			}
			if known {
				event.Confidence = detection.Confidence
			} else {
				event.Confidence = ConfidenceGuess
			}
			if err := persistTokenEvent(ctx, tx, job, stored.transactionHash, stored.raw, event); err != nil {
				return StageResult{}, err
			}
			parsedEvents++
		}
	}
	return StageResult{
		State: ResultComplete,
		Details: map[string]string{
			"contracts": strconv.Itoa(len(detections)), "events": strconv.Itoa(parsedEvents),
			"malformed_logs": strconv.Itoa(malformedLogs),
		},
	}, nil
}

func sortedTokenAddresses[Value any](values map[common.Address]Value) []common.Address {
	addresses := make([]common.Address, 0, len(values))
	for address := range values {
		addresses = append(addresses, address)
	}
	slices.SortFunc(addresses, func(left, right common.Address) int {
		return bytes.Compare(left[:], right[:])
	})
	return addresses
}

type storedTokenLog struct {
	log             TokenLog
	transactionHash []byte
	raw             []byte
}

// readStoredTokenLogs releases the query before callers issue lookups or
// writes on the same transaction. pgx transactions use one connection and
// cannot execute another statement while streaming rows from it.
func readStoredTokenLogs(rows *sql.Rows, job Job) ([]storedTokenLog, error) {
	stored := make([]storedTokenLog, 0)
	for rows.Next() {
		tokenLog, transactionHash, raw, err := scanStoredTokenLog(rows, job)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		stored = append(stored, storedTokenLog{log: tokenLog, transactionHash: transactionHash, raw: raw})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate token logs: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close token logs: %w", err)
	}
	return stored, nil
}

func scanStoredTokenLog(row rowScanner, job Job) (TokenLog, []byte, []byte, error) {
	var logIndex int64
	var transactionHash, address, raw []byte
	if err := row.Scan(&logIndex, &transactionHash, &address, &raw); err != nil {
		return TokenLog{}, nil, nil, fmt.Errorf("scan token log: %w", err)
	}
	if logIndex < 0 || len(transactionHash) != common.HashLength || len(address) != common.AddressLength {
		return TokenLog{}, nil, nil, Permanent(errors.New("stored token log identity is invalid"))
	}
	var wire types.Log
	if err := json.Unmarshal(raw, &wire); err != nil {
		return TokenLog{}, nil, nil, Permanent(fmt.Errorf("decode token log: %w", err))
	}
	tokenLog, err := indexedTokenLog(wire, uint64(logIndex), transactionHash, address, job)
	if err != nil {
		return TokenLog{}, nil, nil, Permanent(err)
	}
	return tokenLog, transactionHash, raw, nil
}

func persistTokenContract(ctx context.Context, tx *sql.Tx, job Job, address common.Address, detection TokenDetection) error {
	var name, symbol, decimals, totalSupply any
	if detection.Name != nil {
		name = *detection.Name
	}
	if detection.Symbol != nil {
		symbol = *detection.Symbol
	}
	if detection.Decimals != nil {
		decimals = int64(*detection.Decimals)
	}
	if detection.TotalSupply != nil {
		totalSupply = *detection.TotalSupply
	}
	if _, err := tx.ExecContext(ctx, dbgen.EnrichLegacyUpsertTokenContract, job.ChainID, address[:], detection.CodeHash[:], detection.Standard, detection.Confidence,
		name, symbol, decimals, totalSupply, detection.MetadataState,
		strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
	); err != nil {
		return fmt.Errorf("persist detected token contract: %w", err)
	}
	return nil
}

func indexedTokenLog(wire types.Log, logIndex uint64, transactionHash, address []byte, job Job) (TokenLog, error) {
	if uint64(wire.Index) != logIndex {
		return TokenLog{}, errors.New("stored token log raw identity is incomplete")
	}
	if wire.TxHash != common.BytesToHash(transactionHash) {
		return TokenLog{}, errors.New("stored token log transaction hash mismatch")
	}
	if wire.BlockHash != job.BlockHash {
		return TokenLog{}, errors.New("stored token log block hash mismatch")
	}
	if wire.BlockNumber != job.BlockNumber {
		return TokenLog{}, errors.New("stored token log block number mismatch")
	}
	if wire.Address != common.BytesToAddress(address) {
		return TokenLog{}, errors.New("stored token log address mismatch")
	}
	return TokenLog{
		Contract: wire.Address,
		Topics:   append([]common.Hash(nil), wire.Topics...),
		Data:     common.CopyBytes(wire.Data),
		LogIndex: logIndex,
	}, nil
}

func detectedToken(ctx context.Context, tx *sql.Tx, job Job, address common.Address) (TokenStandard, Confidence, bool, error) {
	var standard, confidence string
	err := tx.QueryRowContext(ctx, dbgen.EnrichLegacyDetectedToken, job.ChainID, address[:], strconv.FormatUint(job.BlockNumber, 10)).Scan(&standard, &confidence)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("query detected token: %w", err)
	}
	parsedStandard, parsedConfidence := TokenStandard(standard), Confidence(confidence)
	if parsedStandard != TokenERC20 && parsedStandard != TokenERC721 && parsedStandard != TokenERC1155 {
		return "", "", false, Permanent(fmt.Errorf("persisted token standard %q is invalid", standard))
	}
	if confidenceRank(parsedConfidence) == 0 {
		return "", "", false, Permanent(fmt.Errorf("persisted token confidence %q is invalid", confidence))
	}
	return parsedStandard, parsedConfidence, true, nil
}

func persistTokenEvent(ctx context.Context, tx *sql.Tx, job Job, transactionHash, raw []byte, event TokenEvent) error {
	if event.LogIndex > math.MaxInt64 || event.SubIndex > math.MaxInt32 {
		return Permanent(errors.New("token event index exceeds PostgreSQL range"))
	}
	var operator, from, to any
	if event.Operator != nil {
		operator = event.Operator[:]
	}
	if event.From != nil {
		from = event.From[:]
	} else if event.Owner != nil {
		from = event.Owner[:]
	}
	if event.To != nil {
		to = event.To[:]
	} else if event.Spender != nil {
		to = event.Spender[:]
	}
	var tokenID, amount any
	if event.TokenID != "" {
		tokenID = event.TokenID
	}
	if event.Amount != "" {
		amount = event.Amount
	}
	_, err := tx.ExecContext(ctx, dbgen.EnrichLegacyInsertTokenEvent, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		event.LogIndex, event.SubIndex, transactionHash, event.Contract[:], event.Standard,
		event.Kind, operator, from, to, tokenID, amount, event.Confidence, string(raw),
	)
	if err != nil {
		return fmt.Errorf("persist token event: %w", err)
	}
	if event.Kind != TokenTransfer && event.Kind != TokenMint && event.Kind != TokenBurn {
		return nil
	}
	value, ok := new(big.Int).SetString(event.Amount, 10)
	if !ok || value.Sign() < 0 {
		return Permanent(errors.New("token transfer amount is invalid"))
	}
	deltas := make(map[common.Address]*big.Int, 2)
	add := func(owner *common.Address, delta *big.Int) {
		if owner == nil || *owner == (common.Address{}) {
			return
		}
		if deltas[*owner] == nil {
			deltas[*owner] = new(big.Int)
		}
		deltas[*owner].Add(deltas[*owner], delta)
	}
	add(event.From, new(big.Int).Neg(new(big.Int).Set(value)))
	add(event.To, new(big.Int).Set(value))
	for owner, delta := range deltas {
		if delta.Sign() == 0 {
			continue
		}
		_, err := tx.ExecContext(ctx, dbgen.EnrichLegacyInsertTokenDelta, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:], event.LogIndex,
			event.SubIndex, event.Contract[:], owner[:], tokenID, delta.String(),
		)
		if err != nil {
			return fmt.Errorf("persist token balance delta: %w", err)
		}
	}
	return nil
}

type PostgresStatsProcessor struct{ db *sql.DB }

func NewPostgresStatsProcessor(db *sql.DB) (*PostgresStatsProcessor, error) {
	if db == nil {
		return nil, errors.New("stats processor requires a database")
	}
	return &PostgresStatsProcessor{db: db}, nil
}

func (*PostgresStatsProcessor) Stage() StageID { return StatsStage }

func (processor *PostgresStatsProcessor) ProcessLease(
	ctx context.Context,
	lease Lease,
	queue *PostgresJobQueue,
) (StageResult, error) {
	return processor.Process(ctx, bindStagePublication(lease.Job, lease, queue))
}

func (processor *PostgresStatsProcessor) Process(ctx context.Context, job Job) (StageResult, error) {
	if processor == nil || processor.db == nil {
		return StageResult{}, errors.New("process stats stage using nil database")
	}
	if err := job.Validate(); err != nil {
		return StageResult{}, Permanent(err)
	}
	if job.Stage != StatsStage {
		return StageResult{}, Permanent(fmt.Errorf("stats processor received stage %s", job.Stage))
	}
	return runStageTransaction(ctx, processor.db, job, func(ctx context.Context, tx *sql.Tx) (StageResult, error) {
		return processor.processStatsTx(ctx, tx, job)
	})
}

func (processor *PostgresStatsProcessor) processStatsTx(ctx context.Context, tx *sql.Tx, job Job) (StageResult, error) {
	canonical, err := lockCanonicalBlock(ctx, tx, job)
	if err != nil {
		return StageResult{}, err
	}
	if !canonical {
		return StageResult{State: ResultComplete, Details: map[string]string{"outcome": "stale_canonical_skipped"}}, nil
	}
	var raw []byte
	var transactionCount int64
	var configuredStart string
	var parentNumber, parentTimestamp sql.NullString
	var canonicalParent bool
	if err := tx.QueryRowContext(ctx, dbgen.EnrichLegacyBlockStatsSource, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:]).Scan(&raw, &transactionCount, &configuredStart, &parentNumber, &parentTimestamp, &canonicalParent); err != nil {
		return StageResult{}, fmt.Errorf("query stats source block: %w", err)
	}
	if transactionCount < 0 {
		return StageResult{}, Permanent(errors.New("negative transaction count"))
	}
	bundle, err := chainbundle.DecodeStoredBlock(raw)
	if err != nil {
		return StageResult{}, Permanent(fmt.Errorf("decode stats block: %w", err))
	}
	block := bundle.Block
	if block == nil || block.Hash() != job.BlockHash {
		return StageResult{}, Permanent(errors.New("stats block identity mismatch"))
	}
	if block.Number() == nil || !block.Number().IsUint64() || block.NumberU64() != job.BlockNumber {
		return StageResult{}, Permanent(errors.New("stats block number mismatch"))
	}
	configuredStartNumber, err := strconv.ParseUint(configuredStart, 10, 64)
	if err != nil || strconv.FormatUint(configuredStartNumber, 10) != configuredStart || job.BlockNumber < configuredStartNumber {
		return StageResult{}, Permanent(errors.New("stats configured start is missing or inconsistent"))
	}
	if job.BlockNumber > configuredStartNumber {
		expectedParent := strconv.FormatUint(job.BlockNumber-1, 10)
		if !parentNumber.Valid || parentNumber.String != expectedParent || !parentTimestamp.Valid || !canonicalParent {
			return StageResult{}, Permanent(errors.New("stats canonical parent fact is missing or inconsistent"))
		}
	}
	gasUsed := new(big.Int).SetUint64(block.GasUsed())
	gasLimit := new(big.Int).SetUint64(block.GasLimit())
	timestamp := new(big.Int).SetUint64(block.Time())
	var blockInterval, transactionsPerSecond any
	// The configured indexing start defines the statistics observation boundary.
	// Even if an older canonical parent happens to be retained in PostgreSQL, it
	// is outside that boundary and must not manufacture an interval or TPS value.
	if job.BlockNumber > configuredStartNumber {
		parent, ok := new(big.Int).SetString(parentTimestamp.String, 10)
		if !ok || parent.Sign() < 0 || timestamp.Cmp(parent) <= 0 {
			return StageResult{}, Permanent(errors.New("stats parent timestamp is invalid"))
		}
		interval := new(big.Int).Sub(timestamp, parent)
		blockInterval = interval.String()
		transactionsPerSecond = decimalRatio(big.NewInt(transactionCount), interval, 18)
	}
	var baseFee, blobGasUsed, excessBlobGas, blobBaseFee, burned, blobBurned any
	if value := block.BaseFee(); value != nil {
		baseFee = value.String()
		burned = new(big.Int).Mul(value, gasUsed).String()
	}
	header := block.Header()
	if (header.BlobGasUsed == nil) != (header.ExcessBlobGas == nil) {
		return StageResult{}, Permanent(errors.New("stats block has incomplete blob header fields"))
	}
	if header.BlobGasUsed != nil {
		blobGasUsed = strconv.FormatUint(*header.BlobGasUsed, 10)
	}
	if header.ExcessBlobGas != nil {
		excessBlobGas = strconv.FormatUint(*header.ExcessBlobGas, 10)
	}
	receiptFacts, err := readStatsReceiptFacts(ctx, tx, job, transactionCount)
	if err != nil {
		return StageResult{}, err
	}
	if header.BlobGasUsed == nil && receiptFacts.BlobGasUsed.Sign() > 0 {
		return StageResult{}, Permanent(errors.New("receipt blob gas is absent from the block header"))
	}
	if header.BlobGasUsed != nil {
		headerBlobGas := new(big.Int).SetUint64(*header.BlobGasUsed)
		if headerBlobGas.Cmp(receiptFacts.BlobGasUsed) != 0 {
			return StageResult{}, Permanent(errors.New("receipt blob gas does not match the block header"))
		}
	}
	if receiptFacts.GasUsed.Cmp(gasUsed) != 0 {
		return StageResult{}, Permanent(errors.New("receipt gas used does not match the block header"))
	}
	if receiptFacts.BlobGasPrice != nil {
		blobBaseFee = receiptFacts.BlobGasPrice.String()
		blobBurned = new(big.Int).Mul(receiptFacts.BlobGasPrice, receiptFacts.BlobGasUsed).String()
	} else if receiptFacts.BlobGasUsed.Sign() == 0 {
		blobBurned = "0"
	}
	baseFeeBurn := new(big.Int)
	if block.BaseFee() != nil {
		baseFeeBurn.Mul(block.BaseFee(), receiptFacts.GasUsed)
	}
	priorityFee := new(big.Int).Sub(new(big.Int).Set(receiptFacts.ExecutionFee), baseFeeBurn)
	if priorityFee.Sign() < 0 {
		return StageResult{}, Permanent(errors.New("execution fee is below authenticated base fee burn"))
	}
	if _, err := tx.ExecContext(ctx, dbgen.EnrichLegacyInsertBlockStats, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:], transactionCount,
		gasUsed.String(), gasLimit.String(), baseFee, blobGasUsed, burned,
		timestamp.String(), blockInterval, transactionsPerSecond, excessBlobGas, blobBaseFee, blobBurned,
		receiptFacts.ExecutionFee.String(), priorityFee.String(), receiptFacts.FailedTransactions,
		receiptFacts.ContractCreations,
	); err != nil {
		return StageResult{}, fmt.Errorf("persist block statistics: %w", err)
	}
	return StageResult{State: ResultComplete, Details: map[string]string{"transactions": strconv.FormatInt(transactionCount, 10)}}, nil
}

// decimalRatio returns a canonical, bounded fixed-point decimal without using
// float64. PostgreSQL NUMERIC(78,18) is the persisted boundary for stats@3.
func decimalRatio(numerator, denominator *big.Int, scale int) string {
	if numerator == nil || denominator == nil || denominator.Sign() <= 0 || scale < 0 {
		return "0"
	}
	ratio := new(big.Rat).SetFrac(numerator, denominator).FloatString(scale)
	ratio = strings.TrimRight(strings.TrimRight(ratio, "0"), ".")
	if ratio == "" {
		return "0"
	}
	return ratio
}

type statsReceiptFacts struct {
	GasUsed            *big.Int
	ExecutionFee       *big.Int
	BlobGasUsed        *big.Int
	BlobGasPrice       *big.Int
	FailedTransactions int64
	ContractCreations  int64
}

var maxStatsUint256 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

func readStatsReceiptFacts(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	expectedCount int64,
) (statsReceiptFacts, error) {
	rows, err := tx.QueryContext(ctx, dbgen.EnrichLegacyStatsReceiptSource, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:])
	if err != nil {
		return statsReceiptFacts{}, fmt.Errorf("query stats source receipts: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	facts := statsReceiptFacts{
		GasUsed:      new(big.Int),
		ExecutionFee: new(big.Int),
		BlobGasUsed:  new(big.Int),
	}
	var count int64
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return statsReceiptFacts{}, fmt.Errorf("scan stats source receipt: %w", err)
		}
		count++
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return statsReceiptFacts{}, Permanent(fmt.Errorf("decode stats receipt fields: %w", err))
		}
		blobGasUsedPresent := jsonValuePresent(fields["blobGasUsed"])
		blobGasPricePresent := jsonValuePresent(fields["blobGasPrice"])
		effectiveGasPricePresent := jsonValuePresent(fields["effectiveGasPrice"])
		var receipt types.Receipt
		if err := json.Unmarshal(raw, &receipt); err != nil {
			return statsReceiptFacts{}, Permanent(fmt.Errorf("decode stats receipt: %w", err))
		}
		if receipt.Status != types.ReceiptStatusSuccessful && receipt.Status != types.ReceiptStatusFailed {
			return statsReceiptFacts{}, Permanent(errors.New("stats receipt has an invalid status"))
		}
		if !effectiveGasPricePresent || receipt.EffectiveGasPrice == nil || receipt.EffectiveGasPrice.Sign() < 0 {
			return statsReceiptFacts{}, Permanent(errors.New("stats receipt has no authenticated effective gas price"))
		}
		if receipt.EffectiveGasPrice.Cmp(maxStatsUint256) > 0 {
			return statsReceiptFacts{}, Permanent(errors.New("stats receipt effective gas price exceeds uint256"))
		}
		used := new(big.Int).SetUint64(receipt.GasUsed)
		facts.GasUsed.Add(facts.GasUsed, used)
		facts.ExecutionFee.Add(facts.ExecutionFee, new(big.Int).Mul(used, receipt.EffectiveGasPrice))
		if facts.ExecutionFee.Cmp(maxStatsUint256) > 0 {
			return statsReceiptFacts{}, Permanent(errors.New("stats execution fee exceeds uint256"))
		}
		if receipt.Status == types.ReceiptStatusFailed {
			facts.FailedTransactions++
		} else if receipt.ContractAddress != (common.Address{}) {
			facts.ContractCreations++
		}
		if !blobGasUsedPresent && !blobGasPricePresent {
			continue
		}
		if !blobGasUsedPresent || !blobGasPricePresent || receipt.BlobGasPrice == nil {
			return statsReceiptFacts{}, Permanent(errors.New("stats receipt has an incomplete blob fee observation"))
		}
		used = new(big.Int).SetUint64(receipt.BlobGasUsed)
		currentPrice := receipt.BlobGasPrice
		if used.Sign() <= 0 || currentPrice.Sign() <= 0 {
			return statsReceiptFacts{}, Permanent(errors.New("stats receipt has non-positive blob fee facts"))
		}
		if currentPrice.Cmp(maxStatsUint256) > 0 ||
			new(big.Int).Mul(used, currentPrice).Cmp(maxStatsUint256) > 0 {
			return statsReceiptFacts{}, Permanent(errors.New("stats blob fee exceeds uint256"))
		}
		if facts.BlobGasPrice != nil && facts.BlobGasPrice.Cmp(currentPrice) != 0 {
			return statsReceiptFacts{}, Permanent(errors.New("stats receipts disagree on blob gas price"))
		}
		facts.BlobGasPrice = new(big.Int).Set(currentPrice)
		facts.BlobGasUsed.Add(facts.BlobGasUsed, used)
	}
	if err := rows.Err(); err != nil {
		return statsReceiptFacts{}, fmt.Errorf("iterate stats source receipts: %w", err)
	}
	if count != expectedCount {
		return statsReceiptFacts{}, Permanent(errors.New("stats receipt count does not match transaction count"))
	}
	return facts, nil
}

func jsonValuePresent(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) != 0 && !bytes.Equal(trimmed, []byte("null"))
}

func lockCanonicalBlock(ctx context.Context, tx *sql.Tx, job Job) (bool, error) {
	var locked int
	err := tx.QueryRowContext(ctx, dbgen.EnrichLegacyLockCanonicalBlock, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:]).Scan(&locked)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock canonical block: %w", err)
	}
	return locked == 1, nil
}

func commitStageResult(ctx context.Context, tx *sql.Tx, job Job, result StageResult) (StageResult, error) {
	if err := result.validateForFinish(); err != nil {
		return StageResult{}, err
	}
	if err := persistStageResultTx(ctx, tx, job, result); err != nil {
		return StageResult{}, err
	}
	journal, err := encodeDerivedJournal(job.Stage)
	if err != nil {
		return StageResult{}, err
	}
	journalResult, err := tx.ExecContext(ctx, dbgen.EnrichLegacyUpsertDerivedJournal, job.ChainID, job.BlockHash[:], job.Stage.String(), derivedJournalSequence,
		string(journal), strconv.FormatUint(job.BlockNumber, 10),
	)
	if err != nil {
		return StageResult{}, fmt.Errorf("persist block stage journal: %w", err)
	}
	if err := requireDirectStageWrite(journalResult); err != nil {
		return StageResult{}, fmt.Errorf("persist block stage journal: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return StageResult{}, fmt.Errorf("commit block stage: %w", err)
	}
	return result, nil
}

// persistStageResultTx writes the nullable-identity marker used only by direct
// processor fixtures. Durable queue completion uses the lease-bound publication
// helper so every terminal marker carries its exact job and generation.
func persistStageResultTx(ctx context.Context, tx *sql.Tx, job Job, result StageResult) error {
	if tx == nil {
		return errors.New("persist block stage result using nil transaction")
	}
	if err := job.Validate(); err != nil {
		return err
	}
	if err := result.validateForFinish(); err != nil {
		return err
	}
	details := result.Details
	if details == nil {
		details = map[string]string{}
	}
	encodedDetails, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode block stage details: %w", err)
	}
	var lastError any
	if result.Error != "" {
		lastError = result.Error
	}
	writeResult, err := tx.ExecContext(ctx, dbgen.EnrichLegacyInsertStageResult, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		job.Stage.Name, job.Stage.Version, result.State, string(encodedDetails), lastError,
	)
	if err != nil {
		return fmt.Errorf("persist block stage result: %w", err)
	}
	return requireDirectStageWrite(writeResult)
}

func requireDirectStageWrite(result sql.Result) error {
	if result == nil {
		return errors.New("direct block stage write returned no result")
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read direct block stage write count: %w", err)
	}
	if affected != 1 {
		return ErrAtomicPublicationRequired
	}
	return nil
}
