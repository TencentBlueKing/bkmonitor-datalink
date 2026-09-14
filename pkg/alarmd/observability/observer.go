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
	"slices"
	"sort"
	"time"

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

	StageConfigLoaded           = "config_loaded"
	StageLegacyPodCache         = "legacy_pod_cache"
	StageSnapshotRefreshed      = "snapshot_refreshed"
	StageSnapshotUnavailable    = "snapshot_unavailable"
	StageActivationFailed       = "activation_failed"
	StageActiveQGSet            = "active_qg_set"
	StageObjectCatalog          = "object_catalog"
	StageObjectRead             = "object_read"
	StageFrozenPlanGeneration   = "frozen_plan_generation"
	StageActivationHold         = "activation_hold"
	StageScheduleCutover        = "schedule_cutover"
	StageLegacyQGMigration      = "legacy_active_qg_migration"
	StageDrainingQGReconciled   = "draining_query_groups"
	StageAssignmentAcquired     = "assignment_acquired"
	StageAssignmentLost         = "assignment_lost"
	StageRebalancePlanned       = "rebalance_planned"
	StageAssignmentIndexWritten = "assignment_index_written"
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
	StageEvaluationCompleted    = "evaluation_completed"
	StageSideEffectAdmission    = "side_effect_admission"
	StageStateAdmission         = "state_admission"
	StageGapGuardCommitted      = "gap_guard_committed"
	StageMutationCompared       = "mutation_compared"
	StageEventACKed             = "event_acked"
	StageStateApplied           = "state_applied"
	StageProgressCommitted      = "progress_committed"
	StageDependencyLoaded       = "dependency_loaded"
	StageStateCommitted         = "state_committed"
	StageDetectCompleted        = "detect_completed"
	StageTriggerCompleted       = "trigger_completed"
	StageOutputACKed            = "output_acked"
	StageCoverageCompleted      = "coverage_completed"
	StageCoverageGap            = "coverage_gap"
	StageReceiptQueued          = "receipt_queued"
	StageFinalEvidenceQueued    = "final_evidence_queued"
	StageFinalEvidenceACKed     = "final_evidence_acked"
	StageFinalEvidenceDropped   = "final_evidence_dropped"
	StageResourceSoft           = "resource_soft"
	StageResourceHard           = "resource_hard"
	StageResourceResumed        = "resource_resumed"
	StageComparisonCompleted    = "comparison_completed"
	StageComparisonAuditACKed   = "comparison_audit_acked"
	StagePythonSource           = "source"
	StagePythonBuilt            = "built"
	StagePythonEnqueued         = "enqueued"
	StagePythonPublished        = "published"
	StagePythonACKed            = "acked"
	StagePythonDropped          = "dropped"
	StageOther                  = "_other"

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

	ReasonNone ReasonCode = "none"
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
	Result           string
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
var ScheduleCutoverDecisions = []string{"kept", "revised", "cut", "legacy_cut", "retired", "added"}

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
	ObjectReadKinds   = []string{"query_group", "output_context", "segment"}
	ObjectReadResults = []string{"hit", "miss", "share", "missing", "invalid", "object", "legacy_segment", "segment_without_ref", "object_missing", "object_invalid", "object_mismatch"}
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
}

func normalizeRebalanceFacts(facts *RebalanceFacts) *RebalanceFacts {
	if facts == nil {
		return nil
	}
	normalized := *facts
	for _, count := range []*int{
		&normalized.ReadyWorkers, &normalized.Assigned, &normalized.Target, &normalized.MostOwned,
		&normalized.LeastOwned, &normalized.Batch, &normalized.PlannedMoves,
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

// RecoveryGateCause is why a record whose Levels agreed on RECOVERY did not
// send its envelope, or the one shape it was sent past. The set is closed: it
// is a metric label.
type RecoveryGateCause string

const (
	// RecoveryGateLevelUnavailable held the envelope: a Level's state could
	// not be established this round.
	RecoveryGateLevelUnavailable RecoveryGateCause = "level_unavailable"
	// RecoveryGateLevelRecovering held the envelope: a Level read NORMAL with
	// recovery enabled, so a window inside its recovery span still triggers.
	RecoveryGateLevelRecovering RecoveryGateCause = "level_recovering"
	// RecoveryGateLevelWithoutRecovery did not hold: the envelope was sent
	// past a NORMAL Level whose recovery is disabled and which therefore can
	// never say RECOVERY. Counted so the shape's existence can be read.
	RecoveryGateLevelWithoutRecovery RecoveryGateCause = "level_without_recovery"
)

// RecoveryGateFact counts, for one evaluation, the records the gate decided
// under one cause.
type RecoveryGateFact struct {
	Cause   RecoveryGateCause `json:"cause"`
	Records uint64            `json:"records"`
}

// OpenAlertGateOutcome is what the second recovery gate, the consumer's
// open alert set, did with a RECOVERY record every Level had agreed on. The
// set is closed: it is a metric label. A record is counted here or under a
// RecoveryGateCause, never both.
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

// OpenAlertGateFact counts, for one evaluation, the records the second gate
// decided under one outcome.
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
	QueryCooldown          *QueryCooldownFacts
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
	HistoryCoverage       *HistoryCoverageFacts
	Dispatcher            *DispatcherFacts
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
	SourceKind            SourceKind
	QueryPermit           *QueryPermitFacts
	RuntimeConfig         *RuntimeConfigFacts
	QueryFailure          *QueryFailureFacts
	QueryStatus           []QueryStatusFacts
	QueryTiming           *QueryTimingFacts
	SlotReadiness         *SlotReadinessFacts
	ShortPeriodCompletion *ShortPeriodCompletionFacts
	StateApplyChunk       *StateApplyChunkFacts
	StateWriteReuse       *StateWriteReuseFacts
	ActiveQGSet           *ActiveQGSetFacts
	ScheduleCutover       *ScheduleCutoverFacts
	ObjectCatalog         *ObjectCatalogFacts
	ObjectRead            *ObjectReadFacts
	StateGenerationSkew   *StateGenerationSkewFacts
	ActivationHold        *ActivationHoldFacts
	LegacyMigration       *LegacyQGMigrationFacts
	DrainingQG            *DrainingQGFacts
	Rebalance             *RebalanceFacts
	AssignmentIndex       *AssignmentIndexFacts
	CursorAdvance         *CursorAdvanceFacts
	SourceRefresh         *SourceRefreshFacts
	ActivationFailure     *ActivationFailureFacts
	AlgorithmEvaluations  []AlgorithmEvaluationFact
	AlgorithmInputs       []AlgorithmInputFact
	RecoveryGates         []RecoveryGateFact
	OpenAlertGates        []OpenAlertGateFact
	ControlSourceRound    *ControlSourceRoundFacts
	normalized            bool
	stageReasonBucket     bool
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
	observation.HistoryCoverage = normalizeHistoryCoverageFacts(observation.HistoryCoverage)
	observation.QueryPermit = normalizeQueryPermitFacts(observation.QueryPermit)
	observation.QueryTiming = normalizeTimingFacts(observation)
	observation.ShortPeriodCompletion = normalizeShortPeriodCompletion(observation)
	observation.StateApplyChunk = normalizeStateApplyChunk(observation)
	observation.QueryFailure = normalizeQueryFailure(observation.Component, observation.Stage, observation.QueryFailure)
	observation.QueryStatus = normalizeQueryStatus(observation.Component, observation.Stage, observation.QueryStatus)
	observation.SlotReadiness = normalizeSlotReadiness(observation.SlotReadiness)
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
	observation.ActivationHold = normalizeActivationHoldFacts(observation.ActivationHold)
	observation.LegacyMigration = normalizeLegacyQGMigrationFacts(observation.LegacyMigration)
	observation.DrainingQG = normalizeDrainingQGFacts(observation.DrainingQG)
	observation.Rebalance = normalizeRebalanceFacts(observation.Rebalance)
	observation.AssignmentIndex = normalizeAssignmentIndexFacts(observation.AssignmentIndex)
	observation.CursorAdvance = normalizeCursorAdvanceFacts(observation.CursorAdvance)
	observation.SourceRefresh = normalizeSourceRefreshFacts(observation.Component, observation.Stage, observation.SourceRefresh)
	observation.ActivationFailure = normalizeActivationFailureFacts(
		observation.Component, observation.Stage, observation.ActivationFailure,
	)
	observation.AlgorithmEvaluations, observation.AlgorithmInputs = normalizeAlgorithmFacts(observation)
	observation.RecoveryGates = normalizeRecoveryGateFacts(observation)
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
	{ComponentCoverage, StageReceiptQueued},
	{ComponentResource, StageResourceSoft}, {ComponentResource, StageResourceHard},
	{ComponentResource, StageResourceResumed},
	{ComponentComparator, StageComparisonCompleted}, {ComponentComparator, StageComparisonAuditACKed},
	{ComponentPythonProducer, StagePythonSource}, {ComponentPythonProducer, StagePythonBuilt},
	{ComponentPythonProducer, StagePythonEnqueued}, {ComponentPythonProducer, StagePythonPublished},
	{ComponentPythonProducer, StagePythonACKed}, {ComponentPythonProducer, StagePythonDropped},
	{ComponentOther, StageOther},
}

var phaseTwoComponentStages = []ComponentStage{
	{ComponentRuntime, StageLegacyPodCache},
	{ComponentCoverage, StageFinalEvidenceQueued},
	{ComponentCoverage, StageFinalEvidenceACKed},
	{ComponentCoverage, StageFinalEvidenceDropped},
	{ComponentControlPlane, StageSnapshotRefreshed}, {ComponentControlPlane, StageSnapshotUnavailable},
	{ComponentControlPlane, StageActivationFailed},
	{ComponentControlPlane, StageActiveQGSet}, {ComponentControlPlane, StageLegacyQGMigration},
	{ComponentControlPlane, StageDrainingQGReconciled},
	{ComponentControlPlane, StageFrozenPlanGeneration}, {ComponentControlPlane, StageActivationHold},
	{ComponentOwnership, StageAssignmentAcquired}, {ComponentOwnership, StageAssignmentLost},
	{ComponentOwnership, StageRebalancePlanned},
	{ComponentOwnership, StageAssignmentIndexWritten}, {ComponentOwnership, StageAssignmentIndexRead},
	{ComponentOwnership, StageTakeoverStarted}, {ComponentOwnership, StageTakeoverCompleted},
	{ComponentOwnership, StageLeaseRenewed}, {ComponentOwnership, StageFenceChecked},
	{ComponentScheduler, StageScheduleDue}, {ComponentScheduler, StageSlotStarted},
	{ComponentScheduler, StageSlotCompleted}, {ComponentScheduler, StageQueryAdmission},
	{ComponentScheduler, StageQueryCooldown}, {ComponentScheduler, StageRunnerReturned}, {ComponentScheduler, StageDispatcherSnapshot}, {ComponentScheduler, StageQueryPermitWait},
	{ComponentScheduler, StageExpiredRangeReturned},
	{ComponentScheduler, StageRunnerCompleted}, {ComponentScheduler, StageSlotSourceCompleted},
	{ComponentScheduler, StageScheduleCursorAdvanced},
	{ComponentAccess, StageQueryCompleted},
	{ComponentAccess, StageQueryBudgetResolved},
	{ComponentAccess, StageSlotReadinessArrival},
	{ComponentEvaluation, StageEvaluationCompleted},
	{ComponentState, StageStatePreflight}, {ComponentState, StageGapLoaded},
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

var allCommonReasons = joinReasons(unclassifiedReasons, contractClassReasons, []ReasonCode{ReasonOther})
var allResourceReasons = joinReasons(
	unclassifiedReasons, resourceOnlyReasons, contractClassReasons, []ReasonCode{ReasonOther})
var allLogReasons = joinReasons(unclassifiedReasons, resourceOnlyReasons, []ReasonCode{ReasonOther})

var componentStageSet = makeComponentStageSet(allComponentStages)
var metricComponentStageSet = makeComponentStageSet(metricComponentStages)
var resultSet = makeResultSet(allResults)
var operationSet = makeOperationSet(allOperations)
var metricOperationSet = makeOperationSet(metricOperations)
var directionSet = makeDirectionSet(allDirections)
var commonReasonSet = makeReasonSet(unclassifiedReasons)
var resourceReasonSet = makeReasonSet(resourceOnlyReasons)
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
