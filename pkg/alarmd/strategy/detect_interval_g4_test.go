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
	"encoding/json"
	"testing"
)

// steppedG4Request is the request of an item detected every fifteen seconds
// over one-minute windows: its trigger counts fifteen-second steps, and the
// previous point it compares with is the previous detection.
func steppedG4Request(t *testing.T, kind string, config map[string]any, dependency string) CompileRequest {
	t.Helper()
	projection := AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}}
	requirements := g4Requirements(t, dependency)
	if dependency == "previous" {
		requirements[1].RelativeWindow = AlgorithmRelativeWindow{StartOffsetSeconds: -75, EndOffsetSeconds: -15, HalfOpen: true}
		requirements[1].PointOffsetsSeconds = []int64{15}
		requirements[1].NamedPoints = []AlgorithmNamedInputPoint{{Name: "previous", OffsetSeconds: 15}}
	} else {
		requirements[1].PointOffsetsSeconds = []int64{15, 600, 1500}
		requirements[1].NamedPoints = []AlgorithmNamedInputPoint{
			{Name: "previous", OffsetSeconds: 15}, {Name: "previous_10m", OffsetSeconds: 600}, {Name: "previous_25m", OffsetSeconds: 1500},
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

// The previous point of SimpleRingRatio and of OsRestart is the previous
// detection: an evaluation step back, over an aggregation interval's window.
// An item detected every fifteen seconds over one-minute windows compiles
// with its previous point fifteen seconds back.
func TestThePreviousPointOfAComparisonIsTheEvaluationStepBack(t *testing.T) {
	compiler := newTestCompiler(t)
	ringRatio := mustCompileG4Request(t, compiler, steppedG4Request(t, DetectorKindSimpleRingRatio, map[string]any{"floor": 50, "ceil": nil}, "previous"))
	if _, ok := ringRatio.Levels().At(0).Algorithms().At(0).SimpleRingRatioConfig(); !ok {
		t.Fatal("SimpleRingRatio did not compile with its previous point a step back")
	}
	restart := mustCompileG4Request(t, compiler, steppedG4Request(t, DetectorKindOsRestart, map[string]any{}, "uptime_history"))
	if restart.Levels().Len() != 1 {
		t.Fatal("OsRestart did not compile with its previous point a step back")
	}
}
