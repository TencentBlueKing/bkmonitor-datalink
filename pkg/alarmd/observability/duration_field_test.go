// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// durationField is a line's duration_ms, and whether the line has one.
func durationField(t *testing.T, line string) (float64, bool) {
	t.Helper()
	line = strings.TrimSpace(line)
	if line == "" || strings.Contains(line, "\n") {
		t.Fatalf("want one line, got %q", line)
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		t.Fatalf("decode %q: %v", line, err)
	}
	value, ok := event["duration_ms"]
	if !ok {
		return 0, false
	}
	ms, isNumber := value.(float64)
	if !isNumber {
		t.Fatalf("duration_ms %#v is not a number", value)
	}
	return ms, true
}

// A line written for an observation carries duration_ms only where the
// observation is timed: a point event (no Duration, not DurationKnown) has
// none; a measured duration has its milliseconds; a measured zero
// (DurationKnown) is written 0, not dropped.
func TestAnObservationsLineCarriesDurationOnlyWhereItWasTimed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		duration time.Duration
		known    bool
		want     float64
		written  bool
	}{
		{"a point event", 0, false, 0, false},
		{"a measured duration", 1500 * time.Millisecond, true, 1500, true},
		{"a measured zero", 0, true, 0, true},
		{"a positive duration from an older producer", 40 * time.Millisecond, false, 40, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := answeredLine(t, Observation{Component: ComponentAccess, Stage: StageQueryCompleted, Result: ResultFailed,
				ReasonCode: ReasonCode("QUERY_TIMEOUT"), Err: errors.New("static error text"), Duration: tc.duration, DurationKnown: tc.known})
			got, written := durationField(t, line)
			if written != tc.written || got != tc.want {
				t.Fatalf("duration_ms %v (written %v), want %v (written %v): %s", got, written, tc.want, tc.written, line)
			}
		})
	}
}

// The logger's timed events carry duration_ms, zero included; its point
// events (InfoEvent, WarnEvent, ErrorEvent) carry none, and keep the rest
// of the envelope.
func TestTheLoggersPointEventsCarryNoDurationAndItsTimedOnesDo(t *testing.T) {
	for _, tc := range []struct {
		name    string
		write   func(*Logger)
		want    float64
		written bool
	}{
		{"Info timed at zero", func(l *Logger) { l.Info(StageStartup, ResultStarted, 0, 0) }, 0, true},
		{"Warn timed", func(l *Logger) { l.Warn(StageStartup, ResultDegraded, 0, 250*time.Millisecond) }, 250, true},
		{"InfoEvent", func(l *Logger) { l.InfoEvent(StageStartup, ResultStarted, 3, slog.String("detail", "d")) }, 0, false},
		{"WarnEvent", func(l *Logger) { l.WarnEvent(StageStartup, ResultDegraded, 0) }, 0, false},
		{"ErrorEvent", func(l *Logger) { l.ErrorEvent(StageShutdown, ResultFailed, 0) }, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			tc.write(New(ComponentTrigger, &output))
			got, written := durationField(t, output.String())
			if written != tc.written || got != tc.want {
				t.Fatalf("duration_ms %v (written %v), want %v (written %v): %s", got, written, tc.want, tc.written, output.String())
			}
			var event map[string]any
			_ = json.Unmarshal(output.Bytes(), &event)
			for _, field := range []string{"component", "stage", "result", "records"} {
				if _, ok := event[field]; !ok {
					t.Fatalf("line %s has no %s", output.String(), field)
				}
			}
		})
	}
}
