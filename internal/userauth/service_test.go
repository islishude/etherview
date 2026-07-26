package userauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/signinwithethereum/siwe-go"
)

func TestServiceChallengeLoginSessionLifecycle(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 26, 8, 9, 10, 123456789, time.UTC)
	repository := newMemoryRepository()
	service := newTestService(t, repository, &now, &incrementingReader{})
	privateKey, err := crypto.HexToECDSA(
		"4c0883a69102937d6231471b5dbb6204fe5129617082791c64a2d1f769c0f4f7",
	)
	if err != nil {
		t.Fatal(err)
	}
	address := crypto.PubkeyToAddress(privateKey.PublicKey).Hex()

	challenge, err := service.CreateChallenge(t.Context(), strings.ToLower(address))
	if err != nil {
		t.Fatal(err)
	}
	if challenge.Address != address || challenge.ChainID != 11155111 ||
		challenge.ExpiresAt.Sub(challenge.IssuedAt) != 5*time.Minute {
		t.Fatalf("challenge = %+v", challenge)
	}
	message, err := siwe.ParseMessage(challenge.Message)
	if err != nil {
		t.Fatal(err)
	}
	if message.Domain != "explorer.example" || message.URI != "https://explorer.example" ||
		message.Scheme == nil || *message.Scheme != "https" ||
		message.RequestID == nil || *message.RequestID != challenge.ID ||
		message.ChainID != 11155111 || message.Nonce != challenge.Nonce {
		t.Fatalf("SIWE binding = %+v", message)
	}
	signature, err := crypto.Sign(message.EIP191Hash().Bytes(), privateKey)
	if err != nil {
		t.Fatal(err)
	}
	login, err := service.VerifyChallenge(
		t.Context(), challenge.ID, hexutil.Encode(signature),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(login.Credentials.Token) != opaqueValueChars ||
		len(login.Credentials.CSRFToken) != opaqueValueChars {
		t.Fatalf("credential lengths = %d, %d", len(login.Credentials.Token), len(login.Credentials.CSRFToken))
	}
	if login.Session.User.Role != RoleUser ||
		login.Session.User.Status != StatusActive ||
		login.Session.User.Address != address ||
		login.Session.ExpiresAt.Sub(login.Session.CreatedAt) != 7*24*time.Hour {
		t.Fatalf("login session = %+v", login.Session)
	}
	if bytes.Contains(repository.lastMaterial.TokenDigest[:], []byte(login.Credentials.Token)) ||
		bytes.Contains(repository.lastMaterial.CSRFDigest[:], []byte(login.Credentials.CSRFToken)) {
		t.Fatal("repository received plaintext credentials")
	}

	authentication, err := service.Authenticate(t.Context(), login.Credentials.Token)
	if err != nil {
		t.Fatal(err)
	}
	if authentication.Session.ID != login.Session.ID ||
		authentication.CSRFToken != login.Credentials.CSRFToken {
		t.Fatalf("authentication = %+v", authentication)
	}
	if err := authentication.ValidateCSRF(login.Credentials.CSRFToken); err != nil {
		t.Fatal(err)
	}
	if err := authentication.ValidateCSRF(strings.Repeat("A", opaqueValueChars)); !errors.Is(err, ErrCSRFInvalid) {
		t.Fatalf("wrong CSRF error = %v", err)
	}
	rotatedService, err := NewService(repository, Options{
		ChainID: 11155111, PublicURL: "https://explorer.example",
		ChallengeTTL: 5 * time.Minute, SessionTTL: 7 * 24 * time.Hour,
		Pepper: bytes.Repeat([]byte{8}, 32),
		Now:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rotatedService.Authenticate(
		t.Context(), login.Credentials.Token,
	); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("authentication after pepper rotation error = %v", err)
	}
	if err := (Authentication{}).ValidateCSRF(
		base64.RawURLEncoding.EncodeToString(make([]byte, opaqueValueBytes)),
	); !errors.Is(err, ErrCSRFInvalid) {
		t.Fatalf("zero-value authentication CSRF error = %v", err)
	}
	if err := (LoginResult{}).Authentication().ValidateCSRF(
		base64.RawURLEncoding.EncodeToString(make([]byte, opaqueValueBytes)),
	); !errors.Is(err, ErrCSRFInvalid) {
		t.Fatalf("zero-value login result CSRF error = %v", err)
	}

	revoked, err := service.Logout(t.Context(), login.Credentials.Token)
	if err != nil || !revoked {
		t.Fatalf("logout revoked=%t error=%v", revoked, err)
	}
	if _, err := service.Authenticate(t.Context(), login.Credentials.Token); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("authentication after logout error = %v", err)
	}
	if _, err := service.VerifyChallenge(t.Context(), challenge.ID, hexutil.Encode(signature)); !errors.Is(err, ErrChallengeInvalid) {
		t.Fatalf("challenge replay error = %v", err)
	}
}

func TestServiceRejectsSignatureAndChallengeBoundaryFailures(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 26, 8, 9, 10, 0, time.UTC)
	privateKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	wrongKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("wrong signer", func(t *testing.T) {
		repository := newMemoryRepository()
		service := newTestService(t, repository, &now, &incrementingReader{})
		challenge, err := service.CreateChallenge(t.Context(), crypto.PubkeyToAddress(privateKey.PublicKey).Hex())
		if err != nil {
			t.Fatal(err)
		}
		message, _ := siwe.ParseMessage(challenge.Message)
		signature, _ := crypto.Sign(message.EIP191Hash().Bytes(), wrongKey)
		if _, err := service.VerifyChallenge(t.Context(), challenge.ID, hexutil.Encode(signature)); !errors.Is(err, ErrSignatureInvalid) {
			t.Fatalf("wrong signer error = %v", err)
		}
		if repository.completeCalls != 0 {
			t.Fatalf("invalid signature completed %d logins", repository.completeCalls)
		}
	})

	t.Run("expired", func(t *testing.T) {
		repository := newMemoryRepository()
		current := now
		service := newTestService(t, repository, &current, &incrementingReader{})
		challenge, err := service.CreateChallenge(t.Context(), crypto.PubkeyToAddress(privateKey.PublicKey).Hex())
		if err != nil {
			t.Fatal(err)
		}
		message, _ := siwe.ParseMessage(challenge.Message)
		signature, _ := crypto.Sign(message.EIP191Hash().Bytes(), privateKey)
		current = current.Add(5 * time.Minute)
		if _, err := service.VerifyChallenge(t.Context(), challenge.ID, hexutil.Encode(signature)); !errors.Is(err, ErrChallengeInvalid) {
			t.Fatalf("expired challenge error = %v", err)
		}
	})

	t.Run("stored binding mutation", func(t *testing.T) {
		mutations := map[string]func(*Challenge){
			"address": func(challenge *Challenge) {
				challenge.Address = crypto.PubkeyToAddress(wrongKey.PublicKey).Hex()
			},
			"chain": func(challenge *Challenge) {
				challenge.ChainID++
			},
			"domain": func(challenge *Challenge) {
				challenge.Message = strings.Replace(
					challenge.Message, "explorer.example", "evil.example", 1,
				)
			},
			"URI": func(challenge *Challenge) {
				challenge.Message = strings.Replace(
					challenge.Message,
					"URI: https://explorer.example",
					"URI: https://evil.example",
					1,
				)
			},
			"expiration": func(challenge *Challenge) {
				challenge.ExpiresAt = challenge.ExpiresAt.Add(time.Second)
			},
		}
		for name, mutate := range mutations {
			t.Run(name, func(t *testing.T) {
				repository := newMemoryRepository()
				service := newTestService(t, repository, &now, &incrementingReader{})
				challenge, err := service.CreateChallenge(
					t.Context(), crypto.PubkeyToAddress(privateKey.PublicKey).Hex(),
				)
				if err != nil {
					t.Fatal(err)
				}
				message, _ := siwe.ParseMessage(challenge.Message)
				signature, _ := crypto.Sign(message.EIP191Hash().Bytes(), privateKey)
				mutate(&repository.challenge)
				if _, err := service.VerifyChallenge(
					t.Context(), challenge.ID, hexutil.Encode(signature),
				); !errors.Is(err, ErrChallengeInvalid) {
					t.Fatalf("mutated binding error = %v", err)
				}
			})
		}
	})

	t.Run("malleable and invalid recovery signatures", func(t *testing.T) {
		repository := newMemoryRepository()
		service := newTestService(t, repository, &now, &incrementingReader{})
		challenge, err := service.CreateChallenge(
			t.Context(), crypto.PubkeyToAddress(privateKey.PublicKey).Hex(),
		)
		if err != nil {
			t.Fatal(err)
		}
		message, _ := siwe.ParseMessage(challenge.Message)
		signature, _ := crypto.Sign(message.EIP191Hash().Bytes(), privateKey)

		highS := append([]byte(nil), signature...)
		s := new(big.Int).SetBytes(highS[32:64])
		s.Sub(crypto.S256().Params().N, s).FillBytes(highS[32:64])
		highS[64] ^= 1
		invalidV := append([]byte(nil), signature...)
		invalidV[64] = 2
		for name, candidate := range map[string][]byte{
			"high-S": highS, "invalid-v": invalidV,
		} {
			t.Run(name, func(t *testing.T) {
				if _, err := service.VerifyChallenge(
					t.Context(), challenge.ID, hexutil.Encode(candidate),
				); !errors.Is(err, ErrSignatureInvalid) {
					t.Fatalf("signature error = %v", err)
				}
			})
		}
		if repository.completeCalls != 0 {
			t.Fatalf("invalid signatures completed %d logins", repository.completeCalls)
		}
	})

	t.Run("disabled user consumes challenge", func(t *testing.T) {
		repository := newMemoryRepository()
		repository.disabled = true
		service := newTestService(t, repository, &now, &incrementingReader{})
		challenge, err := service.CreateChallenge(t.Context(), crypto.PubkeyToAddress(privateKey.PublicKey).Hex())
		if err != nil {
			t.Fatal(err)
		}
		message, _ := siwe.ParseMessage(challenge.Message)
		signature, _ := crypto.Sign(message.EIP191Hash().Bytes(), privateKey)
		if _, err := service.VerifyChallenge(t.Context(), challenge.ID, hexutil.Encode(signature)); !errors.Is(err, ErrUserDisabled) {
			t.Fatalf("disabled login error = %v", err)
		}
		if repository.challenge.ConsumedAt == nil || len(repository.sessions) != 0 {
			t.Fatalf("disabled challenge/session state = %+v / %d", repository.challenge, len(repository.sessions))
		}
	})

	for _, signature := range []string{
		"", "0x", strings.Repeat("0", 130), "0x" + strings.Repeat("g", 130),
		"0x" + strings.Repeat("0", 128),
	} {
		repository := newMemoryRepository()
		service := newTestService(t, repository, &now, &incrementingReader{})
		challenge, err := service.CreateChallenge(t.Context(), crypto.PubkeyToAddress(privateKey.PublicKey).Hex())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.VerifyChallenge(t.Context(), challenge.ID, signature); !errors.Is(err, ErrSignatureInvalid) {
			t.Errorf("signature %q error = %v", signature, err)
		}
	}
}

func TestServiceConfigurationAndRandomFailures(t *testing.T) {
	t.Parallel()
	repository := newMemoryRepository()
	base := Options{
		ChainID: 1, PublicURL: "https://EXAMPLE.com:443/",
		ChallengeTTL: 5 * time.Minute, SessionTTL: 7 * 24 * time.Hour,
		Pepper: bytes.Repeat([]byte{7}, 32),
	}
	service, err := NewService(repository, base)
	if err != nil {
		t.Fatal(err)
	}
	if service.origin != "https://example.com" ||
		service.authority != "example.com" || service.scheme != "https" {
		t.Fatalf("normalized origin = %q %q %q", service.scheme, service.authority, service.origin)
	}

	tests := []struct {
		name string
		edit func(*Options)
	}{
		{"zero chain", func(options *Options) { options.ChainID = 0 }},
		{"chain overflow", func(options *Options) { options.ChainID = uint64(^uint(0)>>1) + 1 }},
		{"path", func(options *Options) { options.PublicURL = "https://example.com/app" }},
		{"userinfo", func(options *Options) { options.PublicURL = "https://user@example.com" }},
		{"query", func(options *Options) { options.PublicURL = "https://example.com?x=1" }},
		{"scheme", func(options *Options) { options.PublicURL = "ftp://example.com" }},
		{"plaintext non-loopback", func(options *Options) { options.PublicURL = "http://example.com" }},
		{"empty port", func(options *Options) { options.PublicURL = "https://example.com:" }},
		{"zero port", func(options *Options) { options.PublicURL = "https://example.com:0" }},
		{"overflow port", func(options *Options) { options.PublicURL = "https://example.com:65536" }},
		{"trailing dot", func(options *Options) { options.PublicURL = "https://example.com." }},
		{"short pepper", func(options *Options) { options.Pepper = []byte("short") }},
		{"challenge ttl", func(options *Options) { options.ChallengeTTL = time.Nanosecond }},
		{"session ttl", func(options *Options) { options.SessionTTL = time.Nanosecond }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := base
			test.edit(&options)
			if _, err := NewService(repository, options); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}

	base.Random = errorReader{}
	service, err = NewService(repository, base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateChallenge(t.Context(), "0x0000000000000000000000000000000000000001"); err == nil {
		t.Fatal("expected random source error")
	}

	precisionOptions := base
	precisionOptions.ChallengeTTL = 5*time.Minute + time.Nanosecond
	precisionOptions.SessionTTL = time.Hour + time.Nanosecond
	precisionOptions.Random = &incrementingReader{}
	service, err = NewService(repository, precisionOptions)
	if err != nil {
		t.Fatal(err)
	}
	challenge, err := service.CreateChallenge(
		t.Context(), "0x0000000000000000000000000000000000000001",
	)
	if err != nil {
		t.Fatal(err)
	}
	if challenge.ExpiresAt.Sub(challenge.IssuedAt) != 5*time.Minute ||
		service.sessionTTL != time.Hour {
		t.Fatalf("normalized TTLs = %s, %s", challenge.ExpiresAt.Sub(challenge.IssuedAt), service.sessionTTL)
	}
}

func TestOpaqueValuesAreCanonicalAndDomainSeparated(t *testing.T) {
	t.Parallel()
	value := [opaqueValueBytes]byte{1, 2, 3}
	encoded := base64.RawURLEncoding.EncodeToString(value[:])
	decoded, err := decodeOpaqueValue(encoded)
	if err != nil || decoded != value {
		t.Fatalf("decode = %x, %v", decoded, err)
	}
	for _, malformed := range []string{
		"", encoded + "=", strings.Repeat("!", opaqueValueChars),
	} {
		if _, err := decodeOpaqueValue(malformed); err == nil {
			t.Errorf("accepted malformed opaque value %q", malformed)
		}
	}
	pepper := bytes.Repeat([]byte{9}, 32)
	session := sessionDigest(pepper, value)
	csrf := deriveCSRFValue(pepper, value)
	digest := csrfDigest(pepper, csrf)
	if session == csrf || session == digest || csrf == digest {
		t.Fatal("domain-separated values collided")
	}
}

func newTestService(
	t *testing.T,
	repository AuthenticationRepository,
	now *time.Time,
	random io.Reader,
) *Service {
	t.Helper()
	service, err := NewService(repository, Options{
		ChainID: 11155111, PublicURL: "https://explorer.example",
		ChallengeTTL: 5 * time.Minute, SessionTTL: 7 * 24 * time.Hour,
		Pepper: bytes.Repeat([]byte{3}, 32),
		Now:    func() time.Time { return *now }, Random: random,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type memoryRepository struct {
	mu            sync.Mutex
	challenge     Challenge
	sessions      map[[opaqueValueBytes]byte]Session
	lastMaterial  SessionMaterial
	completeCalls int
	disabled      bool
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{sessions: make(map[[opaqueValueBytes]byte]Session)}
}

func (repository *memoryRepository) CreateChallenge(
	_ context.Context,
	challenge Challenge,
) (Challenge, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.challenge = challenge
	return challenge, nil
}

func (repository *memoryRepository) Challenge(_ context.Context, id string) (Challenge, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.challenge.ID != id {
		return Challenge{}, ErrChallengeInvalid
	}
	return repository.challenge, nil
}

func (repository *memoryRepository) CompleteLogin(
	_ context.Context,
	challenge Challenge,
	userID string,
	material SessionMaterial,
) (Session, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.completeCalls++
	if repository.challenge.ConsumedAt != nil ||
		!sameChallenge(repository.challenge, challenge, true) {
		return Session{}, ErrChallengeInvalid
	}
	consumedAt := material.CreatedAt
	repository.challenge.ConsumedAt = &consumedAt
	repository.lastMaterial = material
	if repository.disabled {
		return Session{}, ErrUserDisabled
	}
	lastLoginAt := material.CreatedAt
	user := User{
		ID: userID, ChainID: challenge.ChainID, Address: challenge.Address,
		Role: RoleUser, Status: StatusActive,
		CreatedAt: material.CreatedAt, UpdatedAt: material.CreatedAt,
		LastLoginAt: &lastLoginAt,
	}
	session := Session{
		ID: material.ID, User: user,
		CreatedAt: material.CreatedAt, ExpiresAt: material.ExpiresAt,
		LastUsedAt: material.CreatedAt, csrfDigest: material.CSRFDigest,
	}
	repository.sessions[material.TokenDigest] = session
	return session, nil
}

func (repository *memoryRepository) ActiveSession(
	_ context.Context,
	digest [opaqueValueBytes]byte,
	observedAt time.Time,
	_ time.Time,
) (Session, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	session, exists := repository.sessions[digest]
	if !exists || !observedAt.Before(session.ExpiresAt) {
		return Session{}, ErrSessionInvalid
	}
	return session, nil
}

func (repository *memoryRepository) RevokeSession(
	_ context.Context,
	digest [opaqueValueBytes]byte,
	_ time.Time,
) (bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if _, exists := repository.sessions[digest]; !exists {
		return false, nil
	}
	delete(repository.sessions, digest)
	return true, nil
}

type incrementingReader struct {
	mu   sync.Mutex
	next byte
}

func (reader *incrementingReader) Read(target []byte) (int, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	for index := range target {
		reader.next++
		target[index] = reader.next
	}
	return len(target), nil
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("random source unavailable")
}
