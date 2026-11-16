// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

// stateCacheCollector reports the write-through State cache (fourth-batch
// design §4.7) from the execution store bound to it. Every cell of every
// closed vocabulary is published from the start, so a zero is a reading; a
// process with no store bound publishes none of them.
type stateCacheCollector struct {
	mu                                                             sync.Mutex
	source                                                         func() state.StateCacheStats
	lookups, departures, restored, declined, bytes, budget, groups *prometheus.Desc
}

func newStateCacheCollector() *stateCacheCollector {
	descriptor := func(name, help string, labels ...string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName("", metricSubsystem, name), help, labels, nil)
	}
	return &stateCacheCollector{
		lookups: descriptor("state_cache_lookups_total",
			"Runtime State keys a load asked the write-through State cache for, by result: hit (served from "+
				"what this process last wrote), or why it was read back - miss_absent (nothing kept), "+
				"miss_fence (another owner fence), miss_unknown (the last write's outcome unknown), "+
				"miss_expired (past its key's life less the replay age), miss_replay (a replay, retry or "+
				"older Slot), miss_activation (the Plan's activation changed: moved, put back, reactivated, "+
				"edited), miss_sharded (a split strategy's Plan, never served), miss_over_budget (its Query "+
				"Group kept out: a round of it does not fit the budget), miss_yielded (its Query Group kept out: a "+
				"round of it fits the budget but not what the Slots leave of the retained pool, or it was given back "+
				"for a Slot). Every cell exists from the start, so a zero is a reading.", "result"),
		departures: descriptor("state_cache_departures_total",
			"Entries that left the State cache, by reason: released (the Query Group stopped, was lost, "+
				"declined or handed over), unknown (a write of its request ended in doubt), conflict (its "+
				"write did not apply), expired, renewal_missing, untouched (neither read nor written for two "+
				"of its own intervals), over_budget (its Query Group did not fit the budget: it left whole, or a write "+
				"of it was not taken in), yielded (the same for what the Slots leave of the retained pool, or its "+
				"Query Group given back whole for a Slot that needed the room). A group kept out is judged again "+
				"once a round, by a whole round of it, so one that never fits leaves once rather than every round. "+
				"Every cell exists from the start.", "reason"),
		restored: descriptor("state_cache_restored_total",
			"Values written back over an external change to a key the State cache served, by cause: missing "+
				"(the key was deleted or evicted) and rolled_back (older bytes than this process last wrote, "+
				"with the Query Group's Progress still at this Slot). A legitimate writer is never written "+
				"over: see state_cache_restore_declined_total.", "cause"),
		declined: descriptor("state_cache_restore_declined_total",
			"Conflicts on a key the State cache served that were not written back over, by reason: "+
				"progress_rolled_back (the Progress went back with the State, so the Slot is replayed from "+
				"the store) and newer_version (the stored bytes are no older than the cached ones: a "+
				"legitimate writer's, handled as an ordinary conflict).", "reason"),
		bytes: descriptor("state_cache_bytes",
			"The State cache's charge: each entry's value and key rounded to their size classes plus a fixed "+
				"overhead, summed. Read against state_cache_budget_bytes."),
		budget: descriptor("state_cache_budget_bytes",
			"The most the State cache may hold: the container's thirty-second, borrowed from the retained-bytes "+
				"pool while no Slot has reserved it and given back when one needs it. A Query Group that would go "+
				"past it or past the pool's room is kept out and read back; the resident ones stay."),
		groups: descriptor("state_cache_query_groups", "Query Groups holding entries in the State cache."),
	}
}

// SetStateCacheSource binds the collector to the live execution store.
func (r *Recorder) SetStateCacheSource(source func() state.StateCacheStats) {
	if r == nil || r.phaseTwo.stateCache == nil {
		return
	}
	r.phaseTwo.stateCache.mu.Lock()
	r.phaseTwo.stateCache.source = source
	r.phaseTwo.stateCache.mu.Unlock()
}

func (c *stateCacheCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{c.lookups, c.departures, c.restored, c.declined, c.bytes, c.budget, c.groups} {
		ch <- desc
	}
}

func (c *stateCacheCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	if source == nil {
		return
	}
	stats := source()
	for _, family := range []struct {
		desc   *prometheus.Desc
		words  []string
		counts map[string]uint64
	}{
		{c.lookups, state.StateCacheLookupResults, stats.Lookups},
		{c.departures, state.StateCacheDepartureReasons, stats.Departures},
		{c.restored, state.StateCacheRestoreCauses, stats.Restored},
		{c.declined, state.StateCacheDeclineReasons, stats.Declined},
	} {
		for _, word := range family.words {
			ch <- prometheus.MustNewConstMetric(family.desc, prometheus.CounterValue, float64(family.counts[word]), word)
		}
	}
	ch <- prometheus.MustNewConstMetric(c.bytes, prometheus.GaugeValue, float64(stats.Bytes))
	ch <- prometheus.MustNewConstMetric(c.budget, prometheus.GaugeValue, float64(stats.BudgetBytes))
	ch <- prometheus.MustNewConstMetric(c.groups, prometheus.GaugeValue, float64(stats.QueryGroups))
}
