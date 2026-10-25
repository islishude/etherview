package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/islishude/etherview/internal/turnstile"
)

type TurnstileValidator interface {
	Verify(context.Context, string, string) error
}

func (h *Handler) requireTurnstile(w http.ResponseWriter, r *http.Request, action string) bool {
	enabled := h.cfg.Turnstile.VerificationEnabled
	if action == "csv_export" {
		enabled = h.cfg.Turnstile.CSVExportEnabled
	}
	if !enabled {
		return true
	}
	tokens := r.Header.Values("X-Turnstile-Token")
	err := turnstile.ErrInvalid
	if len(tokens) == 1 && len(tokens[0]) > 0 && len(tokens[0]) <= 2048 {
		if h.turnstile == nil {
			err = turnstile.ErrUnavailable
		} else {
			err = h.turnstile.Verify(r.Context(), tokens[0], action)
		}
	}
	if err == nil {
		return true
	}
	if errors.Is(err, turnstile.ErrInvalid) {
		writeError(w, r, http.StatusForbidden, "turnstile_invalid", "human verification is required; please try again", nil)
	} else {
		writeError(w, r, http.StatusServiceUnavailable, "turnstile_unavailable", "human verification is temporarily unavailable", nil)
	}
	return false
}
