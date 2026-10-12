package access

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestFrozenReadHoldMovesReadinessAndPreservesDataWindow(t *testing.T) {
	ref, frozen := frozenExecution(t)
	requirement := frozen.Requirements[0]
	spec := execution.ScheduleSpec{EvaluationIntervalSeconds: 10, CompletionDeadlineOffsetSeconds: 30, Timezone: "UTC"}
	window := execution.QueryWindow{Start: int64(ref.Slot.EvaluationTime) - 60, End: int64(ref.Slot.EvaluationTime)}
	before, err := frozenConsumerReadyAt(ref, requirement, window, spec, 30*time.Second, 5*time.Second, 0)
	if err != nil {
		t.Fatal(err)
	}
	ref.ReadHoldMillis = 60_000
	after, err := frozenConsumerReadyAt(ref, requirement, window, spec, 30*time.Second, 5*time.Second, 0)
	if err != nil || after != before+60_000 {
		t.Fatalf("ready %d -> %d err=%v", before, after, err)
	}
	if got := settlingWaitWithinBudget(spec, 30*time.Second, 5*time.Second); got != execution.MinimumSettlingWait {
		t.Fatalf("settling wait = %v", got)
	}
	if window.End != int64(ref.Slot.EvaluationTime) {
		t.Fatal("a held read changed its data window")
	}
}
