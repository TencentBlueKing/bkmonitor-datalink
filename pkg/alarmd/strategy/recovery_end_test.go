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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// A recovery written to end closed, or ended by no data, carries that into
// the compiled plan and not into its canonical bytes - the bytes the trigger
// fingerprint and the Level's state compatibility hash - so its state does
// not move; an end or a missing-data mode outside the closed list is
// refused; a disabled recovery has neither.
func TestARecoveryPlanCarriesItsEndAndOneWrittenToRecoverKeepsItsBytes(t *testing.T) {
	typed := func(config string) contract.TypedPlanV1 {
		return contract.TypedPlanV1{Type: RecoveryPlanTypeContinuousTriggerMiss, Version: 1, Config: []byte(config)}
	}
	for _, test := range []struct {
		config, wantCanonical, wantEnd string
	}{
		{`{"enabled":true,"consecutive_windows":4}`, `{"consecutive_windows":4,"enabled":true}`, ""},
		{`{"enabled":true,"consecutive_windows":4,"end":"close"}`, `{"consecutive_windows":4,"enabled":true}`, contract.RecoveryEndClose},
		{`{"enabled":false,"consecutive_windows":4,"end":"close","missing_data":"evaluate_as_zero"}`, `{"consecutive_windows":0,"enabled":false}`, ""},
	} {
		plan, canonical, err := compileRecoveryPlan(typed(test.config))
		if err != nil {
			t.Fatalf("%s: compileRecoveryPlan error = %v", test.config, err)
		}
		if string(canonical.Config) != test.wantCanonical || plan.End != test.wantEnd {
			t.Fatalf("%s: canonical %s end %q, want %s and %q", test.config, canonical.Config, plan.End, test.wantCanonical, test.wantEnd)
		}
	}
	nodata, canonical, err := compileRecoveryPlan(typed(`{"enabled":true,"consecutive_windows":4,"end":"close","missing_data":"evaluate_as_zero"}`))
	if err != nil || nodata.MissingData != contract.RecoveryMissingDataEvaluateAsZero || nodata.End != contract.RecoveryEndClose ||
		string(canonical.Config) != `{"consecutive_windows":4,"enabled":true}` {
		t.Fatalf("close-nodata plan = %+v canonical %s err %v", nodata, canonical.Config, err)
	}
	for _, refused := range []string{`{"enabled":true,"consecutive_windows":4,"end":"resolve"}`, `{"enabled":true,"consecutive_windows":4,"end":"recovery"}`,
		`{"enabled":true,"consecutive_windows":4,"missing_data":"keep_state"}`} {
		if _, _, err := compileRecoveryPlan(typed(refused)); err == nil {
			t.Fatalf("%s: compiled, want an end outside the closed list refused", refused)
		}
	}
}

// Through the compiler: a Level written to end closed and by no data has the
// trigger fingerprint of the same Level written to recover, so switching the
// ending keeps its state; a change to its windows still moves it.
func TestHowARecoveryEndsDoesNotMoveTheTriggerFingerprint(t *testing.T) {
	compiler := newTestCompiler(t)
	fingerprint := func(recovery string) string {
		plan := validPlan()
		plan.StrategyIR.Levels[0].RecoveryPlan.Config = []byte(recovery)
		return mustCompilePlan(t, compiler, plan).Levels().At(0).Fingerprints().Trigger
	}
	plain := fingerprint(`{"enabled":true,"consecutive_windows":4}`)
	if ended := fingerprint(`{"enabled":true,"consecutive_windows":4,"end":"close","missing_data":"evaluate_as_zero"}`); ended != plain {
		t.Fatalf("trigger fingerprint %s with an ending, %s without, want the same", ended, plain)
	}
	if wider := fingerprint(`{"enabled":true,"consecutive_windows":5}`); wider == plain {
		t.Fatal("a wider recovery window kept the trigger fingerprint")
	}
}
