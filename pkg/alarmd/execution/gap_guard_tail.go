// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

// PlanInputsWhole says whether every input of plan answered FULL and was
// available this round - the PRIMARY input and every dependency. It is the
// one judgement of "the Plan's input was whole": a round that meets it is one
// a gap guard's warmup counts, and one on which a Level still held by a guard
// is held only by the past - unless the round proposes a guard of its own.
//
// It can: a dependency that answered FULL with no rows is whole here, and on
// a round with no series that is all a warmup needs, but a series that needed
// it gets a QUERY_EMPTY guard from this round (InputIncompleteForGuard). The
// evaluator knows which, and says so on the outcome (LevelOutcome.GuardTail);
// the inputs alone look whole.
func PlanInputsWhole(inputs []NamedInputBinding, plan PlanIdentity) bool {
	for _, binding := range inputs {
		if binding.Consumer.Plan == plan && !inputWhole(binding) {
			return false
		}
	}
	return true
}

func inputWhole(binding NamedInputBinding) bool {
	return binding.Completeness == CompletenessFull && binding.Disposition == AccessAvailable
}

// SlotInputWholeness answers PlanInputsWhole for every Plan of one Slot from
// a single pass over its inputs, for a caller that asks once per outcome.
//
// A Slot's inputs are one binding per series, Level and requirement, and so
// are its outcomes: asking PlanInputsWhole per outcome is series squared, in
// exactly the Slot that asks most - a guard warming holds every Level of every
// series. The pass runs on the first question, so a Slot that asks none pays
// nothing.
type SlotInputWholeness struct {
	inputs   []NamedInputBinding
	notWhole map[PlanIdentity]struct{}
}

// NewSlotInputWholeness reads inputs only when first asked.
func NewSlotInputWholeness(inputs []NamedInputBinding) *SlotInputWholeness {
	return &SlotInputWholeness{inputs: inputs}
}

func (wholeness *SlotInputWholeness) planWhole(plan PlanIdentity) bool {
	if wholeness.notWhole == nil {
		wholeness.notWhole = make(map[PlanIdentity]struct{})
		for _, binding := range wholeness.inputs {
			if !inputWhole(binding) {
				wholeness.notWhole[binding.Consumer.Plan] = struct{}{}
			}
		}
	}
	_, found := wholeness.notWhole[plan]
	return !found
}

// UnknownIsGuardTail says whether an UNKNOWN Level outcome is only the tail of
// an earlier gap: a standing guard held the Level for a reason an earlier
// round left (outcome.GuardTail), and every input of the outcome's Plan
// answered whole this round. Then nothing about this round is unknown; the
// Level waits for the guard's warmup, and the reason it carries is why the
// guard was opened, not what happened now.
//
// An outcome whose Plan had an incomplete input this round is not a tail,
// even under a guard: whatever the guard's reason, this round has a failure
// of its own, and that is what the Slot reports.
func (wholeness *SlotInputWholeness) UnknownIsGuardTail(outcome LevelOutcome) bool {
	return outcome.Outcome == LevelOutcomeUnknown && outcome.GuardTail && wholeness.planWhole(outcome.Plan)
}
