package billing

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/x402settlement"
	"github.com/jackc/pgx/v5/pgtype"
)

func (ledger *PostgresLedger) ReleaseVerification(ctx context.Context, paymentID, owner string, at time.Time) error {
	id, fence, err := transitionIDs(paymentID, owner)
	if err != nil {
		return err
	}
	return dbaccess.WithQueries(ctx, ledger.db, func(q *dbgen.Queries) error {
		count, e := q.ReleaseBillingTopupVerification(ctx, postgresTime(at), id, fence)
		if e != nil {
			return e
		}
		if count != 1 {
			return ErrStateConflict
		}
		return nil
	})
}

func (ledger *PrepaidLedger) CreditTopupResult(ctx context.Context, paymentID, attempt string, result *x402settlement.Result, origin [32]byte, at time.Time) (Account, error) {
	id, fence, err := transitionIDs(paymentID, attempt)
	if err != nil {
		return Account{}, err
	}
	owner, e := uuid.NewRandom()
	if e != nil {
		return Account{}, e
	}
	var row dbgen.BillingPayment
	err = dbaccess.WithQueries(ctx, ledger.db, func(q *dbgen.Queries) error {
		var e error
		row, e = q.ClaimBillingTopupResult(ctx, uuidValue(owner), id, fence)
		return e
	})
	if err != nil {
		return Account{}, err
	}
	return ledger.publishTopupResult(ctx, row, result, origin, at)
}

func (ledger *PrepaidLedger) publishTopupResult(ctx context.Context, payment dbgen.BillingPayment, result *x402settlement.Result, origin [32]byte, at time.Time) (Account, error) {
	if result == nil || result.State != "succeeded" || result.Evidence == nil || result.Response == nil || !result.Response.Success || payment.AuthorizationID == nil || *payment.AuthorizationID != result.AuthorizationID || hex.EncodeToString(payment.RequestDigest) != result.RequestDigest || !bytes.Equal(payment.FacilitatorDigest, origin[:]) {
		return Account{}, ErrIntegrity
	}
	e := result.Evidence
	amount, ok := new(big.Int).SetString(e.Amount, 10)
	if !ok || amount.Sign() <= 0 || amount.BitLen() > 256 {
		return Account{}, ErrIntegrity
	}
	var original []byte
	if result.OriginalTransaction != "" {
		hash, ok := transactionHashFromHex(result.OriginalTransaction)
		if !ok || zeroTransactionHash(hash) {
			return Account{}, ErrIntegrity
		}
		original = hash.Bytes()
	}
	hash, ok := transactionHashFromHex(e.Transaction)
	if !ok || zeroTransactionHash(hash) || result.Response.Transaction != e.Transaction {
		return Account{}, ErrIntegrity
	}
	proof, err := json.Marshal(e)
	if err != nil {
		return Account{}, err
	}
	entry, err := uuid.NewRandom()
	if err != nil {
		return Account{}, err
	}
	var row dbgen.CreditBillingTopupResultRow
	err = dbaccess.WithQueries(ctx, ledger.db, func(q *dbgen.Queries) error {
		var queryErr error
		row, queryErr = q.CreditBillingTopupResult(ctx, dbgen.CreditBillingTopupResultParams{PaymentID: payment.ID, ReservationOwner: payment.ReconciliationOwner, AuthorizationID: payment.AuthorizationID, RequestDigest: payment.RequestDigest, FacilitatorDigest: origin[:], Network: e.Network, Asset: common.HexToAddress(e.Asset).Bytes(), Payer: common.HexToAddress(e.Payer).Bytes(), Recipient: common.HexToAddress(e.Recipient).Bytes(), AmountAtomic: pgtype.Numeric{Int: amount, Valid: true}, OriginalTransaction: original, TransactionHash: hash.Bytes(), Evidence: proof, TransitionedAt: postgresTime(at), EntryID: uuidValue(entry)})
		return queryErr
	})
	if err != nil {
		return Account{}, err
	}
	return accountFromValues(row.UserID, row.ChainID, row.TotalCreditAtomic, row.TotalDebitAtomic, row.ReservedAtomic, row.CreatedAt, row.UpdatedAt)
}

// Run is owned by the API role, where facilitator credentials already live.
// Claim transactions finish before HTTP. Publication rechecks the current lease
// and all payment/intent bindings in the same statement as the unique credit.
func (dispatcher *TopupDispatcher) Name() string { return "topup-reconciliation" }
func (dispatcher *TopupDispatcher) Run(ctx context.Context) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := dispatcher.reconcileTopups(ctx); err != nil && !errors.Is(err, context.Canceled) {
				dispatcher.recoveryLogger().Error("top-up reconciliation unavailable", "error_code", "topup_reconciliation_unavailable")
			}
		}
	}
}

func (dispatcher *TopupDispatcher) reconcileTopups(ctx context.Context) error {
	routes := dispatcher.routes
	if routes == nil {
		routes = map[string]*TopupDispatcher{"": dispatcher}
	}
	var first *TopupDispatcher
	for _, route := range routes {
		first = route
		break
	}
	if first == nil {
		return nil
	}
	ledger, ok := first.accounts.(*PrepaidLedger)
	if !ok {
		return ErrInvalidInput
	}
	owner, err := uuid.NewRandom()
	if err != nil {
		return err
	}
	var rows []dbgen.BillingPayment
	err = dbaccess.WithQueries(ctx, ledger.db, func(q *dbgen.Queries) error {
		var e error
		rows, e = q.ClaimBillingTopupResults(ctx, uuidValue(owner), ledger.chainNumeric)
		return e
	})
	if err != nil {
		return err
	}
	var workers sync.WaitGroup
	defer workers.Wait()
	slots := make(chan struct{}, 4)
	for _, row := range rows {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case slots <- struct{}{}:
		}
		workers.Go(func() { defer func() { <-slots }(); dispatcher.reconcileTopupResult(ctx, ledger, routes, row) })
	}

	return nil
}

func (dispatcher *TopupDispatcher) recoveryLogger() *slog.Logger {
	if dispatcher.logger != nil {
		return dispatcher.logger
	}
	return slog.Default()
}

func (ledger *PrepaidLedger) failTopupResult(ctx context.Context, row dbgen.BillingPayment, result *x402settlement.Result, at time.Time) error {
	if result == nil || result.State != "failed" || result.Response == nil || result.Response.Success || row.AuthorizationID == nil || *row.AuthorizationID != result.AuthorizationID || hex.EncodeToString(row.RequestDigest) != result.RequestDigest || string(result.Response.Network) != row.Network {
		return ErrIntegrity
	}
	var original []byte
	if result.OriginalTransaction != "" {
		hash, ok := transactionHashFromHex(result.OriginalTransaction)
		if !ok || zeroTransactionHash(hash) {
			return ErrIntegrity
		}
		original = hash.Bytes()
	}
	return dbaccess.WithQueries(ctx, ledger.db, func(q *dbgen.Queries) error {
		_, err := q.FailBillingTopupResult(ctx, dbgen.FailBillingTopupResultParams{PaymentID: row.ID, Owner: row.ReconciliationOwner, AuthorizationID: row.AuthorizationID, RequestDigest: row.RequestDigest, OriginalTransaction: original, TransitionedAt: postgresTime(at)})
		return err
	})
}

func (dispatcher *TopupDispatcher) reconcileTopupResult(ctx context.Context, ledger *PrepaidLedger, routes map[string]*TopupDispatcher, row dbgen.BillingPayment) {
	var route *TopupDispatcher
	for _, candidate := range routes {
		origin := candidate.facilitator.OriginDigest()
		if candidate.network == row.Network && bytes.Equal(origin[:], row.FacilitatorDigest) {
			route = candidate
			break
		}
	}
	if route != nil && row.AuthorizationID != nil {
		attempt, cancel := context.WithTimeout(ctx, 10*time.Second)
		result, err := route.facilitator.PaymentResult(attempt, x402settlement.Reference{AuthorizationID: *row.AuthorizationID, RequestDigest: hex.EncodeToString(row.RequestDigest)})
		if err == nil && result != nil && result.State == "succeeded" {
			_, err = ledger.publishTopupResult(attempt, row, result, route.facilitator.OriginDigest(), time.Now().UTC())
		}
		if err == nil && result != nil && result.State == "failed" {
			err = ledger.failTopupResult(attempt, row, result, time.Now().UTC())
		}
		if err != nil && ctx.Err() == nil {
			dispatcher.recoveryLogger().Warn("top-up result remains pending", "error_code", "topup_result_unavailable")
		}
		cancel()
	}
	_ = dbaccess.WithQueries(ctx, ledger.db, func(q *dbgen.Queries) error {
		_, err := q.ReleaseBillingTopupResult(ctx, row.ID, row.ReconciliationOwner)
		return err
	})
}
