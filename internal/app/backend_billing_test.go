package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/billing"
)

const testAdminBillingID = "00000000-0000-4000-8000-000000000001"

func TestParseAdminBillingCommand(t *testing.T) {
	t.Parallel()
	hash := "0x" + strings.Repeat("12", 32)
	tests := []struct {
		name        string
		action      string
		args        []string
		wantOutcome string
		wantHash    bool
	}{
		{
			name: "inspect", action: "inspect",
			args: []string{"--id", testAdminBillingID},
		},
		{
			name: "settled", action: "reconcile",
			args: []string{
				"--transaction-hash=" + hash,
				"--id=" + testAdminBillingID,
				"--outcome", "settled",
			},
			wantOutcome: "settled", wantHash: true,
		},
		{
			name: "failed", action: "reconcile",
			args: []string{
				"--outcome=failed", "--id", testAdminBillingID,
			},
			wantOutcome: "failed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command, err := parseAdminBillingCommand(
				test.action, test.args,
			)
			if err != nil {
				t.Fatal(err)
			}
			if command.id != testAdminBillingID ||
				command.outcome != test.wantOutcome ||
				(command.transactionHash != nil) != test.wantHash {
				t.Fatalf("command = %+v", command)
			}
			if test.wantHash &&
				"0x"+bytesToLowerHex(command.transactionHash[:]) != hash {
				t.Fatalf(
					"transaction hash = %x",
					command.transactionHash[:],
				)
			}
		})
	}
}

func TestParseAdminBillingCommandRejectsMalformedInput(t *testing.T) {
	t.Parallel()
	validHash := "0x" + strings.Repeat("12", 32)
	tests := []struct {
		name   string
		action string
		args   []string
		want   string
	}{
		{"unknown action", "delete", nil, "action must be"},
		{"missing inspect id", "inspect", nil, "canonical v4 UUID"},
		{
			"non-v4 id", "inspect",
			[]string{"--id", "00000000-0000-1000-8000-000000000001"},
			"canonical v4 UUID",
		},
		{
			"uppercase id", "inspect",
			[]string{"--id", "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA"},
			"canonical v4 UUID",
		},
		{
			"inspect positional", "inspect",
			[]string{"--id", testAdminBillingID, "extra"},
			"does not accept positional",
		},
		{
			"inspect unknown flag", "inspect",
			[]string{"--id", testAdminBillingID, "--outcome", "failed"},
			"flag provided but not defined",
		},
		{
			"duplicate id", "inspect",
			[]string{
				"--id", testAdminBillingID,
				"--id=" + testAdminBillingID,
			},
			"may only be supplied once",
		},
		{
			"missing outcome", "reconcile",
			[]string{"--id", testAdminBillingID},
			"--outcome must be",
		},
		{
			"invalid outcome", "reconcile",
			[]string{"--id", testAdminBillingID, "--outcome", "unknown"},
			"--outcome must be",
		},
		{
			"settled missing hash", "reconcile",
			[]string{
				"--id", testAdminBillingID, "--outcome", "settled",
			},
			"32-byte hexadecimal hash",
		},
		{
			"settled short hash", "reconcile",
			[]string{
				"--id", testAdminBillingID, "--outcome", "settled",
				"--transaction-hash", "0x12",
			},
			"32-byte hexadecimal hash",
		},
		{
			"settled zero hash", "reconcile",
			[]string{
				"--id", testAdminBillingID, "--outcome", "settled",
				"--transaction-hash", "0x" + strings.Repeat("00", 32),
			},
			"must not be zero",
		},
		{
			"failed hash", "reconcile",
			[]string{
				"--id", testAdminBillingID, "--outcome", "failed",
				"--transaction-hash", validHash,
			},
			"forbidden for failed",
		},
		{
			"failed empty hash flag", "reconcile",
			[]string{
				"--id", testAdminBillingID, "--outcome", "failed",
				"--transaction-hash=",
			},
			"forbidden for failed",
		},
		{
			"duplicate outcome", "reconcile",
			[]string{
				"--id", testAdminBillingID, "--outcome", "failed",
				"--outcome=failed",
			},
			"may only be supplied once",
		},
		{
			"reconcile positional", "reconcile",
			[]string{
				"--id", testAdminBillingID, "--outcome", "failed", "extra",
			},
			"does not accept positional",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseAdminBillingCommand(test.action, test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf(
					"error = %v, want substring %q", err, test.want,
				)
			}
		})
	}
}

func TestAdminBillingOutputIsSafeAndOrdered(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(
		2026, 7, 26, 11, 12, 13, 123456000, time.UTC,
	)
	handlerAt := createdAt.Add(time.Second)
	verifiedAt := createdAt.Add(2 * time.Second)
	settlingAt := createdAt.Add(3 * time.Second)
	failureCode := "settlement_unknown"
	userID := "00000000-0000-4000-8000-000000000099"
	apiKeyPrefix := "abcdefghij"
	payer := billing.Address{19: 0x33}
	hash := billing.TransactionHash{31: 0x44}
	fromState := billing.StateVerified
	inspection := billing.Inspection{
		Payment: billing.Payment{
			ID: testAdminBillingID, ChainID: 84532,
			Operation: "listBlocks", Method: "GET",
			Network:      "eip155:84532",
			Asset:        billing.Address{19: 0x11},
			AmountAtomic: "1000",
			Recipient:    billing.Address{19: 0x22},
			Payer:        &payer, UserID: &userID, APIKeyPrefix: &apiKeyPrefix,
			State: billing.StateSettling, FailureCode: &failureCode,
			ReservationExpiresAt: createdAt.Add(2 * time.Minute),
			HandlerStartedAt:     &handlerAt, VerifiedAt: &verifiedAt,
			SettlingAt: &settlingAt, CreatedAt: createdAt,
			UpdatedAt: settlingAt,
		},
		Events: []billing.PaymentEvent{
			{
				ID: 7, PaymentID: testAdminBillingID,
				ToState: billing.StateReserved,
				Code:    "payment_reserved", Actor: billing.ActorRuntime,
				OccurredAt: createdAt,
			},
			{
				ID: 9, PaymentID: testAdminBillingID,
				FromState: &fromState, ToState: billing.StateSettling,
				Code: "settlement_unknown", Actor: billing.ActorRuntime,
				TransactionHash: &hash, OccurredAt: settlingAt,
			},
		},
	}
	output, err := newAdminBillingOutput(
		"inspected", nil, inspection,
	)
	if err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := writeIndentedJSON(&encoded, output); err != nil {
		t.Fatal(err)
	}
	text := encoded.String()
	for _, want := range []string{
		`"status": "inspected"`,
		`"outcome": null`,
		`"chain_id": "84532"`,
		`"asset": "0x0000000000000000000000000000000000000011"`,
		`"payer": "0x0000000000000000000000000000000000000033"`,
		`"transaction_hash": "0x0000000000000000000000000000000000000000000000000000000000000044"`,
		`"id": "7"`,
		`"id": "9"`,
		`"failure_code": "settlement_unknown"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("output is missing %q: %s", want, text)
		}
	}
	for _, forbidden := range []string{
		"fingerprint", "resource_digest", "requirement_digest",
		"facilitator_digest", "reservation_owner",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("output exposes %q: %s", forbidden, text)
		}
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(encoded.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if len(document) != 4 {
		t.Fatalf("top-level fields = %v", document)
	}
}

func TestAdminBillingOutputRejectsMisboundOrUnorderedEvents(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	payment := billing.Payment{
		ID: testAdminBillingID, ChainID: 1,
		Operation: "listBlocks", Method: "GET", Network: "eip155:1",
		AmountAtomic: "1", ReservationExpiresAt: now.Add(time.Minute),
		CreatedAt: now, UpdatedAt: now,
	}
	base := billing.PaymentEvent{
		ID: 2, PaymentID: testAdminBillingID,
		ToState: billing.StateReserved, Code: "payment_reserved",
		Actor: billing.ActorRuntime, OccurredAt: now,
	}
	for _, events := range [][]billing.PaymentEvent{
		{
			base,
			{
				ID: 1, PaymentID: testAdminBillingID,
				ToState: billing.StateVerified, Code: "payment_verified",
				Actor: billing.ActorRuntime, OccurredAt: now,
			},
		},
		{
			{
				ID:        1,
				PaymentID: "00000000-0000-4000-8000-000000000002",
				ToState:   billing.StateReserved, Code: "payment_reserved",
				Actor: billing.ActorRuntime, OccurredAt: now,
			},
		},
	} {
		if _, err := newAdminBillingOutput(
			"inspected", nil,
			billing.Inspection{Payment: payment, Events: events},
		); err == nil {
			t.Fatalf("events unexpectedly accepted: %+v", events)
		}
	}
}

func TestAdminBillingOperationErrorsAreStable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err  error
		want string
	}{
		{billing.ErrNotFound, "was not found"},
		{billing.ErrStateConflict, "not reconcilable"},
		{billing.ErrInvalidInput, "input is invalid"},
		{billing.ErrIntegrity, "integrity check failed"},
		{errors.New("postgres://operator:secret@database/etherview"), "operation failed"},
	}
	for _, test := range tests {
		got := adminBillingOperationError(test.err)
		if got.Error() != "billing "+test.want &&
			!strings.Contains(got.Error(), test.want) {
			t.Fatalf("error = %q, want %q", got, test.want)
		}
		if strings.Contains(got.Error(), "secret") {
			t.Fatalf("nested error escaped: %q", got)
		}
	}
}

func bytesToLowerHex(value []byte) string {
	const alphabet = "0123456789abcdef"
	result := make([]byte, len(value)*2)
	for index, item := range value {
		result[index*2] = alphabet[item>>4]
		result[index*2+1] = alphabet[item&0x0f]
	}
	return string(result)
}
