package enrich

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"unicode/utf8"
)

var ErrABIDecodeLimit = errors.New("ABI decode exceeds configured limit")

type abiDecoder struct {
	data   []byte
	limits DecodeLimits
	budget *abiDecodeBudget
}

func decodeABIValues(types []*abiType, data []byte, limits DecodeLimits) ([]any, error) {
	return decodeABIValuesWithBudget(types, data, limits, newABIDecodeBudget(limits))
}

func decodeABIValuesWithBudget(types []*abiType, data []byte, limits DecodeLimits, budget *abiDecodeBudget) ([]any, error) {
	decoder := abiDecoder{data: data, limits: limits, budget: budget}
	return decoder.decodeTuple(types, 0, 1)
}

type abiDecodeBudget struct {
	limits DecodeLimits
	nodes  int
	work   int
	bytes  int
}

func newABIDecodeBudget(limits DecodeLimits) *abiDecodeBudget {
	return &abiDecodeBudget{limits: limits}
}

func (budget *abiDecodeBudget) addNodes(count int) error {
	return budget.add(&budget.nodes, count, budget.limits.MaxDecodeNodes, "nodes")
}

func (budget *abiDecodeBudget) addWork(count int) error {
	return budget.add(&budget.work, count, budget.limits.MaxDecodeWork, "work")
}

func (budget *abiDecodeBudget) addBytes(count int) error {
	return budget.add(&budget.bytes, count, budget.limits.MaxDecodedBytes, "bytes")
}

func (*abiDecodeBudget) add(used *int, count, limit int, name string) error {
	if count < 0 || *used > limit-count {
		return fmt.Errorf("%w: %s", ErrABIDecodeLimit, name)
	}
	*used += count
	return nil
}

func (decoder *abiDecoder) decodeTuple(types []*abiType, base int, depth int) ([]any, error) {
	if depth > decoder.limits.MaxDepth {
		return nil, errors.New("ABI value nesting exceeds limit")
	}
	if len(types) > decoder.limits.MaxArguments {
		return nil, errors.New("ABI tuple contains too many values")
	}
	if err := decoder.budget.addWork(1); err != nil {
		return nil, err
	}
	values := make([]any, len(types))
	cursor := base
	for index, valueType := range types {
		if err := decoder.budget.addWork(1); err != nil {
			return nil, err
		}
		if valueType.dynamic() {
			offsetWord, err := decoder.word(cursor)
			if err != nil {
				return nil, fmt.Errorf("dynamic value %d offset: %w", index, err)
			}
			offset, err := boundedABIInt(offsetWord, len(decoder.data))
			if err != nil {
				return nil, fmt.Errorf("dynamic value %d offset: %w", index, err)
			}
			if offset%32 != 0 {
				return nil, fmt.Errorf("dynamic value %d offset is not word-aligned", index)
			}
			position, err := checkedAdd(base, offset)
			if err != nil || position > len(decoder.data) {
				return nil, fmt.Errorf("dynamic value %d offset is outside payload", index)
			}
			decoded, err := decoder.decodeDynamic(valueType, position, depth+1)
			if err != nil {
				return nil, fmt.Errorf("dynamic value %d: %w", index, err)
			}
			values[index] = decoded
			cursor, err = checkedAdd(cursor, 32)
			if err != nil {
				return nil, err
			}
			continue
		}
		size, err := valueType.staticSize()
		if err != nil {
			return nil, err
		}
		decoded, err := decoder.decodeStatic(valueType, cursor, depth+1)
		if err != nil {
			return nil, fmt.Errorf("static value %d: %w", index, err)
		}
		values[index] = decoded
		cursor, err = checkedAdd(cursor, size)
		if err != nil {
			return nil, err
		}
	}
	return values, nil
}

func (decoder *abiDecoder) decodeStatic(valueType *abiType, position, depth int) (any, error) {
	if depth > decoder.limits.MaxDepth {
		return nil, errors.New("ABI value nesting exceeds limit")
	}
	if err := decoder.budget.addNodes(1); err != nil {
		return nil, err
	}
	if err := decoder.budget.addWork(1); err != nil {
		return nil, err
	}
	switch valueType.kind {
	case abiUint:
		word, err := decoder.word(position)
		if err != nil {
			return nil, err
		}
		width := valueType.bits / 8
		if !allByte(word[:32-width], 0) {
			return nil, fmt.Errorf("uint%d has non-zero high bits", valueType.bits)
		}
		return new(big.Int).SetBytes(word[32-width:]).String(), nil
	case abiInt:
		word, err := decoder.word(position)
		if err != nil {
			return nil, err
		}
		width := valueType.bits / 8
		valueBytes := word[32-width:]
		negative := valueBytes[0]&0x80 != 0
		padding := byte(0)
		if negative {
			padding = 0xff
		}
		if !allByte(word[:32-width], padding) {
			return nil, fmt.Errorf("int%d is not sign-extended", valueType.bits)
		}
		value := new(big.Int).SetBytes(valueBytes)
		if negative {
			value.Sub(value, new(big.Int).Lsh(big.NewInt(1), uint(valueType.bits)))
		}
		return value.String(), nil
	case abiBool:
		word, err := decoder.word(position)
		if err != nil {
			return nil, err
		}
		if !allByte(word[:31], 0) || word[31] > 1 {
			return nil, errors.New("boolean word is neither zero nor one")
		}
		return word[31] == 1, nil
	case abiAddress:
		word, err := decoder.word(position)
		if err != nil {
			return nil, err
		}
		address, err := AddressFromWord(word)
		if err != nil {
			return nil, err
		}
		return address.String(), nil
	case abiFixedBytes:
		word, err := decoder.word(position)
		if err != nil {
			return nil, err
		}
		if !allByte(word[valueType.size:], 0) {
			return nil, fmt.Errorf("bytes%d has non-zero right padding", valueType.size)
		}
		return "0x" + hex.EncodeToString(word[:valueType.size]), nil
	case abiFunction:
		word, err := decoder.word(position)
		if err != nil {
			return nil, err
		}
		if !allByte(word[24:], 0) {
			return nil, errors.New("function value has non-zero right padding")
		}
		return "0x" + hex.EncodeToString(word[:24]), nil
	case abiArray:
		if valueType.arrayLength < 0 || valueType.element.dynamic() {
			return nil, errors.New("dynamic array passed to static decoder")
		}
		if valueType.arrayLength > decoder.limits.MaxArrayElements {
			return nil, errors.New("ABI array exceeds element limit")
		}
		return decoder.decodeArray(valueType.element, valueType.arrayLength, position, depth)
	case abiTuple:
		return decoder.decodeTuple(valueType.components, position, depth+1)
	default:
		return nil, errors.New("unsupported static ABI type")
	}
}

func (decoder *abiDecoder) decodeDynamic(valueType *abiType, position, depth int) (any, error) {
	if depth > decoder.limits.MaxDepth {
		return nil, errors.New("ABI value nesting exceeds limit")
	}
	if err := decoder.budget.addNodes(1); err != nil {
		return nil, err
	}
	if err := decoder.budget.addWork(1); err != nil {
		return nil, err
	}
	switch valueType.kind {
	case abiDynamicBytes, abiString:
		lengthWord, err := decoder.word(position)
		if err != nil {
			return nil, fmt.Errorf("dynamic byte length: %w", err)
		}
		length, err := boundedABIInt(lengthWord, decoder.limits.MaxDynamicBytes)
		if err != nil {
			return nil, fmt.Errorf("dynamic byte length: %w", err)
		}
		start, err := checkedAdd(position, 32)
		if err != nil {
			return nil, err
		}
		end, err := checkedAdd(start, length)
		if err != nil || end > len(decoder.data) {
			return nil, errors.New("dynamic byte value exceeds payload")
		}
		paddedEnd, err := checkedAdd(start, paddedLength(length))
		if err != nil || paddedEnd > len(decoder.data) {
			return nil, errors.New("dynamic byte padding exceeds payload")
		}
		if !allByte(decoder.data[end:paddedEnd], 0) {
			return nil, errors.New("dynamic byte value has non-zero padding")
		}
		if err := decoder.budget.addBytes(paddedEnd - start); err != nil {
			return nil, err
		}
		value := decoder.data[start:end]
		if valueType.kind == abiString {
			if !utf8.Valid(value) {
				return nil, errors.New("ABI string is not valid UTF-8")
			}
			return string(value), nil
		}
		return "0x" + hex.EncodeToString(value), nil
	case abiArray:
		count := valueType.arrayLength
		base := position
		if count < 0 {
			lengthWord, err := decoder.word(position)
			if err != nil {
				return nil, fmt.Errorf("array length: %w", err)
			}
			count, err = boundedABIInt(lengthWord, decoder.limits.MaxArrayElements)
			if err != nil {
				return nil, fmt.Errorf("array length: %w", err)
			}
			base, err = checkedAdd(position, 32)
			if err != nil {
				return nil, err
			}
		} else if count > decoder.limits.MaxArrayElements {
			return nil, errors.New("ABI array exceeds element limit")
		}
		return decoder.decodeArray(valueType.element, count, base, depth+1)
	case abiTuple:
		return decoder.decodeTuple(valueType.components, position, depth+1)
	default:
		return nil, errors.New("static ABI type passed to dynamic decoder")
	}
}

// decodeArray deliberately does not apply MaxArguments. ABI arrays and tuple
// parameters have separate limits; in particular, a standards-compliant
// ERC-1155 TransferBatch may contain more than 256 elements.
func (decoder *abiDecoder) decodeArray(element *abiType, count, base, depth int) ([]any, error) {
	if depth > decoder.limits.MaxDepth {
		return nil, errors.New("ABI value nesting exceeds limit")
	}
	if count < 0 || count > decoder.limits.MaxArrayElements {
		return nil, errors.New("ABI array exceeds element limit")
	}
	if err := decoder.budget.addWork(1); err != nil {
		return nil, err
	}
	values := make([]any, count)
	cursor := base
	for index := range values {
		if err := decoder.budget.addWork(1); err != nil {
			return nil, err
		}
		var err error
		if element.dynamic() {
			offsetWord, wordErr := decoder.word(cursor)
			if wordErr != nil {
				return nil, fmt.Errorf("array element %d offset: %w", index, wordErr)
			}
			offset, offsetErr := boundedABIInt(offsetWord, len(decoder.data))
			if offsetErr != nil {
				return nil, fmt.Errorf("array element %d offset: %w", index, offsetErr)
			}
			if offset%32 != 0 {
				return nil, fmt.Errorf("array element %d offset is not word-aligned", index)
			}
			position, positionErr := checkedAdd(base, offset)
			if positionErr != nil || position > len(decoder.data) {
				return nil, fmt.Errorf("array element %d offset is outside payload", index)
			}
			values[index], err = decoder.decodeDynamic(element, position, depth+1)
			cursor, wordErr = checkedAdd(cursor, 32)
			if wordErr != nil {
				return nil, wordErr
			}
		} else {
			size, sizeErr := element.staticSize()
			if sizeErr != nil {
				return nil, sizeErr
			}
			values[index], err = decoder.decodeStatic(element, cursor, depth+1)
			cursor, sizeErr = checkedAdd(cursor, size)
			if sizeErr != nil {
				return nil, sizeErr
			}
		}
		if err != nil {
			return nil, fmt.Errorf("array element %d: %w", index, err)
		}
	}
	return values, nil
}

func (decoder *abiDecoder) word(position int) (Word, error) {
	var word Word
	if err := decoder.budget.addWork(1); err != nil {
		return word, err
	}
	if err := decoder.budget.addBytes(32); err != nil {
		return word, err
	}
	end, err := checkedAdd(position, 32)
	if err != nil || position < 0 || end > len(decoder.data) {
		return word, errors.New("ABI word exceeds payload")
	}
	copy(word[:], decoder.data[position:end])
	return word, nil
}

func boundedABIInt(word Word, maximum int) (int, error) {
	value := new(big.Int).SetBytes(word[:])
	if !value.IsUint64() {
		return 0, errors.New("ABI integer exceeds uint64")
	}
	unsigned := value.Uint64()
	if uint64(maximum) < unsigned {
		return 0, fmt.Errorf("ABI integer %d exceeds limit %d", unsigned, maximum)
	}
	return int(unsigned), nil
}

func checkedAdd(left, right int) (int, error) {
	if left < 0 || right < 0 || left > int(^uint(0)>>1)-right {
		return 0, errors.New("ABI offset overflows int")
	}
	return left + right, nil
}

func paddedLength(length int) int {
	if length == 0 {
		return 0
	}
	return (length + 31) / 32 * 32
}

func allByte(value []byte, expected byte) bool {
	for _, item := range value {
		if item != expected {
			return false
		}
	}
	return true
}
