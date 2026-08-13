package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/query"
)

type appProxyQueryStub struct {
	detail          query.ProxyDetail
	upgrades        query.ProxyUpgradePage
	initializations query.ProxyInitializationPage
	diamondCuts     query.DiamondCutPage
	err             error
	address         string
	cursor          string
	limit           int
}

func (stub *appProxyQueryStub) Proxy(_ context.Context, address string) (query.ProxyDetail, error) {
	stub.address = address
	return stub.detail, stub.err
}

func (stub *appProxyQueryStub) ProxyUpgrades(
	_ context.Context,
	address, cursor string,
	limit int,
) (query.ProxyUpgradePage, error) {
	stub.address, stub.cursor, stub.limit = address, cursor, limit
	return stub.upgrades, stub.err
}

func (stub *appProxyQueryStub) ProxyInitializations(
	_ context.Context,
	address, cursor string,
	limit int,
) (query.ProxyInitializationPage, error) {
	stub.address, stub.cursor, stub.limit = address, cursor, limit
	return stub.initializations, stub.err
}

func (stub *appProxyQueryStub) DiamondCuts(
	_ context.Context,
	address, cursor string,
	limit int,
) (query.DiamondCutPage, error) {
	stub.address, stub.cursor, stub.limit = address, cursor, limit
	return stub.diamondCuts, stub.err
}

func TestProxyReaderAdapterMapsWriterModelsToOpenAPI(t *testing.T) {
	t.Parallel()
	const (
		proxyAddress          = "0x1111111111111111111111111111111111111111"
		implementationAddress = "0x2222222222222222222222222222222222222222"
		adminAddress          = "0x3333333333333333333333333333333333333333"
		blockHash             = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		codeHash              = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		transactionHash       = "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	)
	observedAt := time.Date(2026, time.August, 2, 1, 2, 3, 0, time.FixedZone("test", 8*60*60))
	implementation := query.ProxyIdentity{
		Address: implementationAddress, CodeHash: codeHash,
		ArtifactKind: "uups_implementation", StandardVersion: "5.6.1", Verified: true,
	}
	admin := query.ProxyIdentity{
		Address: adminAddress, CodeHash: codeHash,
		ArtifactKind: "proxy_admin", StandardVersion: "5.6.1", Verified: true,
	}
	stub := &appProxyQueryStub{
		detail: query.ProxyDetail{
			Address: proxyAddress, Status: "verified",
			Snapshot:  query.ProxySnapshot{Number: "42", Hash: blockHash},
			Mechanism: "eip1967", Pattern: "uups", StandardVersion: "5.6.1",
			Confidence: "verified", EvidenceState: "exact", BindingID: "ddeb27fb-d9a0-4624-be4d-4615062daed4",
			Proxy:          &query.ProxyIdentity{Address: proxyAddress, CodeHash: codeHash, ArtifactKind: "erc1967_proxy", StandardVersion: "5.6.1", Verified: true},
			Implementation: &implementation,
			Evidence: []query.ProxyEvidence{{
				Subject: "implementation", Source: "direct_call", Result: "authoritative",
				Address: implementationAddress, CodeHash: codeHash, BlockNumber: "42", BlockHash: blockHash,
			}},
		},
		upgrades: query.ProxyUpgradePage{
			ProxyAddress: proxyAddress,
			Snapshot:     query.ProxySnapshot{Number: "42", Hash: blockHash},
			Coverage:     query.ProxyHistoryCoverage{State: "complete", FromBlock: "1", ToBlock: "42"},
			NextCursor:   "next-upgrade",
			Items: []query.ProxyUpgrade{{
				ChangeType: "implementation", EvidenceType: "event",
				BlockNumber: "40", BlockHash: blockHash, BlockTimestamp: observedAt,
				NewImplementation: implementation, TransactionHash: transactionHash,
				LogIndex: "0", EmitterAddress: proxyAddress,
				Management: &query.ProxyManagement{Kind: "proxy_admin", Target: admin},
			}},
		},
		initializations: query.ProxyInitializationPage{
			ContractAddress: proxyAddress,
			Snapshot:        query.ProxySnapshot{Number: "42", Hash: blockHash},
			Coverage:        query.ProxyHistoryCoverage{State: "partial", FromBlock: "10", ToBlock: "42"},
			NextCursor:      "next-initialization",
			Items: []query.ProxyInitialization{{
				Version: "2", BlockNumber: "41", BlockHash: blockHash, BlockTimestamp: observedAt,
				TransactionHash: transactionHash, LogIndex: "1", Implementation: implementation,
			}},
		},
	}
	adapter := newProxyReaderAdapter(stub, 31337, false)
	detail, err := adapter.Proxy(context.Background(), proxyAddress)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Snapshot.ChainId != "31337" || detail.Status != "verified" ||
		detail.BindingId == nil || detail.Implementation == nil ||
		detail.Implementation.VerificationState != "verified" || len(detail.Evidence) != 1 ||
		detail.ImplementationInteraction == nil ||
		detail.ImplementationInteraction.Proxy.Address != detail.Address ||
		detail.ImplementationInteraction.Implementation.Address != implementationAddress {
		t.Fatalf("proxy detail = %+v", detail)
	}
	upgrades, next, err := adapter.ProxyUpgrades(context.Background(), proxyAddress, "page", 7)
	if err != nil {
		t.Fatal(err)
	}
	if next != "next-upgrade" || stub.cursor != "page" || stub.limit != 7 ||
		len(upgrades.Items) != 1 || upgrades.Items[0].Management == nil ||
		upgrades.Items[0].BlockTimestamp.Location() != time.UTC {
		t.Fatalf("proxy upgrades next=%q calls=%q/%d page=%+v", next, stub.cursor, stub.limit, upgrades)
	}
	initializations, next, err := adapter.ProxyInitializations(context.Background(), proxyAddress, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if next != "next-initialization" || len(initializations.Items) != 1 ||
		initializations.Items[0].Version != "2" || initializations.Coverage.State != "partial" {
		t.Fatalf("proxy initializations next=%q page=%+v", next, initializations)
	}
}

func TestProxyReaderAdapterPublishesHighConfidenceUnverifiedInteractionWithoutManagement(t *testing.T) {
	t.Parallel()
	const (
		proxyAddress           = "0x1111111111111111111111111111111111111111"
		implementationAddress  = "0x2222222222222222222222222222222222222222"
		blockHash              = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		proxyCodeHash          = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		implementationCodeHash = "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	)
	stub := &appProxyQueryStub{detail: query.ProxyDetail{
		Address: proxyAddress, Status: "detected_unverified",
		Snapshot:  query.ProxySnapshot{Number: "42", Hash: blockHash},
		Mechanism: "eip1967", Pattern: "unknown", Confidence: "high",
		EvidenceState:  "partial",
		Proxy:          &query.ProxyIdentity{Address: proxyAddress, CodeHash: proxyCodeHash},
		Implementation: &query.ProxyIdentity{Address: implementationAddress, CodeHash: implementationCodeHash},
	}}
	detail, err := newProxyReaderAdapter(stub, 31337, false).Proxy(context.Background(), proxyAddress)
	if err != nil {
		t.Fatal(err)
	}
	if detail.BindingId != nil || detail.Management != nil || detail.ImplementationInteraction == nil {
		t.Fatalf("ordinary interaction exposed the wrong authority: %+v", detail)
	}
	if detail.ImplementationInteraction.Proxy.CodeHash != proxyCodeHash ||
		detail.ImplementationInteraction.Implementation.CodeHash != implementationCodeHash {
		t.Fatalf("interaction identity = %+v", detail.ImplementationInteraction)
	}
}

func TestProxyReaderAdapterRejectsMalformedPersistedPublicIdentity(t *testing.T) {
	t.Parallel()
	stub := &appProxyQueryStub{detail: query.ProxyDetail{
		Address: "0x1111111111111111111111111111111111111111",
		Status:  "invented",
		Snapshot: query.ProxySnapshot{
			Number: "42",
			Hash:   "0x" + strings.Repeat("a", 64),
		},
	}}
	if _, err := newProxyReaderAdapter(stub, 1, false).Proxy(context.Background(), stub.detail.Address); err == nil {
		t.Fatal("malformed persisted proxy status was published")
	}
}

func TestProxyReaderAdapterGatesSafeDetectionV2PublicProjection(t *testing.T) {
	t.Parallel()
	const proxyAddress = "0x1111111111111111111111111111111111111111"
	raw := json.RawMessage(`{
		"status":"confirmed",
		"primary":{"detector":"safe","detector_version":"safe-proxy@1","priority":200,"family":"safe","variant":"safe-proxy","status":"confirmed","confidence":"high","proxy":"0x1111111111111111111111111111111111111111","implementation":"0x2222222222222222222222222222222222222222","implementation_role":"singleton","implementation_path":["0x1111111111111111111111111111111111111111","0x2222222222222222222222222222222222222222"],"canonical_proxy_shell":true,"implementation_has_code":true,"official_singleton":false,"singleton_changed":false,"evidence":[],"warnings":[],"chain_id":"31337","block_number":"42","block_hash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		"outcomes":[{"detector":"safe","detector_version":"safe-proxy@1","priority":200,"family":"safe","variant":"safe-proxy","status":"confirmed","confidence":"high","proxy":"0x1111111111111111111111111111111111111111","implementation":"0x2222222222222222222222222222222222222222","implementation_role":"singleton","implementation_path":[],"canonical_proxy_shell":true,"implementation_has_code":true,"official_singleton":false,"singleton_changed":false,"evidence":[],"warnings":[],"chain_id":"31337","block_number":"42","block_hash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}],
		"conflicts":[]}`)
	stub := &appProxyQueryStub{detail: query.ProxyDetail{
		Address: proxyAddress, Status: "not_detected",
		Snapshot:    query.ProxySnapshot{Number: "42", Hash: "0x" + strings.Repeat("a", 64)},
		DetectionV2: raw,
	}}
	shadow, err := newProxyReaderAdapter(stub, 31337, false).Proxy(context.Background(), proxyAddress)
	if err != nil {
		t.Fatal(err)
	}
	if shadow.ProxyDetectionV2 != nil {
		t.Fatal("shadow-only V2 leaked through the public API")
	}
	public, err := newProxyReaderAdapter(stub, 31337, true).Proxy(context.Background(), proxyAddress)
	if err != nil {
		t.Fatal(err)
	}
	if public.ProxyDetectionV2 == nil || public.ProxyDetectionV2.Primary == nil ||
		public.ProxyDetectionV2.Primary.Family == nil || *public.ProxyDetectionV2.Primary.Family != "safe" ||
		public.ProxyDetectionV2.Primary.ImplementationRole == nil ||
		*public.ProxyDetectionV2.Primary.ImplementationRole != "singleton" {
		t.Fatalf("public Safe projection=%+v", public.ProxyDetectionV2)
	}
}

func TestProxyReaderAdapterPublishesDiamondTargetsAndCutHistoryWithoutSingularImplementation(t *testing.T) {
	t.Parallel()
	const (
		diamondAddress = "0x1111111111111111111111111111111111111111"
		facetAddress   = "0x2222222222222222222222222222222222222222"
		initAddress    = "0x3333333333333333333333333333333333333333"
		blockHash      = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		codeHash       = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		txHash         = "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	)
	raw := json.RawMessage(`{
		"status":"confirmed",
		"primary":{"detector":"erc2535","detector_version":"1.0.0","priority":150,"family":"erc2535","variant":"diamond","status":"confirmed","confidence":"high","proxy":"0x1111111111111111111111111111111111111111","implementation_path":[],"canonical_proxy_shell":false,"implementation_has_code":false,"official_singleton":false,"singleton_changed":false,"targets":[{"address":"0x2222222222222222222222222222222222222222","role":"facet","selectors":["0x11223344"],"code_exists":true,"code_hash":"0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},{"address":"0x1111111111111111111111111111111111111111","role":"immutable","selectors":["0x7a0ed627"],"code_exists":true}],"diamond":{"completeness":"complete","validation":"full","facets":[{"address":"0x2222222222222222222222222222222222222222","role":"facet","selectors":["0x11223344"],"code_exists":true,"code_hash":"0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},{"address":"0x1111111111111111111111111111111111111111","role":"immutable","selectors":["0x7a0ed627"],"code_exists":true}],"selector_to_facet":{"0x11223344":"0x2222222222222222222222222222222222222222","0x7a0ed627":"0x1111111111111111111111111111111111111111"},"implementation_addresses":["0x2222222222222222222222222222222222222222"],"standard_diamond_cut":{"status":"absent"},"truncated":false},"evidence":[],"warnings":[],"chain_id":"31337","block_number":"42","block_hash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		"outcomes":[{"detector":"erc2535","detector_version":"1.0.0","priority":150,"family":"erc2535","variant":"diamond","status":"confirmed","confidence":"high","proxy":"0x1111111111111111111111111111111111111111","implementation_path":[],"canonical_proxy_shell":false,"implementation_has_code":false,"official_singleton":false,"singleton_changed":false,"targets":[{"address":"0x2222222222222222222222222222222222222222","role":"facet","selectors":["0x11223344"],"code_exists":true,"code_hash":"0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},{"address":"0x1111111111111111111111111111111111111111","role":"immutable","selectors":["0x7a0ed627"],"code_exists":true}],"diamond":{"completeness":"complete","validation":"full","facets":[{"address":"0x2222222222222222222222222222222222222222","role":"facet","selectors":["0x11223344"],"code_exists":true,"code_hash":"0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},{"address":"0x1111111111111111111111111111111111111111","role":"immutable","selectors":["0x7a0ed627"],"code_exists":true}],"selector_to_facet":{"0x11223344":"0x2222222222222222222222222222222222222222","0x7a0ed627":"0x1111111111111111111111111111111111111111"},"implementation_addresses":["0x2222222222222222222222222222222222222222"],"standard_diamond_cut":{"status":"absent"},"truncated":false},"evidence":[],"warnings":[],"chain_id":"31337","block_number":"42","block_hash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}],
		"conflicts":[]}`)
	observedAt := time.Date(2026, time.August, 13, 1, 2, 3, 0, time.UTC)
	stub := &appProxyQueryStub{
		detail: query.ProxyDetail{
			Address: diamondAddress, Status: "not_detected",
			Snapshot: query.ProxySnapshot{Number: "42", Hash: blockHash}, DetectionV2: raw,
		},
		diamondCuts: query.DiamondCutPage{
			DiamondAddress: diamondAddress,
			Snapshot:       query.ProxySnapshot{Number: "42", Hash: blockHash},
			Coverage:       query.ProxyHistoryCoverage{State: "complete", FromBlock: "1", ToBlock: "42"},
			Items: []query.DiamondCut{{
				BlockNumber: "1", BlockHash: blockHash, BlockTimestamp: observedAt,
				TransactionHash: txHash, TransactionIndex: "0", LogIndex: "0",
				InitAddress: initAddress, InitCalldata: "0x1234",
				Cuts: []query.DiamondFacetCut{{
					CutIndex: 0, Action: "add", FacetAddress: facetAddress,
					Selectors: []string{"0x11223344"},
				}},
			}},
		},
	}
	adapter := newProxyReaderAdapter(stub, 31337, true)
	detail, err := adapter.Proxy(context.Background(), diamondAddress)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != "detected_unverified" || detail.Implementation != nil ||
		detail.ImplementationAddresses == nil || len(*detail.ImplementationAddresses) != 1 ||
		(*detail.ImplementationAddresses)[0] != facetAddress || detail.ProxyDetectionV2 == nil ||
		detail.ProxyDetectionV2.Primary == nil || detail.ProxyDetectionV2.Primary.Diamond == nil {
		t.Fatalf("Diamond detail=%+v", detail)
	}
	history, _, err := adapter.DiamondCuts(context.Background(), diamondAddress, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Items) != 1 || history.Items[0].InitAddress != initAddress ||
		history.Items[0].Cuts[0].FacetAddress != facetAddress ||
		history.Items[0].Cuts[0].Selectors[0] != "0x11223344" {
		t.Fatalf("DiamondCut history=%+v", history)
	}
}

func TestProxyReaderAdapterAllowsExactCloneBindingWithoutCloneArtifact(t *testing.T) {
	t.Parallel()
	const (
		cloneAddress          = "0x1111111111111111111111111111111111111111"
		implementationAddress = "0x2222222222222222222222222222222222222222"
		blockHash             = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		codeHash              = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	stub := &appProxyQueryStub{detail: query.ProxyDetail{
		Address: cloneAddress, Status: "verified",
		Snapshot:      query.ProxySnapshot{Number: "42", Hash: blockHash},
		Mechanism:     "eip1167",
		Pattern:       "clone",
		Confidence:    "verified",
		EvidenceState: "exact",
		BindingID:     "ddeb27fb-d9a0-4624-be4d-4615062daed4",
		Proxy: &query.ProxyIdentity{
			Address: cloneAddress, CodeHash: codeHash, Verified: false,
		},
		Implementation: &query.ProxyIdentity{
			Address: implementationAddress, CodeHash: codeHash,
			Verified: true,
		},
	}}
	detail, err := newProxyReaderAdapter(stub, 31337, false).Proxy(context.Background(), cloneAddress)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Proxy == nil || detail.Proxy.VerificationState != "unverified" ||
		detail.Implementation == nil || detail.Implementation.VerificationState != "verified" ||
		detail.BindingId == nil || detail.ImplementationInteraction == nil {
		t.Fatalf("clone detail = %+v", detail)
	}
}
