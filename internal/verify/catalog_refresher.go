package verify

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

type catalogRefreshStore interface {
	Refresh(context.Context, Language) (int64, error)
	Versions(context.Context, Language) ([]string, error)
}

// CatalogRefresher keeps immutable compiler catalog generations current. A
// failed fetch retains the last successful generation; only the absence of any
// usable generation is fatal.
type CatalogRefresher struct {
	catalog    catalogRefreshStore
	interval   time.Duration
	logger     *slog.Logger
	state      string
	generation int64
}

const unavailableCatalogRetryInterval = 5 * time.Second

func NewCatalogRefresher(
	catalog catalogRefreshStore,
	interval time.Duration,
	loggers ...*slog.Logger,
) (*CatalogRefresher, error) {
	if catalog == nil || interval < time.Minute || interval > 24*time.Hour {
		return nil, errors.New("compiler catalog refresher configuration is invalid")
	}
	logger := slog.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	return &CatalogRefresher{catalog: catalog, interval: interval, logger: logger}, nil
}

func (refresher *CatalogRefresher) Name() string {
	return "compiler-catalog-refresher"
}

func (refresher *CatalogRefresher) Run(ctx context.Context) error {
	for {
		delay := refresher.refreshDelay(ctx)
		if err := waitForContext(ctx, delay); err != nil {
			return err
		}
	}
}

func (refresher *CatalogRefresher) refreshDelay(ctx context.Context) time.Duration {
	generation, refreshErr := refresher.catalog.Refresh(ctx, LanguageSolidity)
	if refreshErr == nil {
		level := slog.LevelDebug
		if refresher.state != "available" || refresher.generation != generation {
			level = slog.LevelInfo
		}
		refresher.logger.LogAttrs(ctx, level, "compiler catalog refreshed",
			slog.String("event", "compiler_catalog_available"),
			slog.String("component", refresher.Name()),
			slog.String("family", "solcjs"), slog.Int64("generation", generation),
			slog.Int64("next_refresh_ms", refresher.interval.Milliseconds()),
		)
		refresher.state, refresher.generation = "available", generation
		return refresher.interval
	}
	versions, versionsErr := refresher.catalog.Versions(ctx, LanguageSolidity)
	if versionsErr == nil {
		if refresher.state != "stale" {
			refresher.logger.WarnContext(ctx, "compiler catalog refresh failed; retaining prior generation",
				"event", "compiler_catalog_stale", "component", refresher.Name(),
				"family", "solcjs", "version_count", len(versions),
				"error_code", "catalog_refresh_failed",
			)
		}
		refresher.state = "stale"
		return refresher.interval
	}
	if refresher.state != "unavailable" {
		refresher.logger.WarnContext(ctx, "compiler catalog unavailable",
			"event", "compiler_catalog_unavailable", "component", refresher.Name(),
			"family", "solcjs", "error_code", "catalog_unavailable",
			"retry_in_ms", min(refresher.interval, unavailableCatalogRetryInterval).Milliseconds(),
		)
	}
	refresher.state = "unavailable"
	return min(refresher.interval, unavailableCatalogRetryInterval)
}
