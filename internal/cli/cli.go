// Package cli implements Etherview's single-binary command surface.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/islishude/etherview/internal/config"
)

const usage = `Usage:
  etherview serve [--config path] [--roles all|api,sync,...] [--log-level level] [--log-format json|text]
  etherview healthcheck [--url http://127.0.0.1:9090/health/ready] [--timeout duration]
  etherview doctor [--config path] [--log-level level] [--log-format json|text]
  etherview migrate <up|status> [--config path] [--log-level level] [--log-format json|text]
  etherview repair [--config path] [--log-level level] [--log-format json|text] [arguments]
  etherview reindex [--config path] [--log-level level] [--log-format json|text] [arguments]
  etherview admin api-key <create|rotate|revoke|list> [--config path] [--log-level level] [--log-format json|text] [arguments]
  etherview admin label <set|delete|list> [--config path] [--log-level level] [--log-format json|text] [arguments]
  etherview admin repair list [--config path] [--log-level level] [--log-format json|text] [--limit count] [--format json|table]
  etherview admin user set-role --address address --role admin|user [--config path] [--log-level level] [--log-format json|text]
  etherview admin user set-status --address address --status active|disabled [--config path] [--log-level level] [--log-format json|text]
  etherview admin user revoke-sessions --address address [--config path] [--log-level level] [--log-format json|text]
  etherview admin billing inspect --id uuid [--config path] [--log-level level] [--log-format json|text]
  etherview admin billing reconcile --id uuid --outcome settled --transaction-hash hash [--config path] [--log-level level] [--log-format json|text]
  etherview admin billing reconcile --id uuid --outcome failed [--config path] [--log-level level] [--log-format json|text]
  etherview admin derived-verification backfill --reason text [--address factory] [--config path] [--log-level level] [--log-format json|text]
  etherview version
`

// Backend connects command parsing to runtime implementations. Keeping this
// interface narrow makes every command testable without external services.
type Backend interface {
	Serve(context.Context, config.Config, []string) error
	Doctor(context.Context, config.Config, []string) error
	Migrate(context.Context, config.Config, string) error
	Repair(context.Context, config.Config, string, []string) error
	Admin(context.Context, config.Config, string, string, []string) error
}

type Program struct {
	Backend          Backend
	ConfigureLogging func(config.ObservabilityConfig) error
	Version          string
	Stdout           io.Writer
	Stderr           io.Writer
}

func (p Program) Run(ctx context.Context, args []string) int {
	stdout := p.Stdout
	stderr := p.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	if p.Backend == nil && args[0] != "version" && args[0] != "help" &&
		args[0] != "healthcheck" {
		_, _ = fmt.Fprintln(stderr, "etherview: runtime backend is not configured")
		return 1
	}

	var err error
	switch args[0] {
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	case "version":
		version := p.Version
		if version == "" {
			version = "dev"
		}
		_, _ = fmt.Fprintln(stdout, version)
		return 0
	case "healthcheck":
		err = p.runHealthcheck(ctx, args[1:])
	case "serve":
		err = p.runServe(ctx, args[1:])
	case "doctor":
		err = p.runDoctor(ctx, args[1:], stdout)
	case "migrate":
		err = p.runMigrate(ctx, args[1:])
	case "repair", "reindex":
		err = p.runRepair(ctx, args[0], args[1:])
	case "admin":
		err = p.runAdmin(ctx, args[1:])
	default:
		_, _ = fmt.Fprintf(stderr, "etherview: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "etherview: %v\n", err)
		return 1
	}
	return 0
}

func (p Program) runServe(ctx context.Context, args []string) error {
	path, args, logging, err := extractRuntimeFlags("serve", args)
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	rolesFlag := fs.String("roles", "", "comma-separated runtime roles")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("serve: unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	overrides := logging.configOverrides()
	if *rolesFlag != "" {
		overrides.Roles = strings.Split(*rolesFlag, ",")
	}
	cfg, err := config.LoadWithOverrides(path, overrides)
	if err != nil {
		return err
	}
	if err := p.configureLogging(cfg.Observability); err != nil {
		return err
	}
	roles := cfg.Runtime.Roles
	normalized, err := config.NormalizeRoles(roles)
	if err != nil {
		return err
	}
	if err := cfg.ValidateForRoles(normalized); err != nil {
		return err
	}
	return p.Backend.Serve(ctx, cfg, normalized)
}

func (p Program) runDoctor(ctx context.Context, args []string, stdout io.Writer) error {
	path, rest, logging, err := extractRuntimeFlags("doctor", args)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return errors.New("doctor does not accept positional arguments")
	}
	cfg, err := config.LoadWithOverrides(path, logging.configOverrides())
	if err != nil {
		return err
	}
	if err := p.configureLogging(cfg.Observability); err != nil {
		return err
	}
	roles, err := config.NormalizeRoles(cfg.Runtime.Roles)
	if err != nil {
		return err
	}
	roleErr := cfg.ValidateForRoles(roles)
	checkErr := roleErr
	if roleErr == nil {
		checkErr = p.Backend.Doctor(ctx, cfg, roles)
	}
	type endpoint struct {
		Name     string   `json:"name"`
		Purposes []string `json:"purposes"`
	}
	result := struct {
		Valid        bool       `json:"valid"`
		ChainID      string     `json:"chain_id"`
		GenesisHash  string     `json:"genesis_hash,omitempty"`
		StartBlock   string     `json:"start_block"`
		Roles        []string   `json:"roles"`
		DatabaseSet  bool       `json:"database_configured"`
		RPCEndpoints []endpoint `json:"rpc_endpoints"`
		Errors       []string   `json:"errors,omitempty"`
	}{
		Valid:       checkErr == nil,
		ChainID:     fmt.Sprint(cfg.Chain.ID),
		GenesisHash: cfg.Chain.GenesisHash,
		StartBlock:  fmt.Sprint(cfg.Chain.StartBlock),
		Roles:       roles,
		DatabaseSet: strings.TrimSpace(cfg.Database.URL) != "",
		Errors:      validationMessages(checkErr),
	}
	for _, item := range cfg.RPC.Endpoints {
		result.RPCEndpoints = append(result.RPCEndpoints, endpoint{Name: item.Name, Purposes: item.Purposes})
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return err
	}
	return checkErr
}

func validationMessages(err error) []string {
	if err == nil {
		return nil
	}
	parts := strings.Split(err.Error(), "\n")
	messages := make([]string, 0, len(parts))
	for _, part := range parts {
		if message := strings.TrimSpace(part); message != "" {
			messages = append(messages, message)
		}
	}
	return messages
}

func (p Program) runMigrate(ctx context.Context, args []string) error {
	if len(args) == 0 || (args[0] != "up" && args[0] != "status") {
		return errors.New("migrate requires up or status")
	}
	action := args[0]
	cfg, rest, err := loadConfigFlag("migrate", args[1:])
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("migrate: unexpected arguments: %s", strings.Join(rest, " "))
	}
	if err := p.configureLogging(cfg.Observability); err != nil {
		return err
	}
	if cfg.Database.URL == "" {
		return errors.New("database.url is required")
	}
	return p.Backend.Migrate(ctx, cfg, action)
}

func (p Program) runRepair(ctx context.Context, kind string, args []string) error {
	cfg, rest, err := loadConfigFlag(kind, args)
	if err != nil {
		return err
	}
	if err := p.configureLogging(cfg.Observability); err != nil {
		return err
	}
	if cfg.Database.URL == "" {
		return errors.New("database.url is required")
	}
	return p.Backend.Repair(ctx, cfg, kind, rest)
}

func (p Program) runAdmin(ctx context.Context, args []string) error {
	if len(args) < 2 ||
		(args[0] != "api-key" && args[0] != "label" &&
			args[0] != "repair" && args[0] != "user" &&
			args[0] != "billing" && args[0] != "derived-verification") {
		return errors.New(
			"admin requires api-key, label, repair, user, billing, or derived-verification and an action",
		)
	}
	resource, action := args[0], args[1]
	var (
		cfg  config.Config
		rest []string
		err  error
	)
	if resource == "billing" || resource == "user" {
		// User and billing administration need only the writer database.
		// Loading as a non-API role prevents these commands from opening
		// session, fingerprint, or facilitator-header Secret files.
		cfg, rest, err = loadConfigFlagForRoles(
			"admin", args[2:], []string{"maintenance"},
		)
	} else {
		cfg, rest, err = loadConfigFlag("admin", args[2:])
	}
	if err != nil {
		return err
	}
	if err := p.configureLogging(cfg.Observability); err != nil {
		return err
	}
	if cfg.Database.URL == "" {
		return errors.New("database.url is required")
	}
	return p.Backend.Admin(ctx, cfg, resource, action, rest)
}

func loadConfigFlag(
	name string,
	args []string,
) (config.Config, []string, error) {
	path, rest, logging, err := extractRuntimeFlags(name, args)
	if err != nil {
		return config.Config{}, nil, err
	}
	cfg, err := config.LoadWithOverrides(path, logging.configOverrides())
	return cfg, rest, err
}

func loadConfigFlagForRoles(
	name string,
	args []string,
	roles []string,
) (config.Config, []string, error) {
	path, rest, logging, err := extractRuntimeFlags(name, args)
	if err != nil {
		return config.Config{}, nil, err
	}
	overrides := logging.configOverrides()
	overrides.Roles = roles
	cfg, err := config.LoadWithOverrides(path, overrides)
	return cfg, rest, err
}

type loggingOverrides struct {
	level     string
	format    string
	levelSet  bool
	formatSet bool
}

func (p Program) configureLogging(cfg config.ObservabilityConfig) error {
	if p.ConfigureLogging == nil {
		return nil
	}
	return p.ConfigureLogging(cfg)
}

func (overrides loggingOverrides) configOverrides() config.Overrides {
	result := config.Overrides{}
	if overrides.levelSet {
		result.LogLevel = &overrides.level
	}
	if overrides.formatSet {
		result.LogFormat = &overrides.format
	}
	return result
}

// extractRuntimeFlags keeps resource-specific arguments intact for the runtime
// backend. The standard flag package stops at the first positional argument,
// which would otherwise make `admin ...` flag ordering surprising.
func extractRuntimeFlags(
	name string,
	args []string,
) (string, []string, loggingOverrides, error) {
	var (
		path      string
		overrides loggingOverrides
	)
	rest := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch {
		case argument == "--config":
			value, next, err := requiredFlagValue(name, "config", args, index)
			if err != nil {
				return "", nil, loggingOverrides{}, err
			}
			if path != "" {
				return "", nil, loggingOverrides{}, fmt.Errorf("%s: --config may only be supplied once", name)
			}
			path = value
			index = next
		case strings.HasPrefix(argument, "--config="):
			if path != "" {
				return "", nil, loggingOverrides{}, fmt.Errorf("%s: --config may only be supplied once", name)
			}
			path = strings.TrimPrefix(argument, "--config=")
			if path == "" {
				return "", nil, loggingOverrides{}, fmt.Errorf("%s: --config requires a path", name)
			}
		case argument == "--log-level":
			value, next, err := requiredFlagValue(name, "log-level", args, index)
			if err != nil {
				return "", nil, loggingOverrides{}, err
			}
			if err := overrides.setLevel(name, value); err != nil {
				return "", nil, loggingOverrides{}, err
			}
			index = next
		case strings.HasPrefix(argument, "--log-level="):
			if err := overrides.setLevel(name, strings.TrimPrefix(argument, "--log-level=")); err != nil {
				return "", nil, loggingOverrides{}, err
			}
		case argument == "--log-format":
			value, next, err := requiredFlagValue(name, "log-format", args, index)
			if err != nil {
				return "", nil, loggingOverrides{}, err
			}
			if err := overrides.setFormat(name, value); err != nil {
				return "", nil, loggingOverrides{}, err
			}
			index = next
		case strings.HasPrefix(argument, "--log-format="):
			if err := overrides.setFormat(name, strings.TrimPrefix(argument, "--log-format=")); err != nil {
				return "", nil, loggingOverrides{}, err
			}
		default:
			rest = append(rest, argument)
		}
	}
	return path, rest, overrides, nil
}

func requiredFlagValue(
	command string,
	flagName string,
	args []string,
	index int,
) (string, int, error) {
	if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
		return "", index, fmt.Errorf("%s: --%s requires a value", command, flagName)
	}
	return args[index+1], index + 1, nil
}

func (overrides *loggingOverrides) setLevel(command, value string) error {
	if overrides.levelSet {
		return fmt.Errorf("%s: --log-level may only be supplied once", command)
	}
	if !isLogLevel(value) {
		return fmt.Errorf("%s: --log-level must be debug, info, warn, or error", command)
	}
	overrides.level, overrides.levelSet = value, true
	return nil
}

func (overrides *loggingOverrides) setFormat(command, value string) error {
	if overrides.formatSet {
		return fmt.Errorf("%s: --log-format may only be supplied once", command)
	}
	if value != "json" && value != "text" {
		return fmt.Errorf("%s: --log-format must be json or text", command)
	}
	overrides.format, overrides.formatSet = value, true
	return nil
}

func isLogLevel(value string) bool {
	switch value {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}

// extractConfigFlag is retained for focused parser tests.
func extractConfigFlag(name string, args []string) (string, []string, error) {
	path, rest, _, err := extractRuntimeFlags(name, args)
	return path, rest, err
}
