package x402settlement

import exact "github.com/x402-foundation/x402/go/v2/mechanisms/evm/exact/facilitator"

// BusinessReason allows only deterministic rejection codes from the pinned SDK.
func BusinessReason(reason string) string {
	switch reason {
	case exact.ErrInvalidScheme, exact.ErrNetworkMismatch, exact.ErrInvalidPayload, exact.ErrMissingSignature,
		exact.ErrMissingEip712Domain, exact.ErrRecipientMismatch, exact.ErrInvalidAuthorizationValue,
		exact.ErrInvalidRequiredAmount, exact.ErrAuthorizationValueMismatch, exact.ErrInsufficientBalance,
		exact.ErrInvalidSignatureFormat, exact.ErrInvalidSignature, exact.ErrInvalidSignatureS,
		exact.ErrValidBeforeExpired, exact.ErrValidAfterInFuture, exact.ErrEip3009TokenNameMismatch,
		exact.ErrEip3009TokenVersionMismatch, exact.ErrUndeployedSmartWallet, exact.ErrFactoryNotAllowed,
		exact.ErrUnsupportedPayloadType, exact.ErrPermit2InvalidSpender, exact.ErrPermit2RecipientMismatch,
		exact.ErrPermit2DeadlineExpired, exact.ErrPermit2NotYetValid, exact.ErrPermit2AmountMismatch,
		exact.ErrPermit2TokenMismatch, exact.ErrPermit2InvalidSignature, exact.ErrPermit2AllowanceRequired,
		exact.ErrPermit2InsufficientBalance:
		return reason
	default:
		return ""
	}
}
