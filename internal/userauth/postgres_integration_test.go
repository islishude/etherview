//go:build integration

package userauth

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/signinwithethereum/siwe-go"
)

const userAuthTestDatabaseEnvironment = "ETHERVIEW_TEST_DATABASE_URL"

func TestPostgresConcurrentChallengeConsumptionAndImmediateRevocation(t *testing.T) {
	db := newUserAuthPostgres(t)
	repository, err := NewPostgresRepository(db, 11155111)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repository, Options{
		ChainID: 11155111, PublicURL: "https://explorer.example",
		ChallengeTTL: 5 * time.Minute, SessionTTL: 7 * 24 * time.Hour,
		Pepper: bytes.Repeat([]byte{4}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	challenge, err := service.CreateChallenge(
		t.Context(), ethcrypto.PubkeyToAddress(privateKey.PublicKey).Hex(),
	)
	if err != nil {
		t.Fatal(err)
	}
	signature := signUserAuthChallenge(t, challenge, privateKey)

	start := make(chan struct{})
	results := make(chan struct {
		login LoginResult
		err   error
	}, 2)
	var attempts sync.WaitGroup
	for range 2 {
		attempts.Add(1)
		go func() {
			defer attempts.Done()
			<-start
			login, verifyErr := service.VerifyChallenge(
				context.Background(), challenge.ID, signature,
			)
			results <- struct {
				login LoginResult
				err   error
			}{login: login, err: verifyErr}
		}()
	}
	close(start)
	attempts.Wait()
	close(results)

	var login LoginResult
	successes, invalid := 0, 0
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			login = result.login
		case errors.Is(result.err, ErrChallengeInvalid):
			invalid++
		default:
			t.Fatalf("concurrent verification error = %v", result.err)
		}
	}
	if successes != 1 || invalid != 1 {
		t.Fatalf("concurrent results: successes=%d invalid=%d", successes, invalid)
	}
	assertUserAuthCount(t, db, "users", 1)
	assertUserAuthCount(t, db, "user_sessions", 1)
	var consumed bool
	if err := db.QueryRowContext(t.Context(),
		`SELECT consumed_at IS NOT NULL FROM auth_challenges WHERE id = $1::uuid`,
		challenge.ID,
	).Scan(&consumed); err != nil || !consumed {
		t.Fatalf("challenge consumed=%t error=%v", consumed, err)
	}

	authentication, err := service.Authenticate(t.Context(), login.Credentials.Token)
	if err != nil {
		t.Fatal(err)
	}
	replicaRepository, err := NewPostgresRepository(db, 11155111)
	if err != nil {
		t.Fatal(err)
	}
	replicaService, err := NewService(replicaRepository, Options{
		ChainID: 11155111, PublicURL: "https://explorer.example",
		ChallengeTTL: 5 * time.Minute, SessionTTL: 7 * 24 * time.Hour,
		Pepper: bytes.Repeat([]byte{4}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replicaService.Authenticate(
		t.Context(), login.Credentials.Token,
	); err != nil {
		t.Fatalf("second API replica did not observe the writer session: %v", err)
	}
	admin := RoleAdmin
	roleUpdate, err := repository.UpdateUser(
		t.Context(), authentication.Session.User.ID,
		AdminUserUpdate{Role: &admin}, time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if roleUpdate.User.Role != RoleAdmin || roleUpdate.RevokedSessions != 0 {
		t.Fatalf("role update result = %+v", roleUpdate)
	}
	replicaAuthentication, err := replicaService.Authenticate(
		t.Context(), login.Credentials.Token,
	)
	if err != nil || replicaAuthentication.Session.User.Role != RoleAdmin {
		t.Fatalf(
			"second API replica role=%q error=%v",
			replicaAuthentication.Session.User.Role, err,
		)
	}
	disabled := StatusDisabled
	update, err := replicaRepository.UpdateUser(t.Context(), authentication.Session.User.ID, AdminUserUpdate{
		Status: &disabled,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if update.User.Status != StatusDisabled || update.RevokedSessions != 1 {
		t.Fatalf("disable result = %+v", update)
	}
	if _, err := service.Authenticate(t.Context(), login.Credentials.Token); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("authentication after disable error = %v", err)
	}
	if _, err := replicaService.Authenticate(t.Context(), login.Credentials.Token); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("second replica authentication after disable error = %v", err)
	}
}

func TestPostgresConcurrentFirstLoginsShareOneUser(t *testing.T) {
	db := newUserAuthPostgres(t)
	repository, err := NewPostgresRepository(db, 11155111)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repository, Options{
		ChainID: 11155111, PublicURL: "https://explorer.example",
		ChallengeTTL: 5 * time.Minute, SessionTTL: time.Hour,
		Pepper: bytes.Repeat([]byte{5}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	address := ethcrypto.PubkeyToAddress(privateKey.PublicKey).Hex()
	challenges := make([]Challenge, 2)
	signatures := make([]string, 2)
	for index := range challenges {
		challenges[index], err = service.CreateChallenge(t.Context(), address)
		if err != nil {
			t.Fatal(err)
		}
		signatures[index] = signUserAuthChallenge(t, challenges[index], privateKey)
	}

	start := make(chan struct{})
	errorsByLogin := make(chan error, 2)
	var logins sync.WaitGroup
	for index := range challenges {
		logins.Add(1)
		go func(index int) {
			defer logins.Done()
			<-start
			_, verifyErr := service.VerifyChallenge(
				context.Background(), challenges[index].ID, signatures[index],
			)
			errorsByLogin <- verifyErr
		}(index)
	}
	close(start)
	logins.Wait()
	close(errorsByLogin)
	for err := range errorsByLogin {
		if err != nil {
			t.Fatalf("concurrent first login: %v", err)
		}
	}
	assertUserAuthCount(t, db, "users", 1)
	assertUserAuthCount(t, db, "user_sessions", 2)
}

func TestPostgresCleanupIsChainScoped(t *testing.T) {
	db := newUserAuthPostgres(t)
	if _, err := db.ExecContext(
		t.Context(),
		`INSERT INTO chains (chain_id) VALUES (84532)`,
	); err != nil {
		t.Fatal(err)
	}
	const firstChain uint64 = 11155111
	const secondChain uint64 = 84532
	firstRepository, err := NewPostgresRepository(db, firstChain)
	if err != nil {
		t.Fatal(err)
	}
	secondRepository, err := NewPostgresRepository(db, secondChain)
	if err != nil {
		t.Fatal(err)
	}
	newService := func(
		repository *PostgresRepository,
		chainID uint64,
		pepper byte,
	) *Service {
		service, serviceErr := NewService(repository, Options{
			ChainID: chainID, PublicURL: "https://explorer.example",
			ChallengeTTL: 5 * time.Minute, SessionTTL: time.Hour,
			Pepper: bytes.Repeat([]byte{pepper}, 32),
		})
		if serviceErr != nil {
			t.Fatal(serviceErr)
		}
		return service
	}
	firstService := newService(firstRepository, firstChain, 6)
	secondService := newService(secondRepository, secondChain, 7)
	login := func(service *Service) (Challenge, LoginResult) {
		privateKey, keyErr := ethcrypto.GenerateKey()
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		challenge, challengeErr := service.CreateChallenge(
			t.Context(),
			ethcrypto.PubkeyToAddress(privateKey.PublicKey).Hex(),
		)
		if challengeErr != nil {
			t.Fatal(challengeErr)
		}
		result, loginErr := service.VerifyChallenge(
			t.Context(),
			challenge.ID,
			signUserAuthChallenge(t, challenge, privateKey),
		)
		if loginErr != nil {
			t.Fatal(loginErr)
		}
		return challenge, result
	}
	firstChallenge, _ := login(firstService)
	secondChallenge, secondLogin := login(secondService)
	cutoff := time.Now().UTC().Add(2 * time.Hour)
	cleaned, err := firstRepository.Cleanup(t.Context(), cutoff, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if cleaned.Challenges != 1 || cleaned.Sessions != 1 {
		t.Fatalf("first-chain cleanup=%+v", cleaned)
	}
	if _, err := firstRepository.Challenge(
		t.Context(), firstChallenge.ID,
	); !errors.Is(err, ErrChallengeInvalid) {
		t.Fatalf("first-chain challenge survived cleanup: %v", err)
	}
	if _, err := secondRepository.Challenge(
		t.Context(), secondChallenge.ID,
	); err != nil {
		t.Fatalf("second-chain challenge was removed: %v", err)
	}
	if _, err := secondService.Authenticate(
		t.Context(), secondLogin.Credentials.Token,
	); err != nil {
		t.Fatalf("second-chain session was removed: %v", err)
	}
	cleaned, err = secondRepository.Cleanup(t.Context(), cutoff, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if cleaned.Challenges != 1 || cleaned.Sessions != 1 {
		t.Fatalf("second-chain cleanup=%+v", cleaned)
	}
}

func signUserAuthChallenge(
	t *testing.T,
	challenge Challenge,
	privateKey *ecdsa.PrivateKey,
) string {
	t.Helper()
	message, err := siwe.ParseMessage(challenge.Message)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := ethcrypto.Sign(message.EIP191Hash().Bytes(), privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return hexutil.Encode(signature)
}

func newUserAuthPostgres(t *testing.T) *sql.DB {
	t.Helper()
	rawURL := strings.TrimSpace(os.Getenv(userAuthTestDatabaseEnvironment))
	if rawURL == "" {
		t.Skipf("%s is not set", userAuthTestDatabaseEnvironment)
	}
	adminConfig, err := pgx.ParseConfig(rawURL)
	if err != nil {
		t.Fatalf("parse %s: %v", userAuthTestDatabaseEnvironment, err)
	}
	adminConfig.RuntimeParams = cloneUserAuthRuntimeParams(adminConfig.RuntimeParams)
	adminConfig.RuntimeParams["application_name"] = "etherview-userauth-admin"
	adminDB := stdlib.OpenDB(*adminConfig)
	adminDB.SetMaxOpenConns(2)
	adminDB.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := adminDB.PingContext(ctx); err != nil {
		_ = adminDB.Close()
		t.Fatalf("connect to %s: %v", userAuthTestDatabaseEnvironment, err)
	}
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	schema := "etherview_userauth_it_" + hex.EncodeToString(suffix)
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA `+quoteUserAuthIdentifier(schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	testConfig := adminConfig.Copy()
	testConfig.RuntimeParams = cloneUserAuthRuntimeParams(testConfig.RuntimeParams)
	testConfig.RuntimeParams["application_name"] = "etherview-userauth-test"
	testConfig.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*testConfig)
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("connect isolated schema: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_, _ = adminDB.ExecContext(
			cleanupCtx, `DROP SCHEMA `+quoteUserAuthIdentifier(schema)+` CASCADE`,
		)
		_ = adminDB.Close()
	})
	if err := store.RunMigrations(ctx, db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO chains (chain_id) VALUES (11155111)`,
	); err != nil {
		t.Fatalf("insert chain: %v", err)
	}
	return db
}

func cloneUserAuthRuntimeParams(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source)+2)
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func quoteUserAuthIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func assertUserAuthCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var count int
	query := fmt.Sprintf(`SELECT count(*) FROM %s`, quoteUserAuthIdentifier(table))
	if err := db.QueryRowContext(t.Context(), query).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if count != want {
		t.Fatalf("%s count=%d want=%d", table, count, want)
	}
}
