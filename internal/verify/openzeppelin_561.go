package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	gethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

const openZeppelin561Version = "5.6.1"

const (
	proxyArtifactERC1967           = "erc1967_proxy"
	proxyArtifactTransparent       = "transparent_proxy"
	proxyArtifactBeacon            = "beacon_proxy"
	proxyArtifactUUPS              = "uups_implementation"
	proxyArtifactProxyAdmin        = "proxy_admin"
	proxyArtifactUpgradeableBeacon = "upgradeable_beacon"
)

type recognizedProxyArtifact struct {
	Kind                 string
	StandardVersion      string
	RuntimeImmutable     *common.Address
	SourceManifestSHA256 [sha256.Size]byte
}

type openZeppelinArtifactSpec struct {
	kind              string
	file              string
	contract          string
	sources           []string
	immutableVariable string
}

// These SHA-256 values authenticate the exact Solidity source bytes in the
// signed OpenZeppelin Contracts v5.6.1 tag (commit 5fd1781). A later 5.x tag
// must be reviewed and added explicitly; it cannot inherit this exact status.
var openZeppelin561SourceSHA256 = map[string]string{
	"access/Ownable.sol":                                "38578bd71c0a909840e67202db527cc6b4e6b437e0f39f0c909da32c1e30cb81",
	"interfaces/IERC1967.sol":                           "c4e901318ab6d4963582c62c62ce988a34c68dbae71846b67decacde79e9a317",
	"interfaces/draft-IERC1822.sol":                     "73653312bc2eda0ec231553a5295242eaef2d4f46a023532d45fe76cbd0f565f",
	"proxy/ERC1967/ERC1967Proxy.sol":                    "41a3040398d53999dea3251ff8906e11ec1a699362a1d8f4a55bfc7709cc00f3",
	"proxy/ERC1967/ERC1967Utils.sol":                    "a89ba2032ab25959c6fde1e508ebf8f966ff632703d3ef79684f578a8fa8033f",
	"proxy/Proxy.sol":                                   "fa8aae37b2939371fcbce48b814c0d5c7e57e3e267f348459f29941937746c98",
	"proxy/beacon/BeaconProxy.sol":                      "220626a31d80fb0d7a625f0c9ac741d40cd9f3c98ef74eb5b3dd43d224701282",
	"proxy/beacon/IBeacon.sol":                          "c1c9726bbb0ec4c540c7c98059dcded69a8586b46c168036672381033ee76239",
	"proxy/beacon/UpgradeableBeacon.sol":                "26dda9d5bb961b3df26602d49f9f5a0647cfdb78b63cc253aed8527030a64f25",
	"proxy/transparent/ProxyAdmin.sol":                  "4da9a90b1c8b45fdab7d73d8cf81f51cf7d990657ee4e3f943492c3bf0e4c2f1",
	"proxy/transparent/TransparentUpgradeableProxy.sol": "58c759fe76057e1cefa8909b446357aae5cac8a8ba7e6401650208d1b0481508",
	"proxy/utils/UUPSUpgradeable.sol":                   "b5af3bfd79a32c6da2d09b0be38d9b63dcd0d159a1a4a0198431fd00acc1fd52",
	"utils/Address.sol":                                 "73e54e15285455f0e01e136c3663732641537614723ae5dbd820eb3cb036b1fd",
	"utils/Context.sol":                                 "847fda5460fee70f56f4200f59b82ae622bb03c79c77e67af010e31b7e2cc5b6",
	"utils/Errors.sol":                                  "0704b9d6c032cca8512a3bc3f30f49f86f1f03102d2896a3d23e794b82efea66",
	"utils/LowLevelCall.sol":                            "e128cbe9c6c406d5a42c26e4079c0a95b369ce552f2d0c3dfd2fcb836c5708f2",
	"utils/StorageSlot.sol":                             "75704538dcb223239280c6726d9a31cf769a7816718517c997fc7d63bdb70778",
}

var (
	oz561ERC1967Sources = []string{
		"interfaces/IERC1967.sol", "proxy/ERC1967/ERC1967Proxy.sol",
		"proxy/ERC1967/ERC1967Utils.sol", "proxy/Proxy.sol", "proxy/beacon/IBeacon.sol",
		"utils/Address.sol", "utils/Errors.sol", "utils/LowLevelCall.sol", "utils/StorageSlot.sol",
	}
	oz561TransparentSources = append(append([]string(nil), oz561ERC1967Sources...),
		"access/Ownable.sol", "proxy/transparent/ProxyAdmin.sol",
		"proxy/transparent/TransparentUpgradeableProxy.sol", "utils/Context.sol")
	oz561BeaconSources = []string{
		"interfaces/IERC1967.sol", "proxy/ERC1967/ERC1967Utils.sol", "proxy/Proxy.sol",
		"proxy/beacon/BeaconProxy.sol", "proxy/beacon/IBeacon.sol", "utils/Address.sol",
		"utils/Errors.sol", "utils/LowLevelCall.sol", "utils/StorageSlot.sol",
	}
	oz561UUPSSources = []string{
		"interfaces/IERC1967.sol", "interfaces/draft-IERC1822.sol",
		"proxy/ERC1967/ERC1967Utils.sol", "proxy/beacon/IBeacon.sol",
		"proxy/utils/UUPSUpgradeable.sol", "utils/Address.sol", "utils/Errors.sol",
		"utils/LowLevelCall.sol", "utils/StorageSlot.sol",
	}
	oz561ProxyAdminSources        = oz561TransparentSources
	oz561UpgradeableBeaconSources = []string{
		"access/Ownable.sol", "proxy/beacon/IBeacon.sol",
		"proxy/beacon/UpgradeableBeacon.sol", "utils/Context.sol",
	}
)

func recognizeOpenZeppelin561Artifact(
	outcome json.RawMessage,
	targetAddress common.Address,
	actualRuntime []byte,
) (recognizedProxyArtifact, bool) {
	var success struct {
		FileName     string          `json:"file_name"`
		ContractName string          `json:"contract_name"`
		ABI          json.RawMessage `json:"abi"`
		Sources      map[string]struct {
			Content string `json:"content"`
		} `json:"sources"`
		Compilation struct {
			LinearizedBases    []string          `json:"linearizedBaseContracts"`
			ImmutableVariables map[string]string `json:"immutableVariables"`
		} `json:"compilation_artifacts"`
		Runtime struct {
			ImmutableReferences map[string][]bytecodeRange `json:"immutableReferences"`
		} `json:"runtime_code_artifacts"`
		RuntimeMatch *VerificationMatchDetails `json:"runtime_match"`
	}
	if json.Unmarshal(outcome, &success) != nil || success.RuntimeMatch == nil {
		return recognizedProxyArtifact{}, false
	}
	canonicalFile := canonicalOpenZeppelinSourcePath(success.FileName)
	specs := []openZeppelinArtifactSpec{
		{kind: proxyArtifactTransparent, file: "proxy/transparent/TransparentUpgradeableProxy.sol", contract: "TransparentUpgradeableProxy", sources: oz561TransparentSources, immutableVariable: "_admin"},
		{kind: proxyArtifactBeacon, file: "proxy/beacon/BeaconProxy.sol", contract: "BeaconProxy", sources: oz561BeaconSources, immutableVariable: "_beacon"},
		{kind: proxyArtifactERC1967, file: "proxy/ERC1967/ERC1967Proxy.sol", contract: "ERC1967Proxy", sources: oz561ERC1967Sources},
		{kind: proxyArtifactProxyAdmin, file: "proxy/transparent/ProxyAdmin.sol", contract: "ProxyAdmin", sources: oz561ProxyAdminSources},
		{kind: proxyArtifactUpgradeableBeacon, file: "proxy/beacon/UpgradeableBeacon.sol", contract: "UpgradeableBeacon", sources: oz561UpgradeableBeaconSources},
	}
	for _, spec := range specs {
		if canonicalFile != spec.file || success.ContractName != spec.contract {
			continue
		}
		digest, ok := authenticateOpenZeppelin561Sources(success.Sources, spec.sources)
		if !ok {
			return recognizedProxyArtifact{}, false
		}
		artifact := recognizedProxyArtifact{
			Kind: spec.kind, StandardVersion: openZeppelin561Version,
			SourceManifestSHA256: digest,
		}
		if spec.immutableVariable != "" {
			id, ok := exactImmutableVariableID(
				success.Compilation.ImmutableVariables,
				spec.file, spec.contract, spec.immutableVariable,
			)
			if !ok || len(success.Runtime.ImmutableReferences) != 1 {
				return recognizedProxyArtifact{}, false
			}
			value, ok := authenticatedAddressImmutable(
				id,
				success.Runtime.ImmutableReferences,
				success.RuntimeMatch,
				actualRuntime,
			)
			if !ok {
				return recognizedProxyArtifact{}, false
			}
			artifact.RuntimeImmutable = &value
		}
		return artifact, true
	}
	if !hasLinearizedOpenZeppelinBase(
		success.Compilation.LinearizedBases,
		"proxy/utils/UUPSUpgradeable.sol", "UUPSUpgradeable",
	) || !validUUPSArtifactABI(success.ABI) {
		return recognizedProxyArtifact{}, false
	}
	digest, ok := authenticateOpenZeppelin561Sources(success.Sources, oz561UUPSSources)
	immutableID, immutableFound := exactImmutableVariableID(
		success.Compilation.ImmutableVariables,
		"proxy/utils/UUPSUpgradeable.sol", "UUPSUpgradeable", "__self",
	)
	immutableAddress, immutableOK := authenticatedAddressImmutable(
		immutableID, success.Runtime.ImmutableReferences, success.RuntimeMatch, actualRuntime,
	)
	if !ok || !immutableFound || !immutableOK || immutableAddress != targetAddress {
		return recognizedProxyArtifact{}, false
	}
	self := targetAddress
	return recognizedProxyArtifact{
		Kind: proxyArtifactUUPS, StandardVersion: openZeppelin561Version,
		RuntimeImmutable: &self, SourceManifestSHA256: digest,
	}, true
}

func authenticateOpenZeppelin561Sources(
	sources map[string]struct {
		Content string `json:"content"`
	},
	required []string,
) ([sha256.Size]byte, bool) {
	canonical := make(map[string][sha256.Size]byte)
	for name, source := range sources {
		path := canonicalOpenZeppelinSourcePath(name)
		if path == "" {
			continue
		}
		if _, exists := canonical[path]; exists {
			return [sha256.Size]byte{}, false
		}
		canonical[path] = sha256.Sum256([]byte(source.Content))
	}
	paths := append([]string(nil), required...)
	sort.Strings(paths)
	manifest := sha256.New()
	_, _ = manifest.Write([]byte("openzeppelin-contracts\x005.6.1\x00"))
	for _, path := range paths {
		expectedHex, exists := openZeppelin561SourceSHA256[path]
		actual, found := canonical[path]
		expected, decodeErr := hex.DecodeString(expectedHex)
		if !exists || !found || decodeErr != nil || !strings.EqualFold(hex.EncodeToString(actual[:]), expectedHex) {
			return [sha256.Size]byte{}, false
		}
		_, _ = manifest.Write([]byte(path))
		_, _ = manifest.Write([]byte{0})
		_, _ = manifest.Write(expected)
	}
	var digest [sha256.Size]byte
	copy(digest[:], manifest.Sum(nil))
	return digest, true
}

func canonicalOpenZeppelinSourcePath(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	prefixes := []string{
		"npm/@openzeppelin/contracts@5.6.1/",
		"project/node_modules/@openzeppelin/contracts/",
		"node_modules/@openzeppelin/contracts/",
		"@openzeppelin/contracts/",
		"contracts/",
	}
	var path string
	for _, prefix := range prefixes {
		if suffix, found := strings.CutPrefix(name, prefix); found {
			path = suffix
			break
		}
	}
	if path == "" || strings.HasPrefix(path, "/") {
		return ""
	}
	for component := range strings.SplitSeq(path, "/") {
		if component == "" || component == "." || component == ".." {
			return ""
		}
	}
	return path
}

func exactImmutableVariableID(
	variables map[string]string,
	file, contract, variable string,
) (string, bool) {
	var found string
	for id, identity := range variables {
		last := strings.LastIndex(identity, ":")
		if last <= 0 || identity[last+1:] != variable {
			continue
		}
		contractEnd := last
		contractStart := strings.LastIndex(identity[:contractEnd], ":")
		if contractStart <= 0 || identity[contractStart+1:contractEnd] != contract ||
			canonicalOpenZeppelinSourcePath(identity[:contractStart]) != file {
			continue
		}
		if found != "" {
			return "", false
		}
		found = id
	}
	return found, found != ""
}

func authenticatedAddressImmutable(
	id string,
	references map[string][]bytecodeRange,
	match *VerificationMatchDetails,
	actualRuntime []byte,
) (common.Address, bool) {
	if id == "" || match == nil || len(actualRuntime) == 0 {
		return common.Address{}, false
	}
	spans, exists := references[id]
	if !exists || !validAddressImmutableReferences(spans) {
		return common.Address{}, false
	}
	value, ok := decodeImmutableAddress(match.Values.Immutables[id])
	if !ok {
		return common.Address{}, false
	}
	encoded, err := decodeBytecode(match.Values.Immutables[id])
	if err != nil || len(encoded) != common.HashLength {
		return common.Address{}, false
	}
	expectedOffsets := make(map[uint64]struct{}, len(spans))
	orderedSpans := append([]bytecodeRange(nil), spans...)
	sort.Slice(orderedSpans, func(i, j int) bool { return orderedSpans[i].Start < orderedSpans[j].Start })
	var priorEnd uint64
	for index, span := range orderedSpans {
		if span.Start > uint64(len(actualRuntime)) || span.Length > uint64(len(actualRuntime))-span.Start {
			return common.Address{}, false
		}
		if index > 0 && span.Start < priorEnd {
			return common.Address{}, false
		}
		if _, duplicate := expectedOffsets[span.Start]; duplicate {
			return common.Address{}, false
		}
		priorEnd = span.Start + span.Length
		start, end := int(span.Start), int(span.Start+span.Length)
		if !strings.EqualFold(hex.EncodeToString(actualRuntime[start:end]), hex.EncodeToString(encoded)) {
			return common.Address{}, false
		}
		expectedOffsets[span.Start] = struct{}{}
	}
	seen := make(map[uint64]struct{}, len(spans))
	for _, transformation := range match.Transformations {
		if transformation.ID != id {
			continue
		}
		if transformation.Type != "replace" || transformation.Reason != "immutable" {
			return common.Address{}, false
		}
		if _, expected := expectedOffsets[transformation.Offset]; !expected {
			return common.Address{}, false
		}
		if _, duplicate := seen[transformation.Offset]; duplicate {
			return common.Address{}, false
		}
		seen[transformation.Offset] = struct{}{}
	}
	return value, len(seen) == len(expectedOffsets)
}

func validAddressImmutableReferences(spans []bytecodeRange) bool {
	if len(spans) == 0 {
		return false
	}
	for _, span := range spans {
		if span.Length != common.HashLength {
			return false
		}
	}
	return true
}

func decodeImmutableAddress(value string) (common.Address, bool) {
	decoded, err := decodeBytecode(value)
	if err != nil || len(decoded) != common.HashLength {
		return common.Address{}, false
	}
	word := common.BytesToHash(decoded)
	for _, item := range word[:12] {
		if item != 0 {
			return common.Address{}, false
		}
	}
	address := common.BytesToAddress(word[12:])
	return address, address != (common.Address{})
}

func hasLinearizedOpenZeppelinBase(bases []string, file, contract string) bool {
	for _, base := range bases {
		parts := strings.SplitN(base, ":", 2)
		if len(parts) == 2 && canonicalOpenZeppelinSourcePath(parts[0]) == file && parts[1] == contract {
			return true
		}
	}
	return false
}

func validUUPSArtifactABI(raw json.RawMessage) bool {
	if !jsonArray(raw) {
		return false
	}
	parsed, err := gethabi.JSON(strings.NewReader(string(raw)))
	if err != nil {
		return false
	}
	uuid, uuidOK := parsed.Methods["proxiableUUID"]
	version, versionOK := parsed.Methods["UPGRADE_INTERFACE_VERSION"]
	upgrade, upgradeOK := parsed.Methods["upgradeToAndCall"]
	return uuidOK && versionOK && upgradeOK &&
		uuid.Sig == "proxiableUUID()" && uuid.StateMutability == "view" &&
		len(uuid.Inputs) == 0 && len(uuid.Outputs) == 1 && uuid.Outputs[0].Type.String() == "bytes32" &&
		version.Sig == "UPGRADE_INTERFACE_VERSION()" && version.StateMutability == "view" &&
		len(version.Inputs) == 0 && len(version.Outputs) == 1 && version.Outputs[0].Type.String() == "string" &&
		upgrade.Sig == "upgradeToAndCall(address,bytes)" && upgrade.StateMutability == "payable" &&
		len(upgrade.Inputs) == 2 && upgrade.Inputs[0].Type.String() == "address" &&
		upgrade.Inputs[1].Type.String() == "bytes" && len(upgrade.Outputs) == 0
}

func validateRecognizedProxyArtifact(artifact recognizedProxyArtifact) error {
	if artifact.StandardVersion != openZeppelin561Version || artifact.SourceManifestSHA256 == [sha256.Size]byte{} {
		return errors.New("recognized proxy artifact identity is incomplete")
	}
	switch artifact.Kind {
	case proxyArtifactTransparent, proxyArtifactBeacon, proxyArtifactUUPS:
		if artifact.RuntimeImmutable == nil || *artifact.RuntimeImmutable == (common.Address{}) {
			return errors.New("recognized proxy artifact immutable is missing")
		}
	case proxyArtifactERC1967, proxyArtifactProxyAdmin, proxyArtifactUpgradeableBeacon:
		if artifact.RuntimeImmutable != nil {
			return errors.New("recognized proxy artifact has an unexpected immutable")
		}
	default:
		return errors.New("recognized proxy artifact kind is invalid")
	}
	return nil
}
