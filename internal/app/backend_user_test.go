package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/userauth"
)

func TestParseAdminUserCommand(t *testing.T) {
	t.Parallel()
	const address = "0x52908400098527886E0F7030069857D2E4169EE7"
	tests := []struct {
		name       string
		action     string
		args       []string
		wantRole   *userauth.Role
		wantStatus *userauth.Status
	}{
		{
			name: "set role", action: "set-role",
			args:     []string{"--address", " " + address + " ", "--role", " ADMIN "},
			wantRole: rolePointer(userauth.RoleAdmin),
		},
		{
			name: "set status", action: "set-status",
			args:       []string{"--status=DISABLED", "--address=" + address},
			wantStatus: statusPointer(userauth.StatusDisabled),
		},
		{
			name: "revoke sessions", action: "revoke-sessions",
			args: []string{"--address", address},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command, err := parseAdminUserCommand(test.action, test.args)
			if err != nil {
				t.Fatal(err)
			}
			if command.address != address ||
				!equalOptionalRole(command.role, test.wantRole) ||
				!equalOptionalStatus(command.status, test.wantStatus) {
				t.Fatalf("command = %+v", command)
			}
		})
	}
}

func TestParseAdminUserCommandRejectsMalformedInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		action string
		args   []string
		want   string
	}{
		{"unknown action", "delete", nil, "action must be"},
		{"missing address", "set-role", []string{"--role", "admin"}, "requires --address"},
		{
			"malformed address", "set-role",
			[]string{"--address", "alice.eth", "--role", "admin"},
			"20-byte hexadecimal address",
		},
		{
			"missing role", "set-role",
			[]string{"--address", "0x0000000000000000000000000000000000000001"},
			"--role must be admin or user",
		},
		{
			"invalid role", "set-role",
			[]string{
				"--address", "0x0000000000000000000000000000000000000001",
				"--role", "owner",
			},
			"--role must be admin or user",
		},
		{
			"invalid status", "set-status",
			[]string{
				"--address", "0x0000000000000000000000000000000000000001",
				"--status", "blocked",
			},
			"--status must be active or disabled",
		},
		{
			"unexpected position", "revoke-sessions",
			[]string{
				"--address", "0x0000000000000000000000000000000000000001",
				"extra",
			},
			"does not accept positional arguments",
		},
		{
			"wrong action flag", "revoke-sessions",
			[]string{
				"--address", "0x0000000000000000000000000000000000000001",
				"--role", "admin",
			},
			"flag provided but not defined",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseAdminUserCommand(test.action, test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestAdminUserCommandOutputIsStableAndBounded(t *testing.T) {
	t.Parallel()
	user := userauth.User{
		ID:      "123e4567-e89b-42d3-a456-426614174000",
		ChainID: 11155111,
		Address: "0x52908400098527886E0F7030069857D2E4169EE7",
		Role:    userauth.RoleAdmin,
		Status:  userauth.StatusDisabled,
		UpdatedAt: time.Date(
			2026, 7, 26, 10, 11, 12, 123456000, time.UTC,
		),
	}
	var output bytes.Buffer
	if err := writeIndentedJSON(
		&output, newAdminUserCommandOutput("set-status", user, 12),
	); err != nil {
		t.Fatal(err)
	}
	const want = `{
  "status": "updated",
  "action": "set-status",
  "user": {
    "id": "123e4567-e89b-42d3-a456-426614174000",
    "chain_id": "11155111",
    "address": "0x52908400098527886E0F7030069857D2E4169EE7",
    "role": "admin",
    "status": "disabled",
    "updated_at": "2026-07-26T10:11:12.123456Z"
  },
  "revoked_sessions": "12"
}
`
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(output.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 4 {
		t.Fatalf("top-level output fields = %v", fields)
	}
}

func rolePointer(value userauth.Role) *userauth.Role {
	return &value
}

func statusPointer(value userauth.Status) *userauth.Status {
	return &value
}

func equalOptionalRole(left, right *userauth.Role) bool {
	return left == nil && right == nil ||
		left != nil && right != nil && *left == *right
}

func equalOptionalStatus(left, right *userauth.Status) bool {
	return left == nil && right == nil ||
		left != nil && right != nil && *left == *right
}
