// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"math"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

type phaseTwoMetrics struct {
	workflow                        workflowMetrics
	shortPeriod                     shortPeriodMetrics
	queryStatus                     queryStatusMetrics
	queryCooldown                   *prometheus.CounterVec
	slotReadiness                   slotReadinessMetrics
	slotTiming                      *prometheus.HistogramVec
	work                            *prometheus.CounterVec
	busy                            *prometheus.CounterVec
	lastProgress                    *prometheus.GaugeVec
	capacity                        *prometheus.CounterVec
	stateWriteReuse                 *prometheus.CounterVec
	stateWriteChange                *prometheus.CounterVec
	sourceObservations              *prometheus.CounterVec
	sourceRefreshes                 *prometheus.CounterVec
	sourceCompiles                  *prometheus.CounterVec
	sourceReads                     *prometheus.CounterVec
	sourceStrategiesRead            prometheus.Counter
	sourceChangeSignalAge           prometheus.Gauge
	activationFailures              *prometheus.CounterVec
	ownedQueryGroups                *prometheus.GaugeVec
	ownershipTransitions            *prometheus.CounterVec
	queryAdmission                  *prometheus.CounterVec
	activeQGSetCount                prometheus.Gauge
	activeQGSetBytes                prometheus.Gauge
	activeQGSetEncode               *prometheus.HistogramVec
	activeQGSetRedis                *prometheus.HistogramVec
	scheduleCutoverPayload          prometheus.Gauge
	scheduleCutoverTimelineMax      prometheus.Gauge
	scheduleTimelineBytes           prometheus.Histogram
	scheduleSegmentsPruned          prometheus.Counter
	schedulePruneSkipped            *prometheus.CounterVec
	scheduleCutoverDuration         *prometheus.HistogramVec
	scheduleCutoverQueryGroups      *prometheus.CounterVec
	scheduleCutoverTimelinesRead    prometheus.Gauge
	queryFailures                   *prometheus.CounterVec
	objectCatalogObjects            *prometheus.CounterVec
	objectCatalogRedis              *prometheus.HistogramVec
	objectCatalogManifestBytes      prometheus.Gauge
	objectReads                     *prometheus.CounterVec
	stateGenerationSkew             *prometheus.CounterVec
	legacyMigration                 *prometheus.CounterVec
	legacyMigrationScan             prometheus.Histogram
	legacyMigrationTime             *prometheus.HistogramVec
	undrainedDrainingQueryGroups    *loadedGauge
	drainingCursorPrunedQueryGroups *loadedGauge
	rebalancePlannedMoves           *loadedGauge
	assignmentIndexStaleRounds      *loadedGauge
	assignmentIndexWrites           *prometheus.CounterVec
	assignmentIndexReads            *prometheus.CounterVec
	assignmentIndexConfirm          *prometheus.CounterVec
	assignmentRecordReads           *prometheus.CounterVec
	scheduleCursorAdvances          *prometheus.CounterVec
	activationHeldQueryGroups       *loadedGauge
	activationHeldAgeSecondsMax     *loadedGauge
	algorithmEvaluations            *prometheus.CounterVec
	recoveryHeld                    *prometheus.CounterVec
	recoveryPastLevelWithoutRecov   prometheus.Counter
	openAlertGate                   *prometheus.CounterVec
	openAlertSet                    *openAlertSetCollector
	controlSourceRounds             *prometheus.CounterVec
	controlSourceRetainedStale      prometheus.Counter
	controlSource                   *controlSourceCollector
	redisCalls                      redisCallMetrics
	controlCache                    *controlCacheCollector
	legacyPodCache                  *prometheus.CounterVec
	redisPool                       *redisPoolCollector
	canonicalEncoding               *canonicalEncodingCollector
	algorithmInputs                 *prometheus.CounterVec
	seriesAdmission                 *prometheus.CounterVec
	cmdbIndexHosts                  prometheus.Gauge
	hostDisableMonitorStates        prometheus.Gauge
	unmappedSeverity                *prometheus.CounterVec
	cmdbIndexAge                    *prometheus.GaugeVec
	cmdbIndexDegraded               *prometheus.GaugeVec
	dueIndex                        dueIndexMetrics
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
		stateWriteReuse: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "state_write_reuse_total",
			Help: "Runtime State mutations admitted for writing, by how much of what the write carries was " +
				"already stored. Nothing is skipped: this measures what a skip could save before the write " +
				"path changes. identical is the whole blob reproduced; decision_stable is the Level state " +
				"and series guard unchanged while the history window moved, which is where a steady series " +
				"is expected because that window carries a record id and source time that advance every " +
				"round; changed is a moved Level state; unobserved is a key with nothing stored yet, kept " +
				"apart so a starting worker's warm-up does not depress the others. Read the classes " +
				"separately: identical and decision_stable are what two different changes could save and do " +
				"not add up. Their sum is the admitted population, so every class zero with a zero sum " +
				"means the classifier never ran rather than that nothing was reusable. stored says what was " +
				"held when the comparison was made, because a series that keeps recovering and one that " +
				"never leaves history warming are both steady and both pay a write every round, but reach " +
				"it through different branches; averaged together the rate describes neither. The population is " +
				"State mutations admitted for writing, one per mutation per round, and not Redis commands: " +
				"a storage-layer retry reissues a command without a new admission, and a request with " +
				"repeated keys leaves the pipeline for the sequential path. So a saving estimated by " +
				"multiplying a rate from here by a Redis command total mixes two populations. The " +
				"conversion is not assumed to be one: it is this family's sum over a window against the " +
				"pipelined evalsha count over the same window, and it has to be measured before it is used.",
		}, []string{"class", "stored"}),
		stateWriteChange: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "state_write_change_reason_total",
			Help: "For State writes whose stored decision state differed, which field differed first, in a " +
				"fixed comparison order. The class alone cannot be acted on: changed covers a decision that " +
				"really moved, which would end the case for skipping the write, and a field that should never " +
				"have counted as part of the decision, which would mean the predicate is wrong rather than " +
				"the idea, and those point at opposite actions. Counts are first differences, not how many " +
				"fields differ, so they are read as a breakdown of the changed class and nowhere else.",
		}, []string{"reason", "stored"}),
		sourceObservations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "source_observation_total",
			Help: "Phase-two source health episode transitions by bounded source, result and reason class.",
		}, []string{"source_kind", "result", "reason_class"}),
		sourceRefreshes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "source_refresh_total",
			Help: "Phase-two source refresh outcomes by fixed status.",
		}, []string{"status"}),
		sourceReads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "source_read_total",
			Help: "Source refresh rounds by whether they read the strategy documents (full) or reused the previous " +
				"round's observation (skipped), and why: the change signal or active set moved (changed), the previous " +
				"round did not end unchanged (pending), the periodic bound on unsignalled changes passed (periodic), " +
				"the source offered no change signal (missing), or the process remembered no earlier read (elected).",
		}, []string{"mode", "reason"}),
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
	metrics.canonicalEncoding = newCanonicalEncodingCollector()
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
	for _, outcome := range observability.AllSourceReadOutcomes() {
		metrics.sourceReads.WithLabelValues(string(outcome.Mode), string(outcome.Reason))
	}
	metrics.legacyMigration = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "legacy_active_qg_migration_total", Help: "One-time legacy Active QG migration outcomes."}, []string{"result", "reason_class"})
	metrics.legacyMigrationScan = prometheus.NewHistogram(prometheus.HistogramOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "legacy_active_qg_migration_scan_keys", Help: "Redis keys scanned by one-time legacy Active QG migration.", Buckets: legacyMigrationScanBuckets})
	metrics.legacyMigrationTime = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "legacy_active_qg_migration_duration_seconds", Help: "One-time legacy Active QG migration duration.", Buckets: activeQGSetDurationBuckets}, []string{"result"})
	metrics.drainingCursorPrunedQueryGroups = newLoadedGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "draining_cursor_pruned_query_groups", Help: "Replicated per-Pod view of draining Query Groups whose Progress cursor lies before the earliest Slot their Schedule timeline still holds. Such a Query Group can never find the Slot its cursor asks for, so it cannot drain by itself; the count is reported before anything acts on it. Aggregate replicas with max, not sum."})
	metrics.rebalancePlannedMoves = newLoadedGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "rebalance_planned_moves", Help: "Assignments the latest rebalance planning round on this Control Leader would move from the most to the least loaded ready worker. Shadow measurement: only computed, never published. Meaningful on the Control Leader only; aggregate replicas with max, not sum."})
	metrics.assignmentIndexStaleRounds = newLoadedGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "assignment_index_stale_rounds", Help: "Consecutive reconcile rounds in which this worker read the same Assignment index round number. Healthy values are zero and one: the Leader writes once per reconcile interval and workers read on their own interval of the same length, so a reader that runs just before the writer sees the previous round once. Two or more means the index has stopped advancing, which reads exactly like an unchanged fleet otherwise. Per worker; aggregate with max."})
	metrics.assignmentIndexWrites = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "assignment_index_write_total", Help: "Assignment index rounds the Control Leader attempted, by result."}, []string{"result"})
	metrics.assignmentIndexReads = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "assignment_index_read_total", Help: "Assignment index reads by this worker, by result: fresh (round advanced), stale (same round), missing (no index or no set), invalid (unreadable)."}, []string{"result"})
	metrics.assignmentIndexConfirm = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "assignment_index_confirm_total", Help: "Changes a worker took from the Assignment index and confirmed against the Assignment records, by outcome: opened (a candidate the record confirmed), rejected (a candidate the record refused), released (a held Query Group the record confirmed gone), retained (a held Query Group the index dropped but the record still assigns here). The record always wins; rejected and retained measure how often the index was behind it."}, []string{"result"})
	metrics.assignmentRecordReads = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "assignment_record_read_total", Help: "Assignment records this worker read to learn what it owns, by path: index (only the changes the index named) or full (every record of the population, when no usable index was there). The index exists to keep the index path near zero in a quiet round."}, []string{"path"})
	metrics.scheduleCursorAdvances = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "schedule_cursor_advance_total", Help: "Attempts to move a Progress cursor that points into a pruned part of its timeline to the earliest Slot the timeline still holds, by outcome; a conflict also carries the one fact that refused it (refusal), which is empty for every other outcome."}, []string{"result", "refusal"})
	// Every outcome and every refusal of a conflict publishes a zero from the
	// start, so a refusal that never happens reads as zero and not as absent.
	for _, status := range observability.CursorAdvanceStatuses {
		if status == observability.CursorAdvanceConflict {
			for _, refusal := range observability.CursorRefusals {
				metrics.scheduleCursorAdvances.WithLabelValues(status, refusal)
			}
			continue
		}
		metrics.scheduleCursorAdvances.WithLabelValues(status, "")
	}
	metrics.undrainedDrainingQueryGroups = newLoadedGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "undrained_draining_query_groups", Help: "Replicated per-Pod view of retired Query Groups still requiring ownership until their retirement boundary is drained; aggregate replicas with max, not sum."})
	metrics.activationHeldQueryGroups = newLoadedGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "activation_held_query_groups", Help: "Query Groups the publication brings back from retirement that have not drained and were held out of the activation, which went ahead for everyone else. Reported by the Control Leader on every activation attempt and on every reconcile of a publication that still holds some, so it follows the held set down to zero; a value that does not fall is a retirement that is not draining."})
	metrics.sourceStrategiesRead = prometheus.NewCounter(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "source_strategies_read_total", Help: "Strategy documents source refresh rounds asked the source for. A skipped round adds nothing; a full read adds the whole active set."})
	metrics.sourceChangeSignalAge = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "source_change_signal_age_seconds", Help: "How long ago the source's publisher last moved its change signal, in seconds by the Control Leader's clock, as of the latest refresh round. NaN when the latest round found no signal. A value that keeps growing while strategies are being saved means the signal has stopped following the source, and every skipped round since is a round that read nothing for a wrong reason."})
	metrics.activationHeldAgeSecondsMax = newLoadedGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "activation_held_age_seconds_max", Help: "How long the oldest held retirement of the latest activation attempt has waited, in seconds; zero when nothing is held."})
	metrics.algorithmEvaluations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "algorithm_evaluation_total",
		Help: "Algorithm evaluation outcomes by fixed source family and result.",
	}, []string{"algorithm_family", "result"})
	metrics.algorithmInputs = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "algorithm_input_total",
		Help: "Named algorithm input completion by fixed source family, input, dependency point and result.",
	}, []string{"algorithm_family", "input_name", "dependency_point", "result"})
	// A RECOVERY envelope resolves the alert on its series at the consumer
	// whatever Level the alert stands at, so it goes only once every Level has
	// agreed. The two causes a Level withholds agreement for are the label; a
	// zero for either must be readable as "never held", so both are created.
	metrics.recoveryHeld = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "trigger_recovery_held_total",
		Help: "Records whose evaluated Levels agreed on RECOVERY but whose envelope was held because " +
			"another Level had not agreed: level_unavailable is a Level whose state could not be " +
			"established this round, level_recovering a Level reading NORMAL with a triggering window " +
			"still inside its recovery span. The Level results still reach the state; only the envelope " +
			"waits for a later round. Read against algorithm_evaluation_total{result=\"recovery\"}: the " +
			"ratio is the price of asking every Level. This counts hold events, not alerts: a record held " +
			"once and then released and a record held every minute for a week read alike here, and the " +
			"ratio does not separate them either. It says whether the gate is reached and how often, " +
			"never whether some alert is stuck open; a Level whose history stays gapped is the shape that " +
			"holds forever, and only the object page or the state itself can show one.",
	}, []string{"cause"})
	for _, cause := range []observability.RecoveryGateCause{observability.RecoveryGateLevelUnavailable, observability.RecoveryGateLevelRecovering} {
		metrics.recoveryHeld.WithLabelValues(string(cause))
	}
	metrics.recoveryPastLevelWithoutRecov = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "trigger_recovery_past_level_without_recovery_total",
		Help: "RECOVERY envelopes sent past a NORMAL Level whose recovery is disabled. Such a Level can " +
			"never say RECOVERY, so it is not consulted rather than holding the envelope forever. Long at " +
			"zero means no strategy in this deployment pairs a Level with recovery and one without.",
	})
	// The second recovery gate: once every Level has agreed, does the consumer
	// hold an open alert on the series at all. Every outcome is pre-created so
	// a zero reads as "never happened", and not_configured in particular has
	// to be readable at zero: on a production worker it is the wiring having
	// come apart, and an absent series would hide exactly that.
	metrics.openAlertGate = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "trigger_open_alert_gate_total",
		Help: "RECOVERY records every Level had agreed on, by what the consumer's open alert set decided: " +
			"passed (an open alert on the series; the envelope went), held_no_open_alert (none; nothing to " +
			"resolve, no envelope), held_fingerprint_unknown (the series identity the consumer keys alerts by " +
			"could not be built; held and named rather than read as absent), not_configured (the evaluation ran " +
			"without a set; the envelope went as before the gate -- on a production worker this is a wiring " +
			"fault), protocol_not_gated (the Plan does not publish the alert consumer's protocol -- the compatibility " +
			"protocol drops RECOVERY at the sink and alarmd's own decision event has no such consumer; the set was " +
			"not asked). " +
			"Counted apart from trigger_recovery_held_total: a record is counted by one gate only. Like that " +
			"counter this counts records per evaluation, not alerts. Which of passed and held_no_open_alert " +
			"dominates says nothing on its own; read it against open_alert_set_mode, because in " +
			"self_maintained mode the set is this process's own knowledge.",
	}, []string{"outcome"})
	for _, outcome := range observability.OpenAlertGateOutcomes {
		metrics.openAlertGate.WithLabelValues(string(outcome))
	}
	metrics.openAlertSet = newOpenAlertSetCollector()
	metrics.controlSourceRounds = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "control_source_refresh_total",
		Help: "Refresh rounds of the control plane's strategy source on this process, every round, by outcome " +
			"and, for a failed round, the exit it stopped at. Counted on the round, not on the transition into " +
			"an episode: the transition counter records one increment for an episode of any length, so a source " +
			"failing every round for hours was one increment there and is one increment per round here. " +
			"outcome succeeded carries exit none. A failed round's exit names the step: change_signal, " +
			"active_set_read (the store refused the read), active_set_missing (no set published), " +
			"active_set_invalid_id and active_set_duplicate (one element refuses the whole set, every round, " +
			"until the publisher fixes it -- the shape a store outage does not have), documents, " +
			"observation_unstable (the set moved under the read; clears itself), and the steps after the read " +
			"(observation_id, last_good, build_catalog, retain_executable, observation_changed, validate_catalog, " +
			"activation, confirmation, candidate, publish); other is an error no step claimed. The cause's text " +
			"is in the log line of the same round. Only the leader runs rounds: a flat zero on a follower is normal.",
	}, []string{"outcome", "exit"})
	metrics.controlSourceRounds.WithLabelValues(observability.ControlSourceRoundSucceeded, string(controlplane.SourceRefreshExitNone))
	for _, exit := range controlplane.SourceRefreshExits {
		if exit == controlplane.SourceRefreshExitNone {
			continue
		}
		metrics.controlSourceRounds.WithLabelValues(observability.ControlSourceRoundFailed, string(exit))
	}
	metrics.controlSource = newControlSourceCollector()
	metrics.controlSourceRetainedStale = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "control_source_retained_stale_revisions_total",
		Help: "Last-good Plans a Catalog build refused to retain because their persisted facts no longer hold under " +
			"this binary: the current formula derives another revision from them (disposition " +
			"LAST_GOOD_REVISION_STALE) or the current rules no longer accept them (LAST_GOOD_FACTS_INVALID). Within " +
			"one release this is zero by construction; it rises, for every retained Plan at once, when a release " +
			"changes either, and each such Plan leaves the Catalog under its disposition until its document " +
			"compiles again, instead of the whole Catalog failing to build as it did before.",
	})
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
	// Publish every class from the first scrape, at zero. A class created only
	// on its first increment is absent until then, and absent and zero read the
	// same way off a graph while meaning opposite things: one says the
	// classifier never ran, the other that it ran and found none. This reading
	// is expected to contain a genuine zero -- identical should stay there while
	// the history window travels with the decision state -- so that distinction
	// is the measurement.
	for _, class := range observability.AllStateWriteReuseClasses() {
		for _, stored := range observability.AllStateWriteReuseStored() {
			metrics.stateWriteReuse.WithLabelValues(string(class), string(stored))
		}
	}
	for _, reason := range observability.AllStateWriteChangeReasons() {
		for _, stored := range observability.AllStateWriteReuseStored() {
			metrics.stateWriteChange.WithLabelValues(string(reason), string(stored))
		}
	}
	return metrics
}

func (m phaseTwoMetrics) collectors() []prometheus.Collector {
	return append(append(m.workflow.collectors(), []prometheus.Collector{
		m.shortPeriod.completed, m.shortPeriod.duration, m.shortPeriod.lag,
		m.queryStatus.responses,
		m.queryCooldown,
		m.slotReadiness.slack, m.slotReadiness.boundary,
		m.slotTiming,
		m.work, m.busy, m.lastProgress, m.capacity, m.stateWriteReuse, m.stateWriteChange, m.sourceObservations, m.sourceRefreshes, m.sourceCompiles,
		m.sourceReads, m.sourceStrategiesRead, m.sourceChangeSignalAge,
		m.activationFailures, m.unmappedSeverity,
		m.ownedQueryGroups, m.ownershipTransitions,
		m.queryAdmission,
		m.activeQGSetCount, m.activeQGSetBytes, m.activeQGSetEncode, m.activeQGSetRedis,
		m.scheduleCutoverPayload, m.scheduleCutoverTimelineMax, m.scheduleTimelineBytes, m.scheduleSegmentsPruned, m.schedulePruneSkipped, m.scheduleCutoverDuration,
		m.scheduleCutoverQueryGroups, m.scheduleCutoverTimelinesRead,
		m.queryFailures,
		m.objectCatalogObjects, m.objectCatalogRedis, m.objectCatalogManifestBytes, m.objectReads, m.stateGenerationSkew,
		m.legacyMigration, m.legacyMigrationScan, m.legacyMigrationTime,
		m.undrainedDrainingQueryGroups, m.drainingCursorPrunedQueryGroups, m.rebalancePlannedMoves, m.assignmentIndexStaleRounds, m.assignmentIndexWrites, m.assignmentIndexReads, m.assignmentIndexConfirm, m.assignmentRecordReads, m.scheduleCursorAdvances, m.activationHeldQueryGroups, m.activationHeldAgeSecondsMax,
		m.algorithmEvaluations, m.algorithmInputs, m.recoveryHeld, m.recoveryPastLevelWithoutRecov, m.openAlertGate,
	}...), append(append(m.redisCalls.collectors(), m.dueIndex.collectors()...),
		m.controlCache, m.openAlertSet, m.controlSourceRounds, m.controlSource, m.controlSourceRetainedStale,
		m.redisPool, m.canonicalEncoding, m.legacyPodCache,
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
		if facts.RetainedStaleRevisions > 0 {
			m.controlSourceRetainedStale.Add(float64(facts.RetainedStaleRevisions))
		}
		if facts.CompiledStrategies > 0 {
			m.sourceCompiles.WithLabelValues("compiled").Add(float64(facts.CompiledStrategies))
		}
		if facts.ReusedStrategies > 0 {
			m.sourceCompiles.WithLabelValues("reused").Add(float64(facts.ReusedStrategies))
		}
		if observability.ValidSourceReadOutcome(facts.ReadMode, facts.ReadReason) {
			m.sourceReads.WithLabelValues(string(facts.ReadMode), string(facts.ReadReason)).Inc()
			if facts.StrategiesRead > 0 {
				m.sourceStrategiesRead.Add(float64(facts.StrategiesRead))
			}
			if facts.ChangeSignalPresent {
				m.sourceChangeSignalAge.Set(float64(facts.ChangeSignalAgeSeconds))
			} else {
				m.sourceChangeSignalAge.Set(math.NaN())
			}
		}
	}
	if facts := observation.ActivationFailure; facts != nil {
		m.activationFailures.WithLabelValues(string(facts.Stage), string(facts.Class)).Inc()
	}
	if facts := observation.DrainingQG; facts != nil {
		m.undrainedDrainingQueryGroups.Set(float64(facts.Undrained))
		m.drainingCursorPrunedQueryGroups.Set(float64(facts.CursorPruned))
	}
	if facts := observation.Rebalance; facts != nil && observation.Result == observability.ResultSuccess {
		m.rebalancePlannedMoves.Set(float64(facts.PlannedMoves))
	}
	if facts := observation.AssignmentIndex; facts != nil {
		switch observation.Stage {
		case observability.StageAssignmentIndexWritten:
			result := "failed"
			if observation.Result == observability.ResultSuccess {
				result = "success"
			}
			m.assignmentIndexWrites.WithLabelValues(result).Inc()
		case observability.StageAssignmentIndexRead:
			m.assignmentIndexReads.WithLabelValues(facts.Result).Inc()
			m.assignmentIndexStaleRounds.Set(float64(facts.StaleRounds))
			for label, count := range map[string]int{
				observability.AssignmentIndexOpened: facts.Opened, observability.AssignmentIndexRejected: facts.Rejected,
				observability.AssignmentIndexReleased: facts.Released, observability.AssignmentIndexRetained: facts.Retained,
			} {
				if count > 0 {
					m.assignmentIndexConfirm.WithLabelValues(label).Add(float64(count))
				}
			}
			if facts.Reads > 0 {
				path := "index"
				if facts.FullRead {
					path = "full"
				}
				m.assignmentRecordReads.WithLabelValues(path).Add(float64(facts.Reads))
			}
		}
	}
	if facts := observation.CursorAdvance; facts != nil {
		m.scheduleCursorAdvances.WithLabelValues(facts.Status, facts.Refusal).Inc()
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
	for _, fact := range observation.RecoveryGates {
		switch fact.Cause {
		case observability.RecoveryGateLevelUnavailable, observability.RecoveryGateLevelRecovering:
			m.recoveryHeld.WithLabelValues(string(fact.Cause)).Add(float64(fact.Records))
		case observability.RecoveryGateLevelWithoutRecovery:
			m.recoveryPastLevelWithoutRecov.Add(float64(fact.Records))
		}
	}
	for _, fact := range observation.OpenAlertGates {
		m.openAlertGate.WithLabelValues(string(fact.Outcome)).Add(float64(fact.Records))
	}
	if facts := observation.ControlSourceRound; facts != nil {
		m.controlSourceRounds.WithLabelValues(facts.Outcome, controlSourceRoundExit(facts)).Inc()
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
	if facts := observation.StateWriteReuse; facts != nil && !facts.Empty() {
		for key, count := range facts.Counts {
			m.stateWriteReuse.WithLabelValues(string(key.Class), string(key.Stored)).Add(float64(count))
		}
		for key, count := range facts.ChangeReasons {
			m.stateWriteChange.WithLabelValues(string(key.Reason), string(key.Stored)).Add(float64(count))
		}
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

// loadedGauge is a Gauge that emits no series until it has been set once.
// The gauges of the observe-only family have zero as their healthy value,
// and a zero emitted before the first computation reads exactly like a
// healthy steady state: a reader checking "is the count below what it
// should be" right after a rollout gets a confident wrong answer. Holding
// the series back until the first Set turns "not loaded yet" from a wrong
// answer into no answer. Describe is unchanged so the descriptor catalogue
// still lists the family.
//
// This trades one ambiguity for another: before, zero meant "healthy" or
// "not loaded yet"; now an absent series means "not loaded yet" or "the
// endpoint was not scraped at all". The trade only pays because the second
// pair is separable by companion series: when the endpoint is missing every
// series is missing, so a reader that sees the rest of this registry but not
// one of these gauges knows it is looking at a process that has not loaded
// that fact yet. Not emitting is therefore readable only while other series
// of the same registry are always present, the principle stated at the top
// of due_index.go for zero-valued counters, now a precondition of this
// family. Acceptance reads must check a companion series before concluding
// anything from an absent one.
type loadedGauge struct {
	gauge  prometheus.Gauge
	loaded atomic.Bool
}

func newLoadedGauge(opts prometheus.GaugeOpts) *loadedGauge {
	return &loadedGauge{gauge: prometheus.NewGauge(opts)}
}

func (gauge *loadedGauge) Set(value float64) {
	gauge.gauge.Set(value)
	gauge.loaded.Store(true)
}

func (gauge *loadedGauge) Describe(ch chan<- *prometheus.Desc) {
	gauge.gauge.Describe(ch)
}

func (gauge *loadedGauge) Collect(ch chan<- prometheus.Metric) {
	if gauge.loaded.Load() {
		gauge.gauge.Collect(ch)
	}
}
