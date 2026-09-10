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
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

const freshness = time.Minute

func replicas() []string { return []string{"pod-a", "pod-b"} }

// A healthy replica has observed every object it owns. Owning without having
// observed is a separate state with its own test below, and conflating the two
// here would let the whole suite pass while the aggregate treated silence as
// health.
func healthySnapshots() []Snapshot {
	return []Snapshot{
		{Replica: "pod-a", TakenAt: now.Add(-10 * time.Second), Owned: 500, Determined: 500},
		{Replica: "pod-b", TakenAt: now.Add(-10 * time.Second), Owned: 449, Determined: 449},
	}
}

func anomaly(queryGroup string) Anomaly {
	return Anomaly{
		QueryGroup: queryGroup,
		Kind:       "SOURCE_BLOCKED",
		Since:      now.Add(-3 * time.Hour),
		SinceFrom:  SinceBusinessState,
		Replica:    "pod-b",
		Strategies: []StrategyRef{{StrategyID: "8930", BusinessID: "2"}},
	}
}

func TestCompleteViewWithoutAnomaliesIsHealthy(t *testing.T) {
	view := Aggregate(Expectation{QueryGroups: 949, Known: true}, healthySnapshots(), replicas(), now, freshness)
	if view.Health != HealthHealthy {
		t.Fatalf("health = %s, want %s (gaps: %+v)", view.Health, HealthHealthy, view.Gaps)
	}
	if view.Covered != 949 || view.Expected == nil || *view.Expected != 949 || view.Unknown != 0 {
		t.Fatalf("coverage = %d/%v unknown %d, want 949/949 unknown 0", view.Covered, view.Expected, view.Unknown)
	}
}

func TestCompleteViewWithAnomaliesIsDegraded(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[1].Anomalies = []Anomaly{anomaly("qg-1")}
	snapshots[1].TotalAnomalies = 1
	view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, freshness)
	if view.Health != HealthDegraded {
		t.Fatalf("health = %s, want %s (gaps: %+v)", view.Health, HealthDegraded, view.Gaps)
	}
	if len(view.Anomalies) != 1 || view.Anomalies[0].QueryGroup != "qg-1" {
		t.Fatalf("anomalies = %+v, want the single reported object", view.Anomalies)
	}
}

// The failure this whole package exists to prevent: a replica stops publishing,
// its anomalies vanish from the aggregate, and the shorter list reads as an
// improvement. Losing the replica must move the answer to UNKNOWN, never to
// HEALTHY, and never to a quieter DEGRADED.
func TestLosingAReplicaDoesNotLookLikeRecovery(t *testing.T) {
	withAnomaly := healthySnapshots()
	withAnomaly[1].Anomalies = []Anomaly{anomaly("qg-1")}
	withAnomaly[1].TotalAnomalies = 1
	before := Aggregate(Expectation{QueryGroups: 949, Known: true}, withAnomaly, replicas(), now, freshness)
	if before.Health != HealthDegraded || len(before.Anomalies) != 1 {
		t.Fatalf("baseline = %s with %d anomalies, want degraded with 1", before.Health, len(before.Anomalies))
	}

	// pod-b disappears, taking the only known anomaly with it.
	after := Aggregate(Expectation{QueryGroups: 949, Known: true}, withAnomaly[:1], replicas(), now, freshness)
	if after.Health != HealthUnknown {
		t.Fatalf("health after losing a replica = %s, want %s", after.Health, HealthUnknown)
	}
	if len(after.Anomalies) != 0 {
		t.Fatalf("anomalies = %+v, want none visible once the replica is gone", after.Anomalies)
	}
	if after.Unknown != 449 {
		t.Fatalf("unknown objects = %d, want the 449 the missing replica owned", after.Unknown)
	}
	if !hasGap(after, GapReplicaMissing) || !hasGap(after, GapOwnershipShortfall) {
		t.Fatalf("gaps = %+v, want both the missing replica and the coverage shortfall", after.Gaps)
	}
}

// A deployment with no ready replica has produced no evidence at all. Calling
// that healthy turns "everything is gone" into the quietest possible answer.
func TestNoReplicasIsUnknownNotAnIdleDeployment(t *testing.T) {
	view := Aggregate(Expectation{QueryGroups: 0, Known: true}, nil, nil, now, freshness)
	if view.Health != HealthUnknown || !hasGap(view, GapNoReplicas) {
		t.Fatalf("health = %s gaps = %+v, want unknown with no replicas", view.Health, view.Gaps)
	}
}

// Counting is not set arithmetic. More covered than expected means the two
// sides disagree about which objects exist, and a shortfall could still be
// hiding inside that difference.
func TestMoreCoveredThanExpectedIsUnknown(t *testing.T) {
	view := Aggregate(Expectation{QueryGroups: 900, Known: true}, healthySnapshots(), replicas(), now, freshness)
	if view.Health != HealthUnknown || !hasGap(view, GapCoverageInconsistent) {
		t.Fatalf("health = %s gaps = %+v, want unknown when coverage exceeds the denominator", view.Health, view.Gaps)
	}
}

func TestStaleSnapshotIsUnknownRatherThanTrusted(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[0].TakenAt = now.Add(-2 * time.Minute)
	view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, freshness)
	if view.Health != HealthUnknown || !hasGap(view, GapSnapshotStale) {
		t.Fatalf("health = %s gaps = %+v, want unknown with a stale snapshot", view.Health, view.Gaps)
	}
	if view.Covered != 449 {
		t.Fatalf("covered = %d, want only the fresh replica's objects", view.Covered)
	}
}

func TestTruncatedListIsUnknownEvenWhenCoverageAddsUp(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[1].Anomalies = []Anomaly{anomaly("qg-1")}
	snapshots[1].TotalAnomalies = 40
	view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, freshness)
	if view.Health != HealthUnknown || !hasGap(view, GapListTruncated) {
		t.Fatalf("health = %s gaps = %+v, want unknown because the list is bounded", view.Health, view.Gaps)
	}
}

// Only the control leader sees the whole active set; the other replicas see an
// empty one. A view built from a local reading would report full coverage of
// nothing, so an unreadable denominator has to be unknown instead of zero.
func TestUnreadableDenominatorIsUnknownNotAnEmptyDeployment(t *testing.T) {
	view := Aggregate(Expectation{Known: false}, healthySnapshots(), replicas(), now, freshness)
	if view.Health != HealthUnknown || !hasGap(view, GapDenominatorUnavailable) {
		t.Fatalf("health = %s gaps = %+v, want unknown without a denominator", view.Health, view.Gaps)
	}
	if view.Expected != nil {
		t.Fatalf("expected = %v, want absent rather than a number", *view.Expected)
	}
}

func TestCoverageShortfallWithFreshSnapshotsIsStillUnknown(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[1].Owned, snapshots[1].Determined = 400, 400
	view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, freshness)
	if view.Health != HealthUnknown || view.Unknown != 49 {
		t.Fatalf("health = %s unknown = %d, want unknown with 49 unaccounted objects", view.Health, view.Unknown)
	}
}

// The failure a restart produces: the tracker is empty, so every object it owns
// is reported with no anomaly against it. An empty anomaly list from a replica
// that has observed nothing looks exactly like one from a replica where nothing
// is wrong, and calling that HEALTHY turns every rolling deploy into a window
// where the deployment cannot be reported as broken.
func TestOwnedButUnobservedObjectsAreNotHealthy(t *testing.T) {
	restarted := healthySnapshots()
	restarted[0].Determined = 0
	restarted[1].Determined = 0
	view := Aggregate(Expectation{QueryGroups: 949, Known: true}, restarted, replicas(), now, freshness)
	if view.Health != HealthUnknown || !hasGap(view, GapUndetermined) {
		t.Fatalf("health = %s gaps = %+v, want unknown while nothing has been observed", view.Health, view.Gaps)
	}
	if view.Unknown != 949 {
		t.Fatalf("unknown = %d, want every owned object counted as unknown", view.Unknown)
	}
	if view.Covered != 949 {
		t.Fatalf("covered = %d, want ownership coverage to stay complete", view.Covered)
	}
}

// Partial knowledge is partial: the objects a replica can speak for do not
// vouch for the ones it cannot.
func TestPartiallyObservedOwnershipCountsOnlyTheUnobservedAsUnknown(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[1].Determined = 400
	view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, freshness)
	if view.Health != HealthUnknown || view.Unknown != 49 {
		t.Fatalf("health = %s unknown = %d, want the 49 unobserved objects", view.Health, view.Unknown)
	}
	if view.Determined != 900 {
		t.Fatalf("determined = %d, want 900", view.Determined)
	}
}

// A replica cannot report more objects observed than it owns; if it does, the
// two numbers came from different moments and neither can carry the verdict.
func TestMoreDeterminedThanOwnedIsUnknown(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[0].Determined = 600
	view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, freshness)
	if view.Health != HealthUnknown || !hasGap(view, GapCoverageInconsistent) {
		t.Fatalf("health = %s gaps = %+v, want unknown when more is observed than owned", view.Health, view.Gaps)
	}
}

func TestAnomaliesAreOrderedByHowLongTheyHaveBeenWrong(t *testing.T) {
	older := anomaly("qg-old")
	older.Since = now.Add(-5 * time.Hour)
	newer := anomaly("qg-new")
	newer.Since = now.Add(-1 * time.Hour)
	snapshots := healthySnapshots()
	snapshots[1].Anomalies = []Anomaly{newer, older}
	snapshots[1].TotalAnomalies = 2
	view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, freshness)
	if len(view.Anomalies) != 2 || view.Anomalies[0].QueryGroup != "qg-old" {
		t.Fatalf("anomaly order = %+v, want the longest-running object first", view.Anomalies)
	}
}

func hasGap(view View, kind GapKind) bool {
	for _, gap := range view.Gaps {
		if gap.Kind == kind {
			return true
		}
	}
	return false
}

// This gap on its own holds the verdict at UNKNOWN, so naming only the
// condition hands a reader "do not trust this" with nothing to act on. Twelve
// objects left over from a catalogue that shrank and a deployment owning
// hundreds it should not are the same string and opposite problems.
//
// Observed on BKOP after the active set went 949 -> 931: the page reported
// UNKNOWN with unknown=0 and one gap that said only its own name.
func TestCoverageInconsistentGapSaysBothNumbers(t *testing.T) {
	view := Aggregate(Expectation{QueryGroups: 931, Known: true}, healthySnapshots(), replicas(), now, freshness)

	var detail string
	for _, gap := range view.Gaps {
		if gap.Kind == GapCoverageInconsistent {
			detail = gap.Detail
		}
	}
	if detail == "" {
		t.Fatalf("the gap holding the verdict at UNKNOWN explained nothing: %+v", view.Gaps)
	}
	for _, want := range []string{"949", "931", "18"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail %q does not carry %q; a reader cannot size the disagreement", detail, want)
		}
	}
	// Covered is a sum across replicas, so the difference alone cannot separate
	// stale ownership from this view counting the same object twice while a
	// rendezvous change is in flight. Stating only the difference asserts the
	// first, so the split has to travel with it.
	for _, pair := range []string{"a 500", "b 449"} {
		if !strings.Contains(detail, pair) {
			t.Fatalf("detail %q does not carry %q; a reader cannot tell which side is over", detail, pair)
		}
	}
	if !strings.Contains(detail, "sum") {
		t.Fatalf("detail %q does not say Covered is a sum, so the difference reads as objects left over", detail)
	}
}
