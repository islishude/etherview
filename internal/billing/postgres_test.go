package billing

import (
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestReserveInputUsesClosedOperationAndCanonicalUint256(t *testing.T) {
	t.Parallel()
	valid := testReserveInput()
	if err := validateReserveInput(valid); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ReserveInput){
		func(input *ReserveInput) { input.Operation = "getStatus" },
		func(input *ReserveInput) { input.Operation = "futureOperation" },
		func(input *ReserveInput) { input.Network = "eip155:01" },
		func(input *ReserveInput) { input.AmountAtomic = "0" },
		func(input *ReserveInput) { input.AmountAtomic = "01" },
		func(input *ReserveInput) {
			input.AmountAtomic = new(big.Int).Add(maximumUint256, big.NewInt(1)).String()
		},
		func(input *ReserveInput) { input.ObservedAt = time.Time{} },
		func(input *ReserveInput) {
			value := "0000000000"
			input.APIKeyPrefix = &value
		},
	} {
		candidate := valid
		mutate(&candidate)
		if err := validateReserveInput(candidate); err == nil {
			t.Fatalf("invalid reserve input passed: %+v", candidate)
		}
	}
}

func TestIntegerNumericStringRejectsFractionsAndNormalizesExponent(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		numeric pgtype.Numeric
		want    string
		ok      bool
	}{
		{numeric: pgtype.Numeric{Int: big.NewInt(123), Valid: true}, want: "123", ok: true},
		{numeric: pgtype.Numeric{Int: big.NewInt(123), Exp: 2, Valid: true}, want: "12300", ok: true},
		{numeric: pgtype.Numeric{Int: big.NewInt(12300), Exp: -2, Valid: true}, want: "123", ok: true},
		{numeric: pgtype.Numeric{Int: big.NewInt(123), Exp: -2, Valid: true}, ok: false},
		{numeric: pgtype.Numeric{Int: big.NewInt(-1), Valid: true}, ok: false},
		{numeric: pgtype.Numeric{}, ok: false},
	} {
		got, err := integerNumericString(test.numeric)
		if test.ok && (err != nil || got != test.want) {
			t.Errorf("integer numeric=%+v got=%q error=%v", test.numeric, got, err)
		}
		if !test.ok && err == nil {
			t.Errorf("invalid numeric=%+v got=%q", test.numeric, got)
		}
	}
}

func TestFailureCodesAndPaginationAreBounded(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"handler_failed", "settlement_rejected", "a"} {
		if !validFailureCode(value) {
			t.Errorf("valid failure code rejected: %q", value)
		}
	}
	for _, value := range []string{
		"", "UPPER", "has-dash", strings.Repeat("a", 129),
	} {
		if validFailureCode(value) {
			t.Errorf("invalid failure code passed: %q", value)
		}
	}
	createdAt := pgtype.Timestamptz{}
	id := pgtype.UUID{}
	if err := applyPageAfter(&PageAfter{ID: "bad"}, &createdAt, &id); err == nil {
		t.Fatal("malformed page position passed")
	}
}

func TestPaymentFromRowRejectsHostileDurableFacts(t *testing.T) {
	t.Parallel()
	ledger := &PostgresLedger{chainID: 11155111}
	valid := testBillingPaymentRow()
	if _, err := ledger.paymentFromRow(valid); err != nil {
		t.Fatalf("valid row: %v", err)
	}
	tests := map[string]func(*dbgen.BillingPayment){
		"zero payment ID": func(row *dbgen.BillingPayment) {
			row.ID = uuidValue(uuid.Nil)
		},
		"ineligible operation": func(row *dbgen.BillingPayment) {
			row.Operation = "getStatus"
		},
		"oversized uint256": func(row *dbgen.BillingPayment) {
			row.AmountAtomic = pgtype.Numeric{
				Int:   new(big.Int).Add(maximumUint256, big.NewInt(1)),
				Valid: true,
			}
		},
		"short asset": func(row *dbgen.BillingPayment) {
			row.Asset = row.Asset[:19]
		},
		"invalid API key prefix": func(row *dbgen.BillingPayment) {
			value := "not_base32"
			row.ApiKeyPrefix = &value
		},
		"invalid failure code": func(row *dbgen.BillingPayment) {
			value := "HOSTILE"
			row.FailureCode = &value
		},
		"zero transaction hash": func(row *dbgen.BillingPayment) {
			makeSettledBillingPaymentRow(row)
			row.TransactionHash = make([]byte, 32)
		},
		"infinite optional time": func(row *dbgen.BillingPayment) {
			row.HandlerStartedAt = pgtype.Timestamptz{
				Valid: true, InfinityModifier: pgtype.Infinity,
			}
		},
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			row := valid
			row.Asset = append([]byte(nil), valid.Asset...)
			row.TransactionHash = append([]byte(nil), valid.TransactionHash...)
			mutate(&row)
			if _, err := ledger.paymentFromRow(row); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func testBillingPaymentRow() dbgen.BillingPayment {
	createdAt := testReserveInput().ObservedAt
	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	owner := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	input := testReserveInput()
	return dbgen.BillingPayment{
		ID:                   uuidValue(id),
		ChainID:              pgtype.Numeric{Int: big.NewInt(11155111), Valid: true},
		Fingerprint:          append([]byte(nil), input.Fingerprint[:]...),
		ReservationOwner:     uuidValue(owner),
		Method:               "GET",
		Operation:            input.Operation,
		ResourceDigest:       append([]byte(nil), input.ResourceDigest[:]...),
		RequirementDigest:    append([]byte(nil), input.RequirementDigest[:]...),
		ProtocolVersion:      2,
		Scheme:               "exact",
		Network:              input.Network,
		Asset:                append([]byte(nil), input.Asset[:]...),
		AmountAtomic:         pgtype.Numeric{Int: big.NewInt(1000), Valid: true},
		Recipient:            append([]byte(nil), input.Recipient[:]...),
		FacilitatorDigest:    append([]byte(nil), input.FacilitatorDigest[:]...),
		State:                string(StateReserved),
		ReservationExpiresAt: postgresTime(createdAt.Add(2 * time.Minute)),
		CreatedAt:            postgresTime(createdAt),
		UpdatedAt:            postgresTime(createdAt),
	}
}

func makeSettledBillingPaymentRow(row *dbgen.BillingPayment) {
	verifiedAt := row.CreatedAt.Time.Add(time.Second)
	handlerAt := verifiedAt.Add(time.Second)
	settlingAt := handlerAt.Add(time.Second)
	settledAt := settlingAt.Add(time.Second)
	row.State = string(StateSettled)
	row.Payer = make([]byte, 20)
	row.Payer[19] = 1
	row.VerifiedAt = postgresTime(verifiedAt)
	row.HandlerStartedAt = postgresTime(handlerAt)
	row.SettlingAt = postgresTime(settlingAt)
	row.SettledAt = postgresTime(settledAt)
	row.UpdatedAt = postgresTime(settledAt)
	row.TransactionHash = make([]byte, 32)
	row.TransactionHash[31] = 1
}

func testReserveInput() ReserveInput {
	var fingerprint, resource, requirement, facilitator Digest
	var asset, recipient Address
	for index := range fingerprint {
		fingerprint[index] = byte(index + 1)
		resource[index] = byte(index + 2)
		requirement[index] = byte(index + 3)
		facilitator[index] = byte(index + 4)
	}
	for index := range asset {
		asset[index] = byte(index + 5)
		recipient[index] = byte(index + 6)
	}
	return ReserveInput{
		Fingerprint: fingerprint, Operation: "listBlocks",
		ResourceDigest: resource, RequirementDigest: requirement,
		Network: "eip155:84532", Asset: asset, AmountAtomic: "1000",
		Recipient: recipient, FacilitatorDigest: facilitator,
		ObservedAt: time.Date(2026, 7, 26, 1, 2, 3, 0, time.UTC),
	}
}
