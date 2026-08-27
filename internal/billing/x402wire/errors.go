package x402wire

import "errors"

// Phase identifies the hostile boundary that rejected an operation.
type Phase string

const (
	PhaseHeader      Phase = "header"
	PhaseRequirement Phase = "requirement"
	PhaseSupported   Phase = "supported"
	PhaseVerify      Phase = "verify"
	PhaseSettle      Phase = "settle"
)

// FailureClass lets the billing state machine distinguish a definite
// rejection from an unavailable verifier and from an indeterminate settlement.
type FailureClass string

const (
	FailureInvalid           FailureClass = "invalid"
	FailureRejected          FailureClass = "rejected"
	FailureUnavailable       FailureClass = "unavailable"
	FailureSettlementUnknown FailureClass = "settlement_unknown"
	FailureSettlementPending FailureClass = "settlement_pending"
)

const (
	CodeHeaderMissing              = "x402_payment_header_missing"
	CodeHeaderMultiple             = "x402_payment_header_multiple"
	CodeHeaderOversized            = "x402_payment_header_oversized"
	CodeHeaderMalformed            = "x402_payment_header_malformed"
	CodePaymentUnsupported         = "x402_payment_unsupported"
	CodePaymentMismatch            = "x402_payment_mismatch"
	CodeRequirementInvalid         = "x402_requirement_invalid"
	CodeFingerprintInvalid         = "x402_fingerprint_invalid"
	CodeFacilitatorConfigInvalid   = "x402_facilitator_config_invalid"
	CodeFacilitatorUnavailable     = "x402_facilitator_unavailable"
	CodeFacilitatorUnsupported     = "x402_facilitator_unsupported"
	CodeFacilitatorRejected        = "x402_facilitator_rejected"
	CodeFacilitatorResponseInvalid = "x402_facilitator_response_invalid"
	CodeSettlementUnknown          = "x402_settlement_unknown"
	CodeSettlementPending          = "x402_settlement_pending"
)

// BoundaryError deliberately contains only closed values. It never wraps a
// network, JSON, or remote error because those messages may contain credentials
// or hostile input.
type BoundaryError struct {
	Phase Phase
	Class FailureClass
	Code  string
}

func (e *BoundaryError) Error() string {
	if e == nil || e.Code == "" {
		return "x402_boundary_error"
	}
	return e.Code
}

func boundaryError(phase Phase, class FailureClass, code string) error {
	return &BoundaryError{Phase: phase, Class: class, Code: code}
}

// IsFailure reports whether err is a stable x402 boundary failure of class.
func IsFailure(err error, class FailureClass) bool {
	var target *BoundaryError
	return errors.As(err, &target) && target.Class == class
}
