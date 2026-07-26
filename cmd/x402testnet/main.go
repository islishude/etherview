package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/islishude/etherview/internal/x402testnet"
)

func main() {
	os.Exit(run())
}

func run() (exitCode int) {
	defer func() {
		if recover() == nil {
			return
		}
		_, _ = fmt.Fprintln(os.Stderr, x402testnet.CodeFailed)
		exitCode = 1
	}()
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	cfg, err := x402testnet.LoadConfig()
	if err != nil {
		return fail(err)
	}
	defer cfg.ZeroSecrets()

	report, err := x402testnet.Run(ctx, cfg)
	if err != nil {
		return fail(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return fail(err)
	}
	if _, err := os.Stdout.Write(append(encoded, '\n')); err != nil {
		return fail(err)
	}
	return 0
}

func fail(err error) int {
	_, _ = fmt.Fprintln(os.Stderr, x402testnet.ErrorCode(err))
	return 1
}
