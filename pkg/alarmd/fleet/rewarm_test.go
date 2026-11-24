// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// guardedRound is one completed round under a guard carrying reason: the
// frozen trace on the round's start with fixed revisions, then a partial
// completion with the window short by short of nine.
func guardedRound(tracker *Tracker, queryGroup, reason string, short uint32) {
	tracker.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageSlotStarted,
		Result: observability.Result(observability.ResultStarted),
		Trace: observability.TraceFields{QueryGroupKey: queryGroup, StrategyID: "24", BusinessID: "2",
			SnapshotRevision: "snap-a", QueryRevision: "query-a", ScheduleRevision: "schedule-a", EvaluationTime: 1_700_000_000},
	})
	observation := completion(queryGroup, "COMPLETED_WITH_PARTIAL_GAP", "24")
	observation.ProgressCompletionCause, observation.ProgressCompletionReason = "LEVEL_OUTCOME_UNKNOWN", reason
	observation.HistoryCoverage = &observability.HistoryCoverageFacts{Levels: 3, Short: short, Guarded: 3, WorstValid: 9 - short, WorstRequired: 9}
	tracker.Observe(context.Background(), observation)
}

func classifiedRow(t *testing.T, tracker *Tracker, queryGroup string) Anomaly {
	t.Helper()
	rows := tracker.Anomalies()
	Attribute(rows, now)
	for _, row := range rows {
		if row.QueryGroup == queryGroup {
			return row
		}
	}
	t.Fatalf("no row for %s in %+v", queryGroup, rows)
	return Anomaly{}
}

// A trigger fingerprint that changes under a strategy -- a rollout that
// started keying the trigger on a switch -- moves the Plan's activation
// under a Slot, the guard carries CONFIG_DRIFT on every round after, and the
// window starts again. The revisions the rounds ran under did not move, so
// it is not the configuration line; it is this deployment's own rewarm, with
// its start, and it ends by itself. Read the old way it was WINDOW_UNDECIDED,
// "交 alarmd 维护者查", on four rounds out of five.
func TestARewarmTheActivationChangeOpenedIsAlarmdsOwnAndSaysWhenItBegan(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	began := at.at
	// The window gains a point a round, as a window starting again does.
	for short := uint32(8); short > 8-uint32(DefaultDegradedRounds+1); short-- {
		guardedRound(tracker, "qg-24", "CONFIG_DRIFT", short)
		at.at = at.at.Add(time.Minute)
	}
	row := classifiedRow(t, tracker, "qg-24")
	if row.Rewarm == nil || row.Rewarm.Trigger != "CONFIG_DRIFT" || !row.Rewarm.Since.Equal(began) {
		t.Fatalf("rewarm = %+v, want CONFIG_DRIFT since the first guarded round %s", row.Rewarm, began)
	}
	if row.Finding.Check != CheckWindowRewarming || row.Finding.Owner != OwnerAlarmd {
		t.Fatalf("check = %q owner %q, want WINDOW_REWARMING, this deployment's", row.Finding.Check, row.Finding.Owner)
	}
	if want := "计划变更后重攒（CONFIG_DRIFT，起点 " + began.Format("2006-01-02T15:04Z") + "）"; row.Finding.Group != want {
		t.Fatalf("fold = %q, want %q", row.Finding.Group, want)
	}
	if words := standingOf(row); words.State != StateDetecting || words.Action != ActionWatch || words.Watch != WatchWindowFilling {
		t.Fatalf("words = %+v, want detecting and a wait", words)
	}

	// Past the rounds a window of nine takes to fill, the window is not
	// refilling by itself: the undecided window it was.
	for i := 0; i < 9; i++ {
		guardedRound(tracker, "qg-24", "CONFIG_DRIFT", 4)
	}
	if row := classifiedRow(t, tracker, "qg-24"); row.Finding.Check != CheckWindowUndecided {
		t.Fatalf("after %d short rounds: check = %q, want WINDOW_UNDECIDED", row.Coverage.ShortRounds, row.Finding.Check)
	}
}

// A restart's catch-up gives up Slots of short-period objects, and the gap
// raises a guard whose reason is the skip. Within the restart grace of the
// process start, with no cooldown holding the Slot, the window that guard
// holds is the restart's doing, as its skip record is (AFTER_RESTART). The
// same guard on a skip long after the start, or on a Slot a cooldown held,
// stays the undecided window.
func TestARewarmTheRestartsCatchUpOpenedIsAlarmdsOwnAndOthersAreNot(t *testing.T) {
	restart := &clock{at: now}
	tracker := newTracker(t, restart)
	restart.at = restart.at.Add(time.Minute)
	skippedAt := restart.at
	tracker.Observe(context.Background(), completion("qg-restart", "GAP_SKIPPED", "24"))
	for i := 0; i < DefaultDegradedRounds+1; i++ {
		restart.at = restart.at.Add(time.Minute)
		guardedRound(tracker, "qg-restart", "GAP_SKIPPED", 2)
	}
	row := classifiedRow(t, tracker, "qg-restart")
	if row.Rewarm == nil || !row.Rewarm.Since.Equal(skippedAt) || row.Finding.Check != CheckWindowRewarming {
		t.Fatalf("restart catch-up: rewarm %+v check %q, want WINDOW_REWARMING since the skip %s", row.Rewarm, row.Finding.Check, skippedAt)
	}
	if !strings.HasPrefix(row.Finding.Group, "本副本重启或接手时跳过的轮次后重攒（GAP_SKIPPED") {
		t.Fatalf("fold = %q, want the restart's catch-up named", row.Finding.Group)
	}

	// Long after the start and after first seeing the object.
	late := &clock{at: now}
	tracker = newTracker(t, late)
	late.at = late.at.Add(10 * time.Minute)
	tracker.Observe(context.Background(), completion("qg-late", "FULL_COMPLETED", "24"))
	late.at = late.at.Add(10 * time.Minute)
	tracker.Observe(context.Background(), completion("qg-late", "GAP_SKIPPED", "24"))
	for i := 0; i < DefaultDegradedRounds+1; i++ {
		late.at = late.at.Add(time.Minute)
		guardedRound(tracker, "qg-late", "GAP_SKIPPED", 2)
	}
	if row := classifiedRow(t, tracker, "qg-late"); row.Rewarm != nil || row.Finding.Check != CheckWindowUndecided {
		t.Fatalf("skip past the grace: rewarm %+v check %q, want none and WINDOW_UNDECIDED", row.Rewarm, row.Finding.Check)
	}

	// Within the grace, but a cooldown held the Slot: the backend's doing.
	cooled := &clock{at: now}
	tracker = newTracker(t, cooled)
	cooled.at = cooled.at.Add(time.Minute)
	held := completion("qg-cooled", "GAP_SKIPPED", "24")
	held.HeldBy = &observability.HeldByFacts{Decision: "query_cooldown", QueryCooldownFailures: 7}
	tracker.Observe(context.Background(), held)
	for i := 0; i < DefaultDegradedRounds+1; i++ {
		cooled.at = cooled.at.Add(time.Minute)
		guardedRound(tracker, "qg-cooled", "GAP_SKIPPED", 2)
	}
	if row := classifiedRow(t, tracker, "qg-cooled"); row.Rewarm != nil || row.Finding.Check == CheckWindowRewarming {
		t.Fatalf("cooldown-held skip: rewarm %+v check %q, want no rewarm", row.Rewarm, row.Finding.Check)
	}

	// One tracker: the restart's catch-up skip, healthy rounds that refill
	// the window, then hours later a skip that has nothing to do with the
	// restart. Its guard is not the restart's.
	one := &clock{at: now}
	tracker = newTracker(t, one)
	one.at = one.at.Add(time.Minute)
	tracker.Observe(context.Background(), completion("qg-later", "GAP_SKIPPED", "24"))
	for i := 0; i < 30; i++ {
		one.at = one.at.Add(time.Minute)
		tracker.Observe(context.Background(), completion("qg-later", "FULL_COMPLETED", "24"))
	}
	one.at = one.at.Add(5 * time.Hour)
	tracker.Observe(context.Background(), completion("qg-later", "GAP_SKIPPED", "24"))
	for i := 0; i < DefaultDegradedRounds+1; i++ {
		one.at = one.at.Add(time.Minute)
		guardedRound(tracker, "qg-later", "GAP_SKIPPED", 2)
	}
	if row := classifiedRow(t, tracker, "qg-later"); row.Rewarm != nil || row.Finding.Check != CheckWindowUndecided {
		t.Fatalf("a skip hours after the restart's: rewarm %+v check %q, want none and WINDOW_UNDECIDED", row.Rewarm, row.Finding.Check)
	}
}
