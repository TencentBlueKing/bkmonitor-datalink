// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"context"
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

type FinalizationMode string

const (
	FinalizationQueryRequired       FinalizationMode = "QUERY_REQUIRED"
	FinalizationSnapshotRetry       FinalizationMode = "SNAPSHOT_RETRY"
	FinalizationExactSetBlocked     FinalizationMode = "EXACT_SET_BLOCKED"
	FinalizationSnapshotUnavailable FinalizationMode = "SNAPSHOT_UNAVAILABLE"
	FinalizationGapSkipped          FinalizationMode = "GAP_SKIPPED"
)

// FinalizationModes is every mode a finalization can be in.
//
// Published so a test can scan all of them rather than the ones somebody
// remembered. The scan that needs it is the one on the gap scope reason
// vocabulary: a mode that carries Plan targets writes its reason straight onto
// a gap scope, so the vocabulary has to know it, and a list of "the modes that
// do that" maintained by hand is a list the next mode is missing from.
var FinalizationModes = []FinalizationMode{
	FinalizationQueryRequired,
	FinalizationSnapshotRetry,
	FinalizationExactSetBlocked,
	FinalizationSnapshotUnavailable,
	FinalizationGapSkipped,
}

// QueryFreeGapScopeReason is the reason a finalization mode puts on the gap
// scopes of the Plans it finalizes, for the modes that finalize any.
//
// One function rather than a branch inside Validate and a list beside the
// metric. The reason a mode requires and the reason the metric has to name are
// the same fact, and the first time they were derived separately the metric
// read 46.6% "other" while every Validate call passed.
func QueryFreeGapScopeReason(mode FinalizationMode) (ReasonCode, bool) {
	switch mode {
	case FinalizationSnapshotUnavailable:
		return ReasonCode(contract.ReasonSnapshotUnavailable), true
	case FinalizationGapSkipped:
		return ReasonCode(contract.ReasonGapSkipped), true
	default:
		return "", false
	}
}

const ReasonBlockedExactSetUnavailable ReasonCode = ReasonCode(contract.ReasonBlockedExactSetUnavailable)

// FrozenDuePlanTargets is the recoverable identity projection of the frozen
// due Plan set. Its digest must be the one already bound by the Slot contract.
type FrozenDuePlanTargets struct {
	DuePlanSetDigest DuePlanSetDigest
	// Plans are keyed by strategy and piece. A key of an unsplit Plan
	// serializes as the identity did, see PlanKey.
	Plans []PlanKey
}

func (targets FrozenDuePlanTargets) Clone() FrozenDuePlanTargets {
	return FrozenDuePlanTargets{
		DuePlanSetDigest: targets.DuePlanSetDigest,
		Plans:            append([]PlanKey(nil), targets.Plans...),
	}
}

func (targets FrozenDuePlanTargets) Validate(contractRef FrozenExecutionContractRef) error {
	if targets.DuePlanSetDigest != contractRef.DuePlanSetDigest || len(targets.Plans) == 0 {
		return errors.New("alarmd execution: frozen due Plan targets do not match the Slot contract")
	}
	seen := make(map[PlanKey]struct{}, len(targets.Plans))
	for _, plan := range targets.Plans {
		if err := plan.Validate(); err != nil {
			return err
		}
		if _, duplicate := seen[plan]; duplicate {
			return errors.New("alarmd execution: duplicate frozen due Plan target")
		}
		seen[plan] = struct{}{}
	}
	return nil
}

func (targets FrozenDuePlanTargets) Equal(other FrozenDuePlanTargets) bool {
	if targets.DuePlanSetDigest != other.DuePlanSetDigest || len(targets.Plans) != len(other.Plans) {
		return false
	}
	wanted := make(map[PlanKey]struct{}, len(targets.Plans))
	for _, plan := range targets.Plans {
		wanted[plan] = struct{}{}
	}
	for _, plan := range other.Plans {
		if _, ok := wanted[plan]; !ok {
			return false
		}
	}
	return true
}

// QueryFreeFinalization decides whether Execute follows the normal query path
// or the bounded GAP_SKIPPED/SNAPSHOT_UNAVAILABLE finalization path.
type QueryFreeFinalization struct {
	Contract   FrozenExecutionContractRef
	Mode       FinalizationMode
	ReasonCode ReasonCode
	Targets    FrozenDuePlanTargets
}

func (finalization QueryFreeFinalization) Validate(request SlotExecutionRequest) error {
	if finalization.Contract != request.Contract {
		return errors.New("alarmd execution: finalization changed frozen contract")
	}
	switch finalization.Mode {
	case FinalizationSnapshotRetry:
		if finalization.ReasonCode != ReasonCode(contract.ReasonSnapshotRetryPending) ||
			finalization.Targets.DuePlanSetDigest != "" || len(finalization.Targets.Plans) != 0 {
			return errors.New("alarmd execution: Snapshot retry finalization is invalid")
		}
		return nil
	case FinalizationExactSetBlocked:
		if finalization.ReasonCode != ReasonBlockedExactSetUnavailable ||
			finalization.Targets.DuePlanSetDigest != "" || len(finalization.Targets.Plans) != 0 {
			return errors.New("alarmd execution: exact-set block finalization is invalid")
		}
		return nil
	case FinalizationQueryRequired:
		if finalization.ReasonCode != "" && finalization.ReasonCode != observability.ReasonNone {
			return errors.New("alarmd execution: query-required finalization carries a reason")
		}
		if finalization.Targets.DuePlanSetDigest != "" || len(finalization.Targets.Plans) != 0 {
			return errors.New("alarmd execution: query-required finalization carries query-free targets")
		}
		return nil
	case FinalizationSnapshotUnavailable, FinalizationGapSkipped:
		expectedReason, _ := QueryFreeGapScopeReason(finalization.Mode)
		if finalization.ReasonCode != expectedReason {
			return errors.New("alarmd execution: query-free finalization requires its exact reason")
		}
		if err := finalization.Targets.Validate(request.Contract); err != nil {
			return err
		}
		if !finalization.Targets.Equal(request.DuePlanTargets) {
			return errors.New("alarmd execution: query-free targets differ from the request frozen due Plan exact-set")
		}
		return nil
	default:
		return errors.New("alarmd execution: invalid finalization mode")
	}
}

type QueryFreeFinalizationSource interface {
	ResolveFinalization(context.Context, SlotExecutionRequest) (QueryFreeFinalization, error)
}

type ActivationSelection string

const (
	ActivationNone    ActivationSelection = "NONE"
	ActivationCurrent ActivationSelection = "CURRENT"
	ActivationPending ActivationSelection = "PENDING"
)

// ActivatedPlan contains only the current facts required to protect future
// side effects. It is not a replacement for the missing frozen Snapshot.
type ActivatedPlan struct {
	Identity          PlanIdentity
	StateGeneration   StateGeneration
	StateApplyEpoch   StateApplyEpoch
	ScheduleRevision  PlanScheduleRevision
	RequiredFullSlots uint32
	ForceWarming      bool
	// Shard is the piece of a split strategy this activation is for, nil for
	// a Plan that is not split. Activation records are persisted and compared
	// by their bytes, so it is a pointer omitted when nil: every record of an
	// unsplit Plan serializes exactly as before. Compare it with ShardsEqual,
	// never with ==.
	Shard *ShardRef `json:",omitempty"`
	// Carry names, on an activation whose state generation moved while the
	// Plan stayed active, the generation it moved from and the Levels whose
	// detection did not change with it: their stored results are the same
	// facts under the new generation, and are carried over rather than
	// warmed up again. Nil everywhere else, and omitted, so a record without
	// it serializes exactly as before. It only narrows what warms up; the
	// Worker still keeps a carried point only when its detect fingerprint is
	// the new one.
	Carry *StateCarry `json:",omitempty"`
}

// StateCarry is where an activation's Runtime State may be carried from.
type StateCarry struct {
	From   StateGeneration
	Levels []uint32
}

// Validate holds a carry to the activation it is on: it comes from another
// generation, only on an activation that warms up, and names each Level once,
// in order.
func (carry *StateCarry) Validate(plan ActivatedPlan) error {
	if carry == nil {
		return nil
	}
	if carry.From == "" || carry.From == plan.StateGeneration || !plan.ForceWarming {
		return errors.New("alarmd execution: a state carry needs a previous generation on a warming activation")
	}
	for index, level := range carry.Levels {
		if level == 0 || (index > 0 && carry.Levels[index-1] >= level) {
			return errors.New("alarmd execution: a state carry names its Levels once each, in order")
		}
	}
	return nil
}

// CarriesLevel reports whether the activation carries the Level's history
// over from the previous generation.
func (plan ActivatedPlan) CarriesLevel(level uint32) bool {
	if plan.Carry == nil {
		return false
	}
	for _, carried := range plan.Carry.Levels {
		if carried == level {
			return true
		}
	}
	return false
}

func carriesEqual(left, right *StateCarry) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	if left.From != right.From || len(left.Levels) != len(right.Levels) {
		return false
	}
	for index := range left.Levels {
		if left.Levels[index] != right.Levels[index] {
			return false
		}
	}
	return true
}

// Equal compares by content. The shard and the carry are pointers for the
// wire's sake and two records decoded from the same bytes hold different
// ones; an activation that differs only in what it carries is a different
// activation.
func (plan ActivatedPlan) Equal(other ActivatedPlan) bool {
	return plan.Identity == other.Identity && plan.StateGeneration == other.StateGeneration &&
		plan.StateApplyEpoch == other.StateApplyEpoch && plan.ScheduleRevision == other.ScheduleRevision &&
		plan.RequiredFullSlots == other.RequiredFullSlots && plan.ForceWarming == other.ForceWarming &&
		ShardsEqual(plan.Shard, other.Shard) && carriesEqual(plan.Carry, other.Carry)
}

// IsZero reports an activation that selected nothing.
func (plan ActivatedPlan) IsZero() bool { return plan.Equal(ActivatedPlan{}) }

type PlanActivationFact struct {
	Plan      PlanIdentity
	Selection ActivationSelection
	Selected  ActivatedPlan
	// Shard is the piece of a split strategy this fact is about, nil for a
	// Plan that is not split. On the fact and not only on Selected because a
	// fact that selects nothing still says which piece left. Compared with
	// ShardsEqual, keyed through Key.
	Shard *ShardRef `json:",omitempty"`
}

func (fact PlanActivationFact) Equal(other PlanActivationFact) bool {
	return fact.Plan == other.Plan && fact.Selection == other.Selection && fact.Selected.Equal(other.Selected) &&
		ShardsEqual(fact.Shard, other.Shard)
}

// Key is this fact's index key: the strategy and the piece.
func (fact PlanActivationFact) Key() PlanKey { return PlanKeyOf(fact.Plan, ShardOf(fact.Shard)) }

// GapIdentity names the gap marker of the Plan this activation selected,
// shard included: the one way the Worker composes a per-Plan identity from
// an activation, beside DuePlan.GapIdentity for a due Plan.
func (fact PlanActivationFact) GapIdentity() PlanGapIdentity {
	return PlanGapIdentity{Plan: fact.Plan, StateGeneration: fact.Selected.StateGeneration, Shard: ShardOf(fact.Shard)}
}

type PlanActivationRequest struct {
	Contract FrozenExecutionContractRef
	Plans    []PlanKey
}

type PlanActivationResult struct {
	Contract FrozenExecutionContractRef
	Facts    []PlanActivationFact
}

func (result PlanActivationResult) Validate(request PlanActivationRequest) error {
	if err := request.Contract.Validate(); err != nil {
		return err
	}
	if result.Contract != request.Contract || len(request.Plans) == 0 || len(result.Facts) != len(request.Plans) {
		return errors.New("alarmd execution: invalid activation result cardinality or contract")
	}
	wanted := make(map[PlanKey]struct{}, len(request.Plans))
	for _, plan := range request.Plans {
		if err := plan.Validate(); err != nil {
			return err
		}
		if _, duplicate := wanted[plan]; duplicate {
			return errors.New("alarmd execution: duplicate activation request Plan")
		}
		wanted[plan] = struct{}{}
	}
	// Keyed by strategy and piece: a request is one Query Group's Plans, and
	// a Query Group holds one piece of a strategy, so within it the key and
	// the identity coincide - the key is used so that the check reads the
	// same way everywhere facts are indexed.
	seen := make(map[PlanKey]struct{}, len(result.Facts))
	for _, fact := range result.Facts {
		if _, ok := wanted[fact.Key()]; !ok {
			return errors.New("alarmd execution: activation returned an unknown Plan")
		}
		if _, duplicate := seen[fact.Key()]; duplicate {
			return errors.New("alarmd execution: activation returned a duplicate Plan")
		}
		seen[fact.Key()] = struct{}{}
		switch fact.Selection {
		case ActivationNone:
			if !fact.Selected.IsZero() {
				return errors.New("alarmd execution: no-Plan activation carries a selected Plan")
			}
			// A fact that selects nothing names its piece by index alone:
			// there is no Plan on it to read a dimension or a matcher from,
			// and the index is what the requester asked by.
			if fact.Shard != nil && (fact.Shard.Index <= 0 || fact.Shard.Dimension != "" || fact.Shard.Count != 0 || fact.Shard.MatcherDigest != "") {
				return errors.New("alarmd execution: no-Plan activation names its piece by index alone")
			}
		case ActivationCurrent, ActivationPending:
			if fact.Shard != nil {
				if err := fact.Shard.Validate(); err != nil {
					return err
				}
				if fact.Shard.IsZero() {
					return errors.New("alarmd execution: a zero shard is carried as no shard")
				}
			}
			// The selected Plan repeats the fact's identity and its piece, and
			// has to agree on both: the selection is what the Worker executes
			// and the fact is what it is indexed by.
			if !ShardsEqual(fact.Selected.Shard, fact.Shard) {
				return errors.New("alarmd execution: activation selected another piece than the one it is about")
			}
			if fact.Selected.Identity != fact.Plan || fact.Selected.StateGeneration == "" ||
				fact.Selected.StateApplyEpoch == 0 || fact.Selected.ScheduleRevision == "" || fact.Selected.RequiredFullSlots == 0 {
				return errors.New("alarmd execution: incomplete selected activation Plan")
			}
			if err := fact.Selected.Carry.Validate(fact.Selected); err != nil {
				return err
			}
		default:
			return errors.New("alarmd execution: invalid activation selection")
		}
	}
	return nil
}

// Find looks a fact up by its key: the strategy and the piece. Two pieces of
// one strategy are two facts.
func (result PlanActivationResult) Find(key PlanKey) (PlanActivationFact, bool) {
	for _, fact := range result.Facts {
		if fact.Key() == key {
			return fact, true
		}
	}
	return PlanActivationFact{}, false
}

func (result PlanActivationResult) SameSelections(other PlanActivationResult) bool {
	if result.Contract != other.Contract || len(result.Facts) != len(other.Facts) {
		return false
	}
	for _, fact := range result.Facts {
		otherFact, ok := other.Find(fact.Key())
		if !ok || !fact.Equal(otherFact) {
			return false
		}
	}
	return true
}

type PlanActivationSource interface {
	LoadActivations(context.Context, PlanActivationRequest) (PlanActivationResult, error)
}
