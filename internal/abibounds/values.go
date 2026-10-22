package abibounds

import (
	"encoding/binary"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

// CheckValues bounds the work and expanded data of an ABI value graph without
// allocating its values. Repeated offsets consume the budget on every visit.
// It is a preflight only: callers still enforce ABI validity and canonicality.
func CheckValues(arguments abi.Arguments, data []byte) error {
	if len(data) > maxDocumentBytes {
		return ErrLimit
	}
	shapes := make([]*shape, 0, len(arguments))
	nodes := maxNodes
	for _, argument := range arguments {
		if argument.Indexed {
			continue
		}
		s, ok := makeShape(&argument.Type, 0, &nodes)
		if !ok {
			return ErrLimit
		}
		shapes = append(shapes, s)
	}
	v := valueBudget{data: data, nodes: maxNodes, bytes: maxValueBytes}
	if !v.tuple(shapes, 0) {
		return ErrLimit
	}
	return nil
}

type shape struct {
	kind    byte
	size    int
	head    int
	dynamic bool
	elem    *shape
	fields  []*shape
}

func makeShape(t *abi.Type, depth int, nodes *int) (*shape, bool) {
	if t == nil || depth > maxDepth || *nodes <= 0 {
		return nil, false
	}
	*nodes--
	s := &shape{kind: t.T, size: t.Size, head: 32}
	switch t.T {
	case abi.StringTy, abi.BytesTy:
		s.dynamic = true
	case abi.ArrayTy, abi.SliceTy:
		var ok bool
		s.elem, ok = makeShape(t.Elem, depth+1, nodes)
		if !ok || t.Size < 0 || t.Size > maxNodes {
			return nil, false
		}
		s.dynamic = t.T == abi.SliceTy || s.elem.dynamic
		if !s.dynamic {
			if s.elem.head > maxValueBytes/max(1, t.Size) {
				return nil, false
			}
			s.head = t.Size * s.elem.head
		}
	case abi.TupleTy:
		total := 0
		for _, field := range t.TupleElems {
			child, ok := makeShape(field, depth+1, nodes)
			if !ok || child.head > maxValueBytes-total {
				return nil, false
			}
			s.fields = append(s.fields, child)
			total += child.head
			s.dynamic = s.dynamic || child.dynamic
		}
		if !s.dynamic {
			s.head = total
		}
	}
	return s, true
}

type valueBudget struct {
	data         []byte
	nodes, bytes int
}

func (v *valueBudget) word(position int) (int, bool) {
	if position < 0 || position > len(v.data)-32 {
		return 0, false
	}
	word := v.data[position : position+32]
	for _, b := range word[:24] {
		if b != 0 {
			return 0, false
		}
	}
	value := binary.BigEndian.Uint64(word[24:])
	if value > uint64(len(v.data)) {
		return 0, false
	}
	return int(value), true
}

func (v *valueBudget) tuple(fields []*shape, base int) bool {
	cursor := base
	for _, field := range fields {
		if !v.at(field, base, cursor) {
			return false
		}
		cursor += field.head
	}
	return true
}

func (v *valueBudget) at(s *shape, base, position int) bool {
	if position < 0 || position > len(v.data)-s.head {
		return false
	}
	if s.dynamic {
		offset, ok := v.word(position)
		if !ok || offset > len(v.data)-base {
			return false
		}
		position = base + offset
	}
	return v.value(s, position)
}

func (v *valueBudget) value(s *shape, position int) bool {
	if v.nodes <= 0 {
		return false
	}
	v.nodes--
	switch s.kind {
	case abi.StringTy, abi.BytesTy:
		size, ok := v.word(position)
		if !ok || size > len(v.data)-position-32 || size > v.bytes {
			return false
		}
		v.bytes -= size
	case abi.ArrayTy, abi.SliceTy:
		count := s.size
		if s.kind == abi.SliceTy {
			var ok bool
			count, ok = v.word(position)
			if !ok {
				return false
			}
			position += 32
		}
		if count > v.nodes || position > len(v.data) || (s.elem.head > 0 && count > (len(v.data)-position)/s.elem.head) {
			return false
		}
		for i := 0; i < count; i++ {
			if !v.at(s.elem, position, position+i*s.elem.head) {
				return false
			}
		}
	case abi.TupleTy:
		return v.tuple(s.fields, position)
	default:
		if position < 0 || position > len(v.data)-32 || v.bytes < 32 {
			return false
		}
		v.bytes -= 32
	}
	return true
}
