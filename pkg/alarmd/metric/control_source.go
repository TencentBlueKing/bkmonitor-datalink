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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// ControlSourceStats is what the process says about its control source
// refresh when asked, at scrape time. It is a state, read each time: the
// transition counter that existed before answered "did it change" and a
// deployment that was degraded for hours read on it exactly like one that
// never was, because the one change had happened once and scrolled away.
type ControlSourceStats struct {
	// Known is false until the process has completed its first control
	// round, before which it has no role or mode to report; the collector
	// then emits nothing, which reads as "not yet" rather than as any state.
	Known bool
	Role  observability.ControlSourceRole
	Mode  observability.ControlSourceMode
	// LastSuccessAt is the persisted time of the last refresh round that
	// succeeded under this store, by any process. Zero means no round is
	// known to have; the age is then not emitted rather than made up.
	LastSuccessAt time.Time
	// Leading is whether this process runs the refresh, and
	// PendingConfirmationAgeSeconds how long that refresh has been answering
	// PENDING_CONFIRMATION, zero when nothing is pending. Emitted by the
	// leader only.
	Leading                       bool
	PendingConfirmationAgeSeconds float64
}

// controlSourceCollector reads the process's control source state at scrape
// time and emits it as gauges. Nothing here is set on a path: a gauge set
// only when something happens stops moving when things stop happening,
// which is the moment it is read.
type controlSourceCollector struct {
	mu      sync.Mutex
	source  func() ControlSourceStats
	now     func() time.Time
	mode    *prometheus.Desc
	age     *prometheus.Desc
	pending *prometheus.Desc
}

func newControlSourceCollector() *controlSourceCollector {
	descriptor := func(name, help string, labels ...string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, name), help, labels, nil)
	}
	return &controlSourceCollector{
		now: time.Now,
		mode: descriptor("control_source_mode",
			"Which role this process has in refreshing the control plane's strategy source and which state "+
				"that refresh is in, 1 on the current pair and 0 on the others; read each scrape from the "+
				"process's state, not set on a transition. role: leader (this process runs the refresh), "+
				"follower (another process holds the leader lease; this one reads what it publishes), "+
				"unacquired (the lease could not be acquired and it is not known whether anyone holds it -- "+
				"a store error, or no attempt yet). No replica reporting leader means nobody is refreshing. "+
				"mode: healthy (the last round succeeded), degraded_last_good (the last round failed and the "+
				"process serves the last good catalog, which some process once refreshed successfully), "+
				"never_succeeded (the last round failed and no round is known to have ever succeeded under "+
				"this store). On a follower the mode is that of its own control reads. A follower's zero on "+
				"every refresh counter is normal: only the leader refreshes.", "role", "mode"),
		age: descriptor("control_source_last_success_age_seconds",
			"Seconds since the last refresh round that succeeded, under any status including unchanged, "+
				"by any process under this store: the time is persisted without expiry, so the reading "+
				"survives restarts and leader changes and a process that starts against a source that has "+
				"been failing for a day reads a day. Absent until any round has succeeded; a zero would "+
				"read as just now. Past "+controlplane.SourceStalenessBound.String()+", the staleness the "+
				"design accepts for the catalog, fleet health degrades. Reported by every replica from the "+
				"same persisted fact, so a deployment with no leader still reports it rising."),
		pending: descriptor("source_pending_confirmation_age_seconds",
			"Seconds the leader's source refresh has been answering PENDING_CONFIRMATION with no PUBLISHED or "+
				"UNCHANGED since; 0 when nothing is pending. A candidate is published only when two whole "+
				"observations agree, and every pending round counts as a successful refresh, so "+
				"control_source_last_success_age_seconds stays young while a change waits to go live; this is "+
				"the reading that rises. Emitted by the leader only, from this process's rounds: a new leader "+
				"starts it again."),
	}
}

func (c *controlSourceCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.mode
	ch <- c.age
	ch <- c.pending
}

func (c *controlSourceCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source, now := c.source, c.now
	c.mu.Unlock()
	if source == nil {
		return
	}
	stats := source()
	if !stats.Known {
		return
	}
	for _, role := range observability.ControlSourceRoles {
		for _, mode := range observability.ControlSourceModes {
			value := 0.0
			if stats.Role == role && stats.Mode == mode {
				value = 1
			}
			ch <- prometheus.MustNewConstMetric(c.mode, prometheus.GaugeValue, value, string(role), string(mode))
		}
	}
	if !stats.LastSuccessAt.IsZero() {
		age := now().Sub(stats.LastSuccessAt).Seconds()
		if age < 0 {
			age = 0
		}
		ch <- prometheus.MustNewConstMetric(c.age, prometheus.GaugeValue, age)
	}
	if stats.Leading {
		ch <- prometheus.MustNewConstMetric(c.pending, prometheus.GaugeValue, stats.PendingConfirmationAgeSeconds)
	}
}

// SetControlSourceSource binds the process's control source state to the
// collector. Until it is bound the collector emits nothing.
func (r *Recorder) SetControlSourceSource(source func() ControlSourceStats) {
	if r == nil || r.phaseTwo.controlSource == nil {
		return
	}
	r.phaseTwo.controlSource.mu.Lock()
	r.phaseTwo.controlSource.source = source
	r.phaseTwo.controlSource.mu.Unlock()
}

// controlSourceRoundExit maps a round's exit onto the closed set the counter
// pre-creates. An exit the closed set does not know reads as other rather
// than as a new series: a new series would be a label value nobody
// pre-created, which is the absent-versus-zero ambiguity this counter exists
// to avoid.
func controlSourceRoundExit(facts *observability.ControlSourceRoundFacts) string {
	if facts.Outcome == observability.ControlSourceRoundSucceeded {
		return string(controlplane.SourceRefreshExitNone)
	}
	exit := controlplane.SourceRefreshExit(facts.Exit)
	if !controlplane.ValidSourceRefreshExit(exit) || exit == controlplane.SourceRefreshExitNone {
		return string(controlplane.SourceRefreshExitOther)
	}
	return string(exit)
}
