package store

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
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
	GenesisHash common.Hash
}

func ReadSchemaStatus(ctx context.Context, db dbaccess.Database) (SchemaStatus, error) {
	if db == nil {
		return SchemaStatus{}, errors.New("read schema status: nil database")
	}
	expected, err := Migrations()
	if err != nil {
		return SchemaStatus{}, err
	}
	// Resolve through the session search_path, including isolated test schemas.
	ledgerExists, err := dbgen.New(db).StoreLegacyReadSchemaStatusStatement1(ctx)
	if err != nil {
		return SchemaStatus{}, fmt.Errorf("locate migration ledger: %w", err)
	}
	if !ledgerExists {
		status := SchemaStatus{Pending: make([]string, 0, len(expected))}
		for _, migration := range expected {
			status.Pending = append(status.Pending, migration.Version)
		}
		return status, nil
	}
	rows, err := dbgen.New(db).StoreLegacyReadSchemaStatusStatement2(ctx)
	if err != nil {
		return SchemaStatus{}, fmt.Errorf("read migration ledger: %w", err)
	}
	applied := make(map[string]string, len(expected))
	status := SchemaStatus{}
	for _, row := range rows {
		applied[row.Version] = row.Checksum
		status.Applied = append(status.Applied, row.Version)
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

func CheckSchema(ctx context.Context, db dbaccess.Database) error {
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
func BindChainIdentity(ctx context.Context, db dbaccess.Database, chainID string, genesis common.Hash) error {
	if db == nil {
		return errors.New("bind chain identity: nil database")
	}
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return err
	}
	genesisBytes := genesis.Bytes()
	// The advisory lock is the serialization boundary for every chain-scoped
	// writer. Keep READ COMMITTED here so a transaction that waited for another
	// process to bind the same chain gets a fresh snapshot for the INSERT below.
	// REPEATABLE READ/SERIALIZABLE would retain the snapshot established by the
	// blocking lock statement and can spuriously abort concurrent role startup
	// with SQLSTATE 40001 after the first process commits.
	tx, err := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: chainWriteIsolation})
	if err != nil {
		return fmt.Errorf("begin chain identity transaction: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)
	if err := lockChain(ctx, tx, chainID); err != nil {
		return err
	}
	var existing []byte
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).StoreLegacyBindChainIdentityStatement1(ctx, queryValue0)
		if err != nil {
			return err
		}
		existing = queryRow
		return nil
	}()
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		err = func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(chainID); err != nil {
				return err
			}
			queryRow, err := dbgen.New(tx).StoreLegacyBindChainIdentityStatement2(ctx, queryValue0, genesisBytes)
			if err != nil {
				return err
			}
			existing = queryRow
			return nil
		}()
	case err == nil && len(existing) == 0:
		err = func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(chainID); err != nil {
				return err
			}
			queryRow, err := dbgen.New(tx).StoreLegacyBindChainIdentityStatement3(ctx, genesisBytes, queryValue0)
			if err != nil {
				return err
			}
			existing = queryRow
			return nil
		}()
	}
	if err != nil {
		return fmt.Errorf("persist chain identity: %w", err)
	}
	if !strings.EqualFold(hex.EncodeToString(existing), hex.EncodeToString(genesisBytes)) {
		return fmt.Errorf("chain identity mismatch: configured genesis %s, database genesis 0x%s", genesis, hex.EncodeToString(existing))
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit chain identity: %w", err)
	}
	return nil
}

func ReadChainIdentity(ctx context.Context, db dbaccess.Database, chainID string) (ChainIdentity, error) {
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
