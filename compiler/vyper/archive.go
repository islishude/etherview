package vyper

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
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

func unpackSource(ctx context.Context, raw []byte, files map[string][]byte) error {
	compressed, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return errArchive
	}
	defer compressed.Close() //nolint:errcheck
	data, err := io.ReadAll(io.LimitReader(compressed, MaxArchive+1))
	if err != nil || len(data) > MaxArchive {
		return errArchive
	}
	reader := tar.NewReader(bytes.NewReader(data))
	seen := make(map[string]bool)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return errArchive
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			if len(header.PAXRecords) != 1 || header.PAXRecords["comment"] != "d2c0c87a50dcfca115cb68a96d4063a7576e3dfa" {
				return errArchive
			}
			continue
		}
		name := strings.TrimSuffix(header.Name, "/")
		if !safePath(name) || seen[name] || len(seen) >= maxFiles {
			return errArchive
		}
		seen[name] = true
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > MaxArchive {
			return errArchive
		}
		_, relative, ok := strings.Cut(name, "/")
		if !ok {
			return errArchive
		}
		base := strings.ToLower(path.Base(relative))
		if !strings.HasPrefix(relative, "vyper/") && !strings.HasPrefix(base, "license") && !strings.HasPrefix(base, "copying") {
			continue
		}
		value, err := io.ReadAll(io.LimitReader(reader, MaxArchive+1))
		if err != nil || len(value) > MaxArchive {
			return errArchive
		}
		if err := putFile(files, relative, value); err != nil {
			return err
		}
	}
	files["vyper/vyper_git_version.txt"] = []byte("d2c0c87a50dcfca115cb68a96d4063a7576e3dfa")
	files["vyper-0.2.0.dist-info/METADATA"] = []byte("Metadata-Version: 2.1\nName: vyper\nVersion: 0.2.0\n")
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
