package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/jsonstrict"
	"github.com/islishude/etherview/internal/userauth"
)

const maximumAuthRequestBytes = 16 << 10

// UserAuthenticator is the writer-authoritative session boundary used by the
// native browser API. API-key identity is intentionally absent.
type UserAuthenticator interface {
	CreateChallenge(context.Context, string) (userauth.Challenge, error)
	VerifyChallenge(context.Context, string, string) (userauth.LoginResult, error)
	Authenticate(context.Context, string) (SessionAuthentication, error)
	Logout(context.Context, string) (bool, error)
}

// UserAdministration contains only writer-backed user operations.
type UserAdministration interface {
	UserByID(context.Context, string) (userauth.User, error)
	UserByAddress(context.Context, string) (userauth.User, error)
	UpdateDisplayName(context.Context, string, *string, time.Time) (userauth.User, error)
	Users(context.Context, *userauth.UserPageAfter, int) ([]userauth.User, error)
	UpdateUser(context.Context, string, userauth.AdminUserUpdate, time.Time) (userauth.AdminUserUpdateResult, error)
	RevokeAllSessions(context.Context, string, time.Time) (uint64, error)
}

// SessionAuthentication keeps CSRF verification opaque to HTTP fakes while
// the production adapter delegates to userauth's constant-time verifier.
type SessionAuthentication struct {
	Session   userauth.Session
	CSRFToken string
	validate  func(string) error
}

func (authentication SessionAuthentication) ValidateCSRF(token string) error {
	if authentication.validate == nil {
		return userauth.ErrCSRFInvalid
	}
	return authentication.validate(token)
}

// UserAuthAdapter adapts the concrete service without exposing its secret
// session material to the Handler.
type UserAuthAdapter struct {
	Service *userauth.Service
}

func (adapter UserAuthAdapter) CreateChallenge(
	ctx context.Context,
	address string,
) (userauth.Challenge, error) {
	if adapter.Service == nil {
		return userauth.Challenge{}, errors.New("user authentication service is nil")
	}
	return adapter.Service.CreateChallenge(ctx, address)
}

func (adapter UserAuthAdapter) VerifyChallenge(
	ctx context.Context,
	challengeID string,
	signature string,
) (userauth.LoginResult, error) {
	if adapter.Service == nil {
		return userauth.LoginResult{}, errors.New("user authentication service is nil")
	}
	return adapter.Service.VerifyChallenge(ctx, challengeID, signature)
}

func (adapter UserAuthAdapter) Authenticate(
	ctx context.Context,
	token string,
) (SessionAuthentication, error) {
	if adapter.Service == nil {
		return SessionAuthentication{}, errors.New("user authentication service is nil")
	}
	authentication, err := adapter.Service.Authenticate(ctx, token)
	if err != nil {
		return SessionAuthentication{}, err
	}
	return SessionAuthentication{
		Session: authentication.Session, CSRFToken: authentication.CSRFToken,
		validate: authentication.ValidateCSRF,
	}, nil
}

func (adapter UserAuthAdapter) Logout(ctx context.Context, token string) (bool, error) {
	if adapter.Service == nil {
		return false, errors.New("user authentication service is nil")
	}
	return adapter.Service.Logout(ctx, token)
}

func (h *Handler) createAuthChallenge(w http.ResponseWriter, r *http.Request) {
	if !h.userAuthAvailable(w, r) || !h.requireAuthOrigin(w, r) {
		return
	}
	var request gen.AuthChallengeRequest
	if !decodeAuthJSON(w, r, &request) {
		return
	}
	challenge, err := h.userAuth.CreateChallenge(r.Context(), request.Address)
	if err != nil {
		h.handleUserAuthError(w, r, err)
		return
	}
	challengeID, err := uuid.Parse(challenge.ID)
	if err != nil {
		h.logUserAuthFailure(r, "encode authentication challenge", err)
		h.writeUserAuthUnavailable(w, r)
		return
	}
	writeJSON(w, http.StatusCreated, gen.AuthChallengeResponse{
		Data: gen.AuthChallenge{
			ChallengeId: challengeID,
			Message:     challenge.Message,
			ExpiresAt:   challenge.ExpiresAt,
		},
		Meta: h.meta(r),
	})
}

func (h *Handler) verifyAuthChallenge(w http.ResponseWriter, r *http.Request) {
	if !h.userAuthAvailable(w, r) || !h.requireAuthOrigin(w, r) {
		return
	}
	var request gen.AuthVerifyRequest
	if !decodeAuthJSON(w, r, &request) {
		return
	}
	result, err := h.userAuth.VerifyChallenge(
		r.Context(), request.ChallengeId.String(), request.Signature,
	)
	if err != nil {
		h.handleUserAuthError(w, r, err)
		return
	}
	h.setSessionCookie(w, result.Credentials.Token, result.Session.ExpiresAt)
	writeJSON(w, http.StatusCreated, gen.AuthSessionResponse{
		Data: authenticatedSessionModel(
			result.Session, result.Credentials.CSRFToken,
		),
		Meta: h.meta(r),
	})
}

func (h *Handler) authSession(w http.ResponseWriter, r *http.Request) {
	if !h.userAuthAvailable(w, r) {
		return
	}
	token, present, valid := sessionCookie(r)
	if !present {
		h.writeUnauthenticatedSession(w, r)
		return
	}
	if !valid {
		h.clearSessionCookie(w)
		h.writeUnauthenticatedSession(w, r)
		return
	}
	authentication, err := h.userAuth.Authenticate(r.Context(), token)
	if err != nil {
		switch {
		case errors.Is(err, userauth.ErrSessionInvalid),
			errors.Is(err, userauth.ErrUserDisabled):
			h.clearSessionCookie(w)
			h.writeUnauthenticatedSession(w, r)
		default:
			h.handleUserAuthError(w, r, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, gen.AuthSessionResponse{
		Data: authenticatedSessionModel(
			authentication.Session, authentication.CSRFToken,
		),
		Meta: h.meta(r),
	})
}

func (h *Handler) logoutAuthSession(w http.ResponseWriter, r *http.Request) {
	if !h.userAuthAvailable(w, r) || !h.requireAuthOrigin(w, r) {
		return
	}
	token, present, valid := sessionCookie(r)
	if !present || !valid {
		h.clearSessionCookie(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	authentication, err := h.userAuth.Authenticate(r.Context(), token)
	if err != nil {
		switch {
		case errors.Is(err, userauth.ErrSessionInvalid),
			errors.Is(err, userauth.ErrUserDisabled):
			_, _ = h.userAuth.Logout(r.Context(), token)
			h.clearSessionCookie(w)
			w.WriteHeader(http.StatusNoContent)
		default:
			h.handleUserAuthError(w, r, err)
		}
		return
	}
	if !h.requireCSRF(w, r, authentication) {
		return
	}
	if _, err := h.userAuth.Logout(r.Context(), token); err != nil &&
		!errors.Is(err, userauth.ErrSessionInvalid) {
		h.handleUserAuthError(w, r, err)
		return
	}
	h.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) updateCurrentUser(w http.ResponseWriter, r *http.Request) {
	if !h.userAuthAvailable(w, r) || !h.requireAuthOrigin(w, r) {
		return
	}
	authentication, ok := h.requireUserSession(w, r)
	if !ok || !h.requireCSRF(w, r, authentication) {
		return
	}
	var request struct {
		DisplayName json.RawMessage `json:"display_name"`
	}
	if !decodeAuthJSON(w, r, &request) || len(request.DisplayName) == 0 {
		if len(request.DisplayName) == 0 {
			writeError(w, r, http.StatusBadRequest, "invalid_auth_request", "authentication request is invalid", nil)
		}
		return
	}
	var displayName *string
	if !bytes.Equal(request.DisplayName, []byte("null")) {
		var value string
		if err := json.Unmarshal(request.DisplayName, &value); err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_auth_request", "authentication request is invalid", nil)
			return
		}
		displayName = &value
	}
	user, err := h.userAdministration.UpdateDisplayName(
		r.Context(), authentication.Session.User.ID, displayName,
		h.now().UTC().Truncate(time.Microsecond),
	)
	if err != nil {
		h.handleUserAuthError(w, r, err)
		return
	}
	model, ok := h.userModel(w, r, user)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, gen.UserResponse{Data: model, Meta: h.meta(r)})
}

func (h *Handler) listAdminUsers(w http.ResponseWriter, r *http.Request) {
	if !h.userAuthAvailable(w, r) {
		return
	}
	if _, ok := h.requireAdminSession(w, r); !ok {
		return
	}
	limit, ok := parseLimit(w, r, 25)
	if !ok {
		return
	}
	after, ok := h.decodeUserCursor(w, r)
	if !ok {
		return
	}
	users, err := h.userAdministration.Users(r.Context(), after, limit+1)
	if err != nil {
		h.handleUserAuthError(w, r, err)
		return
	}
	meta := h.meta(r)
	if len(users) > limit {
		cursor, cursorErr := h.encodeUserCursor(users[limit-1])
		if cursorErr != nil {
			h.logUserAuthFailure(r, "encode user cursor", cursorErr)
			h.writeUserAuthUnavailable(w, r)
			return
		}
		meta.NextCursor = &cursor
		users = users[:limit]
	}
	models := make([]gen.User, len(users))
	for index := range users {
		model, valid := h.userModel(w, r, users[index])
		if !valid {
			return
		}
		models[index] = model
	}
	writeJSON(w, http.StatusOK, gen.UserListResponse{Data: models, Meta: meta})
}

func (h *Handler) updateAdminUser(w http.ResponseWriter, r *http.Request) {
	if !h.userAuthAvailable(w, r) || !h.requireAuthOrigin(w, r) {
		return
	}
	authentication, ok := h.requireAdminSession(w, r)
	if !ok || !h.requireCSRF(w, r, authentication) {
		return
	}
	userID, ok := validUserID(w, r)
	if !ok {
		return
	}
	var request gen.AdminUserUpdate
	if !decodeAuthJSON(w, r, &request) {
		return
	}
	if request.Role == nil && request.Status == nil {
		writeError(w, r, http.StatusBadRequest, "invalid_auth_request", "role or status is required", nil)
		return
	}
	var update userauth.AdminUserUpdate
	if request.Role != nil {
		role := userauth.Role(*request.Role)
		update.Role = &role
	}
	if request.Status != nil {
		status := userauth.Status(*request.Status)
		update.Status = &status
	}
	result, err := h.userAdministration.UpdateUser(
		r.Context(), userID, update, h.now().UTC().Truncate(time.Microsecond),
	)
	if err != nil {
		h.handleUserAuthError(w, r, err)
		return
	}
	model, valid := h.userModel(w, r, result.User)
	if !valid {
		return
	}
	writeJSON(w, http.StatusOK, gen.UserResponse{Data: model, Meta: h.meta(r)})
}

func (h *Handler) revokeAdminUserSessions(w http.ResponseWriter, r *http.Request) {
	if !h.userAuthAvailable(w, r) || !h.requireAuthOrigin(w, r) {
		return
	}
	authentication, ok := h.requireAdminSession(w, r)
	if !ok || !h.requireCSRF(w, r, authentication) {
		return
	}
	userID, ok := validUserID(w, r)
	if !ok {
		return
	}
	count, err := h.userAdministration.RevokeAllSessions(
		r.Context(), userID, h.now().UTC().Truncate(time.Microsecond),
	)
	if err != nil {
		h.handleUserAuthError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.SessionRevocationResponse{
		Data: gen.SessionRevocation{RevokedSessions: strconv.FormatUint(count, 10)},
		Meta: h.meta(r),
	})
}

func (h *Handler) userAuthAvailable(w http.ResponseWriter, r *http.Request) bool {
	if h.cfg.Features.UserAuth && h.userAuth != nil && h.userAdministration != nil {
		return true
	}
	h.writeUserAuthUnavailable(w, r)
	return false
}

func (h *Handler) requireAuthOrigin(w http.ResponseWriter, r *http.Request) bool {
	values := r.Header.Values("Origin")
	if len(values) != 1 || values[0] != h.authOrigin {
		writeError(w, r, http.StatusForbidden, "origin_invalid", "request origin is invalid", nil)
		return false
	}
	return true
}

func (h *Handler) requireUserSession(
	w http.ResponseWriter,
	r *http.Request,
) (SessionAuthentication, bool) {
	token, present, valid := sessionCookie(r)
	if !present || !valid {
		if present {
			h.clearSessionCookie(w)
		}
		writeError(w, r, http.StatusUnauthorized, "authentication_required", "user authentication is required", nil)
		return SessionAuthentication{}, false
	}
	authentication, err := h.userAuth.Authenticate(r.Context(), token)
	if err != nil {
		if errors.Is(err, userauth.ErrSessionInvalid) {
			h.clearSessionCookie(w)
			writeError(w, r, http.StatusUnauthorized, "authentication_required", "user authentication is required", nil)
			return SessionAuthentication{}, false
		}
		if errors.Is(err, userauth.ErrUserDisabled) {
			h.clearSessionCookie(w)
		}
		h.handleUserAuthError(w, r, err)
		return SessionAuthentication{}, false
	}
	return authentication, true
}

func (h *Handler) requireAdminSession(
	w http.ResponseWriter,
	r *http.Request,
) (SessionAuthentication, bool) {
	authentication, ok := h.requireUserSession(w, r)
	if !ok {
		return SessionAuthentication{}, false
	}
	if authentication.Session.User.Role != userauth.RoleAdmin {
		writeError(w, r, http.StatusForbidden, "admin_required", "administrator access is required", nil)
		return SessionAuthentication{}, false
	}
	return authentication, true
}

func (h *Handler) requireCSRF(
	w http.ResponseWriter,
	r *http.Request,
	authentication SessionAuthentication,
) bool {
	values := r.Header.Values("X-CSRF-Token")
	if len(values) != 1 || authentication.ValidateCSRF(values[0]) != nil {
		writeError(w, r, http.StatusForbidden, "csrf_invalid", "CSRF token is invalid", nil)
		return false
	}
	return true
}

func sessionCookie(r *http.Request) (string, bool, bool) {
	cookies := r.CookiesNamed(userauth.SessionCookieName)
	if len(cookies) == 0 {
		return "", false, true
	}
	if len(cookies) != 1 || cookies[0].Value == "" {
		return "", true, false
	}
	return cookies[0].Value, true, true
}

func (h *Handler) setSessionCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: userauth.SessionCookieName, Value: token,
		Path: "/api/v1", Expires: expiresAt.UTC(),
		HttpOnly: true, Secure: h.authSecureCookie, SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: userauth.SessionCookieName, Value: "",
		Path: "/api/v1", Expires: time.Unix(1, 0).UTC(), MaxAge: -1,
		HttpOnly: true, Secure: h.authSecureCookie, SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) writeUnauthenticatedSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, gen.AuthSessionResponse{
		Data: gen.AuthSession{Authenticated: false},
		Meta: h.meta(r),
	})
}

func authenticatedSessionModel(
	session userauth.Session,
	csrfToken string,
) gen.AuthSession {
	userID, _ := uuid.Parse(session.User.ID)
	return gen.AuthSession{
		Authenticated: true,
		User: &gen.User{
			Id: userID, ChainId: strconv.FormatUint(session.User.ChainID, 10),
			Address: session.User.Address, DisplayName: session.User.DisplayName,
			Role: gen.UserRole(session.User.Role), Status: gen.UserStatus(session.User.Status),
			CreatedAt: session.User.CreatedAt, UpdatedAt: session.User.UpdatedAt,
			LastLoginAt: session.User.LastLoginAt,
		},
		ExpiresAt: &session.ExpiresAt, CsrfToken: &csrfToken,
	}
}

func (h *Handler) userModel(
	w http.ResponseWriter,
	r *http.Request,
	user userauth.User,
) (gen.User, bool) {
	identifier, err := uuid.Parse(user.ID)
	if err != nil || user.ChainID != h.cfg.Chain.ID {
		h.logUserAuthFailure(r, "map stored user", err)
		h.writeUserAuthUnavailable(w, r)
		return gen.User{}, false
	}
	return gen.User{
		Id: identifier, ChainId: strconv.FormatUint(user.ChainID, 10),
		Address: user.Address, DisplayName: user.DisplayName,
		Role: gen.UserRole(user.Role), Status: gen.UserStatus(user.Status),
		CreatedAt: user.CreatedAt, UpdatedAt: user.UpdatedAt,
		LastLoginAt: user.LastLoginAt,
	}, true
}

type userPageCursor struct {
	ChainID   string    `json:"chain_id"`
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

func (h *Handler) decodeUserCursor(
	w http.ResponseWriter,
	r *http.Request,
) (*userauth.UserPageAfter, bool) {
	raw := r.URL.Query().Get("cursor")
	if raw == "" {
		return nil, true
	}
	var cursor userPageCursor
	if DecodeCursor(raw, &cursor) != nil ||
		cursor.ChainID != strconv.FormatUint(h.cfg.Chain.ID, 10) ||
		cursor.CreatedAt.IsZero() {
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is invalid or stale", nil)
		return nil, false
	}
	identifier, err := uuid.Parse(cursor.ID)
	if err != nil || identifier.Version() != 4 {
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is invalid or stale", nil)
		return nil, false
	}
	return &userauth.UserPageAfter{
		CreatedAt: cursor.CreatedAt, ID: identifier.String(),
	}, true
}

func (h *Handler) encodeUserCursor(user userauth.User) (string, error) {
	return EncodeCursor(userPageCursor{
		ChainID:   strconv.FormatUint(h.cfg.Chain.ID, 10),
		CreatedAt: user.CreatedAt, ID: user.ID,
	})
}

func validUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	identifier, err := uuid.Parse(r.PathValue("id"))
	if err != nil || identifier.Version() != 4 {
		writeError(w, r, http.StatusNotFound, "user_not_found", "user was not found", nil)
		return "", false
	}
	return identifier.String(), true
}

func decodeAuthJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	body := http.MaxBytesReader(w, r.Body, maximumAuthRequestBytes)
	data, err := io.ReadAll(body)
	if err != nil || len(data) == 0 ||
		jsonstrict.Validate(data, jsonstrict.Limits{MaxDepth: 8, MaxNodes: 128}) != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_auth_request", "authentication request is invalid", nil)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_auth_request", "authentication request is invalid", nil)
		return false
	}
	return true
}

func (h *Handler) handleUserAuthError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, userauth.ErrChallengeConsumed):
		writeError(w, r, http.StatusConflict, "challenge_consumed", "authentication challenge was already consumed", nil)
	case errors.Is(err, userauth.ErrChallengeExpired):
		writeError(w, r, http.StatusGone, "challenge_expired", "authentication challenge expired", nil)
	case errors.Is(err, userauth.ErrChallengeInvalid):
		writeError(w, r, http.StatusBadRequest, "challenge_invalid", "authentication challenge is invalid", nil)
	case errors.Is(err, userauth.ErrSignatureInvalid):
		writeError(w, r, http.StatusUnauthorized, "signature_invalid", "wallet signature is invalid", nil)
	case errors.Is(err, userauth.ErrSessionInvalid):
		writeError(w, r, http.StatusUnauthorized, "authentication_required", "user authentication is required", nil)
	case errors.Is(err, userauth.ErrCSRFInvalid):
		writeError(w, r, http.StatusForbidden, "csrf_invalid", "CSRF token is invalid", nil)
	case errors.Is(err, userauth.ErrUserDisabled):
		writeError(w, r, http.StatusForbidden, "user_disabled", "user is disabled", nil)
	case errors.Is(err, userauth.ErrUserNotFound):
		writeError(w, r, http.StatusNotFound, "user_not_found", "user was not found", nil)
	case errors.Is(err, userauth.ErrInvalidInput):
		writeError(w, r, http.StatusBadRequest, "invalid_auth_request", "authentication request is invalid", nil)
	default:
		h.logUserAuthFailure(r, "user authentication operation failed", err)
		h.writeUserAuthUnavailable(w, r)
	}
}

func (h *Handler) logUserAuthFailure(r *http.Request, message string, err error) {
	errorType := "<nil>"
	if err != nil {
		errorType = fmt.Sprintf("%T", err)
	}
	h.logger.LogAttrs(
		r.Context(), slog.LevelError, message,
		slog.String("error_code", "user_auth_operation_failed"),
		slog.String("request_id", requestIDFrom(r.Context())),
		slog.String("error_type", errorType),
	)
}

func (h *Handler) writeUserAuthUnavailable(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusServiceUnavailable, "user_auth_unavailable", "user authentication is unavailable", nil)
}
