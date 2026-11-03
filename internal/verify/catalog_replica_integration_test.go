//go:build integration

package verify

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCompilerCatalogReplicasFenceLostSessionAndDelayedRefresh(t *testing.T) {
	db := bytecodeDBPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	build := func(version, commit string) string {
		return `{"path":"soljson-v` + version + `+commit.` + commit + `.js","version":"` + version + `","longVersion":"` + version + `+commit.` + commit + `","sha256":"` + strings.Repeat("42", 32) + `"}`
	}
	first := build("0.8.30", "73712a01")
	second := build("0.8.31", "cccccccc")
	entered, release := make(chan struct{}), make(chan struct{})
	makeCatalog := func(raw string, blocked bool) *CompilerCatalog {
		client := &http.Client{Transport: vyperTestTransport(func(r *http.Request) (*http.Response, error) {
			if blocked {
				close(entered)
				select {
				case <-release:
				case <-r.Context().Done():
					return nil, r.Context().Err()
				}
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(raw))}, nil
		})}
		c, e := NewCompilerCatalog(db, CompilerCatalogOptions{Sources: map[Language]string{LanguageSolidity: "https://compiler.example/list.json"}, AllowedOrigins: []string{"https://compiler.example"}, unsafeHTTPClient: client})
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	a := makeCatalog(`{"builds":[`+first+`],"releases":{}}`, true)
	b := makeCatalog(`{"builds":[`+first+`,`+second+`],"releases":{}}`, false)
	done := make(chan error, 1)
	go func() { _, e := a.Refresh(ctx, LanguageSolidity); done <- e }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, e := b.Refresh(ctx, LanguageSolidity); !errors.Is(e, ErrCompilerCatalogRefreshBusy) {
		t.Fatalf("contender error=%v", e)
	}
	var killed bool
	if e := db.QueryRow(ctx, `SELECT pg_terminate_backend(pid,5000) FROM pg_locks WHERE locktype='advisory' AND granted AND objsubid=1 AND classid=((hashtextextended(current_schema()||':etherview:compiler-catalog:solidity',0)>>32)&4294967295)::oid AND objid=(hashtextextended(current_schema()||':etherview:compiler-catalog:solidity',0)&4294967295)::oid`).Scan(&killed); e != nil || !killed {
		t.Fatalf("terminate catalog session %v %v", killed, e)
	}
	if _, e := b.Refresh(ctx, LanguageSolidity); e != nil {
		t.Fatal(e)
	}
	versions, e := b.Versions(ctx, LanguageSolidity)
	if e != nil || len(versions) != 2 {
		t.Fatalf("new versions=%v err=%v", versions, e)
	}
	close(release)
	if e := <-done; e == nil {
		t.Fatal("stale catalog refreshed after loss of session")
	}
	versions, e = b.Versions(ctx, LanguageSolidity)
	if e != nil || len(versions) != 2 {
		t.Fatalf("stale catalog changed versions: versions=%v err=%v", versions, e)
	}

}
