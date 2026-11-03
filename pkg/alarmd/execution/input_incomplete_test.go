// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The fold and the advance gate read one definition of an incomplete input.
// A Level the gate freezes and the fold cannot see is a Level nothing will
// guard, and the result contract then refuses it on every round; the one
// such input that occurs is a dependency that completed and holds nothing.
func TestTheFoldSeesEveryInputTheAdvanceGateFreezesOn(t *testing.T) {
	plan := PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	level := ConsumerRef{Plan: plan, LevelID: 1, HasLevel: true}
	primary := NamedInputBinding{Consumer: level, Role: InputRolePrimary,
		Completeness: CompletenessFull, DataState: DataStateData, Disposition: AccessAvailable}
	emptyDependency := NamedInputBinding{Consumer: level, Role: InputRoleAlgorithmDependency,
		Completeness: CompletenessFull, DataState: DataStateEmpty, Disposition: AccessAvailable}
	partialPrimary := primary
	partialPrimary.Completeness, partialPrimary.Disposition, partialPrimary.ReasonCode =
		CompletenessPartial, AccessDegraded, ReasonCode(contract.ReasonQueryPartial)

	if !InputIncompleteForGuard(emptyDependency) {
		t.Fatal("a dependency that completed empty is not seen as incomplete; the Level it starves gets no guard")
	}
	if got := InputGuardReason(emptyDependency); got != ReasonCode(contract.ReasonQueryEmpty) {
		t.Fatalf("guard reason for an empty dependency = %q, want %s", got, contract.ReasonQueryEmpty)
	}
	if InputIncompleteForGuard(primary) {
		t.Fatal("a FULL input with data is seen as incomplete")
	}
	// A PRIMARY that completed empty is a Plan with no series, decided by the
	// completion-only rule; guarding it would guard nothing.
	emptyPrimary := primary
	emptyPrimary.DataState = DataStateEmpty
	if InputIncompleteForGuard(emptyPrimary) {
		t.Fatal("a FULL EMPTY PRIMARY is seen as incomplete; the completion-only rule owns it")
	}

	// The gate and the fold agree on the empty dependency: the gate closes,
	// the fold names the guard.
	outcome := LevelOutcome{Plan: plan, LevelID: 1, Outcome: LevelOutcomeUnknown}
	if InputAllowsStateAdvance([]NamedInputBinding{primary, emptyDependency}, outcome) {
		t.Fatal("the advance gate opened on an empty dependency")
	}
	reasons := RoundGapScopeReasons([]NamedInputBinding{primary, emptyDependency}, plan)
	if got := reasons[GapScope{HasLevel: true, LevelID: 1}]; got != ReasonCode(contract.ReasonQueryEmpty) {
		t.Fatalf("round fold = %v, want the Level scope to carry %s", reasons, contract.ReasonQueryEmpty)
	}
	// Beside a PARTIAL input of the same scope the fold says PARTIAL: the
	// empty dependency asks the least protection of the ranked reasons.
	reasons = RoundGapScopeReasons([]NamedInputBinding{partialPrimary, emptyDependency}, plan)
	if got := reasons[GapScope{HasLevel: true, LevelID: 1}]; got != ReasonCode(contract.ReasonQueryPartial) {
		t.Fatalf("round fold beside a PARTIAL input = %v, want %s", reasons, contract.ReasonQueryPartial)
	}
	// And the reason is one the scope vocabulary names, so a marker carrying
	// it is counted under its own name rather than under other.
	named := false
	for _, reason := range contract.GapScopeReasons() {
		if reason == contract.ReasonQueryEmpty {
			named = true
		}
	}
	if !named {
		t.Fatalf("%s is not in GapScopeReasons(): %v", contract.ReasonQueryEmpty, contract.GapScopeReasons())
	}
}
