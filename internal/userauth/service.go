package userauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/signinwithethereum/siwe-go"
)

const (
	defaultTouchInterval = 5 * time.Minute
	siweNonceLength      = 24
	maxSIWEMessageBytes  = 4096
)

type Options struct {
	ChainID       uint64
	PublicURL     string
	ChallengeTTL  time.Duration
	SessionTTL    time.Duration
	Pepper        []byte
	Now           func() time.Time
	Random        io.Reader
	TouchInterval time.Duration
}

type Service struct {
	repository AuthenticationRepository
	chainID    uint64
	chainIDInt int
	scheme     string
	authority  string
	origin     string

	challengeTTL  time.Duration
	sessionTTL    time.Duration
	touchInterval time.Duration
	pepper        []byte
	now           func() time.Time
	random        io.Reader
}

func NewService(repository AuthenticationRepository, options Options) (*Service, error) {
	if repository == nil {
		return nil, errors.New("user authentication repository is required")
	}
	if options.ChainID == 0 || options.ChainID > uint64(^uint(0)>>1) {
		return nil, errors.New("user authentication chain ID does not fit the SIWE verifier")
	}
	scheme, authority, origin, err := normalizePublicOrigin(options.PublicURL)
	if err != nil {
		return nil, err
	}
	if options.ChallengeTTL <= 0 || options.ChallengeTTL > time.Hour {
		return nil, errors.New("user authentication challenge TTL must be between 1ms and 1h")
	}
	challengeTTL := options.ChallengeTTL.Truncate(time.Millisecond)
	if challengeTTL == 0 {
		return nil, errors.New("user authentication challenge TTL must be between 1ms and 1h")
	}
	if options.SessionTTL <= 0 || options.SessionTTL > 365*24*time.Hour {
		return nil, errors.New("user authentication session TTL must be between 1us and 365d")
	}
	sessionTTL := options.SessionTTL.Truncate(time.Microsecond)
	if sessionTTL == 0 {
		return nil, errors.New("user authentication session TTL must be between 1us and 365d")
	}
	if len(options.Pepper) < 32 {
		return nil, errors.New("user authentication pepper must contain at least 32 bytes")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	random := options.Random
	if random == nil {
		random = rand.Reader
	}
	touchInterval := options.TouchInterval
	if touchInterval == 0 {
		touchInterval = defaultTouchInterval
	}
	if touchInterval < 0 || touchInterval > sessionTTL {
		return nil, errors.New("user authentication touch interval is invalid")
	}
	return &Service{
		repository: repository,
		chainID:    options.ChainID, chainIDInt: int(options.ChainID),
		scheme: scheme, authority: authority, origin: origin,
		challengeTTL: challengeTTL, sessionTTL: sessionTTL,
		touchInterval: touchInterval,
		pepper:        append([]byte(nil), options.Pepper...),
		now:           now, random: random,
	}, nil
}

func (service *Service) CreateChallenge(ctx context.Context, address string) (Challenge, error) {
	if service == nil || service.repository == nil {
		return Challenge{}, errors.New("user authentication service is not configured")
	}
	canonicalAddress, err := normalizeAddress(address)
	if err != nil {
		return Challenge{}, err
	}
	id, err := randomUUID(service.random)
	if err != nil {
		return Challenge{}, fmt.Errorf("generate authentication challenge ID: %w", err)
	}
	nonce, err := generateNonce(service.random, siweNonceLength)
	if err != nil {
		return Challenge{}, err
	}
	issuedAt := service.now().UTC().Truncate(time.Millisecond)
	expiresAt := issuedAt.Add(service.challengeTTL)
	message, err := siwe.NewMessage(
		service.authority,
		canonicalAddress,
		service.origin,
		nonce,
		map[string]interface{}{
			"scheme":         service.scheme,
			"chainId":        service.chainIDInt,
			"issuedAt":       issuedAt,
			"expirationTime": expiresAt,
			"requestId":      id,
		},
	)
	if err != nil {
		return Challenge{}, errors.New("construct canonical SIWE message")
	}
	// siwe-go warns for every all-lower/all-upper address, including valid
	// EIP-55 values that contain no case-bearing characters. The independently
	// normalized address and exact message round trip below are authoritative.
	challenge := Challenge{
		ID: id, ChainID: service.chainID, Address: canonicalAddress,
		Message: message.String(), Nonce: nonce,
		IssuedAt: issuedAt, ExpiresAt: expiresAt,
	}
	if len(challenge.Message) == 0 || len(challenge.Message) > maxSIWEMessageBytes {
		return Challenge{}, errors.New("canonical SIWE message exceeds storage limit")
	}
	stored, err := service.repository.CreateChallenge(ctx, challenge)
	if err != nil {
		return Challenge{}, err
	}
	if !sameChallenge(stored, challenge, true) {
		return Challenge{}, errStoredStateInvalid
	}
	return stored, nil
}

func (service *Service) VerifyChallenge(
	ctx context.Context,
	challengeID string,
	signature string,
) (LoginResult, error) {
	if service == nil || service.repository == nil {
		return LoginResult{}, errors.New("user authentication service is not configured")
	}
	normalizedID, err := normalizeUUID(challengeID)
	if err != nil {
		return LoginResult{}, ErrChallengeInvalid
	}
	if !validSignatureEncoding(signature) {
		return LoginResult{}, ErrSignatureInvalid
	}
	challenge, err := service.repository.Challenge(ctx, normalizedID)
	if err != nil {
		switch {
		case errors.Is(err, ErrChallengeConsumed):
			return LoginResult{}, ErrChallengeConsumed
		case errors.Is(err, ErrChallengeExpired):
			return LoginResult{}, ErrChallengeExpired
		case errors.Is(err, ErrChallengeInvalid):
			return LoginResult{}, ErrChallengeInvalid
		}
		return LoginResult{}, err
	}
	now := service.now().UTC().Truncate(time.Millisecond)
	if err := service.validateChallenge(challenge, now); err != nil {
		return LoginResult{}, err
	}
	message, err := siwe.ParseMessage(challenge.Message)
	if err != nil || !service.messageMatchesChallenge(message, challenge) {
		return LoginResult{}, ErrChallengeInvalid
	}
	requestID := challenge.ID
	result, err := message.VerifyWith(ctx, signature, siwe.VerifyParams{
		Scheme: &service.scheme, Domain: &service.authority,
		Nonce: &challenge.Nonce, URI: &service.origin,
		ChainID: &service.chainIDInt, RequestID: &requestID, Time: &now,
	}, siwe.VerifyOptions{})
	if err != nil || result == nil || result.ContractVerified || result.ECDSAPublicKey == nil {
		// siwe errors may contain the supplied signature or message fragments.
		// Do not wrap or expose them.
		return LoginResult{}, ErrSignatureInvalid
	}

	tokenValue, token, err := randomOpaqueValue(service.random)
	if err != nil {
		return LoginResult{}, err
	}
	csrfValue := deriveCSRFValue(service.pepper, tokenValue)
	csrfToken := base64.RawURLEncoding.EncodeToString(csrfValue[:])
	userID, err := randomUUID(service.random)
	if err != nil {
		return LoginResult{}, fmt.Errorf("generate user ID: %w", err)
	}
	sessionID, err := randomUUID(service.random)
	if err != nil {
		return LoginResult{}, fmt.Errorf("generate session ID: %w", err)
	}
	sessionCreatedAt := now.UTC().Truncate(time.Microsecond)
	material := SessionMaterial{
		ID:          sessionID,
		TokenDigest: sessionDigest(service.pepper, tokenValue),
		CSRFDigest:  csrfDigest(service.pepper, csrfValue),
		CreatedAt:   sessionCreatedAt,
		ExpiresAt:   sessionCreatedAt.Add(service.sessionTTL),
	}
	session, err := service.repository.CompleteLogin(ctx, challenge, userID, material)
	if err != nil {
		return LoginResult{}, err
	}
	if !constantTimeEqual(session.csrfDigest[:], material.CSRFDigest[:]) {
		return LoginResult{}, errStoredStateInvalid
	}
	return LoginResult{
		Session:     session,
		Credentials: Credentials{Token: token, CSRFToken: csrfToken},
		csrfValue:   csrfValue,
		valid:       true,
	}, nil
}

func (service *Service) Authenticate(ctx context.Context, token string) (Authentication, error) {
	if service == nil || service.repository == nil {
		return Authentication{}, errors.New("user authentication service is not configured")
	}
	tokenValue, err := decodeOpaqueValue(token)
	if err != nil {
		return Authentication{}, ErrSessionInvalid
	}
	now := service.now().UTC().Truncate(time.Microsecond)
	session, err := service.repository.ActiveSession(
		ctx,
		sessionDigest(service.pepper, tokenValue),
		now,
		now.Add(-service.touchInterval),
	)
	if err != nil {
		if errors.Is(err, ErrSessionInvalid) {
			return Authentication{}, ErrSessionInvalid
		}
		return Authentication{}, err
	}
	csrfValue := deriveCSRFValue(service.pepper, tokenValue)
	actualDigest := csrfDigest(service.pepper, csrfValue)
	if !constantTimeEqual(actualDigest[:], session.csrfDigest[:]) {
		return Authentication{}, errStoredStateInvalid
	}
	return Authentication{
		Session:   session,
		CSRFToken: base64.RawURLEncoding.EncodeToString(csrfValue[:]),
		csrfValue: csrfValue,
		valid:     true,
	}, nil
}

func (service *Service) Logout(ctx context.Context, token string) (bool, error) {
	if service == nil || service.repository == nil {
		return false, errors.New("user authentication service is not configured")
	}
	tokenValue, err := decodeOpaqueValue(token)
	if err != nil {
		return false, ErrSessionInvalid
	}
	return service.repository.RevokeSession(
		ctx,
		sessionDigest(service.pepper, tokenValue),
		service.now().UTC().Truncate(time.Microsecond),
	)
}

func (service *Service) validateChallenge(challenge Challenge, now time.Time) error {
	if challenge.ID == "" || challenge.ChainID != service.chainID ||
		len(challenge.Message) == 0 || len(challenge.Message) > maxSIWEMessageBytes ||
		now.Before(challenge.IssuedAt) {
		return ErrChallengeInvalid
	}
	if challenge.ConsumedAt != nil {
		return ErrChallengeConsumed
	}
	if !now.Before(challenge.ExpiresAt) {
		return ErrChallengeExpired
	}
	if _, err := normalizeUUID(challenge.ID); err != nil {
		return ErrChallengeInvalid
	}
	address, err := normalizeAddress(challenge.Address)
	if err != nil || address != challenge.Address {
		return ErrChallengeInvalid
	}
	return nil
}

func (service *Service) messageMatchesChallenge(message *siwe.Message, challenge Challenge) bool {
	if message == nil || message.String() != challenge.Message ||
		message.Version != "1" || message.Domain != service.authority ||
		message.URI != service.origin || message.ChainID != service.chainIDInt ||
		message.Nonce != challenge.Nonce || message.Address.Hex() != challenge.Address ||
		(message.AddressRaw != nil && "0x"+*message.AddressRaw != challenge.Address) ||
		message.Statement != nil || message.NotBefore != nil || message.Resources != nil ||
		message.Scheme == nil || *message.Scheme != service.scheme ||
		message.RequestID == nil || *message.RequestID != challenge.ID ||
		message.ExpirationTime == nil {
		return false
	}
	issuedAt, issuedErr := time.Parse(time.RFC3339Nano, message.IssuedAt)
	expiresAt, expiresErr := time.Parse(time.RFC3339Nano, *message.ExpirationTime)
	return issuedErr == nil && expiresErr == nil &&
		issuedAt.Equal(challenge.IssuedAt) && expiresAt.Equal(challenge.ExpiresAt)
}

func validSignatureEncoding(signature string) bool {
	if len(signature) != 2+65*2 || !strings.HasPrefix(signature, "0x") {
		return false
	}
	decoded, err := hex.DecodeString(signature[2:])
	return err == nil && len(decoded) == 65
}

func normalizeAddress(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !common.IsHexAddress(value) {
		return "", fmt.Errorf("%w: address must be 20-byte hexadecimal", ErrInvalidInput)
	}
	return common.HexToAddress(value).Hex(), nil
}

func normalizeUUID(value string) (string, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Version() != 4 {
		return "", fmt.Errorf("%w: ID must be a UUIDv4", ErrInvalidInput)
	}
	return parsed.String(), nil
}

func randomUUID(random io.Reader) (string, error) {
	id, err := uuid.NewRandomFromReader(random)
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

func normalizePublicOrigin(raw string) (string, string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Opaque != "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		(parsed.EscapedPath() != "" && parsed.EscapedPath() != "/") {
		return "", "", "", errors.New("user authentication public URL must be an absolute root origin")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && scheme != "http" {
		return "", "", "", errors.New("user authentication public URL must use HTTP or HTTPS")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" || strings.HasSuffix(host, ".") || strings.Contains(host, "%") ||
		strings.HasSuffix(parsed.Host, ":") {
		return "", "", "", errors.New("user authentication public URL host is invalid")
	}
	for index := 0; index < len(host); index++ {
		if host[index] > 0x7f {
			return "", "", "", errors.New("user authentication public URL host must be ASCII")
		}
	}
	if scheme == "http" {
		address := net.ParseIP(host)
		if host != "localhost" && (address == nil || !address.IsLoopback()) {
			return "", "", "", errors.New("user authentication public URL may use HTTP only for loopback development")
		}
	}
	port := parsed.Port()
	if port != "" {
		number, parseErr := strconv.ParseUint(port, 10, 16)
		if parseErr != nil || number == 0 {
			return "", "", "", errors.New("user authentication public URL port must be between 1 and 65535")
		}
	}
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	authority := host
	if net.ParseIP(host) != nil && strings.Contains(host, ":") {
		authority = "[" + host + "]"
	}
	if port != "" {
		authority = net.JoinHostPort(host, port)
	}
	origin := scheme + "://" + authority
	return scheme, authority, origin, nil
}

// CanonicalPublicOrigin returns the exact Origin value bound into SIWE and
// required by the browser HTTP boundary.
func CanonicalPublicOrigin(raw string) (string, error) {
	_, _, origin, err := normalizePublicOrigin(raw)
	return origin, err
}

func sameChallenge(left, right Challenge, includeConsumed bool) bool {
	if left.ID != right.ID || left.ChainID != right.ChainID ||
		left.Address != right.Address || left.Message != right.Message ||
		left.Nonce != right.Nonce || !left.IssuedAt.Equal(right.IssuedAt) ||
		!left.ExpiresAt.Equal(right.ExpiresAt) {
		return false
	}
	if !includeConsumed {
		return true
	}
	if left.ConsumedAt == nil || right.ConsumedAt == nil {
		return left.ConsumedAt == nil && right.ConsumedAt == nil
	}
	return left.ConsumedAt.Equal(*right.ConsumedAt)
}
