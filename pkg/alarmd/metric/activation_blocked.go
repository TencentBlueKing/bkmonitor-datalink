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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// ActivationBlockedReasons are the reasons a cutover holds one Query Group
// back rather than failing the publication: the preconditions only a write
// outside the cutover could break.
var ActivationBlockedReasons = []string{
	controlplane.CutoverReasonActivationRecordMissing, controlplane.CutoverReasonTimelineMissing,
	controlplane.CutoverReasonOpenSegmentClosed, controlplane.CutoverReasonOpenDigestMismatch,
	controlplane.CutoverReasonLegacyRevisionMismatch,
}

// activationBlockedCollector reads the Control Leader's last cutover at
// scrape time. Every label is emitted, zero included.
type activationBlockedCollector struct {
	mu         sync.Mutex
	source     func() controlplane.ActivationBlockedReading
	blocked    *prometheus.Desc
	accounting *prometheus.Desc
	reopened   *prometheus.Desc
	// bodyBytes is the activation body's stored length, read by every
	// replica with the header; see SetActivationBodyBytesSource.
	bodySource func() int64
	bodyBytes  *prometheus.Desc
}

func newActivationBlockedCollector() *activationBlockedCollector {
	return &activationBlockedCollector{
		blocked: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "activation_blocked_query_groups"),
			"Query Groups the Control Leader's last cutover held back, by reason. A held-back Query Group keeps the "+
				"records it had and runs what its open Segment names; the rest of the publication was activated. "+
				"It is judged again at every cutover; the repair subcommand fixes what a write outside the cutover "+
				"broke. Zero on a deployment nothing outside the control plane writes to.", []string{"reason"}, nil),
		accounting: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "activation_blocked_set_total"),
			"Cutovers by how the persisted set of held-back Query Groups compared with the activation body. lost: the "+
				"body counts entries the set does not hold (evicted, or lost in a failover). unaccounted: the set holds "+
				"entries the body does not count (written beside a leader that does not know it, or a rebuilt body). "+
				"unreadable: the set does not decode. Each of the three makes the cutover read every timeline.",
			[]string{"accounting"}, nil),
		reopened: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "activation_timeline_reopened_total"),
			"Timelines a cutover opened again because their key was gone. This used to fail every publication under "+
				"a word that said a dependency did not answer.", nil, nil),
		bodyBytes: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "activation_body_bytes"),
			"Stored length of the activation body at this process's last control read, zero before the first. Every "+
				"publication rewrites the body whole and every replica reads it whole after the header moves, so this "+
				"is what a publication costs each of them regardless of how little changed.", nil, nil),
	}
}

// SetActivationBodyBytesSource binds the body length gauge to the repository.
func (r *Recorder) SetActivationBodyBytesSource(source func() int64) {
	if r == nil || r.phaseTwo.activationBlocked == nil {
		return
	}
	r.phaseTwo.activationBlocked.mu.Lock()
	r.phaseTwo.activationBlocked.bodySource = source
	r.phaseTwo.activationBlocked.mu.Unlock()
}

// SetActivationBlockedSource binds the collector to the repository.
func (r *Recorder) SetActivationBlockedSource(source func() controlplane.ActivationBlockedReading) {
	if r == nil || r.phaseTwo.activationBlocked == nil {
		return
	}
	r.phaseTwo.activationBlocked.mu.Lock()
	r.phaseTwo.activationBlocked.source = source
	r.phaseTwo.activationBlocked.mu.Unlock()
}

func (c *activationBlockedCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.blocked
	ch <- c.accounting
	ch <- c.reopened
	ch <- c.bodyBytes
}

func (c *activationBlockedCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source, bodySource := c.source, c.bodySource
	c.mu.Unlock()
	var reading controlplane.ActivationBlockedReading
	if source != nil {
		reading = source()
	}
	for _, reason := range ActivationBlockedReasons {
		ch <- prometheus.MustNewConstMetric(c.blocked, prometheus.GaugeValue, float64(reading.ByReason[reason]), reason)
	}
	for _, accounting := range controlplane.BlockedSetAccountings {
		ch <- prometheus.MustNewConstMetric(c.accounting, prometheus.CounterValue, float64(reading.Accounting[accounting]), string(accounting))
	}
	ch <- prometheus.MustNewConstMetric(c.reopened, prometheus.CounterValue, float64(reading.Reopened))
	var bodyBytes int64
	if bodySource != nil {
		bodyBytes = bodySource()
	}
	ch <- prometheus.MustNewConstMetric(c.bodyBytes, prometheus.GaugeValue, float64(bodyBytes))
}
