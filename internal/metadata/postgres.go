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
	if err := validateDecimal(chainID, 78, "repository chain ID"); err != nil {
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
	address, _ := request.Token.Bytes()
	blockHash, _ := request.BlockHash.Bytes()

	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("begin metadata enqueue transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	var canonical bool
	if err := tx.QueryRowContext(ctx, canonicalObservationSQL,
		request.ChainID, strconv.FormatUint(request.BlockNumber, 10), blockHash,
	).Scan(&canonical); err != nil {
		return EnqueueResult{}, fmt.Errorf("check metadata source canonicality: %w", err)
	}
	if !canonical {
		return EnqueueResult{}, errors.New("metadata source block is not canonical")
	}
	var nftContract bool
	if err := tx.QueryRowContext(ctx, canonicalNFTContractSQL, request.ChainID, address).Scan(&nftContract); err != nil {
		return EnqueueResult{}, fmt.Errorf("check metadata NFT contract: %w", err)
	}
	if !nftContract {
		return EnqueueResult{}, errors.New("metadata token address is not a canonical ERC-721 or ERC-1155 contract")
	}
	var inserted int
	err = tx.QueryRowContext(ctx, insertMetadataResourceSQL,
		request.ChainID, request.resourceKey(), request.SourceURI,
		address, request.TokenID, strconv.FormatUint(request.BlockNumber, 10), blockHash,
	).Scan(&inserted)
	if errors.Is(err, sql.ErrNoRows) {
		var (
			storedKey, storedURI, storedBlockNumber string
			storedAddress, storedBlockHash          []byte
			storedTokenID                           string
		)
		err = tx.QueryRowContext(ctx, existingMetadataResourceSQL,
			request.ChainID, address, request.TokenID, blockHash,
		).Scan(&storedKey, &storedURI, &storedAddress, &storedTokenID, &storedBlockNumber, &storedBlockHash)
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
	err = tx.QueryRowContext(ctx, enqueueMetadataJobSQL,
		request.ChainID, key, string(payload), request.Priority, request.MaxAttempts,
	).Scan(&jobID)
	created := err == nil
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, existingMetadataJobSQL, request.ChainID, key).Scan(&jobID)
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
	if _, err := tx.ExecContext(ctx, exhaustMetadataJobsSQL, repository.chainID); err != nil {
		return Lease{}, false, fmt.Errorf("finalize exhausted metadata jobs: %w", err)
	}
	var (
		jobID, attempt, maxAttempts int64
		chainID                     string
		payload                     []byte
	)
	err = tx.QueryRowContext(ctx, claimMetadataJobSQL, workerID, token, leaseMicros, repository.chainID).Scan(
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
	result, err := repository.db.ExecContext(ctx, renewMetadataJobSQL, lease.JobID, lease.Token, leaseMicros)
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
		result, err := tx.ExecContext(ctx, recordMetadataRetrySQL,
			lease.Request.ChainID, lease.Request.resourceKey(), lease.Request.SourceURI,
			strconv.FormatUint(lease.Request.BlockNumber, 10), mustHashBytes(lease.Request.BlockHash),
			lease.Attempt, code, message,
		)
		if err != nil {
			return fmt.Errorf("record pending metadata retry: %w", err)
		}
		if err := requireOne(result); err != nil {
			return fmt.Errorf("record pending metadata retry: %w", err)
		}
		if _, err := tx.ExecContext(ctx, insertMetadataAttemptSQL,
			lease.Request.ChainID, lease.Request.resourceKey(), lease.JobID, lease.Attempt,
			StateError, lease.Request.SourceURI, nil, nil, nil, nil, code, message,
		); err != nil {
			return fmt.Errorf("audit metadata retry: %w", err)
		}
		result, err = tx.ExecContext(ctx, retryMetadataJobSQL, lease.JobID, lease.Token, code+": "+message, retryMicros)
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
	address, _ := request.Token.Bytes()
	hash, _ := request.BlockHash.Bytes()
	var current Current
	if err := queryer.QueryRowContext(ctx, currentMetadataResourceSQL,
		request.ChainID, request.resourceKey(), address, request.TokenID,
		strconv.FormatUint(request.BlockNumber, 10), hash, request.SourceURI,
	).Scan(&current.Resource, &current.Canonical); err != nil {
		return Current{}, err
	}
	return current, nil
}

func lockCurrent(ctx context.Context, tx *sql.Tx, request NFTRequest) (Current, error) {
	address, _ := request.Token.Bytes()
	hash, _ := request.BlockHash.Bytes()
	var matches bool
	err := tx.QueryRowContext(ctx, lockMetadataResourceSQL,
		request.ChainID, request.resourceKey(), address, request.TokenID,
		strconv.FormatUint(request.BlockNumber, 10), hash, request.SourceURI,
	).Scan(&matches)
	if errors.Is(err, sql.ErrNoRows) {
		return Current{}, nil
	}
	if err != nil {
		return Current{}, err
	}
	var canonical bool
	if err := tx.QueryRowContext(ctx, canonicalObservationSQL,
		request.ChainID, strconv.FormatUint(request.BlockNumber, 10), hash,
	).Scan(&canonical); err != nil {
		return Current{}, err
	}
	return Current{Resource: matches, Canonical: canonical}, nil
}

func lockOwnedJob(ctx context.Context, tx *sql.Tx, lease Lease) error {
	var payload []byte
	var chainID string
	var maxAttempts int64
	err := tx.QueryRowContext(ctx, lockOwnedMetadataJobSQL, lease.JobID, lease.Token).Scan(&chainID, &payload, &maxAttempts)
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
		result, err := tx.ExecContext(ctx, finishMetadataResourceSQL,
			lease.Request.ChainID, lease.Request.resourceKey(), lease.Request.SourceURI,
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
	if _, err := tx.ExecContext(ctx, insertMetadataAttemptSQL,
		lease.Request.ChainID, lease.Request.resourceKey(), lease.JobID, lease.Attempt,
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
	result, err := tx.ExecContext(ctx, finishMetadataJobSQL,
		lease.JobID, lease.Token, jobStatus, string(summary), errorText,
	)
	if err != nil {
		return fmt.Errorf("finish metadata durable job: %w", err)
	}
	return requireOne(result)
}

func encodePayload(request NFTRequest) ([]byte, error) {
	payload, err := json.Marshal(durablePayload{
		ChainID: request.ChainID, ResourceKey: request.resourceKey(), Token: strings.ToLower(request.Token.String()), TokenID: request.TokenID,
		BlockNumber: strconv.FormatUint(request.BlockNumber, 10), BlockHash: strings.ToLower(request.BlockHash.String()),
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
	return left.ChainID == right.ChainID && left.Token.Equal(right.Token) && left.TokenID == right.TokenID &&
		left.BlockNumber == right.BlockNumber && left.BlockHash.Equal(right.BlockHash) && left.SourceURI == right.SourceURI
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

func mustHashBytes(hash ethrpc.Hash) []byte {
	value, _ := hash.Bytes()
	return value
}

func hashString(outcome Outcome) any {
	if outcome.State != StateAvailable {
		return nil
	}
	return "0x" + fmt.Sprintf("%x", outcome.ContentHash[:])
}

const canonicalObservationSQL = `
SELECT EXISTS (
    SELECT 1 FROM canonical_blocks
    WHERE chain_id = $1::numeric AND number = $2::numeric AND block_hash = $3
)`

const canonicalNFTContractSQL = `
SELECT EXISTS (
    SELECT 1
    FROM token_contracts AS token
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = token.chain_id
     AND canonical.number = token.observed_block_number
     AND canonical.block_hash = token.observed_block_hash
    WHERE token.chain_id = $1::numeric
      AND token.address = $2
      AND token.standard IN ('erc721', 'erc1155')
)`

const insertMetadataResourceSQL = `
INSERT INTO external_metadata (
    chain_id, resource_kind, resource_key, source_uri, state,
    token_address, token_id, observed_block_number, observed_block_hash,
    identity_hash, attempt_count, updated_at
) VALUES (
    $1::numeric, 'nft', $2, $3, 'pending',
    $4, $5::numeric, $6::numeric, $7,
    $7, 0, clock_timestamp()
)
ON CONFLICT DO NOTHING
RETURNING 1`

const existingMetadataResourceSQL = `
SELECT resource_key, source_uri, token_address, token_id::text,
       observed_block_number::text, observed_block_hash
FROM external_metadata
WHERE chain_id = $1::numeric AND resource_kind = 'nft'
  AND token_address = $2 AND token_id = $3::numeric AND observed_block_hash = $4
FOR UPDATE`

const enqueueMetadataJobSQL = `
INSERT INTO durable_jobs (
    chain_id, kind, stage, stage_version, idempotency_key, payload,
    priority, max_attempts
) VALUES (
    $1::numeric, 'metadata', 'nft-metadata', 1, $2,
    $3::jsonb, $4, $5
)
ON CONFLICT (chain_id, kind, idempotency_key) DO NOTHING
RETURNING id`

const existingMetadataJobSQL = `
SELECT id FROM durable_jobs
WHERE chain_id = $1::numeric AND kind = 'metadata' AND idempotency_key = $2`

const exhaustMetadataJobsSQL = `
WITH exhausted AS (
    UPDATE durable_jobs
    SET status = 'failed',
        result = jsonb_build_object('state', 'error', 'code', 'attempts_exhausted'),
        last_error = COALESCE(last_error, 'maximum metadata attempts exhausted'),
        leased_by = NULL, lease_token = NULL, lease_expires_at = NULL,
        updated_at = clock_timestamp()
    WHERE kind = 'metadata'
      AND chain_id = $1::numeric
      AND stage = 'nft-metadata' AND stage_version = 1
      AND attempts >= max_attempts
      AND ((status = 'queued' AND available_at <= clock_timestamp())
        OR (status = 'leased' AND lease_expires_at <= clock_timestamp()))
    RETURNING id, chain_id, attempts, payload, last_error
), updated AS (
    UPDATE external_metadata AS metadata
    SET state = 'error', attempt_count = exhausted.attempts,
        last_error_code = 'attempts_exhausted', last_error = exhausted.last_error,
        fetched_at = clock_timestamp(), terminal_at = clock_timestamp(), updated_at = clock_timestamp()
    FROM exhausted
    WHERE metadata.chain_id = exhausted.chain_id
      AND metadata.resource_kind = 'nft'
      AND metadata.resource_key = exhausted.payload->>'resource_key'
      AND metadata.identity_hash = decode(substr(exhausted.payload->>'block_hash', 3), 'hex')
      AND metadata.source_uri = exhausted.payload->>'source_uri'
      AND metadata.observed_block_number = (exhausted.payload->>'block_number')::numeric
      AND metadata.observed_block_hash = decode(substr(exhausted.payload->>'block_hash', 3), 'hex')
    RETURNING exhausted.id, exhausted.chain_id, exhausted.attempts,
        exhausted.payload, exhausted.last_error
)
INSERT INTO external_metadata_attempts (
    chain_id, resource_kind, resource_key, durable_job_id, attempt, state,
    source_uri, error_code, error_message
)
SELECT chain_id, 'nft', payload->>'resource_key', id, attempts, 'error',
       payload->>'source_uri', 'attempts_exhausted', left(last_error, 1024)
FROM updated
ON CONFLICT (durable_job_id, attempt) DO NOTHING`

const claimMetadataJobSQL = `
WITH candidate AS (
    SELECT id FROM durable_jobs
    WHERE kind = 'metadata'
      AND chain_id = $4::numeric
      AND stage = 'nft-metadata' AND stage_version = 1
      AND attempts < max_attempts
      AND ((status = 'queued' AND available_at <= clock_timestamp())
        OR (status = 'leased' AND lease_expires_at <= clock_timestamp()))
    ORDER BY priority DESC, available_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE durable_jobs AS job
SET status = 'leased', attempts = job.attempts + 1,
    leased_by = $1, lease_token = $2,
    lease_expires_at = clock_timestamp() + ($3 * INTERVAL '1 microsecond'),
    result = NULL, updated_at = clock_timestamp()
FROM candidate
WHERE job.id = candidate.id
RETURNING job.id, job.chain_id::text, job.attempts, job.max_attempts, job.payload`

const renewMetadataJobSQL = `
UPDATE durable_jobs
SET lease_expires_at = clock_timestamp() + ($3 * INTERVAL '1 microsecond'),
    updated_at = clock_timestamp()
WHERE id = $1 AND kind = 'metadata' AND status = 'leased'
  AND lease_token = $2 AND lease_expires_at > clock_timestamp()`

const currentMetadataResourceSQL = `
SELECT
    EXISTS (
        SELECT 1 FROM external_metadata
        WHERE chain_id = $1::numeric AND resource_kind = 'nft' AND resource_key = $2
          AND identity_hash = $6
          AND token_address = $3 AND token_id = $4::numeric
          AND observed_block_number = $5::numeric AND observed_block_hash = $6
          AND source_uri = $7
    ),
    EXISTS (
        SELECT 1 FROM canonical_blocks
        WHERE chain_id = $1::numeric AND number = $5::numeric AND block_hash = $6
    )`

const lockMetadataResourceSQL = `
SELECT token_address = $3
   AND token_id = $4::numeric
   AND observed_block_number = $5::numeric
   AND observed_block_hash = $6
   AND source_uri = $7
FROM external_metadata
WHERE chain_id = $1::numeric AND resource_kind = 'nft' AND resource_key = $2
  AND identity_hash = $6
FOR UPDATE`

const lockOwnedMetadataJobSQL = `
SELECT chain_id::text, payload, max_attempts
FROM durable_jobs
WHERE id = $1 AND kind = 'metadata' AND status = 'leased'
  AND lease_token = $2 AND lease_expires_at > clock_timestamp()
FOR UPDATE`

const finishMetadataResourceSQL = `
UPDATE external_metadata
SET state = $6, resolved_uri = $7, media_type = $8, content_hash = $9,
    document = $10::jsonb, content_size = $11, attempt_count = $12,
    last_error_code = $13, last_error = $14,
    fetched_at = clock_timestamp(), terminal_at = clock_timestamp(), updated_at = clock_timestamp()
WHERE chain_id = $1::numeric AND resource_kind = 'nft' AND resource_key = $2
  AND identity_hash = $5
  AND source_uri = $3 AND observed_block_number = $4::numeric AND observed_block_hash = $5`

const recordMetadataRetrySQL = `
UPDATE external_metadata
SET state = 'pending', attempt_count = $6, last_error_code = $7, last_error = $8,
    fetched_at = clock_timestamp(), terminal_at = NULL, updated_at = clock_timestamp()
WHERE chain_id = $1::numeric AND resource_kind = 'nft' AND resource_key = $2
  AND identity_hash = $5
  AND source_uri = $3 AND observed_block_number = $4::numeric AND observed_block_hash = $5`

const insertMetadataAttemptSQL = `
INSERT INTO external_metadata_attempts (
    chain_id, resource_kind, resource_key, durable_job_id, attempt, state,
    source_uri, resolved_uri, media_type, content_hash, content_size,
    error_code, error_message
) VALUES (
    $1::numeric, 'nft', $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
)
ON CONFLICT (durable_job_id, attempt) DO UPDATE SET
    state = EXCLUDED.state, resolved_uri = EXCLUDED.resolved_uri,
    media_type = EXCLUDED.media_type, content_hash = EXCLUDED.content_hash,
    content_size = EXCLUDED.content_size, error_code = EXCLUDED.error_code,
    error_message = EXCLUDED.error_message, attempted_at = clock_timestamp()`

const finishMetadataJobSQL = `
UPDATE durable_jobs
SET status = $3, result = $4::jsonb, last_error = $5,
    leased_by = NULL, lease_token = NULL, lease_expires_at = NULL,
    updated_at = clock_timestamp()
WHERE id = $1 AND kind = 'metadata' AND status = 'leased'
  AND lease_token = $2 AND lease_expires_at > clock_timestamp()`

const retryMetadataJobSQL = `
UPDATE durable_jobs
SET status = 'queued', available_at = clock_timestamp() + ($4 * INTERVAL '1 microsecond'),
    last_error = $3, result = NULL,
    leased_by = NULL, lease_token = NULL, lease_expires_at = NULL,
    updated_at = clock_timestamp()
WHERE id = $1 AND kind = 'metadata' AND status = 'leased'
  AND lease_token = $2 AND lease_expires_at > clock_timestamp()`
