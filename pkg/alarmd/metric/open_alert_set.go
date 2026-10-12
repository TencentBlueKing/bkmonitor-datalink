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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// openAlertSetCollector reads the process copy of the consumer's open alert
// set at scrape time. Everything about the copy is a state or a cumulative
// count the copy already keeps, so nothing here is incremented on a path;
// the collector asks and reports.
//
// The calibration age is emitted only once a calibration has completed.
// Before that the series does not exist: a zero would read as "calibrated
// just now" and a large number as "lost long ago", and a copy that has not
// calibrated yet is neither.
type openAlertSetCollector struct {
	mu          sync.Mutex
	source      func() openalerts.Stats
	now         func() time.Time
	age         *prometheus.Desc
	unavailable *prometheus.Desc
	refreshes   *prometheus.Desc
	lookups     *prometheus.Desc
	entries     *prometheus.Desc
	tracked     *prometheus.Desc
	evictions   *prometheus.Desc
	refused     *prometheus.Desc
}

func newOpenAlertSetCollector() *openAlertSetCollector {
	descriptor := func(name, help string, labels ...string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, name), help, labels, nil)
	}
	return &openAlertSetCollector{
		now: time.Now,
		age: descriptor("open_alert_set_authoritative_age_seconds",
			"Seconds since the oldest calibration among the tracked strategies' sets completed: a full read of "+
				"the consumer's open alerts for a strategy, reconciled against its index. Absent until one has."),
		unavailable: descriptor("open_alert_set_unavailable_total",
			"Why the copy could not answer from the consumer's sets: read_error (an index read or a calibration "+
				"failed, each time), too_large (a strategy's set larger than one read may hold - members, bytes or "+
				"pages - each time; such a set is not read another way yet, and the first one counted is the trigger "+
				"for reading it a member at a time), location_unconfirmed (the link's Console has not named where the sets are) and "+
				"keying_unconfirmed (it has not said they are keyed by the alert ids this process sends), each "+
				"counted once on entering the state; fleet health degrades with OPEN_ALERT_SET_UNCONFIRMED while "+
				"either lasts. A deployment without the Console counts nothing here.", "reason"),
		refreshes: descriptor("open_alert_set_refresh_total",
			"Refreshes by result: index (a strategy's index read), authoritative (a calibration completed), "+
				"unavailable (either failed). A flat line is the refresh loop not running.", "result"),
		lookups: descriptor("open_alert_set_lookup_total",
			"Lookups by how they were answered. index_member and index_absent are the consumer's index; "+
				"recently_sent is a fingerprint the index does not carry yet that this process sent ABNORMAL for "+
				"within its lag, the copy's word and not the consumer's; self_maintained and passed_through are "+
				"the unavailable policy answering, and which of the two appears is the policy in force. index_stale "+
				"is a set read once whose last successful read or calibration is older than 3 x (the index interval "+
				"+ ceil(tracked strategies / read batch) x the refresh interval), answered by the same policy as a "+
				"set never read; the bound grows with the strategies tracked, so it differs between deployments.", "answer"),
		entries: descriptor("open_alert_set_entries",
			"What the copy holds: member is fingerprints from the last publication, sent_open those this "+
				"process sent ABNORMAL for within the local retention.", "kind"),
		tracked: descriptor("open_alert_set_tracked_strategies",
			"Strategies the copy reads on each refresh: those evaluated by this worker within the tracking "+
				"window. Read against worker_owned_query_groups; well above it is strategies this worker lost "+
				"still inside the window."),
		evictions: descriptor("open_alert_set_evictions_total",
			"Fingerprints this process sent that were dropped from the copy to stay inside its bound, oldest "+
				"first."),
		refused: descriptor("open_alert_set_notices_refused_total",
			"Change notices on the link's channel this process dropped, by why: oversized (past 64 KiB), "+
				"undecodable (not a notice under the strict shape: a field this build does not know, a wrong type, "+
				"content after it), invalid_key (the tenant or strategy it names is not a valid one). A dropped "+
				"notice costs delay only: the strategy is read on the next periodic read. A steady rate is the "+
				"link publishing a notice this build does not read. Read with alarmd-cli invoke metrics.get.", "reason"),
	}
}

// SetOpenAlertSetSource binds the collector to the copy. Safe before or
// after registration; a nil recorder is a no-op.
func (r *Recorder) SetOpenAlertSetSource(source func() openalerts.Stats) {
	if r == nil || r.phaseTwo.openAlertSet == nil {
		return
	}
	r.phaseTwo.openAlertSet.mu.Lock()
	r.phaseTwo.openAlertSet.source = source
	r.phaseTwo.openAlertSet.mu.Unlock()
}

func (c *openAlertSetCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.age
	ch <- c.unavailable
	ch <- c.refreshes
	ch <- c.lookups
	ch <- c.entries
	ch <- c.tracked
	ch <- c.evictions
	ch <- c.refused
}

func (c *openAlertSetCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source, now := c.source, c.now
	c.mu.Unlock()
	if source == nil {
		return
	}
	stats := source()
	if !stats.LoadedAt.IsZero() {
		ch <- prometheus.MustNewConstMetric(c.age, prometheus.GaugeValue, now().Sub(stats.LoadedAt).Seconds())
	}
	for _, reason := range openalerts.UnavailableReasons {
		ch <- prometheus.MustNewConstMetric(c.unavailable, prometheus.CounterValue, float64(stats.Unavailable[reason]), string(reason))
	}
	for _, result := range []string{"authoritative", "unavailable", "index"} {
		ch <- prometheus.MustNewConstMetric(c.refreshes, prometheus.CounterValue, float64(stats.Refreshes[result]), result)
	}
	for _, answer := range openalerts.Answers {
		ch <- prometheus.MustNewConstMetric(c.lookups, prometheus.CounterValue, float64(stats.Lookups[answer]), string(answer))
	}
	ch <- prometheus.MustNewConstMetric(c.entries, prometheus.GaugeValue, float64(stats.Members), "member")
	ch <- prometheus.MustNewConstMetric(c.entries, prometheus.GaugeValue, float64(stats.Added), "sent_open")
	ch <- prometheus.MustNewConstMetric(c.tracked, prometheus.GaugeValue, float64(stats.Tracked))
	ch <- prometheus.MustNewConstMetric(c.evictions, prometheus.CounterValue, float64(stats.Evictions))
	for _, reason := range openalerts.NoticeRefusals {
		ch <- prometheus.MustNewConstMetric(c.refused, prometheus.CounterValue, float64(stats.NoticesRefused[reason]), string(reason))
	}
}
