package facilitator

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/x402settlement"
	x402 "github.com/x402-foundation/x402/go/v2"
)

type readOnlyResultJournal struct {
	settlementJournal
	row dbgen.FacilitatorSettlement
}

func (j readOnlyResultJournal) FacilitatorGet(context.Context, string) (dbgen.FacilitatorSettlement, error) {
	return j.row, nil
}

func TestResultIsAuthenticatedBoundAndReadOnly(t *testing.T) {
	digest := bytes.Repeat([]byte{1}, 32)
	original, external := common.HexToHash("0x11"), common.HexToHash("0x22")
	row := dbgen.FacilitatorSettlement{AuthorizationID: "authorization", RequestDigest: digest, State: "succeeded", TransactionHash: original.Bytes()}
	row.Response, _ = json.Marshal(x402.SettleResponse{Success: true, Transaction: external.Hex(), Network: "eip155:31337"})
	row.SettlementEvidence, _ = json.Marshal(x402settlement.Evidence{Transaction: external.Hex(), Source: "external"})
	service := &Service{token: []byte("test-token"), queries: readOnlyResultJournal{row: row}}
	// No RPC, signer or write methods are supplied, even though the original
	// transaction remains unconfirmed. Result reads cannot perform recovery.
	for _, scenario := range []struct {
		name, token, digest string
		status              int
	}{
		{"unauthenticated", "", hex.EncodeToString(digest), http.StatusUnauthorized},
		{"wrong digest", "test-token", hex.EncodeToString(bytes.Repeat([]byte{2}, 32)), http.StatusConflict},
		{"bound result", "test-token", hex.EncodeToString(digest), http.StatusOK},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			body, _ := json.Marshal(x402settlement.Reference{AuthorizationID: row.AuthorizationID, RequestDigest: scenario.digest})
			request := httptest.NewRequest(http.MethodPost, "/result", bytes.NewReader(body))
			if scenario.token != "" {
				request.Header.Set("Authorization", "Bearer "+scenario.token)
			}
			response := httptest.NewRecorder()
			service.Handler().ServeHTTP(response, request)
			if response.Code != scenario.status {
				t.Fatal(response.Code, response.Body.String())
			}
			if response.Code == http.StatusOK {
				var result x402settlement.Result
				if json.Unmarshal(response.Body.Bytes(), &result) != nil || result.OriginalTransaction != original.Hex() || result.TransactionConfirmed || result.Response.Transaction != external.Hex() || result.Evidence.Transaction != external.Hex() {
					t.Fatal(result)
				}
			}
		})
	}
}
