package events

import "testing"

func TestValidateSyncStatusRequiresExactSafetyHaltClassification(t *testing.T) {
	t.Parallel()
	valid := SyncStatus{
		LatestKnown: true, IndexedKnown: true, HighestCoveredKnown: true,
		BackfillComplete: true,
		ErrorCode:        "finalized_reorg",
		SafetyHalt:       true,
	}
	if err := validateSyncStatus(valid); err != nil {
		t.Fatalf("valid safety halt: %v", err)
	}
	for _, mutate := range []func(*SyncStatus){
		func(status *SyncStatus) { status.SafetyHalt = false },
		func(status *SyncStatus) { status.ErrorCode = "sync_cycle_failed" },
		func(status *SyncStatus) { status.Ready = true },
	} {
		status := valid
		mutate(&status)
		if err := validateSyncStatus(status); err == nil {
			t.Fatalf("invalid safety status was accepted: %+v", status)
		}
	}
}
