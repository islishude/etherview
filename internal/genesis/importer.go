// Package genesis authenticates and imports operator-supplied genesis account
// state after the canonical block-zero core fact becomes available.
package genesis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
)

const (
	maximumDocumentBytes = int64(64 << 20)
	maximumAccounts      = 500_000
)

var errBlockZeroPending = errors.New("canonical block zero is not available")

// Importer is a long-lived sync-role component. It retries only while block
// zero has not reached PostgreSQL; malformed or mismatched input fails closed.
type Importer struct {
	db           *sql.DB
	chainID      string
	expectedHash enrich.Word
	file         string
	digest       [32]byte
	spec         *genesisSpec
	block        *genesisBlockData
	queue        *enrich.PostgresJobQueue
	pollInterval time.Duration
}

func NewImporter(
	db *sql.DB,
	chain config.ChainConfig,
	queue *enrich.PostgresJobQueue,
	pollInterval time.Duration,
) (*Importer, error) {
	if db == nil {
		return nil, errors.New("genesis importer database is nil")
	}
	if chain.ID == 0 {
		return nil, errors.New("genesis importer chain ID is zero")
	}
	if chain.GenesisFile != "" && chain.StartBlock != 0 {
		return nil, errors.New("genesis importer requires indexing from block zero")
	}
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	importer := &Importer{
		db: db, chainID: strconv.FormatUint(chain.ID, 10), file: chain.GenesisFile,
		queue: queue, pollInterval: pollInterval,
	}
	if chain.GenesisHash != "" {
		configuredHash, err := ethrpc.ParseHash(chain.GenesisHash)
		if err != nil {
			return nil, errors.New("genesis importer configured hash is invalid")
		}
		decoded, err := configuredHash.Bytes()
		if err != nil {
			return nil, errors.New("genesis importer configured hash is invalid")
		}
		importer.expectedHash, err = enrich.WordFromBytes(decoded)
		if err != nil {
			return nil, errors.New("genesis importer configured hash is invalid")
		}
	}
	if chain.GenesisFile == "" {
		return importer, nil
	}
	document, err := readBoundedFile(chain.GenesisFile)
	if err != nil {
		return nil, err
	}
	spec, block, err := parseDocument(document, chain.ID)
	if err != nil {
		return nil, err
	}
	importer.digest = sha256.Sum256(document)
	importer.spec = spec
	importer.block = block
	if importer.expectedHash != (enrich.Word{}) &&
		!bytes.Equal(importer.expectedHash[:], block.Hash().Bytes()) {
		return nil, errors.New("genesis document block hash does not match configured genesis hash")
	}
	return importer, nil
}

func (*Importer) Name() string { return "genesis-state-importer" }

func (importer *Importer) Run(ctx context.Context) error {
	if importer == nil || importer.db == nil {
		return errors.New("run nil genesis importer")
	}
	if importer.file == "" {
		if err := importer.markUnavailable(ctx); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}
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

func (importer *Importer) markUnavailable(ctx context.Context) error {
	_, err := importer.db.ExecContext(ctx, `
		INSERT INTO genesis_state_imports (chain_id, state, last_error_code)
		VALUES ($1::numeric, 'unavailable', 'genesis_file_not_configured')
		ON CONFLICT (chain_id) DO UPDATE SET
		    state = 'unavailable',
		    block_hash = NULL,
		    state_root = NULL,
		    document_sha256 = NULL,
		    account_count = NULL,
		    last_error_code = 'genesis_file_not_configured',
		    imported_at = NULL,
		    updated_at = clock_timestamp()
		WHERE genesis_state_imports.state <> 'complete'`, importer.chainID)
	if err != nil {
		return fmt.Errorf("publish unavailable genesis state: %w", err)
	}
	return nil
}

func (importer *Importer) importOnce(ctx context.Context) (enrich.Word, enrich.Word, error) {
	tx, err := importer.db.BeginTx(ctx, nil)
	if err != nil {
		return enrich.Word{}, enrich.Word{}, fmt.Errorf("begin genesis state import: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtext('etherview:genesis-state'), hashtext($1))`,
		importer.chainID,
	); err != nil {
		return enrich.Word{}, enrich.Word{}, fmt.Errorf("lock genesis state import: %w", err)
	}
	var blockHash, raw []byte
	err = tx.QueryRowContext(ctx, `
		SELECT block.hash, block.raw
		FROM canonical_blocks AS canonical
		JOIN blocks AS block
		  ON block.chain_id = canonical.chain_id
		 AND block.number = canonical.number
		 AND block.hash = canonical.block_hash
		WHERE canonical.chain_id = $1::numeric AND canonical.number = 0`,
		importer.chainID,
	).Scan(&blockHash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return enrich.Word{}, enrich.Word{}, errBlockZeroPending
	}
	if err != nil {
		return enrich.Word{}, enrich.Word{}, fmt.Errorf("read canonical block zero: %w", err)
	}
	reference, err := enrich.WordFromBytes(blockHash)
	if err != nil {
		return enrich.Word{}, enrich.Word{}, errors.New("stored block-zero hash is invalid")
	}
	stateRoot, err := stateRootFromBlock(raw)
	if err != nil {
		return enrich.Word{}, enrich.Word{}, err
	}
	if !bytes.Equal(blockHash, importer.block.Hash().Bytes()) {
		return enrich.Word{}, enrich.Word{}, errors.New("genesis document block hash does not match canonical block zero")
	}
	if !bytes.Equal(stateRoot[:], importer.block.Root().Bytes()) {
		return enrich.Word{}, enrich.Word{}, errors.New("genesis document state root does not match canonical block zero")
	}
	var existingState string
	var existingHash, existingRoot []byte
	err = tx.QueryRowContext(ctx, `
		SELECT state, block_hash, state_root
		FROM genesis_state_imports
		WHERE chain_id = $1::numeric
		FOR UPDATE`, importer.chainID,
	).Scan(&existingState, &existingHash, &existingRoot)
	if err == nil && existingState == "complete" {
		if !bytes.Equal(existingHash, blockHash) || !bytes.Equal(existingRoot, stateRoot[:]) {
			return enrich.Word{}, enrich.Word{}, errors.New("stored genesis import identity conflicts with canonical block zero")
		}
		if err := tx.Commit(); err != nil {
			return enrich.Word{}, enrich.Word{}, fmt.Errorf("commit existing genesis import: %w", err)
		}
		return reference, stateRoot, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return enrich.Word{}, enrich.Word{}, fmt.Errorf("read genesis import state: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO genesis_state_imports (
		    chain_id, block_hash, state_root, document_sha256, state,
		    account_count, last_error_code, imported_at, updated_at
		) VALUES (
		    $1::numeric, $2, $3, $4, 'complete', $5::numeric,
		    NULL, clock_timestamp(), clock_timestamp()
		)
		ON CONFLICT (chain_id) DO UPDATE SET
		    block_hash = EXCLUDED.block_hash,
		    state_root = EXCLUDED.state_root,
		    document_sha256 = EXCLUDED.document_sha256,
		    state = EXCLUDED.state,
		    account_count = EXCLUDED.account_count,
		    last_error_code = NULL,
		    imported_at = EXCLUDED.imported_at,
		    updated_at = EXCLUDED.updated_at
		WHERE genesis_state_imports.state <> 'complete'`,
		importer.chainID, blockHash, stateRoot[:], importer.digest[:],
		strconv.Itoa(len(importer.spec.Alloc)),
	); err != nil {
		return enrich.Word{}, enrich.Word{}, fmt.Errorf("persist genesis import identity: %w", err)
	}
	addresses := make([]address, 0, len(importer.spec.Alloc))
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
			return enrich.Word{}, enrich.Word{}, errors.New("compute genesis account storage root")
		}
		code := account.Code
		if code == nil {
			code = []byte{}
		}
		codeHash := keccakHash(code)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO genesis_account_observations (
			    chain_id, address, block_hash, balance, nonce,
			    code_hash, code, storage_root
			) VALUES (
			    $1::numeric, $2, $3, $4::numeric, $5::numeric, $6, $7, $8
			)`,
			importer.chainID, accountAddress[:], blockHash, account.Balance.String(),
			strconv.FormatUint(account.Nonce, 10), codeHash[:], code, storageHash[:],
		); err != nil {
			return enrich.Word{}, enrich.Word{}, fmt.Errorf("persist genesis account: %w", err)
		}
		if len(code) > 0 {
			result, err := tx.ExecContext(ctx, `
				INSERT INTO contract_code_observations AS current (
				    chain_id, address, block_number, block_hash,
				    code_hash, code, canonical
				) VALUES ($1::numeric, $2, 0, $3, $4, $5, TRUE)
				ON CONFLICT (chain_id, address, block_hash) DO UPDATE SET
				    code = COALESCE(current.code, EXCLUDED.code),
				    canonical = TRUE
				WHERE current.code_hash = EXCLUDED.code_hash
				  AND (current.code IS NULL OR current.code = EXCLUDED.code)`,
				importer.chainID, accountAddress[:], blockHash, codeHash[:], code,
			)
			if err != nil {
				return enrich.Word{}, enrich.Word{}, fmt.Errorf("persist genesis code observation: %w", err)
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return enrich.Word{}, enrich.Word{}, fmt.Errorf("count persisted genesis code observation: %w", err)
			}
			if affected != 1 {
				return enrich.Word{}, enrich.Word{}, errors.New("genesis code observation conflicts with an existing exact fact")
			}
		}
	}
	if err := importer.requestProxyReplay(ctx, tx, reference, stateRoot); err != nil {
		return enrich.Word{}, enrich.Word{}, err
	}
	if err := tx.Commit(); err != nil {
		return enrich.Word{}, enrich.Word{}, fmt.Errorf("commit genesis state import: %w", err)
	}
	return reference, stateRoot, nil
}

func (importer *Importer) requestProxyReplay(
	ctx context.Context,
	tx *sql.Tx,
	blockHash enrich.Word,
	stateRoot enrich.Word,
) error {
	if importer.queue == nil {
		return nil
	}
	_, err := importer.queue.EnqueueTx(ctx, tx, enrich.EnqueueRequest{
		Stage: enrich.ProxyStage, ChainID: importer.chainID,
		BlockHash: blockHash, BlockNumber: 0,
		Replay: enrich.ReplaySource{
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
