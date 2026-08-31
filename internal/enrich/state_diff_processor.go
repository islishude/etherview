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
	"sort"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/chainbundle"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
)

var (
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
	index       uint64
	hash        common.Hash
	raw         json.RawMessage
	tx          *types.Transaction
	sender      common.Address
	changes     []stateChange
	authorities []eip7702AuthorizationResult
	executions  []executionCodeResolution
}

type eip7702AuthorizationResult struct {
	index             uint64
	authorization     types.SetCodeAuthorization
	authority         *common.Address
	signatureStatus   string
	applicationStatus string
	skipReason        string
}

type executionCodeResolution struct {
	context        common.Address
	execution      *common.Address
	codeHash       *common.Hash
	resolution     string
	evidenceSource string
}

type stateAccountEvidence struct {
	nonce uint64
	code  []byte
}

type transactionStateEvidence struct {
	pre  map[common.Address]stateAccountEvidence
	post map[common.Address]stateAccountEvidence
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
	if err := processor.fetch(ctx, endpoint.Client, job, transactions, budget); err != nil {
		processor.pool.ReportFailure(endpoint.Name)
		err = withStageDiagnostic(err, StageDiagnostic{Endpoint: endpoint.Name, Phase: "rpc"})
		if traceCapabilityUnavailable(err) {
			return StageResult{}, Unavailable(errStateDiffRPCAbsent)
		}
		return StageResult{}, err
	}
	processor.pool.ReportSuccess(endpoint.Name)
	result, err := processor.persist(ctx, job, transactions, "complete")
	result.diagnostic.Endpoint = endpoint.Name
	return result, err
}

func (processor *StateDiffRPCProcessor) fetch(
	ctx context.Context,
	caller rpcCaller,
	job Job,
	transactions []stateDiffTransaction,
	budget *stateDiffBudget,
) error {
	expected := make([]common.Hash, len(transactions))
	for index := range transactions {
		expected[index] = transactions[index].hash
	}
	var diffRaw json.RawMessage
	if err := caller.CallContext(ctx, &diffRaw, debugTraceBlockByHashMethod, job.BlockHash, map[string]any{
		"tracer":       "prestateTracer",
		"tracerConfig": map[string]any{"diffMode": true},
	}); err != nil {
		return sanitizeTraceRPCError(err)
	}
	if err := budget.add(len(diffRaw), normalizedStateDiffCounts{}, processor.limits); err != nil {
		return Permanent(err)
	}
	results, err := decodeBlockTraceResults(diffRaw, expected)
	if err != nil {
		return Permanent(fmt.Errorf("decode state difference block response: %w", err))
	}
	evidenceByTransaction := make([]transactionStateEvidence, len(transactions))
	needsFullPrestate := false
	for index := range transactions {
		if results[index].err != nil {
			return withStageDiagnostic(results[index].err, StageDiagnostic{
				Code: "state_diff_transaction_failed", Phase: "diff_prestate",
				TransactionHash:  transactions[index].hash,
				TransactionIndex: transactions[index].index, HasTransaction: true,
			})
		}
		changes, counts, err := normalizeStateDiff(results[index].result, processor.limits)
		if err != nil {
			return withStageDiagnostic(
				Permanent(fmt.Errorf("normalize transaction state difference: %w", err)),
				StageDiagnostic{Code: "state_diff_response_invalid", Phase: "normalize_diff", TransactionHash: transactions[index].hash, TransactionIndex: transactions[index].index, HasTransaction: true},
			)
		}
		if err := budget.add(0, counts, processor.limits); err != nil {
			return Permanent(err)
		}
		transactions[index].changes = changes
		evidence, err := decodeTransactionStateEvidence(results[index].result)
		if err != nil {
			return Permanent(fmt.Errorf("decode transaction state evidence: %w", err))
		}
		evidenceByTransaction[index] = evidence
		authorities, executions, err := deriveEIP7702Evidence(
			job.ChainID, transactions[index].tx, transactions[index].sender, evidence,
		)
		if err != nil {
			return Permanent(fmt.Errorf("derive EIP-7702 evidence: %w", err))
		}
		transactions[index].authorities = authorities
		transactions[index].executions = executions
		needsFullPrestate = needsFullPrestate || transactionNeedsFullPrestate(
			transactions[index].tx, executions,
		)
	}
	if !needsFullPrestate {
		return nil
	}
	var prestateRaw json.RawMessage
	if err := caller.CallContext(ctx, &prestateRaw, debugTraceBlockByHashMethod, job.BlockHash, map[string]any{
		"tracer":       "prestateTracer",
		"tracerConfig": map[string]any{"diffMode": false},
	}); err != nil {
		return sanitizeTraceRPCError(err)
	}
	if err := budget.add(len(prestateRaw), normalizedStateDiffCounts{}, processor.limits); err != nil {
		return Permanent(err)
	}
	prestateResults, err := decodeBlockTraceResults(prestateRaw, expected)
	if err != nil {
		return Permanent(fmt.Errorf("decode complete prestate block response: %w", err))
	}
	for index := range transactions {
		if prestateResults[index].err != nil {
			return withStageDiagnostic(prestateResults[index].err, StageDiagnostic{
				Code: "state_diff_transaction_failed", Phase: "complete_prestate",
				TransactionHash:  transactions[index].hash,
				TransactionIndex: transactions[index].index, HasTransaction: true,
			})
		}
		if !transactionNeedsFullPrestate(
			transactions[index].tx, transactions[index].executions,
		) {
			continue
		}
		prestate, counts, err := decodeTransactionPrestate(
			prestateResults[index].result, processor.limits,
		)
		if err != nil {
			return Permanent(fmt.Errorf("decode transaction complete prestate: %w", err))
		}
		if err := budget.add(0, counts, processor.limits); err != nil {
			return Permanent(err)
		}
		evidence := evidenceByTransaction[index]
		supplementTransactionExecutionEvidence(transactions[index].tx, &evidence, prestate)
		authorities, executions, err := deriveEIP7702Evidence(
			job.ChainID, transactions[index].tx, transactions[index].sender, evidence,
		)
		if err != nil {
			return Permanent(fmt.Errorf("derive complete-prestate EIP-7702 evidence: %w", err))
		}
		transactions[index].authorities = authorities
		transactions[index].executions = executions
	}
	return nil
}

func transactionNeedsFullPrestate(
	tx *types.Transaction,
	executions []executionCodeResolution,
) bool {
	if tx == nil || tx.To() == nil {
		return false
	}
	for _, execution := range executions {
		if execution.context == *tx.To() {
			return execution.resolution == "unavailable"
		}
	}
	return true
}

func supplementTransactionExecutionEvidence(
	tx *types.Transaction,
	evidence *transactionStateEvidence,
	prestate map[common.Address]stateAccountEvidence,
) {
	if tx == nil || tx.To() == nil || evidence == nil {
		return
	}
	if evidence.pre == nil {
		evidence.pre = make(map[common.Address]stateAccountEvidence)
	}
	target := *tx.To()
	account, exists := evidence.pre[target]
	if !exists {
		account = cloneStateAccountEvidence(prestate[target])
		evidence.pre[target] = account
	}
	delegate, delegated := types.ParseDelegation(account.code)
	if !delegated {
		return
	}
	if _, exists := evidence.pre[delegate]; exists {
		return
	}
	if delegateAccount, exists := prestate[delegate]; exists {
		evidence.pre[delegate] = cloneStateAccountEvidence(delegateAccount)
	}
}

func cloneStateAccountEvidence(account stateAccountEvidence) stateAccountEvidence {
	return stateAccountEvidence{nonce: account.nonce, code: append([]byte(nil), account.code...)}
}

func (processor *StateDiffRPCProcessor) transactions(
	ctx context.Context,
	job Job,
) ([]stateDiffTransaction, bool, error) {
	var canonical bool
	if err := processor.db.QueryRowContext(ctx, dbgen.EnrichLegacyTraceCanonical, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:]).Scan(&canonical); err != nil {
		return nil, false, fmt.Errorf("check state difference block canonicality: %w", err)
	}
	if !canonical {
		return nil, false, nil
	}
	rows, err := processor.db.QueryContext(ctx, dbgen.EnrichLegacyStateDiffTransactions, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:])
	if err != nil {
		return nil, false, fmt.Errorf("query state difference transactions: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	var result []stateDiffTransaction
	for rows.Next() {
		var index int64
		var hashBytes, raw []byte
		if err := rows.Scan(&index, &hashBytes, &raw); err != nil {
			return nil, false, fmt.Errorf("scan state difference transaction: %w", err)
		}
		if index < 0 || len(hashBytes) != common.HashLength {
			return nil, false, Permanent(errors.New("state difference transaction identity is invalid"))
		}
		hash := common.BytesToHash(hashBytes)
		decoded, sender, err := chainbundle.DecodeTransaction(
			json.RawMessage(raw), job.BlockHash, job.BlockNumber, uint64(index),
		)
		if err != nil {
			return nil, false, Permanent(fmt.Errorf("decode stored state difference transaction: %w", err))
		}
		result = append(result, stateDiffTransaction{
			index: uint64(index), hash: hash, raw: append(json.RawMessage(nil), raw...),
			tx: decoded, sender: sender,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate state difference transactions: %w", err)
	}
	return result, true, nil
}

func decodeTransactionStateEvidence(raw json.RawMessage) (transactionStateEvidence, error) {
	var wire stateDiffWire
	if err := json.Unmarshal(raw, &wire); err != nil || wire.Pre == nil || wire.Post == nil {
		return transactionStateEvidence{}, errors.New("invalid state difference JSON")
	}
	pairs := make(map[common.Address]*stateAccountPair, len(wire.Pre)+len(wire.Post))
	for addressText, account := range wire.Pre {
		address, err := ethrpc.ParseAddress(addressText)
		if err != nil {
			return transactionStateEvidence{}, errors.New("state difference contains invalid account address")
		}
		pair := pairs[address]
		if pair == nil {
			pair = &stateAccountPair{address: address}
			pairs[address] = pair
		}
		pair.pre, pair.hasPre = account, true
	}
	for addressText, account := range wire.Post {
		address, err := ethrpc.ParseAddress(addressText)
		if err != nil {
			return transactionStateEvidence{}, errors.New("state difference contains invalid account address")
		}
		pair := pairs[address]
		if pair == nil {
			pair = &stateAccountPair{address: address}
			pairs[address] = pair
		}
		pair.post, pair.hasPost = account, true
	}
	evidence := transactionStateEvidence{
		pre:  make(map[common.Address]stateAccountEvidence, len(pairs)),
		post: make(map[common.Address]stateAccountEvidence, len(pairs)),
	}
	for address, pair := range pairs {
		preWire, err := decodeStateAccount(pair.pre)
		if err != nil {
			return transactionStateEvidence{}, err
		}
		postWire, err := decodeStateAccount(pair.post)
		if err != nil {
			return transactionStateEvidence{}, err
		}
		pre, err := decodeStateAccountEvidence(preWire)
		if err != nil {
			return transactionStateEvidence{}, err
		}
		post, err := decodeStateAccountEvidence(postWire)
		if err != nil {
			return transactionStateEvidence{}, err
		}
		if pair.hasPre {
			evidence.pre[address] = pre
		}
		if pair.hasPre && pair.hasPost {
			if len(postWire.Nonce) == 0 || bytes.Equal(postWire.Nonce, []byte("null")) {
				post.nonce = pre.nonce
			}
			if postWire.Code == nil {
				post.code = append([]byte(nil), pre.code...)
			}
		}
		if pair.hasPost {
			evidence.post[address] = post
		}
	}
	return evidence, nil
}

func decodeTransactionPrestate(
	raw json.RawMessage,
	limits StateDiffLimits,
) (map[common.Address]stateAccountEvidence, normalizedStateDiffCounts, error) {
	if len(raw) > limits.MaxPayloadBytes {
		return nil, normalizedStateDiffCounts{}, ErrStateDiffLimit
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wire); err != nil || wire == nil {
		return nil, normalizedStateDiffCounts{}, errors.New("invalid complete prestate JSON")
	}
	if len(wire) > limits.MaxAccounts {
		return nil, normalizedStateDiffCounts{}, ErrStateDiffLimit
	}
	counts := normalizedStateDiffCounts{accounts: len(wire)}
	result := make(map[common.Address]stateAccountEvidence, len(wire))
	for addressText, rawAccount := range wire {
		address, err := ethrpc.ParseAddress(addressText)
		if err != nil {
			return nil, normalizedStateDiffCounts{}, errors.New("complete prestate contains invalid account address")
		}
		if _, exists := result[address]; exists {
			return nil, normalizedStateDiffCounts{}, errors.New("complete prestate contains duplicate account address")
		}
		accountWire, err := decodeStateAccount(rawAccount)
		if err != nil {
			return nil, normalizedStateDiffCounts{}, errors.New("complete prestate contains invalid account")
		}
		if accountWire.Balance != nil {
			balance := canonicalStateQuantity(accountWire.Balance)
			if balance == nil || *balance == "" {
				return nil, normalizedStateDiffCounts{}, errors.New("complete prestate contains invalid quantity")
			}
			counts.text += len(*balance)
		}
		account, err := decodeStateAccountEvidence(accountWire)
		if err != nil {
			return nil, normalizedStateDiffCounts{}, err
		}
		counts.code += len(account.code)
		counts.slots += len(accountWire.Storage)
		counts.text += len(addressText)
		if counts.code > limits.MaxCodeBytes || counts.slots > limits.MaxStorageSlots ||
			counts.text > limits.MaxTextBytes {
			return nil, normalizedStateDiffCounts{}, ErrStateDiffLimit
		}
		for keyText, valueText := range accountWire.Storage {
			if _, err := ethrpc.ParseHash(keyText); err != nil {
				return nil, normalizedStateDiffCounts{}, errors.New("complete prestate contains invalid storage key")
			}
			if _, err := ethrpc.ParseHash(valueText); err != nil {
				return nil, normalizedStateDiffCounts{}, errors.New("complete prestate contains invalid storage value")
			}
			counts.text += len(keyText) + len(valueText)
			if counts.text > limits.MaxTextBytes {
				return nil, normalizedStateDiffCounts{}, ErrStateDiffLimit
			}
		}
		result[address] = account
	}
	return result, counts, nil
}

func decodeStateAccountEvidence(wire stateAccountWire) (stateAccountEvidence, error) {
	nonceText, err := canonicalStateNonce(wire.Nonce)
	if err != nil {
		return stateAccountEvidence{}, errors.New("state difference contains invalid quantity")
	}
	var nonce uint64
	if nonceText != nil {
		nonce, err = strconv.ParseUint(*nonceText, 10, 64)
		if err != nil {
			return stateAccountEvidence{}, errors.New("state difference contains invalid quantity")
		}
	}
	var code []byte
	if wire.Code != nil {
		code, err = ethrpc.ParseData(*wire.Code)
		if err != nil {
			return stateAccountEvidence{}, errors.New("state difference contains invalid code")
		}
	}
	return stateAccountEvidence{nonce: nonce, code: append([]byte(nil), code...)}, nil
}

func deriveEIP7702Evidence(
	chainIDText string,
	tx *types.Transaction,
	sender common.Address,
	evidence transactionStateEvidence,
) ([]eip7702AuthorizationResult, []executionCodeResolution, error) {
	if tx == nil {
		return nil, nil, errors.New("transaction is nil")
	}
	chainID, ok := new(big.Int).SetString(chainIDText, 10)
	if !ok || chainID.Sign() < 0 {
		return nil, nil, errors.New("chain id is invalid")
	}
	state := make(map[common.Address]stateAccountEvidence, len(evidence.pre))
	for address, account := range evidence.pre {
		state[address] = stateAccountEvidence{nonce: account.nonce, code: append([]byte(nil), account.code...)}
	}
	authorizations := tx.SetCodeAuthorizations()
	if len(authorizations) > 0 && tx.To() != nil {
		senderAccount, exists := state[sender]
		if !exists {
			return nil, nil, errors.New("set-code transaction lacks sender pre-state evidence")
		}
		if senderAccount.nonce == math.MaxUint64 {
			return nil, nil, errors.New("sender nonce overflow")
		}
		senderAccount.nonce++
		state[sender] = senderAccount
	}
	results := make([]eip7702AuthorizationResult, 0, len(authorizations))
	for index := range authorizations {
		auth := authorizations[index]
		result := eip7702AuthorizationResult{
			index: uint64(index), authorization: auth,
			signatureStatus: "unavailable", applicationStatus: "skipped",
		}
		if !auth.ChainID.IsZero() && auth.ChainID.CmpBig(chainID) != 0 {
			result.skipReason = "wrong_chain_id"
			results = append(results, result)
			continue
		}
		if auth.Nonce == math.MaxUint64 {
			result.skipReason = "nonce_overflow"
			results = append(results, result)
			continue
		}
		authority, err := auth.Authority()
		if err != nil {
			result.signatureStatus = "invalid"
			result.skipReason = "invalid_signature"
			results = append(results, result)
			continue
		}
		result.signatureStatus = "valid"
		result.authority = addressPointer(authority)
		account := state[authority]
		if _, delegated := types.ParseDelegation(account.code); len(account.code) != 0 && !delegated {
			result.skipReason = "authority_has_code"
			results = append(results, result)
			continue
		}
		if account.nonce != auth.Nonce {
			result.skipReason = "nonce_mismatch"
			results = append(results, result)
			continue
		}
		account.nonce = auth.Nonce + 1
		if auth.Address == (common.Address{}) {
			account.code = nil
		} else {
			account.code = types.AddressToDelegation(auth.Address)
		}
		state[authority] = account
		result.applicationStatus = "applied"
		results = append(results, result)
	}
	for _, result := range results {
		if result.applicationStatus != "applied" || result.authority == nil {
			continue
		}
		actual, exists := evidence.post[*result.authority]
		if !exists {
			return nil, nil, errors.New("applied authorization lacks post-state evidence")
		}
		expected := state[*result.authority]
		if !bytes.Equal(actual.code, expected.code) || actual.nonce < expected.nonce {
			return nil, nil, errors.New("authorization result contradicts post-state evidence")
		}
	}
	executions := make([]executionCodeResolution, 0, len(state))
	for contextAddress, account := range state {
		resolution := executionCodeResolution{context: contextAddress, evidenceSource: "prestate_tracer"}
		if delegate, delegated := types.ParseDelegation(account.code); delegated {
			delegateAccount, exists := state[delegate]
			if !exists {
				if _, precompile := vm.PrecompiledContractsPrague[delegate]; precompile {
					resolution.resolution = "empty"
					executions = append(executions, resolution)
					continue
				}
				resolution.resolution = "unavailable"
				resolution.execution = addressPointer(delegate)
				resolution.evidenceSource = "unavailable"
			} else if len(delegateAccount.code) == 0 {
				resolution.resolution = "empty"
			} else {
				resolution.resolution = "eip7702_delegate"
				resolution.execution = addressPointer(delegate)
				hash := crypto.Keccak256Hash(delegateAccount.code)
				resolution.codeHash = &hash
			}
		} else if len(account.code) == 0 {
			resolution.resolution = "empty"
		} else {
			resolution.resolution = "direct"
			resolution.execution = addressPointer(contextAddress)
			hash := crypto.Keccak256Hash(account.code)
			resolution.codeHash = &hash
		}
		executions = append(executions, resolution)
	}
	sort.Slice(executions, func(i, j int) bool {
		return bytes.Compare(executions[i].context[:], executions[j].context[:]) < 0
	})
	return results, executions, nil
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
	accounts, err := stateDiffAccountPairs(wire)
	if err != nil {
		return nil, normalizedStateDiffCounts{}, err
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
		preBalance := canonicalStateQuantity(pre.Balance)
		postBalance := canonicalStateQuantity(post.Balance)
		preCode := canonicalStateCode(pre.Code, &counts)
		postCode := canonicalStateCode(post.Code, &counts)
		// geth's prestateTracer diffMode emits complete scalar pre-state but
		// only changed scalar fields in an existing account's post-state. An
		// omitted post field therefore means unchanged, while an account
		// omitted from post means deleted. Storage is intentionally excluded:
		// a changed slot omitted from post is a clear-to-zero operation.
		if pair.hasPre && pair.hasPost {
			if post.Balance == nil {
				postBalance = preBalance
			}
			if len(post.Nonce) == 0 || bytes.Equal(post.Nonce, []byte("null")) {
				postNonce = preNonce
			}
			if post.Code == nil {
				postCode = preCode
			}
		}
		for _, field := range []struct {
			kind   string
			before *string
			after  *string
		}{
			{kind: "balance", before: preBalance, after: postBalance},
			{kind: "nonce", before: preNonce, after: postNonce},
			{kind: "code", before: preCode, after: postCode},
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

func stateDiffAccountPairs(wire stateDiffWire) (map[common.Address]*stateAccountPair, error) {
	accounts := make(map[common.Address]*stateAccountPair, len(wire.Pre)+len(wire.Post))
	for addressText, rawAccount := range wire.Pre {
		if err := mergeStateDiffAccount(accounts, addressText, rawAccount, true); err != nil {
			return nil, err
		}
	}
	for addressText, rawAccount := range wire.Post {
		if err := mergeStateDiffAccount(accounts, addressText, rawAccount, false); err != nil {
			return nil, err
		}
	}
	return accounts, nil
}

func mergeStateDiffAccount(
	accounts map[common.Address]*stateAccountPair,
	addressText string,
	rawAccount json.RawMessage,
	pre bool,
) error {
	address, err := ethrpc.ParseAddress(addressText)
	if err != nil {
		return errors.New("state difference contains invalid account address")
	}
	pair := accounts[address]
	if pair == nil {
		pair = &stateAccountPair{address: address}
		accounts[address] = pair
	}
	if pre {
		if pair.hasPre {
			return errors.New("state difference contains duplicate account address")
		}
		pair.pre, pair.hasPre = rawAccount, true
		return nil
	}
	if pair.hasPost {
		return errors.New("state difference contains duplicate account address")
	}
	pair.post, pair.hasPost = rawAccount, true
	return nil
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
			if _, err := tx.ExecContext(ctx, dbgen.EnrichLegacyDeleteStateDiffBlock, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:]); err != nil {
				return StageResult{}, fmt.Errorf("clear previous transaction state differences: %w", err)
			}
			if _, err := tx.ExecContext(ctx, dbgen.EnrichLegacyDeleteEIP7702AuthorizationsBlock, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:]); err != nil {
				return StageResult{}, fmt.Errorf("clear previous EIP-7702 authorizations: %w", err)
			}
			if _, err := tx.ExecContext(ctx, dbgen.EnrichLegacyDeleteExecutionCodeResolutionsBlock, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:]); err != nil {
				return StageResult{}, fmt.Errorf("clear previous execution-code resolutions: %w", err)
			}
		}
		changeCount, authorizationCount, executionCount := 0, 0, 0
		proxyRelevantChanges := 0
		for _, transaction := range transactions {
			for _, change := range transaction.changes {
				if _, err := tx.ExecContext(ctx, dbgen.EnrichLegacyInsertStateChange, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
					transaction.hash[:], transaction.index, change.address[:], change.kind,
					change.key, nullableStateValue(change.before), nullableStateValue(change.after),
				); err != nil {
					return StageResult{}, fmt.Errorf("persist transaction state difference: %w", err)
				}
				changeCount++
				if proxyRelevantStateChange(change) {
					proxyRelevantChanges++
				}
			}
			for _, authorization := range transaction.authorities {
				if err := persistEIP7702Authorization(ctx, tx, job, transaction, authorization); err != nil {
					return StageResult{}, err
				}
				authorizationCount++
			}
			for _, execution := range transaction.executions {
				if err := persistExecutionCodeResolution(ctx, tx, job, transaction, execution); err != nil {
					return StageResult{}, err
				}
				executionCount++
			}
		}
		traceRequeued := false
		if canonical {
			traceRequeued, err = resetTerminalDependentStageTx(ctx, tx, job, TraceStage)
			if err != nil {
				return StageResult{}, err
			}
		}
		return StageResult{State: ResultComplete, Details: map[string]string{
			"outcome": outcome, "source": "prestate_tracer",
			"transactions": strconv.Itoa(len(transactions)), "changes": strconv.Itoa(changeCount),
			"authorizations":         strconv.Itoa(authorizationCount),
			"execution_resolutions":  strconv.Itoa(executionCount),
			"proxy_relevant_changes": strconv.Itoa(proxyRelevantChanges),
			"trace_requeued":         strconv.FormatBool(traceRequeued),
		}}, nil
	})
}

func persistEIP7702Authorization(
	ctx context.Context, tx *sql.Tx, job Job, transaction stateDiffTransaction,
	result eip7702AuthorizationResult,
) error {
	var authority any
	if result.authority != nil {
		authority = result.authority[:]
	}
	r := result.authorization.R.Bytes32()
	s := result.authorization.S.Bytes32()
	var reason any
	if result.skipReason != "" {
		reason = result.skipReason
	}
	if _, err := tx.ExecContext(ctx, dbgen.EnrichLegacyInsertEIP7702Authorization, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		transaction.hash[:], transaction.index, result.index,
		result.authorization.ChainID.ToBig().String(), result.authorization.Nonce,
		result.authorization.Address[:], result.authorization.V, r[:], s[:], authority,
		result.signatureStatus, result.applicationStatus, reason,
	); err != nil {
		return fmt.Errorf("persist EIP-7702 authorization: %w", err)
	}
	return nil
}

func persistExecutionCodeResolution(
	ctx context.Context, tx *sql.Tx, job Job, transaction stateDiffTransaction,
	resolution executionCodeResolution,
) error {
	var execution, codeHash any
	if resolution.execution != nil {
		execution = resolution.execution[:]
	}
	if resolution.codeHash != nil {
		codeHash = resolution.codeHash[:]
	}
	if _, err := tx.ExecContext(ctx, dbgen.EnrichLegacyInsertExecutionCodeResolution, job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		transaction.hash[:], transaction.index, resolution.context[:], execution,
		codeHash, resolution.resolution, resolution.evidenceSource,
	); err != nil {
		return fmt.Errorf("persist execution-code resolution: %w", err)
	}
	return nil
}

func proxyRelevantStateChange(change stateChange) bool {
	if change.kind == "code" {
		return true
	}
	if change.kind != "storage" || len(change.key) != common.HashLength {
		return false
	}
	key := common.BytesToHash(change.key)
	switch key {
	case EIP1967ImplementationSlot, EIP1967BeaconSlot, EIP1967AdminSlot:
		return true
	default:
		return false
	}
}

func nullableStateValue(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
