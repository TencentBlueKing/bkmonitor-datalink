// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"

// RoundGapScopeReasons is the reason each gap scope takes from one round's
// incomplete named inputs: the fold of the reasons of that scope's own inputs.
//
// It lives here because two places need the same answer and the result
// contract compares them for equality. The guard a round proposes carries it,
// and the reason an UNKNOWN Level outcome carries under that guard is the same
// value -- so one function, called from both, is what makes the comparison
// hold by construction rather than by two derivations happening to meet. They
// did not meet: the guard took this round's inputs and the outcome took the
// reason off the marker already stored, and a round that added an input to an
// already guarded Level made the two disagree on every Slot.
//
// Only this round's inputs. The stored marker's own reason is deliberately not
// folded in: it is not in the fold order, unranked reasons outrank ranked ones
// by design, so a marker's first reason would win every fold for ever and the
// marker would never again say why it was last held.
func RoundGapScopeReasons(bindings []NamedInputBinding, plan PlanIdentity) map[GapScope]ReasonCode {
	members := make(map[GapScope][]string)
	for _, binding := range bindings {
		if binding.Consumer.Plan != plan || !InputIncompleteForGuard(binding) {
			continue
		}
		scope := GapScope{LevelID: binding.Consumer.LevelID, HasLevel: binding.Consumer.HasLevel}
		members[scope] = append(members[scope], string(InputGuardReason(binding)))
	}
	reasons := make(map[GapScope]ReasonCode, len(members))
	for scope, folded := range members {
		// The reason this is a fold and not a choice is what the refusal cost.
		// Two inputs of one Level failing differently -- one QUERY_UNAVAILABLE,
		// one QUERY_TIMEOUT, which a backend outage produces on every round --
		// left every candidate reason unable to satisfy both comparisons, and
		// the Plan's whole evaluation was refused for as long as that lasted.
		reasons[scope] = ReasonCode(contract.FoldGapReason(folded))
	}
	return reasons
}

// InputIncompleteForGuard is the one definition of an input the round has to
// guard against: the fold that names the guard and the worker that proposes it
// both read this, so what one guards the other names.
//
// It used to be "not FULL", and the advance gate -- InputAllowsStateAdvance --
// read more than that: FULL, carrying data, available, not localized. A Level
// in the difference was frozen by the gate and invisible to the fold, so no
// guard was ever proposed for it and its degraded outcome carried a local
// reason no marker matched. The result contract refused that Level on every
// round for as long as the input stayed that way, which for a dependency
// query that returns no rows is indefinitely. The difference that occurs is
// exactly that one: a dependency that completed and holds nothing. A PRIMARY
// that completed and holds nothing is a Plan with no series to guard and is
// decided elsewhere, so it is not here.
func InputIncompleteForGuard(binding NamedInputBinding) bool {
	if binding.Completeness != CompletenessFull {
		return true
	}
	return binding.Role == InputRoleAlgorithmDependency && binding.DataState == DataStateEmpty &&
		binding.Disposition == AccessAvailable
}

// InputGuardReason is the reason a guard for an incomplete input carries. An
// input that did not complete says why itself; a dependency that completed
// empty succeeded and has no reason of its own, so the guard says what it is.
func InputGuardReason(binding NamedInputBinding) ReasonCode {
	if binding.Completeness == CompletenessFull {
		return ReasonCode(contract.ReasonQueryEmpty)
	}
	return binding.ReasonCode
}

// RoundGuardReasonForLevel is the reason the guard this round proposes will
// carry for one Level, and nothing when this round proposes none for it.
//
// The Level's own scope first, then the Plan's. That is the order the result
// contract accepts a guard in -- either scope's marker may cover a Level's
// outcome -- so reading them in the same order is what keeps the outcome's
// reason equal to the reason of the marker that ends up covering it.
func RoundGuardReasonForLevel(
	bindings []NamedInputBinding, plan PlanIdentity, levelID uint32,
) (ReasonCode, bool) {
	reasons := RoundGapScopeReasons(bindings, plan)
	if reason, found := reasons[GapScope{HasLevel: true, LevelID: levelID}]; found {
		return reason, true
	}
	reason, found := reasons[GapScope{}]
	return reason, found
}
