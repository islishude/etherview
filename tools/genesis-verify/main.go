// Command genesis-verify publishes Preview genesis sources through the ordinary API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "genesis-verify:", err)
		os.Exit(1)
	}
}

func run(parent context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	flags := flag.NewFlagSet("genesis-verify", flag.ContinueOnError)
	flags.SetOutput(stdout)
	directory := flags.String("inventory-dir", "/verification", "read-only source inventory directory")
	genesisFile := flags.String("genesis", "/config/genesis.json", "runtime genesis file")
	api := flags.String("api", "http://api:8080", "internal API origin")
	rpc := flags.String("rpc", "http://geth:8545", "internal Geth endpoint")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid arguments")
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	ctx, cancel := context.WithTimeout(parent, 25*time.Minute)
	defer cancel()
	token, err := io.ReadAll(io.LimitReader(stdin, 1025))
	if err != nil || len(token) > 1024 || strings.TrimSpace(string(token)) == "" {
		return errors.New("missing or invalid verification credential")
	}
	inv, sources, err := loadInventory(*directory, *genesisFile)
	if err != nil {
		return err
	}
	client := newTransport(*api, *rpc, strings.TrimSpace(string(token)))
	v := verifier{
		inventory: inv, bundles: sources,
		request: client.request, rpc: client.rpc,
		wait: wait, timeout: 3 * time.Minute,
		log: func(format string, args ...any) { _, _ = fmt.Fprintf(stdout, format+"\n", args...) },
	}
	return v.verifyAll(ctx)
}
