package verify

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestSourcifyImportV2LocalEvidenceAndHostileSources(t *testing.T) {
	address := "0x" + strings.Repeat("11", 20)
	target := VerificationTarget{ChainID: 1, Address: address, CodeHash: "0x" + hex.EncodeToString(keccak256Bytes([]byte{0x60, 1})), AtBlockHash: "0x" + strings.Repeat("33", 32), CreationBytecode: "0x6002", RuntimeBytecode: "0x6001"}
	for _, name := range []string{"solidity", "source chain", "source chain mismatch", "genesis", "yul", "vyper", "vyper commit", "vyper prerelease", "vyper malformed commit", "runtime mismatch", "unverified", "remote source URL", "wrong language", "wrong chain", "not found"} {
		t.Run(name, func(t *testing.T) {
			var body map[string]any
			_ = json.Unmarshal(sourcifyContractFixture(address), &body)
			bound := target
			sourceChain := uint64(1)
			switch name {
			case "source chain", "source chain mismatch":
				sourceChain = 9007199254740993
				body["chainId"] = strconv.FormatUint(sourceChain, 10)
				if name == "source chain mismatch" {
					body["runtimeBytecode"] = map[string]any{"onchainBytecode": "0x6003"}
				}
			case "genesis":
				bound.GenesisPredeploy = true
				bound.CreationBytecode = ""
			case "vyper", "vyper commit", "vyper prerelease", "vyper malformed commit":
				version := "0.4.3"
				switch name {
				case "vyper commit":
					version = "0.4.3+commit.bff19ea2"
				case "vyper prerelease":
					version = "0.4.3-rc.1"
				case "vyper malformed commit":
					version = "0.4.3+commit.nothex00"
				}
				body["compilation"] = map[string]any{"language": "Vyper", "compiler": "vyper", "compilerVersion": version, "fullyQualifiedName": "A.vy:A"}
				body["stdJsonInput"] = map[string]any{"language": "Vyper", "sources": map[string]any{"A.vy": map[string]any{"content": "@external\ndef value() -> uint256:\n    return 42"}}, "settings": map[string]any{}}
			case "yul":
				body["stdJsonInput"] = map[string]any{"language": "Yul", "sources": map[string]any{"A.sol": map[string]any{"content": "object \"A\" { code { stop() } }"}}, "settings": map[string]any{}}
			case "runtime mismatch":
				body["runtimeBytecode"] = map[string]any{"onchainBytecode": "0x6003"}
			case "unverified":
				body["runtimeMatch"] = nil
			case "remote source URL":
				body["stdJsonInput"].(map[string]any)["sources"] = map[string]any{"A.sol": map[string]any{"urls": []string{"https://secret.example/source"}}}
			case "wrong language":
				body["stdJsonInput"].(map[string]any)["language"] = "Vyper"
			case "wrong chain":
				body["chainId"] = "2"
			}
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet || r.URL.Path != "/v2/contract/"+strconv.FormatUint(sourceChain, 10)+"/"+address || r.URL.Query().Get("fields") != "all" {
					t.Error("import attempted publication or changed identity")
				}
				w.Header().Set("Content-Type", "application/json")
				if name == "not found" {
					w.WriteHeader(http.StatusNotFound)
				}
				_ = json.NewEncoder(w).Encode(body)
			}))
			defer server.Close()
			client, err := newSourcifyClient(SourcifyOptions{BaseURL: server.URL}, server.Client(), nil, true, true)
			if err != nil {
				t.Fatal(err)
			}
			request, err := client.ImportV2(t.Context(), sourceChain, bound)
			valid := name == "source chain" || name == "solidity" || name == "genesis" || name == "vyper" || name == "vyper commit" || name == "yul"
			if (err == nil) != valid || calls != 1 {
				t.Fatalf("valid=%t err=%v calls=%d", valid, err, calls)
			}
			if !valid {
				return
			}
			if request.Kind != JobAddress || request.Target == nil || *request.Target != bound || request.Bytecodes[0].Creation != bound.CreationBytecode || len(request.SourcifyRequest) != 0 {
				t.Fatal("import replaced canonical evidence or scheduled external publication")
			}
			repository := &sourcifyCaptureRepository{}
			service, _ := NewService(repository, 5<<20)
			if _, _, err := service.SubmitV2(t.Context(), request); err != nil {
				t.Fatalf("import could not enter local queue: %v", err)
			}
			if (name == "vyper" || name == "vyper commit") && (repository.stored[0].TargetFile != "A.vy" || repository.stored[0].CompilerVersion != "0.4.3") {
				t.Fatal("Vyper import lost target file")
			}
		})
	}
}
