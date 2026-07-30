package verify

import (
	"errors"
	"net/url"
	"strings"
)

const (
	CompilerPlatformEmscriptenWASM32 = "emscripten-wasm32"

	automaticCatalogSource = "auto"
)

// supportedSolcPlatforms includes values that may occur in immutable
// pre-0031 provenance. New catalog entries are constrained separately to
// emscripten-wasm32 and never select one of these historical CPU formats.
var supportedSolcPlatforms = map[string]struct{}{
	CompilerPlatformEmscriptenWASM32: {},
	"bin":                            {},
	"emscripten-asmjs":               {},
	"linux-amd64":                    {},
	"linux-arm64":                    {},
	"macosx-amd64":                   {},
	"wasm":                           {},
	"windows-amd64":                  {},
}

func resolveCatalogSource(language Language, configured, platform string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured != "" && configured != automaticCatalogSource {
		return configured, nil
	}
	switch language {
	case LanguageSolidity:
		if platform != CompilerPlatformEmscriptenWASM32 {
			return "", errors.New("solidity compiler platform must be emscripten-wasm32")
		}
		return "https://binaries.soliditylang.org/" + platform + "/list.json", nil
	default:
		return "", errors.New("compiler catalog language is unsupported")
	}
}

func catalogArtifactPlatform(language Language, source string) (string, error) {
	if language != LanguageSolidity {
		return "", errors.New("compiler catalog language is unsupported")
	}
	if _, err := url.Parse(source); err != nil {
		return "", errors.New("compiler catalog source URL is invalid")
	}
	return CompilerPlatformEmscriptenWASM32, nil
}

func validCompilerPlatform(platform string) bool {
	_, ok := supportedSolcPlatforms[platform]
	return ok
}
