// Package auth implements operator-issued API keys and request quotas without
// requiring end-user accounts.
package auth

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidAPIKey = errors.New("invalid API key")
	ErrRevokedAPIKey = errors.New("revoked API key")
	ErrMissingAPIKey = errors.New("API key is required")
)

type APIKey struct {
	Prefix    string
	Digest    []byte
	Name      string
	Rate      int
	Burst     int
	CreatedAt time.Time
	RevokedAt *time.Time
}

type IssuedAPIKey struct {
	Token  string
	Record APIKey
}

type Repository interface {
	Put(context.Context, APIKey) error
	ByPrefix(context.Context, string) (APIKey, error)
	Revoke(context.Context, string, time.Time) error
	List(context.Context) ([]APIKey, error)
}

// RotationRepository atomically revokes an active key and stores its
// replacement. Rotation must never leave both keys active or revoke the old
// key without durably storing the new keyed digest.
type RotationRepository interface {
	Rotate(context.Context, string, APIKey) error
}

type Manager struct {
	Repository                    Repository
	Pepper                        []byte
	Now                           func() time.Time
	Random                        func([]byte) (int, error)
	MaxCompatibilityFormBodyBytes int64
}

func (m Manager) Create(ctx context.Context, name string, rate, burst int) (IssuedAPIKey, error) {
	if m.Repository == nil {
		return IssuedAPIKey{}, errors.New("API key repository is required")
	}
	issued, err := m.issue(name, rate, burst)
	if err != nil {
		return IssuedAPIKey{}, err
	}
	if err := m.Repository.Put(ctx, issued.Record); err != nil {
		return IssuedAPIKey{}, err
	}
	return issued, nil
}

// Rotate replaces one active key while preserving its operator-visible name
// and quotas. The replacement plaintext is revealed only in the return value;
// repositories receive only its keyed digest.
func (m Manager) Rotate(ctx context.Context, prefix string) (IssuedAPIKey, error) {
	if m.Repository == nil {
		return IssuedAPIKey{}, errors.New("API key repository is required")
	}
	rotation, ok := m.Repository.(RotationRepository)
	if !ok {
		return IssuedAPIKey{}, errors.New("API key repository does not support atomic rotation")
	}
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if !validPrefix(prefix) {
		return IssuedAPIKey{}, errors.New("API key prefix is invalid")
	}
	current, err := m.Repository.ByPrefix(ctx, prefix)
	if err != nil {
		return IssuedAPIKey{}, ErrInvalidAPIKey
	}
	if current.RevokedAt != nil {
		return IssuedAPIKey{}, ErrRevokedAPIKey
	}
	issued, err := m.issue(current.Name, current.Rate, current.Burst)
	if err != nil {
		return IssuedAPIKey{}, err
	}
	if issued.Record.Prefix == prefix {
		return IssuedAPIKey{}, errors.New("replacement API key prefix collided with the active key")
	}
	if err := rotation.Rotate(ctx, prefix, issued.Record); err != nil {
		return IssuedAPIKey{}, err
	}
	return issued, nil
}

func (m Manager) issue(name string, rate, burst int) (IssuedAPIKey, error) {
	if len(m.Pepper) < 32 {
		return IssuedAPIKey{}, errors.New("API key pepper must contain at least 32 bytes")
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 {
		return IssuedAPIKey{}, errors.New("API key name must contain 1 to 128 bytes")
	}
	if rate <= 0 || burst < rate {
		return IssuedAPIKey{}, errors.New("API key rate must be positive and burst must be at least rate")
	}
	random := m.Random
	if random == nil {
		random = rand.Read
	}
	prefixBytes, secretBytes := make([]byte, 6), make([]byte, 32)
	if _, err := random(prefixBytes); err != nil {
		return IssuedAPIKey{}, fmt.Errorf("generate API key prefix: %w", err)
	}
	if _, err := random(secretBytes); err != nil {
		return IssuedAPIKey{}, fmt.Errorf("generate API key secret: %w", err)
	}
	prefix := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(prefixBytes))
	token := "evk_" + prefix + "_" + base64.RawURLEncoding.EncodeToString(secretBytes)
	now := time.Now().UTC()
	if m.Now != nil {
		now = m.Now().UTC()
	}
	record := APIKey{Prefix: prefix, Digest: digest(m.Pepper, token), Name: name, Rate: rate, Burst: burst, CreatedAt: now}
	return IssuedAPIKey{Token: token, Record: record}, nil
}

func (m Manager) Authenticate(ctx context.Context, token string) (APIKey, error) {
	if len(m.Pepper) < 32 || m.Repository == nil {
		return APIKey{}, errors.New("API key manager is not configured")
	}
	prefix, ok := parseToken(token)
	if !ok {
		return APIKey{}, ErrInvalidAPIKey
	}
	record, err := m.Repository.ByPrefix(ctx, prefix)
	if err != nil {
		return APIKey{}, ErrInvalidAPIKey
	}
	actual := digest(m.Pepper, token)
	if len(record.Digest) != len(actual) || subtle.ConstantTimeCompare(record.Digest, actual) != 1 {
		return APIKey{}, ErrInvalidAPIKey
	}
	if record.RevokedAt != nil {
		return APIKey{}, ErrRevokedAPIKey
	}
	return record, nil
}

func parseToken(token string) (string, bool) {
	// The base64url secret may itself contain underscores. Only the first two
	// underscores are structural delimiters.
	parts := strings.SplitN(token, "_", 3)
	if len(parts) != 3 || parts[0] != "evk" || len(parts[1]) != 10 || len(parts[2]) != 43 {
		return "", false
	}
	if _, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(parts[1])); err != nil {
		return "", false
	}
	if secret, err := base64.RawURLEncoding.DecodeString(parts[2]); err != nil || len(secret) != 32 {
		return "", false
	}
	return parts[1], true
}

func validPrefix(prefix string) bool {
	if len(prefix) != 10 || prefix != strings.ToLower(prefix) {
		return false
	}
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(prefix))
	return err == nil && len(decoded) == 6
}

func digest(pepper []byte, token string) []byte {
	hasher := hmac.New(sha256.New, pepper)
	_, _ = hasher.Write([]byte(token))
	return hasher.Sum(nil)
}

type Identity struct {
	Authenticated bool
	Prefix        string
	Name          string
	Rate          int
	Burst         int
}

type identityKey struct{}

type nativeBoundaryError struct {
	Error nativeBoundaryErrorBody `json:"error"`
}

type nativeBoundaryErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

type compatibilityBoundaryError struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Result  string `json:"result"`
}

func IdentityFrom(ctx context.Context) Identity {
	identity, _ := ctx.Value(identityKey{}).(Identity)
	return identity
}

// Middleware authenticates an optional API key. Native APIs accept only the
// X-API-Key header so credentials never enter URLs; Etherscan-compatible
// apikey query and bounded URL-encoded POST form values are accepted only on
// the exact /v2/api boundary. Conflicting credential sources are rejected.
func (m Manager) Middleware(require bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		compatibility := r.URL.Path == "/v2/api"
		header, unique := singleAPIKey(r.Header.Values("X-API-Key"))
		if !unique {
			writeAuthError(w, r, http.StatusBadRequest, "ambiguous_api_key")
			return
		}
		query, unique := singleAPIKey(r.URL.Query()["apikey"])
		if !unique {
			writeAuthError(w, r, http.StatusBadRequest, "ambiguous_api_key")
			return
		}
		if query != "" && !compatibility {
			writeAuthError(w, r, http.StatusBadRequest, "api_key_query_not_allowed")
			return
		}
		form := ""
		if compatibility && r.Method == http.MethodPost {
			var code string
			form, code = m.compatibilityFormAPIKey(r)
			if code != "" {
				status := http.StatusBadRequest
				if code == "api_key_form_too_large" {
					status = http.StatusRequestEntityTooLarge
				}
				writeAuthError(w, r, status, code)
				return
			}
		}
		token, unambiguous := selectAPIKey(header, query, form)
		if !unambiguous {
			writeAuthError(w, r, http.StatusBadRequest, "ambiguous_api_key")
			return
		}
		if token == "" {
			if require {
				writeAuthError(w, r, http.StatusUnauthorized, "api_key_required")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		record, err := m.Authenticate(r.Context(), token)
		if err != nil {
			writeAuthError(w, r, http.StatusUnauthorized, "invalid_api_key")
			return
		}
		identity := Identity{Authenticated: true, Prefix: record.Prefix, Name: record.Name, Rate: record.Rate, Burst: record.Burst}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, identity)))
	})
}

func (m Manager) compatibilityFormAPIKey(r *http.Request) (string, string) {
	contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
	if contentType == "" || r.Body == nil || r.Body == http.NoBody {
		return "", ""
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", "invalid_api_key_form"
	}
	if mediaType != "application/x-www-form-urlencoded" {
		return "", ""
	}
	maximum := m.MaxCompatibilityFormBodyBytes
	if maximum <= 0 {
		maximum = 6 << 20
	}
	if r.ContentLength > maximum {
		return "", "api_key_form_too_large"
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maximum+1))
	_ = r.Body.Close()
	if err != nil {
		return "", "invalid_api_key_form"
	}
	if int64(len(body)) > maximum {
		return "", "api_key_form_too_large"
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return "", "invalid_api_key_form"
	}
	credential, unique := singleAPIKey(values["apikey"])
	if !unique {
		return "", "ambiguous_api_key"
	}
	return credential, ""
}

func singleAPIKey(values []string) (string, bool) {
	credential := ""
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if credential != "" {
			return "", false
		}
		credential = value
	}
	return credential, true
}

func selectAPIKey(values ...string) (string, bool) {
	selected := ""
	for _, value := range values {
		if value == "" {
			continue
		}
		if selected != "" && selected != value {
			return "", false
		}
		if selected == "" {
			selected = value
		}
	}
	return selected, true
}

func writeAuthError(w http.ResponseWriter, r *http.Request, status int, code string) {
	requestID := boundaryRequestID(r)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(status)
	if r.URL.Path == "/v2/api" {
		_ = json.NewEncoder(w).Encode(compatibilityBoundaryError{
			Status: "0", Message: "NOTOK", Result: "authentication failed: " + code,
		})
		return
	}
	_ = json.NewEncoder(w).Encode(nativeBoundaryError{Error: nativeBoundaryErrorBody{
		Code: code, Message: "authentication failed", RequestID: requestID,
	}})
}

func boundaryRequestID(r *http.Request) string {
	requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
	if requestID != "" && len(requestID) <= 128 {
		return requestID
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err == nil {
		return base64.RawURLEncoding.EncodeToString(random)
	}
	return fmt.Sprintf("auth-%d", time.Now().UnixNano())
}

type MemoryRepository struct {
	mu      sync.RWMutex
	records map[string]APIKey
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{records: make(map[string]APIKey)}
}

func (r *MemoryRepository) Put(_ context.Context, key APIKey) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.records[key.Prefix]; exists {
		return errors.New("API key prefix already exists")
	}
	key.Digest = append([]byte(nil), key.Digest...)
	r.records[key.Prefix] = key
	return nil
}

func (r *MemoryRepository) ByPrefix(_ context.Context, prefix string) (APIKey, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key, exists := r.records[prefix]
	if !exists {
		return APIKey{}, errors.New("API key not found")
	}
	key.Digest = append([]byte(nil), key.Digest...)
	return key, nil
}

func (r *MemoryRepository) Revoke(_ context.Context, prefix string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key, exists := r.records[prefix]
	if !exists {
		return errors.New("API key not found")
	}
	at = at.UTC()
	key.RevokedAt = &at
	r.records[prefix] = key
	return nil
}

func (r *MemoryRepository) Rotate(_ context.Context, prefix string, replacement APIKey) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, exists := r.records[prefix]
	if !exists {
		return errors.New("API key not found")
	}
	if current.RevokedAt != nil {
		return ErrRevokedAPIKey
	}
	if _, exists := r.records[replacement.Prefix]; exists {
		return errors.New("replacement API key prefix already exists")
	}
	if replacement.Name != current.Name || replacement.Rate != current.Rate || replacement.Burst != current.Burst {
		return errors.New("replacement API key policy differs from active key")
	}
	revokedAt := replacement.CreatedAt.UTC()
	current.RevokedAt = &revokedAt
	replacement.Digest = append([]byte(nil), replacement.Digest...)
	r.records[prefix] = current
	r.records[replacement.Prefix] = replacement
	return nil
}

func (r *MemoryRepository) List(_ context.Context) ([]APIKey, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]APIKey, 0, len(r.records))
	for _, key := range r.records {
		key.Digest = nil
		items = append(items, key)
	}
	return items, nil
}
