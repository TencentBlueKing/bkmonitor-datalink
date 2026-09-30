// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"context"
	"math"
	"slices"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type Component string
type Stage string
type Result string
type Operation string
type ReasonCode string
type Direction string
type CapacityBudget string
type SourceKind string
type QueryQueueKind string
type AlgorithmFamily string
type AlgorithmDetectorKind string
type AlgorithmEvaluationResult string
type AlgorithmInputName string
type AlgorithmDependencyPoint string
type AlgorithmInputResult string
type SourceRefreshStatus string
type ActivationFailureStage string
type ActivationFailureClass string

const (
	ComponentRuntime        = "runtime"
	ComponentControlPlane   = "source"
	ComponentOwnership      = "router"
	ComponentScheduler      = "scheduler"
	ComponentConsumer       = "consumer"
	ComponentAdapter        = "adapter"
	ComponentCompiler       = "compiler"
	ComponentAccess         = "access"
	ComponentEvaluation     = "evaluation"
	ComponentState          = "state"
	ComponentProgress       = "progress"
	ComponentDetect         = "detect"
	ComponentOutput         = "output"
	ComponentCoverage       = "coverage"
	ComponentResource       = "resource"
	ComponentPythonProducer = "python_producer"
	ComponentOther          = "_other"

	StageConfigLoaded             = "config_loaded"
	StageEffectiveTimeMaintenance = "effective_time_maintenance"
	// StageAbsentStrategyClose is the control leader's difference between the
	// alert link's unrecovered alerts and the strategy snapshot, and the
	// closes it pushes for strategies that no longer exist.
	StageAbsentStrategyClose  = "absent_strategy_close"
	StageLegacyPodCache       = "legacy_pod_cache"
	StageSnapshotRefreshed    = "snapshot_refreshed"
	StageSnapshotUnavailable  = "snapshot_unavailable"
	StageActivationFailed     = "activation_failed"
	StageActiveQGSet          = "active_qg_set"
	StageObjectCatalog        = "object_catalog"
	StageObjectRead           = "object_read"
	StageFrozenPlanGeneration = "frozen_plan_generation"
	StageActivationHold       = "activation_hold"
	// StageStateCarryDecided is the Control Leader deciding what an
	// activation whose state generation moved may carry; StageStateCarried
	// is a Worker carrying it for each series. See StateCarryFacts.
	StageStateCarryDecided      = "state_carry_decided"
	StageStateCarried           = "state_carried"
	StageScheduleCutover        = "schedule_cutover"
	StageLegacyQGMigration      = "legacy_active_qg_migration"
	StageDrainingQGReconciled   = "draining_query_groups"
	StageAssignmentAcquired     = "assignment_acquired"
	StageAssignmentLost         = "assignment_lost"
	StageRebalancePlanned       = "rebalance_planned"
	StageControlReadsSpent      = "control_reads_spent"
	StageAssignmentIndexWritten = "assignment_index_written"
	// StageAssignmentSwept names one sweep of the Assignment records by the
	// Control Leader: how many named retired Query Groups and how many of
	// those it reclaimed (AssignmentSweepFacts).
	StageAssignmentSwept = "assignment_swept"
	// StageViewPublished names one publication of the Control Leader's
	// desired set over the view stream (decision-016): the term revision
	// it produced and how many Workers' views moved. StageViewSession names
	// one Worker's stream opening, being refused or closing, with why
	// (ViewStreamFacts).
	StageViewPublished = "view_published"
	StageViewSession   = "view_session"
	// StageViewInstalled names one install of a view by a Worker: the
	// version, how many entries it holds and how many of their objects the
	// Worker cannot read (ViewStreamFacts).
	StageViewInstalled          = "view_installed"
	StageAssignmentIndexRead    = "assignment_index_read"
	StageTakeoverStarted        = "takeover_started"
	StageTakeoverCompleted      = "takeover_completed"
	StageLeaseRenewed           = "lease_renewed"
	StageFenceChecked           = "fence_checked"
	StageScheduleDue            = "schedule_due"
	StageSlotStarted            = "slot_started"
	StageSlotCompleted          = "slot_completed"
	StageRunnerCompleted        = "runner_completed"
	StageSlotSourceCompleted    = "slot_source_completed"
	StageScheduleCursorAdvanced = "schedule_cursor_advanced"
	StageReplayExpired          = "replay_expired"
	StageRangeDistanceExpired   = "range_distance_expired"
	StageRangeGateDecided       = "range_gate_decided"
	StageSlotWait               = "slot_wait"
	StageQueryAdmission         = "query_admission"
	StageRestartRecovered       = "restart_recovered"
	StageFleetSnapshotPublish   = "fleet_snapshot_publish"
	StageObservationWindow      = "observation_window"
	StageKafkaAssigned          = "kafka_assigned"
	StageExecutionReceived      = "execution_received"
	StageOffsetGap              = "offset_gap"
	StageOffsetMarked           = "offset_marked"
	StageMessageDecoded         = "message_decoded"
	StageRecordBatchReady       = "record_batch_ready"
	StageRejected               = "rejected"
	StagePlanCompiled           = "plan_compiled"
	StageQueryCompleted         = "query_completed"
	StageQueryBudgetResolved    = "query_budget_resolved"
	StageSlotReadinessArrival   = "slot_readiness_arrival"
	StageStatePreflight         = "state_preflight"
	StageGapLoaded              = "gap_loaded"
	StageNoDataDecided          = "no_data_decided"
	// StageTargetResolved names what one Plan's target plan resolved to in
	// one Slot: the state the admission filter and the absence judgement
	// both read, and each selector's answer by name (decision-017).
	StageTargetResolved = "target_resolved"
	StageSourceWithheld = "source_withheld"
	// StageNoDataSuspended names a strategy that is being evaluated and whose
	// absence detection is not. It is deliberately not source_withheld: that
	// stage means the strategy is not running, and a reader who has learned
	// to treat those lines as outages would read these the same way.
	StageNoDataSuspended = "no_data_suspended"
	// StageGapGuardProgress names one gap scope's standing at the moment a
	// round read it: how far its release condition has got, and why it is
	// held. Reported every round rather than on change, because the state it
	// exists to show is a count that is not moving.
	StageGapGuardProgress = "gap_guard_progress"
	// StageNoDataMemoryRefused names a Plan whose absence memory the store
	// would not take. The round itself was fine: it judged, it reported, its
	// threshold results were sent. What it could not do is write down what it
	// learned, so the next round reads a memory one round old and every round
	// after that does the same.
	//
	// Its own stage rather than an outcome of the no-data partition, because
	// the Plan already has an outcome -- it was evaluated -- and a second one
	// would make the partition stop adding up.
	StageNoDataMemoryRefused = "no_data_memory_refused"
	// StageNoDataMemoryWritten names what became of a Plan's absence-memory
	// write when the store did not refuse it deterministically. Together with
	// the refusals it is every mutation the store was asked for.
	//
	// It exists because "no refusals lately" is not an answer to "is this
	// Plan's memory being kept". A write that lost a race stores nothing just
	// as a refused one does, and a reader with only the refusal lines cannot
	// tell a Plan that recovered from one that started losing races instead.
	StageNoDataMemoryWritten = "no_data_memory_written"
	// StageNoDataMemoryRead names which of the two stored shapes one Plan's
	// memory was read from.
	//
	// It is the only signal that says how far the change of representation has
	// got. Every other one looks the same either way -- the memory is read, the
	// Plan evaluates, the write goes through -- and the count of Plans still on
	// the old record is what the one-shot cleanup waits for. Without it that
	// wait is somebody's guess about how long a rollout takes.
	StageNoDataMemoryRead = "no_data_memory_read"
	// StageNoDataMemoryRenewed names one renewal of a Plan's absence-memory
	// key that actually reached the store.
	//
	// Under the per-group representation a Plan whose groups are steady writes
	// nothing, so renewal on the read is the only thing keeping its memory
	// alive. That makes a renewal that stopped working the failure this
	// representation can have that the old one could not, and it has no other
	// signal: not the write family, which is correctly silent for such a Plan,
	// and not the memory itself, which reads fine right up until it is gone.
	StageNoDataMemoryRenewed = "no_data_memory_renewed"
	// StageSplitPlanned is the Leader deciding what splitting one object
	// would look like, or why it cannot be split. A dry run for now: the
	// stage exists so the decision can be read on the objects it would
	// actually be taken on, before anything acts on it.
	StageSplitPlanned = "split_planned"
	// StageDimensionCensus is a Slot leaving one candidate Plan's dimension
	// census behind (decision-020 section 4.7.3): what its series look like
	// along each dimension, which is what a split is planned from.
	StageDimensionCensus = "dimension_census"
	// StageFrozenStateRenewed names the renewal of the Runtime State keys of
	// the series a Slot read and did not write.
	//
	// The same shape of failure as the no-data memory above, on the other kind
	// of key: a frozen series writes nothing, so nothing refreshes its life,
	// and its state is deleted while the Plan is still evaluating it every
	// minute. Its MISSING reading is the first signal that names that loss --
	// before it, the loss either took a Slot's whole round as a version
	// conflict or went entirely uncounted.
	StageFrozenStateRenewed  = "frozen_state_renewed"
	StageEvaluationCompleted = "evaluation_completed"
	StageSideEffectAdmission = "side_effect_admission"
	StageStateAdmission      = "state_admission"
	StageGapGuardCommitted   = "gap_guard_committed"
	StageMutationCompared    = "mutation_compared"
	StageEventACKed          = "event_acked"
	StageStateApplied        = "state_applied"
	StageProgressCommitted   = "progress_committed"
	// StageExecutionEvidenceWritten names the mark an attempt leaves when it
	// wrote state and then could not write the Slot down. It is the only sign
	// that the mark-writing works at all: nothing downstream fails when it does
	// not, so without this a deployment where every such write fails looks
	// exactly like one where none was ever needed.
	StageExecutionEvidenceWritten = "execution_evidence_written"
	StageDependencyLoaded         = "dependency_loaded"
	StageStateCommitted           = "state_committed"
	StageDetectCompleted          = "detect_completed"
	StageTriggerCompleted         = "trigger_completed"
	StageOutputACKed              = "output_acked"
	StageCoverageCompleted        = "coverage_completed"
	StageCoverageGap              = "coverage_gap"
	StageResourceSoft             = "resource_soft"
	StageResourceHard             = "resource_hard"
	StageResourceResumed          = "resource_resumed"
	StagePythonSource             = "source"
	StagePythonBuilt              = "built"
	StagePythonEnqueued           = "enqueued"
	StagePythonPublished          = "published"
	StagePythonACKed              = "acked"
	StagePythonDropped            = "dropped"
	StageOther                    = "_other"

	ResultTerminal = "terminal"
	ResultRetrying = "retrying"
	ResultPaused   = "paused"
	ResultResumed  = "resumed"
	ResultDegraded = "degraded"
	ResultOther    = "_other"

	ActivationFailureStageActivationLoad  ActivationFailureStage = "activation_load"
	ActivationFailureStageCandidateLoad   ActivationFailureStage = "candidate_load"
	ActivationFailureStageCurrentRecovery ActivationFailureStage = "current_recovery"
	ActivationFailureStageReactivation    ActivationFailureStage = "reactivation"
	ActivationFailureStageCompile         ActivationFailureStage = "compile"
	ActivationFailureStageScheduleCutover ActivationFailureStage = "schedule_cutover"
	ActivationFailureStagePersist         ActivationFailureStage = "persist"

	ActivationFailureClassUnavailable        ActivationFailureClass = "unavailable"
	ActivationFailureClassCorrupt            ActivationFailureClass = "corrupt"
	ActivationFailureClassEpochCollision     ActivationFailureClass = "epoch_collision"
	ActivationFailureClassNotDrained         ActivationFailureClass = "not_drained"
	ActivationFailureClassProjectionConflict ActivationFailureClass = "projection_conflict"
	ActivationFailureClassScheduleConflict   ActivationFailureClass = "schedule_conflict"
	ActivationFailureClassCoverageConflict   ActivationFailureClass = "coverage_conflict"
	ActivationFailureClassCASConflict        ActivationFailureClass = "cas_conflict"
	ActivationFailureClassDependencyIO       ActivationFailureClass = "dependency_io"
	ActivationFailureClassOther              ActivationFailureClass = "other"

	OperationNone               = "none"
	OperationCompile            = "compile"
	OperationCacheHit           = "cache_hit"
	OperationCacheMiss          = "cache_miss"
	OperationCacheEvict         = "cache_evict"
	OperationRequirementCompile = "requirement_compile"
	OperationLoad               = "load"
	OperationDecode             = "decode"
	OperationEncode             = "encode"
	OperationWrite              = "write"
	OperationConsume            = "consume"
	OperationProduce            = "produce"
	OperationACK                = "ack"
	OperationCommit             = "commit"
	OperationOffsetRepair       = "offset_repair"
	OperationSample             = "sample"
	OperationTransition         = "transition"
	OperationNormal             = "normal"
	OperationRetry              = "retry"
	OperationReplay             = "replay"
	OperationProbe              = "probe"
	OperationOther              = "_other"

	DirectionInput    Direction = "input"
	DirectionOutput   Direction = "output"
	DirectionInternal Direction = "internal"
	DirectionOther    Direction = "_other"

	CapacityBudgetSeries         CapacityBudget = "series"
	CapacityBudgetRetainedBytes  CapacityBudget = "retained_bytes"
	CapacityBudgetStateMutations CapacityBudget = "state_mutations"
	CapacityBudgetEvents         CapacityBudget = "events"
	CapacityBudgetGapMutations   CapacityBudget = "gap_mutations"
	CapacityBudgetOther          CapacityBudget = "other"

	SourceKindLegacyStrategy   SourceKind = "legacy_strategy"
	SourceKindCompiledSnapshot SourceKind = "compiled_snapshot"

	QueryQueueNormal   QueryQueueKind = "normal"
	QueryQueueRecovery QueryQueueKind = "recovery"

	AlgorithmFamilyThreshold          AlgorithmFamily = "threshold"
	AlgorithmFamilySimpleRingRatio    AlgorithmFamily = "simple_ring_ratio"
	AlgorithmFamilyOsRestart          AlgorithmFamily = "os_restart"
	AlgorithmFamilyProcPort           AlgorithmFamily = "proc_port"
	AlgorithmFamilyPingUnreachable    AlgorithmFamily = "ping_unreachable"
	AlgorithmFamilySimpleYearRound    AlgorithmFamily = "simple_year_round"
	AlgorithmFamilyAdvancedRingRatio  AlgorithmFamily = "advanced_ring_ratio"
	AlgorithmFamilyAdvancedYearRound  AlgorithmFamily = "advanced_year_round"
	AlgorithmFamilyRingRatioAmplitude AlgorithmFamily = "ring_ratio_amplitude"
	AlgorithmFamilyYearRoundAmplitude AlgorithmFamily = "year_round_amplitude"
	AlgorithmFamilyYearRoundRange     AlgorithmFamily = "year_round_range"

	AlgorithmDetectorKindThreshold          AlgorithmDetectorKind = "Threshold"
	AlgorithmDetectorKindSimpleRingRatio    AlgorithmDetectorKind = "SimpleRingRatio"
	AlgorithmDetectorKindOsRestart          AlgorithmDetectorKind = "OsRestart"
	AlgorithmDetectorKindProcPort           AlgorithmDetectorKind = "ProcPort"
	AlgorithmDetectorKindSimpleYearRound    AlgorithmDetectorKind = "SimpleYearRound"
	AlgorithmDetectorKindAdvancedRingRatio  AlgorithmDetectorKind = "AdvancedRingRatio"
	AlgorithmDetectorKindAdvancedYearRound  AlgorithmDetectorKind = "AdvancedYearRound"
	AlgorithmDetectorKindRingRatioAmplitude AlgorithmDetectorKind = "RingRatioAmplitude"
	AlgorithmDetectorKindYearRoundAmplitude AlgorithmDetectorKind = "YearRoundAmplitude"
	AlgorithmDetectorKindYearRoundRange     AlgorithmDetectorKind = "YearRoundRange"

	AlgorithmEvaluationResultNormal      AlgorithmEvaluationResult = "normal"
	AlgorithmEvaluationResultAbnormal    AlgorithmEvaluationResult = "abnormal"
	AlgorithmEvaluationResultRecovery    AlgorithmEvaluationResult = "recovery"
	AlgorithmEvaluationResultUnavailable AlgorithmEvaluationResult = "unavailable"
	AlgorithmEvaluationResultTerminal    AlgorithmEvaluationResult = "terminal"

	AlgorithmInputNamePrimary AlgorithmInputName = "primary"
	AlgorithmInputNameHistory AlgorithmInputName = "history"

	AlgorithmDependencyPointCurrent          AlgorithmDependencyPoint = "current"
	AlgorithmDependencyPointPrevious         AlgorithmDependencyPoint = "previous"
	AlgorithmDependencyPointTenMinute        AlgorithmDependencyPoint = "ten_minute"
	AlgorithmDependencyPointTwentyFiveMinute AlgorithmDependencyPoint = "twenty_five_minute"
	// Historical offsets stay in provenance; the metric label remains bounded.
	AlgorithmDependencyPointHistorical AlgorithmDependencyPoint = "historical"

	AlgorithmInputResultAvailable   AlgorithmInputResult = "available"
	AlgorithmInputResultMissing     AlgorithmInputResult = "missing"
	AlgorithmInputResultPartial     AlgorithmInputResult = "partial"
	AlgorithmInputResultUnavailable AlgorithmInputResult = "unavailable"

	SourceRefreshPending   SourceRefreshStatus = "PENDING_CONFIRMATION"
	SourceRefreshPublished SourceRefreshStatus = "PUBLISHED"
	SourceRefreshUnchanged SourceRefreshStatus = "UNCHANGED"
	SourceRefreshConflict  SourceRefreshStatus = "PUBLICATION_CONFLICT"

	ReasonNone                                ReasonCode = "none"
	ReasonStateAlreadyAppliedBeforeEvaluation ReasonCode = "state_already_applied_before_evaluation"
	// ReasonInternalUnknown is chosen by a site that has looked at the failure
	// and has nothing finer to say about it.
	ReasonInternalUnknown ReasonCode = "internal_unknown"
	// ReasonNotReported is not chosen by anyone: it is what a failing
	// observation gets when its emitting site reported no reason at all.
	//
	// The two used to be the same value, and they are opposite kinds of fact. A
	// site that says "unknown" has done its job; a site that says nothing is a
	// defect at that site, and the set of such sites is finite - which makes
	// this the value a gate can be written against. Filling in mappings one at
	// a time never ends; "nobody reported one" is a state that can reach zero
	// and be asserted to stay there.
	ReasonNotReported           ReasonCode = "reason_not_reported"
	ReasonCPU                   ReasonCode = "resource_cpu"
	ReasonRSS                   ReasonCode = "resource_rss"
	ReasonHeap                  ReasonCode = "resource_heap"
	ReasonGC                    ReasonCode = "resource_gc"
	ReasonWorkerQueue           ReasonCode = "resource_worker_queue"
	ReasonInflight              ReasonCode = "resource_inflight"
	ReasonConsumerLag           ReasonCode = "resource_consumer_lag"
	ReasonStateBytes            ReasonCode = "resource_state_bytes"
	ReasonContractDeterministic ReasonCode = "contract_deterministic"
	ReasonContractRetryable     ReasonCode = "contract_retryable"
	ReasonContractCoverage      ReasonCode = "contract_coverage"
	ReasonOther                 ReasonCode = "_other"
)

// DurationOnlyStages are the stages whose records carry a duration and nothing
// else. They are emitted by a timing wrapper that stamps every record success,
// deliberately: the failures on those paths report themselves on their own
// records, with their own reasons, so a duration record has no outcome to add.
//
// The list is here, beside the stage names, because two places have to agree
// with it and neither of them is where the records are produced. The object
// page renders a trace and was printing that success stamp as an outcome, so a
// round blocked inside the Slot source showed "fetched what to compute:
// success" directly above the record that said it was blocked -- a reader
// following the trace went one stage past the answer.
//
// The page held its own copy of this list, which is the arrangement that goes
// stale the first time a third timing call is added: the new stage would print
// as an outcome again, with nothing failing. Both sides are checked against
// this one instead.
var DurationOnlyStages = []string{
	StageSlotSourceCompleted,
	StageRunnerCompleted,
}

type ComponentStage struct {
	Component Component
	Stage     Stage
}

type Counts struct {
	Messages   int64
	Records    int64
	Plans      int64
	Levels     int64
	Events     int64
	Bytes      int64
	Keys       int64
	StateBytes int64
	// EnvelopeReads is how many of a state preflight's series had to be read
	// from the older representation as well, because the framed record could
	// not answer for them. It is the second pass's cost, and it is not the
	// migration indicator: a series with no record at all has no frame, so it
	// lands here for ever.
	EnvelopeReads int64
	// The four EnvelopeReads splits into; see execution.StatePreflightResult
	// for the table. EnvelopeAnswered is the only one that ends, and reading
	// it as zero is the whole point of having it -- so all four go on the line
	// at every value, zero included. A count that exists to answer "how many"
	// has its answer erased by omitting its zero.
	EnvelopeAnswered    int64
	EnvelopeCorrupt     int64
	NoRecordYet         int64
	FrameCorruptRescued int64
	FrameCorruptLost    int64
	Unclassified        int64
	// StateFetchMillis and StateDecodeMillis split a state preflight's time
	// into the store's reads and this process's decoding of what they
	// returned; see execution.PreflightTiming.
	StateFetchMillis  int64
	StateDecodeMillis int64
	// EnvelopeReadsApply is how many items of a state apply had their outcome
	// decided by the older representation on the per-key write path, which
	// reads both keys and which none of the preflight's counts can see. The
	// other consumer of the envelope beside EnvelopeAnswered: the envelope
	// can be deleted only when both have stayed at zero for a whole retention
	// window, and a sum could not say which one had not.
	EnvelopeReadsApply int64
}

// TargetResolutionFacts is one Plan's target plan resolved for one Slot:
// the composed state, the age of a snapshot served past a failed refresh,
// and every selector's answer by name. It is the one place the reader
// learns why a target plan's Plan admitted what it admitted and whether its
// absence was judged.
type TargetResolutionFacts struct {
	StrategyID string
	State      string
	// StaleAgeSeconds is non-zero when a selector answered from a snapshot
	// kept past a failed refresh: resolved_from_stale_snapshot.
	StaleAgeSeconds int64
	Selectors       []TargetSelectorFacts
}

// TargetSelectorFacts is one selector's answer.
type TargetSelectorFacts struct {
	Kind, ID, State, Reason string
	Kept, Dropped           int
	// NodeMissing marks a topology reference to a node the topology cache
	// does not list; NodeForeign one whose node holds hosts under another
	// business only. Both are carried to the object row as they are.
	NodeMissing bool
	NodeForeign bool
}

// NoDataSlotFacts is what happened to one Plan's no-data detection in one Slot.
//
// Outcome is the whole point. A Plan that detects no-data lands on exactly one
// outcome every Slot, and the three that are not EVALUATED are different kinds
// of "did not judge" that look identical once the round is over: a query that
// did not cover the period, a Slot that could not carry the work, a memory this
// build cannot read. Folding them together loses the one that never resolves
// on its own.
type NoDataSlotFacts struct {
	Outcome string
	// Plans is how many Plans landed on that outcome, so one observation can
	// carry a whole Slot rather than one per Plan.
	Plans int
}

// GapProgressFacts is one gap scope's release condition as a round found it.
//
// The page's sentence is "releasing needs N consecutive complete rounds,
// currently k". Nothing emitted k or N: they lived only inside the persisted
// marker and inside a conflict error, so a strategy sitting at 0 of 5 for
// hours looked from outside exactly like one nobody had looked at. The
// numbers are what turn "this guard is holding" into "this guard has made no
// progress since it was raised".
//
// Reported on every round a marker is read, deliberately not only when it
// changes. A converging guard moves k every round and a stuck one does not,
// and the stuck one is what somebody is looking for -- a changed-only rule
// would say nothing about exactly the case this exists for.
// GapStatementFacts is one Plan's gap marker statements within one Slot, when
// there is more than one of them.
//
// The two lists are reported apart because they are two different defects: a
// statement in each is the accumulation across series batches assembling a
// shape no batch produced, and two in one list is one batch or one merge
// producing two statements for one key. The digests are what let two Slots'
// lines be compared, and the expected revisions are what say whether the
// second statement could ever have applied after the first.
type GapStatementFacts struct {
	// Shape is "across_lists" or "within_one_list".
	Shape string
	// BeforeEvents and AfterState are each list's "digest@expected_revision",
	// in a stable order.
	BeforeEvents []string
	AfterState   []string
	// Statements is how many there were in total.
	Statements int
}

type GapProgressFacts struct {
	// Scope is "plan" or the level id, so the two kinds are not told apart by
	// a zero.
	Scope string
	// Status is GAPPED or WARMING. Only a warming scope is counting up, and a
	// reader shown k/N against a gapped one would read a stalled count where
	// there is no count.
	Status string
	Reason string
	// Required and Observed are N and k.
	Required uint32
	Observed uint32
	// Progress is where k stands against N as a bounded word, so a reader and
	// a metric label agree without either deriving it again.
	Progress string
}

// NoDataStallFacts names one Plan whose no-data detection has stopped rather
// than missed a round: it has skipped noDataPersistentSkipRounds Slots in a
// row, and this is the round it crossed. Reported once per stall, not once per
// round -- a count of rounds is what the outcome buckets already give.
type NoDataStallFacts struct {
	Outcome string
}

// NoDataAbsenceFacts is what one Plan's no-data round decided about its
// groups, on the round it decided: the counts the evaluation reports, and the
// horizon it decided them against.
//
// One line per Plan per Slot that judged, carrying the counts as the round
// counted them at the site of each decision. Nothing here is a difference
// between this round's memory and the last: a difference reads zero in the
// round that failed to load the memory, which is the round most worth
// reporting. Expired is the absences this round stopped tracking; Suppressed
// the groups it met already stopped -- the standing size of what the horizon
// is holding down, which is the number a deployment that switched the horizon
// on has no other way to see. Absent is the groups still tracked and reported
// absent this round. HorizonSeconds is the Plan's effective horizon as
// compilation froze it, zero for none: the same round key that recompiles a
// Plan when the platform's changes is what keeps this current.
type NoDataAbsenceFacts struct {
	Outcome        string
	HorizonSeconds int64
	// HorizonSource is where the horizon came from as compilation froze it
	// beside the number: PLATFORM or STRATEGY, empty for no horizon and for
	// a Plan compiled before the source was frozen.
	HorizonSource string
	RosterSource  string
	Expected      uint64
	Present       uint64
	Absent        uint64
	Unavailable   uint64
	Dropped       uint64
	Expired       uint64
	Suppressed    uint64
	// AbsentAges is Absent by how long each absence has been open at this
	// round -- this round, under an hour, under a day, a day or more -- as
	// the evaluation filed them from the same start the horizon reads. They
	// sum to Absent. This is what says whether a horizon can reach anything:
	// thousands of absences all under an hour old are groups that report
	// every few rounds and reset their clock, which no horizon stops, and
	// the last bucket is what a one-day horizon would.
	AbsentAges NoDataAbsentAges
}

// NoDataAbsentAges is the age buckets of the absences one round reported.
type NoDataAbsentAges struct {
	ThisRound uint64
	UnderHour uint64
	UnderDay  uint64
	DayOrMore uint64
}

// NoDataAbsenceOutcomes is every count the absence line carries, in the order
// the metric creates them, so a label nothing ever wrote can be told from one
// that wrote zero.
var NoDataAbsenceOutcomes = []string{
	"expected", "present", "absent", "unavailable", "dropped", "expired", "suppressed",
}

// NoDataMemoryRefusalFacts is one Plan's refused absence-memory write: why the
// store said no and, when the refusal was about size, the two numbers it
// compared.
//
// The numbers are the point. "This Plan's memory did not fit" is not something
// a reader can act on: a record a little over the bound and one many times it
// are different situations, and a bound that moved under an unchanged record
// is a third. With the measurement on the line, the same reader can see which
// one this is and whether it is getting worse.
// NoDataMemoryWriteFacts is what became of one Plan's absence-memory write.
//
// Outcome is the store's own word for it rather than a success flag, because
// the four that are not APPLIED are four different situations: the record was
// already this round's, a newer one won, another writer got there, or the
// store could not be reached. A page that only knew "stored / not stored"
// would send a reader looking in the same place for all of them.
type NoDataMemoryWriteFacts struct {
	Outcome string
	// Stored says whether the store now holds what this round wanted written.
	// It is carried rather than derived at each reader, so the one place that
	// decides which outcomes count is the one place anybody has to agree with.
	Stored bool
	// Conflict is the comparison that refused the write, and is set only on a
	// conflict.
	//
	// A refusal that names no values is not something anyone can act on. A
	// whole fleet's writes were refused for a day and the line said
	// reason_not_reported: the store knew which comparison failed and what the
	// two sides were, and none of it left the store. The reason code cannot
	// carry it either, because a conflict is not a rejection and has none.
	Conflict *NoDataMemoryConflictFacts
	// DerivedFrom is the record the refused statement was built from. It is
	// what tells a conflict during a rollout -- a Plan still writing from the
	// whole-memory record -- from one between two writers of the same record.
	DerivedFrom string
}

// NoDataMemoryConflictFacts is the comparison a refused memory write lost.
type NoDataMemoryConflictFacts struct {
	// Kind is the store-wide conflict vocabulary, so this line and the runtime
	// state's answer one query rather than two.
	Kind string
	// Persisted and Proposed are the two memory digests. Empty on a conflict
	// about which record exists rather than about what it says.
	Persisted string
	Proposed  string
	// ExpectedRevision is what the statement was derived against and
	// StoredRevision what the record holds. Both are always rendered, because
	// the pair is the comparison: either alone leaves a reader guessing which
	// way it went.
	ExpectedRevision uint64
	StoredRevision   uint64
}

// NoDataMemoryReadFacts says which stored shape one Plan's memory came from.
type NoDataMemoryReadFacts struct {
	// Representation is execution.NoDataRepresentation as text. NONE is a
	// value and not an omission: every load lands on exactly one of the three,
	// so the three add up to the Plans that were asked for, and a reader can
	// check that rather than assume it.
	Representation string
}

// NoDataMemoryRenewalFacts is what one renewal did.
//
// Renewed false is the ordinary case and not a failure: the key had enough life
// left, which is what the threshold is for. The failure is carried by the
// observation's result and reason, not by this flag, so a reader cannot mistake
// a skipped renewal for a broken one.
type NoDataMemoryRenewalFacts struct {
	Renewed    bool
	TTLSeconds int64
}

// ExecutionEvidenceFacts is how far an earlier attempt at a Slot got.
//
// One typed fact rather than three loose fields, so a reader that has it has
// all of it: the kind without the counts cannot tell a fully executed Slot from
// a partly executed one, and the counts without the kind cannot tell zero
// applied from unreadable.
type ExecutionEvidenceFacts struct {
	Kind         string
	PlansApplied int
	PlansTotal   int
}

// OutputRejectionFacts is a refusal to write the round's events, as the sink
// states it: which of its refusals (a converter that could not build the
// message, a client that would not send it) and the sentence that decided
// it. The sentence is the sink's own and carries no identity; the identity
// is on the trace. It travels as facts because the words used to be read
// out of an error chain three wrappers deep, bounded from whichever end
// happened to keep them, and the two refusals were one code with a broker
// that did not answer.
type OutputRejectionFacts struct {
	Reason string `json:"reason"`
	Detail string `json:"detail"`
}

// FrozenStateRenewalFacts is what one Slot's renewal of frozen Runtime State
// keys found.
//
// Frozen is the population the four outcomes are counted out of, and it is
// here for the same reason StateWriteReuseFacts carries a total: every outcome
// reading zero is the expected healthy state for a Slot with nothing frozen,
// and it is also exactly what a renewal that never runs looks like. With the
// population beside them, all-zero outcomes and a non-zero Frozen is a real
// measurement, and all-zero outcomes with a zero Frozen while the deployment's
// state_load and state_apply rates differ says the candidate set is wrong.
type FrozenStateRenewalFacts struct {
	// Due, Read and Written are where this Slot's series went, counted at the
	// three places the decisions are made, over one population: the series
	// the Slot prepared, real and synthetic no-data alike. Due minus Read is
	// the series the Slot meant to evaluate and never read -- a PRIMARY input
	// that was incomplete skips the State preflight entirely, so those keys
	// age without anything touching them, and a renewal that hangs on the
	// read cannot reach them. Read minus Written is the population the
	// renewal does cover. A Plan folded out of the Slot before any series was
	// prepared is upstream of all three and counted by none.
	//
	// They are here because the first sizing of that population inferred it
	// from state_load minus state_apply, and that difference holds several
	// other things; the estimate came out two orders of magnitude high and the
	// mechanism shipped renewing almost nothing. These three are measured, not
	// inferred, and one of them cannot be derived from the other two.
	Due     int `json:"due"`
	Read    int `json:"read"`
	Written int `json:"written"`
	Frozen  int `json:"frozen"`
	Renewed int `json:"renewed"`
	Fresh   int `json:"fresh"`
	Missing int `json:"missing"`
	Failed  int `json:"failed"`
}

// Record adds one Plan's renewal outcomes to the Slot's.
func (facts *FrozenStateRenewalFacts) Record(renewed, fresh, missing, failed int) {
	facts.Frozen += renewed + fresh + missing + failed
	facts.Renewed += renewed
	facts.Fresh += fresh
	facts.Missing += missing
	facts.Failed += failed
}

// DimensionCensusFacts is one census write: where its values came from, what
// the store did with it, how much of the strategy it could name, and the two
// numbers the candidate decision was made on.
//
// PeakBytes and ShareBytes are on the line because "why is this Plan a
// candidate" is otherwise unanswerable afterwards: the gate is this
// replica's own peak against its own pool share (decision-020 section
// 4.7.3.1), both of which move, and a census that appears or stops
// appearing is otherwise a change with no reading behind it.
//
// OverflowValues and OverflowSeries are the reading that says the bound was
// reached: a census that names four thousand values and hides a tail of
// twenty thousand is not a distribution a split can be planned from, and it
// has to be possible to see that without reading the record.
type DimensionCensusFacts struct {
	Source         string `json:"source"`
	Status         string `json:"status"`
	Series         uint32 `json:"series"`
	Dimensions     int    `json:"dimensions"`
	Values         int    `json:"values"`
	OverflowValues uint32 `json:"overflow_values"`
	OverflowSeries uint32 `json:"overflow_series"`
	Bytes          int    `json:"bytes"`
	Limit          int    `json:"limit"`
	PeakBytes      uint64 `json:"peak_bytes"`
	ShareBytes     uint64 `json:"share_bytes"`
}

// RecordCensus states where the Slot's series went.
func (facts *FrozenStateRenewalFacts) RecordCensus(due, read, written int) {
	facts.Due, facts.Read, facts.Written = due, read, written
}

// Empty is a Slot that neither meant to evaluate a series nor froze one.
//
// A Slot with series due reports even when nothing was frozen, which is the
// whole point of the census: an all-zero outcome family and no census at all
// read identically, and telling them apart took a deployment.
func (facts FrozenStateRenewalFacts) Empty() bool { return facts.Due == 0 && facts.Frozen == 0 }

type NoDataMemoryRefusalFacts struct {
	// Reason is the store's reason code, so a refusal about size and one about
	// a corrupt record are told apart before anyone reads the numbers.
	Reason string
	// Record, Groups and Limit are set only by a bound refusal. Record says
	// what was measured, and today the one bound left on a memory is how many
	// groups it holds: a memory held one field per group has no size a write
	// can exceed, so the byte measurement that used to be here is gone rather
	// than kept as a field nothing fills.
	Record string
	Groups int
	Limit  int
}

// NoDataCensusFacts is how many Plans this Slot had that detect no-data, before
// anything was decided about them.
//
// It exists because every other no-data signal is conditional on a Plan getting
// far enough to land on an outcome, and the failure that has now hidden three
// times running is a Plan never getting there at all: nothing is judged, so
// nothing is counted, so every outcome reads as a computed zero and no line is
// written. This is counted in the same pass that produces the outcomes and is
// reported on every Slot including the ones with none, so the two can be read
// against each other -- they must agree, and a census above the outcomes is a
// Plan that was dropped between being seen and being judged.
type NoDataCensusFacts struct {
	// Hop is where along the way from the leader's Catalog to the worker's
	// Slot this count was taken. Every hop reports one, so the first one that
	// reads zero is where the Plans stop existing.
	//
	// Three releases were spent proving from the call graph that each hop
	// carries the section, and production read zero every time. A count per
	// hop replaces the argument with a number.
	Hop   string
	Plans int
}

// The hops a no-data Plan passes on its way from the leader's Catalog to being
// judged in a Slot. Each is counted where the Plans are in hand, and reported
// whether or not there are any.
const (
	// NoDataHopPublished is the count decoded back out of the bytes the leader
	// just wrote. It is taken from the payload rather than the struct it was
	// built from, because what a later process reads is the payload.
	NoDataHopPublished = "published"
	// NoDataHopAssembledBytes is how many times the Segment's stored object
	// names the no-data section, counted in the bytes before anything decodes
	// them. Beside NoDataHopAssembled it separates a decoding fault from a
	// Segment naming an object that never carried the section: the two produce
	// the same zero everywhere downstream.
	NoDataHopAssembledBytes = "assembled_bytes"
	// NoDataHopAssembled is what a worker got back from the object store for
	// the Segment it is executing, after decoding.
	NoDataHopAssembled = "assembled"
	// NoDataHopFrozen is what survived compilation into the Slot's due set.
	NoDataHopFrozen = "frozen"
	// NoDataHopDue is what the no-data round found when it walked that due set.
	NoDataHopDue = "due"
)

// NoDataHops is every hop, for a reader to bound the family by and for the
// metric to create each label at startup: a hop that reports nothing and a hop
// that reports zero are the whole difference this family exists to show.
var NoDataHops = []string{
	NoDataHopPublished, NoDataHopAssembledBytes, NoDataHopAssembled, NoDataHopFrozen, NoDataHopDue,
}

// SegmentContentFacts says whether the Segment a Slot executes names the object
// the latest publication names for its Query Group.
//
// A Segment carries the object digest it was cut with, and a publication that
// changes execution content writes new objects and leaves the old ones in
// place. A fleet whose Segments are not recut keeps executing the old content
// with every other signal healthy -- the objects load, the digests verify, the
// Plans compile, the Slots pass -- and the only symptom is that a change made
// in the source never takes effect.
type SegmentContentFacts struct {
	State string
}

// SourceWithheldFacts is what one withheld object has to say that nothing else
// already carries.
//
// It is three fields because the rest already have a home: the strategy, the
// business, the snapshot, the level and the scope are all TraceFields, and
// putting them here too would be the same fact in two places under two names.
// What is left is the pair that says what happened and why, plus how much of
// the round did not fit.
//
// Reason is here rather than in ReasonCode because a control-plane observation
// only keeps its reason string when it carries a source kind and the reason is
// one of the source classes; these are neither, so ReasonCode would fold every
// one of them to _other and the line would say a refusal happened without
// saying which.
type SourceWithheldFacts struct {
	Disposition string
	Reason      string
	// Field is where in the strategy document the refusal happened. The reason
	// alone names a class; a document has a few hundred keys, and which one it
	// was is the difference between a line an operator can act on and one they
	// have to reproduce offline. Empty when the refusal is not about a field.
	Field string
	// Dropped is how many further objects the round could not fit into its
	// line budget, reported on the last line of the round. A report that was
	// cut without saying so reads as a complete one.
	Dropped int
}

type QueryPermitFacts struct {
	QueueKind        QueryQueueKind
	Admission        bool
	NormalWaiting    int
	RecoveryWaiting  int
	NormalInflight   int
	RetryInflight    int
	ReplayInflight   int
	ProbeInflight    int
	RecoveryInflight int
}

type ActiveQGSetFacts struct {
	Operation   string
	Result      string
	QueryGroups int
	ObjectBytes int
	Duration    time.Duration
}

// ScheduleCutoverFacts describe one publication cutover as the Control
// Leader wrote it: how many Schedule timelines it rewrote, the bytes it sent
// in the one compare-and-set call, the largest single timeline among them,
// and what the pruning of dead Segments did. PrunesSkipped counts timelines
// that were left unpruned because their Progress could not be read, by
// reason; those timelines keep growing until a later cutover reads it.
type ScheduleCutoverFacts struct {
	Result string
	// Reason names why a failed cutover failed, from the control plane's
	// bounded list. Empty on success. Without it a cutover that has been
	// failing every round says only that it failed, which is what let one
	// fail about twice a minute for eleven hours while the fleet quietly
	// stopped picking up published changes.
	Reason string
	// QueryGroup is the one the cutover was working on when it stopped, so a
	// reader has somewhere to look rather than a whole population.
	QueryGroup       string
	Timelines        int
	PayloadBytes     int
	MaxTimelineBytes int
	TimelineBytes    []int
	SegmentsPruned   int
	PrunesSkipped    map[string]int
	Duration         time.Duration
	// QueryGroups counts what the cutover did with each Query Group, by
	// ScheduleCutoverDecisions; TimelinesRead is how many timelines it read
	// to decide, which is the population until a process has verified the
	// open Segments once and the changed set after; RevisionsFolded is how
	// many superseded output context revisions it folded away;
	// ContentSource names where the previous population came from.
	QueryGroups     map[string]int
	TimelinesRead   int
	RevisionsFolded int
	ContentSource   string
}

// ScheduleCutoverDecisions is the closed vocabulary of what a publication
// cutover does with one Query Group.
var ScheduleCutoverDecisions = []string{"kept", "revised", "cut", "legacy_cut", "retired", "added", "blocked", "reopened", "retired_unwritten"}

// ReplayExpiryFacts describe one Slot the scheduler gave up replaying.
//
// The reason is the point of them. A Slot too old for the replay window and a
// Slot whose own readiness rule holds it past that window look identical from
// outside -- both end as a skipped grid point with no failure anywhere -- and
// they need opposite responses: the first is a worker that fell behind, the
// second is two settings that disagree and will skip every Slot of that period
// for as long as they do.
//
// ReadyAtUnixMilli and DistanceBoundaryUnixMilli are the two instants the
// third reason compared, and are zero for the others.
// RangeGateOutcome values name what became of the catch-up path on a round
// that gave up on a Slot.
//
// Total by construction: the five refusals are the gate's own conditions in
// the order it writes them, the two post-build words are the only other ways
// the range is not used, and applied is the rest. A round that expires a Slot
// reports exactly one of them, so the share that never reaches the builder is
// readable against the share that does without remembering a previous round.
//
// unexplained means the gate and the description of it disagree. It should
// stay at zero; a reading above zero is a defect in one of the two, not a
// state of the Query Group, and is the reason the word exists rather than a
// silent fallthrough.
const (
	RangeGateApplied               = "applied"
	RangeGateCreationDisabled      = "range_creation_disabled"
	RangeGateProgressMissing       = "progress_missing"
	RangeGateNextSlotMoved         = "next_slot_moved"
	RangeGateUnfinishedSlotPresent = "unfinished_slot_present"
	RangeGateNoRangeFlight         = "no_range_flight"
	// The builder's own refusals. These six replaced one not_eligible bucket:
	// a round that reached the builder and came back empty used to be
	// indistinguishable from any other, and the six conditions behind it need
	// different answers -- a schedule whose Plans disagree is a control-plane
	// fact, a Slot whose deadline has not arrived is a round too early, and a
	// range of fewer than two Slots is a Query Group that is not actually
	// backlogged and has nothing to catch up.
	RangeGateRecoveryDisabled   = "recovery_disabled"
	RangeGatePlansMismatch      = "plans_mismatch"
	RangeGateDeadlineNotReached = "deadline_not_reached"
	RangeGateStepsBelowOne      = "steps_below_one"
	RangeGateFreezeFailed       = "freeze_failed"
	RangeGateProofTooLarge      = "proof_too_large"
	RangeGateUnexplained        = "unexplained"
)

// RangeGateOutcomes is every value the outcome takes, for the partition to
// pre-create and for a reader to bound the family by.
var RangeGateOutcomes = []string{
	RangeGateApplied, RangeGateCreationDisabled, RangeGateProgressMissing, RangeGateNextSlotMoved,
	RangeGateUnfinishedSlotPresent, RangeGateNoRangeFlight,
	RangeGateRecoveryDisabled, RangeGatePlansMismatch, RangeGateDeadlineNotReached,
	RangeGateStepsBelowOne, RangeGateFreezeFailed, RangeGateProofTooLarge,
	RangeGateUnexplained,
}

// RangeGateFacts is one round that gave up on a Slot, and what the catch-up
// path did with it.
//
// The three values the gate compared travel beside the word, so a reader can
// check the word against them rather than trust it -- the same reason both
// candidate bounds travel on a distance expiry. They are locals of one
// comparison and are kept nowhere else.
type RangeGateFacts struct {
	Outcome string `json:"outcome"`
	// ProgressNextSlot and ExpectedNextSlot are the two the gate compares;
	// ProgressPresent distinguishes a Progress record that is absent from one
	// whose next Slot happens to be zero.
	ProgressPresent  bool  `json:"progress_present"`
	ProgressNextSlot int64 `json:"progress_next_slot"`
	ExpectedNextSlot int64 `json:"expected_next_slot"`
	// UnfinishedSlotPresent is the condition that would mean the Query Group
	// is still holding the Slot it began, and the evaluation time says which.
	UnfinishedSlotPresent        bool  `json:"unfinished_slot_present"`
	UnfinishedSlotEvaluationTime int64 `json:"unfinished_slot_evaluation_time"`
	RangeCreationEnabled         bool  `json:"range_creation_enabled"`
	// The two candidate bounds the builder's distance branch compared, when
	// the refusal came from a branch that had computed them. BoundsKnown says
	// so: the other refusals never compute these, and reporting zeroes for
	// them would put two numbers that do not exist beside a word, which is
	// what the reader would then divide the population by.
	BoundsKnown   bool  `json:"bounds_known"`
	DistanceBound int64 `json:"distance_bound"`
	DeadlineBound int64 `json:"deadline_bound"`
}

func normalizeRangeGateFacts(facts *RangeGateFacts) *RangeGateFacts {
	if facts == nil {
		return nil
	}
	normalized := *facts
	known := false
	for _, outcome := range RangeGateOutcomes {
		if normalized.Outcome == outcome {
			known = true
			break
		}
	}
	if !known {
		normalized.Outcome = RangeGateUnexplained
	}
	return &normalized
}

// RangeBoundByDistance, RangeBoundByDeadline and RangeBoundByBoth name which
// of the two candidate bounds produced an expired range.
//
// A closed set of three, because a reader partitioning the skipped population
// by it needs every range to land in one of them, and because the two causes
// call for opposite responses: bound by distance is a Query Group far behind
// the head of its schedule, bound by deadline is one that is barely past its
// own query deadline and is being given up on anyway.
const (
	RangeBoundByDistance = "distance"
	RangeBoundByDeadline = "deadline"
	RangeBoundByBoth     = "both"
)

// RangeBoundByValues is every value the label takes, for the partition to
// pre-create and for a reader to bound the family by.
var RangeBoundByValues = []string{RangeBoundByDistance, RangeBoundByDeadline, RangeBoundByBoth}

// RangeDistanceFacts is one expired range given up on for distance, with the
// numbers that decided how wide it is.
//
// These are locals of the call that builds the range and exist nowhere else:
// the sealed proof carries the range that was produced, not the two candidate
// bounds that produced it. Without them a Query Group shedding Slots reports
// GAP_SKIPPED completions and nothing more, and every question about the
// shape of the shedding -- how far behind, which bound bit first, from which
// Slot -- can only be answered by arithmetic on completion counts. A cohort
// skipping forty percent of its Slots went a day without a mechanism for
// exactly that reason.
//
// Both bounds travel, not only the one that won, and BoundBy is derived from
// the same two numbers reported beside it so a reader can check the label
// rather than trust it.
type RangeDistanceFacts struct {
	// HeadSteps is how many intervals the clock is past the first unfinished
	// Slot; DeadlineSteps how many it is past that Slot's own query deadline.
	HeadSteps     int64 `json:"head_steps"`
	DeadlineSteps int64 `json:"deadline_steps"`
	// Steps is the width the range was actually built to, after the segment
	// boundary and the count width have clamped it, and SlotCount the Slots
	// it covers.
	Steps     int64  `json:"steps"`
	SlotCount uint32 `json:"slot_count"`
	// FirstEvaluationTime is the Slot the range starts at, so the skipping can
	// be followed Slot by Slot rather than only counted.
	FirstEvaluationTime int64  `json:"first_evaluation_time"`
	IntervalSeconds     int64  `json:"interval_seconds"`
	MaxReplaySlots      uint32 `json:"max_replay_slots"`
	BoundBy             string `json:"bound_by"`
}

func normalizeRangeDistanceFacts(facts *RangeDistanceFacts) *RangeDistanceFacts {
	if facts == nil {
		return nil
	}
	normalized := *facts
	switch normalized.BoundBy {
	case RangeBoundByDistance, RangeBoundByDeadline, RangeBoundByBoth:
	default:
		// An unnamed bound is a producer this build does not know about, not
		// a reason to drop the rest of the numbers.
		normalized.BoundBy = RangeBoundByBoth
	}
	return &normalized
}

type ReplayExpiryFacts struct {
	Reason                    string
	Distance                  uint32
	AgeSeconds                float64
	ReadyAtUnixMilli          int64
	DistanceBoundaryUnixMilli int64
	// HeldBy is what the round before this one did with the Query Group, when
	// that round did not run its Slot.
	HeldBy *HeldByFacts `json:"held_by,omitempty"`
}

// HeldByNothing is the word for a Slot that no previous round held: either the
// round before it executed, or there was no round before it.
//
// It is a word rather than an absent field so the family is total. "Which
// Slots were held, and by what" is a distribution, and a distribution whose
// commonest case is a missing key cannot be read as one.
const HeldByNothing = "none"

// HeldByReadinessDeferred is the round that entered Execute and was handed
// back by access with an instant to wait for. It is not one of the run
// outcomes -- that reading folds it into execute_returned, because the round
// did return from Execute -- but from the next Slot's point of view it is a
// holder like any other, and the commonest one worth telling apart: "the data
// was not ready yet" and "the query backend refuses this strategy" are
// different problems with different owners.
const HeldByReadinessDeferred = "query_readiness_deferred"

// HeldByFacts is why the round before this one left the Slot unrun.
//
// Four Query Groups shed a Slot every round for hours and the completion line
// said GAP_SKIPPED, which names the outcome and not one thing about the cause.
// The cause was already recorded -- the Runner names its own decision on every
// round -- but in a different line, of a different stage, at a different
// timestamp, so reading it meant joining three tables by Query Group and
// second. Carrying the previous round's word on the line that reports the
// consequence is what makes the consequence answerable on its own.
//
// Decision takes the values run_one_return_total{outcome} takes, plus
// HeldByNothing, deliberately: the two are then the same vocabulary and a
// reader can go straight from "these Slots were skipped, held by X" to the
// fleet-wide rate of X without translating between two word lists.
type HeldByFacts struct {
	Decision string `json:"decision"`
	// AtUnixMilli is when that round reached its decision, so the gap between
	// it and this Slot's own deadline is readable rather than assumed.
	AtUnixMilli int64 `json:"at_ms"`
	// The cooldown's own two numbers, present only when Decision names the
	// cooldown. A cooldown that has failed sixteen times and one that has
	// failed once are the same word and different situations, and the instant
	// it runs to says whether this Slot ever had a chance.
	QueryCooldownFailures   uint32 `json:"query_cooldown_failures,omitempty"`
	QueryCooldownUntilMilli int64  `json:"query_cooldown_until_ms,omitempty"`
	// ReadyAtUnixMilli is the instant access told the previous round to wait
	// for, present only when Decision is the readiness deferral. The word
	// alone says the data was not ready; this says until when, which is the
	// difference between a Slot that missed its chance by a moment and one
	// whose readiness lands past its own deadline every time.
	ReadyAtUnixMilli int64 `json:"ready_at_ms,omitempty"`
}

// HeldByDecisions is every value Decision takes, for the partition to
// pre-create and for a reader to bound the family by.
var HeldByDecisions = append([]string{HeldByNothing, HeldByReadinessDeferred}, RunOutcomes...)

func normalizeHeldByFacts(facts *HeldByFacts) *HeldByFacts {
	if facts == nil {
		return nil
	}
	normalized := *facts
	if !ValidHeldByDecision(normalized.Decision) {
		normalized.Decision = HeldByNothing
	}
	if normalized.Decision != "query_cooldown" {
		normalized.QueryCooldownFailures, normalized.QueryCooldownUntilMilli = 0, 0
	}
	if normalized.Decision != HeldByReadinessDeferred {
		normalized.ReadyAtUnixMilli = 0
	}
	return &normalized
}

func ValidHeldByDecision(value string) bool {
	return value == HeldByNothing || value == HeldByReadinessDeferred || ValidRunOutcome(value)
}

// SlotWaitFacts is one blocking wait inside a Slot attempt, named and timed.
//
// A Slot attempt that is stuck is the one state the whole pipeline cannot
// describe. Every failure reports itself; waiting reports nothing, so an
// attempt that sat for twenty-two seconds between beginning and issuing its
// query left no line at all -- not an error, not a slow duration, nothing. The
// only readable fact was the silence between two timestamps, and silence
// cannot say which of the several things it could have been waiting on it was.
//
// One fact per wait, always measured and always counted; only the slow ones
// spend log quota, because a three-millisecond wait answers no question and
// there are thousands of them a second.
type SlotWaitFacts struct {
	// Wait is one of SlotWaits.
	Wait string
}

// SlotWaits is the closed vocabulary of the blocking waits inside one Slot
// attempt, in the order an attempt meets them.
const (
	// SlotWaitProgressBegin is the fenced Progress write that opens the Slot.
	SlotWaitProgressBegin = "progress_begin"
	// SlotWaitFinalization is deciding whether this Slot needs a query at all,
	// which reads the frozen Plan and so the Segment's content objects.
	SlotWaitFinalization = "finalization"
	// SlotWaitObjectShare is waiting on another goroutine's in-flight read of
	// the same catalog object. This one has no timeout of its own and no error
	// when it is slow: the joiner waits for whatever the leader is doing.
	SlotWaitObjectShare = "object_share"
)

var SlotWaits = []string{SlotWaitProgressBegin, SlotWaitFinalization, SlotWaitObjectShare}

// SlowSlotWait is the threshold above which a wait is worth a log line. It is
// one settling wait: a wait that outlasts the time the product allows for data
// to land is long enough to be the answer to "why did this Slot take so long".
const SlowSlotWait = 10 * time.Second

// ReplayExpiryReasons is the closed vocabulary of why a replay expired. The
// scheduler's typed constants are held to this list by a test rather than by
// hand, so a new reason cannot arrive without a series to count it.
var ReplayExpiryReasons = []string{
	"REPLAY_AGE_EXCEEDED", "REPLAY_DISTANCE_EXCEEDED", "REPLAY_WAIT_EXCEEDS_DISTANCE", "REPLAY_RANGE_EXPIRED",
}

// ObjectCatalogFacts describe one write or renewal of the content-addressed
// Query Group objects, output contexts and the manifest that names them for
// one publication. Written counts objects the operation created, Present
// counts objects it found already stored under their digest and left alone,
// Missing counts referenced objects a renewal could not find.
type ObjectCatalogFacts struct {
	Operation     string
	Result        string
	QueryGroups   int
	Written       int
	Present       int
	Missing       int
	ManifestBytes int
	ObjectBytes   int
	Duration      time.Duration
}

// ObjectReadFacts describe one read of a catalog object by a Worker: the
// kind of object and how the read went, or, for a Segment, whether the
// Query Group was read by content and if not, why.
type ObjectReadFacts struct {
	Kind   string
	Result string
}

// ObjectReadKinds and ObjectReadResults are the closed vocabularies of
// ObjectReadFacts; a value outside them is reported as "other".
var (
	ObjectReadKinds   = []string{"query_group", "output_context", "segment", "manifest"}
	ObjectReadResults = []string{"hit", "miss", "share", "missing", "invalid", "newer", "object", "legacy_segment", "segment_without_ref", "object_missing", "object_invalid", "object_newer", "object_mismatch"}
)

// StateGenerationSkewFacts report a due Plan whose state generation, as the
// activation record names it, disagrees with a generation derived elsewhere:
// "formula" when this process compiles the same Plan to another generation
// than the Control Leader that published it (tolerated: the record's
// generation governs the Slot), "record" when the record names a generation
// the Query Group object published with it does not carry (refused).
type StateGenerationSkewFacts struct {
	Kind string
	// StrategyID names the Plan the skew was found on; the Slot and Segment
	// travel in the observation's trace fields.
	StrategyID string
}

// StateCarryFacts count history carried across a moved state generation.
// Scope is "plan" for the Control Leader's decision per activation and
// "series" for a Worker's per series; Count is how many met Result.
type StateCarryFacts struct {
	Scope  string
	Result string
	Count  int
}

// StateCarryScopes and StateCarryResults are the closed vocabularies of
// StateCarryFacts; a value outside them is reported as "other".
var (
	StateCarryScopes  = []string{"plan", "series"}
	StateCarryResults = map[string][]string{
		"plan":   {"carried", "partial", "none_detect_changed", "none_previous_unreadable", "none_discontinuous"},
		"series": {"carried", "nothing_kept", "skipped_active_guard", "old_missing"},
	}
)

func normalizeStateCarryFacts(facts *StateCarryFacts) *StateCarryFacts {
	if facts == nil {
		return nil
	}
	normalized := StateCarryFacts{Scope: "other", Result: "other", Count: facts.Count}
	for _, scope := range StateCarryScopes {
		if facts.Scope != scope {
			continue
		}
		normalized.Scope = scope
		for _, result := range StateCarryResults[scope] {
			if facts.Result == result {
				normalized.Result = result
			}
		}
	}
	return &normalized
}

// StateGenerationSkewKinds is the closed vocabulary of
// StateGenerationSkewFacts; a value outside it is reported as "other".
var StateGenerationSkewKinds = []string{"formula", "record"}

// ActivationHoldFacts report, for one activation attempt, the Query Groups
// the publication brings back from an earlier retirement. Reappeared counts
// all of them; Held counts the ones that have not drained their retired
// Slots yet and were therefore held out of the activation, which went ahead
// for everyone else. A held Query Group is reported again on every reconcile
// until it drains and is brought back, so the count follows it down to zero.
// MaxAgeSeconds is how long the oldest held retirement has waited.
type ActivationHoldFacts struct {
	Reappeared    int
	Held          int
	MaxAgeSeconds int64
	Samples       []string
	Truncated     bool
}

// MaxActivationHoldSamples bounds the Query Group identities an activation
// hold observation carries.
const MaxActivationHoldSamples = 12

type LegacyQGMigrationFacts struct {
	Result      string
	ReasonClass string
	ScanKeys    int
	Duration    time.Duration
}

const (
	MaxDrainingQGLogSamples          = 12
	MaxActivationFailureQGLogSamples = 8
)

// MaxRebalanceOwnedSamples bounds the per-worker owned counts one rebalance
// planning log line carries; a fleet larger than this still reports its
// counts and marks the list truncated.
const MaxRebalanceOwnedSamples = 64

// RebalanceOwnedSample is the desired-owner count of one ready worker at
// planning time, zero included.
type RebalanceOwnedSample struct {
	WorkerID string `json:"worker_id"`
	Owned    int    `json:"owned"`
}

// MaxRebalanceMoveSamples bounds the moves one rebalance round names. A
// round moves at most one hundredth of the population, nine on the
// reference deployment, so the bound is reached only on a very large
// fleet, and the truncation is then made visible.
const MaxRebalanceMoveSamples = 32

// RebalanceMoveSample is one Assignment the plan would move: the Query
// Group and the workers it would leave and join. Moves are listed so that
// a plan can be checked one Query Group at a time against the Assignments
// and the ready set, which the counts alone cannot support; the shadow
// still publishes nothing.
type RebalanceMoveSample struct {
	QueryGroup string `json:"query_group"`
	From       string `json:"from"`
	To         string `json:"to"`
}

// RebalanceFacts describes one rebalance planning round on the Control
// Leader. It is a shadow measurement: PlannedMoves says how many
// Assignments the round would move from the most to the least loaded ready
// worker, and nothing publishes those moves. Owned lists every ready
// worker, sorted by identity, so the distribution the plan reacted to can
// be read beside the count, and Moves names each Assignment the plan would
// move, in the planner's order, so the plan can be checked one Query Group
// at a time.
type RebalanceFacts struct {
	ReadyWorkers int                    `json:"ready_workers"`
	Assigned     int                    `json:"assigned"`
	Target       int                    `json:"target"`
	MostOwned    int                    `json:"most_owned"`
	LeastOwned   int                    `json:"least_owned"`
	Batch        int                    `json:"batch"`
	PlannedMoves int                    `json:"planned_moves"`
	Owned        []RebalanceOwnedSample `json:"owned,omitempty"`
	Truncated    bool                   `json:"truncated"`
	Moves        []RebalanceMoveSample  `json:"moves,omitempty"`
	// MovesTruncated says the moves were cut at MaxRebalanceMoveSamples;
	// PlannedMoves still counts them all.
	MovesTruncated bool `json:"moves_truncated"`
	// PublishedMoves is how many of the planned moves the round wrote as
	// Assignments, Conflicts how many the store refused because the record
	// had moved under the round. Paused says the round wrote none because
	// the ready set changed within the stabilisation window, PausedForSeconds
	// how much of the window was left.
	PublishedMoves   int     `json:"published_moves"`
	Conflicts        int     `json:"conflicts"`
	Paused           bool    `json:"paused"`
	PausedForSeconds float64 `json:"paused_for_seconds"`
	// Bytes is the same round's byte-constraint planning (decision-020
	// section 5.7), which runs before the count correction above.
	Bytes *ByteConstraintFacts `json:"bytes,omitempty"`
	// ShardAware is the same round's split gate (decision-020 section
	// 4.7.7): how many ready workers, and which of them do not declare the
	// split contract. A split is published only while Unaware is empty.
	ShardAware *ShardAwareFacts `json:"shard_aware,omitempty"`
}

// ShardAwareFacts is one round's split-contract census over the ready set.
// Unaware is a list rather than a count because the replica is what a
// rollout reader looks for, and 0 -> n -> 0 across a roll is the reading.
type ShardAwareFacts struct {
	Ready   int      `json:"ready"`
	Unaware []string `json:"unaware,omitempty"`
	// SplitsHeld is how many splits the round was asked for and did not
	// publish because Unaware is not empty; zero on a fleet asked for none.
	SplitsHeld int `json:"splits_held"`
}

// ByteConstraintFacts is one round's byte-constraint planning: retained
// bytes are a capacity constraint on placement, and a Worker whose Query
// Groups' per-Slot peaks sum past SharePercent of its pool gives its
// largest one to the Worker with the most headroom. What was not judged is
// said as such - PoolUnknown Workers registered no pool, Unread Query
// Groups have no reported peak - so a round that moved nothing can be told
// from a round that could judge nothing.
type ByteConstraintFacts struct {
	SharePercent   int              `json:"share_percent"`
	Judged         int              `json:"judged"`
	PoolUnknown    []string         `json:"pool_unknown,omitempty"`
	Unread         int              `json:"unread"`
	Unsettled      []string         `json:"unsettled,omitempty"`
	Overloaded     []string         `json:"overloaded,omitempty"`
	Unplaceable    []string         `json:"unplaceable,omitempty"`
	PlannedMoves   int              `json:"planned_moves"`
	PublishedMoves int              `json:"published_moves"`
	Conflicts      int              `json:"conflicts"`
	Paused         bool             `json:"paused"`
	Moves          []ByteMoveSample `json:"moves,omitempty"`
}

// ByteMoveSample is one byte-constraint move with the peak it was judged by.
type ByteMoveSample struct {
	QueryGroup string `json:"query_group"`
	From       string `json:"from"`
	To         string `json:"to"`
	Bytes      uint64 `json:"bytes"`
}

// ControlReadFacts is what one control round spent reading the records it
// places Query Groups from.
//
// Round trips are counted where the calls are issued, not inferred from a
// Redis client's own counters. A client-side total cannot say which round
// trips belonged to which round, nor separate the assignment reads from every
// other thing the same client does; and the number this is here to answer --
// "did the round stop spending one round trip per Query Group" -- is exactly
// a per-round, per-purpose number.
//
// Keys beside RoundTrips is what makes the reading falsifiable: keys rising
// while round trips stay flat is the batch working, and both rising together
// is a batch that is not batching. Either can be read off one sample, with no
// memory of a previous round.
type ControlReadFacts struct {
	// QueryGroups is the round's population, the number the assignment read
	// would have cost a round trip each before.
	QueryGroups int `json:"query_groups"`
	// AssignmentKeys and AssignmentRoundTrips are the records read to decide
	// placement; RegistryKeys and RegistryRoundTrips the worker
	// registrations behind the ready set.
	AssignmentKeys         int     `json:"assignment_keys"`
	AssignmentRoundTrips   int     `json:"assignment_round_trips"`
	AssignmentMilliseconds float64 `json:"assignment_milliseconds"`
	RegistryKeys           int     `json:"registry_keys"`
	RegistryRoundTrips     int     `json:"registry_round_trips"`
	RegistryMilliseconds   float64 `json:"registry_milliseconds"`
}

func normalizeControlReadFacts(facts *ControlReadFacts) *ControlReadFacts {
	if facts == nil {
		return nil
	}
	normalized := *facts
	for _, count := range []*int{
		&normalized.QueryGroups, &normalized.AssignmentKeys, &normalized.AssignmentRoundTrips,
		&normalized.RegistryKeys, &normalized.RegistryRoundTrips,
	} {
		if *count < 0 {
			*count = 0
		}
	}
	for _, elapsed := range []*float64{&normalized.AssignmentMilliseconds, &normalized.RegistryMilliseconds} {
		if *elapsed < 0 || math.IsNaN(*elapsed) || math.IsInf(*elapsed, 0) {
			*elapsed = 0
		}
	}
	return &normalized
}

func normalizeRebalanceFacts(facts *RebalanceFacts) *RebalanceFacts {
	if facts == nil {
		return nil
	}
	normalized := *facts
	for _, count := range []*int{
		&normalized.ReadyWorkers, &normalized.Assigned, &normalized.Target, &normalized.MostOwned,
		&normalized.LeastOwned, &normalized.Batch, &normalized.PlannedMoves, &normalized.PublishedMoves, &normalized.Conflicts,
	} {
		if *count < 0 {
			*count = 0
		}
	}
	normalized.Owned = append([]RebalanceOwnedSample(nil), facts.Owned...)
	if len(normalized.Owned) > MaxRebalanceOwnedSamples {
		normalized.Owned = normalized.Owned[:MaxRebalanceOwnedSamples]
		normalized.Truncated = true
	}
	for index := range normalized.Owned {
		if normalized.Owned[index].Owned < 0 {
			normalized.Owned[index].Owned = 0
		}
	}
	normalized.Moves = append([]RebalanceMoveSample(nil), facts.Moves...)
	if len(normalized.Moves) > MaxRebalanceMoveSamples {
		normalized.Moves = normalized.Moves[:MaxRebalanceMoveSamples]
		normalized.MovesTruncated = true
	}
	if gate := facts.ShardAware; gate != nil {
		// Copied rather than shared: the round keeps its own list and the
		// observation is read after the round moves on. Bounded by the ready
		// set, which is the fleet, and truncated at the same sample bound the
		// owned counts use so one enormous fleet cannot make one enormous
		// line.
		bounded := *gate
		bounded.Unaware = append([]string(nil), gate.Unaware...)
		if len(bounded.Unaware) > MaxRebalanceOwnedSamples {
			bounded.Unaware = bounded.Unaware[:MaxRebalanceOwnedSamples]
		}
		if bounded.Ready < 0 {
			bounded.Ready = 0
		}
		if bounded.SplitsHeld < 0 {
			bounded.SplitsHeld = 0
		}
		normalized.ShardAware = &bounded
	}
	return &normalized
}

// AssignmentIndexFacts describes one Assignment index round: written by
// the Control Leader (Round, ControlEpoch, Workers, Rewritten, Missing) or
// read by a worker (Round, Result, StaleRounds, SetRead, Candidates,
// Assigned and the confirmation counts). The index only names candidates,
// so the read side also reports what the records said about every change
// it took from the index: Opened, a candidate the record confirmed;
// Rejected, a candidate the record refused; Released, a held Query Group
// the record confirmed gone; Retained, a held Query Group the index dropped
// but the record still assigns here. Reads is the number of records read
// to decide, the count the index exists to shrink; FullRead marks a round
// that read every record because no usable index was there.
// AssignmentSweepFacts is what one Assignment sweep found and did: records
// scanned, those naming Query Groups the leader no longer runs, and of those
// the ones reclaimed, the ones left because a lease on them is still live,
// and the ones left because they moved under the sweep.
type AssignmentSweepFacts struct {
	Scanned     int `json:"scanned"`
	Retired     int `json:"retired"`
	Reclaimed   int `json:"reclaimed"`
	HeldByLease int `json:"held_by_lease"`
	Changed     int `json:"changed"`
}

// ViewStreamFacts describe one event of the view stream: a publication
// (Event "published": Revision, Affected, and the four numbers of the
// version it closed when Closed is set), or a session event (Event
// "opened", "refused", "closed": WorkerID, Incarnation, Reason).
type ViewStreamFacts struct {
	Event        string `json:"event"`
	WorkerID     string `json:"worker_id,omitempty"`
	Incarnation  string `json:"incarnation,omitempty"`
	ControlEpoch uint64 `json:"control_epoch"`
	Revision     uint64 `json:"revision,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Affected     int    `json:"affected,omitempty"`
	// Closed is the revision whose ledger this publication closed, with
	// its four numbers as they stood; zero when none was closed.
	Closed    uint64 `json:"closed,omitempty"`
	Expected  int    `json:"expected,omitempty"`
	Sent      int    `json:"sent,omitempty"`
	Acked     int    `json:"acked,omitempty"`
	Installed int    `json:"installed,omitempty"`
	Switched  int    `json:"switched,omitempty"`
	// ObjectsMissing is, on a Worker's install, how many objects of the
	// installed view its catalog could not serve.
	ObjectsMissing int `json:"objects_missing,omitempty"`
}

type AssignmentIndexFacts struct {
	Round        uint64 `json:"round"`
	ControlEpoch uint64 `json:"control_epoch,omitempty"`
	Workers      int    `json:"workers,omitempty"`
	Rewritten    int    `json:"rewritten,omitempty"`
	Missing      int    `json:"missing,omitempty"`
	Result       string `json:"result,omitempty"`
	StaleRounds  int    `json:"stale_rounds"`
	SetRead      bool   `json:"set_read,omitempty"`
	Candidates   int    `json:"candidates"`
	Assigned     int    `json:"assigned"`
	Opened       int    `json:"opened"`
	Rejected     int    `json:"rejected"`
	Released     int    `json:"released"`
	Retained     int    `json:"retained"`
	Reads        int    `json:"reads"`
	FullRead     bool   `json:"full_read,omitempty"`
}

// Closed vocabulary of the index read result; anything else normalizes to
// the value that says "do not trust this read".
const (
	AssignmentIndexFresh   = "fresh"
	AssignmentIndexStale   = "stale"
	AssignmentIndexMissing = "missing"
	AssignmentIndexInvalid = "invalid"
)

// Confirmation outcomes of the index read side, the labels of
// assignment_index_confirm_total.
const (
	AssignmentIndexOpened   = "opened"
	AssignmentIndexRejected = "rejected"
	AssignmentIndexReleased = "released"
	AssignmentIndexRetained = "retained"
)

func normalizeAssignmentIndexFacts(facts *AssignmentIndexFacts) *AssignmentIndexFacts {
	if facts == nil {
		return nil
	}
	normalized := *facts
	for _, count := range []*int{
		&normalized.Workers, &normalized.Rewritten, &normalized.Missing, &normalized.StaleRounds,
		&normalized.Candidates, &normalized.Assigned, &normalized.Opened, &normalized.Rejected,
		&normalized.Released, &normalized.Retained, &normalized.Reads,
	} {
		if *count < 0 {
			*count = 0
		}
	}
	switch normalized.Result {
	case "", AssignmentIndexFresh, AssignmentIndexStale, AssignmentIndexMissing, AssignmentIndexInvalid:
	default:
		normalized.Result = AssignmentIndexInvalid
	}
	return &normalized
}

// CursorAdvanceFacts describes one attempt to move a Progress cursor that
// points into a pruned part of the timeline to the earliest retained Slot:
// where it stood, where it was moved to, and what the store said.
type CursorAdvanceFacts struct {
	From   int64  `json:"from"`
	To     int64  `json:"to"`
	Status string `json:"status"`
	// Refusal names the fact the store checked and found against a skip it
	// reports as a conflict, so a conflict that keeps happening says which
	// of its premises fails instead of that one of them does. Empty for
	// every other status.
	Refusal string `json:"refusal,omitempty"`
	// InFlightSlot is the evaluation time of the Slot the store found in
	// flight and an applied skip discarded with the pruned span; a conflict
	// from the compare-and-set names it too. Zero when there was none.
	InFlightSlot int64 `json:"in_flight_slot,omitempty"`
}

// Closed vocabulary of cursor advance outcomes.
const (
	CursorAdvanceApplied    = "applied"
	CursorAdvanceConflict   = "conflict"
	CursorAdvanceStaleOwner = "stale_owner"
	CursorAdvanceRetryable  = "retryable"
	CursorAdvanceFailed     = "failed"
)

// Closed vocabulary of the fact that refused a cursor advance, carried only
// by a conflict. A value outside it is reported as OTHER rather than
// dropped, so a refusal the store learns to name later is still counted.
const (
	CursorRefusalProgressMissing = "PROGRESS_MISSING"
	CursorRefusalRangeInFlight   = "RANGE_IN_FLIGHT"
	CursorRefusalCursorMoved     = "CURSOR_MOVED"
	CursorRefusalCASConflict     = "CAS_CONFLICT"
	CursorRefusalOther           = "OTHER"
)

// CursorRefusals lists the refusals a conflict can carry, OTHER included.
var CursorRefusals = []string{CursorRefusalProgressMissing, CursorRefusalRangeInFlight, CursorRefusalCursorMoved,
	CursorRefusalCASConflict, CursorRefusalOther}

// CursorAdvanceStatuses lists the outcomes a cursor advance reports.
var CursorAdvanceStatuses = []string{CursorAdvanceApplied, CursorAdvanceConflict, CursorAdvanceStaleOwner, CursorAdvanceRetryable, CursorAdvanceFailed}

// In-flight markers of a draining sample: what the Progress record carries
// besides its cursor, because a skip refuses to move a cursor past it.
const (
	DrainingInFlightSlot  = "slot"
	DrainingInFlightRange = "range"
)

func normalizeCursorAdvanceFacts(facts *CursorAdvanceFacts) *CursorAdvanceFacts {
	if facts == nil {
		return nil
	}
	normalized := *facts
	if normalized.From < 0 {
		normalized.From = 0
	}
	if normalized.To < 0 {
		normalized.To = 0
	}
	switch normalized.Status {
	case CursorAdvanceApplied, CursorAdvanceConflict, CursorAdvanceStaleOwner, CursorAdvanceRetryable, CursorAdvanceFailed:
	default:
		normalized.Status = CursorAdvanceFailed
	}
	if normalized.InFlightSlot < 0 {
		normalized.InFlightSlot = 0
	}
	if normalized.Status != CursorAdvanceConflict {
		normalized.Refusal = ""
		return &normalized
	}
	switch normalized.Refusal {
	case CursorRefusalProgressMissing, CursorRefusalRangeInFlight, CursorRefusalCursorMoved, CursorRefusalCASConflict:
	default:
		normalized.Refusal = CursorRefusalOther
	}
	return &normalized
}

// DrainingQGSampleRetired marks a sample whose Query Group left the active set
// because it is past the draining termination window without draining.
// Undrained samples carry no marker so their log shape is unchanged.
const DrainingQGSampleRetired = "RETIRED"

type DrainingQGSample struct {
	QueryGroupKey   string `json:"query_group_key"`
	RetiredBoundary int64  `json:"retired_boundary"`
	NextSlot        int64  `json:"next_slot"`
	ProgressStatus  string `json:"progress_status"`
	Disposition     string `json:"disposition,omitempty"`
	// EarliestRetainedSlot is the first Slot the Query Group's timeline still
	// holds, read from the timeline itself; CursorPruned says the Progress
	// cursor (NextSlot) lies before it, so no read can ever find the Slot the
	// cursor asks for and the Query Group cannot drain on its own. Both are
	// reported only when the timeline was read; a timeline that could not be
	// read makes no claim either way.
	EarliestRetainedSlot int64 `json:"earliest_retained_slot,omitempty"`
	CursorPruned         bool  `json:"cursor_pruned,omitempty"`
	// InFlight says whether the Progress record carries a Slot or a range
	// that was begun and not finished, which a pruned skip refuses to move
	// past when it lies in the retained span. Empty when it carries none.
	InFlight string `json:"in_flight,omitempty"`
}

// DrainingQGFacts carries bounded counts of one draining reconciliation.
// Retired counts undrained Query Groups past the termination window; they are
// no longer active and are not counted as Undrained.
type DrainingQGFacts struct {
	Total     int `json:"total"`
	Undrained int `json:"undrained"`
	Isolated  int `json:"isolated"`
	Retired   int `json:"retired"`
	// CursorPruned counts the draining Query Groups whose Progress cursor lies
	// before the earliest Slot their timeline still holds; see
	// DrainingQGSample.CursorPruned. Reported before anything acts on it.
	CursorPruned int                `json:"cursor_pruned"`
	Samples      []DrainingQGSample `json:"samples,omitempty"`
	Truncated    bool               `json:"truncated"`
}

// SourceRefreshFacts carries one bounded source refresh outcome. Snapshot
// identity is diagnostic log context only; Prometheus consumes Status alone.
// SourceRefreshFacts carries what one refresh round settled. Two different
// objects are described here and they must never share a field: SnapshotRevision
// is the publication this round produced, and ActivatedRevision is the
// publication the fleet is executing, which is older whenever a round ends
// without publishing. A round that publishes nothing has no publication to name,
// so it fills the activated pair and leaves the published pair empty.
//
// The distinction is load-bearing rather than cosmetic. These reach the log under
// one stage, so a reader who cannot tell the two apart from the record will read
// a lagging activation as a stalled publication -- and the lag is normal while
// the stall is not.
type SourceRefreshFacts struct {
	Status            SourceRefreshStatus
	ObservationID     string
	SnapshotRevision  string
	PublicationEpoch  uint64
	ActivatedRevision string
	ActivatedEpoch    uint64
	// ActivationCaughtUp marks a round that published nothing but moved the
	// activation to the publication an earlier round had published and not
	// activated. The counts below then describe that move.
	ActivationCaughtUp bool
	// ActivationRebuilt marks a caught-up round that found no activation
	// record at all and established one from the published Catalog, as a
	// first activation does. Catching up moves an activation; rebuilding
	// writes one where the store had none, which is what a store that came
	// back without its keys leaves behind, and the two must not read alike.
	ActivationRebuilt bool
	// ActiveQueryGroups is a size, not a change. The counts below are a change,
	// and the two are kept apart because a round that publishes nothing has no
	// previous set to difference against: reporting a difference there can only
	// restate the current set against itself and produce zeroes that look
	// measured.
	ActiveQueryGroups      int
	ActiveQueryGroupsKnown bool
	CountsKnown            bool
	OldQueryGroups         int
	NewQueryGroups         int
	AddedQueryGroups       int
	RetiredQueryGroups     int
	// CompiledStrategies and ReusedStrategies say how the round's Catalog was
	// built: how many strategies went through the compiler and how many were
	// taken from an earlier round's compilation of the same document. Their
	// sum is the strategies the round asked the compiler about; a round whose
	// source did not change reports all of them as reused.
	CompiledStrategies int
	ReusedStrategies   int
	// ReadMode and ReadReason say whether the round read the strategy
	// documents from the source or reused an earlier round's observation, and
	// why; StrategiesRead is how many documents it asked the source for. Only
	// the pairs in AllSourceReadOutcomes occur; both are empty for a producer
	// that does not report how it read.
	ReadMode       SourceReadMode
	ReadReason     SourceReadReason
	StrategiesRead int
	// ChangeSignalPresent says the source offered a change signal this round;
	// ChangeSignalAgeSeconds is how long ago its publisher moved it, by the
	// reporting process's clock, and means nothing when not present.
	ChangeSignalPresent    bool
	ChangeSignalAgeSeconds int64
	// RetainedStaleRevisions is how many last-good Plans the round's Catalog
	// refused to retain because their persisted revision no longer derives
	// from their facts; non-zero only across a change of the revision
	// formula, when it is the whole population of retained Plans.
	RetainedStaleRevisions int
}

// SourceReadMode and SourceReadReason mirror the control plane's vocabulary
// for how a refresh round read its source. They are closed: the metric is
// labelled by them.
type (
	SourceReadMode   string
	SourceReadReason string
)

const (
	SourceReadFull    SourceReadMode = "full"
	SourceReadSkipped SourceReadMode = "skipped"

	SourceReadChanged   SourceReadReason = "changed"
	SourceReadPending   SourceReadReason = "pending"
	SourceReadPeriodic  SourceReadReason = "periodic"
	SourceReadMissing   SourceReadReason = "missing"
	SourceReadElected   SourceReadReason = "elected"
	SourceReadUnchanged SourceReadReason = "unchanged"
)

// SourceReadOutcome is one (mode, reason) pair a refresh round can report.
type SourceReadOutcome struct {
	Mode   SourceReadMode
	Reason SourceReadReason
}

// AllSourceReadOutcomes lists every pair a round can report: a full read has
// one of five reasons, a skipped round exactly one.
func AllSourceReadOutcomes() []SourceReadOutcome {
	return []SourceReadOutcome{
		{SourceReadFull, SourceReadChanged}, {SourceReadFull, SourceReadPending}, {SourceReadFull, SourceReadPeriodic},
		{SourceReadFull, SourceReadMissing}, {SourceReadFull, SourceReadElected},
		{SourceReadSkipped, SourceReadUnchanged},
	}
}

// ValidSourceReadOutcome reports whether the pair is one a round can report.
func ValidSourceReadOutcome(mode SourceReadMode, reason SourceReadReason) bool {
	for _, outcome := range AllSourceReadOutcomes() {
		if outcome.Mode == mode && outcome.Reason == reason {
			return true
		}
	}
	return false
}

// ControlSourceRole is what this process is to the control plane's source
// refresh. Only the leader refreshes; a follower reads what the leader
// published; unacquired means the process could not find out either way --
// the acquisition failed or has not happened. follower and unacquired are
// opposite situations for whoever is looking ("someone else is doing it"
// and "nobody may be doing it"), and they were one boolean until a
// deployment sat for hours with no way to tell which it was in.
type ControlSourceRole string

const (
	ControlSourceRoleLeader     ControlSourceRole = "leader"
	ControlSourceRoleFollower   ControlSourceRole = "follower"
	ControlSourceRoleUnacquired ControlSourceRole = "unacquired"
)

// ControlSourceRoles is the closed set, for a reader that pre-creates a
// series per role.
var ControlSourceRoles = []ControlSourceRole{ControlSourceRoleLeader, ControlSourceRoleFollower, ControlSourceRoleUnacquired}

// ControlSourceMode is the state of this process's control refresh right
// now, as opposed to a transition into it. healthy: the last round
// succeeded. degraded_last_good: the last round failed and the process runs
// on the last good catalog, of which there is one somewhere -- this process
// or one before it under the same store succeeded once. never_succeeded: the
// last round failed and no round is known to have ever succeeded under this
// store, which is the state a deployment is in from its first minute when
// the source is broken from the first minute, and the one a transition
// counter cannot show at all.
type ControlSourceMode string

const (
	ControlSourceModeHealthy          ControlSourceMode = "healthy"
	ControlSourceModeDegradedLastGood ControlSourceMode = "degraded_last_good"
	ControlSourceModeNeverSucceeded   ControlSourceMode = "never_succeeded"
)

// ControlSourceModes is the closed set.
var ControlSourceModes = []ControlSourceMode{
	ControlSourceModeHealthy, ControlSourceModeDegradedLastGood, ControlSourceModeNeverSucceeded,
}

// ControlSourceRoundFacts is one refresh round of the control plane's
// source, reported on every round whether it succeeded or failed. A failed
// round names its exit, which the metric counts under a closed set; the
// cause travels in the observation's Err. The transition observation that
// already exists reports the first failure of an episode and nothing after,
// so an episode that never ends is one increment and one line: this fact is
// what makes the rounds after it count.
type ControlSourceRoundFacts struct {
	// Outcome is succeeded or failed.
	Outcome string
	// Exit is none for a succeeded round and where the round stopped for a
	// failed one; a failed round with no exit reads as other.
	Exit string
}

const (
	ControlSourceRoundSucceeded = "succeeded"
	ControlSourceRoundFailed    = "failed"
	// ControlSourceExitNone and ControlSourceExitOther are the two exits this
	// package has to know: the one a succeeded round carries, and the one a
	// failed round that named none is given.
	ControlSourceExitNone  = "none"
	ControlSourceExitOther = "other"
)

// ActivationFailureFacts carries fixed classification, bounded counts and a
// bounded diagnostic sample. Query Group identity is logged only for
// reactivation/not_drained and never becomes a metric label.
type ActivationFailureFacts struct {
	Stage                                ActivationFailureStage
	Class                                ActivationFailureClass
	DrainingQueryGroups                  int
	CandidateQueryGroups                 int
	ReappearedQueryGroups                int
	ReappearedQueryGroupSamples          []string
	ReappearedQueryGroupSamplesTruncated bool
}

// AlgorithmProvenance carries bounded diagnostic coordinates for one
// evaluation or named input. It intentionally contains no series identity,
// dimensions or payload; those remain in the request and are never logged.
type AlgorithmProvenance struct {
	LevelID       uint32 `json:"level_id,omitempty"`
	RequirementID string `json:"requirement_id,omitempty"`
	QueryRef      string `json:"query_ref,omitempty"`
	QueryRevision string `json:"query_revision,omitempty"`
	SourceTime    int64  `json:"source_time,omitempty"`
	QueryStart    int64  `json:"query_start,omitempty"`
	QueryEnd      int64  `json:"query_end,omitempty"`
}

type AlgorithmEvaluationFact struct {
	SourceAlgorithmFamily AlgorithmFamily           `json:"source_algorithm_family"`
	DetectorKind          AlgorithmDetectorKind     `json:"detector_kind"`
	Result                AlgorithmEvaluationResult `json:"result"`
	ReasonCode            ReasonCode                `json:"reason_code"`
	Provenance            AlgorithmProvenance       `json:"provenance,omitempty"`
}

// RecoveryGateCause is the state of the first other Level a RECOVERY record
// was decided beside. None of them holds the envelope: a RECOVERY speaks for
// its own Level only (trigger recoveryGateV2). The set is closed: it is a
// metric label.
type RecoveryGateCause string

const (
	// RecoveryGateLevelUnavailable: another Level's state could not be
	// established this round.
	RecoveryGateLevelUnavailable RecoveryGateCause = "level_unavailable"
	// RecoveryGateLevelRecovering: another Level read NORMAL with recovery
	// enabled, so a window inside its recovery span still triggers.
	RecoveryGateLevelRecovering RecoveryGateCause = "level_recovering"
	// RecoveryGateLevelWithoutRecovery: another Level read NORMAL with its
	// recovery disabled.
	RecoveryGateLevelWithoutRecovery RecoveryGateCause = "level_without_recovery"
)

// RecoveryGateFact counts, for one evaluation, the records the gate decided
// under one cause.
type RecoveryGateFact struct {
	Cause   RecoveryGateCause `json:"cause"`
	Records uint64            `json:"records"`
}

// LevelOutcomeFact is one cell of an evaluation's Level outcomes: the
// outcome kind, the reason for an UNKNOWN or TERMINAL one (empty for the
// business outcomes, which carry none), and how many Level outcomes fell in
// it. LevelOutcomeKinds is the closed list of kinds; the reason is bounded
// by the observation reason catalog, and one outside it is reported as
// other so the cell count stays bounded.
type LevelOutcomeFact struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
	Count   uint64 `json:"count"`
}

// LevelOutcomeKinds is every Level outcome kind, in the execution's words.
var LevelOutcomeKinds = []string{"NORMAL", "ABNORMAL", "RECOVERY", "UNKNOWN", "TERMINAL"}

// LevelOutcomeReasonOther is the reason label for an UNKNOWN or TERMINAL
// outcome whose reason is not a coverage reason.
const LevelOutcomeReasonOther = "other"

// LevelOutcomeReasons is every reason an UNKNOWN or TERMINAL Level outcome
// is counted under by name: the coverage class of the observation catalog -
// the effective time, a warming or gapped window, a gap guard, a query that
// answered partially or not at all - and other for every reason outside it.
// The coverage class is the family a Level that was not judged carries; a
// deterministic or retryable reason on a TERMINAL outcome is a defect with
// its own line and is counted as other here rather than leaking every
// exact code into a label. The metric pre-creates the two outcomes over
// this list, which bounds it.
func LevelOutcomeReasons() []string {
	reasons := make([]string, 0, 1)
	for _, definition := range contract.ReasonCatalogV2() {
		if definition.Class == contract.ReasonClassCoverage && definition.Domains.Has(contract.ReasonDomainObservation) {
			reasons = append(reasons, definition.Code)
		}
	}
	sort.Strings(reasons)
	return append(reasons, LevelOutcomeReasonOther)
}

// LevelOutcomeReasonLabel is the metric label for a Level outcome's reason:
// the reason itself when it is a coverage reason, other otherwise. The log
// line keeps the exact reason; only the metric is bounded this way.
func LevelOutcomeReasonLabel(reason string) string {
	for _, known := range LevelOutcomeReasons() {
		if known == reason {
			return reason
		}
	}
	return LevelOutcomeReasonOther
}

func normalizeLevelOutcomeFacts(observation Observation) []LevelOutcomeFact {
	if observation.Component != ComponentEvaluation || observation.Stage != StageEvaluationCompleted {
		return nil
	}
	facts := make([]LevelOutcomeFact, 0, len(observation.LevelOutcomes))
	for _, fact := range observation.LevelOutcomes {
		if fact.Count == 0 {
			continue
		}
		known := false
		for _, kind := range LevelOutcomeKinds {
			known = known || kind == fact.Outcome
		}
		if !known {
			continue
		}
		if fact.Outcome == "UNKNOWN" || fact.Outcome == "TERMINAL" {
			if fact.Reason == "" {
				fact.Reason = LevelOutcomeReasonOther
			}
		} else {
			fact.Reason = ""
		}
		facts = append(facts, fact)
	}
	if len(facts) == 0 {
		return nil
	}
	return facts
}

// OpenAlertGateOutcome is what the recovery gate, the consumer's open alert
// set, did with a RECOVERY record. The set is closed: it is a metric label.
// Every RECOVERY record is counted here; a RecoveryGateCause describes the
// record beside it and may count the same record.
type OpenAlertGateOutcome string

const (
	// OpenAlertGatePassed: the consumer holds an open alert; the envelope went.
	OpenAlertGatePassed OpenAlertGateOutcome = "passed"
	// OpenAlertGateHeldNoOpenAlert: the consumer holds no alert on the series;
	// nothing to resolve, no envelope.
	OpenAlertGateHeldNoOpenAlert OpenAlertGateOutcome = "held_no_open_alert"
	// OpenAlertGateHeldFingerprintUnknown: the series identity the consumer
	// keys alerts by could not be built; held and named rather than read as
	// absent.
	OpenAlertGateHeldFingerprintUnknown OpenAlertGateOutcome = "held_fingerprint_unknown"
	// OpenAlertGateNotConfigured: the evaluation ran without a set; the
	// envelope went as before the gate existed. On a production worker this
	// counting is the wiring having come apart.
	OpenAlertGateNotConfigured OpenAlertGateOutcome = "not_configured"
	// OpenAlertGateProtocolNotGated: the Plan does not publish the alert
	// consumer's protocol; the set was not asked.
	OpenAlertGateProtocolNotGated OpenAlertGateOutcome = "protocol_not_gated"
)

// OpenAlertGateOutcomes lists every outcome, for the metric that pre-creates
// them all.
var OpenAlertGateOutcomes = []OpenAlertGateOutcome{
	OpenAlertGatePassed, OpenAlertGateHeldNoOpenAlert, OpenAlertGateHeldFingerprintUnknown,
	OpenAlertGateNotConfigured, OpenAlertGateProtocolNotGated,
}

// OpenAlertGateFact counts, for one evaluation, the records the open alert
// gate decided under one outcome.
type OpenAlertGateFact struct {
	Outcome OpenAlertGateOutcome `json:"outcome"`
	Records uint64               `json:"records"`
}

type AlgorithmInputFact struct {
	SourceAlgorithmFamily AlgorithmFamily          `json:"source_algorithm_family"`
	DetectorKind          AlgorithmDetectorKind    `json:"detector_kind"`
	InputName             AlgorithmInputName       `json:"input_name"`
	DependencyPoint       AlgorithmDependencyPoint `json:"dependency_point"`
	Result                AlgorithmInputResult     `json:"result"`
	ReasonCode            ReasonCode               `json:"reason_code"`
	Provenance            AlgorithmProvenance      `json:"provenance,omitempty"`
}

type TraceFields struct {
	TraceID                 string
	ExecutionID             string
	MessageID               string
	QueryGroupKey           string
	SnapshotRevision        string
	QueryRevision           string
	ScheduleRevision        string
	ScheduleSegmentStart    int64
	DuePlanSetDigest        string
	OwnerID                 string
	OwnerEpoch              uint64
	EvaluationTime          int64
	StrategyID              string
	BusinessID              string
	LevelID                 string
	TerminalScope           string
	TerminalFieldPath       string
	RecordID                string
	DimensionIdentityDigest string
	Topic                   string
	Partition               int32
	PartitionKnown          bool
	Offset                  int64
	OffsetKnown             bool
	SourceWindow            string
}

type Observation struct {
	// GapStatements, when set, says one Plan carried more than one gap marker
	// statement in a single Slot. A reading rather than a refusal: the Slot
	// went on, and this is what says the shape happened.
	// GapApplySite is which of the Slot's two gap applies refused, empty when
	// none did. Its own field rather than only inside the error text: which
	// write refused is what separates this Slot's other call from a writer in
	// another process, and that cannot be grouped or counted out of a sentence.
	GapApplySite  string
	GapStatements *GapStatementFacts
	GapExtensions []*GapExtensionFacts
	GapConflict   *GapExtensionFacts
	QueryCooldown *QueryCooldownFacts
	// OutputRejection is the sink's own account of refusing to write the
	// round's events -- the reason word and the converter's or client's
	// sentence, apart from the error chain that wraps them -- on a failed
	// event_acked observation. Nil when the write failed for any other
	// reason, or did not fail.
	OutputRejection *OutputRejectionFacts
	// OutputWrite is the sink's own count of the batch on an event_acked
	// observation: messages handed to the client and events the protocol
	// had no message for. Nil when the sink did not count -- and left
	// absent rather than read as zero, because a success with zero messages
	// is a real state this field exists to name.
	OutputWrite *OutputWriteFacts
	// OutputWireFormat is the wire format the Plan's events are published
	// as, on the evaluation line of the Plan that decided them (empty when
	// the line is not a Plan's); OutputWireFormats how many events of an
	// event_acked batch went out as each.
	OutputWireFormat  string
	OutputWireFormats OutputWireFormatCounts
	// OutputEventKinds is OutputWireFormats split by event kind, on the same
	// event_acked observation.
	OutputEventKinds OutputEventKindCounts
	// PlanSeriesMatched is how many PRIMARY series this Slot's query bound to
	// the Plan an evaluation line is about, on every evaluation line of that
	// Plan and on the completion-only line of a Plan bound to none. A Plan
	// bound to no series evaluates nothing, writes no state and advances no
	// guard: a gap guard on such a Plan sits at 0 of N for as long as the
	// Plan has no input, and read without this number that is a guard
	// warming. Nil when the line is not a Plan's.
	PlanSeriesMatched      *int
	RunOutcome             string
	Attempted              bool
	ExecuteOutcome         string
	ProgressCompletionKind string
	// ProgressCompletionCause says which of the several conditions that all
	// complete a Slot as UNAVAILABLE actually happened. The kind alone cannot
	// tell "the data has not landed yet", which clears itself, from "a Plan
	// could not be decided", which does not.
	ProgressCompletionCause string
	// ProgressCompletionReason is the reason belonging to that cause, one level
	// further down. LEVEL_OUTCOME_UNKNOWN is required by contract to carry a
	// reason of either the coverage class or the retryable class, and those need
	// opposite responses: coverage means the data does not reach this window,
	// retryable means it clears on its own. Without this the two are one
	// population -- on a running deployment, 61 of 62 objects sharing a label
	// that could not say whose problem they were.
	ProgressCompletionReason string
	// HistoryCoverage says how far short of the required window the series in
	// this run actually were. HISTORY_WARMING alone cannot tell a series two
	// rounds into its life, which converges by itself, from a series whose
	// lifetime is shorter than the window, which is short on every round for
	// ever and by design will never produce a recovery. Both report the same
	// reason on every round, so without the counts the two are one population.
	HistoryCoverage *HistoryCoverageFacts
	// HistoryCoverageRejected is set, and HistoryCoverage nil, when the
	// coverage facts this observation carried did not pass normalize: the
	// rule they broke and, for a rule about one window, its series. A
	// refused reading leaves this in its place rather than nothing, so the
	// row and the log say the server declined the reading instead of
	// looking like a run with every window complete.
	HistoryCoverageRejected *CoverageRejection
	// PrimaryInput is what the Slot's PRIMARY query answered with, on the
	// completion line: whether it answered whole, and whether it carried any
	// records. It is the fact that says whose a hole is. A series missing
	// from a round whose query answered whole and carried records was not in
	// the result -- the data's; one missing from a round whose query did not
	// answer was never asked for -- this side's. Nil when the completion
	// carried no primary fact (a Slot skipped without a query).
	PrimaryInput          *PrimaryInputFacts
	Dispatcher            *DispatcherFacts
	DispatchTurnaway      *DispatchTurnawayFacts
	PermitWait            *PermitWaitFacts
	ExpiredRange          *ExpiredRangeFacts
	Component             Component
	Stage                 Stage
	Result                Result
	Operation             Operation
	Direction             Direction
	ReasonCode            ReasonCode
	Duration              time.Duration
	Counts                Counts
	Trace                 TraceFields
	Err                   error
	CapacityBudget        CapacityBudget
	CapacityRejection     *CapacityRejectionFacts
	SlotBudgetUsage       *SlotBudgetUsageFacts
	SlotTiming            *SlotTimingFacts
	SourceKind            SourceKind
	QueryPermit           *QueryPermitFacts
	NoDataSlot            *NoDataSlotFacts
	TargetResolution      *TargetResolutionFacts
	NoDataStall           *NoDataStallFacts
	NoDataAbsence         *NoDataAbsenceFacts
	GapProgress           *GapProgressFacts
	NoDataMemoryRefusal   *NoDataMemoryRefusalFacts
	NoDataMemoryWrite     *NoDataMemoryWriteFacts
	NoDataMemoryRead      *NoDataMemoryReadFacts
	NoDataMemoryRenewal   *NoDataMemoryRenewalFacts
	ExecutionEvidence     *ExecutionEvidenceFacts
	FrozenStateRenewal    *FrozenStateRenewalFacts
	SourceWithheld        *SourceWithheldFacts
	NoDataCensus          *NoDataCensusFacts
	SegmentContent        *SegmentContentFacts
	RuntimeConfig         *RuntimeConfigFacts
	QueryFailure          *QueryFailureFacts
	QueryStatus           []QueryStatusFacts
	QueryUnavailable      []QueryUnavailableFacts
	QueryTiming           *QueryTimingFacts
	SlotReadiness         *SlotReadinessFacts
	ShortPeriodCompletion *ShortPeriodCompletionFacts
	StateApplyChunk       *StateApplyChunkFacts
	StateWriteReuse       *StateWriteReuseFacts
	StateAlreadyApplied   *StateAlreadyAppliedFacts
	StateVersionConflict  *StateVersionConflictFacts
	ActiveQGSet           *ActiveQGSetFacts
	ScheduleCutover       *ScheduleCutoverFacts
	ReplayExpiry          *ReplayExpiryFacts
	// HeldBy is what the round before this one did with the Query Group. It
	// sits on the Observation rather than inside one cohort's fact bundle:
	// it first shipped inside ShortPeriodCompletionFacts, and every Query
	// Group on a sixty-second or longer period -- which is most of the ones
	// whose Slots are being skipped -- has no such bundle, so their
	// completion lines carried no cause at all.
	HeldBy *HeldByFacts
	// SlotCompletionKind is the completion the Slot reached, beside the reason
	// the line reports.
	//
	// Separate fields, not required to agree, and they disagree in the case
	// this line is hardest to read: a Slot whose Level outcomes are UNKNOWN
	// completes COMPLETED_WITH_UNAVAILABLE and copies GAP_SKIPPED up from the
	// Level. On the reason alone that is the same line as a Slot given up on
	// before it ran, and the two want opposite investigations. Its own field
	// rather than ProgressCompletionKind, which target flow reads to fill
	// Completion on a line that already emits its own.
	SlotCompletionKind   string
	RangeDistance        *RangeDistanceFacts
	RangeGate            *RangeGateFacts
	SlotWait             *SlotWaitFacts
	ObjectCatalog        *ObjectCatalogFacts
	ObjectRead           *ObjectReadFacts
	StateGenerationSkew  *StateGenerationSkewFacts
	StateCarry           *StateCarryFacts
	ActivationHold       *ActivationHoldFacts
	LegacyMigration      *LegacyQGMigrationFacts
	DrainingQG           *DrainingQGFacts
	Rebalance            *RebalanceFacts
	ControlReads         *ControlReadFacts
	AssignmentIndex      *AssignmentIndexFacts
	AssignmentSweep      *AssignmentSweepFacts
	ViewStream           *ViewStreamFacts
	CursorAdvance        *CursorAdvanceFacts
	SourceRefresh        *SourceRefreshFacts
	ActivationFailure    *ActivationFailureFacts
	AlgorithmEvaluations []AlgorithmEvaluationFact
	AlgorithmInputs      []AlgorithmInputFact
	RecoveryGates        []RecoveryGateFact
	OpenAlertGates       []OpenAlertGateFact
	// LevelOutcomes is what the evaluation concluded per Level outcome and
	// reason, on an evaluation_completed line: how many Level outcomes were
	// NORMAL, ABNORMAL, RECOVERY, and for UNKNOWN and TERMINAL by which
	// reason. The line's own reason is the Plan's fold - one word for the
	// worst Level - so a Level suppressed by its effective time or held by a
	// warming window had no name on the line unless it was that word.
	LevelOutcomes []LevelOutcomeFact
	// Shardability is the catalog counted by whether a value-list split
	// could be expressed at all, once per publication.
	Shardability *ShardabilityFacts
	// ShardQuery is whether that object's own query could express the split
	// the planner decided on. Beside SplitPlan and not folded into it: one
	// says whether the pieces would be even, the other whether the strategy
	// can be cut at all, and a strategy can be worth splitting and
	// impossible to express.
	ShardQuery *ShardQueryFacts
	// SplitPlan is one object's split decision as the Leader's dry run
	// reached it; SplitRound is what that round looked at. Two structures
	// because they have two subjects - one object, and one round.
	SplitPlan  *SplitPlanFacts
	SplitRound *SplitRoundFacts
	// DimensionCensus is one candidate Plan's census as this Slot wrote it.
	DimensionCensus    *DimensionCensusFacts
	ControlSourceRound *ControlSourceRoundFacts
	normalized         bool
	stageReasonBucket  bool

	// DurationKnown distinguishes a measured zero from an absent timer. Older
	// producers with a positive Duration are also understood as measured.
	DurationKnown          bool
	EvaluationOwner        CostPlanIdentity
	EvaluationRecordsKnown bool
}

type Observer interface {
	Observe(context.Context, Observation)
}

type ObserverFunc func(context.Context, Observation)

func (f ObserverFunc) Observe(ctx context.Context, observation Observation) {
	if f != nil {
		f(ctx, observation)
	}
}

type NopObserver struct{}

func (NopObserver) Observe(context.Context, Observation) {}

type multiObserver []Observer

func Multi(observers ...Observer) Observer {
	result := make(multiObserver, 0, len(observers))
	for _, observer := range observers {
		if observer != nil {
			result = append(result, observer)
		}
	}
	if len(result) == 0 {
		return NopObserver{}
	}
	return result
}

func (m multiObserver) Observe(ctx context.Context, observation Observation) {
	observation = NormalizeObservation(observation)
	for _, observer := range m {
		observer.Observe(ctx, observation)
	}
}

func NormalizeObservation(observation Observation) Observation {
	if observation.normalized {
		return observation
	}
	rawReason := observation.ReasonCode
	observation.Component, observation.Stage = NormalizeComponentStage(observation.Component, observation.Stage)
	if observation.Component != ComponentControlPlane || observation.Result != Result(ResultRecovered) {
		observation.Result = NormalizeResult(observation.Result)
	}
	observation.stageReasonBucket = rawReason == "" ||
		(rawReason == ReasonNone && !resultAllowsNone(observation.Result))
	observation.Operation = NormalizeOperation(observation.Operation)
	observation.Direction = NormalizeDirection(observation.Direction)
	observation.SourceKind = NormalizeSourceKind(observation.SourceKind)
	if observation.Component == ComponentControlPlane && observation.SourceKind != "" && isSourceReasonClass(observation.ReasonCode) {
		observation.ReasonCode = rawReason
	} else {
		observation.ReasonCode = NormalizeReason(observation.ReasonCode, observation.Result)
	}
	observation.CapacityBudget = NormalizeCapacityBudget(observation.CapacityBudget)
	observation.CapacityRejection = normalizeCapacityRejection(observation)
	observation.QueryCooldown = normalizeQueryCooldownFacts(observation.QueryCooldown)
	observation.HistoryCoverage, observation.HistoryCoverageRejected = normalizeHistoryCoverageFacts(observation.HistoryCoverage)
	observation.PrimaryInput = normalizePrimaryInputFacts(observation.PrimaryInput)
	observation.QueryPermit = normalizeQueryPermitFacts(observation.QueryPermit)
	observation.NoDataSlot = normalizeNoDataSlotFacts(observation.NoDataSlot)
	observation.NoDataAbsence = normalizeNoDataAbsenceFacts(observation.NoDataAbsence)
	observation.SourceWithheld = normalizeSourceWithheldFacts(observation.SourceWithheld)
	observation.NoDataCensus = normalizeNoDataCensusFacts(observation.NoDataCensus)
	observation.SegmentContent = normalizeSegmentContentFacts(observation.SegmentContent)
	observation.QueryTiming = normalizeTimingFacts(observation)
	observation.ShortPeriodCompletion = normalizeShortPeriodCompletion(observation)
	observation.StateApplyChunk = normalizeStateApplyChunk(observation)
	observation.QueryFailure = normalizeQueryFailure(observation.Component, observation.Stage, observation.QueryFailure)
	observation.QueryStatus = normalizeQueryStatus(observation.Component, observation.Stage, observation.QueryStatus)
	observation.QueryUnavailable = normalizeQueryUnavailable(observation.Component, observation.Stage, observation.QueryUnavailable)
	observation.SlotReadiness = normalizeSlotReadiness(observation.SlotReadiness)
	observation.DimensionCensus = normalizeDimensionCensusFacts(observation.DimensionCensus)
	observation.SplitPlan = normalizeSplitPlanFacts(observation.SplitPlan)
	if observation.RuntimeConfig != nil {
		if observation.Component != ComponentRuntime || observation.Stage != StageConfigLoaded {
			observation.RuntimeConfig = nil
		} else {
			facts := *observation.RuntimeConfig
			observation.RuntimeConfig = &facts
		}
	}
	observation.ActiveQGSet = normalizeActiveQGSetFacts(observation.ActiveQGSet)
	observation.ScheduleCutover = normalizeScheduleCutoverFacts(observation.ScheduleCutover)
	observation.ObjectCatalog = normalizeObjectCatalogFacts(observation.ObjectCatalog)
	observation.ObjectRead = normalizeObjectReadFacts(observation.ObjectRead)
	observation.StateGenerationSkew = normalizeStateGenerationSkewFacts(observation.StateGenerationSkew)
	observation.StateCarry = normalizeStateCarryFacts(observation.StateCarry)
	observation.ActivationHold = normalizeActivationHoldFacts(observation.ActivationHold)
	observation.LegacyMigration = normalizeLegacyQGMigrationFacts(observation.LegacyMigration)
	observation.DrainingQG = normalizeDrainingQGFacts(observation.DrainingQG)
	observation.Rebalance = normalizeRebalanceFacts(observation.Rebalance)
	observation.ControlReads = normalizeControlReadFacts(observation.ControlReads)
	observation.RangeDistance = normalizeRangeDistanceFacts(observation.RangeDistance)
	observation.RangeGate = normalizeRangeGateFacts(observation.RangeGate)
	observation.HeldBy = normalizeHeldByFacts(observation.HeldBy)
	if observation.ReplayExpiry != nil {
		normalizedExpiry := *observation.ReplayExpiry
		normalizedExpiry.HeldBy = normalizeHeldByFacts(normalizedExpiry.HeldBy)
		observation.ReplayExpiry = &normalizedExpiry
	}
	observation.AssignmentIndex = normalizeAssignmentIndexFacts(observation.AssignmentIndex)
	observation.CursorAdvance = normalizeCursorAdvanceFacts(observation.CursorAdvance)
	observation.SourceRefresh = normalizeSourceRefreshFacts(observation.Component, observation.Stage, observation.SourceRefresh)
	observation.ActivationFailure = normalizeActivationFailureFacts(
		observation.Component, observation.Stage, observation.ActivationFailure,
	)
	observation.AlgorithmEvaluations, observation.AlgorithmInputs = normalizeAlgorithmFacts(observation)
	observation.RecoveryGates = normalizeRecoveryGateFacts(observation)
	observation.LevelOutcomes = normalizeLevelOutcomeFacts(observation)
	observation.OpenAlertGates = normalizeOpenAlertGateFacts(observation)
	observation.ControlSourceRound = normalizeControlSourceRoundFacts(observation)
	observation.Counts = normalizeCounts(observation.Counts)
	observation.normalized = true
	return observation
}

func normalizeActivationFailureFacts(
	component Component,
	stage Stage,
	facts *ActivationFailureFacts,
) *ActivationFailureFacts {
	if facts == nil || component != ComponentControlPlane || stage != StageActivationFailed ||
		!validActivationFailureStage(facts.Stage) || !validActivationFailureClass(facts.Class) {
		return nil
	}
	normalized := *facts
	if normalized.Stage != ActivationFailureStageReactivation ||
		normalized.DrainingQueryGroups < 0 || normalized.CandidateQueryGroups < 0 ||
		normalized.ReappearedQueryGroups < 0 {
		normalized.DrainingQueryGroups = 0
		normalized.CandidateQueryGroups = 0
		normalized.ReappearedQueryGroups = 0
		normalized.ReappearedQueryGroupSamples = nil
		normalized.ReappearedQueryGroupSamplesTruncated = false
		return &normalized
	}
	if normalized.Class != ActivationFailureClassNotDrained {
		normalized.ReappearedQueryGroupSamples = nil
		normalized.ReappearedQueryGroupSamplesTruncated = false
		return &normalized
	}
	normalized.ReappearedQueryGroupSamples = append([]string(nil), facts.ReappearedQueryGroupSamples...)
	limit := normalized.ReappearedQueryGroups
	if limit > MaxActivationFailureQGLogSamples {
		limit = MaxActivationFailureQGLogSamples
	}
	if len(normalized.ReappearedQueryGroupSamples) > limit {
		normalized.ReappearedQueryGroupSamples = normalized.ReappearedQueryGroupSamples[:limit]
		normalized.ReappearedQueryGroupSamplesTruncated = true
	}
	return &normalized
}

var allActivationFailureStages = []ActivationFailureStage{
	ActivationFailureStageActivationLoad, ActivationFailureStageCandidateLoad,
	ActivationFailureStageCurrentRecovery, ActivationFailureStageReactivation,
	ActivationFailureStageCompile, ActivationFailureStageScheduleCutover,
	ActivationFailureStagePersist,
}

var allActivationFailureClasses = []ActivationFailureClass{
	ActivationFailureClassUnavailable, ActivationFailureClassCorrupt,
	ActivationFailureClassEpochCollision, ActivationFailureClassNotDrained,
	ActivationFailureClassProjectionConflict, ActivationFailureClassScheduleConflict,
	ActivationFailureClassCoverageConflict, ActivationFailureClassCASConflict,
	ActivationFailureClassDependencyIO, ActivationFailureClassOther,
}

func validActivationFailureStage(stage ActivationFailureStage) bool {
	return slices.Contains(allActivationFailureStages, stage)
}

func validActivationFailureClass(class ActivationFailureClass) bool {
	return slices.Contains(allActivationFailureClasses, class)
}

func AllActivationFailureStages() []ActivationFailureStage {
	return append([]ActivationFailureStage(nil), allActivationFailureStages...)
}

func AllActivationFailureClasses() []ActivationFailureClass {
	return append([]ActivationFailureClass(nil), allActivationFailureClasses...)
}

// ActivationFailureReason is the reason code an activation failure is
// reported under: stage/class, one word from two closed lists. It is the
// same word the fleet page groups CUTOVER_FAILING on, so the log line and
// the first screen name a failure identically.
//
// The activation_failed line used to carry contract_retryable here, which
// the normaliser folds to _other; the classification the line already had
// in its own fields was not on the field people grep. A running deployment
// logged two such lines a minute for half a day, each saying reason_code
// _other beside activation_failure_class schedule_conflict.
func ActivationFailureReason(stage ActivationFailureStage, class ActivationFailureClass) ReasonCode {
	return ReasonCode(string(stage) + "/" + string(class))
}

// activationFailureReasons is the closed product of the two lists, so the
// normaliser can keep every one verbatim and fold anything else.
var activationFailureReasons = func() []ReasonCode {
	reasons := make([]ReasonCode, 0, len(allActivationFailureStages)*len(allActivationFailureClasses))
	for _, stage := range allActivationFailureStages {
		for _, class := range allActivationFailureClasses {
			reasons = append(reasons, ActivationFailureReason(stage, class))
		}
	}
	return reasons
}()

func normalizeSourceRefreshFacts(component Component, stage Stage, facts *SourceRefreshFacts) *SourceRefreshFacts {
	if facts == nil || component != ComponentControlPlane || stage != StageSnapshotRefreshed ||
		!validSourceRefreshStatus(facts.Status) {
		return nil
	}
	normalized := *facts
	if (normalized.SnapshotRevision == "") != (normalized.PublicationEpoch == 0) {
		normalized.SnapshotRevision = ""
		normalized.PublicationEpoch = 0
	}
	if !normalized.CountsKnown || normalized.OldQueryGroups < 0 || normalized.NewQueryGroups < 0 ||
		normalized.AddedQueryGroups < 0 || normalized.RetiredQueryGroups < 0 ||
		normalized.OldQueryGroups+normalized.AddedQueryGroups !=
			normalized.NewQueryGroups+normalized.RetiredQueryGroups {
		normalized.CountsKnown = false
		normalized.OldQueryGroups = 0
		normalized.NewQueryGroups = 0
		normalized.AddedQueryGroups = 0
		normalized.RetiredQueryGroups = 0
	}
	if (normalized.ReadMode != "" || normalized.ReadReason != "") &&
		!ValidSourceReadOutcome(normalized.ReadMode, normalized.ReadReason) {
		normalized.ReadMode = ""
		normalized.ReadReason = ""
	}
	if normalized.StrategiesRead < 0 {
		normalized.StrategiesRead = 0
	}
	if !normalized.ChangeSignalPresent {
		normalized.ChangeSignalAgeSeconds = 0
	}
	return &normalized
}

func validSourceRefreshStatus(status SourceRefreshStatus) bool {
	switch status {
	case SourceRefreshPending, SourceRefreshPublished, SourceRefreshUnchanged, SourceRefreshConflict:
		return true
	default:
		return false
	}
}

func AllSourceRefreshStatuses() []SourceRefreshStatus {
	return []SourceRefreshStatus{
		SourceRefreshPending, SourceRefreshPublished, SourceRefreshUnchanged, SourceRefreshConflict,
	}
}

// normalizeRecoveryGateFacts keeps the gate facts of a completed evaluation
// whose cause is in the closed set and that count something. The set is
// produced by this process, so a cause outside it is a defect, and it is
// dropped the way an unknown algorithm result is.
func normalizeRecoveryGateFacts(observation Observation) []RecoveryGateFact {
	if observation.Component != ComponentEvaluation || observation.Stage != StageEvaluationCompleted {
		return nil
	}
	facts := make([]RecoveryGateFact, 0, len(observation.RecoveryGates))
	for _, fact := range observation.RecoveryGates {
		if fact.Records == 0 {
			continue
		}
		switch fact.Cause {
		case RecoveryGateLevelUnavailable, RecoveryGateLevelRecovering, RecoveryGateLevelWithoutRecovery:
			facts = append(facts, fact)
		}
	}
	if len(facts) == 0 {
		return nil
	}
	return facts
}

// normalizeControlSourceRoundFacts keeps a round fact only where a round is
// reported, and makes the exit agree with the outcome: a succeeded round has
// none, a failed round has what it named or other.
func normalizeControlSourceRoundFacts(observation Observation) *ControlSourceRoundFacts {
	facts := observation.ControlSourceRound
	if facts == nil || observation.Component != ComponentControlPlane || observation.Stage != StageSnapshotRefreshed {
		return nil
	}
	normalized := *facts
	switch normalized.Outcome {
	case ControlSourceRoundSucceeded:
		normalized.Exit = ControlSourceExitNone
	case ControlSourceRoundFailed:
		if normalized.Exit == "" || normalized.Exit == ControlSourceExitNone {
			normalized.Exit = ControlSourceExitOther
		}
	default:
		return nil
	}
	return &normalized
}

// normalizeOpenAlertGateFacts is normalizeRecoveryGateFacts for the second
// gate: same stage, same closed set, same treatment of a value outside it.
func normalizeOpenAlertGateFacts(observation Observation) []OpenAlertGateFact {
	if observation.Component != ComponentEvaluation || observation.Stage != StageEvaluationCompleted {
		return nil
	}
	facts := make([]OpenAlertGateFact, 0, len(observation.OpenAlertGates))
	for _, fact := range observation.OpenAlertGates {
		if fact.Records == 0 {
			continue
		}
		switch fact.Outcome {
		case OpenAlertGatePassed, OpenAlertGateHeldNoOpenAlert, OpenAlertGateHeldFingerprintUnknown,
			OpenAlertGateNotConfigured, OpenAlertGateProtocolNotGated:
			facts = append(facts, fact)
		}
	}
	if len(facts) == 0 {
		return nil
	}
	return facts
}

func normalizeAlgorithmFacts(observation Observation) ([]AlgorithmEvaluationFact, []AlgorithmInputFact) {
	if observation.Component != ComponentEvaluation || observation.Stage != StageEvaluationCompleted {
		return nil, nil
	}
	evaluations := make([]AlgorithmEvaluationFact, 0, len(observation.AlgorithmEvaluations))
	for _, fact := range observation.AlgorithmEvaluations {
		if !validAlgorithmFamilyDetector(fact.SourceAlgorithmFamily, fact.DetectorKind) ||
			!validAlgorithmEvaluationResult(fact.Result) {
			continue
		}
		fact.ReasonCode = NormalizeReason(fact.ReasonCode, algorithmEvaluationObservationResult(fact.Result))
		evaluations = append(evaluations, fact)
	}
	inputs := make([]AlgorithmInputFact, 0, len(observation.AlgorithmInputs))
	for _, fact := range observation.AlgorithmInputs {
		if !validAlgorithmFamilyDetector(fact.SourceAlgorithmFamily, fact.DetectorKind) ||
			!validAlgorithmInputName(fact.InputName) || !validAlgorithmDependencyPoint(fact.DependencyPoint) ||
			!validAlgorithmInputResult(fact.Result) {
			continue
		}
		fact.ReasonCode = NormalizeReason(fact.ReasonCode, algorithmInputObservationResult(fact.Result))
		inputs = append(inputs, fact)
	}
	return evaluations, inputs
}

func validAlgorithmFamilyDetector(family AlgorithmFamily, detector AlgorithmDetectorKind) bool {
	switch family {
	case AlgorithmFamilyThreshold, AlgorithmFamilyPingUnreachable:
		return detector == AlgorithmDetectorKindThreshold
	case AlgorithmFamilySimpleRingRatio:
		return detector == AlgorithmDetectorKindSimpleRingRatio
	case AlgorithmFamilyOsRestart:
		return detector == AlgorithmDetectorKindOsRestart
	case AlgorithmFamilyProcPort:
		return detector == AlgorithmDetectorKindProcPort
	case AlgorithmFamilySimpleYearRound:
		return detector == AlgorithmDetectorKindSimpleYearRound
	case AlgorithmFamilyAdvancedRingRatio:
		return detector == AlgorithmDetectorKindAdvancedRingRatio
	case AlgorithmFamilyAdvancedYearRound:
		return detector == AlgorithmDetectorKindAdvancedYearRound
	case AlgorithmFamilyRingRatioAmplitude:
		return detector == AlgorithmDetectorKindRingRatioAmplitude
	case AlgorithmFamilyYearRoundAmplitude:
		return detector == AlgorithmDetectorKindYearRoundAmplitude
	case AlgorithmFamilyYearRoundRange:
		return detector == AlgorithmDetectorKindYearRoundRange
	default:
		return false
	}
}

func validAlgorithmEvaluationResult(result AlgorithmEvaluationResult) bool {
	switch result {
	case AlgorithmEvaluationResultNormal, AlgorithmEvaluationResultAbnormal, AlgorithmEvaluationResultRecovery,
		AlgorithmEvaluationResultUnavailable, AlgorithmEvaluationResultTerminal:
		return true
	default:
		return false
	}
}

func validAlgorithmInputName(name AlgorithmInputName) bool {
	return name == AlgorithmInputNamePrimary || name == AlgorithmInputNameHistory
}

func validAlgorithmDependencyPoint(point AlgorithmDependencyPoint) bool {
	switch point {
	case AlgorithmDependencyPointCurrent, AlgorithmDependencyPointPrevious,
		AlgorithmDependencyPointTenMinute, AlgorithmDependencyPointTwentyFiveMinute, AlgorithmDependencyPointHistorical:
		return true
	default:
		return false
	}
}

func validAlgorithmInputResult(result AlgorithmInputResult) bool {
	switch result {
	case AlgorithmInputResultAvailable, AlgorithmInputResultMissing,
		AlgorithmInputResultPartial, AlgorithmInputResultUnavailable:
		return true
	default:
		return false
	}
}

func algorithmEvaluationObservationResult(result AlgorithmEvaluationResult) Result {
	if result == AlgorithmEvaluationResultUnavailable || result == AlgorithmEvaluationResultTerminal {
		return ResultDegraded
	}
	return ResultSuccess
}

func algorithmInputObservationResult(result AlgorithmInputResult) Result {
	if result == AlgorithmInputResultPartial || result == AlgorithmInputResultUnavailable {
		return ResultDegraded
	}
	return ResultSuccess
}

func normalizeDrainingQGFacts(facts *DrainingQGFacts) *DrainingQGFacts {
	if facts == nil {
		return nil
	}
	normalized := *facts
	for _, count := range []*int{&normalized.Total, &normalized.Undrained, &normalized.Isolated, &normalized.Retired} {
		if *count < 0 {
			*count = 0
		}
	}
	normalized.Samples = append([]DrainingQGSample(nil), facts.Samples...)
	if len(normalized.Samples) > MaxDrainingQGLogSamples {
		normalized.Samples = normalized.Samples[:MaxDrainingQGLogSamples]
		normalized.Truncated = true
	}
	for index := range normalized.Samples {
		sample := &normalized.Samples[index]
		if sample.RetiredBoundary < 0 {
			sample.RetiredBoundary = 0
		}
		if sample.NextSlot < 0 {
			sample.NextSlot = 0
		}
		if sample.ProgressStatus != "FOUND" && sample.ProgressStatus != "MISSING" {
			sample.ProgressStatus = "UNKNOWN"
		}
		if sample.Disposition != DrainingQGSampleRetired {
			sample.Disposition = ""
		}
		if sample.InFlight != DrainingInFlightSlot && sample.InFlight != DrainingInFlightRange {
			sample.InFlight = ""
		}
	}
	return &normalized
}

// SchedulePruneSkipReasons is the closed vocabulary of ScheduleCutoverFacts.PrunesSkipped.
var SchedulePruneSkipReasons = []string{"progress_unavailable", "progress_missing"}

// The outcomes a state preflight's second pass splits into, as the closed set
// the counter pre-creates. Only OldRepresentation ends; NoRecordYet never
// does; the three corrupt ones should be zero and are read to confirm it.
//
// Pre-created because this family is read for its zeros, and an outcome nobody
// pre-created is absent -- which reads the same as zero and means the opposite.
const (
	EnvelopePassOldRepresentation = "old_representation"
	EnvelopePassNoRecordYet       = "no_record_yet"
	EnvelopePassEnvelopeCorrupt   = "envelope_corrupt"
	EnvelopePassFrameCorruptSaved = "frame_corrupt_rescued"
	EnvelopePassFrameCorruptLost  = "frame_corrupt_lost"
	// EnvelopePassUnclassified is a shape the split does not know: unreachable
	// today, carried so that a sixth one is counted rather than dropped.
	EnvelopePassUnclassified = "unclassified"
)

var EnvelopePassOutcomes = []string{EnvelopePassOldRepresentation, EnvelopePassNoRecordYet,
	EnvelopePassEnvelopeCorrupt, EnvelopePassFrameCorruptSaved, EnvelopePassFrameCorruptLost, EnvelopePassUnclassified}

func normalizeScheduleCutoverFacts(facts *ScheduleCutoverFacts) *ScheduleCutoverFacts {
	if facts == nil {
		return nil
	}
	normalized := *facts
	if normalized.Result != "success" && normalized.Result != "failure" {
		normalized.Result = "failure"
	}
	for _, count := range []*int{&normalized.Timelines, &normalized.PayloadBytes, &normalized.MaxTimelineBytes, &normalized.SegmentsPruned} {
		if *count < 0 {
			*count = 0
		}
	}
	sizes := make([]int, 0, len(normalized.TimelineBytes))
	for _, size := range normalized.TimelineBytes {
		if size >= 0 {
			sizes = append(sizes, size)
		}
	}
	normalized.TimelineBytes = sizes
	skipped := make(map[string]int, len(SchedulePruneSkipReasons))
	for _, reason := range SchedulePruneSkipReasons {
		if count := normalized.PrunesSkipped[reason]; count > 0 {
			skipped[reason] = count
		}
	}
	normalized.PrunesSkipped = skipped
	if normalized.Duration < 0 {
		normalized.Duration = 0
	}
	return &normalized
}

func normalizeObjectReadFacts(facts *ObjectReadFacts) *ObjectReadFacts {
	if facts == nil {
		return nil
	}
	normalized := ObjectReadFacts{Kind: "other", Result: "other"}
	for _, kind := range ObjectReadKinds {
		if facts.Kind == kind {
			normalized.Kind = kind
		}
	}
	for _, result := range ObjectReadResults {
		if facts.Result == result {
			normalized.Result = result
		}
	}
	return &normalized
}

func normalizeActivationHoldFacts(facts *ActivationHoldFacts) *ActivationHoldFacts {
	if facts == nil {
		return nil
	}
	normalized := *facts
	if normalized.Reappeared < 0 {
		normalized.Reappeared = 0
	}
	if normalized.Held < 0 {
		normalized.Held = 0
	}
	if normalized.Held > normalized.Reappeared {
		normalized.Reappeared = normalized.Held
	}
	if normalized.MaxAgeSeconds < 0 {
		normalized.MaxAgeSeconds = 0
	}
	if len(normalized.Samples) > MaxActivationHoldSamples {
		normalized.Samples = append([]string(nil), normalized.Samples[:MaxActivationHoldSamples]...)
		normalized.Truncated = true
	}
	return &normalized
}

func normalizeStateGenerationSkewFacts(facts *StateGenerationSkewFacts) *StateGenerationSkewFacts {
	if facts == nil {
		return nil
	}
	normalized := StateGenerationSkewFacts{Kind: "other", StrategyID: facts.StrategyID}
	for _, kind := range StateGenerationSkewKinds {
		if facts.Kind == kind {
			normalized.Kind = kind
		}
	}
	return &normalized
}

func normalizeObjectCatalogFacts(facts *ObjectCatalogFacts) *ObjectCatalogFacts {
	if facts == nil {
		return nil
	}
	normalized := *facts
	switch normalized.Operation {
	case "write", "renew":
	default:
		normalized.Operation = ""
	}
	if normalized.Result != "success" && normalized.Result != "failure" {
		normalized.Result = "failure"
	}
	for _, count := range []*int{&normalized.QueryGroups, &normalized.Written, &normalized.Present, &normalized.Missing, &normalized.ManifestBytes, &normalized.ObjectBytes} {
		if *count < 0 {
			*count = 0
		}
	}
	if normalized.Duration < 0 {
		normalized.Duration = 0
	}
	return &normalized
}

func normalizeActiveQGSetFacts(facts *ActiveQGSetFacts) *ActiveQGSetFacts {
	if facts == nil {
		return nil
	}
	normalized := *facts
	switch normalized.Operation {
	case "encode", "read", "write", "renew":
	default:
		normalized.Operation = ""
	}
	if normalized.Result != "success" && normalized.Result != "failure" {
		normalized.Result = "failure"
	}
	if normalized.QueryGroups < 0 {
		normalized.QueryGroups = 0
	}
	if normalized.ObjectBytes < 0 {
		normalized.ObjectBytes = 0
	}
	if normalized.Duration < 0 {
		normalized.Duration = 0
	}
	return &normalized
}

func normalizeLegacyQGMigrationFacts(facts *LegacyQGMigrationFacts) *LegacyQGMigrationFacts {
	if facts == nil {
		return nil
	}
	normalized := *facts
	switch normalized.Result {
	case "success", "fail_closed", "canceled":
	default:
		normalized.Result = "fail_closed"
	}
	switch normalized.ReasonClass {
	case "none", "config", "unsupported", "ownership", "dependency", "data_quality", "capacity", "state_progress", "output", "validation", "contract":
	default:
		normalized.ReasonClass = "contract"
	}
	if normalized.ScanKeys < 0 {
		normalized.ScanKeys = 0
	}
	if normalized.Duration < 0 {
		normalized.Duration = 0
	}
	return &normalized
}

// normalizeSourceWithheldFacts drops a record that does not say what happened.
// A reason with no disposition is half a sentence, and the half it is missing
// is the one that says whether the object is running.
func normalizeSourceWithheldFacts(facts *SourceWithheldFacts) *SourceWithheldFacts {
	if facts == nil || facts.Disposition == "" {
		return nil
	}
	normalized := *facts
	if normalized.Dropped < 0 {
		normalized.Dropped = 0
	}
	return &normalized
}

// normalizeSegmentContentFacts drops facts that name no state. A state this
// build does not know is kept rather than blanked: the label is bounded by the
// list the control plane publishes, and a state added at its site and not in
// that list must show as a new label rather than join another one.
func normalizeSegmentContentFacts(facts *SegmentContentFacts) *SegmentContentFacts {
	if facts == nil || facts.State == "" {
		return nil
	}
	normalized := *facts
	return &normalized
}

// normalizeNoDataCensusFacts clamps a negative count. A census of none is kept:
// zero Plans that detect no-data is the reading this exists to make visible,
// and dropping it would make the Slot that has none look like the Slot that
// never counted.
func normalizeNoDataCensusFacts(facts *NoDataCensusFacts) *NoDataCensusFacts {
	if facts == nil {
		return nil
	}
	normalized := *facts
	if normalized.Plans < 0 {
		normalized.Plans = 0
	}
	// A hop this build does not name is kept rather than blanked, the same way
	// an unknown outcome is: the label is bounded by the list above, and a hop
	// added at its site and not in the list must show as a new label rather
	// than silently join another one.
	return &normalized
}

// normalizeNoDataSlotFacts drops facts that name no outcome and clamps a
// negative count. An outcome this build does not know is kept rather than
// blanked: the label is bounded by the list the evaluation publishes, and a
// name that got here without being on it is worth seeing.
func normalizeNoDataSlotFacts(facts *NoDataSlotFacts) *NoDataSlotFacts {
	if facts == nil || facts.Outcome == "" {
		return nil
	}
	normalized := *facts
	if normalized.Plans < 0 {
		normalized.Plans = 0
	}
	return &normalized
}

// normalizeNoDataAbsenceFacts drops facts that name no outcome and clamps a
// negative horizon to none. The counts are unsigned and are kept as counted:
// an outcome this build does not know is kept for the same reason the Slot
// facts keep one.
func normalizeNoDataAbsenceFacts(facts *NoDataAbsenceFacts) *NoDataAbsenceFacts {
	if facts == nil || facts.Outcome == "" {
		return nil
	}
	normalized := *facts
	if normalized.HorizonSeconds < 0 {
		normalized.HorizonSeconds = 0
	}
	return &normalized
}

func normalizeQueryPermitFacts(facts *QueryPermitFacts) *QueryPermitFacts {
	if facts == nil {
		return nil
	}
	normalized := *facts
	if normalized.QueueKind != QueryQueueNormal && normalized.QueueKind != QueryQueueRecovery {
		normalized.QueueKind = ""
	}
	values := []*int{
		&normalized.NormalWaiting, &normalized.RecoveryWaiting, &normalized.NormalInflight,
		&normalized.RetryInflight, &normalized.ReplayInflight, &normalized.ProbeInflight,
		&normalized.RecoveryInflight,
	}
	for _, value := range values {
		if *value < 0 {
			*value = 0
		}
	}
	return &normalized
}

func isSourceReasonClass(reason ReasonCode) bool {
	switch reason {
	case ReasonNone, ReasonInternalUnknown, ReasonContractDeterministic,
		ReasonContractRetryable, ReasonContractCoverage, ReasonOther:
		return true
	default:
		return false
	}
}

func NormalizeSourceKind(sourceKind SourceKind) SourceKind {
	switch sourceKind {
	case SourceKindLegacyStrategy, SourceKindCompiledSnapshot:
		return sourceKind
	default:
		return ""
	}
}

func AllSourceKinds() []SourceKind {
	return []SourceKind{SourceKindLegacyStrategy, SourceKindCompiledSnapshot}
}

// CapacityBudgets are the budgets a rejection can be labelled with, and so the
// budgets whose ceilings have to be published: a rejection counted under a
// budget whose ceiling nobody reports cannot be read against anything.
//
// This is the one list. The label value, the ceiling the metric publishes and
// the ceiling the page publishes were each written out by hand in a different
// package, joined only by three string literals agreeing -- the same shape as a
// cohort set declared three times, where one member can go missing from all
// three at once and nothing notices. CapacityBudgetOther is excluded: it is
// where an unrecognised budget lands, not a budget with a ceiling of its own.
func CapacityBudgets() []CapacityBudget {
	return []CapacityBudget{
		CapacityBudgetSeries, CapacityBudgetRetainedBytes, CapacityBudgetStateMutations,
		CapacityBudgetEvents, CapacityBudgetGapMutations,
	}
}

func NormalizeCapacityBudget(budget CapacityBudget) CapacityBudget {
	switch budget {
	case "":
		return ""
	case CapacityBudgetSeries, CapacityBudgetRetainedBytes, CapacityBudgetStateMutations,
		CapacityBudgetEvents, CapacityBudgetGapMutations:
		return budget
	default:
		return CapacityBudgetOther
	}
}

func NormalizeDirection(direction Direction) Direction {
	if _, ok := directionSet[direction]; ok {
		return direction
	}
	return DirectionOther
}

func NormalizeComponentStage(component Component, stage Stage) (Component, Stage) {
	if _, ok := componentStageSet[ComponentStage{Component: component, Stage: stage}]; ok {
		return component, stage
	}
	return ComponentOther, StageOther
}

func NormalizeResult(result Result) Result {
	if _, ok := resultSet[result]; ok {
		return result
	}
	return ResultOther
}

func NormalizeOperation(operation Operation) Operation {
	if operation == "" {
		return OperationNone
	}
	if _, ok := operationSet[operation]; ok {
		return operation
	}
	return OperationOther
}

// NormalizeReason accepts only M0's Observation-domain catalog and M8's fixed
// resource/lifecycle catalog. Unknown values collapse to ReasonOther.
func NormalizeReason(reason ReasonCode, result Result) ReasonCode {
	if reason == "" || reason == ReasonNone {
		if resultAllowsNone(result) {
			return ReasonNone
		}
		return ReasonNotReported
	}
	if _, ok := commonReasonSet[reason]; ok {
		return reason
	}
	if _, ok := resourceReasonSet[reason]; ok {
		return reason
	}
	if _, ok := contractObservationReasonSet[string(reason)]; ok {
		return reason
	}
	if _, ok := activationFailureReasonSet[reason]; ok {
		return reason
	}
	if _, ok := viewStreamReasonSet[reason]; ok {
		return reason
	}
	if _, ok := schedulerDecisionReasonSet[reason]; ok {
		return reason
	}
	if _, ok := effectiveMaintenanceReasonSet[reason]; ok {
		return reason
	}
	if _, ok := absentCloseReasonSet[reason]; ok {
		return reason
	}
	return ReasonOther
}

func resultAllowsNone(result Result) bool {
	return result == ResultStarted || result == ResultSuccess || result == ResultResumed
}

// NormalizeMetricReason keeps M8 reasons exact and maps M0's fixed Observation
// catalog to its three contract classes. Exact M0 codes remain available in
// structured logs without expanding Prometheus label cardinality.
func NormalizeMetricReason(component Component, reason ReasonCode, result Result) ReasonCode {
	reason = NormalizeReason(reason, result)
	if _, ok := commonReasonSet[reason]; ok {
		return reason
	}
	if metricReason, ok := contractObservationMetricReasonByCode[string(reason)]; ok {
		return metricReason
	}
	if component == ComponentResource {
		if _, ok := resourceReasonSet[reason]; ok {
			return reason
		}
	}
	return ReasonOther
}

func AllComponentStages() []ComponentStage {
	return append([]ComponentStage(nil), allComponentStages...)
}

// AllMetricComponentStages returns only the phase-one generic metric catalog.
// Phase-two workflow stages remain available to structured observers, but 07
// requires dedicated bounded metrics instead of a generic stage cross product.
func AllMetricComponentStages() []ComponentStage {
	return append([]ComponentStage(nil), metricComponentStages...)
}

func IsGenericMetricComponentStage(component Component, stage Stage) bool {
	_, ok := metricComponentStageSet[ComponentStage{Component: component, Stage: stage}]
	return ok
}

func AllStages() []Stage {
	stages := make([]Stage, 0, len(allComponentStages))
	for _, pair := range allComponentStages {
		stages = append(stages, pair.Stage)
	}
	return stages
}

func AllMetricStages() []Stage {
	stages := make([]Stage, 0, len(metricComponentStages))
	for _, pair := range metricComponentStages {
		stages = append(stages, pair.Stage)
	}
	return stages
}

func AllResults() []Result {
	return append([]Result(nil), allResults...)
}

func AllReasons(component Component) []ReasonCode {
	if component == ComponentResource {
		return append([]ReasonCode(nil), allResourceReasons...)
	}
	return append([]ReasonCode(nil), allCommonReasons...)
}

func AllMetricReasons() []ReasonCode {
	return append([]ReasonCode(nil), allResourceReasons...)
}

func AllLogReasons() []ReasonCode {
	reasons := make([]ReasonCode, 0, len(contractObservationReasons)+len(allLogReasons))
	reasons = append(reasons, contractObservationReasons...)
	reasons = append(reasons, allLogReasons...)
	return reasons
}

func AllOperations() []Operation {
	return append([]Operation(nil), allOperations...)
}

func AllMetricOperations() []Operation {
	return append([]Operation(nil), metricOperations...)
}

func NormalizeMetricOperation(operation Operation) Operation {
	if operation == "" {
		return OperationNone
	}
	if _, ok := metricOperationSet[operation]; ok {
		return operation
	}
	return OperationOther
}

func AllDirections() []Direction {
	return append([]Direction(nil), allDirections...)
}

func normalizeCounts(counts Counts) Counts {
	values := []*int64{
		&counts.Messages, &counts.Records, &counts.Plans, &counts.Levels,
		&counts.Events, &counts.Bytes, &counts.Keys, &counts.StateBytes,
	}
	for _, value := range values {
		if *value < 0 {
			*value = 0
		}
	}
	return counts
}

var metricComponentStages = []ComponentStage{
	{ComponentRuntime, StageStartup}, {ComponentRuntime, StageConfigLoaded},
	{ComponentRuntime, StageEffectiveTimeMaintenance},
	{ComponentRuntime, StageShutdown}, {ComponentRuntime, StageFatal},
	{ComponentRuntime, StageRestartRecovered},
	// Registered on the generic catalog rather than the phase-two one because
	// this has to be answerable from metrics alone: when the snapshot channel
	// itself is broken, the aggregated view can only report that a replica is
	// missing, never why, and the log channel needs collection configured per
	// environment before it can answer anything.
	{ComponentRuntime, StageFleetSnapshotPublish},
	// Same reason: an operator who opened a window and sees no output needs to
	// learn from metrics whether it was applied, and log collection is
	// configured per environment while metrics are always there.
	{ComponentRuntime, StageObservationWindow},
	{ComponentConsumer, StageKafkaAssigned}, {ComponentConsumer, StageExecutionReceived},
	{ComponentConsumer, StageOffsetGap}, {ComponentConsumer, StageOffsetMarked},
	{ComponentAdapter, StageMessageDecoded}, {ComponentAdapter, StageRecordBatchReady},
	{ComponentAdapter, StageRejected},
	{ComponentCompiler, StagePlanCompiled},
	{ComponentState, StageDependencyLoaded}, {ComponentState, StageStateCommitted},
	{ComponentDetect, StageDetectCompleted},
	{ComponentTrigger, StageTriggerCompleted},
	{ComponentOutput, StageOutputACKed},
	{ComponentCoverage, StageCoverageCompleted}, {ComponentCoverage, StageCoverageGap},
	{ComponentResource, StageResourceSoft}, {ComponentResource, StageResourceHard},
	{ComponentResource, StageResourceResumed},
	{ComponentPythonProducer, StagePythonSource}, {ComponentPythonProducer, StagePythonBuilt},
	{ComponentPythonProducer, StagePythonEnqueued}, {ComponentPythonProducer, StagePythonPublished},
	{ComponentPythonProducer, StagePythonACKed}, {ComponentPythonProducer, StagePythonDropped},
	{ComponentOther, StageOther},
}

var phaseTwoComponentStages = []ComponentStage{
	{ComponentRuntime, StageLegacyPodCache},
	// The absent-strategy difference reports through its own metric family,
	// which has a cell per outcome and per denominator. Registered here
	// rather than on the generic catalog so that it does not also buy a
	// slice of the duration histogram's cross product, which would be
	// ninety more series for a loop that runs twelve times an hour on one
	// replica and is fully answered by its own family.
	{ComponentControlPlane, StageAbsentStrategyClose},
	{ComponentControlPlane, StageSnapshotRefreshed}, {ComponentControlPlane, StageSnapshotUnavailable},
	{ComponentControlPlane, StageActivationFailed},
	// The catalog's three stages were emitted from the day the catalog
	// existed and listed nowhere, so NormalizeComponentStage folded every one
	// of them to (_other, _other) and the generic counter admitted them as
	// such: object_read fires once per catalog object read, hit included, and
	// on a live replica that was 300 lines a second into the one cell of
	// observation_total that exists to say "an emitter nobody classified is
	// running" -- with 822,000 in it, a real unclassified emitter would not
	// have moved the needle. Listed here they keep their names and stay out
	// of the generic family, as every other phase-two stage does; their own
	// families (object_read_total, object_catalog_objects_total,
	// schedule_cutover_total) were counting them all along.
	{ComponentControlPlane, StageObjectCatalog}, {ComponentControlPlane, StageObjectRead},
	{ComponentControlPlane, StageScheduleCutover},
	{ComponentControlPlane, StageActiveQGSet}, {ComponentControlPlane, StageLegacyQGMigration},
	{ComponentControlPlane, StageDrainingQGReconciled},
	{ComponentControlPlane, StageFrozenPlanGeneration}, {ComponentControlPlane, StageActivationHold},
	{ComponentControlPlane, StageStateCarryDecided}, {ComponentState, StageStateCarried},
	{ComponentOwnership, StageAssignmentAcquired}, {ComponentOwnership, StageAssignmentLost},
	{ComponentOwnership, StageRebalancePlanned},
	{ComponentOwnership, StageControlReadsSpent},
	{ComponentOwnership, StageAssignmentIndexWritten}, {ComponentOwnership, StageAssignmentIndexRead},
	{ComponentOwnership, StageAssignmentSwept},
	{ComponentOwnership, StageViewPublished}, {ComponentOwnership, StageViewSession}, {ComponentOwnership, StageViewInstalled},
	{ComponentOwnership, StageTakeoverStarted}, {ComponentOwnership, StageTakeoverCompleted},
	{ComponentOwnership, StageLeaseRenewed}, {ComponentOwnership, StageFenceChecked},
	{ComponentScheduler, StageScheduleDue}, {ComponentScheduler, StageSlotStarted},
	{ComponentScheduler, StageSlotCompleted}, {ComponentScheduler, StageQueryAdmission},
	{ComponentScheduler, StageQueryCooldown}, {ComponentScheduler, StageRunnerReturned}, {ComponentScheduler, StageDispatcherSnapshot}, {ComponentScheduler, StageQueryPermitWait},
	{ComponentScheduler, StageExpiredRangeReturned},
	{ComponentScheduler, StageDispatchTurnaway},
	{ComponentScheduler, StageRunnerCompleted}, {ComponentScheduler, StageSlotSourceCompleted},
	{ComponentScheduler, StageScheduleCursorAdvanced}, {ComponentScheduler, StageReplayExpired},
	{ComponentScheduler, StageRangeDistanceExpired},
	{ComponentScheduler, StageRangeGateDecided},
	{ComponentScheduler, StageSlotWait},
	{ComponentAccess, StageQueryCompleted},
	{ComponentAccess, StageQueryBudgetResolved},
	{ComponentAccess, StageSlotReadinessArrival},
	{ComponentEvaluation, StageEvaluationCompleted},
	{ComponentState, StageStatePreflight}, {ComponentState, StageGapLoaded},
	{ComponentState, StageGapGuardProgress},
	{ComponentEvaluation, StageNoDataDecided},
	{ComponentAccess, StageTargetResolved},
	{ComponentProgress, StageExecutionEvidenceWritten},
	{ComponentState, StageNoDataMemoryRead},
	{ComponentState, StageDimensionCensus},
	{ComponentControlPlane, StageSplitPlanned},
	{ComponentState, StageNoDataMemoryRenewed},
	{ComponentState, StageFrozenStateRenewed},
	{ComponentState, StageNoDataMemoryRefused},
	{ComponentState, StageNoDataMemoryWritten},
	{ComponentControlPlane, StageSourceWithheld},
	{ComponentControlPlane, StageNoDataSuspended},
	{ComponentState, StageSideEffectAdmission}, {ComponentState, StageGapGuardCommitted},
	{ComponentState, StageMutationCompared}, {ComponentState, StageStateAdmission},
	{ComponentState, StageStateApplied},
	{ComponentOutput, StageEventACKed},
	{ComponentProgress, StageProgressCommitted},
}

var allComponentStages = append(
	append([]ComponentStage(nil), metricComponentStages...),
	phaseTwoComponentStages...,
)

var allResults = []Result{
	Result(ResultStarted), Result(ResultSuccess), ResultTerminal, ResultRetrying, ResultPaused,
	ResultResumed, ResultDegraded, Result(ResultTimeout), Result(ResultFailed), ResultOther,
}

var metricOperations = []Operation{
	OperationNone, OperationCompile, OperationCacheHit, OperationCacheMiss, OperationCacheEvict,
	OperationRequirementCompile, OperationLoad, OperationDecode, OperationEncode, OperationWrite,
	OperationConsume, OperationProduce, OperationACK, OperationCommit, OperationOffsetRepair,
	OperationSample, OperationTransition, OperationOther,
}

var phaseTwoOperations = []Operation{OperationNormal, OperationRetry, OperationReplay, OperationProbe}
var allOperations = append(append([]Operation(nil), metricOperations...), phaseTwoOperations...)

var allDirections = []Direction{DirectionInput, DirectionOutput, DirectionInternal, DirectionOther}

// unclassifiedReasons are the reasons that carry no classification of their
// own: the absence of one, a site that says it does not know, and a site that
// did not say. They are listed once and shared, so adding one cannot silently
// change what counts as a resource reason.
var unclassifiedReasons = []ReasonCode{ReasonNone, ReasonInternalUnknown, ReasonNotReported}

// The effective-time maintenance's outcomes: the closed list of what one
// maintenance step does to a Query Group, an alert or a close batch. They
// are the reason of its log line and the outcome label of
// effective_close_total, which pre-creates every cell; before the counter
// existed the whole close chain normalized to reason_code=other on the
// metrics side and had no cell of its own.
const (
	// EffectiveCloseAcked counts alerts closed, one per alert the broker
	// acknowledged.
	EffectiveCloseAcked ReasonCode = "close_acked"
	// EffectiveClosePrecheckFailed is the owner and content check before a
	// send refusing: the fence, the content scope or the timeline moved.
	EffectiveClosePrecheckFailed ReasonCode = "close_precheck_failed"
	// EffectiveCloseSendFailed is the producer not acknowledging a batch.
	EffectiveCloseSendFailed ReasonCode = "close_send_failed"
	// EffectiveCloseMaintenanceBusy is a close that could not take the Query
	// Group's flight because a Slot was executing. Once is nothing; on every
	// tick it is a Query Group whose close never happens, which used to be
	// the same silence as a Query Group with nothing to close.
	EffectiveCloseMaintenanceBusy ReasonCode = "maintenance_busy"
	// EffectiveClosePlanUncompilable counts activated Plans the maintenance
	// read could not compile and so cannot judge.
	EffectiveClosePlanUncompilable ReasonCode = "maintenance_plan_uncompilable"
	EffectiveCloseIdentityInvalid  ReasonCode = "close_identity_invalid"
	// EffectiveCloseEffectiveTimeUnknown is a Plan whose effective time could
	// not be resolved at all.
	EffectiveCloseEffectiveTimeUnknown ReasonCode = "effective_time_unknown"
	EffectiveCloseLegacyUnavailable    ReasonCode = "legacy_effective_time_unavailable"
	// EffectiveCloseUnavailable is the Query Group's Plans not readable, or
	// its owner not accepting.
	EffectiveCloseUnavailable       ReasonCode = "unavailable"
	EffectiveCloseUnsupportedRunner ReasonCode = "unsupported_runner"
	// EffectiveCloseViewNotExecutable is a Query Group whose Plans the loop
	// could not read because the executable view does not allow it yet:
	// the view not installed, or the lease behind it. A rollout's shape -
	// every owned Query Group, once or twice, in the seconds after a
	// replica starts - and not a store failure, which is what "unavailable"
	// read as when the two were one word.
	EffectiveCloseViewNotExecutable ReasonCode = "view_not_executable"
)

// EffectiveCloseOutcomes is every outcome, for the metric to pre-create each
// cell and for a reader to bound the family by.
var EffectiveCloseOutcomes = []ReasonCode{
	EffectiveCloseAcked, EffectiveClosePrecheckFailed, EffectiveCloseSendFailed,
	EffectiveCloseMaintenanceBusy, EffectiveClosePlanUncompilable, EffectiveCloseIdentityInvalid,
	EffectiveCloseEffectiveTimeUnknown, EffectiveCloseLegacyUnavailable, EffectiveCloseUnavailable, EffectiveCloseUnsupportedRunner,
	EffectiveCloseViewNotExecutable,
}

// Maintenance details are bounded log reasons, not new metric label dimensions.
var effectiveMaintenanceReasons = EffectiveCloseOutcomes

// AbsentCloseReasons is the absent-strategy difference's vocabulary as
// observability reason codes. The words are the difference's own - one
// vocabulary, declared where the decision is made - so that a log line, the
// metric cell and the code branch cannot drift into three spellings.
var AbsentCloseReasons = absentCloseReasons()

func absentCloseReasons() []ReasonCode {
	reasons := make([]ReasonCode, 0, len(absentalerts.Outcomes)+len(absentalerts.Refusals))
	for _, outcome := range absentalerts.Outcomes {
		reasons = append(reasons, ReasonCode(outcome))
	}
	for _, refusal := range absentalerts.Refusals {
		if refusal == absentalerts.RefusalNone {
			continue
		}
		reasons = append(reasons, ReasonCode(refusal))
	}
	return reasons
}

var contractClassReasons = []ReasonCode{
	ReasonContractDeterministic, ReasonContractRetryable, ReasonContractCoverage,
}
var resourceOnlyReasons = []ReasonCode{
	ReasonCPU, ReasonRSS, ReasonHeap, ReasonGC,
	ReasonWorkerQueue, ReasonInflight, ReasonConsumerLag, ReasonStateBytes,
}

func joinReasons(groups ...[]ReasonCode) []ReasonCode {
	joined := []ReasonCode(nil)
	for _, group := range groups {
		joined = append(joined, group...)
	}
	return joined
}

// ViewStreamReasons is the closed list of words a view_session line may carry
// as its reason: why a Worker found no Leader, why a stream ended, why an
// install was refused, why the Leader refused a Hello. They are the view
// stream's own constants, repeated here because this package is the
// vocabulary's owner and cannot import the stream; a test on the stream side
// holds its constants to this list. Before this the line's reason_code read
// reason_not_reported on every discovery miss and the one word that said
// what happened -- NO_LEADER -- was two levels down in the facts, where a
// count by reason cannot reach it.
var ViewStreamReasons = []ReasonCode{
	// Discovery and the stream's end, on the Worker. The three ways a Leader
	// is not found are three words -- no lease, a lease whose holder has no
	// registration, a registration that advertises no endpoint -- because on
	// a live deployment they shared one, and the one word sent the reader to
	// the lease when the endpoint was what was missing.
	"NO_LEADER", "LEADER_UNREGISTERED", "LEADER_NO_ENDPOINT", "DISCOVERY_FAILED", "LEADER_SILENT", "STREAM_CLOSED", "RECV_FAILED",
	// A process that cannot advertise an endpoint of its own, said once at
	// startup rather than by every other Worker every discovery.
	"LISTENER_UNPARSABLE", "NO_ROUTE",
	// An install the Worker refused.
	"DELTA_BASE_MISMATCH", "DELTA_DIGEST_MISMATCH", "SNAPSHOT_INVALID", "SNAPSHOT_INCOMPLETE", "VIEW_FOR_ANOTHER_WORKER",
	"OBJECTS_NOT_PROBED",
	// A Hello the Leader refused, or why it closed the stream.
	"NOT_LEADER", "UNKNOWN_WORKER", "BAD_TOKEN", "PROTOCOL_VERSION", "REGISTRY_UNAVAILABLE", "HELLO_EXPECTED",
	"REPLACED_BY_NEW_STREAM", "IDLE", "SHUTDOWN",
}

// ViewStreamReasonCode is the reason as the line's reason_code: the word when
// it is one of ViewStreamReasons, empty otherwise. The stream's emitters put
// an endpoint or a free-text detail in the same slot on other events, and
// those stay in the facts, where they are not a bounded code.
func ViewStreamReasonCode(reason string) ReasonCode {
	if _, ok := viewStreamReasonSet[ReasonCode(reason)]; ok {
		return ReasonCode(reason)
	}
	return ""
}

// SchedulerDecisionReasons is the closed list of words the scheduler's own
// decisions carry as their reason_code: why a round gave up on a Slot at the
// range gate (RangeGateOutcomes) and why a replay expired
// (ReplayExpiryReasons). Both words already travelled on their lines, in the
// facts (range_gate, replay_expiry_reason), with the reason field left empty
// -- which a degraded result normalizes to reason_not_reported, the word for
// a site that failed to report, on lines whose site had reported one level
// down, where a count by reason cannot reach it. The lists are the same ones
// the metric partitions pre-create, so a word here is a word a series counts.
var SchedulerDecisionReasons = func() []ReasonCode {
	reasons := make([]ReasonCode, 0, len(RangeGateOutcomes)+len(ReplayExpiryReasons))
	for _, outcome := range RangeGateOutcomes {
		reasons = append(reasons, ReasonCode(outcome))
	}
	for _, reason := range ReplayExpiryReasons {
		reasons = append(reasons, ReasonCode(reason))
	}
	return reasons
}()

var allCommonReasons = joinReasons(unclassifiedReasons, contractClassReasons, []ReasonCode{ReasonOther, ReasonStateAlreadyAppliedBeforeEvaluation})
var allResourceReasons = joinReasons(
	unclassifiedReasons, resourceOnlyReasons, contractClassReasons, []ReasonCode{ReasonOther, ReasonStateAlreadyAppliedBeforeEvaluation})
var allLogReasons = joinReasons(unclassifiedReasons, resourceOnlyReasons, activationFailureReasons, ViewStreamReasons, SchedulerDecisionReasons, effectiveMaintenanceReasons, AbsentCloseReasons, []ReasonCode{ReasonOther, ReasonStateAlreadyAppliedBeforeEvaluation})

var componentStageSet = makeComponentStageSet(allComponentStages)
var metricComponentStageSet = makeComponentStageSet(metricComponentStages)
var resultSet = makeResultSet(allResults)
var operationSet = makeOperationSet(allOperations)
var metricOperationSet = makeOperationSet(metricOperations)
var directionSet = makeDirectionSet(allDirections)
var commonReasonSet = makeReasonSet(joinReasons(unclassifiedReasons, []ReasonCode{ReasonStateAlreadyAppliedBeforeEvaluation}))
var resourceReasonSet = makeReasonSet(resourceOnlyReasons)
var activationFailureReasonSet = makeReasonSet(activationFailureReasons)
var viewStreamReasonSet = makeReasonSet(ViewStreamReasons)
var schedulerDecisionReasonSet = makeReasonSet(SchedulerDecisionReasons)
var effectiveMaintenanceReasonSet = makeReasonSet(effectiveMaintenanceReasons)
var absentCloseReasonSet = makeReasonSet(AbsentCloseReasons)
var contractObservationReasons, contractObservationReasonSet, contractObservationMetricReasonByCode = loadContractObservationReasons()

func makeComponentStageSet(values []ComponentStage) map[ComponentStage]struct{} {
	result := make(map[ComponentStage]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func makeResultSet(values []Result) map[Result]struct{} {
	result := make(map[Result]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func makeOperationSet(values []Operation) map[Operation]struct{} {
	result := make(map[Operation]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func makeDirectionSet(values []Direction) map[Direction]struct{} {
	result := make(map[Direction]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func makeReasonSet(values []ReasonCode) map[ReasonCode]struct{} {
	result := make(map[ReasonCode]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func loadContractObservationReasons() ([]ReasonCode, map[string]struct{}, map[string]ReasonCode) {
	catalog := contract.ReasonCatalogV2()
	reasons := make([]ReasonCode, 0, len(catalog))
	known := make(map[string]struct{}, len(catalog))
	metricReasons := make(map[string]ReasonCode, len(catalog))
	for _, definition := range catalog {
		if !definition.Domains.Has(contract.ReasonDomainObservation) {
			continue
		}
		reasons = append(reasons, ReasonCode(definition.Code))
		known[definition.Code] = struct{}{}
		switch definition.Class {
		case contract.ReasonClassDeterministic:
			metricReasons[definition.Code] = ReasonContractDeterministic
		case contract.ReasonClassRetryable:
			metricReasons[definition.Code] = ReasonContractRetryable
		case contract.ReasonClassCoverage:
			metricReasons[definition.Code] = ReasonContractCoverage
		}
	}
	return reasons, known, metricReasons
}

func normalizeReasons(reasons []ReasonCode) []ReasonCode {
	if len(reasons) == 0 {
		return nil
	}
	normalized := make([]ReasonCode, 0, len(reasons))
	for _, reason := range reasons {
		if reason == ReasonNone {
			normalized = append(normalized, ReasonNone)
			continue
		}
		normalized = append(normalized, NormalizeReason(reason, ResultDegraded))
	}
	return sortedUniqueReasons(normalized)
}

func sortedUniqueReasons(reasons []ReasonCode) []ReasonCode {
	seen := make(map[ReasonCode]struct{}, len(reasons))
	for _, reason := range reasons {
		seen[reason] = struct{}{}
	}
	result := make([]ReasonCode, 0, len(seen))
	for reason := range seen {
		result = append(result, reason)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	if len(result) > 8 {
		result = result[:8]
	}
	return result
}

func NormalizeMetricReasons(component Component, reasons []ReasonCode) []ReasonCode {
	if len(reasons) == 0 {
		return nil
	}
	mapped := make([]ReasonCode, 0, len(reasons))
	for _, reason := range reasons {
		mapped = append(mapped, NormalizeMetricReason(component, reason, ResultDegraded))
	}
	return sortedUniqueReasons(mapped)
}

// NormalizeHealthMetricReasons maps health reasons to the fixed Prometheus
// catalog. Health is a cross-component snapshot, not the Resource component.
func NormalizeHealthMetricReasons(reasons []ReasonCode) []ReasonCode {
	if len(reasons) == 0 {
		return nil
	}
	mapped := make([]ReasonCode, 0, len(reasons))
	known := makeReasonSet(allResourceReasons)
	for _, reason := range reasons {
		reason = NormalizeReason(reason, ResultDegraded)
		if metricReason, ok := contractObservationMetricReasonByCode[string(reason)]; ok {
			reason = metricReason
		}
		if _, ok := known[reason]; !ok {
			reason = ReasonOther
		}
		mapped = append(mapped, reason)
	}
	return sortedUniqueReasons(mapped)
}

// ObserveSlotWait reports one blocking wait inside a Slot attempt. The wait
// name must be one of SlotWaits.
//
// It exists as a function rather than as a line at each site because the three
// waits are only useful read together: the question a reader arrives with is
// "which of them was this attempt in", and an answer that exists for one of
// them and not the others cannot be given.
// The operation is the attempt's own, so a wait reads like every other line of
// that attempt; a shared read that belongs to no single attempt passes none.
func ObserveSlotWait(
	ctx context.Context,
	observer Observer,
	wait string,
	operation Operation,
	started time.Time,
	now func() time.Time,
) {
	if observer == nil || started.IsZero() {
		return
	}
	if now == nil {
		now = time.Now
	}
	elapsed := now().Sub(started)
	if elapsed < 0 {
		return
	}
	result := Result(ResultSuccess)
	if elapsed >= SlowSlotWait {
		// Not a failure -- nothing went wrong and nothing will report one.
		// Degraded is what a Slot attempt that spent this long waiting is,
		// and it is what keeps the line out of the success population.
		result = Result(ResultDegraded)
	}
	// Observability is a fail-open side channel, as at every other boundary.
	defer func() { _ = recover() }()
	observer.Observe(ctx, Observation{
		Component: ComponentScheduler, Stage: StageSlotWait, Result: result, Operation: operation,
		Direction: DirectionInternal, Duration: elapsed, SlotWait: &SlotWaitFacts{Wait: wait},
	})
}
