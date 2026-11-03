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
	"fmt"
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

// A wake that carries no period -- the due index's word for a round that
// could not read the object's schedule -- is not overdue however late it is:
// there is no period to be late by. It is not listed and is counted apart,
// as period unknown. The same object once its period is read, and a whole
// period late, is listed as any other.
func TestAWakeWithNoPeriodIsCountedAndNeverOverdue(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	for _, late := range []time.Duration{time.Second, time.Hour, 24 * time.Hour} {
		anomalies, facts := OverdueAnomalies([]OverdueWake{{QueryGroup: "unread", WakeAt: now.Add(-late)}}, 1, now, "pod-a", nil)
		if len(anomalies) != 0 || facts.PeriodUnknown != 1 || facts.Total != 0 || facts.OldestSeconds != 0 {
			t.Fatalf("%v late with no period: anomalies %+v facts %+v, want none listed and one period unknown", late, anomalies, facts)
		}
	}
	anomalies, facts := OverdueAnomalies([]OverdueWake{{QueryGroup: "unread", WakeAt: now.Add(-time.Hour), IntervalSeconds: 60}}, 1, now, "pod-a", nil)
	if len(anomalies) != 1 || anomalies[0].QueryGroup != "unread" || facts.PeriodUnknown != 0 || facts.Total != 1 {
		t.Fatalf("the same object with its period read: anomalies %+v facts %+v, want it listed overdue", anomalies, facts)
	}
}

// A wake is late past a whole period of its own; one with no period is never
// late, nor is a wake not written. Both the wake list and an object asked after
// alone are judged by this.
func TestAWakeIsLateByItsOwnPeriodOnly(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	for name, want := range map[string]struct {
		wake OverdueWake
		late bool
	}{
		"within its period":  {OverdueWake{WakeAt: now.Add(-30 * time.Second), IntervalSeconds: 60}, false},
		"past its period":    {OverdueWake{WakeAt: now.Add(-61 * time.Second), IntervalSeconds: 60}, true},
		"no period, passed":  {OverdueWake{WakeAt: now.Add(-time.Hour)}, false},
		"no period, ahead":   {OverdueWake{WakeAt: now.Add(time.Second)}, false},
		"no wake written":    {OverdueWake{IntervalSeconds: 60}, false},
		"no wake, no period": {OverdueWake{}, false},
	} {
		if got := want.wake.LateAt(now); got != want.late {
			t.Errorf("%s: late %t, want %t", name, got, want.late)
		}
	}
	anomalies, _ := OverdueAnomalies([]OverdueWake{{QueryGroup: "unwritten", IntervalSeconds: 60}}, 1, now, "pod-a", nil)
	if len(anomalies) != 0 {
		t.Fatalf("anomalies = %+v, want a wake never written left out", anomalies)
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
			Overdue: &OverdueFacts{Total: 2, OldestSeconds: 30, PeriodUnknown: 1}},
		{Replica: "pod-b", TakenAt: at, Owned: 20, Determined: 20,
			Overdue: &OverdueFacts{Total: 3, OldestSeconds: 900, Truncated: true, PeriodUnknown: 2}},
	}, []string{"pod-a", "pod-b"}, at, time.Minute)

	if view.Overdue.Total != 5 || view.Overdue.OldestSeconds != 900 || !view.Overdue.Truncated || view.Overdue.PeriodUnknown != 3 {
		t.Fatalf("overdue = %+v, want 5 objects, the 900s worst case, the truncation kept and 3 periods unknown", view.Overdue)
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

// The objects without a period are named beside their count, every one the
// index returned, oldest wake first, for the publisher to date; published,
// the MaxPeriodUnknownObjects longest without a period, longest first, and
// the count stays the whole number. Across replicas the same.
func TestTheObjectsWithNoPeriodAreNamedBesideTheirCount(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	var wakes []OverdueWake
	for index := 0; index < MaxPeriodUnknownObjects+5; index++ {
		wakes = append(wakes, OverdueWake{QueryGroup: fmt.Sprintf("qg-%02d", index), WakeAt: now.Add(-time.Duration(30-index) * time.Second)})
	}
	_, facts := OverdueAnomalies(wakes, len(wakes), now, "pod-a", nil)
	if facts.PeriodUnknown != MaxPeriodUnknownObjects+5 || len(facts.PeriodUnknownObjects) != MaxPeriodUnknownObjects+5 ||
		facts.PeriodUnknownObjects[0].QueryGroup != "qg-00" || facts.PeriodUnknownObjects[MaxPeriodUnknownObjects+4].QueryGroup != "qg-24" {
		t.Fatalf("facts %+v, want every one counted and named, oldest first", facts)
	}
	for index := range facts.PeriodUnknownObjects {
		facts.PeriodUnknownObjects[index].Since = now.Add(-time.Duration(index%7) * time.Minute)
	}
	longest := LongestWithoutPeriod(facts.PeriodUnknownObjects)
	if len(longest) != MaxPeriodUnknownObjects || !longest[0].Since.Equal(now.Add(-6*time.Minute)) || longest[0].QueryGroup != "qg-06" ||
		!longest[MaxPeriodUnknownObjects-1].Since.Before(longest[0].Since.Add(6*time.Minute)) {
		t.Fatalf("longest %+v, want the %d longest without a period, longest first", longest, MaxPeriodUnknownObjects)
	}
	at := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	named := func(replica string, count int, since time.Time) *OverdueFacts {
		facts := &OverdueFacts{PeriodUnknown: count}
		for index := 0; index < count; index++ {
			facts.PeriodUnknownObjects = append(facts.PeriodUnknownObjects,
				PeriodUnknownObject{QueryGroup: fmt.Sprintf("%s-%02d", replica, index), Since: since.Add(time.Duration(index) * time.Second)})
		}
		return facts
	}
	view := Aggregate(Expectation{QueryGroups: 40, Known: true}, []Snapshot{
		{Replica: "pod-a", TakenAt: at, Owned: 20, Determined: 20, Overdue: named("pod-a", 15, at.Add(-time.Minute))},
		{Replica: "pod-b", TakenAt: at, Owned: 20, Determined: 20, Overdue: named("pod-b", 15, at.Add(-time.Hour))},
	}, []string{"pod-a", "pod-b"}, at, time.Minute)
	objects := view.Overdue.PeriodUnknownObjects
	if view.Overdue.PeriodUnknown != 30 || len(objects) != MaxPeriodUnknownObjects || objects[0].QueryGroup != "pod-b-00" ||
		objects[14].QueryGroup != "pod-b-14" || objects[15].QueryGroup != "pod-a-00" {
		t.Fatalf("aggregated %d named %+v, want 30 counted and the longest without a period named first", view.Overdue.PeriodUnknown, objects)
	}
}

// Among objects first seen at the same moment -- right after a start, all of
// them -- the ones named are the first given, oldest wake first, not the
// first by name.
func TestObjectsWithoutAPeriodSinceTheSameMomentKeepTheOrderGiven(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	_, facts := OverdueAnomalies([]OverdueWake{
		{QueryGroup: "qg-09", WakeAt: now.Add(-2 * time.Minute)},
		{QueryGroup: "qg-01", WakeAt: now.Add(-time.Minute)},
	}, 2, now, "pod-a", nil)
	var objects []PeriodUnknownObject
	for index := 0; index < MaxPeriodUnknownObjects; index++ {
		objects = append(objects, PeriodUnknownObject{QueryGroup: fmt.Sprintf("qg-%02d", 20+index), Since: now.Add(-time.Hour)})
	}
	for _, object := range facts.PeriodUnknownObjects {
		objects = append(objects, PeriodUnknownObject{QueryGroup: object.QueryGroup, Since: now})
	}
	// Room for one of the two tied at now: the twenty an hour old take the rest.
	longest := LongestWithoutPeriod(append(objects[:MaxPeriodUnknownObjects-1:MaxPeriodUnknownObjects-1], objects[MaxPeriodUnknownObjects:]...))
	if len(longest) != MaxPeriodUnknownObjects || longest[MaxPeriodUnknownObjects-1].QueryGroup != "qg-09" {
		t.Fatalf("longest %+v, want qg-09, the older wake, named before qg-01", longest)
	}
}
