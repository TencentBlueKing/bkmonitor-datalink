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

// Every term of the slack derivation and every boundary of the two gates that
// admit it, each read off a configuration that answers differently when that
// one term moves.
//
// The function is called directly, and this is the only place that does. Every
// other case reads the retention back out of StateRequirement, which shows that
// a number travels from the compiler to its readers and says nothing about
// which number it is: a build that granted one point to every Level, or that
// rounded the tolerance the other way, satisfies all of them.
//
// The configuration is what the table holds. requiredPoints is derived here the
// way the compiler derives it, so a row states a Level an operator could write
// rather than an intermediate already half-way through the arithmetic.
func TestRetentionSlackIsDerivedFromTheWalkAndItsTwoGates(t *testing.T) {
	for _, test := range []struct {
		name     string
		window   uint32
		required uint32
		recovery uint32
		disabled bool
		interval uint32
		want     uint32
		because  string
	}{
		{
			name: "short window and a day long recovery run", window: 30, required: 1, recovery: 1440, interval: 60,
			want:    39,
			because: "WindowSize 30 + tolerance 10 + 2 - 3*1",
		},
		{
			name: "window spanning exactly the tolerance", window: 3, required: 3, recovery: 8, interval: 60,
			want:    0,
			because: "required 10 positions at 60s is 600s, which the hole tolerance already covers",
		},
		{
			name: "slack that would come to the whole required window", window: 30, required: 1, recovery: 10, interval: 60,
			want:    0,
			because: "slack 39 equals the required 39, so letting the hole age out is the cheaper wait",
		},
		{
			name: "threshold high enough to absorb the holes", window: 5, required: 3, recovery: 2, interval: 600,
			want:    1,
			because: "the arithmetic comes to -1 and the floor is one spare position",
		},
		{
			// The floor's own boundary, which the row above cannot reach: an
			// arithmetic of -1 is lifted to one by a floor at zero just as it is
			// by a floor at one. Only a Level the derivation lands exactly on
			// zero separates them - and it is the case the floor exists for,
			// since a Level both gates admit that retains nothing would be
			// counted among those paying while paying nothing.
			name: "arithmetic landing exactly on zero", window: 3, required: 2, recovery: 2, interval: 600,
			want:    1,
			because: "3 + 1 + 2 - 6 is zero, and a Level both gates admit retains at least one spare position",
		},
		{
			name: "interval that does not divide the tolerance", window: 30, required: 1, recovery: 1440, interval: 90,
			want:    36,
			because: "600s is 6.67 positions at 90s and a hole occupies the seventh",
		},
		{
			name: "recovery disabled", window: 30, required: 1, recovery: 1440, disabled: true, interval: 60,
			want:    0,
			because: "no walk runs, so no position beyond the trigger window is ever read",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			trigger := TriggerPlan{WindowSize: test.window, RequiredAnomalies: test.required, StepSeconds: test.interval}
			recovery := RecoveryPlan{Enabled: !test.disabled, ConsecutiveWindows: test.recovery}
			requiredPoints := trigger.WindowSize
			if recovery.Enabled {
				requiredPoints += recovery.ConsecutiveWindows - 1
			}
			if got := retentionSlack(requiredPoints, trigger, recovery, test.interval); got != test.want {
				t.Fatalf("retentionSlack(required %d, window %d, threshold %d, run %d, interval %d) = %d, want %d: %s",
					requiredPoints, test.window, test.required, test.recovery, test.interval, got, test.want, test.because)
			}
		})
	}
}

// The pool is charged for the positions a Level retains, not for the ones its
// window requires.
//
// Both of the other cases that read StatePointsPerSeries are built on Levels
// whose window spans less than the hole tolerance, where the retained and the
// required counts are the same number and the two candidate implementations
// cannot be told apart. This is the figure the retained-byte pool of
// decision-019 is sized from, so a build charging the required count
// under-states every Level the slack reaches - and the deployment learns of it
// by running out of memory the accounting said it had.
func TestTheStatePoolIsChargedForTheRetainedPointsNotTheRequiredOnes(t *testing.T) {
	plan := validPlan()
	plan.StrategyIR.Levels[0].TriggerPlan.Config = json.RawMessage(`{"window_size":30,"required_anomalies":1,"step_seconds":60}`)
	plan.StrategyIR.Levels[0].RecoveryPlan.Config = json.RawMessage(`{"enabled":true,"consecutive_windows":1440}`)

	level := mustCompilePlan(t, newTestCompiler(t), plan).Levels()[0]
	requirement := level.StateRequirement()
	if requirement.RequiredDetectHistoryPoints != 1469 || requirement.RetentionPoints != 1508 {
		t.Fatalf("StateRequirement() = %+v, want 1469 required and 1508 retained: without two different "+
			"numbers this case cannot say which one the pool reads", requirement)
	}
	if got := level.ResourceEstimate().StatePointsPerSeries; got != uint64(requirement.RetentionPoints) {
		t.Fatalf("StatePointsPerSeries = %d, want the retained %d rather than the required %d",
			got, requirement.RetentionPoints, requirement.RequiredDetectHistoryPoints)
	}
}
