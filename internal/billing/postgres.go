package billing

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/apiops"
	dbaccess "github.com/islishude/etherview/internal/db"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	// HTTP pagination fetches one extra row to decide whether to emit a cursor.
	// The public limit remains 100.
	maximumBillingPage = 101
)

var (
	billingNetworkPattern = regexp.MustCompile(`^eip155:[1-9][0-9]*$`)
	billingCodePattern    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)
	maximumUint256        = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
)

type PostgresLedger struct {
	db             *sql.DB
	chainID        uint64
	chainNumeric   pgtype.Numeric
	reservationTTL time.Duration
}

func NewPostgresLedger(
	db *sql.DB,
	chainID uint64,
	reservationTTL time.Duration,
) (*PostgresLedger, error) {
	if db == nil {
		return nil, errors.New("billing database is nil")
	}
	if chainID == 0 {
		return nil, errors.New("billing chain ID is zero")
	}
	if reservationTTL < 10*time.Second || reservationTTL > 10*time.Minute {
		return nil, errors.New("billing reservation TTL must be between 10s and 10m")
	}
	return &PostgresLedger{
		db: db, chainID: chainID,
		chainNumeric: pgtype.Numeric{
			Int: new(big.Int).SetUint64(chainID), Valid: true,
		},
		reservationTTL: reservationTTL,
	}, nil
}

func (ledger *PostgresLedger) Reserve(
	ctx context.Context,
	input ReserveInput,
) (Reservation, error) {
	input = normalizeReserveInput(input)
	if err := validateReserveInput(input); err != nil {
		return Reservation{}, err
	}
	paymentID, err := uuid.NewRandom()
	if err != nil {
		return Reservation{}, errors.New("generate billing payment ID")
	}
	owner, err := uuid.NewRandom()
	if err != nil {
		return Reservation{}, errors.New("generate billing reservation owner")
	}
	amount := new(big.Int)
	amount.SetString(input.AmountAtomic, 10)
	topupIntentID, err := optionalUUID(input.TopupIntentID)
	if err != nil {
		return Reservation{}, err
	}
	createdAt := input.ObservedAt.UTC()
	params := dbgen.InsertBillingPaymentParams{
		ID:                   uuidValue(paymentID),
		ChainID:              ledger.chainNumeric,
		Fingerprint:          input.Fingerprint[:],
		ReservationOwner:     uuidValue(owner),
		Method:               input.Method,
		Operation:            input.Operation,
		ResourceDigest:       input.ResourceDigest[:],
		RequirementDigest:    input.RequirementDigest[:],
		Network:              input.Network,
		Asset:                input.Asset[:],
		AmountAtomic:         pgtype.Numeric{Int: amount, Valid: true},
		Recipient:            input.Recipient[:],
		ApiKeyPrefix:         cloneString(input.APIKeyPrefix),
		FacilitatorDigest:    input.FacilitatorDigest[:],
		Purpose:              input.Purpose,
		AssetTransferMethod:  input.AssetTransferMethod,
		PaymentFlow:          input.PaymentFlow,
		FingerprintVersion:   input.FingerprintVersion,
		TopupIntentID:        topupIntentID,
		ReservationExpiresAt: postgresTime(createdAt.Add(ledger.reservationTTL)),
		CreatedAt:            postgresTime(createdAt),
	}
	var row dbgen.BillingPayment
	owned := false
	err = dbaccess.WithTransaction(ctx, ledger.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.InsertBillingPayment(ctx, params)
		if errors.Is(queryErr, pgx.ErrNoRows) {
			row, queryErr = queries.GetBillingPaymentByFingerprint(ctx, input.Fingerprint[:])
			return queryErr
		}
		if queryErr != nil {
			return queryErr
		}
		owned = true
		if input.Purpose == "account_topup" {
			intentID, parseErr := parseUUID(*input.TopupIntentID)
			if parseErr != nil {
				return parseErr
			}
			userID, parseErr := parseUUID(*input.UserID)
			if parseErr != nil {
				return parseErr
			}
			if _, claimErr := queries.ClaimBillingTopupIntent(ctx, dbgen.ClaimBillingTopupIntentParams{
				PaymentID: row.ID, TransitionedAt: postgresTime(createdAt),
				ID: intentID, UserID: userID, ChainID: ledger.chainNumeric,
				Payer: input.ExpectedPayer[:],
			}); claimErr != nil {
				return claimErr
			}
		}
		return queries.AppendBillingPaymentEvent(ctx, dbgen.AppendBillingPaymentEventParams{
			PaymentID: row.ID, ToState: string(StateReserved),
			Code: "payment_reserved", Actor: string(ActorRuntime),
			OccurredAt: postgresTime(createdAt),
		})
	})
	if err != nil {
		return Reservation{}, fmt.Errorf("reserve billing payment: %w", err)
	}
	payment, err := ledger.paymentFromRow(row)
	if err != nil || !sameReservation(payment, input) {
		return Reservation{}, ErrIntegrity
	}
	reservation := Reservation{Payment: payment, Owned: owned}
	if owned {
		reservation.Owner = owner.String()
	}
	return reservation, nil
}

func validateReserveInput(input ReserveInput) error {
	input = normalizeReserveInput(input)
	switch input.Purpose {
	case "legacy_request":
		operation, ok := apiops.Lookup(input.Operation)
		if !ok || !operation.BillingEligible || input.Method != http.MethodGet ||
			input.TopupIntentID != nil {
			return fmt.Errorf("%w: legacy operation is not billing eligible", ErrInvalidInput)
		}
	case "account_topup":
		if input.Operation != "createBillingTopup" || input.Method != http.MethodPost ||
			input.TopupIntentID == nil || input.UserID == nil ||
			input.ExpectedPayer == (common.Address{}) || input.APIKeyPrefix != nil {
			return fmt.Errorf("%w: top-up payment binding is invalid", ErrInvalidInput)
		}
	default:
		return fmt.Errorf("%w: payment purpose is invalid", ErrInvalidInput)
	}
	if input.AssetTransferMethod != "eip3009" && input.AssetTransferMethod != "permit2" ||
		input.PaymentFlow != "authorization" ||
		(input.FingerprintVersion != 1 && input.FingerprintVersion != 2) {
		return fmt.Errorf("%w: payment mechanism is invalid", ErrInvalidInput)
	}
	if !billingNetworkPattern.MatchString(input.Network) {
		return fmt.Errorf("%w: network is not canonical", ErrInvalidInput)
	}
	if input.ObservedAt.IsZero() {
		return fmt.Errorf("%w: observed time is required", ErrInvalidInput)
	}
	amount, ok := new(big.Int).SetString(input.AmountAtomic, 10)
	if !ok || amount.Sign() <= 0 || amount.Cmp(maximumUint256) > 0 ||
		amount.String() != input.AmountAtomic {
		return fmt.Errorf("%w: amount is not a canonical uint256", ErrInvalidInput)
	}
	if input.APIKeyPrefix != nil && !validAPIKeyPrefix(*input.APIKeyPrefix) {
		return fmt.Errorf("%w: API key attribution is invalid", ErrInvalidInput)
	}
	return nil
}

func sameReservation(payment Payment, input ReserveInput) bool {
	return payment.ChainID != 0 &&
		payment.Operation == input.Operation &&
		payment.Method == input.Method && payment.Purpose == input.Purpose &&
		payment.AssetTransferMethod == input.AssetTransferMethod &&
		payment.PaymentFlow == input.PaymentFlow &&
		payment.FingerprintVersion == input.FingerprintVersion &&
		optionalStringsEqual(payment.TopupIntentID, input.TopupIntentID) &&
		payment.Network == input.Network &&
		payment.AmountAtomic == input.AmountAtomic &&
		payment.Asset == input.Asset &&
		payment.Recipient == input.Recipient &&
		digestEqual(payment.fingerprint, input.Fingerprint) &&
		digestEqual(payment.resourceDigest, input.ResourceDigest) &&
		digestEqual(payment.requirementDigest, input.RequirementDigest) &&
		digestEqual(payment.facilitatorDigest, input.FacilitatorDigest)
}

func normalizeReserveInput(input ReserveInput) ReserveInput {
	if input.Method == "" {
		input.Method = http.MethodGet
	}
	if input.Purpose == "" {
		input.Purpose = "legacy_request"
	}
	if input.AssetTransferMethod == "" {
		input.AssetTransferMethod = "eip3009"
	}
	if input.PaymentFlow == "" {
		input.PaymentFlow = "authorization"
	}
	if input.FingerprintVersion == 0 {
		input.FingerprintVersion = 1
	}
	return input
}

func optionalStringsEqual(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func digestEqual(left, right Digest) bool {
	return subtle.ConstantTimeCompare(left[:], right[:]) == 1
}

func (ledger *PostgresLedger) Get(
	ctx context.Context,
	paymentID string,
) (Payment, error) {
	id, err := parseUUID(paymentID)
	if err != nil {
		return Payment{}, ErrNotFound
	}
	var row dbgen.BillingPayment
	err = dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.GetBillingPaymentByID(ctx, id, ledger.chainNumeric)
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, ErrNotFound
	}
	if err != nil {
		return Payment{}, fmt.Errorf("read billing payment: %w", err)
	}
	return ledger.paymentFromRow(row)
}

func (ledger *PostgresLedger) Inspect(
	ctx context.Context,
	paymentID string,
) (Inspection, error) {
	id, err := parseUUID(paymentID)
	if err != nil {
		return Inspection{}, ErrNotFound
	}
	var (
		row       dbgen.BillingPayment
		eventRows []dbgen.BillingPaymentEvent
	)
	err = dbaccess.WithTransaction(ctx, ledger.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.GetBillingPaymentForInspection(
			ctx, id, ledger.chainNumeric,
		)
		if queryErr != nil {
			return queryErr
		}
		eventRows, queryErr = queries.ListBillingPaymentEvents(ctx, id)
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Inspection{}, ErrNotFound
	}
	if err != nil {
		return Inspection{}, fmt.Errorf("inspect billing payment: %w", err)
	}
	payment, err := ledger.paymentFromRow(row)
	if err != nil {
		return Inspection{}, err
	}
	events, err := ledger.eventsFromRows(payment, eventRows)
	if err != nil {
		return Inspection{}, err
	}
	return Inspection{Payment: payment, Events: events}, nil
}

func (ledger *PostgresLedger) MarkVerified(
	ctx context.Context,
	input VerifiedInput,
) (Payment, error) {
	id, owner, err := transitionIDs(input.PaymentID, input.Owner)
	if err != nil || input.ObservedAt.IsZero() {
		return Payment{}, ErrInvalidInput
	}
	userID, err := optionalUUID(input.UserID)
	if err != nil {
		return Payment{}, err
	}
	if input.APIKeyPrefix != nil && !validAPIKeyPrefix(*input.APIKeyPrefix) {
		return Payment{}, ErrInvalidInput
	}
	return ledger.transition(ctx, id, func(queries *dbgen.Queries) error {
		_, queryErr := queries.MarkBillingPaymentVerified(
			ctx,
			dbgen.MarkBillingPaymentVerifiedParams{
				Payer: input.Payer[:], UserID: userID,
				ApiKeyPrefix:   cloneString(input.APIKeyPrefix),
				TransitionedAt: postgresTime(input.ObservedAt),
				ID:             id, ReservationOwner: owner,
			},
		)
		return queryErr
	})
}

func (ledger *PostgresLedger) StartHandler(
	ctx context.Context,
	paymentID string,
	owner string,
	observedAt time.Time,
) (Payment, error) {
	id, fence, err := transitionIDs(paymentID, owner)
	if err != nil || observedAt.IsZero() {
		return Payment{}, ErrInvalidInput
	}
	return ledger.transition(ctx, id, func(queries *dbgen.Queries) error {
		_, queryErr := queries.StartBillingPaymentHandler(
			ctx, postgresTime(observedAt), id, fence,
		)
		return queryErr
	})
}

func (ledger *PostgresLedger) BeginSettlement(
	ctx context.Context,
	paymentID string,
	owner string,
	observedAt time.Time,
) (Payment, error) {
	id, fence, err := transitionIDs(paymentID, owner)
	if err != nil || observedAt.IsZero() {
		return Payment{}, ErrInvalidInput
	}
	return ledger.transition(ctx, id, func(queries *dbgen.Queries) error {
		_, queryErr := queries.MarkBillingPaymentSettling(
			ctx, postgresTime(observedAt), id, fence,
		)
		return queryErr
	})
}

func (ledger *PostgresLedger) MarkSettlementUnknown(
	ctx context.Context,
	paymentID string,
	owner string,
	observedAt time.Time,
) (Payment, error) {
	id, fence, err := transitionIDs(paymentID, owner)
	if err != nil || observedAt.IsZero() {
		return Payment{}, ErrInvalidInput
	}
	return ledger.transition(ctx, id, func(queries *dbgen.Queries) error {
		_, queryErr := queries.MarkBillingPaymentSettlementUnknown(
			ctx, postgresTime(observedAt), id, fence,
		)
		return queryErr
	})
}

func (ledger *PostgresLedger) MarkSettlementPending(
	ctx context.Context,
	paymentID string,
	owner string,
	transactionHash common.Hash,
	observedAt time.Time,
) (Payment, error) {
	if zeroTransactionHash(transactionHash) {
		return Payment{}, ErrInvalidInput
	}
	id, fence, err := transitionIDs(paymentID, owner)
	if err != nil || observedAt.IsZero() {
		return Payment{}, ErrInvalidInput
	}
	return ledger.transition(ctx, id, func(queries *dbgen.Queries) error {
		_, queryErr := queries.MarkBillingPaymentSettlementPending(
			ctx,
			dbgen.MarkBillingPaymentSettlementPendingParams{
				TransactionHash: transactionHash[:], TransitionedAt: postgresTime(observedAt),
				ID: id, ReservationOwner: fence,
			},
		)
		return queryErr
	})
}

func (ledger *PostgresLedger) MarkSettled(
	ctx context.Context,
	paymentID string,
	owner string,
	transactionHash common.Hash,
	observedAt time.Time,
) (Payment, error) {
	if zeroTransactionHash(transactionHash) {
		return Payment{}, ErrInvalidInput
	}
	id, fence, err := transitionIDs(paymentID, owner)
	if err != nil || observedAt.IsZero() {
		return Payment{}, ErrInvalidInput
	}
	return ledger.markSettled(ctx, id, fence, transactionHash, observedAt)
}

func (ledger *PostgresLedger) MarkFailed(
	ctx context.Context,
	paymentID string,
	owner string,
	code string,
	observedAt time.Time,
) (Payment, error) {
	if !validFailureCode(code) || code == "settlement_unknown" ||
		code == "reservation_expired" || strings.HasPrefix(code, "operator_") {
		return Payment{}, ErrInvalidInput
	}
	id, fence, err := transitionIDs(paymentID, owner)
	if err != nil || observedAt.IsZero() {
		return Payment{}, ErrInvalidInput
	}
	return ledger.markFailed(ctx, id, fence, code, observedAt)
}

func (ledger *PostgresLedger) ReconcileSettled(
	ctx context.Context,
	paymentID string,
	transactionHash common.Hash,
	observedAt time.Time,
) (Payment, error) {
	if zeroTransactionHash(transactionHash) || observedAt.IsZero() {
		return Payment{}, ErrInvalidInput
	}
	id, err := parseUUID(paymentID)
	if err != nil {
		return Payment{}, ErrNotFound
	}
	staleBefore := observedAt.UTC().Add(-SettlementCrashReconcileDelay)
	return ledger.transition(ctx, id, func(queries *dbgen.Queries) error {
		_, queryErr := queries.ReconcileBillingPaymentSettled(
			ctx,
			dbgen.ReconcileBillingPaymentSettledParams{
				TransactionHash: transactionHash[:],
				ChainID:         ledger.chainNumeric,
				TransitionedAt:  postgresTime(observedAt),
				StaleBefore:     postgresTime(staleBefore),
				ID:              id,
			},
		)
		return queryErr
	})
}

func (ledger *PostgresLedger) ReconcileFailed(
	ctx context.Context,
	paymentID string,
	observedAt time.Time,
) (Payment, error) {
	if observedAt.IsZero() {
		return Payment{}, ErrInvalidInput
	}
	id, err := parseUUID(paymentID)
	if err != nil {
		return Payment{}, ErrNotFound
	}
	staleBefore := observedAt.UTC().Add(-SettlementCrashReconcileDelay)
	return ledger.transition(ctx, id, func(queries *dbgen.Queries) error {
		_, queryErr := queries.ReconcileBillingPaymentFailed(
			ctx,
			dbgen.ReconcileBillingPaymentFailedParams{
				ID: id, ChainID: ledger.chainNumeric,
				TransitionedAt: postgresTime(observedAt),
				StaleBefore:    postgresTime(staleBefore),
			},
		)
		return queryErr
	})
}

func (ledger *PostgresLedger) markSettled(
	ctx context.Context,
	id pgtype.UUID,
	owner pgtype.UUID,
	transactionHash common.Hash,
	observedAt time.Time,
) (Payment, error) {
	return ledger.transition(ctx, id, func(queries *dbgen.Queries) error {
		_, queryErr := queries.MarkBillingPaymentSettled(
			ctx,
			dbgen.MarkBillingPaymentSettledParams{
				TransactionHash: transactionHash[:],
				TransitionedAt:  postgresTime(observedAt),
				ID:              id, ReservationOwner: owner,
			},
		)
		return queryErr
	})
}

func (ledger *PostgresLedger) markFailed(
	ctx context.Context,
	id pgtype.UUID,
	owner pgtype.UUID,
	code string,
	observedAt time.Time,
) (Payment, error) {
	failureCode := code
	return ledger.transition(ctx, id, func(queries *dbgen.Queries) error {
		_, queryErr := queries.MarkBillingPaymentFailed(
			ctx,
			dbgen.MarkBillingPaymentFailedParams{
				FailureCode:    &failureCode,
				TransitionedAt: postgresTime(observedAt),
				ID:             id, ReservationOwner: owner,
			},
		)
		return queryErr
	})
}

func (ledger *PostgresLedger) transition(
	ctx context.Context,
	id pgtype.UUID,
	update func(*dbgen.Queries) error,
) (Payment, error) {
	var row dbgen.BillingPayment
	err := dbaccess.WithTransaction(ctx, ledger.db, func(queries *dbgen.Queries) error {
		if err := update(queries); err != nil {
			return err
		}
		var queryErr error
		row, queryErr = queries.GetBillingPaymentByID(ctx, id, ledger.chainNumeric)
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, ErrStateConflict
	}
	if err != nil {
		return Payment{}, fmt.Errorf("transition billing payment: %w", err)
	}
	return ledger.paymentFromRow(row)
}

func (ledger *PostgresLedger) Expire(
	ctx context.Context,
	observedAt time.Time,
	limit int,
) (uint64, error) {
	if observedAt.IsZero() || limit < 1 || limit > 1000 {
		return 0, ErrInvalidInput
	}
	var count int64
	err := dbaccess.WithTransaction(ctx, ledger.db, func(queries *dbgen.Queries) error {
		var queryErr error
		count, queryErr = queries.ExpireBillingPayments(
			ctx,
			ledger.chainNumeric,
			postgresTime(observedAt),
			int32(limit),
		)
		return queryErr
	})
	if err != nil {
		return 0, fmt.Errorf("expire billing reservations: %w", err)
	}
	if count < 0 {
		return 0, ErrIntegrity
	}
	return uint64(count), nil
}

func (ledger *PostgresLedger) ListUser(
	ctx context.Context,
	userID string,
	after *PageAfter,
	limit int,
) ([]Payment, error) {
	id, err := parseUUID(userID)
	if err != nil || limit < 1 || limit > maximumBillingPage {
		return nil, ErrInvalidInput
	}
	params := dbgen.ListUserBillingPaymentsParams{
		ChainID: ledger.chainNumeric, UserID: id, PageLimit: int32(limit),
	}
	if err := applyPageAfter(after, &params.BeforeCreatedAt, &params.BeforeID); err != nil {
		return nil, err
	}
	var rows []dbgen.BillingPayment
	err = dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		var queryErr error
		rows, queryErr = queries.ListUserBillingPayments(ctx, params)
		return queryErr
	})
	if err != nil {
		return nil, fmt.Errorf("list user billing payments: %w", err)
	}
	return ledger.paymentsFromRows(rows)
}

func (ledger *PostgresLedger) ListAdmin(
	ctx context.Context,
	filter AdminFilter,
	after *PageAfter,
	limit int,
) ([]Payment, error) {
	if limit < 1 || limit > maximumBillingPage {
		return nil, ErrInvalidInput
	}
	params, err := ledger.adminParams(filter)
	if err != nil {
		return nil, err
	}
	params.PageLimit = int32(limit)
	if err := applyPageAfter(after, &params.BeforeCreatedAt, &params.BeforeID); err != nil {
		return nil, err
	}
	var rows []dbgen.BillingPayment
	err = dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		var queryErr error
		rows, queryErr = queries.ListAdminBillingPayments(ctx, params)
		return queryErr
	})
	if err != nil {
		return nil, fmt.Errorf("list administrator billing payments: %w", err)
	}
	return ledger.paymentsFromRows(rows)
}

func (ledger *PostgresLedger) Summary(
	ctx context.Context,
	filter AdminFilter,
) ([]SummaryRow, error) {
	if filter.FromTime == nil || filter.ToTime == nil ||
		!filter.ToTime.After(*filter.FromTime) ||
		filter.ToTime.Sub(*filter.FromTime) > 31*24*time.Hour {
		return nil, ErrInvalidInput
	}
	params, err := ledger.adminParams(filter)
	if err != nil {
		return nil, err
	}
	rows := []dbgen.SummarizeBillingPaymentsRow{}
	err = dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		var queryErr error
		rows, queryErr = queries.SummarizeBillingPayments(
			ctx,
			dbgen.SummarizeBillingPaymentsParams{
				ChainID:  ledger.chainNumeric,
				FromTime: params.FromTime, ToTime: params.ToTime,
				State: params.State, Operation: params.Operation,
				Network: params.Network, Asset: params.Asset,
			},
		)
		return queryErr
	})
	if err != nil {
		return nil, fmt.Errorf("summarize billing payments: %w", err)
	}
	result := make([]SummaryRow, 0, len(rows))
	for _, row := range rows {
		state := State(row.State)
		asset, assetErr := addressFromBytes(row.Asset)
		count, countErr := integerNumericString(row.PaymentCount)
		amount, amountErr := integerNumericString(row.AmountAtomic)
		operation, operationOK := apiops.Lookup(row.Operation)
		operationEligible := row.Operation == "createBillingTopup" ||
			operationOK && operation.BillingEligible
		countInteger, countOK := new(big.Int).SetString(count, 10)
		amountInteger, amountOK := new(big.Int).SetString(amount, 10)
		if !state.valid() || assetErr != nil || countErr != nil || amountErr != nil ||
			!operationEligible ||
			!billingNetworkPattern.MatchString(row.Network) || len(row.Network) > 96 ||
			!countOK || countInteger.Sign() <= 0 ||
			!amountOK || amountInteger.Sign() <= 0 {
			return nil, ErrIntegrity
		}
		result = append(result, SummaryRow{
			State: state, Operation: row.Operation, Network: row.Network,
			Asset: asset, PaymentCount: count, AmountAtomic: amount,
		})
	}
	return result, nil
}

func (ledger *PostgresLedger) adminParams(
	filter AdminFilter,
) (dbgen.ListAdminBillingPaymentsParams, error) {
	params := dbgen.ListAdminBillingPaymentsParams{ChainID: ledger.chainNumeric}
	if filter.State != nil {
		if !filter.State.valid() {
			return params, ErrInvalidInput
		}
		value := string(*filter.State)
		params.State = &value
	}
	params.Operation = cloneString(filter.Operation)
	if params.Operation != nil {
		operation, ok := apiops.Lookup(*params.Operation)
		if *params.Operation != "createBillingTopup" && (!ok || !operation.BillingEligible) {
			return params, ErrInvalidInput
		}
	}
	params.Network = cloneString(filter.Network)
	if params.Network != nil &&
		(!billingNetworkPattern.MatchString(*params.Network) ||
			len(*params.Network) > 96) {
		return params, ErrInvalidInput
	}
	if filter.Asset != nil {
		params.Asset = append([]byte(nil), filter.Asset[:]...)
	}
	if filter.FromTime != nil {
		if filter.FromTime.IsZero() {
			return params, ErrInvalidInput
		}
		params.FromTime = postgresTime(*filter.FromTime)
	}
	if filter.ToTime != nil {
		if filter.ToTime.IsZero() {
			return params, ErrInvalidInput
		}
		params.ToTime = postgresTime(*filter.ToTime)
	}
	if filter.FromTime != nil && filter.ToTime != nil &&
		!filter.ToTime.After(*filter.FromTime) {
		return params, ErrInvalidInput
	}
	return params, nil
}

func applyPageAfter(
	after *PageAfter,
	createdAt *pgtype.Timestamptz,
	id *pgtype.UUID,
) error {
	if after == nil {
		return nil
	}
	parsed, err := parseUUID(after.ID)
	if err != nil || after.CreatedAt.IsZero() {
		return ErrInvalidInput
	}
	*createdAt = postgresTime(after.CreatedAt)
	*id = parsed
	return nil
}

func (ledger *PostgresLedger) paymentsFromRows(
	rows []dbgen.BillingPayment,
) ([]Payment, error) {
	result := make([]Payment, 0, len(rows))
	for _, row := range rows {
		payment, err := ledger.paymentFromRow(row)
		if err != nil {
			return nil, err
		}
		result = append(result, payment)
	}
	return result, nil
}

func (ledger *PostgresLedger) paymentFromRow(row dbgen.BillingPayment) (Payment, error) {
	id, err := uuidString(row.ID)
	if err != nil {
		return Payment{}, err
	}
	if id == uuid.Nil.String() {
		return Payment{}, ErrIntegrity
	}
	owner, err := uuidString(row.ReservationOwner)
	if err != nil || owner == uuid.Nil.String() {
		return Payment{}, ErrIntegrity
	}
	chainID, err := uint64Numeric(row.ChainID)
	if err != nil || chainID != ledger.chainID {
		return Payment{}, ErrIntegrity
	}
	state := State(row.State)
	amount, err := integerNumericString(row.AmountAtomic)
	amountInteger, amountOK := new(big.Int).SetString(amount, 10)
	if err != nil || !state.valid() ||
		row.ProtocolVersion != 2 || row.Scheme != "exact" ||
		!billingNetworkPattern.MatchString(row.Network) || len(row.Network) > 96 ||
		!validPaymentOperation(row.Purpose, row.Method, row.Operation) || !amountOK ||
		amountInteger.Sign() <= 0 || amountInteger.Cmp(maximumUint256) > 0 {
		return Payment{}, ErrIntegrity
	}
	asset, err := addressFromBytes(row.Asset)
	if err != nil {
		return Payment{}, ErrIntegrity
	}
	recipient, err := addressFromBytes(row.Recipient)
	if err != nil {
		return Payment{}, ErrIntegrity
	}
	fingerprint, err := digestFromBytes(row.Fingerprint)
	if err != nil {
		return Payment{}, ErrIntegrity
	}
	resourceDigest, err := digestFromBytes(row.ResourceDigest)
	if err != nil {
		return Payment{}, ErrIntegrity
	}
	requirementDigest, err := digestFromBytes(row.RequirementDigest)
	if err != nil {
		return Payment{}, ErrIntegrity
	}
	facilitatorDigest, err := digestFromBytes(row.FacilitatorDigest)
	if err != nil {
		return Payment{}, ErrIntegrity
	}
	createdAt, err := requiredTime(row.CreatedAt)
	if err != nil {
		return Payment{}, ErrIntegrity
	}
	updatedAt, err := requiredTime(row.UpdatedAt)
	if err != nil {
		return Payment{}, ErrIntegrity
	}
	expiresAt, err := requiredTime(row.ReservationExpiresAt)
	if err != nil {
		return Payment{}, ErrIntegrity
	}
	handlerStartedAt, err := strictOptionalTime(row.HandlerStartedAt)
	if err != nil {
		return Payment{}, ErrIntegrity
	}
	verifiedAt, err := strictOptionalTime(row.VerifiedAt)
	if err != nil {
		return Payment{}, ErrIntegrity
	}
	settlingAt, err := strictOptionalTime(row.SettlingAt)
	if err != nil {
		return Payment{}, ErrIntegrity
	}
	settledAt, err := strictOptionalTime(row.SettledAt)
	if err != nil {
		return Payment{}, ErrIntegrity
	}
	failedAt, err := strictOptionalTime(row.FailedAt)
	if err != nil {
		return Payment{}, ErrIntegrity
	}
	expiredAt, err := strictOptionalTime(row.ExpiredAt)
	if err != nil {
		return Payment{}, ErrIntegrity
	}
	if !expiresAt.After(createdAt) || updatedAt.Before(createdAt) {
		return Payment{}, ErrIntegrity
	}
	payment := Payment{
		ID: id, ChainID: chainID, Operation: row.Operation, Method: row.Method,
		Purpose: row.Purpose, AssetTransferMethod: row.AssetTransferMethod,
		PaymentFlow: row.PaymentFlow, FingerprintVersion: row.FingerprintVersion,
		Network: row.Network, Asset: asset, AmountAtomic: amount,
		Recipient: recipient, State: state, FailureCode: cloneString(row.FailureCode),
		APIKeyPrefix:         cloneString(row.ApiKeyPrefix),
		ReservationExpiresAt: expiresAt,
		HandlerStartedAt:     handlerStartedAt,
		VerifiedAt:           verifiedAt, SettlingAt: settlingAt,
		SettledAt: settledAt, FailedAt: failedAt,
		ExpiredAt: expiredAt, CreatedAt: createdAt, UpdatedAt: updatedAt,
		fingerprint: fingerprint, resourceDigest: resourceDigest,
		requirementDigest: requirementDigest, facilitatorDigest: facilitatorDigest,
	}
	if row.TopupIntentID.Valid {
		value, intentErr := uuidString(row.TopupIntentID)
		if intentErr != nil {
			return Payment{}, ErrIntegrity
		}
		payment.TopupIntentID = &value
	}
	if len(row.Payer) != 0 {
		value, payerErr := addressFromBytes(row.Payer)
		if payerErr != nil {
			return Payment{}, ErrIntegrity
		}
		payment.Payer = &value
	}
	if row.UserID.Valid {
		value, userErr := uuidString(row.UserID)
		if userErr != nil || value == uuid.Nil.String() {
			return Payment{}, ErrIntegrity
		}
		payment.UserID = &value
	}
	if len(row.TransactionHash) != 0 {
		value, hashErr := transactionHashFromBytes(row.TransactionHash)
		if hashErr != nil || zeroTransactionHash(value) {
			return Payment{}, ErrIntegrity
		}
		payment.TransactionHash = &value
	}
	if payment.APIKeyPrefix != nil && !validAPIKeyPrefix(*payment.APIKeyPrefix) {
		return Payment{}, ErrIntegrity
	}
	if payment.FailureCode != nil && !validFailureCode(*payment.FailureCode) {
		return Payment{}, ErrIntegrity
	}
	if (payment.Purpose != "legacy_request" && payment.Purpose != "account_topup") ||
		(payment.AssetTransferMethod != "eip3009" && payment.AssetTransferMethod != "permit2") ||
		payment.PaymentFlow != "authorization" ||
		(payment.FingerprintVersion != 1 && payment.FingerprintVersion != 2) ||
		(payment.Purpose == "legacy_request") != (payment.TopupIntentID == nil) ||
		(payment.Purpose == "account_topup" && payment.Method != http.MethodPost) {
		return Payment{}, ErrIntegrity
	}
	if !validPaymentStateFacts(payment) {
		return Payment{}, ErrIntegrity
	}
	return payment, nil
}

func validPaymentOperation(purpose, method, operation string) bool {
	if purpose == "account_topup" {
		return method == http.MethodPost && operation == "createBillingTopup"
	}
	if purpose != "legacy_request" || method != http.MethodGet {
		return false
	}
	metadata, ok := apiops.Lookup(operation)
	return ok && metadata.BillingEligible && metadata.Method == method
}

func (ledger *PostgresLedger) eventsFromRows(
	payment Payment,
	rows []dbgen.BillingPaymentEvent,
) ([]PaymentEvent, error) {
	if len(rows) == 0 {
		return nil, ErrIntegrity
	}
	result := make([]PaymentEvent, 0, len(rows))
	var (
		currentState *State
		lastID       int64
		lastTime     time.Time
	)
	for index, row := range rows {
		paymentID, err := uuidString(row.PaymentID)
		if err != nil || paymentID != payment.ID || row.ID <= 0 ||
			(index > 0 && row.ID <= lastID) {
			return nil, ErrIntegrity
		}
		toState := State(row.ToState)
		if !toState.valid() || !validFailureCode(row.Code) {
			return nil, ErrIntegrity
		}
		actor := Actor(row.Actor)
		if actor != ActorRuntime && actor != ActorOperator {
			return nil, ErrIntegrity
		}
		occurredAt, err := requiredTime(row.OccurredAt)
		if err != nil || occurredAt.Before(payment.CreatedAt) ||
			occurredAt.After(payment.UpdatedAt) ||
			(index > 0 && occurredAt.Before(lastTime)) {
			return nil, ErrIntegrity
		}
		var fromState *State
		if row.FromState != nil {
			value := State(*row.FromState)
			if !value.valid() {
				return nil, ErrIntegrity
			}
			fromState = &value
		}
		if !validEventTransition(currentState, fromState, toState) {
			return nil, ErrIntegrity
		}
		var transactionHash *common.Hash
		if len(row.TransactionHash) != 0 {
			value, hashErr := transactionHashFromBytes(row.TransactionHash)
			if hashErr != nil || zeroTransactionHash(value) {
				return nil, ErrIntegrity
			}
			transactionHash = &value
		}
		if (toState == StateSettled && transactionHash == nil) ||
			(toState != StateSettled && toState != StateSettling && transactionHash != nil) ||
			(actor == ActorOperator) != strings.HasPrefix(row.Code, "operator_") {
			return nil, ErrIntegrity
		}
		event := PaymentEvent{
			ID: row.ID, PaymentID: paymentID, FromState: fromState,
			ToState: toState, Code: row.Code, Actor: actor,
			TransactionHash: transactionHash, OccurredAt: occurredAt,
		}
		result = append(result, event)
		state := toState
		currentState = &state
		lastID = row.ID
		lastTime = occurredAt
	}
	if currentState == nil || *currentState != payment.State {
		return nil, ErrIntegrity
	}
	return result, nil
}

func validEventTransition(
	currentState *State,
	fromState *State,
	toState State,
) bool {
	if currentState == nil {
		return fromState == nil && toState == StateReserved
	}
	if fromState == nil || *fromState != *currentState {
		return false
	}
	switch *fromState {
	case StateReserved:
		return toState == StateVerified || toState == StateFailed ||
			toState == StateExpired
	case StateVerified:
		return toState == StateVerified || toState == StateSettling ||
			toState == StateFailed || toState == StateExpired
	case StateSettling:
		return toState == StateSettling || toState == StateSettled ||
			toState == StateFailed
	default:
		return false
	}
}

func validPaymentStateFacts(payment Payment) bool {
	if (payment.Payer != nil) != (payment.VerifiedAt != nil) ||
		(payment.UserID != nil && payment.Payer == nil) ||
		(payment.HandlerStartedAt != nil && payment.VerifiedAt == nil) ||
		(payment.SettlingAt != nil && payment.HandlerStartedAt == nil) {
		return false
	}
	times := []*time.Time{
		payment.VerifiedAt, payment.HandlerStartedAt, payment.SettlingAt,
		payment.SettledAt, payment.FailedAt, payment.ExpiredAt,
	}
	for _, value := range times {
		if value != nil && (value.Before(payment.CreatedAt) ||
			value.After(payment.UpdatedAt)) {
			return false
		}
	}
	latest := payment.CreatedAt
	for _, value := range []*time.Time{
		payment.VerifiedAt, payment.HandlerStartedAt, payment.SettlingAt,
	} {
		if value == nil {
			continue
		}
		if value.Before(latest) {
			return false
		}
		latest = *value
	}
	for _, value := range []*time.Time{
		payment.SettledAt, payment.FailedAt, payment.ExpiredAt,
	} {
		if value != nil && value.Before(latest) {
			return false
		}
	}
	switch payment.State {
	case StateReserved:
		return payment.Payer == nil && payment.UserID == nil &&
			payment.HandlerStartedAt == nil && payment.VerifiedAt == nil &&
			payment.SettlingAt == nil && payment.SettledAt == nil &&
			payment.FailedAt == nil && payment.ExpiredAt == nil &&
			payment.TransactionHash == nil && payment.FailureCode == nil
	case StateVerified:
		return payment.Payer != nil && payment.VerifiedAt != nil &&
			payment.SettlingAt == nil && payment.SettledAt == nil &&
			payment.FailedAt == nil && payment.ExpiredAt == nil &&
			payment.TransactionHash == nil && payment.FailureCode == nil
	case StateSettling:
		return payment.Payer != nil && payment.VerifiedAt != nil &&
			payment.HandlerStartedAt != nil && payment.SettlingAt != nil &&
			payment.SettledAt == nil && payment.FailedAt == nil &&
			payment.ExpiredAt == nil &&
			(payment.FailureCode == nil && payment.TransactionHash == nil ||
				payment.FailureCode != nil && *payment.FailureCode == "settlement_unknown" && payment.TransactionHash == nil ||
				payment.FailureCode != nil && *payment.FailureCode == "settlement_pending" && payment.TransactionHash != nil)
	case StateSettled:
		return payment.Payer != nil && payment.VerifiedAt != nil &&
			payment.HandlerStartedAt != nil && payment.SettlingAt != nil &&
			payment.SettledAt != nil && payment.FailedAt == nil &&
			payment.ExpiredAt == nil && payment.TransactionHash != nil &&
			payment.FailureCode == nil &&
			!payment.SettledAt.Before(*payment.SettlingAt)
	case StateFailed:
		return payment.SettledAt == nil && payment.FailedAt != nil &&
			payment.ExpiredAt == nil &&
			payment.FailureCode != nil &&
			*payment.FailureCode != "settlement_unknown" &&
			*payment.FailureCode != "reservation_expired" &&
			(payment.SettlingAt == nil ||
				!payment.FailedAt.Before(*payment.SettlingAt))
	case StateExpired:
		return payment.SettlingAt == nil && payment.SettledAt == nil &&
			payment.FailedAt == nil && payment.ExpiredAt != nil &&
			payment.TransactionHash == nil && payment.FailureCode != nil &&
			*payment.FailureCode == "reservation_expired"
	default:
		return false
	}
}

func transitionIDs(paymentID, owner string) (pgtype.UUID, pgtype.UUID, error) {
	id, err := parseUUID(paymentID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	fence, err := parseUUID(owner)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	return id, fence, nil
}

func parseUUID(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.String() != strings.ToLower(value) {
		return pgtype.UUID{}, ErrInvalidInput
	}
	return uuidValue(parsed), nil
}

func optionalUUID(value *string) (pgtype.UUID, error) {
	if value == nil {
		return pgtype.UUID{}, nil
	}
	return parseUUID(*value)
}

func uuidValue(value uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte(value), Valid: true}
}

func uuidString(value pgtype.UUID) (string, error) {
	if !value.Valid {
		return "", ErrIntegrity
	}
	return uuid.UUID(value.Bytes).String(), nil
}

func postgresTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func requiredTime(value pgtype.Timestamptz) (time.Time, error) {
	if !value.Valid || value.InfinityModifier != pgtype.Finite {
		return time.Time{}, ErrIntegrity
	}
	return value.Time.UTC(), nil
}

func strictOptionalTime(value pgtype.Timestamptz) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	if value.InfinityModifier != pgtype.Finite {
		return nil, ErrIntegrity
	}
	result := value.Time.UTC()
	return &result, nil
}

func uint64Numeric(value pgtype.Numeric) (uint64, error) {
	integer, err := integerNumeric(value)
	if err != nil || !integer.IsUint64() {
		return 0, ErrIntegrity
	}
	return integer.Uint64(), nil
}

func integerNumericString(value pgtype.Numeric) (string, error) {
	integer, err := integerNumeric(value)
	if err != nil || integer.Sign() < 0 {
		return "", ErrIntegrity
	}
	return integer.String(), nil
}

func integerNumeric(value pgtype.Numeric) (*big.Int, error) {
	if !value.Valid || value.InfinityModifier != pgtype.Finite || value.Int == nil {
		return nil, ErrIntegrity
	}
	result := new(big.Int).Set(value.Int)
	switch {
	case value.Exp > 0:
		result.Mul(result, new(big.Int).Exp(
			big.NewInt(10), big.NewInt(int64(value.Exp)), nil,
		))
	case value.Exp < 0:
		divisor := new(big.Int).Exp(
			big.NewInt(10), big.NewInt(int64(-value.Exp)), nil,
		)
		quotient, remainder := new(big.Int), new(big.Int)
		quotient.QuoRem(result, divisor, remainder)
		if remainder.Sign() != 0 {
			return nil, ErrIntegrity
		}
		result = quotient
	}
	return result, nil
}

func digestFromBytes(value []byte) (Digest, error) {
	var result Digest
	if len(value) != len(result) {
		return result, ErrIntegrity
	}
	copy(result[:], value)
	return result, nil
}

func addressFromBytes(value []byte) (common.Address, error) {
	var result common.Address
	if len(value) != len(result) {
		return result, ErrIntegrity
	}
	copy(result[:], value)
	return result, nil
}

func transactionHashFromBytes(value []byte) (common.Hash, error) {
	var result common.Hash
	if len(value) != len(result) {
		return result, ErrIntegrity
	}
	copy(result[:], value)
	return result, nil
}

func zeroTransactionHash(value common.Hash) bool {
	return value == (common.Hash{})
}

func validFailureCode(value string) bool {
	return billingCodePattern.MatchString(value)
}

func validAPIKeyPrefix(value string) bool {
	if len(value) != 10 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).
		DecodeString(strings.ToUpper(value))
	return err == nil && len(decoded) == 6
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
