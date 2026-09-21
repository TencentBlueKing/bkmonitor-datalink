// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

type diagnosticQueryError struct{}

func (diagnosticQueryError) Error() string { return "https://user:secret@example.test/?token=secret" }
func (diagnosticQueryError) QueryFailure() (string, string) {
	return "series_identity", "IDENTITY_FIELD_MISSING"
}

func TestQueryFailureBoundaryAndHealthySibling(t *testing.T) {
	for _, tc := range []struct{ name, stage, category, code string }{
		{"provider", "execute", "series_identity", "IDENTITY_FIELD_MISSING"},
		{"stream", "stream_complete", "other", "OTHER"},
		// The budget's own value is a metric label and the code grammar is
		// upper case. Taken from the one place that maps between them, so a
		// code that stops parsing cannot pass here by being copied twice.
		{"budget", "execute", "budget", observability.CapacityBudgetFailureCode(observability.CapacityBudgetSeries)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixtureWithBudget(t, worker.ProvisionalBudget{MaxSeries: 1, MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10})
			switch tc.name {
			case "provider":
				f.ports.queryAfterSeriesError = fmt.Errorf("wrapped: %w", diagnosticQueryError{})
			case "stream":
				f.ports.failStage = "gap_load"
			case "budget":
				f.ports.reverseStateReceipts = true
			}
			failed := slotRequest(execution.OperationNormal)
			failed.Contract.Slot.QueryGroup = "failed-query-group"
			failed.OwnerFence.QueryGroup = failed.Contract.Slot.QueryGroup
			result, err := f.coordinator.Execute(context.Background(), failed)
			if err == nil || result.Completed {
				t.Fatalf("failed result=%+v err=%v", result, err)
			}
			if f.ports.eventCount != 0 || f.ports.stateApplyCalls != 0 || !isZeroProgressCommit(f.ports.lastProgress) {
				t.Fatal("failed input reached committed effects")
			}
			var got *observability.QueryFailureFacts
			for _, o := range *f.observations {
				if o.Stage == observability.StageQueryCompleted {
					got = o.QueryFailure
				}
			}
			if got == nil || got.Stage != tc.stage || got.Category != tc.category || got.Code != tc.code {
				t.Fatalf("diagnostics=%+v want=%+v", got, tc)
			}
			f.ports.queryAfterSeriesError = nil
			f.ports.failStage = ""
			f.ports.reverseStateReceipts = false
			healthy := slotRequest(execution.OperationNormal)
			result, err = f.coordinator.Execute(context.Background(), healthy)
			if err != nil || !result.Completed || f.ports.lastProgress.Identity.QueryGroup != healthy.Contract.Slot.QueryGroup {
				t.Fatalf("healthy sibling result=%+v err=%v", result, err)
			}
		})
	}
}
