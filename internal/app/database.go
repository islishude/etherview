package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/islishude/etherview/internal/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func openDatabase(ctx context.Context, cfg config.DatabaseConfig) (*sql.DB, error) {
	parsed, err := pgx.ParseConfig(cfg.URL)
	if err != nil {
		return nil, errors.New("parse PostgreSQL configuration")
	}
	parsed.ConnectTimeout = cfg.ConnectTimeout
	if parsed.RuntimeParams == nil {
		parsed.RuntimeParams = make(map[string]string)
	}
	parsed.RuntimeParams["application_name"] = "etherview"
	parsed.RuntimeParams["statement_timeout"] = strconv.FormatInt(cfg.StatementTimeout.Milliseconds(), 10)
	db := stdlib.OpenDB(*parsed)
	db.SetMaxOpenConns(int(cfg.MaxConnections))
	db.SetMaxIdleConns(int(cfg.MinConnections))
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)
	connectCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := db.PingContext(connectCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %s", redactDatabaseError(err, cfg.URL))
	}
	return db, nil
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
