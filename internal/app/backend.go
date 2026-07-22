// Package app wires the single CLI to the same component implementations used
// by monolith and split-role deployments.
package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/islishude/etherview/internal/adminstore"
	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/maintenance"
	"github.com/islishude/etherview/internal/store"
)

type Backend struct {
	Stdout  io.Writer
	Stderr  io.Writer
	Logger  *slog.Logger
	Version string
}

func (b *Backend) output() io.Writer {
	if b.Stdout == nil {
		return io.Discard
	}
	return b.Stdout
}

func (b *Backend) logger() *slog.Logger {
	if b.Logger == nil {
		return slog.Default()
	}
	return b.Logger
}

func (b *Backend) Migrate(ctx context.Context, cfg config.Config, action string) error {
	db, err := openDatabase(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer db.Close()
	switch action {
	case "up":
		if err := store.RunMigrations(ctx, db); err != nil {
			return err
		}
		status, err := store.ReadSchemaStatus(ctx, db)
		if err != nil {
			return err
		}
		return writeIndentedJSON(b.output(), map[string]any{"status": "compatible", "applied": status.Applied})
	case "status":
		status, err := store.ReadSchemaStatus(ctx, db)
		if err != nil {
			return err
		}
		state := "compatible"
		if len(status.Pending) != 0 {
			state = "incompatible"
		}
		if err := writeIndentedJSON(b.output(), map[string]any{
			"status": state, "applied": status.Applied, "pending": status.Pending,
		}); err != nil {
			return err
		}
		if state != "compatible" {
			return fmt.Errorf("%w: pending migrations %s", store.ErrSchemaIncompatible, strings.Join(status.Pending, ", "))
		}
		return nil
	default:
		return fmt.Errorf("unsupported migration action %q", action)
	}
}

func (b *Backend) Admin(ctx context.Context, cfg config.Config, resource, action string, args []string) error {
	db, err := openDatabase(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := store.CheckSchema(ctx, db); err != nil {
		return err
	}
	switch resource {
	case "api-key":
		return b.adminAPIKey(ctx, db, cfg, action, args)
	case "label":
		return b.adminLabel(ctx, db, cfg, action, args)
	case "repair":
		return b.adminRepair(ctx, db, cfg, action, args)
	default:
		return fmt.Errorf("unsupported admin resource %q", resource)
	}
}

func (b *Backend) adminRepair(ctx context.Context, db *sql.DB, cfg config.Config, action string, args []string) error {
	if action != "list" {
		return fmt.Errorf("unsupported repair admin action %q", action)
	}
	fs := flag.NewFlagSet("admin repair list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 100, "maximum newest requests")
	format := fs.String("format", "json", "output format: json or table")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("repair list: unexpected arguments %s", strings.Join(fs.Args(), " "))
	}
	repository, err := adminstore.New(db, cfg.Chain.ID)
	if err != nil {
		return err
	}
	requests, err := repository.RepairRequests(ctx, *limit)
	if err != nil {
		return err
	}
	return writeRepairRequests(b.output(), *format, requests)
}

func writeRepairRequests(writer io.Writer, format string, requests []adminstore.RepairRequest) error {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json":
		return writeIndentedJSON(writer, requests)
	case "table":
		table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(table, "ID\tOPERATION\tSTAGE\tFROM\tTO\tSTATUS\tFAILURE_PRESENT\tREQUESTED_AT"); err != nil {
			return err
		}
		for _, request := range requests {
			if _, err := fmt.Fprintf(table, "%d\t%s\t%s\t%d\t%d\t%s\t%t\t%s\n",
				request.ID, request.Operation, request.Stage, request.FromBlock, request.ToBlock,
				request.Status, request.FailurePresent, request.RequestedAt.UTC().Format(time.RFC3339),
			); err != nil {
				return err
			}
		}
		return table.Flush()
	default:
		return errors.New("repair list format must be json or table")
	}
}

func (b *Backend) adminAPIKey(ctx context.Context, db *sql.DB, cfg config.Config, action string, args []string) error {
	if len(cfg.Security.APIKeyPepper) < 32 {
		return errors.New("security.api_key_pepper or ETHERVIEW_API_KEY_PEPPER_FILE is required for API key administration")
	}
	repository, err := auth.NewPostgresRepository(db)
	if err != nil {
		return err
	}
	manager := auth.Manager{Repository: repository, Pepper: []byte(cfg.Security.APIKeyPepper)}
	switch action {
	case "create":
		fs := flag.NewFlagSet("admin api-key create", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		name := fs.String("name", "", "operator-visible key name")
		rate := fs.Int("rate", 20, "requests per second")
		burst := fs.Int("burst", 40, "burst requests")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return fmt.Errorf("api-key create: unexpected arguments %s", strings.Join(fs.Args(), " "))
		}
		issued, err := manager.Create(ctx, *name, *rate, *burst)
		if err != nil {
			return err
		}
		return writeIndentedJSON(b.output(), map[string]any{
			"token": issued.Token, "prefix": issued.Record.Prefix, "name": issued.Record.Name,
			"rate": issued.Record.Rate, "burst": issued.Record.Burst,
			"created_at": issued.Record.CreatedAt,
			"warning":    "this token is shown once and cannot be recovered",
		})
	case "revoke":
		if len(args) != 1 {
			return errors.New("api-key revoke requires exactly one key prefix")
		}
		if err := repository.Revoke(ctx, strings.ToLower(args[0]), time.Now().UTC()); err != nil {
			return err
		}
		return writeIndentedJSON(b.output(), map[string]string{"status": "revoked", "prefix": strings.ToLower(args[0])})
	case "rotate":
		if len(args) != 1 {
			return errors.New("api-key rotate requires exactly one active key prefix")
		}
		oldPrefix := strings.ToLower(strings.TrimSpace(args[0]))
		issued, err := manager.Rotate(ctx, oldPrefix)
		if err != nil {
			return err
		}
		return writeIndentedJSON(b.output(), map[string]any{
			"token": issued.Token, "prefix": issued.Record.Prefix,
			"rotated_from": oldPrefix, "name": issued.Record.Name,
			"rate": issued.Record.Rate, "burst": issued.Record.Burst,
			"created_at": issued.Record.CreatedAt,
			"warning":    "this replacement token is shown once and cannot be recovered",
		})
	case "list":
		if len(args) != 0 {
			return errors.New("api-key list accepts no arguments")
		}
		items, err := repository.List(ctx)
		if err != nil {
			return err
		}
		return writeIndentedJSON(b.output(), items)
	default:
		return fmt.Errorf("unsupported api-key action %q", action)
	}
}

func (b *Backend) adminLabel(ctx context.Context, db *sql.DB, cfg config.Config, action string, args []string) error {
	repository, err := adminstore.New(db, cfg.Chain.ID)
	if err != nil {
		return err
	}
	switch action {
	case "set":
		if len(args) != 3 {
			return errors.New("label set requires kind, key, and label")
		}
		stored, err := repository.SetLabel(ctx, args[0], args[1], args[2])
		if err != nil {
			return err
		}
		return writeIndentedJSON(b.output(), map[string]string{"status": "set", "kind": stored.Kind, "key": stored.Key})
	case "delete":
		if len(args) != 2 {
			return errors.New("label delete requires kind and key")
		}
		stored, err := repository.DeleteLabel(ctx, args[0], args[1])
		if err != nil {
			return err
		}
		return writeIndentedJSON(b.output(), map[string]string{"status": "deleted", "kind": stored.Kind, "key": stored.Key})
	case "list":
		if len(args) != 0 {
			return errors.New("label list accepts no arguments")
		}
		items, err := repository.Labels(ctx)
		if err != nil {
			return err
		}
		return writeIndentedJSON(b.output(), items)
	default:
		return fmt.Errorf("unsupported label action %q", action)
	}
}

func (b *Backend) Repair(ctx context.Context, cfg config.Config, operation string, args []string) error {
	fs := flag.NewFlagSet(operation, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	from := fs.Uint64("from", 0, "first block")
	to := fs.Uint64("to", 0, "last block")
	defaultStage := "core"
	if operation == "reindex" {
		defaultStage = ""
	}
	stage := fs.String("stage", defaultStage, "stage to replay")
	reason := fs.String("reason", "", "operator audit reason")
	allowFinalized := fs.Bool("allow-finalized", false, "explicit finalized override")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("%s: unexpected arguments %s", operation, strings.Join(fs.Args(), " "))
	}
	seen := make(map[string]bool)
	fs.Visit(func(item *flag.Flag) { seen[item.Name] = true })
	if !seen["from"] || !seen["to"] || strings.TrimSpace(*reason) == "" {
		return fmt.Errorf("%s requires --from, --to, and --reason", operation)
	}
	normalizedStage := strings.ToLower(strings.TrimSpace(*stage))
	if operation == "reindex" && !seen["stage"] {
		return errors.New("reindex requires --stage token, stats, or trace")
	}
	if err := validateMaintenanceOperationStage(operation, normalizedStage); err != nil {
		return err
	}
	db, err := openDatabase(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := store.CheckSchema(ctx, db); err != nil {
		return err
	}
	if !*allowFinalized {
		finalized, exists, err := finalizedHeight(ctx, db, cfg.Chain.ID)
		if err != nil {
			return err
		}
		if exists && *from <= finalized {
			return fmt.Errorf("requested range begins at %d at/below finalized height %d; pass --allow-finalized with an audit reason to override", *from, finalized)
		}
	}
	repository, err := adminstore.New(db, cfg.Chain.ID)
	if err != nil {
		return err
	}
	request, err := repository.EnqueueRepair(ctx, adminstore.RepairRequest{
		Operation: operation, Stage: normalizedStage, FromBlock: *from, ToBlock: *to,
		AllowFinalized: *allowFinalized, Reason: *reason,
	})
	if err != nil {
		return err
	}
	return writeIndentedJSON(b.output(), request)
}

func validateMaintenanceOperationStage(operation, stage string) error {
	return maintenance.ValidateOperationStage(maintenance.Operation(operation), stage)
}

func finalizedHeight(ctx context.Context, db *sql.DB, chainID uint64) (uint64, bool, error) {
	var raw sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT finalized_number::text
		FROM chain_finality
		WHERE chain_id = $1::numeric`, strconv.FormatUint(chainID, 10)).Scan(&raw)
	if err == sql.ErrNoRows || (err == nil && !raw.Valid) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read finalized height: %w", err)
	}
	value, err := strconv.ParseUint(raw.String, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("decode finalized height: %w", err)
	}
	return value, true, nil
}

func writeIndentedJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
