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
	"encoding/json"
	"testing"
	"time"
)

func capacitySnapshot(replica string, at time.Time, capacity *Capacity) Snapshot {
	return Snapshot{Replica: replica, TakenAt: at, Owned: 10, Determined: 10, Capacity: capacity}
}

// Occupancy is spread over the replicas, so it adds up; a ceiling is the same
// number on every replica, so adding it up would report a budget four times the
// one any replica actually enforces.
func TestCapacityAddsUpOccupancyButNotCeilings(t *testing.T) {
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	view := Aggregate(Expectation{QueryGroups: 20, Known: true}, []Snapshot{
		capacitySnapshot("pod-a", at, &Capacity{
			PermitsHeld: 7, PermitBudget: 32, PermitSeconds: 800, Waiting: 1,
			MemoryUsed: 500, MemoryLimit: 8 << 30, MemorySource: "pod_limit", CPUCores: 8,
			Budgets: map[string]uint64{"state_mutations": 65536},
		}),
		capacitySnapshot("pod-b", at, &Capacity{
			PermitsHeld: 9, PermitBudget: 32, PermitSeconds: 900, Waiting: 2,
			MemoryUsed: 600, MemoryLimit: 8 << 30, MemorySource: "pod_limit", CPUCores: 8,
			Budgets: map[string]uint64{"state_mutations": 65536},
		}),
	}, []string{"pod-a", "pod-b"}, at, time.Minute)

	if view.Capacity == nil {
		t.Fatal("capacity was not reported")
	}
	if view.Capacity.PermitsHeld != 16 || view.Capacity.PermitSeconds != 1700 || view.Capacity.Waiting != 3 {
		t.Fatalf("occupancy did not add up: %+v", view.Capacity)
	}
	if view.Capacity.PermitBudget != 32 || view.Capacity.CPUCores != 8 {
		t.Fatalf("a per-replica ceiling was summed instead of reported once: %+v", view.Capacity)
	}
	if view.Capacity.MemoryUsed != 1100 || view.Capacity.MemoryLimit != 8<<30 {
		t.Fatalf("memory did not aggregate correctly: %+v", view.Capacity)
	}
	if view.Capacity.Budgets["state_mutations"] != 65536 {
		t.Fatalf("per-Slot ceiling lost: %+v", view.Capacity.Budgets)
	}
	if len(view.Capacity.Disagreement) != 0 {
		t.Fatalf("agreeing replicas were reported as disagreeing: %+v", view.Capacity.Disagreement)
	}
}

// Two replicas running different limits is a half-finished rollout. Averaging
// or last-writer-wins would hide exactly the condition worth seeing.
func TestCapacityNamesCeilingsTheReplicasDisagreeOn(t *testing.T) {
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	view := Aggregate(Expectation{QueryGroups: 20, Known: true}, []Snapshot{
		capacitySnapshot("pod-a", at, &Capacity{
			PermitBudget: 32, MemoryLimit: 8 << 30, CPUCores: 8,
			Budgets: map[string]uint64{"state_mutations": 65536},
		}),
		capacitySnapshot("pod-b", at, &Capacity{
			PermitBudget: 16, MemoryLimit: 4 << 30, CPUCores: 4,
			Budgets: map[string]uint64{"state_mutations": 8192},
		}),
	}, []string{"pod-a", "pod-b"}, at, time.Minute)

	disagreement := map[string]bool{}
	for _, name := range view.Capacity.Disagreement {
		disagreement[name] = true
	}
	for _, want := range []string{"permit_budget_per_replica", "cpu_cores_per_replica", "memory_limit_bytes_per_replica", "state_mutations"} {
		if !disagreement[want] {
			t.Fatalf("%q disagreement was not reported: %+v", want, view.Capacity.Disagreement)
		}
	}
}

// The deployment's memory use is summed and its limit is one replica's, and a
// raw reader divided the one by the other: four replicas each at about a
// quarter of a 4 GiB limit read as nearly full. The per-replica share is done
// per replica - the fullest named - and the names say which figure is summed
// and which is one replica's.
func TestTheFullestReplicasMemoryShareIsItsOwnNotTheSumOverOneLimit(t *testing.T) {
	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	limit := uint64(4 << 30)
	used := map[string]uint64{"pod-a": 921 << 20, "pod-b": 1053 << 20, "pod-c": 1576 << 20, "pod-d": 1784 << 20}
	var snapshots []Snapshot
	for _, pod := range []string{"pod-a", "pod-b", "pod-c", "pod-d"} {
		snapshots = append(snapshots, capacitySnapshot(pod, at, &Capacity{MemoryUsed: used[pod], MemoryLimit: limit, MemoryLimitKnown: true, MemorySource: "pod_limit"}))
	}
	view := Aggregate(Expectation{QueryGroups: 20, Known: true}, snapshots, []string{"pod-a", "pod-b", "pod-c", "pod-d"}, at, time.Minute)
	capacity := view.Capacity
	want := float64(1784<<20) / float64(limit)
	if capacity.MemoryUsedShareMaxReplica != "pod-d" || capacity.MemoryUsedShareMax != want {
		t.Fatalf("fullest replica = %s at %.3f, want pod-d at %.3f", capacity.MemoryUsedShareMaxReplica, capacity.MemoryUsedShareMax, want)
	}
	if misread := float64(capacity.MemoryUsed) / float64(capacity.MemoryLimit); misread < 0.9 || capacity.MemoryUsedShareMax > 0.5 {
		t.Fatalf("setup: the summed use over one limit reads %.2f and the fullest replica %.2f; the case is the one that misled", misread, capacity.MemoryUsedShareMax)
	}
	raw, err := json.Marshal(capacity)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"memory_used_bytes_total", "memory_limit_bytes_per_replica", "memory_used_share_max", "memory_used_share_max_replica"} {
		if fields[name] == nil {
			t.Errorf("capacity carries no %q: %v", name, fields)
		}
	}
	for _, name := range []string{"memory_used_bytes", "memory_limit_bytes"} {
		if _, present := fields[name]; present {
			t.Errorf("capacity still carries %q, a name that does not say whether it is summed or one replica's", name)
		}
	}

	// Two replicas at one share name the same one whichever is read first.
	for _, order := range [][]string{{"pod-a", "pod-b"}, {"pod-b", "pod-a"}} {
		tied := Aggregate(Expectation{QueryGroups: 20, Known: true}, []Snapshot{
			capacitySnapshot("pod-a", at, &Capacity{MemoryUsed: 1 << 30, MemoryLimit: limit, MemoryLimitKnown: true}),
			capacitySnapshot("pod-b", at, &Capacity{MemoryUsed: 1 << 30, MemoryLimit: limit, MemoryLimitKnown: true}),
		}, order, at, time.Minute).Capacity
		if tied.MemoryUsedShareMaxReplica != "pod-a" {
			t.Fatalf("read in order %v, the tie named %s, want pod-a", order, tied.MemoryUsedShareMaxReplica)
		}
	}

	// A replica that does not know its limit has no share, and none known
	// leaves the field out rather than reading zero.
	unknown := Aggregate(Expectation{QueryGroups: 20, Known: true}, []Snapshot{
		capacitySnapshot("pod-a", at, &Capacity{MemoryUsed: 1 << 30, MemoryLimit: limit}),
	}, []string{"pod-a"}, at, time.Minute).Capacity
	if unknown.MemoryUsedShareMax != 0 || unknown.MemoryUsedShareMaxReplica != "" {
		t.Fatalf("a replica with no known limit gave a share: %+v", unknown)
	}
}

// "No budget refused anything" is the answer that makes capacity legible on a
// healthy deployment, and it has to survive being asked one minute after a
// restart -- which is why the counts travel in the snapshot rather than being
// read back from a metric that collection may not have gathered yet.
func TestCapacityAddsUpRejectionsAcrossReplicas(t *testing.T) {
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	view := Aggregate(Expectation{QueryGroups: 20, Known: true}, []Snapshot{
		capacitySnapshot("pod-a", at, &Capacity{Rejections: map[string]uint64{"state_mutations": 3}}),
		capacitySnapshot("pod-b", at, &Capacity{Rejections: map[string]uint64{"state_mutations": 4, "events": 1}}),
	}, []string{"pod-a", "pod-b"}, at, time.Minute)

	if view.Capacity.Rejections["state_mutations"] != 7 || view.Capacity.Rejections["events"] != 1 {
		t.Fatalf("rejections did not add up: %+v", view.Capacity.Rejections)
	}
}

// A replica whose snapshot is missing or stale is already excluded from the
// verdict; its capacity must not slip in either, or the page reports occupancy
// for a replica it just said it could not hear from.
func TestCapacityCountsOnlyReplicasThatReported(t *testing.T) {
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	view := Aggregate(Expectation{QueryGroups: 20, Known: true}, []Snapshot{
		capacitySnapshot("pod-a", at, &Capacity{PermitsHeld: 5, PermitBudget: 32}),
	}, []string{"pod-a", "pod-b"}, at, time.Minute)

	if view.Capacity.Replicas != 1 || view.Capacity.PermitsHeld != 5 {
		t.Fatalf("capacity counted a replica that did not report: %+v", view.Capacity)
	}
}

// A snapshot too old to be counted towards coverage is too old to be counted
// towards occupancy. The two are the same read and the page presents capacity as
// the present, so a stale one puts the memory and permits of some earlier moment
// under a heading that says "此刻".
//
// The case that matters is the one where every snapshot is stale: coverage then
// reports no live replica and the verdict is UNKNOWN, and the capacity panel was
// still filling itself in from the snapshots the verdict had just refused. That
// is the worst possible pairing -- the page saying it cannot hear from anyone,
// next to numbers that look like it can.
func TestCapacityRefusesTheSnapshotsTheVerdictRefused(t *testing.T) {
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	view := Aggregate(Expectation{QueryGroups: 20, Known: true}, []Snapshot{
		capacitySnapshot("pod-a", at.Add(-10*time.Minute), &Capacity{
			PermitsHeld: 5, PermitBudget: 32, MemoryUsed: 700 << 20, MemoryLimit: 8 << 30,
		}),
	}, []string{"pod-a"}, at, time.Minute)

	if view.Health != HealthUnknown {
		t.Fatalf("health = %q, want UNKNOWN from a stale snapshot", view.Health)
	}
	if len(view.Replicas) != 0 {
		t.Fatalf("replicas = %v, want none counted", view.Replicas)
	}
	if view.Capacity != nil {
		t.Fatalf("capacity was reported from a snapshot the verdict refused: %+v", view.Capacity)
	}
}

// A replica that has left the deployment keeps its published snapshot until the
// key expires. Coverage already ignores it -- it is not in the expected set --
// and occupancy has to ignore it for the same reason, or a scale-down reads as
// unchanged load right up until the key times out.
func TestCapacityIgnoresAReplicaNoLongerInTheDeployment(t *testing.T) {
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	view := Aggregate(Expectation{QueryGroups: 20, Known: true}, []Snapshot{
		capacitySnapshot("pod-a", at, &Capacity{PermitsHeld: 5, PermitBudget: 32}),
		capacitySnapshot("pod-retired", at, &Capacity{PermitsHeld: 9, PermitBudget: 32}),
	}, []string{"pod-a"}, at, time.Minute)

	if view.Capacity.Replicas != 1 || view.Capacity.PermitsHeld != 5 {
		t.Fatalf("capacity counted a departed replica: %+v", view.Capacity)
	}
}

// A rotation's counts are per replica and the work is split between them, so
// the counts add up. The duration does not: a deployment covers its objects
// only as fast as its slowest replica gets round its own share, and averaging
// would report a coverage time no replica achieves.
func TestRotationAddsUpCountsAndKeepsTheSlowestDuration(t *testing.T) {
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	view := Aggregate(Expectation{QueryGroups: 20, Known: true}, []Snapshot{
		capacitySnapshot("pod-a", at, &Capacity{Rotation: &Rotation{
			Completed: 12, Truncated: 1, Offered: 600, Queued: 590, Deferred: 10, LastSeconds: 2.5,
		}}),
		capacitySnapshot("pod-b", at, &Capacity{Rotation: &Rotation{
			Completed: 9, Truncated: 3, Offered: 450, Queued: 430, Deferred: 20, LastSeconds: 7.25,
		}}),
	}, []string{"pod-a", "pod-b"}, at, time.Minute)

	rotation := view.Capacity.Rotation
	if rotation == nil {
		t.Fatal("rotation was not reported")
	}
	if rotation.Completed != 21 || rotation.Truncated != 4 {
		t.Fatalf("completed=%d truncated=%d, want 21 and 4", rotation.Completed, rotation.Truncated)
	}
	if rotation.Offered != 1050 || rotation.Queued != 1020 || rotation.Deferred != 30 {
		t.Fatalf("object counts did not add up: %+v", rotation)
	}
	if rotation.LastSeconds != 7.25 {
		t.Fatalf("last_seconds = %v, want the slowest replica's 7.25", rotation.LastSeconds)
	}
}

// A replica that has not finished a rotation reports none, and that must not
// read as a rotation of zero: "just started" and "stuck" are opposite
// conditions and a zero would show them the same way.
func TestRotationIsAbsentWhenNoReplicaHasFinishedOne(t *testing.T) {
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	view := Aggregate(Expectation{QueryGroups: 20, Known: true}, []Snapshot{
		capacitySnapshot("pod-a", at, &Capacity{PermitsHeld: 3, PermitBudget: 32}),
	}, []string{"pod-a"}, at, time.Minute)

	if view.Capacity.Rotation != nil {
		t.Fatalf("a deployment with no finished rotation reported %+v", view.Capacity.Rotation)
	}
}
