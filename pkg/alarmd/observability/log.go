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
	return &Logger{
		component: component,
		next: slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{
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
	attributes := []slog.Attr{
		slog.String("component", string(observation.Component)),
		slog.String("stage", string(observation.Stage)),
		slog.String("result", string(observation.Result)),
		slog.String("reason_code", string(observation.ReasonCode)),
		slog.String("operation", string(observation.Operation)),
		slog.String("direction", string(observation.Direction)),
		slog.Int64("duration_ms", observation.Duration.Milliseconds()),
	}
	attributes = appendObservationCounts(attributes, observation.Counts)
	attributes = appendTraceFields(attributes, observation.Trace)
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
		{"execution_id", trace.ExecutionID}, {"message_id", trace.MessageID},
		{"query_group_key", trace.QueryGroupKey}, {"strategy_id", trace.StrategyID},
		{"level_id", trace.LevelID}, {"record_id", trace.RecordID},
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
	return attributes
}

func mandatoryLogStage(stage Stage) bool {
	switch stage {
	case StageStartup, StageConfigLoaded, StageKafkaAssigned, StageShutdown, StageFatal:
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
		return observation.Result == ResultResumed || exceptionalLogResult(observation.Result)
	}
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
