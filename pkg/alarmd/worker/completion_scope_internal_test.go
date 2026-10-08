// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The completion's observation carries where its cause was found as the
// derivation named it; a completion without a cause, or with a cause and no
// place, carries none.
func TestTheCommittedObservationCarriesWhereTheCauseWasFound(t *testing.T) {
	plan := execution.PlanIdentity{TenantID: "system", BusinessID: "2", StrategyID: "4101"}
	got := completionScopeFacts(execution.CompletionAttribution{Cause: execution.CausePrimaryInputUnavailable,
		Scope: execution.CompletionScope{Plan: plan, HasPlan: true, LevelID: 2, HasLevel: true, PhysicalQuery: "physical-query-1"}})
	if got == nil || *got != (observability.CompletionScopeFacts{TenantID: "system", BusinessID: "2", StrategyID: "4101", LevelID: 2,
		HasLevel: true, PhysicalQuery: "physical-query-1"}) {
		t.Fatalf("scope facts %+v, want the Plan, Level and query", got)
	}
	if got := completionScopeFacts(execution.CompletionAttribution{Scope: execution.CompletionScope{Plan: plan, HasPlan: true}}); got != nil {
		t.Fatalf("no cause, scope facts %+v", got)
	}
	if got := completionScopeFacts(execution.CompletionAttribution{Cause: execution.CauseConfigDrift}); got != nil {
		t.Fatalf("a cause with no place, scope facts %+v", got)
	}
}
