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

// Every way a preflight can end is a series from startup, a preflight counts
// once under its result and reason, a word outside the list counts under the
// fold, and the family is the cross product of the two lists and no more.
func TestStatePreflightsAreSeriesFromStartupAndCountByResultAndReason(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	const family = "bkmonitor_alarmd_state_preflight_total"
	before := gatherFamily(t, r, family)
	want := len(observability.StatePreflightResults) * len(observability.StatePreflightReasons)
	if len(before) != want {
		t.Fatalf("%d series before any preflight, want every cell (%d) so a read that never timed out reads as zero", len(before), want)
	}
	preflight := func(result observability.Result, reason observability.ReasonCode, err error) observability.Observation {
		return observability.Observation{
			Component: observability.ComponentState, Stage: observability.StageStatePreflight,
			Result: result, ReasonCode: reason, Err: err,
			Trace:  observability.TraceFields{QueryGroupKey: "qg-secret", EvaluationTime: 600},
			Counts: observability.Counts{Keys: 249, StateBytes: 85_855_443},
		}
	}
	ctx := context.Background()
	r.Observe(ctx, preflight(observability.ResultSuccess, observability.ReasonNone, nil))
	r.Observe(ctx, preflight(observability.ResultSuccess, observability.ReasonNone, nil))
	r.Observe(ctx, preflight(observability.ResultDegraded, observability.ReasonCode(contract.ReasonStateReadTimeout), nil))
	r.Observe(ctx, preflight(observability.ResultDegraded, observability.ReasonCode(contract.ReasonRedisUnavailable), nil))
	r.Observe(ctx, preflight(observability.ResultTerminal, observability.ReasonCode(contract.ReasonStateCorrupt), nil))
	r.Observe(ctx, preflight(observability.ResultFailed, observability.ReasonInternalUnknown, errors.New("store refused")))
	// A retryable word the read path does not use: counted, under the fold.
	r.Observe(ctx, preflight(observability.ResultDegraded, observability.ReasonCode(contract.ReasonStateWriteRetryable), nil))
	// A degraded result with no reason at all is a site that failed to
	// report; normalized to reason_not_reported, it is the fold here.
	r.Observe(ctx, preflight(observability.ResultDegraded, "", nil))
	count := func(result, reason string) float64 {
		return testutil.ToFloat64(r.phaseTwo.statePreflights.WithLabelValues(result, reason))
	}
	for _, check := range []struct {
		result, reason string
		want           float64
	}{
		{"success", "none", 2},
		{"degraded", contract.ReasonStateReadTimeout, 1},
		{"degraded", contract.ReasonRedisUnavailable, 1},
		{"terminal", contract.ReasonStateCorrupt, 1},
		{"failed", "internal_unknown", 1},
		{"degraded", "_other", 2},
	} {
		if got := count(check.result, check.reason); got != check.want {
			t.Fatalf("state_preflight_total{%s,%s} = %v, want %v", check.result, check.reason, got, check.want)
		}
	}
	after := gatherFamily(t, r, family)
	if len(after) != want {
		t.Fatalf("%d series after, want the cross product (%d) and nothing outside it", len(after), want)
	}
	for _, m := range after {
		for _, label := range m.Label {
			if label.GetValue() == "qg-secret" || label.GetValue() == contract.ReasonStateWriteRetryable {
				t.Fatalf("unbounded label on %v", m)
			}
		}
	}
	// And a state apply is not a preflight.
	r.Observe(ctx, observability.Observation{Component: observability.ComponentState, Stage: observability.StageStateApplied, Result: observability.ResultSuccess})
	if got := count("success", "none"); got != 2 {
		t.Fatalf("an apply counted as a preflight: %v", got)
	}
}
