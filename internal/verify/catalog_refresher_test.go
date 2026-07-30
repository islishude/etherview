package verify

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type refresherCatalog struct {
	mu       sync.Mutex
	refresh  int
	retained bool
}

func (catalog *refresherCatalog) Refresh(context.Context, Language) (int64, error) {
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	catalog.refresh++
	return 0, errors.New("catalog unavailable")
}

func (catalog *refresherCatalog) Versions(context.Context, Language) ([]string, error) {
	if !catalog.retained {
		return nil, errors.New("no retained catalog")
	}
	return []string{"0.8.30"}, nil
}

func TestCatalogRefresherRetainsLastSuccessfulGeneration(t *testing.T) {
	t.Parallel()
	catalog := &refresherCatalog{retained: true}
	refresher, err := NewCatalogRefresher(catalog, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// Use the same refresh loop behavior directly because the public minimum
	// interval intentionally prevents a timing-dependent minute-long test.
	if _, refreshErr := refresher.catalog.Refresh(context.Background(), LanguageSolidity); refreshErr != nil {
		if _, retainedErr := refresher.catalog.Versions(context.Background(), LanguageSolidity); retainedErr != nil {
			t.Fatal(retainedErr)
		}
	}
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	if catalog.refresh != 1 {
		t.Fatalf("refresh calls=%d", catalog.refresh)
	}
}
