// Command previewcheck validates the live local Preview topology and its public
// HTTPS feature contract without trusting Docker Compose's --wait result.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/islishude/etherview/internal/previewcheck"
)

func main() {
	var options previewcheck.Options
	flag.StringVar(&options.Root, "root", ".", "repository root")
	flag.StringVar(&options.ProjectName, "project", "etherview-preview", "Docker Compose project name")
	flag.StringVar(&options.DockerCommand, "docker", dockerDefault(), "Docker CLI path")
	flag.StringVar(&options.ConfigURL, "config-url", "https://etherview.localhost:8080/api/v1/config", "public Preview HTTPS config URL")
	flag.StringVar(&options.CAFile, "ca-file", "", "optional PEM CA file for the Preview certificate")
	flag.BoolVar(&options.InsecureSkipVerify, "insecure", false, "explicitly allow the local Preview certificate without verification")
	flag.DurationVar(&options.Timeout, "timeout", previewcheck.DefaultTimeout, "total bounded readiness and stability timeout")
	flag.DurationVar(&options.PollInterval, "poll-interval", previewcheck.DefaultPollInterval, "container polling interval")
	flag.DurationVar(&options.StabilityWindow, "stability-window", previewcheck.DefaultStabilityWindow, "final unchanged-container observation window")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	started := time.Now()
	if err := previewcheck.Check(ctx, options); err != nil {
		fmt.Fprintf(os.Stderr, "preview-check: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("preview-check: PASS (complete topology and stable restart counts for %s; elapsed %s)\n",
		options.StabilityWindow, time.Since(started).Round(time.Millisecond))
}

func dockerDefault() string {
	if value := strings.TrimSpace(os.Getenv("DOCKER")); value != "" {
		return value
	}
	return "docker"
}
