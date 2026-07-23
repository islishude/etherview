package app

import (
	"errors"
	"fmt"
	"maps"
	"os"

	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/verify"
)

func verificationCompiler(cfg config.Config) (verify.Compiler, error) {
	switch cfg.Security.CompilerSandbox {
	case "process":
		artifacts := make(map[verify.Language]map[string]verify.CompilerArtifact, len(cfg.Verification.Artifacts))
		for language, versions := range cfg.Verification.Artifacts {
			converted := make(map[string]verify.CompilerArtifact, len(versions))
			for version, artifact := range versions {
				converted[version] = verify.CompilerArtifact{
					URL: artifact.URL, SHA256: artifact.SHA256, MaxBytes: artifact.MaxBytes,
				}
			}
			artifacts[verify.Language(language)] = converted
		}
		return verify.ProcessCompiler{
			Cache: &verify.CompilerCache{
				Root: cfg.Verification.CacheDirectory, Artifacts: artifacts,
				Timeout: cfg.Verification.Timeout,
			},
			Timeout: cfg.Verification.Timeout, MaxInputBytes: cfg.Verification.MaxInputBytes,
			MaxOutputBytes: cfg.Verification.MaxOutputBytes, Public: cfg.Security.PublicVerification,
		}, nil
	case "container":
		images := make(map[verify.Language]map[string]string, len(cfg.Verification.Images))
		for language, versions := range cfg.Verification.Images {
			converted := make(map[string]string, len(versions))
			maps.Copy(converted, versions)
			images[verify.Language(language)] = converted
		}
		return &verify.ContainerCompiler{
			Runtime: cfg.Verification.ContainerRuntime, Images: images,
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

func verificationWorkerID() string {
	return runtimeWorkerID("verify")
}

func publicVerificationService(cfg config.Config, service *verify.Service) *verify.Service {
	if !cfg.Security.PublicVerification {
		return nil
	}
	return service
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
	value := fmt.Sprintf("%s-%d-%s", host, os.Getpid(), kind)
	if len(value) > 128 {
		value = value[:128]
	}
	return value
}
