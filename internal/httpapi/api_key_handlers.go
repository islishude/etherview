package httpapi

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/auth"
)

var userAPIKeyPrefixPattern = regexp.MustCompile(`^[a-z2-7]{10}$`)

type userAPIKeyCursor struct {
	ChainID   string    `json:"chain_id"`
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	Prefix    string    `json:"prefix"`
}

func (h *Handler) listCurrentUserAPIKeys(w http.ResponseWriter, r *http.Request) {
	if !h.userAPIKeysAvailable(w, r) {
		return
	}
	authentication, ok := h.requireUserSession(w, r)
	if !ok {
		return
	}
	limit, ok := parseLimit(w, r, 25)
	if !ok {
		return
	}
	after, ok := h.decodeUserAPIKeyCursor(w, r, authentication.Session.User.ID)
	if !ok {
		return
	}
	page, err := h.userAPIKeys.List(
		r.Context(), authentication.Session.User.ID, after, limit+1,
	)
	if err != nil {
		h.handleUserAPIKeyError(w, r, err)
		return
	}
	meta := h.meta(r)
	if len(page.Items) > limit {
		cursor, err := h.encodeUserAPIKeyCursor(
			authentication.Session.User.ID, page.Items[limit-1],
		)
		if err != nil {
			h.handleUserAPIKeyError(w, r, err)
			return
		}
		meta.NextCursor = &cursor
		page.Items = page.Items[:limit]
	}
	items := make([]gen.UserAPIKey, len(page.Items))
	for index, key := range page.Items {
		items[index] = userAPIKeyModel(key)
	}
	policy := h.userAPIKeys.Policy()
	writeJSON(w, http.StatusOK, gen.UserAPIKeyPageResponse{
		Data: gen.UserAPIKeyPage{
			Items: items,
			Policy: gen.UserAPIKeyPolicy{
				RatePerSecond: policy.Rate,
				Burst:         policy.Burst,
				MaximumActive: policy.MaximumActive,
				ActiveCount:   page.ActiveCount,
				AllowedScopes: []gen.APIKeyScope{
					gen.APIKeyScope(auth.ScopeRead),
					gen.APIKeyScope(auth.ScopeVerification),
				},
			},
		},
		Meta: meta,
	})
}

func (h *Handler) createCurrentUserAPIKey(w http.ResponseWriter, r *http.Request) {
	if !h.userAPIKeysAvailable(w, r) || !h.requireAuthOrigin(w, r) {
		return
	}
	authentication, ok := h.requireUserSession(w, r)
	if !ok || !h.requireCSRF(w, r, authentication) {
		return
	}
	var request gen.UserAPIKeyCreateRequest
	if !decodeAuthJSON(w, r, &request) {
		return
	}
	name := strings.TrimSpace(request.Name)
	scopes, valid := userAPIKeyScopes(request.Scopes)
	if !validUserAPIKeyName(name) || !valid {
		writeError(w, r, http.StatusBadRequest, "invalid_api_key_request", "API key request is invalid", nil)
		return
	}
	issued, err := h.userAPIKeys.Create(
		r.Context(), authentication.Session.User.ID, name, scopes,
	)
	if err != nil {
		h.handleUserAPIKeyError(w, r, err)
		return
	}
	h.writeIssuedUserAPIKey(w, r, issued)
}

func (h *Handler) rotateCurrentUserAPIKey(w http.ResponseWriter, r *http.Request) {
	if !h.userAPIKeysAvailable(w, r) || !h.requireAuthOrigin(w, r) {
		return
	}
	authentication, ok := h.requireUserSession(w, r)
	if !ok || !h.requireCSRF(w, r, authentication) {
		return
	}
	prefix, ok := validUserAPIKeyPrefix(w, r)
	if !ok {
		return
	}
	issued, err := h.userAPIKeys.Rotate(
		r.Context(), authentication.Session.User.ID, prefix,
	)
	if err != nil {
		h.handleUserAPIKeyError(w, r, err)
		return
	}
	h.writeIssuedUserAPIKey(w, r, issued)
}

func (h *Handler) revokeCurrentUserAPIKey(w http.ResponseWriter, r *http.Request) {
	if !h.userAPIKeysAvailable(w, r) || !h.requireAuthOrigin(w, r) {
		return
	}
	authentication, ok := h.requireUserSession(w, r)
	if !ok || !h.requireCSRF(w, r, authentication) {
		return
	}
	prefix, ok := validUserAPIKeyPrefix(w, r)
	if !ok {
		return
	}
	if err := h.userAPIKeys.Revoke(
		r.Context(), authentication.Session.User.ID, prefix,
		h.now().UTC().Truncate(time.Microsecond),
	); err != nil {
		h.handleUserAPIKeyError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) writeIssuedUserAPIKey(
	w http.ResponseWriter,
	r *http.Request,
	issued auth.IssuedAPIKey,
) {
	writeJSON(w, http.StatusCreated, gen.UserAPIKeyIssuedResponse{
		Data: gen.UserAPIKeyIssued{
			Token: issued.Token,
			Key:   userAPIKeyModel(issued.Record),
		},
		Meta: h.meta(r),
	})
}

func (h *Handler) userAPIKeysAvailable(w http.ResponseWriter, r *http.Request) bool {
	if h.cfg.Features.UserAPIKeys && h.userAPIKeys != nil {
		return true
	}
	writeError(w, r, http.StatusServiceUnavailable, "user_api_keys_unavailable", "user API key management is unavailable", nil)
	return false
}

func (h *Handler) handleUserAPIKeyError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrAPIKeyLimitReached):
		writeError(w, r, http.StatusConflict, "api_key_limit_reached", "active API key limit was reached", nil)
	case errors.Is(err, auth.ErrAPIKeyNotActive):
		writeError(w, r, http.StatusConflict, "api_key_not_active", "API key is not active", nil)
	case errors.Is(err, auth.ErrAPIKeyNotFound):
		writeError(w, r, http.StatusNotFound, "api_key_not_found", "API key was not found", nil)
	default:
		h.logUserAuthFailure(r, "user API key operation failed", err)
		writeError(w, r, http.StatusServiceUnavailable, "user_api_keys_unavailable", "user API key management is unavailable", nil)
	}
}

func userAPIKeyModel(key auth.APIKey) gen.UserAPIKey {
	status := gen.UserAPIKeyStatusActive
	if key.RevokedAt != nil {
		status = gen.UserAPIKeyStatusRevoked
	}
	scopes := make([]gen.APIKeyScope, len(key.Scopes))
	for index, scope := range key.Scopes {
		scopes[index] = gen.APIKeyScope(scope)
	}
	return gen.UserAPIKey{
		Prefix: key.Prefix, Name: key.Name, Scopes: scopes,
		RatePerSecond: key.Rate, Burst: key.Burst, Status: status,
		CreatedAt: key.CreatedAt, RevokedAt: key.RevokedAt,
	}
}

func userAPIKeyScopes(values []gen.APIKeyScope) ([]auth.Scope, bool) {
	scopes := make([]auth.Scope, len(values))
	for index, value := range values {
		scopes[index] = auth.Scope(value)
	}
	normalized, err := auth.NormalizeScopes(scopes)
	return normalized, err == nil && len(normalized) == len(values)
}

func validUserAPIKeyName(value string) bool {
	if value == "" || len(value) > 128 || utf8.RuneCountInString(value) > 128 {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validUserAPIKeyPrefix(w http.ResponseWriter, r *http.Request) (string, bool) {
	prefix := r.PathValue("prefix")
	if !userAPIKeyPrefixPattern.MatchString(prefix) {
		writeError(w, r, http.StatusNotFound, "api_key_not_found", "API key was not found", nil)
		return "", false
	}
	return prefix, true
}

func (h *Handler) decodeUserAPIKeyCursor(
	w http.ResponseWriter,
	r *http.Request,
	userID string,
) (*auth.UserKeyPageAfter, bool) {
	raw := r.URL.Query().Get("cursor")
	if raw == "" {
		return nil, true
	}
	var cursor userAPIKeyCursor
	if DecodeCursor(raw, &cursor) != nil ||
		cursor.ChainID != strconv.FormatUint(h.cfg.Chain.ID, 10) ||
		cursor.UserID != userID || cursor.CreatedAt.IsZero() ||
		!userAPIKeyPrefixPattern.MatchString(cursor.Prefix) {
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is invalid or stale", nil)
		return nil, false
	}
	return &auth.UserKeyPageAfter{
		CreatedAt: cursor.CreatedAt, Prefix: cursor.Prefix,
	}, true
}

func (h *Handler) encodeUserAPIKeyCursor(
	userID string,
	key auth.APIKey,
) (string, error) {
	return EncodeCursor(userAPIKeyCursor{
		ChainID: strconv.FormatUint(h.cfg.Chain.ID, 10), UserID: userID,
		CreatedAt: key.CreatedAt, Prefix: key.Prefix,
	})
}
