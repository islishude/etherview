package httpapi

func (h *Handler) registerVerificationRoutes() {
	h.mux.HandleFunc("POST /api/v1/verification-jobs/local", h.browserVerification(h.submitBrowserAddressVerification))
	h.mux.HandleFunc("POST /api/v1/verification-jobs/sourcify-import", h.browserVerification(h.importBrowserSourcifyContract))
	h.mux.HandleFunc("POST /api/v1/verification-jobs/sourcify", h.browserVerification(h.submitVerifierAuthorized))
	h.mux.HandleFunc("POST /api/v1/verification-jobs/sourcify-from-etherscan", h.browserVerification(h.submitVerifierAuthorized))
	h.mux.HandleFunc("GET /api/v1/verification-jobs/{id}", h.browserVerification(h.verificationJobAuthorized))
	h.mux.HandleFunc("POST /api/v1/contracts/{address}/verification/sourcify", h.importSourcifyContract)
	// Verification and proxy routes remain present while disabled and report
	// the stable typed capability state.
	h.mux.HandleFunc("POST /api/v1/contracts/{address}/verification", h.submitAddressVerification)
	h.mux.HandleFunc("POST /api/v1/verifier/solidity/multipart", h.submitVerifier)
	h.mux.HandleFunc("POST /api/v1/verifier/vyper/multipart", h.submitVerifier)
	h.mux.HandleFunc("POST /api/v1/verifier/vyper/standard-json", h.submitVerifier)
	h.mux.HandleFunc("POST /api/v1/verifier/solidity/standard-json", h.submitVerifier)
	h.mux.HandleFunc("POST /api/v1/verifier/solidity/batch/multipart", h.submitVerifier)
	h.mux.HandleFunc("POST /api/v1/verifier/solidity/batch/standard-json", h.submitVerifier)
	h.mux.HandleFunc("POST /api/v1/verifier/sourcify", h.submitVerifier)
	h.mux.HandleFunc("POST /api/v1/verifier/sourcify/from-etherscan", h.submitVerifier)
	h.mux.HandleFunc("GET /api/v1/verifier/compilers", h.verifierCompilers)
	h.mux.HandleFunc("POST /api/v1/verifier/lookup-methods", h.lookupVerifierMethods)
	h.handleBillable("getVerifierJob", h.verificationJob)
	h.mux.HandleFunc("GET /api/v1/contracts/{address}/verification", h.verifiedContract)
	h.mux.HandleFunc("GET /api/v1/contracts/{address}/proxy", h.contractProxy)
	h.mux.HandleFunc("GET /api/v1/contracts/{address}/proxy/upgrades", h.contractProxyUpgrades)
	h.mux.HandleFunc("GET /api/v1/contracts/{address}/proxy/initializations", h.contractProxyInitializations)
	h.mux.HandleFunc("GET /api/v1/contracts/{address}/proxy/diamond-cuts", h.contractDiamondCuts)
}
