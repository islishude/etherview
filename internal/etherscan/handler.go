// Package etherscan implements the explicitly supported Etherscan V2
// compatibility surface. It does not proxy arbitrary JSON-RPC methods.
package etherscan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/islishude/etherview/internal/auth"
)

var (
	ErrNotFound                      = errors.New("not found")
	ErrTraceUnavailable              = errors.New("trace unavailable")
	ErrPriceUnavailable              = errors.New("price unavailable")
	ErrStateUnavailable              = errors.New("state unavailable")
	ErrStatusUnavailable             = errors.New("receipt status unavailable")
	ErrEstimateUnavailable           = errors.New("block time estimate unavailable")
	ErrBlockAlreadyPassed            = errors.New("block number already passed")
	ErrSupplyUnavailable             = errors.New("supply unavailable")
	ErrTokenUnavailable              = errors.New("token index unavailable")
	ErrUncleUnavailable              = errors.New("uncle index unavailable")
	ErrVerificationUnavailable       = errors.New("verification workflow unavailable")
	ErrVerificationTargetUnavailable = fmt.Errorf("%w: canonical code or creation facts unavailable", ErrVerificationUnavailable)
	ErrProxyVerificationUnavailable  = fmt.Errorf("%w: durable proxy verification is unavailable", ErrVerificationUnavailable)
	ErrVerificationJobNotFound       = errors.New("verification job not found")
	ErrVerificationFailed            = errors.New("verification failed")
	ErrContractUnverified            = errors.New("contract source code not verified")
	ErrPending                       = errors.New("result pending")
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
	ChainID uint64
	Backend Backend
	MaxBody int64
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
		"checkverifystatus":      {required: []string{"guid"}, method: http.MethodPost, keyed: true},
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
	if spec.keyed && !auth.IdentityFrom(r.Context()).Authenticated {
		h.writeError(w, "API Key required")
		return
	}
	if err := validateValues(values, spec); err != nil {
		h.writeError(w, err.Error())
		return
	}
	result, err := h.Backend.Execute(r.Context(), Request{Module: module, Action: action, Values: values})
	if err != nil {
		h.writeBackendError(w, err)
		return
	}
	h.write(w, http.StatusOK, response{Status: "1", Message: "OK", Result: result})
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
	case errors.Is(err, ErrProxyVerificationUnavailable):
		h.writeError(w, "proxy verification workflow unavailable")
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
