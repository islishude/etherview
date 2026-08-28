// Package etherscan implements the explicitly supported Etherscan V2
// compatibility surface. It does not proxy arbitrary JSON-RPC methods.
package etherscan

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/billing"
	"github.com/islishude/etherview/internal/etherscanops"
)

var (
	ErrNotFound                            = errors.New("not found")
	ErrTraceUnavailable                    = errors.New("trace unavailable")
	ErrPriceUnavailable                    = errors.New("price unavailable")
	ErrStateUnavailable                    = errors.New("state unavailable")
	ErrStatusUnavailable                   = errors.New("receipt status unavailable")
	ErrEstimateUnavailable                 = errors.New("block time estimate unavailable")
	ErrBlockAlreadyPassed                  = errors.New("block number already passed")
	ErrSupplyUnavailable                   = errors.New("supply unavailable")
	ErrTokenUnavailable                    = errors.New("token index unavailable")
	ErrUncleUnavailable                    = errors.New("uncle index unavailable")
	ErrVerificationUnavailable             = errors.New("verification workflow unavailable")
	ErrVerificationTargetUnavailable       = fmt.Errorf("%w: canonical code or creation facts unavailable", ErrVerificationUnavailable)
	ErrProxyVerificationTargetUnavailable  = errors.New("proxy verification target unavailable")
	ErrProxySourceUnverified               = errors.New("proxy source code not verified")
	ErrProxyImplementationUnverified       = errors.New("proxy implementation source code not verified")
	ErrProxyExpectedImplementationMismatch = errors.New("expected implementation does not match canonical proxy implementation")
	ErrProxyVerificationFailed             = errors.New("proxy verification failed")
	ErrVerificationJobNotFound             = errors.New("verification job not found")
	ErrVerificationFailed                  = errors.New("verification failed")
	ErrContractUnverified                  = errors.New("contract source code not verified")
	ErrPending                             = errors.New("result pending")
)

var (
	addressPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	hashPattern    = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)
)

type Request struct {
	Module string
	Action string
	Values url.Values
}

type Backend interface {
	Execute(context.Context, Request) (any, error)
}

type Handler struct {
	ChainID      uint64
	Backend      Backend
	MaxBody      int64
	PublicOrigin string
	Usage        *billing.UsageDispatcher
	Quota        func(http.Handler) http.Handler
}

type response struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Result  any    `json:"result"`
}

var supported = map[string]map[string]actionSpec{
	"account": {
		"balance":        {addresses: []string{"address"}, state: true},
		"balancemulti":   {required: []string{"address"}, state: true},
		"txlist":         {addresses: []string{"address"}, list: true},
		"txlistinternal": {optionalAddresses: []string{"address"}, hashOptional: "txhash", list: true, trace: true},
		"tokentx":        {addresses: []string{"address"}, optionalAddresses: []string{"contractaddress"}, list: true},
		"tokennfttx":     {addresses: []string{"address"}, optionalAddresses: []string{"contractaddress"}, list: true},
		"token1155tx":    {addresses: []string{"address"}, optionalAddresses: []string{"contractaddress"}, list: true},
		"tokenbalance":   {addresses: []string{"contractaddress", "address"}, state: true},
		"getminedblocks": {addresses: []string{"address"}, list: true},
	},
	"contract": {
		"getabi":                 {addresses: []string{"address"}},
		"getsourcecode":          {addresses: []string{"address"}},
		"getcontractcreation":    {required: []string{"contractaddresses"}},
		"verifysourcecode":       {addresses: []string{"contractaddress"}, required: []string{"sourceCode", "codeformat", "contractname", "compilerversion"}, method: http.MethodPost, keyed: true},
		"checkverifystatus":      {required: []string{"guid"}, keyed: true},
		"verifyproxycontract":    {addresses: []string{"address"}, optionalAddresses: []string{"expectedimplementation"}, method: http.MethodPost, keyed: true},
		"checkproxyverification": {required: []string{"guid"}, method: http.MethodGet, keyed: true},
	},
	"transaction": {
		"getstatus":          {hashes: []string{"txhash"}},
		"gettxreceiptstatus": {hashes: []string{"txhash"}},
	},
	"logs": {
		"getLogs": {optionalAddresses: []string{"address"}, list: true},
	},
	"block": {
		"getblocknobytime":  {required: []string{"timestamp", "closest"}},
		"getblockcountdown": {required: []string{"blockno"}},
	},
	"stats": {
		"ethsupply":   {},
		"ethprice":    {price: true},
		"tokensupply": {addresses: []string{"contractaddress"}, state: true},
	},
	"token": {
		"tokensupply":     {addresses: []string{"contractaddress"}, state: true},
		"tokenbalance":    {addresses: []string{"contractaddress", "address"}, state: true},
		"tokeninfo":       {addresses: []string{"contractaddress"}},
		"tokenholderlist": {addresses: []string{"contractaddress"}, list: true},
	},
}

type actionSpec struct {
	required          []string
	addresses         []string
	optionalAddresses []string
	hashes            []string
	hashOptional      string
	list              bool
	method            string
	trace             bool
	price             bool
	state             bool
	keyed             bool
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Backend == nil || h.ChainID == 0 {
		h.writeError(w, "compatibility backend is unavailable")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		h.writeErrorStatus(w, http.StatusMethodNotAllowed, "unsupported HTTP method")
		return
	}
	if billing.PaymentHeaderPresent(r.Header) {
		h.writeErrorStatus(w, http.StatusBadRequest, "payment authorization is accepted only for account top-ups")
		return
	}
	maxBody := h.MaxBody
	if maxBody <= 0 {
		maxBody = 6 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseForm(); err != nil {
		h.writeErrorStatus(w, http.StatusBadRequest, "invalid form or request too large")
		return
	}
	values := cloneValues(r.Form)
	if err := rejectRepeatedParameters(values); err != nil {
		h.writeErrorStatus(w, http.StatusBadRequest, "duplicate parameters are not allowed")
		return
	}
	// Authentication is complete before the compatibility handler runs. Never
	// pass credential material into backend requests or strict action parsers.
	delete(values, "apikey")
	chainID, err := strconv.ParseUint(values.Get("chainid"), 10, 64)
	if err != nil || chainID != h.ChainID {
		h.writeError(w, "missing or unsupported chainid")
		return
	}
	module, action := values.Get("module"), values.Get("action")
	actions, ok := supported[module]
	if !ok {
		h.writeError(w, "unsupported module")
		return
	}
	spec, ok := actions[action]
	if !ok {
		h.writeError(w, "unsupported action")
		return
	}
	if spec.method != "" && r.Method != spec.method {
		h.writeError(w, "action requires "+spec.method)
		return
	}
	identity := auth.IdentityFrom(r.Context())
	verificationOperation := spec.keyed ||
		(module == "contract" && (action == "getabi" || action == "getsourcecode"))
	if identity.Authenticated && !identity.HasScope(auth.ScopeRead) &&
		(!verificationOperation || !identity.HasScope(auth.ScopeVerification)) {
		h.writeErrorStatus(w, http.StatusForbidden, "API key scope does not authorize this action")
		return
	}
	if spec.keyed && !identity.HasScope(auth.ScopeVerification) {
		if identity.Authenticated {
			h.writeErrorStatus(w, http.StatusForbidden, "API key scope does not authorize this action")
			return
		}
		h.writeError(w, "API Key required")
		return
	}
	if err := validateValues(values, spec); err != nil {
		h.writeError(w, err.Error())
		return
	}
	operation, operationExists := etherscanops.LookupAction(module, action)
	if !operationExists {
		h.writeErrorStatus(w, http.StatusInternalServerError, "compatibility operation catalog is unavailable")
		return
	}
	amount, priced := h.Usage.Price(operation.ID)
	_ = amount
	if priced {
		values, err = canonicalBillableValues(r.Method, module, action, values, spec)
		if err != nil {
			h.writeErrorStatus(w, http.StatusBadRequest, "invalid billing resource")
			return
		}
	}
	request := Request{Module: module, Action: action, Values: values}
	execute := http.HandlerFunc(func(destination http.ResponseWriter, requestHTTP *http.Request) {
		result, executeErr := h.Backend.Execute(requestHTTP.Context(), request)
		if executeErr != nil {
			h.writeBackendError(destination, executeErr)
			return
		}
		h.write(destination, http.StatusOK, response{Status: "1", Message: "OK", Result: result})
	})
	if !priced {
		h.serveQuota(execute, w, r)
		return
	}
	if !identity.Authenticated {
		h.writeErrorStatus(w, http.StatusUnauthorized, "API Key required")
		return
	}
	if !identity.HasScope(auth.ScopeRead) {
		h.writeErrorStatus(w, http.StatusForbidden, "API key scope does not authorize this action")
		return
	}
	if identity.OwnerUserID == nil {
		h.serveQuota(execute, w, r)
		return
	}
	resourceDigest, canonicalErr := h.canonicalBillingResource(r.Method, values)
	if canonicalErr != nil {
		h.writeErrorStatus(w, http.StatusBadRequest, "invalid billing resource")
		return
	}
	billed := http.HandlerFunc(func(destination http.ResponseWriter, requestHTTP *http.Request) {
		h.Usage.Serve(destination, requestHTTP, billing.UsageRequest{
			UserID: *identity.OwnerUserID, APIKeyPrefix: identity.Prefix,
			Operation: operation.ID, Resource: billing.Digest(resourceDigest),
			MaxBodyBytes: operation.MaxResponseBytes,
			Chargeable:   etherscanChargeable,
			WriteError:   h.writeUsageError,
		}, execute)
	})
	h.serveQuota(billed, w, r)
}

func (h Handler) serveQuota(handler http.Handler, w http.ResponseWriter, r *http.Request) {
	if h.Quota == nil {
		handler.ServeHTTP(w, r)
		return
	}
	wrapped := h.Quota(handler)
	if wrapped == nil {
		h.writeErrorStatus(w, http.StatusServiceUnavailable, "rate limiter is unavailable")
		return
	}
	wrapped.ServeHTTP(w, r)
}

func (h Handler) canonicalBillingResource(method string, values url.Values) ([sha256.Size]byte, error) {
	if h.PublicOrigin == "" || len(values) == 0 {
		return [sha256.Size]byte{}, errors.New("billing resource is unavailable")
	}
	canonical := make(url.Values, len(values))
	for name, items := range values {
		if name == "apikey" || name == "" || len(items) != 1 || items[0] == "" {
			return [sha256.Size]byte{}, errors.New("billing resource is ambiguous")
		}
		canonical.Set(name, items[0])
	}
	resource := method + "\n" + strings.TrimSuffix(h.PublicOrigin, "/") + "/v2/api?" + canonical.Encode()
	if len(resource) > 4096 {
		return [sha256.Size]byte{}, errors.New("billing resource is oversized")
	}
	return sha256.Sum256([]byte(resource)), nil
}

func etherscanChargeable(status int, _ http.Header, body []byte) bool {
	if status != http.StatusOK || len(body) == 0 || len(body) > 8<<20 {
		return false
	}
	var envelope response
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false
	}
	return envelope.Status == "1" && envelope.Message == "OK"
}

func (h Handler) writeUsageError(w http.ResponseWriter, status int, _ string, message string) {
	h.writeErrorStatus(w, status, message)
}

func validateValues(values url.Values, spec actionSpec) error {
	for _, name := range spec.required {
		if strings.TrimSpace(values.Get(name)) == "" {
			return fmt.Errorf("missing required parameter %s", name)
		}
	}
	for _, name := range spec.addresses {
		if !addressPattern.MatchString(values.Get(name)) {
			return fmt.Errorf("invalid address parameter %s", name)
		}
	}
	for _, name := range spec.optionalAddresses {
		if value := values.Get(name); value != "" && !addressPattern.MatchString(value) {
			return fmt.Errorf("invalid address parameter %s", name)
		}
	}
	for _, name := range spec.hashes {
		if !hashPattern.MatchString(values.Get(name)) {
			return fmt.Errorf("invalid hash parameter %s", name)
		}
	}
	if spec.hashOptional != "" && values.Get(spec.hashOptional) != "" && !hashPattern.MatchString(values.Get(spec.hashOptional)) {
		return fmt.Errorf("invalid hash parameter %s", spec.hashOptional)
	}
	if spec.list {
		if raw := values.Get("page"); raw != "" {
			page, err := strconv.Atoi(raw)
			if err != nil || page < 1 {
				return errors.New("page must be a positive integer")
			}
		}
		if raw := values.Get("offset"); raw != "" {
			offset, err := strconv.Atoi(raw)
			if err != nil || offset < 1 || offset > 1000 {
				return errors.New("offset must be between 1 and 1000")
			}
		}
		if order := values.Get("sort"); order != "" && order != "asc" && order != "desc" {
			return errors.New("sort must be asc or desc")
		}
	}
	return nil
}

func (h Handler) writeBackendError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		h.write(w, http.StatusOK, response{Status: "0", Message: "No records found", Result: []any{}})
	case errors.Is(err, ErrCoreUnavailable):
		h.writeError(w, "core coverage unavailable")
	case errors.Is(err, ErrTraceUnavailable):
		h.writeError(w, "trace capability unavailable")
	case errors.Is(err, ErrPriceUnavailable):
		h.writeError(w, "price capability unavailable")
	case errors.Is(err, ErrStateUnavailable):
		h.writeError(w, "state capability unavailable")
	case errors.Is(err, ErrStatusUnavailable):
		h.writeError(w, "receipt status unavailable")
	case errors.Is(err, ErrEstimateUnavailable):
		h.writeError(w, "block time estimate unavailable")
	case errors.Is(err, ErrBlockAlreadyPassed):
		h.writeError(w, "Error! Block number already pass")
	case errors.Is(err, ErrSupplyUnavailable):
		h.writeError(w, "supply capability unavailable")
	case errors.Is(err, ErrTokenUnavailable):
		h.writeError(w, "token index capability unavailable")
	case errors.Is(err, ErrUncleUnavailable):
		h.writeError(w, "uncle index capability unavailable")
	case errors.Is(err, ErrProxyVerificationTargetUnavailable):
		h.writeError(w, "proxy verification target unavailable")
	case errors.Is(err, ErrProxySourceUnverified):
		h.writeError(w, "proxy source code not verified")
	case errors.Is(err, ErrProxyImplementationUnverified):
		h.writeError(w, "proxy implementation source code not verified")
	case errors.Is(err, ErrProxyExpectedImplementationMismatch):
		h.writeError(w, "expected implementation does not match canonical proxy implementation")
	case errors.Is(err, ErrProxyVerificationFailed):
		h.writeError(w, "Fail - Unable to verify proxy contract")
	case errors.Is(err, ErrVerificationTargetUnavailable):
		h.writeError(w, "verification target state unavailable")
	case errors.Is(err, ErrVerificationUnavailable):
		h.writeError(w, "verification workflow unavailable")
	case errors.Is(err, ErrVerificationJobNotFound):
		h.writeError(w, "Unable to locate verification request")
	case errors.Is(err, ErrVerificationFailed):
		h.writeError(w, "Fail - Unable to verify")
	case errors.Is(err, ErrContractUnverified):
		h.writeError(w, "Contract source code not verified")
	case errors.Is(err, ErrInvalidParameter):
		h.writeError(w, err.Error())
	case errors.Is(err, ErrPending):
		h.write(w, http.StatusOK, response{Status: "0", Message: "NOTOK", Result: "Pending in queue"})
	default:
		h.writeErrorStatus(w, http.StatusInternalServerError, "query failed")
	}
}

func (h Handler) writeError(w http.ResponseWriter, message string) {
	h.write(w, http.StatusOK, response{Status: "0", Message: "NOTOK", Result: message})
}

func (h Handler) writeErrorStatus(w http.ResponseWriter, status int, message string) {
	h.write(w, status, response{Status: "0", Message: "NOTOK", Result: message})
}

func (Handler) write(w http.ResponseWriter, status int, payload response) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func cloneValues(values url.Values) url.Values {
	result := make(url.Values, len(values))
	for key, items := range values {
		result[key] = append([]string(nil), items...)
	}
	return result
}
