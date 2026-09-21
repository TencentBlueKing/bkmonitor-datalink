// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution_test

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Every reason a finalization can put on a gap scope is a reason the gap scope
// vocabulary names.
//
// The vocabulary derives itself from the reason catalogue's query-result
// domain, which is the right source for the reasons a scope folds out of its
// incomplete inputs and says nothing at all about the other producer: a Slot
// that ran no query has no inputs to fold and writes its mode's own reason
// straight onto the scope. Those two reasons went unnamed, and the counter
// that was supposed to make an unnamed reason visible put 46.6% of its
// population into "other" instead -- a reading everybody could see and nobody
// could act on.
//
// The scan is over every mode rather than over the two that were missing. The
// next mode is the one nobody will remember to add a line for, and its symptom
// would be the same percentage moving quietly.
func TestEveryFinalizationModesGapScopeReasonIsNamedByTheVocabulary(t *testing.T) {
	contractRef := frozenContract()
	targets := execution.FrozenDuePlanTargets{
		DuePlanSetDigest: contractRef.DuePlanSetDigest,
		Plans:            []execution.PlanIdentity{{TenantID: "tenant", BusinessID: "2", StrategyID: "9"}},
	}
	request := execution.SlotExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1,
		DuePlanTargets: targets,
	}
	named := make(map[string]struct{}, len(contract.GapScopeReasons()))
	for _, reason := range contract.GapScopeReasons() {
		named[reason] = struct{}{}
	}
	if len(execution.FinalizationModes) == 0 {
		t.Fatal("no finalization modes are published; this scan is checking nothing")
	}
	scanned := 0
	for _, mode := range execution.FinalizationModes {
		required, carriesTargets := requiredFinalizationReason(t, request, targets, mode)
		if !carriesTargets {
			// This mode finalizes no Plans, so it writes no gap scope and its
			// reason must not be in the vocabulary either: a label nothing can
			// write is a zero that reads like a measurement.
			if _, listed := named[string(required)]; listed && required != "" {
				t.Errorf("mode %s finalizes no Plans, so it writes no gap scope, yet the vocabulary "+
					"names its reason %q; that label can only ever read zero", mode, required)
			}
			continue
		}
		scanned++
		if _, listed := named[string(required)]; !listed {
			t.Errorf("mode %s finalizes Plans and writes %q onto their gap scopes, and the vocabulary "+
				"does not name it. Every round in that mode lands in %q, which is the label that is "+
				"supposed to mean somebody added a reason without naming it",
				mode, required, contract.GapScopeReasonOther)
		}
		if got := contract.NormalizeGapScopeReason(string(required)); got != string(required) {
			t.Errorf("NormalizeGapScopeReason(%q) = %q for mode %s", required, got, mode)
		}
	}
	if scanned == 0 {
		t.Fatal("no finalization mode was found to finalize Plans; the discriminator below is wrong " +
			"and this test would pass against any vocabulary")
	}
}

// requiredFinalizationReason asks Validate, rather than a second copy of the
// rule, which reason a mode requires and whether it finalizes any Plans.
//
// Both answers come from the contract's own validator: the reason is the one
// value out of the whole catalogue it accepts, and a mode finalizes Plans when
// it accepts a finalization carrying them. Restating either here would make
// this test agree with a table instead of with the code that writes the
// markers.
func requiredFinalizationReason(
	t *testing.T,
	request execution.SlotExecutionRequest,
	targets execution.FrozenDuePlanTargets,
	mode execution.FinalizationMode,
) (execution.ReasonCode, bool) {
	t.Helper()
	candidates := make([]execution.ReasonCode, 0, len(contract.ReasonCatalogV2())+1)
	candidates = append(candidates, "")
	for _, definition := range contract.ReasonCatalogV2() {
		candidates = append(candidates, execution.ReasonCode(definition.Code))
	}
	accepted := make([]execution.ReasonCode, 0, 1)
	carriesTargets := false
	for _, candidate := range candidates {
		withTargets := execution.QueryFreeFinalization{
			Contract: request.Contract, Mode: mode, ReasonCode: candidate, Targets: targets,
		}
		without := execution.QueryFreeFinalization{
			Contract: request.Contract, Mode: mode, ReasonCode: candidate,
		}
		switch {
		case withTargets.Validate(request) == nil:
			accepted, carriesTargets = append(accepted, candidate), true
		case without.Validate(request) == nil:
			accepted = append(accepted, candidate)
		}
	}
	if len(accepted) == 0 {
		t.Fatalf("mode %s accepts no reason at all; it cannot be produced and should not be published", mode)
	}
	return accepted[0], carriesTargets
}
