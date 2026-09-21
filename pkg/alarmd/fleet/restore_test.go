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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

const restoreStaleAfter = 10 * time.Minute

func TestRestoreAfterInconclusiveObservation(t *testing.T) {
	at := time.Now()
	for _, outcome := range []string{"source_not_due", "deferred", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			tracker := NewTracker(nil, "pod-a", func() time.Time { return at })
			tracker.Observe(context.Background(), observability.Observation{
				RunOutcome: outcome,
				Trace:      observability.TraceFields{QueryGroupKey: "qg", StrategyID: "strategy"},
			})
			if tracker.HasConclusion("qg") {
				t.Fatal("inconclusive observation became a conclusion")
			}
			if !tracker.Restore("qg", RestoredState{LastCompletion: "FULL_COMPLETED", NextSlot: at}, at, restoreStaleAfter) {
				t.Fatal("inconclusive observation blocked valid history")
			}
			if tracker.Determined() != 1 || len(tracker.StrategiesFor("qg")) != 1 {
				t.Fatal("restore lost conclusion or strategy metadata")
			}
		})
	}
}

func TestRestorePreservesLiveFailureAndCapacity(t *testing.T) {
	at := time.Now()
	tracker := NewTracker(nil, "pod-a", func() time.Time { return at })
	tracker.maxTracked = 1
	tracker.Observe(context.Background(), observability.Observation{
		RunOutcome: "source_error", Trace: observability.TraceFields{QueryGroupKey: "qg"},
	})
	history := RestoredState{LastCompletion: "FULL_COMPLETED", NextSlot: at}
	if tracker.Restore("qg", history, at, restoreStaleAfter) {
		t.Fatal("history replaced current failure")
	}
	if tracker.Restore("another", history, at, restoreStaleAfter) || tracker.Tracked() != 1 {
		t.Fatal("restore exceeded tracker capacity")
	}
}

// A restart -- including a configuration reload, which changes no code -- used
// to leave the replica unable to speak for anything it owned until every object
// completed a fresh round, so the whole deployment reported UNKNOWN for as long
// as the slowest period. The evidence was never missing: the last completion is
// in the object's Progress.
func TestRestoreLetsARestartedReplicaSpeakForWhatItAlreadyKnew(t *testing.T) {
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	tracker := NewTracker(nil, "pod-a", func() time.Time { return at })

	if !tracker.Restore("qg-a", RestoredState{
		LastCompletion: "FULL_COMPLETED", NextSlot: at.Add(30 * time.Second),
	}, at, restoreStaleAfter) {
		t.Fatal("a healthy persisted completion did not restore")
	}
	if tracker.Determined() != 1 {
		t.Fatalf("determined = %d, want the restored object to be spoken for", tracker.Determined())
	}
	if len(tracker.Anomalies()) != 0 {
		t.Fatalf("a healthy completion was restored as an anomaly: %+v", tracker.Anomalies())
	}
}

// Reporting a restored object as healthy because its last round was not healthy
// enough to be an anomaly yet would be the "empty anomaly list from an empty
// tracker" failure all over again, just sourced from Redis instead of memory.
func TestRestoreDoesNotTurnAFailingObjectGreen(t *testing.T) {
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	tracker := NewTracker(nil, "pod-a", func() time.Time { return at })

	if !tracker.Restore("qg-bad", RestoredState{
		LastCompletion: "COMPLETED_WITH_UNAVAILABLE", NextSlot: at.Add(-time.Minute),
		LastFullSlot: at.Add(-90 * time.Minute),
	}, at, restoreStaleAfter) {
		t.Fatal("an unhealthy persisted completion did not restore")
	}
	anomalies := tracker.Anomalies()
	if len(anomalies) != 1 || anomalies[0].QueryGroup != "qg-bad" {
		t.Fatalf("a failing object was restored without being reported: %+v", anomalies)
	}
	if anomalies[0].ReasonCode != "COMPLETED_WITH_UNAVAILABLE" {
		t.Fatalf("the restored anomaly lost what it said: %+v", anomalies[0])
	}
	// The run started before this process did. Anchoring it at "now" would
	// restart every object's clock on every release and make a failure that has
	// lasted for hours look like it just began.
	//
	// The anchor is the last round known to have completed in full, not the
	// cursor's next slot. This assertion named NextSlot until a live deployment
	// showed what that means for a cursor that is not behind: the next slot is
	// in the future, so three objects reported a start time 38 minutes after the
	// read. It happened to be in the past here only because this fixture's
	// cursor is a minute behind.
	if !anomalies[0].Since.Equal(at.Add(-90 * time.Minute)) {
		t.Fatalf("restored anomaly age was re-anchored to the restart: %v", anomalies[0].Since)
	}
}

// A Progress cursor left far behind describes an object that stopped, not one
// between rounds, so its last completion is history rather than evidence about
// now. Leaving it undetermined holds the verdict at UNKNOWN, which is the
// honest answer and also the safe one.
func TestRestoreRefusesAProgressCursorLeftBehind(t *testing.T) {
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	tracker := NewTracker(nil, "pod-a", func() time.Time { return at })

	if tracker.Restore("qg-stale", RestoredState{
		LastCompletion: "FULL_COMPLETED", NextSlot: at.Add(-restoreStaleAfter - time.Minute),
	}, at, restoreStaleAfter) {
		t.Fatal("a stale Progress cursor was accepted as evidence about now")
	}
	if tracker.Determined() != 0 {
		t.Fatalf("determined = %d, want a stale object to stay unknown", tracker.Determined())
	}
	// An object that never completed anything says nothing either.
	if tracker.Restore("qg-new", RestoredState{NextSlot: at}, at, restoreStaleAfter) {
		t.Fatal("an object with no persisted completion was restored")
	}
}

// A round watched in this process is better evidence than a persisted cursor,
// so a restore must never move a live object backwards.
func TestRestoreLeavesAnObservedObjectAlone(t *testing.T) {
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	tracker := NewTracker(nil, "pod-a", func() time.Time { return at })
	tracker.Observe(context.Background(), observability.Observation{
		ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE",
		Trace:                  observability.TraceFields{QueryGroupKey: "qg-live"},
	})

	if tracker.Restore("qg-live", RestoredState{
		LastCompletion: "FULL_COMPLETED", NextSlot: at,
	}, at, restoreStaleAfter) {
		t.Fatal("a restore overwrote what this process had already observed")
	}
	if !tracker.HasConclusion("qg-live") {
		t.Fatal("HasConclusion did not report an object this process determined")
	}
}

// With the commit's summary of the last round, a restored object lists under
// its cause at once -- the round's reason, both ends of the reason's clock
// at the commit's time, and the summary on the row -- instead of under
// "restored without its cause" until the next round. The first round this
// process completes replaces the summary and, run under other revisions,
// reports the configuration as changed since: the restored cause is then
// history, the same way a change watched in-process is.
func TestRestoreListsTheObjectUnderThePersistedCauseAtOnce(t *testing.T) {
	at := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	tracker := NewTracker(nil, "pod-a", func() time.Time { return at })
	committed := at.Add(-4 * time.Minute)
	round := &RestoredRound{Slot: at.Add(-5 * time.Minute), CompletedAt: committed, Kind: "COMPLETED_WITH_UNAVAILABLE", ReasonCode: "QUERY_TIMEOUT",
		SnapshotRevision: "s1", QueryRevision: "q1", ScheduleRevision: "r1"}
	if !tracker.Restore("qg-1", RestoredState{LastCompletion: "COMPLETED_WITH_UNAVAILABLE", NextSlot: at.Add(-time.Minute),
		LastFullSlot: at.Add(-time.Hour), LastRound: round}, at, restoreStaleAfter) {
		t.Fatal("did not restore")
	}
	rows := tracker.Anomalies()
	if len(rows) != 1 || rows[0].CauseReason != "QUERY_TIMEOUT" || !rows[0].ReasonSince.Equal(committed) || !rows[0].ReasonLastAt.Equal(committed) ||
		rows[0].Restored == nil || rows[0].Restored.ReasonCode != "QUERY_TIMEOUT" || !rows[0].Since.Equal(at.Add(-time.Hour)) {
		t.Fatalf("rows = %+v, want the persisted reason, both clock ends at the commit, the summary on the row, the age at the last full round", rows)
	}
	Attribute(rows, at)
	if rows[0].Finding.Check != CheckBackendNotAnswering || rows[0].Finding.Group != "QUERY/UNLOCATED/TIMEOUT" {
		t.Fatalf("finding = %+v, want the restored object under its cause's line, not under the observation gap", rows[0].Finding)
	}
	// A round completed in this process under other revisions: the summary
	// leaves the row and the configuration reads as changed since.
	tracker.Observe(context.Background(), observability.Observation{
		ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE", ProgressCompletionCause: "LEVEL_OUTCOME_UNKNOWN", ProgressCompletionReason: "QUERY_TIMEOUT",
		Trace: observability.TraceFields{QueryGroupKey: "qg-1", EvaluationTime: 100, SnapshotRevision: "s2", QueryRevision: "q1", ScheduleRevision: "r1"},
	})
	rows = tracker.Anomalies()
	if len(rows) != 1 || rows[0].Restored != nil || !rows[0].ConfigChanged {
		t.Fatalf("rows after a round of this process = %+v, want the summary gone and the configuration marked changed since", rows)
	}
	// A record without the summary restores as before: kind alone, no cause,
	// under the observation gap until a round runs.
	bare := NewTracker(nil, "pod-a", func() time.Time { return at })
	bare.Restore("qg-2", RestoredState{LastCompletion: "COMPLETED_WITH_UNAVAILABLE", NextSlot: at.Add(-time.Minute), LastFullSlot: at.Add(-time.Hour)}, at, restoreStaleAfter)
	bareRows := bare.Anomalies()
	Attribute(bareRows, at)
	if len(bareRows) != 1 || bareRows[0].Restored != nil || bareRows[0].CauseReason != "" || bareRows[0].Finding.Check != CheckObservationGap {
		t.Fatalf("rows from a record without the summary = %+v, want restored without a cause, as before", bareRows)
	}
	// A healthy last round restores no row, and its commit is the success a
	// later failure is judged against.
	healthy := NewTracker(nil, "pod-a", func() time.Time { return at })
	healthy.Restore("qg-3", RestoredState{LastCompletion: "FULL_COMPLETED", NextSlot: at.Add(-time.Minute), LastFullSlot: at.Add(-time.Minute),
		LastRound: &RestoredRound{Slot: at.Add(-time.Minute), CompletedAt: at.Add(-50 * time.Second), Kind: "FULL_COMPLETED"}}, at, restoreStaleAfter)
	if rows := healthy.Anomalies(); len(rows) != 0 {
		t.Fatalf("a healthy restored round listed a row: %+v", rows)
	}
	for round := 0; round < DefaultDegradedRounds; round++ {
		healthy.Observe(context.Background(), observability.Observation{ExecuteOutcome: "error", Err: errors.New("boom"),
			Trace: observability.TraceFields{QueryGroupKey: "qg-3", EvaluationTime: 200}})
	}
	if rows := healthy.Anomalies(); len(rows) != 1 || !rows[0].LastHealthyAt.Equal(at.Add(-50*time.Second)) {
		t.Fatalf("rows after failing = %+v, want the commit's healthy round as the last success", rows)
	}
}
