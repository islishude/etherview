package billing

import (
	"errors"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/apiops"
	x402 "github.com/x402-foundation/x402/go/v2"
)

var errInvalidCanonicalResource = errors.New("billing request resource is invalid")

const maximumCanonicalResourceBytes = 4096

var canonicalEVMNetworkPattern = regexp.MustCompile(`^eip155:[1-9][0-9]*$`)

// canonicalResource reconstructs the resource exclusively from the configured
// public origin and the statically matched operation. Request authority and
// forwarded-host fields are deliberately ignored.
func canonicalResource(publicOrigin string, requestURL *url.URL, spec apiops.Spec) (x402.ResourceInfo, error) {
	if requestURL == nil || spec.Method != "GET" || !spec.BillingEligible ||
		requestURL.ForceQuery || requestURL.Fragment != "" ||
		requestURL.EscapedPath() != requestURL.Path {
		return x402.ResourceInfo{}, errInvalidCanonicalResource
	}
	path, err := canonicalOperationPath(spec, requestURL.Path)
	if err != nil {
		return x402.ResourceInfo{}, err
	}
	query, err := canonicalOperationQuery(spec, requestURL.RawQuery)
	if err != nil {
		return x402.ResourceInfo{}, err
	}
	resourceURL := strings.TrimSuffix(publicOrigin, "/") + path
	if query != "" {
		resourceURL += "?" + query
	}
	if len(resourceURL) > maximumCanonicalResourceBytes || !utf8.ValidString(resourceURL) {
		return x402.ResourceInfo{}, errInvalidCanonicalResource
	}
	return x402.ResourceInfo{
		URL:         resourceURL,
		MimeType:    "application/json",
		ServiceName: "Etherview",
	}, nil
}

func canonicalOperationPath(spec apiops.Spec, requestPath string) (string, error) {
	prefix := "/api/v1"
	if !strings.HasPrefix(requestPath, prefix+"/") && requestPath != prefix {
		return "", errInvalidCanonicalResource
	}
	segments := strings.Split(strings.TrimPrefix(requestPath, prefix), "/")
	template := strings.Split(spec.OpenAPIPath, "/")
	if len(segments) != len(template) {
		return "", errInvalidCanonicalResource
	}
	parameters := make(map[string]apiops.ParameterSpec)
	for _, parameter := range spec.Parameters {
		if parameter.In != apiops.ParameterPath {
			continue
		}
		if parameter.Name == "" || parameter.Repetition != apiops.RepetitionReject ||
			!parameter.Required {
			return "", errInvalidCanonicalResource
		}
		if _, exists := parameters[parameter.Name]; exists {
			return "", errInvalidCanonicalResource
		}
		parameters[parameter.Name] = parameter
	}
	result := make([]string, len(template))
	used := 0
	for index := range template {
		part := template[index]
		if !strings.HasPrefix(part, "{") || !strings.HasSuffix(part, "}") {
			if part != segments[index] {
				return "", errInvalidCanonicalResource
			}
			result[index] = part
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")
		parameter, ok := parameters[name]
		if !ok {
			return "", errInvalidCanonicalResource
		}
		value, err := canonicalParameter(parameter, segments[index])
		if err != nil {
			return "", err
		}
		result[index] = value
		used++
	}
	if used != len(parameters) {
		return "", errInvalidCanonicalResource
	}
	return prefix + strings.Join(result, "/"), nil
}

func canonicalOperationQuery(spec apiops.Spec, raw string) (string, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "", errInvalidCanonicalResource
	}
	parameters := make(map[string]apiops.ParameterSpec)
	for _, parameter := range spec.Parameters {
		if parameter.In != apiops.ParameterQuery {
			continue
		}
		if parameter.Name == "" || parameter.Repetition != apiops.RepetitionReject {
			return "", errInvalidCanonicalResource
		}
		if _, exists := parameters[parameter.Name]; exists {
			return "", errInvalidCanonicalResource
		}
		parameters[parameter.Name] = parameter
	}
	for key, items := range values {
		if _, ok := parameters[key]; !ok || len(items) != 1 || key == "" ||
			!utf8.ValidString(key) || !utf8.ValidString(items[0]) {
			return "", errInvalidCanonicalResource
		}
	}

	canonical := make(url.Values, len(values)+1)
	for name, parameter := range parameters {
		items, present := values[name]
		if !present {
			if parameter.Required {
				return "", errInvalidCanonicalResource
			}
			if parameter.HasDefault {
				value, canonicalErr := canonicalParameter(
					parameter, parameter.DefaultValue,
				)
				if canonicalErr != nil {
					return "", canonicalErr
				}
				canonical.Set(name, value)
			}
			continue
		}
		value, err := canonicalParameter(parameter, items[0])
		if err != nil {
			return "", err
		}
		canonical.Set(name, value)
	}
	return canonical.Encode(), nil
}

func canonicalParameter(
	parameter apiops.ParameterSpec,
	value string,
) (string, error) {
	if parameter.TrimSpace {
		value = strings.TrimSpace(value)
	}
	if !utf8.ValidString(value) ||
		parameter.MinimumBytes > 0 && len(value) < parameter.MinimumBytes ||
		parameter.MaximumBytes > 0 && len(value) > parameter.MaximumBytes {
		return "", errInvalidCanonicalResource
	}
	switch parameter.Type {
	case apiops.ParameterAddress:
		canonical, ok := canonicalFixedHex(value, 20)
		if !ok {
			return "", errInvalidCanonicalResource
		}
		return canonical, nil
	case apiops.ParameterBillingState:
		switch value {
		case "reserved", "verified", "settling", "settled", "failed", "expired":
			return value, nil
		default:
			return "", errInvalidCanonicalResource
		}
	case apiops.ParameterBlockIdentifier:
		return canonicalBlockIdentifier(value)
	case apiops.ParameterEVMNetwork:
		if !canonicalEVMNetworkPattern.MatchString(value) {
			return "", errInvalidCanonicalResource
		}
		return value, nil
	case apiops.ParameterHash:
		canonical, ok := canonicalFixedHex(value, 32)
		if !ok {
			return "", errInvalidCanonicalResource
		}
		return canonical, nil
	case apiops.ParameterInteger:
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil ||
			parameter.HasMinimum && parsed < parameter.Minimum ||
			parameter.HasMaximum && parsed > parameter.Maximum {
			return "", errInvalidCanonicalResource
		}
		return strconv.FormatUint(parsed, 10), nil
	case apiops.ParameterOpaqueCursor, apiops.ParameterText:
		return value, nil
	case apiops.ParameterRFC3339:
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return "", errInvalidCanonicalResource
		}
		return parsed.UTC().Format(time.RFC3339Nano), nil
	case apiops.ParameterUint256:
		canonical, ok := canonicalUint256(value, false)
		if !ok || canonical != value {
			return "", errInvalidCanonicalResource
		}
		return canonical, nil
	case apiops.ParameterUUID:
		parsed, err := uuid.Parse(value)
		if err != nil {
			return "", errInvalidCanonicalResource
		}
		return parsed.String(), nil
	default:
		return "", errInvalidCanonicalResource
	}
}

func canonicalBlockIdentifier(value string) (string, error) {
	if hash, ok := canonicalFixedHex(value, 32); ok {
		return hash, nil
	}
	base := 10
	digits := value
	if after, ok := strings.CutPrefix(value, "0x"); ok {
		base, digits = 16, after
	}
	if digits == "" {
		return "", errInvalidCanonicalResource
	}
	number, err := strconv.ParseUint(digits, base, 64)
	if err != nil {
		return "", errInvalidCanonicalResource
	}
	return strconv.FormatUint(number, 10), nil
}

func canonicalFixedHex(value string, byteLength int) (string, bool) {
	if len(value) != 2+byteLength*2 || !strings.HasPrefix(value, "0x") {
		return "", false
	}
	for index := 2; index < len(value); index++ {
		character := value[index]
		if character < '0' || character > '9' &&
			(character < 'a' || character > 'f') &&
			(character < 'A' || character > 'F') {
			return "", false
		}
	}
	return strings.ToLower(value), true
}

func canonicalUint256(value string, positive bool) (string, bool) {
	if value == "" || len(value) > 1 && value[0] == '0' {
		return "", false
	}
	parsed, ok := new(big.Int).SetString(value, 10)
	if !ok || parsed.Sign() < 0 || positive && parsed.Sign() == 0 ||
		parsed.BitLen() > 256 {
		return "", false
	}
	return parsed.String(), true
}
