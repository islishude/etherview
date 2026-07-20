package maintenance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"strings"
	"time"

	dbaccess "github.com/islishude/etherview/internal/db"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5/pgtype"
)

type CatalogCleanupResult struct {
	Ran           bool
	MinGeneration int64
	Deleted       int64
}

type CatalogCleaner interface {
	Sweep(context.Context, uint64, int64, int, time.Time) (CatalogCleanupResult, error)
}

type PostgresCatalogCleaner struct {
	db *sql.DB
}

func NewPostgresCatalogCleaner(db *sql.DB) (*PostgresCatalogCleaner, error) {
	if db == nil {
		return nil, errors.New("catalog maintenance database is nil")
	}
	return &PostgresCatalogCleaner{db: db}, nil
}

func (cleaner *PostgresCatalogCleaner) Sweep(
	ctx context.Context,
	chainID uint64,
	retentionGenerations int64,
	deleteBatch int,
	now time.Time,
) (CatalogCleanupResult, error) {
	if cleaner == nil || cleaner.db == nil {
		return CatalogCleanupResult{}, errors.New("catalog maintenance database is nil")
	}
	if chainID == 0 || retentionGenerations < 1000 || retentionGenerations > 10_000_000 ||
		deleteBatch <= 0 || deleteBatch > math.MaxInt32 || now.IsZero() {
		return CatalogCleanupResult{}, errors.New("catalog maintenance sweep options are invalid")
	}
	result := CatalogCleanupResult{}
	err := dbaccess.WithTransaction(ctx, cleaner.db, func(queries *dbgen.Queries) error {
		locked, err := queries.TrySearchCatalogMaintenanceLock(ctx, fmt.Sprintf("%d", chainID))
		if err != nil {
			return err
		}
		if !locked {
			return nil
		}
		minimum, err := queries.PruneSearchCatalog(ctx, uint64MaintenanceNumeric(chainID), retentionGenerations)
		if err != nil {
			return err
		}
		deleted, err := queries.DeleteExpiredAdapterObservations(
			ctx,
			uint64MaintenanceNumeric(chainID),
			pgtype.Timestamptz{Time: now.UTC(), Valid: true},
			int32(deleteBatch),
		)
		if err != nil {
			return err
		}
		result = CatalogCleanupResult{Ran: true, MinGeneration: minimum, Deleted: deleted}
		return nil
	})
	if err != nil {
		return CatalogCleanupResult{}, fmt.Errorf("run catalog maintenance transaction: %w", err)
	}
	return result, nil
}

type CatalogHousekeeperOptions struct {
	ServiceName          string
	ChainID              uint64
	Interval             time.Duration
	RetryInterval        time.Duration
	RetentionGenerations int64
	AdapterDeleteBatch   int
	Now                  func() time.Time
}

func (options *CatalogHousekeeperOptions) defaults() {
	if options.ServiceName == "" {
		options.ServiceName = "search-catalog-maintenance"
	}
	if options.Interval <= 0 {
		options.Interval = 15 * time.Minute
	}
	if options.RetryInterval <= 0 {
		options.RetryInterval = min(options.Interval, 30*time.Second)
	}
	if options.RetentionGenerations == 0 {
		options.RetentionGenerations = 100_000
	}
	if options.AdapterDeleteBatch == 0 {
		options.AdapterDeleteBatch = 1_000
	}
	if options.Now == nil {
		options.Now = time.Now
	}
}

type CatalogHousekeeper struct {
	cleaner CatalogCleaner
	logger  *slog.Logger
	options CatalogHousekeeperOptions
}

func NewCatalogHousekeeper(
	cleaner CatalogCleaner,
	logger *slog.Logger,
	options CatalogHousekeeperOptions,
) (*CatalogHousekeeper, error) {
	if cleaner == nil {
		return nil, errors.New("catalog housekeeper requires a cleaner")
	}
	options.defaults()
	options.ServiceName = strings.TrimSpace(options.ServiceName)
	if options.ServiceName == "" || len(options.ServiceName) > 128 || options.ChainID == 0 ||
		options.Interval <= 0 || options.RetryInterval <= 0 || options.RetryInterval > options.Interval ||
		options.RetentionGenerations < 1000 || options.RetentionGenerations > 10_000_000 ||
		options.AdapterDeleteBatch <= 0 || options.AdapterDeleteBatch > 10_000 || options.Now == nil {
		return nil, errors.New("catalog housekeeper options are invalid")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &CatalogHousekeeper{cleaner: cleaner, logger: logger, options: options}, nil
}

func (housekeeper *CatalogHousekeeper) Name() string {
	if housekeeper == nil || housekeeper.options.ServiceName == "" {
		return "search-catalog-maintenance"
	}
	return housekeeper.options.ServiceName
}

func (housekeeper *CatalogHousekeeper) Run(ctx context.Context) error {
	if housekeeper == nil || housekeeper.cleaner == nil {
		return errors.New("run nil catalog housekeeper")
	}
	delay := time.Duration(0)
	retry := housekeeper.options.RetryInterval
	for {
		if delay > 0 {
			if err := waitForContext(ctx, delay); err != nil {
				return err
			}
		}
		_, err := housekeeper.cleaner.Sweep(
			ctx,
			housekeeper.options.ChainID,
			housekeeper.options.RetentionGenerations,
			housekeeper.options.AdapterDeleteBatch,
			housekeeper.options.Now().UTC(),
		)
		if err == nil {
			delay = housekeeper.options.Interval
			retry = housekeeper.options.RetryInterval
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		housekeeper.logger.WarnContext(ctx, "catalog maintenance sweep failed", "error_code", "catalog_maintenance_failed")
		delay = retry
		if retry < housekeeper.options.Interval {
			if retry >= housekeeper.options.Interval/2 {
				retry = housekeeper.options.Interval
			} else {
				retry *= 2
			}
		}
	}
}

func uint64MaintenanceNumeric(value uint64) pgtype.Numeric {
	return pgtype.Numeric{Int: new(big.Int).SetUint64(value), Valid: true}
}
