package ens

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	dbaccess "github.com/islishude/etherview/internal/db"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	ErrIdentityConflict = errors.New("ENS observation identity conflict")
	ErrSnapshotInvalid  = errors.New("ENS address-name snapshot is invalid or expired")
)

type Generation struct {
	ID               int64
	PolicyKey        string
	CoinType         *big.Int
	OfficialEndpoint string
	OfficialBlock    BlockRef
	CustomEndpoint   string
	CustomCoinType   *big.Int
	CustomBlock      *BlockRef
	CreatedAt        time.Time
	FreshUntil       time.Time
	RetainUntil      time.Time
}

type GenerationCandidate struct {
	PolicyKey        string
	CoinType         *big.Int
	OfficialEndpoint string
	OfficialBlock    BlockRef
	CustomEndpoint   string
	CustomCoinType   *big.Int
	CustomBlock      *BlockRef
	CreatedAt        time.Time
	FreshUntil       time.Time
	RetainUntil      time.Time
}

type Observation struct {
	ID              int64
	GenerationID    int64
	Source          Source
	Direction       string
	LookupKey       string
	Outcome         Outcome
	Name            string
	Address         common.Address
	Resolver        common.Address
	ReverseResolver common.Address
	ObservedAt      time.Time
}

type Repository struct {
	db      *sql.DB
	chainID uint64
	chain   pgtype.Numeric
}

func NewRepository(db *sql.DB, chainID uint64) (*Repository, error) {
	if db == nil {
		return nil, errors.New("ENS repository database is nil")
	}
	if chainID == 0 {
		return nil, errors.New("ENS repository chain ID must be positive")
	}
	return &Repository{
		db: db, chainID: chainID,
		chain: pgtype.Numeric{Int: new(big.Int).SetUint64(chainID), Valid: true},
	}, nil
}

func (repository *Repository) FreshGeneration(
	ctx context.Context,
	policyKey string,
	now time.Time,
) (Generation, bool, error) {
	var row dbgen.EnsResolutionGeneration
	err := dbaccess.WithQueries(ctx, repository.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.GetFreshENSResolutionGeneration(
			ctx, repository.chain, policyKey, timestamptz(now),
		)
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Generation{}, false, nil
	}
	if err != nil {
		return Generation{}, false, fmt.Errorf("read fresh ENS generation: %w", err)
	}
	generation, err := decodeGeneration(row)
	return generation, err == nil, err
}

// CreateGeneration serializes only the final PostgreSQL selection and insert.
// All source RPC calls used to build candidate must already be complete.
func (repository *Repository) CreateGeneration(
	ctx context.Context,
	candidate GenerationCandidate,
) (Generation, error) {
	var row dbgen.EnsResolutionGeneration
	err := dbaccess.WithTransaction(ctx, repository.db, func(queries *dbgen.Queries) error {
		if err := queries.AcquireENSGenerationLock(ctx, repository.chain); err != nil {
			return err
		}
		fresh, err := queries.GetFreshENSResolutionGeneration(
			ctx, repository.chain, candidate.PolicyKey, timestamptz(candidate.CreatedAt),
		)
		if err == nil {
			row = fresh
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		params, err := repository.generationParams(candidate)
		if err != nil {
			return err
		}
		row, err = queries.InsertENSResolutionGeneration(ctx, params)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Generation{}, errors.New("custom ENS generation block is no longer canonical")
	}
	if err != nil {
		return Generation{}, fmt.Errorf("create ENS generation: %w", err)
	}
	return decodeGeneration(row)
}

func (repository *Repository) Generation(
	ctx context.Context,
	id int64,
	policyKey string,
	now time.Time,
) (Generation, error) {
	var row dbgen.EnsResolutionGeneration
	err := dbaccess.WithQueries(ctx, repository.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.GetENSResolutionGeneration(ctx, dbgen.GetENSResolutionGenerationParams{
			GenerationID: id, ChainID: repository.chain, PolicyKey: policyKey, NowAt: timestamptz(now),
		})
		return queryErr
	})
	if err != nil {
		return Generation{}, fmt.Errorf("read ENS generation: %w", err)
	}
	return decodeGeneration(row)
}

func (repository *Repository) generationParams(
	candidate GenerationCandidate,
) (dbgen.InsertENSResolutionGenerationParams, error) {
	if candidate.PolicyKey == "" || candidate.CoinType == nil || candidate.CoinType.Sign() <= 0 ||
		candidate.CoinType.BitLen() > 256 || candidate.OfficialEndpoint == "" ||
		candidate.OfficialBlock.Hash == (common.Hash{}) || candidate.CreatedAt.IsZero() ||
		!candidate.FreshUntil.After(candidate.CreatedAt) || candidate.RetainUntil.Before(candidate.FreshUntil) {
		return dbgen.InsertENSResolutionGenerationParams{}, errors.New("ENS generation candidate is invalid")
	}
	params := dbgen.InsertENSResolutionGenerationParams{
		ChainID: repository.chain, PolicyKey: candidate.PolicyKey,
		CoinType:         pgtype.Numeric{Int: new(big.Int).Set(candidate.CoinType), Valid: true},
		OfficialEndpoint: candidate.OfficialEndpoint,
		OfficialBlockNumber: pgtype.Numeric{
			Int: new(big.Int).SetUint64(candidate.OfficialBlock.Number), Valid: true,
		},
		OfficialBlockHash: candidate.OfficialBlock.Hash.Bytes(),
		CreatedAt:         timestamptz(candidate.CreatedAt), FreshUntil: timestamptz(candidate.FreshUntil),
		RetainUntil: timestamptz(candidate.RetainUntil),
	}
	if candidate.CustomBlock != nil {
		if candidate.CustomEndpoint == "" || candidate.CustomCoinType == nil || candidate.CustomCoinType.Sign() <= 0 ||
			candidate.CustomCoinType.BitLen() > 256 || candidate.CustomBlock.Hash == (common.Hash{}) {
			return dbgen.InsertENSResolutionGenerationParams{}, errors.New("custom ENS generation candidate is invalid")
		}
		params.CustomEndpoint = &candidate.CustomEndpoint
		params.CustomCoinType = pgtype.Numeric{Int: new(big.Int).Set(candidate.CustomCoinType), Valid: true}
		params.CustomBlockNumber = pgtype.Numeric{
			Int: new(big.Int).SetUint64(candidate.CustomBlock.Number), Valid: true,
		}
		params.CustomBlockHash = candidate.CustomBlock.Hash.Bytes()
	} else if candidate.CustomEndpoint != "" {
		return dbgen.InsertENSResolutionGenerationParams{}, errors.New("custom ENS endpoint has no block")
	}
	return params, nil
}

func (repository *Repository) Observation(
	ctx context.Context,
	generationID int64,
	source Source,
	direction, lookupKey string,
) (Observation, bool, error) {
	var row dbgen.EnsNameObservation
	err := dbaccess.WithQueries(ctx, repository.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.GetENSNameObservation(ctx, dbgen.GetENSNameObservationParams{
			GenerationID: generationID, ChainID: repository.chain, Source: string(source),
			Direction: direction, LookupKey: lookupKey,
		})
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Observation{}, false, nil
	}
	if err != nil {
		return Observation{}, false, fmt.Errorf("read ENS observation: %w", err)
	}
	observation, err := decodeObservation(row)
	return observation, err == nil, err
}

func (repository *Repository) RecordObservation(
	ctx context.Context,
	observation Observation,
) (Observation, error) {
	params, err := repository.observationParams(observation)
	if err != nil {
		return Observation{}, err
	}
	var row dbgen.EnsNameObservation
	err = dbaccess.WithQueries(ctx, repository.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.InsertENSNameObservation(ctx, params)
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Observation{}, ErrIdentityConflict
	}
	if err != nil {
		return Observation{}, fmt.Errorf("record ENS observation: %w", err)
	}
	return decodeObservation(row)
}

func (repository *Repository) EnsureObservationPublished(ctx context.Context, observationID int64) error {
	if observationID <= 0 {
		return errors.New("ENS observation ID is invalid")
	}
	return dbaccess.WithQueries(ctx, repository.db, func(queries *dbgen.Queries) error {
		return queries.EnsureENSNameObservationPublished(ctx, observationID, repository.chain)
	})
}

func (repository *Repository) observationParams(
	observation Observation,
) (dbgen.InsertENSNameObservationParams, error) {
	if observation.GenerationID <= 0 || observation.Source != SourceOfficial && observation.Source != SourceCustom ||
		(observation.Direction != "forward" && observation.Direction != "primary") || observation.LookupKey == "" ||
		(observation.Outcome != OutcomeResolved && observation.Outcome != OutcomeNoRecord) || observation.ObservedAt.IsZero() {
		return dbgen.InsertENSNameObservationParams{}, errors.New("ENS observation is invalid")
	}
	params := dbgen.InsertENSNameObservationParams{
		GenerationID: observation.GenerationID, ChainID: repository.chain,
		Source: string(observation.Source), Direction: observation.Direction,
		LookupKey: observation.LookupKey, Outcome: string(observation.Outcome),
		ObservedAt: timestamptz(observation.ObservedAt),
	}
	if observation.Name != "" {
		params.Name = &observation.Name
	}
	if observation.Address != (common.Address{}) {
		params.Address = observation.Address.Bytes()
	}
	if observation.Resolver != (common.Address{}) {
		params.Resolver = observation.Resolver.Bytes()
	}
	if observation.ReverseResolver != (common.Address{}) {
		params.ReverseResolver = observation.ReverseResolver.Bytes()
	}
	return params, nil
}

func (repository *Repository) FreshFailure(
	ctx context.Context,
	generationID int64,
	source Source,
	direction, lookupKey string,
	now time.Time,
) (string, bool, error) {
	var row dbgen.EnsResolutionFailure
	err := dbaccess.WithQueries(ctx, repository.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.GetFreshENSResolutionFailure(ctx, dbgen.GetFreshENSResolutionFailureParams{
			GenerationID: generationID, ChainID: repository.chain, Source: string(source),
			Direction: direction, LookupKey: lookupKey, NowAt: timestamptz(now),
		})
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read ENS failure: %w", err)
	}
	if row.Code == "" || !row.ExpiresAt.Valid || !row.ExpiresAt.Time.After(now) {
		return "", false, errors.New("stored ENS failure is invalid")
	}
	return row.Code, true, nil
}

func (repository *Repository) RecordFailure(
	ctx context.Context,
	generationID int64,
	source Source,
	direction, lookupKey, code string,
	observedAt, expiresAt time.Time,
) error {
	if generationID <= 0 || lookupKey == "" || code == "" || !expiresAt.After(observedAt) {
		return errors.New("ENS failure is invalid")
	}
	return dbaccess.WithQueries(ctx, repository.db, func(queries *dbgen.Queries) error {
		return queries.InsertENSResolutionFailure(ctx, dbgen.InsertENSResolutionFailureParams{
			GenerationID: generationID, ChainID: repository.chain, Source: string(source),
			Direction: direction, LookupKey: lookupKey, Code: code,
			ObservedAt: timestamptz(observedAt), ExpiresAt: timestamptz(expiresAt),
		})
	})
}

func (repository *Repository) CreateSnapshot(
	ctx context.Context,
	generationID int64,
	createdAt, expiresAt time.Time,
) (string, error) {
	id := uuid.New()
	var row dbgen.EnsAddressNameSnapshot
	err := dbaccess.WithQueries(ctx, repository.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.InsertENSAddressNameSnapshot(ctx, dbgen.InsertENSAddressNameSnapshotParams{
			ID: pgtype.UUID{Bytes: [16]byte(id), Valid: true}, ChainID: repository.chain,
			GenerationID: generationID, CreatedAt: timestamptz(createdAt), ExpiresAt: timestamptz(expiresAt),
		})
		return queryErr
	})
	if err != nil {
		return "", fmt.Errorf("create ENS address-name snapshot: %w", err)
	}
	if !row.ID.Valid || row.ID.Bytes != [16]byte(id) {
		return "", errors.New("stored ENS snapshot identity is invalid")
	}
	return id.String(), nil
}

func (repository *Repository) SnapshotGeneration(
	ctx context.Context,
	snapshotID, policyKey string,
	now time.Time,
) (Generation, error) {
	id, err := uuid.Parse(snapshotID)
	if err != nil {
		return Generation{}, ErrSnapshotInvalid
	}
	var snapshot dbgen.EnsAddressNameSnapshot
	err = dbaccess.WithQueries(ctx, repository.db, func(queries *dbgen.Queries) error {
		var queryErr error
		snapshot, queryErr = queries.GetENSAddressNameSnapshot(ctx, dbgen.GetENSAddressNameSnapshotParams{
			ID: pgtype.UUID{Bytes: [16]byte(id), Valid: true}, ChainID: repository.chain,
			NowAt: timestamptz(now), PolicyKey: policyKey,
		})
		return queryErr
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Generation{}, ErrSnapshotInvalid
		}
		return Generation{}, fmt.Errorf("read ENS address-name snapshot: %w", err)
	}
	generation, err := repository.Generation(ctx, snapshot.GenerationID, policyKey, now)
	if errors.Is(err, pgx.ErrNoRows) {
		return Generation{}, ErrSnapshotInvalid
	}
	return generation, err
}

func decodeGeneration(row dbgen.EnsResolutionGeneration) (Generation, error) {
	coinType, err := integerNumeric(row.CoinType)
	if err != nil || coinType.Sign() <= 0 || coinType.BitLen() > 256 {
		return Generation{}, errors.New("stored ENS generation coin type is invalid")
	}
	officialNumber, err := numericUint64(row.OfficialBlockNumber)
	if err != nil || len(row.OfficialBlockHash) != common.HashLength {
		return Generation{}, errors.New("stored ENS official block is invalid")
	}
	if row.ID <= 0 || row.PolicyKey == "" || row.OfficialEndpoint == "" || !row.CreatedAt.Valid ||
		!row.FreshUntil.Valid || !row.RetainUntil.Valid || !row.FreshUntil.Time.After(row.CreatedAt.Time) ||
		row.RetainUntil.Time.Before(row.FreshUntil.Time) {
		return Generation{}, errors.New("stored ENS generation is invalid")
	}
	result := Generation{
		ID: row.ID, PolicyKey: row.PolicyKey, CoinType: coinType,
		OfficialEndpoint: row.OfficialEndpoint,
		OfficialBlock:    BlockRef{Number: officialNumber, Hash: common.BytesToHash(row.OfficialBlockHash)},
		CreatedAt:        row.CreatedAt.Time.UTC(), FreshUntil: row.FreshUntil.Time.UTC(), RetainUntil: row.RetainUntil.Time.UTC(),
	}
	customPresent := row.CustomEndpoint != nil || row.CustomCoinType.Valid || row.CustomBlockNumber.Valid || len(row.CustomBlockHash) > 0
	if customPresent {
		if row.CustomEndpoint == nil || *row.CustomEndpoint == "" || !row.CustomCoinType.Valid ||
			!row.CustomBlockNumber.Valid || len(row.CustomBlockHash) != common.HashLength {
			return Generation{}, errors.New("stored custom ENS generation is invalid")
		}
		coinType, err := integerNumeric(row.CustomCoinType)
		if err != nil || coinType.Sign() <= 0 || coinType.BitLen() > 256 {
			return Generation{}, errors.New("stored custom ENS coin type is invalid")
		}
		number, err := numericUint64(row.CustomBlockNumber)
		if err != nil {
			return Generation{}, errors.New("stored custom ENS block number is invalid")
		}
		result.CustomEndpoint = *row.CustomEndpoint
		result.CustomCoinType = coinType
		result.CustomBlock = &BlockRef{Number: number, Hash: common.BytesToHash(row.CustomBlockHash)}
	}
	return result, nil
}

func decodeObservation(row dbgen.EnsNameObservation) (Observation, error) {
	if row.ID <= 0 || row.GenerationID <= 0 || !row.ObservedAt.Valid {
		return Observation{}, errors.New("stored ENS observation is invalid")
	}
	result := Observation{
		ID: row.ID, GenerationID: row.GenerationID, Source: Source(row.Source),
		Direction: row.Direction, LookupKey: row.LookupKey, Outcome: Outcome(row.Outcome),
		ObservedAt: row.ObservedAt.Time.UTC(),
	}
	if row.Name != nil {
		result.Name = *row.Name
	}
	for field, value := range map[string][]byte{
		"address": row.Address, "resolver": row.Resolver, "reverse resolver": row.ReverseResolver,
	} {
		if len(value) != 0 && len(value) != common.AddressLength {
			return Observation{}, fmt.Errorf("stored ENS observation %s is invalid", field)
		}
	}
	if len(row.Address) > 0 {
		result.Address = common.BytesToAddress(row.Address)
	}
	if len(row.Resolver) > 0 {
		result.Resolver = common.BytesToAddress(row.Resolver)
	}
	if len(row.ReverseResolver) > 0 {
		result.ReverseResolver = common.BytesToAddress(row.ReverseResolver)
	}
	return result, nil
}

func numericUint64(value pgtype.Numeric) (uint64, error) {
	integer, err := integerNumeric(value)
	if err != nil || !integer.IsUint64() {
		return 0, errors.New("numeric value is not uint64")
	}
	return integer.Uint64(), nil
}

func integerNumeric(value pgtype.Numeric) (*big.Int, error) {
	if !value.Valid || value.NaN || value.InfinityModifier != pgtype.Finite || value.Int == nil {
		return nil, errors.New("numeric value is not finite")
	}
	result := new(big.Int).Set(value.Int)
	switch {
	case value.Exp > 0:
		result.Mul(result, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(value.Exp)), nil))
	case value.Exp < 0:
		divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-value.Exp)), nil)
		quotient, remainder := new(big.Int), new(big.Int)
		quotient.QuoRem(result, divisor, remainder)
		if remainder.Sign() != 0 {
			return nil, errors.New("numeric value is fractional")
		}
		result = quotient
	}
	if result.Sign() < 0 {
		return nil, errors.New("numeric value is negative")
	}
	return result, nil
}

func timestamptz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}
