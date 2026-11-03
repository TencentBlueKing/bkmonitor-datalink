package scheduler

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"testing"
	"time"
)

func TestShortPeriodSnapshotFallbackDeadlineAndMixedCohort(t *testing.T) {
	source := &ProductionSlotSource{queryReserve: 5 * time.Second}
	schedule := execution.FrozenQueryGroupSchedule{Plans: []execution.FrozenPlanSchedule{
		{Spec: execution.ScheduleSpec{EvaluationIntervalSeconds: 10, Timezone: "UTC", CompletionDeadlineOffsetSeconds: 30}},
		{Spec: execution.ScheduleSpec{EvaluationIntervalSeconds: 15, Timezone: "UTC", CompletionDeadlineOffsetSeconds: 30}},
	}}
	deadline, err := source.scheduleQueryDeadline(schedule, 120)
	if err != nil || deadline != 145000 || shortPeriodCohort(schedule, 120) != "10s" {
		t.Fatalf("deadline=%d err=%v", deadline, err)
	}
	if shortPeriodCohort(schedule, 135) != "15s" {
		t.Fatal("cohort not due-specific")
	}
	schedule.Plans[0].Spec.CompletionDeadlineOffsetSeconds = 0
	deadline, err = source.scheduleQueryDeadline(schedule, 120)
	if err != nil || deadline != 125000 {
		t.Fatal("old Schedule fallback gained grace")
	}
}
