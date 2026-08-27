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

const (
	ComponentRuntime        = "runtime"
	ComponentConsumer       = "consumer"
	ComponentAdapter        = "adapter"
	ComponentCompiler       = "compiler"
	ComponentState          = "state"
	ComponentDetect         = "detect"
	ComponentOutput         = "output"
	ComponentCoverage       = "coverage"
	ComponentResource       = "resource"
	ComponentPythonProducer = "python_producer"
	ComponentOther          = "_other"

	StageConfigLoaded         = "config_loaded"
	StageRestartRecovered     = "restart_recovered"
	StageKafkaAssigned        = "kafka_assigned"
	StageExecutionReceived    = "execution_received"
	StageOffsetGap            = "offset_gap"
	StageOffsetCommitted      = "offset_committed"
	StageMessageDecoded       = "message_decoded"
	StageRecordBatchReady     = "record_batch_ready"
	StageRejected             = "rejected"
	StagePlanCompiled         = "plan_compiled"
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
	OperationOther              = "_other"

	DirectionInput    Direction = "input"
	DirectionOutput   Direction = "output"
	DirectionInternal Direction = "internal"
	DirectionOther    Direction = "_other"

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

type TraceFields struct {
	ExecutionID             string
	MessageID               string
	QueryGroupKey           string
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
	Component         Component
	Stage             Stage
	Result            Result
	Operation         Operation
	Direction         Direction
	ReasonCode        ReasonCode
	Duration          time.Duration
	Counts            Counts
	Trace             TraceFields
	Err               error
	normalized        bool
	stageReasonBucket bool
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
	observation.Result = NormalizeResult(observation.Result)
	observation.stageReasonBucket = rawReason == "" ||
		(rawReason == ReasonNone && !resultAllowsNone(observation.Result))
	observation.Operation = NormalizeOperation(observation.Operation)
	observation.Direction = NormalizeDirection(observation.Direction)
	observation.ReasonCode = NormalizeReason(observation.ReasonCode, observation.Result)
	observation.Counts = normalizeCounts(observation.Counts)
	observation.normalized = true
	return observation
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

func AllStages() []Stage {
	stages := make([]Stage, 0, len(allComponentStages))
	for _, pair := range allComponentStages {
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

var allComponentStages = []ComponentStage{
	{ComponentRuntime, StageStartup}, {ComponentRuntime, StageConfigLoaded},
	{ComponentRuntime, StageShutdown}, {ComponentRuntime, StageFatal},
	{ComponentRuntime, StageRestartRecovered},
	{ComponentConsumer, StageKafkaAssigned}, {ComponentConsumer, StageExecutionReceived},
	{ComponentConsumer, StageOffsetGap}, {ComponentConsumer, StageOffsetCommitted},
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

var allResults = []Result{
	Result(ResultStarted), Result(ResultSuccess), ResultTerminal, ResultRetrying, ResultPaused,
	ResultResumed, ResultDegraded, Result(ResultTimeout), Result(ResultFailed), ResultOther,
}

var allOperations = []Operation{
	OperationNone, OperationCompile, OperationCacheHit, OperationCacheMiss, OperationCacheEvict,
	OperationRequirementCompile, OperationLoad, OperationDecode, OperationEncode, OperationWrite,
	OperationConsume, OperationProduce, OperationACK, OperationCommit, OperationOffsetRepair,
	OperationSample, OperationTransition, OperationOther,
}

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
var resultSet = makeResultSet(allResults)
var operationSet = makeOperationSet(allOperations)
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
