//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	cliUserAuthAddress    = "0x52908400098527886E0F7030069857D2E4169EE7"
	cliUserAuthAddressHex = "52908400098527886E0F7030069857D2E4169EE7"
	cliUserAuthID         = "10000000-0000-4000-8000-000000000001"
	cliUserAuthOtherID    = "10000000-0000-4000-8000-000000000002"
)

func TestCLIAdminUserMutationsAreWriterBackedAndChainScoped(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	if _, err := db.ExecContext(ctx, `INSERT INTO chains (chain_id) VALUES (1), (2)`); err != nil {
		t.Fatalf("insert user-auth chains: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (
			id, chain_id, address, role, status, created_at, updated_at
		) VALUES
			($1::uuid, 1, decode($3, 'hex'), 'user', 'active',
			 '2026-07-01T00:00:00Z', '2026-07-01T00:00:00Z'),
			($2::uuid, 2, decode($3, 'hex'), 'user', 'active',
			 '2026-07-01T00:00:00Z', '2026-07-01T00:00:00Z')`,
		cliUserAuthID, cliUserAuthOtherID, cliUserAuthAddressHex,
	); err != nil {
		t.Fatalf("insert user-auth users: %v", err)
	}
	insertCLIUserSessions(t, ctx, db, cliUserAuthID, "20", "11", "12")
	insertCLIUserSessions(t, ctx, db, cliUserAuthOtherID, "30", "21")

	var schema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatalf("read integration schema: %v", err)
	}
	databaseURL := isolatedDatabaseURL(t, schema)
	configPath := filepath.Join(t.TempDir(), "etherview-user-admin.yaml")
	configBody := fmt.Sprintf(
		"chain:\n  id: 1\ndatabase:\n  url: %s\n",
		strconv.Quote(databaseURL),
	)
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatalf("write user admin config: %v", err)
	}

	runner := newCLIRunner()
	lowerAddress := strings.ToLower(cliUserAuthAddress)
	code, stdout, stderr := runner.run(
		ctx, "admin", "user", "set-role",
		"--address", lowerAddress, "--role", "admin", "--config", configPath,
	)
	output := decodeCLIAdminUserOutput(t, code, stdout, stderr)
	if output.Status != "updated" || output.Action != "set-role" ||
		output.User.ID != cliUserAuthID || output.User.ChainID != "1" ||
		output.User.Address != cliUserAuthAddress || output.User.Role != "admin" ||
		output.User.Status != "active" || output.RevokedSessions != "0" ||
		output.User.UpdatedAt == "" {
		t.Fatalf("set-role output = %+v", output)
	}
	assertCLIUserState(t, ctx, db, cliUserAuthID, "admin", "active")
	if strings.Contains(stdout, databaseURL) {
		t.Fatal("set-role output contains the database URL")
	}

	code, stdout, stderr = runner.run(
		ctx, "admin", "user", "revoke-sessions",
		"--config="+configPath, "--address", cliUserAuthAddress,
	)
	output = decodeCLIAdminUserOutput(t, code, stdout, stderr)
	if output.Status != "sessions-revoked" ||
		output.Action != "revoke-sessions" || output.RevokedSessions != "2" {
		t.Fatalf("revoke-sessions output = %+v", output)
	}
	assertCLIActiveSessions(t, ctx, db, cliUserAuthID, 0)
	assertCLIActiveSessions(t, ctx, db, cliUserAuthOtherID, 1)
	code, stdout, stderr = runner.run(
		ctx, "admin", "user", "revoke-sessions",
		"--address", cliUserAuthAddress, "--config", configPath,
	)
	output = decodeCLIAdminUserOutput(t, code, stdout, stderr)
	if output.RevokedSessions != "0" {
		t.Fatalf("idempotent revoke-sessions output = %+v", output)
	}

	insertCLIUserSessions(t, ctx, db, cliUserAuthID, "40", "31", "32")
	code, stdout, stderr = runner.run(
		ctx, "admin", "user", "set-status",
		"--address="+cliUserAuthAddress, "--status", "disabled",
		"--config", configPath,
	)
	output = decodeCLIAdminUserOutput(t, code, stdout, stderr)
	if output.Status != "updated" || output.Action != "set-status" ||
		output.User.Status != "disabled" || output.User.Role != "admin" ||
		output.RevokedSessions != "2" {
		t.Fatalf("disable output = %+v", output)
	}
	assertCLIUserState(t, ctx, db, cliUserAuthID, "admin", "disabled")
	assertCLIActiveSessions(t, ctx, db, cliUserAuthID, 0)
	assertCLIActiveSessions(t, ctx, db, cliUserAuthOtherID, 1)

	code, stdout, stderr = runner.run(
		ctx, "admin", "user", "set-role",
		"--address", cliUserAuthAddress, "--role", "user",
		"--config", configPath,
	)
	output = decodeCLIAdminUserOutput(t, code, stdout, stderr)
	if output.User.Role != "user" || output.User.Status != "disabled" ||
		output.RevokedSessions != "0" {
		t.Fatalf("role downgrade output = %+v", output)
	}

	code, stdout, stderr = runner.run(
		ctx, "admin", "user", "set-status",
		"--address", cliUserAuthAddress, "--status", "active",
		"--config", configPath,
	)
	output = decodeCLIAdminUserOutput(t, code, stdout, stderr)
	if output.User.Role != "user" || output.User.Status != "active" ||
		output.RevokedSessions != "0" {
		t.Fatalf("enable output = %+v", output)
	}
	assertCLIUserState(t, ctx, db, cliUserAuthID, "user", "active")
	assertCLIUserState(t, ctx, db, cliUserAuthOtherID, "user", "active")
	assertCLIActiveSessions(t, ctx, db, cliUserAuthID, 0)

	code, stdout, stderr = runner.run(
		ctx, "admin", "user", "set-role",
		"--address", cliUserAuthAddress, "--role", "owner",
		"--config", configPath,
	)
	if code != 1 || stdout != "" ||
		stderr != "etherview: user set-role --role must be admin or user\n" {
		t.Fatalf("invalid role code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runner.run(
		ctx, "admin", "user", "revoke-sessions",
		"--address", "0x0000000000000000000000000000000000000001",
		"--config", configPath,
	)
	if code != 1 || stdout != "" || stderr != "etherview: user not found\n" {
		t.Fatalf("missing user code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

type cliAdminUserOutput struct {
	Status string `json:"status"`
	Action string `json:"action"`
	User   struct {
		ID        string `json:"id"`
		ChainID   string `json:"chain_id"`
		Address   string `json:"address"`
		Role      string `json:"role"`
		Status    string `json:"status"`
		UpdatedAt string `json:"updated_at"`
	} `json:"user"`
	RevokedSessions string `json:"revoked_sessions"`
}

func decodeCLIAdminUserOutput(
	t *testing.T,
	code int,
	stdout, stderr string,
) cliAdminUserOutput {
	t.Helper()
	var output cliAdminUserOutput
	if err := json.Unmarshal([]byte(stdout), &output); code != 0 || err != nil || stderr != "" {
		t.Fatalf(
			"admin user output code=%d output=%+v stdout=%q stderr=%q error=%v",
			code, output, stdout, stderr, err,
		)
	}
	return output
}

func insertCLIUserSessions(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	userID, idPrefix string,
	digestPrefixes ...string,
) {
	t.Helper()
	for index, digestPrefix := range digestPrefixes {
		sessionID := fmt.Sprintf(
			"%s000000-0000-4000-8000-%012d", idPrefix, index+1,
		)
		if _, err := db.ExecContext(ctx, `
			INSERT INTO user_sessions (
				id, user_id, token_digest, csrf_digest,
				created_at, expires_at, last_used_at
			) VALUES (
				$1::uuid, $2::uuid, decode(repeat($3, 32), 'hex'),
				decode(repeat($4, 32), 'hex'), now(), now() + interval '1 day', now()
			)`,
			sessionID, userID, digestPrefix, digestPrefix,
		); err != nil {
			t.Fatalf("insert CLI user session %d: %v", index, err)
		}
	}
}

func assertCLIUserState(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	userID, wantRole, wantStatus string,
) {
	t.Helper()
	var role, status string
	if err := db.QueryRowContext(
		ctx, `SELECT role, status FROM users WHERE id = $1::uuid`, userID,
	).Scan(&role, &status); err != nil {
		t.Fatalf("read CLI user state: %v", err)
	}
	if role != wantRole || status != wantStatus {
		t.Fatalf(
			"user %s role/status = %s/%s, want %s/%s",
			userID, role, status, wantRole, wantStatus,
		)
	}
}

func assertCLIActiveSessions(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	userID string,
	want int,
) {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*)
		FROM user_sessions
		WHERE user_id = $1::uuid AND revoked_at IS NULL`,
		userID,
	).Scan(&count); err != nil {
		t.Fatalf("count active CLI user sessions: %v", err)
	}
	if count != want {
		t.Fatalf("active CLI user sessions = %d, want %d", count, want)
	}
}
