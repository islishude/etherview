package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	dbaccess "github.com/islishude/etherview/internal/db"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrSchemaIncompatible = errors.New("database schema is incompatible")

// SchemaStatus reports whether every embedded migration is present with the
// exact checksum. Serving must not silently migrate a production database.
type SchemaStatus struct {
	Applied []string
	Pending []string
}

type ChainIdentity struct {
	ChainID     string
	GenesisHash ethrpc.Hash
}

func ReadSchemaStatus(ctx context.Context, db *sql.DB) (SchemaStatus, error) {
	if db == nil {
		return SchemaStatus{}, errors.New("read schema status: nil database")
	}
	expected, err := Migrations()
	if err != nil {
		return SchemaStatus{}, err
	}
	var ledger sql.NullString
	// Resolve through the connection search_path. Production uses public by
	// default, while tests and managed deployments can isolate Etherview in a
	// dedicated schema without making status checks look in the wrong ledger.
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('etherview_schema_migrations')::text`).Scan(&ledger); err != nil {
		return SchemaStatus{}, fmt.Errorf("locate migration ledger: %w", err)
	}
	if !ledger.Valid {
		status := SchemaStatus{Pending: make([]string, 0, len(expected))}
		for _, migration := range expected {
			status.Pending = append(status.Pending, migration.Version)
		}
		return status, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT version, checksum
		FROM etherview_schema_migrations
		ORDER BY version`)
	if err != nil {
		return SchemaStatus{}, fmt.Errorf("read migration ledger: %w", err)
	}
	defer rows.Close()
	applied := make(map[string]string, len(expected))
	status := SchemaStatus{}
	for rows.Next() {
		var version, checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return SchemaStatus{}, fmt.Errorf("scan migration ledger: %w", err)
		}
		applied[version] = checksum
		status.Applied = append(status.Applied, version)
	}
	if err := rows.Err(); err != nil {
		return SchemaStatus{}, fmt.Errorf("iterate migration ledger: %w", err)
	}
	for _, migration := range expected {
		checksum, ok := applied[migration.Version]
		if !ok {
			status.Pending = append(status.Pending, migration.Version)
			continue
		}
		if checksum != migration.Checksum {
			return SchemaStatus{}, fmt.Errorf("%w: migration %s checksum differs", ErrSchemaIncompatible, migration.Version)
		}
		delete(applied, migration.Version)
	}
	if len(applied) != 0 {
		unknown := make([]string, 0, len(applied))
		for version := range applied {
			unknown = append(unknown, version)
		}
		return SchemaStatus{}, fmt.Errorf("%w: database has unknown migrations %s", ErrSchemaIncompatible, strings.Join(unknown, ", "))
	}
	return status, nil
}

func CheckSchema(ctx context.Context, db *sql.DB) error {
	status, err := ReadSchemaStatus(ctx, db)
	if err != nil {
		return err
	}
	if len(status.Pending) != 0 {
		return fmt.Errorf("%w: pending migrations %s; run `etherview migrate up`", ErrSchemaIncompatible, strings.Join(status.Pending, ", "))
	}
	return nil
}

// BindChainIdentity persists the chain/genesis pair and rejects reuse of a
// database with another genesis, including when the numeric chain ID matches.
func BindChainIdentity(ctx context.Context, db *sql.DB, chainID string, genesis ethrpc.Hash) error {
	if db == nil {
		return errors.New("bind chain identity: nil database")
	}
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return err
	}
	genesisBytes, err := genesis.Bytes()
	if err != nil {
		return fmt.Errorf("bind chain identity: %w", err)
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin chain identity transaction: %w", err)
	}
	defer tx.Rollback()
	if err := lockChain(ctx, tx, chainID); err != nil {
		return err
	}
	var existing []byte
	err = tx.QueryRowContext(ctx, `
		INSERT INTO chains (chain_id, genesis_hash)
		VALUES ($1::numeric, $2)
		ON CONFLICT (chain_id) DO UPDATE
		SET genesis_hash = COALESCE(chains.genesis_hash, EXCLUDED.genesis_hash)
		RETURNING genesis_hash`, chainID, genesisBytes).Scan(&existing)
	if err != nil {
		return fmt.Errorf("persist chain identity: %w", err)
	}
	if !strings.EqualFold(hex.EncodeToString(existing), hex.EncodeToString(genesisBytes)) {
		return fmt.Errorf("chain identity mismatch: configured genesis %s, database genesis 0x%s", genesis, hex.EncodeToString(existing))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit chain identity: %w", err)
	}
	return nil
}

func ReadChainIdentity(ctx context.Context, db *sql.DB, chainID string) (ChainIdentity, error) {
	if db == nil {
		return ChainIdentity{}, errors.New("read chain identity: nil database")
	}
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return ChainIdentity{}, err
	}
	integer, ok := new(big.Int).SetString(chainID, 10)
	if !ok {
		return ChainIdentity{}, errors.New("read chain identity: normalized chain ID is not numeric")
	}
	var row dbgen.GetChainIdentityRow
	err = dbaccess.WithQueries(ctx, db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.GetChainIdentity(ctx, pgtype.Numeric{Int: integer, Valid: true})
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ChainIdentity{}, errors.New("configured chain is not initialized; start a sync role after running migrations")
	}
	if err != nil {
		return ChainIdentity{}, fmt.Errorf("query chain identity: %w", err)
	}
	hash, err := hashFromBytes(row.GenesisHash)
	if err != nil {
		return ChainIdentity{}, fmt.Errorf("decode stored genesis hash: %w", err)
	}
	return ChainIdentity{ChainID: row.ChainID, GenesisHash: hash}, nil
}
