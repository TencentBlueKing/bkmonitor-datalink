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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/activecopy"
)

// activeCopyCollector reads alarmd's own copy of the link's active alerts
// (activecopy) at scrape time: every cell is a count or a size the copy
// keeps, pre-created so a zero reads as "never happened".
type activeCopyCollector struct {
	mu          sync.Mutex
	source      func() activecopy.Stats
	lookups     *prometheus.Desc
	passes      *prometheus.Desc
	entries     *prometheus.Desc
	unsupported *prometheus.Desc
}

func newActiveCopyCollector() *activeCopyCollector {
	descriptor := func(name, help string, labels ...string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName("", metricSubsystem, name), help, labels, nil)
	}
	return &activeCopyCollector{
		lookups: descriptor("active_copy_lookup_total",
			"The recovery gate's and the no-data end's lookups by how the copy answered: own_sent (this process's "+
				"acknowledged ABNORMAL), search_hit (an active alert the link's search found within 5 minutes), "+
				"search_miss (two misses at least 2 minutes apart, within an hour), young_miss (one run of misses "+
				"shorter than that: held, not judged), unknown (no current word: queued for the next pass, held "+
				"meanwhile), full (unknown and not queued, the copy at its bound), link_unsupported (unknown while the "+
				"link answers it cannot search: queued for the retry, held meanwhile).", "answer"),
		passes: descriptor("active_copy_pass_total",
			"The refresh passes' requests and results: request (a search of at most 500 fingerprints, tenant + "+
				"event source + status active, across strategies), link_busy (429: the pass stopped, the "+
				"fingerprints stay queued and held), link_unsupported (501/404/405: the pass stopped, asked again after 1 "+
				"minute doubling to 10 while it lasts; warned once an episode), fingerprint_refused (a queued "+
				"fingerprint that is not native, taken out of its batch unasked; per fingerprint), link_refused (401/403: the credential was refused; the pass "+
				"stopped, the fingerprints stay queued and held; warned once an episode), read_failed (any other failure: held), hit and miss (fingerprints answered), foreign_item "+
				"(an item that is not an active alert of a queued fingerprint, this source and its strategy), "+
				"dropped (forgotten when a strategy came back after 10 minutes untracked).", "outcome"),
		entries: descriptor("active_copy_entries",
			"What the copy holds: known (fingerprints with a word) and queued (waiting for a pass).", "kind"),
		unsupported: descriptor("active_copy_link_unsupported",
			"1 while the link answers that it cannot search (501, 404 or 405), until a search is answered again."),
	}
}

// SetActiveCopySource binds the collector to the copy. Safe before or
// after registration; a nil recorder is a no-op.
func (r *Recorder) SetActiveCopySource(source func() activecopy.Stats) {
	if r == nil || r.phaseTwo.activeCopy == nil {
		return
	}
	r.phaseTwo.activeCopy.mu.Lock()
	r.phaseTwo.activeCopy.source = source
	r.phaseTwo.activeCopy.mu.Unlock()
}

func (c *activeCopyCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.lookups
	ch <- c.passes
	ch <- c.entries
	ch <- c.unsupported
}

func (c *activeCopyCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	if source == nil {
		return
	}
	stats := source()
	for _, answer := range activecopy.Answers {
		ch <- prometheus.MustNewConstMetric(c.lookups, prometheus.CounterValue, float64(stats.Answers[answer]), string(answer))
	}
	for _, outcome := range activecopy.Outcomes {
		ch <- prometheus.MustNewConstMetric(c.passes, prometheus.CounterValue, float64(stats.Outcomes[outcome]), outcome)
	}
	ch <- prometheus.MustNewConstMetric(c.entries, prometheus.GaugeValue, float64(stats.Known), "known")
	ch <- prometheus.MustNewConstMetric(c.entries, prometheus.GaugeValue, float64(stats.Queued), "queued")
	unsupported := 0.0
	if stats.Unsupported {
		unsupported = 1
	}
	ch <- prometheus.MustNewConstMetric(c.unsupported, prometheus.GaugeValue, unsupported)
}
