// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"sync"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A batch of a Plan's evaluations mixing routine ones with ones that are
// not says each of the others as it was, and the routine ones as one line
// as the batch ends: as many evaluations, and no series' trace.
func TestABatchSaysItsRoutineEvaluationsAsOneLineAndTheOthersEach(t *testing.T) {
	var mu sync.Mutex
	var said []observability.Observation
	stream := &streamedExecution{coordinator: &SlotExecutionCoordinator{ports: Ports{
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			mu.Lock()
			said = append(said, observation)
			mu.Unlock()
		})}}}
	due := execution.DuePlan{Identity: execution.PlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: "7"}}
	line := func(series string, reason observability.ReasonCode) observability.Observation {
		return observability.Observation{Component: observability.ComponentEvaluation,
			Stage: observability.StageEvaluationCompleted, Result: observability.ResultSuccess, ReasonCode: reason,
			Counts: observability.Counts{Records: 30}, DurationKnown: true,
			Trace:         observability.TraceFields{StrategyID: "7", DimensionIdentityDigest: series},
			LevelOutcomes: []observability.LevelOutcomeFact{{Outcome: "NORMAL", Count: 1}}}
	}
	ctx := context.Background()
	stream.sayEvaluation(ctx, due, line("r1", ""))
	stream.sayEvaluation(ctx, due, line("x1", "QUERY_TIMEOUT"))
	stream.sayEvaluation(ctx, due, line("r2", ""))
	stream.sayEvaluation(ctx, due, line("r3", ""))
	if len(said) != 1 || said[0].Trace.DimensionIdentityDigest != "x1" || said[0].ReasonCode != "QUERY_TIMEOUT" {
		t.Fatalf("said %+v before the batch ended, want only the line with a reason, as it was", said)
	}
	stream.sayRoutineEvaluations(ctx)
	if len(said) != 2 {
		t.Fatalf("said %d lines, want the one with a reason and one of the routine ones", len(said))
	}
	merged := said[1]
	if merged.Counts.Evaluations != 3 || merged.Counts.Records != 90 || merged.Trace.DimensionIdentityDigest != "" ||
		len(merged.LevelOutcomes) != 1 || merged.LevelOutcomes[0].Count != 3 {
		t.Fatalf("routine line %+v, want three evaluations' counts and no series", merged)
	}
	stream.sayRoutineEvaluations(ctx)
	if len(said) != 2 {
		t.Fatal("a batch with nothing left to say said its routine line again")
	}
}
