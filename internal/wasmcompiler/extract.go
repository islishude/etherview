// Package wasmcompiler implements the fixed compiler guest ABI, not a general
// JavaScript or WASI execution service. ADR-0048 owns its trust boundary.
package wasmcompiler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"regexp"
	"strconv"

	"github.com/pierrec/lz4/v4"
	wasmbin "github.com/tetratelabs/wabin/binary"
	"github.com/tetratelabs/wabin/wasm"
)

const MaxArtifactBytes = 200 << 20
const MaxWasmBytes = 256 << 20

var ErrArtifact = errors.New("invalid compiler artifact")
var ErrUnsupported = errors.New("unsupported compiler artifact ABI")

// Artifact records mappings extracted from authenticated soljson. Binary is the
// original compiler WASM, not a rebuilt or rewritten compiler.
type Artifact struct {
	ModernExceptions     bool
	ExceptionHeader      uint32
	CatchObject          bool
	ExceptionAttrs       bool
	DestructorTrampoline string
	Binary               []byte
	Imports              map[string]string
	Exports              map[string]string
	Constants            map[string]uint32
	Module               *wasm.Module
}

var (
	importsPattern  = regexp.MustCompile(`(?:var )?(?:asmLibraryArg|wasmImports)\s*=\s*\{([^{}]+)\}`)
	bindingPattern  = regexp.MustCompile(`"([^"\\]+)"\s*:\s*([A-Za-z_$][A-Za-z0-9_$]*|[0-9]+)`)
	exportPattern   = regexp.MustCompile(`Module\["([^"\\]+)"\]\s*=\s*asm\["([^"\\]+)"\]`)
	constantPattern = regexp.MustCompile(`\b(STACK_BASE|STACK_MAX|DYNAMIC_BASE|DYNAMICTOP_PTR|TOTAL_MEMORY|INITIAL_MEMORY|jsCallStartIndex)\s*=\s*([0-9]+)`)
)

// Extract accepts only authenticated, bounded official compiler data. It does
// not evaluate JavaScript. Unsupported packaging is never executed as fallback.
func Extract(ctx context.Context, raw []byte, expected [32]byte) (*Artifact, error) {
	if len(raw) == 0 || len(raw) > MaxArtifactBytes || sha256.Sum256(raw) != expected {
		return nil, ErrArtifact
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	binary, glue, err := payload(ctx, raw)
	if err != nil {
		return nil, err
	}
	raw = glue
	a := &Artifact{Binary: binary, ModernExceptions: bytes.Contains(raw, []byte("function ExceptionInfo(")), Imports: map[string]string{}, Exports: map[string]string{}, Constants: map[string]uint32{}}
	if a.ModernExceptions {
		a.ExceptionHeader = 24
	}
	a.CatchObject = bytes.Contains(raw, []byte("function CatchInfo("))
	a.ExceptionAttrs = bytes.Contains(raw, []byte("var ExceptionInfoAttrs=")) || bytes.Contains(raw, []byte("var ExceptionInfoAttrs ="))
	if a.CatchObject {
		a.ExceptionHeader = 16
	}
	if a.ModernExceptions && !a.ExceptionAttrs {
		m := regexp.MustCompile(`this\.ptr\s*=\s*excPtr\s*-\s*([0-9]+)`).FindSubmatch(raw)
		if m == nil {
			return nil, ErrUnsupported
		}
		n, e := strconv.ParseUint(string(m[1]), 10, 32)
		if e != nil || n != uint64(a.ExceptionHeader) {
			return nil, ErrUnsupported
		}
	}
	if a.ExceptionAttrs {
		m := regexp.MustCompile(`ExceptionInfoAttrs\s*=\s*\{([^}]+)\}`).FindSubmatch(raw)
		if m == nil {
			return nil, ErrUnsupported
		}
		for _, entry := range []struct{ name, value string }{{"SIZE", "16"}, {"TYPE_OFFSET", "8"}, {"DESTRUCTOR_OFFSET", "0"}, {"REFCOUNT_OFFSET", "4"}} {
			re := regexp.MustCompile(`\b` + entry.name + `\s*:\s*` + entry.value + `\b`)
			if !re.Match(m[1]) {
				return nil, ErrUnsupported
			}
		}
	}
	if m := regexp.MustCompile(`var buffer\s*=\s*([0-9]+);\s*HEAP32\[buffer`).FindSubmatch(raw); m != nil {
		n, e := strconv.ParseUint(string(m[1]), 10, 32)
		if e != nil {
			return nil, ErrArtifact
		}
		a.Constants["catchBuffer"] = uint32(n)
	}
	if m := regexp.MustCompile(`Module\["(dynCall_[iv]i)"\]\s*\(\s*info\.destructor`).FindSubmatch(raw); m != nil {
		a.DestructorTrampoline = string(m[1])
	}
	if err != nil || len(a.Binary) > MaxWasmBytes || !bytes.HasPrefix(a.Binary, []byte{0, 97, 115, 109, 1, 0, 0, 0}) {
		return nil, ErrArtifact
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	a.Module, err = wasmbin.DecodeModule(a.Binary, wasm.CoreFeaturesV2)
	if err != nil {
		return nil, ErrArtifact
	}
	matches := importsPattern.FindAllSubmatch(raw, 2)
	if len(matches) != 1 {
		return nil, ErrUnsupported
	}
	for _, m := range bindingPattern.FindAllSubmatch(matches[0][1], -1) {
		a.Imports[string(m[1])] = string(m[2])
	}
	for _, m := range exportPattern.FindAllSubmatch(raw, -1) {
		a.Exports[string(m[1])] = string(m[2])
	}
	for _, m := range constantPattern.FindAllSubmatch(raw, -1) {
		v, e := strconv.ParseUint(string(m[2]), 10, 32)
		if e != nil {
			return nil, ErrArtifact
		}
		a.Constants[string(m[1])] = uint32(v)
	}
	if len(a.Exports) == 0 {
		return nil, ErrUnsupported
	}
	a.Module.CodeSection = nil
	a.Module.DataSection = nil
	a.Module.CustomSections = nil
	a.Module.NameSection = nil
	return a, nil
}
func unpack(ctx context.Context, src []byte, size int) ([]byte, error) {
	dst := make([]byte, size)
	offset := 0
	for len(src) >= 4 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n := binary.LittleEndian.Uint32(src)
		src = src[4:]
		if n == 0 {
			if offset != size || len(src) > 3 {
				return nil, ErrArtifact
			}
			return dst, nil
		}
		plain := n&0x80000000 != 0
		n &= 0x7fffffff
		if n > uint32(len(src)) {
			return nil, io.ErrUnexpectedEOF
		}
		var written int
		if plain {
			if int(n) > len(dst)-offset {
				return nil, ErrArtifact
			}
			written = copy(dst[offset:], src[:n])
		} else {
			var err error
			written, err = lz4.UncompressBlock(src[:n], dst[offset:])
			if err != nil || written <= 0 {
				return nil, ErrArtifact
			}
		}
		offset += written
		src = src[n:]
	}
	if offset != size || len(src) != 0 {
		return nil, ErrArtifact
	}
	return dst, nil
}
