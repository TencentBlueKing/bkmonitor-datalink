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
	if !anomalies[0].Since.Equal(at.Add(-time.Minute)) {
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
