package verify

import (
	"encoding/json"
	"errors"
	"io/fs"
	"path"
	"strings"
	"unicode/utf8"
)

const (
	GeasCompilerVersion = "0.3.3"
	maxGeasSources      = 1024
	maxGeasSourcePath   = 384
	maxGeasContractName = 256
)

// GeasRequest is the durable, compiler-specific portion of an address
// verification submission. Sources are always inline and addressed through a
// virtual filesystem; entrypoints can never name host files.
type GeasRequest struct {
	Sources            map[string]string `json:"sources"`
	RuntimeEntrypoint  string            `json:"runtime_entrypoint"`
	CreationEntrypoint string            `json:"creation_entrypoint,omitempty"`
}

func prepareGeasRequest(
	request *GeasRequest,
	contractName *string,
	maxInputBytes int,
) error {
	if request == nil || len(request.Sources) == 0 || len(request.Sources) > maxGeasSources {
		return errors.New("geas sources are invalid")
	}
	if maxInputBytes <= 0 {
		maxInputBytes = defaultCompilerInputBytes
	}
	canonical := make(map[string]string, len(request.Sources))
	for name, content := range request.Sources {
		if !validGeasSourcePath(name) || !utf8.ValidString(content) {
			return errors.New("geas source is invalid")
		}
		canonical[name] = content
	}
	request.Sources = canonical
	if !validGeasSourcePath(request.RuntimeEntrypoint) {
		return errors.New("geas runtime entrypoint is invalid")
	}
	if _, ok := request.Sources[request.RuntimeEntrypoint]; !ok {
		return errors.New("geas runtime entrypoint is missing from sources")
	}
	if request.CreationEntrypoint != "" {
		if !validGeasSourcePath(request.CreationEntrypoint) {
			return errors.New("geas creation entrypoint is invalid")
		}
		if _, ok := request.Sources[request.CreationEntrypoint]; !ok {
			return errors.New("geas creation entrypoint is missing from sources")
		}
	}
	name := strings.TrimSpace(*contractName)
	if name == "" {
		name = strings.TrimSuffix(path.Base(request.RuntimeEntrypoint), path.Ext(request.RuntimeEntrypoint))
	}
	if !validGeasContractName(name) {
		return errors.New("geas contract name is invalid")
	}
	*contractName = name
	encoded, err := json.Marshal(request)
	if err != nil || len(encoded) > maxInputBytes {
		return errors.New("geas sources exceed the input limit")
	}
	return nil
}

func validGeasSourcePath(name string) bool {
	if name == "" || name == "." || len(name) > maxGeasSourcePath ||
		strings.Contains(name, "\\") || path.Clean(name) != name || !fs.ValidPath(name) {
		return false
	}
	for _, character := range name {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func validGeasContractName(name string) bool {
	if name == "" || len(name) > maxGeasContractName || !utf8.ValidString(name) {
		return false
	}
	for _, character := range name {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}
