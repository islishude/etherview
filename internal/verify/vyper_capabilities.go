package verify

import (
	_ "embed"
	"encoding/json"
	"errors"
	"slices"
)

// VyperCapabilities records settings tested against an exact official compiler.
type VyperCapabilities struct {
	OptimizationModes []string `json:"optimization_modes"`
	EVMVersions       []string `json:"evm_versions"`
	DefaultEVMVersion string   `json:"default_evm_version"`
	BytecodeMetadata  bool     `json:"bytecode_metadata"`
	EnableDecimals    bool     `json:"enable_decimals"`
}

//go:embed vyper_capabilities.json
var vyperCapabilitiesJSON []byte

func VyperVersionCapabilities(version string) (VyperCapabilities, bool) {
	var profiles map[string]VyperCapabilities
	if json.Unmarshal(vyperCapabilitiesJSON, &profiles) != nil {
		return VyperCapabilities{}, false
	}
	profile, ok := profiles[normalizeCompilerVersion(version)]
	return profile, ok
}

func validateVyperSettings(settings map[string]any, profile VyperCapabilities) error {
	invalid := errors.New("unsupported Vyper compiler setting")
	for name, value := range settings {
		switch name {
		case "evmVersion":
			text, ok := value.(string)
			if !ok || !slices.Contains(profile.EVMVersions, text) {
				return invalid
			}
		case "optimize":
			if flag, ok := value.(bool); ok {
				if slices.Contains(profile.OptimizationModes, "codesize") {
					return invalid
				}
				// Historical Standard JSON used a boolean; retain its meaning explicitly.
				value = "none"
				if flag {
					value = "gas"
				}
			}
			text, ok := value.(string)
			if !ok || !slices.Contains(profile.OptimizationModes, text) {
				return invalid
			}
			settings[name] = value
		case "bytecodeMetadata":
			if !profile.BytecodeMetadata {
				return invalid
			}
		case "enable_decimals":
			if !profile.EnableDecimals {
				return invalid
			}
		}
	}
	return nil
}
