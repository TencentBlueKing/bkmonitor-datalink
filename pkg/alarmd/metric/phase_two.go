// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"math"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

type phaseTwoMetrics struct {
	workflow                        workflowMetrics
	shortPeriod                     shortPeriodMetrics
	queryStatus                     queryStatusMetrics
	queryUnavailable                queryUnavailableMetrics
	queryCooldown                   *prometheus.CounterVec
	slotReadiness                   slotReadinessMetrics
	slotWait                        *prometheus.HistogramVec
	slotTiming                      *prometheus.HistogramVec
	work                            *prometheus.CounterVec
	busy                            *prometheus.CounterVec
	lastProgress                    *prometheus.GaugeVec
	capacity                        *prometheus.CounterVec
	stateWriteReuse                 *prometheus.CounterVec
	stateWriteChange                *prometheus.CounterVec
	stateAlreadyApplied             *prometheus.CounterVec
	stateVersionConflict            *prometheus.CounterVec
	ownershipRefusals               *prometheus.CounterVec
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
	noDataSlotPlans                 *prometheus.CounterVec
	noDataStalls                    *prometheus.CounterVec
	noDataMemoryRefusals            *prometheus.CounterVec
	noDataMemoryWrites              *prometheus.CounterVec
	gapGuardScopeRounds             *prometheus.CounterVec
	noDataPlansSeen                 prometheus.Counter
	noDataPlansByHop                *prometheus.CounterVec
	noDataMemoryReads               *prometheus.CounterVec
	noDataMemoryRenewals            *prometheus.CounterVec
	queryFreeCompletions            *prometheus.CounterVec
	executionEvidenceWrites         *prometheus.CounterVec
	frozenStateRenewals             *prometheus.CounterVec
	frozenStateCensus               *prometheus.CounterVec
	segmentContent                  *prometheus.CounterVec
	sourceWithheldLines             *prometheus.CounterVec
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
	scheduleCutovers                *prometheus.CounterVec
	replayExpiries                  *prometheus.CounterVec
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
	rebalanceGap                    *loadedGauge
	assignmentMoves                 *prometheus.CounterVec
	rebalancePaused                 *prometheus.CounterVec
	controlReadRoundTrips           *prometheus.CounterVec
	controlReadKeys                 *prometheus.CounterVec
	controlReadDuration             *prometheus.HistogramVec
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
	levelAbnormal                   *prometheus.CounterVec
	recoveryPastLevelWithoutRecov   prometheus.Counter
	openAlertGate                   *prometheus.CounterVec
	openAlertSet                    *openAlertSetCollector
	controlSourceRounds             *prometheus.CounterVec
	controlSourceRetainedStale      prometheus.Counter
	controlSource                   *controlSourceCollector
	platformSettings                *platformSettingsCollector
	redisCalls                      redisCallMetrics
	redisHealth                     *redisClientHealthBook
	controlCache                    *controlCacheCollector
	dispatchRotation                *dispatchRotationCollector
	localView                       *localViewCollector
	viewStream                      *viewStreamCollector
	viewClient                      *viewClientCollector
	legacyPodCache                  *prometheus.CounterVec
	redisPool                       *redisPoolCollector
	renewalGate                     *renewalGateCollector
	canonicalEncoding               *canonicalEncodingCollector
	algorithmInputs                 *prometheus.CounterVec
	seriesAdmission                 *prometheus.CounterVec
	cmdbIndexHosts                  prometheus.Gauge
	cmdbIndexServiceInstances       prometheus.Gauge
	hostDisableMonitorStates        prometheus.Gauge
	unmappedSeverity                *prometheus.CounterVec
	cmdbIndexAge                    *prometheus.GaugeVec
	cmdbIndexDegraded               *prometheus.GaugeVec
	dueIndex                        dueIndexMetrics
	controlFacts                    controlFactsMetrics
	// catalogComposition reports what the Catalog the leader last built is
	// made of; see catalog_composition.go.
	catalogComposition *catalogCompositionCollector
}

var activeQGSetDurationBuckets = []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 30}

// controlReadDurationBuckets spans a single pipelined batch on a healthy
// link through a reconcile round that is in trouble. The lower buckets are
// where a batched read belongs; the upper ones exist so a round that went
// back to waiting per Query Group is visible as a shape rather than as one
// saturated top bucket.
var controlReadDurationBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// ControlReadKinds is every value the control-read label takes.
//
// Published so the series budget is derived from the same list the families
// are pre-created from. A bound written out by hand beside a list is a second
// derivation of one fact, and the two drift the first time a third read is
// added: the budget test keeps passing against the number somebody typed.
var ControlReadKinds = []string{"assignment", "registry"}

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
		stateAlreadyApplied: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "state_already_applied_total",
			Help: "Runtime State mutations found already on disk, by where that was decided and how. " +
				"site: preflight is the view the Slot loaded before evaluating, apply is the bytes the CAS " +
				"met when it tried to write. kind: stable is the ordinary retry that read the stored " +
				"revision and found its own statement there; revision_skew is the same statement -- same " +
				"ApplyVersion, same digest -- found at a revision the mutation did not expect, which is a " +
				"write that landed while its reply was lost or was sent twice, and which used to be " +
				"classified STATE_VERSION_CONFLICT and send the Slot into a retry that could not succeed. " +
				"revision_skew is the only reading that says whether such re-sends happen in production: " +
				"non-zero and aligned with conflict bursts confirms the mechanism, a steady zero says " +
				"something else writes the key. Every pair is published at zero so absent and zero read " +
				"apart. Population is mutations, not Redis commands.",
		}, []string{"site", "kind"}),
		stateVersionConflict: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "state_version_conflict_total",
			Help: "Runtime State mutations refused STATE_VERSION_CONFLICT, by where that was decided and which " +
				"comparison refused them. site: preflight is the view the Slot loaded before evaluating, apply " +
				"is the bytes the write met. kind: missing is a key the mutation expected at a revision and did " +
				"not find, which is what a TTL that ran out between the Slot's read and its write leaves -- the " +
				"retry then reads missing, expects nothing and succeeds, so this kind rising alone is the key's " +
				"lifetime against the Slot's duration and not a competing writer; revision_moved is a key another " +
				"write advanced after the read; revision_reset is a key found below the revision expected, gone " +
				"and written fresh since the read; same_version_other_statement is the expected revision holding " +
				"this window's ApplyVersion with a different digest, two evaluations of one window that " +
				"disagree; version_incomparable is a view that reached the comparison without an ordering; " +
				"other is a store that refused without saying which. The status alone reads the same for all of " +
				"them and each points at a different fix. Every pair is published at zero so absent and zero " +
				"read apart. Population is mutations, not Redis commands.",
		}, []string{"site", "kind"}),
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
		ownershipRefusals: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "ownership_refusals_total",
			Help: "Refusals the ownership store answered, by which of its four words and where it was met. " +
				"refusal: OWNERSHIP_STALE_FENCE (the fence's epoch, token or deadline no longer match the lease), " +
				"OWNERSHIP_NOT_DESIRED (the assignment names another worker), OWNERSHIP_LEASE_BUSY (another " +
				"owner holds the lease), CONTENT_SCOPE_MOVED (the content scope a write was fenced against has " +
				"moved; the lease itself is live). site: admission is the side-effect admission check before a " +
				"Slot's writes, state_apply the fenced State write itself, lease the worker's acquire, release and " +
				"takeover, renewal the lease renewal, control the control leader's assignment writes. Every " +
				"pair is created at startup, so a zero is never happened and not an absent series; a change " +
				"of content scope is read as state_apply CONTENT_SCOPE_MOVED rising for the old scope after it " +
				"took effect and nothing before. Counted once per observation, not per key: a fenced batch " +
				"refused as a whole is one.",
		}, []string{"site", "refusal"}),
		noDataSlotPlans: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "worker_no_data_slot_plans_total",
			Help: "Plans that detect no-data, counted once per Slot by what happened to that detection. " +
				"The outcomes partition: every such Plan lands on exactly one every Slot, so their sum is " +
				"the no-data Plans this worker evaluated. EVALUATED is the only one that judged anything. " +
				"The other three are different kinds of not judging and must be read apart, because once " +
				"the round is over they look identical: SKIPPED_QUERY_NOT_FULL is a query that did not " +
				"cover the period and resolves itself next round; SKIPPED_MEMORY_UNREADABLE is a record " +
				"written by a newer build, which lasts as long as a rollback does; and " +
				"SKIPPED_SLOT_BUDGET is the Slot being unable to carry the work, which does not resolve " +
				"on its own - a history roster only grows, so a Plan that did not fit this round does " +
				"not fit the next one either. A steady zero on that last one is the expected reading and " +
				"any non-zero is worth acting on. All four labels are created at startup so a zero can " +
				"be told from a label nothing ever wrote. " +
				"Read the fleet's sum of all four over a minute against the leader's " +
				"sum(catalog_no_data_plans) times the Slots in that minute: they are the same Plans " +
				"counted at the two ends of the publication, so the two should agree. All four at zero " +
				"while the leader reports Plans is what a Plan losing its no-data section between the " +
				"leader and the worker looks like, and it looks like nothing else: the Plans still " +
				"execute, nothing fails, and every label here reads as a computed zero.",
		}, []string{"outcome"}),
		noDataPlansSeen: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "worker_no_data_plans_seen_total",
			Help: "Plans that detect no-data, counted once per Slot where this worker finds them, before " +
				"anything is decided about them. " +
				"Read it against sum(worker_no_data_slot_plans_total): the two are produced by one pass " +
				"over one list and must agree, so a census above the outcomes is a Plan dropped between " +
				"being found and being judged. " +
				"It exists because every outcome is conditional on a Plan reaching a decision, and the " +
				"failure that hid three releases running is a Plan never reaching one -- nothing judged, " +
				"nothing counted, four computed zeros and no log line. This is the number that separates " +
				"'this worker has no such Plan' from 'it has them and judged none'. Read it against the " +
				"leader's sum(catalog_no_data_plans) times the Slots in the window: the leader says how " +
				"many exist, this says how many arrived.",
		}),
		noDataPlansByHop: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "no_data_plans_by_hop_total",
			Help: "Plans that detect no-data, counted at each hop between the leader's Catalog and the " +
				"Slot that judges them, so the hop where they stop existing is a number rather than an " +
				"argument. published is what decodes back out of the bytes the leader wrote; assembled " +
				"is what a worker got back from the object store for its Segment; frozen is what " +
				"survived compilation into the due set; due is what the no-data round found there. " +
				"Read them in that order against the leader's sum(catalog_no_data_plans): the first hop " +
				"that reads zero while the one before it does not is where the section is being lost. " +
				"published is reported by the leader once per publication and the rest by every worker " +
				"once per Slot, so compare rates rather than raw sums across hops on different sides. " +
				"published only advances when a publication is actually written, so a flat zero there " +
				"means either that every publication carried none or that there was no publication at " +
				"all -- read it against object_catalog_objects_total{operation=\"write\"}, which is how " +
				"you tell those apart. assembled and frozen are per Slot and directly comparable with " +
				"each other and with worker_no_data_plans_seen_total over the same window. " +
				"Every label is created at startup, because a hop reporting nothing and a hop reporting " +
				"zero are the whole difference this family exists to show. It exists because three " +
				"releases were spent proving from the call graph that every hop carries the section " +
				"while production read zero at the end of it.",
		}, []string{"hop"}),
		segmentContent: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "segment_content_freshness_total",
			Help: "Slots frozen against a Segment, by whether that Segment names the execution object the " +
				"latest publication names for its Query Group. current is what a converged fleet " +
				"reports. stale means the fleet is executing content the control plane no longer " +
				"publishes: a Segment is cut when the schedule changes and carries the object digest it " +
				"was cut with, a publication that changes execution content writes new objects and " +
				"leaves the old ones in place renewed, and a Segment that is never recut keeps the old " +
				"ones indefinitely. legacy is a Segment that names no object and is served from the " +
				"Snapshot. unknown is a comparison that could not be made -- no publication, no " +
				"manifest, or the Query Group is not in it -- and is reported rather than folded into " +
				"current, because 'could not check' and 'checked and current' are the two a reader must " +
				"not confuse. " +
				"Any non-zero stale is worth acting on and nothing else reports it: the objects load, " +
				"the digests verify, the Plans compile and the Slots pass, so the only symptom is that " +
				"a change made in the source never takes effect. This is not about any one field; " +
				"every execution field stops at the Segment the same way.",
		}, []string{"state"}),
		sourceWithheldLines: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "control_source_withheld_lines_total",
			Help: "Source objects whose disposition changed in a refresh round, by whether the round named " +
				"the object in a log line or its line budget cut it. A partition of the changed objects: " +
				"each one is named or dropped, never both, so their sum is how much changed. " +
				"named is what a reader can act on -- each one is a log line at stage source_withheld " +
				"carrying the strategy, what happened to it and why. dropped is what the round decided " +
				"not to write, which happens when more objects changed at once than one round names; the " +
				"objects behind it are withheld all the same and are counted in catalog_withheld_objects. " +
				"Both labels are created at startup, so a steady zero on dropped can be told from a label " +
				"nothing ever wrote, and it is only a computed zero while named moves: on a leader that " +
				"reports neither, nothing changed that round, which is the steady state. Reported by the " +
				"leader only: no other replica refreshes the source.",
		}, []string{"result"}),
		queryAdmission: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "worker_query_admission_total",
			Help: "Process-wide physical query permit admission outcomes by fixed operation and result. " +
				"started is not one of the outcomes and does not partition with them: it is recorded only " +
				"when the acquisition found no free permit and had to join the waiter queue, and the same " +
				"acquisition records its real outcome afterwards. So started over success is the share of " +
				"queries that waited for a permit rather than a failure rate - measured over a settled " +
				"window it is 10.3 against 18.3 a second, which is 56% of normal admissions waiting while " +
				"the container sits at 16% of its CPU. Adding started to the other results double counts " +
				"every query that waited. timeout and failed are the ones that cost a query: timeout is a " +
				"deadline reached while waiting, failed is the waiter queue itself being full or recovery " +
				"permits being off.",
		}, []string{"operation", "result"}),
	}
	metrics.noDataStalls = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "worker_no_data_persistent_skips_total",
		Help: "Plans whose no-data detection stopped rather than missed a round, by the outcome it is " +
			"stuck on. Counted once per stall and not once per round: a Plan skipping for a day and a " +
			"hundred Plans each missing one round are the same increment on " +
			"worker_no_data_slot_plans_total, which is why that family cannot answer this and this one " +
			"exists. A Plan becomes countable again only after it evaluates, so this rising means " +
			"something new has stopped. Read it against worker_no_data_slot_plans_total{outcome}: the " +
			"skips there are a rate and these are the ones that became a state. Every outcome that can " +
			"stall has a label at startup, so a zero is a zero rather than a label nothing wrote.",
	}, []string{"outcome"})
	metrics.noDataMemoryRefusals = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "worker_no_data_memory_refusals_total",
		Help: "Absence-memory writes the store refused deterministically, by reason and by which record " +
			"was measured when the reason was size. A refusal does not fail the Slot, so nothing else " +
			"in this family moves when it happens: the Plan evaluated, reported and was counted as " +
			"evaluated, and only the record it would have written was lost. That is why this exists - " +
			"without it a Plan whose memory has stopped being writable is indistinguishable from one " +
			"whose memory is fine, for as long as it lasts. Rising and staying up is one object the " +
			"store will not take, and the line beside it carries the bytes and the bound. record is " +
			"empty for a refusal that was not about size.",
	}, []string{"reason", "record"})
	metrics.noDataMemoryWrites = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "worker_no_data_memory_writes_total",
		Help: "Absence-memory writes by what became of them, counting the ones that worked. " +
			"APPLIED and ALREADY_APPLIED mean the store now holds what the round wanted; " +
			"STALE_VERSION, CONFLICT and RETRYABLE_IO mean it does not, and each is a different " +
			"situation. Read it as the answer to \"is this Plan's memory being kept\", which the " +
			"refusal family cannot answer on its own: a write that lost a race stores nothing just as " +
			"a refused one does, so an absence of refusals is not recovery. This family plus " +
			"worker_no_data_memory_refusals_total is every mutation the store was asked for. " +
			"Every outcome has a label at startup, so a zero is a zero rather than a label nothing wrote.",
	}, []string{"outcome"})
	metrics.noDataMemoryReads = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "worker_no_data_memory_reads_total",
		Help: "One per Plan per round that read its absence memory, by which stored shape it came " +
			"from. WHOLE_MEMORY is the single-value record this build reads and no longer writes, " +
			"PER_GROUP is the one it writes, NONE is a Plan with no memory yet or one whose read " +
			"failed. It is the only signal that says how far the change of representation has got: " +
			"every other one looks the same either way, and WHOLE_MEMORY at zero across a rolling " +
			"window is the condition the one-shot cleanup waits for. WHOLE_MEMORY rising again after " +
			"reaching zero is a Plan that went back to an older build, which is the one case where " +
			"deleting the old records would lose rounds. The three add up to the Plans that were " +
			"asked for, so the sum is checkable rather than assumed, and every label exists at " +
			"startup so a zero is a zero rather than a label nothing wrote.",
	}, []string{"representation"})
	metrics.noDataMemoryRenewals = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "worker_no_data_memory_renewals_total",
		Help: "Absence-memory key renewals that reached the store, by result and reason. Under the " +
			"per-group representation a Plan whose groups are steady writes nothing at all, so this " +
			"is the only thing keeping its memory alive and the only signal that says whether that " +
			"is working: the write family is correctly silent for such a Plan, and the memory reads " +
			"fine right up until it is gone. Renewals the process answered from its own gate are not " +
			"counted -- they would bury the ones that reached the store. A failure here does not " +
			"fail the round; it means memories are on their way to expiring.",
	}, []string{"result", "reason"})
	metrics.frozenStateRenewals = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "worker_frozen_state_renewals_total",
		Help: "Runtime State keys a Slot read and did not write, by what the renewal found. A key's " +
			"life is set by its write and by nothing else, so a series whose Levels stay frozen -- " +
			"incomplete inputs, a held trigger -- has its state deleted while the Plan is still " +
			"evaluating it every minute. missing is that loss, named for the first time: before " +
			"this it either cost a whole Slot a version conflict, when the expiry landed inside the " +
			"read-to-write window, or cost the series its history with nothing recording it at all. " +
			"renewed is the mechanism working and should be steadily non-zero wherever freezes " +
			"happen; fresh is a key with life to spare, including the ones decided without asking " +
			"Redis; failed does not fail the Slot and means keys are on their way to expiring. The " +
			"four sum to the keys read and not written, which worker_work_total answers " +
			"independently as state_load minus state_apply -- the two disagreeing is the reading " +
			"that says the candidate set is wrong rather than that nothing is frozen.",
	}, []string{"result"})
	metrics.frozenStateCensus = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "worker_frozen_state_census_total",
		Help: "Where a Slot's series went, counted at the three places the decisions are made, over one " +
			"population: the series the Slot prepared for evaluation, real and synthetic no-data alike. due " +
			"is what it meant to evaluate, read is what it issued a Runtime State preflight for, written is " +
			"what it actually wrote. due-read is the series skipped before the State read -- a PRIMARY " +
			"input that was incomplete never reaches the preflight, so those keys age with nothing " +
			"touching them and a renewal that hangs on the read cannot reach them. read-written is the " +
			"population worker_frozen_state_renewals_total covers. What none of the three can count: a " +
			"Plan folded out of the Slot before any series was prepared -- a round-level gap, a Plan " +
			"withheld at compile -- has keys that age too, and they are upstream of due. Both differences " +
			"are needed and neither derives from the other; sizing that population instead from state_load " +
			"minus state_apply, which holds several other things, put the estimate two orders of magnitude " +
			"out and shipped a renewal that renewed almost nothing.",
	}, []string{"stage"})
	metrics.gapGuardScopeRounds = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "worker_gap_guard_scope_rounds_total",
		Help: "One per held gap scope per round that read it, by status, by why it is held, and by " +
			"where its count stands against its requirement. The rate is how many scopes are being " +
			"held; progress=none holding up is a guard that has had no complete round since it was " +
			"raised, which is the state somebody is looking for and the one a changed-only signal " +
			"would say nothing about. It is counted per round rather than gauged because whether a " +
			"guard is moving is a question about a stretch of time, and because a gauge would need a " +
			"memory of the previous round that survives a Query Group changing owner. " +
			"progress=ready should stay near zero: a warming scope cannot persist in that state. " +
			"reason=other is a reason nobody named here. Every combination has a label at startup.",
	}, []string{"status", "reason", "progress"})
	metrics.dueIndex = newDueIndexMetrics()
	metrics.controlFacts = newControlFactsMetrics()
	metrics.redisCalls = newRedisCallMetrics()
	metrics.redisHealth = &redisClientHealthBook{}
	metrics.controlCache = newControlCacheCollector()
	metrics.dispatchRotation = newDispatchRotationCollector()
	metrics.localView = newLocalViewCollector()
	metrics.viewStream = newViewStreamCollector()
	metrics.viewClient = newViewClientCollector()
	metrics.legacyPodCache = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "legacy_pod_cache_total", Help: "Existing Python Pod cache reads by bounded result."}, []string{"result"})
	metrics.redisPool = newRedisPoolCollector()
	metrics.renewalGate = newRenewalGateCollector()
	metrics.canonicalEncoding = newCanonicalEncodingCollector()
	metrics.shortPeriod = newShortPeriodMetrics()
	metrics.queryStatus = newQueryStatusMetrics()
	metrics.queryUnavailable = newQueryUnavailableMetrics()
	metrics.queryCooldown = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "query_cooldown_events_total", Help: "External source_backend query cooldown transitions and failed real probes by bounded event."}, []string{"event"})
	metrics.slotReadiness = newSlotReadinessMetrics()
	metrics.slotTiming = newSlotTimingMetrics()
	metrics.slotWait = newSlotWaitMetrics()
	for _, wait := range observability.SlotWaits {
		metrics.slotWait.WithLabelValues(wait)
	}
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
	metrics.scheduleCutovers = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "schedule_cutover_total",
		Help: "Publication cutovers by result and, when they failed, why. The cutover is what moves the " +
			"fleet onto newly published execution content: it closes the open Segment of every Query " +
			"Group whose content changed and opens a new one naming the new object. A cutover that " +
			"fails leaves every Segment where it is, so the fleet keeps executing what it was " +
			"executing and every later publication fails the same way at the same place -- the " +
			"leader compiles, publishes and writes objects normally the whole time, and what a reader " +
			"sees is that changes made in the source stop taking effect. " +
			"Read failure by reason: activation_record_missing is the activation and the open Segments " +
			"disagreeing about which Plans exist; segment_conflict is an open Segment not in the state " +
			"the cutover requires; digest_mismatch is stored content that does not hash to its name; " +
			"conflict is losing a compare-and-set, which is expected occasionally and clears itself; " +
			"unavailable is content that is not stored or has expired; invalid_request is being asked " +
			"for something that is not a cutover; io is the store failing underneath. " +
			"other must stay at zero: every failure the cutover can return is named above, so a " +
			"non-zero other is a failure path that was added without a name -- which is the state this " +
			"family was created out of. Every label exists from startup, and a sustained non-zero on " +
			"any reason but conflict means the fleet is frozen on the content it already had. " +
			"Reported by the leader only.",
	}, []string{"result", "reason"})
	metrics.queryFreeCompletions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "query_free_completion_total",
		Help: "Slots finished without querying, by kind and by what an earlier attempt at the same Slot " +
			"was found to have done. STATE_APPLIED means every due Plan was already evaluated and " +
			"alerted and only the bookkeeping was lost, which is not a gap; MIXED means some of them " +
			"were, which still is; NONE_FOUND means no earlier attempt got that far; UNREADABLE means " +
			"the record could not be read, which is not the same as nothing being there; ABSENT means " +
			"this runtime is not recording evidence at all. The five add up to the query-free " +
			"completions, so a reader can check the partition rather than assume it. Read a rising " +
			"{GAP_SKIPPED, STATE_APPLIED} beside a flat {GAP_SKIPPED, NONE_FOUND} as the defect this " +
			"exists for; during a rollout NONE_FOUND also covers Slots an older build wrote, so it " +
			"says nothing until every replica is on this version.",
	}, []string{"kind", "evidence"})
	metrics.executionEvidenceWrites = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "execution_evidence_written_total",
		Help: "Marks left by an attempt that wrote state and then could not write its Slot down, by " +
			"result. It is the only sign the mark-writing works: nothing downstream fails when it " +
			"does not, so without this a deployment where every such write fails looks exactly like " +
			"one that never needed a mark. A failure here does not fail the Slot -- it means a later " +
			"query-free completion will have no evidence and record a gap it does not owe.",
	}, []string{"result"})
	for _, reason := range controlplane.CutoverReasons {
		metrics.scheduleCutovers.WithLabelValues("failure", reason)
	}
	metrics.scheduleCutovers.WithLabelValues("success", "")
	for _, decision := range observability.ScheduleCutoverDecisions {
		metrics.scheduleCutoverQueryGroups.WithLabelValues(decision)
	}
	metrics.replayExpiries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "replay_expired_total",
		Help: "Slots the scheduler gave up replaying, by reason. REPLAY_AGE_EXCEEDED and " +
			"REPLAY_DISTANCE_EXCEEDED are ordinary: the Slot is older than the replay window, or too " +
			"many grid points have passed since it. REPLAY_RANGE_EXPIRED is a persisted range of such " +
			"Slots being finalized after a restart or handoff. " +
			"REPLAY_WAIT_EXCEEDS_DISTANCE is a defect report and must stay at zero: the Slot was " +
			"still inside its replay window and the readiness rule would have held the read until " +
			"after that window closed, so the replay would have been dispatched, made to wait, and " +
			"then abandoned for being late. It means the settling wait and the replay window have " +
			"been derived from settings that disagree, and every Slot of that period which misses " +
			"its live deadline will be skipped for as long as they do. Read with " +
			"short_period_completion_total{completion_kind=\"GAP_SKIPPED\"}: this counter says which " +
			"of the skipped Slots were skipped by a rule rather than by falling behind.",
	}, []string{"reason"})
	for _, reason := range observability.ReplayExpiryReasons {
		metrics.replayExpiries.WithLabelValues(reason)
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
	metrics.rebalancePlannedMoves = newLoadedGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "rebalance_planned_moves", Help: "Assignments the latest rebalance round on this Control Leader planned to move from the most to the least loaded ready worker. Read beside assignment_moves_total: planned and not published for more than one stabilisation window is a ready set that keeps changing. Meaningful on the Control Leader only; aggregate replicas with max, not sum."})
	metrics.rebalanceGap = newLoadedGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "rebalance_gap", Help: "Owned Query Groups on the most loaded ready worker minus those on the least loaded, as the latest rebalance round on this Control Leader saw them. Zero is even; a gap that stays above five percent of the even share across rounds is a writer that is not moving. Meaningful on the Control Leader only; aggregate replicas with max, not sum."})
	metrics.assignmentMoves = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "assignment_moves_total", Help: "Assignments the Control Leader moved to another ready worker, by reason. reason=rebalance is a move to even the owned counts out; each costs the Query Group at most one Slot on the old holder."}, []string{"reason"})
	metrics.rebalancePaused = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "rebalance_paused_total", Help: "Rebalance rounds that planned moves and published none, by reason. reason=set_unstable is the ready set having changed within the stabilisation window, which is what a rolling update or a replica joining looks like; rising without end is a set that never settles."}, []string{"reason"})
	metrics.assignmentMoves.WithLabelValues("rebalance")
	metrics.rebalancePaused.WithLabelValues("set_unstable")
	metrics.controlReadRoundTrips = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "control_read_round_trips_total", Help: "Redis round trips the Control Leader's reconcile round spent reading the records it places Query Groups from, by read: assignment (one Assignment record per Query Group of the population) or registry (the index plus one registration per ready worker). Counted where the calls are issued, so it is this round's own number and not a share of a client-wide total. Read it against control_read_keys_total on the same read: keys rising while round trips stay flat is the batch doing its job, both rising together is a batch that is not batching."}, []string{"read"})
	metrics.controlReadKeys = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "control_read_keys_total", Help: "Records the Control Leader's reconcile round asked for, by read. The denominator for control_read_round_trips_total; before the reads were batched the two were equal by construction."}, []string{"read"})
	metrics.controlReadDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "control_read_duration_seconds", Help: "Wall time one reconcile round spent inside each control-plane read, by read. Reported for failed rounds too: a round that took far longer than the others is the one most likely to have failed, and leaving those out would drop them from the distribution somebody is looking at them in.", Buckets: controlReadDurationBuckets}, []string{"read"})
	for _, read := range ControlReadKinds {
		metrics.controlReadRoundTrips.WithLabelValues(read)
		metrics.controlReadKeys.WithLabelValues(read)
	}
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
	metrics.levelAbnormal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "level_abnormal_total",
		Help: "Level verdicts of ABNORMAL, by whether the detection window they were reached on was " +
			"full. The trigger decides ABNORMAL before it reads completeness, and the output contract " +
			"pins that order: WARMING and GAPPED history permit only monotonic ABNORMAL. Under N-of-M " +
			"that is sound -- anomalies counted across a hole are a lower bound, so an incomplete window " +
			"never over-fires -- but an alert opened on one cannot close until the window is FULL again, " +
			"and a window that stays short holds it open for ever. window=incomplete is how much alerting " +
			"rides on that; read it against window=full, never alone, because zero of zero and zero of " +
			"ten thousand are different readings. Both series exist from start so zero is a reading. " +
			"This is the number a decision to discard state on leaving the degraded pool is judged " +
			"against: after such a change the incomplete cell should stop moving.",
	}, []string{"window"})
	for _, window := range []string{"full", "incomplete"} {
		metrics.levelAbnormal.WithLabelValues(window)
	}
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
	metrics.platformSettings = newPlatformSettingsCollector()
	metrics.controlSourceRetainedStale = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "control_source_retained_stale_revisions_total",
		Help: "Last-good Plans a Catalog build refused to retain because their persisted facts no longer hold under " +
			"this binary: the current formula derives another revision from them (disposition " +
			"LAST_GOOD_REVISION_STALE) or the current rules no longer accept them (LAST_GOOD_FACTS_INVALID). Within " +
			"one release this is zero by construction; it rises, for every retained Plan at once, when a release " +
			"changes either, and each such Plan leaves the Catalog under its disposition until its document " +
			"compiles again, instead of the whole Catalog failing to build as it did before.",
	})
	metrics.catalogComposition = newCatalogCompositionCollector()
	metrics.seriesAdmission = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "series_admission_total",
		Help: "Access-path admission decisions by filter, outcome and bounded reason.",
	}, []string{"filter", "result", "reason"})
	metrics.cmdbIndexHosts = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "cmdb_host_index_hosts",
		Help: "Hosts in the in-memory CMDB index the target filter decides on.",
	})
	metrics.cmdbIndexServiceInstances = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "cmdb_service_instance_index_instances",
		Help: "Service instances in the in-memory CMDB index the target filter decides on; zero while a series " +
			"names an instance is an instance cache nobody writes, and such series are admitted with the gap named.",
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
	for _, site := range observability.AllStateAlreadyAppliedSites() {
		for _, kind := range observability.AllStateAlreadyAppliedKinds() {
			metrics.stateAlreadyApplied.WithLabelValues(string(site), string(kind))
		}
		for _, kind := range observability.AllStateVersionConflictKinds() {
			metrics.stateVersionConflict.WithLabelValues(string(site), string(kind))
		}
	}
	for _, site := range ownershipRefusalSites {
		for _, refusal := range ownership.RefusalReasons {
			metrics.ownershipRefusals.WithLabelValues(site, refusal)
		}
	}
	for _, outcome := range nodata.SlotOutcomes {
		metrics.noDataSlotPlans.WithLabelValues(string(outcome))
	}
	for _, outcome := range nodata.SlotOutcomes {
		if outcome == nodata.OutcomeEvaluated {
			// A Plan that evaluated has not stalled, so the label would be a
			// combination that cannot happen rather than a zero worth reading.
			continue
		}
		metrics.noDataStalls.WithLabelValues(string(outcome))
	}
	for _, status := range execution.GapScopeStatuses {
		for _, reason := range append(contract.GapScopeReasons(), contract.GapScopeReasonOther) {
			for _, progress := range contract.GapScopeProgressValues {
				metrics.gapGuardScopeRounds.WithLabelValues(string(status), reason, progress)
			}
		}
	}
	for _, outcome := range execution.NoDataWriteOutcomes {
		metrics.noDataMemoryWrites.WithLabelValues(string(outcome))
	}
	for _, shape := range []struct{ result, reason string }{
		{string(observability.ResultSuccess), string(observability.ReasonNone)},
		{string(observability.ResultDegraded), contract.ReasonBackendCapabilityMissing},
		{string(observability.ResultDegraded), contract.ReasonRedisUnavailable},
	} {
		// Pre-created for the same reason as the rest: the reading is the zero,
		// and here it is the success one -- a deployment whose renewals stopped
		// shows a success series that has stopped rising, which an absent series
		// cannot show.
		metrics.noDataMemoryRenewals.WithLabelValues(shape.result, shape.reason)
	}
	for _, kind := range execution.QueryFreeCompletionKinds {
		for _, reading := range execution.ExecutionEvidenceReadings {
			metrics.queryFreeCompletions.WithLabelValues(string(kind), reading)
		}
	}
	for _, result := range []string{string(observability.ResultSuccess), string(observability.ResultDegraded)} {
		metrics.executionEvidenceWrites.WithLabelValues(result)
	}
	for _, stage := range frozenCensusStages {
		// Pre-created for the reading that started this: a replica reporting
		// nothing and a replica with nothing due looked the same.
		metrics.frozenStateCensus.WithLabelValues(stage)
	}
	for _, outcome := range execution.FrozenRenewalOutcomes {
		// Pre-created, because two of the four readings are zeros somebody
		// acts on: missing staying at zero is what says the mechanism has
		// closed the silent loss, and renewed staying at zero on a deployment
		// that freezes series is what says it never ran. An absent series
		// cannot say either.
		metrics.frozenStateRenewals.WithLabelValues(frozenRenewalLabel(outcome))
	}
	for _, representation := range execution.NoDataRepresentations {
		// Pre-created, because the reading this family exists for is a zero:
		// WHOLE_MEMORY reaching zero and staying there is what says every Plan
		// has moved, and a series that is absent rather than zero cannot say
		// the difference between "none left" and "nobody looked".
		metrics.noDataMemoryReads.WithLabelValues(string(representation))
	}
	for _, refusal := range execution.NoDataRefusals {
		// Pre-created, because the reading this family exists for is the zero.
		// A Plan whose memory the store will not take produces no other signal
		// -- it evaluated, it reported, it was counted as evaluated -- so a
		// series that is absent rather than zero leaves a reader unable to say
		// whether nothing was refused or nothing was looking.
		metrics.noDataMemoryRefusals.WithLabelValues(string(refusal.Reason), string(refusal.Record))
	}
	for _, result := range sourceWithheldLineResults {
		metrics.sourceWithheldLines.WithLabelValues(result)
	}
	for _, hop := range observability.NoDataHops {
		metrics.noDataPlansByHop.WithLabelValues(hop)
	}
	for _, state := range controlplane.SegmentContentStates {
		metrics.segmentContent.WithLabelValues(state)
	}
	return metrics
}

// sourceWithheldLineResults is what can happen to one changed object in a
// round: the round named it, or the line budget cut it. A partition, and the
// reason both are pre-created -- dropped is expected to stay at zero, and a
// zero nobody can tell from an absent label says nothing.
var sourceWithheldLineResults = []string{sourceWithheldLineNamed, sourceWithheldLineDropped}

const (
	sourceWithheldLineNamed   = "named"
	sourceWithheldLineDropped = "dropped"
)

func (m phaseTwoMetrics) collectors() []prometheus.Collector {
	return append(append(m.workflow.collectors(), []prometheus.Collector{
		m.shortPeriod.completed, m.shortPeriod.duration, m.shortPeriod.lag,
		m.queryStatus.responses,
		m.queryUnavailable.attributions,
		m.queryCooldown,
		m.slotReadiness.slack, m.slotReadiness.boundary,
		m.slotTiming, m.slotWait,
		m.work, m.busy, m.lastProgress, m.capacity, m.stateWriteReuse, m.stateWriteChange, m.stateAlreadyApplied, m.stateVersionConflict, m.sourceObservations, m.sourceRefreshes, m.sourceCompiles,
		m.sourceReads, m.sourceStrategiesRead, m.sourceChangeSignalAge,
		m.activationFailures, m.unmappedSeverity,
		m.ownedQueryGroups, m.ownershipTransitions, m.ownershipRefusals,
		m.queryAdmission,
		m.noDataSlotPlans, m.noDataStalls, m.noDataMemoryRefusals, m.noDataMemoryWrites, m.gapGuardScopeRounds, m.noDataPlansSeen, m.noDataPlansByHop, m.segmentContent, m.sourceWithheldLines,
		m.activeQGSetCount, m.activeQGSetBytes, m.activeQGSetEncode, m.activeQGSetRedis,
		m.scheduleCutoverPayload, m.scheduleCutoverTimelineMax, m.scheduleTimelineBytes, m.scheduleSegmentsPruned, m.schedulePruneSkipped, m.scheduleCutoverDuration,
		m.scheduleCutovers,
		m.scheduleCutoverQueryGroups, m.scheduleCutoverTimelinesRead, m.replayExpiries,
		m.queryFailures,
		m.objectCatalogObjects, m.objectCatalogRedis, m.objectCatalogManifestBytes, m.objectReads, m.stateGenerationSkew,
		m.legacyMigration, m.legacyMigrationScan, m.legacyMigrationTime,
		m.undrainedDrainingQueryGroups, m.drainingCursorPrunedQueryGroups, m.rebalancePlannedMoves, m.rebalanceGap, m.assignmentMoves, m.rebalancePaused, m.controlReadRoundTrips, m.controlReadKeys, m.controlReadDuration, m.assignmentIndexStaleRounds, m.assignmentIndexWrites, m.assignmentIndexReads, m.assignmentIndexConfirm, m.assignmentRecordReads, m.scheduleCursorAdvances, m.activationHeldQueryGroups, m.activationHeldAgeSecondsMax,
		m.algorithmEvaluations, m.algorithmInputs, m.levelAbnormal, m.recoveryHeld, m.recoveryPastLevelWithoutRecov, m.openAlertGate,
	}...), append(append(append(m.redisCalls.collectors(), m.dueIndex.collectors()...), m.controlFacts.collectors()...),
		m.controlCache, m.dispatchRotation, m.localView, m.viewStream, m.viewClient, m.openAlertSet, m.controlSourceRounds, m.controlSource,
		m.controlSourceRetainedStale, m.platformSettings,
		m.redisPool, m.renewalGate, m.canonicalEncoding, m.legacyPodCache,
		m.seriesAdmission, m.cmdbIndexHosts, m.cmdbIndexServiceInstances, m.hostDisableMonitorStates, m.cmdbIndexAge,
		m.cmdbIndexDegraded, m.catalogComposition, m.noDataMemoryReads, m.noDataMemoryRenewals,
		m.queryFreeCompletions, m.executionEvidenceWrites, m.frozenStateRenewals, m.frozenStateCensus)...)
}

func (m phaseTwoMetrics) observe(observation observability.Observation) {
	m.workflow.observe(observation)
	m.shortPeriod.observe(observation)
	m.queryStatus.observe(observation)
	m.queryUnavailable.observe(observation)
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
	if facts := observation.ControlReads; facts != nil {
		for read, spent := range map[string]struct {
			keys       int
			roundTrips int
			elapsed    float64
		}{
			"assignment": {facts.AssignmentKeys, facts.AssignmentRoundTrips, facts.AssignmentMilliseconds},
			"registry":   {facts.RegistryKeys, facts.RegistryRoundTrips, facts.RegistryMilliseconds},
		} {
			m.controlReadKeys.WithLabelValues(read).Add(float64(spent.keys))
			m.controlReadRoundTrips.WithLabelValues(read).Add(float64(spent.roundTrips))
			m.controlReadDuration.WithLabelValues(read).Observe(spent.elapsed / 1000)
		}
	}
	if facts := observation.Rebalance; facts != nil && observation.Result == observability.ResultSuccess {
		m.rebalancePlannedMoves.Set(float64(facts.PlannedMoves))
		m.rebalanceGap.Set(float64(facts.MostOwned - facts.LeastOwned))
		if facts.PublishedMoves > 0 {
			m.assignmentMoves.WithLabelValues("rebalance").Add(float64(facts.PublishedMoves))
		}
		if facts.Paused {
			m.rebalancePaused.WithLabelValues("set_unstable").Inc()
		}
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
	if facts := observation.ReplayExpiry; facts != nil {
		m.replayExpiries.WithLabelValues(facts.Reason).Inc()
	}
	m.observeSlotWait(observation)
	if facts := observation.ScheduleCutover; facts != nil {
		m.scheduleCutoverDuration.WithLabelValues(facts.Result).Observe(facts.Duration.Seconds())
		m.scheduleCutovers.WithLabelValues(facts.Result, facts.Reason).Inc()
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
	if facts := observation.HistoryCoverage; facts != nil && facts.Abnormal > 0 {
		m.levelAbnormal.WithLabelValues("full").Add(float64(facts.Abnormal - facts.AbnormalOnIncomplete))
		m.levelAbnormal.WithLabelValues("incomplete").Add(float64(facts.AbnormalOnIncomplete))
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
	if observation.Component == observability.ComponentEvaluation &&
		observation.Stage == observability.StageNoDataDecided {
		m.observeNoDataSlot(observation)
		m.observeNoDataStall(observation)
	}
	if facts := observation.NoDataMemoryRefusal; facts != nil {
		m.noDataMemoryRefusals.WithLabelValues(facts.Reason, facts.Record).Inc()
	}
	if facts := observation.NoDataMemoryWrite; facts != nil {
		m.noDataMemoryWrites.WithLabelValues(facts.Outcome).Inc()
	}
	if observation.Stage == observability.StageExecutionEvidenceWritten {
		m.executionEvidenceWrites.WithLabelValues(string(observation.Result)).Inc()
	}
	if observation.Stage == observability.StageProgressCommitted &&
		(observation.ProgressCompletionKind == string(execution.CompletionGapSkipped) ||
			observation.ProgressCompletionKind == string(execution.CompletionSnapshotUnavailable)) {
		// Counted here, unconditionally, for every query-free completion --
		// including the ones carrying no evidence. A counter that only fired
		// when there was something to say would leave a runtime that records
		// nothing looking like one where nothing happened.
		m.queryFreeCompletions.WithLabelValues(
			observation.ProgressCompletionKind, readingOf(observation.ExecutionEvidence),
		).Inc()
	}
	if facts := observation.NoDataMemoryRead; facts != nil {
		m.noDataMemoryReads.WithLabelValues(facts.Representation).Inc()
	}
	if observation.Stage == observability.StageNoDataMemoryRenewed && observation.NoDataMemoryRenewal != nil {
		m.noDataMemoryRenewals.WithLabelValues(
			string(observation.Result), string(observation.ReasonCode),
		).Inc()
	}
	if facts := observation.FrozenStateRenewal; facts != nil {
		m.frozenStateCensus.WithLabelValues(frozenCensusDue).Add(float64(facts.Due))
		m.frozenStateCensus.WithLabelValues(frozenCensusRead).Add(float64(facts.Read))
		m.frozenStateCensus.WithLabelValues(frozenCensusWritten).Add(float64(facts.Written))
		// Added rather than incremented once: one observation carries a whole
		// Slot's outcomes, and a Slot that renewed two hundred keys is not the
		// same event as one that renewed one.
		m.frozenStateRenewals.WithLabelValues(
			frozenRenewalLabel(execution.FrozenRenewalRenewed)).Add(float64(facts.Renewed))
		m.frozenStateRenewals.WithLabelValues(
			frozenRenewalLabel(execution.FrozenRenewalFresh)).Add(float64(facts.Fresh))
		m.frozenStateRenewals.WithLabelValues(
			frozenRenewalLabel(execution.FrozenRenewalMissing)).Add(float64(facts.Missing))
		m.frozenStateRenewals.WithLabelValues(
			frozenRenewalLabel(execution.FrozenRenewalFailed)).Add(float64(facts.Failed))
	}
	if facts := observation.GapProgress; facts != nil {
		m.gapGuardScopeRounds.WithLabelValues(
			facts.Status, contract.NormalizeGapScopeReason(facts.Reason), facts.Progress,
		).Inc()
	}
	// Dispatched on the facts rather than on a component and stage: the hops
	// are reported from the control plane and from the evaluation, and the
	// point of the family is that one reader sees all of them.
	m.observeNoDataCensus(observation)
	if facts := observation.SegmentContent; facts != nil {
		m.segmentContent.WithLabelValues(facts.State).Inc()
	}
	if observation.Component == observability.ComponentControlPlane &&
		observation.Stage == observability.StageSourceWithheld {
		m.observeSourceWithheld(observation)
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
	if site := ownershipRefusalSite(observation); site != "" {
		m.ownershipRefusals.WithLabelValues(site, string(observation.ReasonCode)).Inc()
	}
	if facts := observation.StateWriteReuse; facts != nil && !facts.Empty() {
		for key, count := range facts.Counts {
			m.stateWriteReuse.WithLabelValues(string(key.Class), string(key.Stored)).Add(float64(count))
		}
		for key, count := range facts.ChangeReasons {
			m.stateWriteChange.WithLabelValues(string(key.Reason), string(key.Stored)).Add(float64(count))
		}
	}
	if facts := observation.StateAlreadyApplied; facts != nil && !facts.Empty() {
		for key, count := range facts.Counts {
			m.stateAlreadyApplied.WithLabelValues(string(key.Site), string(observability.NormalizeStateAlreadyAppliedKind(key.Kind))).Add(float64(count))
		}
	}
	if facts := observation.StateVersionConflict; facts != nil && !facts.Empty() {
		for key, count := range facts.Counts {
			m.stateVersionConflict.WithLabelValues(string(key.Site), string(observability.NormalizeStateVersionConflictKind(key.Kind))).Add(float64(count))
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

// The three stages of the series census, in the order a reader walks them.
const (
	frozenCensusDue     = "due"
	frozenCensusRead    = "read"
	frozenCensusWritten = "written"
)

var frozenCensusStages = []string{frozenCensusDue, frozenCensusRead, frozenCensusWritten}

// frozenRenewalLabel is the label one renewal outcome is counted under.
//
// Lower case, unlike the contract value it comes from, because it reads beside
// the other result labels of this subsystem rather than beside the Go
// constant. Derived rather than written out as a second list: a map here would
// be a place for a fifth outcome to be missing from, and the contract already
// owns which outcomes exist.
func frozenRenewalLabel(outcome execution.FrozenRenewalOutcome) string {
	return strings.ToLower(string(outcome))
}

func (m phaseTwoMetrics) observeNoDataSlot(observation observability.Observation) {
	facts := observation.NoDataSlot
	if facts == nil || facts.Plans <= 0 {
		return
	}
	m.noDataSlotPlans.WithLabelValues(facts.Outcome).Add(float64(facts.Plans))
}

// observeNoDataStall counts one Plan the round it stopped, not every round it
// stays stopped.
func (m phaseTwoMetrics) observeNoDataStall(observation observability.Observation) {
	facts := observation.NoDataStall
	if facts == nil || facts.Outcome == "" {
		return
	}
	m.noDataStalls.WithLabelValues(facts.Outcome).Inc()
}

// observeNoDataCensus counts the Plans a Slot found, including none. A Slot
// with no such Plan still reports, because that zero is the reading.
func (m phaseTwoMetrics) observeNoDataCensus(observation observability.Observation) {
	facts := observation.NoDataCensus
	if facts == nil {
		return
	}
	if facts.Hop != "" {
		m.noDataPlansByHop.WithLabelValues(facts.Hop).Add(float64(facts.Plans))
	}
	// The unlabelled counter is the due hop and nothing else, kept because it
	// is what the current read-out asks for. One emission feeds both, so they
	// cannot disagree.
	if facts.Hop == observability.NoDataHopDue {
		m.noDataPlansSeen.Add(float64(facts.Plans))
	}
}

// observeSourceWithheld counts one named line, and the cut the round reported
// on its last line. The drop rides on a line rather than on its own
// observation because there is no round with a drop and no line: the budget
// only cuts what did not fit after it was filled.
func (m phaseTwoMetrics) observeSourceWithheld(observation observability.Observation) {
	facts := observation.SourceWithheld
	if facts == nil {
		return
	}
	m.sourceWithheldLines.WithLabelValues(sourceWithheldLineNamed).Inc()
	if facts.Dropped > 0 {
		m.sourceWithheldLines.WithLabelValues(sourceWithheldLineDropped).Add(float64(facts.Dropped))
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

// ownershipRefusalSites is where a refusal can be met, as the counter's site
// label spells them. One list so the pre-registration and the classifier
// below cannot disagree.
var ownershipRefusalSites = []string{"admission", "state_apply", "lease", "renewal", "control"}

// ownershipRefusalSite classifies an observation for ownership_refusals_total:
// the site when its reason is one of the store's four refusals, "" when it
// is not one to count. The admission check is counted on the fence_checked
// relay and not on the state-side admission line it is relayed from, so one
// refusal is one.
func ownershipRefusalSite(observation observability.Observation) string {
	if !isOwnershipRefusalReason(observation.ReasonCode) {
		return ""
	}
	switch observation.Component {
	case observability.ComponentOwnership:
		switch observation.Stage {
		case observability.StageFenceChecked:
			return "admission"
		case observability.StageLeaseRenewed:
			return "renewal"
		case observability.StageAssignmentAcquired, observability.StageAssignmentLost,
			observability.StageTakeoverStarted, observability.StageTakeoverCompleted:
			return "lease"
		default:
			return "control"
		}
	case observability.ComponentState:
		if observation.Stage == observability.StageStateApplied {
			return "state_apply"
		}
	}
	return ""
}

func isOwnershipRefusalReason(reason observability.ReasonCode) bool {
	for _, refusal := range ownership.RefusalReasons {
		if string(reason) == refusal {
			return true
		}
	}
	return false
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

// readingOf is the counter's word for one completion's evidence.
//
// It delegates rather than switching here: the split between "every Plan" and
// "some of them" is the same split the gap fold turns on, and two copies of it
// would let the page and the counter disagree about which Slots were gaps.
func readingOf(facts *observability.ExecutionEvidenceFacts) string {
	if facts == nil {
		return execution.EvidenceReadingAbsent
	}
	return execution.ReadEvidence(&execution.ExecutionEvidence{
		Kind:         execution.ExecutionEvidenceKind(facts.Kind),
		PlansApplied: facts.PlansApplied, PlansTotal: facts.PlansTotal,
	})
}
