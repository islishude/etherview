package verify

import (
	"context"
	"errors"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"time"
)

type admissionPrincipalKey struct{}

// WithAdmissionPrincipal accepts only server-authenticated identities. A principal
// is deliberately outside SubmissionV2 and its deduplication digest.
func WithAdmissionPrincipal(ctx context.Context, principal string) context.Context {
	return context.WithValue(ctx, admissionPrincipalKey{}, principal)
}
func admissionPrincipal(ctx context.Context) string {
	if value, ok := ctx.Value(admissionPrincipalKey{}).(string); ok && value != "" {
		return value
	}
	return "system:verification"
}
func (repository *PostgresRepository) beginCapacityTx(ctx context.Context) (pgx.Tx, error) {
	tx, err := repository.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	if err = dbgen.New(tx).VerifyLockBudget(ctx); err != nil {
		dbaccess.Rollback(ctx, tx)
		return nil, err
	}
	return tx, nil
}
func capacityError(err error) error {
	if postgres, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch postgres.Code {
		case "P7501":
			return ServiceError{Code: ServiceUserCapacity}
		case "P7502":
			return ServiceError{Code: ServiceGlobalCapacity}
		case "P7503":
			return ServiceError{Code: ServiceInvalidRequest}
		}
	}
	return err
}

type InputCleanup struct{ Repository *PostgresRepository }

func (*InputCleanup) Name() string { return "verification-input-cleanup" }
func (cleanup *InputCleanup) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if _, err := dbgen.New(cleanup.Repository.db).VerifyCleanupInputs(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
