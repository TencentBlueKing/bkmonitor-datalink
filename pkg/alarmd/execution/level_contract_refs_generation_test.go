// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Whatever moves a Plan's Level contract refs moves its state generation.
// A record is keyed by the generation and held to the refs; a change that
// moved the refs alone would meet the records under the same key with
// another contract on every build, with no formula skew to name it, and
// refuse every loaded record for good rather than for a rollout. Each input
// of the refs is changed here and both are asserted to move; an input added
// to the refs without being added to the generation fails this test.
func TestWhateverMovesTheContractRefsMovesTheGeneration(t *testing.T) {
	base := compiledPlanWithTriggerConfig(t, json.RawMessage(`{"window_size":30,"required_anomalies":5,"step_seconds":60}`))
	baseRefs, err := execution.DeriveRuntimeLevelContractRefs(base)
	if err != nil {
		t.Fatal(err)
	}
	for name, moved := range map[string]func(t *testing.T) (generation string, refs []execution.RuntimeLevelContractRef){
		"the state requirement (window)": func(t *testing.T) (string, []execution.RuntimeLevelContractRef) {
			plan := compiledPlanWithTriggerConfig(t, json.RawMessage(`{"window_size":31,"required_anomalies":5,"step_seconds":60}`))
			refs, err := execution.DeriveRuntimeLevelContractRefs(plan)
			if err != nil {
				t.Fatal(err)
			}
			return plan.StateCompatibilityHash(), refs
		},
		"the trigger fingerprint (required anomalies)": func(t *testing.T) (string, []execution.RuntimeLevelContractRef) {
			plan := compiledPlanWithTriggerConfig(t, json.RawMessage(`{"window_size":30,"required_anomalies":6,"step_seconds":60}`))
			refs, err := execution.DeriveRuntimeLevelContractRefs(plan)
			if err != nil {
				t.Fatal(err)
			}
			return plan.StateCompatibilityHash(), refs
		},
		"the detect fingerprint (threshold)": func(t *testing.T) (string, []execution.RuntimeLevelContractRef) {
			plan := compiledPlanWithDetectThreshold(t, "51")
			refs, err := execution.DeriveRuntimeLevelContractRefs(plan)
			if err != nil {
				t.Fatal(err)
			}
			return plan.StateCompatibilityHash(), refs
		},
	} {
		t.Run(name, func(t *testing.T) {
			generation, refs := moved(t)
			if reflect.DeepEqual(refs, baseRefs) {
				t.Fatalf("changing %s did not move the refs; this case does not exercise the coupling", name)
			}
			if generation == base.StateCompatibilityHash() {
				t.Fatalf("changing %s moved the refs and not the generation: every record under the unchanged key would be refused for good", name)
			}
		})
	}
	// And the refs are a function of those inputs alone: the same Plan
	// compiled twice derives the same refs, so a Leader and a Worker on one
	// build agree.
	again, err := execution.DeriveRuntimeLevelContractRefs(compiledPlanWithTriggerConfig(t, json.RawMessage(`{"window_size":30,"required_anomalies":5,"step_seconds":60}`)))
	if err != nil || !reflect.DeepEqual(again, baseRefs) {
		t.Fatalf("the same Plan derives other refs on a second compile: %+v vs %+v (%v)", again, baseRefs, err)
	}
	if len(baseRefs) != 1 || len(baseRefs[0].LevelStateCompatibility) != 64 || strings.Trim(baseRefs[0].LevelStateCompatibility, "0123456789abcdef") != "" {
		t.Fatalf("refs = %+v, want one canonical digest per Level", baseRefs)
	}
}
