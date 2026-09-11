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

// Reaching the memory limit is something one container does on its own. A
// deployment where a single replica keeps being held at its limit is a
// deployment with a memory problem, and dividing that replica's trouble by the
// replicas that are fine would report it as nearly nothing.
func TestReachingTheMemoryLimitAddsUpAcrossReplicas(t *testing.T) {
	at := time.Date(2026, 9, 11, 15, 0, 0, 0, time.UTC)
	view := Aggregate(Expectation{QueryGroups: 20, Known: true}, []Snapshot{
		capacitySnapshot("pod-a", at, &Capacity{MemoryLimitHits: 40, MemoryOOMKills: 1}),
		capacitySnapshot("pod-b", at, &Capacity{MemoryLimitHits: 0, MemoryOOMKills: 0}),
	}, []string{"pod-a", "pod-b"}, at, time.Minute)

	if view.Capacity.MemoryLimitHits != 40 {
		t.Fatalf("hits = %d, want the one replica's 40 kept whole", view.Capacity.MemoryLimitHits)
	}
	if view.Capacity.MemoryOOMKills != 1 {
		t.Fatalf("kills = %d, want the kill reported", view.Capacity.MemoryOOMKills)
	}
}

// The counts are summed, so a replica that could not read its cgroup files
// contributes a silent zero. Calling the total measured would let one
// unreadable container make the whole deployment look like it never reached its
// limits -- and the page is at its most confident on exactly that reading.
func TestOneReplicaThatMeasuredNothingMakesTheWholeAnswerUnmeasured(t *testing.T) {
	at := time.Date(2026, 9, 11, 15, 0, 0, 0, time.UTC)
	view := Aggregate(Expectation{QueryGroups: 20, Known: true}, []Snapshot{
		capacitySnapshot("pod-a", at, &Capacity{MemoryLimitKnown: true, ThrottledKnown: true}),
		capacitySnapshot("pod-b", at, &Capacity{MemoryLimitKnown: false, ThrottledKnown: true}),
	}, []string{"pod-a", "pod-b"}, at, time.Minute)

	if view.Capacity.MemoryLimitKnown {
		t.Fatal("the deployment claimed a memory reading one of its replicas never took")
	}
	// The dimension that every replica did measure keeps its answer: losing it
	// too would hide a real throttling reading behind an unrelated gap.
	if !view.Capacity.ThrottledKnown {
		t.Fatal("throttling was reported as unmeasured although both replicas measured it")
	}
}

// Every ceiling on the panel is derived from the core count, so where the core
// count came from decides whether the whole table describes this container or
// the host it happens to be running on. A process that could not read the
// container's quota sizes itself for the host and then publishes ceilings that
// look authoritative and are several times too large.
func TestTheCoreCountCarriesWhereItCameFrom(t *testing.T) {
	at := time.Date(2026, 9, 11, 15, 0, 0, 0, time.UTC)
	view := Aggregate(Expectation{QueryGroups: 20, Known: true}, []Snapshot{
		capacitySnapshot("pod-a", at, &Capacity{CPUCores: 8, CPUSource: "cpu_quota"}),
	}, []string{"pod-a"}, at, time.Minute)

	if view.Capacity.CPUSource != "cpu_quota" {
		t.Fatalf("cpu source = %q, want the provenance carried to the page", view.Capacity.CPUSource)
	}
}

// Named even though both replicas report eight cores. Two replicas that reached
// the same number by different routes -- one reading the quota, one falling
// back to a host that happens to have that many cores -- are one reschedule
// away from disagreeing, and the core count alone cannot show it.
func TestReplicasResolvingTheirCoresDifferentlyAreNamedEvenWhenTheCountsAgree(t *testing.T) {
	at := time.Date(2026, 9, 11, 15, 0, 0, 0, time.UTC)
	view := Aggregate(Expectation{QueryGroups: 20, Known: true}, []Snapshot{
		capacitySnapshot("pod-a", at, &Capacity{CPUCores: 8, CPUSource: "cpu_quota"}),
		capacitySnapshot("pod-b", at, &Capacity{CPUCores: 8, CPUSource: "runtime_default"}),
	}, []string{"pod-a", "pod-b"}, at, time.Minute)

	found := false
	for _, name := range view.Capacity.Disagreement {
		if name == "cpu_source" {
			found = true
		}
	}
	if !found {
		t.Fatalf("disagreement = %v, want cpu_source named", view.Capacity.Disagreement)
	}
	// The count itself agrees, so it must not be reported as a disagreement --
	// that would send whoever reads it looking for a difference in the wrong
	// place.
	for _, name := range view.Capacity.Disagreement {
		if name == "cpu_cores" {
			t.Fatalf("cpu_cores reported as disagreeing when both replicas said 8")
		}
	}
}

// A core count with no measured usage beside it says the same thing to a
// deployment using half a core and one using seven -- and that count is what
// every ceiling on the panel is derived from, so it is the figure that most
// needed a load beside it. Summed across replicas, like the other occupancy.
func TestCPUTimeAddsUpSoTheCoreCountCanBeReadAgainstIt(t *testing.T) {
	at := time.Date(2026, 9, 11, 18, 0, 0, 0, time.UTC)
	view := Aggregate(Expectation{QueryGroups: 20, Known: true}, []Snapshot{
		capacitySnapshot("pod-a", at, &Capacity{CPUSeconds: 120.5, CPUCores: 8}),
		capacitySnapshot("pod-b", at, &Capacity{CPUSeconds: 80.25, CPUCores: 8}),
	}, []string{"pod-a", "pod-b"}, at, time.Minute)

	if view.Capacity.CPUSeconds != 200.75 {
		t.Fatalf("cpu seconds = %v, want the replicas' usage added up", view.Capacity.CPUSeconds)
	}
	// The ceiling is per replica and must not be summed here; the page does that
	// multiplication itself and would double it otherwise.
	if view.Capacity.CPUCores != 8 {
		t.Fatalf("cpu cores = %d, want the per-replica ceiling reported once", view.Capacity.CPUCores)
	}
}
