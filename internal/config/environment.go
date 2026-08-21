package config

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"
)

var allowedRoles = []string{"api", "sync", "enrich", "trace", "metadata", "maintenance"}

// NormalizeRoles validates roles, expands all, removes duplicates, and returns
// roles in stable architectural order.
func NormalizeRoles(input []string) ([]string, error) {
	if len(input) == 0 {
		return nil, errors.New("runtime.roles cannot be empty")
	}
	wanted := make(map[string]bool, len(input))
	for _, raw := range input {
		for role := range strings.SplitSeq(raw, ",") {
			role = strings.ToLower(strings.TrimSpace(role))
			if role == "" {
				continue
			}
			if role == "all" {
				for _, item := range allowedRoles {
					wanted[item] = true
				}
				continue
			}
			if role == "verify" {
				return nil, errors.New(`runtime role "verify" was removed; use the api role with its solc-js executor`)
			}
			known := slices.Contains(allowedRoles, role)
			if !known {
				return nil, fmt.Errorf("unsupported runtime role %q", role)
			}
			wanted[role] = true
		}
	}
	if len(wanted) == 0 {
		return nil, errors.New("runtime.roles cannot be empty")
	}
	out := make([]string, 0, len(wanted))
	for _, role := range allowedRoles {
		if wanted[role] {
			out = append(out, role)
		}
	}
	return out, nil
}

func applyEnvironment(cfg *Config, lookup func(string) (string, bool), readFile func(string) ([]byte, error)) error {
	return applyEnvironmentForRoles(cfg, lookup, readFile, nil)
}

func applyEnvironmentForRoles(
	cfg *Config,
	lookup func(string) (string, bool),
	readFile func(string) ([]byte, error),
	forcedRoles []string,
) error {
	apiRole, err := applyRoleEnvironment(cfg, lookup, forcedRoles)
	if err != nil {
		return err
	}
	for _, apply := range []func() error{
		func() error { return applySecretEnvironment(cfg, lookup, readFile, apiRole) },
		func() error { return applyStringEnvironment(cfg, lookup, readFile, apiRole) },
		func() error { return applyNumericEnvironment(cfg, lookup) },
		func() error { return applyDurationEnvironment(cfg, lookup) },
		func() error { return applyBooleanEnvironment(cfg, lookup) },
		func() error { return applyRPCEnvironment(cfg, lookup, readFile, apiRole) },
	} {
		if err := apply(); err != nil {
			return err
		}
	}
	return nil
}

func parseSecretHeaders(value string) (map[string]string, error) {
	if len(value) > 16<<10 {
		return nil, errors.New("ETHERVIEW_X402_FACILITATOR_HEADERS exceeds 16384 bytes")
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("ETHERVIEW_X402_FACILITATOR_HEADERS must be a JSON object of string values")
	}
	headers := make(map[string]string)
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok {
			return nil, errors.New("ETHERVIEW_X402_FACILITATOR_HEADERS must be a JSON object of string values")
		}
		if _, exists := headers[key]; exists {
			return nil, errors.New("ETHERVIEW_X402_FACILITATOR_HEADERS contains a duplicate header")
		}
		var headerValue string
		if err := decoder.Decode(&headerValue); err != nil {
			return nil, errors.New("ETHERVIEW_X402_FACILITATOR_HEADERS must be a JSON object of string values")
		}
		headers[key] = headerValue
		if len(headers) > 32 {
			return nil, errors.New("ETHERVIEW_X402_FACILITATOR_HEADERS contains too many headers")
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') {
		return nil, errors.New("ETHERVIEW_X402_FACILITATOR_HEADERS must be a JSON object of string values")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("ETHERVIEW_X402_FACILITATOR_HEADERS contains trailing JSON")
	}
	return headers, nil
}

// parseEnvironmentRPCEndpoints keeps the original comma-separated shorthand
// while allowing the same Secret value to carry purpose and per-process rate
// policy. Parse failures never include the raw value because RPC URLs may
// contain credentials.
func parseEnvironmentRPCEndpoints(value string) ([]RPCEndpoint, error) {
	return parseEnvironmentRPCEndpointsNamed("ETHERVIEW_RPC_URLS", value, []string{"all"})
}

func parseEnvironmentRPCEndpointsNamed(name, value string, defaultPurposes []string) ([]RPCEndpoint, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "[") {
		decoder := json.NewDecoder(strings.NewReader(value))
		decoder.DisallowUnknownFields()
		var endpoints []RPCEndpoint
		if err := decoder.Decode(&endpoints); err != nil {
			return nil, fmt.Errorf("%s contains invalid endpoint JSON", name)
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s contains invalid endpoint JSON", name)
		}
		if len(endpoints) == 0 {
			return nil, fmt.Errorf("%s endpoint JSON must not be empty", name)
		}
		return endpoints, nil
	}
	var endpoints []RPCEndpoint
	for raw := range strings.SplitSeq(value, ",") {
		raw = strings.TrimSpace(raw)
		if raw != "" {
			endpoints = append(endpoints, RPCEndpoint{
				Name: fmt.Sprintf("env-%d", len(endpoints)+1), URL: raw, Purposes: slices.Clone(defaultPurposes),
			})
		}
	}
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("%s must contain at least one endpoint", name)
	}
	return endpoints, nil
}

func lookupValueOrFile(name string, lookup func(string) (string, bool), readFile func(string) ([]byte, error)) (string, error) {
	value, valueSet := lookup(envPrefix + name)
	path, fileSet := lookup(envPrefix + name + "_FILE")
	if valueSet && fileSet {
		return "", fmt.Errorf("%s%s and %s%s_FILE are mutually exclusive", envPrefix, name, envPrefix, name)
	}
	if fileSet {
		data, err := readFile(path)
		if err != nil {
			return "", fmt.Errorf("read %s%s_FILE: %w", envPrefix, name, err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	return strings.TrimSpace(value), nil
}

func setString(lookup func(string) (string, bool), name string, target *string) {
	if value, ok := lookup(envPrefix + name); ok {
		*target = strings.TrimSpace(value)
	}
}

func setExactString(lookup func(string) (string, bool), name string, target *string) {
	if value, ok := lookup(envPrefix + name); ok {
		*target = value
	}
}

func setUint64(lookup func(string) (string, bool), name string, target *uint64) error {
	if value, ok := lookup(envPrefix + name); ok {
		parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return fmt.Errorf("parse %s%s: %w", envPrefix, name, err)
		}
		*target = parsed
	}
	return nil
}

func setInt(lookup func(string) (string, bool), name string, target *int) error {
	if value, ok := lookup(envPrefix + name); ok {
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("parse %s%s: %w", envPrefix, name, err)
		}
		*target = parsed
	}
	return nil
}

func setInt64(lookup func(string) (string, bool), name string, target *int64) error {
	if value, ok := lookup(envPrefix + name); ok {
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return fmt.Errorf("parse %s%s: %w", envPrefix, name, err)
		}
		*target = parsed
	}
	return nil
}

func setFloat64(lookup func(string) (string, bool), name string, target *float64) error {
	if value, ok := lookup(envPrefix + name); ok {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return fmt.Errorf("parse %s%s: %w", envPrefix, name, err)
		}
		*target = parsed
	}
	return nil
}

func setInt32(lookup func(string) (string, bool), name string, target *int32) error {
	if value, ok := lookup(envPrefix + name); ok {
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 32)
		if err != nil {
			return fmt.Errorf("parse %s%s: %w", envPrefix, name, err)
		}
		*target = int32(parsed)
	}
	return nil
}

func setUint8(lookup func(string) (string, bool), name string, target *uint8) error {
	if value, ok := lookup(envPrefix + name); ok {
		parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 8)
		if err != nil {
			return fmt.Errorf("parse %s%s: %w", envPrefix, name, err)
		}
		*target = uint8(parsed)
	}
	return nil
}

func setDuration(lookup func(string) (string, bool), name string, target *time.Duration) error {
	if value, ok := lookup(envPrefix + name); ok {
		parsed, err := time.ParseDuration(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("parse %s%s: %w", envPrefix, name, err)
		}
		*target = parsed
	}
	return nil
}

func setBool(lookup func(string) (string, bool), name string, target *bool) error {
	if value, ok := lookup(envPrefix + name); ok {
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("parse %s%s: %w", envPrefix, name, err)
		}
		*target = parsed
	}
	return nil
}

func splitCSV(value string) []string {
	var result []string
	for item := range strings.SplitSeq(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func validAdapterNamespace(value string) bool {
	if len(value) < 1 || len(value) > 63 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validS3Bucket(value string) bool {
	if len(value) < 3 || len(value) > 63 || value[0] == '-' || value[0] == '.' ||
		value[len(value)-1] == '-' || value[len(value)-1] == '.' ||
		strings.Contains(value, "..") || strings.Contains(value, ".-") || strings.Contains(value, "-.") {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '.' || character == '-' {
			continue
		}
		return false
	}
	// DNS-looking IPv4 addresses are prohibited as S3 bucket names.
	return net.ParseIP(value) == nil
}

func validCanonicalTrustedProxy(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	if address, err := netip.ParseAddr(value); err == nil {
		return address.Zone() == "" && !address.Is4In6() && address.String() == value
	}
	prefix, err := netip.ParsePrefix(value)
	return err == nil && prefix.Addr().Zone() == "" && !prefix.Addr().Is4In6() &&
		prefix == prefix.Masked() && prefix.String() == value
}

func validFixedHex(value string, byteLen int) bool {
	if len(value) != 2+byteLen*2 || !strings.HasPrefix(value, "0x") {
		return false
	}
	_, err := hex.DecodeString(value[2:])
	return err == nil
}
