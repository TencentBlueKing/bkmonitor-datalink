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

// degradeUntilListed drives one object past the degraded threshold the same way
// the pipeline does, so these tests exercise the observed path rather than
// reaching into the state behind it.
func degradeUntilListed(tracker *Tracker, queryGroup string) {
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), observability.Observation{
			Trace:                  observability.TraceFields{QueryGroupKey: queryGroup},
			ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE",
		})
	}
}

// A restored object used to take its start time from the Progress cursor's next
// slot. That is the round which has not run yet, so for an object whose cursor
// is healthy it is in the future -- by a whole period. Three objects on a live
// deployment carried a start time 38 minutes ahead of the read.
//
// The page renders that as a negative age, and the list is ordered oldest-first,
// so the object sorted to the end: the rule that exists to keep the
// longest-running objects visible was pushing the mis-stamped ones out of view.
func TestARestoredObjectDoesNotTakeItsStartTimeFromARoundThatHasNotHappened(t *testing.T) {
	at := time.Date(2026, 9, 11, 13, 22, 0, 0, time.UTC)
	tracker := NewTracker(nil, "replica-a", func() time.Time { return at })

	// A healthy hourly cursor: the next round is due in 38 minutes, and the last
	// full completion was two hours ago.
	if !tracker.Restore("qg-future-cursor", RestoredState{
		LastCompletion: "COMPLETED_WITH_UNAVAILABLE",
		NextSlot:       at.Add(38 * time.Minute),
		LastFullSlot:   at.Add(-2 * time.Hour),
	}, at, time.Hour) {
		t.Fatal("the object should have been restored from its persisted completion")
	}

	anomalies := tracker.Anomalies()
	if len(anomalies) != 1 {
		t.Fatalf("restored anomalies = %d, want 1", len(anomalies))
	}
	if anomalies[0].Since.After(at) {
		t.Errorf("Since = %s, which is after the read at %s: nothing can have started in the future",
			anomalies[0].Since, at)
	}
	if got, want := anomalies[0].Since, at.Add(-2*time.Hour); !got.Equal(want) {
		t.Errorf("Since = %s, want the last full completion %s", got, want)
	}
	if got := anomalies[0].SinceFrom; got != SinceRestoredLastFull {
		t.Errorf("SinceFrom = %q, want %q: the timestamp is a bound, and the row has to say so",
			got, SinceRestoredLastFull)
	}
}

// Nothing persisted says when an object started going wrong, so an object with
// no full completion on record has no anchor at all. Reporting the handover and
// saying that is what it is beats reporting the epoch, and beats reporting a
// round that has not run.
func TestAnObjectWithNoFullCompletionOnRecordStartsItsClockAtTheHandover(t *testing.T) {
	at := time.Date(2026, 9, 11, 13, 22, 0, 0, time.UTC)
	tracker := NewTracker(nil, "replica-a", func() time.Time { return at })

	if !tracker.Restore("qg-never-full", RestoredState{
		LastCompletion: "COMPLETED_WITH_UNAVAILABLE",
		NextSlot:       at.Add(5 * time.Minute),
	}, at, time.Hour) {
		t.Fatal("the object should have been restored from its persisted completion")
	}

	anomalies := tracker.Anomalies()
	if len(anomalies) != 1 {
		t.Fatalf("restored anomalies = %d, want 1", len(anomalies))
	}
	if got := anomalies[0].Since; !got.Equal(at) {
		t.Errorf("Since = %s, want the handover %s", got, at)
	}
	if got := anomalies[0].SinceFrom; got != SinceRestoredAtRestart {
		t.Errorf("SinceFrom = %q, want %q", got, SinceRestoredAtRestart)
	}
}

// The provenance has to be carried per object rather than stamped on the list.
// A deployment mid-restart holds both kinds at once and they render as the same
// column, which is how a bound spent a release being read as a measurement.
func TestAnObservedRunAndARestoredOneDoNotClaimTheSameProvenance(t *testing.T) {
	at := time.Date(2026, 9, 11, 13, 22, 0, 0, time.UTC)
	tracker := NewTracker(nil, "replica-a", func() time.Time { return at })

	if !tracker.Restore("qg-restored", RestoredState{
		LastCompletion: "COMPLETED_WITH_UNAVAILABLE",
		NextSlot:       at.Add(time.Hour),
		LastFullSlot:   at.Add(-30 * time.Minute),
	}, at, 2*time.Hour) {
		t.Fatal("the object should have been restored")
	}
	degradeUntilListed(tracker, "qg-observed")

	sources := map[string]SinceSource{}
	for _, anomaly := range tracker.Anomalies() {
		sources[anomaly.QueryGroup] = anomaly.SinceFrom
	}
	if len(sources) != 2 {
		t.Fatalf("listed objects = %d, want both the restored and the observed one", len(sources))
	}
	if got := sources["qg-restored"]; got != SinceRestoredLastFull {
		t.Errorf("restored object SinceFrom = %q, want %q", got, SinceRestoredLastFull)
	}
	if got := sources["qg-observed"]; got != SinceSnapshotContinuity {
		t.Errorf("observed object SinceFrom = %q, want %q", got, SinceSnapshotContinuity)
	}
	if sources["qg-restored"] == sources["qg-observed"] {
		t.Error("both objects report the same provenance, which is the state this field exists to distinguish")
	}
}

// The refusal is the backstop for whatever produces a start time next. It is
// checked apart from the restore path on purpose: the restore path is where the
// last one came from, and a guard that only covers the bug already found is not
// a guard.
func TestAStartTimeInTheFutureIsRefusedRatherThanSortedToTheEnd(t *testing.T) {
	at := time.Date(2026, 9, 11, 13, 22, 0, 0, time.UTC)
	tracker := NewTracker(nil, "replica-a", func() time.Time { return at })

	degradeUntilListed(tracker, "qg-ordinary")
	degradeUntilListed(tracker, "qg-future")

	// Reach past the API to plant the impossible value. No caller can produce it
	// today, which is the point: the guard has to hold for the one that can.
	tracker.mu.Lock()
	tracker.groups["qg-future"].runStartedAt = at.Add(2 * time.Hour)
	tracker.mu.Unlock()

	anomalies := tracker.Anomalies()
	if len(anomalies) != 2 {
		t.Fatalf("anomalies = %d, want 2", len(anomalies))
	}
	var refused *Anomaly
	for index := range anomalies {
		if anomalies[index].Since.After(at) {
			t.Fatalf("%s reports Since = %s, later than the read at %s",
				anomalies[index].QueryGroup, anomalies[index].Since, at)
		}
		if anomalies[index].QueryGroup == "qg-future" {
			refused = &anomalies[index]
		}
	}
	if refused == nil {
		t.Fatal("the object with the impossible timestamp is not in the list at all")
	}
	if refused.SinceFrom != SinceRefusedFuture {
		t.Errorf("SinceFrom = %q, want %q: a silently clamped row is indistinguishable from a healthy one",
			refused.SinceFrom, SinceRefusedFuture)
	}
}

// Every value the tracker can produce has to be in the closed list, because the
// list is what the page's wording is checked against. A value that is produced
// but unlisted renders as its raw name next to a timestamp whose meaning that
// name was supposed to explain.
func TestEverySinceSourceTheTrackerProducesIsInTheClosedList(t *testing.T) {
	listed := map[SinceSource]bool{}
	for _, source := range SinceSources {
		listed[source] = true
	}
	for _, source := range []SinceSource{
		SinceSnapshotContinuity, SinceRestoredLastFull, SinceRestoredAtRestart, SinceRefusedFuture,
	} {
		if !listed[source] {
			t.Errorf("the tracker produces %q but SinceSources does not list it", source)
		}
	}
}
