package wasmcompiler

import (
	"context"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"golang.org/x/crypto/sha3"
)

// InstantiateKeccak exposes only the fixed three-i32 ABI to the Python guest.
// Status zero means success; invalid guest ranges never access host memory.
func InstantiateKeccak(ctx context.Context, r wazero.Runtime) error {
	_, err := r.NewHostModuleBuilder("etherview").NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, m api.Module, stack []uint64) {
		source, size, destination := uint32(stack[0]), uint32(stack[1]), uint32(stack[2])
		stack[0] = 1
		memory := m.Memory()
		if memory == nil {
			return
		}
		data, ok := memory.Read(source, size)
		if !ok {
			return
		}
		if _, ok = memory.Read(destination, 32); !ok {
			return
		}
		h := sha3.NewLegacyKeccak256()
		for len(data) > 0 {
			if ctx.Err() != nil {
				return
			}
			n := min(len(data), 64<<10)
			_, _ = h.Write(data[:n])
			data = data[n:]
		}
		if ctx.Err() != nil {
			return
		}
		if memory.Write(destination, h.Sum(nil)) {
			stack[0] = 0
		}
	}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).Export("keccak256").Instantiate(ctx)
	return err
}
