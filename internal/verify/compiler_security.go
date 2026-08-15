package verify

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/islishude/etherview/internal/netpolicy"
)

const (
	defaultCompilerArtifactBytes = int64(200 << 20)
	maximumCompilerArtifactBytes = int64(1 << 30)
	compilerCacheValidationTries = 8
	defaultCompilerInputBytes    = 5 << 20
	defaultCompilerOutputBytes   = 64 << 20
	defaultCompilerTimeout       = 2 * time.Minute
)

type compilerCacheFileValidation uint8

const (
	compilerCacheFileInvalid compilerCacheFileValidation = iota
	compilerCacheFileValid
	compilerCacheFileIdentityChanged
)

type outboundResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type compilerCacheFileOperations struct {
	lstat func(string) (os.FileInfo, error)
	open  func(string) (*os.File, error)
	hash  func(*os.File, int64) ([sha256.Size]byte, error)
}

var operatingSystemCompilerCacheFileOperations = compilerCacheFileOperations{
	lstat: os.Lstat,
	open:  os.Open,
	hash:  hashCompilerCacheFile,
}

func validateCompilerArtifact(
	language Language,
	version string,
	artifact CompilerArtifact,
	allowHTTP bool,
) (*url.URL, [sha256.Size]byte, int64, error) {
	if language != LanguageSolidity {
		return nil, [sha256.Size]byte{}, 0, fmt.Errorf("language %q is not allowlisted", language)
	}
	if !versionPattern.MatchString(version) {
		return nil, [sha256.Size]byte{}, 0, errors.New("invalid compiler version")
	}
	digest, err := decodeCompilerDigest(artifact.SHA256)
	if err != nil || artifact.SHA256 != strings.ToLower(artifact.SHA256) {
		return nil, [sha256.Size]byte{}, 0, errors.New("compiler artifact SHA-256 is invalid")
	}
	maximum := artifact.MaxBytes
	if maximum == 0 {
		maximum = defaultCompilerArtifactBytes
	}
	if maximum < 1 || maximum > maximumCompilerArtifactBytes {
		return nil, [sha256.Size]byte{}, 0, errors.New("compiler artifact size limit is invalid")
	}
	parsed, err := url.Parse(strings.TrimSpace(artifact.URL))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && (!allowHTTP || parsed.Scheme != "http")) || len(parsed.String()) > 4096 {
		return nil, [sha256.Size]byte{}, 0, errors.New("compiler artifact URL is not allowed")
	}
	return parsed, digest, maximum, nil
}

func secureCompilerCacheRoot(root string) error {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return errors.New("compiler cache root must be an absolute clean path")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return errors.New("create compiler cache")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return errors.New("compiler cache root must be a non-symlink directory without group or world write access")
	}
	return nil
}

func validCompilerCacheFile(path string, expected [sha256.Size]byte, maximum int64) bool {
	return validCompilerCacheFileUsing(
		operatingSystemCompilerCacheFileOperations,
		path,
		expected,
		maximum,
	)
}

func validCompilerCacheFileUsing(
	operations compilerCacheFileOperations,
	path string,
	expected [sha256.Size]byte,
	maximum int64,
) bool {
	for range compilerCacheValidationTries {
		switch validateCompilerCacheFile(operations, path, expected, maximum) {
		case compilerCacheFileValid:
			return true
		case compilerCacheFileInvalid:
			return false
		case compilerCacheFileIdentityChanged:
			continue
		}
	}
	return false
}

func validateCompilerCacheFile(
	operations compilerCacheFileOperations,
	path string,
	expected [sha256.Size]byte,
	maximum int64,
) compilerCacheFileValidation {
	if operations.lstat == nil || operations.open == nil || operations.hash == nil {
		return compilerCacheFileInvalid
	}
	info, err := operations.lstat(path)
	if err != nil || !validCompilerCacheFileInfo(info, maximum) {
		return compilerCacheFileInvalid
	}
	file, err := operations.open(path)
	if err != nil {
		return compilerCacheFileInvalid
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !validCompilerCacheFileInfo(opened, maximum) {
		return compilerCacheFileInvalid
	}
	if !os.SameFile(info, opened) {
		return compilerCacheFileIdentityChanged
	}
	digest, err := operations.hash(file, maximum)
	if err != nil {
		return compilerCacheFileInvalid
	}
	after, err := operations.lstat(path)
	if err != nil || !validCompilerCacheFileInfo(after, maximum) {
		return compilerCacheFileInvalid
	}
	if !os.SameFile(opened, after) {
		return compilerCacheFileIdentityChanged
	}
	if digest != expected {
		return compilerCacheFileInvalid
	}
	return compilerCacheFileValid
}

func hashCompilerCacheFile(
	file *os.File,
	maximum int64,
) ([sha256.Size]byte, error) {
	hasher := sha256.New()
	if _, err := io.Copy(hasher, io.LimitReader(file, maximum+1)); err != nil {
		return [sha256.Size]byte{}, err
	}
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	return digest, nil
}

func validCompilerCacheFileInfo(info os.FileInfo, maximum int64) bool {
	return info != nil && info.Mode().IsRegular() && info.Mode().Perm() == 0o400 &&
		info.Size() >= 1 && info.Size() <= maximum
}

func restrictedOutboundClient(
	configured *http.Client,
	timeout time.Duration,
	resolver outboundResolver,
	allowPrivate bool,
) *http.Client {
	if timeout <= 0 {
		timeout = defaultCompilerTimeout
	}
	var client http.Client
	if configured != nil {
		client = *configured
	} else {
		if resolver == nil {
			resolver = net.DefaultResolver
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		transport.MaxIdleConns = 8
		transport.MaxIdleConnsPerHost = 1
		transport.ResponseHeaderTimeout = timeout
		transport.TLSHandshakeTimeout = timeout
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialRestrictedOutboundHost(ctx, network, address, resolver, allowPrivate, timeout)
		}
		client.Transport = transport
	}
	client.Timeout = timeout
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("compiler artifact redirects are not allowed")
	}
	return &client
}

func dialRestrictedOutboundHost(
	ctx context.Context,
	network, address string,
	resolver outboundResolver,
	allowPrivate bool,
	timeout time.Duration,
) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("split restricted outbound address")
	}
	addresses, err := resolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("resolve restricted outbound host")
	}
	for _, candidate := range addresses {
		if !allowPrivate && !netpolicy.PublicIP(candidate.IP) {
			return nil, errors.New("restricted outbound host resolves to a disallowed network")
		}
	}
	dialer := net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	for _, candidate := range addresses {
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
		if dialErr == nil {
			return connection, nil
		}
	}
	return nil, errors.New("dial restricted outbound host")
}
