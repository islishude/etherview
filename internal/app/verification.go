package app

import (
	"errors"
	"fmt"
	"os"

	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/etherscan"
	"github.com/islishude/etherview/internal/httpapi"
	"github.com/islishude/etherview/internal/verify"
)

func verificationCompiler(cfg config.Config, catalog *verify.CompilerCatalog) (verify.Compiler, error) {
	switch cfg.Security.CompilerSandbox {
	case "process":
		return &verify.CatalogProcessCompiler{
			Catalog: catalog,
			Cache: &verify.CompilerCache{
				Root: cfg.Verification.CacheDirectory, Timeout: cfg.Verification.Timeout,
			},
			Timeout: cfg.Verification.Timeout, MaxInputBytes: cfg.Verification.MaxInputBytes,
			MaxOutputBytes: cfg.Verification.MaxOutputBytes,
		}, nil
	case "container":
		return &verify.CatalogRunnerCompiler{
			Catalog: catalog,
			Cache: &verify.CompilerCache{
				Root: cfg.Verification.CacheDirectory, Timeout: cfg.Verification.Timeout,
			},
			Runtime: cfg.Verification.ContainerRuntime, RunnerImage: cfg.Verification.RunnerImage,
			Timeout: cfg.Verification.Timeout, MaxInputBytes: cfg.Verification.MaxInputBytes,
			MaxOutputBytes: cfg.Verification.MaxOutputBytes, Memory: cfg.Verification.ContainerMemory,
			CPUs: cfg.Verification.ContainerCPUs, PIDs: cfg.Verification.ContainerPIDs,
		}, nil
	case "disabled":
		return nil, errors.New("verification compiler sandbox is disabled")
	default:
		return nil, fmt.Errorf("unsupported verification compiler sandbox %q", cfg.Security.CompilerSandbox)
	}
}

func verificationWorkerID(index int) string {
	return runtimeWorkerID(indexedWorkerName("verify", index))
}

func publicVerificationService(cfg config.Config, service *verify.Service) *verify.Service {
	if !cfg.Security.PublicVerification {
		return nil
	}
	return service
}

func verificationCapabilityInterfaces(
	service *verify.Service,
	publicService *verify.Service,
) (
	httpapi.VerificationReader,
	httpapi.VerificationSubmitter,
	etherscan.VerificationService,
) {
	var reader httpapi.VerificationReader
	if service != nil {
		reader = service
	}
	var submitter httpapi.VerificationSubmitter
	var compatibility etherscan.VerificationService
	if publicService != nil {
		submitter = publicService
		compatibility = publicService
	}
	return reader, submitter, compatibility
}

func sourcifyClient(cfg config.Config) (*verify.SourcifyClient, error) {
	if !cfg.Features.Sourcify {
		return nil, nil
	}
	client, err := verify.NewSourcifyClient(verify.SourcifyOptions{
		BaseURL:          cfg.Sourcify.BaseURL,
		Timeout:          cfg.Sourcify.Timeout,
		MaxRequestBytes:  cfg.Sourcify.MaxRequestBytes,
		MaxResponseBytes: cfg.Sourcify.MaxResponseBytes,
		Attempts:         cfg.Sourcify.Attempts,
		PollInterval:     cfg.Sourcify.PollInterval,
		MaxPolls:         cfg.Sourcify.MaxPolls,
	})
	if err != nil {
		return nil, fmt.Errorf("configure Sourcify client: %w", err)
	}
	return client, nil
}

func runtimeWorkerID(kind string) string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return runtimeWorkerIDForHost(host, os.Getpid(), kind)
}

func runtimeWorkerIDForHost(host string, pid int, kind string) string {
	suffix := fmt.Sprintf("-%d-%s", pid, kind)
	if len(suffix) >= 128 {
		suffix = suffix[len(suffix)-127:]
	}
	maximumHostBytes := 128 - len(suffix)
	if len(host) > maximumHostBytes {
		host = host[:maximumHostBytes]
	}
	return host + suffix
}
