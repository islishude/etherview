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
	if delay := refresher.refreshDelay(context.Background()); delay != time.Minute {
		t.Fatalf("refresh delay=%s, want 1m", delay)
	}
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	if catalog.refresh != 1 {
		t.Fatalf("refresh calls=%d", catalog.refresh)
	}
}

func TestCatalogRefresherRetriesWhenNoGenerationExists(t *testing.T) {
	t.Parallel()
	catalog := &refresherCatalog{}
	refresher, err := NewCatalogRefresher(catalog, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if delay := refresher.refreshDelay(context.Background()); delay != unavailableCatalogRetryInterval {
		t.Fatalf("refresh delay=%s, want %s", delay, unavailableCatalogRetryInterval)
	}
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	if catalog.refresh != 1 {
		t.Fatalf("refresh calls=%d", catalog.refresh)
	}
}
