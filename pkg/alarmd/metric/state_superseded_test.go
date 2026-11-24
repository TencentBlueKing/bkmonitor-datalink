// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// alarmd_state_superseded_total counts each skip site's count under the site
// and the Slot's operation, every pair from the first scrape; a completion
// that carries the Slot's total is not counted again, and an operation that
// is not a Slot's folds into other.
func TestStateSupersededCountsBySiteAndOperation(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	const family = "alarmd_state_superseded_total"
	for _, site := range []string{"state", "gap", "other"} {
		for _, operation := range []string{"normal", "retry", "replay", "probe", string(observability.OperationOther)} {
			if samples := redisSamples(t, recorder, family, map[string]string{"site": site, "operation": operation}); len(samples) != 1 {
				t.Fatalf("%s/%s has %d series before anything was skipped, want one at zero", site, operation, len(samples))
			}
		}
	}
	ctx := context.Background()
	skip := func(site observability.StateSupersededSite, operation observability.Operation, count int64) {
		recorder.Observe(ctx, observability.Observation{Component: observability.ComponentState, Stage: observability.StageMutationCompared,
			Result: observability.ResultSuccess, Operation: operation,
			StateSuperseded: &observability.StateSupersededFacts{Site: site, Count: count}})
	}
	skip(observability.StateSupersededAtState, observability.OperationNormal, 1)
	skip(observability.StateSupersededAtState, observability.OperationNormal, 1)
	skip(observability.StateSupersededAtGap, observability.OperationReplay, 3)
	skip(observability.StateSupersededAtState, observability.OperationSupplement, 1)
	skip(observability.StateSupersededAtGap, observability.OperationProbe, 0)
	// A count below zero cannot come from a skip; it is not added, where a
	// counter would panic taking it.
	skip(observability.StateSupersededAtGap, observability.OperationRetry, -1)
	// The completion line's total is the same skips again.
	recorder.Observe(ctx, observability.Observation{Component: observability.ComponentScheduler, Stage: observability.StageSlotCompleted,
		Result: observability.ResultSuccess, Operation: observability.OperationNormal,
		SlotSuperseded: &observability.SlotSupersededFacts{Series: 2}})
	for _, want := range []struct {
		site, operation string
		count           float64
	}{
		{"state", "normal", 2}, {"gap", "replay", 3}, {"state", string(observability.OperationOther), 1},
		{"gap", "probe", 0}, {"gap", "retry", 0}, {"state", "replay", 0}, {"gap", "normal", 0},
	} {
		if got := redisCounterSum(t, recorder, family, map[string]string{"site": want.site, "operation": want.operation}); got != want.count {
			t.Errorf("%s{site=%s,operation=%s} = %v, want %v", family, want.site, want.operation, got, want.count)
		}
	}
}
