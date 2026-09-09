package wasmcompiler

import (
	"bytes"
	"context"
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"
)

var packedStart = regexp.MustCompile(`\}\)\(\s*"`)
var packedSize = regexp.MustCompile(`^\s*,\s*([0-9]+)\s*\)`)

// payload separates the large data literal from its small glue program. Binding
// scanners never repeatedly traverse megabytes of base64 compiler bytes.
func payload(ctx context.Context, raw []byte) (binary, glue []byte, err error) {
	const prefix = "data:application/octet-stream;base64,"
	marker := []byte(prefix + "AGFzbQ")
	if index := bytes.Index(raw, marker); index >= 0 {
		start := index + len(prefix)
		end := bytes.IndexByte(raw[start:], '"')
		if end < 0 {
			return nil, nil, ErrArtifact
		}
		end += start
		if bytes.Contains(raw[end:], marker) {
			return nil, nil, ErrArtifact
		}
		binary, err = base64.StdEncoding.DecodeString(string(raw[start:end]))
		if err != nil {
			return nil, nil, ErrArtifact
		}
		glue = append(bytes.Clone(raw[:start]), raw[end:]...)
		return binary, glue, nil
	}
	// The fixed block-LZ4 decoder precedes its data literal in supported builds.
	location := packedStart.FindIndex(raw[:min(len(raw), 16<<10)])
	if location == nil || !bytes.Contains(raw[:location[0]], []byte("uncompress")) {
		return nil, nil, ErrUnsupported
	}
	start := location[1]
	end := bytes.IndexByte(raw[start:], '"')
	if end < 0 {
		return nil, nil, ErrArtifact
	}
	end += start
	if bytes.ContainsAny(raw[start:end], "\\\r\n") {
		return nil, nil, ErrArtifact
	}
	size := packedSize.FindSubmatch(raw[end+1 : min(len(raw), end+128)])
	if size == nil {
		return nil, nil, ErrUnsupported
	}
	n, e := strconv.ParseUint(string(size[1]), 10, 32)
	if e != nil || n == 0 || n > MaxWasmBytes {
		return nil, nil, ErrArtifact
	}
	compressed, e := base64.RawStdEncoding.DecodeString(strings.TrimRight(string(raw[start:end]), "="))
	if e != nil {
		return nil, nil, ErrArtifact
	}
	binary, err = unpack(ctx, compressed, int(n))
	if err != nil {
		return nil, nil, err
	}
	glue = append(bytes.Clone(raw[:start]), raw[end:]...)
	if len(packedStart.FindAllIndex(glue, -1)) != 1 {
		return nil, nil, ErrUnsupported
	}
	return binary, glue, nil
}
