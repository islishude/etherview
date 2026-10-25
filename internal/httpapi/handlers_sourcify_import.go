package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/verify"
)

func (h *Handler) importSourcifyContract(w http.ResponseWriter, r *http.Request) {
	if h.verificationSubmitter == nil || h.verificationTargets == nil || h.sourcifyImporter == nil || !h.cfg.Features.Sourcify {
		writeError(w, r, http.StatusServiceUnavailable, "verification_unavailable", "Sourcify import is unavailable", nil)
		return
	}
	if !h.requireAPIKey(w, r, auth.ScopeVerification) {
		return
	}
	address := strings.ToLower(r.PathValue("address"))
	if !addressPattern.MatchString(address) {
		writeError(w, r, http.StatusBadRequest, "invalid_verification_request", "contract address is invalid", nil)
		return
	}
	var body *gen.SourcifySourceImportRequest
	if !h.decodeBoundedJSON(w, r, &body, "invalid_verification_request", "Sourcify import request requires a source chain ID") {
		return
	}
	if body == nil {
		writeError(w, r, http.StatusBadRequest, "invalid_verification_request", "Sourcify import request requires a source chain ID", nil)
		return
	}
	sourceChainID, err := strconv.ParseUint(body.ChainId, 10, 64)
	if err != nil || sourceChainID == 0 || strconv.FormatUint(sourceChainID, 10) != body.ChainId {
		writeError(w, r, http.StatusBadRequest, "invalid_verification_request", "source chain ID must be a positive canonical uint64 string", nil)
		return
	}
	// ResolveVerificationTarget closes its database snapshot before returning;
	// no connection or transaction is retained across the external lookup.
	target, err := h.verificationTargets.ResolveVerificationTarget(r.Context(), address)
	if err != nil || target.ChainID != h.cfg.Chain.ID || !strings.EqualFold(target.Address, address) {
		h.handleVerificationTargetError(w, r, err)
		return
	}
	request, err := h.sourcifyImporter.ImportV2(r.Context(), sourceChainID, target)
	if err != nil {
		switch {
		case errors.Is(err, verify.ErrSourcifyNotFound):
			writeError(w, r, http.StatusNotFound, "sourcify_not_found", "No verified contract was found on Sourcify for this chain and address", nil)
		case errors.Is(err, verify.ErrSourcifyTargetMismatch):
			writeError(w, r, http.StatusConflict, "sourcify_target_mismatch", "Sourcify runtime does not match the canonical local contract", nil)
		case errors.Is(err, verify.ErrSourcifyInvalidResponse):
			writeError(w, r, http.StatusBadGateway, "sourcify_invalid_source", "Sourcify sources or compiler settings cannot be imported", nil)
		default:
			writeError(w, r, http.StatusServiceUnavailable, "sourcify_unavailable", "Sourcify source retrieval is unavailable", nil)
		}
		return
	}
	h.submitV2(w, r, request)
}
