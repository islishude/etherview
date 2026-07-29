//go:build hardhat3verify

package hardhat3_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/etherscan"
)

const (
	hardhat3ChainID = uint64(123456)
	hardhat3Address = "0x1234567890123456789012345678901234567890"
	hardhat3GUID    = "123e4567-e89b-42d3-a456-426614174000"
)

type hardhat3Backend struct {
	mu       sync.Mutex
	methods  []string
	requests []etherscan.Request
	polls    int
}

func (b *hardhat3Backend) recordMethod(method string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.methods = append(b.methods, method)
}

func (b *hardhat3Backend) Execute(_ context.Context, request etherscan.Request) (any, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	request.Values = cloneValues(request.Values)
	b.requests = append(b.requests, request)

	switch request.Module + "." + request.Action {
	case "contract.getsourcecode":
		return nil, etherscan.ErrContractUnverified
	case "contract.verifysourcecode":
		return hardhat3GUID, nil
	case "contract.checkverifystatus":
		b.polls++
		if b.polls == 1 {
			return nil, etherscan.ErrPending
		}
		return "Pass - Verified", nil
	default:
		return nil, errors.New("unexpected Hardhat 3 compatibility request")
	}
}

func (b *hardhat3Backend) snapshot() ([]string, []etherscan.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	methods := append([]string(nil), b.methods...)
	requests := make([]etherscan.Request, len(b.requests))
	for index, request := range b.requests {
		request.Values = cloneValues(request.Values)
		requests[index] = request
	}
	return methods, requests
}

func TestHardhat3EtherscanProviderVerificationFlow(t *testing.T) {
	manager := auth.Manager{
		Repository:                    auth.NewMemoryRepository(),
		Pepper:                        bytes.Repeat([]byte{3}, 32),
		MaxCompatibilityFormBodyBytes: 1 << 20,
	}
	issued, err := manager.Create(context.Background(), "hardhat-3-provider", 100, 100)
	if err != nil {
		t.Fatal(err)
	}

	backend := &hardhat3Backend{}
	compatibility := manager.Middleware(false, etherscan.Handler{
		ChainID: hardhat3ChainID,
		Backend: backend,
		MaxBody: 1 << 20,
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backend.recordMethod(r.Method)
		compatibility.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	node := os.Getenv("ETHERVIEW_HARDHAT3_NODE")
	if node == "" {
		node = "node"
	}
	commandContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(commandContext, node, "provider.mjs")
	command.Dir = fixtureDirectory(t)
	command.Env = append(os.Environ(),
		"ETHERVIEW_HARDHAT3_BASE_URL="+server.URL,
		"ETHERVIEW_HARDHAT3_API_KEY="+issued.Token,
		"ETHERVIEW_HARDHAT3_CHAIN_ID="+strconv.FormatUint(hardhat3ChainID, 10),
		"ETHERVIEW_HARDHAT3_ADDRESS="+hardhat3Address,
		"ETHERVIEW_HARDHAT3_GUID="+hardhat3GUID,
		"NO_PROXY=127.0.0.1,localhost",
		"no_proxy=127.0.0.1,localhost",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		if errors.Is(commandContext.Err(), context.DeadlineExceeded) {
			t.Fatalf("Hardhat 3 provider timed out")
		}
		t.Fatalf("Hardhat 3 provider failed: %v\n%s", err, output)
	}

	methods, requests := backend.snapshot()
	if want := []string{http.MethodGet, http.MethodPost, http.MethodGet, http.MethodGet}; !reflect.DeepEqual(methods, want) {
		t.Fatalf("HTTP methods=%v, want %v", methods, want)
	}
	if len(requests) != 4 {
		t.Fatalf("backend requests=%d, want 4", len(requests))
	}
	assertRequest(t, requests[0], "getsourcecode", url.Values{
		"address": {hardhat3Address},
	})
	assertRequest(t, requests[1], "verifysourcecode", url.Values{
		"contractaddress":      {hardhat3Address},
		"codeformat":           {"solidity-standard-json-input"},
		"contractname":         {"contracts/Hardhat3Compatibility.sol:Hardhat3Compatibility"},
		"compilerversion":      {"v0.8.30+commit.73712a01"},
		"constructorArguments": {"1234"},
	})
	var compilerInput struct {
		Language string `json:"language"`
		Sources  map[string]struct {
			Content string `json:"content"`
		} `json:"sources"`
	}
	if err := json.Unmarshal([]byte(requests[1].Values.Get("sourceCode")), &compilerInput); err != nil {
		t.Fatalf("decode Hardhat 3 Standard JSON: %v", err)
	}
	if compilerInput.Language != "Solidity" ||
		compilerInput.Sources["contracts/Hardhat3Compatibility.sol"].Content == "" {
		t.Fatalf("unexpected Hardhat 3 compiler input: %#v", compilerInput)
	}
	for _, request := range requests[2:] {
		assertRequest(t, request, "checkverifystatus", url.Values{"guid": {hardhat3GUID}})
	}
}

func fixtureDirectory(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(directory, "package.json")
	if _, err := os.Stat(manifest); err != nil {
		t.Fatalf("locate Hardhat 3 fixture: %v", err)
	}
	return directory
}

func assertRequest(t *testing.T, request etherscan.Request, action string, expected url.Values) {
	t.Helper()
	if request.Module != "contract" || request.Action != action {
		t.Fatalf("request=%#v, want contract.%s", request, action)
	}
	if request.Values.Get("chainid") != strconv.FormatUint(hardhat3ChainID, 10) {
		t.Fatalf("%s chainid=%q", action, request.Values.Get("chainid"))
	}
	if request.Values.Get("apikey") != "" {
		t.Fatalf("%s leaked API key to backend", action)
	}
	for name, values := range expected {
		if got := request.Values[name]; !reflect.DeepEqual(got, values) {
			t.Fatalf("%s %s=%v, want %v", action, name, got, values)
		}
	}
}

func cloneValues(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for name, entries := range values {
		cloned[name] = append([]string(nil), entries...)
	}
	return cloned
}
