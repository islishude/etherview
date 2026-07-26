package x402testnet

import (
	"bytes"
	"encoding/hex"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/apiops"
)

const (
	testnetEnvPrefix              = "ETHERVIEW_X402_TESTNET_"
	realPaymentConfirmation       = "BASE_SEPOLIA_REAL_PAYMENT"
	maxTestnetURLBytes            = 4096
	maxPrivateKeyFileBytes  int64 = 256
	maxRPCURLFileBytes      int64 = 4096
	maxWriterURLFileBytes   int64 = 8192
)

const (
	codeConfigInvalid     = "x402_testnet_config_invalid"
	codeSecretFileInvalid = "x402_testnet_secret_file_invalid"
	codeSecretInvalid     = "x402_testnet_secret_invalid"
)

var revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Config is the complete one-shot testnet contract. No field has an implicit
// production default: only the payment network and chain are fixed by this
// Base Sepolia-only harness.
type Config struct {
	Revision                   string
	TargetURL                  string
	ExpectedResourceURL        string
	ExpectedOperation          string
	ExpectedAccess             string
	ExpectedAsset              string
	ExpectedAssetDecimals      uint8
	ExpectedAssetEIP712Name    string
	ExpectedAssetEIP712Version string
	ExpectedAmountAtomic       string
	ExpectedRecipient          string
	ExpectedPayer              string
	ExpectedMaxTimeoutSeconds  uint32
	LedgerChainID              uint64
	PrivateKey                 []byte
	RPCURL                     string
	WriterDatabaseURL          string
}

type environmentLookup func(string) (string, bool)
type secretReader func(string, int64) ([]byte, error)
type revisionValidator func(string) bool

// LoadConfig validates every non-secret expectation before opening any Secret
// file. This ordering prevents an accidental real-payment setup from touching
// credentials when its explicit confirmation or public bindings are invalid.
func LoadConfig() (Config, error) {
	return loadConfigWithRevision(
		os.LookupEnv,
		readSecretFile,
		runningRevisionMatches,
	)
}

func loadConfig(lookup environmentLookup, readSecret secretReader) (Config, error) {
	return loadConfigWithRevision(
		lookup,
		readSecret,
		func(string) bool { return true },
	)
}

func loadConfigWithRevision(
	lookup environmentLookup,
	readSecret secretReader,
	validateRevision revisionValidator,
) (Config, error) {
	values := make(map[string]string, 18)
	for _, name := range []string{
		"CONFIRM",
		"REVISION",
		"TARGET_URL",
		"EXPECTED_RESOURCE_URL",
		"EXPECTED_OPERATION",
		"EXPECTED_ACCESS",
		"EXPECTED_ASSET",
		"EXPECTED_ASSET_DECIMALS",
		"EXPECTED_ASSET_EIP712_NAME",
		"EXPECTED_ASSET_EIP712_VERSION",
		"EXPECTED_AMOUNT_ATOMIC",
		"EXPECTED_RECIPIENT",
		"EXPECTED_PAYER",
		"EXPECTED_MAX_TIMEOUT_SECONDS",
		"LEDGER_CHAIN_ID",
	} {
		value, ok := lookup(testnetEnvPrefix + name)
		if !ok || value == "" {
			return Config{}, boundaryError(codeConfigInvalid)
		}
		values[name] = value
	}

	cfg, err := validatePublicConfig(values)
	if err != nil {
		return Config{}, err
	}
	if validateRevision == nil || !validateRevision(cfg.Revision) {
		return Config{}, boundaryError(codeConfigInvalid)
	}

	// Plaintext alternatives are forbidden even if their corresponding _FILE
	// variables are also present.
	for _, name := range []string{
		"PRIVATE_KEY",
		"RPC_URL",
		"WRITER_DATABASE_URL",
	} {
		if _, present := lookup(testnetEnvPrefix + name); present {
			return Config{}, boundaryError(codeConfigInvalid)
		}
	}

	filePaths := make(map[string]string, 3)
	for _, name := range []string{
		"PRIVATE_KEY_FILE",
		"RPC_URL_FILE",
		"WRITER_DATABASE_URL_FILE",
	} {
		value, ok := lookup(testnetEnvPrefix + name)
		if !ok || !validSecretPath(value) {
			return Config{}, boundaryError(codeConfigInvalid)
		}
		filePaths[name] = value
	}

	privateKeyText, err := readSecret(
		filePaths["PRIVATE_KEY_FILE"], maxPrivateKeyFileBytes,
	)
	if err != nil {
		return Config{}, boundaryError(codeSecretFileInvalid)
	}
	defer zeroBytes(privateKeyText)
	privateKey, err := parsePrivateKey(privateKeyText, cfg.ExpectedPayer)
	if err != nil {
		return Config{}, err
	}
	cfg.PrivateKey = privateKey

	rpcURL, err := readSecret(filePaths["RPC_URL_FILE"], maxRPCURLFileBytes)
	if err != nil {
		cfg.ZeroSecrets()
		return Config{}, boundaryError(codeSecretFileInvalid)
	}
	defer zeroBytes(rpcURL)
	if !validRPCURL(string(rpcURL)) {
		cfg.ZeroSecrets()
		return Config{}, boundaryError(codeSecretInvalid)
	}
	cfg.RPCURL = string(rpcURL)

	writerURL, err := readSecret(
		filePaths["WRITER_DATABASE_URL_FILE"], maxWriterURLFileBytes,
	)
	if err != nil {
		cfg.ZeroSecrets()
		return Config{}, boundaryError(codeSecretFileInvalid)
	}
	defer zeroBytes(writerURL)
	if !validWriterDatabaseURL(string(writerURL)) {
		cfg.ZeroSecrets()
		return Config{}, boundaryError(codeSecretInvalid)
	}
	cfg.WriterDatabaseURL = string(writerURL)
	return cfg, nil
}

func runningRevisionMatches(expected string) bool {
	info, ok := debug.ReadBuildInfo()
	return matchesBuildRevision(expected, info, ok)
}

func matchesBuildRevision(
	expected string,
	info *debug.BuildInfo,
	ok bool,
) bool {
	if !ok || info == nil ||
		info.Main.Path != "github.com/islishude/etherview" {
		return false
	}
	var (
		revision string
		vcs      string
		modified string
		counts   = make(map[string]int, 3)
	)
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs":
			vcs = setting.Value
			counts[setting.Key]++
		case "vcs.revision":
			revision = setting.Value
			counts[setting.Key]++
		case "vcs.modified":
			modified = setting.Value
			counts[setting.Key]++
		}
	}
	return counts["vcs"] == 1 &&
		counts["vcs.revision"] == 1 &&
		counts["vcs.modified"] == 1 &&
		vcs == "git" &&
		modified == "false" &&
		revisionPattern.MatchString(revision) &&
		revision == expected
}

func validatePublicConfig(values map[string]string) (Config, error) {
	if values["CONFIRM"] != realPaymentConfirmation ||
		!revisionPattern.MatchString(values["REVISION"]) {
		return Config{}, boundaryError(codeConfigInvalid)
	}
	operation, ok := apiops.Lookup(values["EXPECTED_OPERATION"])
	if !ok || !operation.BillingEligible || operation.Method != http.MethodGet {
		return Config{}, boundaryError(codeConfigInvalid)
	}
	if access := values["EXPECTED_ACCESS"]; access != "x402" &&
		access != "api_key_or_x402" {
		return Config{}, boundaryError(codeConfigInvalid)
	}

	target, err := validateHTTPSRequestURL(values["TARGET_URL"], operation)
	if err != nil {
		return Config{}, err
	}
	resource, err := validateHTTPSRequestURL(
		values["EXPECTED_RESOURCE_URL"], operation,
	)
	if err != nil || target.Scheme != resource.Scheme ||
		target.Host != resource.Host {
		return Config{}, boundaryError(codeConfigInvalid)
	}

	asset, ok := canonicalAddress(values["EXPECTED_ASSET"])
	if !ok {
		return Config{}, boundaryError(codeConfigInvalid)
	}
	recipient, ok := canonicalAddress(values["EXPECTED_RECIPIENT"])
	if !ok {
		return Config{}, boundaryError(codeConfigInvalid)
	}
	payer, ok := canonicalAddress(values["EXPECTED_PAYER"])
	if !ok {
		return Config{}, boundaryError(codeConfigInvalid)
	}
	decimals, ok := canonicalUint(
		values["EXPECTED_ASSET_DECIMALS"], 0, 255,
	)
	if !ok {
		return Config{}, boundaryError(codeConfigInvalid)
	}
	timeout, ok := canonicalUint(
		values["EXPECTED_MAX_TIMEOUT_SECONDS"], 1, 60,
	)
	if !ok {
		return Config{}, boundaryError(codeConfigInvalid)
	}
	if values["LEDGER_CHAIN_ID"] != strconv.FormatUint(
		baseSepoliaChainID, 10,
	) {
		return Config{}, boundaryError(codeConfigInvalid)
	}
	if !canonicalAtomicAmount(values["EXPECTED_AMOUNT_ATOMIC"]) ||
		!validEIP712Text(values["EXPECTED_ASSET_EIP712_NAME"], 128) ||
		!validEIP712Text(values["EXPECTED_ASSET_EIP712_VERSION"], 64) {
		return Config{}, boundaryError(codeConfigInvalid)
	}

	return Config{
		Revision:                   values["REVISION"],
		TargetURL:                  target.String(),
		ExpectedResourceURL:        resource.String(),
		ExpectedOperation:          string(operation.ID),
		ExpectedAccess:             values["EXPECTED_ACCESS"],
		ExpectedAsset:              asset,
		ExpectedAssetDecimals:      uint8(decimals),
		ExpectedAssetEIP712Name:    values["EXPECTED_ASSET_EIP712_NAME"],
		ExpectedAssetEIP712Version: values["EXPECTED_ASSET_EIP712_VERSION"],
		ExpectedAmountAtomic:       values["EXPECTED_AMOUNT_ATOMIC"],
		ExpectedRecipient:          recipient,
		ExpectedPayer:              payer,
		ExpectedMaxTimeoutSeconds:  uint32(timeout),
		LedgerChainID:              baseSepoliaChainID,
	}, nil
}

func validateHTTPSRequestURL(
	raw string,
	operation apiops.Spec,
) (*url.URL, error) {
	if raw == "" || raw != strings.TrimSpace(raw) ||
		len(raw) > maxTestnetURLBytes || !utf8.ValidString(raw) {
		return nil, boundaryError(codeConfigInvalid)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" ||
		parsed.User != nil || parsed.Opaque != "" || parsed.Fragment != "" ||
		parsed.RawFragment != "" || parsed.ForceQuery || parsed.RawPath != "" {
		return nil, boundaryError(codeConfigInvalid)
	}
	if strings.HasSuffix(parsed.Hostname(), ".") ||
		!asciiString(parsed.Hostname()) {
		return nil, boundaryError(codeConfigInvalid)
	}
	if port := parsed.Port(); port != "" {
		number, parseErr := strconv.ParseUint(port, 10, 16)
		if parseErr != nil || number == 0 {
			return nil, boundaryError(codeConfigInvalid)
		}
	}
	if parsed.Path == "" || parsed.Path != path.Clean(parsed.Path) {
		return nil, boundaryError(codeConfigInvalid)
	}
	values, err := url.ParseQuery(parsed.RawQuery)
	if err != nil || parsed.RawQuery != values.Encode() {
		return nil, boundaryError(codeConfigInvalid)
	}
	allowedQuery := make(map[string]apiops.ParameterSpec)
	for _, parameter := range operation.Parameters {
		if parameter.In == apiops.ParameterQuery {
			allowedQuery[parameter.Name] = parameter
		}
	}
	for name, items := range values {
		if _, ok := allowedQuery[name]; !ok || len(items) != 1 {
			return nil, boundaryError(codeConfigInvalid)
		}
	}
	for name, parameter := range allowedQuery {
		if parameter.Required && len(values[name]) != 1 {
			return nil, boundaryError(codeConfigInvalid)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc(operation.MuxPattern, func(http.ResponseWriter, *http.Request) {})
	request := &http.Request{
		Method: operation.Method,
		URL:    parsed,
		Host:   parsed.Host,
	}
	_, pattern := mux.Handler(request)
	if pattern != operation.MuxPattern {
		return nil, boundaryError(codeConfigInvalid)
	}
	return parsed, nil
}

func canonicalAddress(value string) (string, bool) {
	if !common.IsHexAddress(value) {
		return "", false
	}
	address := common.HexToAddress(value)
	canonical := address.Hex()
	return canonical, address != (common.Address{}) && value == canonical
}

func canonicalUint(value string, minimum, maximum uint64) (uint64, bool) {
	number, err := strconv.ParseUint(value, 10, 64)
	return number, err == nil && number >= minimum && number <= maximum &&
		value == strconv.FormatUint(number, 10)
}

func canonicalAtomicAmount(value string) bool {
	if value == "" || value[0] == '0' {
		return false
	}
	amount, ok := new(big.Int).SetString(value, 10)
	if !ok || amount.Sign() <= 0 {
		return false
	}
	maximum := new(big.Int).Sub(
		new(big.Int).Lsh(big.NewInt(1), 256),
		big.NewInt(1),
	)
	return amount.Cmp(maximum) <= 0 && amount.String() == value
}

func validEIP712Text(value string, maximumBytes int) bool {
	if value == "" || value != strings.TrimSpace(value) ||
		len(value) > maximumBytes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func readSecretFile(path string, maximumBytes int64) ([]byte, error) {
	if !validSecretPath(path) {
		return nil, boundaryError(codeSecretFileInvalid)
	}
	before, err := os.Lstat(path)
	if err != nil || !validSecretFileInfo(before, maximumBytes) {
		return nil, boundaryError(codeSecretFileInvalid)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, boundaryError(codeSecretFileInvalid)
	}
	defer file.Close() //nolint:errcheck
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) ||
		!validSecretFileInfo(opened, maximumBytes) {
		return nil, boundaryError(codeSecretFileInvalid)
	}
	data, err := io.ReadAll(io.LimitReader(file, maximumBytes+1))
	if err != nil || int64(len(data)) > maximumBytes {
		zeroBytes(data)
		return nil, boundaryError(codeSecretFileInvalid)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(opened, after) ||
		!validSecretFileInfo(after, maximumBytes) {
		zeroBytes(data)
		return nil, boundaryError(codeSecretFileInvalid)
	}
	if bytes.HasSuffix(data, []byte{'\n'}) {
		data = data[:len(data)-1]
		if bytes.HasSuffix(data, []byte{'\r'}) {
			data = data[:len(data)-1]
		}
	}
	if len(data) == 0 || !bytes.Equal(data, bytes.TrimSpace(data)) {
		zeroBytes(data)
		return nil, boundaryError(codeSecretInvalid)
	}
	return data, nil
}

func validSecretPath(path string) bool {
	return path != "" && path == strings.TrimSpace(path) &&
		filepath.IsAbs(path) && filepath.Clean(path) == path
}

func validSecretFileInfo(info os.FileInfo, maximumBytes int64) bool {
	mode := info.Mode()
	return mode.IsRegular() && mode&os.ModeSymlink == 0 &&
		mode.Perm()&0o077 == 0 && mode.Perm()&0o400 != 0 &&
		info.Size() > 0 && info.Size() <= maximumBytes
}

func parsePrivateKey(value []byte, expectedPayer string) ([]byte, error) {
	if len(value) != 64 {
		return nil, boundaryError(codeSecretInvalid)
	}
	decoded := make([]byte, 32)
	if _, err := hex.Decode(decoded, value); err != nil ||
		hex.EncodeToString(decoded) != string(value) {
		zeroBytes(decoded)
		return nil, boundaryError(codeSecretInvalid)
	}
	privateKey, err := crypto.ToECDSA(decoded)
	if err != nil ||
		crypto.PubkeyToAddress(privateKey.PublicKey).Hex() != expectedPayer {
		zeroBytes(decoded)
		return nil, boundaryError(codeSecretInvalid)
	}
	return decoded, nil
}

func validRPCURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return raw != "" && raw == strings.TrimSpace(raw) &&
		len(raw) <= int(maxRPCURLFileBytes) && utf8.ValidString(raw) &&
		err == nil && parsed.Scheme == "https" && validURLAuthority(parsed) &&
		parsed.User == nil && parsed.Opaque == "" && parsed.Fragment == "" &&
		parsed.RawFragment == "" && !parsed.ForceQuery
}

func validWriterDatabaseURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return raw != "" && raw == strings.TrimSpace(raw) &&
		len(raw) <= int(maxWriterURLFileBytes) && utf8.ValidString(raw) &&
		err == nil && (parsed.Scheme == "postgres" ||
		parsed.Scheme == "postgresql") && validURLAuthority(parsed) &&
		parsed.Opaque == "" && parsed.Fragment == "" &&
		parsed.RawFragment == "" && !parsed.ForceQuery &&
		parsed.Path != "" && parsed.Path != "/"
}

func validURLAuthority(parsed *url.URL) bool {
	if parsed == nil || parsed.Hostname() == "" ||
		strings.HasSuffix(parsed.Hostname(), ".") ||
		!asciiString(parsed.Hostname()) {
		return false
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.ParseUint(port, 10, 16)
		return err == nil && number != 0
	}
	return true
}

func asciiString(value string) bool {
	for index := range len(value) {
		if value[index] > 0x7f {
			return false
		}
	}
	return true
}

// ZeroSecrets removes the directly mutable secret material before command
// exit. Go strings cannot be reliably overwritten, so URL strings are cleared
// and are never serialized or included in an error.
func (cfg *Config) ZeroSecrets() {
	if cfg == nil {
		return
	}
	zeroBytes(cfg.PrivateKey)
	cfg.PrivateKey = nil
	cfg.RPCURL = ""
	cfg.WriterDatabaseURL = ""
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
