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
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// A level refused because a check returned an error carries what the error
// said. The code and the path say where; the text says what - which key the
// strict decode met - and without it a level refused at level.trigger_plan had
// to be compiled again offline to find that out.
func TestALevelRefusedByAnErrorSaysWhatTheErrorSaid(t *testing.T) {
	tests := []struct {
		name, trigger, recovery, fieldPath, mention string
	}{
		{"a trigger key the decoder does not know", `{"window_size":5,"required_anomalies":3,"step_seconds":60,"cw_calendars":[]}`,
			`{"enabled":true,"consecutive_windows":3}`, "level.trigger_plan", "cw_calendars"},
		{"a recovery field of another type", `{"window_size":5,"required_anomalies":3,"step_seconds":60}`,
			`{"enabled":"yes","consecutive_windows":3}`, "level.recovery_plan", "enabled"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := validPlan()
			plan.StrategyIR.Levels[0].TriggerPlan.Config = json.RawMessage(test.trigger)
			plan.StrategyIR.Levels[0].RecoveryPlan.Config = json.RawMessage(test.recovery)
			terminals := mustCompileResult(t, newTestCompiler(t), plan).LevelTerminals()
			if len(terminals) != 1 || terminals[0].ReasonCode != contract.ReasonLevelInvalid || terminals[0].FieldPath != test.fieldPath {
				t.Fatalf("terminals = %#v, want one LEVEL_INVALID at %s", terminals, test.fieldPath)
			}
			if !strings.Contains(terminals[0].Detail, test.mention) {
				t.Fatalf("detail = %q, want the error's account naming %q", terminals[0].Detail, test.mention)
			}
		})
	}
	// A refusal decided without an error - a window past the deployment's
	// bound - has nothing more to say than its code and path.
	limits := testLimits()
	limits.MaxTriggerWindowSize = 4
	compiler, err := NewCompiler(NewDefaultAlgorithmCompilerRegistry(), limits)
	if err != nil {
		t.Fatal(err)
	}
	plan := validPlan()
	plan.StrategyIR.Levels[0].TriggerPlan.Config = json.RawMessage(`{"window_size":5,"required_anomalies":3,"step_seconds":60}`)
	terminals := mustCompileResult(t, compiler, plan).LevelTerminals()
	if len(terminals) != 1 || terminals[0].ReasonCode != contract.ReasonLevelBudgetExceeded || terminals[0].Detail != "" {
		t.Fatalf("terminals = %#v, want a budget refusal without text", terminals)
	}
}

// The same for an algorithm whose configuration its compiler refuses.
func TestALevelRefusedForItsAlgorithmConfigSaysWhatWasWrong(t *testing.T) {
	plan := validPlan()
	plan.StrategyIR.Levels[0] = validLevel(1, 1, "not-a-decimal")
	terminals := mustCompileResult(t, newTestCompiler(t), plan).LevelTerminals()
	if len(terminals) != 1 || terminals[0].ReasonCode != contract.ReasonLevelInvalid ||
		terminals[0].FieldPath != "level.detect_plan.algorithms" {
		t.Fatalf("terminals = %#v, want one LEVEL_INVALID at the algorithms", terminals)
	}
	if !strings.Contains(terminals[0].Detail, "not-a-decimal") && !strings.Contains(terminals[0].Detail, "threshold") {
		t.Fatalf("detail = %q, want the compiler's account of the threshold", terminals[0].Detail)
	}
}

// Plan-wide refusals decided by an error carry it too: a legacy output that
// does not validate, and a no-data level whose source level's trigger plan
// does not decode for its uptime.
func TestAPlanRefusedByAnErrorSaysWhatTheErrorSaid(t *testing.T) {
	plan := validPlan()
	plan.LegacyOutput = &contract.LegacyOutputContext{}
	terminal := mustCompileResult(t, newTestCompiler(t), plan).PlanTerminal()
	if terminal == nil || terminal.FieldPath != "legacy_output" || !strings.Contains(terminal.Detail, "legacy output") {
		t.Fatalf("plan terminal = %#v, want legacy_output with the validation's account", terminal)
	}

	plan = validPlan()
	plan.NoData = &contract.NoDataConfigV1{Continuous: 5, Level: 1}
	plan.StrategyIR.Levels[0].TriggerPlan.Config = json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60,"uptime":"everyday"}`)
	terminal = mustCompileResult(t, newTestCompiler(t), plan).PlanTerminal()
	if terminal == nil || !strings.HasPrefix(terminal.FieldPath, "strategy_ir.levels.trigger_plan") || terminal.Detail == "" {
		t.Fatalf("plan terminal = %#v, want the no-data level's uptime refused with the decoder's account", terminal)
	}
}
