package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/islishude/etherview/internal/app"
	"github.com/islishude/etherview/internal/cli"
	"github.com/islishude/etherview/internal/observability"
)

var (
	version   = "dev"
	revision  = "unknown"
	buildDate = "unknown"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger := observability.NewLogger(observability.LoggerOptions{
		Writer: os.Stderr, Service: "etherview", Version: version,
	})
	backend := &app.Backend{Stdout: os.Stdout, Stderr: os.Stderr, Logger: logger, Version: version}
	program := cli.Program{
		Backend: backend, Version: fmt.Sprintf("%s (revision=%s built=%s)", version, revision, buildDate),
		Stdout: os.Stdout, Stderr: os.Stderr,
	}
	os.Exit(program.Run(ctx, os.Args[1:]))
}
