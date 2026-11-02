package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestFilenameRoundtrip(t *testing.T) {
	for _, name := range []string{"v1.0+build.json", "literal%61.json", "100%.json", "my file.json", "token#1?.json", "文档.json"} {
		for _, wrap := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/wrap=%t", name, wrap), func(t *testing.T) {
				content := []byte{0, 255, 128, 1}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost {
						t.Errorf("method = %s", r.Method)
					}
					switch r.URL.Path {
					case "/api/v0/add":
						reader, err := r.MultipartReader()
						if err != nil {
							t.Error(err)
							return
						}
						part, err := reader.NextPart()
						if err != nil {
							t.Error(err)
							return
						}
						// Match Kubo's filename decoding, not just net/http's parser.
						decoded, err := url.QueryUnescape(part.FileName())
						if err != nil || decoded != name {
							t.Errorf("Kubo filename = %q, want %q: %v", decoded, name, err)
						}
						got, err := io.ReadAll(part)
						if err != nil || !bytes.Equal(got, content) {
							t.Errorf("upload content = %v: %v", got, err)
						}
						if _, err := reader.NextPart(); err != io.EOF {
							t.Errorf("multipart end: %v", err)
						}
						_, _ = fmt.Fprintf(w, `{"Name":%q,"Hash":%q}`, decoded, testCID)
						if wrap {
							_, _ = fmt.Fprintf(w, "\n"+`{"Name":"","Hash":%q}`, testCID)
						}
					case "/api/v0/cat":
						want := "/ipfs/" + testCID
						if wrap {
							want += "/" + name
						}
						if got := r.URL.Query().Get("arg"); got != want {
							t.Errorf("RPC path = %q, want %q", got, want)
						}
						_, _ = w.Write(content)
					default:
						t.Errorf("unexpected RPC %s", r.URL.Path)
					}
				}))
				defer server.Close()
				input := filepath.Join(t.TempDir(), name)
				mustWrite(t, input, content)
				args := []string{"upload", "--api", server.URL}
				if wrap {
					args = append(args, "--wrap")
				}
				var stdout, stderr bytes.Buffer
				if code := run(t.Context(), append(args, input), &stdout, &stderr); code != 0 || stdout.String() != testCID+"\n" {
					t.Fatalf("upload exit=%d stdout=%q stderr=%s", code, &stdout, &stderr)
				}
				raw := testCID
				uri := &url.URL{Scheme: "ipfs", Host: testCID}
				if wrap {
					raw += "/" + name
					uri.Path = "/" + name
				}
				for _, source := range []string{raw, uri.String()} {
					output := filepath.Join(t.TempDir(), "download")
					stdout.Reset()
					stderr.Reset()
					code := run(t.Context(), []string{"download", "--api", server.URL, "--output", output, source}, &stdout, &stderr)
					got, err := os.ReadFile(output)
					if code != 0 || err != nil || !bytes.Equal(got, content) {
						t.Fatalf("download %q: exit=%d read=%v stderr=%s", source, code, err, &stderr)
					}
				}
			})
		}
	}
}

func TestContentPathEncoding(t *testing.T) {
	for _, test := range []struct{ input, path string }{
		{testCID, "/ipfs/" + testCID},
		{"ipfs://" + testCID, "/ipfs/" + testCID},
		{testCID + "/literal%20+?#.json", "/ipfs/" + testCID + "/literal%20+?#.json"},
		{testCID + "/%2e%2e/file", "/ipfs/" + testCID + "/%2e%2e/file"},
		{"ipfs://" + testCID + "/my%20file.json", "/ipfs/" + testCID + "/my file.json"},
		{"ipfs://" + testCID + "/v1.0+build.json", "/ipfs/" + testCID + "/v1.0+build.json"},
		{"ipfs://" + testCID + "/token%231%3F.json", "/ipfs/" + testCID + "/token#1?.json"},
		{"ipfs://" + testCID + "/literal%2561.json", "/ipfs/" + testCID + "/literal%61.json"},
		{"ipfs://" + testCID + "/%252e%252e/file", "/ipfs/" + testCID + "/%2e%2e/file"},
	} {
		got, err := contentPath(test.input)
		if err != nil || got != test.path {
			t.Errorf("%q: got %q, want %q: %v", test.input, got, test.path, err)
		}
	}
	for _, suffix := range []string{
		"/../file", "/./file", "/%2e%2e/file", "/.%2e/file", "/%2E/file",
		"/a%2fb", "/a%2Fb", "/a%5cb", "/file%00", "/file%0a", "/file%7f",
		"/bad%", "/bad%zz", "//file", "/", "/file?arg=x", "/file?", "/file#x", "/file#",
	} {
		if _, err := contentPath("ipfs://" + testCID + suffix); err == nil {
			t.Errorf("invalid URI accepted: %q", suffix)
		}
	}
	for _, authority := range []string{"", "user@" + testCID, testCID + ":5001", "invalid"} {
		if _, err := contentPath("ipfs://" + authority + "/file"); err == nil {
			t.Errorf("invalid authority accepted: %q", authority)
		}
	}
}
