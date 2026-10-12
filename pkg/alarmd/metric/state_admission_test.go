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
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Every way an admission call can end is a series from startup, including
// STATE_BUDGET_EXCEEDED: a Plan refused at every round for its lifetime was
// on no counter, so a strategy that had stopped detecting moved nothing
// fleet-wide. A call counts once under its result and reason, a word outside
// the list under the fold, and the family is the cross product and no more.
func TestStateAdmissionsAreSeriesFromStartupAndCountByResultAndReason(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	const family = "bkmonitor_alarmd_state_admission_total"
	before := gatherFamily(t, r, family)
	want := len(observability.StateAdmissionResults) * len(observability.StateAdmissionReasons)
	if len(before) != want {
		t.Fatalf("%d series before any admission, want every cell (%d) so a refusal that never happened reads as zero", len(before), want)
	}
	admission := func(result observability.Result, reason observability.ReasonCode, err error) observability.Observation {
		return observability.Observation{
			Component: observability.ComponentState, Stage: observability.StageStateAdmission,
			Result: result, ReasonCode: reason, Err: err,
			Trace:  observability.TraceFields{QueryGroupKey: "qg-secret", StrategyID: "848", EvaluationTime: 600},
			Counts: observability.Counts{Keys: 14},
			StateApplyChunk: &observability.StateApplyChunkFacts{Count: 1, RefusalRules: []string{"lifetime_past_ceiling"},
				RefusalText: "required TTL 840h0m0s exceeds maximum 720h0m0s"},
		}
	}
	ctx := context.Background()
	budget := observability.ReasonCode(contract.ReasonStateBudgetExceeded)
	r.Observe(ctx, admission(observability.ResultSuccess, observability.ReasonNone, nil))
	r.Observe(ctx, admission(observability.ResultTerminal, budget, nil))
	r.Observe(ctx, admission(observability.ResultTerminal, budget, nil))
	r.Observe(ctx, admission(observability.ResultTerminal, observability.ReasonCode(contract.ReasonStateCorrupt), nil))
	r.Observe(ctx, admission(observability.ResultFailed, observability.ReasonInternalUnknown, errors.New("store did not answer")))
	// A word the admission does not use: counted, under the fold.
	r.Observe(ctx, admission(observability.ResultTerminal, observability.ReasonCode(contract.ReasonStateWriteRetryable), nil))
	// The preflight's stage is not an admission.
	preflight := admission(observability.ResultTerminal, budget, nil)
	preflight.Stage = observability.StageStatePreflight
	r.Observe(ctx, preflight)
	count := func(result, reason string) float64 {
		return testutil.ToFloat64(r.phaseTwo.stateAdmissions.WithLabelValues(result, reason))
	}
	for _, check := range []struct {
		result, reason string
		want           float64
	}{
		{"success", "none", 1},
		{"terminal", contract.ReasonStateBudgetExceeded, 2},
		{"terminal", contract.ReasonStateCorrupt, 1},
		{"failed", "internal_unknown", 1},
		{"terminal", "_other", 1},
	} {
		if got := count(check.result, check.reason); got != check.want {
			t.Fatalf("state_admission_total{%s,%s} = %v, want %v", check.result, check.reason, got, check.want)
		}
	}
	after := gatherFamily(t, r, family)
	if len(after) != want {
		t.Fatalf("%d series after, want the cross product (%d) and nothing outside it", len(after), want)
	}
	for _, m := range after {
		for _, label := range m.Label {
			if label.GetValue() == "qg-secret" || label.GetValue() == "848" || label.GetValue() == contract.ReasonStateWriteRetryable {
				t.Fatalf("unbounded label on %v", m)
			}
		}
	}
}
