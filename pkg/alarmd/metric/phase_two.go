// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

type phaseTwoMetrics struct {
	workflow                     workflowMetrics
	shortPeriod                  shortPeriodMetrics
	queryStatus                  queryStatusMetrics
	queryCooldown                *prometheus.CounterVec
	slotReadiness                slotReadinessMetrics
	slotTiming                   *prometheus.HistogramVec
	work                         *prometheus.CounterVec
	busy                         *prometheus.CounterVec
	lastProgress                 *prometheus.GaugeVec
	capacity                     *prometheus.CounterVec
	sourceObservations           *prometheus.CounterVec
	sourceRefreshes              *prometheus.CounterVec
	sourceCompiles               *prometheus.CounterVec
	activationFailures           *prometheus.CounterVec
	ownedQueryGroups             *prometheus.GaugeVec
	ownershipTransitions         *prometheus.CounterVec
	queryAdmission               *prometheus.CounterVec
	activeQGSetCount             prometheus.Gauge
	activeQGSetBytes             prometheus.Gauge
	activeQGSetEncode            *prometheus.HistogramVec
	activeQGSetRedis             *prometheus.HistogramVec
	scheduleCutoverPayload       prometheus.Gauge
	scheduleCutoverTimelineMax   prometheus.Gauge
	scheduleTimelineBytes        prometheus.Histogram
	scheduleSegmentsPruned       prometheus.Counter
	schedulePruneSkipped         *prometheus.CounterVec
	scheduleCutoverDuration      *prometheus.HistogramVec
	scheduleCutoverQueryGroups   *prometheus.CounterVec
	scheduleCutoverTimelinesRead prometheus.Gauge
	queryFailures                *prometheus.CounterVec
	objectCatalogObjects         *prometheus.CounterVec
	objectCatalogRedis           *prometheus.HistogramVec
	objectCatalogManifestBytes   prometheus.Gauge
	objectReads                  *prometheus.CounterVec
	stateGenerationSkew          *prometheus.CounterVec
	legacyMigration              *prometheus.CounterVec
	legacyMigrationScan          prometheus.Histogram
	legacyMigrationTime          *prometheus.HistogramVec
	undrainedDrainingQueryGroups prometheus.Gauge
	activationHeldQueryGroups    prometheus.Gauge
	activationHeldAgeSecondsMax  prometheus.Gauge
	algorithmEvaluations         *prometheus.CounterVec
	redisCalls                   redisCallMetrics
	controlCache                 *controlCacheCollector
	legacyPodCache               *prometheus.CounterVec
	redisPool                    *redisPoolCollector
	algorithmInputs              *prometheus.CounterVec
	seriesAdmission              *prometheus.CounterVec
	cmdbIndexHosts               prometheus.Gauge
	hostDisableMonitorStates     prometheus.Gauge
	unmappedSeverity             *prometheus.CounterVec
	cmdbIndexAge                 *prometheus.GaugeVec
	cmdbIndexDegraded            *prometheus.GaugeVec
	dueIndex                     dueIndexMetrics
}

var activeQGSetDurationBuckets = []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 30}

// Timeline sizes from one Segment (about a kilobyte) up past the sizes that
// made a publication cutover exceed the Redis write timeout.
var scheduleTimelineBytesBuckets = []float64{1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216}
var legacyMigrationScanBuckets = []float64{1, 10, 100, 500, 1000, 5000, 10000, 25000, 50000}

var phaseTwoBusyStages = []string{"query", "evaluation", "event", "state", "progress", "other"}
var phaseTwoWorkKinds = []string{
	"physical_query", "record", "plan_record", "level_result", "slot",
	"event_ack", "state_load", "state_apply", "progress_commit",
}
var phaseTwoProgressKinds = []string{
	"query_completed", "evaluation_completed", "event_acked", "state_applied", "progress_committed",
}

// phaseTwoBudgets is derived from the one list rather than repeating it. A
// budget added there but not here would be a rejection label the metric refuses
// to publish, which reads as "that budget never rejected anything".
var phaseTwoBudgets = func() []string {
	budgets := observability.CapacityBudgets()
	names := make([]string, 0, len(budgets)+1)
	for _, budget := range budgets {
		names = append(names, string(budget))
	}
	return append(names, string(observability.CapacityBudgetOther))
}()
var phaseTwoCapacityResults = []string{"admitted", "rejected", "other"}
var phaseTwoSourceResults = []string{"degraded", "recovered"}

// sourceCompileResults is the closed vocabulary of how a refresh round
// obtained each strategy's compilation.
var sourceCompileResults = []string{"compiled", "reused"}
var phaseTwoReadyQueueKinds = []string{"normal", "recovery"}
var phaseTwoQueryInflightKinds = []string{"normal", "retry", "replay", "probe"}
var phaseTwoQueryAdmissionResults = []observability.Result{
	observability.ResultStarted,
	observability.ResultSuccess,
	observability.ResultFailed,
	observability.ResultPaused,
	observability.ResultTimeout,
}
var phaseTwoOwnershipTransitions = []observability.Stage{
	observability.StageAssignmentAcquired,
	observability.StageAssignmentLost,
	observability.StageTakeoverStarted,
	observability.StageTakeoverCompleted,
	observability.StageFenceChecked,
}

func newPhaseTwoMetrics() phaseTwoMetrics {
	metrics := phaseTwoMetrics{
		work: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "worker_work_total",
			Help: "Bounded phase-two worker business work by stable work kind.",
		}, []string{"work_kind"}),
		busy: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "worker_busy_seconds_total",
			Help: "Phase-two worker busy time by coarse workflow stage.",
		}, []string{"stage"}),
		lastProgress: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "last_progress_timestamp_seconds",
			Help: "Unix timestamp of the latest successful phase-two completion boundary.",
		}, []string{"kind"}),
		capacity: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "capacity_transition_total",
			Help: "Process-wide phase-two capacity admission outcomes by fixed budget kind.",
		}, []string{"budget", "result"}),
		sourceObservations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "source_observation_total",
			Help: "Phase-two source health episode transitions by bounded source, result and reason class.",
		}, []string{"source_kind", "result", "reason_class"}),
		sourceRefreshes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "source_refresh_total",
			Help: "Phase-two source refresh outcomes by fixed status.",
		}, []string{"status"}),
		sourceCompiles: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "source_compile_total",
			Help: "Strategies a source refresh round asked the compiler about, by whether they were compiled " +
				"or taken from an earlier round's compilation of the same document; the two add up to the " +
				"strategies of the round, and a round whose source did not change is all reused.",
		}, []string{"result"}),
		activationFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "activation_failure_total",
			Help: "Control activation failures by fixed stage and class.",
		}, []string{"activation_failure_stage", "activation_failure_class"}),
		ownedQueryGroups: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "worker_owned_query_groups",
			Help: "Query groups currently owned by this complete worker role.",
		}, []string{"worker_role"}),
		ownershipTransitions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "ownership_transition_total",
			Help: "Ownership lifecycle transitions by bounded transition, result and reason class.",
		}, []string{"transition", "result", "reason_class"}),
		queryAdmission: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "worker_query_admission_total",
			Help: "Process-wide physical query permit admission outcomes by fixed operation and result.",
		}, []string{"operation", "result"}),
	}
	metrics.dueIndex = newDueIndexMetrics()
	metrics.redisCalls = newRedisCallMetrics()
	metrics.controlCache = newControlCacheCollector()
	metrics.legacyPodCache = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "legacy_pod_cache_total", Help: "Existing Python Pod cache reads by bounded result."}, []string{"result"})
	metrics.redisPool = newRedisPoolCollector()
	metrics.shortPeriod = newShortPeriodMetrics()
	metrics.queryStatus = newQueryStatusMetrics()
	metrics.queryCooldown = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "query_cooldown_events_total", Help: "External source_backend query cooldown transitions and failed real probes by bounded event."}, []string{"event"})
	metrics.slotReadiness = newSlotReadinessMetrics()
	metrics.slotTiming = newSlotTimingMetrics()
	metrics.workflow = newWorkflowMetrics()
	metrics.activeQGSetCount = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "active_qg_set_query_groups", Help: "Query groups in the current immutable Active Set."})
	metrics.activeQGSetBytes = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "active_qg_set_object_bytes", Help: "Encoded bytes in the current immutable Active Set."})
	metrics.activeQGSetEncode = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "active_qg_set_encode_duration_seconds", Help: "Active Set canonical encoding duration.", Buckets: activeQGSetDurationBuckets}, []string{"result"})
	metrics.activeQGSetRedis = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "active_qg_set_redis_duration_seconds", Help: "Active Set Redis operation duration.", Buckets: activeQGSetDurationBuckets}, []string{"operation", "result"})
	metrics.scheduleCutoverPayload = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "schedule_cutover_payload_bytes", Help: "Bytes the Control Leader sent in the last publication cutover compare-and-set call."})
	metrics.scheduleCutoverTimelineMax = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "schedule_timeline_bytes_max", Help: "Largest Schedule timeline written by the last publication cutover. Rising across cutovers means some timeline is never pruned."})
	metrics.scheduleTimelineBytes = prometheus.NewHistogram(prometheus.HistogramOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "schedule_timeline_bytes", Help: "Schedule timeline sizes as written by publication cutovers.", Buckets: scheduleTimelineBytesBuckets})
	metrics.scheduleSegmentsPruned = prometheus.NewCounter(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "schedule_segments_pruned_total", Help: "Closed Schedule Segments dropped by publication cutovers because no Slot in them is read anymore."})
	metrics.schedulePruneSkipped = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "schedule_prune_skipped_total", Help: "Schedule timelines a cutover left unpruned, by reason."}, []string{"reason"})
	metrics.scheduleCutoverDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "schedule_cutover_duration_seconds", Help: "Publication cutover compare-and-set duration.", Buckets: activeQGSetDurationBuckets}, []string{"result"})
	for _, reason := range observability.SchedulePruneSkipReasons {
		metrics.schedulePruneSkipped.WithLabelValues(reason)
	}
	metrics.scheduleCutoverQueryGroups = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "schedule_cutover_query_groups_total", Help: "Query Groups by what a publication cutover did with them: kept (content and contexts unchanged, no write), revised (contexts changed, one output context revision appended), cut (content changed, Segment closed and reopened), legacy_cut (Segment named no content and was cut once), retired, added."}, []string{"decision"})
	for _, decision := range observability.ScheduleCutoverDecisions {
		metrics.scheduleCutoverQueryGroups.WithLabelValues(decision)
	}
	metrics.scheduleCutoverTimelinesRead = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "schedule_cutover_timelines_read", Help: "Schedule timelines the last publication cutover read to decide. Equal to the population on the first cutover of a Control Leader process, the changed set afterwards."})
	// The failure code itself is an open vocabulary and stays in the log and
	// the fleet view; the counter carries the bounded stage and category so a
	// family of failures that produces no completion at all still has a rate.
	metrics.queryFailures = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "query_failure_total", Help: "Slot attempts that failed before producing a completion, by the Coordinator stage that failed and the failure category. The failure code is in the log line and the fleet view."}, []string{"stage", "category"})
	for _, stage := range observability.QueryFailureStages {
		for _, category := range observability.QueryFailureCategories {
			metrics.queryFailures.WithLabelValues(stage, category)
		}
	}
	metrics.objectCatalogObjects = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "object_catalog_objects_total", Help: "Content-addressed catalog objects by what a write or renewal did with them: written, present (already stored under their digest) or missing (referenced but not found on renewal)."}, []string{"operation", "outcome"})
	metrics.objectCatalogRedis = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "object_catalog_redis_duration_seconds", Help: "Object catalog write or renewal duration.", Buckets: activeQGSetDurationBuckets}, []string{"operation", "result"})
	metrics.objectCatalogManifestBytes = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "object_catalog_manifest_bytes", Help: "Encoded bytes of the manifest written for the latest publication."})
	metrics.objectReads = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "object_read_total", Help: "Catalog object reads by a Worker, by object kind and outcome; for a Segment, whether its Query Group was read by content and if not, why."}, []string{"kind", "result"})
	// Pre-created so that "no skew" reads as zeros, not as an absent family.
	metrics.stateGenerationSkew = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "state_generation_skew_total", Help: "Due Plans whose activation record names a state generation that disagrees with one derived elsewhere: formula (this process compiles the same Plan to another generation than the Control Leader that published it; tolerated, the record's generation governs the Slot; expected while a release rolls, a version mismatch if it persists), record (the record names a generation the Query Group object published with it does not carry; the Slot is refused)."}, []string{"kind"})
	for _, kind := range observability.StateGenerationSkewKinds {
		metrics.stateGenerationSkew.WithLabelValues(kind)
	}
	for _, result := range sourceCompileResults {
		metrics.sourceCompiles.WithLabelValues(result)
	}
	metrics.legacyMigration = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "legacy_active_qg_migration_total", Help: "One-time legacy Active QG migration outcomes."}, []string{"result", "reason_class"})
	metrics.legacyMigrationScan = prometheus.NewHistogram(prometheus.HistogramOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "legacy_active_qg_migration_scan_keys", Help: "Redis keys scanned by one-time legacy Active QG migration.", Buckets: legacyMigrationScanBuckets})
	metrics.legacyMigrationTime = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "legacy_active_qg_migration_duration_seconds", Help: "One-time legacy Active QG migration duration.", Buckets: activeQGSetDurationBuckets}, []string{"result"})
	metrics.undrainedDrainingQueryGroups = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "undrained_draining_query_groups", Help: "Replicated per-Pod view of retired Query Groups still requiring ownership until their retirement boundary is drained; aggregate replicas with max, not sum."})
	metrics.activationHeldQueryGroups = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "activation_held_query_groups", Help: "Query Groups the latest activation attempt brought back from retirement that had not drained; today any of them fails the whole activation (activation_failure_total{reactivation,not_drained}), so an attempt with a non-zero value is an attempt that failed for them. Set by the Control Leader on every attempt that reached the reactivation check."})
	metrics.activationHeldAgeSecondsMax = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "activation_held_age_seconds_max", Help: "How long the oldest held retirement of the latest activation attempt has waited, in seconds; zero when nothing is held."})
	metrics.algorithmEvaluations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "algorithm_evaluation_total",
		Help: "Algorithm evaluation outcomes by fixed source family and result.",
	}, []string{"algorithm_family", "result"})
	metrics.algorithmInputs = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "algorithm_input_total",
		Help: "Named algorithm input completion by fixed source family, input, dependency point and result.",
	}, []string{"algorithm_family", "input_name", "dependency_point", "result"})
	metrics.seriesAdmission = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "series_admission_total",
		Help: "Access-path admission decisions by filter, outcome and bounded reason.",
	}, []string{"filter", "result", "reason"})
	metrics.cmdbIndexHosts = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "cmdb_host_index_hosts",
		Help: "Hosts in the in-memory CMDB index the target filter decides on.",
	})
	// The list is a transcription of a platform setting an operator can change
	// without alarmd noticing. Publishing how many states it is filtering on
	// makes that drift a one-query check instead of a shadow reconcile.
	metrics.hostDisableMonitorStates = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "host_disable_monitor_states",
		Help: "Host states the access path treats as not monitored; zero means the filter is not installed.",
	})
	// An alert level this build has no name for arrives at the consumer under
	// its default severity: not an error anywhere, just an alert at the wrong
	// level. The platform states its levels in the strategy snapshot and may
	// grow more, so the moment one appears has to be visible here rather than
	// in whatever noticed the alerts looked wrong.
	metrics.unmappedSeverity = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "unmapped_severity_total",
		Help: "Events published with a severity derived from the level number, because no name was known for it.",
	}, []string{"level"})
	metrics.cmdbIndexAge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "cmdb_host_index_age_seconds",
		Help: "Age of the CMDB index alarmd holds, and of the platform refresh it was built from.",
	}, []string{"kind"})
	metrics.cmdbIndexDegraded = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "cmdb_host_index_degraded",
		Help: "Whether the CMDB index is unusable for filtering, by bounded reason.",
	}, []string{"reason"})
	return metrics
}

func (m phaseTwoMetrics) collectors() []prometheus.Collector {
	return append(append(m.workflow.collectors(), []prometheus.Collector{
		m.shortPeriod.completed, m.shortPeriod.duration, m.shortPeriod.lag,
		m.queryStatus.responses,
		m.queryCooldown,
		m.slotReadiness.slack, m.slotReadiness.boundary,
		m.slotTiming,
		m.work, m.busy, m.lastProgress, m.capacity, m.sourceObservations, m.sourceRefreshes, m.sourceCompiles,
		m.activationFailures, m.unmappedSeverity,
		m.ownedQueryGroups, m.ownershipTransitions,
		m.queryAdmission,
		m.activeQGSetCount, m.activeQGSetBytes, m.activeQGSetEncode, m.activeQGSetRedis,
		m.scheduleCutoverPayload, m.scheduleCutoverTimelineMax, m.scheduleTimelineBytes, m.scheduleSegmentsPruned, m.schedulePruneSkipped, m.scheduleCutoverDuration,
		m.scheduleCutoverQueryGroups, m.scheduleCutoverTimelinesRead,
		m.queryFailures,
		m.objectCatalogObjects, m.objectCatalogRedis, m.objectCatalogManifestBytes, m.objectReads, m.stateGenerationSkew,
		m.legacyMigration, m.legacyMigrationScan, m.legacyMigrationTime,
		m.undrainedDrainingQueryGroups, m.activationHeldQueryGroups, m.activationHeldAgeSecondsMax,
		m.algorithmEvaluations, m.algorithmInputs,
	}...), append(append(m.redisCalls.collectors(), m.dueIndex.collectors()...),
		m.controlCache, m.redisPool, m.legacyPodCache,
		m.seriesAdmission, m.cmdbIndexHosts, m.hostDisableMonitorStates, m.cmdbIndexAge, m.cmdbIndexDegraded)...)
}

func (m phaseTwoMetrics) observe(observation observability.Observation) {
	m.workflow.observe(observation)
	m.shortPeriod.observe(observation)
	m.queryStatus.observe(observation)
	if facts := observation.QueryCooldown; facts != nil {
		m.queryCooldown.WithLabelValues(facts.Event).Inc()
	}
	m.slotReadiness.observe(observation)
	m.observeSlotTiming(observation)
	if facts := observation.SourceRefresh; facts != nil {
		m.sourceRefreshes.WithLabelValues(string(facts.Status)).Inc()
		if facts.CompiledStrategies > 0 {
			m.sourceCompiles.WithLabelValues("compiled").Add(float64(facts.CompiledStrategies))
		}
		if facts.ReusedStrategies > 0 {
			m.sourceCompiles.WithLabelValues("reused").Add(float64(facts.ReusedStrategies))
		}
	}
	if facts := observation.ActivationFailure; facts != nil {
		m.activationFailures.WithLabelValues(string(facts.Stage), string(facts.Class)).Inc()
	}
	if facts := observation.DrainingQG; facts != nil {
		m.undrainedDrainingQueryGroups.Set(float64(facts.Undrained))
	}
	if facts := observation.ActivationHold; facts != nil && observation.Stage == observability.StageActivationHold {
		m.activationHeldQueryGroups.Set(float64(facts.Held))
		m.activationHeldAgeSecondsMax.Set(float64(facts.MaxAgeSeconds))
	}
	if facts := observation.ActiveQGSet; facts != nil {
		if facts.Operation == "encode" {
			m.activeQGSetEncode.WithLabelValues(facts.Result).Observe(facts.Duration.Seconds())
		} else if facts.Operation == "read" || facts.Operation == "write" || facts.Operation == "renew" {
			m.activeQGSetRedis.WithLabelValues(facts.Operation, facts.Result).Observe(facts.Duration.Seconds())
			if facts.Operation == "renew" && facts.Result == "success" {
				m.activeQGSetCount.Set(float64(facts.QueryGroups))
				m.activeQGSetBytes.Set(float64(facts.ObjectBytes))
			}
		}
	}
	if facts := observation.QueryFailure; facts != nil && observation.Result == observability.ResultFailed {
		m.queryFailures.WithLabelValues(facts.Stage, facts.Category).Inc()
	}
	if facts := observation.ScheduleCutover; facts != nil {
		m.scheduleCutoverDuration.WithLabelValues(facts.Result).Observe(facts.Duration.Seconds())
		m.scheduleSegmentsPruned.Add(float64(facts.SegmentsPruned))
		for reason, count := range facts.PrunesSkipped {
			m.schedulePruneSkipped.WithLabelValues(reason).Add(float64(count))
		}
		for _, size := range facts.TimelineBytes {
			m.scheduleTimelineBytes.Observe(float64(size))
		}
		if facts.Result == "success" {
			m.scheduleCutoverPayload.Set(float64(facts.PayloadBytes))
			m.scheduleCutoverTimelineMax.Set(float64(facts.MaxTimelineBytes))
			m.scheduleCutoverTimelinesRead.Set(float64(facts.TimelinesRead))
			for decision, count := range facts.QueryGroups {
				m.scheduleCutoverQueryGroups.WithLabelValues(decision).Add(float64(count))
			}
		}
	}
	if facts := observation.ObjectCatalog; facts != nil {
		m.objectCatalogRedis.WithLabelValues(facts.Operation, facts.Result).Observe(facts.Duration.Seconds())
		m.objectCatalogObjects.WithLabelValues(facts.Operation, "written").Add(float64(facts.Written))
		m.objectCatalogObjects.WithLabelValues(facts.Operation, "present").Add(float64(facts.Present))
		m.objectCatalogObjects.WithLabelValues(facts.Operation, "missing").Add(float64(facts.Missing))
		if facts.Operation == "write" && facts.Result == "success" {
			m.objectCatalogManifestBytes.Set(float64(facts.ManifestBytes))
		}
	}
	if facts := observation.ObjectRead; facts != nil {
		m.objectReads.WithLabelValues(facts.Kind, facts.Result).Inc()
	}
	if facts := observation.StateGenerationSkew; facts != nil {
		m.stateGenerationSkew.WithLabelValues(facts.Kind).Inc()
	}
	if facts := observation.LegacyMigration; facts != nil {
		m.legacyMigration.WithLabelValues(facts.Result, facts.ReasonClass).Inc()
		m.legacyMigrationScan.Observe(float64(facts.ScanKeys))
		m.legacyMigrationTime.WithLabelValues(facts.Result).Observe(facts.Duration.Seconds())
	}
	for _, fact := range observation.AlgorithmEvaluations {
		m.algorithmEvaluations.WithLabelValues(
			string(fact.SourceAlgorithmFamily), string(fact.Result),
		).Inc()
	}
	for _, fact := range observation.AlgorithmInputs {
		m.algorithmInputs.WithLabelValues(
			string(fact.SourceAlgorithmFamily), string(fact.InputName),
			string(fact.DependencyPoint), string(fact.Result),
		).Inc()
	}
	if observation.Component == observability.ComponentScheduler &&
		observation.Stage == observability.StageQueryAdmission && observation.QueryPermit != nil {
		m.observeQueryPermit(observation)
	}
	if observation.Component == observability.ComponentControlPlane && observation.SourceKind != "" &&
		(observation.Result == observability.ResultDegraded || observation.Result == observability.Result(observability.ResultRecovered)) {
		m.sourceObservations.WithLabelValues(
			string(observation.SourceKind), string(observation.Result), string(observation.ReasonCode),
		).Inc()
	}
	if observation.Component == observability.ComponentOwnership && isOwnershipTransitionStage(observation.Stage) {
		reasonClass := observability.NormalizeMetricReason(
			observability.ComponentOwnership, observation.ReasonCode, observation.Result,
		)
		m.ownershipTransitions.WithLabelValues(
			string(observation.Stage), string(observation.Result), string(reasonClass),
		).Inc()
	}
	if observation.Component == observability.ComponentResource && observation.CapacityBudget != "" {
		result := "other"
		if observation.Result == observability.ResultPaused || observation.Result == observability.ResultFailed {
			result = "rejected"
		} else if observation.Result == observability.ResultSuccess || observation.Result == observability.ResultResumed {
			result = "admitted"
		}
		m.capacity.WithLabelValues(normalizePhaseTwoBudget(observation.CapacityBudget), result).Inc()
	}
	stage := phaseTwoBusyStage(observation.Stage)
	if stage == "" {
		return
	}
	if observation.Duration >= 0 {
		m.busy.WithLabelValues(stage).Add(observation.Duration.Seconds())
	}
	if phaseTwoWorkCompleted(observation) {
		for kind, count := range phaseTwoWork(observation) {
			if count > 0 {
				m.work.WithLabelValues(kind).Add(float64(count))
			}
		}
	}
	if kind := phaseTwoProgressKind(observation.Stage); kind != "" && observation.Result == observability.ResultSuccess {
		m.lastProgress.WithLabelValues(kind).Set(float64(time.Now().Unix()))
	}
}

func (m phaseTwoMetrics) observeQueryPermit(observation observability.Observation) {
	facts := observation.QueryPermit
	// Occupancy is no longer published from here. These are permit events, and
	// publishing a level only when it is about to change reports the boundary
	// rather than the interval -- in production the resulting gauge never rose
	// above one while thousands of permits were granted per minute. The permit
	// collector reads the same counters at scrape time instead.
	if facts.Admission && isPhaseTwoQueryOperation(observation.Operation) &&
		isPhaseTwoQueryAdmissionResult(observation.Result) {
		m.queryAdmission.WithLabelValues(string(observation.Operation), string(observation.Result)).Inc()
	}
}

func isPhaseTwoQueryOperation(operation observability.Operation) bool {
	switch operation {
	case observability.OperationNormal, observability.OperationRetry,
		observability.OperationReplay, observability.OperationProbe:
		return true
	default:
		return false
	}
}

func isPhaseTwoQueryAdmissionResult(result observability.Result) bool {
	for _, allowed := range phaseTwoQueryAdmissionResults {
		if result == allowed {
			return true
		}
	}
	return false
}

func (r *Recorder) SetOwnedQueryGroups(count int) {
	if r == nil || r.phaseTwo.ownedQueryGroups == nil {
		return
	}
	if count < 0 {
		count = 0
	}
	r.phaseTwo.ownedQueryGroups.WithLabelValues("complete").Set(float64(count))
}

func isOwnershipTransitionStage(stage observability.Stage) bool {
	for _, transition := range phaseTwoOwnershipTransitions {
		if stage == transition {
			return true
		}
	}
	return false
}

func phaseTwoWorkCompleted(observation observability.Observation) bool {
	if observation.Err != nil {
		return false
	}
	switch observation.Stage {
	case observability.StageEventACKed:
		return observation.Result == observability.ResultSuccess
	case observability.StageProgressCommitted:
		return observation.Result == observability.ResultSuccess || observation.Result == observability.ResultDegraded ||
			observation.Result == observability.ResultTerminal
	default:
		return observation.Result == observability.ResultSuccess || observation.Result == observability.ResultDegraded ||
			observation.Result == observability.ResultTerminal
	}
}

func phaseTwoBusyStage(stage observability.Stage) string {
	switch stage {
	case observability.StageQueryCompleted:
		return "query"
	case observability.StageEvaluationCompleted:
		return "evaluation"
	case observability.StageEventACKed:
		return "event"
	case observability.StageStatePreflight, observability.StageGapLoaded, observability.StageSideEffectAdmission,
		observability.StageGapGuardCommitted, observability.StageMutationCompared,
		observability.StageStateAdmission, observability.StageStateApplied:
		return "state"
	case observability.StageProgressCommitted:
		return "progress"
	default:
		return ""
	}
}

func phaseTwoProgressKind(stage observability.Stage) string {
	switch stage {
	case observability.StageQueryCompleted, observability.StageEvaluationCompleted,
		observability.StageEventACKed, observability.StageStateApplied, observability.StageProgressCommitted:
		return string(stage)
	default:
		return ""
	}
}

func phaseTwoWork(observation observability.Observation) map[string]int64 {
	work := make(map[string]int64, 4)
	switch observation.Stage {
	case observability.StageEvaluationCompleted:
		work["record"] = observation.Counts.Records
	case observability.StageEventACKed:
		work["event_ack"] = observation.Counts.Events
	case observability.StageStatePreflight:
		work["state_load"] = observation.Counts.Keys
	case observability.StageStateApplied:
		work["state_apply"] = observation.Counts.Keys
	case observability.StageProgressCommitted:
		work["progress_commit"] = 1
	}
	return work
}

func normalizePhaseTwoBudget(budget observability.CapacityBudget) string {
	switch budget {
	case observability.CapacityBudgetSeries, observability.CapacityBudgetRetainedBytes,
		observability.CapacityBudgetStateMutations, observability.CapacityBudgetEvents,
		observability.CapacityBudgetGapMutations:
		return string(budget)
	default:
		return "other"
	}
}
