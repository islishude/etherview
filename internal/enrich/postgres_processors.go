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

	"github.com/islishude/etherview/internal/ethrpc"
)

var (
	TokenStage = StageID{Name: "token", Version: 1}
	// stats@2 adds timestamp/cadence plus EIP-4844 fee observations. Keeping a
	// new durable identity prevents old stats@1 completions from claiming the
	// expanded persisted contract.
	StatsStage = StageID{Name: "stats", Version: 2}
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

	rows, err := tx.QueryContext(ctx, tokenLogsSQL, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:])
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
	detections := make(map[Address]TokenDetection, len(addresses))
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
	if err := processor.db.QueryRowContext(ctx, tokenCanonicalSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
	).Scan(&canonical); err != nil {
		return false, fmt.Errorf("check token block canonicality: %w", err)
	}
	return canonical, nil
}

func (processor *PostgresTokenProcessor) collectTokenEvidence(ctx context.Context, job Job) (map[Address]TokenLogEvidence, error) {
	rows, err := processor.db.QueryContext(ctx, tokenLogsSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
	)
	if err != nil {
		return nil, fmt.Errorf("query token detection logs: %w", err)
	}
	storedLogs, err := readStoredTokenLogs(rows, job)
	if err != nil {
		return nil, err
	}
	evidence := make(map[Address]TokenLogEvidence)
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

func (processor *PostgresTokenProcessor) persistDetectedTokenBlock(ctx context.Context, job Job, detections map[Address]TokenDetection) (StageResult, error) {
	return runStageTransaction(ctx, processor.db, job, func(ctx context.Context, tx *sql.Tx) (StageResult, error) {
		return processor.persistDetectedTokenBlockTx(ctx, tx, job, detections)
	})
}

func (processor *PostgresTokenProcessor) persistDetectedTokenBlockTx(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	detections map[Address]TokenDetection,
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
	rows, err := tx.QueryContext(ctx, tokenLogsSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
	)
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

func sortedTokenAddresses[Value any](values map[Address]Value) []Address {
	addresses := make([]Address, 0, len(values))
	for address := range values {
		addresses = append(addresses, address)
	}
	slices.SortFunc(addresses, func(left, right Address) int {
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
	if logIndex < 0 || len(transactionHash) != 32 || len(address) != 20 {
		return TokenLog{}, nil, nil, Permanent(errors.New("stored token log identity is invalid"))
	}
	var wire ethrpc.Log
	if err := json.Unmarshal(raw, &wire); err != nil {
		return TokenLog{}, nil, nil, Permanent(fmt.Errorf("decode token log: %w", err))
	}
	tokenLog, err := indexedTokenLog(wire, uint64(logIndex), transactionHash, address, job)
	if err != nil {
		return TokenLog{}, nil, nil, Permanent(err)
	}
	return tokenLog, transactionHash, raw, nil
}

func persistTokenContract(ctx context.Context, tx *sql.Tx, job Job, address Address, detection TokenDetection) error {
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
	if _, err := tx.ExecContext(ctx, upsertTokenContractSQL,
		job.ChainID, address[:], detection.CodeHash[:], detection.Standard, detection.Confidence,
		name, symbol, decimals, totalSupply, detection.MetadataState,
		strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
	); err != nil {
		return fmt.Errorf("persist detected token contract: %w", err)
	}
	return nil
}

func indexedTokenLog(wire ethrpc.Log, logIndex uint64, transactionHash, address []byte, job Job) (TokenLog, error) {
	if wire.LogIndex == nil {
		return TokenLog{}, errors.New("stored token log raw identity is incomplete")
	}
	wireIndex, err := wire.LogIndex.Uint64()
	if err != nil || wireIndex != logIndex || wire.TransactionHash == nil || wire.BlockHash == nil || wire.BlockNumber == nil {
		return TokenLog{}, errors.New("stored token log raw identity is incomplete")
	}
	wireTransactionHash, err := wire.TransactionHash.Bytes()
	if err != nil || !equalBytes(wireTransactionHash, transactionHash) {
		return TokenLog{}, errors.New("stored token log transaction hash mismatch")
	}
	wireBlockHash, err := wire.BlockHash.Bytes()
	if err != nil || !equalBytes(wireBlockHash, job.BlockHash[:]) {
		return TokenLog{}, errors.New("stored token log block hash mismatch")
	}
	wireBlockNumber, err := wire.BlockNumber.Uint64()
	if err != nil || wireBlockNumber != job.BlockNumber {
		return TokenLog{}, errors.New("stored token log block number mismatch")
	}
	wireAddress, err := wire.Address.Bytes()
	if err != nil || !equalBytes(wireAddress, address) {
		return TokenLog{}, errors.New("stored token log address mismatch")
	}
	contract, err := ParseAddress(wire.Address.String())
	if err != nil {
		return TokenLog{}, err
	}
	data, err := wire.Data.Bytes()
	if err != nil {
		return TokenLog{}, fmt.Errorf("decode token log data: %w", err)
	}
	topics := make([]Word, len(wire.Topics))
	for index, topic := range wire.Topics {
		topics[index], err = ParseWord(topic.String())
		if err != nil {
			return TokenLog{}, fmt.Errorf("decode token log topic %d: %w", index, err)
		}
	}
	return TokenLog{Contract: contract, Topics: topics, Data: data, LogIndex: logIndex}, nil
}

func detectedToken(ctx context.Context, tx *sql.Tx, job Job, address Address) (TokenStandard, Confidence, bool, error) {
	var standard, confidence string
	err := tx.QueryRowContext(ctx, detectedTokenSQL,
		job.ChainID, address[:], strconv.FormatUint(job.BlockNumber, 10),
	).Scan(&standard, &confidence)
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
	_, err := tx.ExecContext(ctx, insertTokenEventSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
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
	deltas := make(map[Address]*big.Int, 2)
	add := func(owner *Address, delta *big.Int) {
		if owner == nil || *owner == (Address{}) {
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
		_, err := tx.ExecContext(ctx, insertTokenDeltaSQL,
			job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:], event.LogIndex,
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
	if err := tx.QueryRowContext(ctx, blockStatsSourceSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
	).Scan(&raw, &transactionCount, &configuredStart, &parentNumber, &parentTimestamp, &canonicalParent); err != nil {
		return StageResult{}, fmt.Errorf("query stats source block: %w", err)
	}
	if transactionCount < 0 {
		return StageResult{}, Permanent(errors.New("negative transaction count"))
	}
	var block ethrpc.Block
	if err := json.Unmarshal(raw, &block); err != nil {
		return StageResult{}, Permanent(fmt.Errorf("decode stats block: %w", err))
	}
	if block.Hash == nil || block.Number == nil || !strings.EqualFold(block.Hash.String(), job.BlockHash.String()) {
		return StageResult{}, Permanent(errors.New("stats block identity mismatch"))
	}
	number, err := block.Number.Uint64()
	if err != nil || number != job.BlockNumber {
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
	gasUsed, err := block.GasUsed.Big()
	if err != nil {
		return StageResult{}, Permanent(fmt.Errorf("decode block gas used: %w", err))
	}
	gasLimit, err := block.GasLimit.Big()
	if err != nil {
		return StageResult{}, Permanent(fmt.Errorf("decode block gas limit: %w", err))
	}
	timestamp, err := block.Timestamp.Big()
	if err != nil {
		return StageResult{}, Permanent(fmt.Errorf("decode block timestamp: %w", err))
	}
	var blockInterval, transactionsPerSecond any
	if parentTimestamp.Valid {
		parent, ok := new(big.Int).SetString(parentTimestamp.String, 10)
		if !ok || parent.Sign() < 0 || timestamp.Cmp(parent) <= 0 {
			return StageResult{}, Permanent(errors.New("stats parent timestamp is invalid"))
		}
		interval := new(big.Int).Sub(timestamp, parent)
		blockInterval = interval.String()
		transactionsPerSecond = decimalRatio(big.NewInt(transactionCount), interval, 18)
	}
	var baseFee, blobGasUsed, excessBlobGas, blobBaseFee, burned, blobBurned any
	if block.BaseFeePerGas != nil {
		value, err := block.BaseFeePerGas.Big()
		if err != nil {
			return StageResult{}, Permanent(fmt.Errorf("decode block base fee: %w", err))
		}
		baseFee = value.String()
		burned = new(big.Int).Mul(value, gasUsed).String()
	}
	if block.BlobGasUsed != nil {
		value, err := block.BlobGasUsed.Big()
		if err != nil {
			return StageResult{}, Permanent(fmt.Errorf("decode block blob gas used: %w", err))
		}
		blobGasUsed = value.String()
	}
	if block.ExcessBlobGas != nil {
		value, err := block.ExcessBlobGas.Big()
		if err != nil {
			return StageResult{}, Permanent(fmt.Errorf("decode block excess blob gas: %w", err))
		}
		excessBlobGas = value.String()
	}
	receiptBlobGas, receiptBlobPrice, err := statsReceiptBlobFees(ctx, tx, job)
	if err != nil {
		return StageResult{}, err
	}
	if block.BlobGasUsed == nil && receiptBlobGas.Sign() > 0 {
		return StageResult{}, Permanent(errors.New("receipt blob gas is absent from the block header"))
	}
	if block.BlobGasUsed != nil {
		headerBlobGas, _ := block.BlobGasUsed.Big()
		if headerBlobGas.Cmp(receiptBlobGas) != 0 {
			return StageResult{}, Permanent(errors.New("receipt blob gas does not match the block header"))
		}
	}
	if receiptBlobPrice != nil {
		blobBaseFee = receiptBlobPrice.String()
		blobBurned = new(big.Int).Mul(receiptBlobPrice, receiptBlobGas).String()
	} else if receiptBlobGas.Sign() == 0 {
		blobBurned = "0"
	}
	if _, err := tx.ExecContext(ctx, insertBlockStatsSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:], transactionCount,
		gasUsed.String(), gasLimit.String(), baseFee, blobGasUsed, burned,
		timestamp.String(), blockInterval, transactionsPerSecond, excessBlobGas, blobBaseFee, blobBurned,
	); err != nil {
		return StageResult{}, fmt.Errorf("persist block statistics: %w", err)
	}
	return StageResult{State: ResultComplete, Details: map[string]string{"transactions": strconv.FormatInt(transactionCount, 10)}}, nil
}

// decimalRatio returns a canonical, bounded fixed-point decimal without using
// float64. PostgreSQL NUMERIC(78,18) is the persisted boundary for stats@2.
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

func statsReceiptBlobFees(ctx context.Context, tx *sql.Tx, job Job) (*big.Int, *big.Int, error) {
	rows, err := tx.QueryContext(ctx, statsReceiptSourceSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
	)
	if err != nil {
		return nil, nil, fmt.Errorf("query stats source receipts: %w", err)
	}
	defer rows.Close()
	total := new(big.Int)
	var price *big.Int
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, nil, fmt.Errorf("scan stats source receipt: %w", err)
		}
		var receipt ethrpc.Receipt
		if err := json.Unmarshal(raw, &receipt); err != nil {
			return nil, nil, Permanent(fmt.Errorf("decode stats receipt: %w", err))
		}
		if receipt.BlobGasUsed == nil && receipt.BlobGasPrice == nil {
			continue
		}
		if receipt.BlobGasUsed == nil || receipt.BlobGasPrice == nil {
			return nil, nil, Permanent(errors.New("stats receipt has an incomplete blob fee observation"))
		}
		used, err := receipt.BlobGasUsed.Big()
		if err != nil {
			return nil, nil, Permanent(fmt.Errorf("decode receipt blob gas used: %w", err))
		}
		currentPrice, err := receipt.BlobGasPrice.Big()
		if err != nil {
			return nil, nil, Permanent(fmt.Errorf("decode receipt blob gas price: %w", err))
		}
		if price != nil && price.Cmp(currentPrice) != 0 {
			return nil, nil, Permanent(errors.New("stats receipts disagree on blob gas price"))
		}
		price = new(big.Int).Set(currentPrice)
		total.Add(total, used)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate stats source receipts: %w", err)
	}
	return total, price, nil
}

func lockCanonicalBlock(ctx context.Context, tx *sql.Tx, job Job) (bool, error) {
	var locked int
	err := tx.QueryRowContext(ctx, lockCanonicalBlockSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
	).Scan(&locked)
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
	journalResult, err := tx.ExecContext(ctx, upsertDerivedJournalSQL,
		job.ChainID, job.BlockHash[:], job.Stage.String(), derivedJournalSequence,
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

// persistStageResultTx is shared by derived-output transactions and the
// durable queue's terminal lease transaction. In particular, failed and
// unavailable attempts have no output transaction of their own, so queue
// completion must write this row before releasing the lease.
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
	writeResult, err := tx.ExecContext(ctx, insertStageResultSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
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

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

const lockCanonicalBlockSQL = `
SELECT 1
FROM canonical_blocks
WHERE chain_id = $1::numeric AND number = $2::numeric AND block_hash = $3
FOR KEY SHARE`

const tokenLogsSQL = `
SELECT log_index, tx_hash, address, raw
FROM logs
WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3
ORDER BY log_index`

const tokenCanonicalSQL = `
SELECT EXISTS (
    SELECT 1
    FROM canonical_blocks
    WHERE chain_id = $1::numeric AND number = $2::numeric AND block_hash = $3
)`

const upsertTokenContractSQL = `
INSERT INTO token_contracts AS current (
    chain_id, address, code_hash, standard, confidence,
    name, symbol, decimals, total_supply, metadata_state,
    observed_block_number, observed_block_hash
) VALUES (
    $1::numeric, $2, $3, $4, $5,
    $6, $7, $8, $9::numeric, $10,
    $11::numeric, $12
)
ON CONFLICT (chain_id, address, code_hash, observed_block_hash) DO UPDATE SET
    standard = CASE
        WHEN (CASE EXCLUDED.confidence WHEN 'verified' THEN 4 WHEN 'high' THEN 3 WHEN 'inferred' THEN 2 ELSE 1 END) >
             (CASE current.confidence WHEN 'verified' THEN 4 WHEN 'high' THEN 3 WHEN 'inferred' THEN 2 ELSE 1 END)
          OR (
             EXCLUDED.confidence = current.confidence AND current.standard = 'unknown'
          )
        THEN EXCLUDED.standard
        ELSE current.standard
    END,
    confidence = CASE
        WHEN (CASE EXCLUDED.confidence WHEN 'verified' THEN 4 WHEN 'high' THEN 3 WHEN 'inferred' THEN 2 ELSE 1 END) >
             (CASE current.confidence WHEN 'verified' THEN 4 WHEN 'high' THEN 3 WHEN 'inferred' THEN 2 ELSE 1 END)
        THEN EXCLUDED.confidence
        ELSE current.confidence
    END,
    name = COALESCE(EXCLUDED.name, current.name),
    symbol = COALESCE(EXCLUDED.symbol, current.symbol),
    decimals = COALESCE(EXCLUDED.decimals, current.decimals),
    total_supply = COALESCE(EXCLUDED.total_supply, current.total_supply),
    metadata_state = CASE
        WHEN EXCLUDED.metadata_state = 'complete' OR current.metadata_state = 'complete' THEN 'complete'
        ELSE EXCLUDED.metadata_state
    END,
    updated_at = now()`

const detectedTokenSQL = `
SELECT token.standard, token.confidence
FROM token_contracts AS token
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = token.chain_id
 AND canonical.number = token.observed_block_number
 AND canonical.block_hash = token.observed_block_hash
WHERE token.chain_id = $1::numeric
  AND token.address = $2
  AND token.observed_block_number <= $3::numeric
  AND token.standard <> 'unknown'
ORDER BY token.observed_block_number DESC, token.updated_at DESC
LIMIT 1`

const insertTokenEventSQL = `
INSERT INTO token_events (
    chain_id, block_number, block_hash, log_index, sub_index, transaction_hash,
    token_address, standard, event_kind, operator, from_address, to_address,
    token_id, amount, canonical, confidence, raw
) VALUES (
    $1::numeric, $2::numeric, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
    $13::numeric, $14::numeric, true, $15, $16::jsonb
)
ON CONFLICT (chain_id, block_number, block_hash, log_index, sub_index) DO UPDATE SET
    transaction_hash = EXCLUDED.transaction_hash,
    token_address = EXCLUDED.token_address,
    standard = EXCLUDED.standard,
    event_kind = EXCLUDED.event_kind,
    operator = EXCLUDED.operator,
    from_address = EXCLUDED.from_address,
    to_address = EXCLUDED.to_address,
    token_id = EXCLUDED.token_id,
    amount = EXCLUDED.amount,
    canonical = true,
    confidence = EXCLUDED.confidence,
    raw = EXCLUDED.raw`

const insertTokenDeltaSQL = `
INSERT INTO token_balance_deltas (
    chain_id, block_number, block_hash, log_index, sub_index,
    token_address, owner_address, token_id, delta, canonical
) VALUES ($1::numeric, $2::numeric, $3, $4, $5, $6, $7, $8::numeric, $9::numeric, true)
ON CONFLICT (
    chain_id, block_number, block_hash, log_index, sub_index, token_address, owner_address
) DO UPDATE SET token_id = EXCLUDED.token_id, delta = EXCLUDED.delta, canonical = true`

const blockStatsSourceSQL = `
SELECT block.raw, count(inclusion.tx_index), configuration.configured_start::text,
       parent.number::text, parent.timestamp::text,
       COALESCE(bool_or(canonical_parent.block_hash IS NOT NULL), FALSE)
FROM blocks AS block
JOIN core_index_configuration AS configuration
  ON configuration.chain_id = block.chain_id
LEFT JOIN transaction_inclusions AS inclusion
  ON inclusion.chain_id = block.chain_id
 AND inclusion.block_number = block.number
 AND inclusion.block_hash = block.hash
LEFT JOIN blocks AS parent
  ON parent.chain_id = block.chain_id
 AND parent.hash = block.parent_hash
LEFT JOIN canonical_blocks AS canonical_parent
  ON canonical_parent.chain_id = parent.chain_id
 AND canonical_parent.number = parent.number
 AND canonical_parent.block_hash = parent.hash
WHERE block.chain_id = $1::numeric AND block.number = $2::numeric AND block.hash = $3
GROUP BY block.raw, configuration.configured_start, parent.number, parent.timestamp`

const statsReceiptSourceSQL = `
SELECT receipt.raw
FROM receipts AS receipt
WHERE receipt.chain_id = $1::numeric
  AND receipt.block_number = $2::numeric
  AND receipt.block_hash = $3
ORDER BY receipt.tx_index`

const insertBlockStatsSQL = `
INSERT INTO block_statistics (
    chain_id, block_number, block_hash, transaction_count, gas_used, gas_limit,
    base_fee_per_gas, blob_gas_used, burned_wei, block_timestamp,
    block_interval_seconds, transactions_per_second, excess_blob_gas,
    blob_base_fee_per_gas, blob_burned_wei, canonical
) VALUES (
    $1::numeric, $2::numeric, $3, $4, $5::numeric, $6::numeric, $7::numeric,
    $8::numeric, $9::numeric, $10::numeric, $11::numeric, $12::numeric,
    $13::numeric, $14::numeric, $15::numeric, true
)
ON CONFLICT (chain_id, block_number, block_hash) DO UPDATE SET
    transaction_count = EXCLUDED.transaction_count,
    gas_used = EXCLUDED.gas_used,
    gas_limit = EXCLUDED.gas_limit,
    base_fee_per_gas = EXCLUDED.base_fee_per_gas,
    blob_gas_used = EXCLUDED.blob_gas_used,
    burned_wei = EXCLUDED.burned_wei,
    block_timestamp = EXCLUDED.block_timestamp,
    block_interval_seconds = EXCLUDED.block_interval_seconds,
    transactions_per_second = EXCLUDED.transactions_per_second,
    excess_blob_gas = EXCLUDED.excess_blob_gas,
    blob_base_fee_per_gas = EXCLUDED.blob_base_fee_per_gas,
    blob_burned_wei = EXCLUDED.blob_burned_wei,
    canonical = true,
    computed_at = now()`

const insertStageResultSQL = `
INSERT INTO block_stage_results AS current (
    chain_id, block_number, block_hash, stage, stage_version, state, details, last_error
) VALUES ($1::numeric, $2::numeric, $3, $4, $5, $6, $7::jsonb, $8)
ON CONFLICT (chain_id, block_hash, stage, stage_version) DO UPDATE SET
    block_number = EXCLUDED.block_number,
    state = EXCLUDED.state,
    details = EXCLUDED.details,
    last_error = EXCLUDED.last_error,
    completed_at = now()
WHERE current.durable_job_id IS NULL
  AND current.job_generation IS NULL`
