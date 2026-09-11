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
	"errors"

	"github.com/prometheus/client_golang/prometheus"
)

// FleetCount is one bounded classification and how many objects carry it.
type FleetCount struct {
	Value string
	Count int
	// OldestAgeSeconds is how long the longest-running member of this
	// classification has been in it. Zero when the classification carries no
	// age, which is not the same as "one second old".
	OldestAgeSeconds float64
}

// FleetVerdict is the deployment-wide judgment, shaped for export.
//
// The page and the alert rule must not disagree about whether anything is
// wrong, and the only way to guarantee that is for both to read the same
// judgment. So the judgment is computed once, inside alarmd, and exported;
// the host writes rules against it rather than reimplementing the arithmetic
// and drifting from the page one release later.
type FleetVerdict struct {
	Health string
	// Expected is nil when the denominator could not be read. It is left out
	// of the export rather than sent as zero: zero expected objects is a real
	// state that means something else entirely.
	Expected   *int
	Covered    int
	Determined int
	Unknown    int
	// Stalled counts objects whose rounds stopped finishing altogether. It is
	// the one number here that never resolves on its own.
	Stalled   int
	Anomalies []FleetCount
	Gaps      []FleetCount
	// Failures says what broke, which the anomaly count cannot. A hundred
	// objects sharing one completion kind is one number; whether they share one
	// broken dependency or scattered unrelated causes is the question that
	// decides who gets called.
	Failures []FleetCount
}

// FleetVerdictSource returns the current judgment. Reading it costs one control
// plane read per scrape, which is the same read the object API already does.
type FleetVerdictSource func() FleetVerdict

type fleetCollector struct {
	source FleetVerdictSource

	health     *prometheus.Desc
	objects    *prometheus.Desc
	anomalies  *prometheus.Desc
	anomalyAge *prometheus.Desc
	stalled    *prometheus.Desc
	gaps       *prometheus.Desc
	failures   *prometheus.Desc
}

func newFleetCollector(source FleetVerdictSource) *fleetCollector {
	descriptor := func(name, help string, labels []string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, name), help, labels, nil)
	}
	return &fleetCollector{
		source: source,
		health: descriptor("fleet_health",
			"Deployment-wide judgment as alarmd itself decides it; alert on this rather than recomputing it.",
			[]string{"health_state"}),
		objects: descriptor("fleet_objects",
			"Objects by coverage state. Covered minus determined is counted into unknown. "+
				"A non-zero unknown does NOT mean something is broken -- a replica that just restarted owns "+
				"objects it cannot yet speak for -- it means the question cannot be answered, which is why "+
				"unknown is never folded into healthy. The expected state is absent when the denominator "+
				"could not be read; it is never reported as zero.",
			[]string{"state"}),
		anomalies: descriptor("fleet_anomalies",
			"Objects currently anomalous, by closed kind; unknown kinds are counted as OTHER. "+
				"This counts objects, not rounds: one object failing every round for an hour stays 1. "+
				"It does NOT say anything is unrecoverable -- see fleet_stalled_objects for that.",
			[]string{"kind"}),
		anomalyAge: descriptor("fleet_anomaly_oldest_age_seconds",
			"How long the longest-running anomaly of each kind has lasted, from its own start point.",
			[]string{"kind"}),
		stalled: descriptor("fleet_stalled_objects",
			"Objects whose rounds stopped finishing for longer than the deployment's own budget for "+
				"terminating a Slot that cannot complete, counted from the first round of the current "+
				"unbroken sequence that reached execution and did not finish. Any round that ends, even "+
				"degraded, or that is blocked before execution, ends the sequence and drops the object "+
				"from this count; how long the object has been anomalous overall does not enter into "+
				"it. Unlike the other counts here this one does not resolve on its own. It is derived from "+
				"how long the failing sequence has been observed, so it under-reports after a restart "+
				"rather than over-reporting: zero is weaker evidence than non-zero.",
			nil),
		gaps: descriptor("fleet_gaps",
			"Reasons the view is incomplete, by closed kind; unknown kinds are counted as OTHER. "+
				"Any of these means the judgment is UNKNOWN rather than green. A gap is not itself a "+
				"failure of the pipeline: it says this answer cannot be trusted, not that objects are broken.",
			[]string{"kind"}),
		failures: descriptor("fleet_failures",
			"Anomalous objects by what broke, using the classification the pipeline already publishes; "+
				"unknown categories are counted as other. Objects whose last round failed before reaching a "+
				"classification are absent here, so this total can be lower than fleet_anomalies -- the "+
				"difference is objects nobody can yet say anything about, not objects that are fine.",
			[]string{"category"}),
	}
}

func (c *fleetCollector) Describe(descriptions chan<- *prometheus.Desc) {
	descriptions <- c.health
	descriptions <- c.objects
	descriptions <- c.anomalies
	descriptions <- c.anomalyAge
	descriptions <- c.stalled
	descriptions <- c.gaps
	descriptions <- c.failures
}

func (c *fleetCollector) Collect(metrics chan<- prometheus.Metric) {
	verdict := c.source()
	if verdict.Health == "" {
		// A scrape that could not reach a judgment must not publish one. Emitting
		// a default here would export "healthy" for a deployment nobody asked.
		return
	}
	metrics <- prometheus.MustNewConstMetric(c.health, prometheus.GaugeValue, 1, verdict.Health)
	if verdict.Expected != nil {
		metrics <- prometheus.MustNewConstMetric(c.objects, prometheus.GaugeValue, float64(*verdict.Expected), "expected")
	}
	metrics <- prometheus.MustNewConstMetric(c.objects, prometheus.GaugeValue, float64(verdict.Covered), "covered")
	metrics <- prometheus.MustNewConstMetric(c.objects, prometheus.GaugeValue, float64(verdict.Determined), "determined")
	metrics <- prometheus.MustNewConstMetric(c.objects, prometheus.GaugeValue, float64(verdict.Unknown), "unknown")
	metrics <- prometheus.MustNewConstMetric(c.stalled, prometheus.GaugeValue, float64(verdict.Stalled))
	for _, count := range verdict.Anomalies {
		if count.Value == "" {
			continue
		}
		metrics <- prometheus.MustNewConstMetric(c.anomalies, prometheus.GaugeValue, float64(count.Count), count.Value)
		if count.OldestAgeSeconds > 0 {
			metrics <- prometheus.MustNewConstMetric(c.anomalyAge, prometheus.GaugeValue, count.OldestAgeSeconds, count.Value)
		}
	}
	for _, count := range verdict.Gaps {
		if count.Value == "" {
			continue
		}
		metrics <- prometheus.MustNewConstMetric(c.gaps, prometheus.GaugeValue, float64(count.Count), count.Value)
	}
	for _, count := range verdict.Failures {
		if count.Value == "" {
			continue
		}
		metrics <- prometheus.MustNewConstMetric(c.failures, prometheus.GaugeValue, float64(count.Count), count.Value)
	}
}

// BindFleet registers the judgment collector. It is bound once, like the health
// collector, because two sources would mean two answers to the same question.
func (r *Recorder) BindFleet(source FleetVerdictSource) error {
	if r == nil || r.registry == nil {
		return errors.New("metric: initialized recorder is required")
	}
	if source == nil {
		return errors.New("metric: fleet verdict source is required")
	}
	r.fleetMu.Lock()
	defer r.fleetMu.Unlock()
	if r.fleetBound {
		return errors.New("metric: fleet verdict source is already bound")
	}
	if err := r.registry.Register(newFleetCollector(source)); err != nil {
		return err
	}
	r.fleetBound = true
	return nil
}
