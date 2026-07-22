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
  etherview serve [--config path] [--roles all|api,sync,...]
  etherview doctor [--config path]
  etherview migrate <up|status> [--config path]
  etherview repair [--config path] [arguments]
  etherview reindex [--config path] [arguments]
  etherview admin api-key <create|rotate|revoke|list> [--config path] [arguments]
  etherview admin label <set|delete|list> [--config path] [arguments]
  etherview admin repair list [--config path] [--limit count] [--format json|table]
  etherview version
`

// Backend connects command parsing to runtime implementations. Keeping this
// interface narrow makes every command testable without external services.
type Backend interface {
	Serve(context.Context, config.Config, []string) error
	Migrate(context.Context, config.Config, string) error
	Repair(context.Context, config.Config, string, []string) error
	Admin(context.Context, config.Config, string, string, []string) error
}

type Program struct {
	Backend Backend
	Version string
	Stdout  io.Writer
	Stderr  io.Writer
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
		fmt.Fprint(stderr, usage)
		return 2
	}
	if p.Backend == nil && args[0] != "version" && args[0] != "help" {
		fmt.Fprintln(stderr, "etherview: runtime backend is not configured")
		return 1
	}

	var err error
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "version":
		version := p.Version
		if version == "" {
			version = "dev"
		}
		fmt.Fprintln(stdout, version)
		return 0
	case "serve":
		err = p.runServe(ctx, args[1:])
	case "doctor":
		err = p.runDoctor(args[1:], stdout)
	case "migrate":
		err = p.runMigrate(ctx, args[1:])
	case "repair", "reindex":
		err = p.runRepair(ctx, args[0], args[1:])
	case "admin":
		err = p.runAdmin(ctx, args[1:])
	default:
		fmt.Fprintf(stderr, "etherview: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "etherview: %v\n", err)
		return 1
	}
	return 0
}

func (p Program) runServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("config", "", "configuration file")
	rolesFlag := fs.String("roles", "", "comma-separated runtime roles")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("serve: unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	roles := cfg.Runtime.Roles
	if *rolesFlag != "" {
		roles = strings.Split(*rolesFlag, ",")
	}
	normalized, err := config.NormalizeRoles(roles)
	if err != nil {
		return err
	}
	if err := cfg.ValidateForRoles(normalized); err != nil {
		return err
	}
	return p.Backend.Serve(ctx, cfg, normalized)
}

func (p Program) runDoctor(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("config", "", "configuration file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("doctor does not accept positional arguments")
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	roles, err := config.NormalizeRoles(cfg.Runtime.Roles)
	if err != nil {
		return err
	}
	roleErr := cfg.ValidateForRoles(roles)
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
		Valid:       roleErr == nil,
		ChainID:     fmt.Sprint(cfg.Chain.ID),
		GenesisHash: cfg.Chain.GenesisHash,
		StartBlock:  fmt.Sprint(cfg.Chain.StartBlock),
		Roles:       roles,
		DatabaseSet: strings.TrimSpace(cfg.Database.URL) != "",
		Errors:      validationMessages(roleErr),
	}
	for _, item := range cfg.RPC.Endpoints {
		result.RPCEndpoints = append(result.RPCEndpoints, endpoint{Name: item.Name, Purposes: item.Purposes})
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return err
	}
	return roleErr
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
	if cfg.Database.URL == "" {
		return errors.New("database.url is required")
	}
	return p.Backend.Repair(ctx, cfg, kind, rest)
}

func (p Program) runAdmin(ctx context.Context, args []string) error {
	if len(args) < 2 || (args[0] != "api-key" && args[0] != "label" && args[0] != "repair") {
		return errors.New("admin requires api-key, label, or repair and an action")
	}
	resource, action := args[0], args[1]
	cfg, rest, err := loadConfigFlag("admin", args[2:])
	if err != nil {
		return err
	}
	if cfg.Database.URL == "" {
		return errors.New("database.url is required")
	}
	return p.Backend.Admin(ctx, cfg, resource, action, rest)
}

func loadConfigFlag(name string, args []string) (config.Config, []string, error) {
	path, rest, err := extractConfigFlag(name, args)
	if err != nil {
		return config.Config{}, nil, err
	}
	cfg, err := config.Load(path)
	return cfg, rest, err
}

// extractConfigFlag keeps resource-specific arguments intact for the runtime
// backend. The standard flag package stops at the first positional argument,
// which would otherwise make `admin ... --config` ordering surprising.
func extractConfigFlag(name string, args []string) (string, []string, error) {
	var path string
	rest := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch {
		case argument == "--config":
			if path != "" {
				return "", nil, fmt.Errorf("%s: --config may only be supplied once", name)
			}
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return "", nil, fmt.Errorf("%s: --config requires a path", name)
			}
			path = args[index+1]
			index++
		case strings.HasPrefix(argument, "--config="):
			if path != "" {
				return "", nil, fmt.Errorf("%s: --config may only be supplied once", name)
			}
			path = strings.TrimPrefix(argument, "--config=")
			if path == "" {
				return "", nil, fmt.Errorf("%s: --config requires a path", name)
			}
		default:
			rest = append(rest, argument)
		}
	}
	return path, rest, nil
}
