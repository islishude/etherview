package x402testnet

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/billing"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	testAsset     = "0x3333333333333333333333333333333333333333"
	testRecipient = "0x2222222222222222222222222222222222222222"
	testPayer     = "0x1111111111111111111111111111111111111111"
	testTxHash    = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testPaymentID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
)

type fakeLedgerVerificationStore struct {
	ids          []string
	inspection   billing.Inspection
	findErr      error
	inspectErr   error
	closeErr     error
	findCalls    int
	inspectCalls int
}

func (store *fakeLedgerVerificationStore) find(
	_ context.Context,
	_ ledgerExpectation,
	_ time.Time,
) ([]string, error) {
	store.findCalls++
	return append([]string(nil), store.ids...), store.findErr
}

func (store *fakeLedgerVerificationStore) inspect(
	_ context.Context,
	_ string,
) (billing.Inspection, error) {
	store.inspectCalls++
	return store.inspection, store.inspectErr
}

func (store *fakeLedgerVerificationStore) close() error {
	return store.closeErr
}

func TestWriterFenceRejectsRecoveryAndReadOnlyConnections(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 26, 1, 2, 3, 0, time.UTC)
	validTime := pgtype.Timestamptz{Time: now, Valid: true}
	tests := []struct {
		name       string
		inRecovery bool
		readOnly   string
		fence      pgtype.Timestamptz
		wantCode   string
	}{
		{
			name:       "recovery",
			inRecovery: true, readOnly: "off", fence: validTime,
			wantCode: "ledger_writer_required",
		},
		{
			name:     "read only",
			readOnly: "on", fence: validTime,
			wantCode: "ledger_writer_required",
		},
		{
			name:     "missing fence",
			readOnly: "off", fence: pgtype.Timestamptz{},
			wantCode: "ledger_writer_check_failed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := writerFence(
				test.inRecovery,
				test.readOnly,
				test.fence,
			)
			if got := ErrorCode(err); got != test.wantCode {
				t.Fatalf("ErrorCode() = %q, want %q", got, test.wantCode)
			}
		})
	}
	fence, err := writerFence(false, "off", validTime)
	if err != nil || !fence.Equal(now) {
		t.Fatalf("writerFence() = %v, %v", fence, err)
	}
}

func TestLedgerVerifierRequiresOneExactPayment(t *testing.T) {
	t.Parallel()
	options := validLedgerExpectation(t)
	fence := time.Date(2026, 7, 26, 1, 2, 3, 0, time.UTC)
	tests := []struct {
		name     string
		mutate   func(*fakeLedgerVerificationStore)
		wantCode string
	}{
		{
			name: "missing",
			mutate: func(store *fakeLedgerVerificationStore) {
				store.ids = nil
			},
			wantCode: "ledger_payment_not_found",
		},
		{
			name: "duplicate",
			mutate: func(store *fakeLedgerVerificationStore) {
				store.ids = []string{testPaymentID, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}
			},
			wantCode: "ledger_payment_not_unique",
		},
		{
			name: "lookup failure is redacted",
			mutate: func(store *fakeLedgerVerificationStore) {
				store.findErr = errors.New("postgres://secret@db.example/private")
			},
			wantCode: "ledger_lookup_failed",
		},
		{
			name: "payment mismatch",
			mutate: func(store *fakeLedgerVerificationStore) {
				store.inspection.Payment.AmountAtomic = "999"
			},
			wantCode: "ledger_payment_mismatch",
		},
		{
			name: "settled failure code mismatch",
			mutate: func(store *fakeLedgerVerificationStore) {
				code := "settlement_unknown"
				store.inspection.Payment.FailureCode = &code
			},
			wantCode: "ledger_payment_mismatch",
		},
		{
			name: "event mismatch",
			mutate: func(store *fakeLedgerVerificationStore) {
				store.inspection.Events[2].Code = "payment_verified"
			},
			wantCode: "ledger_event_mismatch",
		},
		{
			name: "extra event",
			mutate: func(store *fakeLedgerVerificationStore) {
				store.inspection.Events = append(
					store.inspection.Events,
					store.inspection.Events[len(store.inspection.Events)-1],
				)
			},
			wantCode: "ledger_event_mismatch",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := &fakeLedgerVerificationStore{
				ids:        []string{testPaymentID},
				inspection: settledInspection(t, options, fence),
			}
			test.mutate(store)
			verifier := &LedgerVerifier{
				store: store, options: options, fence: fence,
			}
			_, err := verifier.Verify(context.Background(), testTxHash)
			if got := ErrorCode(err); got != test.wantCode {
				t.Fatalf("ErrorCode() = %q, want %q", got, test.wantCode)
			}
			if test.wantCode == "ledger_payment_not_unique" &&
				store.inspectCalls != 0 {
				t.Fatal("duplicate lookup inspected a payment")
			}
		})
	}
}

func TestLedgerExpectationRequiresFullDigests(t *testing.T) {
	t.Parallel()
	var resourceDigest, requirementDigest [32]byte
	resourceDigest[31] = 1
	requirementDigest[31] = 2
	options := LedgerOptions{
		ChainID: baseSepoliaChainID, Operation: "listBlocks",
		ResourceDigest:    resourceDigest,
		RequirementDigest: requirementDigest,
		Network:           baseSepoliaNetwork, Asset: testAsset,
		AmountAtomic: "1000", Recipient: testRecipient, Payer: testPayer,
	}
	if _, ok := parseLedgerExpectation(options); !ok {
		t.Fatal("complete expectation was rejected")
	}
	options.ResourceDigest = [32]byte{}
	if _, ok := parseLedgerExpectation(options); ok {
		t.Fatal("zero resource digest was accepted")
	}
	options.ResourceDigest = resourceDigest
	options.RequirementDigest = [32]byte{}
	if _, ok := parseLedgerExpectation(options); ok {
		t.Fatal("zero requirement digest was accepted")
	}
}

func TestLedgerVerifierAcceptsStrictSettledEventChain(t *testing.T) {
	t.Parallel()
	options := validLedgerExpectation(t)
	fence := time.Date(2026, 7, 26, 1, 2, 3, 0, time.UTC)
	inspection := settledInspection(t, options, fence)
	userID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	inspection.Payment.UserID = &userID
	store := &fakeLedgerVerificationStore{
		ids: []string{testPaymentID}, inspection: inspection,
	}
	verifier := &LedgerVerifier{
		store: store, options: options, fence: fence,
	}
	evidence, err := verifier.Verify(context.Background(), testTxHash)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if evidence.PaymentID != testPaymentID ||
		evidence.UserID == nil || *evidence.UserID != userID ||
		evidence.EventCount != 5 ||
		!evidence.CreatedAt.Equal(fence) ||
		evidence.SettledAt.IsZero() {
		t.Fatalf("unexpected evidence: %+v", evidence)
	}
}

func TestLedgerVerifierRejectsWrongFinalEventHash(t *testing.T) {
	t.Parallel()
	options := validLedgerExpectation(t)
	fence := time.Date(2026, 7, 26, 1, 2, 3, 0, time.UTC)
	inspection := settledInspection(t, options, fence)
	wrong, ok := parseTransactionHash(
		"0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	)
	if !ok {
		t.Fatal("test hash did not parse")
	}
	inspection.Events[4].TransactionHash = &wrong
	verifier := &LedgerVerifier{
		store: &fakeLedgerVerificationStore{
			ids: []string{testPaymentID}, inspection: inspection,
		},
		options: options,
		fence:   fence,
	}
	_, err := verifier.Verify(context.Background(), testTxHash)
	if got := ErrorCode(err); got != "ledger_event_mismatch" {
		t.Fatalf("ErrorCode() = %q", got)
	}
}

func validLedgerExpectation(t *testing.T) ledgerExpectation {
	t.Helper()
	var resourceDigest, requirementDigest [32]byte
	resourceDigest[31] = 1
	requirementDigest[31] = 2
	result, ok := parseLedgerExpectation(LedgerOptions{
		ChainID: baseSepoliaChainID, Operation: "listBlocks",
		ResourceDigest:    resourceDigest,
		RequirementDigest: requirementDigest,
		Network:           baseSepoliaNetwork, Asset: testAsset,
		AmountAtomic: "1000", Recipient: testRecipient, Payer: testPayer,
	})
	if !ok {
		t.Fatal("valid ledger expectation did not parse")
	}
	return result
}

func settledInspection(
	t *testing.T,
	expected ledgerExpectation,
	createdAt time.Time,
) billing.Inspection {
	t.Helper()
	transactionHash, ok := parseTransactionHash(testTxHash)
	if !ok {
		t.Fatal("test transaction hash did not parse")
	}
	payer := expected.payer
	verifiedAt := createdAt.Add(time.Second)
	handlerAt := createdAt.Add(2 * time.Second)
	settlingAt := createdAt.Add(3 * time.Second)
	settledAt := createdAt.Add(4 * time.Second)
	payment := billing.Payment{
		ID: testPaymentID, ChainID: expected.chainID,
		Operation: expected.operation, Method: "GET",
		Network: expected.network, Asset: expected.asset,
		AmountAtomic: expected.amountAtomic, Recipient: expected.recipient,
		Payer: &payer, TransactionHash: &transactionHash,
		State:                billing.StateSettled,
		ReservationExpiresAt: createdAt.Add(2 * time.Minute),
		VerifiedAt:           &verifiedAt, HandlerStartedAt: &handlerAt,
		SettlingAt: &settlingAt, SettledAt: &settledAt,
		CreatedAt: createdAt, UpdatedAt: settledAt,
	}
	events := []billing.PaymentEvent{
		{
			ID: 1, PaymentID: testPaymentID,
			ToState: billing.StateReserved, Code: "payment_reserved",
			Actor: billing.ActorRuntime, OccurredAt: createdAt,
		},
		{
			ID: 2, PaymentID: testPaymentID,
			FromState: new(billing.StateReserved),
			ToState:   billing.StateVerified, Code: "payment_verified",
			Actor: billing.ActorRuntime, OccurredAt: verifiedAt,
		},
		{
			ID: 3, PaymentID: testPaymentID,
			FromState: new(billing.StateVerified),
			ToState:   billing.StateVerified, Code: "handler_started",
			Actor: billing.ActorRuntime, OccurredAt: handlerAt,
		},
		{
			ID: 4, PaymentID: testPaymentID,
			FromState: new(billing.StateVerified),
			ToState:   billing.StateSettling, Code: "settlement_started",
			Actor: billing.ActorRuntime, OccurredAt: settlingAt,
		},
		{
			ID: 5, PaymentID: testPaymentID,
			FromState: new(billing.StateSettling),
			ToState:   billing.StateSettled, Code: "payment_settled",
			Actor: billing.ActorRuntime, TransactionHash: &transactionHash,
			OccurredAt: settledAt,
		},
	}
	return billing.Inspection{Payment: payment, Events: events}
}
