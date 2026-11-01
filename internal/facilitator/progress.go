package facilitator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/common"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
)

// progress reads only the journal. In particular it never enters the settlement
// gate, reconciles receipts, signs, or rebroadcasts an authorization.
func (s *Service) progress(w http.ResponseWriter, r *http.Request) {
	in, _, id, _, err := s.decode(r)
	if err != nil {
		http.Error(w, "invalid_payload", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	row, err := s.queries.FacilitatorGet(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		respond(w, SettlementProgress{Stage: Submitting})
		return
	}
	if err != nil {
		http.Error(w, "journal_unavailable", http.StatusServiceUnavailable)
		return
	}
	body, err := canonicalJSON(in)
	if err != nil {
		http.Error(w, "invalid_payload", http.StatusBadRequest)
		return
	}
	digest := sha256.Sum256(body)
	if !bytes.Equal(row.RequestDigest, digest[:]) {
		http.Error(w, "authorization_conflict", http.StatusConflict)
		return
	}
	respond(w, journalProgress(row))
}

func journalProgress(row dbgen.FacilitatorSettlement) SettlementProgress {
	result := SettlementProgress{Stage: Submitting}
	if len(row.TransactionHash) == 32 {
		hash := common.BytesToHash(row.TransactionHash).Hex()
		result.Transaction = &hash
		result.Stage = Unknown
		if row.BroadcastStatus == "confirmed" || row.State == "succeeded" {
			result.Stage = Confirming
		}
	}
	return result
}
