package query

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/islishude/etherview/internal/api/gen"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/httpapi"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type genesisCursor struct {
	ChainID   string `json:"chain_id"`
	BlockHash string `json:"block_hash"`
	After     string `json:"after"`
}

var errGenesisImportNotCanonical = errors.New("completed genesis import is not canonical")

func (r *PostgresReader) GenesisAccounts(
	ctx context.Context,
	encodedCursor string,
	limit int,
) ([]gen.GenesisAccount, string, error) {
	if limit <= 0 || limit > 100 {
		return nil, "", fmt.Errorf("genesis account limit %d is outside 1..100", limit)
	}
	chainInteger, ok := new(big.Int).SetString(r.chainID, 10)
	if !ok || chainInteger.Sign() <= 0 {
		return nil, "", errors.New("query reader chain ID is invalid")
	}
	var (
		imported dbgen.GetGenesisImportRow
		rows     []dbgen.ListGenesisAccountsRow
	)
	err := dbaccess.WithQueries(ctx, r.db, func(queries *dbgen.Queries) error {
		var err error
		imported, err = queries.GetGenesisImport(ctx, pgtype.Numeric{Int: chainInteger, Valid: true})
		if err != nil {
			return err
		}
		if imported.State != "complete" {
			return nil
		}
		if !imported.Canonical || len(imported.BlockHash) != 32 || len(imported.StateRoot) != 32 {
			return errGenesisImportNotCanonical
		}
		_, after, err := decodeGenesisCursor(encodedCursor, r.chainID, imported.BlockHash)
		if err != nil {
			return err
		}
		rows, err = queries.ListGenesisAccounts(ctx, dbgen.ListGenesisAccountsParams{
			ChainID:   pgtype.Numeric{Int: chainInteger, Valid: true},
			BlockHash: imported.BlockHash, AfterAddress: after,
			PageLimit: int32(limit + 1),
		})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", httpapi.NewCapabilityUnavailableError(
			"genesis_state", "unavailable", "genesis_state_not_imported",
		)
	}
	if errors.Is(err, errGenesisImportNotCanonical) {
		return nil, "", httpapi.NewCapabilityUnavailableError(
			"genesis_state", "failed", "genesis_state_not_canonical",
		)
	}
	if err != nil {
		if errors.Is(err, httpapi.ErrInvalidCursor) {
			return nil, "", err
		}
		return nil, "", fmt.Errorf("query genesis accounts: %w", err)
	}
	switch imported.State {
	case "complete":
	case "pending":
		return nil, "", httpapi.ErrNotReady
	case "failed":
		code := controlledGenesisCode(imported.LastErrorCode, "genesis_import_failed")
		return nil, "", httpapi.NewCapabilityUnavailableError("genesis_state", "failed", code)
	default:
		code := controlledGenesisCode(imported.LastErrorCode, "genesis_file_not_configured")
		return nil, "", httpapi.NewCapabilityUnavailableError("genesis_state", "unavailable", code)
	}
	models := make([]gen.GenesisAccount, 0, min(limit, len(rows)))
	for index, row := range rows {
		if index == limit {
			break
		}
		model, err := genesisAccountModel(row)
		if err != nil {
			return nil, "", err
		}
		models = append(models, model)
	}
	var next string
	if len(rows) > limit && len(models) > 0 {
		next, err = httpapi.EncodeCursor(genesisCursor{
			ChainID: r.chainID, BlockHash: "0x" + hex.EncodeToString(imported.BlockHash),
			After: strings.ToLower(models[len(models)-1].Address),
		})
		if err != nil {
			return nil, "", fmt.Errorf("encode genesis account cursor: %w", err)
		}
	}
	return models, next, nil
}

func decodeGenesisCursor(
	encoded string,
	chainID string,
	blockHash []byte,
) (genesisCursor, []byte, error) {
	if encoded == "" {
		return genesisCursor{}, []byte{}, nil
	}
	var cursor genesisCursor
	if err := httpapi.DecodeCursor(encoded, &cursor); err != nil {
		return genesisCursor{}, nil, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	expectedHash := "0x" + hex.EncodeToString(blockHash)
	if cursor.ChainID != chainID || cursor.BlockHash != expectedHash {
		return genesisCursor{}, nil, ErrInvalidCursor
	}
	if len(cursor.After) != 42 || !strings.HasPrefix(cursor.After, "0x") {
		return genesisCursor{}, nil, ErrInvalidCursor
	}
	after, err := hex.DecodeString(cursor.After[2:])
	if err != nil || len(after) != 20 {
		return genesisCursor{}, nil, ErrInvalidCursor
	}
	return cursor, after, nil
}

func genesisAccountModel(row dbgen.ListGenesisAccountsRow) (gen.GenesisAccount, error) {
	if len(row.Address) != 20 || len(row.CodeHash) != 32 ||
		len(row.StorageRoot) != 32 || len(row.BlockHash) != 32 {
		return gen.GenesisAccount{}, errors.New("stored genesis account identity is invalid")
	}
	if err := validateUint256Decimal(row.Balance); err != nil {
		return gen.GenesisAccount{}, errors.New("stored genesis account balance is invalid")
	}
	if err := validateUint256Decimal(row.Nonce); err != nil {
		return gen.GenesisAccount{}, errors.New("stored genesis account nonce is invalid")
	}
	address, err := ChecksumAddress("0x" + hex.EncodeToString(row.Address))
	if err != nil {
		return gen.GenesisAccount{}, errors.New("stored genesis account address is invalid")
	}
	kind := gen.GenesisAccountTypeEoa
	if row.Contract {
		kind = gen.GenesisAccountTypeContract
	}
	return gen.GenesisAccount{
		Address: address, Type: kind, Balance: row.Balance, Nonce: row.Nonce,
		CodeHash:    "0x" + hex.EncodeToString(row.CodeHash),
		StorageRoot: "0x" + hex.EncodeToString(row.StorageRoot),
		BlockHash:   "0x" + hex.EncodeToString(row.BlockHash),
	}, nil
}

func validateUint256Decimal(value string) error {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return errors.New("non-canonical decimal")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return errors.New("non-decimal quantity")
		}
	}
	integer, ok := new(big.Int).SetString(value, 10)
	if !ok || integer.Sign() < 0 || integer.BitLen() > 256 {
		return errors.New("quantity exceeds uint256")
	}
	return nil
}

func controlledGenesisCode(value *string, fallback string) string {
	if value == nil {
		return fallback
	}
	candidate := strings.TrimSpace(*value)
	for index, character := range candidate {
		if index == 0 && (character < 'a' || character > 'z') {
			return fallback
		}
		if character != '_' && (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') {
			return fallback
		}
	}
	if candidate == "" || len(candidate) > 128 {
		return fallback
	}
	return candidate
}
