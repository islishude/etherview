// Command x402facilitator runs the standalone multi-chain settlement service.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/islishude/etherview/internal/facilitator"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error { return runArgs(os.Args[1:]) }
func runArgs(args []string) error {
	flags := flag.NewFlagSet("x402facilitator", flag.ContinueOnError)
	check := flags.Bool("check-config", false, "validate configuration and local files without external calls")
	path := flags.String("config", "/etc/etherview/facilitator.yaml", "configuration file")
	health := flags.String("healthcheck", "", "local liveness URL")
	recoverID := flags.String("rebroadcast", "", "operator-authorized exact journal transaction rebroadcast")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || (*check && (*health != "" || *recoverID != "")) {
		return errors.New("invalid_arguments")
	}
	if *health != "" {
		client := &http.Client{Timeout: 2 * time.Second}
		r, err := client.Get(*health)
		if err != nil {
			return errors.New("healthcheck_failed")
		}
		_ = r.Body.Close()
		if r.StatusCode != http.StatusNoContent {
			return errors.New("healthcheck_failed")
		}
		return nil
	}
	config, err := facilitator.LoadConfig(*path)
	if err != nil {
		return err
	}
	if *check {
		return facilitator.CheckConfig(config)
	}
	startup, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	service, err := facilitator.New(startup, config)
	cancel()
	if err != nil {
		return err
	}
	defer service.Close()
	if *recoverID != "" {
		ctx, stop := context.WithTimeout(context.Background(), 25*time.Second)
		defer stop()
		return service.Rebroadcast(ctx, *recoverID)
	}
	server := &http.Server{Addr: config.Server.Listen, Handler: service.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	failure := make(chan error, 1)
	go func() {
		if config.Server.AllowInsecureHTTP {
			failure <- server.ListenAndServe()
		} else {
			failure <- server.ListenAndServeTLS(config.Server.TLSCertFile, config.Server.TLSKeyFile)
		}
	}()
	select {
	case <-ctx.Done():
	case err = <-failure:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return errors.New("facilitator_listener_failed")
		}
	}
	shutdown, done := context.WithTimeout(context.Background(), 30*time.Second)
	defer done()
	if err = server.Shutdown(shutdown); err != nil {
		_ = server.Close()
		return errors.New("facilitator_shutdown_timeout")
	}
	return nil
}
