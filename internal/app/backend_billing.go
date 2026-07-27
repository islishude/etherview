package app

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/billing"
	"github.com/islishude/etherview/internal/config"
)

type adminBillingCommand struct {
	id              string
	outcome         string
	transactionHash *common.Hash
}

type adminBillingOutput struct {
	Status  string                    `json:"status"`
	Outcome *string                   `json:"outcome"`
	Payment adminBillingPaymentOutput `json:"payment"`
	Events  []adminBillingEventOutput `json:"events"`
}

type adminBillingPaymentOutput struct {
	ID                   string  `json:"id"`
	ChainID              string  `json:"chain_id"`
	Operation            string  `json:"operation"`
	Method               string  `json:"method"`
	Network              string  `json:"network"`
	Asset                string  `json:"asset"`
	AmountAtomic         string  `json:"amount_atomic"`
	Recipient            string  `json:"recipient"`
	Payer                *string `json:"payer"`
	UserID               *string `json:"user_id"`
	APIKeyPrefix         *string `json:"api_key_prefix"`
	TransactionHash      *string `json:"transaction_hash"`
	State                string  `json:"state"`
	FailureCode          *string `json:"failure_code"`
	ReservationExpiresAt string  `json:"reservation_expires_at"`
	HandlerStartedAt     *string `json:"handler_started_at"`
	VerifiedAt           *string `json:"verified_at"`
	SettlingAt           *string `json:"settling_at"`
	SettledAt            *string `json:"settled_at"`
	FailedAt             *string `json:"failed_at"`
	ExpiredAt            *string `json:"expired_at"`
	CreatedAt            string  `json:"created_at"`
	UpdatedAt            string  `json:"updated_at"`
}

type adminBillingEventOutput struct {
	ID              string  `json:"id"`
	PaymentID       string  `json:"payment_id"`
	FromState       *string `json:"from_state"`
	ToState         string  `json:"to_state"`
	Code            string  `json:"code"`
	Actor           string  `json:"actor"`
	TransactionHash *string `json:"transaction_hash"`
	OccurredAt      string  `json:"occurred_at"`
}

func (b *Backend) adminBilling(
	ctx context.Context,
	db *sql.DB,
	cfg config.Config,
	action string,
	args []string,
) error {
	command, err := parseAdminBillingCommand(action, args)
	if err != nil {
		return err
	}
	ledger, err := billing.NewPostgresLedger(
		db, cfg.Chain.ID, cfg.Billing.ReservationTTL,
	)
	if err != nil {
		return errors.New("configure billing ledger administration")
	}

	status := "inspected"
	var outcome *string
	var inspection billing.Inspection
	switch action {
	case "inspect":
		inspection, err = ledger.Inspect(ctx, command.id)
		if err != nil {
			return adminBillingOperationError(err)
		}
	case "reconcile":
		operationAt := time.Now().UTC().Truncate(time.Microsecond)
		var payment billing.Payment
		switch command.outcome {
		case "settled":
			payment, err = ledger.ReconcileSettled(
				ctx, command.id, *command.transactionHash, operationAt,
			)
		case "failed":
			payment, err = ledger.ReconcileFailed(
				ctx, command.id, operationAt,
			)
		}
		if err != nil {
			return adminBillingOperationError(err)
		}
		status = "reconciled"
		value := command.outcome
		outcome = &value
		// The reconciliation transition and its event are already atomic.
		// Returning that transaction's payment avoids a second database read
		// that could make a committed operator outcome look unsuccessful.
		inspection = billing.Inspection{
			Payment: payment, Events: []billing.PaymentEvent{},
		}
	default:
		return errors.New(
			"billing admin action must be inspect or reconcile",
		)
	}

	output, err := newAdminBillingOutput(status, outcome, inspection)
	if err != nil {
		return errors.New("billing ledger contains invalid operator output")
	}
	return writeIndentedJSON(b.output(), output)
}

func parseAdminBillingCommand(
	action string,
	args []string,
) (adminBillingCommand, error) {
	switch action {
	case "inspect":
		if err := rejectDuplicateAdminBillingFlags(args); err != nil {
			return adminBillingCommand{}, err
		}
		fs := flag.NewFlagSet("admin billing inspect", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		id := fs.String("id", "", "payment UUID")
		if err := fs.Parse(args); err != nil {
			return adminBillingCommand{}, err
		}
		if fs.NArg() != 0 {
			return adminBillingCommand{}, errors.New(
				"billing inspect does not accept positional arguments",
			)
		}
		normalized, err := canonicalBillingPaymentID(*id)
		if err != nil {
			return adminBillingCommand{}, err
		}
		return adminBillingCommand{id: normalized}, nil
	case "reconcile":
		if err := rejectDuplicateAdminBillingFlags(args); err != nil {
			return adminBillingCommand{}, err
		}
		fs := flag.NewFlagSet("admin billing reconcile", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		id := fs.String("id", "", "payment UUID")
		outcome := fs.String(
			"outcome", "", "reconciliation outcome: settled or failed",
		)
		transactionHash := fs.String(
			"transaction-hash", "", "settled transaction hash",
		)
		if err := fs.Parse(args); err != nil {
			return adminBillingCommand{}, err
		}
		if fs.NArg() != 0 {
			return adminBillingCommand{}, errors.New(
				"billing reconcile does not accept positional arguments",
			)
		}
		normalized, err := canonicalBillingPaymentID(*id)
		if err != nil {
			return adminBillingCommand{}, err
		}
		command := adminBillingCommand{id: normalized, outcome: *outcome}
		switch command.outcome {
		case "settled":
			hash, hashErr := parseAdminBillingTransactionHash(
				*transactionHash,
			)
			if hashErr != nil {
				return adminBillingCommand{}, hashErr
			}
			command.transactionHash = &hash
		case "failed":
			if flagWasSet(fs, "transaction-hash") {
				return adminBillingCommand{}, errors.New(
					"billing reconcile --transaction-hash is forbidden for failed outcome",
				)
			}
		default:
			return adminBillingCommand{}, errors.New(
				"billing reconcile --outcome must be settled or failed",
			)
		}
		return command, nil
	default:
		return adminBillingCommand{}, errors.New(
			"billing admin action must be inspect or reconcile",
		)
	}
}

func canonicalBillingPaymentID(value string) (string, error) {
	identifier, err := uuid.Parse(value)
	if err != nil || identifier.Version() != 4 ||
		identifier.String() != value {
		return "", errors.New(
			"billing admin command requires a canonical v4 UUID --id",
		)
	}
	return identifier.String(), nil
}

func parseAdminBillingTransactionHash(
	value string,
) (common.Hash, error) {
	var result common.Hash
	if len(value) != 2+common.HashLength*2 || !strings.HasPrefix(value, "0x") {
		return result, errors.New(
			"billing reconcile --transaction-hash must be a 32-byte hexadecimal hash",
		)
	}
	raw, err := hex.DecodeString(value[2:])
	if err != nil || len(raw) != common.HashLength {
		return result, errors.New(
			"billing reconcile --transaction-hash must be a 32-byte hexadecimal hash",
		)
	}
	copy(result[:], raw)
	if result == (common.Hash{}) {
		return result, errors.New(
			"billing reconcile --transaction-hash must not be zero",
		)
	}
	return result, nil
}

func rejectDuplicateAdminBillingFlags(args []string) error {
	seen := make(map[string]bool)
	for _, argument := range args {
		if !strings.HasPrefix(argument, "--") || argument == "--" {
			continue
		}
		name := strings.TrimPrefix(argument, "--")
		if index := strings.IndexByte(name, '='); index >= 0 {
			name = name[:index]
		}
		switch name {
		case "id", "outcome", "transaction-hash":
			if seen[name] {
				return fmt.Errorf(
					"billing admin flag --%s may only be supplied once",
					name,
				)
			}
			seen[name] = true
		}
	}
	return nil
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(item *flag.Flag) {
		if item.Name == name {
			found = true
		}
	})
	return found
}

func adminBillingOperationError(err error) error {
	switch {
	case errors.Is(err, billing.ErrNotFound):
		return errors.New("billing payment was not found")
	case errors.Is(err, billing.ErrStateConflict):
		return errors.New("billing payment is not reconcilable")
	case errors.Is(err, billing.ErrInvalidInput):
		return errors.New("billing operation input is invalid")
	case errors.Is(err, billing.ErrIntegrity):
		return errors.New("billing ledger integrity check failed")
	default:
		return errors.New("billing ledger operation failed")
	}
}

func newAdminBillingOutput(
	status string,
	outcome *string,
	inspection billing.Inspection,
) (adminBillingOutput, error) {
	payment, err := adminBillingPaymentModel(inspection.Payment)
	if err != nil {
		return adminBillingOutput{}, err
	}
	events := make([]adminBillingEventOutput, len(inspection.Events))
	var previousEventID int64
	for index, event := range inspection.Events {
		if event.PaymentID != inspection.Payment.ID ||
			event.ID <= previousEventID {
			return adminBillingOutput{}, errors.New(
				"billing inspection events are not ordered",
			)
		}
		model, eventErr := adminBillingEventModel(event)
		if eventErr != nil {
			return adminBillingOutput{}, eventErr
		}
		events[index] = model
		previousEventID = event.ID
	}
	return adminBillingOutput{
		Status: status, Outcome: outcome, Payment: payment, Events: events,
	}, nil
}

func adminBillingPaymentModel(
	payment billing.Payment,
) (adminBillingPaymentOutput, error) {
	if payment.ID == "" || payment.ChainID == 0 ||
		payment.Operation == "" || payment.Method != "GET" ||
		payment.Network == "" || payment.AmountAtomic == "" ||
		payment.ReservationExpiresAt.IsZero() ||
		payment.CreatedAt.IsZero() || payment.UpdatedAt.IsZero() {
		return adminBillingPaymentOutput{}, errors.New(
			"billing payment output is invalid",
		)
	}
	return adminBillingPaymentOutput{
		ID: payment.ID, ChainID: strconv.FormatUint(payment.ChainID, 10),
		Operation: payment.Operation, Method: payment.Method,
		Network: payment.Network, Asset: billingAddressString(payment.Asset),
		AmountAtomic: payment.AmountAtomic,
		Recipient:    billingAddressString(payment.Recipient),
		Payer:        optionalBillingAddressString(payment.Payer),
		UserID:       cloneBillingOutputString(payment.UserID),
		APIKeyPrefix: cloneBillingOutputString(payment.APIKeyPrefix),
		TransactionHash: optionalBillingHashString(
			payment.TransactionHash,
		),
		State: string(payment.State), FailureCode: cloneBillingOutputString(
			payment.FailureCode,
		),
		ReservationExpiresAt: billingTimeString(
			payment.ReservationExpiresAt,
		),
		HandlerStartedAt: billingOptionalTimeString(
			payment.HandlerStartedAt,
		),
		VerifiedAt: billingOptionalTimeString(payment.VerifiedAt),
		SettlingAt: billingOptionalTimeString(payment.SettlingAt),
		SettledAt:  billingOptionalTimeString(payment.SettledAt),
		FailedAt:   billingOptionalTimeString(payment.FailedAt),
		ExpiredAt:  billingOptionalTimeString(payment.ExpiredAt),
		CreatedAt:  billingTimeString(payment.CreatedAt),
		UpdatedAt:  billingTimeString(payment.UpdatedAt),
	}, nil
}

func adminBillingEventModel(
	event billing.PaymentEvent,
) (adminBillingEventOutput, error) {
	if event.ID <= 0 || event.PaymentID == "" ||
		event.ToState == "" || event.Code == "" || event.Actor == "" ||
		event.OccurredAt.IsZero() {
		return adminBillingEventOutput{}, errors.New(
			"billing event output is invalid",
		)
	}
	var fromState *string
	if event.FromState != nil {
		value := string(*event.FromState)
		fromState = &value
	}
	return adminBillingEventOutput{
		ID: strconv.FormatInt(event.ID, 10), PaymentID: event.PaymentID,
		FromState: fromState, ToState: string(event.ToState),
		Code: event.Code, Actor: string(event.Actor),
		TransactionHash: optionalBillingHashString(
			event.TransactionHash,
		),
		OccurredAt: billingTimeString(event.OccurredAt),
	}, nil
}

func billingAddressString(value common.Address) string {
	return "0x" + hex.EncodeToString(value[:])
}

func optionalBillingAddressString(value *common.Address) *string {
	if value == nil {
		return nil
	}
	result := billingAddressString(*value)
	return &result
}

func optionalBillingHashString(value *common.Hash) *string {
	if value == nil {
		return nil
	}
	result := "0x" + hex.EncodeToString(value[:])
	return &result
}

func billingTimeString(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func billingOptionalTimeString(value *time.Time) *string {
	if value == nil {
		return nil
	}
	result := billingTimeString(*value)
	return &result
}

func cloneBillingOutputString(value *string) *string {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
