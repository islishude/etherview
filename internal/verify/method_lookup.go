package verify

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/core/vm"
)

type MethodLookupRequest struct {
	Bytecode  string            `json:"bytecode"`
	ABI       json.RawMessage   `json:"abi"`
	SourceMap string            `json:"source_map"`
	FileIDs   map[string]string `json:"file_ids"`
}

type MethodSource struct {
	Selector  string `json:"selector"`
	Signature string `json:"signature"`
	FileName  string `json:"file_name"`
	Offset    int    `json:"offset"`
	Length    int    `json:"length"`
}

type evmInstruction struct {
	pc       int
	op       vm.OpCode
	argument []byte
}

type sourceMapEntry struct {
	offset int
	length int
	fileID int
}

// LookupMethods recognizes the common Solidity/Vyper selector dispatcher and
// omits methods whose destination cannot be mapped safely.
func LookupMethods(request MethodLookupRequest) ([]MethodSource, error) {
	code, err := decodeBytecode(request.Bytecode)
	if err != nil || len(code) == 0 {
		return nil, errors.New("method lookup bytecode is invalid")
	}
	if len(request.SourceMap) > 16<<20 || len(request.FileIDs) > 4096 || !jsonArray(request.ABI) {
		return nil, errors.New("method lookup input exceeds configured bounds")
	}
	parsedABI, err := abi.JSON(strings.NewReader(string(request.ABI)))
	if err != nil {
		return nil, errors.New("method lookup ABI is invalid")
	}
	instructions, err := decodeInstructions(code)
	if err != nil {
		return nil, err
	}
	sourceMap, err := parseSourceMap(request.SourceMap, len(instructions))
	if err != nil {
		return nil, err
	}
	pcIndex := make(map[int]int, len(instructions))
	for index, instruction := range instructions {
		pcIndex[instruction.pc] = index
	}
	signatures := make(map[[4]byte]string)
	for _, method := range parsedABI.Methods {
		if len(method.ID) != 4 {
			continue
		}
		var selector [4]byte
		copy(selector[:], method.ID)
		signatures[selector] = method.Sig
	}
	methods := make([]MethodSource, 0, len(signatures))
	seen := make(map[[4]byte]struct{})
	for index := 0; index+3 < len(instructions); index++ {
		selectorInstruction := instructions[index]
		equalInstruction := instructions[index+1]
		destinationInstruction := instructions[index+2]
		jumpInstruction := instructions[index+3]
		if selectorInstruction.op != vm.PUSH4 || equalInstruction.op != vm.EQ ||
			destinationInstruction.op < vm.PUSH1 || destinationInstruction.op > vm.PUSH4 ||
			jumpInstruction.op != vm.JUMPI {
			continue
		}
		var selector [4]byte
		copy(selector[:], selectorInstruction.argument)
		signature, known := signatures[selector]
		if !known {
			continue
		}
		destination := 0
		for _, value := range destinationInstruction.argument {
			destination = destination<<8 | int(value)
		}
		sourceIndex, exists := pcIndex[destination]
		if !exists || sourceIndex >= len(sourceMap) {
			continue
		}
		location := sourceMap[sourceIndex]
		fileName, exists := request.FileIDs[strconv.Itoa(location.fileID)]
		if !exists || !validStandardJSONSourceName(fileName) ||
			location.offset < 0 || location.length < 0 {
			continue
		}
		if _, duplicate := seen[selector]; duplicate {
			continue
		}
		seen[selector] = struct{}{}
		methods = append(methods, MethodSource{
			Selector: "0x" + hex.EncodeToString(selector[:]), Signature: signature,
			FileName: fileName, Offset: location.offset, Length: location.length,
		})
	}
	return methods, nil
}

func decodeInstructions(code []byte) ([]evmInstruction, error) {
	instructions := make([]evmInstruction, 0, len(code)/2)
	for pc := 0; pc < len(code); {
		op := vm.OpCode(code[pc])
		size := 0
		if op >= vm.PUSH1 && op <= vm.PUSH32 {
			size = int(op-vm.PUSH1) + 1
		}
		if size > len(code)-pc-1 {
			return nil, errors.New("method lookup bytecode has a truncated push")
		}
		instruction := evmInstruction{pc: pc, op: op}
		if size > 0 {
			instruction.argument = append([]byte(nil), code[pc+1:pc+1+size]...)
		}
		instructions = append(instructions, instruction)
		pc += 1 + size
	}
	return instructions, nil
}

func parseSourceMap(raw string, instructionCount int) ([]sourceMapEntry, error) {
	parts := strings.Split(raw, ";")
	if len(parts) > instructionCount || len(parts) > 1<<20 {
		return nil, errors.New("method lookup source map is invalid")
	}
	entries := make([]sourceMapEntry, len(parts))
	current := sourceMapEntry{fileID: -1}
	for index, part := range parts {
		fields := strings.Split(part, ":")
		if len(fields) > 5 {
			return nil, errors.New("method lookup source map is invalid")
		}
		for fieldIndex := range 3 {
			if fieldIndex >= len(fields) || fields[fieldIndex] == "" {
				continue
			}
			value, err := strconv.Atoi(fields[fieldIndex])
			if err != nil {
				return nil, errors.New("method lookup source map is invalid")
			}
			switch fieldIndex {
			case 0:
				current.offset = value
			case 1:
				current.length = value
			case 2:
				current.fileID = value
			}
		}
		entries[index] = current
	}
	for len(entries) < instructionCount {
		entries = append(entries, current)
	}
	return entries, nil
}
