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
	"log/slog"
	"testing"
	"time"
)

// The evaluation and Slot completion lines of an empty answer, an algorithm
// unavailable under a timeout, and a warming or gapped window keep the level
// and the admission they had before the query line split its result from the
// evaluation's (d03b7a7b4): their result and reason are the evaluation's and
// the Slot's, and the carried facts beside them change neither.
func TestEvaluationAndSlotCompletionLinesKeepTheirLevel(t *testing.T) {
	for _, testCase := range []struct {
		reason ReasonCode
		level  slog.Level
	}{
		{"QUERY_EMPTY", slog.LevelInfo},
		{"QUERY_TIMEOUT", slog.LevelWarn},
		{"HISTORY_WARMING", slog.LevelInfo},
		{"HISTORY_GAPPED", slog.LevelInfo},
	} {
		for _, stage := range []struct {
			component Component
			stage     Stage
		}{{ComponentEvaluation, StageEvaluationCompleted}, {ComponentScheduler, StageSlotCompleted}} {
			for _, carried := range []*CarriedReasonFacts{nil, {OriginSlot: 1_700_000_040, OriginCause: "QUERY_TIMEOUT"}} {
				observation := NormalizeObservation(Observation{Component: stage.component, Stage: stage.stage,
					Result: ResultDegraded, ReasonCode: testCase.reason, Duration: time.Millisecond, CarriedReason: carried})
				if got := observationLevel(observation); got != testCase.level {
					t.Fatalf("%s %s carried=%t: level %s, want %s", stage.stage, testCase.reason, carried != nil, got, testCase.level)
				}
				limiter, err := NewScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Hour, MaxEvents: 100, MaxScopes: 1024})
				if err != nil {
					t.Fatal(err)
				}
				policy, err := NewScopedBoundedLogPolicy(limiter)
				if err != nil {
					t.Fatal(err)
				}
				if admission := policy.Admit(observation); !admission.Allowed || admission.Unwritten {
					t.Fatalf("%s %s carried=%t: admission %+v, want written", stage.stage, testCase.reason, carried != nil, admission)
				}
			}
		}
	}
}
