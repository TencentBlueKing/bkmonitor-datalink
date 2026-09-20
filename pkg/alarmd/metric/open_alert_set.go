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
// The age of the last authoritative publication is emitted only once there
// has been one. Before that the series does not exist: a zero would read as
// "loaded just now" and a large number as "lost long ago", and a copy that
// never loaded is neither -- the publisher may not be deployed, which the
// mode gauge says on its own.
type openAlertSetCollector struct {
	mu          sync.Mutex
	source      func() openalerts.Stats
	now         func() time.Time
	mode        *prometheus.Desc
	age         *prometheus.Desc
	unavailable *prometheus.Desc
	refreshes   *prometheus.Desc
	lookups     *prometheus.Desc
	entries     *prometheus.Desc
	tracked     *prometheus.Desc
	evictions   *prometheus.Desc
}

func newOpenAlertSetCollector() *openAlertSetCollector {
	descriptor := func(name, help string, labels ...string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, name), help, labels, nil)
	}
	return &openAlertSetCollector{
		now: time.Now,
		mode: descriptor("open_alert_set_mode",
			"Which state the process copy of the consumer's open alert set is in, 1 on the current one and 0 on "+
				"the others. authoritative: the consumer's publication was read and is fresh. self_maintained: it "+
				"was read once and the latest read found it missing, stale, unreadable, under another fingerprint "+
				"algorithm, or failed; the copy answers from the last publication plus what this process sent. "+
				"never_loaded: nothing read since the process started, which is the state before the publisher is "+
				"deployed and is not a fault on its own. Moving in and out of self_maintained without anyone noticing "+
				"is the failure this gauge exists for.", "mode"),
		age: descriptor("open_alert_set_authoritative_age_seconds",
			"Seconds since the last authoritative publication was read. Absent until there has been one. Past "+
				"open_alert_set_staleness_cycles times the publisher's cycle the copy is stale and fleet health "+
				"degrades; that bound is also how long a recovery this process sent but the consumer never "+
				"received stays held before the publication corrects the copy."),
		unavailable: descriptor("open_alert_set_unavailable_total",
			"Refreshes that did not yield an authoritative publication, by why: read_error (the read failed), "+
				"heartbeat_missing (no heartbeat key), heartbeat_unreadable (a heartbeat field missing or malformed), "+
				"heartbeat_stale (older than the staleness bound), fingerprint_version (the publisher computes "+
				"fingerprints under another algorithm; every lookup would miss, so it is not read as empty).", "reason"),
		refreshes: descriptor("open_alert_set_refresh_total",
			"Refreshes by result: authoritative or unavailable. One per publisher cycle; a flat line is the "+
				"refresh loop not running.", "result"),
		lookups: descriptor("open_alert_set_lookup_total",
			"Lookups by how they were answered. authoritative_member and authoritative_absent are the "+
				"publication's word; recently_sent is a fingerprint this process sent ABNORMAL for inside the "+
				"publisher's lag; not_yet_loaded is a strategy first asked about after the last read; "+
				"self_maintained and passed_through are the unavailable policy answering, and which of the two "+
				"appears is the policy in force.", "answer"),
		entries: descriptor("open_alert_set_entries",
			"What the copy holds: member is fingerprints from the last publication, sent_open those this "+
				"process sent ABNORMAL for and has not sent RECOVERY for since, sent_closed the reverse.", "kind"),
		tracked: descriptor("open_alert_set_tracked_strategies",
			"Strategies the copy reads on each refresh: those evaluated by this worker within the tracking "+
				"window. Read against worker_owned_query_groups; well above it is strategies this worker lost "+
				"still inside the window."),
		evictions: descriptor("open_alert_set_evictions_total",
			"Fingerprints this process sent that were dropped from the copy to stay inside its bound, oldest "+
				"first. In self_maintained mode each one is an alert whose recovery now waits for the publication."),
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
	ch <- c.mode
	ch <- c.age
	ch <- c.unavailable
	ch <- c.refreshes
	ch <- c.lookups
	ch <- c.entries
	ch <- c.tracked
	ch <- c.evictions
}

func (c *openAlertSetCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source, now := c.source, c.now
	c.mu.Unlock()
	if source == nil {
		return
	}
	stats := source()
	for _, mode := range openalerts.Modes {
		value := 0.0
		if stats.Mode == mode {
			value = 1
		}
		ch <- prometheus.MustNewConstMetric(c.mode, prometheus.GaugeValue, value, string(mode))
	}
	if !stats.LoadedAt.IsZero() {
		ch <- prometheus.MustNewConstMetric(c.age, prometheus.GaugeValue, now().Sub(stats.LoadedAt).Seconds())
	}
	for _, reason := range openalerts.UnavailableReasons {
		ch <- prometheus.MustNewConstMetric(c.unavailable, prometheus.CounterValue, float64(stats.Unavailable[reason]), string(reason))
	}
	for _, result := range []string{"authoritative", "unavailable"} {
		ch <- prometheus.MustNewConstMetric(c.refreshes, prometheus.CounterValue, float64(stats.Refreshes[result]), result)
	}
	for _, answer := range openalerts.Answers {
		ch <- prometheus.MustNewConstMetric(c.lookups, prometheus.CounterValue, float64(stats.Lookups[answer]), string(answer))
	}
	ch <- prometheus.MustNewConstMetric(c.entries, prometheus.GaugeValue, float64(stats.Members), "member")
	ch <- prometheus.MustNewConstMetric(c.entries, prometheus.GaugeValue, float64(stats.Added), "sent_open")
	ch <- prometheus.MustNewConstMetric(c.entries, prometheus.GaugeValue, float64(stats.Removed), "sent_closed")
	ch <- prometheus.MustNewConstMetric(c.tracked, prometheus.GaugeValue, float64(stats.Tracked))
	ch <- prometheus.MustNewConstMetric(c.evictions, prometheus.CounterValue, float64(stats.Evictions))
}
