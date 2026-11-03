// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"context"
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func gatherFamily(t *testing.T, r *Recorder, name string) []*dto.Metric {
	t.Helper()
	families, err := r.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == name {
			return family.Metric
		}
	}
	return nil
}

// The three states a RECOVERY can be decided beside exist at zero before
// anything is observed, so a zero reads as "never happened" rather than "never
// exported", and each adds under its own label. The families this replaced,
// held and past-without-recovery, are gone: the gate no longer holds on
// another Level, and a family that could only read zero would be misread.
func TestRecoveryBesideLevelCounterStartsAtZeroAndAddsByState(t *testing.T) {
	const beside = "alarmd_trigger_recovery_beside_level_total"
	r := NewRecorder(BuildInfo{})

	for _, gone := range []string{"alarmd_trigger_recovery_held_total", "alarmd_trigger_recovery_past_level_without_recovery_total"} {
		if got := gatherFamily(t, r, gone); got != nil {
			t.Fatalf("%s is still exported: %v", gone, got)
		}
	}
	initial := gatherFamily(t, r, beside)
	if len(initial) != 3 {
		t.Fatalf("states before any observation = %d series, want all three created at zero", len(initial))
	}
	for _, m := range initial {
		if m.GetCounter().GetValue() != 0 || len(m.Label) != 1 || m.Label[0].GetName() != "beside" {
			t.Fatalf("state before any observation = %v, want beside=... 0", m)
		}
	}

	r.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentEvaluation, Stage: observability.StageEvaluationCompleted,
		RecoveryGates: []observability.RecoveryGateFact{
			{Cause: observability.RecoveryGateLevelUnavailable, Records: 2},
			{Cause: observability.RecoveryGateLevelRecovering, Records: 1},
			{Cause: observability.RecoveryGateLevelWithoutRecovery, Records: 3},
			{Cause: observability.RecoveryGateCause("qg-secret"), Records: 9},
		},
	})
	byState := map[string]float64{}
	for _, m := range gatherFamily(t, r, beside) {
		byState[m.Label[0].GetValue()] = m.GetCounter().GetValue()
	}
	want := map[string]float64{"level_unavailable": 2, "level_recovering": 1, "level_without_recovery": 3}
	if len(byState) != len(want) {
		t.Fatalf("beside by state = %v, want %v and nothing else", byState, want)
	}
	for state, value := range want {
		if byState[state] != value {
			t.Fatalf("beside by state = %v, want %v", byState, want)
		}
	}
}

// A withheld recovery is counted by its reason, which exists at zero before
// anything is observed; a reason outside the contract's list adds nothing.
func TestRecoveryWithheldCounterStartsAtZeroAndAddsByReason(t *testing.T) {
	const withheld = "alarmd_trigger_recovery_withheld_total"
	r := NewRecorder(BuildInfo{})
	initial := gatherFamily(t, r, withheld)
	if len(initial) != 2 {
		t.Fatalf("before any observation = %v, want both reasons at 0", initial)
	}
	for _, m := range initial {
		if m.GetCounter().GetValue() != 0 {
			t.Fatalf("before any observation = %v, want every reason at 0", initial)
		}
	}
	r.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentEvaluation, Stage: observability.StageEvaluationCompleted,
		RecoveryWithheld: []observability.RecoveryWithheldFact{
			{Reason: observability.RecoveryWithheldZeroCount, Levels: 3},
			{Reason: observability.RecoveryWithheldReason("quiet"), Levels: 9},
		},
	})
	byReason := map[string]float64{}
	for _, m := range gatherFamily(t, r, withheld) {
		byReason[m.Label[0].GetValue()] = m.GetCounter().GetValue()
	}
	if len(byReason) != 2 || byReason["zero_count"] != 3 || byReason["no_data_last_point_unknown"] != 0 {
		t.Fatalf("after one observation = %v, want zero_count at 3 and nothing else moved", byReason)
	}
}
