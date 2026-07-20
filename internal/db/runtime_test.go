package dbaccess

import (
	"context"
	"database/sql"
	"testing"

	"github.com/islishude/etherview/internal/db/gen"
)

func TestWithQueriesRejectsMissingInputs(t *testing.T) {
	t.Parallel()
	if err := WithQueries(context.Background(), nil, func(_ *dbgen.Queries) error { return nil }); err == nil {
		t.Fatal("nil database was accepted")
	}
}

func TestWithTransactionRejectsMissingInputs(t *testing.T) {
	t.Parallel()
	if err := WithTransaction(context.Background(), nil, func(_ *dbgen.Queries) error { return nil }); err == nil {
		t.Fatal("nil transaction database was accepted")
	}
	if err := WithTransaction(context.Background(), &sql.DB{}, nil); err == nil {
		t.Fatal("nil transaction callback was accepted")
	}
}
