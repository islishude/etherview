package query

import (
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/httpapi"
)

func TestProxyHistoryCursorBindsChainAddressKindAndSnapshot(t *testing.T) {
	reader := &PostgresReader{chainID: "1"}
	address := common.HexToAddress("0x0000000000000000000000000000000000000011")
	cursor, err := httpapi.EncodeCursor(proxyHistoryCursor{
		Version: 1, ChainID: "1", Address: strings.ToLower(address.Hex()),
		Kind: "upgrades", SnapshotNumber: 12, SnapshotHash: testHash(12),
		DurableJobID: 91, JobGeneration: 3,
		BeforeBlockNumber: 10, BeforeEventOrder: 3, BeforeSourceRank: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	parsedAddress, parsed, err := reader.decodeProxyHistoryCursor(address.Hex(), cursor, "upgrades")
	if err != nil {
		t.Fatalf("decode exact proxy cursor: %v", err)
	}
	if parsedAddress != address || parsed.SnapshotNumber != 12 || parsed.BeforeBlockNumber != 10 {
		t.Fatalf("decoded cursor=%+v address=%s", parsed, parsedAddress)
	}
	for name, mutate := range map[string]func(*proxyHistoryCursor){
		"chain":    func(value *proxyHistoryCursor) { value.ChainID = "2" },
		"address":  func(value *proxyHistoryCursor) { value.Address = strings.ToLower(common.HexToAddress("0x22").Hex()) },
		"kind":     func(value *proxyHistoryCursor) { value.Kind = "initializations" },
		"snapshot": func(value *proxyHistoryCursor) { value.SnapshotHash = "0x01" },
		"job":      func(value *proxyHistoryCursor) { value.DurableJobID = 0 },
		"generation": func(value *proxyHistoryCursor) {
			value.JobGeneration = 0
		},
		"boundary": func(value *proxyHistoryCursor) { value.BeforeBlockNumber = 13 },
	} {
		t.Run(name, func(t *testing.T) {
			value := parsed
			mutate(&value)
			encoded, err := httpapi.EncodeCursor(value)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = reader.decodeProxyHistoryCursor(address.Hex(), encoded, "upgrades")
			if !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("error=%v, want invalid cursor", err)
			}
		})
	}
}

func TestApplyProxyDetectionKeepsInteractionUnverified(t *testing.T) {
	address := common.HexToAddress("0x0000000000000000000000000000000000000011")
	implementation := common.HexToAddress("0x0000000000000000000000000000000000000022")
	admin := common.HexToAddress("0x0000000000000000000000000000000000000033")
	standard := "5.6.1"
	detail := ProxyDetail{Address: address.Hex()}
	err := applyProxyDetection(&detail, address, dbgen.GetLatestPublishedProxyDetectionRow{
		ObservationBlockNumber: "7", ObservationBlockHash: common.BigToHash(proxyTestBig(7)).Bytes(),
		ProxyCodeHash: common.BigToHash(proxyTestBig(71)).Bytes(), ProxyKind: "eip1967",
		ProxyPattern: "transparent", StandardVersion: &standard,
		ImplementationAddress:  implementation.Bytes(),
		ImplementationCodeHash: common.BigToHash(proxyTestBig(72)).Bytes(),
		AdminAddress:           admin.Bytes(), AdminCodeHash: common.BigToHash(proxyTestBig(73)).Bytes(),
		Confidence: "high", EvidenceState: "exact",
		ProxyVerified: true, ImplementationVerified: true, AdminVerified: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != ProxyStatusDetectedUnverified || detail.BindingID != "" ||
		detail.Management != nil ||
		detail.Proxy.ArtifactKind != "" || detail.Proxy.StandardVersion != "" ||
		detail.Admin.ArtifactKind != "" || len(detail.Evidence) != 3 {
		t.Fatalf("detail=%+v evidence=%+v", detail, detail.Evidence)
	}
	if detail.Evidence[2].Subject != "admin" || detail.Evidence[2].Source != "admin_slot" {
		t.Fatalf("unbound admin evidence=%+v, want admin_slot", detail.Evidence[2])
	}
}

func TestApplyProxyDetectionBeaconHasOneDirectCallImplementationEvidence(t *testing.T) {
	address := common.HexToAddress("0x0000000000000000000000000000000000000011")
	implementation := common.HexToAddress("0x0000000000000000000000000000000000000022")
	beacon := common.HexToAddress("0x0000000000000000000000000000000000000033")
	detail := ProxyDetail{Address: address.Hex()}
	if err := applyProxyDetection(&detail, address, dbgen.GetLatestPublishedProxyDetectionRow{
		ObservationBlockNumber: "7", ObservationBlockHash: common.BigToHash(proxyTestBig(7)).Bytes(),
		ProxyCodeHash: common.BigToHash(proxyTestBig(71)).Bytes(), ProxyKind: "beacon",
		ProxyPattern: "beacon", ImplementationAddress: implementation.Bytes(),
		ImplementationCodeHash: common.BigToHash(proxyTestBig(72)).Bytes(),
		BeaconAddress:          beacon.Bytes(), BeaconCodeHash: common.BigToHash(proxyTestBig(73)).Bytes(),
		Confidence: "high", EvidenceState: "exact", ImplementationObservationBlockNumber: "9",
		ImplementationObservationBlockHash: common.BigToHash(proxyTestBig(9)).Bytes(),
	}); err != nil {
		t.Fatal(err)
	}
	implementationEvidence := 0
	beaconSlotEvidence := 0
	for _, evidence := range detail.Evidence {
		if evidence.Subject == "implementation" {
			implementationEvidence++
			if evidence.Source != "direct_call" {
				t.Fatalf("implementation evidence=%+v, want direct_call", evidence)
			}
		}
		if evidence.Source == "verified_artifact" {
			t.Fatalf("synthetic artifact evidence leaked: %+v", evidence)
		}
		if evidence.Subject == "beacon" && evidence.Source == "beacon_slot" {
			beaconSlotEvidence++
		}
	}
	if implementationEvidence != 1 || beaconSlotEvidence != 1 {
		t.Fatalf("implementation=%d beacon_slot=%d: %+v", implementationEvidence, beaconSlotEvidence, detail.Evidence)
	}
	if detail.Evidence[1].BlockNumber != "9" ||
		detail.Evidence[1].BlockHash != strings.ToLower(common.BigToHash(proxyTestBig(9)).Hex()) {
		t.Fatalf("Beacon implementation evidence uses wrong observation: %+v", detail.Evidence[1])
	}
}

func TestApplyProxyDetectionAllowsPartialBeaconWithoutImplementationObservation(t *testing.T) {
	address := common.HexToAddress("0x0000000000000000000000000000000000000011")
	beacon := common.HexToAddress("0x0000000000000000000000000000000000000033")
	detail := ProxyDetail{Address: address.Hex()}
	if err := applyProxyDetection(&detail, address, dbgen.GetLatestPublishedProxyDetectionRow{
		ObservationBlockNumber: "7", ObservationBlockHash: common.BigToHash(proxyTestBig(7)).Bytes(),
		ProxyCodeHash: common.BigToHash(proxyTestBig(71)).Bytes(), ProxyKind: "beacon",
		ProxyPattern: "unknown", BeaconAddress: beacon.Bytes(),
		BeaconCodeHash: common.BigToHash(proxyTestBig(73)).Bytes(),
		Confidence:     "inferred", EvidenceState: "partial",
		ImplementationObservationBlockNumber: "", ImplementationObservationBlockHash: nil,
	}); err != nil {
		t.Fatal(err)
	}
	if detail.Status != ProxyStatusDetectedUnverified || detail.Implementation != nil ||
		detail.EvidenceState != "partial" || len(detail.Evidence) != 2 {
		t.Fatalf("partial Beacon detail=%+v evidence=%+v", detail, detail.Evidence)
	}
	for _, evidence := range detail.Evidence {
		if evidence.Subject == "implementation" {
			t.Fatalf("partial Beacon invented implementation evidence: %+v", detail.Evidence)
		}
	}
}

func TestApplyVerifiedProxyBindingRejectsMalformedProvenance(t *testing.T) {
	address := common.HexToAddress("0x0000000000000000000000000000000000000011")
	standard := "5.6.1"
	base := dbgen.GetCurrentVerifiedProxyBindingRow{
		BindingID:     "00000000-0000-0000-0000-000000000001",
		ProxyCodeHash: common.BigToHash(proxyTestBig(71)).Bytes(), ProxyVerified: true, ProxyKind: "eip1967",
		ProxyPattern: "erc1967", StandardVersion: &standard,
		ImplementationAddress:  common.HexToAddress("0x22").Bytes(),
		ImplementationCodeHash: common.BigToHash(proxyTestBig(72)).Bytes(),
		ManagementKind:         "none", ObservationBlockNumber: "7",
		ObservationBlockHash: common.BigToHash(proxyTestBig(7)).Bytes(), SnapshotNumber: "8",
		SnapshotHash:                         common.BigToHash(proxyTestBig(8)).Bytes(),
		ImplementationObservationBlockNumber: "7",
		ImplementationObservationBlockHash:   common.BigToHash(proxyTestBig(7)).Bytes(),
	}
	detail := ProxyDetail{Address: address.Hex(), Snapshot: ProxySnapshot{
		Number: "8", Hash: strings.ToLower(common.BigToHash(proxyTestBig(8)).Hex()),
	}}
	valid := detail
	if err := applyVerifiedProxyBinding(&valid, address, base); err != nil {
		t.Fatalf("valid binding rejected: %v", err)
	}
	for name, mutate := range map[string]func(*dbgen.GetCurrentVerifiedProxyBindingRow){
		"snapshot_hash": func(row *dbgen.GetCurrentVerifiedProxyBindingRow) { row.SnapshotHash = []byte{1} },
		"observation_hash": func(row *dbgen.GetCurrentVerifiedProxyBindingRow) {
			row.ObservationBlockHash = []byte{1}
		},
		"version": func(row *dbgen.GetCurrentVerifiedProxyBindingRow) {
			invalid := "5.5.0"
			row.StandardVersion = &invalid
		},
		"pattern": func(row *dbgen.GetCurrentVerifiedProxyBindingRow) { row.ProxyPattern = "unknown" },
	} {
		t.Run(name, func(t *testing.T) {
			row := base
			mutate(&row)
			copyDetail := detail
			if err := applyVerifiedProxyBinding(&copyDetail, address, row); err == nil {
				t.Fatal("malformed verified binding provenance was accepted")
			}
		})
	}
}

func TestApplyVerifiedCloneBindingKeepsRuntimeUnverified(t *testing.T) {
	address := common.HexToAddress("0x0000000000000000000000000000000000000011")
	implementation := common.HexToAddress("0x0000000000000000000000000000000000000022")
	row := dbgen.GetCurrentVerifiedProxyBindingRow{
		BindingID: "00000000-0000-0000-0000-000000000001", ProxyVerified: false,
		ProxyCodeHash: common.BigToHash(proxyTestBig(71)).Bytes(), ProxyKind: "eip1167",
		ProxyPattern: "clone", StandardVersion: nil,
		ImplementationAddress:  implementation.Bytes(),
		ImplementationCodeHash: common.BigToHash(proxyTestBig(72)).Bytes(),
		ManagementKind:         "none", ObservationBlockNumber: "7",
		ObservationBlockHash: common.BigToHash(proxyTestBig(7)).Bytes(), SnapshotNumber: "8",
		SnapshotHash:                         common.BigToHash(proxyTestBig(8)).Bytes(),
		ImplementationObservationBlockNumber: "7",
		ImplementationObservationBlockHash:   common.BigToHash(proxyTestBig(7)).Bytes(),
	}
	detail := ProxyDetail{Address: address.Hex(), Snapshot: ProxySnapshot{
		Number: "8", Hash: strings.ToLower(common.BigToHash(proxyTestBig(8)).Hex()),
	}}
	if err := applyVerifiedProxyBinding(&detail, address, row); err != nil {
		t.Fatal(err)
	}
	if detail.Status != ProxyStatusVerified || detail.BindingID == "" ||
		detail.StandardVersion != "" || detail.Proxy.Verified ||
		detail.Proxy.ArtifactKind != "" || !detail.Implementation.Verified {
		t.Fatalf("clone binding detail=%+v", detail)
	}
}

func TestProxyUpgradeAndInitializationModelsPreserveStringQuantities(t *testing.T) {
	oldImplementation := common.HexToAddress("0x0000000000000000000000000000000000000011")
	newImplementation := common.HexToAddress("0x0000000000000000000000000000000000000022")
	transactionHash := common.BigToHash(proxyTestBig(99))
	blockHash := common.BigToHash(proxyTestBig(10))
	logIndex := int64(4)
	upgrade, err := proxyUpgradeModel(dbgen.ListProxyUpgradeHistoryRow{
		BlockNumber: "10", BlockHash: blockHash.Bytes(), BlockTimestamp: "1700000000",
		EventOrder: 4, SourceRank: 0, ChangeType: "implementation", EvidenceType: "event",
		OldImplementationAddress:  oldImplementation.Bytes(),
		OldImplementationCodeHash: common.BigToHash(proxyTestBig(101)).Bytes(),
		NewImplementationAddress:  newImplementation.Bytes(),
		NewImplementationCodeHash: common.BigToHash(proxyTestBig(102)).Bytes(),
		TransactionHash:           transactionHash.Bytes(), LogIndex: &logIndex,
		EmitterAddress: oldImplementation.Bytes(), NewImplementationVerified: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if upgrade.BlockNumber != "10" || upgrade.LogIndex != "4" ||
		upgrade.TransactionHash != strings.ToLower(transactionHash.Hex()) ||
		upgrade.NewImplementation.Address != newImplementation.Hex() ||
		!upgrade.NewImplementation.Verified || upgrade.BlockTimestamp.Unix() != 1700000000 {
		t.Fatalf("upgrade=%+v", upgrade)
	}
	initialization, err := proxyInitializationModel(dbgen.ListProxyInitializationHistoryRow{
		Version: "18446744073709551615", BlockNumber: "10", BlockHash: blockHash.Bytes(),
		BlockTimestamp: "1700000000", TransactionHash: transactionHash.Bytes(), LogIndex: 5,
		ImplementationAddress:  newImplementation.Bytes(),
		ImplementationCodeHash: common.BigToHash(proxyTestBig(102)).Bytes(),
		ImplementationVerified: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if initialization.Version != "18446744073709551615" || initialization.LogIndex != "5" ||
		!initialization.BlockTimestamp.Equal(time.Unix(1700000000, 0).UTC()) {
		t.Fatalf("initialization=%+v", initialization)
	}
}

func TestDiamondCutModelPreservesOrderingAndKeepsInitSeparate(t *testing.T) {
	diamondFacet := common.HexToAddress("0x0000000000000000000000000000000000000022")
	initTarget := common.HexToAddress("0x0000000000000000000000000000000000000033")
	transactionHash := common.BigToHash(proxyTestBig(99))
	blockHash := common.BigToHash(proxyTestBig(10))
	model, err := diamondCutModel(dbgen.ListDiamondCutHistoryRow{
		BlockNumber: "10", BlockHash: blockHash.Bytes(), BlockTimestamp: "1700000000",
		TransactionHash: transactionHash.Bytes(), TransactionIndex: 2, LogIndex: 4,
		InitAddress: initTarget.Bytes(), InitCalldata: []byte{0x12, 0x34},
		Cuts: []byte(`[
			{"cut_index":0,"action":0,"facet_address":"0x0000000000000000000000000000000000000022","selectors":["0x11223344"]},
			{"cut_index":1,"action":2,"facet_address":"0x0000000000000000000000000000000000000000","selectors":["0xaabbccdd"]}
		]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if model.TransactionIndex != "2" || model.LogIndex != "4" ||
		model.InitAddress != initTarget.Hex() || model.InitCalldata != "0x1234" ||
		len(model.Cuts) != 2 || model.Cuts[0].FacetAddress != diamondFacet.Hex() ||
		model.Cuts[0].Action != "add" || model.Cuts[1].Action != "remove" ||
		model.Cuts[1].FacetAddress == model.InitAddress {
		t.Fatalf("DiamondCut=%+v", model)
	}
}

func proxyTestBig(value int64) *big.Int {
	return big.NewInt(value)
}

func TestProxyModelRejectsMalformedPersistentIdentity(t *testing.T) {
	_, err := proxyUpgradeModel(dbgen.ListProxyUpgradeHistoryRow{
		BlockNumber: "1", BlockHash: make([]byte, 32), BlockTimestamp: "1",
		NewImplementationAddress: []byte{1}, NewImplementationCodeHash: make([]byte, 32),
	})
	if err == nil {
		t.Fatal("malformed implementation address was accepted")
	}
	if _, err := proxyBlockTimestamp("18446744073709551615"); err == nil {
		t.Fatal("timestamp above int64 was accepted")
	}
}
