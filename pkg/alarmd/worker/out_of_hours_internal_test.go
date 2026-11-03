// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// day is a UTC midnight, the reference the schedule's clock minutes are read
// against.
const day = int64(1_791_504_000)

func clock(hour, minute, second int64) int64 { return day + hour*3600 + minute*60 + second }

// nightPlan is in hours from 22:00 to 04:00 UTC, the end minute included:
// out of hours from 04:01 to 21:59.
func nightPlan(t *testing.T, identity execution.PlanIdentity, uptime string, snapshot bool) execution.DuePlan {
	t.Helper()
	compiled := compileEffectiveTimePlanForTest(t, identity, []uint32{1}, false, func(plan *contract.EvaluationPlanV2) {
		plan.StrategyIR.Levels[0].TriggerPlan.Config = json.RawMessage(
			`{"window_size":1,"required_anomalies":1,"step_seconds":60,"timezone_ref":"BUSINESS_LOCAL","uptime":` + uptime + `}`)
		if snapshot {
			plan.EffectiveTimeSnapshot = json.RawMessage(`{"schema_version":1,"status":"READY","business_timezone":"UTC","calendars":[]}`)
		}
	})
	return execution.DuePlan{Identity: identity, CompiledPlan: compiled}
}

const nightHours = `{"time_ranges":[{"start":"22:00","end":"04:00"}],"active_calendars":[],"calendars":[]}`

func outOfHoursHeader(due execution.DuePlan, slot, segmentStart int64) execution.InternalExecutionHeader {
	return execution.InternalExecutionHeader{
		Contract: execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: "qg-13", EvaluationTime: execution.EvaluationTime(slot)},
			ScheduleSegmentStart: execution.EvaluationTime(segmentStart)},
		DuePlans: []execution.DuePlan{due},
	}
}

func windowWithHoles(plan execution.PlanIdentity, end int64, holes ...int64) execution.HistoryCoverage {
	return execution.HistoryCoverage{Windows: []execution.WindowCoverage{{Plan: plan, LevelID: 1, Series: "series-a", Valid: 1,
		Required: uint32(len(holes) + 1), End: end, Missing: holes, MissingTotal: uint32(len(holes))}}}
}

// A hole is out of hours by the schedule at its own evaluation time -- its
// minute plus the Slot's distance from the window's end -- across both ends
// of the hours: holes after 04:00 are out, holes from 22:00 are in. A data
// minute of 04:00:30 evaluated at 04:01:00 is out, as its Level was.
func TestAHoleIsOutOfHoursByTheScheduleAtItsOwnEvaluationTime(t *testing.T) {
	identity := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "13"}
	due := nightPlan(t, identity, nightHours, true)
	coordinator := &SlotExecutionCoordinator{}
	// Ran the Plan since the day began, so no hole is before this process.
	coordinator.markOutOfHours(context.Background(), outOfHoursHeader(due, day, day), nil)

	for _, tc := range []struct {
		name   string
		slot   int64
		holes  []int64
		inside []int64
	}{
		{"across the end of the hours", clock(4, 5, 0),
			[]int64{clock(3, 58, 30), clock(3, 59, 30), clock(4, 0, 30), clock(4, 1, 30), clock(4, 2, 30)},
			[]int64{clock(4, 0, 30), clock(4, 1, 30), clock(4, 2, 30)}},
		{"across the start of the hours", clock(22, 3, 0),
			[]int64{clock(21, 57, 30), clock(21, 58, 30), clock(21, 59, 30), clock(22, 0, 30)},
			[]int64{clock(21, 57, 30), clock(21, 58, 30)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			coverage := windowWithHoles(identity, tc.slot-30, tc.holes...)
			coordinator.markOutOfHours(context.Background(), outOfHoursHeader(due, tc.slot, day), &coverage)
			if got := coverage.Windows[0].Inactive; !reflect.DeepEqual(got, tc.inside) {
				t.Fatalf("out of hours = %v, want %v", got, tc.inside)
			}
		})
	}
}

// A hole is not judged when the time it maps to is not known to be under
// this schedule: before this process first ran the Plan with it, before the
// Slot's schedule segment began, or where the schedule cannot say. Each is
// left unknown, which keeps the window in its line.
func TestAHoleWhoseTimeIsNotKnownToBeUnderTheScheduleIsNotJudged(t *testing.T) {
	identity := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "13"}
	due := nightPlan(t, identity, nightHours, true)
	holes := []int64{clock(4, 1, 30), clock(4, 2, 30), clock(4, 3, 30)}

	t.Run("before this process first ran the Plan", func(t *testing.T) {
		coordinator := &SlotExecutionCoordinator{}
		coverage := windowWithHoles(identity, clock(4, 4, 30), holes...)
		coordinator.markOutOfHours(context.Background(), outOfHoursHeader(due, clock(4, 5, 0), day), &coverage)
		if got := coverage.Windows[0].Inactive; len(got) != 0 {
			t.Fatalf("out of hours = %v on the first Slot this process ran, want none judged", got)
		}
		// From then on, the holes after its first Slot are judged.
		coverage = windowWithHoles(identity, clock(4, 6, 30), append(holes, clock(4, 4, 30), clock(4, 5, 30))...)
		coordinator.markOutOfHours(context.Background(), outOfHoursHeader(due, clock(4, 7, 0), day), &coverage)
		if got, want := coverage.Windows[0].Inactive, []int64{clock(4, 4, 30), clock(4, 5, 30)}; !reflect.DeepEqual(got, want) {
			t.Fatalf("out of hours = %v, want only the holes from this process's first Slot on %v", got, want)
		}
	})
	t.Run("a replayed Slot from before the requirement was first seen", func(t *testing.T) {
		coordinator := &SlotExecutionCoordinator{}
		coordinator.markOutOfHours(context.Background(), outOfHoursHeader(due, clock(4, 10, 0), day), nil)
		// A catch-up of 04:05 after the requirement was first seen at 04:10:
		// its holes are from before then and stay unknown.
		coverage := windowWithHoles(identity, clock(4, 4, 30), holes...)
		coordinator.markOutOfHours(context.Background(), outOfHoursHeader(due, clock(4, 5, 0), day), &coverage)
		if got := coverage.Windows[0].Inactive; len(got) != 0 {
			t.Fatalf("out of hours = %v from a replayed Slot before the requirement was first seen, want none judged", got)
		}
	})
	t.Run("before the Slot's schedule segment began", func(t *testing.T) {
		coordinator := &SlotExecutionCoordinator{}
		coordinator.markOutOfHours(context.Background(), outOfHoursHeader(due, day, day), nil)
		coverage := windowWithHoles(identity, clock(4, 4, 30), holes...)
		coordinator.markOutOfHours(context.Background(), outOfHoursHeader(due, clock(4, 5, 0), clock(4, 3, 0)), &coverage)
		if got, want := coverage.Windows[0].Inactive, []int64{clock(4, 2, 30), clock(4, 3, 30)}; !reflect.DeepEqual(got, want) {
			t.Fatalf("out of hours = %v, want only the holes from the segment's start %v", got, want)
		}
	})
	t.Run("a schedule the Worker cannot resolve", func(t *testing.T) {
		calendar := nightPlan(t, identity, `{"time_ranges":[{"start":"22:00","end":"04:00"}],"active_calendars":[7]}`, false)
		coordinator := &SlotExecutionCoordinator{}
		coordinator.markOutOfHours(context.Background(), outOfHoursHeader(calendar, day, day), nil)
		coverage := windowWithHoles(identity, clock(4, 4, 30), holes...)
		coordinator.markOutOfHours(context.Background(), outOfHoursHeader(calendar, clock(4, 5, 0), day), &coverage)
		if got := coverage.Windows[0].Inactive; len(got) != 0 {
			t.Fatalf("out of hours = %v with no calendar to read, want none: unknown is not outside", got)
		}
	})
	t.Run("a window with a hole it did not list, a round with a window it did not name", func(t *testing.T) {
		coordinator := &SlotExecutionCoordinator{}
		coordinator.markOutOfHours(context.Background(), outOfHoursHeader(due, day, day), nil)
		unlisted := windowWithHoles(identity, clock(4, 4, 30), holes...)
		unlisted.Windows[0].MissingTotal++
		coordinator.markOutOfHours(context.Background(), outOfHoursHeader(due, clock(4, 5, 0), day), &unlisted)
		unnamed := windowWithHoles(identity, clock(4, 4, 30), holes...)
		unnamed.Short = 2
		coordinator.markOutOfHours(context.Background(), outOfHoursHeader(due, clock(4, 5, 0), day), &unnamed)
		if len(unlisted.Windows[0].Inactive) != 0 || len(unnamed.Windows[0].Inactive) != 0 {
			t.Fatalf("out of hours = %v / %v, want none resolved where the reading cannot pass",
				unlisted.Windows[0].Inactive, unnamed.Windows[0].Inactive)
		}
	})
	t.Run("a Plan always in effect", func(t *testing.T) {
		always := execution.DuePlan{Identity: identity, CompiledPlan: compileEffectiveTimePlanForTest(t, identity, []uint32{1}, false)}
		coordinator := &SlotExecutionCoordinator{}
		coverage := windowWithHoles(identity, clock(4, 4, 30), holes...)
		coordinator.markOutOfHours(context.Background(), outOfHoursHeader(always, clock(4, 5, 0), day), &coverage)
		if got, remembered := coverage.Windows[0].Inactive, len(coordinator.effectiveSeen.entries); len(got) != 0 || remembered != 0 {
			t.Fatalf("out of hours = %v, %d Plans remembered, want none for a Plan always in effect", got, remembered)
		}
	})
}

// The first-seen record keeps one entry per Plan requirement: a Plan whose
// requirement changes keeps only the new one, starting from its own first
// Slot, and a Plan that stops coming is dropped after the TTL.
func TestTheFirstSeenRecordIsReplacedOnAnEditAndDroppedWhenAPlanLeaves(t *testing.T) {
	var seen effectiveTimeSeen
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "13"}
	other := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "24"}
	now := time.Unix(day, 0)
	if since := seen.note(plan, []string{"a"}, 100, now); since["a"] != 100 {
		t.Fatalf("since=%v, want the first Slot", since)
	}
	if since := seen.note(plan, []string{"a"}, 200, now); since["a"] != 100 {
		t.Fatalf("since=%v, want the first Slot kept", since)
	}
	// A replayed Slot for an earlier time runs under today's requirement;
	// it does not move the bound back.
	if since := seen.note(plan, []string{"a"}, 50, now); since["a"] != 100 {
		t.Fatalf("since=%v after a replay of an earlier Slot, want the first Slot seen kept", since)
	}
	if since := seen.note(plan, []string{"b"}, 300, now); since["b"] != 300 || len(since) != 1 {
		t.Fatalf("since=%v, want only the edited requirement, from its own first Slot", since)
	}
	if since := seen.note(other, []string{"c"}, 300, now); since["c"] != 300 {
		t.Fatalf("since=%v, want the second Plan from its own first Slot", since)
	}
	if since := seen.note(plan, []string{"b"}, 350, now); since["b"] != 300 {
		t.Fatalf("since=%v, want the first Plan kept beside the second", since)
	}
	if since := seen.note(other, []string{"c"}, 350, now); since["c"] != 300 {
		t.Fatalf("since=%v, want the second Plan kept beside the first", since)
	}
	later := now.Add(effectiveTimeSeenTTL + time.Minute)
	seen.note(plan, []string{"b"}, 400, later)
	if since := seen.note(other, []string{"c"}, 500, later); since["c"] != 500 {
		t.Fatalf("since=%v after the TTL, want the Plan that stopped coming dropped and seen afresh", since)
	}
}

// The round's worst case: MaxCoverageWindows windows of MaxWindowHolesListed
// holes each, every hole a distinct second the schedule is asked about.
func BenchmarkMarkOutOfHoursAtTheRoundsBounds(b *testing.B) {
	t := &testing.T{}
	identity := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "13"}
	due := nightPlan(t, identity, nightHours, true)
	coordinator := &SlotExecutionCoordinator{}
	coordinator.markOutOfHours(context.Background(), outOfHoursHeader(due, day, day), nil)
	slot := clock(6, 0, 0)
	var coverage execution.HistoryCoverage
	for window := 0; window < execution.MaxCoverageWindows; window++ {
		var holes []int64
		for hole := 0; hole < execution.MaxWindowHolesListed; hole++ {
			holes = append(holes, slot-30-int64(window*execution.MaxWindowHolesListed+hole+1)*60)
		}
		coverage.Windows = append(coverage.Windows, execution.WindowCoverage{Plan: identity, LevelID: 1,
			Series: execution.SeriesIdentityDigest(rune('a' + window)), End: slot - 30, Missing: holes, MissingTotal: uint32(len(holes))})
	}
	header := outOfHoursHeader(due, slot, day)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		coordinator.markOutOfHours(context.Background(), header, &coverage)
	}
}

// The commit carries the schedule's reading: a Slot out of its Plan's hours
// commits with the listed holes of its short window marked out of hours, on
// the observation the fleet reads the round from.
func TestTheCommitCarriesTheOutOfHoursHolesOnItsObservation(t *testing.T) {
	fixture := newPlanIsolationFixture(t, nil)
	healthy := fixture.header.DuePlans[1].Identity
	fixture.header.DuePlans[1].CompiledPlan = nightPlan(t, healthy, nightHours, true).CompiledPlan
	slot := int64(fixture.header.Contract.Slot.EvaluationTime)
	// The requirement was in effect for this process since the day began.
	fixture.coordinator.markOutOfHours(context.Background(), execution.InternalExecutionHeader{
		Contract: execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: "query-group", EvaluationTime: execution.EvaluationTime(slot - slot%86400)},
			ScheduleSegmentStart: execution.EvaluationTime(slot - slot%86400)},
		DuePlans: fixture.header.DuePlans,
	}, nil)
	commitReady(fixture)
	holes := []int64{slot - 60, slot}
	fixture.evaluated.Plans[1].HistoryCoverage = execution.HistoryCoverage{Levels: 1, Short: 1, WorstValid: 1, WorstRequired: 3,
		Windows: []execution.WindowCoverage{{Plan: healthy, LevelID: 1, Series: "healthy-series", Valid: 1, Required: 3, End: slot,
			Missing: holes, MissingTotal: uint32(len(holes))}}}
	if _, err := fixture.coordinator.finalizePreparedWithGaps(context.Background(), fixture.request, fixture.header, fixture.bindings,
		fixture.loaded, execution.GapLoadResult{}, fixture.evaluated, nil, queryAvailabilityEvidence{}, seriesCensus{}, nil); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	for _, observation := range fixture.observations {
		if observation.Stage != observability.StageProgressCommitted || observation.HistoryCoverage == nil {
			continue
		}
		for _, window := range observation.HistoryCoverage.Windows {
			if window.Strategy == healthy.StrategyID {
				if !reflect.DeepEqual(window.Inactive, holes) {
					t.Fatalf("committed window inactive = %v, want the out-of-hours holes %v", window.Inactive, holes)
				}
				return
			}
		}
	}
	t.Fatal("no committed observation carried the Plan's short window")
}

// staleOwnerProgress refuses every commit as the store does when the Query
// Group moved while the Slot ran.
type staleOwnerProgress struct{ execution.ProgressStore }

func (staleOwnerProgress) CommitProgress(context.Context, execution.ProgressCommitRequest) (execution.ProgressCommitResult, error) {
	return execution.ProgressCommitResult{Status: execution.ProgressStaleOwner}, nil
}

// A Progress commit refused for a stale owner is the handover's refusal: the
// progress line names OWNERSHIP_STALE_FENCE, and the Slot's error carries the
// ownership word, so the Slot line names it as the handover does. Another
// refusal keeps its status and no ownership word.
func TestAProgressCommitRefusedForAStaleOwnerNamesTheHandover(t *testing.T) {
	fixture := newPlanIsolationFixture(t, nil)
	commitReady(fixture)
	fixture.coordinator.ports.Progress = staleOwnerProgress{fixture.coordinator.ports.Progress}
	_, err := fixture.coordinator.finalizePreparedWithGaps(context.Background(), fixture.request, fixture.header, fixture.bindings,
		fixture.loaded, execution.GapLoadResult{}, fixture.evaluated, nil, queryAvailabilityEvidence{}, seriesCensus{}, nil)
	if refusal, refused := ownership.RefusalReason(err); !refused || refusal != contract.ReasonOwnershipStaleFence {
		t.Fatalf("Slot error = %v, want it to carry %s", err, contract.ReasonOwnershipStaleFence)
	}
	found := false
	for _, observation := range fixture.observations {
		if observation.Stage == observability.StageProgressCommitted {
			found = true
			if observation.ReasonCode != observability.ReasonCode(contract.ReasonOwnershipStaleFence) {
				t.Fatalf("progress line reason = %q, want %s", observation.ReasonCode, contract.ReasonOwnershipStaleFence)
			}
		}
	}
	if !found {
		t.Fatal("no progress_committed observation")
	}
	if _, refused := ownership.RefusalReason(progressNotCommitted(execution.ProgressConflict)); refused {
		t.Fatal("a conflict carried an ownership word")
	}
}
