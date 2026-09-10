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
	// ThrottledSeconds separates "busy" from "not allowed to run". A process
	// sitting at its CPU quota and an idle one being held back are identical in
	// any busy-time measure, and only this tells them apart.
	ThrottledSeconds float64 `json:"throttled_seconds,omitempty"`
	CPUCores         int     `json:"cpu_cores,omitempty"`
	// Budgets are the derived per-Slot ceilings, keyed the way rejections are
	// labelled so a rejection can be read against the limit it hit. They are
	// per-Slot caps rather than a pool, so the honest reading is how close the
	// largest Slot came -- never a utilization ratio.
	Budgets map[string]uint64 `json:"budgets,omitempty"`
	// Rejections counts admission refusals per budget since the process
	// started. Zero across the board is the useful common case: it says no
	// budget is the thing holding this deployment back.
	Rejections map[string]uint64 `json:"rejections,omitempty"`
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
	ThrottledSeconds float64           `json:"throttled_seconds,omitempty"`
	CPUCores         int               `json:"cpu_cores,omitempty"`
	Budgets          map[string]uint64 `json:"budgets,omitempty"`
	Rejections       map[string]uint64 `json:"rejections,omitempty"`
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
		} else if capacity.MemoryLimit != facts.MemoryLimit {
			disagreed["memory_limit"] = struct{}{}
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
