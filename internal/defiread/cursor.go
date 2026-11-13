package defiread

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"io"
	"math/big"
	"regexp"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/deficontract"
)

var cursorDeployment = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// Fixed-width chain quantities keep a 32-deployment cursor inside the shared
// 1024-character API limit. No database writes are needed to freeze traversal.
func encodeCursor(c cursor) (string, error) {
	if len(c.Runs) > 32 || len(c.AfterDeployment) > 64 {
		return "", deficontract.ErrCursor
	}
	digest, e := hex.DecodeString(c.Digest)
	if e != nil || len(digest) != 32 {
		return "", deficontract.ErrCursor
	}
	key := strings.Split(c.AfterKey, ":")
	if len(key) != 3 || !common.IsHexAddress(key[0]) || !strings.EqualFold(key[2], c.Owner) {
		return "", deficontract.ErrCursor
	}
	var b bytes.Buffer
	b.WriteByte(1)
	b.Write(common.HexToAddress(c.Owner).Bytes())
	b.WriteByte(protocolIndex(c.Protocol))
	b.Write(digest)
	b.WriteByte(byte(len(c.Runs)))
	for _, id := range c.Runs {
		u, e := uuid.Parse(id)
		if e != nil {
			return "", deficontract.ErrCursor
		}
		b.Write(u[:])
	}
	b.WriteByte(protocolIndex(deficontract.Protocol(c.AfterProtocol)))
	b.WriteByte(byte(len(c.AfterDeployment)))
	b.WriteString(c.AfterDeployment)
	b.Write(common.HexToAddress(key[0]).Bytes())
	if key[1] == "" {
		b.WriteByte(0)
	} else {
		n, ok := new(big.Int).SetString(key[1], 10)
		if !ok || n.Sign() < 0 || n.BitLen() > 256 || n.String() != key[1] {
			return "", deficontract.ErrCursor
		}
		b.WriteByte(1)
		b.Write(n.FillBytes(make([]byte, 32)))
	}
	return base64.RawURLEncoding.EncodeToString(b.Bytes()), nil
}
func decodeCursor(text string) (cursor, error) {
	var c cursor
	if len(text) > 1024 {
		return c, deficontract.ErrCursor
	}
	raw, e := base64.RawURLEncoding.DecodeString(text)
	if e != nil {
		return c, deficontract.ErrCursor
	}
	r := bytes.NewReader(raw)
	read := func(n int) []byte {
		b := make([]byte, n)
		if _, err := io.ReadFull(r, b); err != nil {
			e = err
		}
		return b
	}
	c.Version = int(read(1)[0])
	c.Owner = strings.ToLower(common.BytesToAddress(read(20)).Hex())
	p := read(1)[0]
	c.Digest = hex.EncodeToString(read(32))
	count := int(read(1)[0])
	if count == 0 || count > 32 || int(p) > len(deficontract.Protocols()) {
		return c, deficontract.ErrCursor
	}
	if p > 0 {
		c.Protocol = deficontract.Protocols()[p-1]
	}
	for range count {
		u, err := uuid.FromBytes(read(16))
		if err != nil {
			return c, deficontract.ErrCursor
		}
		c.Runs = append(c.Runs, u.String())
	}
	after := read(1)[0]
	if after == 0 || int(after) > len(deficontract.Protocols()) {
		return c, deficontract.ErrCursor
	}
	c.AfterProtocol = string(deficontract.Protocols()[after-1])
	length := int(read(1)[0])
	if length > 64 {
		return c, deficontract.ErrCursor
	}
	c.AfterDeployment = string(read(length))
	if !cursorDeployment.MatchString(c.AfterDeployment) {
		return c, deficontract.ErrCursor
	}
	contract := common.BytesToAddress(read(20))
	hasID := read(1)[0]
	id := ""
	if hasID == 1 {
		id = new(big.Int).SetBytes(read(32)).String()
	} else if hasID != 0 {
		return c, deficontract.ErrCursor
	}
	c.AfterKey = strings.ToLower(contract.Hex()) + ":" + id + ":" + c.Owner
	if e != nil || r.Len() != 0 || c.Version != 1 {
		return c, deficontract.ErrCursor
	}
	return c, nil
}
func protocolIndex(p deficontract.Protocol) byte {
	for i, v := range deficontract.Protocols() {
		if p == v {
			return byte(i + 1)
		}
	}
	return 0
}
