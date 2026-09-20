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

// The two causes a RECOVERY envelope is held for exist at zero before anything
// is held, so a zero can be read as "never held" rather than "never exported";
// a held record adds under its cause, and a record sent past a Level without
// recovery adds to its own counter and not to a held cause.
func TestRecoveryGateCountersStartAtZeroAndAddByCause(t *testing.T) {
	const held = "bkmonitor_alarmd_trigger_recovery_held_total"
	const passed = "bkmonitor_alarmd_trigger_recovery_past_level_without_recovery_total"
	r := NewRecorder(BuildInfo{})

	initial := gatherFamily(t, r, held)
	if len(initial) != 2 {
		t.Fatalf("held causes before any observation = %d series, want both created at zero", len(initial))
	}
	for _, m := range initial {
		if m.GetCounter().GetValue() != 0 || len(m.Label) != 1 || m.Label[0].GetName() != "cause" {
			t.Fatalf("held cause before any observation = %v, want cause=... 0", m)
		}
	}
	if got := gatherFamily(t, r, passed); len(got) != 1 || got[0].GetCounter().GetValue() != 0 {
		t.Fatalf("passed counter before any observation = %v, want one series at zero", got)
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
	byCause := map[string]float64{}
	for _, m := range gatherFamily(t, r, held) {
		byCause[m.Label[0].GetValue()] = m.GetCounter().GetValue()
	}
	if len(byCause) != 2 || byCause["level_unavailable"] != 2 || byCause["level_recovering"] != 1 {
		t.Fatalf("held by cause = %v, want level_unavailable 2 and level_recovering 1 and nothing else", byCause)
	}
	if got := gatherFamily(t, r, passed); len(got) != 1 || got[0].GetCounter().GetValue() != 3 {
		t.Fatalf("passed counter = %v, want 3", got)
	}
}
