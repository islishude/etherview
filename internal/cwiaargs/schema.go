package cwiaargs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/islishude/etherview/internal/jsonstrict"
)

const (
	AnalysisVersion = 1
	SchemaVersion   = 2
	SchemaSource    = "solidity_ast"
	SchemaEncoding  = "solady-cwia-offsets"
	HelperSHA256    = "0xbc97b0d077a3c5d5603808caeeb3fe572dcb2448c5536b66316d1b6b129cfca3"

	MaxAnalysisBytes = 8 << 10
	MaxFields        = 64
	MaxGetters       = 16
	MaxArguments     = 0x5fd3

	StatusDecoded           = "decoded"
	StatusSchemaUnavailable = "schema_unavailable"
	StatusSchemaInvalid     = "schema_invalid"
	StatusDataInvalid       = "data_invalid"

	ReasonASTUnavailable   = "ast_unavailable"
	ReasonMalformed        = "malformed_analysis"
	ReasonUnsupported      = "unsupported_access"
	ReasonAmbiguous        = "ambiguous_layout"
	ReasonIncomplete       = "incomplete_layout"
	ReasonConflict         = "schema_conflict"
	ReasonLimit            = "limit_exceeded"
	ReasonLengthMismatch   = "length_mismatch"
	ReasonNoncanonical     = "noncanonical_value"
	AnalysisStatusDerived  = "derived"
	AnalysisStatusInvalid  = "invalid"
	AnalysisStatusMissing  = "unavailable"
	FieldRoleValue         = "value"
	FieldRoleLength        = "length"
	SizeKindFixed          = "fixed"
	SizeKindField          = "field"
	SizeKindRemaining      = "remaining"
	ResolutionExactAddress = "exact_address"
	ResolutionCodeHash     = "code_hash"
)

var (
	fieldNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	uintTypePattern  = regexp.MustCompile(`^uint(?:8|16|24|32|40|48|56|64|72|80|88|96|104|112|120|128|136|144|152|160|168|176|184|192|200|208|216|224|232|240|248|256)$`)
)

type Analysis struct {
	AnalysisVersion int     `json:"analysis_version"`
	Status          string  `json:"status"`
	Reason          string  `json:"reason,omitempty"`
	Schema          *Schema `json:"schema,omitempty"`
}

type Schema struct {
	Version      int     `json:"version"`
	Source       string  `json:"source"`
	Encoding     string  `json:"encoding"`
	HelperSHA256 string  `json:"helper_sha256"`
	SHA256       string  `json:"sha256"`
	Fields       []Field `json:"fields"`
}

type Field struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Offset  int      `json:"offset"`
	Role    string   `json:"role"`
	Getters []string `json:"getters"`
	Size    Size     `json:"size"`
}

type Size struct {
	Kind       string `json:"kind"`
	Bytes      *int   `json:"bytes,omitempty"`
	Field      string `json:"field,omitempty"`
	Multiplier int    `json:"multiplier,omitempty"`
}

type Value struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
	Value  any    `json:"value"`
}

type Decoding struct {
	Status           string  `json:"status"`
	Reason           string  `json:"reason,omitempty"`
	Schema           *Schema `json:"schema,omitempty"`
	SchemaResolution string  `json:"schema_resolution,omitempty"`
	Arguments        []Value `json:"arguments"`
}

type schemaDigestPayload struct {
	Version      int     `json:"version"`
	Source       string  `json:"source"`
	Encoding     string  `json:"encoding"`
	HelperSHA256 string  `json:"helper_sha256"`
	Fields       []Field `json:"fields"`
}

func FinalizeSchema(fields []Field) (Schema, error) {
	schema := Schema{
		Version: SchemaVersion, Source: SchemaSource, Encoding: SchemaEncoding,
		HelperSHA256: HelperSHA256, Fields: cloneFields(fields),
	}
	if err := validateSchemaShape(schema, false); err != nil {
		return Schema{}, err
	}
	digest, err := schemaDigest(schema)
	if err != nil {
		return Schema{}, err
	}
	schema.SHA256 = digest
	return schema, nil
}

func DerivedAnalysis(schema Schema) (Analysis, error) {
	if err := validateSchemaShape(schema, true); err != nil {
		return Analysis{}, err
	}
	return Analysis{AnalysisVersion: AnalysisVersion, Status: AnalysisStatusDerived, Schema: &schema}, nil
}

func UnavailableAnalysis() Analysis {
	return Analysis{AnalysisVersion: AnalysisVersion, Status: AnalysisStatusMissing, Reason: ReasonASTUnavailable}
}

func InvalidAnalysis(reason string) Analysis {
	if !validAnalysisFailure(reason) {
		reason = ReasonMalformed
	}
	return Analysis{AnalysisVersion: AnalysisVersion, Status: AnalysisStatusInvalid, Reason: reason}
}

func MarshalAnalysis(analysis Analysis) ([]byte, error) {
	if err := validateAnalysis(analysis); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(analysis)
	if err != nil || len(encoded) > MaxAnalysisBytes {
		return nil, errors.New("CWIA AST analysis exceeds its encoding limit")
	}
	return encoded, nil
}

func ParseAnalysis(raw []byte) (Analysis, error) {
	if len(raw) == 0 || len(raw) > MaxAnalysisBytes {
		return Analysis{}, errors.New("CWIA AST analysis is outside its size limit")
	}
	if err := jsonstrict.Validate(raw, jsonstrict.Limits{
		MaxDepth: 8, MaxNodes: 4096, SafeIntegersOnly: true,
	}); err != nil {
		return Analysis{}, errors.New("CWIA AST analysis is malformed")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var analysis Analysis
	if err := decoder.Decode(&analysis); err != nil {
		return Analysis{}, errors.New("CWIA AST analysis is malformed")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Analysis{}, errors.New("CWIA AST analysis has trailing JSON")
	}
	if err := validateAnalysis(analysis); err != nil {
		return Analysis{}, err
	}
	return analysis, nil
}

func Decode(rawHex string, analysis Analysis, resolution string) Decoding {
	result := Decoding{Arguments: []Value{}}
	switch analysis.Status {
	case AnalysisStatusMissing:
		result.Status, result.Reason = StatusSchemaUnavailable, ReasonASTUnavailable
		return result
	case AnalysisStatusInvalid:
		result.Status, result.Reason = StatusSchemaInvalid, analysis.Reason
		return result
	case AnalysisStatusDerived:
	default:
		result.Status, result.Reason = StatusSchemaInvalid, ReasonMalformed
		return result
	}
	if analysis.Schema == nil || (resolution != ResolutionExactAddress && resolution != ResolutionCodeHash) {
		result.Status, result.Reason = StatusSchemaInvalid, ReasonMalformed
		return result
	}
	schema := *analysis.Schema
	result.Schema = &schema
	result.SchemaResolution = resolution
	raw, err := hexutil.Decode(rawHex)
	if err != nil || len(raw) > MaxArguments {
		result.Status, result.Reason = StatusDataInvalid, ReasonLengthMismatch
		return result
	}
	values := make([]Value, 0, len(schema.Fields))
	numeric := make(map[string]*big.Int)
	cursor := 0
	for _, field := range schema.Fields {
		if field.Offset != cursor {
			result.Status, result.Reason = StatusDataInvalid, ReasonLengthMismatch
			return result
		}
		length, ok := resolveFieldLength(field, raw, numeric)
		if !ok || length < 0 || length > MaxArguments-field.Offset {
			result.Status, result.Reason = StatusDataInvalid, ReasonLengthMismatch
			return result
		}
		end := field.Offset + length
		if end > len(raw) {
			result.Status, result.Reason = StatusDataInvalid, ReasonLengthMismatch
			return result
		}
		value, integer, valid := decodeValue(field.Type, raw[field.Offset:end])
		if !valid {
			result.Status, result.Reason = StatusDataInvalid, ReasonNoncanonical
			return result
		}
		if integer != nil {
			numeric[field.Name] = integer
		}
		values = append(values, Value{
			Name: field.Name, Type: field.Type, Offset: field.Offset,
			Length: length, Value: value,
		})
		cursor = end
	}
	if cursor != len(raw) {
		result.Status, result.Reason = StatusDataInvalid, ReasonLengthMismatch
		return result
	}
	result.Status = StatusDecoded
	result.Arguments = values
	return result
}

func AnalysisDigest(analysis Analysis) string {
	if analysis.Status != AnalysisStatusDerived || analysis.Schema == nil {
		return ""
	}
	return analysis.Schema.SHA256
}

func cloneFields(fields []Field) []Field {
	cloned := make([]Field, len(fields))
	for index, field := range fields {
		cloned[index] = field
		cloned[index].Getters = append([]string(nil), field.Getters...)
		if field.Size.Bytes != nil {
			value := *field.Size.Bytes
			cloned[index].Size.Bytes = &value
		}
	}
	return cloned
}

func validateAnalysis(analysis Analysis) error {
	if analysis.AnalysisVersion != AnalysisVersion {
		return errors.New("CWIA AST analysis version is invalid")
	}
	switch analysis.Status {
	case AnalysisStatusDerived:
		if analysis.Reason != "" || analysis.Schema == nil {
			return errors.New("derived CWIA AST analysis is malformed")
		}
		return validateSchemaShape(*analysis.Schema, true)
	case AnalysisStatusMissing:
		if analysis.Reason != ReasonASTUnavailable || analysis.Schema != nil {
			return errors.New("unavailable CWIA AST analysis is malformed")
		}
	case AnalysisStatusInvalid:
		if !validAnalysisFailure(analysis.Reason) || analysis.Schema != nil {
			return errors.New("invalid CWIA AST analysis is malformed")
		}
	default:
		return errors.New("CWIA AST analysis status is invalid")
	}
	return nil
}

func validAnalysisFailure(reason string) bool {
	switch reason {
	case ReasonMalformed, ReasonUnsupported, ReasonAmbiguous, ReasonIncomplete, ReasonConflict, ReasonLimit:
		return true
	default:
		return false
	}
}

func validateSchemaShape(schema Schema, requireDigest bool) error {
	if schema.Version != SchemaVersion || schema.Source != SchemaSource ||
		schema.Encoding != SchemaEncoding || schema.HelperSHA256 != HelperSHA256 ||
		len(schema.Fields) == 0 || len(schema.Fields) > MaxFields {
		return errors.New("CWIA AST schema identity is invalid")
	}
	knownFields := make(map[string]Field, len(schema.Fields))
	cursor := 0
	for index, field := range schema.Fields {
		if len(field.Name) == 0 || len(field.Name) > 64 || !fieldNamePattern.MatchString(field.Name) {
			return errors.New("CWIA AST schema field name is invalid")
		}
		if _, duplicate := knownFields[field.Name]; duplicate {
			return errors.New("CWIA AST schema field name is duplicated")
		}
		if field.Offset != cursor || field.Offset < 0 || field.Offset > MaxArguments ||
			(field.Role != FieldRoleValue && field.Role != FieldRoleLength) ||
			!validFieldType(field.Type) || len(field.Getters) > MaxGetters {
			return errors.New("CWIA AST schema field is invalid")
		}
		if !sort.StringsAreSorted(field.Getters) {
			return errors.New("CWIA AST schema getters are not sorted")
		}
		for getterIndex, getter := range field.Getters {
			if getter == "" || len(getter) > 256 ||
				(getterIndex > 0 && getter == field.Getters[getterIndex-1]) {
				return errors.New("CWIA AST schema getter is invalid")
			}
		}
		if field.Role == FieldRoleLength {
			if _, unsigned := uintBits(field.Type); !unsigned {
				return errors.New("CWIA AST schema length field is not unsigned")
			}
		}
		minimum, fixed, err := validateSize(field, knownFields, index == len(schema.Fields)-1)
		if err != nil {
			return err
		}
		knownFields[field.Name] = field
		if fixed {
			cursor += minimum
			if cursor > MaxArguments {
				return errors.New("CWIA AST schema exceeds the immutable argument limit")
			}
		} else if index != len(schema.Fields)-1 {
			return errors.New("dynamic CWIA AST schema field must be last")
		}
	}
	if requireDigest {
		if !fixedHash(schema.SHA256) {
			return errors.New("CWIA AST schema digest is invalid")
		}
		digest, err := schemaDigest(schema)
		if err != nil || digest != schema.SHA256 {
			return errors.New("CWIA AST schema digest does not match")
		}
	} else if schema.SHA256 != "" {
		return errors.New("unfinished CWIA AST schema carries a digest")
	}
	return nil
}

func validateSize(field Field, knownFields map[string]Field, last bool) (int, bool, error) {
	size := field.Size
	switch size.Kind {
	case SizeKindFixed:
		if size.Bytes == nil || *size.Bytes < 0 || *size.Bytes > MaxArguments ||
			size.Field != "" || size.Multiplier != 0 {
			return 0, false, errors.New("fixed CWIA AST schema size is invalid")
		}
		if !validFixedTypeWidth(field.Type, *size.Bytes) {
			return 0, false, errors.New("CWIA AST schema type and size disagree")
		}
		return *size.Bytes, true, nil
	case SizeKindField:
		if size.Bytes != nil || size.Field == "" ||
			(size.Multiplier != 1 && size.Multiplier != 32) {
			return 0, false, errors.New("field-sized CWIA AST schema size is invalid")
		}
		lengthField, exists := knownFields[size.Field]
		if !exists || lengthField.Role != FieldRoleLength || !last ||
			(field.Type != "bytes" && field.Type != "uint256[]" && field.Type != "bytes32[]") {
			return 0, false, errors.New("CWIA AST schema length dependency is invalid")
		}
		if field.Type == "bytes" && size.Multiplier != 1 ||
			(field.Type == "uint256[]" || field.Type == "bytes32[]") && size.Multiplier != 32 {
			return 0, false, errors.New("CWIA AST schema length multiplier is invalid")
		}
		return 0, false, nil
	case SizeKindRemaining:
		if size.Bytes != nil || size.Field != "" || size.Multiplier != 0 || !last || field.Type != "bytes" {
			return 0, false, errors.New("remaining CWIA AST schema size is invalid")
		}
		return 0, false, nil
	default:
		return 0, false, errors.New("CWIA AST schema size kind is invalid")
	}
}

func validFieldType(value string) bool {
	return value == "address" || value == "bytes32" || value == "bytes" ||
		value == "uint256[]" || value == "bytes32[]" || uintTypePattern.MatchString(value)
}

func validFixedTypeWidth(value string, width int) bool {
	switch value {
	case "address":
		return width == common.AddressLength
	case "bytes32":
		return width == common.HashLength
	case "bytes":
		return width >= 0
	case "uint256[]", "bytes32[]":
		return width >= 0 && width%common.HashLength == 0
	default:
		bits, ok := uintBits(value)
		return ok && width == bits/8
	}
}

func uintBits(value string) (int, bool) {
	if !uintTypePattern.MatchString(value) {
		return 0, false
	}
	bits, err := strconv.Atoi(strings.TrimPrefix(value, "uint"))
	return bits, err == nil
}

func schemaDigest(schema Schema) (string, error) {
	payload := schemaDigestPayload{
		Version: schema.Version, Source: schema.Source, Encoding: schema.Encoding,
		HelperSHA256: schema.HelperSHA256, Fields: cloneFields(schema.Fields),
	}
	encoded, err := json.Marshal(payload)
	if err != nil || len(encoded) > MaxAnalysisBytes {
		return "", errors.New("CWIA AST schema cannot be normalized")
	}
	digest := sha256.Sum256(encoded)
	return "0x" + hex.EncodeToString(digest[:]), nil
}

func resolveFieldLength(field Field, raw []byte, numeric map[string]*big.Int) (int, bool) {
	switch field.Size.Kind {
	case SizeKindFixed:
		return *field.Size.Bytes, true
	case SizeKindRemaining:
		return len(raw) - field.Offset, len(raw) >= field.Offset
	case SizeKindField:
		value := numeric[field.Size.Field]
		if value == nil || !value.IsInt64() || value.Sign() < 0 {
			return 0, false
		}
		count := value.Int64()
		if count > int64(MaxArguments) || count > math.MaxInt/int64(field.Size.Multiplier) {
			return 0, false
		}
		return int(count) * field.Size.Multiplier, true
	default:
		return 0, false
	}
}

func decodeValue(valueType string, encoded []byte) (any, *big.Int, bool) {
	switch valueType {
	case "address":
		if len(encoded) != common.AddressLength {
			return nil, nil, false
		}
		return common.BytesToAddress(encoded).Hex(), nil, true
	case "bytes32":
		if len(encoded) != common.HashLength {
			return nil, nil, false
		}
		return hexutil.Encode(encoded), nil, true
	case "bytes":
		return hexutil.Encode(encoded), nil, true
	case "uint256[]":
		if len(encoded)%common.HashLength != 0 {
			return nil, nil, false
		}
		values := make([]string, len(encoded)/common.HashLength)
		for index := range values {
			values[index] = new(big.Int).SetBytes(encoded[index*32 : (index+1)*32]).String()
		}
		return values, nil, true
	case "bytes32[]":
		if len(encoded)%common.HashLength != 0 {
			return nil, nil, false
		}
		values := make([]string, len(encoded)/common.HashLength)
		for index := range values {
			values[index] = hexutil.Encode(encoded[index*32 : (index+1)*32])
		}
		return values, nil, true
	default:
		bits, ok := uintBits(valueType)
		if !ok || len(encoded) != bits/8 {
			return nil, nil, false
		}
		integer := new(big.Int).SetBytes(encoded)
		return integer.String(), integer, true
	}
}

func fixedHash(value string) bool {
	return len(value) == 66 && strings.HasPrefix(value, "0x") && func() bool {
		decoded, err := hex.DecodeString(value[2:])
		return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
	}()
}

func SortAndUniqueStrings(values []string) []string {
	sort.Strings(values)
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func FixedSize(bytes int) Size {
	return Size{Kind: SizeKindFixed, Bytes: &bytes}
}

func FieldSize(field string, multiplier int) Size {
	return Size{Kind: SizeKindField, Field: field, Multiplier: multiplier}
}

func RemainingSize() Size { return Size{Kind: SizeKindRemaining} }

func ValidateFieldName(value string) bool {
	return len(value) > 0 && len(value) <= 64 && fieldNamePattern.MatchString(value)
}

func UintWidth(value string) (int, bool) {
	bits, ok := uintBits(value)
	return bits / 8, ok
}

func ValidateResolution(value string) error {
	if value != ResolutionExactAddress && value != ResolutionCodeHash {
		return fmt.Errorf("invalid CWIA schema resolution %q", value)
	}
	return nil
}
