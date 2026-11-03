package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/verify"
)

func (h *Handler) importSourcifyContract(w http.ResponseWriter, r *http.Request) {
	if h.verificationSubmitter == nil || h.verificationTargets == nil || !h.cfg.Features.Sourcify {
		writeError(w, r, http.StatusServiceUnavailable, "verification_unavailable", "Sourcify import is unavailable", nil)
		return
	}
	if !h.requireAPIKey(w, r, auth.ScopeVerification) {
		return
	}
	h.importSourcifyContractAuthorized(w, r)
}

func (h *Handler) importSourcifyContractAuthorized(w http.ResponseWriter, r *http.Request) {
	if h.verificationSubmitter == nil || h.verificationTargets == nil || !h.cfg.Features.Sourcify {
		writeError(w, r, http.StatusServiceUnavailable, "verification_unavailable", "Sourcify import is unavailable", nil)
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
	h.importSourcifyContractInput(w, r, address, body)
}

func (h *Handler) importSourcifyContractInput(w http.ResponseWriter, r *http.Request, address string, body *gen.SourcifySourceImportRequest) {
	sourceChainID, err := strconv.ParseUint(body.ChainId, 10, 64)
	if err != nil || sourceChainID == 0 || strconv.FormatUint(sourceChainID, 10) != body.ChainId {
		writeError(w, r, http.StatusBadRequest, "invalid_verification_request", "source chain ID must be a positive canonical uint64 string", nil)
		return
	}
	// ResolveVerificationTarget closes its snapshot before the durable enqueue.
	// The verification worker performs every external source request.
	target, err := h.verificationTargets.ResolveVerificationTarget(r.Context(), address)
	if err != nil || target.ChainID != h.cfg.Chain.ID || !strings.EqualFold(target.Address, address) {
		h.handleVerificationTargetError(w, r, err)
		return
	}
	h.submitV2(w, r, verify.SubmissionV2{Kind: verify.JobSourcifyImport, SourceChainID: sourceChainID, Target: &target})
}
