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
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

const (
	ComponentTrigger    = "trigger"
	ComponentComparator = "comparator"

	StageStartup         = "startup"
	StageDetect          = "detect"
	StageFatal           = "fatal"
	StageShutdown        = "shutdown"
	StageDecisionACK     = "go_decision_ack"
	StageComparisonACK   = "comparison_audit_ack"
	StageCapacityDrop    = "capacity_drop"
	StageCoverageRelease = "coverage_release"
	StageCoverageReset   = "coverage_reset"
	StageOffsetReset     = "offset_reset"

	ResultStarted     = "started"
	ResultBrokerACK   = "broker_ack"
	ResultSuccess     = "success"
	ResultFailed      = "failed"
	ResultSkipped     = "skipped"
	ResultTimeout     = "timeout"
	ResultDropped     = "dropped"
	ResultReleased    = "released"
	ResultRecovered   = "recovered"
	ResultInvalidated = "invalidated"
)

// Logger emits the fixed event envelope used by alarmd. Metric labels remain
// bounded; diagnostic log attributes may include record coordinates and a
// sanitized error, but never payloads or credentials.
type Logger struct {
	component string
	next      *slog.Logger
	writer    *serializedLogWriter
}

// BoundedLogPolicy always records one-time lifecycle transitions. Routine
// success is omitted. Repeated transitions, recovery and exceptional results
// use a concrete limiter with fixed per-reason or reason-empty stage buckets;
// M8 does not hard-code a sampling rate or time window before G3 calibration.
type BoundedLogPolicy struct {
	repeated *WindowLogLimiter
}

func NewBoundedLogPolicy(limiter *WindowLogLimiter) (*BoundedLogPolicy, error) {
	if limiter == nil {
		return nil, errors.New("observability: bounded log policy requires a window limiter")
	}
	return &BoundedLogPolicy{repeated: limiter}, nil
}

func (p *BoundedLogPolicy) ShouldLog(observation Observation) bool {
	if mandatoryLogStage(observation.Stage) {
		return true
	}
	if !repeatedLogObservation(observation) || p == nil || p.repeated == nil {
		return false
	}
	return p.repeated.Allow(observation)
}

type LoggingObserver struct {
	logger *Logger
	policy *BoundedLogPolicy
}

func NewLoggingObserver(logger *Logger, policy *BoundedLogPolicy) *LoggingObserver {
	return &LoggingObserver{logger: logger, policy: policy}
}

func New(component string, writer io.Writer) *Logger {
	if writer == nil {
		writer = io.Discard
	}
	locked := &serializedLogWriter{next: writer}
	return &Logger{
		writer:    locked,
		component: component,
		next: slog.New(slog.NewJSONHandler(locked, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})),
	}
}

func Discard(component string) *Logger {
	return New(component, io.Discard)
}

func (l *Logger) Info(stage, result string, records int, duration time.Duration, attrs ...slog.Attr) {
	l.log(slog.LevelInfo, stage, result, records, duration, attrs...)
}

func (l *Logger) Error(stage, result string, records int, duration time.Duration, attrs ...slog.Attr) {
	l.log(slog.LevelError, stage, result, records, duration, attrs...)
}

func (l *LoggingObserver) Observe(ctx context.Context, observation Observation) {
	if l == nil || l.logger == nil || l.logger.next == nil || l.policy == nil {
		return
	}
	observation = NormalizeObservation(observation)
	if !l.policy.ShouldLog(observation) {
		return
	}
	l.logger.logObservation(ctx, observation)
}

func (l *Logger) logObservation(ctx context.Context, observation Observation) {
	observation.Trace = mergeTraceFields(observation.Trace, TraceFieldsFromContext(ctx))
	attributes := []slog.Attr{
		slog.String("component", string(observation.Component)),
		slog.String("stage", string(observation.Stage)),
		slog.String("result", string(observation.Result)),
		slog.String("reason_code", string(observation.ReasonCode)),
		slog.String("operation", string(observation.Operation)),
		slog.String("direction", string(observation.Direction)),
		slog.Int64("duration_ms", observation.Duration.Milliseconds()),
	}
	if f := observation.QueryTiming; f != nil {
		attributes = append(attributes, slog.Any("query_timing", f))
	}
	if f := observation.ShortPeriodCompletion; f != nil {
		attributes = append(attributes, slog.Any("short_period_completion", f))
	}
	if f := observation.CapacityRejection; f != nil {
		attributes = append(attributes, slog.String("capacity_phase", f.Phase), slog.Uint64("capacity_shared_used", f.SharedUsed), slog.Uint64("capacity_requested", f.Requested), slog.Uint64("capacity_limit", f.Limit))
		if f.OwnUsed != nil {
			attributes = append(attributes, slog.Uint64("capacity_own_used", *f.OwnUsed))
		}
	}
	if observation.CapacityBudget != "" {
		attributes = append(attributes, slog.String("capacity_budget", string(observation.CapacityBudget)))
	}
	if observation.SourceKind != "" {
		attributes = append(attributes, slog.String("source_kind", string(observation.SourceKind)))
	}
	attributes = appendObservationCounts(attributes, observation.Counts)
	attributes = appendTraceFields(attributes, observation.Trace)
	if f := observation.QueryFailure; f != nil {
		attributes = append(attributes, slog.String("failure_stage", f.Stage), slog.String("failure_category", f.Category), slog.String("failure_code", f.Code))
	}
	if observation.RuntimeConfig != nil {
		attributes = append(attributes, slog.Any("runtime_config", observation.RuntimeConfig))
	}
	if facts := observation.DrainingQG; facts != nil {
		attributes = append(attributes,
			slog.Int("draining_total", facts.Total),
			slog.Int("draining_undrained", facts.Undrained),
			slog.Int("draining_isolated", facts.Isolated),
			slog.Bool("draining_samples_truncated", facts.Truncated),
			slog.Any("draining_samples", facts.Samples),
		)
	}
	if facts := observation.SourceRefresh; facts != nil {
		attributes = append(attributes,
			slog.String("source_refresh_status", string(facts.Status)),
			slog.Bool("source_refresh_counts_known", facts.CountsKnown),
		)
		if facts.ObservationID != "" {
			attributes = append(attributes, slog.String("source_observation_id", facts.ObservationID))
		}
		if facts.SnapshotRevision != "" {
			attributes = append(attributes, slog.String("snapshot_revision", facts.SnapshotRevision))
		}
		if facts.PublicationEpoch > 0 {
			attributes = append(attributes, slog.Uint64("publication_epoch", facts.PublicationEpoch))
		}
		if facts.CountsKnown {
			attributes = append(attributes,
				slog.Int("old_query_groups", facts.OldQueryGroups),
				slog.Int("new_query_groups", facts.NewQueryGroups),
				slog.Int("added_query_groups", facts.AddedQueryGroups),
				slog.Int("retired_query_groups", facts.RetiredQueryGroups),
			)
		}
	}
	if facts := observation.ActivationFailure; facts != nil {
		attributes = append(attributes,
			slog.String("activation_failure_stage", string(facts.Stage)),
			slog.String("activation_failure_class", string(facts.Class)),
		)
		if facts.Stage == ActivationFailureStageReactivation {
			attributes = append(attributes,
				slog.Int("draining_query_groups", facts.DrainingQueryGroups),
				slog.Int("candidate_query_groups", facts.CandidateQueryGroups),
				slog.Int("reappeared_query_groups", facts.ReappearedQueryGroups),
			)
			if facts.Class == ActivationFailureClassNotDrained {
				attributes = append(attributes,
					slog.Bool("reappeared_query_group_samples_truncated", facts.ReappearedQueryGroupSamplesTruncated),
					slog.Any("reappeared_query_group_samples", facts.ReappearedQueryGroupSamples),
				)
			}
		}
	}
	if len(observation.AlgorithmEvaluations) > 0 {
		attributes = append(attributes, slog.Any("algorithm_evaluations", observation.AlgorithmEvaluations))
	}
	if len(observation.AlgorithmInputs) > 0 {
		attributes = append(attributes, slog.Any("algorithm_inputs", observation.AlgorithmInputs))
	}
	if observation.Err != nil {
		attributes = append(attributes, slog.String("error_type", fmt.Sprintf("%T", observation.Err)))
	}
	level := slog.LevelInfo
	if observation.Result == Result(ResultFailed) || observation.Result == Result(ResultTimeout) {
		level = slog.LevelError
	}
	if ctx == nil {
		ctx = context.Background()
	}
	l.next.LogAttrs(ctx, level, "alarmd event", attributes...)
}

func (l *Logger) log(
	level slog.Level,
	stage, result string,
	records int,
	duration time.Duration,
	attrs ...slog.Attr,
) {
	if l == nil || l.next == nil {
		return
	}
	if duration < 0 {
		duration = 0
	}
	fixed := []slog.Attr{
		slog.String("component", l.component),
		slog.String("stage", stage),
		slog.String("result", result),
		slog.Int("records", records),
		slog.Int64("duration_ms", duration.Milliseconds()),
	}
	l.next.LogAttrs(context.Background(), level, "alarmd event", append(fixed, attrs...)...)
}

func appendObservationCounts(attributes []slog.Attr, counts Counts) []slog.Attr {
	values := []struct {
		name  string
		value int64
	}{
		{"messages", counts.Messages}, {"records", counts.Records}, {"plans", counts.Plans},
		{"levels", counts.Levels}, {"events", counts.Events}, {"bytes", counts.Bytes},
		{"keys", counts.Keys}, {"state_bytes", counts.StateBytes},
	}
	for _, value := range values {
		if value.value > 0 {
			attributes = append(attributes, slog.Int64(value.name, value.value))
		}
	}
	return attributes
}

func appendTraceFields(attributes []slog.Attr, trace TraceFields) []slog.Attr {
	strings := []struct {
		name  string
		value string
	}{
		{"trace_id", trace.TraceID}, {"execution_id", trace.ExecutionID}, {"message_id", trace.MessageID},
		{"query_group_key", trace.QueryGroupKey}, {"strategy_id", trace.StrategyID},
		{"snapshot_revision", trace.SnapshotRevision}, {"query_revision", trace.QueryRevision},
		{"schedule_revision", trace.ScheduleRevision}, {"due_plan_set_digest", trace.DuePlanSetDigest},
		{"owner_id", trace.OwnerID},
		{"level_id", trace.LevelID}, {"terminal_scope", trace.TerminalScope},
		{"field_path", trace.TerminalFieldPath}, {"record_id", trace.RecordID},
		{"dimension_identity_digest", trace.DimensionIdentityDigest}, {"topic", trace.Topic},
		{"source_window", trace.SourceWindow},
	}
	for _, value := range strings {
		if value.value != "" {
			attributes = append(attributes, slog.String(value.name, value.value))
		}
	}
	if trace.PartitionKnown {
		attributes = append(attributes, slog.Int64("partition", int64(trace.Partition)))
	}
	if trace.OffsetKnown {
		attributes = append(attributes, slog.Int64("offset", trace.Offset))
	}
	if trace.OwnerEpoch > 0 {
		attributes = append(attributes, slog.Uint64("owner_epoch", trace.OwnerEpoch))
	}
	if trace.EvaluationTime > 0 {
		attributes = append(attributes, slog.Int64("evaluation_time", trace.EvaluationTime))
	}
	if trace.ScheduleSegmentStart > 0 {
		attributes = append(attributes, slog.Int64("schedule_segment_start", trace.ScheduleSegmentStart))
	}
	return attributes
}

func mandatoryLogStage(stage Stage) bool {
	switch stage {
	case StageStartup, StageConfigLoaded, StageKafkaAssigned, StageShutdown, StageFatal:
		return true
	case StageSnapshotRefreshed, StageSnapshotUnavailable, StageAssignmentAcquired, StageAssignmentLost,
		StageTakeoverStarted, StageTakeoverCompleted:
		return true
	default:
		return false
	}
}

func repeatedLogObservation(observation Observation) bool {
	switch observation.Stage {
	case StageOffsetGap, StageResourceSoft, StageResourceHard, StageResourceResumed, StageRestartRecovered:
		return true
	default:
		return isPhaseTwoWorkflowStage(observation.Stage) || observation.Result == ResultResumed || exceptionalLogResult(observation.Result)
	}
}

func isPhaseTwoWorkflowStage(stage Stage) bool {
	for _, pair := range phaseTwoComponentStages {
		if pair.Stage == stage {
			return true
		}
	}
	return false
}

func exceptionalLogResult(result Result) bool {
	switch result {
	case ResultTerminal, ResultRetrying, ResultPaused, Result(ResultTimeout), Result(ResultFailed):
		return true
	default:
		return false
	}
}

var _ Observer = (*LoggingObserver)(nil)

type serializedLogWriter struct {
	mu   sync.Mutex
	next io.Writer
}

func (w *serializedLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.next.Write(p)
}
