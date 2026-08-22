package httpapi

func (h *Handler) registerVerificationRoutes() {
	// Verification and proxy routes remain present while disabled and report
	// the stable typed capability state.
	h.mux.HandleFunc("POST /api/v1/contracts/{address}/verification", h.submitAddressVerification)
	h.mux.HandleFunc("POST /api/v1/verifier/solidity/multipart", h.submitVerifier)
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
