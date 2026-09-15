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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
)

// platformSettingsCollector reads the process copy of the platform's
// settings at scrape time. Everything about the copy is a state or a count
// the copy keeps; nothing is set on a path.
//
// The age of the last publication is emitted only once there has been one:
// a zero would read as "read just now" on a copy that never loaded, and a
// copy that never loaded is not a fault on its own -- the publisher may not
// be deployed, which the mode says.
type platformSettingsCollector struct {
	mu          sync.Mutex
	source      func() platformsettings.Stats
	now         func() time.Time
	mode        *prometheus.Desc
	age         *prometheus.Desc
	refreshes   *prometheus.Desc
	unavailable *prometheus.Desc
	changes     *prometheus.Desc
}

func newPlatformSettingsCollector() *platformSettingsCollector {
	descriptor := func(name, help string, labels ...string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, name), help, labels, nil)
	}
	return &platformSettingsCollector{
		now: time.Now,
		mode: descriptor("platform_settings_mode",
			"Which state the process copy of the platform's settings (host_disable_monitor_states, "+
				"is_access_bk_data, bkdata_cmdb_level_tables, file_system_type_ignore) is in, 1 on the current one "+
				"and 0 on the others; read each scrape from the copy's state. not_configured: the deployment "+
				"renders no distribution source, the copy answers from its own configuration and the platform's "+
				"code defaults, which is what alarmd did before it could read the platform, and is not a fault. "+
				"never_loaded: a source is configured and no read has found a publication since the process "+
				"started. authoritative: the last read found a publication and every field decoded. stale: there "+
				"was a publication once and the last read did not yield one; the copy answers from the last "+
				"publication, and past the staleness bound fleet health degrades.", "mode"),
		age: descriptor("platform_settings_authoritative_age_seconds",
			"Seconds since the last publication of the platform's settings was read. Absent until there has "+
				"been one. Past "+platformsettings.DefaultStalenessBound.String()+" without one the copy is stale "+
				"beyond its bound and fleet health degrades."),
		refreshes: descriptor("platform_settings_refresh_total",
			"Reads of the distribution by result: authoritative (a publication, every field decoded) or "+
				"unavailable. One per minute; a flat line is the refresh loop not running.", "result"),
		unavailable: descriptor("platform_settings_unavailable_total",
			"Reads that yielded no publication, by why: read_error (the read did not happen), unpublished (the "+
				"revision key is absent -- nothing was ever published, and the field keys beside it are not read "+
				"as no override), decode_error (a field is not of its declared type; the whole publication is "+
				"refused rather than half of it, and the log line names the field and the value).", "reason"),
		changes: descriptor("platform_settings_change_total",
			"Reads on which the effective value of a field changed, by field: what one edit on the platform's "+
				"page produces exactly once on every replica. The effective value is the publication resolved "+
				"through the deployment's own layer and the code defaults, so a publication that repeats what "+
				"was already in effect counts nothing.", "field"),
	}
}

func (c *platformSettingsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.mode
	ch <- c.age
	ch <- c.refreshes
	ch <- c.unavailable
	ch <- c.changes
}

func (c *platformSettingsCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source, now := c.source, c.now
	c.mu.Unlock()
	if source == nil {
		return
	}
	stats := source()
	for _, mode := range platformsettings.Modes {
		value := 0.0
		if stats.Mode == mode {
			value = 1
		}
		ch <- prometheus.MustNewConstMetric(c.mode, prometheus.GaugeValue, value, string(mode))
	}
	if !stats.LoadedAt.IsZero() {
		age := now().Sub(stats.LoadedAt).Seconds()
		if age < 0 {
			age = 0
		}
		ch <- prometheus.MustNewConstMetric(c.age, prometheus.GaugeValue, age)
	}
	for _, result := range []string{"authoritative", "unavailable"} {
		ch <- prometheus.MustNewConstMetric(c.refreshes, prometheus.CounterValue, float64(stats.Refreshes[result]), result)
	}
	for _, reason := range platformsettings.UnavailableReasons {
		ch <- prometheus.MustNewConstMetric(c.unavailable, prometheus.CounterValue, float64(stats.Unavailable[reason]), string(reason))
	}
	for _, field := range platformsettings.Fields {
		ch <- prometheus.MustNewConstMetric(c.changes, prometheus.CounterValue, float64(stats.Changes[field]), string(field))
	}
}

// SetPlatformSettingsSource binds the process copy to the collector. Until
// it is bound the collector emits nothing.
func (r *Recorder) SetPlatformSettingsSource(source func() platformsettings.Stats) {
	if r == nil || r.phaseTwo.platformSettings == nil {
		return
	}
	r.phaseTwo.platformSettings.mu.Lock()
	r.phaseTwo.platformSettings.source = source
	r.phaseTwo.platformSettings.mu.Unlock()
}
