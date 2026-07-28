package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/islishude/etherview/internal/app"
	"github.com/islishude/etherview/internal/cli"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/observability"
)

func main() {
	var (
		version, revision, buildDate = cli.BuildMetadata()
	)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	backend := &app.Backend{Stdout: os.Stdout, Stderr: os.Stderr, Version: version}
	program := cli.Program{
		Backend: backend, Version: fmt.Sprintf("%s (revision=%s built=%s)", version, revision, buildDate),
		Stdout: os.Stdout, Stderr: os.Stderr,
		ConfigureLogging: func(cfg config.ObservabilityConfig) error {
			backend.Logger = observability.NewLogger(observability.LoggerOptions{
				Writer: os.Stderr, Level: observability.ParseLogLevel(cfg.LogLevel),
				Format:  observability.LogFormat(cfg.LogFormat),
				Service: "etherview", Version: version,
			})
			return nil
		},
	}
	os.Exit(program.Run(ctx, os.Args[1:]))
}
