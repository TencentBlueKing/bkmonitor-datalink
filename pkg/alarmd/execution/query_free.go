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

const ReasonBlockedExactSetUnavailable ReasonCode = ReasonCode(contract.ReasonBlockedExactSetUnavailable)

// FrozenDuePlanTargets is the recoverable identity projection of the frozen
// due Plan set. Its digest must be the one already bound by the Slot contract.
type FrozenDuePlanTargets struct {
	DuePlanSetDigest DuePlanSetDigest
	Plans            []PlanIdentity
}

func (targets FrozenDuePlanTargets) Clone() FrozenDuePlanTargets {
	return FrozenDuePlanTargets{
		DuePlanSetDigest: targets.DuePlanSetDigest,
		Plans:            append([]PlanIdentity(nil), targets.Plans...),
	}
}

func (targets FrozenDuePlanTargets) Validate(contractRef FrozenExecutionContractRef) error {
	if targets.DuePlanSetDigest != contractRef.DuePlanSetDigest || len(targets.Plans) == 0 {
		return errors.New("alarmd execution: frozen due Plan targets do not match the Slot contract")
	}
	seen := make(map[PlanIdentity]struct{}, len(targets.Plans))
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
	wanted := make(map[PlanIdentity]struct{}, len(targets.Plans))
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
		expectedReason := ReasonCode(contract.ReasonSnapshotUnavailable)
		if finalization.Mode == FinalizationGapSkipped {
			expectedReason = ReasonCode(contract.ReasonGapSkipped)
		}
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
}

type PlanActivationFact struct {
	Plan      PlanIdentity
	Selection ActivationSelection
	Selected  ActivatedPlan
}

func (fact PlanActivationFact) Equal(other PlanActivationFact) bool {
	return fact == other
}

type PlanActivationRequest struct {
	Contract FrozenExecutionContractRef
	Plans    []PlanIdentity
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
	wanted := make(map[PlanIdentity]struct{}, len(request.Plans))
	for _, plan := range request.Plans {
		if err := plan.Validate(); err != nil {
			return err
		}
		if _, duplicate := wanted[plan]; duplicate {
			return errors.New("alarmd execution: duplicate activation request Plan")
		}
		wanted[plan] = struct{}{}
	}
	seen := make(map[PlanIdentity]struct{}, len(result.Facts))
	for _, fact := range result.Facts {
		if _, ok := wanted[fact.Plan]; !ok {
			return errors.New("alarmd execution: activation returned an unknown Plan")
		}
		if _, duplicate := seen[fact.Plan]; duplicate {
			return errors.New("alarmd execution: activation returned a duplicate Plan")
		}
		seen[fact.Plan] = struct{}{}
		switch fact.Selection {
		case ActivationNone:
			if fact.Selected != (ActivatedPlan{}) {
				return errors.New("alarmd execution: no-Plan activation carries a selected Plan")
			}
		case ActivationCurrent, ActivationPending:
			if fact.Selected.Identity != fact.Plan || fact.Selected.StateGeneration == "" ||
				fact.Selected.StateApplyEpoch == 0 || fact.Selected.ScheduleRevision == "" || fact.Selected.RequiredFullSlots == 0 {
				return errors.New("alarmd execution: incomplete selected activation Plan")
			}
		default:
			return errors.New("alarmd execution: invalid activation selection")
		}
	}
	return nil
}

func (result PlanActivationResult) Find(plan PlanIdentity) (PlanActivationFact, bool) {
	for _, fact := range result.Facts {
		if fact.Plan == plan {
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
		otherFact, ok := other.Find(fact.Plan)
		if !ok || !fact.Equal(otherFact) {
			return false
		}
	}
	return true
}

type PlanActivationSource interface {
	LoadActivations(context.Context, PlanActivationRequest) (PlanActivationResult, error)
}
