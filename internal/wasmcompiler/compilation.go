package wasmcompiler

import (
	"context"
	"github.com/tetratelabs/wazero/experimental"
	"runtime"
)

// Bound parallel code generation inside one isolated input process. This does
// not add verification workers, retain instances, or cache native machine code.
func compilationContext(ctx context.Context) context.Context {
	return experimental.WithCompilationWorkers(ctx, min(4, runtime.GOMAXPROCS(0)))
}
