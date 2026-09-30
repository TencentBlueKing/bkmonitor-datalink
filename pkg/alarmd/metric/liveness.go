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
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// LivenessLoops are the long-running loops the liveness probe judges.
var LivenessLoops = []string{"control", "dispatch"}

// loopTurnDurationBuckets run from a millisecond to past the control loop's
// fifteen-minute bound, by fours.
var loopTurnDurationBuckets = prometheus.ExponentialBuckets(0.001, 4, 11)

// LivenessReading is what the liveness probe judges by, read at scrape time.
// TurnAge carries only the loops that have started: a loop that has not is
// not late, and reading it as zero would say it just turned.
type LivenessReading struct {
	TurnAge                map[string]time.Duration
	ExecutionsPastDeadline int
}

type livenessCollector struct {
	mu        sync.Mutex
	source    func() LivenessReading
	turnAge   *prometheus.Desc
	pastLimit *prometheus.Desc
	durations *prometheus.HistogramVec
}

func newLivenessCollector() *livenessCollector {
	durations := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "loop_turn_duration_seconds",
		Help: "How long one turn of a long-running loop took, by loop, failed turns included. control is the " +
			"refresh and reconcile loop; dispatch is the scheduler's queue loop, measured up to the wait. " +
			"The liveness bound on a loop is judged against the longest turn this shows.",
		Buckets: loopTurnDurationBuckets,
	}, []string{"loop"})
	for _, loop := range LivenessLoops {
		durations.WithLabelValues(loop)
	}
	return &livenessCollector{
		turnAge: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "loop_turn_age_seconds"),
			"Seconds since a long-running loop last finished a turn, successful or not, by loop. A loop past its "+
				"bound fails the liveness probe. Absent until the loop starts.", []string{"loop"}, nil),
		pastLimit: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "executions_past_deadline"),
			"Slot executions that have run more than a minute past their deadline and not returned. An execution "+
				"that honours its context returns at the deadline, so anything here ignored it. When every execution "+
				"slot holds one for five minutes the liveness probe fails.", nil, nil),
		durations: durations,
	}
}

// SetLivenessSource binds the collector to the runtime's liveness reading.
func (r *Recorder) SetLivenessSource(source func() LivenessReading) {
	if r == nil || r.phaseTwo.liveness == nil {
		return
	}
	r.phaseTwo.liveness.mu.Lock()
	r.phaseTwo.liveness.source = source
	r.phaseTwo.liveness.mu.Unlock()
}

// ObserveLoopTurn records one finished turn of a named loop.
func (r *Recorder) ObserveLoopTurn(loop string, took time.Duration) {
	if r == nil || r.phaseTwo.liveness == nil || !knownLabel(LivenessLoops, loop) {
		return
	}
	r.phaseTwo.liveness.durations.WithLabelValues(loop).Observe(took.Seconds())
}

func (c *livenessCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.turnAge
	ch <- c.pastLimit
	c.durations.Describe(ch)
}

func (c *livenessCollector) Collect(ch chan<- prometheus.Metric) {
	c.durations.Collect(ch)
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	var reading LivenessReading
	if source != nil {
		reading = source()
	}
	for _, loop := range LivenessLoops {
		if age, started := reading.TurnAge[loop]; started {
			ch <- prometheus.MustNewConstMetric(c.turnAge, prometheus.GaugeValue, age.Seconds(), loop)
		}
	}
	ch <- prometheus.MustNewConstMetric(c.pastLimit, prometheus.GaugeValue, float64(reading.ExecutionsPastDeadline))
}
