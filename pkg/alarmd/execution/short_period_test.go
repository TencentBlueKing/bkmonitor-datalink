package execution

import (
	"encoding/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"testing"
)

func TestShortPeriodSchedulePreservesOldBytesRevisionAndCadence(t *testing.T) {
	for _, interval := range []int64{10, 15, 30, 60} {
		old := ScheduleSpec{EvaluationIntervalSeconds: interval, Timezone: "UTC"}
		before, _ := json.Marshal(old)
		legacy := struct {
			Interval  int64          `json:"evaluation_interval"`
			Alignment EvaluationTime `json:"alignment"`
			Timezone  string         `json:"timezone"`
		}{interval, 0, "UTC"}
		want, _ := json.Marshal(legacy)
		revision, _ := DerivePlanScheduleRevision(old)
		oldRevision, _ := contract.DeriveCanonicalDigestV2("alarmd-plan-schedule-v1", legacy)
		if string(before) != string(want) || string(revision) != oldRevision {
			t.Fatalf("legacy bytes/revision changed: %d", interval)
		}
		var decoded ScheduleSpec
		if err := json.Unmarshal(before, &decoded); err != nil {
			t.Fatal(err)
		}
		deadline, ok := decoded.CompletionDeadlineUnixMilli(120)
		if !ok || deadline != (120+interval)*1000 {
			t.Fatal("legacy deadline changed")
		}
		if interval > 15 {
			continue
		}
		updated := old
		updated.CompletionDeadlineOffsetSeconds = 30
		newRevision, _ := DerivePlanScheduleRevision(updated)
		deadline, ok = updated.CompletionDeadlineUnixMilli(120)
		if !ok || deadline != 150000 || newRevision == revision {
			t.Fatal("new deadline not frozen")
		}
		for at := EvaluationTime(110); at < 180; at++ {
			if old.IsAligned(at) != updated.IsAligned(at) {
				t.Fatal("cadence changed")
			}
		}
	}
}
