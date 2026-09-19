// Package genesis authenticates and imports operator-supplied genesis account
// state after the canonical block-zero core fact becomes available.
package genesis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"
	pgxpool "github.com/jackc/pgx/v5/pgxpool"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/config"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/stagecontract"
)

const (
	maximumDocumentBytes = int64(64 << 20)
	maximumAccounts      = 500_000
)

var errBlockZeroPending = errors.New("canonical block zero is not available")

var (
	errConfiguredGenesisHashMismatch = errors.New(
		"genesis document block hash does not match configured genesis hash",
	)
	errCanonicalGenesisHashMismatch = errors.New(
		"genesis document block hash does not match canonical block zero",
	)
	errCanonicalGenesisRootMismatch = errors.New(
		"genesis document state root does not match canonical block zero",
	)
	errStoredGenesisDigestMismatch = errors.New(
		"stored completed genesis import conflicts with configured SHA-256",
	)
)

// Importer is a long-lived sync-role component. It retries only while block
// zero has not reached PostgreSQL; malformed or mismatched input fails closed.
type Importer struct {
	db           *pgxpool.Pool
	chainID      string
	chainNumber  uint64
	expectedHash common.Hash
	file         string
	remote       *remoteSource
	digest       [32]byte
	spec         *core.Genesis
	block        *types.Block
	queue        *enrich.PostgresJobQueue
	pollInterval time.Duration
	logger       *slog.Logger
}

type remoteImportCheckpoint struct {
	version string
	state   string
}

func NewImporter(
	db *pgxpool.Pool,
	chain config.ChainConfig,
	queue *enrich.PostgresJobQueue,
	pollInterval time.Duration,
	loggers ...*slog.Logger,
) (*Importer, error) {
	if db == nil {
		return nil, errors.New("genesis importer database is nil")
	}
	if chain.ID == 0 {
		return nil, errors.New("genesis importer chain ID is zero")
	}
	if chain.GenesisFile != "" && chain.GenesisURL != "" {
		return nil, errors.New("genesis importer file and URL are mutually exclusive")
	}
	if (chain.GenesisFile != "" || chain.GenesisURL != "") && chain.StartBlock != 0 {
		return nil, errors.New("genesis importer requires indexing from block zero")
	}
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	importer := &Importer{
		db: db, chainID: strconv.FormatUint(chain.ID, 10), chainNumber: chain.ID,
		file: chain.GenesisFile, queue: queue, pollInterval: pollInterval,
	}
	importer.logger = slog.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		importer.logger = loggers[0]
	}
	if chain.GenesisHash != "" {
		configuredHash, err := parseHashText(chain.GenesisHash)
		if err != nil {
			return nil, errors.New("genesis importer configured hash is invalid")
		}
		importer.expectedHash = configuredHash
	}
	if chain.GenesisURL != "" {
		remote, err := newRemoteSource(
			chain.GenesisURL,
			chain.GenesisSHA256,
			chain.GenesisFetchTimeout,
		)
		if err != nil {
			return nil, err
		}
		importer.remote = remote
		return importer, nil
	}
	if chain.GenesisFile == "" {
		return importer, nil
	}
	document, err := readBoundedFile(chain.GenesisFile)
	if err != nil {
		return nil, err
	}
	if err := importer.prepareDocument(document, sha256.Sum256(document)); err != nil {
		return nil, err
	}
	return importer, nil
}

func (importer *Importer) prepareDocument(document []byte, digest [sha256.Size]byte) error {
	spec, block, err := parseDocument(document, importer.chainNumber)
	if err != nil {
		return err
	}
	if importer.expectedHash != (common.Hash{}) &&
		!bytes.Equal(importer.expectedHash[:], block.Hash().Bytes()) {
		return errConfiguredGenesisHashMismatch
	}
	importer.digest = digest
	importer.spec = spec
	importer.block = block
	return nil
}

func (*Importer) Name() string { return "genesis-state-importer" }

func (importer *Importer) Run(ctx context.Context) error {
	if importer == nil || importer.db == nil {
		return errors.New("run nil genesis importer")
	}
	if importer.remote != nil {
		return importer.runRemote(ctx)
	}
	if importer.file == "" {
		if err := importer.markUnavailable(ctx); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}
	return importer.runPrepared(ctx)
}

func (importer *Importer) runPrepared(ctx context.Context) error {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			_, _, err := importer.importOnce(ctx)
			if errors.Is(err, errBlockZeroPending) {
				timer.Reset(importer.pollInterval)
				continue
			}
			if err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		}
	}
}

func (importer *Importer) runRemote(ctx context.Context) error {
	for {
		before, complete, err := importer.completedRemoteImport(ctx, importer.db)
		if err != nil {
			return err
		}
		if complete {
			<-ctx.Done()
			return ctx.Err()
		}
		if err := importer.waitForCanonicalBlockZero(ctx); err != nil {
			return err
		}
		err = importer.withRemoteSourceLock(ctx, func(conn *pgxpool.Conn) error {
			after, complete, err := importer.completedRemoteImport(ctx, conn)
			if err != nil || complete {
				return err
			}
			if after.version != before.version {
				return concurrentRemoteFailure(after)
			}
			document, err := importer.remote.fetch(ctx)
			if err != nil {
				return importer.recordRemoteFetchFailure(ctx, conn, err)
			}
			if err := importer.prepareDocument(document.Bytes, document.SHA256); err != nil {
				code := "genesis_remote_document_invalid"
				if errors.Is(err, errConfiguredGenesisHashMismatch) {
					code = "genesis_remote_block_hash_mismatch"
				}
				return importer.recordRemoteFailure(
					ctx,
					conn,
					remoteFailureFailed,
					code,
					remoteFailure(remoteFailureFailed, code),
				)
			}
			_, _, err = importer.importOnceUsing(ctx, conn)
			if errors.Is(err, errCanonicalGenesisHashMismatch) {
				return importer.recordRemoteFailure(
					ctx,
					conn,
					remoteFailureFailed,
					"genesis_remote_block_hash_mismatch",
					remoteFailure(remoteFailureFailed, "genesis_remote_block_hash_mismatch"),
				)
			}
			if errors.Is(err, errCanonicalGenesisRootMismatch) {
				return importer.recordRemoteFailure(
					ctx,
					conn,
					remoteFailureFailed,
					"genesis_remote_state_root_mismatch",
					remoteFailure(remoteFailureFailed, "genesis_remote_state_root_mismatch"),
				)
			}
			if err != nil {
				return err
			}
			_, complete, err = importer.completedRemoteImport(ctx, conn)
			if err != nil {
				return err
			}
			if !complete {
				return errors.New("remote genesis import did not publish a complete identity")
			}
			return nil
		})
		if errors.Is(err, errBlockZeroPending) {
			continue
		}
		if err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}
}

func (importer *Importer) completedRemoteImport(
	ctx context.Context,
	queryer dbgen.DBTX,
) (remoteImportCheckpoint, bool, error) {
	var (
		checkpoint                        remoteImportCheckpoint
		blockHash, stateRoot, digest, raw []byte
		canonical                         bool
	)
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(importer.chainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(queryer).GenesisWriteCompletedRemoteImportStatement1(ctx, queryValue0)
		if err != nil {
			return err
		}
		checkpoint.version = queryRow.ImportedXmin
		checkpoint.state = queryRow.State
		blockHash = queryRow.BlockHash
		stateRoot = queryRow.StateRoot
		digest = queryRow.DocumentSha256
		canonical = queryRow.Canonical
		raw = queryRow.Raw
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		return remoteImportCheckpoint{}, false, nil
	}
	if err != nil {
		return remoteImportCheckpoint{}, false, fmt.Errorf("read completed genesis import: %w", err)
	}
	if checkpoint.state != "complete" {
		return checkpoint, false, nil
	}
	if !canonical || len(blockHash) != common.HashLength ||
		len(stateRoot) != common.HashLength || len(digest) != sha256.Size {
		return checkpoint, false, errors.New("stored completed genesis import identity is invalid")
	}
	canonicalRoot, err := stateRootFromBlock(raw)
	if err != nil {
		return checkpoint, false, errors.New("stored completed genesis import canonical state root is invalid")
	}
	if !bytes.Equal(stateRoot, canonicalRoot[:]) {
		return checkpoint, false, errors.New("stored completed genesis import conflicts with canonical block zero state root")
	}
	if importer.expectedHash != (common.Hash{}) &&
		!bytes.Equal(importer.expectedHash[:], blockHash) {
		return checkpoint, false, errors.New("stored completed genesis import conflicts with configured genesis hash")
	}
	if importer.remote != nil && importer.remote.expectedDigest != nil &&
		!bytes.Equal(importer.remote.expectedDigest[:], digest) {
		return checkpoint, false, errStoredGenesisDigestMismatch
	}
	return checkpoint, true, nil
}

func concurrentRemoteFailure(checkpoint remoteImportCheckpoint) error {
	switch checkpoint.state {
	case string(remoteFailureFailed):
		return remoteFailure(remoteFailureFailed, "genesis_remote_failed")
	case string(remoteFailureUnavailable):
		return remoteFailure(remoteFailureUnavailable, "genesis_remote_unavailable")
	default:
		return remoteFailure(remoteFailureUnavailable, "genesis_remote_state_changed")
	}
}

func (importer *Importer) waitForCanonicalBlockZero(ctx context.Context) error {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			var available bool
			if err := func() error {
				var queryValue0 pgtype.Numeric
				if err := queryValue0.Scan(importer.chainID); err != nil {
					return err
				}
				queryRow, err := dbgen.New(importer.db).GenesisWriteWaitForCanonicalBlockZeroStatement1(ctx, queryValue0)
				if err != nil {
					return err
				}
				available = queryRow
				return nil
			}(); err != nil {
				return fmt.Errorf("wait for canonical block zero: %w", err)
			}
			if available {
				return nil
			}
			timer.Reset(importer.pollInterval)
		}
	}
}

func (importer *Importer) withRemoteSourceLock(
	ctx context.Context,
	action func(*pgxpool.Conn) error,
) (result error) {
	conn, err := importer.db.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("reserve genesis remote source connection: %w", err)
	}
	locked := false
	uncertain := false
	defer func() {
		if locked {
			unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			var unlocked bool
			unlockErr := func() error {

				queryRow, err := dbgen.New(conn).GenesisWriteWithRemoteSourceLockStatement1(unlockCtx, importer.chainID)
				if err != nil {
					return err
				}
				unlocked = queryRow
				return nil
			}()
			cancel()
			if unlockErr != nil || !unlocked {
				uncertain = true
				result = errors.Join(result, errors.New("release genesis remote source lock"))
			}
		}
		if uncertain {
			dbaccess.Discard(ctx, conn)
		} else {
			conn.Release()
		}
	}()
	for !locked {
		if err := func() error {

			queryRow, err := dbgen.New(conn).GenesisWriteWithRemoteSourceLockStatement2(ctx, importer.chainID)
			if err != nil {
				return err
			}
			locked = queryRow
			return nil
		}(); err != nil {
			uncertain = true
			return fmt.Errorf("lock genesis remote source: %w", err)
		}
		if !locked {
			timer := time.NewTimer(importer.pollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return action(conn)
}

func (importer *Importer) recordRemoteFetchFailure(
	ctx context.Context,
	execer dbgen.DBTX,
	fetchErr error,
) error {
	kind, code, ok := remoteErrorDetails(fetchErr)
	if !ok {
		kind, code = remoteFailureUnavailable, "genesis_remote_unavailable"
		fetchErr = remoteFailure(kind, code)
	}
	return importer.recordRemoteFailure(ctx, execer, kind, code, fetchErr)
}

func (importer *Importer) recordRemoteFailure(
	ctx context.Context,
	execer dbgen.DBTX,
	kind remoteFailureKind,
	code string,
	cause error,
) error {
	if kind != remoteFailureUnavailable && kind != remoteFailureFailed {
		return errors.New("record genesis remote failure with invalid state")
	}
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(importer.chainID); err != nil {
			return err
		}
		return dbgen.New(execer).GenesisWriteRecordRemoteFailureStatement1(ctx, queryValue0, string(kind), new(code))
	}(); err != nil {
		return fmt.Errorf("publish genesis remote source failure: %w", err)
	}
	importer.logger.WarnContext(ctx, "genesis state import transitioned",
		"event", "genesis_state_transitioned", "component", importer.Name(),
		"source", "https", "transition", slog.GroupValue(
			slog.String("state", string(kind)), slog.String("code", code),
		),
	)
	return cause
}

func (importer *Importer) markUnavailable(ctx context.Context) error {
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(importer.chainID); err != nil {
			return err
		}
		return dbgen.New(importer.db).GenesisWriteMarkUnavailableStatement1(ctx, queryValue0)
	}()
	if err != nil {
		return fmt.Errorf("publish unavailable genesis state: %w", err)
	}
	importer.logger.InfoContext(ctx, "genesis state import transitioned",
		"event", "genesis_state_transitioned", "component", importer.Name(),
		"source", "none", "transition", slog.GroupValue(
			slog.String("state", "unavailable"), slog.String("code", "genesis_file_not_configured"),
		),
	)
	return nil
}

func (importer *Importer) importOnce(ctx context.Context) (common.Hash, common.Hash, error) {
	return importer.importOnceUsing(ctx, importer.db)
}

func (importer *Importer) importOnceUsing(
	ctx context.Context,
	beginner interface {
		BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
	},
) (common.Hash, common.Hash, error) {
	tx, err := beginner.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return common.Hash{}, common.Hash{}, fmt.Errorf("begin genesis state import: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)
	if err := dbgen.New(tx).GenesisWriteImportOnceUsingStatement1(ctx, importer.chainID); err != nil {
		return common.Hash{}, common.Hash{}, fmt.Errorf("lock genesis state import: %w", err)
	}
	var blockHash, raw []byte
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(importer.chainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).GenesisWriteImportOnceUsingStatement2(ctx, queryValue0)
		if err != nil {
			return err
		}
		blockHash = queryRow.Hash
		raw = queryRow.Raw
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		return common.Hash{}, common.Hash{}, errBlockZeroPending
	}
	if err != nil {
		return common.Hash{}, common.Hash{}, fmt.Errorf("read canonical block zero: %w", err)
	}
	if len(blockHash) != common.HashLength {
		return common.Hash{}, common.Hash{}, errors.New("stored block-zero hash is invalid")
	}
	reference := common.BytesToHash(blockHash)
	stateRoot, err := stateRootFromBlock(raw)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	if !bytes.Equal(blockHash, importer.block.Hash().Bytes()) {
		return common.Hash{}, common.Hash{}, errCanonicalGenesisHashMismatch
	}
	if !bytes.Equal(stateRoot[:], importer.block.Root().Bytes()) {
		return common.Hash{}, common.Hash{}, errCanonicalGenesisRootMismatch
	}
	var existingState string
	var existingHash, existingRoot, existingDigest []byte
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(importer.chainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).GenesisWriteImportOnceUsingStatement3(ctx, queryValue0)
		if err != nil {
			return err
		}
		existingState = queryRow.State
		existingHash = queryRow.BlockHash
		existingRoot = queryRow.StateRoot
		existingDigest = queryRow.DocumentSha256
		return nil
	}()
	if err == nil && existingState == "complete" {
		if !bytes.Equal(existingHash, blockHash) || !bytes.Equal(existingRoot, stateRoot[:]) {
			return common.Hash{}, common.Hash{}, errors.New("stored genesis import identity conflicts with canonical block zero")
		}
		if importer.remote != nil && importer.remote.expectedDigest != nil &&
			!bytes.Equal(existingDigest, importer.remote.expectedDigest[:]) {
			return common.Hash{}, common.Hash{}, errStoredGenesisDigestMismatch
		}
		if err := tx.Commit(ctx); err != nil {
			return common.Hash{}, common.Hash{}, fmt.Errorf("commit existing genesis import: %w", err)
		}
		return reference, stateRoot, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return common.Hash{}, common.Hash{}, fmt.Errorf("read genesis import state: %w", err)
	}
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(importer.chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.Itoa(len(importer.spec.Alloc))); err != nil {
			return err
		}
		return dbgen.New(tx).GenesisWriteImportOnceUsingStatement4(ctx, dbgen.GenesisWriteImportOnceUsingStatement4Params{ChainID: queryValue0, BlockHash: blockHash, StateRoot: stateRoot[:], DocumentSha256: importer.digest[:], AccountCount: queryValue1})
	}(); err != nil {
		return common.Hash{}, common.Hash{}, fmt.Errorf("persist genesis import identity: %w", err)
	}
	addresses := make([]common.Address, 0, len(importer.spec.Alloc))
	for accountAddress := range importer.spec.Alloc {
		addresses = append(addresses, accountAddress)
	}
	sort.Slice(addresses, func(i, j int) bool {
		return bytes.Compare(addresses[i][:], addresses[j][:]) < 0
	})
	for _, accountAddress := range addresses {
		account := importer.spec.Alloc[accountAddress]
		storageHash, err := storageRoot(account.Storage)
		if err != nil {
			return common.Hash{}, common.Hash{}, errors.New("compute genesis account storage root")
		}
		code := account.Code
		if code == nil {
			code = []byte{}
		}
		codeHash := crypto.Keccak256Hash(code)
		if err := func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(importer.chainID); err != nil {
				return err
			}
			var queryValue1 pgtype.Numeric
			if err := queryValue1.Scan(account.Balance.String()); err != nil {
				return err
			}
			var queryValue2 pgtype.Numeric
			if err := queryValue2.Scan(strconv.FormatUint(account.Nonce, 10)); err != nil {
				return err
			}
			return dbgen.New(tx).GenesisWriteImportOnceUsingStatement5(ctx, dbgen.GenesisWriteImportOnceUsingStatement5Params{ChainID: queryValue0, Address: accountAddress[:], BlockHash: blockHash, Balance: queryValue1, Nonce: queryValue2, CodeHash: codeHash[:], Code: code, StorageRoot: storageHash[:]})
		}(); err != nil {
			return common.Hash{}, common.Hash{}, fmt.Errorf("persist genesis account: %w", err)
		}
		if len(code) > 0 {
			result, err := func() (int64, error) {
				var queryValue0 pgtype.Numeric
				if err := queryValue0.Scan(importer.chainID); err != nil {
					return 0, err
				}
				return dbgen.New(tx).GenesisWriteImportOnceUsingStatement6(ctx, dbgen.GenesisWriteImportOnceUsingStatement6Params{ChainID: queryValue0, Address: accountAddress[:], BlockHash: blockHash, CodeHash: codeHash[:], Code: code})
			}()
			if err != nil {
				return common.Hash{}, common.Hash{}, fmt.Errorf("persist genesis code observation: %w", err)
			}
			affected := result
			if affected != 1 {
				return common.Hash{}, common.Hash{}, errors.New("genesis code observation conflicts with an existing exact fact")
			}
		}
	}
	if err := importer.requestProxyReplay(ctx, tx, reference, stateRoot); err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return common.Hash{}, common.Hash{}, fmt.Errorf("commit genesis state import: %w", err)
	}
	source := "file"
	if importer.remote != nil {
		source = "https"
	}
	importer.logger.InfoContext(ctx, "genesis state import transitioned",
		"event", "genesis_state_transitioned", "component", importer.Name(),
		"source", source,
		"block", slog.GroupValue(
			slog.String("number", "0"), slog.String("hash", strings.ToLower(reference.Hex())),
			slog.String("state_root", strings.ToLower(stateRoot.Hex())),
		),
		"document_sha256", fmt.Sprintf("%x", importer.digest),
		"account_count", len(importer.spec.Alloc),
		"transition", slog.GroupValue(slog.String("state", "complete")),
	)
	return reference, stateRoot, nil
}

func (importer *Importer) requestProxyReplay(
	ctx context.Context,
	tx pgx.Tx,
	blockHash common.Hash,
	stateRoot common.Hash,
) error {
	if importer.queue == nil {
		return nil
	}
	_, err := importer.queue.EnqueueTx(ctx, tx, stagecontract.EnqueueRequest{
		Stage: stagecontract.Proxy, ChainID: importer.chainID,
		BlockHash: blockHash, BlockNumber: 0,
		Replay: stagecontract.ReplaySource{
			Kind: "genesis-import",
			Key:  blockHash.String() + ":" + stateRoot.String(),
		},
	})
	if err != nil {
		return fmt.Errorf("request proxy replay after genesis import: %w", err)
	}
	return nil
}

func readBoundedFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open genesis file: %w", err)
	}
	defer file.Close() //nolint:errcheck
	document, err := io.ReadAll(io.LimitReader(file, maximumDocumentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read genesis file: %w", err)
	}
	if int64(len(document)) > maximumDocumentBytes {
		return nil, errors.New("genesis file exceeds 67108864 bytes")
	}
	return document, nil
}
