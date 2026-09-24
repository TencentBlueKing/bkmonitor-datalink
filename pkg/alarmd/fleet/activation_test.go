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
)

// The leader's activation standing is the deployment's, kept whole on the
// view; past the bound it is a degradation, and the verdict is DEGRADED with
// every object healthy -- which is the deployment this exists for.
func TestAggregateCarriesTheLeadersActivationStanding(t *testing.T) {
	behind := &ActivationFacts{Applied: "bdc6ffcb", Published: "e7a1b2c3", Behind: true, BehindBeyondBound: true,
		ConsecutiveFailures: 120, FailureStage: "schedule_cutover", FailureClass: "schedule_conflict"}
	snapshots := []Snapshot{
		{Replica: "pod-a", TakenAt: now, Owned: 1, Determined: 1, Activation: behind},
		{Replica: "pod-b", TakenAt: now, Owned: 1, Determined: 1},
	}
	view := Aggregate(Expectation{Known: true, QueryGroups: 2}, snapshots, []string{"pod-a", "pod-b"}, now, freshness)
	if view.Activation == nil || *view.Activation != *behind || view.ActivationReplica != "pod-a" {
		t.Fatalf("view activation = %+v from %q, want the leader's, whole", view.Activation, view.ActivationReplica)
	}
	if len(view.Degradations) != 1 || view.Degradations[0] != (Degradation{Kind: DegradationActivationBehind, Replica: "pod-a"}) {
		t.Fatalf("degradations = %+v, want ACTIVATION_BEHIND on pod-a", view.Degradations)
	}
	if view.Health != HealthDegraded || view.Healthy != 2 {
		t.Fatalf("health = %s with %d healthy, want DEGRADED with every object healthy", view.Health, view.Healthy)
	}

	// Behind but not yet beyond the bound: on the view, not a degradation.
	// A transient failure must not degrade the verdict; six minutes of it
	// must.
	within := *behind
	within.BehindBeyondBound = false
	snapshots[0].Activation = &within
	transient := Aggregate(Expectation{Known: true, QueryGroups: 2}, snapshots, []string{"pod-a", "pod-b"}, now, freshness)
	if transient.Activation == nil || !transient.Activation.Behind || len(transient.Degradations) != 0 || transient.Health != HealthHealthy {
		t.Fatalf("within the bound: activation=%+v degradations=%+v health=%s, want behind, no degradation, HEALTHY",
			transient.Activation, transient.Degradations, transient.Health)
	}

	// No replica attempted activation: absent, not level.
	snapshots[0].Activation = nil
	none := Aggregate(Expectation{Known: true, QueryGroups: 2}, snapshots, []string{"pod-a", "pod-b"}, now, freshness)
	if none.Activation != nil || none.ActivationReplica != "" {
		t.Fatalf("with no attempt: activation=%+v from %q, want absent", none.Activation, none.ActivationReplica)
	}
}

// The two standings are lines on the first screen, first, with no objects and
// their replicas named; the activation one carries the facts its sentence is
// built from. A fleet executing a stale publication decided the verdict for
// half a day with no line anywhere, which is what these are for.
func TestStandingsAreTheFirstLinesAndNameReplicas(t *testing.T) {
	view := View{
		Activation: &ActivationFacts{Behind: true, BehindBeyondBound: true, ConsecutiveFailures: 3,
			FailureStage: "schedule_cutover", FailureClass: "schedule_conflict"},
		ActivationReplica: "pod-a",
		Degradations: []Degradation{
			{Kind: DegradationActivationBehind, Replica: "pod-a"},
			{Kind: DegradationOpenAlertSetStale, Replica: "pod-a"},
			{Kind: DegradationOpenAlertSetStale, Replica: "pod-b"},
			{Kind: DegradationControlLeaderAbsent, Replica: "pod-c"},
		},
		Anomalies: []Anomaly{{QueryGroup: "qg-1", Kind: KindOverdueWake, Finding: Finding{Check: CheckSlotsOverdue, Group: "pod-a"}}},
	}
	reports := ReportChecks([][]Anomaly{view.Anomalies}, nil, &view, now)
	if len(reports) != 3 || reports[0].Code != CheckCutoverFailing || reports[1].Code != CheckReplicaDegraded || reports[2].Code != CheckSlotsOverdue {
		t.Fatalf("reports = %+v, want CUTOVER_FAILING, REPLICA_DEGRADED, then the object checks", reports)
	}
	cutover := reports[0]
	if cutover.Owner != OwnerAlarmd || cutover.Objects != 0 || cutover.Activation == nil || cutover.Replica != "pod-a" ||
		len(cutover.Groups) != 1 || cutover.Groups[0].Key != "schedule_cutover/schedule_conflict" ||
		len(cutover.Groups[0].Replicas) != 1 || cutover.Groups[0].Replicas[0] != "pod-a" {
		t.Fatalf("CUTOVER_FAILING = %+v, want ours, no objects, the leader's facts, one group on stage/class naming pod-a", cutover)
	}
	degraded := reports[1]
	if degraded.Owner != OwnerAlarmd || degraded.GroupBy != GroupByDegradation || degraded.Objects != 0 || len(degraded.Groups) != 2 {
		t.Fatalf("REPLICA_DEGRADED = %+v, want ours, folded on kind, two kinds (the activation one has its own line)", degraded)
	}
	groups := map[string][]string{}
	for _, group := range degraded.Groups {
		groups[group.Key] = group.Replicas
	}
	if len(groups[string(DegradationOpenAlertSetStale)]) != 2 || len(groups[string(DegradationControlLeaderAbsent)]) != 1 ||
		groups[string(DegradationActivationBehind)] != nil {
		t.Fatalf("REPLICA_DEGRADED groups = %+v, want two replicas under the open alert set, one under leader absent, none under activation", groups)
	}
	// Opening either line lists no objects: the rows route answers empty
	// rather than inventing rows for a fact that has none.
	if rows := UnderCheck(CheckCutoverFailing, "", &view, now); len(rows) != 0 {
		t.Fatalf("CUTOVER_FAILING lists %d objects, want none", len(rows))
	}
}

// A behind that has not held past the bound is not a line: the transient
// failure of one round must not put the deployment's first line up.
func TestAStandingWithinTheBoundIsNotALine(t *testing.T) {
	view := View{Activation: &ActivationFacts{Behind: true, ConsecutiveFailures: 1}, ActivationReplica: "pod-a"}
	if reports := ReportChecks(nil, nil, &view, now); len(reports) != 0 {
		t.Fatalf("reports = %+v, want none within the bound", reports)
	}
}

// A stale source's fold carries where its last round stopped and what it
// said, so the line names the failure -- a catalogue that would not
// validate -- rather than the kind, which sent readers to the previous
// incident's cause.
func TestTheStaleSourceFoldCarriesTheFailureBehindIt(t *testing.T) {
	view := View{Degradations: []Degradation{
		{Kind: DegradationControlSourceStale, Replica: "pod-a", Stage: "validate_catalog", Text: "plan retention exceeds catalog retention"},
		{Kind: DegradationControlSourceStale, Replica: "pod-b"},
	}}
	reports := ReportChecks(nil, nil, &view, now)
	if len(reports) != 1 || len(reports[0].Groups) != 1 {
		t.Fatalf("reports = %+v, want one REPLICA_DEGRADED line with one fold", reports)
	}
	group := reports[0].Groups[0]
	if group.Stage != "validate_catalog" || group.Text != "plan retention exceeds catalog retention" || len(group.Replicas) != 2 {
		t.Fatalf("fold = %+v, want both replicas and the failure the first one carries", group)
	}
}

// Query Groups the last cutover held back degrade the verdict by name - with
// how many, why and which - while the rest of the publication is active.
// None held back, no degradation.
func TestAggregateNamesQueryGroupsTheCutoverHeldBack(t *testing.T) {
	held := &ActivationFacts{Applied: "e7a1b2c3", Published: "e7a1b2c3", BlockedQueryGroups: 2,
		BlockedReasons: "open_digest_mismatch=1,open_segment_closed_or_ahead=1", BlockedSamples: "qg-a:open_digest_mismatch,qg-b:open_segment_closed_or_ahead"}
	snapshots := []Snapshot{
		{Replica: "pod-a", TakenAt: now, Owned: 1, Determined: 1, Activation: held},
		{Replica: "pod-b", TakenAt: now, Owned: 1, Determined: 1},
	}
	view := Aggregate(Expectation{Known: true, QueryGroups: 2}, snapshots, []string{"pod-a", "pod-b"}, now, freshness)
	if len(view.Degradations) != 1 || view.Degradations[0].Kind != DegradationActivationBlocked || view.Degradations[0].Replica != "pod-a" ||
		!strings.Contains(view.Degradations[0].Text, "2 held back") || !strings.Contains(view.Degradations[0].Text, "qg-a:open_digest_mismatch") {
		t.Fatalf("degradations = %+v, want ACTIVATION_BLOCKED naming the two", view.Degradations)
	}
	if view.Health != HealthDegraded {
		t.Fatalf("health = %s, want DEGRADED", view.Health)
	}
	clear := *held
	clear.BlockedQueryGroups, clear.BlockedReasons, clear.BlockedSamples = 0, "", ""
	snapshots[0].Activation = &clear
	if view := Aggregate(Expectation{Known: true, QueryGroups: 2}, snapshots, []string{"pod-a", "pod-b"}, now, freshness); len(view.Degradations) != 0 {
		t.Fatalf("nothing held back, degradations = %+v", view.Degradations)
	}
}
