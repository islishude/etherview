package app

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/userauth"
)

type adminUserCommand struct {
	address string
	role    *userauth.Role
	status  *userauth.Status
}

type adminUserCommandOutput struct {
	Status          string           `json:"status"`
	Action          string           `json:"action"`
	User            adminUserSummary `json:"user"`
	RevokedSessions string           `json:"revoked_sessions"`
}

type adminUserSummary struct {
	ID        string          `json:"id"`
	ChainID   string          `json:"chain_id"`
	Address   string          `json:"address"`
	Role      userauth.Role   `json:"role"`
	Status    userauth.Status `json:"status"`
	UpdatedAt string          `json:"updated_at"`
}

func (b *Backend) adminUser(
	ctx context.Context,
	db *sql.DB,
	cfg config.Config,
	action string,
	args []string,
) error {
	command, err := parseAdminUserCommand(action, args)
	if err != nil {
		return err
	}
	repository, err := userauth.NewPostgresRepository(db, cfg.Chain.ID)
	if err != nil {
		return err
	}
	user, err := repository.UserByAddress(ctx, command.address)
	if err != nil {
		return err
	}

	var revoked uint64
	operationAt := time.Now().UTC().Truncate(time.Microsecond)
	switch action {
	case "set-role":
		result, updateErr := repository.UpdateUser(ctx, user.ID, userauth.AdminUserUpdate{
			Role: command.role,
		}, operationAt)
		if updateErr != nil {
			return updateErr
		}
		user, revoked = result.User, result.RevokedSessions
	case "set-status":
		result, updateErr := repository.UpdateUser(ctx, user.ID, userauth.AdminUserUpdate{
			Status: command.status,
		}, operationAt)
		if updateErr != nil {
			return updateErr
		}
		user, revoked = result.User, result.RevokedSessions
	case "revoke-sessions":
		revoked, err = repository.RevokeAllSessions(
			ctx, user.ID, operationAt,
		)
		if err != nil {
			return err
		}
	}
	return writeIndentedJSON(
		b.output(), newAdminUserCommandOutput(action, user, revoked),
	)
}

func parseAdminUserCommand(action string, args []string) (adminUserCommand, error) {
	switch action {
	case "set-role":
		fs, address := newAdminUserFlagSet(action)
		role := fs.String("role", "", "new user role: admin or user")
		if err := parseAdminUserFlags(fs, args); err != nil {
			return adminUserCommand{}, err
		}
		value := userauth.Role(strings.ToLower(strings.TrimSpace(*role)))
		if !validAdminUserRole(value) {
			return adminUserCommand{}, errors.New("user set-role --role must be admin or user")
		}
		return adminUserCommand{address: *address, role: &value}, nil
	case "set-status":
		fs, address := newAdminUserFlagSet(action)
		status := fs.String("status", "", "new user status: active or disabled")
		if err := parseAdminUserFlags(fs, args); err != nil {
			return adminUserCommand{}, err
		}
		value := userauth.Status(strings.ToLower(strings.TrimSpace(*status)))
		if !validAdminUserStatus(value) {
			return adminUserCommand{}, errors.New(
				"user set-status --status must be active or disabled",
			)
		}
		return adminUserCommand{address: *address, status: &value}, nil
	case "revoke-sessions":
		fs, address := newAdminUserFlagSet(action)
		if err := parseAdminUserFlags(fs, args); err != nil {
			return adminUserCommand{}, err
		}
		return adminUserCommand{address: *address}, nil
	default:
		return adminUserCommand{}, errors.New(
			"user admin action must be set-role, set-status, or revoke-sessions",
		)
	}
}

func newAdminUserFlagSet(action string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("admin user "+action, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs, fs.String("address", "", "target user wallet address")
}

func parseAdminUserFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("user admin command does not accept positional arguments")
	}
	address := fs.Lookup("address").Value.String()
	address = strings.TrimSpace(address)
	if address == "" {
		return errors.New("user admin command requires --address")
	}
	if _, err := ethrpc.ParseAddress(address); err != nil {
		return errors.New("user admin --address must be a 20-byte hexadecimal address")
	}
	if err := fs.Set("address", address); err != nil {
		return errors.New("normalize user admin address")
	}
	return nil
}

func validAdminUserRole(role userauth.Role) bool {
	return role == userauth.RoleAdmin || role == userauth.RoleUser
}

func validAdminUserStatus(status userauth.Status) bool {
	return status == userauth.StatusActive || status == userauth.StatusDisabled
}

func newAdminUserCommandOutput(
	action string,
	user userauth.User,
	revoked uint64,
) adminUserCommandOutput {
	status := "updated"
	if action == "revoke-sessions" {
		status = "sessions-revoked"
	}
	return adminUserCommandOutput{
		Status: status, Action: action,
		User: adminUserSummary{
			ID: user.ID, ChainID: strconv.FormatUint(user.ChainID, 10),
			Address: user.Address, Role: user.Role, Status: user.Status,
			UpdatedAt: user.UpdatedAt.UTC().Format(time.RFC3339Nano),
		},
		RevokedSessions: strconv.FormatUint(revoked, 10),
	}
}
