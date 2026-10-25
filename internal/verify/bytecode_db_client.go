package verify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// BytecodeDBOptions bounds a search-only external source adapter.
type BytecodeDBOptions struct {
	UnsafeAllowPrivateNetworks bool
	BaseURL                    string
	Timeout                    time.Duration
	MaxResponseBytes           int64
	MaxInputBytes              int
}

type BytecodeDBClient struct {
	base    *url.URL
	http    *http.Client
	options BytecodeDBOptions
}

type BytecodeDBSource struct {
	FileName         string            `json:"fileName"`
	ContractName     string            `json:"contractName"`
	CompilerVersion  string            `json:"compilerVersion"`
	CompilerSettings string            `json:"compilerSettings"`
	SourceType       string            `json:"sourceType"`
	SourceFiles      map[string]string `json:"sourceFiles"`
	MatchType        string            `json:"matchType"`
}

type BytecodeDBError struct {
	Code       string
	RetryAfter time.Duration
}

func (e BytecodeDBError) Error() string { return "bytecode database " + e.Code }

func NewBytecodeDBClient(options BytecodeDBOptions) (*BytecodeDBClient, error) {
	base, err := url.Parse(options.BaseURL)
	if err != nil || base.Scheme != "https" || base.Hostname() == "" || base.User != nil || base.Opaque != "" || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || len(options.BaseURL) > 4096 || unsafeSourcifyPath(base.EscapedPath()) {
		return nil, errors.New("invalid bytecode database base URL")
	}
	if options.Timeout < 100*time.Millisecond || options.Timeout > 2*time.Minute || options.MaxResponseBytes < 1 || options.MaxResponseBytes > 64<<20 || options.MaxInputBytes < 1 || options.MaxInputBytes > 64<<20 {
		return nil, errors.New("invalid bytecode database bounds")
	}
	return &BytecodeDBClient{base: base, http: restrictedOutboundClient(nil, options.Timeout, nil, options.UnsafeAllowPrivateNetworks), options: options}, nil
}

func (c *BytecodeDBClient) Identity() []byte {
	sum := sha256.Sum256([]byte(strings.TrimRight(c.base.String(), "/")))
	return sum[:]
}

// Search never invokes a verification or source-upload endpoint.
func (c *BytecodeDBClient) Search(ctx context.Context, code []byte, kind string) ([]BytecodeDBSource, error) {
	if len(code) == 0 || len(code) > c.options.MaxInputBytes || (kind != "DEPLOYED_BYTECODE" && kind != "CREATION_INPUT") {
		return nil, BytecodeDBError{Code: "invalid_request"}
	}
	payload, _ := json.Marshal(struct {
		Bytecode     string `json:"bytecode"`
		BytecodeType string `json:"bytecodeType"`
	}{"0x" + hex.EncodeToString(code), kind})
	endpoint := *c.base
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/api/v2/bytecodes/sources:search"
	endpoint.RawPath = ""
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, BytecodeDBError{Code: "invalid_request"}
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, BytecodeDBError{Code: "unavailable"}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		code := "rejected"
		if response.StatusCode == 429 {
			code = "rate_limited"
		} else if response.StatusCode >= 500 {
			code = "unavailable"
		}
		delay := time.Duration(0)
		if seconds, e := strconv.ParseInt(response.Header.Get("Retry-After"), 10, 32); e == nil {
			delay = time.Duration(seconds) * time.Second
		} else if until, e := http.ParseTime(response.Header.Get("Retry-After")); e == nil {
			delay = time.Until(until)
		}
		return nil, BytecodeDBError{Code: code, RetryAfter: min(max(delay, 0), 24*time.Hour)}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, c.options.MaxResponseBytes+1))
	if err != nil {
		return nil, BytecodeDBError{Code: "unavailable"}
	}
	if int64(len(raw)) > c.options.MaxResponseBytes {
		return nil, BytecodeDBError{Code: "response_too_large"}
	}
	// The strict decoder rejects duplicate keys and bounds depth before typed decoding.
	if _, err = decodeStandardJSONObject(raw); err != nil {
		return nil, BytecodeDBError{Code: "invalid_response"}
	}
	var result struct {
		Sources []BytecodeDBSource `json:"sources"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Sources == nil {
		return nil, BytecodeDBError{Code: "invalid_response"}
	}
	return result.Sources, nil
}

func bytecodeDBSourceDigest(source BytecodeDBSource) []byte {
	// Provider match labels are ranking hints, not part of compilation identity.
	source.MatchType = ""
	raw, _ := json.Marshal(source)
	sum := sha256.Sum256(raw)
	return sum[:]
}

func bytecodeDBCandidates(sources []BytecodeDBSource) ([]BytecodeDBSource, bool) {
	slices.SortFunc(sources, func(a, b BytecodeDBSource) int {
		if (a.MatchType == "FULL") != (b.MatchType == "FULL") {
			if a.MatchType == "FULL" {
				return -1
			}
			return 1
		}
		if cmp := strings.Compare(a.FileName, b.FileName); cmp != 0 {
			return cmp
		}
		if cmp := strings.Compare(a.ContractName, b.ContractName); cmp != 0 {
			return cmp
		}
		return bytes.Compare(bytecodeDBSourceDigest(a), bytecodeDBSourceDigest(b))
	})
	seen := map[string]bool{}
	result := make([]BytecodeDBSource, 0, 5)
	limited := false
	for _, source := range sources {
		key := string(bytecodeDBSourceDigest(source))
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(result) == 5 {
			limited = true
			continue
		}
		result = append(result, source)
	}
	return result, limited
}

// Provider commit labels only select a candidate release. Compiler evidence
// always comes from the application lock and local bytecode verification.
var bytecodeDBVyperVersionPattern = regexp.MustCompile(`^v?([0-9]+\.[0-9]+\.[0-9]+)(?:\+commit\.[0-9a-fA-F]{7,64})?$`)

func bytecodeDBSubmission(source BytecodeDBSource, target VerificationTarget, maximum int) (SubmissionV2, error) {
	request := SubmissionV2{Kind: JobAddress, CompilerVersion: source.CompilerVersion, Target: &target, Bytecodes: []BytecodePair{{Creation: target.CreationBytecode, Runtime: target.RuntimeBytecode}}}
	switch source.SourceType {
	case "SOLIDITY":
		request.Language = LanguageSolidity
	case "YUL":
		request.Language = LanguageYul
	case "VYPER":
		version := bytecodeDBVyperVersionPattern.FindStringSubmatch(source.CompilerVersion)
		if len(version) != 2 || !stableVyperVersion(version[1]) {
			return SubmissionV2{}, BytecodeDBError{Code: "unsupported_version"}
		}
		request.CompilerVersion = version[1]
		request.Language = LanguageVyper
		request.TargetFile = source.FileName
	default:
		return SubmissionV2{}, BytecodeDBError{Code: "unsupported_language"}
	}
	if !validGeasSourcePath(source.FileName) || source.ContractName == "" || len(source.ContractName) > 256 || len(source.SourceFiles) == 0 || len(source.SourceFiles) > maxStandardJSONSources {
		return SubmissionV2{}, BytecodeDBError{Code: "invalid_candidate"}
	}
	sources := map[string]any{}
	size := len(source.CompilerSettings)
	for name, content := range source.SourceFiles {
		size += len(name) + len(content)
		if size > maximum || !validGeasSourcePath(name) {
			return SubmissionV2{}, BytecodeDBError{Code: "invalid_candidate"}
		}
		sources[name] = map[string]string{"content": content}
	}
	if _, ok := sources[source.FileName]; !ok {
		return SubmissionV2{}, BytecodeDBError{Code: "invalid_candidate"}
	}
	settings, err := decodeStandardJSONObject([]byte(source.CompilerSettings))
	if err != nil {
		return SubmissionV2{}, BytecodeDBError{Code: "invalid_candidate"}
	}
	language := string(request.Language)
	switch request.Language {
	case LanguageSolidity:
		language = "Solidity"
	case LanguageYul:
		language = "Yul"
	case LanguageVyper:
		language = "Vyper"
	}
	request.StandardJSON, err = json.Marshal(map[string]any{"language": language, "sources": sources, "settings": settings})
	if err != nil || len(request.StandardJSON) > maximum {
		return SubmissionV2{}, BytecodeDBError{Code: "invalid_candidate"}
	}
	request.ContractNameHint = source.FileName + ":" + source.ContractName
	return request, nil
}
