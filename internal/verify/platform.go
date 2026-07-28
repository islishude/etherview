package verify

import (
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"errors"
	"net/url"
	"os"
	"runtime"
	"strings"
)

const (
	CompilerPlatformBin              = "bin"
	CompilerPlatformEmscriptenASMJS  = "emscripten-asmjs"
	CompilerPlatformEmscriptenWASM32 = "emscripten-wasm32"
	CompilerPlatformLinuxAMD64       = "linux-amd64"
	CompilerPlatformLinuxARM64       = "linux-arm64"
	CompilerPlatformMacOSAMD64       = "macosx-amd64"
	CompilerPlatformWASM             = "wasm"
	CompilerPlatformWindowsAMD64     = "windows-amd64"

	automaticCatalogSource = "auto"
	defaultVyperCatalogURL = "https://raw.githubusercontent.com/blockscout/solc-bin/main/vyper.list.json"
)

var supportedSolcPlatforms = map[string]struct{}{
	CompilerPlatformBin:              {},
	CompilerPlatformEmscriptenASMJS:  {},
	CompilerPlatformEmscriptenWASM32: {},
	CompilerPlatformLinuxAMD64:       {},
	CompilerPlatformLinuxARM64:       {},
	CompilerPlatformMacOSAMD64:       {},
	CompilerPlatformWASM:             {},
	CompilerPlatformWindowsAMD64:     {},
}

// NativeCompilerPlatform selects the best native platform published by
// solc-bin for this process. macOS arm64 intentionally selects the published
// macOS amd64 build, which can run under Rosetta; solc-bin has no macOS arm64
// directory.
func NativeCompilerPlatform() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		return CompilerPlatformLinuxAMD64, nil
	case "linux/arm64":
		return CompilerPlatformLinuxARM64, nil
	case "darwin/amd64", "darwin/arm64":
		return CompilerPlatformMacOSAMD64, nil
	case "windows/amd64":
		return CompilerPlatformWindowsAMD64, nil
	default:
		return "", errors.New("host has no native solc-bin platform")
	}
}

func resolveCatalogSource(language Language, configured, platform string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured != "" && configured != automaticCatalogSource {
		return configured, nil
	}
	switch language {
	case LanguageSolidity:
		if !validCompilerPlatform(platform) {
			return "", errors.New("solidity compiler platform is unsupported")
		}
		return "https://binaries.soliditylang.org/" + platform + "/list.json", nil
	case LanguageVyper:
		return defaultVyperCatalogURL, nil
	default:
		return "", errors.New("compiler catalog language is unsupported")
	}
}

func catalogArtifactPlatform(language Language, source string) (string, error) {
	if language == LanguageVyper {
		// The Blockscout Vyper catalog currently references the historical
		// PyInstaller `.linux` release assets, which are Linux x86-64.
		return CompilerPlatformLinuxAMD64, nil
	}
	parsed, err := url.Parse(source)
	if err != nil {
		return "", errors.New("compiler catalog source URL is invalid")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for _, part := range parts {
		if validCompilerPlatform(part) {
			return part, nil
		}
	}
	return "", errors.New("compiler catalog platform cannot be determined")
}

func executablePlatform(path string) (string, error) {
	if file, err := elf.Open(path); err == nil {
		defer file.Close() //nolint:errcheck
		switch file.Machine {
		case elf.EM_X86_64:
			return CompilerPlatformLinuxAMD64, nil
		case elf.EM_AARCH64:
			return CompilerPlatformLinuxARM64, nil
		default:
			return "", errors.New("compiler ELF architecture is unsupported")
		}
	}
	if file, err := macho.Open(path); err == nil {
		defer file.Close() //nolint:errcheck
		if file.Cpu == macho.CpuAmd64 {
			return CompilerPlatformMacOSAMD64, nil
		}
		return "", errors.New("compiler Mach-O architecture is unsupported")
	}
	if file, err := pe.Open(path); err == nil {
		defer file.Close() //nolint:errcheck
		if file.Machine == pe.IMAGE_FILE_MACHINE_AMD64 {
			return CompilerPlatformWindowsAMD64, nil
		}
		return "", errors.New("compiler PE architecture is unsupported")
	}
	prefix := make([]byte, 512)
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open compiler artifact")
	}
	count, _ := file.Read(prefix)
	_ = file.Close()
	text := strings.TrimSpace(string(prefix[:count]))
	if strings.Contains(text, "WebAssembly") || strings.Contains(text, "Module") {
		return "", errors.New("solc-js catalog entries are not standalone compiler packages")
	}
	return "", errors.New("compiler artifact format is unsupported")
}

// ExecutablePlatform identifies a downloaded compiler without executing it.
func ExecutablePlatform(path string) (string, error) {
	return executablePlatform(path)
}

func compilerPlatformMatches(actual, expected string) bool {
	return actual == expected
}

func validCompilerPlatform(platform string) bool {
	_, ok := supportedSolcPlatforms[platform]
	return ok
}

func ociPlatform(platform string) (string, error) {
	switch platform {
	case CompilerPlatformLinuxAMD64:
		return "linux/amd64", nil
	case CompilerPlatformLinuxARM64:
		return "linux/arm64", nil
	default:
		return "", errors.New("compiler artifact cannot execute in the Linux container runner")
	}
}
