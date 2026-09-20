package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	cid "github.com/ipfs/go-cid"
)

func newClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("RPC redirects are not supported")
		},
	}
}

func rpcRequest(ctx context.Context, client *http.Client, endpoint string, query url.Values, body io.Reader, contentType string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"?"+query.Encode(), body)
	if err != nil {
		return nil, errors.New("cannot create RPC request")
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("RPC transport failed")
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return nil, fmt.Errorf("RPC returned HTTP %d", response.StatusCode)
	}
	return response, nil
}

func upload(ctx context.Context, client *http.Client, opts options) (string, error) {
	// Lstat rejects symlinks and special files before Open can block on a FIFO.
	info, err := os.Lstat(opts.input)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("upload input must be a regular file")
	}
	file, err := os.Open(opts.input)
	if err != nil {
		return "", errors.New("cannot open upload input")
	}
	defer file.Close() //nolint:errcheck
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return "", errors.New("upload input changed during open")
	}
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	completed := make(chan error, 1)
	go func() {
		// Kubo query-unescapes the multipart filename, including '+' as space.
		part, writeErr := form.CreateFormFile("file", url.QueryEscape(filepath.Base(opts.input)))
		if writeErr == nil {
			_, writeErr = io.Copy(part, file)
		}
		if writeErr == nil {
			writeErr = form.Close()
		}
		_ = writer.CloseWithError(writeErr)
		completed <- writeErr
	}()
	query := url.Values{
		"cid-version": {"1"}, "hash": {"sha2-256"}, "raw-leaves": {"true"},
		"pin": {"true"}, "wrap-with-directory": {strconv.FormatBool(opts.wrap)},
		"chunker": {"size-262144"}, "progress": {"false"},
	}
	response, requestErr := rpcRequest(ctx, client, opts.api+"/api/v0/add", query, reader, form.FormDataContentType())
	if requestErr != nil {
		_ = reader.CloseWithError(io.ErrClosedPipe)
		<-completed
		return "", requestErr
	}
	defer response.Body.Close() //nolint:errcheck
	// Kubo can send streaming response headers before consuming the entire
	// multipart request. Drain its bounded response while the producer runs;
	// only then close and join the producer (also on partial/error responses).
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	_ = reader.CloseWithError(io.ErrClosedPipe)
	writeErr := <-completed
	if writeErr != nil {
		return "", errors.New("upload stream failed")
	}
	if err != nil || len(body) > 1<<20 || rpcStreamError(response) {
		return "", errors.New("invalid or incomplete upload response")
	}
	return addedCID(body, filepath.Base(opts.input), opts.wrap)
}

func addedCID(body []byte, name string, wrap bool) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	var hashes []string
	for {
		var result struct {
			Name string
			Hash string
		}
		if err := decoder.Decode(&result); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return "", errors.New("invalid upload response")
		}
		expectedName := name
		if len(hashes) == 1 && wrap {
			expectedName = ""
		}
		decoded, err := cid.Decode(result.Hash)
		if err != nil || decoded.Version() != 1 || result.Name != expectedName || len(hashes) >= 2 {
			return "", errors.New("invalid upload result")
		}
		hashes = append(hashes, result.Hash)
	}
	expected := 1
	if wrap {
		expected = 2
	}
	if len(hashes) != expected {
		return "", errors.New("incomplete upload result")
	}
	return hashes[len(hashes)-1], nil
}

func download(ctx context.Context, client *http.Client, opts options) error {
	if _, err := os.Lstat(opts.output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output already exists or is inaccessible")
	}
	file, err := os.CreateTemp(filepath.Dir(opts.output), ".ipfs-download-*")
	if err != nil {
		return errors.New("cannot create output temporary file")
	}
	defer os.Remove(file.Name()) //nolint:errcheck
	defer file.Close()           //nolint:errcheck
	response, err := rpcRequest(ctx, client, opts.api+"/api/v0/cat", url.Values{"arg": {opts.input}}, nil, "")
	if err != nil {
		return err
	}
	defer response.Body.Close() //nolint:errcheck
	if _, err := io.Copy(file, response.Body); err != nil || rpcStreamError(response) {
		return errors.New("download stream failed")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := file.Sync(); err != nil {
		return errors.New("cannot sync downloaded file")
	}
	if err := file.Close(); err != nil {
		return errors.New("cannot close downloaded file")
	}
	// A same-directory hard link publishes the complete file atomically and
	// refuses an existing destination, including one created during download.
	if err := os.Link(file.Name(), opts.output); err != nil {
		return errors.New("cannot publish download without overwriting output")
	}
	return nil
}

func rpcStreamError(response *http.Response) bool {
	return strings.TrimSpace(response.Header.Get("X-Stream-Error")) != "" ||
		strings.TrimSpace(response.Trailer.Get("X-Stream-Error")) != ""
}
