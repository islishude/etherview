package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/verify"
)

type browserVerificationResponseKey struct{}

// browserVerification authenticates before decoding input, resolving a target,
// contacting an upstream, or enqueueing work. It never creates an API identity.
func (h *Handler) browserVerification(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.userAuthAvailable(w, r) {
			return
		}
		if r.Method != http.MethodGet && !h.requireAuthOrigin(w, r) {
			return
		}
		authentication, ok := h.requireUserSession(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodGet {
			if !h.requireCSRF(w, r, authentication) {
				return
			}
			if !h.cfg.Features.Verification || !h.cfg.Security.PublicVerification || (r.Pattern != "POST /api/v1/verification-jobs/local" && !h.cfg.Features.Sourcify) {
				writeError(w, r, http.StatusServiceUnavailable, "verification_unavailable", "contract verification submission is unavailable", nil)
				return
			}
			if !h.requireTurnstile(w, r, "verification") {
				return
			}
		} else if id, err := uuid.Parse(r.PathValue("id")); err != nil || id.String() != r.PathValue("id") {
			writeError(w, r, http.StatusBadRequest, "invalid_verification_request", "verification job ID is invalid", nil)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), browserVerificationResponseKey{}, true)))
	}
}

func (h *Handler) writeVerificationJob(w http.ResponseWriter, r *http.Request, status int, job verify.VerificationJob) {
	model := verificationJobModel(job)
	if r.Context().Value(browserVerificationResponseKey{}) != true {
		writeJSON(w, status, gen.VerificationJobResponse{Data: model, Meta: h.meta(r)})
		return
	}
	browser := gen.BrowserVerificationJob{
		Id: model.Id, Kind: gen.BrowserVerificationJobKind(model.Kind),
		Status:    gen.BrowserVerificationJobStatus(model.Status),
		ErrorCode: model.ErrorCode, CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt,
	}
	if len(job.Outcome) > 0 {
		var outcome gen.BrowserVerificationOutcome
		if json.Unmarshal(job.Outcome, &outcome) == nil {
			browser.Outcome = &outcome
		}
	}
	// Project only target identity from durable input. Never serialize the request,
	// which may contain unpublished sources or encrypted upstream credentials.
	if request := job.RequestV2; request != nil {
		var address, chain string
		if job.Kind == verify.JobAddress && request.Target != nil {
			address, chain = request.Target.Address, strconv.FormatUint(request.Target.ChainID, 10)
		} else if job.Kind == verify.JobSourcify || job.Kind == verify.JobSourcifyFromEtherscan {
			var target struct {
				Address string `json:"address"`
				ChainID string `json:"chain_id"`
			}
			if json.Unmarshal(request.SourcifyRequest, &target) == nil {
				address, chain = target.Address, target.ChainID
			}
		}
		if canonical, err := checksumAddress(address); err == nil {
			if id, err := strconv.ParseUint(chain, 10, 64); err == nil && id > 0 && strconv.FormatUint(id, 10) == chain {
				browser.Address, browser.ChainId = &canonical, &chain
			}
		}
	}
	writeJSON(w, status, gen.BrowserVerificationJobResponse{Data: browser, Meta: h.meta(r)})
}

func (h *Handler) submitBrowserAddressVerification(w http.ResponseWriter, r *http.Request) {
	if h.verificationSubmitter == nil || h.verificationTargets == nil {
		writeError(w, r, http.StatusServiceUnavailable, "verification_unavailable", "contract verification submission is unavailable", nil)
		return
	}
	var body struct {
		Address string `json:"address"`
		verifierSubmission
	}
	if !h.decodeBoundedJSON(w, r, &body, "invalid_verification_request", "address verification request is invalid") {
		return
	}
	address := strings.ToLower(body.Address)
	if !addressPattern.MatchString(address) {
		writeError(w, r, http.StatusBadRequest, "invalid_verification_request", "contract address is invalid", nil)
		return
	}
	h.submitAddressVerificationInput(w, r, address, body.verifierSubmission)
}

func (h *Handler) importBrowserSourcifyContract(w http.ResponseWriter, r *http.Request) {
	if h.verificationSubmitter == nil || h.verificationTargets == nil || h.sourcifyImporter == nil || !h.cfg.Features.Sourcify {
		writeError(w, r, http.StatusServiceUnavailable, "verification_unavailable", "Sourcify import is unavailable", nil)
		return
	}
	var body *gen.BrowserSourcifySourceImportRequest
	if !h.decodeBoundedJSON(w, r, &body, "invalid_verification_request", "Sourcify import request is invalid") {
		return
	}
	if body == nil || !addressPattern.MatchString(strings.ToLower(body.Address)) {
		writeError(w, r, http.StatusBadRequest, "invalid_verification_request", "contract address is invalid", nil)
		return
	}
	h.importSourcifyContractInput(w, r, strings.ToLower(body.Address), &gen.SourcifySourceImportRequest{ChainId: body.ChainId})
}
