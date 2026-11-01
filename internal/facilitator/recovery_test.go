package facilitator

import (
	"bytes"
	"context"
	"errors"
	"github.com/ethereum/go-ethereum"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/ethclient"
	dbgen "github.com/islishude/etherview/internal/db/gen"
)

func TestPendingResponseRetainsPayerAndOriginalHash(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer upstream.Close()
	client, err := ethclient.Dial(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ch := &chainRuntime{adapter: &chainClient{client: client, config: testConfig().Chains[0]}}
	row := dbgen.FacilitatorSettlement{Network: ch.adapter.config.Network, TransactionHash: bytes.Repeat([]byte{1}, 32)}
	response, err := (&Service{}).reconcile(context.Background(), ch, row, testRequest())
	if err != nil || response.Success || response.ErrorReason != "settlement_pending" || response.Transaction == "" || response.Payer != "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266" {
		t.Fatalf("pending response lost reconciliation identity: %+v %v", response, err)
	}
}

type receiptRPCError struct {
	code    int
	message string
}

func (e receiptRPCError) Error() string  { return e.message }
func (e receiptRPCError) ErrorCode() int { return e.code }
func TestReceiptIndexingIsReadOnlyRetryable(t *testing.T) {
	for _, test := range []struct {
		err  error
		want bool
	}{
		{ethereum.NotFound, true},
		{receiptRPCError{-32000, "transaction indexing is in progress"}, true},
		{receiptRPCError{-32001, "transaction indexing is in progress"}, false},
		{errors.New("transaction indexing is in progress"), false},
		{context.DeadlineExceeded, false},
	} {
		if receiptNotReady(test.err) != test.want {
			t.Fatalf("incorrect receipt retry classification: %v", test.err)
		}
	}
}
