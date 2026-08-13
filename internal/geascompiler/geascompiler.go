// Package geascompiler implements the narrow stdin/stdout protocol used by the
// standalone Geas helper. It intentionally has no host-file or network input.
package geascompiler

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fjl/geas/asm"
)

const (
	ProtocolSchema      = "etherview-geas-compiler-v1"
	CompilerVersion     = "0.3.3"
	MaximumInputBytes   = 64 << 20
	MaximumOutputBytes  = 64 << 20
	MaximumSources      = 1024
	MaximumSourcePath   = 384
	IncludeDepthLimit   = 64
	CompilationMaxError = 10
)

type Request struct {
	Schema     string            `json:"schema"`
	Sources    map[string]string `json:"sources"`
	Entrypoint string            `json:"entrypoint"`
}

type Response struct {
	Schema     string   `json:"schema"`
	Successful bool     `json:"successful"`
	Bytecode   string   `json:"bytecode,omitempty"`
	Sources    []string `json:"sources"`
}

func Run(arguments []string, stdin io.Reader, stdout, stderr io.Writer) (code int) {
	defer func() {
		if recover() != nil {
			_, _ = io.WriteString(stderr, "compiler runtime invariant failed\n")
			code = 2
		}
	}()
	if len(arguments) != 1 {
		return fail(stderr)
	}
	switch arguments[0] {
	case "--self-test":
		response, err := Compile(Request{
			Schema: ProtocolSchema, Sources: map[string]string{"selftest.eas": "push 1\n"},
			Entrypoint: "selftest.eas",
		})
		if err != nil || !response.Successful || response.Bytecode != "0x6001" ||
			len(response.Sources) != 1 || response.Sources[0] != "selftest.eas" {
			return fail(stderr)
		}
		return encode(stdout, response, stderr)
	case "--compile":
		body, err := io.ReadAll(io.LimitReader(stdin, MaximumInputBytes+1))
		if err != nil || len(body) == 0 || len(body) > MaximumInputBytes {
			return fail(stderr)
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		var request Request
		if decoder.Decode(&request) != nil {
			return fail(stderr)
		}
		var trailing any
		if !errors.Is(decoder.Decode(&trailing), io.EOF) {
			return fail(stderr)
		}
		response, err := Compile(request)
		if err != nil {
			return fail(stderr)
		}
		return encode(stdout, response, stderr)
	default:
		return fail(stderr)
	}
}

func Compile(request Request) (Response, error) {
	response := Response{Schema: ProtocolSchema, Sources: make([]string, 0)}
	if request.Schema != ProtocolSchema || len(request.Sources) == 0 ||
		len(request.Sources) > MaximumSources || !validPath(request.Entrypoint) {
		return response, errors.New("invalid Geas compiler request")
	}
	if _, ok := request.Sources[request.Entrypoint]; !ok {
		return response, errors.New("geas entrypoint is missing")
	}
	filesystem := &sourceFilesystem{
		sources: make(map[string][]byte, len(request.Sources)),
		read:    make(map[string]struct{}),
	}
	for name, content := range request.Sources {
		if !validPath(name) || !utf8.ValidString(content) {
			return response, errors.New("invalid Geas source")
		}
		filesystem.sources[name] = []byte(content)
	}
	compiler := asm.New(filesystem)
	compiler.SetStackCheck(true)
	compiler.SetIncludeDepthLimit(IncludeDepthLimit)
	compiler.SetMaxErrors(CompilationMaxError)
	bytecode := compiler.CompileFile(request.Entrypoint)
	response.Sources = filesystem.readPaths()
	if compiler.Failed() || bytecode == nil {
		return response, nil
	}
	if len(bytecode) > MaximumOutputBytes {
		return Response{}, errors.New("geas bytecode exceeds the helper limit")
	}
	response.Successful = true
	response.Bytecode = "0x" + hex.EncodeToString(bytecode)
	return response, nil
}

func validPath(name string) bool {
	if name == "" || name == "." || len(name) > MaximumSourcePath ||
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

func encode(stdout io.Writer, response Response, stderr io.Writer) int {
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if encoder.Encode(response) != nil {
		return fail(stderr)
	}
	return 0
}

func fail(stderr io.Writer) int {
	_, _ = io.WriteString(stderr, "compiler request failed\n")
	return 1
}

type sourceFilesystem struct {
	sources map[string][]byte
	read    map[string]struct{}
}

func (filesystem *sourceFilesystem) Open(name string) (fs.File, error) {
	content, err := filesystem.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return &sourceFile{name: path.Base(name), Reader: bytes.NewReader(content)}, nil
}

func (filesystem *sourceFilesystem) ReadFile(name string) ([]byte, error) {
	if !validPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	content, ok := filesystem.sources[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	filesystem.read[name] = struct{}{}
	return append([]byte(nil), content...), nil
}

func (filesystem *sourceFilesystem) readPaths() []string {
	paths := make([]string, 0, len(filesystem.read))
	for name := range filesystem.read {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	return paths
}

type sourceFile struct {
	name string
	*bytes.Reader
}

func (*sourceFile) Close() error { return nil }

func (file *sourceFile) Stat() (fs.FileInfo, error) {
	return sourceFileInfo{name: file.name, size: file.Size()}, nil
}

type sourceFileInfo struct {
	name string
	size int64
}

func (info sourceFileInfo) Name() string  { return info.name }
func (info sourceFileInfo) Size() int64   { return info.size }
func (sourceFileInfo) Mode() fs.FileMode  { return 0o444 }
func (sourceFileInfo) ModTime() time.Time { return time.Time{} }
func (sourceFileInfo) IsDir() bool        { return false }
func (sourceFileInfo) Sys() any           { return nil }

var _ fs.ReadFileFS = (*sourceFilesystem)(nil)

func (response Response) Equal(other Response) bool {
	return response.Schema == other.Schema && response.Successful == other.Successful &&
		response.Bytecode == other.Bytecode && slicesEqual(response.Sources, other.Sources)
}

func slicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (response Response) Validate(request Request) error {
	if response.Schema != ProtocolSchema || response.Sources == nil ||
		!sort.StringsAreSorted(response.Sources) || len(response.Sources) > len(request.Sources) {
		return errors.New("invalid Geas compiler response")
	}
	seen := make(map[string]struct{}, len(response.Sources))
	for _, name := range response.Sources {
		if _, ok := request.Sources[name]; !ok {
			return errors.New("geas compiler response names an unknown source")
		}
		if _, duplicate := seen[name]; duplicate {
			return errors.New("geas compiler response duplicates a source")
		}
		seen[name] = struct{}{}
	}
	if _, ok := seen[request.Entrypoint]; !ok {
		return errors.New("geas compiler response omits its entrypoint")
	}
	if response.Successful {
		decoded, err := hex.DecodeString(strings.TrimPrefix(response.Bytecode, "0x"))
		if err != nil || !strings.HasPrefix(response.Bytecode, "0x") || len(decoded) > MaximumOutputBytes {
			return errors.New("invalid Geas compiler bytecode")
		}
	} else if response.Bytecode != "" {
		return errors.New("failed Geas response contains bytecode")
	}
	return nil
}

func (response Response) String() string {
	return fmt.Sprintf("Geas compilation success=%t sources=%d", response.Successful, len(response.Sources))
}
