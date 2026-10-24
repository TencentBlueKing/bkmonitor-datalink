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
	"errors"
	"math"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// ReasonOrigin is where a stored guard reason came from: the Slot that first
// stamped the guard (its evaluation time) and the reason that round stamped
// it with - a physical QUERY_TIMEOUT in round S, a gap marker a GAP_SKIPPED
// opened at S.
//
// A guard's reason outlives the round that raised it: every round the guard
// holds a Level, the Level's UNKNOWN outcome carries that reason again. Read
// on a later round, the word alone cannot be told apart from the same failure
// happening now; the origin is what separates them.
//
// It is written once, when the guard is first stamped, and copied unchanged
// while the guard is carried. A guard stamped by a build that did not write
// it, or carried from one, has none, and none is reported: nothing invents
// one later.
type ReasonOrigin struct {
	Slot  EvaluationTime
	Cause ReasonCode
}

// Known says the origin was recorded.
func (origin ReasonOrigin) Known() bool {
	return origin.Slot > 0 && origin.Cause != ""
}

// StampReasonOrigin is the origin a guard first stamped at slot with reason
// carries; zero when either is missing.
func StampReasonOrigin(slot EvaluationTime, reason ReasonCode) ReasonOrigin {
	if slot <= 0 || reason == "" {
		return ReasonOrigin{}
	}
	return ReasonOrigin{Slot: slot, Cause: reason}
}

// CarriedReasonFacts is a carried reason's origin as a line carries it; nil
// when the reason was not carried. One construction for every line that
// reports one: query_completed, evaluation_completed, slot_completed and
// progress_committed.
func CarriedReasonFacts(carried bool, origin ReasonOrigin) *observability.CarriedReasonFacts {
	if !carried {
		return nil
	}
	facts := &observability.CarriedReasonFacts{}
	if origin.Known() {
		facts.OriginSlot, facts.OriginCause = int64(origin.Slot), observability.ReasonCode(origin.Cause)
	}
	return facts
}

// CarriedReason is where the completion's reason came from when it was
// carried, for the completion lines; nil when there is no cause or the
// reason was the round's own.
func (cause SlotCompletionCause) CarriedReason() *observability.CarriedReasonFacts {
	return CarriedReasonFacts(cause.Cause != "" && cause.ReasonCarried, cause.ReasonOrigin)
}

// packedOriginSlot is origin as a per-series record stores it next to
// reason, the reason it is carried with: its Slot alone, the cause being
// that reason. Zero - no origin - for an origin that is not known, is not
// reason's, or whose Slot a uint32 cannot hold.
func packedOriginSlot(origin ReasonOrigin, reason ReasonCode) uint32 {
	if !origin.Known() || origin.Cause != reason || origin.Slot > math.MaxUint32 {
		return 0
	}
	return uint32(origin.Slot)
}

// unpackedOrigin is the origin a per-series record stored as slot next to
// reason. A Slot with no reason beside it comes back half, which the State
// contract refuses (validateReasonOrigin).
func unpackedOrigin(slot uint32, reason ReasonCode) ReasonOrigin {
	if slot == 0 {
		return ReasonOrigin{}
	}
	return ReasonOrigin{Slot: EvaluationTime(slot), Cause: reason}
}

// ReasonOrigin is the origin of the Level's stored guard reason.
func (level RuntimeLevelStateView) ReasonOrigin() ReasonOrigin {
	return unpackedOrigin(level.GapReasonOriginSlot, level.GapReasonCode)
}

// ReasonOrigin is the origin the mutation writes for the Level's guard reason.
func (level RuntimeLevelStateMutation) ReasonOrigin() ReasonOrigin {
	return unpackedOrigin(level.GapReasonOriginSlot, level.GapReasonCode)
}

// WithReasonOrigin is the mutation carrying origin for its guard reason. Set
// it after GapReasonCode: an origin that is not that reason's is not stored.
func (level RuntimeLevelStateMutation) WithReasonOrigin(origin ReasonOrigin) RuntimeLevelStateMutation {
	level.GapReasonOriginSlot = packedOriginSlot(origin, level.GapReasonCode)
	return level
}

// ReasonOrigin is the origin of the scope's reason: the Slot that opened it.
func (scope GapScopeState) ReasonOrigin() ReasonOrigin {
	return ReasonOrigin{Slot: scope.ReasonOriginSlot, Cause: scope.ReasonOriginCause}
}

// StampGapScopeOrigins writes the origin of every scope a gap write opened or
// re-opened under a new reason: the Slot of the write (the evaluation time on
// its apply version) and the scope's reason. A scope already held for the
// same reason keeps the origin it has - the Slot that opened it - and so does
// a warming scope, which nothing re-opens. A scope this write does not name
// is left as it is.
//
// The store calls it on the scopes it is about to write, whichever path
// produced them, so a new marker, a strengthened one and a same-Slot
// extension all stamp by one rule.
func StampGapScopeOrigins(previous, next []GapScopeState, mutation PlanGapMutation) []GapScopeState {
	opened := make(map[GapScope]struct{}, len(mutation.Scopes))
	for _, scope := range mutation.Scopes {
		if scope.Kind == GapOpen || scope.Kind == GapStrengthen {
			opened[scope.Scope] = struct{}{}
		}
	}
	held := make(map[GapScope]GapScopeState, len(previous))
	for _, scope := range previous {
		held[scope.Scope] = scope
	}
	for index := range next {
		scope := &next[index]
		before, existed := held[scope.Scope]
		if existed && before.ReasonCode == scope.ReasonCode {
			// Carried: the guard was already up for this reason.
			scope.ReasonOriginSlot, scope.ReasonOriginCause = before.ReasonOriginSlot, before.ReasonOriginCause
			continue
		}
		if _, stamped := opened[scope.Scope]; !stamped {
			// Not opened by this write and no longer the reason it was
			// opened for: whatever origin it held is not this reason's.
			scope.ReasonOriginSlot, scope.ReasonOriginCause = 0, ""
			continue
		}
		origin := StampReasonOrigin(mutation.ApplyVersion.EvaluationTime, scope.ReasonCode)
		scope.ReasonOriginSlot, scope.ReasonOriginCause = origin.Slot, origin.Cause
	}
	return next
}

// validateReasonOrigin refuses an origin written half, or on a Level that
// holds no guard.
func validateReasonOrigin(origin ReasonOrigin, completeness HistoryCompleteness) error {
	if origin == (ReasonOrigin{}) {
		return nil
	}
	if !origin.Known() {
		return errors.New("alarmd execution: State mutation Level reason origin is incomplete")
	}
	if completeness != HistoryWarming && completeness != HistoryGapped {
		return errors.New("alarmd execution: State mutation Level carries a reason origin without a guard")
	}
	return ValidateResultReason(observability.ResultDegraded, origin.Cause)
}
