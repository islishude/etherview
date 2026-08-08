package enrich

import (
	"encoding/hex"
	"encoding/json"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
)

type proxyDetectionResolutionDocument struct {
	Status     ProxyDetectionStatus             `json:"status"`
	Primary    *proxyDetectionDocument          `json:"primary,omitempty"`
	Outcomes   []proxyDetectionDocument         `json:"outcomes"`
	Conflicts  []string                         `json:"conflicts"`
	ShadowDiff proxyDetectionShadowDiffDocument `json:"shadow_diff"`
}

type proxyDetectionShadowDiffDocument struct {
	Different bool     `json:"different"`
	Reasons   []string `json:"reasons"`
}

type proxyDetectionDocument struct {
	Detector                string                   `json:"detector"`
	DetectorVersion         string                   `json:"detector_version"`
	Priority                int                      `json:"priority"`
	Family                  ProxyFamily              `json:"family,omitempty"`
	Variant                 string                   `json:"variant,omitempty"`
	Status                  ProxyDetectionStatus     `json:"status"`
	Confidence              ProxyDetectionConfidence `json:"confidence"`
	Proxy                   string                   `json:"proxy"`
	Implementation          string                   `json:"implementation,omitempty"`
	ImplementationRole      ProxyImplementationRole  `json:"implementation_role,omitempty"`
	ImplementationPath      []string                 `json:"implementation_path"`
	Admin                   string                   `json:"admin,omitempty"`
	Beacon                  string                   `json:"beacon,omitempty"`
	CanonicalProxyShell     bool                     `json:"canonical_proxy_shell"`
	ImplementationHasCode   bool                     `json:"implementation_has_code"`
	OfficialSingleton       bool                     `json:"official_singleton"`
	SingletonVersion        string                   `json:"singleton_version,omitempty"`
	SingletonDeploymentType string                   `json:"singleton_deployment_type,omitempty"`
	InitialSingleton        string                   `json:"initial_singleton,omitempty"`
	SingletonChanged        bool                     `json:"singleton_changed"`
	Evidence                []proxyEvidenceDocument  `json:"evidence"`
	Warnings                []string                 `json:"warnings"`
	ChainID                 string                   `json:"chain_id"`
	BlockNumber             string                   `json:"block_number"`
	BlockHash               string                   `json:"block_hash"`
}

type proxyEvidenceDocument struct {
	Kind        ProxyEvidenceKind `json:"kind"`
	Description string            `json:"description"`
	Address     string            `json:"address,omitempty"`
	Slot        string            `json:"slot,omitempty"`
	Value       string            `json:"value,omitempty"`
}

func marshalProxyDetectionResolution(resolution ProxyDetectionResolution) ([]byte, error) {
	document := proxyDetectionResolutionDocument{
		Status: resolution.Status, Outcomes: make([]proxyDetectionDocument, len(resolution.Outcomes)),
		Conflicts: append([]string(nil), resolution.Conflicts...),
		ShadowDiff: proxyDetectionShadowDiffDocument{
			Different: resolution.LegacyProjectionChanged,
			Reasons:   append([]string(nil), resolution.LegacyDiffReasons...),
		},
	}
	if document.Conflicts == nil {
		document.Conflicts = []string{}
	}
	if document.ShadowDiff.Reasons == nil {
		document.ShadowDiff.Reasons = []string{}
	}
	for index := range resolution.Outcomes {
		document.Outcomes[index] = proxyDetectionToDocument(resolution.Outcomes[index])
	}
	if resolution.Primary != nil {
		primary := proxyDetectionToDocument(*resolution.Primary)
		document.Primary = &primary
	}
	return json.Marshal(document)
}

func proxyDetectionToDocument(detection ProxyDetectionV2) proxyDetectionDocument {
	document := proxyDetectionDocument{
		Detector: detection.Detector, DetectorVersion: detection.DetectorVersion,
		Priority: detection.Priority, Family: detection.Family, Variant: detection.Variant,
		Status: detection.Status, Confidence: detection.Confidence, Proxy: detection.Proxy.Hex(),
		ImplementationRole:      detection.ImplementationRole,
		CanonicalProxyShell:     detection.CanonicalProxyShell,
		ImplementationHasCode:   detection.ImplementationHasCode,
		OfficialSingleton:       detection.OfficialSingleton,
		SingletonVersion:        detection.SingletonVersion,
		SingletonDeploymentType: detection.SingletonDeploymentType,
		SingletonChanged:        detection.SingletonChanged,
		Evidence:                make([]proxyEvidenceDocument, len(detection.Evidence)),
		Warnings:                append([]string(nil), detection.Warnings...),
		ImplementationPath:      make([]string, len(detection.ImplementationPath)),
		ChainID:                 detection.ChainID, BlockNumber: strconv.FormatUint(detection.BlockNumber, 10),
		BlockHash: detection.BlockHash.Hex(),
	}
	if document.Warnings == nil {
		document.Warnings = []string{}
	}
	for index, address := range detection.ImplementationPath {
		document.ImplementationPath[index] = address.Hex()
	}
	document.Implementation = optionalAddressString(detection.Implementation)
	document.Admin = optionalAddressString(detection.Admin)
	document.Beacon = optionalAddressString(detection.Beacon)
	document.InitialSingleton = optionalAddressString(detection.InitialSingleton)
	for index, evidence := range detection.Evidence {
		document.Evidence[index] = proxyEvidenceDocument{
			Kind: evidence.Kind, Description: evidence.Description,
			Address: optionalAddressString(evidence.Address),
		}
		if evidence.Slot != nil {
			document.Evidence[index].Slot = evidence.Slot.Hex()
		}
		if len(evidence.Value) != 0 {
			document.Evidence[index].Value = "0x" + hex.EncodeToString(evidence.Value)
		}
	}
	return document
}

func optionalAddressString(address *common.Address) string {
	if address == nil {
		return ""
	}
	return address.Hex()
}
