package verify

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestLookupMethodsMapsDispatcherDestinations(t *testing.T) {
	t.Parallel()
	hash := crypto.Keccak256([]byte("transfer(address,uint256)"))
	var selector [4]byte
	copy(selector[:], hash[:4])
	code := selectorDispatcherFixture(selector, 11)
	code = append(code, 0x00, 0x5b, 0x00)
	methods, err := LookupMethods(MethodLookupRequest{
		Bytecode: "0x" + hex.EncodeToString(code),
		ABI: json.RawMessage(`[
			{"type":"function","name":"transfer","inputs":[
				{"name":"to","type":"address"},{"name":"amount","type":"uint256"}
			],"outputs":[]}
		]`),
		SourceMap: "0:1:0;;;;;30:40:2",
		FileIDs:   map[string]string{"2": "Token.sol"},
	})
	if err != nil {
		t.Fatalf("lookup methods: %v", err)
	}
	if len(methods) != 1 {
		t.Fatalf("methods = %#v", methods)
	}
	if methods[0].Signature != "transfer(address,uint256)" ||
		methods[0].FileName != "Token.sol" ||
		methods[0].Offset != 30 || methods[0].Length != 40 {
		t.Fatalf("method = %#v", methods[0])
	}
}

func selectorDispatcherFixture(selector [4]byte, destination uint16) []byte {
	result := []byte{byte(vm.PUSH4)}
	result = append(result, selector[:]...)
	result = append(result, byte(vm.EQ), byte(vm.PUSH2))
	var encoded [2]byte
	binary.BigEndian.PutUint16(encoded[:], destination)
	result = append(result, encoded[:]...)
	return append(result, byte(vm.JUMPI))
}

func TestLookupMethodsOmitsUnknownOrUnmappedMethods(t *testing.T) {
	t.Parallel()
	var selector [4]byte
	copy(selector[:], []byte{1, 2, 3, 4})
	code := selectorDispatcherFixture(selector, 10)
	code = append(code, 0x00, 0x5b)
	methods, err := LookupMethods(MethodLookupRequest{
		Bytecode:  "0x" + hex.EncodeToString(code),
		ABI:       json.RawMessage(`[]`),
		SourceMap: "0:1:0",
		FileIDs:   map[string]string{"0": "A.sol"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(methods) != 0 {
		t.Fatalf("unexpected methods: %#v", methods)
	}
}
