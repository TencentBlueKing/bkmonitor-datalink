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
	"testing"
	"time"
)

// A fixed threshold cannot serve a population whose periods run from ten
// seconds to ten minutes. The same twenty-four seconds is two missed turns for
// one object and a normal round in progress for another, and any single number
// calls one of them wrong.
func TestHowLateIsLateIsMeasuredInTheObjectsOwnPeriod(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	wakes := []OverdueWake{
		// Ten-second object, twenty-four seconds past its wake: two turns gone.
		{QueryGroup: "fast", WakeAt: now.Add(-24 * time.Second), IntervalSeconds: 10},
		// Minute object, the same twenty-four seconds: a slow round, not a miss.
		{QueryGroup: "slow", WakeAt: now.Add(-24 * time.Second), IntervalSeconds: 60},
	}

	anomalies, facts := OverdueAnomalies(wakes, len(wakes), now, "pod-a", nil)

	if len(anomalies) != 1 || anomalies[0].QueryGroup != "fast" {
		t.Fatalf("anomalies = %+v, want only the object that has actually missed turns", anomalies)
	}
	if facts.Total != 1 || facts.Truncated {
		t.Fatalf("facts = %+v, want one overdue object and no truncation", facts)
	}
	if anomalies[0].Kind != KindOverdueWake || anomalies[0].ReasonCode != ReasonWakeMissed {
		t.Fatalf("anomaly = %+v, want the overdue classification", anomalies[0])
	}
	// The duration column has to read as "how long it has not been evaluated",
	// so it starts when the object should have been picked up.
	if !anomalies[0].Since.Equal(now.Add(-24 * time.Second)) {
		t.Fatalf("since = %s, want the wake time it missed", anomalies[0].Since)
	}
}

// An object with no period stated still has to be reported once its wake time
// has passed. Dropping it would turn a missing field into silence about the
// object, and silence here is what health looks like.
func TestAnObjectWithNoStatedPeriodIsStillReported(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	anomalies, _ := OverdueAnomalies([]OverdueWake{
		{QueryGroup: "unknown-period", WakeAt: now.Add(-time.Second)},
	}, 1, now, "pod-a", nil)

	if len(anomalies) != 1 {
		t.Fatalf("anomalies = %+v, want the object kept rather than silently dropped", anomalies)
	}
}

// A truncated list is a floor, not a count. Reporting it as a count would
// understate the incident by exactly the amount that made it worth reporting --
// the same reason the anomaly list carries anomalies_total.
func TestATruncatedOverdueListSaysSo(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	_, facts := OverdueAnomalies([]OverdueWake{
		{QueryGroup: "a", WakeAt: now.Add(-time.Hour), IntervalSeconds: 60},
	}, 250, now, "pod-a", nil)

	if !facts.Truncated {
		t.Fatalf("facts = %+v, want the published list marked as a sample", facts)
	}
}

// Absent and zero are different answers and the second is the good news. A
// deployment with nothing holding wake times must not report that nothing is
// being missed.
func TestNoDueIndexIsNotTheSameAsNothingOverdue(t *testing.T) {
	at := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	silent := Aggregate(Expectation{QueryGroups: 20, Known: true},
		[]Snapshot{{Replica: "pod-a", TakenAt: at, Owned: 20, Determined: 20}},
		[]string{"pod-a"}, at, time.Minute)
	if silent.Overdue != nil {
		t.Fatalf("overdue = %+v, want absent where nothing holds wake times", silent.Overdue)
	}

	watching := Aggregate(Expectation{QueryGroups: 20, Known: true},
		[]Snapshot{{Replica: "pod-a", TakenAt: at, Owned: 20, Determined: 20, Overdue: &OverdueFacts{}}},
		[]string{"pod-a"}, at, time.Minute)
	if watching.Overdue == nil || watching.Overdue.Total != 0 {
		t.Fatalf("overdue = %+v, want a reported zero", watching.Overdue)
	}
}

// Counts add up because each replica parks its own objects; the oldest is the
// worst one anywhere, which is the number that decides whether anyone has to
// act. Averaging it would describe no replica.
func TestOverdueAddsUpAndKeepsTheWorstWait(t *testing.T) {
	at := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	view := Aggregate(Expectation{QueryGroups: 40, Known: true}, []Snapshot{
		{Replica: "pod-a", TakenAt: at, Owned: 20, Determined: 20,
			Overdue: &OverdueFacts{Total: 2, OldestSeconds: 30}},
		{Replica: "pod-b", TakenAt: at, Owned: 20, Determined: 20,
			Overdue: &OverdueFacts{Total: 3, OldestSeconds: 900, Truncated: true}},
	}, []string{"pod-a", "pod-b"}, at, time.Minute)

	if view.Overdue.Total != 5 || view.Overdue.OldestSeconds != 900 || !view.Overdue.Truncated {
		t.Fatalf("overdue = %+v, want 5 objects, the 900s worst case, and the truncation kept", view.Overdue)
	}
}

// An overdue object is not a stalled one. Stalled says the rounds stopped
// finishing; this says they stopped starting -- or started and never came back.
// Marking it stalled as well would report one condition as two.
func TestAnOverdueObjectIsNotAlsoMarkedStalled(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	anomalies, _ := OverdueAnomalies([]OverdueWake{
		{QueryGroup: "parked", WakeAt: now.Add(-time.Hour), IntervalSeconds: 60},
	}, 1, now, "pod-a", nil)

	MarkStalled(anomalies, now, time.Minute)

	if anomalies[0].Stalled {
		t.Fatal("an object that is not being run was also reported as a round that will not finish")
	}
}

// The kind reaches the metric label set, which is closed. A kind that falls
// through to OTHER is a kind nobody can alert on separately, and this is the
// one condition that does not resolve on its own.
func TestTheOverdueKindIsInTheClosedLabelSet(t *testing.T) {
	if got := MetricKind(KindOverdueWake); got != KindOverdueWake {
		t.Fatalf("MetricKind(%q) = %q, want the kind itself rather than the OTHER bucket",
			KindOverdueWake, got)
	}
}
