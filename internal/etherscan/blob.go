package etherscan

import (
	"context"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
)

var ErrBlobUnavailable = errors.New("blob content unavailable")

func validateBlobParameters(values url.Values) error {
	raw := values.Get("index")
	index, err := strconv.ParseUint(raw, 10, 64)
	if !hashPattern.MatchString(values.Get("blockhash")) || err != nil || strconv.FormatUint(index, 10) != raw {
		return invalidParameter("invalid blob blockhash or index")
	}
	return nil
}
func (b *PostgresBackend) blobContent(ctx context.Context, values url.Values) (any, error) {
	if err := validateBlobParameters(values); err != nil {
		return nil, err
	}
	if b.blobReader == nil {
		return nil, ErrBlobUnavailable
	}
	index, _ := strconv.ParseUint(values.Get("index"), 10, 64)
	data, err := b.blobReader.BlobContent(ctx, values.Get("blockhash"), index)
	if err != nil {
		return nil, ErrBlobUnavailable
	}
	if len(data) != 131072 {
		return nil, ErrBlobUnavailable
	}
	return "0x" + hex.EncodeToString(data), nil
}
