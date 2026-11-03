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
	"reflect"
	"testing"
)

// The view groups the counted replicas by the build they reported, most
// replicas first. A replica that reported none is a group of its own with an
// empty build: it is never filed under a version it may not be running.
func TestAggregateGroupsCountedReplicasByBuild(t *testing.T) {
	newer := BuildFacts{Version: "0.2.4506", Commit: "62ee924d", SchemaVersion: "v3"}
	older := BuildFacts{Version: "0.2.4505", Commit: "4f338bf3", SchemaVersion: "v3"}
	snapshots := []Snapshot{
		{Replica: "pod-a", TakenAt: now, Build: &older},
		{Replica: "pod-b", TakenAt: now, Build: &newer},
		{Replica: "pod-c", TakenAt: now, Build: &newer},
		{Replica: "pod-d", TakenAt: now},
	}
	all := []string{"pod-a", "pod-b", "pod-c", "pod-d"}
	view := Aggregate(Expectation{Known: true}, snapshots, all, now, freshness)
	want := []BuildGroup{
		{Build: newer, Replicas: []string{"pod-b", "pod-c"}},
		{Build: older, Replicas: []string{"pod-a"}},
		{Build: BuildFacts{}, Replicas: []string{"pod-d"}},
	}
	if !reflect.DeepEqual(view.Builds, want) {
		t.Fatalf("builds = %+v, want %+v", view.Builds, want)
	}
	for _, replica := range view.PerReplica {
		switch replica.Replica {
		case "pod-a":
			if replica.Build == nil || *replica.Build != older {
				t.Fatalf("pod-a build = %+v, want %+v", replica.Build, older)
			}
		case "pod-d":
			if replica.Build != nil {
				t.Fatalf("pod-d build = %+v, want absent: it reported none", *replica.Build)
			}
		}
	}

	// A replica whose snapshot is stale is not counted, so it is in no group;
	// the groups describe the replicas the totals were added up from.
	stale := append([]Snapshot(nil), snapshots...)
	stale[0].TakenAt = now.Add(-2 * freshness)
	partial := Aggregate(Expectation{Known: true}, stale, all, now, freshness)
	for _, group := range partial.Builds {
		for _, replica := range group.Replicas {
			if replica == "pod-a" {
				t.Fatalf("stale pod-a filed under %+v; a replica the totals left out must not be in a build group", group.Build)
			}
		}
	}

	// One build on every counted replica is one group. The page reads the
	// length to say "一致", so a deployment that agrees must produce exactly
	// one entry and not one per replica.
	agreed := Aggregate(Expectation{Known: true}, []Snapshot{
		{Replica: "pod-a", TakenAt: now, Build: &newer},
		{Replica: "pod-b", TakenAt: now, Build: &newer},
	}, []string{"pod-a", "pod-b"}, now, freshness)
	if len(agreed.Builds) != 1 || len(agreed.Builds[0].Replicas) != 2 {
		t.Fatalf("builds = %+v, want one group holding both replicas", agreed.Builds)
	}

	// Empty, not null, with no replicas counted: the page iterates it.
	none := Aggregate(Expectation{Known: true}, nil, all, now, freshness)
	encoded, err := json.Marshal(none)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if string(decoded["builds"]) != "[]" {
		t.Fatalf("builds with nothing counted encoded as %s, want []", decoded["builds"])
	}
}

// An evenly split deployment lists the same way on every read: the group
// order cannot depend on which snapshot the store returned first.
func TestBuildGroupsOrderIsStableAcrossReadOrder(t *testing.T) {
	a := BuildFacts{Version: "0.2.4506", Commit: "62ee924d"}
	b := BuildFacts{Version: "0.2.4505", Commit: "4f338bf3"}
	first := Aggregate(Expectation{Known: true}, []Snapshot{
		{Replica: "pod-a", TakenAt: now, Build: &a}, {Replica: "pod-b", TakenAt: now, Build: &b},
	}, []string{"pod-a", "pod-b"}, now, freshness)
	second := Aggregate(Expectation{Known: true}, []Snapshot{
		{Replica: "pod-b", TakenAt: now, Build: &b}, {Replica: "pod-a", TakenAt: now, Build: &a},
	}, []string{"pod-b", "pod-a"}, now, freshness)
	if !reflect.DeepEqual(first.Builds, second.Builds) {
		t.Fatalf("group order depends on read order: %+v vs %+v", first.Builds, second.Builds)
	}
	if first.Builds[0].Build != a {
		t.Fatalf("even split lists %+v first, want the higher version %+v", first.Builds[0].Build, a)
	}
}
