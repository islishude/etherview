package defiread

import (
	"bytes"
	"context"
	"encoding/base64"
	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/deficontract"
	"strings"
	"testing"
)

func TestCursorBoundAndIdentity(t *testing.T) {
	c := cursor{Version: 1, Owner: "0x" + strings.Repeat("12", 20), Protocol: deficontract.UniswapV3, Digest: strings.Repeat("ab", 32), AfterProtocol: "uniswap-v3", AfterDeployment: strings.Repeat("a", 64)}
	c.AfterKey = "0x" + strings.Repeat("34", 20) + ":115792089237316195423570985008687907853269984665640564039457584007913129639935:" + c.Owner
	for range 32 {
		c.Runs = append(c.Runs, uuid.NewString())
	}
	encoded, e := encodeCursor(c)
	if e != nil || len(encoded) > 1024 {
		t.Fatal(len(encoded), e)
	}
	got, e := decodeCursor(encoded)
	if e != nil || got.AfterKey != c.AfterKey || got.Owner != c.Owner || len(got.Runs) != 32 || got.Digest != c.Digest {
		t.Fatal(got, e)
	}
	for i := 0; i < len(encoded); i++ {
		if _, e := decodeCursor(encoded[:i]); e == nil {
			t.Fatalf("accepted truncation %d", i)
		}
	}
	reader := &Reader{Digest: c.Digest}
	_, e = reader.Positions(context.Background(), deficontract.Request{Owner: "0x" + strings.Repeat("56", 20), Protocol: c.Protocol, Cursor: encoded})
	if e != deficontract.ErrCursor {
		t.Fatal("owner not bound", e)
	}
}
func TestDisabledIsNotAnEmptyPortfolio(t *testing.T) {
	r := &Reader{}
	page, e := r.Positions(context.Background(), deficontract.Request{Owner: "0x" + strings.Repeat("12", 20)})
	if e != nil || len(page.Deployments) != 6 || page.Positions == nil {
		t.Fatal(page, e)
	}
	for _, d := range page.Deployments {
		if d.State != "not_configured" {
			t.Fatal(d)
		}
	}
}
func FuzzCursor(f *testing.F) {
	f.Add("")
	f.Add("AAAA")
	f.Fuzz(func(t *testing.T, s string) { _, _ = decodeCursor(s) })
}

func TestCursorRejectsInvalidDeploymentText(t *testing.T) {
	owner := "0x" + strings.Repeat("12", 20)
	c := cursor{Version: 1, Owner: owner, Protocol: deficontract.UniswapV3, Digest: strings.Repeat("ab", 32), Runs: []string{uuid.NewString()}, AfterProtocol: "uniswap-v3", AfterDeployment: "deployment-marker", AfterKey: "0x" + strings.Repeat("34", 20) + ":1:" + owner}
	encoded, err := encodeCursor(c)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	offset := bytes.Index(raw, []byte(c.AfterDeployment))
	if offset < 0 {
		t.Fatal("missing deployment")
	}
	for _, invalid := range []byte{0, 0xff, '/', 'A'} {
		mutated := append([]byte{}, raw...)
		mutated[offset] = invalid
		if _, err = decodeCursor(base64.RawURLEncoding.EncodeToString(mutated)); err != deficontract.ErrCursor {
			t.Fatalf("invalid deployment byte %x accepted: %v", invalid, err)
		}
	}
}
