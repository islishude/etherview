package enrich

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/islishude/etherview/internal/ethrpc"
)

var (
	StateDiffStage        = StageID{Name: "state_diff", Version: 1}
	ErrStateDiffLimit     = errors.New("state difference exceeds configured limit")
	errStateDiffRPCAbsent = errors.New("state difference RPC capability unavailable")
)

type StateDiffLimits struct {
	MaxPayloadBytes      int
	MaxAccounts          int
	MaxStorageSlots      int
	MaxCodeBytes         int
	MaxTextBytes         int
	MaxBlockPayloadBytes int
	MaxBlockAccounts     int
	MaxBlockStorageSlots int
	MaxBlockCodeBytes    int
	MaxBlockTextBytes    int
}

func DefaultStateDiffLimits() StateDiffLimits {
	return StateDiffLimits{
		MaxPayloadBytes: 8 << 20, MaxAccounts: 10_000,
		MaxStorageSlots: 100_000, MaxCodeBytes: 8 << 20,
		MaxTextBytes:         16 << 20,
		MaxBlockPayloadBytes: 32 << 20, MaxBlockAccounts: 20_000,
		MaxBlockStorageSlots: 200_000, MaxBlockCodeBytes: 16 << 20,
		MaxBlockTextBytes: 64 << 20,
	}
}

func (limits StateDiffLimits) validate() error {
	if limits.MaxPayloadBytes <= 0 || limits.MaxAccounts <= 0 ||
		limits.MaxStorageSlots <= 0 || limits.MaxCodeBytes <= 0 || limits.MaxTextBytes <= 0 ||
		limits.MaxBlockPayloadBytes <= 0 || limits.MaxBlockAccounts <= 0 ||
		limits.MaxBlockStorageSlots <= 0 || limits.MaxBlockCodeBytes <= 0 ||
		limits.MaxBlockTextBytes <= 0 {
		return errors.New("all state difference limits must be positive")
	}
	return nil
}

type StateDiffRPCProcessor struct {
	db     *sql.DB
	pool   *ethrpc.Pool
	limits StateDiffLimits
}

func NewStateDiffRPCProcessor(db *sql.DB, pool *ethrpc.Pool, limits StateDiffLimits) (*StateDiffRPCProcessor, error) {
	if db == nil || pool == nil {
		return nil, errors.New("state difference processor requires a database and RPC pool")
	}
	if limits == (StateDiffLimits{}) {
		limits = DefaultStateDiffLimits()
	}
	if err := limits.validate(); err != nil {
		return nil, err
	}
	return &StateDiffRPCProcessor{db: db, pool: pool, limits: limits}, nil
}

func (*StateDiffRPCProcessor) Stage() StageID { return StateDiffStage }

func (processor *StateDiffRPCProcessor) ProcessLease(
	ctx context.Context,
	lease Lease,
	queue *PostgresJobQueue,
) (StageResult, error) {
	return processor.Process(ctx, bindStagePublication(lease.Job, lease, queue))
}

type stateDiffTransaction struct {
	index   uint64
	hash    common.Hash
	changes []stateChange
}

type stateChange struct {
	address common.Address
	kind    string
	key     []byte
	before  *string
	after   *string
}

type stateDiffWire struct {
	Pre  map[string]json.RawMessage `json:"pre"`
	Post map[string]json.RawMessage `json:"post"`
}

type stateAccountWire struct {
	Balance *string           `json:"balance"`
	Nonce   json.RawMessage   `json:"nonce"`
	Code    *string           `json:"code"`
	Storage map[string]string `json:"storage"`
}

type stateAccountPair struct {
	address common.Address
	pre     json.RawMessage
	post    json.RawMessage
	hasPre  bool
	hasPost bool
}

type stateDiffBudget struct {
	payload  int
	accounts int
	slots    int
	code     int
	text     int
}

func (processor *StateDiffRPCProcessor) Process(ctx context.Context, job Job) (StageResult, error) {
	if processor == nil || processor.db == nil || processor.pool == nil {
		return StageResult{}, errors.New("process state difference stage using unconfigured processor")
	}
	if err := job.Validate(); err != nil {
		return StageResult{}, Permanent(err)
	}
	if job.Stage != StateDiffStage {
		return StageResult{}, Permanent(fmt.Errorf("state difference processor received stage %s", job.Stage))
	}
	transactions, canonical, err := processor.transactions(ctx, job)
	if err != nil {
		return StageResult{}, err
	}
	if !canonical {
		return processor.persist(ctx, job, nil, "stale_canonical_skipped")
	}
	if len(transactions) == 0 {
		return processor.persist(ctx, job, transactions, "complete")
	}
	endpoint, err := processor.pool.Acquire(ethrpc.PurposeTrace)
	if err != nil {
		return StageResult{}, Unavailable(err)
	}
	if endpoint.Capabilities.Status(ethrpc.CapabilityDebugTrace) == ethrpc.AvailabilityUnavailable {
		return StageResult{}, Unavailable(errStateDiffRPCAbsent)
	}
	budget := &stateDiffBudget{}
	for index := range transactions {
		var raw json.RawMessage
		if err := endpoint.Client.CallContext(ctx, &raw, "debug_traceTransaction",
			transactions[index].hash.String(),
			map[string]any{
				"tracer":       "prestateTracer",
				"tracerConfig": map[string]any{"diffMode": true},
			},
		); err != nil {
			processor.pool.ReportFailure(endpoint.Name)
			if traceCapabilityUnavailable(err) {
				return StageResult{}, Unavailable(errStateDiffRPCAbsent)
			}
			return StageResult{}, ethrpc.SanitizeError(err)
		}
		changes, counts, err := normalizeStateDiff(raw, processor.limits)
		if err != nil {
			processor.pool.ReportFailure(endpoint.Name)
			return StageResult{}, Permanent(fmt.Errorf("normalize transaction state difference: %w", err))
		}
		if err := budget.add(len(raw), counts, processor.limits); err != nil {
			processor.pool.ReportFailure(endpoint.Name)
			return StageResult{}, Permanent(err)
		}
		transactions[index].changes = changes
	}
	processor.pool.ReportSuccess(endpoint.Name)
	return processor.persist(ctx, job, transactions, "complete")
}

func (processor *StateDiffRPCProcessor) transactions(
	ctx context.Context,
	job Job,
) ([]stateDiffTransaction, bool, error) {
	var canonical bool
	if err := processor.db.QueryRowContext(ctx, traceCanonicalSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
	).Scan(&canonical); err != nil {
		return nil, false, fmt.Errorf("check state difference block canonicality: %w", err)
	}
	if !canonical {
		return nil, false, nil
	}
	rows, err := processor.db.QueryContext(ctx, stateDiffTransactionsSQL,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
	)
	if err != nil {
		return nil, false, fmt.Errorf("query state difference transactions: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	var result []stateDiffTransaction
	for rows.Next() {
		var index int64
		var hashBytes []byte
		if err := rows.Scan(&index, &hashBytes); err != nil {
			return nil, false, fmt.Errorf("scan state difference transaction: %w", err)
		}
		if index < 0 || len(hashBytes) != common.HashLength {
			return nil, false, Permanent(errors.New("state difference transaction identity is invalid"))
		}
		result = append(result, stateDiffTransaction{
			index: uint64(index), hash: common.BytesToHash(hashBytes),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate state difference transactions: %w", err)
	}
	return result, true, nil
}

type normalizedStateDiffCounts struct {
	accounts int
	slots    int
	code     int
	text     int
}

func normalizeStateDiff(raw json.RawMessage, limits StateDiffLimits) ([]stateChange, normalizedStateDiffCounts, error) {
	if len(raw) > limits.MaxPayloadBytes {
		return nil, normalizedStateDiffCounts{}, ErrStateDiffLimit
	}
	var wire stateDiffWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, normalizedStateDiffCounts{}, errors.New("invalid state difference JSON")
	}
	if wire.Pre == nil || wire.Post == nil {
		return nil, normalizedStateDiffCounts{}, errors.New("state difference must contain pre and post objects")
	}
	accounts := make(map[common.Address]*stateAccountPair, len(wire.Pre)+len(wire.Post))
	for addressText, rawAccount := range wire.Pre {
		address, err := ethrpc.ParseAddress(addressText)
		if err != nil {
			return nil, normalizedStateDiffCounts{}, errors.New("state difference contains invalid account address")
		}
		pair := accounts[address]
		if pair == nil {
			pair = &stateAccountPair{address: address}
			accounts[address] = pair
		}
		if pair.hasPre {
			return nil, normalizedStateDiffCounts{}, errors.New("state difference contains duplicate account address")
		}
		pair.pre, pair.hasPre = rawAccount, true
	}
	for addressText, rawAccount := range wire.Post {
		address, err := ethrpc.ParseAddress(addressText)
		if err != nil {
			return nil, normalizedStateDiffCounts{}, errors.New("state difference contains invalid account address")
		}
		pair := accounts[address]
		if pair == nil {
			pair = &stateAccountPair{address: address}
			accounts[address] = pair
		}
		if pair.hasPost {
			return nil, normalizedStateDiffCounts{}, errors.New("state difference contains duplicate account address")
		}
		pair.post, pair.hasPost = rawAccount, true
	}
	if len(accounts) > limits.MaxAccounts {
		return nil, normalizedStateDiffCounts{}, ErrStateDiffLimit
	}
	counts := normalizedStateDiffCounts{accounts: len(accounts)}
	var changes []stateChange
	for _, pair := range accounts {
		pre, err := decodeStateAccount(pair.pre)
		if err != nil {
			return nil, normalizedStateDiffCounts{}, err
		}
		post, err := decodeStateAccount(pair.post)
		if err != nil {
			return nil, normalizedStateDiffCounts{}, err
		}
		preNonce, err := canonicalStateNonce(pre.Nonce)
		if err != nil {
			return nil, normalizedStateDiffCounts{}, errors.New("state difference contains invalid quantity")
		}
		postNonce, err := canonicalStateNonce(post.Nonce)
		if err != nil {
			return nil, normalizedStateDiffCounts{}, errors.New("state difference contains invalid quantity")
		}
		for _, field := range []struct {
			kind   string
			before *string
			after  *string
		}{
			{kind: "balance", before: canonicalStateQuantity(pre.Balance), after: canonicalStateQuantity(post.Balance)},
			{kind: "nonce", before: preNonce, after: postNonce},
			{kind: "code", before: canonicalStateCode(pre.Code, &counts), after: canonicalStateCode(post.Code, &counts)},
		} {
			if field.kind == "balance" || field.kind == "nonce" {
				if (field.before != nil && *field.before == "") || (field.after != nil && *field.after == "") {
					return nil, normalizedStateDiffCounts{}, errors.New("state difference contains invalid quantity")
				}
			}
			if field.kind == "code" {
				if (field.before != nil && *field.before == "") || (field.after != nil && *field.after == "") {
					return nil, normalizedStateDiffCounts{}, errors.New("state difference contains invalid code")
				}
				if counts.code > limits.MaxCodeBytes {
					return nil, normalizedStateDiffCounts{}, ErrStateDiffLimit
				}
			}
			if stateValuesEqual(field.before, field.after) {
				continue
			}
			changes = append(changes, stateChange{
				address: pair.address, kind: field.kind, key: []byte{},
				before: field.before, after: field.after,
			})
		}
		storageKeys := make(map[string]struct{}, len(pre.Storage)+len(post.Storage))
		for key := range pre.Storage {
			storageKeys[key] = struct{}{}
		}
		for key := range post.Storage {
			storageKeys[key] = struct{}{}
		}
		counts.slots += len(storageKeys)
		if counts.slots > limits.MaxStorageSlots {
			return nil, normalizedStateDiffCounts{}, ErrStateDiffLimit
		}
		for keyText := range storageKeys {
			key, err := ethrpc.ParseHash(keyText)
			if err != nil {
				return nil, normalizedStateDiffCounts{}, errors.New("state difference contains invalid storage key")
			}
			before, err := canonicalStorageValue(pre.Storage, keyText)
			if err != nil {
				return nil, normalizedStateDiffCounts{}, err
			}
			after, err := canonicalStorageValue(post.Storage, keyText)
			if err != nil {
				return nil, normalizedStateDiffCounts{}, err
			}
			if stateValuesEqual(before, after) {
				continue
			}
			changes = append(changes, stateChange{
				address: pair.address, kind: "storage", key: key[:], before: before, after: after,
			})
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if comparison := bytes.Compare(changes[i].address[:], changes[j].address[:]); comparison != 0 {
			return comparison < 0
		}
		if changes[i].kind != changes[j].kind {
			return changes[i].kind < changes[j].kind
		}
		return bytes.Compare(changes[i].key, changes[j].key) < 0
	})
	for _, change := range changes {
		counts.text += len(change.kind)
		if change.before != nil {
			counts.text += len(*change.before)
		}
		if change.after != nil {
			counts.text += len(*change.after)
		}
	}
	if counts.text > limits.MaxTextBytes {
		return nil, normalizedStateDiffCounts{}, ErrStateDiffLimit
	}
	return changes, counts, nil
}

func decodeStateAccount(raw json.RawMessage) (stateAccountWire, error) {
	if len(raw) == 0 {
		return stateAccountWire{}, nil
	}
	var account stateAccountWire
	if err := json.Unmarshal(raw, &account); err != nil {
		return stateAccountWire{}, errors.New("state difference contains invalid account")
	}
	return account, nil
}

func canonicalStateQuantity(value *string) *string {
	if value == nil {
		return nil
	}
	quantity, err := ethrpc.ParseQuantity(*value)
	if err != nil {
		invalid := ""
		return &invalid
	}
	canonical := quantity.String()
	return &canonical
}

func canonicalStateNonce(raw json.RawMessage) (*string, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	if raw[0] == '"' {
		var encoded string
		if err := json.Unmarshal(raw, &encoded); err != nil {
			return nil, err
		}
		quantity, err := ethrpc.ParseQuantity(encoded)
		if err != nil || !quantity.IsUint64() {
			return nil, ethrpc.ErrInvalidQuantity
		}
		canonical := quantity.String()
		return &canonical, nil
	}
	var nonce uint64
	if err := json.Unmarshal(raw, &nonce); err != nil {
		return nil, err
	}
	canonical := strconv.FormatUint(nonce, 10)
	return &canonical, nil
}

func canonicalStateCode(value *string, counts *normalizedStateDiffCounts) *string {
	if value == nil {
		return nil
	}
	decoded, err := ethrpc.ParseData(*value)
	if err != nil {
		invalid := ""
		return &invalid
	}
	counts.code += len(decoded)
	canonical := hexutil.Encode(decoded)
	return &canonical
}

func canonicalStorageValue(storage map[string]string, key string) (*string, error) {
	value, exists := storage[key]
	if !exists {
		return nil, nil
	}
	word, err := ethrpc.ParseHash(value)
	if err != nil {
		return nil, errors.New("state difference contains invalid storage value")
	}
	canonical := word.Hex()
	return &canonical, nil
}

func stateValuesEqual(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (budget *stateDiffBudget) add(payload int, counts normalizedStateDiffCounts, limits StateDiffLimits) error {
	budget.payload += payload
	budget.accounts += counts.accounts
	budget.slots += counts.slots
	budget.code += counts.code
	budget.text += counts.text
	if budget.payload > limits.MaxBlockPayloadBytes || budget.accounts > limits.MaxBlockAccounts ||
		budget.slots > limits.MaxBlockStorageSlots || budget.code > limits.MaxBlockCodeBytes ||
		budget.text > limits.MaxBlockTextBytes {
		return ErrStateDiffLimit
	}
	return nil
}

func (processor *StateDiffRPCProcessor) persist(
	ctx context.Context,
	job Job,
	transactions []stateDiffTransaction,
	outcome string,
) (StageResult, error) {
	return runStageTransaction(ctx, processor.db, job, func(ctx context.Context, tx *sql.Tx) (StageResult, error) {
		canonical, err := lockCanonicalBlock(ctx, tx, job)
		if err != nil {
			return StageResult{}, err
		}
		if !canonical {
			outcome = "stale_canonical_skipped"
			transactions = nil
		}
		if canonical {
			if _, err := tx.ExecContext(ctx, deleteStateDiffBlockSQL,
				job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
			); err != nil {
				return StageResult{}, fmt.Errorf("clear previous transaction state differences: %w", err)
			}
		}
		changeCount := 0
		for _, transaction := range transactions {
			for _, change := range transaction.changes {
				if _, err := tx.ExecContext(ctx, insertStateChangeSQL,
					job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
					transaction.hash[:], transaction.index, change.address[:], change.kind,
					change.key, nullableStateValue(change.before), nullableStateValue(change.after),
				); err != nil {
					return StageResult{}, fmt.Errorf("persist transaction state difference: %w", err)
				}
				changeCount++
			}
		}
		return StageResult{State: ResultComplete, Details: map[string]string{
			"outcome": outcome, "source": "prestate_tracer",
			"transactions": strconv.Itoa(len(transactions)), "changes": strconv.Itoa(changeCount),
		}}, nil
	})
}

func nullableStateValue(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

const stateDiffTransactionsSQL = `
SELECT tx_index, tx_hash
FROM transaction_inclusions
WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3
ORDER BY tx_index`

const deleteStateDiffBlockSQL = `
DELETE FROM transaction_state_changes
WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3`

const insertStateChangeSQL = `
INSERT INTO transaction_state_changes (
    chain_id, block_number, block_hash, transaction_hash, transaction_index,
    address, field_kind, storage_key, before_value, after_value, canonical
) VALUES (
    $1::numeric, $2::numeric, $3, $4, $5, $6, $7, $8, $9, $10, true
)`
