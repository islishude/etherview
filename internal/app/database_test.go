package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
)

func TestReadDatabaseConfig(t *testing.T) {
	t.Parallel()
	base := config.DatabaseConfig{
		URL:            "postgres://writer.example/etherview",
		MaxConnections: 20, MinConnections: 2,
		ConnectTimeout: 7 * time.Second, StatementTimeout: 11 * time.Second,
	}
	tests := []struct {
		name    string
		config  config.DatabaseConfig
		enabled bool
		want    config.DatabaseConfig
	}{
		{name: "disabled", config: base},
		{
			name: "URL inherits limits", enabled: true,
			config: func() config.DatabaseConfig {
				cfg := base
				cfg.ReadURL = "postgres://reader.example/etherview"
				return cfg
			}(),
			want: config.DatabaseConfig{
				URL:            "postgres://reader.example/etherview",
				MaxConnections: 20, MinConnections: 2,
				ConnectTimeout: 7 * time.Second, StatementTimeout: 11 * time.Second,
			},
		},
		{
			name: "limits inherit URL and unset values", enabled: true,
			config: func() config.DatabaseConfig {
				cfg := base
				cfg.ReadMaxConnections = 8
				cfg.ReadMinConnections = 1
				return cfg
			}(),
			want: config.DatabaseConfig{
				URL:            "postgres://writer.example/etherview",
				MaxConnections: 8, MinConnections: 1,
				ConnectTimeout: 7 * time.Second, StatementTimeout: 11 * time.Second,
			},
		},
		{
			name: "partial limit inherits writer minimum", enabled: true,
			config: func() config.DatabaseConfig {
				cfg := base
				cfg.ReadMaxConnections = 10
				return cfg
			}(),
			want: config.DatabaseConfig{
				URL:            "postgres://writer.example/etherview",
				MaxConnections: 10, MinConnections: 2,
				ConnectTimeout: 7 * time.Second, StatementTimeout: 11 * time.Second,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, enabled := readDatabaseConfig(test.config)
			if enabled != test.enabled {
				t.Fatalf("enabled=%t want %t", enabled, test.enabled)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("read config=%#v want %#v", got, test.want)
			}
		})
	}
}

func TestDatabaseConnectionConfigEnforcesPoolRole(t *testing.T) {
	t.Parallel()
	cfg := config.DatabaseConfig{
		URL:            "postgres://example.invalid/etherview?application_name=caller&default_transaction_read_only=off",
		ConnectTimeout: 3 * time.Second, StatementTimeout: 5 * time.Second,
	}
	reader, err := databaseConnectionConfig(cfg, "etherview-reader", true)
	if err != nil {
		t.Fatal(err)
	}
	if reader.RuntimeParams["application_name"] != "etherview-reader" ||
		reader.RuntimeParams["default_transaction_read_only"] != "on" ||
		reader.RuntimeParams["statement_timeout"] != "5000" {
		t.Fatalf("unexpected reader runtime parameters: %#v", reader.RuntimeParams)
	}
	writer, err := databaseConnectionConfig(cfg, "etherview-writer", false)
	if err != nil {
		t.Fatal(err)
	}
	if writer.RuntimeParams["application_name"] != "etherview-writer" ||
		writer.RuntimeParams["default_transaction_read_only"] != "off" {
		t.Fatalf("unexpected writer runtime parameters: %#v", writer.RuntimeParams)
	}
}

func TestReadDatabaseConfigIsUsedOnlyByAPIRoles(t *testing.T) {
	t.Parallel()
	cfg := config.DatabaseConfig{
		URL:            "postgres://writer.example/etherview",
		ReadURL:        "postgres://reader.example/etherview",
		MaxConnections: 20, MinConnections: 2,
	}
	if _, enabled := readDatabaseConfigForRoles(
		cfg,
		map[components.Role]bool{components.RoleSync: true},
	); enabled {
		t.Fatal("sync-only process enabled the API read pool")
	}
	if readCfg, enabled := readDatabaseConfigForRoles(
		cfg,
		map[components.Role]bool{components.RoleAPI: true},
	); !enabled || readCfg.URL != cfg.ReadURL {
		t.Fatalf("API read config=%#v enabled=%t", readCfg, enabled)
	}
}

type testDatabasePinger struct {
	calls *int
	err   error
}

func (pinger testDatabasePinger) Ping(context.Context) error {
	(*pinger.calls)++
	return pinger.err
}

func TestDatabasePingerGroupChecksEveryConfiguredPool(t *testing.T) {
	t.Parallel()
	writerCalls, readerCalls := 0, 0
	group := databasePingerGroup{
		testDatabasePinger{calls: &writerCalls},
		testDatabasePinger{calls: &readerCalls, err: errors.New("reader unavailable")},
	}
	if err := group.Ping(context.Background()); err == nil {
		t.Fatal("expected reader health failure")
	}
	if writerCalls != 1 || readerCalls != 1 {
		t.Fatalf("ping calls writer=%d reader=%d", writerCalls, readerCalls)
	}
}

func TestNativeDatabasePoolBoundsAndSessionConfiguration(t *testing.T) {
	t.Parallel()
	config := config.DatabaseConfig{
		URL:            "postgres://localhost/etherview?pool_max_conns=99&pool_min_conns=8",
		MaxConnections: 7, MinConnections: 2,
		ConnectTimeout: 3 * time.Second, StatementTimeout: 11 * time.Second,
	}
	pool, err := databasePoolConfig(config, "etherview-reader", true)
	if err != nil {
		t.Fatal(err)
	}
	if pool.MaxConns != 7 || pool.MinConns != 2 || pool.MaxConnIdleTime != 5*time.Minute || pool.MaxConnLifetime != 30*time.Minute {
		t.Fatalf("pool bounds differ from explicit config: %+v", pool)
	}
	if _, leaked := pool.ConnConfig.RuntimeParams["pool_max_conns"]; leaked {
		t.Fatal("pool parameter leaked into PostgreSQL session")
	}
	if pool.ConnConfig.ConnectTimeout != config.ConnectTimeout || pool.ConnConfig.RuntimeParams["default_transaction_read_only"] != "on" || pool.ConnConfig.RuntimeParams["application_name"] != "etherview-reader" || pool.ConnConfig.RuntimeParams["statement_timeout"] != "11000" {
		t.Fatal("pool lost connection/session settings")
	}
}
