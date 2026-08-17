//go:build previewmetadatae2e

package previewmetadatae2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/netpolicy"
	"github.com/islishude/etherview/internal/testcompose"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	previewTimeout          = 10 * time.Minute
	waitTimeout             = 3 * time.Minute
	previewChainID          = "48815"
	previewChainIDHex       = "0xbeaf"
	metadataTokenID         = "1"
	metadataGateway         = "https://ipfs.io"
	metadataURI             = "ipfs://bafybeibnsoufr2renqzsh347nrx54wcubt5lgkeivez63xvivplfwhtpym/metadata.json"
	resolvedMetadataURL     = "https://ipfs.io/ipfs/bafybeibnsoufr2renqzsh347nrx54wcubt5lgkeivez63xvivplfwhtpym/metadata.json"
	resolvedImageURL        = "https://ipfs.io/ipfs/bafybeidfjqmasnpu6z7gvn7l6wthdcyzxh5uystkky3xvutddbapchbopi/no-time-to-explain.jpeg"
	expectedMetadataSHA256  = "a87d3d327d1a2c7f839000c080e07cd152b49ddf653f1a5afa5144eeec103d8d"
	expectedMetadataBytes   = 205
	previewDatabasePassword = "etherview-preview-metadata-e2e"

	// PreviewMetadataNFT.sol is compiled once with the repository's locked
	// solc 0.8.30, optimizer runs=200, evmVersion=prague, and compiler metadata
	// disabled. The live gate deploys this reviewed artifact without invoking a
	// compiler, Hardhat, ethers, or cast.
	previewNFTCreationBytecode = "0x60a0604052348015600e575f5ffd5b50336080819052604051600191905f907fddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef908290a460805161028561005b5f395f61013501526102855ff3fe608060405234801561000f575f5ffd5b506004361061003f575f3560e01c806301ffc9a7146100435780636352211e1461006b578063c87b56dd14610096575b5f5ffd5b6100566100513660046101ba565b6100b6565b60405190151581526020015b60405180910390f35b61007e6100793660046101e8565b6100ec565b6040516001600160a01b039091168152602001610062565b6100a96100a43660046101e8565b610159565b60405161006291906101ff565b5f6301ffc9a760e01b6001600160e01b0319831614806100e657506380ac58cd60e01b6001600160e01b03198316145b92915050565b5f816001146101325760405162461bcd60e51b815260206004820152600d60248201526c36b4b9b9b4b733903a37b5b2b760991b60448201526064015b60405180910390fd5b507f0000000000000000000000000000000000000000000000000000000000000000919050565b60608160011461019b5760405162461bcd60e51b815260206004820152600d60248201526c36b4b9b9b4b733903a37b5b2b760991b6044820152606401610129565b6040518060800160405280605081526020016102356050913992915050565b5f602082840312156101ca575f5ffd5b81356001600160e01b0319811681146101e1575f5ffd5b9392505050565b5f602082840312156101f8575f5ffd5b5035919050565b602081525f82518060208401528060208501604085015e5f604082850101526040601f19601f8301168401019150509291505056fe697066733a2f2f62616679626569626e736f7566723272656e717a73683334376e727835347763756274356c676b656976657a363378766976706c6677687470796d2f6d657461646174612e6a736f6e"
)

//go:embed testdata/metadata.json
var expectedMetadata []byte

type harness struct {
	t         *testing.T
	root      string
	project   *testcompose.Project
	rpc       *rpc.Client
	db        *pgxpool.Pool
	http      *http.Client
	apiURL    string
	artifacts string
	image     string
	succeeded bool
}

type rpcReceipt struct {
	TransactionHash common.Hash    `json:"transactionHash"`
	BlockHash       common.Hash    `json:"blockHash"`
	BlockNumber     hexutil.Uint64 `json:"blockNumber"`
	ContractAddress common.Address `json:"contractAddress"`
	Status          hexutil.Uint64 `json:"status"`
}

type metadataSnapshot struct {
	State        string
	ResolvedURI  string
	MediaType    string
	ContentHash  string
	ContentSize  int64
	Document     string
	AttemptCount int
	FetchedAt    time.Time
}

type jobSnapshot struct {
	ID          int64
	Status      string
	Attempts    int
	MaxAttempts int
	Result      string
}

type metadataTransitionLog struct {
	Message string `json:"msg"`
	Result  string `json:"result"`
	Job     struct {
		ID          int64  `json:"id"`
		Attempt     uint64 `json:"attempt"`
		MaxAttempts uint64 `json:"max_attempts"`
	} `json:"job"`
	NFT struct {
		Contract string `json:"contract"`
		ID       string `json:"id"`
	} `json:"nft"`
	Block struct {
		Number string `json:"number"`
		Hash   string `json:"hash"`
	} `json:"block"`
	Transition struct {
		State string `json:"state"`
		Code  string `json:"code"`
	} `json:"transition"`
	Source struct {
		Scheme string `json:"scheme"`
	} `json:"source"`
	Request struct {
		Scheme    string `json:"scheme"`
		Host      string `json:"host"`
		Path      string `json:"path"`
		Redirects int    `json:"redirects"`
	} `json:"request"`
	Network struct {
		ResolvedIPs      []string `json:"resolved_ips"`
		ConnectedIP      string   `json:"connected_ip"`
		RejectedIPs      []string `json:"rejected_ips"`
		RejectedReasons  []string `json:"rejected_reasons"`
		RejectedPrefixes []string `json:"rejected_prefixes"`
		PolicyBypassed   bool     `json:"policy_bypassed"`
	} `json:"network"`
}

func TestPreviewPublicNFTMetadata(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), previewTimeout)
	defer cancel()
	h := newHarness(t)
	t.Cleanup(h.cleanup)
	h.run(ctx)
	h.succeeded = true
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root := repositoryRoot(t)
	artifacts, err := os.MkdirTemp("", "etherview-preview-metadata-")
	if err != nil {
		t.Fatal(err)
	}
	image := valueOrDefault("IMAGE", "etherview:local")
	genesis := filepath.Join(root, ".local", "preview-genesis.json")
	for _, required := range []string{
		genesis,
		filepath.Join(root, ".local", "preview-tls", "tls.crt"),
		filepath.Join(root, ".local", "preview-tls", "tls.key"),
		filepath.Join(root, ".local", "preview-tls", "rootCA.pem"),
	} {
		if info, statErr := os.Stat(required); statErr != nil || !info.Mode().IsRegular() {
			t.Fatalf("required Preview runtime file %s is unavailable; run the Make target: %v", required, statErr)
		}
	}
	project := testcompose.NewQuiet(
		root,
		testcompose.UniqueProjectName("etherview-preview-metadata"),
		"compose.preview.yaml",
		"e2e/previewmetadata/compose.yaml",
	)
	project.Env = map[string]string{
		"ETHERVIEW_IMAGE":                 image,
		"ETHERVIEW_CONFIG_FILE":           filepath.Join(root, "deploy", "preview.config.yaml"),
		"ETHERVIEW_GENESIS_FILE":          genesis,
		"GETH_GENESIS_FILE":               genesis,
		"ETHERVIEW_METADATA_IPFS_GATEWAY": metadataGateway,
		"ETHERVIEW_PORT":                  "0",
		"ETHERVIEW_METRICS_PORT":          "0",
		"GETH_HTTP_PORT":                  "0",
		"GETH_WS_PORT":                    "0",
		"POSTGRES_PASSWORD":               previewDatabasePassword,
	}
	return &harness{t: t, root: root, project: project, artifacts: artifacts, image: image}
}

func (h *harness) run(ctx context.Context) {
	h.validateFixture()
	if _, err := h.project.Run(
		ctx, "up", "-d", "--no-build", "--wait", "--wait-timeout", "180", "--remove-orphans",
	); err != nil {
		h.t.Fatal(err)
	}
	h.connect(ctx)
	h.assertPublicConfig(ctx)
	receipt := h.deployNFT(ctx)
	h.waitTokenContract(ctx, receipt)
	h.waitSourceObservation(ctx, receipt)
	metadata := h.waitMetadata(ctx, receipt)
	h.assertMetadataAPI(ctx, receipt)
	job := h.waitJob(ctx, receipt)
	h.assertAttempt(ctx, job.ID)
	transition := h.assertTransitionLog(ctx, receipt, job)
	h.restartMetadataAndAssertPersistence(ctx, receipt, metadata, job)
	h.writeReport(ctx, receipt, metadata, job, transition)
}

func (h *harness) validateFixture() {
	h.t.Helper()
	if len(expectedMetadata) != expectedMetadataBytes {
		h.t.Fatalf("metadata fixture bytes = %d, want %d", len(expectedMetadata), expectedMetadataBytes)
	}
	digest := sha256.Sum256(expectedMetadata)
	if got := hex.EncodeToString(digest[:]); got != expectedMetadataSHA256 {
		h.t.Fatalf("metadata fixture SHA-256 = %s, want %s", got, expectedMetadataSHA256)
	}
	if len(common.FromHex(previewNFTCreationBytecode)) == 0 {
		h.t.Fatal("Preview NFT creation bytecode is empty or malformed")
	}
}

func (h *harness) connect(ctx context.Context) {
	h.t.Helper()
	rpcBinding, err := h.project.Port(ctx, "geth", 8545)
	if err != nil {
		h.t.Fatal(err)
	}
	h.rpc, err = rpc.DialContext(ctx, "http://"+rpcBinding)
	if err != nil {
		h.t.Fatal(err)
	}
	postgresBinding, err := h.project.Port(ctx, "postgres", 5432)
	if err != nil {
		h.t.Fatal(err)
	}
	databaseURL := "postgres://etherview:" + previewDatabasePassword + "@" + postgresBinding + "/etherview?sslmode=disable"
	h.db, err = pgxpool.New(ctx, databaseURL)
	if err != nil {
		h.t.Fatal(err)
	}
	waitFor(h.t, ctx, "Preview PostgreSQL connection", func() (bool, string, error) {
		err := h.db.Ping(ctx)
		return err == nil, fmt.Sprint(err), err
	})
	apiBinding, err := h.project.Port(ctx, "api", 8080)
	if err != nil {
		h.t.Fatal(err)
	}
	roots := x509.NewCertPool()
	ca, err := os.ReadFile(filepath.Join(h.root, ".local", "preview-tls", "rootCA.pem"))
	if err != nil || !roots.AppendCertsFromPEM(ca) {
		h.t.Fatalf("load Preview TLS root: %v", err)
	}
	h.http = &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    roots,
		}},
	}
	h.apiURL = "https://" + apiBinding
}

func (h *harness) assertPublicConfig(ctx context.Context) {
	h.t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.apiURL+"/api/v1/config", nil)
	if err != nil {
		h.t.Fatal(err)
	}
	response, err := h.http.Do(request)
	if err != nil {
		h.t.Fatal(err)
	}
	defer response.Body.Close() //nolint:errcheck
	if response.StatusCode != http.StatusOK {
		h.t.Fatalf("Preview config status = %d", response.StatusCode)
	}
	var payload struct {
		Data struct {
			Features map[string]bool `json:"features"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		h.t.Fatal(err)
	}
	for _, feature := range []string{"verification", "nft_metadata"} {
		if !payload.Data.Features[feature] {
			h.t.Fatalf("Preview public config does not enable %s", feature)
		}
	}
}

func (h *harness) deployNFT(ctx context.Context) rpcReceipt {
	h.t.Helper()
	var chainID string
	h.rpcCall(ctx, &chainID, "eth_chainId")
	if chainID != previewChainIDHex {
		h.t.Fatalf("Preview chain ID = %s, want %s", chainID, previewChainIDHex)
	}
	var accounts []common.Address
	h.rpcCall(ctx, &accounts, "eth_accounts")
	if len(accounts) == 0 || accounts[0] == (common.Address{}) {
		h.t.Fatal("Preview Geth exposes no unlocked development account")
	}
	developer := accounts[0]
	var balance hexutil.Big
	h.rpcCall(ctx, &balance, "eth_getBalance", developer, "latest")
	if (*big.Int)(&balance).Sign() <= 0 {
		h.t.Fatalf("Preview unlocked account %s has no balance", developer)
	}
	var hash common.Hash
	h.rpcCall(ctx, &hash, "eth_sendTransaction", map[string]any{
		"from": developer, "data": previewNFTCreationBytecode,
		"gas": "0x2dc6c0", "gasPrice": "0x3b9aca00",
	})
	if hash == (common.Hash{}) {
		h.t.Fatal("Preview NFT deployment returned an empty transaction hash")
	}
	var receipt rpcReceipt
	waitFor(h.t, ctx, "Preview NFT deployment receipt", func() (bool, string, error) {
		err := h.rpc.CallContext(ctx, &receipt, "eth_getTransactionReceipt", hash)
		if err != nil {
			return false, err.Error(), err
		}
		return receipt.TransactionHash != (common.Hash{}), receipt.TransactionHash.Hex(), nil
	})
	if uint64(receipt.Status) != 1 || receipt.ContractAddress == (common.Address{}) ||
		receipt.BlockHash == (common.Hash{}) || uint64(receipt.BlockNumber) == 0 {
		h.t.Fatalf("Preview NFT deployment receipt = %#v", receipt)
	}
	return receipt
}

func (h *harness) waitTokenContract(ctx context.Context, receipt rpcReceipt) {
	h.t.Helper()
	waitFor(h.t, ctx, "exact ERC-721 contract observation", func() (bool, string, error) {
		var standard, confidence, number, hash string
		err := h.db.QueryRow(ctx, `
			SELECT standard, confidence, observed_block_number::text,
			       '0x' || encode(observed_block_hash, 'hex')
			FROM token_contracts
			WHERE chain_id = $1::numeric AND address = $2
			  AND observed_block_hash = $3
			ORDER BY updated_at DESC LIMIT 1`,
			previewChainID, receipt.ContractAddress.Bytes(), receipt.BlockHash.Bytes(),
		).Scan(&standard, &confidence, &number, &hash)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, "not observed", nil
		}
		if err != nil {
			return false, err.Error(), err
		}
		wantNumber := strconv.FormatUint(uint64(receipt.BlockNumber), 10)
		if standard != "erc721" || confidence != "high" || number != wantNumber ||
			!strings.EqualFold(hash, receipt.BlockHash.Hex()) {
			return false, fmt.Sprintf("%s:%s:%s:%s", standard, confidence, number, hash), nil
		}
		return true, "erc721:high", nil
	})
}

func (h *harness) waitSourceObservation(ctx context.Context, receipt rpcReceipt) {
	h.t.Helper()
	waitFor(h.t, ctx, "exact NFT metadata source observation", func() (bool, string, error) {
		var standard, state, sourceURI, number, hash string
		err := h.db.QueryRow(ctx, `
			SELECT standard, state, source_uri, block_number::text,
			       '0x' || encode(block_hash, 'hex')
			FROM nft_metadata_source_observations
			WHERE chain_id = $1::numeric AND token_address = $2
			  AND token_id = $3::numeric AND block_hash = $4`,
			previewChainID, receipt.ContractAddress.Bytes(), metadataTokenID, receipt.BlockHash.Bytes(),
		).Scan(&standard, &state, &sourceURI, &number, &hash)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, "not observed", nil
		}
		if err != nil {
			return false, err.Error(), err
		}
		wantNumber := strconv.FormatUint(uint64(receipt.BlockNumber), 10)
		if standard != "erc721" || state != "found" || sourceURI != metadataURI ||
			number != wantNumber || !strings.EqualFold(hash, receipt.BlockHash.Hex()) {
			return false, fmt.Sprintf("%s:%s:%s:%s", standard, state, number, hash), nil
		}
		return true, "found", nil
	})
}

func (h *harness) waitMetadata(ctx context.Context, receipt rpcReceipt) metadataSnapshot {
	h.t.Helper()
	var snapshot metadataSnapshot
	waitFor(h.t, ctx, "available exact NFT metadata", func() (bool, string, error) {
		err := h.db.QueryRow(ctx, `
			SELECT state, COALESCE(resolved_uri, ''), COALESCE(media_type, ''),
			       COALESCE(encode(content_hash, 'hex'), ''), COALESCE(content_size, 0),
			       COALESCE(document::text, 'null'), attempt_count,
			       COALESCE(fetched_at, 'epoch'::timestamptz)
			FROM external_metadata
			WHERE chain_id = $1::numeric AND resource_kind = 'nft'
			  AND token_address = $2 AND token_id = $3::numeric
			  AND observed_block_hash = $4`,
			previewChainID, receipt.ContractAddress.Bytes(), metadataTokenID, receipt.BlockHash.Bytes(),
		).Scan(
			&snapshot.State, &snapshot.ResolvedURI, &snapshot.MediaType, &snapshot.ContentHash,
			&snapshot.ContentSize, &snapshot.Document, &snapshot.AttemptCount, &snapshot.FetchedAt,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, "not created", nil
		}
		if err != nil {
			return false, err.Error(), err
		}
		if snapshot.State == "unsafe" || snapshot.State == "unavailable" || snapshot.State == "error" {
			return false, snapshot.State, fmt.Errorf("metadata reached terminal state %s", snapshot.State)
		}
		if snapshot.State != "available" {
			return false, snapshot.State, nil
		}
		return true, snapshot.ContentHash, nil
	})
	if snapshot.ResolvedURI != resolvedMetadataURL || snapshot.MediaType != "application/json" ||
		snapshot.ContentHash != expectedMetadataSHA256 || snapshot.ContentSize != expectedMetadataBytes ||
		snapshot.AttemptCount != 1 {
		h.t.Fatalf("metadata snapshot = %#v", snapshot)
	}
	var expectedValue, storedValue any
	if err := json.Unmarshal(expectedMetadata, &expectedValue); err != nil {
		h.t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(snapshot.Document), &storedValue); err != nil {
		h.t.Fatal(err)
	}
	if !reflect.DeepEqual(expectedValue, storedValue) {
		h.t.Fatalf("stored metadata document = %s", snapshot.Document)
	}
	return snapshot
}

func (h *harness) assertMetadataAPI(ctx context.Context, receipt rpcReceipt) {
	h.t.Helper()
	path := fmt.Sprintf(
		"/api/v1/nfts/%s/%s/metadata",
		receipt.ContractAddress.Hex(),
		metadataTokenID,
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.apiURL+path, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	response, err := h.http.Do(request)
	if err != nil {
		h.t.Fatal(err)
	}
	defer response.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(response.Body)
	if err != nil {
		h.t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
		h.t.Fatalf("metadata API status=%d cache=%q body=%s", response.StatusCode, response.Header.Get("Cache-Control"), body)
	}
	if bytes.Contains(body, []byte(metadataURI)) || bytes.Contains(body, []byte(resolvedMetadataURL)) ||
		bytes.Contains(body, []byte(`"animation_url"`)) || bytes.Contains(body, []byte(`"external_url"`)) {
		h.t.Fatalf("metadata API exposed a forbidden field: %s", body)
	}
	var payload struct {
		Data struct {
			ChainID               string `json:"chain_id"`
			TokenAddress          string `json:"token_address"`
			TokenID               string `json:"token_id"`
			State                 string `json:"state"`
			Name                  string `json:"name"`
			Description           string `json:"description"`
			NameTruncated         bool   `json:"name_truncated"`
			DescriptionTruncated  bool   `json:"description_truncated"`
			Attributes            []any  `json:"attributes"`
			OmittedAttributeCount int    `json:"omitted_attribute_count"`
			Observation           struct {
				ChainID     string `json:"chain_id"`
				BlockNumber string `json:"block_number"`
				BlockHash   string `json:"block_hash"`
			} `json:"observation"`
			Image struct {
				State        string `json:"state"`
				URL          string `json:"url"`
				SourceScheme string `json:"source_scheme"`
			} `json:"image"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		h.t.Fatal(err)
	}
	data := payload.Data
	if data.ChainID != previewChainID || !strings.EqualFold(data.TokenAddress, receipt.ContractAddress.Hex()) ||
		data.TokenID != metadataTokenID || data.State != "available" || data.Name != "No time to explain!" ||
		data.Description != "I said there was no time to explain, and I stand by that." ||
		data.NameTruncated || data.DescriptionTruncated || len(data.Attributes) != 0 || data.OmittedAttributeCount != 0 ||
		data.Observation.ChainID != previewChainID ||
		data.Observation.BlockNumber != strconv.FormatUint(uint64(receipt.BlockNumber), 10) ||
		!strings.EqualFold(data.Observation.BlockHash, receipt.BlockHash.Hex()) ||
		data.Image.State != "available" || data.Image.URL != resolvedImageURL || data.Image.SourceScheme != "ipfs" {
		h.t.Fatalf("metadata API payload = %#v", data)
	}

	mediaRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		h.apiURL+strings.TrimSuffix(path, "/metadata")+"/media",
		nil,
	)
	if err != nil {
		h.t.Fatal(err)
	}
	mediaResponse, err := h.http.Do(mediaRequest)
	if err != nil {
		h.t.Fatal(err)
	}
	defer mediaResponse.Body.Close() //nolint:errcheck
	mediaBody, err := io.ReadAll(mediaResponse.Body)
	if err != nil {
		h.t.Fatal(err)
	}
	if mediaResponse.StatusCode != http.StatusUnauthorized || !bytes.Contains(mediaBody, []byte(`"code":"api_key_required"`)) {
		h.t.Fatalf("anonymous media status=%d body=%s", mediaResponse.StatusCode, mediaBody)
	}
}

func (h *harness) waitJob(ctx context.Context, receipt rpcReceipt) jobSnapshot {
	h.t.Helper()
	var snapshot jobSnapshot
	waitFor(h.t, ctx, "successful durable metadata job", func() (bool, string, error) {
		err := h.db.QueryRow(ctx, `
			SELECT id, status, attempts, max_attempts, result::text
			FROM durable_jobs
			WHERE chain_id = $1::numeric AND kind = 'metadata'
			  AND stage = 'nft-metadata' AND stage_version = 1
			  AND payload->>'token_address' = $2
			  AND payload->>'token_id' = $3
			  AND payload->>'block_hash' = $4`,
			previewChainID, strings.ToLower(receipt.ContractAddress.Hex()), metadataTokenID,
			strings.ToLower(receipt.BlockHash.Hex()),
		).Scan(&snapshot.ID, &snapshot.Status, &snapshot.Attempts, &snapshot.MaxAttempts, &snapshot.Result)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, "not created", nil
		}
		if err != nil {
			return false, err.Error(), err
		}
		if snapshot.Status == "failed" || snapshot.Status == "cancelled" {
			return false, snapshot.Status, fmt.Errorf("metadata job reached terminal status %s", snapshot.Status)
		}
		return snapshot.Status == "succeeded", snapshot.Status, nil
	})
	if snapshot.Attempts != 1 || snapshot.MaxAttempts != 5 {
		h.t.Fatalf("metadata job attempts = %d/%d", snapshot.Attempts, snapshot.MaxAttempts)
	}
	var result struct {
		State       string `json:"state"`
		Code        string `json:"code"`
		ContentHash string `json:"content_hash"`
		ContentSize int64  `json:"content_size"`
	}
	if err := json.Unmarshal([]byte(snapshot.Result), &result); err != nil {
		h.t.Fatal(err)
	}
	if result.State != "available" || result.Code != "" ||
		result.ContentHash != "0x"+expectedMetadataSHA256 || result.ContentSize != expectedMetadataBytes {
		h.t.Fatalf("metadata job result = %#v", result)
	}
	return snapshot
}

func (h *harness) assertAttempt(ctx context.Context, jobID int64) {
	h.t.Helper()
	var count int
	var state, contentHash string
	var size int64
	if err := h.db.QueryRow(ctx, `
		SELECT count(*)::int, min(state), min(encode(content_hash, 'hex')), min(content_size)
		FROM external_metadata_attempts
		WHERE durable_job_id = $1`, jobID,
	).Scan(&count, &state, &contentHash, &size); err != nil {
		h.t.Fatal(err)
	}
	if count != 1 || state != "available" || contentHash != expectedMetadataSHA256 || size != expectedMetadataBytes {
		h.t.Fatalf("metadata attempts = %d:%s:%s:%d", count, state, contentHash, size)
	}
}

func (h *harness) assertTransitionLog(
	ctx context.Context,
	receipt rpcReceipt,
	job jobSnapshot,
) metadataTransitionLog {
	h.t.Helper()
	output, err := h.project.Run(ctx, "logs", "--no-color", "metadata")
	if err != nil {
		h.t.Fatal(err)
	}
	logs := string(output)
	if strings.Contains(logs, metadataURI) || strings.Contains(logs, resolvedMetadataURL) {
		h.t.Fatal("metadata logs exposed a complete source or resolved URI")
	}
	var matches []metadataTransitionLog
	for line := range strings.SplitSeq(logs, "\n") {
		start := strings.IndexByte(line, '{')
		if start < 0 {
			continue
		}
		var record metadataTransitionLog
		if err := json.Unmarshal([]byte(line[start:]), &record); err != nil {
			continue
		}
		if record.Message == "metadata fetch transitioned" && record.Job.ID == job.ID &&
			strings.EqualFold(record.NFT.Contract, receipt.ContractAddress.Hex()) && record.NFT.ID == metadataTokenID {
			matches = append(matches, record)
		}
	}
	if len(matches) != 1 {
		h.t.Fatalf("matching metadata success logs = %d", len(matches))
	}
	record := matches[0]
	if record.Result != "succeeded" || record.Job.Attempt != 1 || record.Job.MaxAttempts != 5 ||
		record.Block.Number != strconv.FormatUint(uint64(receipt.BlockNumber), 10) ||
		!strings.EqualFold(record.Block.Hash, receipt.BlockHash.Hex()) ||
		record.Transition.State != "available" || record.Transition.Code != "" ||
		record.Source.Scheme != "ipfs" || record.Request.Scheme != "https" ||
		record.Request.Host != "ipfs.io" || record.Request.Path != "/ipfs/bafybeibnsoufr2renqzsh347nrx54wcubt5lgkeivez63xvivplfwhtpym/metadata.json" ||
		record.Request.Redirects != 0 {
		h.t.Fatalf("metadata transition log = %#v", record)
	}
	assertNetworkEvidence(h.t, record.Network.ResolvedIPs, record.Network.ConnectedIP, record.Network.PolicyBypassed)
	return record
}

func assertNetworkEvidence(t *testing.T, resolved []string, connected string, bypassed bool) {
	t.Helper()
	if len(resolved) == 0 || connected == "" {
		t.Fatalf("metadata network evidence is incomplete: resolved=%v connected=%q", resolved, connected)
	}
	fakeIP := false
	for _, raw := range append(append([]string(nil), resolved...), connected) {
		ip := net.ParseIP(raw)
		decision := netpolicy.ClassifyIP(ip)
		if decision.Allowed {
			continue
		}
		if decision.Classification != netpolicy.IPClassificationSpecialPurpose || decision.Prefix != "198.18.0.0/15" {
			t.Fatalf("metadata gate connected through unexpected non-public address %s (%+v)", raw, decision)
		}
		fakeIP = true
	}
	if bypassed != fakeIP {
		t.Fatalf("metadata network bypass = %t, fake-IP evidence = %t", bypassed, fakeIP)
	}
}

func (h *harness) restartMetadataAndAssertPersistence(
	ctx context.Context,
	receipt rpcReceipt,
	before metadataSnapshot,
	job jobSnapshot,
) {
	h.t.Helper()
	if _, err := h.project.Run(
		ctx, "up", "-d", "--no-deps", "--force-recreate", "--wait", "--wait-timeout", "90", "metadata",
	); err != nil {
		h.t.Fatal(err)
	}
	deadline := time.NewTimer(6 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			h.t.Fatal(ctx.Err())
		case <-deadline.C:
			return
		case <-ticker.C:
			var state string
			var attemptCount, attempts, attemptRows int
			err := h.db.QueryRow(ctx, `
				SELECT metadata.state, metadata.attempt_count, job.attempts,
				       (SELECT count(*)::int FROM external_metadata_attempts WHERE durable_job_id = job.id)
				FROM external_metadata AS metadata
				JOIN durable_jobs AS job ON job.id = $5
				WHERE metadata.chain_id = $1::numeric AND metadata.resource_kind = 'nft'
				  AND metadata.token_address = $2 AND metadata.token_id = $3::numeric
				  AND metadata.observed_block_hash = $4`,
				previewChainID, receipt.ContractAddress.Bytes(), metadataTokenID, receipt.BlockHash.Bytes(), job.ID,
			).Scan(&state, &attemptCount, &attempts, &attemptRows)
			if err != nil {
				h.t.Fatal(err)
			}
			if state != before.State || attemptCount != 1 || attempts != 1 || attemptRows != 1 {
				h.t.Fatalf("metadata changed after restart: %s attempts=%d/%d rows=%d", state, attemptCount, attempts, attemptRows)
			}
		}
	}
}

func (h *harness) writeReport(
	ctx context.Context,
	receipt rpcReceipt,
	metadata metadataSnapshot,
	job jobSnapshot,
	transition metadataTransitionLog,
) {
	h.t.Helper()
	revision := strings.TrimSpace(commandOutput(ctx, h.root, "git", "rev-parse", "HEAD"))
	dirty := strings.TrimSpace(commandOutput(ctx, h.root, "git", "status", "--porcelain")) != ""
	imageID := strings.TrimSpace(commandOutput(ctx, h.root, dockerCommand(), "image", "inspect", "--format", "{{.Id}}", h.image))
	report := map[string]any{
		"revision": revision, "dirty": dirty, "image_id": imageID,
		"chain_id": previewChainID, "contract": strings.ToLower(receipt.ContractAddress.Hex()),
		"token_id": metadataTokenID, "block_number": uint64(receipt.BlockNumber),
		"block_hash":     strings.ToLower(receipt.BlockHash.Hex()),
		"cid":            "bafybeibnsoufr2renqzsh347nrx54wcubt5lgkeivez63xvivplfwhtpym",
		"content_sha256": metadata.ContentHash, "content_size": metadata.ContentSize,
		"media_type": metadata.MediaType, "attempts": job.Attempts,
		"policy_bypassed": transition.Network.PolicyBypassed,
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.artifacts, "report.json"), append(encoded, '\n'), 0o600); err != nil {
		h.t.Fatal(err)
	}
	h.t.Logf("preview metadata report: %s", encoded)
}

func (h *harness) rpcCall(ctx context.Context, result any, method string, args ...any) {
	h.t.Helper()
	if err := h.rpc.CallContext(ctx, result, method, args...); err != nil {
		h.t.Fatalf("RPC %s: %v", method, err)
	}
}

func (h *harness) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if h.project != nil {
		h.captureDiagnostics(ctx)
		if err := h.project.Down(ctx); err != nil {
			h.t.Logf("Preview metadata cleanup: %v", err)
		}
	}
	if h.rpc != nil {
		h.rpc.Close()
	}
	if h.db != nil {
		h.db.Close()
	}
	if h.succeeded {
		if err := os.RemoveAll(h.artifacts); err != nil {
			h.t.Logf("remove successful Preview metadata artifacts: %v", err)
		}
		return
	}
	h.t.Logf("retained Preview metadata diagnostics: %s", h.artifacts)
}

func (h *harness) captureDiagnostics(ctx context.Context) {
	for name, args := range map[string][]string{
		"compose-ps.txt":   {"ps", "--all"},
		"compose-logs.txt": {"logs", "--no-color", "--timestamps"},
	} {
		output, _ := h.project.Run(ctx, args...)
		redacted := bytes.ReplaceAll(output, []byte(previewDatabasePassword), []byte("[REDACTED]"))
		_ = os.WriteFile(filepath.Join(h.artifacts, name), redacted, 0o600)
	}
}

func waitFor(
	t *testing.T,
	ctx context.Context,
	description string,
	probe func() (bool, string, error),
) {
	t.Helper()
	deadline := time.NewTimer(waitTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	last := "not attempted"
	for {
		ready, detail, err := probe()
		if detail != "" {
			last = detail
		}
		if err != nil {
			t.Fatalf("%s: %v (%s)", description, err, last)
		}
		if ready {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s: %v (%s)", description, ctx.Err(), last)
		case <-deadline.C:
			t.Fatalf("%s timed out (%s)", description, last)
		case <-ticker.C:
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if info, statErr := os.Stat(filepath.Join(directory, "go.mod")); statErr == nil && info.Mode().IsRegular() {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("repository root was not found")
		}
		directory = parent
	}
}

func commandOutput(ctx context.Context, directory, name string, args ...string) string {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = directory
	output, _ := command.Output()
	return string(output)
}

func dockerCommand() string {
	value := strings.TrimSpace(os.Getenv("DOCKER"))
	if value == "" || strings.ContainsAny(value, " \t") {
		return "docker"
	}
	return value
}

func valueOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
