// Command loadtest drives the public Etherview API for reproducible load and
// soak evidence. It is a repository verification tool, not a production
// server subcommand and is not copied into the production image.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/islishude/etherview/internal/loadtest"
)

type pathsFlag []string

func (paths *pathsFlag) String() string { return strings.Join(*paths, ",") }
func (paths *pathsFlag) Set(value string) error {
	*paths = append(*paths, value)
	return nil
}

type environmentDefaults struct {
	baseURL                string
	paths                  pathsFlag
	rate                   int
	duration               time.Duration
	concurrency            int
	requestTimeout         time.Duration
	maximumP95             time.Duration
	maximumErrorRate       float64
	minimumThroughputRatio float64
	maximumLag             uint64
	apiKeyFile             string
	profile                string
	revision               string
	dataset                string
	hardware               string
	rpcBehavior            string
}

func main() {
	defaults, err := loadEnvironmentDefaults(os.LookupEnv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "loadtest:", err)
		os.Exit(2)
	}
	paths := append(pathsFlag(nil), defaults.paths...)
	baseURL := flag.String("base-url", defaults.baseURL, "Etherview HTTP origin")
	flag.Var(&paths, "path", "same-origin API path; repeat for a route mix")
	rate := flag.Int("rate", defaults.rate, "target requests per second")
	duration := flag.Duration("duration", defaults.duration, "test duration")
	concurrency := flag.Int("concurrency", defaults.concurrency, "maximum in-flight workers")
	requestTimeout := flag.Duration("request-timeout", defaults.requestTimeout, "per-request timeout")
	maximumP95 := flag.Duration("max-p95", defaults.maximumP95, "maximum accepted p95 latency")
	maximumErrorRate := flag.Float64("max-error-rate", defaults.maximumErrorRate, "maximum accepted error fraction")
	minimumThroughputRatio := flag.Float64(
		"min-throughput-ratio",
		defaults.minimumThroughputRatio,
		"minimum achieved/target throughput",
	)
	maximumLag := flag.Uint64("max-lag", defaults.maximumLag, "maximum accepted core lag in blocks")
	apiKeyFile := flag.String(
		"api-key-file",
		defaults.apiKeyFile,
		"optional file containing an X-API-Key value",
	)
	profile := flag.String("profile", defaults.profile, "profile name recorded in JSON")
	revision := flag.String("revision", defaults.revision, "source revision recorded in JSON")
	dataset := flag.String("dataset", defaults.dataset, "dataset description recorded in JSON")
	hardware := flag.String("hardware", defaults.hardware, "hardware description recorded in JSON")
	rpcBehavior := flag.String(
		"rpc-behavior",
		defaults.rpcBehavior,
		"RPC latency/error model recorded in JSON",
	)
	flag.Parse()

	if len(paths) == 0 {
		paths = pathsFlag{
			"/api/v1/config",
			"/api/v1/status",
			"/api/v1/blocks?limit=20",
			"/api/v1/transactions?limit=20",
		}
	}
	apiKey, err := readAPIKey(*apiKeyFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "loadtest:", err)
		os.Exit(2)
	}
	report, runErr := loadtest.Run(context.Background(), loadtest.Config{
		BaseURL: *baseURL, Paths: paths, Rate: *rate, Duration: *duration,
		Concurrency: *concurrency, RequestTimeout: *requestTimeout,
		MaximumP95: *maximumP95, MaximumErrorRate: *maximumErrorRate,
		MinimumThroughputRatio: *minimumThroughputRatio, MaximumLag: *maximumLag,
		APIKey: apiKey, Profile: *profile, Revision: *revision,
		Dataset: *dataset, Hardware: *hardware, RPCBehavior: *rpcBehavior,
	})
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, "loadtest: encode report")
		os.Exit(1)
	}
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "loadtest:", runErr)
		os.Exit(1)
	}
}

func loadEnvironmentDefaults(lookup func(string) (string, bool)) (environmentDefaults, error) {
	defaults := environmentDefaults{
		baseURL: "http://127.0.0.1:8080", rate: 100, duration: 30 * time.Second,
		concurrency: 32, requestTimeout: 5 * time.Second, maximumP95: 500 * time.Millisecond,
		maximumErrorRate: 0.001, minimumThroughputRatio: 0.95, maximumLag: 2,
		profile: "load-smoke", revision: "working-tree", dataset: "unspecified",
		hardware: "unspecified", rpcBehavior: "unspecified",
	}
	defaults.baseURL = stringEnvironment(lookup, "ETHERVIEW_LOAD_BASE_URL", defaults.baseURL)
	defaults.apiKeyFile = stringEnvironment(lookup, "ETHERVIEW_LOAD_API_KEY_FILE", "")
	defaults.profile = stringEnvironment(lookup, "ETHERVIEW_LOAD_PROFILE", defaults.profile)
	defaults.revision = stringEnvironment(lookup, "ETHERVIEW_LOAD_REVISION", defaults.revision)
	defaults.dataset = stringEnvironment(lookup, "ETHERVIEW_LOAD_DATASET", defaults.dataset)
	defaults.hardware = stringEnvironment(lookup, "ETHERVIEW_LOAD_HARDWARE", defaults.hardware)
	defaults.rpcBehavior = stringEnvironment(
		lookup,
		"ETHERVIEW_LOAD_RPC_BEHAVIOR",
		defaults.rpcBehavior,
	)
	var err error
	if defaults.rate, err = intEnvironment(lookup, "ETHERVIEW_LOAD_RATE", defaults.rate); err != nil {
		return environmentDefaults{}, err
	}
	if defaults.duration, err = durationEnvironment(
		lookup,
		"ETHERVIEW_LOAD_DURATION",
		defaults.duration,
	); err != nil {
		return environmentDefaults{}, err
	}
	if defaults.concurrency, err = intEnvironment(
		lookup,
		"ETHERVIEW_LOAD_CONCURRENCY",
		defaults.concurrency,
	); err != nil {
		return environmentDefaults{}, err
	}
	if defaults.requestTimeout, err = durationEnvironment(
		lookup,
		"ETHERVIEW_LOAD_REQUEST_TIMEOUT",
		defaults.requestTimeout,
	); err != nil {
		return environmentDefaults{}, err
	}
	if defaults.maximumP95, err = durationEnvironment(
		lookup,
		"ETHERVIEW_LOAD_MAX_P95",
		defaults.maximumP95,
	); err != nil {
		return environmentDefaults{}, err
	}
	if defaults.maximumErrorRate, err = floatEnvironment(
		lookup,
		"ETHERVIEW_LOAD_MAX_ERROR_RATE",
		defaults.maximumErrorRate,
	); err != nil {
		return environmentDefaults{}, err
	}
	if defaults.minimumThroughputRatio, err = floatEnvironment(
		lookup,
		"ETHERVIEW_LOAD_MIN_THROUGHPUT_RATIO",
		defaults.minimumThroughputRatio,
	); err != nil {
		return environmentDefaults{}, err
	}
	if defaults.maximumLag, err = uint64Environment(
		lookup,
		"ETHERVIEW_LOAD_MAX_LAG",
		defaults.maximumLag,
	); err != nil {
		return environmentDefaults{}, err
	}
	if encoded, ok := lookup("ETHERVIEW_LOAD_PATHS"); ok {
		if err := json.Unmarshal([]byte(encoded), &defaults.paths); err != nil || len(defaults.paths) == 0 {
			return environmentDefaults{}, errors.New("ETHERVIEW_LOAD_PATHS must be a non-empty JSON string array")
		}
	}
	return defaults, nil
}

func stringEnvironment(
	lookup func(string) (string, bool),
	name string,
	fallback string,
) string {
	if value, ok := lookup(name); ok {
		return value
	}
	return fallback
}

func intEnvironment(
	lookup func(string) (string, bool),
	name string,
	fallback int,
) (int, error) {
	value, ok := lookup(name)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return parsed, nil
}

func uint64Environment(
	lookup func(string) (string, bool),
	name string,
	fallback uint64,
) (uint64, error) {
	value, ok := lookup(name)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an unsigned integer", name)
	}
	return parsed, nil
}

func floatEnvironment(
	lookup func(string) (string, bool),
	name string,
	fallback float64,
) (float64, error) {
	value, ok := lookup(name)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a decimal number", name)
	}
	return parsed, nil
}

func durationEnvironment(
	lookup func(string) (string, bool),
	name string,
	fallback time.Duration,
) (time.Duration, error) {
	value, ok := lookup(name)
	if !ok {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration", name)
	}
	return parsed, nil
}

func readAPIKey(path string) (string, error) {
	value, valueSet := os.LookupEnv("ETHERVIEW_LOAD_API_KEY")
	if path != "" && valueSet {
		return "", errors.New("-api-key-file and ETHERVIEW_LOAD_API_KEY are mutually exclusive")
	}
	sourceSet := valueSet
	if path != "" {
		sourceSet = true
		file, err := os.Open(path)
		if err != nil {
			return "", errors.New("read API key file")
		}
		defer file.Close() //nolint:errcheck
		data, err := io.ReadAll(io.LimitReader(file, 4_097))
		if err != nil {
			return "", errors.New("read API key file")
		}
		if len(data) > 4_096 {
			return "", errors.New("API key exceeds 4096 bytes")
		}
		value = string(data)
	}
	if len(value) > 4_096 {
		return "", errors.New("API key exceeds 4096 bytes")
	}
	value = strings.TrimSpace(value)
	if sourceSet && value == "" {
		return "", errors.New("API key source is empty")
	}
	return value, nil
}
