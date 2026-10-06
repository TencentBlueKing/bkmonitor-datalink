// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import "github.com/prometheus/client_golang/prometheus"

// controlFactsMetrics count what the control-plane reads of one replica did.
//
// A replica may no longer exit because a control read failed, so the reading
// that used to be "the process died" has to exist somewhere. It is here, and
// it is two pairs rather than two counters: each series whose healthy value is
// zero is published next to one that grows whenever the mechanism runs, because
// a lone zero cannot be told from a counter nothing writes to.
type controlFactsMetrics struct {
	read        *prometheus.CounterVec
	unavailable *prometheus.CounterVec
	rebuilt     *prometheus.CounterVec
	applied     *prometheus.CounterVec
	invalid     *prometheus.CounterVec
}

// controlFactNames is the closed set of control-plane facts a replica reads
// before it can run anything.
//
//	activation    which publication the fleet executes, and therefore which
//	              Query Groups exist. A replica that cannot read it does not
//	              know what to run and registers as starting, not ready.
//	snapshot      the published Catalog the activation names.
//	leader_lease  whether this replica refreshes the source this round. A
//	              failed read is not "not leader": it is not knowing, and the
//	              replica waits rather than assuming either answer.
var controlFactNames = []string{"activation", "snapshot", "leader_lease"}

// controlFactUnavailableReasons separate the two answers a store can give.
// They are different conditions and they lead to different work, so one label
// value for both would leave the page unable to say which happened.
//
//	missing      the store answered and the record is not there. Somebody has
//	             to write it back; on a lost activation that is the Control
//	             Leader, from the published Catalog.
//	read_failed  the store did not answer. Retrying is the whole of the work.
var controlFactUnavailableReasons = []string{"missing", "read_failed"}

// controlHealthStatuses are what one applied control round left behind.
// invalid is the program-defect case: a refresh returned a health fact this
// replica cannot act on, which used to end the process.
var controlHealthStatuses = []string{"healthy", "degraded_last_good", "invalid"}

// controlHealthInvalidFields name the field that was wrong, because "invalid"
// alone sends a reader to read all of them.
var controlHealthInvalidFields = []string{"status", "source_kind", "reason_code", "cause", "query_groups"}

func newControlFactsMetrics() controlFactsMetrics {
	metrics := controlFactsMetrics{
		read: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "control_facts_read_total",
			Help: "Control-plane facts this replica read successfully, by fact. It exists to be read " +
				"beside control_facts_unavailable_total: that one is zero on a healthy deployment, and " +
				"a zero there means nothing only while this one is growing.",
		}, []string{"fact"}),
		unavailable: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "control_facts_unavailable_total",
			Help: "Control-plane reads that came back without the fact, by fact and by which answer the " +
				"store gave. reason=missing is the record not being there, which somebody has to write " +
				"back; reason=read_failed is the store not answering, which retrying fixes. Neither ends " +
				"the process: a replica that cannot read the facts waits under them, so this rising and " +
				"then stopping is what a recovered outage looks like, and this rising without stopping " +
				"is a replica that never joined.",
		}, []string{"fact", "reason"}),
		rebuilt: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "control_facts_rebuilt_total",
			Help: "Control-plane facts the Control Leader wrote back because the store had none, by fact. " +
				"fact=activation is the activation established again from the published Catalog after " +
				"the record was found missing; one increment per lost record is what a recovered store " +
				"reload looks like, and none while control_facts_unavailable_total{reason=missing} keeps " +
				"rising is a Leader that cannot rebuild it.",
		}, []string{"fact"}),
		applied: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "control_health_facts_total",
			Help: "Control refresh results this replica applied, by the health fact each carried. " +
				"status=invalid is a defect in this program rather than a deployment condition: the " +
				"refresh returned something the replica cannot act on, and it keeps the previous fact " +
				"and carries on instead of exiting.",
		}, []string{"status"}),
		invalid: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "control_health_invalid_total",
			Help: "Invalid control health facts by the field that was wrong. Read beside " +
				"control_health_facts_total, which grows on every round; this one is zero on a correct " +
				"program, and naming the field is what makes it actionable rather than a thing to watch.",
		}, []string{"field"}),
	}
	for _, fact := range controlFactNames {
		metrics.read.WithLabelValues(fact)
		metrics.rebuilt.WithLabelValues(fact)
		for _, reason := range controlFactUnavailableReasons {
			metrics.unavailable.WithLabelValues(fact, reason)
		}
	}
	for _, status := range controlHealthStatuses {
		metrics.applied.WithLabelValues(status)
	}
	for _, field := range controlHealthInvalidFields {
		metrics.invalid.WithLabelValues(field)
	}
	return metrics
}

func (m controlFactsMetrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{m.read, m.unavailable, m.rebuilt, m.applied, m.invalid}
}

// RecordControlFactRebuilt counts one control-plane fact the Control Leader
// wrote back after finding the store had none.
func (r *Recorder) RecordControlFactRebuilt(fact string) {
	if r == nil || !knownLabel(controlFactNames, fact) {
		return
	}
	r.phaseTwo.controlFacts.rebuilt.WithLabelValues(fact).Inc()
}

// RecordControlFactRead counts one control-plane fact this replica read.
func (r *Recorder) RecordControlFactRead(fact string) {
	if r == nil || !knownLabel(controlFactNames, fact) {
		return
	}
	r.phaseTwo.controlFacts.read.WithLabelValues(fact).Inc()
}

// RecordControlFactUnavailable counts one control-plane read that came back
// without the fact. It is called where the read returns, not reconstructed
// from the degraded state afterwards: the state says a replica is degraded
// now, and this says how many rounds it took to get there and whether they
// stopped.
func (r *Recorder) RecordControlFactUnavailable(fact, reason string) {
	if r == nil || !knownLabel(controlFactNames, fact) || !knownLabel(controlFactUnavailableReasons, reason) {
		return
	}
	r.phaseTwo.controlFacts.unavailable.WithLabelValues(fact, reason).Inc()
}

// RecordControlHealthFact counts one applied control round by the health fact
// it carried.
func (r *Recorder) RecordControlHealthFact(status string) {
	if r == nil || !knownLabel(controlHealthStatuses, status) {
		return
	}
	r.phaseTwo.controlFacts.applied.WithLabelValues(status).Inc()
}

// RecordControlHealthInvalid names the field of an unusable health fact.
func (r *Recorder) RecordControlHealthInvalid(field string) {
	if r == nil || !knownLabel(controlHealthInvalidFields, field) {
		return
	}
	r.phaseTwo.controlFacts.invalid.WithLabelValues(field).Inc()
}

func knownLabel(known []string, value string) bool {
	for _, candidate := range known {
		if candidate == value {
			return true
		}
	}
	return false
}
