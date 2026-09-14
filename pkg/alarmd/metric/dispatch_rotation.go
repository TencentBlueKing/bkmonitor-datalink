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
)

// DispatchRotationCounts is the dispatcher's walk over the objects it owns, in
// the two populations it counts.
//
// None of this was on /metrics. It reached the verdict page over the API and
// nowhere else, which meant the one signal that says the deployment stopped
// covering its objects -- truncated climbing while completed does not -- could
// not be alerted on, and the only counter that answers "was anything turned away
// for lack of a place" could only be read by a person looking at a page.
//
// The two populations are published as two metrics rather than as labels on one.
// Rotations and object-turns are different things counted at different rates,
// and a single metric invites a ratio between a numerator and a denominator that
// do not describe the same population -- an error that does not announce itself
// in the metric name or in its HELP.
type DispatchRotationCounts struct {
	// Rotations.
	Completed uint64
	Truncated uint64
	// Object turns within a walk. Offered is every object the walk reached and
	// is the denominator for the rest; the others are outcomes of some of those
	// turns and do not add up to it, because an object can also be passed over
	// by the due index without being either queued or turned away.
	Offered           uint64
	Queued            uint64
	DeferredQueueFull uint64
	DeferredNotBetter uint64
}

type dispatchRotationCollector struct {
	mu        sync.Mutex
	source    func() *DispatchRotationCounts
	rotations *prometheus.Desc
	turns     *prometheus.Desc
}

func newDispatchRotationCollector() *dispatchRotationCollector {
	return &dispatchRotationCollector{
		rotations: prometheus.NewDesc(
			prometheus.BuildFQName(metricNamespace, metricSubsystem, "dispatch_rotation_total"),
			"Walks over the objects this replica owns, by whether the walk reached all of them. "+
				"One rotation is one full pass. truncated rising while completed stays flat is a "+
				"deployment that has stopped covering its objects, which no per-object signal reports: "+
				"an object the walk never reaches produces no failure anywhere, it simply does not run. "+
				"Counts rotations, not objects; do not divide it by dispatch_walk_total.",
			[]string{"result"}, nil,
		),
		turns: prometheus.NewDesc(
			prometheus.BuildFQName(metricNamespace, metricSubsystem, "dispatch_walk_total"),
			"Turns the walk gave to an object, by what came of the turn. offered counts every object "+
				"the walk reached and is the denominator for the others, which do not sum to it because "+
				"an object can also be passed over as not due. deferred_queue_full is the ready queue "+
				"having no place, and the walk stops there, so every object behind that one goes "+
				"unoffered on the same pass -- more room changes it. deferred_not_better is the recovery "+
				"queue being full of objects all due sooner than this one, which is an ordering and not "+
				"a lack of room -- more room changes nothing. Summing the two hides the only difference "+
				"that decides whether there is anything to do. "+
				"Both deferred results are expected to read zero, and zero is the success case here "+
				"rather than a counter nobody wired: the ready queue is sized above what a walk places "+
				"in it, and the recovery queue's capacity is at least the number of objects this Worker "+
				"owns while holding at most one entry per object, so it can only reach its bound during "+
				"the moment after the owned set shrinks and before the stale entries are dropped. A "+
				"non-zero value means the owned set just changed, or one of those two sizings was "+
				"altered -- not that something finally started being counted.",
			[]string{"result"}, nil,
		),
	}
}

// SetDispatchRotationSource binds the collector to the dispatcher's published
// facts. Safe before or after registration, and a nil recorder is a no-op, so
// wiring never has to be ordered against metric construction.
func (r *Recorder) SetDispatchRotationSource(source func() *DispatchRotationCounts) {
	if r == nil || r.phaseTwo.dispatchRotation == nil {
		return
	}
	r.phaseTwo.dispatchRotation.mu.Lock()
	r.phaseTwo.dispatchRotation.source = source
	r.phaseTwo.dispatchRotation.mu.Unlock()
}

func (c *dispatchRotationCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.rotations
	ch <- c.turns
}

func (c *dispatchRotationCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	if source == nil {
		return
	}
	// Nothing is published before a walk has run. A set of zeros would say the
	// walk ran and turned nobody away, which is the reassuring reading of a
	// replica that has not started walking yet.
	counts := source()
	if counts == nil {
		return
	}
	for result, value := range map[string]uint64{
		"completed": counts.Completed, "truncated": counts.Truncated,
	} {
		ch <- prometheus.MustNewConstMetric(c.rotations, prometheus.CounterValue, float64(value), result)
	}
	for result, value := range map[string]uint64{
		"offered": counts.Offered, "queued": counts.Queued,
		"deferred_queue_full": counts.DeferredQueueFull, "deferred_not_better": counts.DeferredNotBetter,
	} {
		ch <- prometheus.MustNewConstMetric(c.turns, prometheus.CounterValue, float64(value), result)
	}
}
