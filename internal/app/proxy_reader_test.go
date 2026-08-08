package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/query"
)

type appProxyQueryStub struct {
	detail          query.ProxyDetail
	upgrades        query.ProxyUpgradePage
	initializations query.ProxyInitializationPage
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
	adapter := newProxyReaderAdapter(stub, 31337)
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
	detail, err := newProxyReaderAdapter(stub, 31337).Proxy(context.Background(), proxyAddress)
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
	if _, err := newProxyReaderAdapter(stub, 1).Proxy(context.Background(), stub.detail.Address); err == nil {
		t.Fatal("malformed persisted proxy status was published")
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
	detail, err := newProxyReaderAdapter(stub, 31337).Proxy(context.Background(), cloneAddress)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Proxy == nil || detail.Proxy.VerificationState != "unverified" ||
		detail.Implementation == nil || detail.Implementation.VerificationState != "verified" ||
		detail.BindingId == nil || detail.ImplementationInteraction == nil {
		t.Fatalf("clone detail = %+v", detail)
	}
}
