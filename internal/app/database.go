package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	pgxpool "github.com/jackc/pgx/v5/pgxpool"

	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/store"
	"github.com/jackc/pgx/v5"
)

func openDatabase(ctx context.Context, cfg config.DatabaseConfig) (*pgxpool.Pool, error) {
	return openDatabaseWithOptions(ctx, cfg, "etherview-writer", false)
}

func openReadDatabase(ctx context.Context, cfg config.DatabaseConfig) (*pgxpool.Pool, error) {
	return openDatabaseWithOptions(ctx, cfg, "etherview-reader", true)
}

func openDatabaseWithOptions(
	ctx context.Context,
	cfg config.DatabaseConfig,
	applicationName string,
	readOnly bool,
) (*pgxpool.Pool, error) {
	poolConfig, err := databasePoolConfig(cfg, applicationName, readOnly)
	if err != nil {
		return nil, errors.New("parse PostgreSQL pool configuration")
	}
	db, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, errors.New("create PostgreSQL pool")
	}
	connectCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := db.Ping(connectCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %s", redactDatabaseError(err, cfg.URL))
	}
	return db, nil
}

func databasePoolConfig(cfg config.DatabaseConfig, applicationName string, readOnly bool) (*pgxpool.Config, error) {
	parsed, err := databaseConnectionConfig(cfg, applicationName, readOnly)
	if err != nil {
		return nil, errors.New("parse PostgreSQL configuration")
	}
	poolConfig, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, errors.New("parse PostgreSQL pool configuration")
	}
	poolConfig.ConnConfig = parsed
	poolConfig.MaxConns = cfg.MaxConnections
	poolConfig.MinConns = cfg.MinConnections
	poolConfig.MaxConnIdleTime = 5 * time.Minute
	poolConfig.MaxConnLifetime = 30 * time.Minute
	return poolConfig, nil
}

func databaseConnectionConfig(
	cfg config.DatabaseConfig,
	applicationName string,
	readOnly bool,
) (*pgx.ConnConfig, error) {
	pool, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, err
	}
	parsed := pool.ConnConfig
	parsed.ConnectTimeout = cfg.ConnectTimeout
	if parsed.RuntimeParams == nil {
		parsed.RuntimeParams = make(map[string]string)
	}
	parsed.RuntimeParams["application_name"] = applicationName
	parsed.RuntimeParams["statement_timeout"] = strconv.FormatInt(cfg.StatementTimeout.Milliseconds(), 10)
	if readOnly {
		parsed.RuntimeParams["default_transaction_read_only"] = "on"
	} else {
		parsed.RuntimeParams["default_transaction_read_only"] = "off"
	}
	return parsed, nil
}

func readDatabaseConfig(cfg config.DatabaseConfig) (config.DatabaseConfig, bool) {
	if cfg.ReadURL == "" && cfg.ReadMaxConnections == 0 && cfg.ReadMinConnections == 0 {
		return config.DatabaseConfig{}, false
	}
	readCfg := config.DatabaseConfig{
		URL:              cfg.ReadURL,
		MaxConnections:   cfg.ReadMaxConnections,
		MinConnections:   cfg.ReadMinConnections,
		ConnectTimeout:   cfg.ConnectTimeout,
		StatementTimeout: cfg.StatementTimeout,
	}
	if readCfg.URL == "" {
		readCfg.URL = cfg.URL
	}
	if readCfg.MaxConnections == 0 {
		readCfg.MaxConnections = cfg.MaxConnections
	}
	if readCfg.MinConnections == 0 {
		readCfg.MinConnections = cfg.MinConnections
	}
	return readCfg, true
}

func readDatabaseConfigForRoles(
	cfg config.DatabaseConfig,
	roles map[components.Role]bool,
) (config.DatabaseConfig, bool) {
	if !roles[components.RoleAPI] {
		return config.DatabaseConfig{}, false
	}
	return readDatabaseConfig(cfg)
}

func checkReadDatabaseSchema(ctx context.Context, db *pgxpool.Pool) error {
	if err := store.CheckSchema(ctx, db); err != nil {
		return fmt.Errorf("check read database schema: %w", err)
	}
	return nil
}

func validateReadDatabaseIdentity(
	ctx context.Context,
	db *pgxpool.Pool,
	writerIdentity store.ChainIdentity,
) error {
	readerIdentity, err := store.ReadChainIdentity(ctx, db, writerIdentity.ChainID)
	if err != nil {
		return fmt.Errorf("validate read database chain identity: %w", err)
	}
	if readerIdentity.ChainID != writerIdentity.ChainID ||
		readerIdentity.GenesisHash != writerIdentity.GenesisHash {
		return errors.New("read database chain identity does not match writer")
	}
	return nil
}

func redactDatabaseError(err error, rawURL string) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if rawURL != "" {
		message = strings.ReplaceAll(message, rawURL, "[database-url-redacted]")
	}
	if parsed, parseErr := url.Parse(rawURL); parseErr == nil && parsed.User != nil {
		if password, ok := parsed.User.Password(); ok && password != "" {
			message = strings.ReplaceAll(message, password, "[redacted]")
		}
	}
	return message
}
