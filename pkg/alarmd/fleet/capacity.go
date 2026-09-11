// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import "sort"

// Capacity is one replica's answer to "how close am I to my limits".
//
// It travels in the replica's snapshot rather than being read back out of the
// time series database. The page then reports occupancy from the same read that
// produces the verdict, with no dependency on collection having happened, and
// it is right on a deployment that was started a minute ago.
type Capacity struct {
	// PermitsHeld and PermitBudget are an instant. They answer "is anything
	// running right now", not "was the budget full", which is what PermitSeconds
	// is for -- a scrape or a page load lands at an arbitrary moment, and a
	// level read at an arbitrary moment says nothing about the interval.
	PermitsHeld  int `json:"permits_held"`
	PermitBudget int `json:"permit_budget"`
	// PermitSeconds is cumulative permit-hold time. Two reads of it divided by
	// the wall time between them give the mean number of permits occupied, which
	// is the number the budget is meant to be compared against.
	PermitSeconds float64 `json:"permit_seconds"`
	// Waiting is callers queued for a permit. Non-zero means the budget is the
	// constraint right now; zero does not mean it has room to spare, because a
	// caller admitted immediately never appears here.
	Waiting      int    `json:"waiting"`
	QueueBudget  int    `json:"queue_budget"`
	MemoryUsed   uint64 `json:"memory_used_bytes,omitempty"`
	MemoryLimit  uint64 `json:"memory_limit_bytes,omitempty"`
	MemorySource string `json:"memory_source,omitempty"`
	// MemoryLimitHits and MemoryOOMKills are the container's own account of
	// having reached the memory limit, as opposed to a ratio somebody has to
	// judge. The operator set the limit; these say whether the process ran into
	// it, so "is memory enough" has an answer nobody has to pick a threshold
	// for.
	MemoryLimitHits uint64 `json:"memory_limit_hits,omitempty"`
	MemoryOOMKills  uint64 `json:"memory_oom_kills,omitempty"`
	// MemoryLimitKnown and ThrottledKnown say the counters above were actually
	// read.
	//
	// Without them a zero means two different things -- "this container never
	// reached its limit" and "these files were not readable here" -- and the
	// page would answer the capacity question most confidently in exactly the
	// case where it measured nothing. A deployment outside a container, or on a
	// cgroup layout this build does not handle, would read as comfortably
	// within its limits.
	MemoryLimitKnown bool `json:"memory_limit_known,omitempty"`
	ThrottledKnown   bool `json:"throttled_known,omitempty"`
	// ThrottledSeconds separates "busy" from "not allowed to run". A process
	// sitting at its CPU quota and an idle one being held back are identical in
	// any busy-time measure, and only this tells them apart.
	ThrottledSeconds float64 `json:"throttled_seconds,omitempty"`
	CPUCores         int     `json:"cpu_cores,omitempty"`
	// CPUSource says where the core count came from, the way MemorySource does
	// for memory -- and it matters more, because every ceiling below is derived
	// from it. A process that failed to read the container's quota sizes itself
	// for the host, and then reports a whole table of ceilings that look
	// authoritative and are several times too large. Without this the page has
	// no way to tell that apart from a correctly sized deployment.
	CPUSource string `json:"cpu_source,omitempty"`
	// Budgets are the derived per-Slot ceilings, keyed the way rejections are
	// labelled so a rejection can be read against the limit it hit. They are
	// per-Slot caps rather than a pool, so the honest reading is how close the
	// largest Slot came -- never a utilization ratio.
	Budgets map[string]uint64 `json:"budgets,omitempty"`
	// Rejections counts admission refusals per budget since the process
	// started. Zero across the board is the useful common case: it says no
	// budget is the thing holding this deployment back.
	Rejections map[string]uint64 `json:"rejections,omitempty"`
	// Rotation says whether this replica is still getting round every object it
	// owns, and how long that takes. An object list can say an object is
	// degraded; only this says an object is never reached.
	Rotation *Rotation `json:"rotation,omitempty"`
	// Pulled is how much this replica actually read. Absent on a replica that
	// does not report it.
	Pulled *SeriesPull `json:"pulled,omitempty"`
}

// Rotation is one pass of the dispatcher over everything a replica owns.
//
// One generation is one rotation: the walk considers every owned object exactly
// once before it finishes. So "did everything get a turn" has an answer, and
// "how long does a full turn take" is the number an alert on coverage needs --
// the one §11 asked for and nothing measured.
type Rotation struct {
	// Completed counts rotations that reached every owned object. Truncated
	// counts those cut short, usually by a full ready queue. A deployment whose
	// truncated count climbs while completed does not is one that has stopped
	// covering its objects, which no per-object signal reports.
	Completed uint64 `json:"completed"`
	Truncated uint64 `json:"truncated"`
	// Offered and Queued count objects: how many the walk reached, and how many
	// of those got a place. Deferred counts turn-aways instead, and the same
	// object turned away on ten passes counts ten times -- that repetition is
	// the signal, because a deferral is not a failure but a deferral that keeps
	// happening is an object nothing will reach.
	Offered  uint64 `json:"offered"`
	Queued   uint64 `json:"queued"`
	Deferred uint64 `json:"deferred"`
	// LastSeconds is how long the most recent completed rotation took. It is an
	// instant, not an average: a rotation either finished or it did not, and
	// averaging the two would describe neither.
	LastSeconds float64 `json:"last_seconds"`
}

// CapacityView is the deployment's capacity as the page receives it.
//
// Occupancy is summed because the permits are per replica and the work is
// spread over them; the ceilings are reported as one value because every
// replica derives them from the same container shape, and a replica that
// disagrees is reported rather than averaged away.
type CapacityView struct {
	Replicas         int               `json:"replicas"`
	PermitsHeld      int               `json:"permits_held"`
	PermitBudget     int               `json:"permit_budget"`
	PermitSeconds    float64           `json:"permit_seconds"`
	Waiting          int               `json:"waiting"`
	QueueBudget      int               `json:"queue_budget"`
	MemoryUsed       uint64            `json:"memory_used_bytes,omitempty"`
	MemoryLimit      uint64            `json:"memory_limit_bytes,omitempty"`
	MemorySource     string            `json:"memory_source,omitempty"`
	MemoryLimitHits  uint64            `json:"memory_limit_hits,omitempty"`
	MemoryOOMKills   uint64            `json:"memory_oom_kills,omitempty"`
	MemoryLimitKnown bool              `json:"memory_limit_known,omitempty"`
	ThrottledKnown   bool              `json:"throttled_known,omitempty"`
	ThrottledSeconds float64           `json:"throttled_seconds,omitempty"`
	CPUCores         int               `json:"cpu_cores,omitempty"`
	CPUSource        string            `json:"cpu_source,omitempty"`
	Budgets          map[string]uint64 `json:"budgets,omitempty"`
	Rejections       map[string]uint64 `json:"rejections,omitempty"`
	// Rotation is summed across replicas for the counts and reported as the
	// slowest for the duration: a deployment covers its objects only as fast as
	// its slowest replica gets round its own share.
	Rotation *Rotation `json:"rotation,omitempty"`
	// Pulled adds up across replicas: the work is split between them, so what
	// the deployment read is what its replicas read.
	Pulled *SeriesPull `json:"pulled,omitempty"`
	// Disagreement names ceilings the replicas do not agree on. Two replicas
	// running different limits is a real condition -- a half-finished rollout --
	// and averaging it would hide exactly the thing worth seeing.
	Disagreement []string `json:"disagreement,omitempty"`
}

func aggregateCapacity(view *View, snapshots []Snapshot) {
	capacity := CapacityView{Budgets: map[string]uint64{}, Rejections: map[string]uint64{}}
	disagreed := map[string]struct{}{}
	for _, snapshot := range snapshots {
		facts := snapshot.Capacity
		if facts == nil {
			continue
		}
		capacity.Replicas++
		capacity.PermitsHeld += facts.PermitsHeld
		capacity.PermitSeconds += facts.PermitSeconds
		capacity.Waiting += facts.Waiting
		capacity.MemoryUsed += facts.MemoryUsed
		capacity.ThrottledSeconds += facts.ThrottledSeconds
		// Summed, like the throttling: reaching the limit is something each
		// container does on its own, and a deployment where one replica keeps
		// hitting it is a deployment hitting it. Averaging would divide one
		// replica's trouble by the replicas that are fine.
		capacity.MemoryLimitHits += facts.MemoryLimitHits
		capacity.MemoryOOMKills += facts.MemoryOOMKills
		// Ceilings are per replica and expected to be identical. The first one
		// seen sets the value; a later one that differs is named rather than
		// silently overwritten.
		setCeiling := func(name string, current *int, seen int) {
			if capacity.Replicas == 1 {
				*current = seen
				return
			}
			if *current != seen {
				disagreed[name] = struct{}{}
			}
		}
		setCeiling("permit_budget", &capacity.PermitBudget, facts.PermitBudget)
		setCeiling("queue_budget", &capacity.QueueBudget, facts.QueueBudget)
		setCeiling("cpu_cores", &capacity.CPUCores, facts.CPUCores)
		if capacity.Replicas == 1 {
			capacity.MemoryLimit, capacity.MemorySource = facts.MemoryLimit, facts.MemorySource
			capacity.CPUSource = facts.CPUSource
			capacity.MemoryLimitKnown, capacity.ThrottledKnown = facts.MemoryLimitKnown, facts.ThrottledKnown
		} else {
			// Every replica has to have measured, because the counts are summed:
			// a replica that read nothing contributes a silent zero, and calling
			// the total "measured" would let one unreadable container make the
			// deployment look like it never reached its limits.
			capacity.MemoryLimitKnown = capacity.MemoryLimitKnown && facts.MemoryLimitKnown
			capacity.ThrottledKnown = capacity.ThrottledKnown && facts.ThrottledKnown
			if capacity.MemoryLimit != facts.MemoryLimit {
				disagreed["memory_limit"] = struct{}{}
			}
			// Named even when the core counts agree. Two replicas that arrived
			// at the same number by different routes -- one reading the quota,
			// one falling back to the host it happens to share a size with --
			// are one node reschedule away from disagreeing, and only the
			// source says so.
			if capacity.CPUSource != facts.CPUSource {
				disagreed["cpu_source"] = struct{}{}
			}
		}
		for budget, ceiling := range facts.Budgets {
			if existing, seen := capacity.Budgets[budget]; seen && existing != ceiling {
				disagreed[budget] = struct{}{}
				continue
			}
			capacity.Budgets[budget] = ceiling
		}
		for budget, count := range facts.Rejections {
			capacity.Rejections[budget] += count
		}
		if facts.Pulled != nil {
			if capacity.Pulled == nil {
				capacity.Pulled = &SeriesPull{}
			}
			capacity.Pulled.Series += facts.Pulled.Series
			capacity.Pulled.Records += facts.Pulled.Records
		}
		if facts.Rotation != nil {
			if capacity.Rotation == nil {
				capacity.Rotation = &Rotation{}
			}
			capacity.Rotation.Completed += facts.Rotation.Completed
			capacity.Rotation.Truncated += facts.Rotation.Truncated
			capacity.Rotation.Offered += facts.Rotation.Offered
			capacity.Rotation.Queued += facts.Rotation.Queued
			capacity.Rotation.Deferred += facts.Rotation.Deferred
			if facts.Rotation.LastSeconds > capacity.Rotation.LastSeconds {
				capacity.Rotation.LastSeconds = facts.Rotation.LastSeconds
			}
		}
	}
	if capacity.Replicas == 0 {
		return
	}
	for name := range disagreed {
		capacity.Disagreement = append(capacity.Disagreement, name)
	}
	sort.Strings(capacity.Disagreement)
	view.Capacity = &capacity
}
