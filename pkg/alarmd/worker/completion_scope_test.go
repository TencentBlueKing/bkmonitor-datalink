// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A Slot whose primary query did not answer commits a completion that says
// where: the observation the page and the counter read carries the cause,
// the reason access gave the binding, and the query it was.
func TestAnUnavailableSlotSaysWhichQueryAndWhy(t *testing.T) {
	fixture := newFixture(t, false, "")
	for _, operation := range []execution.Operation{execution.OperationNormal, execution.OperationRetry} {
		if _, err := fixture.coordinator.Execute(context.Background(), slotRequest(operation)); err != nil {
			t.Fatalf("%s: %v", operation, err)
		}
	}
	var committed *observability.Observation
	for index := range *fixture.observations {
		if observation := (*fixture.observations)[index]; observation.Stage == observability.StageProgressCommitted &&
			observation.ProgressCompletionKind == string(execution.CompletionUnavailable) {
			committed = &(*fixture.observations)[index]
		}
	}
	if committed == nil {
		t.Fatalf("no unavailable completion was committed: %+v", *fixture.observations)
	}
	scope := committed.ProgressCompletionScope
	if committed.ProgressCompletionCause != string(execution.CausePrimaryInputUnavailable) || committed.ProgressCompletionReason != "QUERY_UNAVAILABLE" ||
		scope == nil || scope.StrategyID != "7" || scope.PhysicalQuery != "physical-query-1" {
		t.Fatalf("committed cause %q reason %q scope %+v, want the primary query unavailable, with access's reason and the query",
			committed.ProgressCompletionCause, committed.ProgressCompletionReason, scope)
	}
}
