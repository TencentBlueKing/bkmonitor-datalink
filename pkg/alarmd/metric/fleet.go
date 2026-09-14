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
	// QueryCooldown counts visible objects retaining a query cooldown policy; nil means not measured.
	QueryCooldown *int
	// Expected is nil when the denominator could not be read. It is left out
	// of the export rather than sent as zero: zero expected objects is a real
	// state that means something else entirely.
	Expected   *int
	Covered    int
	Determined int
	Unknown    int
	// Healthy, Anomalous, Demoted, Undecidable and ByDesign are the columns
	// that partition Determined, exported so the split can be checked from the
	// outside instead of only read off a page.
	//
	// The reason they are here is an attribution that had to be retracted: a
	// release that moved objects out of the anomaly column was credited with a
	// drop of twenty-two, and the next release, which touched none of that
	// code, showed a similar drop at the same age. The anomaly count alone
	// drifts with the deployment, so a before-and-after on it cannot attribute
	// anything.
	//
	// With the columns beside it the claim becomes an identity checkable in
	// one window on one pod: anomalies down N and the receiving column up N.
	// Drift moves both sides and leaves the difference alone.
	Healthy     int
	Anomalous   int
	Demoted     int
	Undecidable int
	ByDesign    int
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
	// Workers counts the replicas the view could count by whether they have
	// applied the Activation the control plane published: acked, lagging or
	// unknown. Their sum is the counted replicas.
	Workers []FleetCount
}

// FleetVerdictSource returns the current judgment. Reading it costs one control
// plane read per scrape, which is the same read the object API already does.
type FleetVerdictSource func() FleetVerdict

type fleetCollector struct {
	source        FleetVerdictSource
	queryCooldown *prometheus.Desc

	health     *prometheus.Desc
	objects    *prometheus.Desc
	partition  *prometheus.Desc
	anomalies  *prometheus.Desc
	anomalyAge *prometheus.Desc
	stalled    *prometheus.Desc
	gaps       *prometheus.Desc
	failures   *prometheus.Desc
	workers    *prometheus.Desc
}

func newFleetCollector(source FleetVerdictSource) *fleetCollector {
	descriptor := func(name, help string, labels []string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, name), help, labels, nil)
	}
	return &fleetCollector{
		source:        source,
		queryCooldown: descriptor("fleet_query_cooldown_objects", "Visible objects isolated by external source_backend query cooldown, including expired permits awaiting a real query; demoted objects are counted, since holding a cooldown is what demotes an object. Lower bound when the anomaly or demoted list is truncated or fleet coverage is incomplete.", nil),
		health: descriptor("fleet_health",
			"Deployment-wide judgment as alarmd itself decides it; alert on this rather than recomputing it.",
			[]string{"health_state"}),
		partition: descriptor("fleet_partition_objects",
			"Objects by which column of the split they are in. Unlike fleet_objects these DO add up: "+
				"healthy, anomalous, demoted, undecidable, by_design and unknown partition the "+
				"deployment, every object is in exactly one, and their sum is covered. That is what "+
				"the family is for -- a change that moves objects between columns shows as one "+
				"falling and another rising by the same amount in the same scrape, and drift moves "+
				"both sides and leaves the difference alone. A before-and-after on the anomaly count "+
				"alone attributes nothing, which is why this exists. "+
				"It is a separate family from fleet_objects precisely because these add up and those "+
				"do not: summing a family where some members overlap and others partition gives a "+
				"number that looks plausible and means nothing. "+
				"While a rollout is in progress these under-count: a replica on an older build does "+
				"not publish the columns and its objects stay in anomalous. Compare within one pod "+
				"and one build, never across a rollout -- an attribution made across one had to be "+
				"retracted, which is the reason this family was added.",
			[]string{"column"}),
		objects: descriptor("fleet_objects",
			"Objects by coverage state. These overlap and MUST NOT be summed: determined is part of "+
				"covered, unknown is covered minus determined. For counts that partition the "+
				"deployment use fleet_partition_objects. "+
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
		workers: descriptor("fleet_workers",
			"Replicas the fleet view counted, by whether they have applied the Activation the control plane "+
				"published: acked, lagging or unknown. Unknown is a replica that reported no version or a "+
				"published version that could not be read; it is never folded into acked. The three sum to "+
				"the counted replicas. Written by every replica from the same shared facts, so aggregate with "+
				"max, not sum.",
			[]string{"state"}),
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
	descriptions <- c.workers
	descriptions <- c.queryCooldown
	descriptions <- c.health
	descriptions <- c.objects
	descriptions <- c.partition
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
	if verdict.QueryCooldown != nil {
		metrics <- prometheus.MustNewConstMetric(c.queryCooldown, prometheus.GaugeValue, float64(*verdict.QueryCooldown))
	}
	metrics <- prometheus.MustNewConstMetric(c.health, prometheus.GaugeValue, 1, verdict.Health)
	if verdict.Expected != nil {
		metrics <- prometheus.MustNewConstMetric(c.objects, prometheus.GaugeValue, float64(*verdict.Expected), "expected")
	}
	metrics <- prometheus.MustNewConstMetric(c.objects, prometheus.GaugeValue, float64(verdict.Covered), "covered")
	metrics <- prometheus.MustNewConstMetric(c.objects, prometheus.GaugeValue, float64(verdict.Determined), "determined")
	metrics <- prometheus.MustNewConstMetric(c.objects, prometheus.GaugeValue, float64(verdict.Unknown), "unknown")
	// The columns, in their own family. Emitted as zero when a column is empty,
	// which is a real measurement: this build always knows the answer. A build
	// that did not have these columns emitted no series at all, and that
	// absence is the distinction that matters to whoever reads a gap.
	//
	// Unknown is repeated here from fleet_objects rather than left out. It is
	// part of this partition -- objects nobody can speak for are still objects
	// -- and a family that adds up only after the reader remembers to fetch
	// one member from somewhere else does not add up.
	for column, count := range map[string]int{
		"healthy": verdict.Healthy, "anomalous": verdict.Anomalous, "demoted": verdict.Demoted,
		"undecidable": verdict.Undecidable, "by_design": verdict.ByDesign, "unknown": verdict.Unknown,
	} {
		metrics <- prometheus.MustNewConstMetric(c.partition, prometheus.GaugeValue, float64(count), column)
	}
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
	for _, count := range verdict.Workers {
		if count.Value == "" {
			continue
		}
		metrics <- prometheus.MustNewConstMetric(c.workers, prometheus.GaugeValue, float64(count.Count), count.Value)
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
