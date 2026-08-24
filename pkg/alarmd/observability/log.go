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

	ResultStarted   = "started"
	ResultBrokerACK = "broker_ack"
	ResultSuccess   = "success"
	ResultFailed    = "failed"
	ResultSkipped   = "skipped"
	ResultTimeout   = "timeout"
	ResultDropped   = "dropped"
	ResultReleased  = "released"
)

// Logger emits the fixed event envelope used by alarmd. Metric labels remain
// bounded; diagnostic log attributes may include record coordinates and a
// sanitized error, but never payloads or credentials.
type Logger struct {
	component string
	next      *slog.Logger
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
