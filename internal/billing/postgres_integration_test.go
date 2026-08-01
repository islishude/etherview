//go:build integration

package billing

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"maps"
	"math/big"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const billingTestDatabaseEnvironment = "ETHERVIEW_TEST_DATABASE_URL"

func TestPostgresBillingReplayFenceAndUnknownReconciliation(t *testing.T) {
	db := newBillingPostgres(t)
	ledger, err := NewPostgresLedger(db, 11155111, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	input := testReserveInput()
	start := make(chan struct{})
	results := make(chan Reservation, 8)
	errorsByReserve := make(chan error, 8)
	var attempts sync.WaitGroup
	for range 8 {
		attempts.Go(func() {
			<-start
			reservation, reserveErr := ledger.Reserve(context.Background(), input)
			results <- reservation
			errorsByReserve <- reserveErr
		})
	}
	close(start)
	attempts.Wait()
	close(results)
	close(errorsByReserve)
	for reserveErr := range errorsByReserve {
		if reserveErr != nil {
			t.Fatalf("concurrent reserve: %v", reserveErr)
		}
	}
	var owned Reservation
	ownedCount := 0
	paymentID := ""
	for reservation := range results {
		if paymentID == "" {
			paymentID = reservation.Payment.ID
		}
		if reservation.Payment.ID != paymentID {
			t.Fatalf("fingerprint created multiple payments: %q and %q", paymentID, reservation.Payment.ID)
		}
		if reservation.Owned {
			ownedCount++
			owned = reservation
		} else if reservation.Owner != "" {
			t.Fatal("duplicate fingerprint received reservation fence")
		}
	}
	if ownedCount != 1 {
		t.Fatalf("owned reservations=%d", ownedCount)
	}
	assertBillingEventCount(t, db, paymentID, 1)

	var payer common.Address
	payer[19] = 9
	verifiedAt := input.ObservedAt.Add(time.Second)
	verified, err := ledger.MarkVerified(t.Context(), VerifiedInput{
		PaymentID: paymentID, Owner: owned.Owner, Payer: payer,
		ObservedAt: verifiedAt,
	})
	if err != nil || verified.State != StateVerified {
		t.Fatalf("verify payment=%+v error=%v", verified, err)
	}

	handlerResults := make(chan error, 2)
	start = make(chan struct{})
	for range 2 {
		attempts.Go(func() {
			<-start
			_, startErr := ledger.StartHandler(
				context.Background(), paymentID, owned.Owner,
				verifiedAt.Add(time.Second),
			)
			handlerResults <- startErr
		})
	}
	close(start)
	attempts.Wait()
	close(handlerResults)
	successes, conflicts := 0, 0
	for startErr := range handlerResults {
		switch {
		case startErr == nil:
			successes++
		case errors.Is(startErr, ErrStateConflict):
			conflicts++
		default:
			t.Fatalf("start handler: %v", startErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("handler starts successes=%d conflicts=%d", successes, conflicts)
	}
	if _, err := ledger.BeginSettlement(
		t.Context(), paymentID, owned.Owner, verifiedAt.Add(2*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	var prematureHash common.Hash
	prematureHash[31] = 6
	if _, err := ledger.ReconcileSettled(
		t.Context(), paymentID, prematureHash, verifiedAt.Add(2500*time.Millisecond),
	); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("operator reconciled a non-unknown settlement as settled: %v", err)
	}
	if _, err := ledger.ReconcileFailed(
		t.Context(), paymentID, verifiedAt.Add(2500*time.Millisecond),
	); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("operator reconciled a non-unknown settlement as failed: %v", err)
	}
	unknown, err := ledger.MarkSettlementUnknown(
		t.Context(), paymentID, owned.Owner, verifiedAt.Add(3*time.Second),
	)
	if err != nil || unknown.State != StateSettling ||
		unknown.FailureCode == nil || *unknown.FailureCode != "settlement_unknown" {
		t.Fatalf("unknown payment=%+v error=%v", unknown, err)
	}
	if _, err := ledger.ReconcileSettled(
		t.Context(), paymentID, prematureHash, verifiedAt.Add(2500*time.Millisecond),
	); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("operator moved an unknown settlement timestamp backward: %v", err)
	}
	if _, err := ledger.ReconcileFailed(
		t.Context(), paymentID, verifiedAt.Add(2500*time.Millisecond),
	); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("operator moved an unknown failure timestamp backward: %v", err)
	}
	replayed, err := ledger.Reserve(t.Context(), input)
	if err != nil || replayed.Owned || replayed.Payment.State != StateSettling ||
		replayed.Owner != "" {
		t.Fatalf("unknown replay=%+v error=%v", replayed, err)
	}
	var transactionHash common.Hash
	transactionHash[31] = 7
	settled, err := ledger.ReconcileSettled(
		t.Context(), paymentID, transactionHash, verifiedAt.Add(4*time.Second),
	)
	if err != nil || settled.State != StateSettled ||
		settled.TransactionHash == nil || *settled.TransactionHash != transactionHash {
		t.Fatalf("reconciled payment=%+v error=%v", settled, err)
	}
	if _, err := ledger.ReconcileFailed(
		t.Context(), paymentID, verifiedAt.Add(5*time.Second),
	); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("terminal payment reconciled twice: %v", err)
	}
	assertBillingEventCount(t, db, paymentID, 6)
	inspection, err := ledger.Inspect(t.Context(), paymentID)
	if err != nil || inspection.Payment.ID != paymentID ||
		len(inspection.Events) != 6 {
		t.Fatalf("inspection=%+v error=%v", inspection, err)
	}
	for index, event := range inspection.Events {
		if event.PaymentID != paymentID ||
			(index > 0 && event.ID <= inspection.Events[index-1].ID) {
			t.Fatalf("events are not ordered and bound: %+v", inspection.Events)
		}
	}
	lastEvent := inspection.Events[len(inspection.Events)-1]
	if lastEvent.Actor != ActorOperator ||
		lastEvent.Code != "operator_reconciled_settled" ||
		lastEvent.TransactionHash == nil ||
		*lastEvent.TransactionHash != transactionHash {
		t.Fatalf("reconciliation event=%+v", lastEvent)
	}

	if _, err := db.ExecContext(t.Context(),
		`UPDATE billing_payments SET amount_atomic = amount_atomic + 1 WHERE id = $1::uuid`,
		paymentID,
	); err == nil {
		t.Fatal("settled financial fields were mutable")
	}
	if _, err := db.ExecContext(t.Context(),
		`UPDATE billing_payment_events SET code = 'changed' WHERE payment_id = $1::uuid`,
		paymentID,
	); err == nil {
		t.Fatal("billing event was mutable")
	}
}

func TestPostgresBillingCrashWindowReconciliationIsStaleAndFenced(t *testing.T) {
	db := newBillingPostgres(t)
	ledger, err := NewPostgresLedger(db, 11155111, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	failedReservation, failedSettlingAt := createSettlingPayment(
		t, ledger, 31, nil,
	)
	freshAt := failedSettlingAt.Add(SettlementCrashReconcileDelay - time.Nanosecond)
	var transactionHash common.Hash
	transactionHash[31] = 0xa1
	if _, err := ledger.ReconcileSettled(
		t.Context(), failedReservation.Payment.ID, transactionHash, freshAt,
	); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("fresh crash-window row reconciled as settled: %v", err)
	}
	if _, err := ledger.ReconcileFailed(
		t.Context(), failedReservation.Payment.ID, freshAt,
	); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("fresh crash-window row reconciled as failed: %v", err)
	}
	failed, err := ledger.ReconcileFailed(
		t.Context(), failedReservation.Payment.ID,
		failedSettlingAt.Add(SettlementCrashReconcileDelay),
	)
	if err != nil || failed.State != StateFailed ||
		failed.FailureCode == nil ||
		*failed.FailureCode != "operator_reconciled_failed" {
		t.Fatalf("stale reconciliation=%+v error=%v", failed, err)
	}
	failedInspection, err := ledger.Inspect(t.Context(), failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	failedEvent := failedInspection.Events[len(failedInspection.Events)-1]
	if failedEvent.Code != "operator_reconciled_stale_settling_failed" ||
		failedEvent.Actor != ActorOperator {
		t.Fatalf("stale failure event=%+v", failedEvent)
	}

	settledReservation, settledSettlingAt := createSettlingPayment(
		t, ledger, 32, nil,
	)
	transactionHash[31] = 0xa2
	if _, err := db.ExecContext(
		t.Context(), `INSERT INTO chains (chain_id) VALUES (999)`,
	); err != nil {
		t.Fatal(err)
	}
	otherChainLedger, err := NewPostgresLedger(db, 999, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := otherChainLedger.ReconcileSettled(
		t.Context(), settledReservation.Payment.ID, transactionHash,
		settledSettlingAt.Add(SettlementCrashReconcileDelay),
	); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("cross-chain operator reconciliation: %v", err)
	}
	stillSettling, err := ledger.Get(t.Context(), settledReservation.Payment.ID)
	if err != nil || stillSettling.State != StateSettling {
		t.Fatalf("cross-chain reconciliation changed payment=%+v error=%v", stillSettling, err)
	}
	settled, err := ledger.ReconcileSettled(
		t.Context(), settledReservation.Payment.ID, transactionHash,
		settledSettlingAt.Add(SettlementCrashReconcileDelay),
	)
	if err != nil || settled.State != StateSettled ||
		settled.TransactionHash == nil ||
		*settled.TransactionHash != transactionHash {
		t.Fatalf("stale settled reconciliation=%+v error=%v", settled, err)
	}
	settledInspection, err := ledger.Inspect(t.Context(), settled.ID)
	if err != nil {
		t.Fatal(err)
	}
	settledEvent := settledInspection.Events[len(settledInspection.Events)-1]
	if settledEvent.Code != "operator_reconciled_stale_settling_settled" ||
		settledEvent.Actor != ActorOperator ||
		settledEvent.TransactionHash == nil {
		t.Fatalf("stale settled event=%+v", settledEvent)
	}

	racedReservation, racedSettlingAt := createSettlingPayment(
		t, ledger, 33, nil,
	)
	raceAt := racedSettlingAt.Add(SettlementCrashReconcileDelay)
	transactionHash[31] = 0xa3
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, raceErr := ledger.MarkSettled(
			context.Background(), racedReservation.Payment.ID,
			racedReservation.Owner, transactionHash, raceAt,
		)
		results <- raceErr
	}()
	go func() {
		<-start
		_, raceErr := ledger.ReconcileFailed(
			context.Background(), racedReservation.Payment.ID, raceAt,
		)
		results <- raceErr
	}()
	close(start)
	firstErr, secondErr := <-results, <-results
	successes, conflicts := 0, 0
	for _, raceErr := range []error{firstErr, secondErr} {
		switch {
		case raceErr == nil:
			successes++
		case errors.Is(raceErr, ErrStateConflict):
			conflicts++
		default:
			t.Fatalf("runtime/operator race: %v", raceErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("runtime/operator successes=%d conflicts=%d", successes, conflicts)
	}
	racedInspection, err := ledger.Inspect(
		t.Context(), racedReservation.Payment.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(racedInspection.Events) != 5 ||
		(racedInspection.Payment.State != StateSettled &&
			racedInspection.Payment.State != StateFailed) {
		t.Fatalf("raced inspection=%+v", racedInspection)
	}
}

func TestPostgresBillingListsPaginationFiltersAndSummary(t *testing.T) {
	db := newBillingPostgres(t)
	ledger, err := NewPostgresLedger(db, 11155111, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	userID := uuid.NewString()
	var userAddress common.Address
	userAddress[19] = testReserveInput().Fingerprint[0]
	insertBillingUser(t, db, userID, userAddress)

	base := testReserveInput().ObservedAt.Add(10 * time.Minute)
	var created []Payment
	for index, operation := range []string{"listBlocks", "getBlock", "listBlocks"} {
		input := testReserveInput()
		input.Fingerprint[1] ^= byte(50 + index)
		input.ResourceDigest[0] ^= byte(50 + index)
		input.Operation = operation
		input.AmountAtomic = []string{"100", "200", "300"}[index]
		input.ObservedAt = base.Add(time.Duration(index) * time.Minute)
		reservation, settlingAt := createSettlingPaymentFromInput(
			t, ledger, input, &userID,
		)
		if index == 1 {
			payment, transitionErr := ledger.MarkFailed(
				t.Context(), reservation.Payment.ID, reservation.Owner,
				"handler_failed", settlingAt.Add(time.Second),
			)
			if transitionErr != nil {
				t.Fatal(transitionErr)
			}
			created = append(created, payment)
			continue
		}
		var hash common.Hash
		hash[31] = byte(index + 1)
		payment, transitionErr := ledger.MarkSettled(
			t.Context(), reservation.Payment.ID, reservation.Owner, hash,
			settlingAt.Add(time.Second),
		)
		if transitionErr != nil {
			t.Fatal(transitionErr)
		}
		created = append(created, payment)
	}

	firstPage, err := ledger.ListUser(t.Context(), userID, nil, 2)
	if err != nil || len(firstPage) != 2 ||
		firstPage[0].ID != created[2].ID ||
		firstPage[1].ID != created[1].ID {
		t.Fatalf("first page=%+v error=%v", firstPage, err)
	}
	secondPage, err := ledger.ListUser(t.Context(), userID, &PageAfter{
		CreatedAt: firstPage[1].CreatedAt,
		ID:        firstPage[1].ID,
	}, 2)
	if err != nil || len(secondPage) != 1 ||
		secondPage[0].ID != created[0].ID {
		t.Fatalf("second page=%+v error=%v", secondPage, err)
	}
	if _, err := ledger.ListUser(
		t.Context(), userID, nil, maximumBillingPage+1,
	); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized internal page: %v", err)
	}

	operation := "listBlocks"
	adminRows, err := ledger.ListAdmin(t.Context(), AdminFilter{
		Operation: &operation,
	}, nil, maximumBillingPage)
	if err != nil || len(adminRows) != 2 {
		t.Fatalf("filtered admin rows=%+v error=%v", adminRows, err)
	}
	fromTime, toTime := base.Add(-time.Second), base.Add(3*time.Minute)
	summary, err := ledger.Summary(t.Context(), AdminFilter{
		FromTime: &fromTime, ToTime: &toTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	totalCount, totalAmount := new(big.Int), new(big.Int)
	for _, row := range summary {
		count, ok := new(big.Int).SetString(row.PaymentCount, 10)
		if !ok {
			t.Fatalf("summary count=%q", row.PaymentCount)
		}
		amount, ok := new(big.Int).SetString(row.AmountAtomic, 10)
		if !ok {
			t.Fatalf("summary amount=%q", row.AmountAtomic)
		}
		totalCount.Add(totalCount, count)
		totalAmount.Add(totalAmount, amount)
	}
	if totalCount.String() != "3" || totalAmount.String() != "600" {
		t.Fatalf("summary count=%s amount=%s rows=%+v", totalCount, totalAmount, summary)
	}
}

func TestPostgresBillingUserAttributionIsExactOptionalAndNotBackfilled(t *testing.T) {
	db := newBillingPostgres(t)
	ledger, err := NewPostgresLedger(db, 11155111, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(
		t.Context(), `INSERT INTO chains (chain_id) VALUES (999)`,
	); err != nil {
		t.Fatal(err)
	}

	type reservedPayment struct {
		reservation Reservation
		input       ReserveInput
	}
	reserve := func(identityByte byte) reservedPayment {
		input := testReserveInput()
		input.Fingerprint[0] ^= identityByte
		input.ResourceDigest[0] ^= identityByte
		input.ObservedAt = input.ObservedAt.Add(
			time.Duration(identityByte) * time.Minute,
		)
		reservation, reserveErr := ledger.Reserve(t.Context(), input)
		if reserveErr != nil {
			t.Fatal(reserveErr)
		}
		return reservedPayment{reservation: reservation, input: input}
	}
	verify := func(
		payment reservedPayment,
		payer common.Address,
		userID *string,
	) (Payment, error) {
		return ledger.MarkVerified(t.Context(), VerifiedInput{
			PaymentID:  payment.reservation.Payment.ID,
			Owner:      payment.reservation.Owner,
			Payer:      payer,
			UserID:     userID,
			ObservedAt: payment.input.ObservedAt.Add(time.Second),
		})
	}

	activeID, disabledID := uuid.NewString(), uuid.NewString()
	var activeAddress, disabledAddress common.Address
	activeAddress[19], disabledAddress[19] = 0xa1, 0xa2
	insertBillingUserRecord(
		t, db, activeID, 11155111, activeAddress, "active",
	)
	insertBillingUserRecord(
		t, db, disabledID, 11155111, disabledAddress, "disabled",
	)
	activePayment, err := verify(reserve(70), activeAddress, &activeID)
	if err != nil || activePayment.UserID == nil ||
		*activePayment.UserID != activeID {
		t.Fatalf("active attribution=%+v error=%v", activePayment, err)
	}
	disabledPayment, err := verify(
		reserve(71), disabledAddress, &disabledID,
	)
	if err != nil || disabledPayment.UserID == nil ||
		*disabledPayment.UserID != disabledID {
		t.Fatalf("disabled attribution=%+v error=%v", disabledPayment, err)
	}

	var accountlessPayer common.Address
	accountlessPayer[19] = 0xa3
	accountless, err := verify(reserve(72), accountlessPayer, nil)
	if err != nil || accountless.UserID != nil ||
		accountless.State != StateVerified {
		t.Fatalf("accountless payment=%+v error=%v", accountless, err)
	}

	mismatchedID := uuid.NewString()
	var mismatchedUserAddress, actualPayer common.Address
	mismatchedUserAddress[19], actualPayer[19] = 0xa4, 0xa5
	insertBillingUserRecord(
		t, db, mismatchedID, 11155111, mismatchedUserAddress, "active",
	)
	mismatched := reserve(73)
	if _, err := verify(mismatched, actualPayer, &mismatchedID); !errors.Is(
		err, ErrStateConflict,
	) {
		t.Fatalf("mismatched address attribution: %v", err)
	}
	mismatchedRow, err := ledger.Get(
		t.Context(), mismatched.reservation.Payment.ID,
	)
	if err != nil || mismatchedRow.State != StateReserved ||
		mismatchedRow.UserID != nil {
		t.Fatalf("mismatched row=%+v error=%v", mismatchedRow, err)
	}

	crossChainID := uuid.NewString()
	var crossChainPayer common.Address
	crossChainPayer[19] = 0xa6
	insertBillingUserRecord(
		t, db, crossChainID, 999, crossChainPayer, "active",
	)
	crossChain := reserve(74)
	if _, err := verify(
		crossChain, crossChainPayer, &crossChainID,
	); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("cross-chain attribution: %v", err)
	}
	crossChainRow, err := ledger.Get(
		t.Context(), crossChain.reservation.Payment.ID,
	)
	if err != nil || crossChainRow.State != StateReserved ||
		crossChainRow.UserID != nil {
		t.Fatalf("cross-chain row=%+v error=%v", crossChainRow, err)
	}

	var historicalPayer common.Address
	historicalPayer[19] = 0xa7
	historicalReservation := reserve(75)
	historical, err := verify(historicalReservation, historicalPayer, nil)
	if err != nil {
		t.Fatal(err)
	}
	handlerAt := historicalReservation.input.ObservedAt.Add(2 * time.Second)
	if _, err := ledger.StartHandler(
		t.Context(), historical.ID, historicalReservation.reservation.Owner,
		handlerAt,
	); err != nil {
		t.Fatal(err)
	}
	settlingAt := handlerAt.Add(time.Second)
	if _, err := ledger.BeginSettlement(
		t.Context(), historical.ID, historicalReservation.reservation.Owner,
		settlingAt,
	); err != nil {
		t.Fatal(err)
	}
	var transactionHash common.Hash
	transactionHash[31] = 0xa7
	if _, err := ledger.MarkSettled(
		t.Context(), historical.ID, historicalReservation.reservation.Owner,
		transactionHash, settlingAt.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	historicalUserID := uuid.NewString()
	insertBillingUserRecord(
		t, db, historicalUserID, 11155111, historicalPayer, "active",
	)
	historical, err = ledger.Get(t.Context(), historical.ID)
	if err != nil || historical.UserID != nil ||
		historical.State != StateSettled {
		t.Fatalf("historical payment=%+v error=%v", historical, err)
	}
	userRows, err := ledger.ListUser(
		t.Context(), historicalUserID, nil, maximumBillingPage,
	)
	if err != nil || len(userRows) != 0 {
		t.Fatalf("historical user rows=%+v error=%v", userRows, err)
	}
	adminRows, err := ledger.ListAdmin(
		t.Context(), AdminFilter{}, nil, maximumBillingPage,
	)
	if err != nil {
		t.Fatal(err)
	}
	foundHistorical := false
	for _, payment := range adminRows {
		if payment.ID == historical.ID {
			foundHistorical = true
			if payment.UserID != nil {
				t.Fatalf("administrator row was backfilled: %+v", payment)
			}
		}
	}
	if !foundHistorical {
		t.Fatalf("administrator list omitted historical payment %s", historical.ID)
	}
}

func TestPostgresBillingFailureAndExpiryAreTerminal(t *testing.T) {
	db := newBillingPostgres(t)
	ledger, err := NewPostgresLedger(db, 11155111, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first := testReserveInput()
	reserved, err := ledger.Reserve(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	var payer common.Address
	payer[0] = 3
	if _, err := ledger.MarkVerified(t.Context(), VerifiedInput{
		PaymentID: reserved.Payment.ID, Owner: reserved.Owner, Payer: payer,
		ObservedAt: first.ObservedAt.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.StartHandler(
		t.Context(), reserved.Payment.ID, reserved.Owner,
		first.ObservedAt.Add(2*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	failed, err := ledger.MarkFailed(
		t.Context(), reserved.Payment.ID, reserved.Owner, "handler_failed",
		first.ObservedAt.Add(3*time.Second),
	)
	if err != nil || failed.State != StateFailed {
		t.Fatalf("failed payment=%+v error=%v", failed, err)
	}

	second := first
	second.Fingerprint[0] ^= 0xff
	second.ResourceDigest[0] ^= 0xff
	expiring, err := ledger.Reserve(t.Context(), second)
	if err != nil {
		t.Fatal(err)
	}
	count, err := ledger.Expire(
		t.Context(), second.ObservedAt.Add(2*time.Minute), 100,
	)
	if err != nil || count != 1 {
		t.Fatalf("expired count=%d error=%v", count, err)
	}
	expired, err := ledger.Get(t.Context(), expiring.Payment.ID)
	if err != nil || expired.State != StateExpired ||
		expired.FailureCode == nil || *expired.FailureCode != "reservation_expired" {
		t.Fatalf("expired payment=%+v error=%v", expired, err)
	}
	if _, err := ledger.MarkVerified(t.Context(), VerifiedInput{
		PaymentID: expired.ID, Owner: expiring.Owner, Payer: payer,
		ObservedAt: second.ObservedAt.Add(2*time.Minute + time.Second),
	}); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("expired payment became verified: %v", err)
	}
}

func TestPostgresBillingExpiryIsChainScoped(t *testing.T) {
	db := newBillingPostgres(t)
	if _, err := db.ExecContext(
		t.Context(),
		`INSERT INTO chains (chain_id) VALUES (84532)`,
	); err != nil {
		t.Fatal(err)
	}
	firstLedger, err := NewPostgresLedger(db, 11155111, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	secondLedger, err := NewPostgresLedger(db, 84532, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	firstInput := testReserveInput()
	first, err := firstLedger.Reserve(t.Context(), firstInput)
	if err != nil {
		t.Fatal(err)
	}
	secondInput := firstInput
	secondInput.Fingerprint[0] ^= 0xff
	secondInput.ResourceDigest[0] ^= 0xff
	second, err := secondLedger.Reserve(t.Context(), secondInput)
	if err != nil {
		t.Fatal(err)
	}
	expiredAt := firstInput.ObservedAt.Add(2 * time.Minute)
	count, err := firstLedger.Expire(t.Context(), expiredAt, 1000)
	if err != nil || count != 1 {
		t.Fatalf("first-chain expired count=%d error=%v", count, err)
	}
	if payment, err := firstLedger.Get(
		t.Context(), first.Payment.ID,
	); err != nil || payment.State != StateExpired {
		t.Fatalf("first-chain payment=%+v error=%v", payment, err)
	}
	if payment, err := secondLedger.Get(
		t.Context(), second.Payment.ID,
	); err != nil || payment.State != StateReserved {
		t.Fatalf("second-chain payment=%+v error=%v", payment, err)
	}
	count, err = secondLedger.Expire(t.Context(), expiredAt, 1000)
	if err != nil || count != 1 {
		t.Fatalf("second-chain expired count=%d error=%v", count, err)
	}
}

func createSettlingPayment(
	t *testing.T,
	ledger *PostgresLedger,
	identityByte byte,
	userID *string,
) (Reservation, time.Time) {
	t.Helper()
	input := testReserveInput()
	input.Fingerprint[0] ^= identityByte
	input.ResourceDigest[0] ^= identityByte
	input.ObservedAt = input.ObservedAt.Add(time.Duration(identityByte) * time.Minute)
	return createSettlingPaymentFromInput(t, ledger, input, userID)
}

func createSettlingPaymentFromInput(
	t *testing.T,
	ledger *PostgresLedger,
	input ReserveInput,
	userID *string,
) (Reservation, time.Time) {
	t.Helper()
	reservation, err := ledger.Reserve(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	var payer common.Address
	payer[19] = input.Fingerprint[0]
	verifiedAt := input.ObservedAt.Add(time.Second)
	if _, err := ledger.MarkVerified(t.Context(), VerifiedInput{
		PaymentID:  reservation.Payment.ID,
		Owner:      reservation.Owner,
		Payer:      payer,
		UserID:     userID,
		ObservedAt: verifiedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.StartHandler(
		t.Context(), reservation.Payment.ID, reservation.Owner,
		verifiedAt.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	settlingAt := verifiedAt.Add(2 * time.Second)
	if _, err := ledger.BeginSettlement(
		t.Context(), reservation.Payment.ID, reservation.Owner, settlingAt,
	); err != nil {
		t.Fatal(err)
	}
	return reservation, settlingAt
}

func insertBillingUser(
	t *testing.T,
	db *sql.DB,
	userID string,
	address common.Address,
) {
	t.Helper()
	insertBillingUserRecord(t, db, userID, 11155111, address, "active")
}

func insertBillingUserRecord(
	t *testing.T,
	db *sql.DB,
	userID string,
	chainID uint64,
	address common.Address,
	status string,
) {
	t.Helper()
	createdAt := testReserveInput().ObservedAt
	if _, err := db.ExecContext(
		t.Context(),
		`INSERT INTO users (
			id, chain_id, address, role, status, created_at, updated_at
		) VALUES ($1::uuid, $2::numeric, $3, 'user', $4, $5, $5)`,
		userID, chainID, address[:], status, createdAt,
	); err != nil {
		t.Fatalf("insert billing user: %v", err)
	}
}

func newBillingPostgres(t *testing.T) *sql.DB {
	t.Helper()
	rawURL := strings.TrimSpace(os.Getenv(billingTestDatabaseEnvironment))
	if rawURL == "" {
		t.Skipf("%s is not set", billingTestDatabaseEnvironment)
	}
	adminConfig, err := pgx.ParseConfig(rawURL)
	if err != nil {
		t.Fatalf("parse %s: %v", billingTestDatabaseEnvironment, err)
	}
	adminConfig.RuntimeParams = cloneBillingRuntimeParams(adminConfig.RuntimeParams)
	adminConfig.RuntimeParams["application_name"] = "etherview-billing-admin"
	adminDB := stdlib.OpenDB(*adminConfig)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := adminDB.PingContext(ctx); err != nil {
		_ = adminDB.Close()
		t.Fatalf("connect to %s: %v", billingTestDatabaseEnvironment, err)
	}
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	schema := "etherview_billing_it_" + hex.EncodeToString(suffix)
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA `+quoteBillingIdentifier(schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	testConfig := adminConfig.Copy()
	testConfig.RuntimeParams = cloneBillingRuntimeParams(testConfig.RuntimeParams)
	testConfig.RuntimeParams["application_name"] = "etherview-billing-test"
	testConfig.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*testConfig)
	db.SetMaxOpenConns(12)
	db.SetMaxIdleConns(6)
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("connect isolated schema: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_, _ = adminDB.ExecContext(
			cleanupCtx, `DROP SCHEMA `+quoteBillingIdentifier(schema)+` CASCADE`,
		)
		_ = adminDB.Close()
	})
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO chains (chain_id) VALUES (11155111)`,
	); err != nil {
		t.Fatalf("insert chain: %v", err)
	}
	return db
}

func cloneBillingRuntimeParams(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source)+2)
	maps.Copy(cloned, source)
	return cloned
}

func quoteBillingIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func assertBillingEventCount(t *testing.T, db *sql.DB, paymentID string, want int) {
	t.Helper()
	var count int
	if err := db.QueryRowContext(
		t.Context(),
		`SELECT count(*) FROM billing_payment_events WHERE payment_id = $1::uuid`,
		paymentID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("billing event count=%d want=%d", count, want)
	}
}
