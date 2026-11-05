//go:build runtimee2e

package runtimee2e

import (
	"context"
	"strings"
	"time"

	"github.com/islishude/etherview/internal/testdbguard"
)

func (h *harness) startDeadlockGuard(ctx context.Context) {
	if h.deadlockGuard != nil {
		return
	}
	if err := h.project.Up(ctx, "postgres"); err != nil {
		h.t.Fatal(err)
	}
	binding, err := h.project.Port(ctx, "postgres", 5432)
	if err != nil {
		h.t.Fatal(err)
	}
	h.deadlockGuard, err = testdbguard.Start(ctx, "postgres://etherview:etherview-runtime-e2e@"+binding+"/etherview?sslmode=disable")
	if err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) finishDeadlockGuard() {
	if h.deadlockGuard == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Stop every enabled Compose service except the database. Derive the set
	// from the rendered topology so new roles cannot silently escape the gate.
	configured, configErr := h.project.Run(ctx, "config", "--services")
	if configErr != nil {
		h.t.Error("list services before deadlock check:", configErr)
	} else {
		args := []string{"stop", "--timeout", "20"}
		for service := range strings.FieldsSeq(string(configured)) {
			if service != "postgres" {
				args = append(args, service)
			}
		}
		if len(args) == 3 {
			h.t.Error("no clients found in runtime topology")
		} else if _, err := h.project.Run(ctx, args...); err != nil {
			h.t.Error("stop database clients before deadlock check:", err)
		}
	}

	if h.db != nil {
		pool := h.db
		h.db = nil
		closeCtx, closeCancel := context.WithTimeout(ctx, 5*time.Second)
		if err := testdbguard.ClosePool(closeCtx, pool); err != nil {
			h.t.Error(err)
		}
		closeCancel()
	}
	report, err := h.deadlockGuard.Finish(ctx)
	h.deadlockGuard = nil
	h.writeJSONArtifact("deadlocks.json", report)
	if err != nil {
		h.t.Error(err)
	}
}
