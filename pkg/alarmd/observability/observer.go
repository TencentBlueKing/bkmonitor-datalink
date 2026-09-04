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

	StageConfigLoaded         = "config_loaded"
	StageSnapshotRefreshed    = "snapshot_refreshed"
	StageSnapshotUnavailable  = "snapshot_unavailable"
	StageActiveQGSet          = "active_qg_set"
	StageLegacyQGMigration    = "legacy_active_qg_migration"
	StageDrainingQGReconciled = "draining_query_groups"
	StageAssignmentAcquired   = "assignment_acquired"
	StageAssignmentLost       = "assignment_lost"
	StageTakeoverStarted      = "takeover_started"
	StageTakeoverCompleted    = "takeover_completed"
	StageLeaseRenewed         = "lease_renewed"
	StageFenceChecked         = "fence_checked"
	StageScheduleDue          = "schedule_due"
	StageSlotStarted          = "slot_started"
	StageSlotCompleted        = "slot_completed"
	StageQueryAdmission       = "query_admission"
	StageRestartRecovered     = "restart_recovered"
	StageKafkaAssigned        = "kafka_assigned"
	StageExecutionReceived    = "execution_received"
	StageOffsetGap            = "offset_gap"
	StageOffsetMarked         = "offset_marked"
	StageMessageDecoded       = "message_decoded"
	StageRecordBatchReady     = "record_batch_ready"
	StageRejected             = "rejected"
	StagePlanCompiled         = "plan_compiled"
	StageQueryCompleted       = "query_completed"
	StageStatePreflight       = "state_preflight"
	StageGapLoaded            = "gap_loaded"
	StageEvaluationCompleted  = "evaluation_completed"
	StageSideEffectAdmission  = "side_effect_admission"
	StageStateAdmission       = "state_admission"
	StageGapGuardCommitted    = "gap_guard_committed"
	StageMutationCompared     = "mutation_compared"
	StageEventACKed           = "event_acked"
	StageStateApplied         = "state_applied"
	StageProgressCommitted    = "progress_committed"
	StageDependencyLoaded     = "dependency_loaded"
	StageStateCommitted       = "state_committed"
	StageDetectCompleted      = "detect_completed"
	StageTriggerCompleted     = "trigger_completed"
	StageOutputACKed          = "output_acked"
	StageCoverageCompleted    = "coverage_completed"
	StageCoverageGap          = "coverage_gap"
	StageReceiptQueued        = "receipt_queued"
	StageResourceSoft         = "resource_soft"
	StageResourceHard         = "resource_hard"
	StageResourceResumed      = "resource_resumed"
	StageComparisonCompleted  = "comparison_completed"
	StageComparisonAuditACKed = "comparison_audit_acked"
	StagePythonSource         = "source"
	StagePythonBuilt          = "built"
	StagePythonEnqueued       = "enqueued"
	StagePythonPublished      = "published"
	StagePythonACKed          = "acked"
	StagePythonDropped        = "dropped"
	StageOther                = "_other"

	ResultTerminal = "terminal"
	ResultRetrying = "retrying"
	ResultPaused   = "paused"
	ResultResumed  = "resumed"
	ResultDegraded = "degraded"
	ResultOther    = "_other"

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

	AlgorithmFamilyThreshold       AlgorithmFamily = "threshold"
	AlgorithmFamilySimpleRingRatio AlgorithmFamily = "simple_ring_ratio"
	AlgorithmFamilyOsRestart       AlgorithmFamily = "os_restart"
	AlgorithmFamilyProcPort        AlgorithmFamily = "proc_port"
	AlgorithmFamilyPingUnreachable AlgorithmFamily = "ping_unreachable"

	AlgorithmDetectorKindThreshold       AlgorithmDetectorKind = "Threshold"
	AlgorithmDetectorKindSimpleRingRatio AlgorithmDetectorKind = "SimpleRingRatio"
	AlgorithmDetectorKindOsRestart       AlgorithmDetectorKind = "OsRestart"
	AlgorithmDetectorKindProcPort        AlgorithmDetectorKind = "ProcPort"

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

	AlgorithmInputResultAvailable   AlgorithmInputResult = "available"
	AlgorithmInputResultMissing     AlgorithmInputResult = "missing"
	AlgorithmInputResultPartial     AlgorithmInputResult = "partial"
	AlgorithmInputResultUnavailable AlgorithmInputResult = "unavailable"

	SourceRefreshPending   SourceRefreshStatus = "PENDING_CONFIRMATION"
	SourceRefreshPublished SourceRefreshStatus = "PUBLISHED"
	SourceRefreshUnchanged SourceRefreshStatus = "UNCHANGED"
	SourceRefreshConflict  SourceRefreshStatus = "PUBLICATION_CONFLICT"

	ReasonNone                  ReasonCode = "none"
	ReasonInternalUnknown       ReasonCode = "internal_unknown"
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

type LegacyQGMigrationFacts struct {
	Result      string
	ReasonClass string
	ScanKeys    int
	Duration    time.Duration
}

const MaxDrainingQGLogSamples = 8

type DrainingQGSample struct {
	QueryGroupKey   string `json:"query_group_key"`
	RetiredBoundary int64  `json:"retired_boundary"`
	NextSlot        int64  `json:"next_slot"`
	ProgressStatus  string `json:"progress_status"`
}

type DrainingQGFacts struct {
	Total     int                `json:"total"`
	Undrained int                `json:"undrained"`
	Isolated  int                `json:"isolated"`
	Samples   []DrainingQGSample `json:"samples,omitempty"`
	Truncated bool               `json:"truncated"`
}

// SourceRefreshFacts carries one bounded source refresh outcome. Snapshot
// identity is diagnostic log context only; Prometheus consumes Status alone.
type SourceRefreshFacts struct {
	Status             SourceRefreshStatus
	ObservationID      string
	SnapshotRevision   string
	PublicationEpoch   uint64
	CountsKnown        bool
	OldQueryGroups     int
	NewQueryGroups     int
	AddedQueryGroups   int
	RetiredQueryGroups int
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
	Component            Component
	Stage                Stage
	Result               Result
	Operation            Operation
	Direction            Direction
	ReasonCode           ReasonCode
	Duration             time.Duration
	Counts               Counts
	Trace                TraceFields
	Err                  error
	CapacityBudget       CapacityBudget
	SourceKind           SourceKind
	QueryPermit          *QueryPermitFacts
	ActiveQGSet          *ActiveQGSetFacts
	LegacyMigration      *LegacyQGMigrationFacts
	DrainingQG           *DrainingQGFacts
	SourceRefresh        *SourceRefreshFacts
	AlgorithmEvaluations []AlgorithmEvaluationFact
	AlgorithmInputs      []AlgorithmInputFact
	normalized           bool
	stageReasonBucket    bool
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
	observation.QueryPermit = normalizeQueryPermitFacts(observation.QueryPermit)
	observation.ActiveQGSet = normalizeActiveQGSetFacts(observation.ActiveQGSet)
	observation.LegacyMigration = normalizeLegacyQGMigrationFacts(observation.LegacyMigration)
	observation.DrainingQG = normalizeDrainingQGFacts(observation.DrainingQG)
	observation.SourceRefresh = normalizeSourceRefreshFacts(observation.Component, observation.Stage, observation.SourceRefresh)
	observation.AlgorithmEvaluations, observation.AlgorithmInputs = normalizeAlgorithmFacts(observation)
	observation.Counts = normalizeCounts(observation.Counts)
	observation.normalized = true
	return observation
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
		AlgorithmDependencyPointTenMinute, AlgorithmDependencyPointTwentyFiveMinute:
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
	for _, count := range []*int{&normalized.Total, &normalized.Undrained, &normalized.Isolated} {
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
		return ReasonInternalUnknown
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
	{ComponentControlPlane, StageSnapshotRefreshed}, {ComponentControlPlane, StageSnapshotUnavailable},
	{ComponentControlPlane, StageActiveQGSet}, {ComponentControlPlane, StageLegacyQGMigration},
	{ComponentControlPlane, StageDrainingQGReconciled},
	{ComponentOwnership, StageAssignmentAcquired}, {ComponentOwnership, StageAssignmentLost},
	{ComponentOwnership, StageTakeoverStarted}, {ComponentOwnership, StageTakeoverCompleted},
	{ComponentOwnership, StageLeaseRenewed}, {ComponentOwnership, StageFenceChecked},
	{ComponentScheduler, StageScheduleDue}, {ComponentScheduler, StageSlotStarted},
	{ComponentScheduler, StageSlotCompleted}, {ComponentScheduler, StageQueryAdmission},
	{ComponentAccess, StageQueryCompleted},
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

var allCommonReasons = []ReasonCode{
	ReasonNone, ReasonInternalUnknown,
	ReasonContractDeterministic, ReasonContractRetryable, ReasonContractCoverage,
	ReasonOther,
}
var allResourceReasons = []ReasonCode{
	ReasonNone, ReasonInternalUnknown, ReasonCPU, ReasonRSS, ReasonHeap, ReasonGC,
	ReasonWorkerQueue, ReasonInflight, ReasonConsumerLag, ReasonStateBytes,
	ReasonContractDeterministic, ReasonContractRetryable, ReasonContractCoverage,
	ReasonOther,
}
var allLogReasons = []ReasonCode{
	ReasonNone, ReasonInternalUnknown, ReasonCPU, ReasonRSS, ReasonHeap, ReasonGC,
	ReasonWorkerQueue, ReasonInflight, ReasonConsumerLag, ReasonStateBytes, ReasonOther,
}

var componentStageSet = makeComponentStageSet(allComponentStages)
var metricComponentStageSet = makeComponentStageSet(metricComponentStages)
var resultSet = makeResultSet(allResults)
var operationSet = makeOperationSet(allOperations)
var metricOperationSet = makeOperationSet(metricOperations)
var directionSet = makeDirectionSet(allDirections)
var commonReasonSet = makeReasonSet(allCommonReasons[:2])
var resourceReasonSet = makeReasonSet(allLogReasons[2 : len(allLogReasons)-1])
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
