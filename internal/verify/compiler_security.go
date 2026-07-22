package verify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/islishude/etherview/internal/netpolicy"
)

const (
	defaultCompilerArtifactBytes = int64(200 << 20)
	maximumCompilerArtifactBytes = int64(1 << 30)
	defaultCompilerInputBytes    = 5 << 20
	defaultCompilerOutputBytes   = 64 << 20
	defaultCompilerTimeout       = 2 * time.Minute
)

var (
	containerMemoryPattern = regexp.MustCompile(`^([1-9][0-9]*)([bkmg])$`)
	containerCPUsPattern   = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,3})?$`)
	containerImagePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,254}$`)
	containerNamePattern   = regexp.MustCompile(`^etherview-compiler-[0-9a-f]{32}$`)
)

type outboundResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

func validateCompilerArtifact(
	language Language,
	version string,
	artifact CompilerArtifact,
	allowHTTP bool,
) (*url.URL, [sha256.Size]byte, int64, error) {
	if language != LanguageSolidity && language != LanguageVyper {
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
		(parsed.Scheme != "https" && !(allowHTTP && parsed.Scheme == "http")) || len(parsed.String()) > 4096 {
		return nil, [sha256.Size]byte{}, 0, errors.New("compiler artifact URL is not allowed")
	}
	return parsed, digest, maximum, nil
}

func validateProcessManifest(artifacts map[Language]map[string]CompilerArtifact) error {
	if len(artifacts) == 0 {
		return errors.New("compiler artifacts are required")
	}
	for language, versions := range artifacts {
		if language != LanguageSolidity && language != LanguageVyper {
			return fmt.Errorf("language %q is not allowlisted", language)
		}
		if len(versions) == 0 {
			return fmt.Errorf("compiler artifacts for %s are required", language)
		}
		for version, artifact := range versions {
			if _, _, _, err := validateCompilerArtifact(language, version, artifact, false); err != nil {
				return err
			}
		}
	}
	return nil
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
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o500 || info.Size() < 1 || info.Size() > maximum {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Mode().Perm() != 0o500 {
		return false
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, io.LimitReader(file, maximum+1)); err != nil {
		return false
	}
	return opened.Size() <= maximum && string(hasher.Sum(nil)) == string(expected[:])
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

func parseContainerImage(image string) ([sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	if strings.Count(image, "@sha256:") != 1 {
		return zero, errors.New("compiler container image must be pinned by digest")
	}
	parts := strings.SplitN(image, "@sha256:", 2)
	if !containerImagePattern.MatchString(parts[0]) || strings.Contains(parts[0], "//") || strings.Contains(parts[0], "..") {
		return zero, errors.New("compiler container image name is invalid")
	}
	if parts[1] != strings.ToLower(parts[1]) {
		return zero, errors.New("compiler container image digest is invalid")
	}
	digest, err := decodeCompilerDigest(parts[1])
	if err != nil {
		return zero, errors.New("compiler container image digest is invalid")
	}
	return digest, nil
}

func validateContainerManifest(images map[Language]map[string]string) (map[Language]map[string]string, error) {
	if len(images) == 0 {
		return nil, errors.New("compiler container images are required")
	}
	validated := make(map[Language]map[string]string, len(images))
	for language, versions := range images {
		if language != LanguageSolidity && language != LanguageVyper {
			return nil, fmt.Errorf("language %q is not allowlisted", language)
		}
		if len(versions) == 0 {
			return nil, fmt.Errorf("compiler container images for %s are required", language)
		}
		validated[language] = make(map[string]string, len(versions))
		for version, image := range versions {
			if !versionPattern.MatchString(version) {
				return nil, errors.New("invalid compiler version")
			}
			if _, err := parseContainerImage(image); err != nil {
				return nil, err
			}
			validated[language][version] = image
		}
	}
	return validated, nil
}

func sortedCompilerLanguages(images map[Language]map[string]string) []Language {
	languages := make([]Language, 0, len(images))
	for language := range images {
		languages = append(languages, language)
	}
	sort.Slice(languages, func(left, right int) bool { return languages[left] < languages[right] })
	return languages
}

func validateContainerResources(memory, cpus string, pids int) error {
	match := containerMemoryPattern.FindStringSubmatch(memory)
	if len(match) != 3 {
		return errors.New("compiler container memory limit is invalid")
	}
	amount, err := strconv.ParseUint(match[1], 10, 64)
	if err != nil {
		return errors.New("compiler container memory limit is invalid")
	}
	multiplier := uint64(1)
	switch match[2] {
	case "k":
		multiplier = 1 << 10
	case "m":
		multiplier = 1 << 20
	case "g":
		multiplier = 1 << 30
	}
	if amount > ^uint64(0)/multiplier {
		return errors.New("compiler container memory limit is invalid")
	}
	bytes := amount * multiplier
	if bytes < 64<<20 || bytes > 16<<30 {
		return errors.New("compiler container memory limit must be between 64m and 16g")
	}
	if !containerCPUsPattern.MatchString(cpus) {
		return errors.New("compiler container CPU limit is invalid")
	}
	cpuValue, err := strconv.ParseFloat(cpus, 64)
	if err != nil || cpuValue <= 0 || cpuValue > 64 {
		return errors.New("compiler container CPU limit must be greater than zero and at most 64")
	}
	if pids < 1 || pids > 4096 {
		return errors.New("compiler container PID limit must be between 1 and 4096")
	}
	return nil
}

func randomCompilerContainerName() (string, error) {
	var value [16]byte
	if _, err := io.ReadFull(rand.Reader, value[:]); err != nil {
		return "", errors.New("generate compiler container name")
	}
	return "etherview-compiler-" + hex.EncodeToString(value[:]), nil
}

func validCompilerContainerName(value string) bool {
	return containerNamePattern.MatchString(value)
}
