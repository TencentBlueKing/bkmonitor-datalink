// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import "strconv"

// OpenAlertCounts is a replica's count of open alerts for the strategies its
// copy of the consumer's open set tracks -- the strategies of the objects it
// owns -- as its gate reads them. Only strategies with at least one are
// listed; a tracked strategy whose set the copy cannot count (not loaded,
// stale, refused for its size) is in Unknown and must never read as zero.
// A strategy of an owned object the copy does not track -- refused for the
// copy's capacity, or not taken up yet after a takeover -- is in Unknown too.
// Both lists take the store's per-list budget, and Cut says one was cut;
// OwnedUnnamed says the replica could not name its owned objects' strategies,
// so an untracked one could be in neither list. Either way absence is not
// zero.
type OpenAlertCounts struct {
	Counts       []StrategyOpenCount `json:"counts,omitempty"`
	Unknown      []string            `json:"unknown,omitempty"`
	Cut          bool                `json:"cut,omitempty"`
	OwnedUnnamed bool                `json:"owned_unnamed,omitempty"`
}

// StrategyOpenCount is one strategy's open alerts on one replica's copy.
type StrategyOpenCount struct {
	Strategy string `json:"strategy"`
	Count    int    `json:"count"`
}

// withinCountBudget keeps the longest prefix of the sorted counts that fits.
func withinCountBudget(counts []StrategyOpenCount, budget int) []StrategyOpenCount {
	if budget <= 0 {
		return counts
	}
	spent := 0
	for index, count := range counts {
		spent += len(count.Strategy) + len(strconv.Itoa(count.Count)) + len(`{"strategy":"","count":},`)
		if spent > budget {
			return counts[:index]
		}
	}
	return counts
}

// OpenView is the replicas' open-alert counts kept apart by replica, in
// memory with the view a diagnosis decides on: two replicas tracking the same
// strategy hold copies of one consumer set, so their counts are taken at the
// largest, never added.
type OpenView struct {
	counts   map[string]map[string]int
	unknown  map[string]map[string]bool
	complete map[string]bool
}

// aggregateOpenAlerts gathers each replica's counts. Absent on every replica
// is no copy anywhere, and the view stays without one.
func aggregateOpenAlerts(view *View, snapshots []Snapshot) {
	var open *OpenView
	for _, snapshot := range snapshots {
		if snapshot.OpenAlerts == nil {
			continue
		}
		if open == nil {
			open = &OpenView{counts: map[string]map[string]int{}, unknown: map[string]map[string]bool{}, complete: map[string]bool{}}
		}
		counts, unknown := map[string]int{}, map[string]bool{}
		for _, count := range snapshot.OpenAlerts.Counts {
			counts[count.Strategy] = count.Count
		}
		for _, strategy := range snapshot.OpenAlerts.Unknown {
			unknown[strategy] = true
		}
		open.counts[snapshot.Replica], open.unknown[snapshot.Replica] = counts, unknown
		open.complete[snapshot.Replica] = !snapshot.OpenAlerts.Cut && !snapshot.OpenAlerts.OwnedUnnamed
	}
	view.OpenAlerts = open
}

// strategyOpenAlerts is the strategy's open alerts across the replicas its
// objects are on: the largest count, exact when every one of those replicas
// published its counts whole and could count the strategy, at least when a
// count exists beside a replica that could not, and unknown (nil) when
// nothing is known.
func strategyOpenAlerts(row DiagnosisRow, open *OpenView) *BoardCount {
	if open == nil {
		return nil
	}
	replicas := map[string]bool{}
	for _, plan := range row.Plans {
		if plan.QueryGroup != "" && plan.Replica != "" {
			replicas[plan.Replica] = true
		}
	}
	if len(replicas) == 0 {
		return nil
	}
	value, exact := 0, true
	for replica := range replicas {
		counts, published := open.counts[replica]
		if !published || !open.complete[replica] || open.unknown[replica][row.StrategyID] {
			exact = false
		}
		if count := counts[row.StrategyID]; count > value {
			value = count
		}
	}
	if exact {
		return &BoardCount{Value: value}
	}
	if value > 0 {
		return &BoardCount{Value: value, AtLeast: true}
	}
	return nil
}
