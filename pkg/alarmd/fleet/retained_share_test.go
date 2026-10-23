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
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// shareShare is a share whose 95 percent is a whole number of bytes, so the
// two sides of the threshold are one byte apart.
const shareShare uint64 = 1_000_000

// slotCompleted is one completed Slot of the object holding retained bytes
// against the share, as the production completion row carries them.
func slotCompleted(queryGroup string, retained, share uint64, slot time.Time) observability.Observation {
	return observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageSlotCompleted,
		ExecuteOutcome: "COMPLETED", Result: observability.ResultSuccess,
		Trace: observability.TraceFields{QueryGroupKey: queryGroup, StrategyID: "4101", BusinessID: "2",
			EvaluationTime: slot.Unix()},
		SlotBudgetUsage: &observability.SlotBudgetUsageFacts{RetainedBytes: retained, RetainedShareBytes: share},
	}
}

func shareRows(tracker *Tracker) map[string]Anomaly {
	return rowsOfKind(tracker.RetainedShare(), KindRetainedShareApproaching)
}

// The threshold is a boundary, tested one byte either side of it, at the
// value the rule is decided on rather than at the rounded percent the page
// shows: 949,999 bytes is 94 percent rounded down and must not list, 950,000
// is exactly 95 and must.
func TestAnObjectIsListedFromNinetyFivePercentOfItsShareAndNotBefore(t *testing.T) {
	at := &clock{at: time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)}
	tracker := newTracker(t, at)
	below := shareShare*RetainedShareApproachPercent/100 - 1
	tracker.Observe(context.Background(), slotCompleted("qg-below", below, shareShare, at.at))
	tracker.Observe(context.Background(), slotCompleted("qg-at", below+1, shareShare, at.at))

	rows := shareRows(tracker)
	if _, listed := rows["qg-below"]; listed {
		t.Errorf("an object one byte under %d%% of its share is listed", RetainedShareApproachPercent)
	}
	row, listed := rows["qg-at"]
	if !listed {
		t.Fatalf("an object at exactly %d%% of its share is not listed: rows %+v", RetainedShareApproachPercent, rows)
	}
	facts := row.RetainedShare
	if facts == nil || facts.RetainedBytes != below+1 || facts.ShareBytes != shareShare || facts.PercentOfShare != 95 {
		t.Fatalf("row facts = %+v, want the completion's bytes, its share and 95", facts)
	}
	if len(row.Strategies) != 1 || row.Strategies[0].StrategyID != "4101" {
		t.Errorf("row strategies = %+v, want the strategy the page groups this line by", row.Strategies)
	}
}

// Only a Slot that completed carries a reading. A failed Slot reports what it
// had taken when it stopped - usually less - and letting that clear the row
// would take the warning away on the round the object is in trouble.
func TestAFailedSlotDoesNotClearTheReading(t *testing.T) {
	at := &clock{at: time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)}
	tracker := newTracker(t, at)
	tracker.Observe(context.Background(), slotCompleted("qg", shareShare*97/100, shareShare, at.at))
	failed := slotCompleted("qg", 0, shareShare, at.at.Add(time.Minute))
	failed.Err, failed.Result = errors.New("query timeout"), observability.ResultFailed
	tracker.Observe(context.Background(), failed)
	if _, listed := shareRows(tracker)["qg"]; !listed {
		t.Fatal("a failed Slot reporting zero retained bytes cleared the object from the line")
	}
}

// A refused attempt at the share keeps the object on this line at the
// reading it had: the round the share refuses is the round the line is for,
// and an object refused on every round would otherwise never be on it.
func TestAShareRefusedAttemptKeepsTheObjectOnThisLine(t *testing.T) {
	at := &clock{at: time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)}
	tracker := newTracker(t, at)
	tracker.Observe(context.Background(), slotCompleted("qg", shareShare*99/100, shareShare, at.at))
	refused := slotCompleted("qg", 0, shareShare, at.at.Add(time.Minute))
	refused.Err, refused.Result = errors.New("share exceeded"), observability.ResultTerminal
	refused.ReasonCode = observability.ReasonCode(contract.ReasonQGBudgetShareExceeded)
	tracker.Observe(context.Background(), refused)
	row, listed := shareRows(tracker)["qg"]
	if !listed || row.RetainedShare.RetainedBytes != shareShare*99/100 {
		t.Fatalf("listed %t row %+v: a refused attempt took the object off the line or replaced its reading", listed, row.RetainedShare)
	}
}

// A refusal at the share keeps the object on this line and keeps its Since:
// refusals come and go, and restarting Since at each one would hide an
// object growing into its share behind a clock that never runs long.
func TestARefusalBetweenCompletionsKeepsSince(t *testing.T) {
	at := &clock{at: time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)}
	tracker := newTracker(t, at)
	first := at.at
	tracker.Observe(context.Background(), slotCompleted("qg", shareShare*97/100, shareShare, at.at))
	at.at = at.at.Add(time.Minute)
	refused := slotCompleted("qg", 0, shareShare, at.at)
	refused.Err, refused.Result = errors.New("share exceeded"), observability.ResultTerminal
	refused.ReasonCode = observability.ReasonCode(contract.ReasonQGBudgetShareExceeded)
	tracker.Observe(context.Background(), refused)
	if row, listed := shareRows(tracker)["qg"]; !listed || !row.Since.Equal(first) {
		t.Fatalf("listed %v since %v on the refused round, want listed since %v", listed, row.Since, first)
	}
	at.at = at.at.Add(time.Minute)
	tracker.Observe(context.Background(), slotCompleted("qg", shareShare*98/100, shareShare, at.at))
	row, listed := shareRows(tracker)["qg"]
	if !listed || !row.Since.Equal(first) {
		t.Fatalf("listed %v since %v after a refusal between completions, want listed since %v", listed, row.Since, first)
	}
}

// Since is when the object reached the threshold, kept across the rounds it
// stays there and started again after a round below it: how long it has been
// near the wall is what says whether it is growing into it.
func TestSinceIsWhenTheObjectReachedTheThreshold(t *testing.T) {
	at := &clock{at: time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)}
	tracker := newTracker(t, at)
	first := at.at
	tracker.Observe(context.Background(), slotCompleted("qg", shareShare*96/100, shareShare, at.at))
	at.at = at.at.Add(time.Minute)
	tracker.Observe(context.Background(), slotCompleted("qg", shareShare*97/100, shareShare, at.at))
	if row := shareRows(tracker)["qg"]; !row.Since.Equal(first) || !row.RetainedShare.Since.Equal(first) {
		t.Fatalf("since = %v, want the first round at the threshold %v", row.Since, first)
	}
	at.at = at.at.Add(time.Minute)
	tracker.Observe(context.Background(), slotCompleted("qg", shareShare*50/100, shareShare, at.at))
	if _, listed := shareRows(tracker)["qg"]; listed {
		t.Fatal("an object back under the threshold is still listed")
	}
	at.at = at.at.Add(time.Minute)
	again := at.at
	tracker.Observe(context.Background(), slotCompleted("qg", shareShare*96/100, shareShare, at.at))
	if row := shareRows(tracker)["qg"]; !row.Since.Equal(again) {
		t.Fatalf("since = %v after a round below, want the round it came back %v", row.Since, again)
	}
}

// The row says which phase filled the share, because that says who acts: the
// state phase is the strategy's retention, the output phase is what this build
// holds per round. Four distinct numbers, so a field carried under another's
// name cannot pass.
func TestTheRowSaysWhichPhaseFilledTheShare(t *testing.T) {
	at := &clock{at: now.Add(-time.Minute)}
	tracker := NewTracker(nil, "pod-a", at.Now)
	observed := slotCompleted("qg", shareShare*97/100, shareShare, at.at)
	observed.SlotBudgetUsage.RetainedInputBytes = 11
	observed.SlotBudgetUsage.RetainedStateBytes = 22
	observed.SlotBudgetUsage.RetainedOutputBytes = shareShare*97/100 - 11 - 22 - 44
	observed.SlotBudgetUsage.RetainedGapBytes = 44
	tracker.Observe(context.Background(), observed)
	facts := shareRows(tracker)["qg"].RetainedShare
	if facts == nil || facts.RetainedInputBytes != 11 || facts.RetainedStateBytes != 22 || facts.RetainedGapBytes != 44 ||
		facts.RetainedOutputBytes != shareShare*97/100-77 || facts.ThresholdPercent != RetainedShareApproachPercent {
		t.Fatalf("row facts = %+v, want the four phases and the threshold as the completion carried them", facts)
	}
	snapshots := healthySnapshots()
	snapshots[0].RetainedShare = tracker.RetainedShare()
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, []string{"pod-a", "pod-b"})
	entry := requestJSON(t, handler, "/api/health")["retained_share"].([]any)[0].(map[string]any)
	for field, want := range map[string]float64{"retained_input_bytes": 11, "retained_state_bytes": 22, "retained_gap_bytes": 44,
		"threshold_percent": RetainedShareApproachPercent} {
		if entry[field] != want {
			t.Errorf("health entry %s = %v, want %v", field, entry[field], want)
		}
	}
}

// A completion without a share is a producer that did not carry one - an
// older build during a roll - and lists nothing rather than dividing by it.
func TestACompletionWithoutAShareListsNothing(t *testing.T) {
	at := &clock{at: time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)}
	tracker := newTracker(t, at)
	tracker.Observe(context.Background(), slotCompleted("qg", shareShare, 0, at.at))
	if rows := shareRows(tracker); len(rows) != 0 {
		t.Fatalf("rows = %+v, want none without a share", rows)
	}
}

// The line end to end, from the replica's snapshot: the merged view carries
// the rows, the object lands on RETAINED_SHARE_APPROACHING under its strategy,
// and the line is the strategy's while it is still detecting.
func TestTheSnapshotRowsReachTheirLine(t *testing.T) {
	now := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	at := &clock{at: now}
	tracker := newTracker(t, at)
	tracker.Observe(context.Background(), slotCompleted("qg-full", shareShare*96/100, shareShare, now))
	snapshot := Snapshot{Replica: "pod-a", TakenAt: now, RetainedShare: tracker.RetainedShare()}
	view := Aggregate(Expectation{Known: true, QueryGroups: 1}, []Snapshot{snapshot}, []string{"pod-a"}, now, time.Minute)
	if len(view.RetainedShare) != 1 {
		t.Fatalf("view carries %d rows, want the snapshot's one", len(view.RetainedShare))
	}
	finding := view.RetainedShare[0].Finding
	if finding.Check != CheckRetainedShareApproaching || finding.Owner != OwnerStrategy || finding.Result != ResultCompleted {
		t.Fatalf("finding = %+v, want the strategy's approaching line with the round completed", finding)
	}
	var line *CheckReport
	for _, report := range ReportChecks(nil, nil, &view, now) {
		if report.Code == CheckRetainedShareApproaching {
			line = &report
			break
		}
	}
	if line == nil || line.Current != 1 {
		t.Fatalf("line = %+v, want RETAINED_SHARE_APPROACHING with the one object", line)
	}
	if pair := checkWords[CheckRetainedShareApproaching]; pair.State != StateDetecting || pair.Action != ActionStrategyEdit {
		t.Errorf("words = %+v, want detecting and the strategy's to act on", pair)
	}
}

// The health response - what the page opens on and what alarmd-cli's
// fleet.get returns - names the objects, fullest first, with how close each
// is. Read as JSON through the route, because the response struct having the
// field is not the same as the route filling it.
func TestTheHealthResponseNamesTheObjectsNearTheirShare(t *testing.T) {
	snapshots := healthySnapshots()
	at := &clock{at: now.Add(-time.Minute)}
	tracker := NewTracker(nil, "pod-a", at.Now)
	tracker.Observe(context.Background(), slotCompleted("qg-96", shareShare*96/100, shareShare, at.at))
	tracker.Observe(context.Background(), slotCompleted("qg-99", shareShare*99/100, shareShare, at.at))
	tracker.Observe(context.Background(), slotCompleted("qg-50", shareShare*50/100, shareShare, at.at))
	snapshots[0].RetainedShare = tracker.RetainedShare()
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, []string{"pod-a", "pod-b"})

	body := requestJSON(t, handler, "/api/health")
	list, ok := body["retained_share"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("retained_share = %v, want the two objects past the threshold and not the one at half", body["retained_share"])
	}
	first := list[0].(map[string]any)
	if first["query_group"] != "qg-99" || first["percent_of_share"].(float64) != 99 || first["share_bytes"].(float64) != float64(shareShare) {
		t.Fatalf("first entry = %v, want the fullest object with its percent and share", first)
	}
	if strategies, _ := first["strategies"].([]any); len(strategies) != 1 {
		t.Fatalf("first entry strategies = %v, want the strategy to act on", first["strategies"])
	}
}

// shareGivenUp is one Slot given up because the share refused it, as the
// scheduler's slot_skipped line carries the refusal's bytes.
func shareGivenUp(queryGroup string, ownUsed, requested, share uint64, slot time.Time) observability.Observation {
	return observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageSlotSkipped,
		Result: observability.ResultDegraded, ReasonCode: "GAP_SKIPPED",
		Trace: observability.TraceFields{QueryGroupKey: queryGroup, StrategyID: "4101", BusinessID: "2",
			EvaluationTime: slot.Unix()},
		SlotSkipped: &observability.SlotSkippedFacts{Path: observability.SlotSkippedQueryFree,
			FirstSlot: slot.Unix(), LastSlot: slot.Unix(), SlotCount: 1, Reason: "GAP_SKIPPED",
			Cause: observability.CompletionCauseResourceShareExceeded,
			Share: &observability.SlotSkipShareFacts{Phase: "normal_output", OwnUsedBytes: ownUsed,
				RequestedBytes: requested, LimitBytes: share, Refusals: 2}},
	}
}

// shareRefusedAttempt is one attempt of a Slot the share refused, which the
// Slot retries before it is given up.
func shareRefusedAttempt(queryGroup string, slot time.Time) observability.Observation {
	return observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageSlotCompleted,
		Result: observability.ResultFailed, ReasonCode: contract.ReasonQGBudgetShareExceeded, Err: errors.New("refused"),
		Trace: observability.TraceFields{QueryGroupKey: queryGroup, StrategyID: "4101", BusinessID: "2",
			EvaluationTime: slot.Unix()},
	}
}

// An object refused at its share on every Slot completes none, and it is the
// object the row exists for: it is listed from its first Slot given up, with
// the refusal's bytes against the share and marked refused, and it stays
// listed through the refused attempts of every Slot after - not dropped on
// each attempt and back on each give-up.
func TestAnObjectThatNeverCompletesStaysOnTheRowAtTheRefusalsBytes(t *testing.T) {
	at := &clock{at: time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)}
	tracker := newTracker(t, at)
	const group = "qg-never"
	first := at.at
	tracker.Observe(context.Background(), shareRefusedAttempt(group, first))
	if _, listed := shareRows(tracker)[group]; listed {
		t.Fatal("listed before any reading: a refused attempt carries no bytes")
	}
	tracker.Observe(context.Background(), shareGivenUp(group, shareShare-1000, 5000, shareShare, first))
	for minute := 1; minute <= 3; minute++ {
		at.at = first.Add(time.Duration(minute) * time.Minute)
		tracker.Observe(context.Background(), shareRefusedAttempt(group, at.at))
		row, listed := shareRows(tracker)[group]
		if !listed {
			t.Fatalf("minute %d: an object refused on every Slot dropped off the row on a refused attempt", minute)
		}
		if row.Since != first {
			t.Fatalf("minute %d: since %s, want the first Slot given up %s", minute, row.Since, first)
		}
		tracker.Observe(context.Background(), shareGivenUp(group, shareShare-1000, 5000, shareShare, at.at))
	}
	row, listed := shareRows(tracker)[group]
	if !listed {
		t.Fatal("an object refused on every Slot is not on the row")
	}
	facts := row.RetainedShare
	if facts == nil || !facts.Refused || facts.RetainedBytes != shareShare+4000 || facts.ShareBytes != shareShare ||
		facts.PercentOfShare != 100 || facts.Slot != at.at.Unix() || row.Since != first {
		t.Fatalf("row facts %+v since %s, want refused at %d of %d for Slot %d since %s",
			facts, row.Since, shareShare+4000, shareShare, at.at.Unix(), first)
	}
}

// An object that grew into its share and was then refused reads as near the
// wall since its first completed Slot at the threshold, not since the
// refusal; and one that completes again under the threshold leaves the row.
func TestARefusalKeepsTheSinceTheObjectReachedTheThresholdAt(t *testing.T) {
	at := &clock{at: time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)}
	tracker := newTracker(t, at)
	const group = "qg-grew"
	near := shareShare * 98 / 100
	start := at.at
	tracker.Observe(context.Background(), slotCompleted(group, near, shareShare, start))
	at.at = start.Add(5 * time.Minute)
	tracker.Observe(context.Background(), shareGivenUp(group, near, shareShare/10, shareShare, at.at))
	row, listed := shareRows(tracker)[group]
	if !listed || !row.RetainedShare.Refused || row.Since != start {
		t.Fatalf("listed %t row %+v since %s, want refused since the first Slot at the threshold %s", listed, row.RetainedShare, row.Since, start)
	}
	at.at = start.Add(10 * time.Minute)
	tracker.Observe(context.Background(), slotCompleted(group, shareShare/2, shareShare, at.at))
	if _, listed := shareRows(tracker)[group]; listed {
		t.Fatal("an object whose Slot completed at half its share is still on the row")
	}
}

// A round that completes any way other than a Slot given up for the share
// ends the refused reading, even when it carried no bytes to replace it: the
// object leaves the line rather than reading refused until a completion
// with usage comes.
func TestARoundCompletedWithoutUsageEndsTheRefusedReading(t *testing.T) {
	at := &clock{at: time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)}
	tracker := newTracker(t, at)
	const group = "qg-unrefused"
	tracker.Observe(context.Background(), shareGivenUp(group, shareShare, 5000, shareShare, at.at))
	if row, listed := shareRows(tracker)[group]; !listed || !row.RetainedShare.Refused {
		t.Fatalf("fixture: listed %t row %+v, want the refused reading", listed, row.RetainedShare)
	}
	at.at = at.at.Add(time.Minute)
	tracker.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentProgress, Stage: observability.StageProgressCommitted,
		ProgressCompletionKind: "GAP_SKIPPED", ProgressCompletionCause: "EXPIRED_REPLAY", ProgressCompletionReason: "GAP_SKIPPED",
		Trace: observability.TraceFields{QueryGroupKey: group, StrategyID: "4101", BusinessID: "2", EvaluationTime: at.at.Unix()},
	})
	if row, listed := shareRows(tracker)[group]; listed {
		t.Fatalf("a round completed for another cause left the refused reading on the line: %+v", row.RetainedShare)
	}
}

// A Slot given up for the share itself keeps the reading: its own progress
// line is not a round that ends the refusal.
func TestAShareGiveUpsOwnCompletionKeepsTheRefusedReading(t *testing.T) {
	at := &clock{at: time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)}
	tracker := newTracker(t, at)
	const group = "qg-still-refused"
	tracker.Observe(context.Background(), shareGivenUp(group, shareShare, 5000, shareShare, at.at))
	tracker.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentProgress, Stage: observability.StageProgressCommitted,
		ProgressCompletionKind: "GAP_SKIPPED", ProgressCompletionCause: observability.CompletionCauseResourceShareExceeded,
		ProgressCompletionReason: "GAP_SKIPPED",
		Trace:                    observability.TraceFields{QueryGroupKey: group, StrategyID: "4101", BusinessID: "2", EvaluationTime: at.at.Unix()},
	})
	if row, listed := shareRows(tracker)[group]; !listed || !row.RetainedShare.Refused {
		t.Fatalf("listed %t row %+v: the give-up's own completion ended the refused reading", listed, row.RetainedShare)
	}
}
