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

// outputEntry is the output_kafka endpoint entry as the replica publishes it
// once the sink is opened lazily: ready or not, the attempts so far, and the
// last failure through the log's redaction.
func outputEntry(ready bool, attempts int, failure string) Endpoint {
	entry := Endpoint{Role: EndpointOutputKafka, Kind: "kafka", Address: "kafka-0:9092", Prefix: "alarmd_event",
		Configured: true, Ready: &ready}
	if attempts > 0 {
		entry.Attempts = &attempts
	}
	if failure != "" {
		age := 12.0
		entry.LastFailureAgeSeconds, entry.LastFailure = &age, failure
	}
	return entry
}

// A replica whose output sink is not open is a standing of its own: up,
// ready for nothing, assigned nothing, and on no object row. The view reads
// every replica's own entry -- not the one list it shows -- and the
// standing carries how long (since the process started, because a sink
// once open stays open), how many attempts, and what the last one said.
// The verdict is DEGRADED for it, as for any replica-level standing.
func TestAReplicaWhoseOutputIsNotReadyIsAStandingWithItsFacts(t *testing.T) {
	snapshots := idleSnapshots()
	// pod-a opened its output at once; its list is the newest and is the one
	// the view shows. It is aggregated first (the view walks the expected
	// replicas in order), so a reading that consulted the shown list rather
	// than each replica's own would find an open output when it reached
	// pod-b, and have nothing to say.
	snapshots[0].TakenAt = now.Add(-5 * time.Second)
	snapshots[0].StartedAt = now.Add(-2 * time.Hour)
	snapshots[0].Dependencies = []Endpoint{outputEntry(true, 1, "")}
	// pod-b restarted five minutes ago and has not opened its output since.
	snapshots[1].StartedAt = now.Add(-5 * time.Minute)
	snapshots[1].Dependencies = []Endpoint{outputEntry(false, 12, "kafka: dial tcp 10.0.0.1:9092: i/o timeout")}
	view := Aggregate(Expectation{QueryGroups: 0, Known: true}, snapshots, replicas(), now, freshness)
	if view.DependenciesReplica != "pod-a" {
		t.Fatalf("dependencies shown from %q, want the newest list, pod-a's", view.DependenciesReplica)
	}
	var notReady []Degradation
	for _, degradation := range view.Degradations {
		if degradation.Kind == DegradationOutputNotReady {
			notReady = append(notReady, degradation)
		}
	}
	if len(notReady) != 1 {
		t.Fatalf("degradations = %+v, want exactly one OUTPUT_NOT_READY, pod-b's", view.Degradations)
	}
	standing := notReady[0]
	if standing.Replica != "pod-b" || standing.Stage != "output" || standing.Text != "kafka: dial tcp 10.0.0.1:9092: i/o timeout" {
		t.Errorf("standing = %+v, want pod-b's output failure", standing)
	}
	if standing.AgeSeconds == nil || *standing.AgeSeconds != 300 {
		t.Errorf("age = %v, want 300 seconds: not ready since the process started", standing.AgeSeconds)
	}
	if standing.Attempts == nil || *standing.Attempts != 12 {
		t.Errorf("attempts = %v, want the sink's 12", standing.Attempts)
	}
	if view.Health != HealthDegraded {
		t.Errorf("health = %s, want DEGRADED for a replica that cannot open its output", view.Health)
	}
	// The line folds it under REPLICA_DEGRADED with the facts whole, so the
	// page can say it of each replica.
	var line *CheckReport
	for _, report := range ReportChecks(nil, nil, &view, now) {
		if report.Code == CheckReplicaDegraded {
			copied := report
			line = &copied
		}
	}
	if line == nil {
		t.Fatal("no REPLICA_DEGRADED line for a replica whose output is not ready")
	}
	var group *CheckGroup
	for index := range line.Groups {
		if line.Groups[index].Key == string(DegradationOutputNotReady) {
			group = &line.Groups[index]
		}
	}
	if group == nil || len(group.Replicas) != 1 || group.Replicas[0] != "pod-b" {
		t.Fatalf("groups = %+v, want an OUTPUT_NOT_READY fold naming pod-b", line.Groups)
	}
	if len(group.Degradations) != 1 || group.Degradations[0].AgeSeconds == nil || *group.Degradations[0].AgeSeconds != 300 ||
		group.Degradations[0].Attempts == nil || *group.Degradations[0].Attempts != 12 {
		t.Errorf("fold carries %+v, want the standing whole with its age and attempts", group.Degradations)
	}
}

// An open output is no standing, and an entry from a build before the
// readiness fact existed says nothing either way: the page must not read
// "no fact" as "not ready".
func TestAnOpenOrUnreportedOutputIsNoStanding(t *testing.T) {
	for name, entry := range map[string]Endpoint{
		"open":       outputEntry(true, 3, ""),
		"unreported": {Role: EndpointOutputKafka, Kind: "kafka", Address: "kafka-0:9092", Configured: true},
	} {
		snapshots := idleSnapshots()
		snapshots[0].StartedAt = now.Add(-5 * time.Minute)
		snapshots[0].Dependencies = []Endpoint{entry}
		view := Aggregate(Expectation{QueryGroups: 0, Known: true}, snapshots, replicas(), now, freshness)
		for _, degradation := range view.Degradations {
			if degradation.Kind == DegradationOutputNotReady {
				t.Errorf("%s output read as not ready: %+v", name, degradation)
			}
		}
		if view.Health == HealthDegraded {
			t.Errorf("%s output degrades the verdict: %+v", name, view.Degradations)
		}
	}
}

// Each replica's own dependency record rides on its per-replica row, and the
// one list the view shows says how many replicas it is one of. On a live
// deployment "is every replica's output open" could not be answered from the
// verdict route: the list shown was the replica that published last, and the
// question had to be put to each process's readiness endpoint. The rows
// answer it -- and the rows have to be each replica's own, not the shown
// list copied under every name.
func TestEachReplicaCarriesItsOwnDependencyRecord(t *testing.T) {
	snapshots := idleSnapshots()
	// pod-a published last, so its list is the one shown; its output is open.
	snapshots[0].TakenAt = now.Add(-5 * time.Second)
	snapshots[0].StartedAt = now.Add(-2 * time.Hour)
	snapshots[0].Dependencies = []Endpoint{outputEntry(true, 1, "")}
	// pod-b's is not, and its list is older.
	snapshots[1].StartedAt = now.Add(-5 * time.Minute)
	snapshots[1].Dependencies = []Endpoint{outputEntry(false, 12, "kafka: dial tcp 10.0.0.1:9092: i/o timeout")}
	view := Aggregate(Expectation{QueryGroups: 0, Known: true}, snapshots, replicas(), now, freshness)
	if view.DependenciesReplica != "pod-a" || view.DependenciesReplicas != 2 {
		t.Fatalf("shown list is %q of %d replicas, want pod-a's, one of 2", view.DependenciesReplica, view.DependenciesReplicas)
	}
	rows := map[string]ReplicaView{}
	for _, row := range view.PerReplica {
		rows[row.Replica] = row
	}
	for replica, wantReady := range map[string]bool{"pod-a": true, "pod-b": false} {
		row, present := rows[replica]
		if !present {
			t.Fatalf("no per-replica row for %s: rows = %+v", replica, view.PerReplica)
		}
		output := endpointByRole(row.Dependencies, EndpointOutputKafka)
		if output == nil || output.Ready == nil {
			t.Fatalf("%s row carries %+v, want its own output entry with the readiness fact", replica, row.Dependencies)
		}
		if *output.Ready != wantReady {
			t.Errorf("%s row says output ready=%v, want %v: the row has to be this replica's own record, not the shown list", replica, *output.Ready, wantReady)
		}
	}
	// The row's list is a copy: a later reader of the snapshot's slice cannot
	// change what the row says.
	snapshots[1].Dependencies[0].Address = "changed"
	if rows["pod-b"].Dependencies[0].Address == "changed" {
		t.Error("the per-replica row aliases the snapshot's slice")
	}
	// A replica that published none (an older build) carries none, and is
	// not counted among those that did.
	older := idleSnapshots()
	older[0].Dependencies = []Endpoint{outputEntry(true, 1, "")}
	view = Aggregate(Expectation{QueryGroups: 0, Known: true}, older, replicas(), now, freshness)
	if view.DependenciesReplicas != 1 {
		t.Errorf("replicas with a list = %d, want 1: pod-b published none", view.DependenciesReplicas)
	}
	for _, row := range view.PerReplica {
		if row.Replica == "pod-b" && row.Dependencies != nil {
			t.Errorf("pod-b published no dependencies and its row carries %+v", row.Dependencies)
		}
	}
}

// Each replica's readiness rides on its own row, bit by bit, and the view
// counts the replicas that answer their own probe with no. Four of the six
// bits could only be read by asking each process on a live deployment; the
// rows answer it from the fleet. A replica that published no readiness is an
// older build, not a replica that is not ready, and the count leaves it out.
func TestEachReplicaCarriesItsOwnReadinessAndTheViewCountsTheNotReady(t *testing.T) {
	snapshots := idleSnapshots()
	// pod-a published last and is ready on every bit.
	snapshots[0].TakenAt = now.Add(-5 * time.Second)
	snapshots[0].Readiness = &ReadinessFacts{State: "ready", Ready: true, ConfigLoaded: true, SchemaReady: true,
		AssignmentReady: true, RuntimeStateReady: true, OutputSinkReady: true, SnapshotReady: true}
	// pod-b is up, publishing, and not ready: its runtime state store has not
	// answered, and it says so.
	snapshots[1].Readiness = &ReadinessFacts{State: "not_ready", Ready: false, Reasons: []string{"REDIS_UNAVAILABLE"},
		ConfigLoaded: true, SchemaReady: true, AssignmentReady: true, RuntimeStateReady: false, OutputSinkReady: true, SnapshotReady: true}
	view := Aggregate(Expectation{QueryGroups: 0, Known: true}, snapshots, replicas(), now, freshness)
	if view.ReplicasNotReady != 1 {
		t.Fatalf("replicas not ready = %d, want 1: pod-b on its own word", view.ReplicasNotReady)
	}
	rows := map[string]ReplicaView{}
	for _, row := range view.PerReplica {
		rows[row.Replica] = row
	}
	a, b := rows["pod-a"], rows["pod-b"]
	if a.Readiness == nil || !a.Readiness.Ready || !a.Readiness.RuntimeStateReady {
		t.Errorf("pod-a row readiness = %+v, want its own: ready on every bit", a.Readiness)
	}
	if b.Readiness == nil || b.Readiness.Ready || b.Readiness.RuntimeStateReady || !b.Readiness.OutputSinkReady ||
		len(b.Readiness.Reasons) != 1 || b.Readiness.Reasons[0] != "REDIS_UNAVAILABLE" {
		t.Errorf("pod-b row readiness = %+v, want its own: not ready, runtime state the bit, one reason", b.Readiness)
	}
	// The row's facts are a copy: the reasons a later reader appends to the
	// snapshot's slice do not reach the row.
	snapshots[1].Readiness.Reasons[0] = "changed"
	if b.Readiness.Reasons[0] == "changed" {
		t.Error("the per-replica row aliases the snapshot's readiness")
	}
	// An older build publishes no readiness: no row fact, and not counted.
	older := idleSnapshots()
	older[0].Readiness = snapshots[0].Readiness
	view = Aggregate(Expectation{QueryGroups: 0, Known: true}, older, replicas(), now, freshness)
	if view.ReplicasNotReady != 0 {
		t.Errorf("replicas not ready = %d, want 0: pod-b published no readiness, which is not a no", view.ReplicasNotReady)
	}
	for _, row := range view.PerReplica {
		if row.Replica == "pod-b" && row.Readiness != nil {
			t.Errorf("pod-b published no readiness and its row carries %+v", row.Readiness)
		}
	}
}
