package enrich

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

var ErrTraceLimit = errors.New("trace exceeds configured limit")

type TraceSource string

const (
	TraceCallTracer TraceSource = "call_tracer"
	TraceAPI        TraceSource = "trace_api"
)

type TraceState string

const (
	TraceComplete    TraceState = "complete"
	TraceUnavailable TraceState = "unavailable"
)

type TraceLimits struct {
	MaxPayloadBytes      int
	MaxFrames            int
	MaxDepth             int
	MaxDataBytes         int
	MaxTextBytes         int
	MaxBlockPayloadBytes int
	MaxBlockFrames       int
	MaxBlockDataBytes    int
	MaxBlockTextBytes    int
}

func DefaultTraceLimits() TraceLimits {
	return TraceLimits{
		MaxPayloadBytes: 32 << 20, MaxFrames: 100_000, MaxDepth: 128,
		MaxDataBytes: 8 << 20, MaxTextBytes: 1 << 20,
		MaxBlockPayloadBytes: 64 << 20, MaxBlockFrames: 200_000,
		MaxBlockDataBytes: 32 << 20, MaxBlockTextBytes: 4 << 20,
	}
}

func (limits TraceLimits) validate() error {
	if limits.MaxPayloadBytes <= 0 || limits.MaxFrames <= 0 || limits.MaxDepth <= 0 || limits.MaxDataBytes <= 0 || limits.MaxTextBytes <= 0 ||
		limits.MaxBlockPayloadBytes <= 0 || limits.MaxBlockFrames <= 0 || limits.MaxBlockDataBytes <= 0 || limits.MaxBlockTextBytes <= 0 {
		return errors.New("all trace limits must be positive")
	}
	return nil
}

type CallFrame struct {
	Index          int
	ParentIndex    int
	TraceAddress   []uint32
	Type           string
	From           *Address
	To             *Address
	Value          string
	Gas            string
	GasUsed        string
	Input          []byte
	Output         []byte
	Error          string
	RevertReason   string
	DirectReverted bool
	Reverted       bool // True when this frame or any ancestor reverted.
}

type NormalizedTrace struct {
	State  TraceState
	Source TraceSource
	Frames []CallFrame
	Reason string
}

func UnavailableTrace(source TraceSource, reason string) NormalizedTrace {
	if reason == "" {
		reason = "trace capability unavailable"
	}
	return NormalizedTrace{State: TraceUnavailable, Source: source, Reason: reason}
}

type callTracerWire struct {
	Type         string            `json:"type"`
	From         string            `json:"from"`
	To           string            `json:"to"`
	Value        string            `json:"value"`
	Gas          string            `json:"gas"`
	GasUsed      string            `json:"gasUsed"`
	Input        string            `json:"input"`
	Output       string            `json:"output"`
	Error        string            `json:"error"`
	RevertReason string            `json:"revertReason"`
	Calls        []json.RawMessage `json:"calls"`
}

type traceBuilder struct {
	limits    TraceLimits
	frames    []CallFrame
	dataBytes int
	textBytes int
}

func NormalizeCallTracer(data []byte, limits TraceLimits) (NormalizedTrace, error) {
	if err := limits.validate(); err != nil {
		return NormalizedTrace{}, err
	}
	if len(data) > limits.MaxPayloadBytes {
		return NormalizedTrace{}, fmt.Errorf("%w: payload bytes", ErrTraceLimit)
	}
	if len(bytes.TrimSpace(data)) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return NormalizedTrace{}, errors.New("callTracer returned no root frame")
	}
	builder := traceBuilder{limits: limits}
	if err := builder.appendCallTracer(json.RawMessage(data), nil, -1, false); err != nil {
		return NormalizedTrace{}, err
	}
	return NormalizedTrace{State: TraceComplete, Source: TraceCallTracer, Frames: builder.frames}, nil
}

func (builder *traceBuilder) appendCallTracer(raw json.RawMessage, path []uint32, parent int, ancestorReverted bool) error {
	if len(path) > builder.limits.MaxDepth {
		return fmt.Errorf("%w: depth", ErrTraceLimit)
	}
	if len(builder.frames) >= builder.limits.MaxFrames {
		return fmt.Errorf("%w: frame count", ErrTraceLimit)
	}
	var wire callTracerWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return fmt.Errorf("decode callTracer frame %v: %w", path, err)
	}
	frame, err := builder.callFrame(wire, path, parent, ancestorReverted)
	if err != nil {
		return fmt.Errorf("callTracer frame %v: %w", path, err)
	}
	frame.Index = len(builder.frames)
	builder.frames = append(builder.frames, frame)
	for childIndex, child := range wire.Calls {
		childPath := append(append([]uint32(nil), path...), uint32(childIndex))
		if err := builder.appendCallTracer(child, childPath, frame.Index, frame.Reverted); err != nil {
			return err
		}
	}
	return nil
}

func (builder *traceBuilder) callFrame(wire callTracerWire, path []uint32, parent int, ancestorReverted bool) (CallFrame, error) {
	frameType, err := normalizeCallTracerType(wire.Type)
	if err != nil {
		return CallFrame{}, err
	}
	from, err := optionalTraceAddress(wire.From)
	if err != nil {
		return CallFrame{}, fmt.Errorf("from: %w", err)
	}
	to, err := optionalTraceAddress(wire.To)
	if err != nil {
		return CallFrame{}, fmt.Errorf("to: %w", err)
	}
	input, err := optionalTraceData(wire.Input)
	if err != nil {
		return CallFrame{}, fmt.Errorf("input: %w", err)
	}
	output, err := optionalTraceData(wire.Output)
	if err != nil {
		return CallFrame{}, fmt.Errorf("output: %w", err)
	}
	if err := builder.addData(len(input) + len(output)); err != nil {
		return CallFrame{}, err
	}
	if err := builder.addText(len(wire.Error) + len(wire.RevertReason)); err != nil {
		return CallFrame{}, err
	}
	for name, quantity := range map[string]string{"value": wire.Value, "gas": wire.Gas, "gasUsed": wire.GasUsed} {
		if err := validateTraceQuantity(quantity); err != nil {
			return CallFrame{}, fmt.Errorf("%s: %w", name, err)
		}
	}
	direct := wire.Error != "" || wire.RevertReason != ""
	frame := CallFrame{
		ParentIndex: parent, TraceAddress: append([]uint32(nil), path...), Type: frameType,
		From: from, To: to, Value: wire.Value, Gas: wire.Gas, GasUsed: wire.GasUsed,
		Input: input, Output: output, Error: wire.Error, RevertReason: wire.RevertReason,
		DirectReverted: direct, Reverted: ancestorReverted || direct,
	}
	if err := validateTraceFrameAddresses(frame); err != nil {
		return CallFrame{}, err
	}
	if len(path) == 0 && frame.Type != "CALL" && frame.Type != "CREATE" {
		return CallFrame{}, fmt.Errorf("transaction root has invalid type %q", frame.Type)
	}
	return frame, nil
}

func (builder *traceBuilder) addData(size int) error {
	if size < 0 || builder.dataBytes > builder.limits.MaxDataBytes-size {
		return fmt.Errorf("%w: input/output bytes", ErrTraceLimit)
	}
	builder.dataBytes += size
	return nil
}

func (builder *traceBuilder) addText(size int) error {
	if size < 0 || builder.textBytes > builder.limits.MaxTextBytes-size {
		return fmt.Errorf("%w: error text bytes", ErrTraceLimit)
	}
	builder.textBytes += size
	return nil
}

func normalizeCallTracerType(value string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "SUICIDE" {
		value = "SELFDESTRUCT"
	}
	switch value {
	case "CALL", "CALLCODE", "STATICCALL", "DELEGATECALL", "CREATE", "CREATE2", "SELFDESTRUCT":
		return value, nil
	default:
		return "", fmt.Errorf("unsupported callTracer type %q", value)
	}
}

type traceAPIWire struct {
	Action              json.RawMessage `json:"action"`
	Result              json.RawMessage `json:"result"`
	Error               string          `json:"error"`
	Subtraces           int             `json:"subtraces"`
	TraceAddress        []uint64        `json:"traceAddress"`
	Type                string          `json:"type"`
	BlockHash           string          `json:"blockHash"`
	BlockNumber         json.RawMessage `json:"blockNumber"`
	TransactionHash     string          `json:"transactionHash"`
	TransactionPosition json.RawMessage `json:"transactionPosition"`
}

type traceActionWire struct {
	CallType       string `json:"callType"`
	From           string `json:"from"`
	To             string `json:"to"`
	Address        string `json:"address"`
	RefundAddress  string `json:"refundAddress"`
	Author         string `json:"author"`
	Value          string `json:"value"`
	Balance        string `json:"balance"`
	Gas            string `json:"gas"`
	Input          string `json:"input"`
	Init           string `json:"init"`
	CreationMethod string `json:"creationMethod"`
}

type traceResultWire struct {
	Address string `json:"address"`
	GasUsed string `json:"gasUsed"`
	Output  string `json:"output"`
	Code    string `json:"code"`
}

type pendingTraceFrame struct {
	wire  traceAPIWire
	frame CallFrame
}

type TraceIdentity struct {
	BlockHash        Word
	BlockNumber      uint64
	TransactionHash  Word
	TransactionIndex uint64
}

func (identity TraceIdentity) validate() error {
	if identity.BlockHash.IsZero() || identity.TransactionHash.IsZero() {
		return errors.New("trace identity requires non-zero block and transaction hashes")
	}
	return nil
}

func NormalizeTraceAPI(data []byte, limits TraceLimits, identity TraceIdentity) (NormalizedTrace, error) {
	if err := limits.validate(); err != nil {
		return NormalizedTrace{}, err
	}
	if err := identity.validate(); err != nil {
		return NormalizedTrace{}, err
	}
	if len(data) > limits.MaxPayloadBytes {
		return NormalizedTrace{}, fmt.Errorf("%w: payload bytes", ErrTraceLimit)
	}
	var wires []traceAPIWire
	if err := json.Unmarshal(data, &wires); err != nil {
		return NormalizedTrace{}, fmt.Errorf("decode trace_* response: %w", err)
	}
	if len(wires) == 0 {
		return NormalizedTrace{}, errors.New("trace_transaction returned no transaction root frame")
	}
	if len(wires) > limits.MaxFrames {
		return NormalizedTrace{}, fmt.Errorf("%w: frame count", ErrTraceLimit)
	}
	pending := make([]pendingTraceFrame, len(wires))
	builder := traceBuilder{limits: limits}
	for index, wire := range wires {
		if err := validateTraceAPIIdentity(wire, identity); err != nil {
			return NormalizedTrace{}, fmt.Errorf("trace_* frame %d identity: %w", index, err)
		}
		if wire.Subtraces < 0 || wire.Subtraces > limits.MaxFrames {
			return NormalizedTrace{}, fmt.Errorf("trace_* frame %d has invalid subtrace count", index)
		}
		if len(wire.TraceAddress) > limits.MaxDepth {
			return NormalizedTrace{}, fmt.Errorf("%w: depth", ErrTraceLimit)
		}
		path := make([]uint32, len(wire.TraceAddress))
		for partIndex, part := range wire.TraceAddress {
			if part > uint64(^uint32(0)) {
				return NormalizedTrace{}, fmt.Errorf("trace address component exceeds uint32")
			}
			path[partIndex] = uint32(part)
		}
		frame, err := builder.traceAPIFrame(wire, path)
		if err != nil {
			return NormalizedTrace{}, fmt.Errorf("trace_* frame %v: %w", path, err)
		}
		pending[index] = pendingTraceFrame{wire: wire, frame: frame}
	}
	sort.Slice(pending, func(left, right int) bool {
		return compareTracePath(pending[left].frame.TraceAddress, pending[right].frame.TraceAddress) < 0
	})
	indices := make(map[string]int, len(pending))
	frames := make([]CallFrame, len(pending))
	children := make(map[string]int, len(pending))
	rootCount := 0
	for index, item := range pending {
		key := tracePathKey(item.frame.TraceAddress)
		if _, exists := indices[key]; exists {
			return NormalizedTrace{}, fmt.Errorf("duplicate trace address %v", item.frame.TraceAddress)
		}
		frame := item.frame
		frame.Index = index
		frame.ParentIndex = -1
		if len(frame.TraceAddress) == 0 {
			rootCount++
			if frame.Type != "CALL" && frame.Type != "CREATE" {
				return NormalizedTrace{}, fmt.Errorf("transaction root has invalid type %q", frame.Type)
			}
		} else {
			parentKey := tracePathKey(frame.TraceAddress[:len(frame.TraceAddress)-1])
			parent, exists := indices[parentKey]
			if !exists {
				return NormalizedTrace{}, fmt.Errorf("trace address %v has no parent", frame.TraceAddress)
			}
			childIndex := frame.TraceAddress[len(frame.TraceAddress)-1]
			if childIndex >= uint32(pending[parent].wire.Subtraces) {
				return NormalizedTrace{}, fmt.Errorf("trace address %v exceeds parent subtrace count", frame.TraceAddress)
			}
			frame.ParentIndex = parent
			frame.Reverted = frame.DirectReverted || frames[parent].Reverted
			children[parentKey]++
		}
		indices[key] = index
		frames[index] = frame
	}
	if rootCount != 1 {
		return NormalizedTrace{}, fmt.Errorf("trace_transaction returned %d transaction roots", rootCount)
	}
	for _, item := range pending {
		key := tracePathKey(item.frame.TraceAddress)
		if children[key] != item.wire.Subtraces {
			return NormalizedTrace{}, fmt.Errorf("trace address %v declares %d subtraces but has %d", item.frame.TraceAddress, item.wire.Subtraces, children[key])
		}
	}
	return NormalizedTrace{State: TraceComplete, Source: TraceAPI, Frames: frames}, nil
}

func validateTraceAPIIdentity(wire traceAPIWire, expected TraceIdentity) error {
	transactionHash, err := ParseWord(wire.TransactionHash)
	if err != nil || transactionHash != expected.TransactionHash {
		return errors.New("transaction hash does not match requested transaction")
	}
	blockHash, err := ParseWord(wire.BlockHash)
	if err != nil || blockHash != expected.BlockHash {
		return errors.New("block hash does not match canonical inclusion")
	}
	blockNumber, err := parseTraceWireUint64(wire.BlockNumber)
	if err != nil || blockNumber != expected.BlockNumber {
		return errors.New("block number does not match canonical inclusion")
	}
	position, err := parseTraceWireUint64(wire.TransactionPosition)
	if err != nil || position != expected.TransactionIndex {
		return errors.New("transaction position does not match canonical inclusion")
	}
	return nil
}

func parseTraceWireUint64(raw json.RawMessage) (uint64, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return 0, errors.New("missing trace identity number")
	}
	if strings.HasPrefix(trimmed, `"`) {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return 0, err
		}
		if strings.HasPrefix(text, "0x") {
			if err := validateTraceQuantity(text); err != nil {
				return 0, err
			}
			return strconv.ParseUint(text[2:], 16, 64)
		}
		if text == "" || (len(text) > 1 && text[0] == '0') {
			return 0, errors.New("trace identity decimal is not canonical")
		}
		return strconv.ParseUint(text, 10, 64)
	}
	if strings.ContainsAny(trimmed, ".eE+-") || (len(trimmed) > 1 && trimmed[0] == '0') {
		return 0, errors.New("trace identity number is not a canonical uint64")
	}
	return strconv.ParseUint(trimmed, 10, 64)
}

func (builder *traceBuilder) traceAPIFrame(wire traceAPIWire, path []uint32) (CallFrame, error) {
	var action traceActionWire
	if err := json.Unmarshal(wire.Action, &action); err != nil {
		return CallFrame{}, fmt.Errorf("action: %w", err)
	}
	var result traceResultWire
	if len(wire.Result) > 0 && string(wire.Result) != "null" {
		if err := json.Unmarshal(wire.Result, &result); err != nil {
			return CallFrame{}, fmt.Errorf("result: %w", err)
		}
	}
	frame := CallFrame{TraceAddress: path, Error: wire.Error, DirectReverted: wire.Error != "", Reverted: wire.Error != ""}
	if err := builder.addText(len(wire.Error)); err != nil {
		return CallFrame{}, err
	}
	var err error
	switch strings.ToLower(wire.Type) {
	case "call":
		frame.Type = strings.ToUpper(action.CallType)
		if frame.Type == "" {
			frame.Type = "CALL"
		}
		frame.From, err = optionalTraceAddress(action.From)
		if err != nil {
			return CallFrame{}, fmt.Errorf("from: %w", err)
		}
		frame.To, err = optionalTraceAddress(action.To)
		if err != nil {
			return CallFrame{}, fmt.Errorf("to: %w", err)
		}
		frame.Value, frame.Gas, frame.GasUsed = action.Value, action.Gas, result.GasUsed
		frame.Input, err = optionalTraceData(action.Input)
		if err != nil {
			return CallFrame{}, fmt.Errorf("input: %w", err)
		}
		frame.Output, err = optionalTraceData(result.Output)
		if err != nil {
			return CallFrame{}, fmt.Errorf("output: %w", err)
		}
	case "create", "create2":
		frame.Type = "CREATE"
		if strings.EqualFold(wire.Type, "create2") || strings.EqualFold(action.CreationMethod, "create2") {
			frame.Type = "CREATE2"
		}
		frame.From, err = optionalTraceAddress(action.From)
		if err != nil {
			return CallFrame{}, fmt.Errorf("from: %w", err)
		}
		frame.To, err = optionalTraceAddress(result.Address)
		if err != nil {
			return CallFrame{}, fmt.Errorf("result address: %w", err)
		}
		frame.Value, frame.Gas, frame.GasUsed = action.Value, action.Gas, result.GasUsed
		frame.Input, err = optionalTraceData(action.Init)
		if err != nil {
			return CallFrame{}, fmt.Errorf("init: %w", err)
		}
		frame.Output, err = optionalTraceData(result.Code)
		if err != nil {
			return CallFrame{}, fmt.Errorf("code: %w", err)
		}
	case "suicide", "selfdestruct":
		frame.Type = "SELFDESTRUCT"
		frame.From, err = optionalTraceAddress(action.Address)
		if err != nil {
			return CallFrame{}, fmt.Errorf("address: %w", err)
		}
		frame.To, err = optionalTraceAddress(action.RefundAddress)
		if err != nil {
			return CallFrame{}, fmt.Errorf("refund address: %w", err)
		}
		frame.Value = action.Balance
	case "reward":
		frame.Type = "REWARD"
		frame.To, err = optionalTraceAddress(action.Author)
		if err != nil {
			return CallFrame{}, fmt.Errorf("author: %w", err)
		}
		frame.Value = action.Value
	default:
		return CallFrame{}, fmt.Errorf("unsupported trace type %q", wire.Type)
	}
	for name, quantity := range map[string]string{"value": frame.Value, "gas": frame.Gas, "gasUsed": frame.GasUsed} {
		if err := validateTraceQuantity(quantity); err != nil {
			return CallFrame{}, fmt.Errorf("%s: %w", name, err)
		}
	}
	if err := builder.addData(len(frame.Input) + len(frame.Output)); err != nil {
		return CallFrame{}, err
	}
	if err := validateTraceFrameAddresses(frame); err != nil {
		return CallFrame{}, err
	}
	return frame, nil
}

func validateTraceFrameAddresses(frame CallFrame) error {
	switch frame.Type {
	case "CALL", "CALLCODE", "STATICCALL", "DELEGATECALL", "SELFDESTRUCT":
		if frame.From == nil || frame.To == nil {
			return fmt.Errorf("%s frame requires from and to addresses", frame.Type)
		}
	case "CREATE", "CREATE2":
		if frame.From == nil || (frame.To == nil && !frame.DirectReverted) {
			return fmt.Errorf("%s frame requires a sender and successful result address", frame.Type)
		}
	case "REWARD":
		if frame.To == nil {
			return errors.New("REWARD frame requires an author address")
		}
	default:
		return fmt.Errorf("unsupported normalized trace type %q", frame.Type)
	}
	return nil
}

func optionalTraceAddress(value string) (*Address, error) {
	if value == "" || value == "0x" {
		return nil, nil
	}
	address, err := ParseAddress(value)
	if err != nil {
		return nil, err
	}
	return addressPointer(address), nil
}

func optionalTraceData(value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	return decodeDataHex(value)
}

func validateTraceQuantity(value string) error {
	if value == "" {
		return nil
	}
	if !strings.HasPrefix(value, "0x") || len(value) < 3 {
		return errors.New("quantity must start with 0x and contain a digit")
	}
	if len(value) > 3 && value[2] == '0' {
		return errors.New("quantity has a leading zero")
	}
	for _, character := range value[2:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
			return errors.New("quantity contains a non-hex digit")
		}
	}
	return nil
}

func compareTracePath(left, right []uint32) int {
	limit := min(len(right), len(left))
	for index := 0; index < limit; index++ {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}
	return len(left) - len(right)
}

func tracePathKey(path []uint32) string {
	var builder strings.Builder
	for index, part := range path {
		if index > 0 {
			builder.WriteByte('.')
		}
		builder.WriteString(strconv.FormatUint(uint64(part), 10))
	}
	return builder.String()
}
