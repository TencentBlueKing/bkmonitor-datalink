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

// partitionTracker drives a tracker into a known partition: some objects healthy,
// some failing on this deployment's own execution, some held in the pool.
func partitionTracker(t *testing.T, at time.Time, healthy, failing, pooled int) *Tracker {
	t.Helper()
	tracker := NewTracker(nil, "replica-a", func() time.Time { return at })
	observe := func(queryGroup string, observation observability.Observation) {
		observation.Trace.QueryGroupKey = queryGroup
		tracker.Observe(context.Background(), observation)
	}
	for index := 0; index < healthy; index++ {
		observe(qgName("healthy", index), observability.Observation{ProgressCompletionKind: "FULL_COMPLETED"})
	}
	for index := 0; index < failing; index++ {
		name := qgName("failing", index)
		for round := 0; round < DefaultDegradedRounds; round++ {
			observe(name, observability.Observation{ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE"})
		}
	}
	for index := 0; index < pooled; index++ {
		name := qgName("pooled", index)
		for round := 0; round < DefaultDegradedRounds; round++ {
			observe(name, observability.Observation{ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE"})
		}
		observe(name, observability.Observation{QueryCooldown: &observability.QueryCooldownFacts{
			Event: "entered", Until: at.Add(time.Minute), LastQueryAt: at, Failures: 3,
		}})
	}
	return tracker
}

func qgName(prefix string, index int) string {
	return prefix + "-" + string(rune('a'+index%26)) + string(rune('a'+index/26))
}

// The whole point of splitting the pool out is that the columns partition the
// objects: every object a replica can speak for lands in exactly one, and the
// three add up to what it said it could speak for.
//
// Without this, the split is an arrangement of lists that happens to look right
// on the page today. The equation is what makes "the rest is real problems"
// a statement rather than a hope.
func TestTheFourColumnsAccountForEveryObject(t *testing.T) {
	at := time.Date(2026, 9, 11, 14, 0, 0, 0, time.UTC)
	tracker := partitionTracker(t, at, 40, 7, 5)

	anomalies := tracker.Anomalies()
	demoted := tracker.Demoted()
	determined := tracker.Determined()

	view := Aggregate(Expectation{Known: true, QueryGroups: 52}, []Snapshot{{
		Replica: "replica-a", TakenAt: at, Owned: 52, Determined: determined,
		Anomalies: anomalies, TotalAnomalies: len(anomalies),
		Demoted: demoted, TotalDemoted: len(demoted),
	}}, []string{"replica-a"}, at, time.Minute)

	if got := view.Healthy + view.AnomaliesTotal + view.DemotedTotal + view.Unknown; got != *view.Expected {
		t.Errorf("healthy %d + anomalies %d + demoted %d + unknown %d = %d, want the expected %d",
			view.Healthy, view.AnomaliesTotal, view.DemotedTotal, view.Unknown, got, *view.Expected)
	}
	if view.AnomaliesTotal != 7 {
		t.Errorf("anomalies = %d, want the 7 this deployment is failing on", view.AnomaliesTotal)
	}
	if view.DemotedTotal != 5 {
		t.Errorf("demoted = %d, want the 5 held back", view.DemotedTotal)
	}
	if view.Healthy != 40 {
		t.Errorf("healthy = %d, want 40", view.Healthy)
	}
}

// A backend outage that pools more objects must not make this deployment's own
// verdict worse. That inversion -- wider outage, sicker-looking alarmd -- is
// what made the health number unusable during the outages it was most needed in.
func TestAWiderOutageDoesNotMakeTheDeploymentLookSicker(t *testing.T) {
	at := time.Date(2026, 9, 11, 14, 0, 0, 0, time.UTC)
	for _, pooled := range []int{1, 50} {
		tracker := partitionTracker(t, at, 40, 0, pooled)
		demoted := tracker.Demoted()
		view := Aggregate(Expectation{Known: true, QueryGroups: 40 + pooled}, []Snapshot{{
			Replica: "replica-a", TakenAt: at, Owned: 40 + pooled, Determined: tracker.Determined(),
			Anomalies: tracker.Anomalies(), Demoted: demoted, TotalDemoted: len(demoted),
		}}, []string{"replica-a"}, at, time.Minute)
		if view.Health != HealthHealthy {
			t.Errorf("with %d objects pooled and nothing failing on this deployment, health = %q, want %q (gaps: %+v)",
				pooled, view.Health, HealthHealthy, view.Gaps)
		}
	}
}

// Demotion takes objects out of the health denominator. A pool that fills and
// never drains therefore makes a deployment look better the worse it gets, and
// the pool's own size cannot show that: once it has swallowed everything it can,
// a stuck pool and a still-down backend report the same steady number.
//
// Exits are the positive control. This asserts they are actually counted,
// because a control that is always zero is indistinguishable from the failure it
// is supposed to catch.
func TestTheViewCanShowThatObjectsDoLeaveThePool(t *testing.T) {
	at := time.Date(2026, 9, 11, 14, 0, 0, 0, time.UTC)
	tracker := partitionTracker(t, at, 10, 0, 3)

	if _, _, exits, _ := tracker.DemotionFlow(); exits != 0 {
		t.Fatalf("exits = %d before anything left the pool", exits)
	}
	entries, _, _, _ := tracker.DemotionFlow()
	if entries != 3 {
		t.Fatalf("entries = %d, want 3", entries)
	}

	tracker.Observe(context.Background(), observability.Observation{
		Trace:         observability.TraceFields{QueryGroupKey: qgName("pooled", 0)},
		QueryCooldown: &observability.QueryCooldownFacts{Event: "recovered"},
	})

	entries, _, exits, lastExit := tracker.DemotionFlow()
	if exits != 1 {
		t.Errorf("exits = %d after one object left the pool, want 1", exits)
	}
	if entries != 3 {
		t.Errorf("entries = %d, want the count to stay cumulative rather than track occupancy", entries)
	}
	if !lastExit.Equal(at) {
		t.Errorf("last exit = %s, want %s", lastExit, at)
	}
	if got := len(tracker.Demoted()); got != 2 {
		t.Errorf("pool holds %d, want 2 after one left", got)
	}
	// The object that left is not healthy yet -- nothing has completed a round
	// for it since. It goes back to this deployment's column rather than being
	// counted as recovered, because leaving the pool is not evidence of anything.
	if got := len(tracker.Anomalies()); got != 1 {
		t.Errorf("anomalies = %d after one object left the pool, want the 1 that left and has not completed since", got)
	}
}

// The pool's whole claim is "these are not this deployment's fault", which is a
// claim a reader has to be able to check. A column of objects sharing one
// failure code restates the column's own name; the symptom is what separates
// one broken backend from several.
func TestTheSymptomSurvivesAllTheWayToTheSummary(t *testing.T) {
	at := time.Date(2026, 9, 11, 15, 30, 0, 0, time.UTC)
	tracker := NewTracker(nil, "replica-a", func() time.Time { return at })
	observe := func(queryGroup, detail string) {
		tracker.Observe(context.Background(), observability.Observation{
			Trace: observability.TraceFields{QueryGroupKey: queryGroup},
			QueryFailure: &observability.QueryFailureFacts{
				Stage: "provider", Category: "source_backend", Code: "QUERY_UNAVAILABLE", Detail: detail,
			},
		})
		for round := 0; round < DefaultDegradedRounds; round++ {
			tracker.Observe(context.Background(), observability.Observation{
				Trace:                  observability.TraceFields{QueryGroupKey: queryGroup},
				ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE",
			})
		}
	}
	observe("qg-a", "http_status=503")
	observe("qg-b", "http_status=503")
	observe("qg-c", "transport=connection_refused")

	anomalies := tracker.Anomalies()
	if len(anomalies) != 3 {
		t.Fatalf("anomalies = %d, want 3", len(anomalies))
	}
	for _, anomaly := range anomalies {
		if anomaly.Failure == nil || anomaly.Failure.Detail == "" {
			t.Fatalf("%s carries no symptom: %+v", anomaly.QueryGroup, anomaly.Failure)
		}
	}
	summary := summarize(anomalies)
	got := map[string]int{}
	for _, count := range summary.ByFailureDetail {
		got[count.Value] = count.Count
	}
	if got["http_status=503"] != 2 || got["transport=connection_refused"] != 1 {
		t.Errorf("symptom counts = %v, want two 503s and one refused connection", got)
	}
	// One code across all three, which is exactly why the code alone cannot
	// answer "what is wrong with these".
	if len(summary.ByFailureCode) != 1 {
		t.Errorf("failure codes = %+v, want the single code the symptom has to split",
			summary.ByFailureCode)
	}
}

// Pooled objects whose own cooldown window has already elapsed are due to be
// retried and have not been. The count is derived from each object's own
// deadline, so it can be reported without anyone first deciding what number is
// too many -- and it is the number that distinguishes a stuck way out from a
// backend that is simply still down.
func TestPooledObjectsPastTheirOwnDeadlineAreCounted(t *testing.T) {
	at := time.Date(2026, 9, 11, 14, 0, 0, 0, time.UTC)
	demoted := []Anomaly{
		{QueryGroup: "due", Kind: KindQueryCooldown, Since: at.Add(-time.Hour),
			QueryCooldown: &observability.QueryCooldownFacts{Until: at.Add(-10 * time.Minute)}},
		{QueryGroup: "waiting", Kind: KindQueryCooldown, Since: at.Add(-time.Hour),
			QueryCooldown: &observability.QueryCooldownFacts{Until: at.Add(10 * time.Minute)}},
	}
	view := Aggregate(Expectation{Known: true, QueryGroups: 2}, []Snapshot{{
		Replica: "replica-a", TakenAt: at, Owned: 2, Determined: 2,
		Demoted: demoted, TotalDemoted: len(demoted),
	}}, []string{"replica-a"}, at, time.Minute)

	if view.DemotedDue != 1 {
		t.Errorf("demoted past their own deadline = %d, want 1", view.DemotedDue)
	}
	if view.DemotedTotal != 2 {
		t.Errorf("demoted = %d, want 2", view.DemotedTotal)
	}
}

// The columns claiming more objects than the replicas can speak for means
// something is counted twice. Letting the healthy column absorb it would turn a
// double count into a quietly smaller healthy number, which is the one direction
// nobody checks.
func TestColumnsClaimingMoreObjectsThanAreKnownHoldTheVerdict(t *testing.T) {
	at := time.Date(2026, 9, 11, 14, 0, 0, 0, time.UTC)
	anomalies := []Anomaly{{QueryGroup: "a", Kind: KindDegradedRun, Since: at}}
	demoted := []Anomaly{{QueryGroup: "a", Kind: KindQueryCooldown, Since: at,
		QueryCooldown: &observability.QueryCooldownFacts{Until: at.Add(time.Minute)}}}

	view := Aggregate(Expectation{Known: true, QueryGroups: 1}, []Snapshot{{
		Replica: "replica-a", TakenAt: at, Owned: 1, Determined: 1,
		Anomalies: anomalies, TotalAnomalies: 1, Demoted: demoted, TotalDemoted: 1,
	}}, []string{"replica-a"}, at, time.Minute)

	if view.Health != HealthUnknown {
		t.Errorf("health = %q with both columns claiming the same object, want %q", view.Health, HealthUnknown)
	}
	if view.Healthy != 0 {
		t.Errorf("healthy = %d, want 0 rather than a negative count rendered as a number", view.Healthy)
	}
}
