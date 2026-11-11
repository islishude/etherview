package billing

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/billing/x402wire"
	x402 "github.com/x402-foundation/x402/go/v2"
)

func TestTopupPendingEncodingFailureRetainsHash(t *testing.T) {
	t.Parallel()
	dispatcher, _, accounts, facilitator, _, intent := testTopupDispatcher(t)
	requirements, err := dispatcher.requirements(intent)
	if err != nil {
		t.Fatal(err)
	}
	hash := common.HexToHash("0x" + strings.Repeat("ab", 32))
	facilitator.verify = &x402.VerifyResponse{IsValid: true, Payer: intent.Payer.Hex()}
	facilitator.settle = &x402.SettleResponse{
		Success: false, ErrorReason: "settlement_pending", Transaction: hash.Hex(),
		Network: x402.Network(intent.Network), Payer: intent.Payer.Hex(), Amount: intent.AmountAtomic,
		// Exercise an encoder failure after a hash is durably retained. The HTTP
		// client normally strips unsupported fields before this boundary.
		ErrorMessage: strings.Repeat("x", 513),
	}
	facilitator.settleErr = &x402wire.BoundaryError{
		Phase: x402wire.PhaseSettle, Class: x402wire.FailureSettlementPending,
		Code: x402wire.CodeSettlementPending,
	}
	request := httptest.NewRequest(http.MethodPost, requirements[0].Resource().URL, nil)
	request.Header.Set(x402wire.PaymentSignatureHeader, topupPaymentHeader(t, requirements[0], intent.Payer))
	response := httptest.NewRecorder()
	dispatcher.Serve(response, request, intent, func(http.ResponseWriter, Account, TopupIntent, Payment) {
		t.Fatal("pending payment credited")
	}, nil)
	if response.Code != http.StatusServiceUnavailable || response.Header().Get(x402wire.PaymentResponseHeader) != "" ||
		!strings.Contains(response.Body.String(), x402wire.CodeSettlementUnknown) {
		t.Fatalf("encoding failure reported as valid pending response: status=%d headers=%v body=%s", response.Code, response.Header(), response.Body)
	}
	if accounts.pendingCalls != 1 || accounts.pendingHash != hash || accounts.creditCalls != 0 ||
		accounts.failCalls != 0 || accounts.unknownCalls != 0 || facilitator.settleCalls != 1 {
		t.Fatalf("encoding failure changed durable payment or resubmitted it: accounts=%+v settle_calls=%d", accounts, facilitator.settleCalls)
	}
}
