package vyper

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"
)

const maxFiles = 16384

var errArchive = errors.New("unsafe Vyper archive")

func safePath(name string) bool {
	return strings.IndexFunc(name, func(r rune) bool { return r < 32 || r == 127 }) < 0 && fs.ValidPath(name) && !strings.ContainsAny(name, "\\\x00") && len(name) <= 1024
}

func putFile(files map[string][]byte, name string, data []byte) error {
	if !safePath(name) || len(files) >= maxFiles {
		return errArchive
	}
	if old, ok := files[name]; ok && !bytes.Equal(old, data) {
		return errArchive
	}
	var total int
	for _, value := range files {
		total += len(value)
	}
	if total+len(data) > MaxArchive {
		return errArchive
	}
	files[name] = data
	return nil
}

func keepWheelFile(name string) bool {
	if slices.Contains(strings.Split(name, "/"), "__pycache__") {
		return false
	}
	if strings.HasSuffix(strings.Split(name, "/")[0], ".data") {
		return false
	}
	return !slices.Contains([]string{".so", ".pyd", ".dll", ".dylib", ".pyc"}, path.Ext(name))
}

func unpackWheel(ctx context.Context, raw []byte, files map[string][]byte) error {
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil || len(reader.File) > maxFiles {
		return errArchive
	}
	seen := make(map[string]bool)
	var total uint64
	for _, file := range reader.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := strings.TrimSuffix(file.Name, "/")
		total += file.UncompressedSize64
		if !safePath(name) || seen[name] || file.Mode()&fs.ModeSymlink != 0 || file.Flags&1 != 0 || file.UncompressedSize64 > MaxArchive || total > MaxArchive {
			return errArchive
		}
		seen[name] = true
		if file.FileInfo().IsDir() {
			continue
		}
		if !file.Mode().IsRegular() {
			return errArchive
		}
		stream, err := file.Open()
		if err != nil {
			return errArchive
		}
		data, readErr := io.ReadAll(io.LimitReader(stream, MaxArchive+1))
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil || len(data) > MaxArchive || uint64(len(data)) != file.UncompressedSize64 {
			return errArchive
		}
		if keepWheelFile(name) {
			if err := putFile(files, name, data); err != nil {
				return err
			}
		}
	}
	return nil
}

func makeZip(ctx context.Context, files map[string][]byte) ([]byte, error) {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)}
		header.SetMode(0o444)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err = entry.Write(files[name]); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if out.Len() > MaxArchive {
		return nil, errArchive
	}
	return out.Bytes(), nil
}
