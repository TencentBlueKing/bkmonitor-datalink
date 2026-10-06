// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"math"
	"slices"
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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// splitRoundDispositions is what one round can do with an over-share object,
// and the label set the round family is pre-created with. Written once and
// read by both the pre-creation and the test that counts it, so a
// disposition added in one place cannot be missing from the other.
var splitRoundDispositions = []string{"over_share", "examined", "skipped"}

type phaseTwoMetrics struct {
	workflow                       workflowMetrics
	shortPeriod                    shortPeriodMetrics
	queryStatus                    queryStatusMetrics
	queryUnavailable               queryUnavailableMetrics
	queryCooldown                  *prometheus.CounterVec
	slotReadiness                  slotReadinessMetrics
	slotWait                       *prometheus.HistogramVec
	slotTiming                     *prometheus.HistogramVec
	work                           *prometheus.CounterVec
	busy                           *prometheus.CounterVec
	lastProgress                   *prometheus.GaugeVec
	capacity                       *prometheus.CounterVec
	stateWriteReuse                *prometheus.CounterVec
	stateWriteChange               *prometheus.CounterVec
	stateAlreadyApplied            *prometheus.CounterVec
	stateVersionConflict           *prometheus.CounterVec
	ownershipRefusals              *prometheus.CounterVec
	sourceObservations             *prometheus.CounterVec
	sourceRefreshes                *prometheus.CounterVec
	sourceCompiles                 *prometheus.CounterVec
	sourceReads                    *prometheus.CounterVec
	sourceStrategiesRead           prometheus.Counter
	sourceChangeSignalAge          prometheus.Gauge
	activationFailures             *prometheus.CounterVec
	ownedQueryGroups               *prometheus.GaugeVec
	ownershipTransitions           *prometheus.CounterVec
	queryAdmission                 *prometheus.CounterVec
	noDataSlotPlans                *prometheus.CounterVec
	noDataAbsences                 *prometheus.CounterVec
	targetPlanResolutions          *prometheus.CounterVec
	targetSelectorResolutions      *prometheus.CounterVec
	noDataStalls                   *prometheus.CounterVec
	noDataMemoryRefusals           *prometheus.CounterVec
	noDataMemoryWrites             *prometheus.CounterVec
	gapGuardScopeRounds            *prometheus.CounterVec
	noDataPlansSeen                prometheus.Counter
	noDataPlansByHop               *prometheus.CounterVec
	noDataMemoryReads              *prometheus.CounterVec
	noDataMemoryRenewals           *prometheus.CounterVec
	queryFreeCompletions           *prometheus.CounterVec
	executionEvidenceWrites        *prometheus.CounterVec
	outputEventsByWireFormat       *prometheus.CounterVec
	outputEventsWithoutMessage     *prometheus.CounterVec
	outputEventsByKind             *prometheus.CounterVec
	outputEventsRejected           *prometheus.CounterVec
	outputRejectedStrategyOverflow prometheus.Counter
	outputRejectedStrategies       *boundedLabels
	frozenStateRenewals            *prometheus.CounterVec
	frozenStateCensus              *prometheus.CounterVec
	segmentContent                 *prometheus.CounterVec
	sourceWithheldLines            *prometheus.CounterVec
	activeQGSetCount               prometheus.Gauge
	activeQGSetBytes               prometheus.Gauge
	activeQGSetEncode              *prometheus.HistogramVec
	activeQGSetRedis               *prometheus.HistogramVec
	scheduleCutoverPayload         prometheus.Gauge
	scheduleCutoverTimelineMax     prometheus.Gauge
	scheduleTimelineBytes          prometheus.Histogram
	scheduleSegmentsPruned         prometheus.Counter
	envelopePass                   *prometheus.CounterVec
	retainedShareApproaching       prometheus.Counter
	envelopeApply                  prometheus.Counter
	schedulePruneSkipped           *prometheus.CounterVec
	scheduleCutoverDuration        *prometheus.HistogramVec
	scheduleCutovers               *prometheus.CounterVec
	replayExpiries                 *prometheus.CounterVec
	rangeGateDecisions             *prometheus.CounterVec
	statePreflights                *prometheus.CounterVec
	scheduleCutoverQueryGroups     *prometheus.CounterVec
	scheduleCutoverTimelinesRead   prometheus.Gauge
	// The last successful cutover's exact duration, set with its payload and
	// timelines read so the three describe one cutover; the first
	// successful cutover of this process, which reads every timeline, kept
	// apart and never overwritten; every successful cutover's payload as a
	// distribution. See observeScheduleCutover.
	scheduleCutoverLastDuration   prometheus.Gauge
	scheduleCutoverFirstDuration  prometheus.Gauge
	scheduleCutoverFirstTimelines prometheus.Gauge
	scheduleCutoverPayloadSize    prometheus.Histogram
	// scheduleCutoverFirstSeen is a pointer: the metrics are passed by value,
	// and a flag copied with them would never stay set.
	scheduleCutoverFirstSeen        *atomic.Bool
	queryFailures                   *prometheus.CounterVec
	objectCatalogObjects            *prometheus.CounterVec
	objectCatalogRedis              *prometheus.HistogramVec
	objectCatalogManifestBytes      prometheus.Gauge
	objectCatalogWrittenBytes       *prometheus.CounterVec
	objectReads                     *prometheus.CounterVec
	stateGenerationSkew             *prometheus.CounterVec
	stateCarry                      *prometheus.CounterVec
	legacyMigration                 *prometheus.CounterVec
	legacyMigrationScan             prometheus.Histogram
	legacyMigrationTime             *prometheus.HistogramVec
	undrainedDrainingQueryGroups    *loadedGauge
	drainingCursorPrunedQueryGroups *loadedGauge
	rebalancePlannedMoves           *loadedGauge
	shardUnawareReadyReplicas       *loadedGauge
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
	recoveryBeside                  *prometheus.CounterVec
	levelAbnormal                   *prometheus.CounterVec
	historyCoverageRejected         *prometheus.CounterVec
	historyCoverageUnsummarised     *prometheus.CounterVec
	levelOutcomes                   *prometheus.CounterVec
	dimensionCensusWrites           *prometheus.CounterVec
	splitPlans                      *prometheus.CounterVec
	splitRoundObjects               *prometheus.CounterVec
	shardQueries                    *prometheus.CounterVec
	splitRounds                     prometheus.Counter
	shardabilityPlans               *prometheus.CounterVec
	dimensionCensusValues           *prometheus.CounterVec
	openAlertGate                   *prometheus.CounterVec
	openAlertSet                    *openAlertSetCollector
	activationRebuild               *activationRebuildCollector
	activationBlocked               *activationBlockedCollector
	effectiveClose                  *effectiveCloseCollector
	absentClose                     *absentCloseCollector
	targetScopeClose                *targetScopeCloseCollector
	linkdConsole                    *linkdConsoleCollector
	controlSourceRounds             *prometheus.CounterVec
	strategiesReturnedAfterRemoval  prometheus.Counter
	queryCooldownSaves              *prometheus.CounterVec
	leaderForward                   *prometheus.HistogramVec
	controlSourceRetainedStale      prometheus.Counter
	controlSource                   *controlSourceCollector
	leaderRound                     *leaderRoundCollector
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
	fleetSnapshotBytes              prometheus.Gauge
	fleetViewSnapshotLoads          prometheus.Counter
	fleetViewSnapshotBytes          prometheus.Counter
	retainedPeakCensusGroups        prometheus.Gauge
	retainedPeakCensusOverflow      prometheus.Gauge
	hostDisableMonitorStates        prometheus.Gauge
	unmappedSeverity                *prometheus.CounterVec
	cmdbIndexAge                    *prometheus.GaugeVec
	cmdbIndexDegraded               *prometheus.GaugeVec
	dueIndex                        dueIndexMetrics
	controlFacts                    controlFactsMetrics
	startupDependencyWaits          *prometheus.CounterVec
	liveness                        *livenessCollector
	// catalogComposition reports what the Catalog the leader last built is
	// made of; see catalog_composition.go.
	catalogComposition *catalogCompositionCollector
}

var activeQGSetDurationBuckets = []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 30}

// scheduleCutoverDurationBuckets are the Active Set buckets with 10 and 20
// seconds between 5 and 30: a leader's first cutover reads every timeline
// and lands there, and "under 30 seconds" could not tell a change of it.
var scheduleCutoverDurationBuckets = []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10, 20, 30}

// scheduleCutoverPayloadBuckets run from 1 KiB to 16 MiB by fours: a cutover
// writing only heads is kilobytes, one writing every timeline megabytes.
var scheduleCutoverPayloadBuckets = prometheus.ExponentialBuckets(1024, 4, 8)

// leaderForwardBuckets resolve the forward's own bounds: two seconds for a
// strategy's standing, two and a half for a diagnosis page.
var leaderForwardBuckets = []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 2.5, 5}

// LeaderForwardRoutes and LeaderForwardResults are the closed label values
// of leader_forward_duration_seconds; anything else is recorded as the
// route or result "other" would be, which is not at all.
var (
	LeaderForwardRoutes  = []string{"strategy", "diagnosis"}
	LeaderForwardResults = []string{"answered", "timeout", "refused", "error", "no_leader", "canceled"}
)

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
		targetPlanResolutions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "target_plan_resolution_total",
			Help: "Target plans resolved, once per Plan per Slot, by the composed state the admission filter and " +
				"the absence judgement both read: Complete is every selector answered and every member validated; " +
				"Incomplete is every selector answered with members dropped in validation, so the records of the kept " +
				"members are admitted and absence is not judged; Unavailable is at least one selector that could not " +
				"be resolved, so the other selectors' members are admitted and absence is not judged. A rising " +
				"Unavailable with a steady Complete is one Plan's selector, not the caches.",
		}, []string{"state"}),
		targetSelectorResolutions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "target_selector_resolutions_total",
			Help: "Selectors of target plans resolved, once per selector per Plan per Slot, by kind, state and the " +
				"closed reason behind an Unavailable or Incomplete state: key_missing, json_invalid, " +
				"structure_invalid, model_mismatch, read_failed, stale, index_unavailable, node_missing, " +
				"node_in_other_business, members_dropped, source_unwired, model_representation_unresolved. OKEmpty " +
				"with node_missing is a topology reference to a node the topology cache does not list; OKEmpty with " +
				"node_in_other_business is one whose node is listed but hosts machines under another business only; " +
				"static Unavailable with model_representation_unresolved is a model_inst_id plan whose members the " +
				"host cache knows no host for - a non-host model without a model_match, or a host cache without the " +
				"canonical identity on its records.",
		}, []string{"kind", "state", "reason"}),
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
		noDataAbsences: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "worker_no_data_absences_total",
			Help: "Groups of the Plans that detect no-data, summed over every Slot that judged them, by what " +
				"the round counted each group as. expected is the roster's size and present what arrived; " +
				"absent is the groups tracked and reported absent this round; unavailable the groups a round " +
				"that did not see the whole period could not judge; dropped the series whose dimensions did " +
				"not match the item. expired is the absences the tracking horizon stopped this round and " +
				"suppressed the groups the round met already stopped -- the standing size of what the horizon " +
				"is holding down. Read the last two against each other: expired moving is the horizon acting, " +
				"suppressed is what it has acted on and is still holding; a deployment that switched the " +
				"horizon on and reads zero on both has a horizon nothing reached. Both are counted where each " +
				"group is decided, never by differencing one round's memory against the last, so the round " +
				"that failed to load its memory does not read as a quiet one. Every label is created at " +
				"startup so a zero can be told from a label nothing ever wrote. Read absent + expired + " +
				"suppressed against the fleet page's per-object line, which carries the same counts per " +
				"Plan for the round it last decided.",
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
	metrics.startupDependencyWaits = newStartupDependencyWaits()
	metrics.liveness = newLivenessCollector()
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
	// The state preflight's second pass, split by what it found. Pre-created
	// at zero for every outcome, because this family is read for its zeros:
	// the compatibility read may go when old_representation has been zero
	// across the fleet, and the three defect outcomes are read to confirm they
	// are zero. A label value nobody pre-created is absent, and absent and
	// zero are the two readings this has to keep apart.
	//
	// A counter and not the log line it is also written to: the preflight line
	// is rate-limited like every other workflow stage, so a busy deployment
	// merges most of them away. A sampled line can carry a non-zero -- wait
	// and one appears -- but "zero everywhere, always" cannot be established
	// from a sample at all, and that is the reading the deletion waits on.
	metrics.envelopePass = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem,
		Name: "state_envelope_pass_series_total",
		Help: "Series a state preflight's second pass classified, by what it found. old_representation is the migration " +
			"stock and the only outcome that ends; no_record_yet is a series with no record at all and never ends. " +
			"envelope_corrupt, frame_corrupt_rescued and frame_corrupt_lost are damaged records, not writers, and " +
			"should be zero. The sum is below state_preflight's envelope_reads by the series whose read failed and " +
			"never reached the split."}, []string{"outcome"})
	for _, outcome := range observability.EnvelopePassOutcomes {
		metrics.envelopePass.WithLabelValues(outcome)
	}
	// Completed Slots at or past the threshold of their one-object share of
	// the retained pool. A counter, not a gauge per object: the objects are
	// named on the page and in fleet.get, and a label per Query Group would
	// grow with the fleet. What this answers is whether any Slot on this
	// replica is near the wall at all - a rate above zero - which is the
	// alerting question; the share refusal after it stops a strategy whole.
	metrics.retainedShareApproaching = prometheus.NewCounter(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem,
		Name: "state_retained_share_approaching_slots_total",
		Help: "Completed Slots whose retained bytes reached at least 95 percent of the one-object share of the " +
			"retained pool they were admitted under. A Slot past the share is refused as QG_BUDGET_SHARE_EXCEEDED " +
			"every round and its strategy stops; these are the Slots before that. The objects are listed by name " +
			"under RETAINED_SHARE_APPROACHING on the page and in fleet.get."})
	// The envelope's other consumer: the per-key write path, which reads both
	// keys of every series it writes and which no preflight count can see. A
	// counter for the reason the pass's family is one - its log key is on the
	// state_applied line, which is sampled like every workflow stage and
	// omitted at zero, and "zero for a whole window" is the one reading the
	// deletion waits on. No label, so the series exists at zero from the first
	// scrape.
	metrics.envelopeApply = prometheus.NewCounter(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem,
		Name: "state_envelope_apply_items_total",
		Help: "State writes on the per-key path whose outcome the older envelope representation decided: the record " +
			"the write was classified against came from the envelope, or an unreadable envelope with no frame refused " +
			"it. The envelope can be deleted only when this and state_envelope_pass_series_total{outcome=\"old_representation\"} " +
			"have both stayed at zero for a whole retention window. A write request that fails part-way reports no " +
			"items, so its round is under-counted, never over-counted."})
	metrics.schedulePruneSkipped = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "schedule_prune_skipped_total", Help: "Schedule timelines a cutover left unpruned, by reason."}, []string{"reason"})
	metrics.scheduleCutoverDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "schedule_cutover_duration_seconds", Help: "Publication cutover compare-and-set duration.", Buckets: scheduleCutoverDurationBuckets}, []string{"result"})
	metrics.scheduleCutoverFirstSeen = new(atomic.Bool)
	metrics.scheduleCutoverLastDuration = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "schedule_cutover_last_duration_seconds",
		Help: "Exact duration of the last successful publication cutover, set together with schedule_cutover_payload_bytes and schedule_cutover_timelines_read so the three describe the same cutover."})
	metrics.scheduleCutoverFirstDuration = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "schedule_cutover_first_duration_seconds",
		Help: "Exact duration of this process's first successful publication cutover, which reads every timeline: the full read is decided once per process and not again when leadership is lost and regained, so a later term's first cutover does not read everything and is not this one. Set once and never overwritten; see schedule_cutover_first_timelines_read for whether it has run."})
	metrics.scheduleCutoverFirstTimelines = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "schedule_cutover_first_timelines_read",
		Help: "Timelines this process's first successful publication cutover read; set once with schedule_cutover_first_duration_seconds. Zero means this process has not completed a cutover yet: a successful one reads at least one timeline."})
	metrics.scheduleCutoverPayloadSize = prometheus.NewHistogram(prometheus.HistogramOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "schedule_cutover_payload_size_bytes",
		Help: "Bytes each successful publication cutover sent, as a distribution since the process started; schedule_cutover_payload_bytes is the last one only.", Buckets: scheduleCutoverPayloadBuckets})
	for _, reason := range observability.SchedulePruneSkipReasons {
		metrics.schedulePruneSkipped.WithLabelValues(reason)
	}
	metrics.scheduleCutoverQueryGroups = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "schedule_cutover_query_groups_total", Help: "Query Groups by what a publication cutover did with them: kept (content and contexts unchanged, no write), revised (contexts changed, one output context revision appended), cut (content changed, Segment closed and reopened), legacy_cut (Segment named no content and was cut once), retired, added, blocked (a precondition only a write outside the cutover could break failed; this Query Group keeps its records and is judged again at the next cutover, the rest of the publication goes ahead), reopened (the timeline key was gone; a new one was opened), retired_unwritten (left the publication with a timeline that failed a precondition; retired without writing it)."}, []string{"decision"})
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
	metrics.outputEventsByWireFormat = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "output_events_by_wire_format_total",
		Help: "Events handed to the output sink, by the wire format they were published as: " +
			"python_compatible is the event the Python alert builder reads, standard_raw_event the raw " +
			"event the alert pipeline consumes, _other an event whose word this build does not name or " +
			"that carried none. Counted on every event_acked, the refused batches included -- a batch " +
			"the broker would not take still was what it was -- so read it beside " +
			"event_acked's result for what actually landed. Every format is created at startup: a " +
			"standard_raw_event that reads zero is a deployment where no event went the standard way, " +
			"and it reads zero rather than not at all. The leader's catalog_plans_by_wire_format says " +
			"how many Plans would publish each way; this says how many events did.",
	}, []string{"format"})
	for _, format := range observability.WireFormats {
		metrics.outputEventsByWireFormat.WithLabelValues(format)
	}
	metrics.outputEventsWithoutMessage = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "output_events_without_message_total",
		Help: "Events handed to the output sink that the protocol had no message for, by the event's " +
			"resolved wire format and kind, as the sink decided each. Under python_compatible that is " +
			"every RECOVERY: the Python protocol carries anomaly points and nothing else, so alarmd " +
			"assembles the recovery envelope from the records and the sink drops it. This is the " +
			"number that says how much of that a deployment does, which is what decides whether the " +
			"open-alert gate -- which today runs only for standard_raw_event -- should run for the " +
			"compatible protocol too and stop the envelope before it is built. A counter rather than " +
			"the event_acked line's events_without_message summed: log lines are bounded by the " +
			"emitter's limiter, so a sum over them is a lower bound, and a lower bound cannot say " +
			"'not much'. Every format and kind is created at startup; a kind or format this build does " +
			"not name folds to _other. Read against output_events_by_wire_format_total{format}: the " +
			"difference is what the broker was actually handed.",
	}, []string{"format", "event_kind"})
	for _, format := range observability.WireFormats {
		for _, kind := range observability.OutputEventKinds {
			metrics.outputEventsWithoutMessage.WithLabelValues(format, kind)
		}
	}
	metrics.outputEventsRejected = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "output_events_rejected_total",
		Help: "Events the output sink would not write because of their own content, one each, by the " +
			"rule the event broke and the strategy it was decided for. The rest of the batch is written: " +
			"a refused event takes its own series with it (the series' State stays where it was and the " +
			"next round decides it again, so a refusal that keeps holding counts once a round) and no " +
			"other series. Rules are closed; one this build does not name folds to _other. The strategy " +
			"label holds the first OutputRejectedStrategyLabels strategies refused in this process and " +
			"folds the rest to _other, counted apart in output_events_rejected_strategies_overflow_total. " +
			"Every rule is created at startup with strategy=_other, so zero reads as zero.",
	}, []string{"rule", "strategy"})
	for _, rule := range observability.OutputRejectRules {
		metrics.outputEventsRejected.WithLabelValues(rule, observability.OutputRejectOther)
	}
	metrics.outputRejectedStrategyOverflow = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "output_events_rejected_strategies_overflow_total",
		Help: "Rejected events whose strategy did not get a label of its own on output_events_rejected_total " +
			"because OutputRejectedStrategyLabels strategies already had one. Non-zero means the per-strategy " +
			"split is partial: read which strategies through the event_acked lines.",
	})
	metrics.outputRejectedStrategies = &boundedLabels{limit: OutputRejectedStrategyLabels}
	metrics.outputEventsByKind = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "output_events_by_kind_total",
		Help: "Events handed to the output sink, by the wire format they were published as and their " +
			"kind: output_events_by_wire_format_total split once more, the same events counted at the " +
			"same site. It exists for one question the format alone cannot answer: whether a RECOVERY " +
			"left on the standard raw event line -- after a window that held a series filled, the " +
			"recovery the trigger then decides is an event of this kind under that format, and a " +
			"deployment reading only the format count sees the anomaly and the recovery as one number. " +
			"Read {format=\"standard_raw_event\",event_kind=\"RECOVERY\"} against " +
			"output_events_without_message_total for the same pair: on the standard line every " +
			"recovery becomes a message, so the difference is what the broker was handed. Counted on " +
			"every event_acked, refused batches included. Every format and kind is created at startup; " +
			"an unnamed kind or format folds to _other.",
	}, []string{"format", "event_kind"})
	for _, format := range observability.WireFormats {
		for _, kind := range observability.OutputEventKinds {
			metrics.outputEventsByKind.WithLabelValues(format, kind)
		}
	}
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
	// The word each round that gave up on a Slot puts on its range_gate line,
	// as a series: the log had the thirteen words and the metric had none, so
	// "which refusal is holding the Query Groups that never catch up" could
	// be read from one Slot's line and from no counter. Every outcome from
	// startup, applied included -- the rounds that did reach the builder are
	// the denominator the refusals are read against.
	metrics.rangeGateDecisions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "range_gate_total",
		Help: "Rounds that gave up on a Slot, by what the catch-up path did with it. applied is a " +
			"range built and handed on; every other word is the condition that refused one, the same " +
			"word the round's range_gate_decided line carries as reason_code. Read the refusals " +
			"against applied: a Query Group whose rounds are all refused for one condition is a " +
			"Query Group that never catches up, and this says which condition.",
	}, []string{"outcome"})
	for _, outcome := range observability.RangeGateOutcomes {
		metrics.rangeGateDecisions.WithLabelValues(outcome)
	}
	// The preflight's result and reason, as a series: the log line and the
	// fleet's object row named them per object, and fleet-wide there was
	// only the duration histogram's count, which says how many reads ran and
	// nothing about how they ended. Every cell from startup, so a read that
	// has never timed out reads as zero rather than as an absent family.
	metrics.statePreflights = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "state_preflight_total",
		Help: "Runtime State preflight reads by how they ended: success when every series' state came back; " +
			"degraded when some did not for a retryable reason (STATE_READ_TIMEOUT is this process's own read " +
			"deadline, REDIS_UNAVAILABLE the store not answering); terminal when some cannot be read " +
			"(STATE_CORRUPT, STATE_BUDGET_EXCEEDED); failed when the store refused the request outright. One " +
			"per preflight call, not per series; the series it covered are in worker_work_total{work_kind=\"state_load\"}.",
	}, []string{"result", "reason"})
	for _, result := range observability.StatePreflightResults {
		for _, reason := range observability.StatePreflightReasons {
			metrics.statePreflights.WithLabelValues(string(result), string(reason))
		}
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
	// The bytes behind object_catalog_objects_total{outcome="written"}: what a
	// publication added to the control plane's store. It is the churn rate;
	// the current catalog's objects are renewed and resident on top of it. The count alone could not answer what a longer
	// retention costs or how large a one-off rewrite was; both are bytes.
	metrics.objectCatalogWrittenBytes = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "object_catalog_written_bytes_total",
		Help: "Bytes the Control Leader wrote to the object catalog, by kind: object (Query Group execution objects and output contexts stored for the first time under their digest) and manifest (the manifest of a publication's revision, counted on every successful write, which a revision written again by a new Leader counts twice though the store renewed it). This counts churn: objects the current catalog still references are renewed every round and stay resident, apart from this rate; objects a publication replaced stay for the catalog retention, about this rate times the retention. What the store holds is the current catalog's bytes plus that. Objects stored by a batch whose pipeline then failed as a whole are not counted, so a failed round can undercount."},
		[]string{"kind"})
	for _, kind := range []string{"object", "manifest"} {
		metrics.objectCatalogWrittenBytes.WithLabelValues(kind)
	}
	metrics.objectReads = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "object_read_total", Help: "Catalog object reads by a Worker, by object kind and outcome; for a Segment, whether its Query Group was read by content and if not, why."}, []string{"kind", "result"})
	// Pre-created so that "no skew" reads as zeros, not as an absent family.
	metrics.stateGenerationSkew = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "state_generation_skew_total", Help: "Due Plans whose activation record names a state generation that disagrees with one derived elsewhere: formula (this process compiles the same Plan to another generation than the Control Leader that published it; tolerated, the record's generation governs the Slot; expected while a release rolls, a version mismatch if it persists), record (the record names a generation the Query Group object published with it does not carry; the Slot is refused)."}, []string{"kind"})
	metrics.stateCarry = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "state_generation_carry_total", Help: "History carried across a state generation that moved while its Plan stayed active. scope=plan: the Control Leader's decision per activation (carried: every Level's detection is unchanged, the Plan warms up for one full Slot and its series keep their results; partial and none_*: the Plan warms up whole, as before). scope=series: what a Worker did for each series with no record under the new generation (carried: results with the new detect fingerprint kept; nothing_kept: none were; skipped_active_guard: the old record still guarded holes and the series starts over; old_missing: the old generation held nothing)."}, []string{"scope", "result"})
	for _, scope := range observability.StateCarryScopes {
		for _, result := range observability.StateCarryResults[scope] {
			metrics.stateCarry.WithLabelValues(scope, result)
		}
	}
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
	metrics.shardUnawareReadyReplicas = newLoadedGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "shard_unaware_ready_replicas",
		Help: "Ready replicas whose registration does not declare the strategy-split contract (shard-aware.v1), as the latest " +
			"reconcile round on this Control Leader saw them. A split is published only while this is zero, and a split fleet " +
			"is collapsed to one piece per strategy while it is not. Across a rolling release it goes 0, n, 0; a rollback puts " +
			"the rolled-back replica back on it. Which replicas they are is on /api/health rebalance.shard_aware.unaware. " +
			"Meaningful on the Control Leader only; aggregate replicas with max, not sum."})
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
	// A coverage fact set the observer refused is counted under the rule it
	// broke. Every rule exists from start so that zero is a reading: before
	// this counter the refusal was silent, and a deployment could not say
	// whether the shape had ever occurred, let alone which rule it fell to.
	metrics.historyCoverageRejected = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "history_coverage_rejected_total",
		Help: "Coverage fact sets the observer refused as not describing one run, by the rule they " +
			"broke -- a count that could not have come from counting the same windows, a named window " +
			"that does not add up to its own shortfall, and so on. The refused set leaves no counts " +
			"behind; the row and the log carry the rule in their place. Every rule is created at " +
			"startup, so a zero says the shape has not occurred; a non-zero cell names a producer whose " +
			"counting has drifted from the observer's contract and is the number to read before " +
			"trusting any coverage from that build.",
	}, []string{"rule"})
	for _, rule := range observability.CoverageRejectionRules {
		metrics.historyCoverageRejected.WithLabelValues(string(rule))
	}
	// The series a run handled without summarising a window for them, by the
	// reason it could not. This is the denominator history_coverage's Levels
	// lacks: a run that resumed or could not load most of its series reports a
	// small Levels, and without this counter that reading is the same shape as
	// a small object. Both causes are created at startup, so a zero says the
	// shape has not occurred; a cell that runs with Levels low is the reading
	// that says a round described part of an object rather than all of it.
	metrics.historyCoverageUnsummarised = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "history_coverage_unsummarised_total",
		Help: "Series a run produced Level outcomes for without summarising a detection window, by cause. " +
			"resumed: State was already applied at this Slot's version, so the round was bookkeeping and not an " +
			"evaluation. constrained: State could not be loaded, so there was nothing to evaluate. Read beside " +
			"history_coverage levels -- levels plus these is what the run actually handled, and levels alone is " +
			"not how many Levels the object is watched on.",
	}, []string{"cause"})
	for _, cause := range []string{"resumed", "constrained"} {
		metrics.historyCoverageUnsummarised.WithLabelValues(cause)
	}
	// Level outcomes by kind and, for the two kinds that carry one, by
	// reason. The evaluation line's reason is the Plan's fold - one word for
	// the worst Level - so a Level suppressed by its effective time or held
	// on a warming window had no cell anywhere unless it was that word.
	// Every cell is created at start over the closed lists: the business
	// outcomes with no reason, UNKNOWN and TERMINAL over the observation
	// catalog and other.
	metrics.levelOutcomes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "level_outcome_total",
		Help: "Level outcomes the evaluation concluded, by outcome kind and, for UNKNOWN and TERMINAL, by reason. " +
			"NORMAL, ABNORMAL and RECOVERY carry no reason. UNKNOWN by reason is the reading for a Level that was not " +
			"judged: EFFECTIVE_TIME_INACTIVE is its configured hours, EFFECTIVE_TIME_UNKNOWN a schedule that could not " +
			"be resolved, HISTORY_WARMING a window not yet full, GAP_* a guard. This counts Level outcomes per series " +
			"per evaluation, not alerts and not strategies; read a reason against level_outcome_total summed over " +
			"every cell for its share.",
	}, []string{"outcome", "reason"})
	for _, outcome := range observability.LevelOutcomeKinds {
		if outcome == "UNKNOWN" || outcome == "TERMINAL" {
			for _, reason := range observability.LevelOutcomeReasons() {
				metrics.levelOutcomes.WithLabelValues(outcome, reason)
			}
			continue
		}
		metrics.levelOutcomes.WithLabelValues(outcome, "")
	}
	// What the Leader's split dry run decided, by outcome (decision-020
	// section 4.7.4). One family and one label: "this object was not split"
	// is the answer a reader arrives with, and the reasons behind it call for
	// different actions.
	metrics.splitPlans = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "split_plan_total",
		Help: "Split decisions this Leader reached, by outcome. PLANNED is an object a split was computed " +
			"for and NOT acted on - the planner only reports for now, and the line's split_dry_run says so. " +
			"UNDER_SHARE is the ordinary answer, counted so that 'nothing was planned' can be told from " +
			"'nothing was looked at'. NO_CENSUS is expected for one round after a replica takes an object " +
			"over; standing, it means the census is not being written. VALUE_TOO_HEAVY is the object that " +
			"cannot be cut by matching values at all and needs hashing. TAIL_TOO_LARGE is the census's own " +
			"bound in the way, SKEW_UNREACHABLE a split that would be undone as fast as it was made, " +
			"TOO_FEW_VALUES a dimension too coarse to cut on, CENSUS_STALE a distribution that is no longer " +
			"this object's, and NO_READING a number missing - never read as no pressure.",
	}, []string{"outcome"})
	for _, outcome := range observability.SplitOutcomes() {
		metrics.splitPlans.WithLabelValues(outcome)
	}
	// What each round of the dry run looked at, as opposed to what it decided
	// about any one object. A separate family for a separate subject: a
	// round's counts wearing an object's outcome word is how one label comes
	// to have two meanings.
	metrics.splitRoundObjects = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "split_round_objects_total",
		Help: "Objects each split dry run round met, by what the round did with them. over_share is how " +
			"many the readings put past the share a single object may hold, examined how many a split was " +
			"worked out for, and skipped the rest. Read skipped against over_share: standing skips are not " +
			"a split problem but a round finding far more over-share objects than a split trigger should " +
			"ever name, and the readings to look at then are the pools and the peaks.",
	}, []string{"disposition"})
	for _, disposition := range splitRoundDispositions {
		metrics.splitRoundObjects.WithLabelValues(disposition)
	}
	// Whether the objects a split was planned for could express it. The
	// catalog's own census (shardable_*) says how much of the whole fleet a
	// value list could cut; this says how much of the population that
	// actually needs cutting can be cut, and the two are read together: if
	// the objects over their share are disjunctive far more often than the
	// fleet at large, then value lists miss precisely the strategies the
	// split exists for.
	metrics.shardQueries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "shard_query_total",
		Help: "Planned splits this Leader tried to express as queries, by what the strategy's own query " +
			"allowed. BUILT is a split the queries express. DISJUNCTIVE is the structural one: a condition " +
			"list is flat, so a matcher appended after an 'or' changes what the existing conditions mean, " +
			"and such a strategy cannot be cut by a value list at all - read it against " +
			"catalog_shardability_plans_total{answer=\"disjunctive\"} to see whether the objects that need splitting are the ones value lists " +
			"cannot serve. NOT_STRUCTURED is PromQL, DIMENSION_NOT_QUERYABLE a dimension the query does not " +
			"group by, TOO_MANY_VALUES a matcher past the value bound, NOT_PLANNED and NO_QUERIES nothing " +
			"to build from, and INVALID this build producing facts the query contract refuses. The unit is one " +
			"object per dry-run round: an object that stays over its share is counted again every round, so a " +
			"share of this family is weighted by how long each object stayed, while the catalog family is one " +
			"Plan per publication. Compare the two as shares of their own totals over the same window, and read a " +
			"standing object as many counts, not many objects.",
	}, []string{"outcome"})
	for _, outcome := range observability.ShardQueryOutcomes() {
		metrics.shardQueries.WithLabelValues(outcome)
	}
	// How many rounds the dry run ran, apart from what they found. The round
	// family adds each round's counts, so a round with nothing over its
	// share adds zero to every cell - and a family that stays at zero then
	// reads the same whether the dry run ran every round and found nothing,
	// or never ran. This is the denominator that tells them apart.
	metrics.splitRounds = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "split_rounds_total",
		Help: "Split dry run rounds this Leader ran, one per placement round that reached the dry run. " +
			"Read split_round_objects_total against it: rounds rising with over_share flat is a fleet with " +
			"nothing over its share; rounds flat is a Leader whose placement round never gets that far, or " +
			"a replica that is not the Leader.",
	})
	// The whole catalog counted by whether a value-list split could be
	// expressed for each Plan, once per publication this replica wrote
	// (decision-020 section 4.7.2). A counter rather than a gauge: only the
	// replica that publishes counts, and a gauge pre-created at zero would
	// say "a catalog of no Plans" on every other replica, where a counter at
	// zero says what is true there - this replica counted no publication.
	// Read as a ratio over a window, which is per-publication shares
	// weighted by how often the catalog was published.
	metrics.shardabilityPlans = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "catalog_shardability_plans_total",
		Help: "Plans in each catalog publication this replica wrote, by whether a value-list split could be " +
			"expressed for them. splittable can take a matcher; disjunctive has an 'or' in its own conditions, " +
			"which a flat condition list cannot be cut under; not_structured is PromQL; no_queries carries no " +
			"query facts; unrecognised is an answer this build does not know. The five sum to the Plans " +
			"published, one Plan per publication. Read disjunctive over the sum, against shard_query_total{outcome=\"DISJUNCTIVE\"} " +
			"over the planned splits: the first is the fleet, the second the objects that need splitting.",
	}, []string{"answer"})
	for _, cell := range (observability.ShardabilityFacts{}).Cells() {
		metrics.shardabilityPlans.WithLabelValues(cell.Answer)
	}
	// What the dimension census did, by where its values came from and what
	// the store said (decision-020 section 4.7.3). Two families rather than
	// one: how many censuses were taken is a different question from how
	// much of a strategy they could name, and a reader asking the second
	// needs the overflow beside the named values or the answer is a number
	// with no denominator.
	metrics.dimensionCensusWrites = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "dimension_census_total",
		Help: "Dimension censuses this replica took, by source and by what the store did with them. " +
			"source=round is the ordinary one, counted from the series the round evaluated; source=roster is the " +
			"fallback for a round that saw no series, and its values are an upper bound because the no-data roster " +
			"remembers groups that are gone. status=WRITTEN is stored, REJECTED is refused whole (too large or " +
			"unencodable - never truncated, because a cut census reads like a distribution), RETRYABLE is the store " +
			"not answering. Only candidate Query Groups take one, so a flat zero here is a fleet with no object " +
			"heavy enough to split.",
	}, []string{"source", "status"})
	for _, source := range observability.DimensionCensusSources() {
		for _, status := range observability.DimensionCensusStatuses() {
			metrics.dimensionCensusWrites.WithLabelValues(source, status)
		}
	}
	metrics.dimensionCensusValues = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "dimension_census_values_total",
		Help: "Dimension values the censuses named, and what they could not: kind=named is values carried in the " +
			"census, kind=overflow_values is values the bound left out, kind=overflow_series is the series on those " +
			"values. Read named against overflow_series: a census that names four thousand values while a hundred " +
			"thousand series sit in the overflow is not a distribution a split can be planned from.",
	}, []string{"kind"})
	for _, kind := range []string{"named", "overflow_values", "overflow_series"} {
		metrics.dimensionCensusValues.WithLabelValues(kind)
	}
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
	// Was trigger_recovery_held_total and
	// trigger_recovery_past_level_without_recovery_total. The gate no longer
	// holds on another Level, so a held count would read zero for ever; what
	// is left to read is how often a RECOVERY goes on beside a Level that
	// used to hold it.
	metrics.recoveryBeside = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "trigger_recovery_beside_level_total",
		Help: "RECOVERY records decided while another Level of the same record was in the named state: " +
			"level_unavailable (its state could not be established this round), level_recovering (it read " +
			"NORMAL with a triggering window still inside its recovery span), level_without_recovery (it read " +
			"NORMAL with recovery disabled). Counted by the first such Level in Level order, unavailable and " +
			"recovering before without-recovery. None of these holds the envelope: a RECOVERY is written as its " +
			"own Level's evaluation, and the alert consumer ends only an alert of that severity, recording any " +
			"other as orphaned. The first two are the records that used to wait for that Level; read them " +
			"against the consumer's orphaned count, which they bound together with the open alert set.",
	}, []string{"beside"})
	for _, cause := range []observability.RecoveryGateCause{observability.RecoveryGateLevelUnavailable, observability.RecoveryGateLevelRecovering, observability.RecoveryGateLevelWithoutRecovery} {
		metrics.recoveryBeside.WithLabelValues(string(cause))
	}
	// The recovery gate: does the consumer hold an open alert on the series
	// at all. Every outcome is pre-created so
	// a zero reads as "never happened", and not_configured in particular has
	// to be readable at zero: on a production worker it is the wiring having
	// come apart, and an absent series would hide exactly that.
	metrics.openAlertGate = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "trigger_open_alert_gate_total",
		Help: "RECOVERY records, by what the consumer's open alert set decided: " +
			"passed (an open alert on the series; the envelope went), held_no_open_alert (none; nothing to " +
			"resolve, no envelope), held_fingerprint_unknown (the series identity the consumer keys alerts by " +
			"could not be built; held and named rather than read as absent), not_configured (the evaluation ran " +
			"without a set; the envelope went as before the gate -- on a production worker this is a wiring " +
			"fault), protocol_not_gated (the Plan does not publish the alert consumer's protocol -- the compatibility " +
			"protocol drops RECOVERY at the sink and alarmd's own decision event has no such consumer; the set was " +
			"not asked). " +
			"It counts records per evaluation, not alerts. Which of passed and held_no_open_alert " +
			"dominates says nothing on its own; read it against open_alert_set_mode, because in " +
			"self_maintained mode the set is this process's own knowledge.",
	}, []string{"outcome"})
	for _, outcome := range observability.OpenAlertGateOutcomes {
		metrics.openAlertGate.WithLabelValues(string(outcome))
	}
	metrics.openAlertSet = newOpenAlertSetCollector()
	metrics.activationRebuild = newActivationRebuildCollector()
	metrics.activationBlocked = newActivationBlockedCollector()
	metrics.effectiveClose = newEffectiveCloseCollector()
	metrics.absentClose = newAbsentCloseCollector()
	metrics.targetScopeClose = newTargetScopeCloseCollector()
	metrics.linkdConsole = newLinkdConsoleCollector()
	metrics.strategiesReturnedAfterRemoval = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "catalog_strategy_returned_after_removal_total",
		Help: "Strategies the source listed again after their Plan had already left the Catalog: absent past the " +
			"removal grace, withdrawn, then back. A return inside the grace is not one. Counted by the Control " +
			"Leader's source-set ledger; read it summed over replicas, since only the Leader counts.",
	})
	metrics.queryCooldownSaves = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "query_cooldown_saves_total",
		Help: "Writes of a Query Group's query cooldown pool record, by result: written; superseded (a later " +
			"owner's record is there, so this owner's write was refused -- the successor's pool state stands); " +
			"failed (the runtime store did not take the write, and the pool state it carried is lost to the " +
			"next restart or owner). Written on a change of the pool state only, never per round.",
	}, []string{"result"})
	for _, result := range QueryCooldownSaveResults {
		metrics.queryCooldownSaves.WithLabelValues(result)
	}
	metrics.leaderForward = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "leader_forward_duration_seconds",
		Help: "Requests a replica handed to the Control Leader's listener because it could not answer them itself, " +
			"by route (strategy: one strategy's standing; diagnosis: an environment diagnosis page) and result: " +
			"answered (the Leader replied, whatever its status), timeout (the hop's own bound ran out, which " +
			"includes a Leader still reading what the reply needs), refused (nothing listening at the Leader's " +
			"endpoint), error (any other failure of the hop), no_leader (discovery named none, or none with an " +
			"endpoint), canceled (the reader went away first, so nothing is known of the Leader). The duration is the replica's wait, the Leader's work included. Before this the hop's " +
			"failure was one word on the reply, FORWARD_FAILED, and which of these it was was not kept anywhere.",
		Buckets: leaderForwardBuckets,
	}, []string{"route", "result"})
	// Every pair exists from the start: the question this answers after a
	// release is whether diagnosis/timeout is zero, and a series that does
	// not exist reads as nothing, not as zero.
	for _, route := range LeaderForwardRoutes {
		for _, result := range LeaderForwardResults {
			metrics.leaderForward.WithLabelValues(route, result)
		}
	}
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
	metrics.leaderRound = newLeaderRoundCollector()
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
	// The fleet snapshot this replica publishes, and the snapshots every
	// fleet view read pulls. A view is one MGET over every replica's
	// snapshot on the replica that answers, and it is read on every page
	// load and every native OB invocation; the MGET rode the state store's
	// connection, whose own traffic drowned it -- a load of two hundred
	// views a minute could not be told from the baseline's drift on the
	// per-connection counters. These count only what the fleet store does,
	// so their difference over a window is the views' alone; the gauge is
	// the true size of one replica's snapshot, which the read cost is a
	// multiple of.
	metrics.fleetSnapshotBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "fleet_snapshot_bytes",
		Help: "Bytes of the fleet snapshot this replica last published to the snapshot store. A fleet view on any replica " +
			"reads every replica's snapshot, so one view costs about the sum of this across the fleet.",
	})
	metrics.fleetViewSnapshotLoads = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "fleet_view_snapshot_loads_total",
		Help: "Fleet snapshot store reads this replica made to build a fleet view: one per /api or OB channel request that " +
			"needed the view. Written only by the fleet store, so a difference over a window is the views' alone.",
	})
	metrics.fleetViewSnapshotBytes = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "fleet_view_snapshot_bytes_total",
		Help: "Bytes of fleet snapshots this replica read from the snapshot store to build fleet views. Divided by " +
			"fleet_view_snapshot_loads_total it is the true per-view read size.",
	})
	// The heartbeat's cost census: how many Query Groups it holds a reading
	// for, and how many observations it dropped for being full. The census is
	// bounded far above any owned count and pruned to the roster on every
	// report, so the overflow is zero on a replica anything reports to; it
	// is readable here because a number nobody can read is not a bound.
	metrics.retainedPeakCensusGroups = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "retained_peak_census_groups",
		Help: "Query Groups the heartbeat's retained-peak census holds a reading for on this replica, after the roster pruned it.",
	})
	metrics.retainedPeakCensusOverflow = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "retained_peak_census_overflow",
		Help: "Observations the retained-peak census dropped since the process started because it was full. Non-zero is a " +
			"replica whose census no roster has pruned; read as a counter, published from the census's own count.",
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
	for _, outcome := range observability.NoDataAbsenceOutcomes {
		metrics.noDataAbsences.WithLabelValues(outcome)
	}
	for _, state := range targetplan.ResolutionStates {
		metrics.targetPlanResolutions.WithLabelValues(string(state))
	}
	// The selector cells are created on first observation - three kinds by
	// four states by the reasons is mostly triples that cannot happen - but
	// the ones an operator acts on are created at zero, so a zero there is
	// "has not happened" rather than "nothing ever counted here".
	for _, cell := range [][3]string{
		{targetplan.SelectorKindGroup, string(targetplan.SelectorUnavailable), targetplan.ReasonKeyMissing},
		{targetplan.SelectorKindGroup, string(targetplan.SelectorUnavailable), targetplan.ReasonReadFailed},
		{targetplan.SelectorKindGroup, string(targetplan.SelectorUnavailable), targetplan.ReasonStale},
		{targetplan.SelectorKindGroup, string(targetplan.SelectorIncomplete), targetplan.ReasonMembersDropped},
		{targetplan.SelectorKindTopology, string(targetplan.SelectorUnavailable), targetplan.ReasonIndexUnavailable},
		{targetplan.SelectorKindTopology, string(targetplan.SelectorOKEmpty), targetplan.ReasonNodeMissing},
		{targetplan.SelectorKindTopology, string(targetplan.SelectorOKEmpty), targetplan.ReasonNodeForeign},
		{targetplan.SelectorKindStatic, string(targetplan.SelectorUnavailable), targetplan.ReasonModelUnresolved},
	} {
		metrics.targetSelectorResolutions.WithLabelValues(cell[0], cell[1], cell[2])
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

// observeEnvelopePass records one preflight's second pass, by outcome.
//
// Every outcome has its label pre-created at zero, so a fleet that has
// finished migrating reads as zero rather than as absent -- which is the
// distinction this family exists to make, and the one a log line cannot make
// because the preflight line is sampled.
func (m phaseTwoMetrics) observeEnvelopePass(counts observability.Counts) {
	for outcome, value := range map[string]int64{
		observability.EnvelopePassOldRepresentation: counts.EnvelopeAnswered,
		observability.EnvelopePassNoRecordYet:       counts.NoRecordYet,
		observability.EnvelopePassEnvelopeCorrupt:   counts.EnvelopeCorrupt,
		observability.EnvelopePassFrameCorruptSaved: counts.FrameCorruptRescued,
		observability.EnvelopePassFrameCorruptLost:  counts.FrameCorruptLost,
		observability.EnvelopePassUnclassified:      counts.Unclassified,
	} {
		if value > 0 {
			m.envelopePass.WithLabelValues(outcome).Add(float64(value))
		}
	}
}

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
		m.noDataSlotPlans, m.noDataAbsences, m.targetPlanResolutions, m.targetSelectorResolutions, m.noDataStalls, m.noDataMemoryRefusals, m.noDataMemoryWrites, m.gapGuardScopeRounds, m.noDataPlansSeen, m.noDataPlansByHop, m.segmentContent, m.sourceWithheldLines,
		m.activeQGSetCount, m.activeQGSetBytes, m.activeQGSetEncode, m.activeQGSetRedis,
		m.scheduleCutoverPayload, m.scheduleCutoverTimelineMax, m.scheduleTimelineBytes, m.scheduleSegmentsPruned, m.envelopePass, m.envelopeApply, m.retainedShareApproaching, m.schedulePruneSkipped, m.scheduleCutoverDuration,
		m.scheduleCutovers,
		m.scheduleCutoverQueryGroups, m.scheduleCutoverTimelinesRead, m.scheduleCutoverLastDuration, m.scheduleCutoverFirstDuration,
		m.scheduleCutoverFirstTimelines, m.scheduleCutoverPayloadSize, m.replayExpiries, m.rangeGateDecisions, m.statePreflights,
		m.queryFailures,
		m.objectCatalogObjects, m.objectCatalogRedis, m.objectCatalogManifestBytes, m.objectCatalogWrittenBytes, m.objectReads, m.stateGenerationSkew, m.stateCarry,
		m.legacyMigration, m.legacyMigrationScan, m.legacyMigrationTime,
		m.undrainedDrainingQueryGroups, m.drainingCursorPrunedQueryGroups, m.rebalancePlannedMoves, m.shardUnawareReadyReplicas, m.rebalanceGap, m.assignmentMoves, m.rebalancePaused, m.controlReadRoundTrips, m.controlReadKeys, m.controlReadDuration, m.assignmentIndexStaleRounds, m.assignmentIndexWrites, m.assignmentIndexReads, m.assignmentIndexConfirm, m.assignmentRecordReads, m.scheduleCursorAdvances, m.activationHeldQueryGroups, m.activationHeldAgeSecondsMax,
		m.algorithmEvaluations, m.algorithmInputs, m.levelAbnormal, m.levelOutcomes, m.splitPlans, m.splitRoundObjects, m.shardQueries, m.splitRounds, m.shardabilityPlans, m.dimensionCensusWrites, m.dimensionCensusValues, m.historyCoverageRejected, m.historyCoverageUnsummarised, m.recoveryBeside, m.openAlertGate,
	}...), append(append(append(m.redisCalls.collectors(), m.dueIndex.collectors()...), m.controlFacts.collectors()...),
		m.startupDependencyWaits, m.liveness, m.controlCache, m.dispatchRotation, m.localView, m.viewStream, m.viewClient, m.openAlertSet, m.activationRebuild, m.activationBlocked, m.effectiveClose, m.absentClose, m.targetScopeClose, m.linkdConsole, m.controlSourceRounds, m.strategiesReturnedAfterRemoval, m.queryCooldownSaves, m.leaderForward, m.controlSource, m.leaderRound,
		m.controlSourceRetainedStale, m.platformSettings,
		m.redisPool, m.renewalGate, m.canonicalEncoding, m.legacyPodCache,
		m.seriesAdmission, m.cmdbIndexHosts, m.cmdbIndexServiceInstances, m.hostDisableMonitorStates, m.cmdbIndexAge,
		m.fleetSnapshotBytes, m.fleetViewSnapshotLoads, m.fleetViewSnapshotBytes, m.retainedPeakCensusGroups, m.retainedPeakCensusOverflow,
		m.cmdbIndexDegraded, m.catalogComposition, m.noDataMemoryReads, m.noDataMemoryRenewals,
		m.queryFreeCompletions, m.executionEvidenceWrites, m.outputEventsByWireFormat, m.outputEventsWithoutMessage, m.outputEventsByKind, m.outputEventsRejected, m.outputRejectedStrategyOverflow, m.frozenStateRenewals, m.frozenStateCensus)...)
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
		if facts.ShardAware != nil {
			m.shardUnawareReadyReplicas.Set(float64(len(facts.ShardAware.Unaware)))
		}
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
	if facts := observation.RangeGate; facts != nil {
		// The normalized word: an outcome outside the list has already been
		// folded to unexplained, so the label set is the list and no more.
		m.rangeGateDecisions.WithLabelValues(facts.Outcome).Inc()
	}
	if observation.Component == observability.ComponentState && observation.Stage == observability.StageStatePreflight {
		result, reason := observability.NormalizeStatePreflight(observation.Result, observation.ReasonCode)
		m.statePreflights.WithLabelValues(string(result), string(reason)).Inc()
		m.observeEnvelopePass(observation.Counts)
	}
	if observation.Component == observability.ComponentState && observation.Stage == observability.StageStateApplied &&
		observation.Counts.EnvelopeReadsApply > 0 {
		m.envelopeApply.Add(float64(observation.Counts.EnvelopeReadsApply))
	}
	if observation.Stage == observability.StageSlotCompleted && observation.Err == nil {
		if usage := observation.SlotBudgetUsage; usage != nil &&
			observability.RetainedShareApproaching(usage.RetainedBytes, usage.RetainedShareBytes) {
			m.retainedShareApproaching.Inc()
		}
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
			m.scheduleCutoverLastDuration.Set(facts.Duration.Seconds())
			m.scheduleCutoverPayloadSize.Observe(float64(facts.PayloadBytes))
			if m.scheduleCutoverFirstSeen.CompareAndSwap(false, true) {
				m.scheduleCutoverFirstDuration.Set(facts.Duration.Seconds())
				m.scheduleCutoverFirstTimelines.Set(float64(facts.TimelinesRead))
			}
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
		if facts.Operation == "write" {
			// Objects are counted whenever they were written, success or not:
			// a write that stored them and then failed on the manifest still
			// left them in the store for the retention.
			m.objectCatalogWrittenBytes.WithLabelValues("object").Add(float64(facts.ObjectBytes))
		}
		if facts.Operation == "write" && facts.Result == "success" {
			m.objectCatalogWrittenBytes.WithLabelValues("manifest").Add(float64(facts.ManifestBytes))
			m.objectCatalogManifestBytes.Set(float64(facts.ManifestBytes))
			// Counted on the write that succeeded and on no other: a failed
			// write is retried under the same revision and counted then, so
			// counting the failure too would count that catalog twice.
			if shardability := observation.Shardability; shardability != nil {
				for _, cell := range shardability.Cells() {
					m.shardabilityPlans.WithLabelValues(cell.Answer).Add(float64(cell.Plans))
				}
			}
		}
	}
	if facts := observation.ObjectRead; facts != nil {
		m.objectReads.WithLabelValues(facts.Kind, facts.Result).Inc()
	}
	if facts := observation.StateGenerationSkew; facts != nil {
		m.stateGenerationSkew.WithLabelValues(facts.Kind).Inc()
	}
	if facts := observation.StateCarry; facts != nil && facts.Count > 0 {
		m.stateCarry.WithLabelValues(facts.Scope, facts.Result).Add(float64(facts.Count))
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
	if facts := observation.ShardQuery; facts != nil {
		m.shardQueries.WithLabelValues(facts.Outcome).Inc()
	}
	if facts := observation.SplitRound; facts != nil {
		m.splitRounds.Inc()
		m.splitRoundObjects.WithLabelValues("over_share").Add(float64(facts.OverShare))
		m.splitRoundObjects.WithLabelValues("examined").Add(float64(facts.Examined))
		m.splitRoundObjects.WithLabelValues("skipped").Add(float64(facts.Skipped))
	}
	if facts := observation.SplitPlan; facts != nil {
		// By outcome and nothing else. How many pieces THIS strategy would be
		// cut into is on the line, where it costs one field; as a metric it
		// would be one series per strategy, which is a label set bounded by
		// how many strategies a deployment has - that is, not bounded.
		m.splitPlans.WithLabelValues(facts.Outcome).Inc()
	}
	if facts := observation.DimensionCensus; facts != nil {
		m.dimensionCensusWrites.WithLabelValues(facts.Source, facts.Status).Inc()
		m.dimensionCensusValues.WithLabelValues("named").Add(float64(facts.Values))
		m.dimensionCensusValues.WithLabelValues("overflow_values").Add(float64(facts.OverflowValues))
		m.dimensionCensusValues.WithLabelValues("overflow_series").Add(float64(facts.OverflowSeries))
	}
	if rejected := observation.HistoryCoverageRejected; rejected != nil {
		m.historyCoverageRejected.WithLabelValues(string(rejected.Rule)).Inc()
	}
	if facts := observation.HistoryCoverage; facts != nil {
		if facts.Resumed > 0 {
			m.historyCoverageUnsummarised.WithLabelValues("resumed").Add(float64(facts.Resumed))
		}
		if facts.Constrained > 0 {
			m.historyCoverageUnsummarised.WithLabelValues("constrained").Add(float64(facts.Constrained))
		}
	}
	if facts := observation.HistoryCoverage; facts != nil && facts.Abnormal > 0 {
		m.levelAbnormal.WithLabelValues("full").Add(float64(facts.Abnormal - facts.AbnormalOnIncomplete))
		m.levelAbnormal.WithLabelValues("incomplete").Add(float64(facts.AbnormalOnIncomplete))
	}
	for _, fact := range observation.LevelOutcomes {
		reason := ""
		if fact.Outcome == "UNKNOWN" || fact.Outcome == "TERMINAL" {
			reason = observability.LevelOutcomeReasonLabel(fact.Reason)
		}
		m.levelOutcomes.WithLabelValues(fact.Outcome, reason).Add(float64(fact.Count))
	}
	for _, fact := range observation.RecoveryGates {
		m.recoveryBeside.WithLabelValues(string(fact.Cause)).Add(float64(fact.Records))
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
		m.observeNoDataAbsence(observation)
	}
	if facts := observation.TargetResolution; facts != nil {
		m.observeTargetResolution(facts)
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
	if observation.Stage == observability.StageEventACKed {
		for format, count := range observation.OutputWireFormats {
			m.outputEventsByWireFormat.WithLabelValues(observability.NormalizeWireFormat(format)).Add(float64(count))
		}
		for key, count := range observation.OutputEventKinds {
			m.outputEventsByKind.WithLabelValues(
				observability.NormalizeWireFormat(key.Format), observability.NormalizeOutputEventKind(key.EventKind),
			).Add(float64(count))
		}
		if write := observation.OutputWrite; write != nil {
			for _, rejected := range write.Rejected {
				strategy, labelled := m.outputRejectedStrategies.label(rejected.StrategyID)
				if !labelled {
					m.outputRejectedStrategyOverflow.Inc()
				}
				m.outputEventsRejected.WithLabelValues(observability.NormalizeOutputRejectRule(rejected.Rule), strategy).Inc()
			}
			for _, bucket := range write.WithoutMessageBy {
				m.outputEventsWithoutMessage.WithLabelValues(
					observability.NormalizeWireFormat(bucket.Format), observability.NormalizeOutputEventKind(bucket.EventKind),
				).Add(float64(bucket.Events))
			}
		}
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

// observeTargetResolution counts one Plan's resolution and each of its
// selectors. The labels are closed by the resolver's own lists; a word off
// them lands on other rather than opening a series.
func (m phaseTwoMetrics) observeTargetResolution(facts *observability.TargetResolutionFacts) {
	state := "other"
	for _, known := range targetplan.ResolutionStates {
		if string(known) == facts.State {
			state = facts.State
		}
	}
	m.targetPlanResolutions.WithLabelValues(state).Inc()
	for _, selector := range facts.Selectors {
		kind, selectorState, reason := "other", "other", "other"
		switch selector.Kind {
		case targetplan.SelectorKindStatic, targetplan.SelectorKindGroup, targetplan.SelectorKindTopology:
			kind = selector.Kind
		}
		for _, known := range targetplan.SelectorStates {
			if string(known) == selector.State {
				selectorState = selector.State
			}
		}
		for _, known := range targetplan.SelectorReasons {
			if known == selector.Reason {
				reason = selector.Reason
			}
		}
		m.targetSelectorResolutions.WithLabelValues(kind, selectorState, reason).Inc()
	}
}

func (m phaseTwoMetrics) observeNoDataSlot(observation observability.Observation) {
	facts := observation.NoDataSlot
	if facts == nil || facts.Plans <= 0 {
		return
	}
	m.noDataSlotPlans.WithLabelValues(facts.Outcome).Add(float64(facts.Plans))
}

// observeNoDataAbsence adds one judging Plan's group counts to each cell. The
// zeros are added too, which changes nothing in the counter and everything in
// what a flat zero means: the label was written by a round that counted none.
func (m phaseTwoMetrics) observeNoDataAbsence(observation observability.Observation) {
	facts := observation.NoDataAbsence
	if facts == nil {
		return
	}
	for outcome, count := range map[string]uint64{
		"expected": facts.Expected, "present": facts.Present, "absent": facts.Absent,
		"unavailable": facts.Unavailable, "dropped": facts.Dropped,
		"expired": facts.Expired, "suppressed": facts.Suppressed,
	} {
		m.noDataAbsences.WithLabelValues(outcome).Add(float64(count))
	}
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

// Unload takes the gauge off the scrape until something sets it again. For
// a reading that belongs to a role this process has stopped playing: the
// last value it had is not this process's answer any more, and a series
// that keeps reporting it is worse than one that is absent, because the
// absent one is read as "not this replica" and the stale one is read as a
// current answer - and these gauges are aggregated with max across
// replicas, so one replica's stale number outranks the current Leader's.
func (gauge *loadedGauge) Unload() {
	gauge.loaded.Store(false)
}

func (gauge *loadedGauge) Describe(ch chan<- *prometheus.Desc) {
	gauge.gauge.Describe(ch)
}

func (gauge *loadedGauge) Collect(ch chan<- prometheus.Metric) {
	if gauge.loaded.Load() {
		gauge.gauge.Collect(ch)
	}
}

// ControlLeaderStepDown takes this process's Control Leader readings off the
// scrape: it is not the Leader any more, and what it last saw as one is not
// an answer about the fleet now. Every one of these is a leader-round gauge
// whose HELP says to aggregate replicas with max, which is exactly the
// aggregation a stale value wins.
//
// The per-replica gauges are not here: a replica reports its own view of
// draining Query Groups, its own index staleness, whether it leads or not,
// and those readings stay true.
func (r *Recorder) ControlLeaderStepDown() {
	if r == nil {
		return
	}
	for _, gauge := range []*loadedGauge{
		r.phaseTwo.rebalancePlannedMoves, r.phaseTwo.rebalanceGap, r.phaseTwo.shardUnawareReadyReplicas,
		r.phaseTwo.activationHeldQueryGroups, r.phaseTwo.activationHeldAgeSecondsMax,
	} {
		if gauge != nil {
			gauge.Unload()
		}
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

// AddStrategiesReturnedAfterRemoval counts strategies the source-set ledger
// saw listed again after their Plan had left the Catalog.
// QueryCooldownSaveResults is every result of a pool record write, closed.
var QueryCooldownSaveResults = []string{"written", "superseded", "failed"}

// ObserveQueryCooldownSave counts one pool record write by its result; a
// result outside QueryCooldownSaveResults is dropped rather than creating a
// series.
func (r *Recorder) ObserveQueryCooldownSave(result string) {
	if r == nil || r.phaseTwo.queryCooldownSaves == nil {
		return
	}
	for _, known := range QueryCooldownSaveResults {
		if result == known {
			r.phaseTwo.queryCooldownSaves.WithLabelValues(result).Inc()
			return
		}
	}
}

func (r *Recorder) AddStrategiesReturnedAfterRemoval(n int) {
	if r == nil || r.phaseTwo.strategiesReturnedAfterRemoval == nil || n <= 0 {
		return
	}
	r.phaseTwo.strategiesReturnedAfterRemoval.Add(float64(n))
}

// ObserveLeaderForward records one hop a replica handed to the Control
// Leader, by route and result from the closed lists; a value outside them
// is dropped rather than creating a series.
func (r *Recorder) ObserveLeaderForward(route, result string, duration time.Duration) {
	if r == nil || r.phaseTwo.leaderForward == nil || !slices.Contains(LeaderForwardRoutes, route) || !slices.Contains(LeaderForwardResults, result) {
		return
	}
	r.phaseTwo.leaderForward.WithLabelValues(route, result).Observe(duration.Seconds())
}
