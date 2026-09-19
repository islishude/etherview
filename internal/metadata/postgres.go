package metadata

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"

	"github.com/ethereum/go-ethereum/common"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
)

var (
	ErrLeaseLost                = errors.New("metadata job lease is no longer owned")
	ErrExactNFTMetadataConflict = errors.New("exact NFT metadata observation conflicts with persisted source")
)

type PostgresRepository struct {
	db      dbaccess.Database
	chainID string
	random  io.Reader
}

func NewPostgresRepository(db dbaccess.Database, chainID string) (*PostgresRepository, error) {
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

	tx, err := repository.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("begin metadata enqueue transaction: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)
	var canonical bool
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(request.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(request.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).MetadataCanonicalObservation(ctx, queryValue0, queryValue1, blockHash)
		if err != nil {
			return err
		}
		canonical = queryRow
		return nil
	}(); err != nil {
		return EnqueueResult{}, fmt.Errorf("check metadata source canonicality: %w", err)
	}
	if !canonical {
		return EnqueueResult{}, errors.New("metadata source block is not canonical")
	}
	var nftContract bool
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(request.ChainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).MetadataCanonicalNFTContract(ctx, queryValue0, address)
		if err != nil {
			return err
		}
		nftContract = queryRow
		return nil
	}(); err != nil {
		return EnqueueResult{}, fmt.Errorf("check metadata NFT contract: %w", err)
	}
	if !nftContract {
		return EnqueueResult{}, errors.New("metadata token address is not a canonical ERC-721 or ERC-1155 contract")
	}

	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(request.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(request.TokenID); err != nil {
			return err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(strconv.FormatUint(request.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).MetadataWriteInsertMetadataResource(ctx, dbgen.MetadataWriteInsertMetadataResourceParams{ChainID: queryValue0, ResourceKey: request.resourceKey(), SourceUri: request.SourceURI, TokenAddress: address, TokenID: queryValue1, ObservedBlockNumber: queryValue2, ObservedBlockHash: blockHash})
		if err != nil {
			return err
		}
		_ = int(queryRow)
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		var (
			storedKey, storedURI, storedBlockNumber string
			storedAddress, storedBlockHash          []byte
			storedTokenID                           string
		)
		err = func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(request.ChainID); err != nil {
				return err
			}
			var queryValue1 pgtype.Numeric
			if err := queryValue1.Scan(request.TokenID); err != nil {
				return err
			}
			queryRow, err := dbgen.New(tx).MetadataExistingMetadataResource(ctx, dbgen.MetadataExistingMetadataResourceParams{ChainID: queryValue0, TokenAddress: address, TokenID: queryValue1, ObservedBlockHash: blockHash})
			if err != nil {
				return err
			}
			storedKey = queryRow.ResourceKey
			storedURI = queryRow.SourceUri
			storedAddress = queryRow.TokenAddress
			storedTokenID = queryRow.TokenID
			storedBlockNumber = queryRow.ObservedBlockNumber
			storedBlockHash = queryRow.ObservedBlockHash
			return nil
		}()
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
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(request.ChainID); err != nil {
			return err
		}
		if request.MaxAttempts > 2147483647 {
			return errors.New("invalid stored query value")
		}
		queryRow, err := dbgen.New(tx).MetadataWriteEnqueueMetadataJob(ctx, dbgen.MetadataWriteEnqueueMetadataJobParams{ChainID: queryValue0, IdempotencyKey: key, Payload: []byte(string(payload)), Priority: request.Priority, MaxAttempts: int32(request.MaxAttempts)})
		if err != nil {
			return err
		}
		jobID = queryRow
		return nil
	}()
	created := err == nil
	if errors.Is(err, pgx.ErrNoRows) {
		err = func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(request.ChainID); err != nil {
				return err
			}
			queryRow, err := dbgen.New(tx).MetadataExistingMetadataJob(ctx, queryValue0, key)
			if err != nil {
				return err
			}
			jobID = queryRow
			return nil
		}()
	}
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("enqueue NFT metadata job: %w", err)
	}
	if jobID <= 0 {
		return EnqueueResult{}, errors.New("metadata database returned an invalid job ID")
	}
	if err := tx.Commit(ctx); err != nil {
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
	tx, err := repository.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Lease{}, false, fmt.Errorf("begin metadata claim transaction: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(repository.chainID); err != nil {
			return err
		}
		return dbgen.New(tx).MetadataExhaustMetadataJobs(ctx, queryValue0)
	}(); err != nil {
		return Lease{}, false, fmt.Errorf("finalize exhausted metadata jobs: %w", err)
	}
	var (
		jobID, attempt, maxAttempts int64
		chainID                     string
		payload                     []byte
	)
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(repository.chainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).MetadataClaimMetadataJob(ctx, dbgen.MetadataClaimMetadataJobParams{LeasedBy: new(workerID), LeaseToken: new(token), LeaseMicroseconds: leaseMicros, ChainID: queryValue0})
		if err != nil {
			return err
		}
		jobID = queryRow.ID
		chainID = queryRow.JobChainID
		attempt = int64(queryRow.Attempts)
		maxAttempts = int64(queryRow.MaxAttempts)
		payload = queryRow.Payload
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
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
	if err := tx.Commit(ctx); err != nil {
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
	result, err := dbgen.New(repository.db).MetadataWriteRenewMetadataJob(ctx, leaseMicros, lease.JobID, new(lease.Token))
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
	tx, err := repository.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin metadata finish transaction: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)
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
	if err := tx.Commit(ctx); err != nil {
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
	tx, err := repository.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin metadata retry transaction: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)
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
		result, err := func() (int64, error) {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(lease.Request.ChainID); err != nil {
				return 0, err
			}
			var queryValue1 pgtype.Numeric
			if err := queryValue1.Scan(strconv.FormatUint(lease.Request.BlockNumber, 10)); err != nil {
				return 0, err
			}
			if lease.Attempt > 2147483647 {
				return 0, errors.New("invalid stored query value")
			}
			return dbgen.New(tx).MetadataWriteRecordMetadataRetry(ctx, dbgen.MetadataWriteRecordMetadataRetryParams{ChainID: queryValue0, ResourceKey: lease.Request.resourceKey(), SourceUri: lease.Request.SourceURI, ObservedBlockNumber: queryValue1, IdentityHash: mustHashBytes(lease.Request.BlockHash), AttemptCount: int32(lease.Attempt), LastErrorCode: new(code), LastError: new(message)})
		}()
		if err != nil {
			return fmt.Errorf("record pending metadata retry: %w", err)
		}
		if err := requireOne(result); err != nil {
			return fmt.Errorf("record pending metadata retry: %w", err)
		}
		if err := func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(lease.Request.ChainID); err != nil {
				return err
			}
			if lease.Attempt > 2147483647 {
				return errors.New("invalid stored query value")
			}
			return dbgen.New(tx).MetadataWriteInsertMetadataAttempt(ctx, dbgen.MetadataWriteInsertMetadataAttemptParams{ChainID: queryValue0, ResourceKey: lease.Request.resourceKey(), DurableJobID: lease.JobID, Attempt: int32(lease.Attempt), State: string(StateError), SourceUri: lease.Request.SourceURI, ResolvedUri: nil, MediaType: nil, ContentHash: nil, ContentSize: nil, ErrorCode: new(code), ErrorMessage: new(message)})
		}(); err != nil {
			return fmt.Errorf("audit metadata retry: %w", err)
		}
		result, err = dbgen.New(tx).MetadataWriteRetryMetadataJob(ctx, dbgen.MetadataWriteRetryMetadataJobParams{ID: lease.JobID, LeaseToken: new(lease.Token), LastError: new(code + ": " + message), RetryMicroseconds: retryMicros})
		if err != nil {
			return fmt.Errorf("queue metadata retry: %w", err)
		}
		if err := requireOne(result); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit metadata retry transaction: %w", err)
	}
	return nil
}

type queryRower = dbgen.DBTX

func queryCurrent(ctx context.Context, queryer queryRower, request NFTRequest) (Current, error) {
	address := request.Token.Bytes()
	hash := request.BlockHash.Bytes()
	var current Current
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(request.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(request.TokenID); err != nil {
			return err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(strconv.FormatUint(request.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(queryer).MetadataCurrentMetadataResource(ctx, dbgen.MetadataCurrentMetadataResourceParams{ChainID: queryValue0, ResourceKey: request.resourceKey(), TokenAddress: address, TokenID: queryValue1, ObservedBlockNumber: queryValue2, IdentityHash: hash, SourceUri: request.SourceURI})
		if err != nil {
			return err
		}
		current.Resource = queryRow.Exists
		current.Canonical = queryRow.Exists_2
		return nil
	}(); err != nil {
		return Current{}, err
	}
	return current, nil
}

func lockCurrent(ctx context.Context, tx pgx.Tx, request NFTRequest) (Current, error) {
	address := request.Token.Bytes()
	hash := request.BlockHash.Bytes()
	var matches bool
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(request.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(request.TokenID); err != nil {
			return err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(strconv.FormatUint(request.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).MetadataLockMetadataResource(ctx, dbgen.MetadataLockMetadataResourceParams{ChainID: queryValue0, ResourceKey: request.resourceKey(), TokenAddress: address, TokenID: queryValue1, ObservedBlockNumber: queryValue2, ObservedBlockHash: hash, SourceUri: request.SourceURI})
		if err != nil {
			return err
		}
		if queryRow == nil {
			return errors.New("invalid stored query value")
		}
		matches = *queryRow
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		return Current{}, nil
	}
	if err != nil {
		return Current{}, err
	}
	var canonical bool
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(request.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(request.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).MetadataCanonicalObservation(ctx, queryValue0, queryValue1, hash)
		if err != nil {
			return err
		}
		canonical = queryRow
		return nil
	}(); err != nil {
		return Current{}, err
	}
	return Current{Resource: matches, Canonical: canonical}, nil
}

func lockOwnedJob(ctx context.Context, tx pgx.Tx, lease Lease) error {
	var payload []byte
	var chainID string
	var maxAttempts int64
	err := func() error {

		queryRow, err := dbgen.New(tx).MetadataLockOwnedMetadataJob(ctx, lease.JobID, new(lease.Token))
		if err != nil {
			return err
		}
		chainID = queryRow.ChainID
		payload = queryRow.Payload
		maxAttempts = int64(queryRow.MaxAttempts)
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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

func finishLocked(ctx context.Context, tx pgx.Tx, lease Lease, outcome Outcome, updateResource bool) error {
	if err := outcome.validate(); err != nil {
		return err
	}
	var resolvedURI *string
	var mediaType *string
	var contentHash []byte
	var document []byte
	var contentSize *int64
	var errorCode *string
	var errorText *string
	if outcome.State == StateAvailable {
		resolvedURI = new(outcome.ResolvedURI)
		mediaType = new(outcome.MediaType)
		contentHash = outcome.ContentHash[:]
		document = []byte(string(outcome.Document))
		contentSize = new(outcome.ContentSize)
	} else {
		errorCode = new(outcome.Code)
		errorText = new(outcome.Message)
	}
	if updateResource {
		result, err := func() (int64, error) {
			if lease.Attempt > 2147483647 {
				return 0, errors.New("invalid stored query value")
			}
			var queryValue1 pgtype.Numeric
			if err := queryValue1.Scan(lease.Request.ChainID); err != nil {
				return 0, err
			}
			var queryValue2 pgtype.Numeric
			if err := queryValue2.Scan(strconv.FormatUint(lease.Request.BlockNumber, 10)); err != nil {
				return 0, err
			}
			return dbgen.New(tx).MetadataWriteFinishMetadataResource(ctx, dbgen.MetadataWriteFinishMetadataResourceParams{State: string(outcome.State), ResolvedUri: resolvedURI, MediaType: mediaType, ContentHash: contentHash, Document: document, ContentSize: contentSize, AttemptCount: int32(lease.Attempt), LastErrorCode: errorCode, LastError: errorText, ChainID: queryValue1, ResourceKey: lease.Request.resourceKey(), IdentityHash: mustHashBytes(lease.Request.BlockHash), SourceUri: lease.Request.SourceURI, ObservedBlockNumber: queryValue2})
		}()
		if err != nil {
			return fmt.Errorf("persist metadata outcome: %w", err)
		}
		if err := requireOne(result); err != nil {
			return fmt.Errorf("persist metadata outcome: %w", err)
		}
	}
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(lease.Request.ChainID); err != nil {
			return err
		}
		if lease.Attempt > 2147483647 {
			return errors.New("invalid stored query value")
		}
		return dbgen.New(tx).MetadataWriteInsertMetadataAttempt(ctx, dbgen.MetadataWriteInsertMetadataAttemptParams{ChainID: queryValue0, ResourceKey: lease.Request.resourceKey(), DurableJobID: lease.JobID, Attempt: int32(lease.Attempt), State: string(outcome.State), SourceUri: lease.Request.SourceURI, ResolvedUri: resolvedURI, MediaType: mediaType, ContentHash: contentHash, ContentSize: contentSize, ErrorCode: errorCode, ErrorMessage: errorText})
	}(); err != nil {
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
	result, err := dbgen.New(tx).MetadataWriteFinishMetadataJob(ctx, dbgen.MetadataWriteFinishMetadataJobParams{Status: jobStatus, Result: []byte(string(summary)), LastError: errorText, ID: lease.JobID, LeaseToken: new(lease.Token)})
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

func requireOne(result int64) error {
	affected := result
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
