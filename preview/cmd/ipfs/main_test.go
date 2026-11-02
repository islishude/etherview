package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testCID = "bafybeibnsoufr2renqzsh347nrx54wcubt5lgkeivez63xvivplfwhtpym"

func TestUpload(t *testing.T) {
	for _, body := range [][]byte{nil, {0, 255, 1, 0, 128}, bytes.Repeat([]byte("large"), 65536)} {
		for _, wrap := range []bool{false, true} {
			t.Run(fmt.Sprintf("bytes=%d/wrap=%t", len(body), wrap), func(t *testing.T) {
				file := filepath.Join(t.TempDir(), "sample.bin")
				if err := os.WriteFile(file, body, 0o600); err != nil {
					t.Fatal(err)
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost || r.URL.Path != "/api/v0/add" {
						t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					}
					for key, expected := range map[string]string{
						"cid-version": "1", "hash": "sha2-256", "raw-leaves": "true", "pin": "true",
						"wrap-with-directory": fmt.Sprint(wrap), "chunker": "size-262144", "progress": "false",
					} {
						if r.URL.Query().Get(key) != expected {
							t.Errorf("query %s = %q", key, r.URL.Query().Get(key))
						}
					}
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
					got, err := io.ReadAll(part)
					if err != nil || !bytes.Equal(got, body) || part.FileName() != "sample.bin" {
						t.Errorf("multipart mismatch: %v", err)
					}
					if _, err := reader.NextPart(); err != io.EOF {
						t.Errorf("multipart end: %v", err)
					}
					_, _ = fmt.Fprintf(w, `{"Name":"sample.bin","Hash":%q}`, testCID)
					if wrap {
						_, _ = fmt.Fprintf(w, "\n"+`{"Name":"","Hash":%q}`, testCID)
					}
				}))
				defer server.Close()
				args := []string{"upload", "--api", server.URL}
				if wrap {
					args = append(args, "--wrap")
				}
				var out, stderr bytes.Buffer
				if code := run(t.Context(), append(args, file), &out, &stderr); code != 0 || out.String() != testCID+"\n" {
					t.Fatalf("exit=%d stdout=%q stderr=%s", code, &out, &stderr)
				}
			})
		}
	}
}

func TestDownload(t *testing.T) {
	for _, body := range [][]byte{nil, {0, 255, 1, 0, 128}, bytes.Repeat([]byte("large"), 65536)} {
		t.Run(fmt.Sprintf("bytes=%d", len(body)), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v0/cat" || r.URL.Query().Get("arg") != "/ipfs/"+testCID+"/sample.bin" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				_, _ = w.Write(body)
			}))
			defer server.Close()
			dir := t.TempDir()
			output := filepath.Join(dir, "download")
			var stdout, stderr bytes.Buffer
			code := run(t.Context(), []string{"download", "--api", server.URL, "--output", output, "ipfs://" + testCID + "/sample.bin"}, &stdout, &stderr)
			got, err := os.ReadFile(output)
			if code != 0 || err != nil || !bytes.Equal(got, body) || stdout.Len() != 0 {
				t.Fatalf("exit=%d read=%v stderr=%s", code, err, &stderr)
			}
			assertNoTemporaryFiles(t, dir)
		})
	}
}

func TestUploadEarlyStreamingResponse(t *testing.T) {
	input := filepath.Join(t.TempDir(), "large.bin")
	body := bytes.Repeat([]byte{0, 255, 1}, 1<<20)
	mustWrite(t, input, body)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
			t.Error(err)
			return
		}
		w.WriteHeader(http.StatusOK)
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
			return
		}
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
		got, err := io.ReadAll(part)
		if err != nil || !bytes.Equal(got, body) {
			t.Errorf("streamed request truncated: %v (%d bytes)", err, len(got))
			return
		}
		if _, err := reader.NextPart(); err != io.EOF {
			t.Errorf("multipart end: %v", err)
			return
		}
		_, _ = fmt.Fprintf(w, `{"Name":"large.bin","Hash":%q}`, testCID)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), []string{"upload", "--api", server.URL, input}, &stdout, &stderr); code != 0 || stdout.String() != testCID+"\n" {
		t.Fatalf("streaming upload exit=%d: %s", code, &stderr)
	}
}

func TestDownloadFailuresPreserveOutput(t *testing.T) {
	for _, mode := range []string{"status", "truncated", "trailer", "cancel", "exists", "concurrent-create", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			output := filepath.Join(dir, "download")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "exists" {
				mustWrite(t, output, []byte("original"))
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "exists":
					t.Error("existing output should reject before RPC")
				case "status":
					http.Error(w, "secret upstream details", 500)
				case "truncated":
					w.Header().Set("Content-Length", "1000")
					_, _ = w.Write([]byte("partial"))
				case "trailer":
					w.Header().Set("Trailer", "X-Stream-Error")
					_, _ = w.Write([]byte("partial"))
					w.Header().Set("X-Stream-Error", "secret error")
				case "cancel":
					cancel()
					<-r.Context().Done()
				case "concurrent-create":
					mustWrite(t, output, []byte("original"))
					_, _ = w.Write([]byte("new"))
				case "redirect":
					http.Redirect(w, r, "/secret", http.StatusFound)
				}
			}))
			defer server.Close()
			var stdout, stderr bytes.Buffer
			code := run(ctx, []string{"download", "--api", server.URL, "--output", output, testCID}, &stdout, &stderr)
			if code != 1 || strings.Contains(stderr.String(), "secret") || strings.Contains(stderr.String(), server.URL) {
				t.Fatalf("exit=%d stderr=%s", code, &stderr)
			}
			got, err := os.ReadFile(output)
			if mode == "exists" || mode == "concurrent-create" {
				if err != nil || string(got) != "original" {
					t.Fatalf("original overwritten: %q, %v", got, err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("failed download published output: %v", err)
			}
			assertNoTemporaryFiles(t, dir)
		})
	}
}

func TestUploadFailures(t *testing.T) {
	for _, mode := range []string{"directory", "symlink", "early-status", "cancel", "timeout", "trailer", "malformed", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "file")
			mustWrite(t, input, bytes.Repeat([]byte{1}, 1<<20))
			if mode == "directory" {
				input = dir
			}
			if mode == "symlink" {
				link := filepath.Join(dir, "link")
				if err := os.Symlink(input, link); err != nil {
					t.Fatal(err)
				}
				input = link
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "directory", "symlink":
					t.Error("invalid file must not make RPC requests")
				case "early-status":
					w.WriteHeader(500)
				case "cancel":
					cancel()
				case "timeout":
					time.Sleep(100 * time.Millisecond)
				default:
					_, _ = io.Copy(io.Discard, r.Body)
					switch mode {
					case "trailer":
						w.Header().Set("Trailer", "X-Stream-Error")
						_, _ = fmt.Fprintf(w, `{"Name":"file","Hash":%q}`, testCID)
						w.Header().Set("X-Stream-Error", "secret error")
					case "truncated":
						w.Header().Set("Content-Length", "1000")
						_, _ = fmt.Fprintf(w, `{"Name":"file","Hash":%q}`, testCID)
					default:
						_, _ = io.WriteString(w, `{"Hash":"bad"}`)
					}
				}
			}))
			defer server.Close()
			var stdout, stderr bytes.Buffer
			args := []string{"upload", "--api", server.URL}
			if mode == "timeout" {
				args = append(args, "--timeout", "10ms")
			}
			if code := run(ctx, append(args, input), &stdout, &stderr); code != 1 || stdout.Len() != 0 {
				t.Fatalf("exit=%d out=%s stderr=%s", code, &stdout, &stderr)
			}
		})
	}
}

func TestArgumentValidation(t *testing.T) {
	for _, args := range [][]string{
		nil, {"unknown"}, {"upload"}, {"upload", "--timeout", "0", "file"},
		{"upload", "--api", "http://user:secret@example.test", "file"},
		{"upload", "--api", "http://example.test/api", "file"},
		{"download", testCID}, {"download", "--output", "file", "invalid"},
	} {
		var out bytes.Buffer
		if code := run(t.Context(), args, io.Discard, &out); code != 2 {
			t.Errorf("args=%v exit=%d: %s", args, code, &out)
		}
	}
	for _, suffix := range []string{"/../file", "/./file", "//file", "/a\\b", "/file\x00"} {
		if _, err := contentPath(testCID + suffix); err == nil {
			t.Errorf("unsafe path accepted: %q", suffix)
		}
	}
}

func assertNoTemporaryFiles(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".ipfs-download-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary downloads remain: %v %v", matches, err)
	}
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Error(err)
	}
}
