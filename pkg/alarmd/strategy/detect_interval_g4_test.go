// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package strategy

import (
	"context"
	"encoding/json"
	"testing"
)

// steppedG4Request is the request of an item detected every fifteen seconds
// over one-minute windows: its trigger counts fifteen-second steps.
// The previous point, SimpleRingRatio's or OsRestart's, is previous seconds
// back.
func steppedG4Request(t *testing.T, kind string, config map[string]any, dependency string, previous int64) CompileRequest {
	t.Helper()
	projection := AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}}
	requirements := g4Requirements(t, dependency)
	if dependency == "previous" {
		requirements[1].RelativeWindow = AlgorithmRelativeWindow{StartOffsetSeconds: -previous - 60, EndOffsetSeconds: -previous, HalfOpen: true}
		requirements[1].PointOffsetsSeconds = []int64{previous}
		requirements[1].NamedPoints = []AlgorithmNamedInputPoint{{Name: "previous", OffsetSeconds: previous}}
	} else {
		requirements[1].PointOffsetsSeconds = []int64{previous, 600, 1500}
		requirements[1].NamedPoints = []AlgorithmNamedInputPoint{
			{Name: "previous", OffsetSeconds: previous}, {Name: "previous_10m", OffsetSeconds: 600}, {Name: "previous_25m", OffsetSeconds: 1500},
		}
	}
	requirements[1] = withG4RequirementID(t, requirements[1])
	request := g4CompileRequest(t, kind, config, projection, requirements)
	request.Plan.StrategyIR.ExecutionSemantics.EvaluationInterval = 15
	var trigger map[string]any
	if err := json.Unmarshal(request.Plan.StrategyIR.Levels[0].TriggerPlan.Config, &trigger); err != nil {
		t.Fatal(err)
	}
	trigger["step_seconds"] = 15
	request.Plan.StrategyIR.Levels[0].TriggerPlan.Config = mustJSON(trigger)
	return request
}

// SimpleRingRatio and OsRestart compare with the previous aggregation
// window, as the Python detectors do (simple_ring_ratio.py and
// os_restart.py get_history_offsets [agg_interval]): an item detected every
// fifteen seconds over one-minute windows compiles with its previous point
// a minute back, and a previous point a step back is refused.
func TestThePreviousPointOfARingRatioIsThePreviousAggregationWindow(t *testing.T) {
	compiler := newTestCompiler(t)
	for _, test := range []struct{ kind, dependency string }{
		{DetectorKindSimpleRingRatio, "previous"}, {DetectorKindOsRestart, "uptime_history"},
	} {
		t.Run(test.kind, func(t *testing.T) {
			config := map[string]any{}
			if test.kind == DetectorKindSimpleRingRatio {
				config = map[string]any{"floor": 50, "ceil": nil}
			}
			compiled := mustCompileG4Request(t, compiler, steppedG4Request(t, test.kind, config, test.dependency, 60))
			if compiled.Levels().Len() != 1 {
				t.Fatal("refused its previous point an aggregation interval back")
			}
			stepBack, err := compiler.Compile(context.Background(), steppedG4Request(t, test.kind, config, test.dependency, 15))
			if plan, ok := stepBack.Plan(); err == nil && ok && plan.Levels().Len() != 0 {
				t.Fatal("compiled with its previous point a step back, within the current window")
			}
		})
	}
}
