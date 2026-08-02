package verify

import (
	"context"
	"errors"
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
	catalog  catalogRefreshStore
	interval time.Duration
}

const unavailableCatalogRetryInterval = 5 * time.Second

func NewCatalogRefresher(catalog catalogRefreshStore, interval time.Duration) (*CatalogRefresher, error) {
	if catalog == nil || interval < time.Minute || interval > 24*time.Hour {
		return nil, errors.New("compiler catalog refresher configuration is invalid")
	}
	return &CatalogRefresher{catalog: catalog, interval: interval}, nil
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
	if _, err := refresher.catalog.Refresh(ctx, LanguageSolidity); err == nil {
		return refresher.interval
	}
	if _, err := refresher.catalog.Versions(ctx, LanguageSolidity); err == nil {
		return refresher.interval
	}
	return min(refresher.interval, unavailableCatalogRetryInterval)
}
