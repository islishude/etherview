package metadata

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
)

var (
	ErrLeaseLost                = errors.New("metadata job lease is no longer owned")
	ErrExactNFTMetadataConflict = errors.New("exact NFT metadata observation conflicts with persisted source")
)

type PostgresRepository struct {
	db      *sql.DB
	chainID string
	random  io.Reader
}

func NewPostgresRepository(db *sql.DB, chainID string) (*PostgresRepository, error) {
	if db == nil {
		return nil, errors.New("metadata repository requires a database")
	}
	if err := validateDecimal(chainID, "repository chain ID"); err != nil {
		return nil, err
	}
	return &PostgresRepository{db: db, chainID: chainID, random: rand.Reader}, nil
}

type durablePayload struct {
	ChainID     string `json:"_chain_id"`
	ResourceKey string `json:"resource_key"`
	Token       string `json:"token_address"`
	TokenID     string `json:"token_id"`
	BlockNumber string `json:"block_number"`
	BlockHash   string `json:"block_hash"`
	SourceURI   string `json:"source_uri"`
}

func (repository *PostgresRepository) EnqueueNFT(ctx context.Context, request NFTRequest) (EnqueueResult, error) {
	if repository == nil || repository.db == nil {
		return EnqueueResult{}, errors.New("enqueue NFT metadata using nil PostgreSQL repository")
	}
	if err := request.Validate(); err != nil {
		return EnqueueResult{}, err
	}
	if request.ChainID != repository.chainID {
		return EnqueueResult{}, errors.New("metadata request chain differs from repository chain")
	}
	if request.MaxAttempts == 0 {
		request.MaxAttempts = DefaultMaxAttempts
	}
	key, err := request.idempotencyKey()
	if err != nil {
		return EnqueueResult{}, err
	}
	payload, err := encodePayload(request)
	if err != nil {
		return EnqueueResult{}, err
	}
	address := request.Token.Bytes()
	blockHash := request.BlockHash.Bytes()

	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("begin metadata enqueue transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	var canonical bool
	if err := tx.QueryRowContext(ctx, dbgen.MetadataCanonicalObservation, request.ChainID, strconv.FormatUint(request.BlockNumber, 10), blockHash).Scan(&canonical); err != nil {
		return EnqueueResult{}, fmt.Errorf("check metadata source canonicality: %w", err)
	}
	if !canonical {
		return EnqueueResult{}, errors.New("metadata source block is not canonical")
	}
	var nftContract bool
	if err := tx.QueryRowContext(ctx, dbgen.MetadataCanonicalNFTContract, request.ChainID, address).Scan(&nftContract); err != nil {
		return EnqueueResult{}, fmt.Errorf("check metadata NFT contract: %w", err)
	}
	if !nftContract {
		return EnqueueResult{}, errors.New("metadata token address is not a canonical ERC-721 or ERC-1155 contract")
	}
	var inserted int
	err = tx.QueryRowContext(ctx, dbgen.MetadataWriteInsertMetadataResource, request.ChainID, request.resourceKey(), request.SourceURI,
		address, request.TokenID, strconv.FormatUint(request.BlockNumber, 10), blockHash,
	).Scan(&inserted)
	if errors.Is(err, sql.ErrNoRows) {
		var (
			storedKey, storedURI, storedBlockNumber string
			storedAddress, storedBlockHash          []byte
			storedTokenID                           string
		)
		err = tx.QueryRowContext(ctx, dbgen.MetadataExistingMetadataResource, request.ChainID, address, request.TokenID, blockHash).Scan(&storedKey, &storedURI, &storedAddress, &storedTokenID, &storedBlockNumber, &storedBlockHash)
		if err == nil && (storedKey != request.resourceKey() || storedURI != request.SourceURI ||
			!bytes.Equal(storedAddress, address) || storedTokenID != request.TokenID ||
			storedBlockNumber != strconv.FormatUint(request.BlockNumber, 10) || !bytes.Equal(storedBlockHash, blockHash)) {
			return EnqueueResult{}, ErrExactNFTMetadataConflict
		}
	}
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("insert NFT metadata resource: %w", err)
	}
	var jobID int64
	err = tx.QueryRowContext(ctx, dbgen.MetadataWriteEnqueueMetadataJob, request.ChainID, key, string(payload), request.Priority, request.MaxAttempts).Scan(&jobID)
	created := err == nil
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, dbgen.MetadataExistingMetadataJob, request.ChainID, key).Scan(&jobID)
	}
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("enqueue NFT metadata job: %w", err)
	}
	if jobID <= 0 {
		return EnqueueResult{}, errors.New("metadata database returned an invalid job ID")
	}
	if err := tx.Commit(); err != nil {
		return EnqueueResult{}, fmt.Errorf("commit metadata enqueue transaction: %w", err)
	}
	return EnqueueResult{JobID: jobID, Created: created}, nil
}

func (repository *PostgresRepository) Claim(ctx context.Context, workerID string, leaseFor time.Duration) (Lease, bool, error) {
	if repository == nil || repository.db == nil {
		return Lease{}, false, errors.New("claim NFT metadata using nil PostgreSQL repository")
	}
	if strings.TrimSpace(workerID) == "" || len(workerID) > 128 {
		return Lease{}, false, errors.New("metadata worker ID must contain between 1 and 128 bytes")
	}
	leaseMicros, err := durationMicroseconds(leaseFor, false)
	if err != nil {
		return Lease{}, false, fmt.Errorf("metadata lease duration: %w", err)
	}
	token, err := randomToken(repository.random)
	if err != nil {
		return Lease{}, false, fmt.Errorf("generate metadata lease token: %w", err)
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Lease{}, false, fmt.Errorf("begin metadata claim transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, dbgen.MetadataExhaustMetadataJobs, repository.chainID); err != nil {
		return Lease{}, false, fmt.Errorf("finalize exhausted metadata jobs: %w", err)
	}
	var (
		jobID, attempt, maxAttempts int64
		chainID                     string
		payload                     []byte
	)
	err = tx.QueryRowContext(ctx, dbgen.MetadataClaimMetadataJob, workerID, token, leaseMicros, repository.chainID).Scan(
		&jobID, &chainID, &attempt, &maxAttempts, &payload,
	)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return Lease{}, false, fmt.Errorf("commit empty metadata claim: %w", err)
		}
		return Lease{}, false, nil
	}
	if err != nil {
		return Lease{}, false, fmt.Errorf("claim metadata job: %w", err)
	}
	request, err := decodePayload(payload, maxAttempts)
	if err != nil {
		return Lease{}, false, fmt.Errorf("decode claimed metadata job %d: %w", jobID, err)
	}
	if jobID <= 0 || attempt <= 0 || attempt > maxAttempts || maxAttempts > int64(MaximumMaxAttempts) {
		return Lease{}, false, errors.New("claimed metadata job contains invalid counters")
	}
	if request.ChainID != chainID {
		return Lease{}, false, errors.New("claimed metadata payload chain differs from its durable job")
	}
	if err := tx.Commit(); err != nil {
		return Lease{}, false, fmt.Errorf("commit metadata claim: %w", err)
	}
	return Lease{
		JobID: jobID, Token: token, Request: request,
		Attempt: uint32(attempt), MaxAttempts: uint32(maxAttempts),
	}, true, nil
}

func (repository *PostgresRepository) Renew(ctx context.Context, lease Lease, leaseFor time.Duration) error {
	if repository == nil || repository.db == nil {
		return errors.New("renew NFT metadata using nil PostgreSQL repository")
	}
	if err := lease.Validate(); err != nil {
		return err
	}
	if lease.Request.ChainID != repository.chainID {
		return ErrLeaseLost
	}
	leaseMicros, err := durationMicroseconds(leaseFor, false)
	if err != nil {
		return fmt.Errorf("metadata lease duration: %w", err)
	}
	result, err := repository.db.ExecContext(ctx, dbgen.MetadataWriteRenewMetadataJob, lease.JobID, lease.Token, leaseMicros)
	if err != nil {
		return fmt.Errorf("renew metadata job: %w", err)
	}
	return requireOne(result)
}

func (repository *PostgresRepository) Current(ctx context.Context, lease Lease) (Current, error) {
	if repository == nil || repository.db == nil {
		return Current{}, errors.New("check NFT metadata using nil PostgreSQL repository")
	}
	if err := lease.Validate(); err != nil {
		return Current{}, err
	}
	if lease.Request.ChainID != repository.chainID {
		return Current{}, ErrLeaseLost
	}
	return queryCurrent(ctx, repository.db, lease.Request)
}

func (repository *PostgresRepository) Finish(ctx context.Context, lease Lease, outcome Outcome) error {
	if repository == nil || repository.db == nil {
		return errors.New("finish NFT metadata using nil PostgreSQL repository")
	}
	if err := lease.Validate(); err != nil {
		return err
	}
	if lease.Request.ChainID != repository.chainID {
		return ErrLeaseLost
	}
	if err := outcome.validate(); err != nil {
		return err
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin metadata finish transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if err := lockOwnedJob(ctx, tx, lease); err != nil {
		return err
	}
	current, err := lockCurrent(ctx, tx, lease.Request)
	if err != nil {
		return fmt.Errorf("recheck metadata canonical identity: %w", err)
	}
	updateResource := current.Resource
	if !current.Resource {
		outcome = terminalOutcome(StateUnavailable, "superseded", "metadata source was superseded by a newer canonical observation")
	} else if !current.Canonical {
		outcome = terminalOutcome(StateUnavailable, "source_block_noncanonical", "metadata source block is no longer canonical")
	}
	if err := finishLocked(ctx, tx, lease, outcome, updateResource); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit metadata finish transaction: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) Retry(ctx context.Context, lease Lease, code, message string, after time.Duration) error {
	if repository == nil || repository.db == nil {
		return errors.New("retry NFT metadata using nil PostgreSQL repository")
	}
	if err := lease.Validate(); err != nil {
		return err
	}
	if lease.Request.ChainID != repository.chainID {
		return ErrLeaseLost
	}
	if err := validateErrorCode(code); err != nil {
		return err
	}
	message = boundedText(message, MaxStoredErrorBytes)
	retryMicros, err := durationMicroseconds(after, true)
	if err != nil {
		return fmt.Errorf("metadata retry delay: %w", err)
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin metadata retry transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if err := lockOwnedJob(ctx, tx, lease); err != nil {
		return err
	}
	current, err := lockCurrent(ctx, tx, lease.Request)
	if err != nil {
		return fmt.Errorf("recheck retried metadata identity: %w", err)
	}
	if !current.Resource {
		if err := finishLocked(ctx, tx, lease, terminalOutcome(StateUnavailable, "superseded", "metadata source was superseded by a newer canonical observation"), false); err != nil {
			return err
		}
	} else if !current.Canonical {
		if err := finishLocked(ctx, tx, lease, terminalOutcome(StateUnavailable, "source_block_noncanonical", "metadata source block is no longer canonical"), true); err != nil {
			return err
		}
	} else if lease.Attempt >= lease.MaxAttempts {
		if err := finishLocked(ctx, tx, lease, terminalOutcome(StateError, "attempts_exhausted", message), true); err != nil {
			return err
		}
	} else {
		result, err := tx.ExecContext(ctx, dbgen.MetadataWriteRecordMetadataRetry, lease.Request.ChainID, lease.Request.resourceKey(), lease.Request.SourceURI,
			strconv.FormatUint(lease.Request.BlockNumber, 10), mustHashBytes(lease.Request.BlockHash),
			lease.Attempt, code, message,
		)
		if err != nil {
			return fmt.Errorf("record pending metadata retry: %w", err)
		}
		if err := requireOne(result); err != nil {
			return fmt.Errorf("record pending metadata retry: %w", err)
		}
		if _, err := tx.ExecContext(ctx, dbgen.MetadataWriteInsertMetadataAttempt, lease.Request.ChainID, lease.Request.resourceKey(), lease.JobID, lease.Attempt,
			StateError, lease.Request.SourceURI, nil, nil, nil, nil, code, message,
		); err != nil {
			return fmt.Errorf("audit metadata retry: %w", err)
		}
		result, err = tx.ExecContext(ctx, dbgen.MetadataWriteRetryMetadataJob, lease.JobID, lease.Token, code+": "+message, retryMicros)
		if err != nil {
			return fmt.Errorf("queue metadata retry: %w", err)
		}
		if err := requireOne(result); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit metadata retry transaction: %w", err)
	}
	return nil
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func queryCurrent(ctx context.Context, queryer queryRower, request NFTRequest) (Current, error) {
	address := request.Token.Bytes()
	hash := request.BlockHash.Bytes()
	var current Current
	if err := queryer.QueryRowContext(ctx, dbgen.MetadataCurrentMetadataResource, request.ChainID, request.resourceKey(), address, request.TokenID,
		strconv.FormatUint(request.BlockNumber, 10), hash, request.SourceURI,
	).Scan(&current.Resource, &current.Canonical); err != nil {
		return Current{}, err
	}
	return current, nil
}

func lockCurrent(ctx context.Context, tx *sql.Tx, request NFTRequest) (Current, error) {
	address := request.Token.Bytes()
	hash := request.BlockHash.Bytes()
	var matches bool
	err := tx.QueryRowContext(ctx, dbgen.MetadataLockMetadataResource, request.ChainID, request.resourceKey(), address, request.TokenID,
		strconv.FormatUint(request.BlockNumber, 10), hash, request.SourceURI,
	).Scan(&matches)
	if errors.Is(err, sql.ErrNoRows) {
		return Current{}, nil
	}
	if err != nil {
		return Current{}, err
	}
	var canonical bool
	if err := tx.QueryRowContext(ctx, dbgen.MetadataCanonicalObservation, request.ChainID, strconv.FormatUint(request.BlockNumber, 10), hash).Scan(&canonical); err != nil {
		return Current{}, err
	}
	return Current{Resource: matches, Canonical: canonical}, nil
}

func lockOwnedJob(ctx context.Context, tx *sql.Tx, lease Lease) error {
	var payload []byte
	var chainID string
	var maxAttempts int64
	err := tx.QueryRowContext(ctx, dbgen.MetadataLockOwnedMetadataJob, lease.JobID, lease.Token).Scan(&chainID, &payload, &maxAttempts)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("lock metadata job lease: %w", err)
	}
	stored, err := decodePayload(payload, maxAttempts)
	if err != nil {
		return fmt.Errorf("decode owned metadata job: %w", err)
	}
	if chainID != stored.ChainID || !sameRequest(stored, lease.Request) || uint32(maxAttempts) != lease.MaxAttempts {
		return ErrLeaseLost
	}
	return nil
}

func finishLocked(ctx context.Context, tx *sql.Tx, lease Lease, outcome Outcome, updateResource bool) error {
	if err := outcome.validate(); err != nil {
		return err
	}
	var (
		resolvedURI any
		mediaType   any
		contentHash any
		document    any
		contentSize any
		errorCode   any
		errorText   any
	)
	if outcome.State == StateAvailable {
		resolvedURI = outcome.ResolvedURI
		mediaType = outcome.MediaType
		contentHash = outcome.ContentHash[:]
		document = string(outcome.Document)
		contentSize = outcome.ContentSize
	} else {
		errorCode = outcome.Code
		errorText = outcome.Message
	}
	if updateResource {
		result, err := tx.ExecContext(ctx, dbgen.MetadataWriteFinishMetadataResource, lease.Request.ChainID, lease.Request.resourceKey(), lease.Request.SourceURI,
			strconv.FormatUint(lease.Request.BlockNumber, 10), mustHashBytes(lease.Request.BlockHash),
			outcome.State, resolvedURI, mediaType, contentHash, document, contentSize,
			lease.Attempt, errorCode, errorText,
		)
		if err != nil {
			return fmt.Errorf("persist metadata outcome: %w", err)
		}
		if err := requireOne(result); err != nil {
			return fmt.Errorf("persist metadata outcome: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, dbgen.MetadataWriteInsertMetadataAttempt, lease.Request.ChainID, lease.Request.resourceKey(), lease.JobID, lease.Attempt,
		outcome.State, lease.Request.SourceURI, resolvedURI, mediaType, contentHash, contentSize,
		errorCode, errorText,
	); err != nil {
		return fmt.Errorf("audit metadata outcome: %w", err)
	}
	jobStatus := "succeeded"
	if outcome.State == StateError {
		jobStatus = "failed"
	}
	summary, err := json.Marshal(map[string]any{
		"state": outcome.State, "code": outcome.Code,
		"content_hash": hashString(outcome), "content_size": outcome.ContentSize,
	})
	if err != nil {
		return fmt.Errorf("encode metadata job outcome: %w", err)
	}
	result, err := tx.ExecContext(ctx, dbgen.MetadataWriteFinishMetadataJob, lease.JobID, lease.Token, jobStatus, string(summary), errorText)
	if err != nil {
		return fmt.Errorf("finish metadata durable job: %w", err)
	}
	return requireOne(result)
}

func encodePayload(request NFTRequest) ([]byte, error) {
	payload, err := json.Marshal(durablePayload{
		ChainID: request.ChainID, ResourceKey: request.resourceKey(), Token: strings.ToLower(request.Token.Hex()), TokenID: request.TokenID,
		BlockNumber: strconv.FormatUint(request.BlockNumber, 10), BlockHash: strings.ToLower(request.BlockHash.Hex()),
		SourceURI: request.SourceURI,
	})
	if err != nil {
		return nil, fmt.Errorf("encode metadata job payload: %w", err)
	}
	if len(payload) > 8192 {
		return nil, errors.New("metadata job payload exceeds 8192 bytes")
	}
	return payload, nil
}

func decodePayload(payload []byte, maxAttempts int64) (NFTRequest, error) {
	if len(payload) == 0 || len(payload) > 8192 || maxAttempts <= 0 || maxAttempts > int64(MaximumMaxAttempts) {
		return NFTRequest{}, errors.New("metadata job payload or max attempts is outside bounds")
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	var stored durablePayload
	if err := decoder.Decode(&stored); err != nil {
		return NFTRequest{}, err
	}
	blockNumber, err := strconv.ParseUint(stored.BlockNumber, 10, 64)
	if err != nil || strconv.FormatUint(blockNumber, 10) != stored.BlockNumber {
		return NFTRequest{}, errors.New("metadata job block number is not a canonical uint64")
	}
	address, err := ethrpc.ParseAddress(stored.Token)
	if err != nil {
		return NFTRequest{}, err
	}
	hash, err := ethrpc.ParseHash(stored.BlockHash)
	if err != nil {
		return NFTRequest{}, err
	}
	request := NFTRequest{
		Token: address, TokenID: stored.TokenID, BlockNumber: blockNumber,
		BlockHash: hash, SourceURI: stored.SourceURI, MaxAttempts: uint32(maxAttempts),
	}
	request.ChainID = stored.ChainID
	if request.resourceKey() != stored.ResourceKey {
		return NFTRequest{}, errors.New("metadata job resource key is not canonical")
	}
	return request, nil
}

func sameRequest(left, right NFTRequest) bool {
	return left.ChainID == right.ChainID && left.Token == right.Token && left.TokenID == right.TokenID &&
		left.BlockNumber == right.BlockNumber && left.BlockHash == right.BlockHash && left.SourceURI == right.SourceURI
}

func randomToken(source io.Reader) (string, error) {
	if source == nil {
		return "", errors.New("metadata random source is nil")
	}
	value := make([]byte, 32)
	if _, err := io.ReadFull(source, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func durationMicroseconds(value time.Duration, allowZero bool) (int64, error) {
	if value < 0 || value == 0 && !allowZero {
		return 0, errors.New("duration must be positive")
	}
	if value == 0 {
		return 0, nil
	}
	microseconds := value / time.Microsecond
	if value%time.Microsecond != 0 {
		microseconds++
	}
	return int64(microseconds), nil
}

func requireOne(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read metadata update count: %w", err)
	}
	if affected != 1 {
		return ErrLeaseLost
	}
	return nil
}

func mustHashBytes(hash common.Hash) []byte {
	return hash.Bytes()
}

func hashString(outcome Outcome) any {
	if outcome.State != StateAvailable {
		return nil
	}
	return "0x" + fmt.Sprintf("%x", outcome.ContentHash[:])
}
