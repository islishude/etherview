package facilitator

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/jsonstrict"
	"github.com/islishude/etherview/internal/x402settlement"
	"github.com/jackc/pgx/v5"
	x402 "github.com/x402-foundation/x402/go/v2"
)

func (s *Service) result(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 2049))
	var ref x402settlement.Reference
	if err != nil || len(body) > 2048 || jsonstrict.Validate(body, jsonstrict.Limits{MaxDepth: 2, MaxNodes: 8, SafeIntegersOnly: true}) != nil {
		http.Error(w, "invalid_lookup", http.StatusBadRequest)
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&ref) != nil || len(ref.AuthorizationID) == 0 || len(ref.AuthorizationID) > 512 || len(ref.RequestDigest) != 64 {
		http.Error(w, "invalid_lookup", http.StatusBadRequest)
		return
	}
	digest, err := hex.DecodeString(ref.RequestDigest)
	if err != nil {
		http.Error(w, "invalid_lookup", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	row, err := s.queries.FacilitatorGet(ctx, ref.AuthorizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "settlement_not_found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "journal_unavailable", http.StatusServiceUnavailable)
		return
	}
	if !bytes.Equal(digest, row.RequestDigest) {
		http.Error(w, "authorization_conflict", http.StatusConflict)
		return
	}
	result := x402settlement.Result{Reference: ref, State: "pending", TransactionConfirmed: row.TransactionConfirmed}
	if len(row.TransactionHash) > 0 {
		result.OriginalTransaction = common.BytesToHash(row.TransactionHash).Hex()
	}
	if len(row.Response) > 0 {
		var response x402.SettleResponse
		if json.Unmarshal(row.Response, &response) != nil {
			http.Error(w, "journal_unavailable", http.StatusServiceUnavailable)
			return
		}
		result.Response = &response
		result.State = row.State
	}
	if len(row.SettlementEvidence) > 0 {
		var evidence x402settlement.Evidence
		if json.Unmarshal(row.SettlementEvidence, &evidence) != nil {
			http.Error(w, "journal_unavailable", http.StatusServiceUnavailable)
			return
		}
		result.Evidence = &evidence
	}
	respond(w, result)
}
