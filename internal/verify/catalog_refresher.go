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
	ticker := time.NewTicker(refresher.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			for _, language := range []Language{LanguageSolidity, LanguageVyper} {
				if _, err := refresher.catalog.Refresh(ctx, language); err != nil {
					if _, retainedErr := refresher.catalog.Versions(ctx, language); retainedErr != nil {
						return errors.Join(err, retainedErr)
					}
				}
			}
		}
	}
}
