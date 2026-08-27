package billing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	dbaccess "github.com/islishude/etherview/internal/db"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type PrepaidLedger struct {
	db           *sql.DB
	chainID      uint64
	chainNumeric pgtype.Numeric
	network      string
	asset        common.Address
	recipient    common.Address
	topupTTL     time.Duration
	usageTTL     time.Duration
}

type PrepaidOptions struct {
	ChainID   uint64
	Network   string
	Asset     common.Address
	Recipient common.Address
	TopupTTL  time.Duration
	UsageTTL  time.Duration
}

func NewPrepaidLedger(db *sql.DB, options PrepaidOptions) (*PrepaidLedger, error) {
	if db == nil || options.ChainID == 0 ||
		!billingNetworkPattern.MatchString(options.Network) ||
		options.Asset == (common.Address{}) {
		return nil, fmt.Errorf("%w: prepaid ledger configuration", ErrInvalidInput)
	}
	if options.TopupTTL < time.Minute || options.TopupTTL > time.Hour ||
		options.UsageTTL < 10*time.Second || options.UsageTTL > 10*time.Minute {
		return nil, fmt.Errorf("%w: prepaid ledger TTL", ErrInvalidInput)
	}
	return &PrepaidLedger{
		db: db, chainID: options.ChainID,
		chainNumeric: pgtype.Numeric{Int: new(big.Int).SetUint64(options.ChainID), Valid: true},
		network:      options.Network, asset: options.Asset, recipient: options.Recipient,
		topupTTL: options.TopupTTL, usageTTL: options.UsageTTL,
	}, nil
}

func (ledger *PrepaidLedger) EnsureAccount(
	ctx context.Context,
	userID string,
	observedAt time.Time,
) (Account, error) {
	identifier, err := parseUUID(userID)
	if err != nil || observedAt.IsZero() {
		return Account{}, ErrInvalidInput
	}
	var row dbgen.EnsureBillingAccountRow
	err = dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.EnsureBillingAccount(ctx, dbgen.EnsureBillingAccountParams{
			Network: ledger.network, Asset: ledger.asset[:],
			CreatedAt: postgresTime(observedAt), UserID: identifier,
			ChainID: ledger.chainNumeric,
		})
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("ensure billing account: %w", err)
	}
	return accountFromValues(
		row.UserID, row.ChainID, row.Network, row.Asset,
		row.TotalCreditAtomic, row.TotalDebitAtomic, row.ReservedAtomic,
		row.CreatedAt, row.UpdatedAt,
	)
}

func (ledger *PrepaidLedger) Account(
	ctx context.Context,
	userID string,
) (Account, error) {
	identifier, err := parseUUID(userID)
	if err != nil {
		return Account{}, ErrNotFound
	}
	var row dbgen.BillingAccount
	err = dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.GetBillingAccount(ctx, dbgen.GetBillingAccountParams{
			UserID: identifier, ChainID: ledger.chainNumeric,
			Network: ledger.network, Asset: ledger.asset[:],
		})
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("read billing account: %w", err)
	}
	return accountFromValues(
		row.UserID, row.ChainID, row.Network, row.Asset,
		row.TotalCreditAtomic, row.TotalDebitAtomic, row.ReservedAtomic,
		row.CreatedAt, row.UpdatedAt,
	)
}

func (ledger *PrepaidLedger) CreateTopupIntent(
	ctx context.Context,
	input CreateTopupIntentInput,
) (TopupIntent, error) {
	userID, err := parseUUID(input.UserID)
	amount, ok := canonicalPositiveNumeric(input.AmountAtomic)
	if err != nil || !ok || input.Payer == (common.Address{}) ||
		ledger.recipient == (common.Address{}) || input.ObservedAt.IsZero() {
		return TopupIntent{}, ErrInvalidInput
	}
	if _, err := ledger.EnsureAccount(ctx, input.UserID, input.ObservedAt); err != nil {
		return TopupIntent{}, err
	}
	identifier, err := uuid.NewRandom()
	if err != nil {
		return TopupIntent{}, errors.New("generate billing top-up intent ID")
	}
	var row dbgen.BillingTopupIntent
	err = dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.CreateBillingTopupIntent(ctx, dbgen.CreateBillingTopupIntentParams{
			ID: uuidValue(identifier), Network: ledger.network, Asset: ledger.asset[:],
			AmountAtomic: pgtype.Numeric{Int: amount, Valid: true},
			Recipient:    ledger.recipient[:], ExpiresAt: postgresTime(input.ObservedAt.Add(ledger.topupTTL)),
			CreatedAt: postgresTime(input.ObservedAt), UserID: userID,
			ChainID: ledger.chainNumeric, Payer: input.Payer[:],
		})
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return TopupIntent{}, ErrNotFound
	}
	if err != nil {
		return TopupIntent{}, fmt.Errorf("create billing top-up intent: %w", err)
	}
	return topupIntentFromRow(row)
}

func (ledger *PrepaidLedger) TopupIntent(
	ctx context.Context,
	userID, intentID string,
) (TopupIntent, error) {
	user, err := parseUUID(userID)
	if err != nil {
		return TopupIntent{}, ErrNotFound
	}
	identifier, err := parseUUID(intentID)
	if err != nil {
		return TopupIntent{}, ErrNotFound
	}
	var row dbgen.BillingTopupIntent
	err = dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.GetUserBillingTopupIntent(
			ctx, identifier, ledger.chainNumeric, user,
		)
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return TopupIntent{}, ErrNotFound
	}
	if err != nil {
		return TopupIntent{}, fmt.Errorf("read billing top-up intent: %w", err)
	}
	return topupIntentFromRow(row)
}

func (ledger *PrepaidLedger) ListTopupIntents(
	ctx context.Context,
	userID string,
	after *PageAfter,
	limit int,
) ([]TopupIntent, error) {
	identifier, err := parseUUID(userID)
	if err != nil || limit < 1 || limit > maximumBillingPage {
		return nil, ErrInvalidInput
	}
	params := dbgen.ListUserBillingTopupIntentsParams{
		ChainID: ledger.chainNumeric, UserID: identifier, PageLimit: int32(limit),
	}
	if after != nil {
		beforeID, parseErr := parseUUID(after.ID)
		if parseErr != nil || after.CreatedAt.IsZero() {
			return nil, ErrInvalidInput
		}
		params.BeforeCreatedAt, params.BeforeID = postgresTime(after.CreatedAt), beforeID
	}
	var rows []dbgen.BillingTopupIntent
	err = dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		var queryErr error
		rows, queryErr = queries.ListUserBillingTopupIntents(ctx, params)
		return queryErr
	})
	if err != nil {
		return nil, fmt.Errorf("list billing top-up intents: %w", err)
	}
	return topupIntentsFromRows(rows)
}

func (ledger *PrepaidLedger) ListAdminAccounts(
	ctx context.Context,
	after *AccountPageAfter,
	limit int,
) ([]Account, error) {
	if limit < 1 || limit > maximumBillingPage {
		return nil, ErrInvalidInput
	}
	params := dbgen.ListAdminBillingAccountsParams{
		ChainID: ledger.chainNumeric, Network: ledger.network,
		Asset: ledger.asset[:], PageLimit: int32(limit),
	}
	if after != nil {
		userID, err := parseUUID(after.UserID)
		if err != nil || after.UpdatedAt.IsZero() {
			return nil, ErrInvalidInput
		}
		params.BeforeUpdatedAt, params.BeforeUserID = postgresTime(after.UpdatedAt), userID
	}
	var rows []dbgen.BillingAccount
	err := dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		var queryErr error
		rows, queryErr = queries.ListAdminBillingAccounts(ctx, params)
		return queryErr
	})
	if err != nil {
		return nil, fmt.Errorf("list billing accounts: %w", err)
	}
	result := make([]Account, len(rows))
	for index, row := range rows {
		result[index], err = accountFromValues(
			row.UserID, row.ChainID, row.Network, row.Asset,
			row.TotalCreditAtomic, row.TotalDebitAtomic, row.ReservedAtomic,
			row.CreatedAt, row.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (ledger *PrepaidLedger) AccountSummary(ctx context.Context) (AccountSummary, error) {
	var row dbgen.SummarizeBillingAccountsRow
	err := dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.SummarizeBillingAccounts(
			ctx, ledger.chainNumeric, ledger.network, ledger.asset[:],
		)
		return queryErr
	})
	if err != nil {
		return AccountSummary{}, fmt.Errorf("summarize billing accounts: %w", err)
	}
	count, err := integerNumericString(row.AccountCount)
	if err != nil {
		return AccountSummary{}, ErrIntegrity
	}
	credit, err := integerNumericString(row.TotalCreditAtomic)
	if err != nil {
		return AccountSummary{}, ErrIntegrity
	}
	debit, err := integerNumericString(row.TotalDebitAtomic)
	if err != nil {
		return AccountSummary{}, ErrIntegrity
	}
	reserved, err := integerNumericString(row.ReservedAtomic)
	if err != nil {
		return AccountSummary{}, ErrIntegrity
	}
	creditInt, _ := new(big.Int).SetString(credit, 10)
	debitInt, _ := new(big.Int).SetString(debit, 10)
	reservedInt, _ := new(big.Int).SetString(reserved, 10)
	available := new(big.Int).Sub(creditInt, debitInt)
	available.Sub(available, reservedInt)
	if available.Sign() < 0 {
		return AccountSummary{}, ErrIntegrity
	}
	return AccountSummary{
		AccountCount: count, TotalCreditAtomic: credit,
		TotalDebitAtomic: debit, ReservedAtomic: reserved,
		AvailableAtomic: available.String(),
	}, nil
}

func (ledger *PrepaidLedger) ListAdminTopupIntents(
	ctx context.Context,
	after *PageAfter,
	limit int,
) ([]TopupIntent, error) {
	if limit < 1 || limit > maximumBillingPage {
		return nil, ErrInvalidInput
	}
	params := dbgen.ListAdminBillingTopupIntentsParams{
		ChainID: ledger.chainNumeric, Network: ledger.network,
		Asset: ledger.asset[:], PageLimit: int32(limit),
	}
	if after != nil {
		beforeID, err := parseUUID(after.ID)
		if err != nil || after.CreatedAt.IsZero() {
			return nil, ErrInvalidInput
		}
		params.BeforeCreatedAt, params.BeforeID = postgresTime(after.CreatedAt), beforeID
	}
	var rows []dbgen.BillingTopupIntent
	err := dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		var queryErr error
		rows, queryErr = queries.ListAdminBillingTopupIntents(ctx, params)
		return queryErr
	})
	if err != nil {
		return nil, fmt.Errorf("list administrator billing top-ups: %w", err)
	}
	return topupIntentsFromRows(rows)
}

func (ledger *PrepaidLedger) BeginTopupSettlement(
	ctx context.Context,
	paymentID, owner, intentID string,
	observedAt time.Time,
) error {
	payment, fence, intent, err := topupTransitionIDs(paymentID, owner, intentID)
	if err != nil || observedAt.IsZero() {
		return ErrInvalidInput
	}
	err = dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		_, queryErr := queries.BeginBillingTopupSettlement(ctx, dbgen.BeginBillingTopupSettlementParams{
			TransitionedAt: postgresTime(observedAt), PaymentID: payment,
			ReservationOwner: fence, IntentID: intent,
		})
		return queryErr
	})
	return topupTransitionError("begin top-up settlement", err)
}

func (ledger *PrepaidLedger) MarkTopupSettlementUnknown(
	ctx context.Context,
	paymentID, owner, intentID string,
	observedAt time.Time,
) error {
	payment, fence, intent, err := topupTransitionIDs(paymentID, owner, intentID)
	if err != nil || observedAt.IsZero() {
		return ErrInvalidInput
	}
	err = dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		_, queryErr := queries.MarkBillingTopupSettlementUnknown(ctx, dbgen.MarkBillingTopupSettlementUnknownParams{
			TransitionedAt: postgresTime(observedAt), PaymentID: payment,
			ReservationOwner: fence, IntentID: intent,
		})
		return queryErr
	})
	return topupTransitionError("mark top-up settlement unknown", err)
}

func (ledger *PrepaidLedger) MarkTopupSettlementPending(
	ctx context.Context,
	paymentID, owner, intentID string,
	transactionHash common.Hash,
	observedAt time.Time,
) error {
	if zeroTransactionHash(transactionHash) {
		return ErrInvalidInput
	}
	payment, fence, intent, err := topupTransitionIDs(paymentID, owner, intentID)
	if err != nil || observedAt.IsZero() {
		return ErrInvalidInput
	}
	err = dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		_, queryErr := queries.MarkBillingTopupSettlementPending(ctx, dbgen.MarkBillingTopupSettlementPendingParams{
			TransactionHash: transactionHash[:], TransitionedAt: postgresTime(observedAt),
			PaymentID: payment, ReservationOwner: fence, IntentID: intent,
		})
		return queryErr
	})
	return topupTransitionError("mark top-up settlement pending", err)
}

func (ledger *PrepaidLedger) CreditTopup(
	ctx context.Context,
	paymentID, owner string,
	transactionHash common.Hash,
	observedAt time.Time,
) (Account, error) {
	if zeroTransactionHash(transactionHash) || observedAt.IsZero() {
		return Account{}, ErrInvalidInput
	}
	payment, fence, err := transitionIDs(paymentID, owner)
	if err != nil {
		return Account{}, ErrInvalidInput
	}
	entryID, err := uuid.NewRandom()
	if err != nil {
		return Account{}, errors.New("generate billing top-up credit entry ID")
	}
	var row dbgen.CreditBillingTopupRow
	err = dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.CreditBillingTopup(ctx, dbgen.CreditBillingTopupParams{
			PaymentID: payment, ReservationOwner: fence,
			TransactionHash: transactionHash[:], TransitionedAt: postgresTime(observedAt),
			EntryID: uuidValue(entryID),
		})
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrStateConflict
	}
	if err != nil {
		return Account{}, fmt.Errorf("credit billing top-up: %w", err)
	}
	return accountFromValues(
		row.UserID, row.ChainID, row.Network, row.Asset,
		row.TotalCreditAtomic, row.TotalDebitAtomic, row.ReservedAtomic,
		row.CreatedAt, row.UpdatedAt,
	)
}

func (ledger *PrepaidLedger) ReconcileTopupSettled(
	ctx context.Context,
	paymentID string,
	transactionHash common.Hash,
	observedAt time.Time,
) (Account, error) {
	if zeroTransactionHash(transactionHash) || observedAt.IsZero() {
		return Account{}, ErrInvalidInput
	}
	payment, err := parseUUID(paymentID)
	if err != nil {
		return Account{}, ErrNotFound
	}
	entryID, err := uuid.NewRandom()
	if err != nil {
		return Account{}, errors.New("generate reconciled top-up credit entry ID")
	}
	var row dbgen.ReconcileBillingTopupSettledRow
	err = dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.ReconcileBillingTopupSettled(
			ctx,
			dbgen.ReconcileBillingTopupSettledParams{
				PaymentID: payment, ChainID: ledger.chainNumeric,
				TransitionedAt:  postgresTime(observedAt),
				StaleBefore:     postgresTime(observedAt.Add(-SettlementCrashReconcileDelay)),
				TransactionHash: transactionHash[:], EntryID: uuidValue(entryID),
			},
		)
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrStateConflict
	}
	if err != nil {
		return Account{}, fmt.Errorf("reconcile billing top-up: %w", err)
	}
	return accountFromValues(
		row.UserID, row.ChainID, row.Network, row.Asset,
		row.TotalCreditAtomic, row.TotalDebitAtomic, row.ReservedAtomic,
		row.CreatedAt, row.UpdatedAt,
	)
}

func (ledger *PrepaidLedger) ReconcileTopupFailed(
	ctx context.Context,
	paymentID string,
	observedAt time.Time,
) error {
	if observedAt.IsZero() {
		return ErrInvalidInput
	}
	payment, err := parseUUID(paymentID)
	if err != nil {
		return ErrNotFound
	}
	err = dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		_, queryErr := queries.ReconcileBillingTopupFailed(
			ctx,
			dbgen.ReconcileBillingTopupFailedParams{
				PaymentID: payment, ChainID: ledger.chainNumeric,
				TransitionedAt: postgresTime(observedAt),
				StaleBefore:    postgresTime(observedAt.Add(-SettlementCrashReconcileDelay)),
			},
		)
		return queryErr
	})
	return topupTransitionError("reconcile failed billing top-up", err)
}

func (ledger *PrepaidLedger) FailTopupPayment(
	ctx context.Context,
	paymentID, owner, intentID, code string,
	observedAt time.Time,
) error {
	if !validFailureCode(code) || code == "settlement_unknown" ||
		code == "settlement_pending" || observedAt.IsZero() {
		return ErrInvalidInput
	}
	payment, fence, intent, err := topupTransitionIDs(paymentID, owner, intentID)
	if err != nil {
		return ErrInvalidInput
	}
	err = dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		_, queryErr := queries.FailBillingTopupPayment(ctx, dbgen.FailBillingTopupPaymentParams{
			FailureCode: &code, TransitionedAt: postgresTime(observedAt),
			PaymentID: payment, ReservationOwner: fence, IntentID: intent,
		})
		return queryErr
	})
	return topupTransitionError("fail top-up payment", err)
}

func (ledger *PrepaidLedger) FailTopupSettlement(
	ctx context.Context,
	paymentID, owner, intentID, code string,
	observedAt time.Time,
) error {
	if !validFailureCode(code) || code == "settlement_unknown" ||
		code == "settlement_pending" || observedAt.IsZero() {
		return ErrInvalidInput
	}
	payment, fence, intent, err := topupTransitionIDs(paymentID, owner, intentID)
	if err != nil {
		return ErrInvalidInput
	}
	err = dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		_, queryErr := queries.FailBillingTopupSettlement(ctx, dbgen.FailBillingTopupSettlementParams{
			FailureCode: &code, TransitionedAt: postgresTime(observedAt),
			PaymentID: payment, ReservationOwner: fence, IntentID: intent,
		})
		return queryErr
	})
	return topupTransitionError("fail top-up settlement", err)
}

func (ledger *PrepaidLedger) ReserveUsage(
	ctx context.Context,
	input ReserveUsageInput,
) (UsageReservation, error) {
	userID, err := parseUUID(input.UserID)
	amount, ok := canonicalPositiveNumeric(input.AmountAtomic)
	if err != nil || !ok || input.ObservedAt.IsZero() ||
		(input.Method != http.MethodGet && input.Method != http.MethodPost) ||
		!validAPIKeyPrefix(input.APIKeyPrefix) || !validUsageOperation(input.Operation) {
		return UsageReservation{}, ErrInvalidInput
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return UsageReservation{}, errors.New("generate billing usage ID")
	}
	owner, err := uuid.NewRandom()
	if err != nil {
		return UsageReservation{}, errors.New("generate billing usage owner")
	}
	var row dbgen.ReserveBillingUsageRow
	err = dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.ReserveBillingUsage(ctx, dbgen.ReserveBillingUsageParams{
			ApiKeyPrefix: input.APIKeyPrefix, UserID: userID,
			ChainID: ledger.chainNumeric, Network: ledger.network, Asset: ledger.asset[:],
			AmountAtomic: pgtype.Numeric{Int: amount, Valid: true},
			ID:           uuidValue(id), ReservationOwner: uuidValue(owner),
			Method: input.Method, Operation: input.Operation,
			ResourceDigest:       input.Resource[:],
			ReservationExpiresAt: postgresTime(input.ObservedAt.Add(ledger.usageTTL)),
			CreatedAt:            postgresTime(input.ObservedAt),
		})
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return UsageReservation{}, ErrInsufficientCredit
	}
	if err != nil {
		return UsageReservation{}, fmt.Errorf("reserve billing usage: %w", err)
	}
	charge, err := usageFromReserveRow(row)
	if err != nil {
		return UsageReservation{}, err
	}
	return UsageReservation{Charge: charge, Owner: owner.String()}, nil
}

func (ledger *PrepaidLedger) CommitUsage(
	ctx context.Context,
	input CommitUsageInput,
) (UsageCharge, error) {
	id, owner, err := transitionIDs(input.ChargeID, input.Owner)
	if err != nil || input.ObservedAt.IsZero() || input.ResponseBytes < 0 {
		return UsageCharge{}, ErrInvalidInput
	}
	entryID, err := uuid.NewRandom()
	if err != nil {
		return UsageCharge{}, errors.New("generate billing usage entry ID")
	}
	var row dbgen.CommitBillingUsageRow
	err = dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		var queryErr error
		responseBytes := input.ResponseBytes
		row, queryErr = queries.CommitBillingUsage(ctx, dbgen.CommitBillingUsageParams{
			ID: id, ReservationOwner: owner,
			CommittedAt: postgresTime(input.ObservedAt), EntryID: uuidValue(entryID),
			ResponseDigest: input.Response[:], ResponseBytes: &responseBytes,
		})
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return UsageCharge{}, ErrStateConflict
	}
	if err != nil {
		return UsageCharge{}, fmt.Errorf("commit billing usage: %w", err)
	}
	return usageFromCommitRow(row)
}

func (ledger *PrepaidLedger) ReleaseUsage(
	ctx context.Context,
	chargeID, owner, code string,
	observedAt time.Time,
) (UsageCharge, error) {
	id, fence, err := transitionIDs(chargeID, owner)
	if err != nil || observedAt.IsZero() || !validFailureCode(code) {
		return UsageCharge{}, ErrInvalidInput
	}
	var row dbgen.ReleaseBillingUsageRow
	err = dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.ReleaseBillingUsage(ctx, dbgen.ReleaseBillingUsageParams{
			ID: id, ReservationOwner: fence, ReleasedAt: postgresTime(observedAt),
			FailureCode: &code,
		})
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return UsageCharge{}, ErrStateConflict
	}
	if err != nil {
		return UsageCharge{}, fmt.Errorf("release billing usage: %w", err)
	}
	return usageFromReleaseRow(row)
}

func (ledger *PrepaidLedger) ListUsage(
	ctx context.Context,
	userID string,
	after *PageAfter,
	limit int,
) ([]UsageCharge, error) {
	identifier, err := parseUUID(userID)
	if err != nil || limit < 1 || limit > maximumBillingPage {
		return nil, ErrInvalidInput
	}
	params := dbgen.ListUserBillingUsageParams{
		ChainID: ledger.chainNumeric, UserID: identifier, PageLimit: int32(limit),
	}
	if after != nil {
		beforeID, parseErr := parseUUID(after.ID)
		if parseErr != nil || after.CreatedAt.IsZero() {
			return nil, ErrInvalidInput
		}
		params.BeforeCreatedAt = postgresTime(after.CreatedAt)
		params.BeforeID = beforeID
	}
	var rows []dbgen.BillingUsageCharge
	err = dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		var queryErr error
		rows, queryErr = queries.ListUserBillingUsage(ctx, params)
		return queryErr
	})
	if err != nil {
		return nil, fmt.Errorf("list billing usage: %w", err)
	}
	result := make([]UsageCharge, len(rows))
	for index := range rows {
		result[index], err = usageFromModel(rows[index])
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (ledger *PrepaidLedger) ListAdminUsage(
	ctx context.Context,
	after *PageAfter,
	limit int,
) ([]UsageCharge, error) {
	if limit < 1 || limit > maximumBillingPage {
		return nil, ErrInvalidInput
	}
	params := dbgen.ListAdminBillingUsageParams{
		ChainID: ledger.chainNumeric, Network: ledger.network,
		Asset: ledger.asset[:], PageLimit: int32(limit),
	}
	if after != nil {
		beforeID, err := parseUUID(after.ID)
		if err != nil || after.CreatedAt.IsZero() {
			return nil, ErrInvalidInput
		}
		params.BeforeCreatedAt, params.BeforeID = postgresTime(after.CreatedAt), beforeID
	}
	var rows []dbgen.BillingUsageCharge
	err := dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		var queryErr error
		rows, queryErr = queries.ListAdminBillingUsage(ctx, params)
		return queryErr
	})
	if err != nil {
		return nil, fmt.Errorf("list administrator billing usage: %w", err)
	}
	result := make([]UsageCharge, len(rows))
	for index := range rows {
		result[index], err = usageFromModel(rows[index])
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (ledger *PrepaidLedger) Adjust(
	ctx context.Context,
	input AdjustmentInput,
) (Account, error) {
	userID, err := parseUUID(input.UserID)
	amount, ok := canonicalPositiveNumeric(input.AmountAtomic)
	reason := strings.TrimSpace(input.Reason)
	if err != nil || !ok || input.ObservedAt.IsZero() ||
		(input.Direction != "credit" && input.Direction != "debit") ||
		reason == "" || len(reason) > 256 {
		return Account{}, ErrInvalidInput
	}
	entryID, err := uuid.NewRandom()
	if err != nil {
		return Account{}, errors.New("generate billing adjustment entry ID")
	}
	sourceID, err := uuid.NewRandom()
	if err != nil {
		return Account{}, errors.New("generate billing adjustment source ID")
	}
	var row dbgen.AdjustBillingAccountRow
	err = dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.AdjustBillingAccount(ctx, dbgen.AdjustBillingAccountParams{
			UserID: userID, ChainID: ledger.chainNumeric, Network: ledger.network,
			Asset: ledger.asset[:], Direction: input.Direction,
			AmountAtomic: pgtype.Numeric{Int: amount, Valid: true},
			OccurredAt:   postgresTime(input.ObservedAt), EntryID: uuidValue(entryID),
			SourceID: uuidValue(sourceID), Reason: &reason,
		})
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrInsufficientCredit
	}
	if err != nil {
		return Account{}, fmt.Errorf("adjust billing account: %w", err)
	}
	return accountFromValues(
		row.UserID, row.ChainID, row.Network, row.Asset,
		row.TotalCreditAtomic, row.TotalDebitAtomic, row.ReservedAtomic,
		row.CreatedAt, row.UpdatedAt,
	)
}

func (ledger *PrepaidLedger) Expire(
	ctx context.Context,
	observedAt time.Time,
	limit int,
) (ExpiryResult, error) {
	if observedAt.IsZero() || limit < 1 || limit > 1_000 {
		return ExpiryResult{}, ErrInvalidInput
	}
	params := dbgen.ExpireBillingUsageReservationsParams{
		ChainID: ledger.chainNumeric, Network: ledger.network, Asset: ledger.asset[:],
		ObservedAt: postgresTime(observedAt), ExpireLimit: int32(limit),
	}
	var result ExpiryResult
	err := dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		count, queryErr := queries.ExpireBillingUsageReservations(ctx, params)
		if queryErr != nil {
			return queryErr
		}
		result.UsageReservations = uint64(count)
		count, queryErr = queries.ExpireBillingTopupPayments(
			ctx, ledger.chainNumeric, postgresTime(observedAt), int32(limit),
		)
		if queryErr != nil {
			return queryErr
		}
		result.TopupPayments = uint64(count)
		count, queryErr = queries.ExpireOpenBillingTopupIntents(
			ctx,
			dbgen.ExpireOpenBillingTopupIntentsParams{
				ChainID: ledger.chainNumeric, Network: ledger.network, Asset: ledger.asset[:],
				ObservedAt: postgresTime(observedAt), ExpireLimit: int32(limit),
			},
		)
		if queryErr != nil {
			return queryErr
		}
		result.TopupIntents = uint64(count)
		return nil
	})
	if err != nil {
		return ExpiryResult{}, fmt.Errorf("expire prepaid billing state: %w", err)
	}
	return result, nil
}

func canonicalPositiveNumeric(value string) (*big.Int, bool) {
	parsed, ok := new(big.Int).SetString(value, 10)
	return parsed, ok && parsed.Sign() > 0 && parsed.Cmp(maximumUint256) <= 0 && parsed.String() == value
}

func topupTransitionIDs(paymentID, owner, intentID string) (
	pgtype.UUID,
	pgtype.UUID,
	pgtype.UUID,
	error,
) {
	payment, fence, err := transitionIDs(paymentID, owner)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, err
	}
	intent, err := parseUUID(intentID)
	return payment, fence, intent, err
}

func topupTransitionError(action string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStateConflict
	}
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	return nil
}

func validUsageOperation(value string) bool {
	return strings.HasPrefix(value, "etherscan.") && len(value) > len("etherscan.") && len(value) <= 128
}

func accountFromValues(
	userID pgtype.UUID,
	chainID pgtype.Numeric,
	network string,
	asset []byte,
	credit pgtype.Numeric,
	debit pgtype.Numeric,
	reserved pgtype.Numeric,
	created pgtype.Timestamptz,
	updated pgtype.Timestamptz,
) (Account, error) {
	user, err := uuidString(userID)
	if err != nil {
		return Account{}, ErrIntegrity
	}
	chain, err := uint64Numeric(chainID)
	if err != nil || !billingNetworkPattern.MatchString(network) {
		return Account{}, ErrIntegrity
	}
	address, err := addressFromBytes(asset)
	if err != nil {
		return Account{}, ErrIntegrity
	}
	creditText, err := integerNumericString(credit)
	if err != nil {
		return Account{}, ErrIntegrity
	}
	debitText, err := integerNumericString(debit)
	if err != nil {
		return Account{}, ErrIntegrity
	}
	reservedText, err := integerNumericString(reserved)
	if err != nil {
		return Account{}, ErrIntegrity
	}
	creditInt, _ := new(big.Int).SetString(creditText, 10)
	debitInt, _ := new(big.Int).SetString(debitText, 10)
	reservedInt, _ := new(big.Int).SetString(reservedText, 10)
	available := new(big.Int).Sub(creditInt, debitInt)
	available.Sub(available, reservedInt)
	if available.Sign() < 0 {
		return Account{}, ErrIntegrity
	}
	createdAt, err := requiredTime(created)
	if err != nil {
		return Account{}, ErrIntegrity
	}
	updatedAt, err := requiredTime(updated)
	if err != nil || updatedAt.Before(createdAt) {
		return Account{}, ErrIntegrity
	}
	return Account{
		UserID: user, ChainID: chain, Network: network, Asset: address,
		TotalCreditAtomic: creditText, TotalDebitAtomic: debitText,
		ReservedAtomic: reservedText, AvailableAtomic: available.String(),
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}, nil
}

func topupIntentFromRow(row dbgen.BillingTopupIntent) (TopupIntent, error) {
	id, err := uuidString(row.ID)
	if err != nil {
		return TopupIntent{}, ErrIntegrity
	}
	userID, err := uuidString(row.UserID)
	if err != nil {
		return TopupIntent{}, ErrIntegrity
	}
	chainID, err := uint64Numeric(row.ChainID)
	if err != nil {
		return TopupIntent{}, ErrIntegrity
	}
	asset, err := addressFromBytes(row.Asset)
	if err != nil {
		return TopupIntent{}, ErrIntegrity
	}
	recipient, err := addressFromBytes(row.Recipient)
	if err != nil {
		return TopupIntent{}, ErrIntegrity
	}
	payer, err := addressFromBytes(row.Payer)
	if err != nil {
		return TopupIntent{}, ErrIntegrity
	}
	amount, err := integerNumericString(row.AmountAtomic)
	if err != nil {
		return TopupIntent{}, ErrIntegrity
	}
	state := TopupIntentState(row.State)
	if !state.valid() || !billingNetworkPattern.MatchString(row.Network) {
		return TopupIntent{}, ErrIntegrity
	}
	expiresAt, err := requiredTime(row.ExpiresAt)
	if err != nil {
		return TopupIntent{}, ErrIntegrity
	}
	createdAt, err := requiredTime(row.CreatedAt)
	if err != nil {
		return TopupIntent{}, ErrIntegrity
	}
	updatedAt, err := requiredTime(row.UpdatedAt)
	if err != nil {
		return TopupIntent{}, ErrIntegrity
	}
	result := TopupIntent{
		ID: id, UserID: userID, ChainID: chainID, Network: row.Network,
		Asset: asset, AmountAtomic: amount, Recipient: recipient, Payer: payer,
		State: state, FailureCode: cloneString(row.FailureCode), ExpiresAt: expiresAt,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
	if len(row.TransactionHash) != 0 {
		value, valueErr := transactionHashFromBytes(row.TransactionHash)
		if valueErr != nil || zeroTransactionHash(value) {
			return TopupIntent{}, ErrIntegrity
		}
		result.TransactionHash = &value
	}
	if row.ActivePaymentID.Valid {
		value, valueErr := uuidString(row.ActivePaymentID)
		if valueErr != nil {
			return TopupIntent{}, ErrIntegrity
		}
		result.ActivePaymentID = &value
	}
	result.ProcessingAt, err = strictOptionalTime(row.ProcessingAt)
	if err != nil {
		return TopupIntent{}, ErrIntegrity
	}
	result.SettlingAt, err = strictOptionalTime(row.SettlingAt)
	if err != nil {
		return TopupIntent{}, ErrIntegrity
	}
	result.CreditedAt, err = strictOptionalTime(row.CreditedAt)
	if err != nil {
		return TopupIntent{}, ErrIntegrity
	}
	result.FailedAt, err = strictOptionalTime(row.FailedAt)
	if err != nil {
		return TopupIntent{}, ErrIntegrity
	}
	result.ExpiredAt, err = strictOptionalTime(row.ExpiredAt)
	if err != nil {
		return TopupIntent{}, ErrIntegrity
	}
	return result, nil
}

func topupIntentsFromRows(rows []dbgen.BillingTopupIntent) ([]TopupIntent, error) {
	result := make([]TopupIntent, len(rows))
	for index := range rows {
		var err error
		result[index], err = topupIntentFromRow(rows[index])
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func usageFromReserveRow(row dbgen.ReserveBillingUsageRow) (UsageCharge, error) {
	return usageFromValues(
		row.ID, row.ReservationOwner, row.UserID, row.ApiKeyPrefix,
		row.ChainID, row.Network, row.Asset, row.Method, row.Operation,
		row.ResourceDigest, row.AmountAtomic, row.State, row.FailureCode,
		row.ResponseDigest, row.ResponseBytes, row.ReservationExpiresAt,
		row.CommittedAt, row.ReleasedAt, row.ExpiredAt, row.CreatedAt, row.UpdatedAt,
	)
}

func usageFromCommitRow(row dbgen.CommitBillingUsageRow) (UsageCharge, error) {
	return usageFromValues(
		row.ID, row.ReservationOwner, row.UserID, row.ApiKeyPrefix,
		row.ChainID, row.Network, row.Asset, row.Method, row.Operation,
		row.ResourceDigest, row.AmountAtomic, row.State, row.FailureCode,
		row.ResponseDigest, row.ResponseBytes, row.ReservationExpiresAt,
		row.CommittedAt, row.ReleasedAt, row.ExpiredAt, row.CreatedAt, row.UpdatedAt,
	)
}

func usageFromReleaseRow(row dbgen.ReleaseBillingUsageRow) (UsageCharge, error) {
	return usageFromValues(
		row.ID, row.ReservationOwner, row.UserID, row.ApiKeyPrefix,
		row.ChainID, row.Network, row.Asset, row.Method, row.Operation,
		row.ResourceDigest, row.AmountAtomic, row.State, row.FailureCode,
		row.ResponseDigest, row.ResponseBytes, row.ReservationExpiresAt,
		row.CommittedAt, row.ReleasedAt, row.ExpiredAt, row.CreatedAt, row.UpdatedAt,
	)
}

func usageFromModel(row dbgen.BillingUsageCharge) (UsageCharge, error) {
	return usageFromValues(
		row.ID, row.ReservationOwner, row.UserID, row.ApiKeyPrefix,
		row.ChainID, row.Network, row.Asset, row.Method, row.Operation,
		row.ResourceDigest, row.AmountAtomic, row.State, row.FailureCode,
		row.ResponseDigest, row.ResponseBytes, row.ReservationExpiresAt,
		row.CommittedAt, row.ReleasedAt, row.ExpiredAt, row.CreatedAt, row.UpdatedAt,
	)
}

func usageFromValues(
	idValue, ownerValue, userValue pgtype.UUID,
	apiKey string,
	chainValue pgtype.Numeric,
	network string,
	assetBytes []byte,
	method, operation string,
	resourceBytes []byte,
	amountValue pgtype.Numeric,
	stateValue string,
	failure *string,
	responseBytes []byte,
	responseLength *int64,
	expiresValue, committedValue, releasedValue, expiredValue,
	createdValue, updatedValue pgtype.Timestamptz,
) (UsageCharge, error) {
	id, err := uuidString(idValue)
	if err != nil {
		return UsageCharge{}, ErrIntegrity
	}
	owner, err := uuidString(ownerValue)
	if err != nil {
		return UsageCharge{}, ErrIntegrity
	}
	userID, err := uuidString(userValue)
	if err != nil {
		return UsageCharge{}, ErrIntegrity
	}
	chainID, err := uint64Numeric(chainValue)
	if err != nil || !billingNetworkPattern.MatchString(network) || !validAPIKeyPrefix(apiKey) ||
		(method != http.MethodGet && method != http.MethodPost) || !validUsageOperation(operation) {
		return UsageCharge{}, ErrIntegrity
	}
	asset, err := addressFromBytes(assetBytes)
	if err != nil {
		return UsageCharge{}, ErrIntegrity
	}
	resource, err := digestFromBytes(resourceBytes)
	if err != nil {
		return UsageCharge{}, ErrIntegrity
	}
	amount, err := integerNumericString(amountValue)
	if err != nil {
		return UsageCharge{}, ErrIntegrity
	}
	state := UsageState(stateValue)
	if !state.valid() {
		return UsageCharge{}, ErrIntegrity
	}
	expiresAt, err := requiredTime(expiresValue)
	if err != nil {
		return UsageCharge{}, ErrIntegrity
	}
	createdAt, err := requiredTime(createdValue)
	if err != nil {
		return UsageCharge{}, ErrIntegrity
	}
	updatedAt, err := requiredTime(updatedValue)
	if err != nil {
		return UsageCharge{}, ErrIntegrity
	}
	result := UsageCharge{
		ID: id, Owner: owner, UserID: userID, APIKeyPrefix: apiKey,
		ChainID: chainID, Network: network, Asset: asset, Method: method,
		Operation: operation, ResourceDigest: resource, AmountAtomic: amount,
		State: state, FailureCode: cloneString(failure), ResponseBytes: responseLength,
		ReservationExpiresAt: expiresAt, CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
	if len(responseBytes) > 0 {
		value, valueErr := digestFromBytes(responseBytes)
		if valueErr != nil {
			return UsageCharge{}, ErrIntegrity
		}
		result.ResponseDigest = &value
	}
	result.CommittedAt, err = strictOptionalTime(committedValue)
	if err != nil {
		return UsageCharge{}, ErrIntegrity
	}
	result.ReleasedAt, err = strictOptionalTime(releasedValue)
	if err != nil {
		return UsageCharge{}, ErrIntegrity
	}
	result.ExpiredAt, err = strictOptionalTime(expiredValue)
	if err != nil {
		return UsageCharge{}, ErrIntegrity
	}
	return result, nil
}
