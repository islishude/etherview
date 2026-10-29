package enssync

import (
	"bytes"
	"encoding/json"
	"io"
)

func ReadManifest(reader io.Reader) (Manifest, error) {
	raw, e := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
	if e != nil || len(raw) > 1<<20 {
		return Manifest{}, ErrInvalidInput
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var input Manifest
	if decoder.Decode(&input) != nil {
		return Manifest{}, ErrInvalidInput
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return Manifest{}, ErrInvalidInput
	}
	m, _, _, e := validateManifest(input)
	return m, e
}
