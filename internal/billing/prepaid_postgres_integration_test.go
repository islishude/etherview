//go:build integration

package billing

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
)

func TestPrepaidTopupCreditsOnceAndPendingReusesKnownHash(t *testing.T) {
	db := newBillingPostgres(t)
	userID := uuid.NewString()
	var payer, asset, recipient common.Address
	payer[19], asset[19], recipient[19] = 0xa1, 0xb1, 0xc1
	insertBillingUser(t, db, userID, payer)
	prepaid := newTestPrepaidLedger(t, db, asset, recipient)
	payments, err := NewPostgresLedger(db, 11155111, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	base := testReserveInput().ObservedAt

	firstIntent, firstReservation := createReservedTopup(
		t, prepaid, payments, userID, payer, "50", base, 1,
	)
	verifiedAt := base.Add(time.Second)
	if _, err := payments.MarkVerified(t.Context(), VerifiedInput{
		PaymentID: firstReservation.Payment.ID, Owner: firstReservation.Owner,
		Payer: payer, UserID: &userID, ObservedAt: verifiedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := prepaid.BeginTopupSettlement(
		t.Context(), firstReservation.Payment.ID, firstReservation.Owner,
		firstIntent.ID, verifiedAt.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	var firstHash common.Hash
	firstHash[31] = 1
	start := make(chan struct{})
	results := make(chan error, 2)
	var attempts sync.WaitGroup
	for range 2 {
		attempts.Go(func() {
			<-start
			_, creditErr := prepaid.CreditTopup(
				context.Background(), firstReservation.Payment.ID,
				firstReservation.Owner, firstHash, verifiedAt.Add(2*time.Second),
			)
			results <- creditErr
		})
	}
	close(start)
	attempts.Wait()
	close(results)
	successes, conflicts := 0, 0
	for creditErr := range results {
		switch {
		case creditErr == nil:
			successes++
		case errors.Is(creditErr, ErrStateConflict):
			conflicts++
		default:
			t.Fatalf("concurrent top-up credit: %v", creditErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("top-up credit successes=%d conflicts=%d", successes, conflicts)
	}
	assertBillingAccountAmounts(t, prepaid, userID, "50", "0", "50")
	assertBillingEntryCount(t, db, "topup", firstReservation.Payment.ID, 1)

	secondBase := base.Add(10 * time.Minute)
	secondIntent, secondReservation := createReservedTopup(
		t, prepaid, payments, userID, payer, "50", secondBase, 2,
	)
	secondVerifiedAt := secondBase.Add(time.Second)
	if _, err := payments.MarkVerified(t.Context(), VerifiedInput{
		PaymentID: secondReservation.Payment.ID, Owner: secondReservation.Owner,
		Payer: payer, UserID: &userID, ObservedAt: secondVerifiedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := prepaid.BeginTopupSettlement(
		t.Context(), secondReservation.Payment.ID, secondReservation.Owner,
		secondIntent.ID, secondVerifiedAt.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	var pendingHash, wrongHash common.Hash
	pendingHash[31], wrongHash[31] = 2, 3
	if err := prepaid.MarkTopupSettlementPending(
		t.Context(), secondReservation.Payment.ID, secondReservation.Owner,
		secondIntent.ID, pendingHash, secondVerifiedAt.Add(2*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := prepaid.ReconcileTopupSettled(
		t.Context(), secondReservation.Payment.ID, wrongHash,
		secondVerifiedAt.Add(3*time.Second),
	); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("pending top-up accepted replacement hash: %v", err)
	}
	account, err := prepaid.ReconcileTopupSettled(
		t.Context(), secondReservation.Payment.ID, pendingHash,
		secondVerifiedAt.Add(3*time.Second),
	)
	if err != nil || account.TotalCreditAtomic != "100" {
		t.Fatalf("pending reconciliation account=%+v error=%v", account, err)
	}
	payment, err := payments.Get(t.Context(), secondReservation.Payment.ID)
	if err != nil || payment.TransactionHash == nil || *payment.TransactionHash != pendingHash {
		t.Fatalf("pending reconciliation payment=%+v error=%v", payment, err)
	}
	assertBillingEntryCount(t, db, "topup", secondReservation.Payment.ID, 1)

	thirdBase := base.Add(20 * time.Minute)
	thirdIntent, thirdReservation := createReservedTopup(
		t, prepaid, payments, userID, payer, "25", thirdBase, 3,
	)
	thirdVerifiedAt := thirdBase.Add(time.Second)
	if _, err := payments.MarkVerified(t.Context(), VerifiedInput{
		PaymentID: thirdReservation.Payment.ID, Owner: thirdReservation.Owner,
		Payer: payer, UserID: &userID, ObservedAt: thirdVerifiedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := prepaid.BeginTopupSettlement(
		t.Context(), thirdReservation.Payment.ID, thirdReservation.Owner,
		thirdIntent.ID, thirdVerifiedAt.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	var failedHash common.Hash
	failedHash[31] = 4
	if err := prepaid.MarkTopupSettlementPending(
		t.Context(), thirdReservation.Payment.ID, thirdReservation.Owner,
		thirdIntent.ID, failedHash, thirdVerifiedAt.Add(2*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(
		t.Context(),
		`UPDATE billing_topup_intents SET transaction_hash = $2 WHERE id = $1::uuid`,
		thirdIntent.ID, wrongHash[:],
	); err == nil {
		t.Fatal("known top-up settlement hash was replaceable")
	}
	if err := prepaid.ReconcileTopupFailed(
		t.Context(), thirdReservation.Payment.ID, thirdVerifiedAt.Add(3*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	failedIntent, err := prepaid.TopupIntent(t.Context(), userID, thirdIntent.ID)
	if err != nil || failedIntent.State != TopupIntentFailed ||
		failedIntent.TransactionHash == nil || *failedIntent.TransactionHash != failedHash {
		t.Fatalf("failed top-up intent=%+v error=%v", failedIntent, err)
	}
	assertBillingAccountAmounts(t, prepaid, userID, "100", "0", "100")
	assertBillingEntryCount(t, db, "topup", thirdReservation.Payment.ID, 0)
}

func TestPrepaidUsageSharesBalanceAndExpiresReservations(t *testing.T) {
	db := newBillingPostgres(t)
	userID := uuid.NewString()
	var payer, asset, recipient common.Address
	payer[19], asset[19], recipient[19] = 0xa2, 0xb2, 0xc2
	insertBillingUser(t, db, userID, payer)
	prepaid := newTestPrepaidLedger(t, db, asset, recipient)
	base := testReserveInput().ObservedAt
	if _, err := prepaid.EnsureAccount(t.Context(), userID, base); err != nil {
		t.Fatal(err)
	}
	if _, err := prepaid.Adjust(t.Context(), AdjustmentInput{
		UserID: userID, Direction: "credit", AmountAtomic: "100",
		Reason: "integration fixture", ObservedAt: base.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	insertPrepaidAPIKey(t, db, "aaaaaaaaaa", userID, base)
	insertPrepaidAPIKey(t, db, "bbbbbbbbbb", userID, base)

	reserve := func(prefix, amount string, identity byte, observedAt time.Time) (UsageReservation, error) {
		var resource Digest
		resource[31] = identity
		return prepaid.ReserveUsage(context.Background(), ReserveUsageInput{
			UserID: userID, APIKeyPrefix: prefix, Method: http.MethodGet,
			Operation: "etherscan.account.balance", Resource: resource,
			AmountAtomic: amount, ObservedAt: observedAt,
		})
	}
	start := make(chan struct{})
	reservations := make(chan UsageReservation, 2)
	errorsByReserve := make(chan error, 2)
	var attempts sync.WaitGroup
	for identity := byte(1); identity <= 2; identity++ {
		attempts.Go(func() {
			<-start
			reservation, reserveErr := reserve("aaaaaaaaaa", "80", identity, base.Add(2*time.Second))
			reservations <- reservation
			errorsByReserve <- reserveErr
		})
	}
	close(start)
	attempts.Wait()
	close(reservations)
	close(errorsByReserve)
	successes, insufficient := 0, 0
	var owned UsageReservation
	for reserveErr := range errorsByReserve {
		switch {
		case reserveErr == nil:
			successes++
		case errors.Is(reserveErr, ErrInsufficientCredit):
			insufficient++
		default:
			t.Fatalf("concurrent usage reserve: %v", reserveErr)
		}
	}
	for reservation := range reservations {
		if reservation.Owner != "" {
			owned = reservation
		}
	}
	if successes != 1 || insufficient != 1 {
		t.Fatalf("usage reserve successes=%d insufficient=%d", successes, insufficient)
	}
	if _, err := prepaid.ReleaseUsage(
		t.Context(), owned.Charge.ID, owned.Owner, "logical_failure", base.Add(3*time.Second),
	); err != nil {
		t.Fatal(err)
	}

	committedReservation, err := reserve("aaaaaaaaaa", "30", 3, base.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	releasedReservation, err := reserve("bbbbbbbbbb", "40", 4, base.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	var response Digest
	response[31] = 9
	if _, err := prepaid.CommitUsage(t.Context(), CommitUsageInput{
		ChargeID: committedReservation.Charge.ID, Owner: committedReservation.Owner,
		Response: response, ResponseBytes: 123, ObservedAt: base.Add(5 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := prepaid.ReleaseUsage(
		t.Context(), releasedReservation.Charge.ID, releasedReservation.Owner,
		"logical_failure", base.Add(5*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	assertBillingAccountAmounts(t, prepaid, userID, "100", "30", "70")

	expiring, err := reserve("bbbbbbbbbb", "60", 5, base.Add(6*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	expiry, err := prepaid.Expire(t.Context(), expiring.Charge.ReservationExpiresAt, 100)
	if err != nil || expiry.UsageReservations != 1 {
		t.Fatalf("usage expiry=%+v error=%v", expiry, err)
	}
	assertBillingAccountAmounts(t, prepaid, userID, "100", "30", "70")
	assertBillingEntryCount(t, db, "usage", committedReservation.Charge.ID, 1)

	// Historical P66 payments remain audit facts and never create account credit.
	legacy, err := NewPostgresLedger(db, 11155111, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	input := testReserveInput()
	input.ObservedAt = base.Add(20 * time.Minute)
	reservation, settlingAt := createSettlingPaymentFromInput(t, legacy, input, nil)
	var transactionHash common.Hash
	transactionHash[31] = 0xef
	if _, err := legacy.MarkSettled(
		t.Context(), reservation.Payment.ID, reservation.Owner,
		transactionHash, settlingAt.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	assertBillingAccountAmounts(t, prepaid, userID, "100", "30", "70")
}

func newTestPrepaidLedger(
	t *testing.T,
	database *sql.DB,
	asset, recipient common.Address,
) *PrepaidLedger {
	t.Helper()
	ledger, err := NewPrepaidLedger(database, PrepaidOptions{
		ChainID: 11155111, Network: "eip155:11155111", Asset: asset,
		Recipient: recipient, TopupTTL: 10 * time.Minute, UsageTTL: 2 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ledger
}

func createReservedTopup(
	t *testing.T,
	prepaid *PrepaidLedger,
	payments *PostgresLedger,
	userID string,
	payer common.Address,
	amount string,
	observedAt time.Time,
	identity byte,
) (TopupIntent, Reservation) {
	t.Helper()
	intent, err := prepaid.CreateTopupIntent(t.Context(), CreateTopupIntentInput{
		UserID: userID, Payer: payer, AmountAtomic: amount, ObservedAt: observedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := testReserveInput()
	input.Fingerprint[0] ^= identity
	input.ResourceDigest[0] ^= identity
	input.ObservedAt = observedAt
	input.Operation = "createBillingTopup"
	input.Method = http.MethodPost
	input.Purpose = "account_topup"
	input.AssetTransferMethod = "eip3009"
	input.PaymentFlow = "authorization"
	input.FingerprintVersion = 2
	input.TopupIntentID = &intent.ID
	input.UserID = &userID
	input.ExpectedPayer = payer
	input.Network = intent.Network
	input.Asset = intent.Asset
	input.AmountAtomic = intent.AmountAtomic
	input.Recipient = intent.Recipient
	reservation, err := payments.ReserveTopup(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	return intent, reservation
}

func insertPrepaidAPIKey(
	t *testing.T,
	db *sql.DB,
	prefix, userID string,
	createdAt time.Time,
) {
	t.Helper()
	var digest Digest
	digest[0] = prefix[0]
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO api_keys (
			prefix, digest, name, rate_per_second, burst, created_at,
			owner_user_id, scopes
		) VALUES ($1, $2, $1, 10, 20, $3, $4::uuid, ARRAY['api:read']::text[])
	`, prefix, digest[:], createdAt, userID); err != nil {
		t.Fatalf("insert prepaid API key: %v", err)
	}
}

func assertBillingAccountAmounts(
	t *testing.T,
	ledger *PrepaidLedger,
	userID, credit, debit, available string,
) {
	t.Helper()
	account, err := ledger.Account(t.Context(), userID)
	if err != nil || account.TotalCreditAtomic != credit ||
		account.TotalDebitAtomic != debit || account.ReservedAtomic != "0" ||
		account.AvailableAtomic != available {
		t.Fatalf("billing account=%+v error=%v", account, err)
	}
}

func assertBillingEntryCount(
	t *testing.T,
	db *sql.DB,
	kind, sourceID string,
	want int,
) {
	t.Helper()
	var count int
	if err := db.QueryRowContext(t.Context(), `
		SELECT count(*) FROM billing_account_entries
		WHERE kind = $1 AND source_id = $2::uuid
	`, kind, sourceID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("billing entry kind=%s source=%s count=%d want=%d", kind, sourceID, count, want)
	}
}
