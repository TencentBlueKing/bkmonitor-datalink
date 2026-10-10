// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// LeaderRoundStageTotal is the stage label that carries the whole round, so
// a stage's share is one division between two series of one family.
const LeaderRoundStageTotal = "total"

// LeaderRoundStats is the control leader's reconcile rounds since this
// process started: how many, by result, and the seconds each stage has
// taken across them, the whole round under LeaderRoundStageTotal. Leading
// is false on a process that has run none, which emits nothing.
type LeaderRoundStats struct {
	Leading bool
	Rounds  map[string]uint64
	Seconds map[string]float64
}

// leaderRoundCollector reads the rounds at scrape time. Counters, not a
// histogram: the question they answer is where the round's time goes, and
// that is the ratio of two sums; the latest round's exact stages are on the
// fleet snapshot.
type leaderRoundCollector struct {
	mu      sync.Mutex
	source  func() LeaderRoundStats
	rounds  *prometheus.Desc
	seconds *prometheus.Desc
}

func newLeaderRoundCollector() *leaderRoundCollector {
	return &leaderRoundCollector{
		rounds: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "leader_rounds_total"),
			"Reconcile rounds the control leader ran in this process, by result: completed, or failed in one of "+
				"its stages. Emitted by a process once it has led a round, both results from then on.",
			[]string{"result"}, nil),
		seconds: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "leader_round_stage_seconds_total"),
			"Seconds the control leader's reconcile rounds in this process spent in each stage, and the whole "+
				"round under stage=\"total\". A stage's share of the round is its increase over total's; the "+
				"latest round stage by stage is leader_round on the fleet snapshot. Every stage is emitted "+
				"once the process has led a round, zero for a stage no round reached.",
			[]string{"stage"}, nil),
	}
}

func (c *leaderRoundCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.rounds
	ch <- c.seconds
}

func (c *leaderRoundCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	if source == nil {
		return
	}
	stats := source()
	if !stats.Leading {
		return
	}
	for _, result := range []string{fleet.LeaderRoundCompleted, fleet.LeaderRoundFailed} {
		ch <- prometheus.MustNewConstMetric(c.rounds, prometheus.CounterValue, float64(stats.Rounds[result]), result)
	}
	for _, stage := range append(append([]string(nil), fleet.LeaderRoundStages...), LeaderRoundStageTotal) {
		ch <- prometheus.MustNewConstMetric(c.seconds, prometheus.CounterValue, stats.Seconds[stage], stage)
	}
}

// SetLeaderRoundSource binds the process's leader rounds to the collector.
// Until it is bound the collector emits nothing.
func (r *Recorder) SetLeaderRoundSource(source func() LeaderRoundStats) {
	if r == nil || r.phaseTwo.leaderRound == nil {
		return
	}
	r.phaseTwo.leaderRound.mu.Lock()
	r.phaseTwo.leaderRound.source = source
	r.phaseTwo.leaderRound.mu.Unlock()
}
