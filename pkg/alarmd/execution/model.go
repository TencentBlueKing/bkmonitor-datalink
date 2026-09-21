// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package execution contains the phase-two internal contracts shared by the
// scheduler, access, evaluation, state and worker packages. It deliberately
// carries no Kafka consumer identity or dependency-specific DTOs.
package execution

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

type QueryGroupIdentity string
type SnapshotRevision string
type QueryRevision string
type ScheduleRevision string
type PlanScheduleRevision string
type StateGeneration string

// ObjectDigest names one Query Group execution object by its content, and
// OutputContextDigest names one Plan's output context the same way. A change
// to a Query Group is a transition of its ObjectDigest and nothing else; an
// edit that only moves the OutputContextDigest changes how events are
// rendered, not what is evaluated.
type ObjectDigest string
type OutputContextDigest string
type StateApplyEpoch uint64
type EvaluationTime int64
type DuePlanSetDigest string
type RequirementID string
type DatasetName string
type SeriesIdentityDigest string
type SlotIdentityDigest string
type MutationDigest string

type SlotIdentity struct {
	QueryGroup     QueryGroupIdentity
	EvaluationTime EvaluationTime
}

// FrozenExecutionContractRef identifies immutable computation semantics. It
// is not an ownership or side-effect authorization token.
type FrozenExecutionContractRef struct {
	Slot                 SlotIdentity
	SnapshotRevision     SnapshotRevision
	QueryRevision        QueryRevision
	ScheduleRevision     ScheduleRevision
	ScheduleSegmentStart EvaluationTime
	DuePlanSetDigest     DuePlanSetDigest
}

func (ref FrozenExecutionContractRef) Validate() error {
	switch {
	case ref.Slot.QueryGroup == "":
		return errors.New("alarmd execution: query group is required")
	case ref.Slot.EvaluationTime <= 0:
		return errors.New("alarmd execution: positive evaluation time is required")
	case ref.SnapshotRevision == "":
		return errors.New("alarmd execution: snapshot revision is required")
	case ref.QueryRevision == "":
		return errors.New("alarmd execution: query revision is required")
	case ref.ScheduleRevision == "":
		return errors.New("alarmd execution: schedule revision is required")
	case ref.ScheduleSegmentStart <= 0 || ref.ScheduleSegmentStart > ref.Slot.EvaluationTime:
		return errors.New("alarmd execution: valid schedule segment start is required")
	case ref.DuePlanSetDigest == "":
		return errors.New("alarmd execution: due plan set digest is required")
	default:
		return nil
	}
}

type Operation string

const (
	OperationNormal Operation = "normal"
	OperationRetry  Operation = "retry"
	OperationReplay Operation = "replay"
	OperationProbe  Operation = "probe"
)

func (operation Operation) Validate() error {
	switch operation {
	case OperationNormal, OperationRetry, OperationReplay, OperationProbe:
		return nil
	default:
		return fmt.Errorf("alarmd execution: unsupported operation %q", operation)
	}
}

type Result = observability.Result
type ReasonCode = observability.ReasonCode

func ValidateResultReason(result Result, reason ReasonCode) error {
	switch result {
	case observability.ResultSuccess, observability.ResultResumed:
		if reason != "" && reason != observability.ReasonNone {
			return errors.New("alarmd execution: successful result must use reason none")
		}
		return nil
	case observability.ResultDegraded, observability.ResultTerminal, observability.ResultRetrying,
		observability.ResultPaused, observability.ResultFailed, observability.ResultTimeout,
		observability.ResultSkipped:
		if reason == "" || reason == observability.ReasonNone {
			return errors.New("alarmd execution: non-success result requires a reason")
		}
		if reason == observability.ReasonInternalUnknown ||
			contract.ReasonAllowedForV2(string(reason), contract.ReasonDomainObservation) {
			return nil
		}
		return errors.New("alarmd execution: unknown observation reason")
	default:
		return errors.New("alarmd execution: invalid execution result")
	}
}

type OwnerFence struct {
	QueryGroup QueryGroupIdentity
	OwnerID    string
	OwnerEpoch uint64
	LeaseToken string
}

func (fence OwnerFence) Validate(contractRef FrozenExecutionContractRef) error {
	if fence.QueryGroup != contractRef.Slot.QueryGroup || fence.OwnerID == "" || fence.OwnerEpoch == 0 || fence.LeaseToken == "" {
		return errors.New("alarmd execution: valid current owner fence is required")
	}
	return nil
}

type SlotExecutionRequest struct {
	// Observation-only cadence; never part of persisted projection or identity.
	ShortPeriodCohort              string `json:"-"`
	Contract                       FrozenExecutionContractRef
	DuePlanTargets                 FrozenDuePlanTargets
	EarliestQueryDeadlineUnixMilli int64
	RecoveryUntilUnixMilli         int64
	KeepUntilUnixMilli             int64
	// ReplayExpired is a non-identity scheduler fact. Finalization still uses
	// RecoveryUntilUnixMilli to distinguish distance expiry from age expiry.
	ReplayExpired    bool
	Operation        Operation
	AttemptNo        uint32
	OwnerFence       OwnerFence
	ExpectedNextSlot EvaluationTime
	ExpiredRange     *ExpiredRangeProjectionV1
	// ContentScope names the execution content this Slot runs under: the
	// ObjectDigest of the Schedule Segment it was frozen from (decision-016).
	// Every fenced write the Slot makes -- State, Progress, the expired range
	// -- declares it, and the store's fence refuses the write by name once
	// the Assignment record has moved the Query Group to other content. Empty
	// for a Segment written before Segments named their content, in which
	// case the fence compares what it always compared and nothing more.
	ContentScope string
}

func (request SlotExecutionRequest) Validate() error {
	if err := request.Contract.Validate(); err != nil {
		return err
	}
	if err := request.DuePlanTargets.Validate(request.Contract); err != nil {
		return err
	}
	if request.EarliestQueryDeadlineUnixMilli <= int64(request.Contract.Slot.EvaluationTime)*1000 {
		return errors.New("alarmd execution: earliest query deadline must follow the frozen evaluation time")
	}
	if request.RecoveryUntilUnixMilli <= request.EarliestQueryDeadlineUnixMilli ||
		request.KeepUntilUnixMilli <= request.RecoveryUntilUnixMilli {
		return errors.New("alarmd execution: frozen recovery and retention boundaries are invalid")
	}
	if request.ReplayExpired && request.Operation != OperationNormal {
		return errors.New("alarmd execution: replay-expired Slot must use normal dispatch")
	}
	if err := request.Operation.Validate(); err != nil {
		return err
	}
	if request.AttemptNo == 0 {
		return errors.New("alarmd execution: positive Slot attempt number is required")
	}
	if err := request.OwnerFence.Validate(request.Contract); err != nil {
		return err
	}
	if request.ExpiredRange != nil {
		if err := request.ExpiredRange.Validate(); err != nil {
			return err
		}
		if !request.ReplayExpired || request.Operation != OperationNormal ||
			request.ExpectedNextSlot != request.ExpiredRange.First.Contract.Slot.EvaluationTime ||
			!request.UnfinishedProjection().Equal(request.ExpiredRange.Last) {
			return errors.New("alarmd execution: expired range request differs from fixed tail")
		}
	} else if request.ExpectedNextSlot != request.Contract.Slot.EvaluationTime {
		return errors.New("alarmd execution: expected next slot must equal the frozen evaluation time")
	}
	return nil
}

func (request SlotExecutionRequest) UnfinishedProjection() UnfinishedSlotProjection {
	return UnfinishedSlotProjection{
		Contract: request.Contract, DuePlanTargets: request.DuePlanTargets.Clone(),
		EarliestQueryDeadlineUnixMilli: request.EarliestQueryDeadlineUnixMilli,
		KeepUntilUnixMilli:             request.KeepUntilUnixMilli,
		ContentScope:                   request.ContentScope,
	}
}

type QueryExecutionRequest struct {
	Contract  FrozenExecutionContractRef
	Operation Operation
	AttemptNo uint32
}

func (request QueryExecutionRequest) Validate() error {
	if err := request.Contract.Validate(); err != nil {
		return err
	}
	if err := request.Operation.Validate(); err != nil {
		return err
	}
	if request.AttemptNo == 0 {
		return errors.New("alarmd execution: positive query attempt number is required")
	}
	return nil
}

type PlanIdentity struct {
	TenantID   string
	BusinessID string
	StrategyID string
}

func (identity PlanIdentity) Validate() error {
	if identity.TenantID == "" || identity.BusinessID == "" || identity.StrategyID == "" {
		return errors.New("alarmd execution: complete plan identity is required")
	}
	return nil
}

type ConsumerRef struct {
	Plan     PlanIdentity
	LevelID  uint32
	HasLevel bool
}

type InputRole string

const (
	InputRolePrimary             InputRole = "PRIMARY"
	InputRoleAlgorithmDependency InputRole = "ALGORITHM_DEPENDENCY"
)

type DuePlan struct {
	Identity                    PlanIdentity
	CompiledPlan                *strategy.CompiledPlan
	StateGeneration             StateGeneration
	StateApplyEpoch             StateApplyEpoch
	ScheduleRevision            PlanScheduleRevision
	ScheduleSpec                ScheduleSpec
	CompletionDeadlineUnixMilli int64
	PartialCapabilities         []LevelPartialCapability
}

type Completeness string

const (
	CompletenessFull        Completeness = contract.QueryCompletenessFull
	CompletenessPartial     Completeness = contract.QueryCompletenessPartial
	CompletenessUnavailable Completeness = contract.QueryCompletenessUnavailable
)

type DataState string

const (
	DataStateUnknown DataState = ""
	DataStateData    DataState = "DATA"
	DataStateEmpty   DataState = "EMPTY"
)

type AccessDisposition string

const (
	AccessAvailable   AccessDisposition = "AVAILABLE"
	AccessDegraded    AccessDisposition = "DEGRADED"
	AccessUnavailable AccessDisposition = "UNAVAILABLE"
	AccessTerminal    AccessDisposition = "TERMINAL"
)

type ImpactScope string

const (
	ImpactPlan   ImpactScope = "PLAN"
	ImpactLevel  ImpactScope = "LEVEL"
	ImpactSeries ImpactScope = "SERIES"
)

type InputTerminal struct {
	ReasonCode     ReasonCode
	ImpactScope    ImpactScope
	RecordID       string
	SourceTime     int64
	SeriesIdentity SeriesIdentityDigest
}

type NamedInputBinding struct {
	Consumer        ConsumerRef
	RequirementID   RequirementID
	DatasetName     DatasetName
	Role            InputRole
	ProviderResult  ProviderResultRef
	QueryWindow     QueryWindow
	Dataset         *Dataset
	View            *DatasetView
	Completeness    Completeness
	DataState       DataState
	Disposition     AccessDisposition
	ReasonCode      ReasonCode
	ImpactScope     ImpactScope
	QualityFacts    []InputQualityFact
	Terminals       []InputTerminal
	PartialEvidence *PartialEvidence
	Provenance      InputProvenance
}

type InputProvenance struct {
	PhysicalQuery PhysicalQueryDigest
	AttemptNo     uint32
	TraceID       string
}

type StateKeyIdentity struct {
	Plan                 PlanIdentity
	StateGeneration      StateGeneration
	SeriesIdentityDigest SeriesIdentityDigest
}

type PlanGapIdentity struct {
	Plan            PlanIdentity
	StateGeneration StateGeneration
}

// PlanNoDataIdentity names one Plan's no-data memory. It is keyed the same way
// a gap is: by Plan and by the generation of the execution content, so a Plan
// whose content moves starts its absence clocks fresh rather than inheriting
// timestamps decided against a roster that no longer means the same thing.
type PlanNoDataIdentity struct {
	Plan            PlanIdentity
	StateGeneration StateGeneration
}

type ApplyVersion struct {
	StateApplyEpoch StateApplyEpoch
	EvaluationTime  EvaluationTime
	SlotDigest      SlotIdentityDigest
}

func BuildApplyVersion(contractRef FrozenExecutionContractRef, epoch StateApplyEpoch) (ApplyVersion, error) {
	if err := contractRef.Validate(); err != nil {
		return ApplyVersion{}, err
	}
	if epoch == 0 {
		return ApplyVersion{}, errors.New("alarmd execution: positive state apply epoch is required")
	}
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-slot-identity-v2", struct {
		QueryGroup     QueryGroupIdentity `json:"query_group"`
		EvaluationTime EvaluationTime     `json:"evaluation_time"`
	}{
		QueryGroup:     contractRef.Slot.QueryGroup,
		EvaluationTime: contractRef.Slot.EvaluationTime,
	})
	if err != nil {
		return ApplyVersion{}, fmt.Errorf("alarmd execution: derive Slot identity digest: %w", err)
	}
	return ApplyVersion{
		StateApplyEpoch: epoch,
		EvaluationTime:  contractRef.Slot.EvaluationTime,
		SlotDigest:      SlotIdentityDigest(digest),
	}, nil
}

func (version ApplyVersion) Validate() error {
	if version.StateApplyEpoch == 0 || version.EvaluationTime <= 0 || version.SlotDigest == "" {
		return errors.New("alarmd execution: complete apply version is required")
	}
	return nil
}

type StatePreflightItem struct {
	Identity     StateKeyIdentity
	ApplyVersion ApplyVersion
}

type PlanGapLoadItem struct {
	Identity         PlanGapIdentity
	ApplyVersion     ApplyVersion
	ScheduleRevision PlanScheduleRevision
	// Retention is the Plan's own state retention, which is what the key's
	// lifetime is derived from. These keys name a state generation, so a Plan
	// whose execution content changes leaves the old one behind; its life has to
	// outlast the window it describes and no longer.
	//
	// Empty means the caller has no Plan-specific need and takes the floor. The
	// floor is the safe direction: a key that lives too long is a byte of waste,
	// and one that expires too early restarts a clock that was still running.
	Retention []StateRetentionRequirement
}

type PlanNoDataLoadItem struct {
	Identity         PlanNoDataIdentity
	ApplyVersion     ApplyVersion
	ScheduleRevision PlanScheduleRevision
	Retention        []StateRetentionRequirement
}

// InternalExecution is retained as an execution-package validation aggregate.
// Phase-two runtime hand-off uses InternalExecutionHeader, SeriesExecutionBatch
// and EvaluationRequest; the Coordinator must not exchange this aggregate.
type InternalExecution struct {
	Contract           FrozenExecutionContractRef
	DuePlans           []DuePlan
	Requirements       []DataRequirement
	Inputs             []NamedInputBinding
	EffectiveTimeFacts []BoundEffectiveTimeFact
	StatePreflight     []StatePreflightItem
	GapPreflight       []PlanGapLoadItem
	// FullPlans is, per due Plan, the Plan before any view was chosen for the
	// series being validated. DuePlans carries the view -- what this series'
	// inputs and state are judged against -- and this carries what the Plan
	// as a whole has, which is what a Plan-scoped record such as a gap guard
	// is validated against. Absent (nil) when no view was chosen; readers fall
	// back to the due Plan itself.
	FullPlans map[PlanIdentity]*strategy.CompiledPlan
}

// fullPlanOf is the Plan as a whole for a due Plan that may be a view of it.
func (input InternalExecution) fullPlanOf(plan DuePlan) *strategy.CompiledPlan {
	if full, found := input.FullPlans[plan.Identity]; found && full != nil {
		return full
	}
	return plan.CompiledPlan
}

// planOwnsLevel says whether a level belongs to the Plan as a whole: one of the
// strategy's declared levels, or the Plan's no-data level. It is the question
// a Plan-scoped record asks -- a gap guard protects the Plan, and a scope it
// names on either kind of level is loaded for every series' round, real or
// synthetic -- as opposed to the question an input or a state mutation asks,
// which is answered by the view (compiledPlanHasLevel on DuePlan.CompiledPlan).
func planOwnsLevel(plan *strategy.CompiledPlan, levelID uint32) bool {
	if compiledPlanHasLevel(plan, levelID) {
		return true
	}
	if level := plan.NoDataLevel(); level != nil && level.Definition().LevelID == levelID {
		return true
	}
	return false
}

func (input InternalExecution) Validate(expected FrozenExecutionContractRef) error {
	if err := input.Contract.Validate(); err != nil {
		return err
	}
	if input.Contract != expected {
		return errors.New("alarmd execution: provider changed frozen contract")
	}
	if len(input.DuePlans) == 0 {
		return errors.New("alarmd execution: at least one due plan is required")
	}
	plans := make(map[PlanIdentity]DuePlan, len(input.DuePlans))
	for _, due := range input.DuePlans {
		if err := due.Identity.Validate(); err != nil {
			return err
		}
		if _, duplicate := plans[due.Identity]; duplicate {
			return errors.New("alarmd execution: duplicate due plan identity")
		}
		if due.CompiledPlan == nil || due.StateGeneration == "" || due.StateApplyEpoch == 0 ||
			due.ScheduleRevision == "" || due.CompletionDeadlineUnixMilli <= 0 {
			return errors.New("alarmd execution: incomplete due plan")
		}
		planRef := due.CompiledPlan.PlanRef()
		fingerprints := due.CompiledPlan.Fingerprints()
		if planRef.StrategyID != due.Identity.StrategyID || planRef.StrategyRevision == "" ||
			planRef.StateCompatibilityHash == "" || fingerprints.Detect == "" || fingerprints.Trigger == "" {
			return errors.New("alarmd execution: due plan does not contain a complete compiled contract")
		}
		if err := validatePartialCapabilities(due); err != nil {
			return err
		}
		plans[due.Identity] = due
	}
	requirements := make(map[RequirementID]DataRequirement, len(input.Requirements))
	consumerRequirements := make(map[struct {
		Consumer      ConsumerRef
		RequirementID RequirementID
	}]struct{})
	for _, requirement := range input.Requirements {
		if err := requirement.Validate(plans); err != nil {
			return err
		}
		if _, duplicate := requirements[requirement.RequirementID]; duplicate {
			return errors.New("alarmd execution: duplicate DataRequirement identity")
		}
		requirements[requirement.RequirementID] = requirement
		for _, consumer := range requirement.Consumers {
			consumerRequirements[struct {
				Consumer      ConsumerRef
				RequirementID RequirementID
			}{Consumer: consumer.Consumer, RequirementID: requirement.RequirementID}] = struct{}{}
		}
	}
	if len(requirements) == 0 {
		return errors.New("alarmd execution: at least one DataRequirement is required")
	}
	duePlanSetDigest, err := DeriveDuePlanSetDigest(input.DuePlans, input.Requirements)
	if err != nil {
		return err
	}
	if input.Contract.DuePlanSetDigest != duePlanSetDigest {
		return errors.New("alarmd execution: frozen due Plan set digest does not match prepared execution")
	}
	primaryBindings := make(map[PlanIdentity]uint32, len(plans))
	inputBindings := make(map[struct {
		Consumer      ConsumerRef
		RequirementID RequirementID
	}]struct{}, len(input.Inputs))
	for _, binding := range input.Inputs {
		due, ok := plans[binding.Consumer.Plan]
		requirement, requirementFound := requirements[binding.RequirementID]
		if !ok || !requirementFound || binding.DatasetName == "" {
			return errors.New("alarmd execution: input binding references an unknown plan or dataset")
		}
		if requirement.DatasetName != binding.DatasetName || requirement.Role != binding.Role ||
			requirement.AbsoluteWindow(input.Contract.Slot.EvaluationTime) != binding.QueryWindow {
			return errors.New("alarmd execution: input binding differs from its DataRequirement")
		}
		if err := validateOptionalLevel(binding.Consumer.LevelID, binding.Consumer.HasLevel); err != nil {
			return err
		}
		if binding.Consumer.HasLevel && !compiledPlanHasLevel(due.CompiledPlan, binding.Consumer.LevelID) {
			return errors.New("alarmd execution: input binding references an unknown compiled Level")
		}
		switch binding.Role {
		case InputRolePrimary:
			primaryBindings[binding.Consumer.Plan]++
		case InputRoleAlgorithmDependency:
		default:
			return errors.New("alarmd execution: invalid input role")
		}
		bindingIdentity := struct {
			Consumer      ConsumerRef
			RequirementID RequirementID
		}{Consumer: binding.Consumer, RequirementID: binding.RequirementID}
		if _, duplicate := inputBindings[bindingIdentity]; duplicate {
			return errors.New("alarmd execution: duplicate consumer requirement binding")
		}
		inputBindings[bindingIdentity] = struct{}{}
		if _, declared := consumerRequirements[bindingIdentity]; !declared {
			return errors.New("alarmd execution: input binding consumer is not declared by its DataRequirement")
		}
		if err := binding.QueryWindow.Validate(); err != nil {
			return err
		}
		if binding.ImpactScope != ImpactPlan && binding.ImpactScope != ImpactLevel && binding.ImpactScope != ImpactSeries {
			return errors.New("alarmd execution: invalid input impact scope")
		}
		if binding.ImpactScope == ImpactLevel && !binding.Consumer.HasLevel {
			return errors.New("alarmd execution: LEVEL input impact requires a Level-scoped consumer")
		}
		for _, fact := range binding.QualityFacts {
			if err := validateInputFact(fact.ReasonCode, fact.ImpactScope, fact.RecordID, fact.SourceTime, fact.SeriesIdentity, false); err != nil {
				return err
			}
			if fact.ImpactScope == ImpactLevel && !binding.Consumer.HasLevel {
				return errors.New("alarmd execution: LEVEL quality fact requires a Level-scoped consumer")
			}
		}
		for _, terminal := range binding.Terminals {
			if err := validateInputFact(terminal.ReasonCode, terminal.ImpactScope, terminal.RecordID, terminal.SourceTime, terminal.SeriesIdentity, true); err != nil {
				return err
			}
			if terminal.ImpactScope == ImpactLevel && !binding.Consumer.HasLevel {
				return errors.New("alarmd execution: LEVEL terminal fact requires a Level-scoped consumer")
			}
		}
		switch binding.Completeness {
		case CompletenessFull:
			if binding.ProviderResult == "" || binding.Dataset == nil || binding.View == nil || !binding.View.Uses(binding.Dataset) ||
				(binding.DataState != DataStateData && binding.DataState != DataStateEmpty) ||
				(binding.Disposition != AccessAvailable && binding.Disposition != AccessDegraded) {
				return errors.New("alarmd execution: invalid FULL input binding")
			}
			if err := validateDatasetState(binding.Dataset, binding.DataState); err != nil {
				return err
			}
			if binding.Disposition == AccessDegraded && len(binding.Terminals) == 0 && len(binding.QualityFacts) == 0 {
				return errors.New("alarmd execution: degraded FULL input requires localized quality evidence")
			}
			if binding.PartialEvidence != nil {
				return errors.New("alarmd execution: FULL input must not carry PARTIAL evidence")
			}
		case CompletenessPartial:
			if binding.ProviderResult == "" || binding.Dataset == nil || binding.View == nil || !binding.View.Uses(binding.Dataset) ||
				binding.Disposition != AccessDegraded ||
				(binding.DataState != DataStateData && binding.DataState != DataStateEmpty) {
				return errors.New("alarmd execution: invalid PARTIAL input binding")
			}
			if err := validateDatasetState(binding.Dataset, binding.DataState); err != nil {
				return err
			}
			if binding.PartialEvidence != nil {
				if err := binding.PartialEvidence.Validate(); err != nil {
					return err
				}
			}
		case CompletenessUnavailable:
			if binding.Dataset != nil || binding.View != nil || binding.DataState != DataStateUnknown ||
				(binding.Disposition != AccessUnavailable && binding.Disposition != AccessTerminal) {
				return errors.New("alarmd execution: invalid UNAVAILABLE input binding")
			}
			if binding.PartialEvidence != nil {
				return errors.New("alarmd execution: UNAVAILABLE input must not carry PARTIAL evidence")
			}
		default:
			return errors.New("alarmd execution: invalid input completeness")
		}
		if binding.ProviderResult != "" && (binding.Provenance.PhysicalQuery == "" || binding.Provenance.AttemptNo == 0) {
			return errors.New("alarmd execution: ProviderResult binding requires physical query provenance")
		}
		switch binding.Disposition {
		case AccessAvailable:
			if err := ValidateResultReason(observability.ResultSuccess, binding.ReasonCode); err != nil {
				return err
			}
		case AccessDegraded, AccessUnavailable:
			if err := requireReasonClass(binding.ReasonCode, contract.ReasonClassCoverage); err != nil {
				return err
			}
		case AccessTerminal:
			if err := requireReasonClass(binding.ReasonCode, contract.ReasonClassDeterministic); err != nil {
				return err
			}
		default:
			return errors.New("alarmd execution: invalid access disposition")
		}
	}
	if len(inputBindings) != len(consumerRequirements) {
		return errors.New("alarmd execution: every DataRequirement consumer requires one input binding")
	}
	for plan := range plans {
		if primaryBindings[plan] == 0 {
			return errors.New("alarmd execution: every due plan requires a PRIMARY input binding")
		}
	}
	expectedStateIdentities, err := selectedPrimaryStateIdentities(input.Inputs, plans)
	if err != nil {
		return err
	}
	stateIdentities := make(map[StateKeyIdentity]struct{}, len(input.StatePreflight))
	for _, item := range input.StatePreflight {
		due, ok := plans[item.Identity.Plan]
		if !ok || item.Identity.StateGeneration == "" || item.Identity.SeriesIdentityDigest == "" {
			return errors.New("alarmd execution: invalid state preflight identity")
		}
		expectedVersion, err := BuildApplyVersion(input.Contract, due.StateApplyEpoch)
		if err != nil {
			return err
		}
		if item.Identity.StateGeneration != due.StateGeneration || item.ApplyVersion != expectedVersion {
			return errors.New("alarmd execution: state preflight version does not match the frozen plan")
		}
		if err := item.ApplyVersion.Validate(); err != nil {
			return err
		}
		if _, duplicate := stateIdentities[item.Identity]; duplicate {
			return errors.New("alarmd execution: duplicate state preflight identity")
		}
		stateIdentities[item.Identity] = struct{}{}
	}
	if len(stateIdentities) != len(expectedStateIdentities) {
		return errors.New("alarmd execution: state preflight does not exactly cover selected PRIMARY series")
	}
	for identity := range expectedStateIdentities {
		if _, found := stateIdentities[identity]; !found {
			return errors.New("alarmd execution: selected PRIMARY series is missing state preflight")
		}
	}
	if err := validateEffectiveTimeFacts(input, plans); err != nil {
		return err
	}
	gapIdentities := make(map[PlanGapIdentity]struct{}, len(input.GapPreflight))
	for _, item := range input.GapPreflight {
		due, ok := plans[item.Identity.Plan]
		if !ok || item.Identity.StateGeneration == "" || item.Identity.StateGeneration != due.StateGeneration {
			return errors.New("alarmd execution: invalid gap preflight identity")
		}
		expectedVersion, err := BuildApplyVersion(input.Contract, due.StateApplyEpoch)
		if err != nil {
			return err
		}
		if item.ApplyVersion != expectedVersion || item.ScheduleRevision != due.ScheduleRevision {
			return errors.New("alarmd execution: gap preflight version does not match the frozen plan")
		}
		if _, duplicate := gapIdentities[item.Identity]; duplicate {
			return errors.New("alarmd execution: duplicate gap preflight identity")
		}
		gapIdentities[item.Identity] = struct{}{}
	}
	if len(gapIdentities) != len(plans) {
		return errors.New("alarmd execution: every due Plan requires exactly one gap preflight")
	}
	for identity, due := range plans {
		if _, found := gapIdentities[PlanGapIdentity{Plan: identity, StateGeneration: due.StateGeneration}]; !found {
			return errors.New("alarmd execution: due Plan is missing its gap preflight")
		}
	}
	return nil
}

func selectedPrimaryStateIdentities(
	bindings []NamedInputBinding,
	plans map[PlanIdentity]DuePlan,
) (map[StateKeyIdentity]struct{}, error) {
	identities := make(map[StateKeyIdentity]struct{})
	for _, binding := range bindings {
		if binding.Role != InputRolePrimary {
			continue
		}
		due := plans[binding.Consumer.Plan]
		if binding.Dataset != nil {
			for index := 0; index < binding.View.Len(); index++ {
				record, _ := binding.View.Record(index)
				identity := StateKeyIdentity{
					Plan: binding.Consumer.Plan, StateGeneration: due.StateGeneration,
					SeriesIdentityDigest: SeriesIdentityDigest(record.DimensionIdentityDigest()),
				}
				if identity.SeriesIdentityDigest == "" {
					return nil, errors.New("alarmd execution: selected PRIMARY record lacks series identity")
				}
				identities[identity] = struct{}{}
			}
		}
		for _, fact := range binding.QualityFacts {
			if fact.ImpactScope == ImpactSeries {
				identities[StateKeyIdentity{
					Plan: binding.Consumer.Plan, StateGeneration: due.StateGeneration, SeriesIdentityDigest: fact.SeriesIdentity,
				}] = struct{}{}
			}
		}
		for _, fact := range binding.Terminals {
			if fact.ImpactScope == ImpactSeries {
				identities[StateKeyIdentity{
					Plan: binding.Consumer.Plan, StateGeneration: due.StateGeneration, SeriesIdentityDigest: fact.SeriesIdentity,
				}] = struct{}{}
			}
		}
	}
	return identities, nil
}

func validateOptionalLevel(levelID uint32, hasLevel bool) error {
	if hasLevel != (levelID != 0) {
		return errors.New("alarmd execution: level presence and level ID disagree")
	}
	return nil
}

func compiledPlanHasLevel(plan *strategy.CompiledPlan, levelID uint32) bool {
	for _, level := range plan.Levels() {
		if level.Definition().LevelID == levelID {
			return true
		}
	}
	return false
}

func validateDatasetState(dataset *Dataset, state DataState) error {
	if dataset == nil {
		return errors.New("alarmd execution: available input requires a dataset")
	}
	switch state {
	case DataStateData:
		if dataset.Len() == 0 {
			return errors.New("alarmd execution: DATA requires at least one record")
		}
	case DataStateEmpty:
		if dataset.Len() != 0 {
			return errors.New("alarmd execution: EMPTY requires an empty dataset")
		}
	default:
		return errors.New("alarmd execution: available input requires DATA or EMPTY")
	}
	return nil
}

func validateInputFact(
	reason ReasonCode,
	impact ImpactScope,
	recordID string,
	sourceTime int64,
	series SeriesIdentityDigest,
	terminal bool,
) error {
	if impact != ImpactPlan && impact != ImpactLevel && impact != ImpactSeries {
		return errors.New("alarmd execution: invalid input fact impact scope")
	}
	reasonClass := contract.ReasonClassCoverage
	if terminal {
		reasonClass = contract.ReasonClassDeterministic
	}
	if err := requireReasonClass(reason, reasonClass); err != nil {
		return err
	}
	if impact == ImpactSeries && (recordID == "" || sourceTime <= 0 || series == "") {
		return errors.New("alarmd execution: SERIES input fact requires a stable record anchor and series locator")
	}
	return nil
}

type StateLoadStatus string

const (
	StateFoundReady           StateLoadStatus = "FOUND_READY"
	StateMissingWarming       StateLoadStatus = "MISSING_WARMING"
	StateFoundWarming         StateLoadStatus = "FOUND_WARMING"
	StateFoundGapped          StateLoadStatus = "FOUND_GAPPED"
	StateDeterministicInvalid StateLoadStatus = "DETERMINISTIC_INVALID"
	StateRetryableIO          StateLoadStatus = "RETRYABLE_IO"
)

type LevelFactResult string

const (
	LevelFactNormal      LevelFactResult = "NORMAL"
	LevelFactAnomalous   LevelFactResult = "ANOMALOUS"
	LevelFactUnavailable LevelFactResult = "UNAVAILABLE"
	LevelFactError       LevelFactResult = "ERROR"
)

type HistoryCompleteness string

const (
	HistoryFull    HistoryCompleteness = "FULL"
	HistoryWarming HistoryCompleteness = "WARMING"
	HistoryGapped  HistoryCompleteness = "GAPPED"
)

type StateLevelFact struct {
	LevelID           uint32
	DetectFingerprint string
	Result            LevelFactResult
}

type StateHistoryPoint struct {
	RecordID   string
	SourceTime int64
	Levels     []StateLevelFact
}

type StateGuardFact struct {
	Status               HistoryCompleteness
	ReasonCode           ReasonCode
	WarmupRequirementRef string
}

type RuntimeLevelStateView struct {
	LevelID                 uint32
	LevelStateCompatibility string
	HistoryCompleteness     HistoryCompleteness
	GapReasonCode           ReasonCode
	WarmupRequirementRef    string
	LastProcessedEventTime  int64
}

type RuntimeStateView struct {
	Identity                StateKeyIdentity
	BlobRevision            uint64
	PersistedApplyVersion   ApplyVersion
	PersistedMutationDigest MutationDigest
	LastProcessedEventTime  int64
	SeriesGuard             *StateGuardFact
	Levels                  []RuntimeLevelStateView
	History                 []StateHistoryPoint
	Status                  StateLoadStatus
	ReasonCode              ReasonCode
	VersionComparison       ApplyVersionComparison
}

type StatePreflightRequest struct {
	Contract FrozenExecutionContractRef
	Items    []StatePreflightItem
}

type StatePreflightResult struct {
	Items []RuntimeStateView
}

func (result StatePreflightResult) Find(identity StateKeyIdentity) (RuntimeStateView, bool) {
	for _, item := range result.Items {
		if item.Identity == identity {
			return item, true
		}
	}
	return RuntimeStateView{}, false
}

type ApplyVersionComparison string

const (
	ApplyVersionPersistedOlder ApplyVersionComparison = "PERSISTED_OLDER"
	ApplyVersionEqual          ApplyVersionComparison = "PERSISTED_EQUAL"
	ApplyVersionPersistedNewer ApplyVersionComparison = "PERSISTED_NEWER"
)

func CompareApplyVersion(persisted, candidate ApplyVersion) ApplyVersionComparison {
	if persisted.StateApplyEpoch < candidate.StateApplyEpoch ||
		(persisted.StateApplyEpoch == candidate.StateApplyEpoch && persisted.EvaluationTime < candidate.EvaluationTime) {
		return ApplyVersionPersistedOlder
	}
	if persisted.StateApplyEpoch > candidate.StateApplyEpoch ||
		(persisted.StateApplyEpoch == candidate.StateApplyEpoch && persisted.EvaluationTime > candidate.EvaluationTime) {
		return ApplyVersionPersistedNewer
	}
	if persisted.SlotDigest < candidate.SlotDigest {
		return ApplyVersionPersistedOlder
	}
	if persisted.SlotDigest > candidate.SlotDigest {
		return ApplyVersionPersistedNewer
	}
	return ApplyVersionEqual
}

func ClassifyStatePreflight(request StatePreflightRequest, result StatePreflightResult) (StatePreflightResult, error) {
	if request.Contract.Validate() != nil || len(result.Items) != len(request.Items) {
		return StatePreflightResult{}, errors.New("alarmd execution: invalid state preflight result cardinality")
	}
	wanted := make(map[StateKeyIdentity]ApplyVersion, len(request.Items))
	for _, item := range request.Items {
		if _, duplicate := wanted[item.Identity]; duplicate {
			return StatePreflightResult{}, errors.New("alarmd execution: duplicate state preflight request identity")
		}
		wanted[item.Identity] = item.ApplyVersion
	}
	classified := StatePreflightResult{Items: make([]RuntimeStateView, len(result.Items))}
	seen := make(map[StateKeyIdentity]struct{}, len(result.Items))
	for index, view := range result.Items {
		candidate, ok := wanted[view.Identity]
		if !ok {
			return StatePreflightResult{}, errors.New("alarmd execution: state preflight returned an unknown identity")
		}
		if _, duplicate := seen[view.Identity]; duplicate {
			return StatePreflightResult{}, errors.New("alarmd execution: state preflight returned a duplicate identity")
		}
		seen[view.Identity] = struct{}{}
		switch view.Status {
		case StateMissingWarming:
			if view.BlobRevision != 0 || view.PersistedApplyVersion != (ApplyVersion{}) ||
				view.PersistedMutationDigest != "" || len(view.History) != 0 ||
				view.LastProcessedEventTime != 0 || view.SeriesGuard != nil || len(view.Levels) != 0 ||
				(view.ReasonCode != "" && view.ReasonCode != observability.ReasonNone) {
				return StatePreflightResult{}, errors.New("alarmd execution: missing state carries persisted payload")
			}
			view.VersionComparison = ApplyVersionPersistedOlder
		case StateFoundReady, StateFoundWarming, StateFoundGapped:
			if view.BlobRevision == 0 || view.PersistedMutationDigest == "" ||
				(view.ReasonCode != "" && view.ReasonCode != observability.ReasonNone) {
				return StatePreflightResult{}, errors.New("alarmd execution: found state requires revision/digest and reason none")
			}
			if err := view.PersistedApplyVersion.Validate(); err != nil {
				return StatePreflightResult{}, fmt.Errorf("alarmd execution: invalid persisted apply version: %w", err)
			}
			if err := validateRuntimeStatePayload(view); err != nil {
				return StatePreflightResult{}, err
			}
			view.VersionComparison = CompareApplyVersion(view.PersistedApplyVersion, candidate)
		case StateRetryableIO:
			if runtimeStateHasPayload(view) {
				return StatePreflightResult{}, errors.New("alarmd execution: unavailable state carries trusted payload")
			}
			if err := requireReasonClass(view.ReasonCode, contract.ReasonClassRetryable); err != nil {
				return StatePreflightResult{}, err
			}
		case StateDeterministicInvalid:
			if runtimeStateHasTrustedPayload(view) {
				return StatePreflightResult{}, errors.New("alarmd execution: terminal state carries trusted payload")
			}
			if err := requireReasonClass(view.ReasonCode, contract.ReasonClassDeterministic); err != nil {
				return StatePreflightResult{}, err
			}
			view.VersionComparison = ApplyVersionPersistedOlder
		default:
			return StatePreflightResult{}, errors.New("alarmd execution: unknown runtime state status")
		}
		classified.Items[index] = view
	}
	return classified, nil
}

func runtimeStateHasPayload(view RuntimeStateView) bool {
	return view.BlobRevision != 0 || view.PersistedApplyVersion != (ApplyVersion{}) ||
		view.PersistedMutationDigest != "" || view.LastProcessedEventTime != 0 || view.SeriesGuard != nil ||
		len(view.Levels) != 0 || len(view.History) != 0
}

func runtimeStateHasTrustedPayload(view RuntimeStateView) bool {
	return view.PersistedApplyVersion != (ApplyVersion{}) || view.PersistedMutationDigest != "" ||
		view.LastProcessedEventTime != 0 || view.SeriesGuard != nil || len(view.Levels) != 0 || len(view.History) != 0
}

func validateRuntimeStatePayload(view RuntimeStateView) error {
	if len(view.Levels) == 0 {
		return errors.New("alarmd execution: found state requires typed Level views")
	}
	levelIDs := make(map[uint32]struct{}, len(view.Levels))
	hasWarming := false
	hasGapped := false
	for _, level := range view.Levels {
		if level.LevelID == 0 || level.LevelStateCompatibility == "" || level.WarmupRequirementRef == "" {
			return errors.New("alarmd execution: incomplete runtime Level state view")
		}
		if _, duplicate := levelIDs[level.LevelID]; duplicate {
			return errors.New("alarmd execution: duplicate runtime Level state view")
		}
		levelIDs[level.LevelID] = struct{}{}
		switch level.HistoryCompleteness {
		case HistoryFull:
			if level.GapReasonCode != "" && level.GapReasonCode != observability.ReasonNone {
				return errors.New("alarmd execution: FULL runtime Level must not carry a gap reason")
			}
		case HistoryWarming:
			hasWarming = true
			if err := ValidateResultReason(observability.ResultDegraded, level.GapReasonCode); err != nil {
				return err
			}
		case HistoryGapped:
			hasGapped = true
			if err := ValidateResultReason(observability.ResultDegraded, level.GapReasonCode); err != nil {
				return err
			}
		default:
			return errors.New("alarmd execution: invalid runtime Level history completeness")
		}
	}
	if view.SeriesGuard != nil {
		if view.SeriesGuard.Status != HistoryWarming && view.SeriesGuard.Status != HistoryGapped {
			return errors.New("alarmd execution: invalid series guard status")
		}
		if view.SeriesGuard.WarmupRequirementRef == "" {
			return errors.New("alarmd execution: series guard requires a warmup reference")
		}
		if err := ValidateResultReason(observability.ResultDegraded, view.SeriesGuard.ReasonCode); err != nil {
			return err
		}
		hasWarming = hasWarming || view.SeriesGuard.Status == HistoryWarming
		hasGapped = hasGapped || view.SeriesGuard.Status == HistoryGapped
	}
	switch view.Status {
	case StateFoundReady:
		if hasWarming || hasGapped {
			return errors.New("alarmd execution: FOUND_READY carries an active guard")
		}
	case StateFoundWarming:
		if !hasWarming || hasGapped {
			return errors.New("alarmd execution: FOUND_WARMING does not match typed completeness")
		}
	case StateFoundGapped:
		if !hasGapped {
			return errors.New("alarmd execution: FOUND_GAPPED does not match typed completeness")
		}
	}
	return validateStateHistory(view.History, levelIDs)
}

func validateStateHistory(history []StateHistoryPoint, levelIDs map[uint32]struct{}) error {
	var previous StateHistoryPoint
	for index, point := range history {
		if point.RecordID == "" || point.SourceTime <= 0 {
			return errors.New("alarmd execution: incomplete state history point")
		}
		if index > 0 && (point.SourceTime < previous.SourceTime ||
			(point.SourceTime == previous.SourceTime && point.RecordID <= previous.RecordID)) {
			return errors.New("alarmd execution: state history points must be uniquely ordered")
		}
		pointLevels := make(map[uint32]struct{}, len(point.Levels))
		for _, fact := range point.Levels {
			if _, ok := levelIDs[fact.LevelID]; !ok || fact.DetectFingerprint == "" {
				return errors.New("alarmd execution: state history references an unknown or incomplete Level")
			}
			if _, duplicate := pointLevels[fact.LevelID]; duplicate {
				return errors.New("alarmd execution: duplicate Level fact in state history")
			}
			pointLevels[fact.LevelID] = struct{}{}
			switch fact.Result {
			case LevelFactNormal, LevelFactAnomalous, LevelFactUnavailable, LevelFactError:
			default:
				return errors.New("alarmd execution: invalid state history Level result")
			}
		}
		previous = point
	}
	return nil
}

type GapGuardSnapshot struct {
	Identity                PlanGapIdentity
	MarkerRevision          uint64
	PersistedApplyVersion   ApplyVersion
	PersistedMutationDigest MutationDigest
	Status                  GapLoadStatus
	LastScheduleRevision    PlanScheduleRevision
	Scopes                  []GapScopeState
	ReasonCode              ReasonCode
}

type GapLoadStatus string

const (
	GapFound            GapLoadStatus = "FOUND"
	GapClearedTombstone GapLoadStatus = "CLEARED_TOMBSTONE"
	GapMissing          GapLoadStatus = "MISSING"
	GapUnavailable      GapLoadStatus = "UNAVAILABLE"
	GapTerminal         GapLoadStatus = "TERMINAL"
)

type GapLoadRequest struct {
	Contract FrozenExecutionContractRef
	Items    []PlanGapLoadItem
}

type GapLoadResult struct {
	Items []GapGuardSnapshot
}

func (result GapLoadResult) Find(identity PlanGapIdentity) (GapGuardSnapshot, bool) {
	for _, item := range result.Items {
		if item.Identity == identity {
			return item, true
		}
	}
	return GapGuardSnapshot{}, false
}

func ValidateGapLoad(request GapLoadRequest, result GapLoadResult) error {
	if len(result.Items) != len(request.Items) {
		return errors.New("alarmd execution: invalid gap load result cardinality")
	}
	wanted := make(map[PlanGapIdentity]struct{}, len(request.Items))
	for _, item := range request.Items {
		if _, duplicate := wanted[item.Identity]; duplicate {
			return errors.New("alarmd execution: duplicate gap load request identity")
		}
		wanted[item.Identity] = struct{}{}
	}
	seen := make(map[PlanGapIdentity]struct{}, len(result.Items))
	for _, item := range result.Items {
		if _, ok := wanted[item.Identity]; !ok {
			return errors.New("alarmd execution: gap load returned an unknown identity")
		}
		if _, duplicate := seen[item.Identity]; duplicate {
			return errors.New("alarmd execution: gap load returned a duplicate identity")
		}
		seen[item.Identity] = struct{}{}
		switch item.Status {
		case GapMissing:
			if gapSnapshotHasPayload(item) || (item.ReasonCode != "" && item.ReasonCode != observability.ReasonNone) {
				return errors.New("alarmd execution: missing gap marker carries persisted payload")
			}
		case GapFound:
			if item.MarkerRevision == 0 || item.PersistedMutationDigest == "" || len(item.Scopes) == 0 ||
				(item.ReasonCode != "" && item.ReasonCode != observability.ReasonNone) {
				return errors.New("alarmd execution: found gap marker requires revision/digest/scopes and reason none")
			}
			if err := item.PersistedApplyVersion.Validate(); err != nil {
				return fmt.Errorf("alarmd execution: invalid gap apply version: %w", err)
			}
			if item.LastScheduleRevision == "" {
				return errors.New("alarmd execution: found gap marker requires its last plan schedule revision")
			}
			seenScopes := make(map[GapScope]struct{}, len(item.Scopes))
			for _, scope := range item.Scopes {
				if err := validateOptionalLevel(scope.Scope.LevelID, scope.Scope.HasLevel); err != nil {
					return err
				}
				if _, duplicate := seenScopes[scope.Scope]; duplicate {
					return errors.New("alarmd execution: found gap marker contains a duplicate scope")
				}
				seenScopes[scope.Scope] = struct{}{}
				if scope.Status != GapStatusGapped && scope.Status != GapStatusWarming {
					return errors.New("alarmd execution: invalid persisted gap status")
				}
				if scope.RequiredFullSlots == 0 || scope.ObservedFullSlots >= scope.RequiredFullSlots {
					return errors.New("alarmd execution: active gap scope requires an unmet positive warmup target")
				}
				if err := ValidateResultReason(observability.ResultDegraded, scope.ReasonCode); err != nil {
					return err
				}
			}
		case GapClearedTombstone:
			if item.MarkerRevision == 0 || item.PersistedMutationDigest == "" || len(item.Scopes) != 0 ||
				(item.ReasonCode != "" && item.ReasonCode != observability.ReasonNone) {
				return errors.New("alarmd execution: cleared gap tombstone requires version facts and zero scopes")
			}
			if err := item.PersistedApplyVersion.Validate(); err != nil {
				return fmt.Errorf("alarmd execution: invalid cleared gap apply version: %w", err)
			}
			if item.LastScheduleRevision == "" {
				return errors.New("alarmd execution: cleared gap tombstone requires its last plan schedule revision")
			}
		case GapUnavailable:
			if gapSnapshotHasPayload(item) {
				return errors.New("alarmd execution: unavailable gap marker carries trusted payload")
			}
			if err := requireReasonClass(item.ReasonCode, contract.ReasonClassRetryable); err != nil {
				return err
			}
		case GapTerminal:
			if gapSnapshotHasPayload(item) {
				return errors.New("alarmd execution: terminal gap marker carries trusted payload")
			}
			if err := requireReasonClass(item.ReasonCode, contract.ReasonClassDeterministic); err != nil {
				return err
			}
		default:
			return errors.New("alarmd execution: unknown gap load status")
		}
	}
	return nil
}

func gapSnapshotHasPayload(item GapGuardSnapshot) bool {
	return item.MarkerRevision != 0 || item.PersistedApplyVersion != (ApplyVersion{}) ||
		item.PersistedMutationDigest != "" || item.LastScheduleRevision != "" || len(item.Scopes) != 0
}

type EvaluationRequest struct {
	Header InternalExecutionHeader
	Inputs []SeriesEvaluationInputRequest
	State  StatePreflightResult
	Gaps   GapLoadResult
	// OpenAlerts is the consumer's open alert set the second recovery gate
	// asks. The worker passes it on every request; nil is a caller with no
	// gate and is counted as such, see OpenAlertGateCounts.NotConfigured.
	OpenAlerts contract.OpenAlertSet
}

type StateMutation struct {
	Identity             StateKeyIdentity
	ExpectedBlobRevision uint64
	ApplyVersion         ApplyVersion
	MutationDigest       MutationDigest
	AffectedRecords      []RecordAnchor
	SeriesGuard          *StateGuardFact
	Levels               []RuntimeLevelStateMutation
	Points               []StateHistoryPoint
	// sealedDigest is written by BuildStateMutation and read by ValidateDigest.
	// It is unexported so that no encoder and no caller outside this package can
	// reach it, and it never takes part in the digest.
	sealedDigest *sealedStateMutationDigest
}

type StatePreflightDisposition string

const (
	StateProceed         StatePreflightDisposition = "PROCEED"
	StateAlreadyApplied  StatePreflightDisposition = "ALREADY_APPLIED"
	StateStaleVersion    StatePreflightDisposition = "STALE_VERSION"
	StateVersionConflict StatePreflightDisposition = "STATE_VERSION_CONFLICT"
)

// StateAlreadyAppliedKind says how an ALREADY_APPLIED was decided. stable is
// the ordinary replay: the retry read the stored revision, expected it, and
// found its own statement. revision_skew is the same statement found at a
// revision the mutation did not expect -- the write landed and its reply was
// lost, or something re-sent it -- which used to be classified a conflict.
// The two are counted apart because revision_skew is the only reading that
// can say whether that re-send happens in production, and how often, and a
// fix whose trigger cannot be seen is a fix nobody can confirm.
type StateAlreadyAppliedKind string

const (
	StateAlreadyAppliedStable       StateAlreadyAppliedKind = "stable"
	StateAlreadyAppliedRevisionSkew StateAlreadyAppliedKind = "revision_skew"
	// StateAlreadyAppliedRepeatedKey is a revision_skew with a known cause:
	// the same request carried this key twice, and the later copy met the
	// earlier copy's bytes. That is not a re-sent write, it is an evaluation
	// that produced two mutations for one series identity, and it is counted
	// apart because the fix is at the producer and a steady revision_skew
	// that is really this would otherwise read as a re-sending client.
	StateAlreadyAppliedRepeatedKey StateAlreadyAppliedKind = "repeated_key"
)

func AllStateAlreadyAppliedKinds() []StateAlreadyAppliedKind {
	return []StateAlreadyAppliedKind{StateAlreadyAppliedStable, StateAlreadyAppliedRevisionSkew, StateAlreadyAppliedRepeatedKey}
}

// StateVersionConflictKind says which comparison refused a
// STATE_VERSION_CONFLICT. The status alone reads the same for a key that
// expired between the Slot's read and its write, a key another writer moved,
// and a Slot that re-evaluated one window into a different statement, and the
// fix for each lives somewhere else: the first is the key's TTL against the
// Slot's duration, the second is ownership, the third is the evaluation. Each
// kind is one branch of the classifier, so a count by kind is a count of
// branches taken and not a second reading of the same facts.
type StateVersionConflictKind string

const (
	// StateVersionConflictMissing: the mutation expected a stored revision and
	// the key is not there. A key that expires between the preflight read and
	// the apply leaves exactly this; the retry reads missing, expects nothing
	// and succeeds, so a steady count here with no rising revision_moved is
	// the TTL, not a writer.
	StateVersionConflictMissing StateVersionConflictKind = "missing"
	// StateVersionConflictRevisionMoved: the stored revision is ahead of the
	// one expected, and the bytes there are not this statement. Something
	// wrote the key after the Slot read it.
	StateVersionConflictRevisionMoved StateVersionConflictKind = "revision_moved"
	// StateVersionConflictRevisionReset: the stored revision is behind the
	// one expected. Revisions only grow on one key, so the key was gone and
	// written fresh since the read: an expiry or a flush followed by another
	// writer, which is the missing kind seen one write later.
	StateVersionConflictRevisionReset StateVersionConflictKind = "revision_reset"
	// StateVersionConflictSameVersionOtherStatement: the revision is the one
	// expected and the stored ApplyVersion is this mutation's, but the digest
	// differs. Two evaluations of one window produced two statements for one
	// series; the store did not move, the input did.
	StateVersionConflictSameVersionOtherStatement StateVersionConflictKind = "same_version_other_statement"
	// StateVersionConflictVersionIncomparable: the revision is the one
	// expected and the stored ApplyVersion could not be ordered against the
	// mutation's. The view reached the classifier without a comparison.
	StateVersionConflictVersionIncomparable StateVersionConflictKind = "version_incomparable"
)

func AllStateVersionConflictKinds() []StateVersionConflictKind {
	return []StateVersionConflictKind{StateVersionConflictMissing, StateVersionConflictRevisionMoved,
		StateVersionConflictRevisionReset, StateVersionConflictSameVersionOtherStatement, StateVersionConflictVersionIncomparable}
}

// StateMutationClassification is one mutation's disposition against the
// stored view with, for the two dispositions that have more than one way of
// being reached, which one it was. AlreadyApplied is set only for
// ALREADY_APPLIED and VersionConflict only for STATE_VERSION_CONFLICT.
type StateMutationClassification struct {
	Disposition     StatePreflightDisposition
	AlreadyApplied  StateAlreadyAppliedKind
	VersionConflict StateVersionConflictKind
}

func ClassifyStateMutation(view RuntimeStateView, mutation StateMutation) StatePreflightDisposition {
	return ClassifyStateMutationDetail(view, mutation).Disposition
}

// ClassifyStateMutationDetail is ClassifyStateMutation with how the
// disposition was reached. It is the only place the stored view and a
// mutation are compared; the store's apply paths and the coordinator's
// preflight both read their kinds from here.
func ClassifyStateMutationDetail(view RuntimeStateView, mutation StateMutation) StateMutationClassification {
	// The same statement already on disk is applied, whichever revision it
	// landed at. This is decided before the revision is compared because the
	// revision cannot tell our own landed write from somebody else's: a write
	// re-sent after its reply was lost meets its own bytes one revision up.
	// Called a conflict, that sent the Slot into a retry that re-evaluated
	// against post-Slot state and conflicted on every attempt. The digest is
	// the whole mutation less the revision it expected, so equal digests under
	// an equal ApplyVersion are the same statement.
	if view.VersionComparison == ApplyVersionEqual && mutation.MutationDigest != "" &&
		view.PersistedMutationDigest == mutation.MutationDigest {
		if mutation.ExpectedBlobRevision != view.BlobRevision {
			return StateMutationClassification{Disposition: StateAlreadyApplied, AlreadyApplied: StateAlreadyAppliedRevisionSkew}
		}
		return StateMutationClassification{Disposition: StateAlreadyApplied, AlreadyApplied: StateAlreadyAppliedStable}
	}
	if mutation.ExpectedBlobRevision != view.BlobRevision {
		if view.BlobRevision < mutation.ExpectedBlobRevision {
			return StateMutationClassification{Disposition: StateVersionConflict, VersionConflict: StateVersionConflictRevisionReset}
		}
		return StateMutationClassification{Disposition: StateVersionConflict, VersionConflict: StateVersionConflictRevisionMoved}
	}
	switch view.VersionComparison {
	case ApplyVersionPersistedOlder:
		return StateMutationClassification{Disposition: StateProceed}
	case ApplyVersionPersistedNewer:
		return StateMutationClassification{Disposition: StateStaleVersion}
	case ApplyVersionEqual:
		if view.PersistedMutationDigest != mutation.MutationDigest {
			return StateMutationClassification{Disposition: StateVersionConflict, VersionConflict: StateVersionConflictSameVersionOtherStatement}
		}
		return StateMutationClassification{Disposition: StateAlreadyApplied, AlreadyApplied: StateAlreadyAppliedStable}
	default:
		return StateMutationClassification{Disposition: StateVersionConflict, VersionConflict: StateVersionConflictVersionIncomparable}
	}
}

// StateWriteReuse says how much of what a mutation is about to write is
// already stored. It answers one question against production traffic before
// any write path changes: how often does a steady series write bytes that
// reproduce what Redis already holds.
//
// This is the predicate a skip would consult, not a restatement of its
// reasoning. A hit rate measured with a look-alike describes the look-alike,
// so when a write path starts skipping it must call this same function.
type StateWriteReuse string

const (
	// StateWriteReuseUnobserved is the answer while nothing is stored to
	// compare against. A worker that has just started witnesses no digest, so
	// every round would otherwise classify as changed and read as "never
	// reusable". Keeping it as its own class holds the warm-up out of the
	// rate instead of depressing it, and lets a reader tell "not comparable
	// yet" from "compared and different".
	StateWriteReuseUnobserved StateWriteReuse = "unobserved"
	// StateWriteReuseIdentical means the whole blob reproduces what is stored.
	StateWriteReuseIdentical StateWriteReuse = "identical"
	// StateWriteReuseDecisionStable means every Level state and the series
	// guard are unchanged while the rest of the blob moved. Both steady states
	// a deployment spends its rounds in land here -- a series that keeps
	// recovering and one that never leaves history warming -- rather than in
	// identical, because the history window carries a RecordID and SourceTime
	// that advance every round.
	StateWriteReuseDecisionStable StateWriteReuse = "decision_stable"
	// StateWriteReuseChanged means a Level state or the series guard moved.
	StateWriteReuseChanged StateWriteReuse = "changed"
)

// StateWriteChangeReason names the first field a comparison found different.
// A class alone cannot be acted on: "changed" covers both a decision that truly
// moved, which would end the case for skipping the write, and a field that
// should never have been counted as part of the decision, which would mean the
// predicate is wrong rather than the idea. Those two point at opposite actions
// and are indistinguishable without the field name.
type StateWriteChangeReason string

const (
	StateWriteChangeNone          StateWriteChangeReason = "none"
	StateWriteChangeLevelCount    StateWriteChangeReason = "level_count"
	StateWriteChangeLevelMissing  StateWriteChangeReason = "level_missing"
	StateWriteChangeCompatibility StateWriteChangeReason = "level_compatibility"
	StateWriteChangeCompleteness  StateWriteChangeReason = "history_completeness"
	StateWriteChangeGapReason     StateWriteChangeReason = "gap_reason"
	StateWriteChangeWarmupRef     StateWriteChangeReason = "warmup_ref"
	StateWriteChangeProcessedTime StateWriteChangeReason = "processed_time"
	StateWriteChangeSeriesGuard   StateWriteChangeReason = "series_guard"
	StateWriteChangeReasonOther   StateWriteChangeReason = "other"
)

// AllStateWriteChangeReasons is the complete bounded set, "other" included.
func AllStateWriteChangeReasons() []StateWriteChangeReason {
	return []StateWriteChangeReason{
		StateWriteChangeNone, StateWriteChangeLevelCount, StateWriteChangeLevelMissing,
		StateWriteChangeCompatibility, StateWriteChangeCompleteness, StateWriteChangeGapReason,
		StateWriteChangeWarmupRef, StateWriteChangeProcessedTime, StateWriteChangeSeriesGuard,
		StateWriteChangeReasonOther,
	}
}

// ClassifyStateWriteReuse compares a mutation against the state the preflight
// witnessed for the same key. It reports only what the comparison supports and
// never that a write may be skipped: the two are not the same claim while the
// stored window is the only source of the retention history.
//
// The reason is the first field found different, in a fixed order, and is
// meaningful only when the class is changed.
func ClassifyStateWriteReuse(view RuntimeStateView, mutation StateMutation) (StateWriteReuse, StateWriteChangeReason) {
	if view.PersistedMutationDigest == "" {
		return StateWriteReuseUnobserved, StateWriteChangeNone
	}
	if view.PersistedMutationDigest == mutation.MutationDigest {
		return StateWriteReuseIdentical, StateWriteChangeNone
	}
	if reason := decisionStateChange(view, mutation); reason != StateWriteChangeNone {
		return StateWriteReuseChanged, reason
	}
	return StateWriteReuseDecisionStable, StateWriteChangeNone
}

// decisionStateChange names the first difference between the persisted Level
// state and the one about to be written, or none. It compares fields rather
// than a digest because there is no digest over this subset: adding one would
// be a second construction of the same fact, and the two could disagree.
//
// The field order is fixed so that the reported reason is reproducible. A
// mutation differing in several fields reports the first in this order, which
// means the counts are "first difference", not "how many differ".
func decisionStateChange(view RuntimeStateView, mutation StateMutation) StateWriteChangeReason {
	if len(view.Levels) != len(mutation.Levels) {
		return StateWriteChangeLevelCount
	}
	for _, level := range mutation.Levels {
		stored, found := findPersistedLevel(view.Levels, level.LevelID)
		switch {
		case !found:
			return StateWriteChangeLevelMissing
		case stored.LevelStateCompatibility != level.LevelStateCompatibility:
			return StateWriteChangeCompatibility
		case stored.HistoryCompleteness != level.HistoryCompleteness:
			return StateWriteChangeCompleteness
		case stored.GapReasonCode != level.GapReasonCode:
			return StateWriteChangeGapReason
		case stored.WarmupRequirementRef != level.WarmupRequirementRef:
			return StateWriteChangeWarmupRef
		case stored.LastProcessedEventTime != level.LastProcessedEventTime:
			return StateWriteChangeProcessedTime
		}
	}
	if !seriesGuardUnchanged(view.SeriesGuard, mutation.SeriesGuard) {
		return StateWriteChangeSeriesGuard
	}
	return StateWriteChangeNone
}

func findPersistedLevel(levels []RuntimeLevelStateView, id uint32) (RuntimeLevelStateView, bool) {
	for _, level := range levels {
		if level.LevelID == id {
			return level, true
		}
	}
	return RuntimeLevelStateView{}, false
}

func seriesGuardUnchanged(stored, next *StateGuardFact) bool {
	if stored == nil || next == nil {
		return stored == nil && next == nil
	}
	return *stored == *next
}

type GapMutationKind string

const (
	GapOpen       GapMutationKind = "OPEN"
	GapStrengthen GapMutationKind = "STRENGTHEN"
	GapWarmup     GapMutationKind = "WARMUP"
	GapClear      GapMutationKind = "CLEAR"
)

type GapStatus string

const (
	GapStatusGapped  GapStatus = "GAPPED"
	GapStatusWarming GapStatus = "WARMING"
)

// GapScopeStatuses is every status a held scope can be in, for the partition
// to pre-create and for a reader to bound a family by. A scope that is neither
// is not held: the marker drops it.
var GapScopeStatuses = []GapStatus{GapStatusGapped, GapStatusWarming}

type GapScope struct {
	LevelID  uint32
	HasLevel bool
}

type GapScopeState struct {
	Scope             GapScope
	Status            GapStatus
	ReasonCode        ReasonCode
	RequiredFullSlots uint32
	ObservedFullSlots uint32
}

type GapScopeMutation struct {
	Scope             GapScope
	Kind              GapMutationKind
	ReasonCode        ReasonCode
	RequiredFullSlots uint32
}

type PlanGapMutation struct {
	Identity               PlanGapIdentity
	ExpectedMarkerRevision uint64
	ApplyVersion           ApplyVersion
	ScheduleRevision       PlanScheduleRevision
	MutationDigest         MutationDigest
	Scopes                 []GapScopeMutation
}

// NoDataGroupMemory is what a Plan remembers about one no-data group between
// Slots: when it was last seen with data, and when it was first called absent.
//
// Both are timestamps, and the absence of any third field is load-bearing. The
// duration a no-data event reports is derived from these two, so a build that
// did not run for some rounds - upgraded, rolled back, out of budget - derives
// the same duration as one that ran every round. A field counting rounds would
// be wrong by exactly the rounds nobody ran, and wrong in the quiet direction:
// it would under-report an outage that was running the whole time. There is a
// test that fails when a field is added here, so that adding one is a decision
// rather than an oversight.
type NoDataGroupMemory struct {
	GroupKey    string `json:"group_key"`
	LastSeen    int64  `json:"last_seen,omitempty"`
	FirstAbsent int64  `json:"first_absent,omitempty"`
}

// NoDataGroupAbsence is what a record holds about a group that was not seen in
// the round PresentAsOf names: when it was last seen, and when it was first
// called absent. Either may be zero - the whole-item group has never been seen
// as a series and carries no LastSeen - but not both, because a group that
// remembers nothing is not stored.
type NoDataGroupAbsence struct {
	LastSeen    int64 `json:"last_seen,omitempty"`
	FirstAbsent int64 `json:"first_absent,omitempty"`
}

// NoDataGroupDelta is one group's stored value.
//
// Absent nil means the group was seen in the round PresentAsOf names, and that
// is the whole value: its LastSeen is PresentAsOf and it has no first-absent.
// Writing the timestamp per group instead would write the same number once per
// group, which for a Plan with thousands of them is the difference the whole
// representation change exists to remove.
//
// The compression is only valid for a group whose LastSeen really is
// PresentAsOf. A group the roster stopped expecting while it was present keeps
// its own older LastSeen and never gets a FirstAbsent - that is where a history
// roster grows from - so it is written out in full, or its last-seen time would
// silently follow the Plan's and its absence would read as shorter than it was.
type NoDataGroupDelta struct {
	GroupKey string              `json:"group_key"`
	Absent   *NoDataGroupAbsence `json:"absent,omitempty"`
}

// PlanNoDataMutation changes one Plan's no-data memory.
//
// It is a delta, not the memory: Set carries the groups whose stored value
// this round changes and Del the ones it removes, both relative to the record
// at ExpectedMarkerRevision. That is why the expected revision is not advisory
// here the way it is for a whole replacement - a delta applied to a different
// version is a different memory, so a revision mismatch is a conflict rather
// than something to be reconciled.
//
// It carries two digests because there are two questions and one value cannot
// answer both. MemoryDigest identifies the memory the delta results in: two
// rounds that reach the same memory carry the same one whatever they had to
// change to get there, which is what lets the store answer "already applied"
// without holding the memory. MutationDigest identifies this statement, covers
// MemoryDigest, and is the only one the store can recompute - the memory is
// deliberately not on the wire, so without it a payload could name any memory
// digest it liked and the store would store it.
type PlanNoDataMutation struct {
	Identity      PlanNoDataIdentity
	SchemaVersion NoDataMemorySchema
	// DerivedFrom is the representation this round read the memory out of.
	//
	// A marker revision belongs to the record that issued it, and the two
	// representations keep separate ones. A statement derived from the
	// whole-memory record therefore has no revision to expect of the per-group
	// record, and must not be compared against one: the first deployment that
	// did compare them refused every write in the fleet -- the per-group record
	// did not exist yet, the expected revision came off the blob, and the
	// mismatch read as "the record vanished" on every round forever.
	//
	// It also decides what the statement is. A delta describes the difference
	// from the record it was derived against, so a delta derived from the blob
	// is meaningless to the hash: the groups it leaves out are the ones that
	// did not change since the blob, and they are not in the hash at all. Only
	// a statement derived from the per-group record is a delta; every other
	// one carries the whole memory and replaces what is stored.
	DerivedFrom            NoDataRepresentation
	ExpectedMarkerRevision uint64
	// LoadedApplyVersion is the apply version of the record this statement was
	// derived against, and zero when it was derived against none.
	//
	// It is the guard a whole-record statement has where a delta has the
	// revision. A delta proves at apply time that the record is the one it
	// was derived from by expecting its revision; a statement derived from the
	// other record has no revision of this one to expect, and without this
	// field the only thing standing between it and a record somebody wrote
	// after the read is the ordering of Slot versions -- which lets a write
	// from an older Slot, landing between this Slot's read and its write, be
	// replaced rather than met. Apply versions are the one currency both
	// records share, so this is what a whole-record statement expects instead:
	// the record it is replacing must not be newer than the one it read.
	LoadedApplyVersion ApplyVersion
	ApplyVersion       ApplyVersion
	ScheduleRevision   PlanScheduleRevision
	// RosterVersion names the derivation the expected set came from. It is in
	// the digest because the same group timestamps decided against a different
	// roster are a different memory, and a reader comparing two records has no
	// other way to tell.
	RosterVersion string
	// PresentAsOf is the round this Plan last had data in, which every group
	// written without an absence was seen in. It never moves backwards: a round
	// that saw nothing carries the previous one forward.
	PresentAsOf int64
	// MemoryDigest is what the store keeps beside the record and compares the
	// next statement against.
	MemoryDigest   MutationDigest
	MutationDigest MutationDigest
	// GroupCount is how many groups the resulting memory holds. The store
	// cannot count them - it holds the record and applies a delta to it without
	// reading the groups - so the writer, which has the whole memory in hand,
	// states the number the group bound is checked against.
	GroupCount uint32
	Set        []NoDataGroupDelta
	Del        []string
}

// ReplacesWholeRecord reports whether this statement stands on its own.
//
// True for everything but a per-group delta: those are the statements whose
// Set is the entire memory and whose Del is empty, and applying one has to
// leave the record holding exactly that and nothing it held before.
func (mutation PlanNoDataMutation) ReplacesWholeRecord() bool {
	return mutation.DerivedFrom != NoDataRepresentationPerGroup
}

type StateEvaluation struct {
	Mutation StateMutation
	Events   []contract.TriggerEventV1
}

type PlanDisposition string

const (
	PlanDecided         PlanDisposition = "DECIDED"
	PlanDecidedDegraded PlanDisposition = "DECIDED_DEGRADED"
	PlanUnavailable     PlanDisposition = "UNAVAILABLE"
	PlanReadinessGap    PlanDisposition = "READINESS_GAP"
	PlanTerminal        PlanDisposition = "TERMINAL"
	PlanRetryPending    PlanDisposition = "RETRY_PENDING"
)

// RecoveryGateCounts says what became of this Plan's records whose evaluated
// Levels agreed on RECOVERY. A held record wrote its Level results to the
// state but produced no envelope this round, because a sibling Level had not
// agreed: its state was unknown, or its recovery span still held a
// triggering window. A record sent past a Level without recovery produced its
// envelope; that Level can never say RECOVERY and is not consulted.
type RecoveryGateCounts struct {
	HeldLevelUnavailable         uint64
	HeldLevelRecovering          uint64
	SentPastLevelWithoutRecovery uint64
}

// OpenAlertGateCounts are, per Plan evaluation, what the second recovery
// gate did with the RECOVERY records every Level had agreed on. A record is
// counted here or in RecoveryGateCounts, never both: a Level that holds the
// envelope is asked first, and the set is then not asked. Passed records
// produced their envelope. The two held kinds produced none: the consumer
// holds no open alert on the series, or the series identity it keys alerts
// by could not be built. NotConfigured is a caller that passed no set, the
// behaviour before the gate existed. ProtocolNotGated is a Plan that does
// not publish the alert consumer's protocol, so the set was not asked.
type OpenAlertGateCounts struct {
	Passed                 uint64
	HeldNoOpenAlert        uint64
	HeldFingerprintUnknown uint64
	NotConfigured          uint64
	ProtocolNotGated       uint64
}

type PlanEvaluationResult struct {
	Plan              PlanIdentity
	Disposition       PlanDisposition
	ReasonCode        ReasonCode
	LevelOutcomes     []LevelOutcome
	GuardBeforeEvents []PlanGapMutation
	StateResults      []StateEvaluation
	GuardAfterState   []PlanGapMutation
	RecoveryGate      RecoveryGateCounts
	OpenAlertGate     OpenAlertGateCounts
	// HistoryCoverage is observation only. It lives here and nowhere else: a
	// second copy on the enclosing result would be one more pair of numbers
	// that have to agree about the same evaluation, and the first time they
	// disagreed the page would describe a window that was never summarised.
	// Validate does not check it -- nothing decides anything from it, and a
	// sound decision must not be rejected over a wrong count beside it.
	HistoryCoverage HistoryCoverage
}

// HistoryCoverage is how far the detection window fell short of the points the
// algorithm asked for.
//
// It exists because HISTORY_WARMING is two unrelated situations wearing one
// label. A series two rounds into its life is short by a point or two and
// converges on its own. A series whose lifetime is shorter than the window --
// a network device that lives as long as one pod, a container name that never
// repeats -- is short by most of the window on every round, for ever. The
// verdict is character-for-character identical; only the shortfall separates
// them, and the shortfall never left the window code.
//
// Observation only, and deliberately not on any persisted structure. It is
// recomputed from the live window every round, so a stored copy would be a
// derived value that a later reader could assert equal to a freshly computed
// one -- an assertion the next change to the window arithmetic breaks for
// every series at once, on the first rollout that carries it.
type HistoryCoverage struct {
	// Levels is how many Level summaries were counted; Short how many of those
	// had fewer valid positions than they required. Short == 0 with Levels > 0
	// is a window that was complete, which is a different thing from a window
	// nobody looked at.
	Levels uint32
	Short  uint32
	// WorstValid and WorstRequired are one Level's pair -- the Level with the
	// largest shortfall -- and not a minimum over one field beside a maximum
	// over the other. Taken independently they describe a Level that may not
	// exist, and the number a reader would act on would be one no series ever
	// reported.
	WorstValid    uint32
	WorstRequired uint32
	// Empty is how many of the short windows held no valid position at all.
	//
	// It is counted apart from Short because zero is not a small number here,
	// it is a different situation. A window with some points is a series that
	// is being read and has not been alive long enough; a window with none is
	// a series whose current record produced nothing this Level could use --
	// the newest position is always the record being evaluated, so it is valid
	// unless detection returned UNAVAILABLE or ERROR for that Level.
	//
	// Kept because both report HISTORY_WARMING and the second is the terminal
	// state of a series whose data stopped: as the last real point slides out
	// of the window the verdict goes FULL, GAPPED for as many rounds as the
	// window is wide, then WARMING for ever. Without this count that ending
	// is indistinguishable from a series that simply churns, and the page
	// would describe a dead metric as working as designed.
	Empty uint32
	// Guarded is how many of these windows reported a completeness that was not
	// computed from the window this round.
	//
	// A Level whose persisted state says WARMING or GAPPED keeps forcing that
	// verdict onto the evaluation until the loaded history already forms a full
	// window at the *last processed* record; a Plan gap record forces it the
	// same way. While a guard is in force the freshly computed verdict is
	// discarded and the held one is reported -- but the position counts beside
	// it are the live ones, computed from the window at this record.
	//
	// So the reason code and the numbers under it can be from two different
	// moments, and a window that has already refilled goes on reporting the
	// verdict it had when it had not. Without this count nothing downstream can
	// tell "this is what the window says now" from "this is what it said, and
	// it has not been allowed to say anything else yet" -- which is the
	// difference between a condition to act on and a condition that has passed.
	Guarded uint32
	// Fresh is how many of these windows belong to a series that had no
	// persisted runtime state when this round loaded it, and ShortFresh how
	// many of the *short* ones do. They are two counts because they have two
	// denominators: Fresh is out of Levels, ShortFresh out of Short, and a
	// share taken across the pair would be a ratio whose top and bottom
	// describe different populations.
	//
	// This is the one fact that separates the two situations a short window
	// reports identically, and it is the separation the shortfall alone cannot
	// make however many rounds it is watched:
	//
	//   - A series whose identity churns -- a pod name, a container, a task id
	//     in the aggregation dimensions -- is a *different* series every few
	//     rounds. Each one is genuinely new, each one starts its window from
	//     nothing, and none of them ever lives long enough to fill it. The
	//     strategy's dimensions are what has to change; alarmd is doing exactly
	//     what it was asked.
	//   - A series that has been evaluated for hours and is still short is not
	//     new and never was. Its window is short because its data is missing.
	//     Nothing about the strategy's dimensions will change that.
	//
	// Both report HISTORY_WARMING on every round, both keep the shortfall
	// counter climbing for ever, and until now the layer that publishes them
	// held no series identity at all -- so the page had to say, in as many
	// words, that it could not tell which. The identity does not have to be
	// published to answer it: whether this round *loaded state* for the series
	// is already decided before the window is summarised, and one bit of it is
	// all the question needs.
	//
	// Two things it does not say, which whoever reads it has to know. A change
	// of StateGeneration re-keys every series at once, so the round after a
	// strategy edit reports every window fresh without anything having churned;
	// only a run of such rounds means churn. And state has a lifetime, so a
	// series that stopped being evaluated for long enough comes back counted as
	// fresh -- which is true of it in the only sense used here (no history was
	// loaded) but is not the series being new.
	Fresh      uint32
	ShortFresh uint32
	// Abnormal is how many Level verdicts in this run were ABNORMAL, and
	// AbnormalOnIncomplete how many of those were reached on a window that was
	// not FULL.
	//
	// The trigger decides ABNORMAL before it consults completeness, and the
	// output contract pins that order: WARMING and GAPPED history "permit only
	// monotonic ABNORMAL". Under N-of-M that is sound -- anomalies counted
	// across a hole are a lower bound, so the verdict never over-fires -- but
	// the alert it opens cannot close until the window is FULL again, and a
	// window that stays short keeps it open for ever. How much alerting rides
	// on incomplete windows was, until this pair, a claim about the code and
	// not a reading; it is the number a decision to reset state on leaving the
	// degraded pool would be judged against, before and after.
	//
	// Two counts, not a ratio: a ratio of zero over zero and of zero over ten
	// thousand are different readings, and only the pair keeps them apart.
	Abnormal             uint32
	AbnormalOnIncomplete uint32
	// Unusable is how many Levels could not use this round's record at all:
	// the detection returned UNAVAILABLE or ERROR for it, so the point went
	// into the window with no valid bit. UnusableReason is the first such
	// Level's reason code -- REQUIRED_VALUE_MISSING, a type mismatch, a
	// detector's declared refusal -- which is the one fact that says why.
	//
	// This is what an empty window is made of. A record that never arrives is
	// never evaluated, so it never reaches the window; an empty window is a
	// record that did arrive and could not be used, every round, and until this
	// was carried the two read the same and the empty ones were filed as the
	// data's.
	Unusable       uint32
	UnusableReason string
}

// ObserveUnusable records one Level whose detection could not use this
// round's record, with the reason the detection gave. The first reason is
// kept: it is the one to show, and a record that fails several Levels for
// several reasons is rare enough that one is the right amount to carry.
func (coverage *HistoryCoverage) ObserveUnusable(reason string) {
	if coverage == nil {
		return
	}
	coverage.Unusable++
	if coverage.UnusableReason == "" {
		coverage.UnusableReason = reason
	}
}

// Observe folds one Level summary in. Zero required points means the window
// declined to judge, which is not the same as a window that was judged and
// found complete, so it is not counted at all.
//
// guarded says the completeness reported for this window was held over from a
// guard rather than computed from the window now. It is counted for every
// window, short or not: a guarded window whose live counts are complete is the
// clearest case of a stale verdict and the one a reader most needs to see.
//
// fresh says no runtime state was loaded for this window's series this round.
// It is counted twice on purpose, once against every window and once against
// the short ones, because the question it answers is about the short ones and
// the base rate is what says whether the answer means anything.
func (coverage *HistoryCoverage) Observe(validPositions, requiredPositions uint32, guarded, fresh bool) {
	if coverage == nil || requiredPositions == 0 {
		return
	}
	coverage.Levels++
	if guarded {
		coverage.Guarded++
	}
	if fresh {
		coverage.Fresh++
	}
	if validPositions >= requiredPositions {
		return
	}
	coverage.Short++
	if fresh {
		coverage.ShortFresh++
	}
	if validPositions == 0 {
		coverage.Empty++
	}
	if requiredPositions-validPositions > coverage.WorstRequired-coverage.WorstValid {
		coverage.WorstValid, coverage.WorstRequired = validPositions, requiredPositions
	}
}

// Merge folds another coverage in, keeping the worse of the two pairs whole.
func (coverage *HistoryCoverage) Merge(other HistoryCoverage) {
	if coverage == nil || other.Levels == 0 {
		return
	}
	coverage.Levels += other.Levels
	coverage.Short += other.Short
	coverage.Empty += other.Empty
	coverage.Guarded += other.Guarded
	coverage.Fresh += other.Fresh
	coverage.ShortFresh += other.ShortFresh
	coverage.Abnormal += other.Abnormal
	coverage.AbnormalOnIncomplete += other.AbnormalOnIncomplete
	coverage.Unusable += other.Unusable
	if coverage.UnusableReason == "" {
		coverage.UnusableReason = other.UnusableReason
	}
	if other.Short > 0 && other.WorstRequired-other.WorstValid > coverage.WorstRequired-coverage.WorstValid {
		coverage.WorstValid, coverage.WorstRequired = other.WorstValid, other.WorstRequired
	}
}

type EvaluationResult struct {
	Contract   FrozenExecutionContractRef
	Plans      []PlanEvaluationResult
	Result     Result
	ReasonCode ReasonCode
}

func buildEvaluationInternalExecution(request EvaluationRequest) (InternalExecution, error) {
	if len(request.Inputs) == 0 {
		return InternalExecution{}, errors.New("alarmd execution: evaluation named inputs are required")
	}
	first := request.Inputs[0]
	if first.Contract != request.Header.Contract || !first.Consumer.HasLevel || first.SeriesIdentity == "" {
		return InternalExecution{}, errors.New("alarmd execution: incomplete evaluation named-input scope")
	}
	var due DuePlan
	found := false
	for _, candidate := range request.Header.DuePlans {
		if candidate.Identity == first.Consumer.Plan {
			due, found = candidate, true
			break
		}
	}
	if !found || due.CompiledPlan == nil {
		return InternalExecution{}, errors.New("alarmd execution: evaluation inputs do not exactly cover one due Plan")
	}
	// The Plan the inputs are judged against is the view for this kind of
	// series -- the same choice the evaluator made when it produced the result
	// being validated. A synthetic no-data series carries one input for the
	// no-data level; measuring it against the strategy's declared levels read
	// as "inputs do not cover the Plan" on every no-data round in production,
	// and the state its evaluation wrote read as a Level contract the Plan did
	// not have. The full Plan is kept beside the view: a gap guard belongs to
	// the Plan, not to one view of it, and is validated against every level
	// the Plan has (see planOwnsLevel).
	full := due.CompiledPlan
	due, err := PlanViewFor(due, first.Kind)
	if err != nil {
		return InternalExecution{}, err
	}
	if len(request.Inputs) != len(due.CompiledPlan.Levels()) {
		return InternalExecution{}, errors.New("alarmd execution: evaluation inputs do not exactly cover one due Plan")
	}
	input := InternalExecution{Contract: request.Header.Contract, DuePlans: []DuePlan{due},
		FullPlans: map[PlanIdentity]*strategy.CompiledPlan{due.Identity: full}}
	for index, level := range due.CompiledPlan.Levels() {
		current := request.Inputs[index]
		if current.Contract != request.Header.Contract || current.Consumer.Plan != due.Identity || !current.Consumer.HasLevel ||
			current.Consumer.LevelID != level.Definition().LevelID || current.SeriesIdentity != first.SeriesIdentity ||
			len(current.RequirementIDs) == 0 || len(current.RequirementIDs) != len(current.Inputs) {
			return InternalExecution{}, errors.New("alarmd execution: evaluation inputs are not one ordered Plan/series Level cover")
		}
		primary := 0
		for bindingIndex, binding := range current.Inputs {
			if binding.Consumer != current.Consumer || binding.RequirementID != current.RequirementIDs[bindingIndex] {
				return InternalExecution{}, errors.New("alarmd execution: evaluation binding differs from its named-input exact set")
			}
			if binding.Role == InputRolePrimary {
				primary++
			}
			input.Inputs = append(input.Inputs, binding)
		}
		if primary != 1 {
			return InternalExecution{}, errors.New("alarmd execution: each evaluation Level requires one PRIMARY input")
		}
	}
	for _, requirement := range request.Header.Requirements {
		filtered := requirement
		filtered.Consumers = nil
		for _, consumer := range requirement.Consumers {
			if consumer.Consumer.Plan == due.Identity {
				filtered.Consumers = append(filtered.Consumers, consumer)
			}
		}
		if len(filtered.Consumers) != 0 {
			input.Requirements = append(input.Requirements, filtered)
		}
	}
	version, err := BuildApplyVersion(request.Header.Contract, due.StateApplyEpoch)
	if err != nil {
		return InternalExecution{}, err
	}
	identity := StateKeyIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration,
		SeriesIdentityDigest: first.SeriesIdentity}
	input.StatePreflight = []StatePreflightItem{{Identity: identity, ApplyVersion: version}}
	for _, fact := range request.Header.EffectiveTimeFacts {
		if fact.Consumer.Plan == due.Identity && fact.SeriesIdentity == first.SeriesIdentity {
			input.EffectiveTimeFacts = append(input.EffectiveTimeFacts, fact)
		}
	}
	input.GapPreflight = []PlanGapLoadItem{{Identity: PlanGapIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration},
		ApplyVersion: version, ScheduleRevision: due.ScheduleRevision}}
	return input, nil
}

func (result EvaluationResult) Validate(request EvaluationRequest) error {
	input, err := buildEvaluationInternalExecution(request)
	if err != nil {
		return err
	}
	if result.Contract != input.Contract {
		return errors.New("alarmd execution: evaluation changed frozen contract")
	}
	if result.Result == "" {
		return errors.New("alarmd execution: evaluation result is required")
	}
	if err := ValidateResultReason(result.Result, result.ReasonCode); err != nil {
		return err
	}
	due := make(map[PlanIdentity]DuePlan, len(input.DuePlans))
	for _, plan := range input.DuePlans {
		due[plan.Identity] = plan
	}
	seen := make(map[PlanIdentity]struct{}, len(result.Plans))
	stateIdentities := make(map[StateKeyIdentity]struct{})
	eventIDs := make(map[string]struct{})
	eventRecords := make(map[struct {
		Plan       PlanIdentity
		RecordID   string
		SourceTime int64
	}]struct{})
	expectedResult := observability.Result(observability.ResultSuccess)
	degradedReasons := make(map[ReasonCode]struct{})
	terminalReasons := make(map[ReasonCode]struct{})
	retryReasons := make(map[ReasonCode]struct{})
	hasRetryPending := false
	for _, planResult := range result.Plans {
		plan, ok := due[planResult.Plan]
		if !ok {
			return errors.New("alarmd execution: evaluation returned an unknown plan")
		}
		if _, duplicate := seen[planResult.Plan]; duplicate {
			return errors.New("alarmd execution: evaluation returned a duplicate plan")
		}
		seen[planResult.Plan] = struct{}{}
		primary, primaryTerminal, err := derivePlanPrimaryInputFact(input, planResult.Plan)
		if err != nil {
			return err
		}
		if err := validatePlanPrimaryDisposition(primary, primaryTerminal, planResult.Disposition); err != nil {
			return err
		}
		if primary.Completeness == CompletenessFull && primary.DataState == DataStateEmpty && len(planResult.StateResults) != 0 {
			return errors.New("alarmd execution: FULL EMPTY PRIMARY cannot produce state mutations or events")
		}
		if planResult.Disposition != PlanDecided && planResult.Disposition != PlanDecidedDegraded &&
			planResult.Disposition != PlanRetryPending {
			for _, state := range planResult.StateResults {
				if len(state.Events) != 0 || len(state.Mutation.Points) != 0 ||
					(state.Mutation.SeriesGuard == nil && !stateMutationHasLevelGuard(state.Mutation)) {
					return errors.New("alarmd execution: non-decision Plan may persist only a bounded Runtime State guard")
				}
			}
		}
		if err := validateLoadedFactDisposition(planResult, request.State, request.Gaps); err != nil {
			return err
		}
		levelContracts := runtimeLevelContracts{plan: plan.CompiledPlan}
		if err := validateLoadedStateContracts(plan, request.State, &levelContracts); err != nil {
			return err
		}
		if err := validateLevelOutcomes(input, plan, planResult, request.State, request.Gaps); err != nil {
			return err
		}
		localTerminalReasons := make(map[ReasonCode]struct{})
		localUnknownReasons := make(map[ReasonCode]struct{})
		for _, outcome := range planResult.LevelOutcomes {
			switch outcome.Outcome {
			case LevelOutcomeTerminal:
				localTerminalReasons[outcome.ReasonCode] = struct{}{}
			case LevelOutcomeUnknown:
				localUnknownReasons[outcome.ReasonCode] = struct{}{}
			}
		}
		if planResult.Disposition == PlanDecided && (len(localTerminalReasons) != 0 || len(localUnknownReasons) != 0) {
			return errors.New("alarmd execution: localized terminal or unknown outcome requires degraded Plan disposition")
		}
		if err := validateLoadedGapLevels(input.fullPlanOf(plan), plan.Identity, request.Gaps); err != nil {
			return err
		}
		switch planResult.Disposition {
		case PlanDecided:
		case PlanDecidedDegraded, PlanUnavailable, PlanReadinessGap, PlanTerminal, PlanRetryPending:
			if planResult.ReasonCode == "" || planResult.ReasonCode == observability.ReasonNone {
				return errors.New("alarmd execution: non-FULL plan disposition requires a reason")
			}
			reasonClass := contract.ReasonClassCoverage
			if planResult.Disposition == PlanRetryPending {
				reasonClass = contract.ReasonClassRetryable
			} else if planResult.Disposition == PlanTerminal || len(localTerminalReasons) > 0 {
				reasonClass = contract.ReasonClassDeterministic
			}
			if err := requireReasonClass(planResult.ReasonCode, reasonClass); err != nil {
				return err
			}
			if planResult.Disposition == PlanRetryPending {
				if len(localUnknownReasons) == 0 {
					return errors.New("alarmd execution: retry-pending Plan requires a localized UNKNOWN outcome")
				}
				if _, ok := localUnknownReasons[planResult.ReasonCode]; !ok {
					return errors.New("alarmd execution: retry-pending Plan reason does not match localized UNKNOWN outcome")
				}
				retryReasons[planResult.ReasonCode] = struct{}{}
				hasRetryPending = true
			} else if planResult.Disposition == PlanTerminal || len(localTerminalReasons) > 0 {
				if planResult.Disposition != PlanTerminal {
					if _, ok := localTerminalReasons[planResult.ReasonCode]; !ok {
						return errors.New("alarmd execution: degraded Plan reason does not match localized terminal outcome")
					}
				}
				terminalReasons[planResult.ReasonCode] = struct{}{}
				expectedResult = observability.ResultTerminal
			} else if expectedResult != observability.ResultTerminal {
				if len(localUnknownReasons) > 0 {
					if _, ok := localUnknownReasons[planResult.ReasonCode]; !ok {
						return errors.New("alarmd execution: degraded Plan reason does not match localized unknown outcome")
					}
				}
				degradedReasons[planResult.ReasonCode] = struct{}{}
				expectedResult = observability.ResultDegraded
			} else {
				degradedReasons[planResult.ReasonCode] = struct{}{}
			}
		default:
			return errors.New("alarmd execution: invalid plan disposition")
		}
		if err := validateDegradedGuardCoverage(input, planResult, request.State, request.Gaps); err != nil {
			return err
		}
		for _, stateResult := range planResult.StateResults {
			mutation := stateResult.Mutation
			candidate, candidateFound := findStatePreflight(input.StatePreflight, mutation.Identity)
			if mutation.Identity.Plan != planResult.Plan || mutation.Identity.StateGeneration != plan.StateGeneration ||
				!candidateFound || mutation.ApplyVersion != candidate.ApplyVersion {
				return errors.New("alarmd execution: invalid state mutation")
			}
			if err := mutation.ValidateDigest(); err != nil {
				return err
			}
			if err := mutation.ApplyVersion.Validate(); err != nil {
				return err
			}
			if _, duplicate := stateIdentities[mutation.Identity]; duplicate {
				return errors.New("alarmd execution: duplicate state mutation identity")
			}
			stateIdentities[mutation.Identity] = struct{}{}
			view, found := request.State.Find(mutation.Identity)
			if !found || mutation.ExpectedBlobRevision != view.BlobRevision {
				return errors.New("alarmd execution: state mutation does not match its loaded view")
			}
			if view.Status != StateFoundReady && view.Status != StateFoundWarming &&
				view.Status != StateFoundGapped && view.Status != StateMissingWarming &&
				view.Status != StateDeterministicInvalid {
				return errors.New("alarmd execution: unavailable or terminal state cannot be mutated")
			}
			for _, level := range mutation.Levels {
				ref, found := levelContracts.find(level.LevelID)
				if !found || level.LevelStateCompatibility != ref.LevelStateCompatibility ||
					level.WarmupRequirementRef != ref.WarmupRequirementRef {
					return errors.New("alarmd execution: State mutation Level contract differs from the compiled Plan")
				}
			}
			if mutation.SeriesGuard != nil {
				seriesWarmup, err := levelContracts.seriesWarmup()
				if err != nil || mutation.SeriesGuard.WarmupRequirementRef != seriesWarmup {
					return errors.New("alarmd execution: State mutation series guard differs from the compiled Plan")
				}
			}
			for _, anchor := range mutation.AffectedRecords {
				if !primaryStateAnchorExists(input, planResult.Plan, mutation.Identity.SeriesIdentityDigest, anchor) {
					return errors.New("alarmd execution: State mutation anchor is absent from PRIMARY data or localized facts")
				}
			}
			points := make(map[string]struct{}, len(mutation.Points))
			for _, point := range mutation.Points {
				points[fmt.Sprintf("%s\x00%d", point.RecordID, point.SourceTime)] = struct{}{}
			}
			for index := range stateResult.Events {
				event := &stateResult.Events[index]
				if err := contract.ValidateTriggerEventV1(event); err != nil {
					return fmt.Errorf("alarmd execution: invalid TriggerEvent: %w", err)
				}
				if event.TenantID != planResult.Plan.TenantID || event.BusinessID != planResult.Plan.BusinessID ||
					event.PlanRef != plan.CompiledPlan.PlanRef() {
					return errors.New("alarmd execution: TriggerEvent belongs to another plan")
				}
				fingerprints := plan.CompiledPlan.Fingerprints()
				if event.DetectPlanFingerprint != fingerprints.Detect || event.TriggerStateFingerprint != fingerprints.Trigger {
					return errors.New("alarmd execution: TriggerEvent fingerprints do not match the compiled plan")
				}
				if event.EvaluationTime != int64(input.Contract.Slot.EvaluationTime) ||
					event.RecordRef.DimensionIdentityDigest != string(mutation.Identity.SeriesIdentityDigest) {
					return errors.New("alarmd execution: TriggerEvent does not match its frozen Slot or state series")
				}
				if !primaryDatasetContainsEventRecord(input, planResult.Plan, event) {
					return errors.New("alarmd execution: TriggerEvent record is absent from the Plan PRIMARY Dataset")
				}
				if _, ok := points[fmt.Sprintf("%s\x00%d", event.RecordRef.RecordID, event.RecordRef.SourceTime)]; !ok {
					return errors.New("alarmd execution: TriggerEvent record is absent from its state mutation")
				}
				if _, duplicate := eventIDs[event.EventID]; duplicate {
					return errors.New("alarmd execution: duplicate TriggerEvent ID")
				}
				eventIDs[event.EventID] = struct{}{}
				recordIdentity := struct {
					Plan       PlanIdentity
					RecordID   string
					SourceTime int64
				}{Plan: planResult.Plan, RecordID: event.RecordRef.RecordID, SourceTime: event.RecordRef.SourceTime}
				if _, duplicate := eventRecords[recordIdentity]; duplicate {
					return errors.New("alarmd execution: one Plan record produced multiple TriggerEvents")
				}
				eventRecords[recordIdentity] = struct{}{}
			}
		}
		if len(planResult.GuardBeforeEvents) > 1 || len(planResult.GuardAfterState) > 1 ||
			(len(planResult.GuardBeforeEvents) > 0 && len(planResult.GuardAfterState) > 0) {
			return errors.New("alarmd execution: one Plan gap marker cannot be mutated twice in one Slot")
		}
		for _, mutation := range planResult.GuardBeforeEvents {
			if err := validatePlanGapMutation(input, plan, request.Gaps, mutation, true); err != nil {
				return err
			}
		}
		for _, mutation := range planResult.GuardAfterState {
			if err := validatePlanGapMutation(input, plan, request.Gaps, mutation, false); err != nil {
				return err
			}
		}
		if planResult.Disposition == PlanDecided {
			for _, outcome := range planResult.LevelOutcomes {
				switch outcome.Outcome {
				case LevelOutcomeUnknown, LevelOutcomeTerminal:
					degradedReasons[outcome.ReasonCode] = struct{}{}
					if expectedResult != observability.ResultTerminal {
						expectedResult = observability.ResultDegraded
					}
				}
			}
		}
	}
	if len(seen) != len(due) {
		return errors.New("alarmd execution: every due plan requires one result")
	}
	if hasRetryPending {
		expectedResult = observability.ResultRetrying
	}
	if result.Result != expectedResult {
		return errors.New("alarmd execution: aggregate result does not match Plan dispositions")
	}
	if expectedResult == observability.ResultRetrying {
		if _, ok := retryReasons[result.ReasonCode]; !ok {
			return errors.New("alarmd execution: retrying aggregate reason does not match a retry-pending Plan")
		}
	} else if expectedResult == observability.ResultTerminal {
		if _, ok := terminalReasons[result.ReasonCode]; !ok {
			return errors.New("alarmd execution: terminal aggregate reason does not match a terminal Plan")
		}
	} else if expectedResult == observability.ResultDegraded {
		if _, ok := degradedReasons[result.ReasonCode]; !ok {
			return errors.New("alarmd execution: degraded aggregate reason does not match a degraded Plan")
		}
	}
	return nil
}

func primaryDatasetContainsEventRecord(input InternalExecution, plan PlanIdentity, event *contract.TriggerEventV1) bool {
	return selectedPrimaryContainsAnchor(input, plan, SeriesIdentityDigest(event.RecordRef.DimensionIdentityDigest), RecordAnchor{
		RecordID: event.RecordRef.RecordID, SourceTime: event.RecordRef.SourceTime,
	})
}

func selectedPrimaryContainsAnchor(
	input InternalExecution,
	plan PlanIdentity,
	series SeriesIdentityDigest,
	anchor RecordAnchor,
) bool {
	for _, binding := range input.Inputs {
		if binding.Consumer.Plan != plan || binding.Role != InputRolePrimary || binding.View == nil {
			continue
		}
		for index := 0; index < binding.View.Len(); index++ {
			record, ok := binding.View.Record(index)
			if ok && record.RecordID() == anchor.RecordID && record.SourceTime() == anchor.SourceTime &&
				record.DimensionIdentityDigest() == string(series) {
				return true
			}
		}
	}
	return false
}

func primaryStateAnchorExists(
	input InternalExecution,
	plan PlanIdentity,
	series SeriesIdentityDigest,
	anchor RecordAnchor,
) bool {
	if selectedPrimaryContainsAnchor(input, plan, series, anchor) {
		return true
	}
	for _, binding := range input.Inputs {
		if binding.Consumer.Plan != plan || binding.Role != InputRolePrimary {
			continue
		}
		for _, fact := range binding.QualityFacts {
			if fact.ImpactScope == ImpactSeries && fact.RecordID == anchor.RecordID && fact.SourceTime == anchor.SourceTime &&
				fact.SeriesIdentity == series {
				return true
			}
		}
		for _, terminal := range binding.Terminals {
			if terminal.ImpactScope == ImpactSeries && terminal.RecordID == anchor.RecordID && terminal.SourceTime == anchor.SourceTime &&
				terminal.SeriesIdentity == series {
				return true
			}
		}
	}
	return false
}

func validatePlanPrimaryDisposition(fact PrimaryInputFact, terminal bool, disposition PlanDisposition) error {
	if terminal && disposition != PlanTerminal {
		return errors.New("alarmd execution: terminal PRIMARY input requires a terminal Plan result")
	}
	if terminal {
		return nil
	}
	switch fact.Completeness {
	case CompletenessPartial:
		if disposition == PlanDecided {
			return errors.New("alarmd execution: PARTIAL PRIMARY input cannot produce an unqualified decision")
		}
	case CompletenessUnavailable:
		if disposition == PlanDecided || disposition == PlanDecidedDegraded {
			return errors.New("alarmd execution: unavailable PRIMARY input cannot produce a decision")
		}
	case CompletenessFull:
	default:
		return errors.New("alarmd execution: invalid Plan PRIMARY completeness")
	}
	return nil
}

func validateLoadedFactDisposition(
	plan PlanEvaluationResult,
	states StatePreflightResult,
	gaps GapLoadResult,
) error {
	terminalReasons := make(map[ReasonCode]struct{})
	unavailableReasons := make(map[ReasonCode]struct{})
	loads := LoadedFactDispositionError{Plan: plan.Plan, Disposition: plan.Disposition, PlanReason: plan.ReasonCode}
	for _, item := range gaps.Items {
		if item.Identity.Plan != plan.Plan {
			continue
		}
		switch item.Status {
		case GapTerminal:
			terminalReasons[item.ReasonCode] = struct{}{}
			loads.TerminalGaps++
		case GapUnavailable:
			unavailableReasons[item.ReasonCode] = struct{}{}
			loads.UnavailableGaps++
		}
	}
	for _, item := range states.Items {
		if item.Identity.Plan == plan.Plan && item.Status == StateRetryableIO {
			unavailableReasons[item.ReasonCode] = struct{}{}
			loads.RetryableStates++
		}
	}
	refuse := func(code string) error {
		refused := loads
		refused.Code = code
		for reason := range terminalReasons {
			refused.LoadReasons = append(refused.LoadReasons, reason)
		}
		for reason := range unavailableReasons {
			refused.LoadReasons = append(refused.LoadReasons, reason)
		}
		sort.Slice(refused.LoadReasons, func(i, j int) bool { return refused.LoadReasons[i] < refused.LoadReasons[j] })
		return &refused
	}
	if len(terminalReasons) > 0 {
		if plan.Disposition != PlanTerminal && plan.Disposition != PlanRetryPending {
			return refuse(QueryFailureCodeTerminalLoadIgnored)
		}
		if plan.Disposition == PlanTerminal {
			if _, ok := terminalReasons[plan.ReasonCode]; !ok {
				return refuse(QueryFailureCodeTerminalLoadReasonMismatch)
			}
		}
		if len(unavailableReasons) == 0 {
			return nil
		}
	}
	if len(unavailableReasons) > 0 {
		if plan.Disposition != PlanRetryPending {
			return refuse(QueryFailureCodeRetryableLoadNotRetryPending)
		}
		if _, ok := unavailableReasons[plan.ReasonCode]; !ok {
			return refuse(QueryFailureCodeRetryPendingReasonMismatch)
		}
	} else if plan.Disposition == PlanRetryPending {
		return refuse(QueryFailureCodeRetryPendingWithoutRetryableLoad)
	}
	return nil
}

// The five failure codes a LoadedFactDispositionError reports, one per way
// a Plan result can disagree with the State and gap loads of its Slot.
const (
	QueryFailureCodeTerminalLoadIgnored              = "TERMINAL_LOAD_IGNORED"
	QueryFailureCodeTerminalLoadReasonMismatch       = "TERMINAL_LOAD_REASON_MISMATCH"
	QueryFailureCodeRetryableLoadNotRetryPending     = "RETRYABLE_LOAD_NOT_RETRY_PENDING"
	QueryFailureCodeRetryPendingReasonMismatch       = "RETRY_PENDING_REASON_MISMATCH"
	QueryFailureCodeRetryPendingWithoutRetryableLoad = "RETRY_PENDING_WITHOUT_RETRYABLE_LOAD"
)

// LoadedFactDispositionError reports a Plan result whose disposition
// disagrees with what the State and gap loads of the same Slot said: a
// terminal load the result ignored or named another reason for, a retryable
// load the result did not answer with a retry-pending Plan or answered with
// another reason, or a retry-pending Plan with no retryable load behind it.
// On the reference deployment it appears on the replica a rollout is
// replacing, where loads are cut off by the cancelled context while the
// evaluator still decides. It carries its own code, so the fleet view and
// the log tell the five apart, and the Plan's disposition and reason with
// the loads by kind and their reasons, so that which side is wrong, the
// evaluator or the loads, can be read from the line instead of guessed.
type LoadedFactDispositionError struct {
	Code            string
	Plan            PlanIdentity
	Disposition     PlanDisposition
	PlanReason      ReasonCode
	RetryableStates int
	UnavailableGaps int
	TerminalGaps    int
	// LoadReasons is every distinct reason the terminal and retryable loads
	// carried, sorted.
	LoadReasons []ReasonCode
}

func (err *LoadedFactDispositionError) Error() string {
	what := map[string]string{
		QueryFailureCodeTerminalLoadIgnored:              "terminal State/Gap load cannot be ignored",
		QueryFailureCodeTerminalLoadReasonMismatch:       "terminal Plan reason does not match State/Gap load",
		QueryFailureCodeRetryableLoadNotRetryPending:     "retryable State/Gap load requires retry-pending Plan",
		QueryFailureCodeRetryPendingReasonMismatch:       "retry-pending Plan reason does not match State/Gap load",
		QueryFailureCodeRetryPendingWithoutRetryableLoad: "retry-pending Plan lacks a retryable State/Gap load",
	}[err.Code]
	if what == "" {
		what = "Plan disposition does not match State/Gap load"
	}
	return fmt.Sprintf("alarmd execution: %s: strategy %s disposition %s reason %s; loaded %d retryable State, %d unavailable gap, %d terminal gap, reasons %v",
		what, err.Plan.StrategyID, err.Disposition, err.PlanReason, err.RetryableStates, err.UnavailableGaps, err.TerminalGaps, err.LoadReasons)
}

// QueryFailure names the failure for the query failure facts: the category
// is left to the stage that wraps it, the code is this error's own.
func (err *LoadedFactDispositionError) QueryFailure() (string, string) {
	return "", err.Code
}

// QueryFailureDetail is the disposition, the Plan's reason, the loads by
// kind and the first loaded reason in the bounded detail grammar (lower
// case, at most 96 bytes), so the shape survives rate limiting.
func (err *LoadedFactDispositionError) QueryFailureDetail() string {
	planReason := strings.ToLower(string(err.PlanReason))
	if planReason == "" {
		planReason = "none"
	}
	load := "none"
	if len(err.LoadReasons) > 0 {
		load = strings.ToLower(string(err.LoadReasons[0]))
	}
	detail := "plan=" + strings.ToLower(string(err.Disposition)) + "-reason=" + planReason +
		"-states=" + strconv.Itoa(err.RetryableStates) + "-gaps=" + strconv.Itoa(err.UnavailableGaps) +
		"-terminal=" + strconv.Itoa(err.TerminalGaps) + "-load=" + load
	if len(detail) > 96 {
		detail = detail[:96]
	}
	return detail
}

// StateContractMismatchError reports loaded Runtime State whose Level contract,
// history fingerprint or series guard is not the compiled Plan's. Every input
// of those contracts is folded into the state compatibility hash, so a Plan
// whose contracts moved runs under a new state generation and never loads
// this state; reaching this error means the state under the generation was
// written by a Plan the generation does not describe. It carries its own
// failure code so that the fleet view and the log name it apart from other
// evaluation failures: the Slot does not complete, it retries with backoff,
// and this code is the only trace the Query Group leaves.
type StateContractMismatchError struct {
	What string
}

func (err *StateContractMismatchError) Error() string {
	return "alarmd execution: loaded Runtime State " + err.What + " differs from the compiled Plan"
}

// QueryFailure names the failure for the query failure facts: the category
// is left to the stage that wraps it, the code is this error's own.
func (err *StateContractMismatchError) QueryFailure() (string, string) {
	return "", QueryFailureCodeStateContractMismatch
}

// QueryFailureCodeStateContractMismatch is the failure code a
// StateContractMismatchError reports.
const QueryFailureCodeStateContractMismatch = "STATE_LEVEL_CONTRACT_MISMATCH"

func validateLoadedStateContracts(plan DuePlan, states StatePreflightResult, levelContracts *runtimeLevelContracts) error {
	for _, state := range states.Items {
		if state.Identity.Plan != plan.Identity ||
			(state.Status != StateFoundReady && state.Status != StateFoundWarming && state.Status != StateFoundGapped) {
			continue
		}
		for _, level := range state.Levels {
			ref, found := levelContracts.find(level.LevelID)
			if !found || level.LevelStateCompatibility != ref.LevelStateCompatibility ||
				level.WarmupRequirementRef != ref.WarmupRequirementRef {
				return &StateContractMismatchError{What: "Level contract"}
			}
		}
		for _, point := range state.History {
			for _, fact := range point.Levels {
				ref, found := levelContracts.find(fact.LevelID)
				if !found || fact.DetectFingerprint != ref.DetectFingerprint {
					return &StateContractMismatchError{What: "history"}
				}
			}
		}
		if state.SeriesGuard != nil {
			seriesWarmup, err := levelContracts.seriesWarmup()
			if err != nil || state.SeriesGuard.WarmupRequirementRef != seriesWarmup {
				return &StateContractMismatchError{What: "series guard"}
			}
		}
	}
	return nil
}

func validateLoadedGapLevels(full *strategy.CompiledPlan, identity PlanIdentity, gaps GapLoadResult) error {
	for _, item := range gaps.Items {
		if item.Identity.Plan != identity {
			continue
		}
		for _, scope := range item.Scopes {
			if scope.Scope.HasLevel && !planOwnsLevel(full, scope.Scope.LevelID) {
				return errors.New("alarmd execution: persisted gap references an unknown compiled Level")
			}
		}
	}
	return nil
}

func validatePlanGapMutation(
	input InternalExecution,
	plan DuePlan,
	loaded GapLoadResult,
	mutation PlanGapMutation,
	beforeEvents bool,
) error {
	candidate, candidateFound := findGapPreflight(input.GapPreflight, mutation.Identity)
	if mutation.Identity.Plan != plan.Identity || mutation.Identity.StateGeneration != plan.StateGeneration ||
		!candidateFound || mutation.ApplyVersion != candidate.ApplyVersion ||
		mutation.ScheduleRevision != candidate.ScheduleRevision ||
		len(mutation.Scopes) == 0 {
		return errors.New("alarmd execution: invalid Plan gap mutation")
	}
	if err := mutation.ApplyVersion.Validate(); err != nil {
		return err
	}
	if err := mutation.ValidateDigest(); err != nil {
		return err
	}
	snapshot, found := loaded.Find(mutation.Identity)
	if !found || mutation.ExpectedMarkerRevision != snapshot.MarkerRevision {
		return errors.New("alarmd execution: Plan gap mutation does not match its loaded marker")
	}
	seen := make(map[GapScope]struct{}, len(mutation.Scopes))
	for _, scope := range mutation.Scopes {
		if err := validateOptionalLevel(scope.Scope.LevelID, scope.Scope.HasLevel); err != nil {
			return err
		}
		if scope.Scope.HasLevel && !planOwnsLevel(input.fullPlanOf(plan), scope.Scope.LevelID) {
			return errors.New("alarmd execution: gap mutation references an unknown compiled Level")
		}
		if _, duplicate := seen[scope.Scope]; duplicate {
			return errors.New("alarmd execution: duplicate Plan gap scope mutation")
		}
		seen[scope.Scope] = struct{}{}
		if beforeEvents {
			if scope.Kind != GapOpen && scope.Kind != GapStrengthen {
				return errors.New("alarmd execution: invalid pre-event gap mutation")
			}
			if err := ValidateResultReason(observability.ResultDegraded, scope.ReasonCode); err != nil {
				return err
			}
		} else {
			if scope.Kind != GapWarmup && scope.Kind != GapClear {
				return errors.New("alarmd execution: invalid post-state gap mutation")
			}
			if scope.Kind == GapClear {
				if err := ValidateResultReason(observability.ResultSuccess, scope.ReasonCode); err != nil {
					return err
				}
			} else if err := ValidateResultReason(observability.ResultDegraded, scope.ReasonCode); err != nil {
				return err
			}
		}
	}
	return nil
}

func findStatePreflight(items []StatePreflightItem, identity StateKeyIdentity) (StatePreflightItem, bool) {
	for _, item := range items {
		if item.Identity == identity {
			return item, true
		}
	}
	return StatePreflightItem{}, false
}

func findGapPreflight(items []PlanGapLoadItem, identity PlanGapIdentity) (PlanGapLoadItem, bool) {
	for _, item := range items {
		if item.Identity == identity {
			return item, true
		}
	}
	return PlanGapLoadItem{}, false
}

type SideEffectAdmissionRequest struct {
	Contract        FrozenExecutionContractRef
	Plan            PlanIdentity
	StateApplyEpoch StateApplyEpoch
	OwnerFence      OwnerFence
}

type SideEffectAdmissionResult struct {
	Admitted   bool
	ReasonCode ReasonCode
}

func (result SideEffectAdmissionResult) Validate() error {
	if result.Admitted {
		return ValidateResultReason(observability.ResultSuccess, result.ReasonCode)
	}
	return ValidateResultReason(observability.ResultFailed, result.ReasonCode)
}

type GapGuardApplyRequest struct {
	Contract FrozenExecutionContractRef
	Items    []PlanGapMutation
}

type GapGuardApplyStatus string

const (
	GapGuardApplied        GapGuardApplyStatus = "APPLIED"
	GapGuardAlreadyApplied GapGuardApplyStatus = "ALREADY_APPLIED"
	GapGuardStale          GapGuardApplyStatus = "STALE_VERSION"
	GapGuardConflict       GapGuardApplyStatus = "CONFLICT"
	GapGuardRetryable      GapGuardApplyStatus = "RETRYABLE_IO"
	GapGuardRejected       GapGuardApplyStatus = "DETERMINISTIC_INVALID"
)

type GapGuardApplyItemResult struct {
	Identity   PlanGapIdentity
	Status     GapGuardApplyStatus
	ReasonCode ReasonCode
}

type GapGuardApplyResult struct {
	Items []GapGuardApplyItemResult
}

func (result GapGuardApplyResult) Validate() error {
	for _, item := range result.Items {
		var err error
		switch item.Status {
		case GapGuardApplied, GapGuardAlreadyApplied:
			err = ValidateResultReason(observability.ResultSuccess, item.ReasonCode)
		case GapGuardRetryable:
			err = requireReasonClass(item.ReasonCode, contract.ReasonClassRetryable)
		case GapGuardRejected:
			err = requireReasonClass(item.ReasonCode, contract.ReasonClassDeterministic)
		case GapGuardStale, GapGuardConflict:
			err = requireReasonNone(item.ReasonCode)
		default:
			err = errors.New("alarmd execution: unknown gap guard apply status")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// StateApplyRequest carries the mutations of exactly one Plan. Retention is
// that Plan's frozen per-Level retention need, from which the store derives the
// write TTL; it is required, so a caller that cannot state what its keys must
// outlive is rejected instead of silently writing at the configured ceiling.
type StateApplyRequest struct {
	Contract  FrozenExecutionContractRef
	Retention []StateRetentionRequirement
	Items     []StateMutation
}

type StateAdmissionStatus string

const (
	StateAdmissionAccepted             StateAdmissionStatus = "ADMITTED"
	StateAdmissionRetryable            StateAdmissionStatus = "RETRYABLE_IO"
	StateAdmissionDeterministicInvalid StateAdmissionStatus = "DETERMINISTIC_INVALID"
)

type StateAdmissionItemResult struct {
	Identity   StateKeyIdentity
	Status     StateAdmissionStatus
	ReasonCode ReasonCode
	// EncodedBytes is the stored size of an admitted mutation as the store
	// measured it; it is observation input only and zero when unknown.
	EncodedBytes int
}

type StateAdmissionResult struct {
	Items []StateAdmissionItemResult
}

func (result StateAdmissionResult) Validate() error {
	for _, item := range result.Items {
		var err error
		switch item.Status {
		case StateAdmissionAccepted:
			err = ValidateResultReason(observability.ResultSuccess, item.ReasonCode)
		case StateAdmissionRetryable:
			err = requireReasonClass(item.ReasonCode, contract.ReasonClassRetryable)
		case StateAdmissionDeterministicInvalid:
			err = requireReasonClass(item.ReasonCode, contract.ReasonClassDeterministic)
		default:
			err = errors.New("alarmd execution: unknown state admission status")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

type StateApplyStatus string

const (
	StateApplied                   StateApplyStatus = "APPLIED"
	StateApplyAlreadyApplied       StateApplyStatus = "ALREADY_APPLIED"
	StateApplyStale                StateApplyStatus = "STALE_VERSION"
	StateApplyCASConflict          StateApplyStatus = "CAS_CONFLICT"
	StateApplyVersionConflict      StateApplyStatus = "STATE_VERSION_CONFLICT"
	StateApplyRetryable            StateApplyStatus = "RETRYABLE_IO"
	StateApplyDeterministicInvalid StateApplyStatus = "DETERMINISTIC_INVALID"
)

type StateApplyItemResult struct {
	Identity   StateKeyIdentity
	Status     StateApplyStatus
	ReasonCode ReasonCode
	// AlreadyApplied says how an ALREADY_APPLIED was decided and
	// VersionConflict how a STATE_VERSION_CONFLICT was; each is empty for
	// every other status. StoredBlobRevision is the revision the key was found
	// at for both, zero when it was not found, so a line can say how far the
	// expectation was off; StoredVersionComparison is how the stored
	// ApplyVersion ordered against the mutation's, empty when there was
	// nothing stored to compare.
	AlreadyApplied          StateAlreadyAppliedKind
	VersionConflict         StateVersionConflictKind
	StoredBlobRevision      uint64
	StoredVersionComparison ApplyVersionComparison
	// RepeatedKey says the request itself carried this key earlier. On an
	// ALREADY_APPLIED it is the repeated_key kind; on a STATE_VERSION_CONFLICT
	// it is the one fact that tells a producer that made two different
	// statements for one series from a writer that lost a race, and the
	// status alone reads the same for both.
	RepeatedKey bool
}

// MarkVersionConflict fills in a STATE_VERSION_CONFLICT with the values the
// comparison used, so the item carries them to whoever reads the refusal.
func (item *StateApplyItemResult) MarkVersionConflict(kind StateVersionConflictKind, view RuntimeStateView) {
	item.Status, item.VersionConflict = StateApplyVersionConflict, kind
	item.StoredBlobRevision, item.StoredVersionComparison = view.BlobRevision, view.VersionComparison
}

type StateApplyResult struct {
	Items []StateApplyItemResult
}

func (result StateApplyResult) Validate() error {
	for _, item := range result.Items {
		var err error
		switch item.Status {
		case StateApplied, StateApplyAlreadyApplied:
			err = ValidateResultReason(observability.ResultSuccess, item.ReasonCode)
		case StateApplyRetryable, StateApplyCASConflict:
			err = requireReasonClass(item.ReasonCode, contract.ReasonClassRetryable)
		case StateApplyDeterministicInvalid:
			err = requireReasonClass(item.ReasonCode, contract.ReasonClassDeterministic)
		case StateApplyVersionConflict, StateApplyStale:
			err = requireReasonNone(item.ReasonCode)
		default:
			err = errors.New("alarmd execution: unknown state apply status")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

type CompletionKind string

const (
	CompletionFull                CompletionKind = "FULL_COMPLETED"
	CompletionFullEmpty           CompletionKind = "FULL_EMPTY_COMPLETED"
	CompletionPartialGap          CompletionKind = "COMPLETED_WITH_PARTIAL_GAP"
	CompletionUnavailable         CompletionKind = "COMPLETED_WITH_UNAVAILABLE"
	CompletionTerminal            CompletionKind = "COMPLETED_WITH_TERMINAL"
	CompletionGapSkipped          CompletionKind = "GAP_SKIPPED"
	CompletionSnapshotUnavailable CompletionKind = "SNAPSHOT_UNAVAILABLE"
)

type PrimaryInputFact struct {
	Completeness Completeness
	DataState    DataState
}

func DerivePrimaryInputFact(input InternalExecution) (PrimaryInputFact, error) {
	return derivePrimaryInputFact(input, nil)
}

func derivePlanPrimaryInputFact(input InternalExecution, plan PlanIdentity) (PrimaryInputFact, bool, error) {
	fact, err := derivePrimaryInputFact(input, &plan)
	if err != nil {
		return PrimaryInputFact{}, false, err
	}
	terminal := false
	for _, binding := range input.Inputs {
		if binding.Role == InputRolePrimary && binding.Consumer.Plan == plan && binding.Disposition == AccessTerminal {
			terminal = true
		}
	}
	return fact, terminal, nil
}

func derivePrimaryInputFact(input InternalExecution, plan *PlanIdentity) (PrimaryInputFact, error) {
	fact := PrimaryInputFact{Completeness: CompletenessFull, DataState: DataStateEmpty}
	found := false
	anyData := false
	for _, binding := range input.Inputs {
		if binding.Role != InputRolePrimary || (plan != nil && binding.Consumer.Plan != *plan) {
			continue
		}
		found = true
		anyData = anyData || binding.DataState == DataStateData
		switch binding.Completeness {
		case CompletenessUnavailable:
			fact.Completeness = CompletenessUnavailable
		case CompletenessPartial:
			if fact.Completeness != CompletenessUnavailable {
				fact.Completeness = CompletenessPartial
			}
		case CompletenessFull:
		default:
			return PrimaryInputFact{}, errors.New("alarmd execution: invalid PRIMARY completeness")
		}
	}
	if !found {
		return PrimaryInputFact{}, errors.New("alarmd execution: PRIMARY input is required")
	}
	if fact.Completeness == CompletenessUnavailable {
		fact.DataState = DataStateUnknown
	} else if anyData {
		fact.DataState = DataStateData
	}
	return fact, nil
}

// CompletionCause names which of the several conditions that all complete a
// Slot as UNAVAILABLE, or all complete it as COMPLETED_WITH_PARTIAL_GAP,
// actually occurred.
//
// Each of the two kinds folds different things into one word, and they call
// for opposite responses: data that has not landed in storage yet resolves
// itself, while a Plan that could not be decided does not; a primary input
// the provider returned with a stretch of the window missing is the data
// link's to explain, while a Slot whose Plans moved under it clears on the
// next Slot by itself. Operators reading a list of hundreds of "degraded"
// objects could not tell which was which, so the list was not actionable and
// taught them to ignore it.
//
// The type was named UnavailableCause while only the UNAVAILABLE kind carried
// one. The rename changes nothing outside this process: every consumer holds
// the value as a plain string and nothing persisted holds it at all.
//
// This is carried as observation only. Putting it on the persisted completion
// would change how consecutive gaps fold into a Progress gap summary, which is
// a durable structure and a separate decision.
type CompletionCause string

const (
	// CauseDataNotReady is a readiness gap: the data for this evaluation has
	// not arrived in storage yet. It resolves without anyone doing anything,
	// and it is the majority of what the page currently shows as degraded.
	CauseDataNotReady CompletionCause = "DATA_NOT_READY"
	// CausePlanUnavailable is a Plan that could not be decided at all.
	CausePlanUnavailable CompletionCause = "PLAN_UNAVAILABLE"
	// CausePrimaryInputUnavailable is the query for the primary input coming
	// back with nothing usable.
	CausePrimaryInputUnavailable CompletionCause = "PRIMARY_INPUT_UNAVAILABLE"
	// CauseLevelOutcomeUnknown is a Level whose outcome could not be determined
	// even though its Plan was.
	CauseLevelOutcomeUnknown CompletionCause = "LEVEL_OUTCOME_UNKNOWN"
	// CausePrimaryInputPartial is the provider answering the primary input's
	// query with a stretch of the window missing. The Slot completes as
	// COMPLETED_WITH_PARTIAL_GAP, and the missing stretch is the data link's or
	// the storage's to explain, not this process's.
	CausePrimaryInputPartial CompletionCause = "PRIMARY_INPUT_PARTIAL"
	// CauseConfigDrift is a Slot whose activated Plans changed while it was
	// executing. It also completes as COMPLETED_WITH_PARTIAL_GAP, but nobody
	// needs to look: the strategy was edited, and the next Slot runs under the
	// new selection. The Worker's drift constructor decides it from the same
	// primary fact this derivation reads; it is listed here so the ranking
	// covers every cause a completion can carry.
	CauseConfigDrift CompletionCause = "CONFIG_DRIFT"
)

// DeriveCompletionKind reports the completion kind alone, which is what the
// contract and every persisted structure use.
func DeriveCompletionKind(input InternalExecution, result EvaluationResult) (CompletionKind, error) {
	kind, _, err := deriveCompletion(input, result)
	return kind, err
}

// DeriveCompletion reports the kind together with why it was unavailable.
//
// The two come from one traversal on purpose: derived separately they would be
// two functions that must agree about the same Slot, and the first time they
// disagreed the page would explain a completion that did not happen.
func DeriveCompletion(input InternalExecution, result EvaluationResult) (CompletionKind, CompletionCause, error) {
	return deriveCompletion(input, result)
}

func deriveCompletion(input InternalExecution, result EvaluationResult) (CompletionKind, CompletionCause, error) {
	kind, cause, _, err := deriveCompletionDetail(input, result)
	return kind, cause, err
}

// CompletionAttribution is why a Slot completed the way it did: the condition
// that folded into the kind, and that condition's own reason.
//
// The two travel as one value rather than as two parameters for the reason the
// derivation returns them from a single traversal -- carried separately they
// become two things that must agree about the same Slot, and the first time
// they disagree the page explains a completion that did not happen.
type CompletionAttribution struct {
	Cause  CompletionCause
	Reason ReasonCode
	// Coverage is the evidence the reason was derived from, and it travels
	// here for exactly the reason the reason travels beside the cause: a label
	// that leaves without the numbers behind it arrives somewhere that cannot
	// check it. HISTORY_WARMING has been doing that since it was written --
	// identical on a window one point from converging and on a window that
	// will never converge, with the count that separates them discarded two
	// layers upstream.
	//
	// Zero when the Slot did not summarise any window, which is not the same
	// as a Slot whose windows were all complete.
	Coverage HistoryCoverage
}

// DeriveCompletionDetail adds the reason that belongs to the reported cause.
//
// Separate from DeriveCompletion so the existing callers keep their signature,
// and one traversal still decides all three: derived apart they would be
// functions that must agree about the same Slot.
func DeriveCompletionDetail(input InternalExecution, result EvaluationResult) (
	CompletionKind, CompletionCause, ReasonCode, error) {
	return deriveCompletionDetail(input, result)
}

func deriveCompletionDetail(input InternalExecution, result EvaluationResult) (
	CompletionKind, CompletionCause, ReasonCode, error) {
	if len(result.Plans) == 0 {
		return "", "", "", errors.New("alarmd execution: no Plan results to complete")
	}
	primary, err := DerivePrimaryInputFact(input)
	if err != nil {
		return "", "", "", err
	}
	// A Slot can hit several of these at once. The cause reported is the most
	// actionable one rather than the first or the commonest: a readiness gap
	// beside a Plan that could not be decided is a Slot someone should look at,
	// and reporting the gap would say the opposite.
	cause := CompletionCause("")
	// The reason travels with the cause it belongs to, decided by the same
	// comparison, so the two cannot end up describing different findings.
	//
	// It is carried at all because the cause alone stops one level short of the
	// answer: LEVEL_OUTCOME_UNKNOWN is required by contract to carry a reason of
	// either the coverage class or the retryable class, and those point in
	// opposite directions -- coverage means the data does not reach this window,
	// retryable means it will clear on its own. Reporting only the cause makes
	// them one indistinguishable population, which on a running deployment was
	// 61 of 62 objects sharing a single label that could not say whose problem
	// they were.
	reason := ReasonCode("")
	note := func(candidate CompletionCause, candidateReason ReasonCode) {
		if causeRank(candidate) > causeRank(cause) {
			cause, reason = candidate, candidateReason
		}
	}
	hasPartial := primary.Completeness == CompletenessPartial
	hasUnavailable := primary.Completeness == CompletenessUnavailable
	if hasUnavailable {
		note(CausePrimaryInputUnavailable, "")
	}
	if hasPartial {
		note(CausePrimaryInputPartial, "")
	}
	allFullEmpty := primary.Completeness == CompletenessFull && primary.DataState == DataStateEmpty
	hasTerminal := false
	for _, plan := range result.Plans {
		switch plan.Disposition {
		case PlanTerminal:
			hasTerminal = true
		case PlanUnavailable:
			hasUnavailable = true
			note(CausePlanUnavailable, plan.ReasonCode)
		case PlanReadinessGap:
			hasUnavailable = true
			note(CauseDataNotReady, plan.ReasonCode)
		case PlanRetryPending:
			return "", "", "", errors.New("alarmd execution: retry-pending Plan cannot derive a completed Slot")
		case PlanDecided, PlanDecidedDegraded:
			if plan.Disposition == PlanDecidedDegraded {
				// A Plan degraded beside a FULL primary input would complete
				// the Slot as COMPLETED_WITH_PARTIAL_GAP with no cause. Nothing
				// produces that today: every producer of DECIDED_DEGRADED sets
				// it beside a PARTIAL or UNAVAILABLE primary, or beside a Level
				// outcome that decides another kind. No cause is minted for a
				// path without a producer; if one appears, its Slots count in
				// the shortfall from a full cause rate, which is where paths
				// nobody has identified yet belong.
				hasPartial = true
			}
			for _, outcome := range plan.LevelOutcomes {
				switch outcome.Outcome {
				case LevelOutcomeTerminal:
					hasTerminal = true
				case LevelOutcomeUnknown:
					hasUnavailable = true
					note(CauseLevelOutcomeUnknown, outcome.ReasonCode)
				}
			}
		default:
			return "", "", "", errors.New("alarmd execution: invalid Plan disposition for completion")
		}
	}
	switch {
	case hasTerminal:
		return CompletionTerminal, "", "", nil
	case hasUnavailable:
		return CompletionUnavailable, cause, reason, nil
	case hasPartial:
		return CompletionPartialGap, cause, reason, nil
	case allFullEmpty:
		return CompletionFullEmpty, "", "", nil
	default:
		return CompletionFull, "", "", nil
	}
}

// causeRank orders the causes by how much a human can do about them. A higher
// rank wins when a Slot hits several at once.
//
// The causes of the two kinds never compete for a Slot: an UNAVAILABLE
// completion always has one of the upper four noted, and a PARTIAL_GAP
// completion never has any of them. Ranking the partial causes strictly below
// DATA_NOT_READY is what keeps that true when the conditions of both kinds
// are noted on one Slot, which a partial primary beside a readiness gap is.
//
// DATA_NOT_READY is lowest of the upper four because nothing needs doing: it
// clears when the data lands. Everything above it is something that did not
// work. CONFIG_DRIFT is lowest of the partial pair for the same reason.
//
// PRIMARY_INPUT_UNAVAILABLE outranks PLAN_UNAVAILABLE because the streaming
// path marks a Plan unavailable precisely when its primary input was, so on
// every such Slot the two are noted together; reporting the Plan named the
// consequence and hid the cause an operator can act on, and on a running
// deployment every Slot whose query came back with nothing usable was listed
// as a Plan that could not be decided. A Plan unavailable for a reason of its
// own is still reported as such, because it is then the only cause noted.
func causeRank(cause CompletionCause) int {
	switch cause {
	case CausePrimaryInputUnavailable:
		return 6
	case CausePlanUnavailable:
		return 5
	case CauseLevelOutcomeUnknown:
		return 4
	case CauseDataNotReady:
		return 3
	case CausePrimaryInputPartial:
		return 2
	case CauseConfigDrift:
		return 1
	default:
		return 0
	}
}

type ProgressIdentity struct {
	QueryGroup QueryGroupIdentity
}

// ExecutionEvidenceKind is what a query-free completion learned about how far
// an earlier attempt at this Slot got.
type ExecutionEvidenceKind string

const (
	// EvidenceStateApplied is at least one due Plan whose state an earlier
	// attempt wrote. The events went out before the state did, so this also
	// means those Plans alerted.
	EvidenceStateApplied ExecutionEvidenceKind = "STATE_APPLIED"
	// EvidenceNoneFound is a mark that was read and was not there. It is the
	// ordinary case: a Slot that never got past its query leaves none.
	EvidenceNoneFound ExecutionEvidenceKind = "NONE_FOUND"
	// EvidenceUnreadable is a mark that could not be read, which is not the
	// same as one that is not there. The difference is the whole point of
	// having the value: "nothing ran" and "nobody could say" lead somewhere
	// different, and folding the second into the first is how a detection that
	// did happen gets recorded as one that never did.
	EvidenceUnreadable ExecutionEvidenceKind = "UNREADABLE"
)

// ExecutionEvidenceKinds is every value, for a metric to bound itself by.
var ExecutionEvidenceKinds = []ExecutionEvidenceKind{
	EvidenceStateApplied, EvidenceNoneFound, EvidenceUnreadable,
}

// The readings a counter takes of one completion's evidence.
//
// Four of them, and they are not the three kinds: STATE_APPLIED splits into
// "every Plan" and "some of them", which is the split the gap fold turns on and
// therefore the one a reader has to be able to see. It is derived here rather
// than added to the kinds, so Validate and the counter cannot end up with two
// vocabularies for one fact.
const (
	EvidenceReadingFullyApplied = "STATE_APPLIED"
	EvidenceReadingMixed        = "MIXED"
	EvidenceReadingNoneFound    = "NONE_FOUND"
	EvidenceReadingUnreadable   = "UNREADABLE"
	// EvidenceReadingAbsent is a completion carrying no evidence at all: a
	// build or a deployment without the port. It is a label rather than a
	// skipped observation, so the readings add up to the query-free
	// completions and a reader can check that instead of assuming it -- and so
	// a runtime that is not recording evidence says so here rather than by the
	// other three staying at zero, which is what "nothing has gone wrong"
	// looks like too.
	EvidenceReadingAbsent = "ABSENT"
)

// ExecutionEvidenceReadings is every label the counter may carry.
var ExecutionEvidenceReadings = []string{
	EvidenceReadingFullyApplied, EvidenceReadingMixed, EvidenceReadingNoneFound,
	EvidenceReadingUnreadable, EvidenceReadingAbsent,
}

// ReadEvidence names what a completion's evidence says, for the counter.
func ReadEvidence(evidence *ExecutionEvidence) string {
	if evidence == nil {
		return EvidenceReadingAbsent
	}
	switch evidence.Kind {
	case EvidenceStateApplied:
		if evidence.FullyApplied() {
			return EvidenceReadingFullyApplied
		}
		return EvidenceReadingMixed
	case EvidenceNoneFound:
		return EvidenceReadingNoneFound
	case EvidenceUnreadable:
		return EvidenceReadingUnreadable
	default:
		return EvidenceReadingAbsent
	}
}

// QueryFreeCompletionKinds is the two kinds this counter is partitioned by.
var QueryFreeCompletionKinds = []CompletionKind{CompletionGapSkipped, CompletionSnapshotUnavailable}

// ExecutionEvidence is what a query-free completion found out about an earlier
// attempt at the same Slot.
//
// A query-free completion happens when a Slot missed its replay window: it does
// not query, does not load state, and holds nothing but the frozen due Plans.
// So it cannot tell "this Slot was never evaluated" from "this Slot evaluated,
// sent its events, wrote its state, and then failed to write down that it had
// done so" -- and it used to record both as a gap, which is the second one
// recorded as the first.
//
// It appears only on the two query-free kinds. A completion that came from the
// query path is its own evidence.
type ExecutionEvidence struct {
	Kind ExecutionEvidenceKind
	// PlansApplied is how many of PlansTotal an earlier attempt got to. Both
	// are carried rather than a ratio or a flag: a Slot that applied three of
	// ten Plans is a different situation from one that applied ten, and the
	// fold below treats them differently.
	PlansApplied int
	PlansTotal   int
}

// Validate checks one reading of an earlier attempt against itself.
func (evidence ExecutionEvidence) Validate() error {
	if evidence.PlansApplied < 0 || evidence.PlansTotal < 0 {
		return errors.New("alarmd execution: execution evidence counts must not be negative")
	}
	if evidence.PlansApplied > evidence.PlansTotal {
		return errors.New("alarmd execution: execution evidence applied more Plans than the Slot had")
	}
	switch evidence.Kind {
	case EvidenceStateApplied:
		if evidence.PlansApplied < 1 {
			return errors.New("alarmd execution: STATE_APPLIED evidence names no Plan")
		}
	case EvidenceNoneFound, EvidenceUnreadable:
		if evidence.PlansApplied != 0 {
			return fmt.Errorf("alarmd execution: %s evidence names %d applied Plans",
				evidence.Kind, evidence.PlansApplied)
		}
	default:
		return fmt.Errorf("alarmd execution: unknown execution evidence kind %q", evidence.Kind)
	}
	return nil
}

// FullyApplied reports whether an earlier attempt got to every Plan this Slot
// was going to evaluate.
//
// It is the one question the gap fold asks, and it lives here so the fold and
// the page cannot answer it differently. Anything less than all of them is a
// partial execution: some Plans really were not evaluated, and the Slot still
// owes them a gap.
func (evidence ExecutionEvidence) FullyApplied() bool {
	return evidence.Kind == EvidenceStateApplied && evidence.PlansTotal > 0 &&
		evidence.PlansApplied == evidence.PlansTotal
}

// SlotExecutionEvidenceStore records which Plans of a Slot an attempt applied,
// and reads it back for a completion that cannot know otherwise.
//
// Both halves are best-effort by construction. Recording failure costs a later
// completion its evidence and must not change what the failing attempt reports;
// a read failure is UNREADABLE and must not stop a Slot that has already missed
// its window from finishing.
type SlotExecutionEvidenceStore interface {
	Record(ctx context.Context, slot SlotIdentity, duePlans []PlanIdentity,
		applied []PlanIdentity, recoveryUntil time.Time, now time.Time) error
	Read(ctx context.Context, slot SlotIdentity, duePlans []PlanIdentity) (ExecutionEvidence, error)
}

type SlotCompletion struct {
	Contract   FrozenExecutionContractRef
	Kind       CompletionKind
	Primary    *PrimaryInputFact
	Result     Result
	ReasonCode ReasonCode
	// Evidence is how far an earlier attempt at this Slot got, and is set only
	// on the two query-free kinds. Nil is a real state and not an omission to
	// be worked around: a completion written by a build from before this
	// existed has none, and the fold treats that exactly as it did before.
	Evidence *ExecutionEvidence
}

type ProgressCommitRequest struct {
	Identity         ProgressIdentity
	OwnerFence       OwnerFence
	ExpectedNextSlot EvaluationTime
	Completion       SlotCompletion
	Projection       UnfinishedSlotProjection
	// ContentScope is declared to the fence of this write; see
	// SlotExecutionRequest.ContentScope.
	ContentScope string
}

func (request ProgressCommitRequest) Validate() error {
	if request.Identity.QueryGroup == "" {
		return errors.New("alarmd execution: complete Progress identity is required")
	}
	if err := request.OwnerFence.Validate(request.Completion.Contract); err != nil {
		return err
	}
	if request.Identity.QueryGroup != request.Completion.Contract.Slot.QueryGroup ||
		request.ExpectedNextSlot != request.Completion.Contract.Slot.EvaluationTime {
		return errors.New("alarmd execution: Progress request does not match the completed Slot")
	}
	if !request.Projection.IsZero() {
		if err := request.Projection.Validate(); err != nil {
			return err
		}
		if request.Projection.Contract != request.Completion.Contract {
			return errors.New("alarmd execution: Progress projection does not match the completed Slot")
		}
	}
	if err := request.Completion.Contract.Validate(); err != nil {
		return err
	}
	if err := ValidateResultReason(request.Completion.Result, request.Completion.ReasonCode); err != nil {
		return err
	}
	if request.Completion.Kind == CompletionGapSkipped || request.Completion.Kind == CompletionSnapshotUnavailable {
		if request.Completion.Primary != nil || request.Completion.Result != observability.ResultDegraded {
			return errors.New("alarmd execution: query-free completion requires no PRIMARY and a degraded result")
		}
		expectedReason := ReasonCode(contract.ReasonGapSkipped)
		if request.Completion.Kind == CompletionSnapshotUnavailable {
			expectedReason = ReasonCode(contract.ReasonSnapshotUnavailable)
		}
		if request.Completion.ReasonCode != expectedReason {
			return errors.New("alarmd execution: query-free completion requires its exact reason")
		}
		if evidence := request.Completion.Evidence; evidence != nil {
			if err := evidence.Validate(); err != nil {
				return err
			}
		}
		return nil
	}
	if request.Completion.Evidence != nil {
		// A completion that came from the query path is its own evidence: it
		// queried, it evaluated, and it is saying so. Carrying a reading of an
		// earlier attempt there would be a second answer to a question already
		// answered, and the fold would have two places to look.
		return fmt.Errorf("alarmd execution: %s completion carries execution evidence, which only a "+
			"query-free completion may", request.Completion.Kind)
	}
	if request.Completion.Primary == nil {
		return errors.New("alarmd execution: business completion requires PRIMARY facts")
	}
	switch request.Completion.Primary.Completeness {
	case CompletenessFull, CompletenessPartial:
		if request.Completion.Primary.DataState != DataStateData && request.Completion.Primary.DataState != DataStateEmpty {
			return errors.New("alarmd execution: available PRIMARY completion requires DATA or EMPTY")
		}
	case CompletenessUnavailable:
		if request.Completion.Primary.DataState != DataStateUnknown {
			return errors.New("alarmd execution: unavailable PRIMARY completion requires UNKNOWN")
		}
	default:
		return errors.New("alarmd execution: Progress request requires PRIMARY completeness")
	}
	switch request.Completion.Kind {
	case CompletionFull:
		if request.Completion.Primary.Completeness != CompletenessFull || request.Completion.Primary.DataState != DataStateData {
			return errors.New("alarmd execution: FULL completion requires FULL+DATA PRIMARY")
		}
		if request.Completion.Result != observability.ResultSuccess {
			return errors.New("alarmd execution: FULL completion requires a successful result")
		}
	case CompletionFullEmpty:
		if request.Completion.Primary.Completeness != CompletenessFull || request.Completion.Primary.DataState != DataStateEmpty {
			return errors.New("alarmd execution: FULL_EMPTY completion requires FULL+EMPTY PRIMARY")
		}
		if request.Completion.Result != observability.ResultSuccess {
			return errors.New("alarmd execution: FULL_EMPTY completion requires a successful result")
		}
	case CompletionPartialGap:
		if request.Completion.Primary.Completeness == CompletenessUnavailable {
			return errors.New("alarmd execution: PARTIAL completion cannot hide unavailable PRIMARY")
		}
		if request.Completion.Result != observability.ResultDegraded {
			return errors.New("alarmd execution: PARTIAL completion requires a degraded result")
		}
		if err := requireReasonClass(request.Completion.ReasonCode, contract.ReasonClassCoverage); err != nil {
			return err
		}
	case CompletionUnavailable:
		if request.Completion.Result != observability.ResultDegraded {
			return errors.New("alarmd execution: UNAVAILABLE completion requires a degraded result")
		}
		if err := requireReasonClass(request.Completion.ReasonCode, contract.ReasonClassCoverage); err != nil {
			return err
		}
	case CompletionTerminal:
		if request.Completion.Result != observability.ResultTerminal {
			return errors.New("alarmd execution: TERMINAL completion requires a terminal result")
		}
		if err := requireReasonClass(request.Completion.ReasonCode, contract.ReasonClassDeterministic); err != nil {
			return err
		}
	default:
		return errors.New("alarmd execution: invalid completion kind")
	}
	return nil
}

type ProgressCommitStatus string

const (
	ProgressCommitted   ProgressCommitStatus = "COMMITTED"
	ProgressConflict    ProgressCommitStatus = "CONFLICT"
	ProgressStaleOwner  ProgressCommitStatus = "STALE_OWNER"
	ProgressRetryableIO ProgressCommitStatus = "RETRYABLE_IO"
)

type ScheduleProgress struct {
	Identity           ProgressIdentity
	NextSlot           EvaluationTime
	LastFullSlot       EvaluationTime
	LastCompletionKind CompletionKind
	CurrentOrRecentGap *ProgressGapSummary
	UnfinishedSlot     *UnfinishedSlotProjection
	UnfinishedRange    *ExpiredRangeProjectionV1 `json:",omitempty"`
	// LastCompletion is what the round that last moved this cursor concluded.
	//
	// LastCompletionKind above is one word of it and was all there was: a
	// reader with a Query Group that stopped could see that the last round
	// ended in a gap and not which Slot it was, when it happened, why, or
	// against which frozen contract -- and the round itself had scrolled out
	// of the log by the time anyone looked. Every field here was already in
	// hand at the commit, so carrying them costs no read.
	//
	// Nil on a record written before this field existed. That is a real
	// answer -- this process has not committed a round for that Query Group
	// since the upgrade -- and it is why the field is a pointer rather than a
	// zero-valued struct that would read as a completion at Slot zero.
	LastCompletion *LastCompletionSummary `json:"last_completion,omitempty"`
}

// LastCompletionSummary is one committed round, as the commit already knew it.
//
// The names are the wire contract the page reads by and are fixed; a field
// renamed here is a field the page stops finding, with nothing failing to say
// so.
type LastCompletionSummary struct {
	// Slot is the evaluation time the round completed, not the one it moved to.
	Slot EvaluationTime `json:"slot"`
	// CompletedAt is when this process committed it, RFC3339. It is the
	// commit's clock rather than the Slot's, because the question it answers is
	// "how long ago did anything happen here", and a Slot time answers that
	// only for a deployment that is keeping up -- which is not the deployment
	// anybody is looking at when they ask.
	CompletedAt string `json:"completed_at"`
	// Kind and ReasonCode are how it ended and why, the same two the round
	// reported. The reason is empty for a completion that had none.
	Kind       CompletionKind `json:"kind"`
	ReasonCode ReasonCode     `json:"reason_code,omitempty"`
	// Contract is the frozen contract the round ran under, verbatim. It is
	// what makes the summary checkable against anything else that names the
	// same Slot: a summary carrying a Slot number and no contract cannot be
	// told from one written by a different generation of the same Query Group.
	Contract FrozenExecutionContractRef `json:"contract"`
}

type UnfinishedSlotProjection struct {
	Contract                       FrozenExecutionContractRef
	DuePlanTargets                 FrozenDuePlanTargets
	EarliestQueryDeadlineUnixMilli int64
	KeepUntilUnixMilli             int64
	// ContentScope is the content the Slot was begun under
	// (SlotExecutionRequest.ContentScope), kept so a retry from this
	// projection declares the same content the first attempt did. It is
	// metadata of the projection, like a Segment's ObjectDigest is of the
	// Segment: it takes no part in Equal, so a projection begun by a binary
	// that did not write it is still the same unfinished Slot. Omitted when
	// empty, so records written before it existed re-encode unchanged.
	ContentScope string `json:",omitempty"`
}

func (projection UnfinishedSlotProjection) Validate() error {
	if err := projection.Contract.Validate(); err != nil {
		return err
	}
	if err := projection.DuePlanTargets.Validate(projection.Contract); err != nil {
		return err
	}
	if projection.EarliestQueryDeadlineUnixMilli <= int64(projection.Contract.Slot.EvaluationTime)*1000 ||
		projection.KeepUntilUnixMilli <= projection.EarliestQueryDeadlineUnixMilli {
		return errors.New("alarmd execution: invalid unfinished Slot projection boundaries")
	}
	return nil
}

func (projection UnfinishedSlotProjection) IsZero() bool {
	return projection.Contract == (FrozenExecutionContractRef{}) && projection.DuePlanTargets.DuePlanSetDigest == "" &&
		len(projection.DuePlanTargets.Plans) == 0 && projection.EarliestQueryDeadlineUnixMilli == 0 && projection.KeepUntilUnixMilli == 0
}

func (projection UnfinishedSlotProjection) Equal(other UnfinishedSlotProjection) bool {
	return projection.Contract == other.Contract &&
		projection.EarliestQueryDeadlineUnixMilli == other.EarliestQueryDeadlineUnixMilli &&
		projection.KeepUntilUnixMilli == other.KeepUntilUnixMilli &&
		projection.DuePlanTargets.Equal(other.DuePlanTargets)
}

type ProgressBeginRequest struct {
	Identity   ProgressIdentity
	OwnerFence OwnerFence
	Projection UnfinishedSlotProjection
	// ContentScope is declared to the fence of this write; see
	// SlotExecutionRequest.ContentScope.
	ContentScope string
}

type ProgressBeginResult struct {
	Status     ProgressCommitStatus
	ReasonCode ReasonCode
}

func (result ProgressBeginResult) Validate() error {
	return (ProgressCommitResult(result)).Validate()
}

type ProgressLoadStatus string

const (
	ProgressFound   ProgressLoadStatus = "FOUND"
	ProgressMissing ProgressLoadStatus = "MISSING"
)

type ProgressLoadResult struct {
	Status   ProgressLoadStatus
	Progress *ScheduleProgress
}

func (result ProgressLoadResult) Validate(identity ProgressIdentity) error {
	if identity.QueryGroup == "" {
		return errors.New("alarmd execution: complete Progress identity is required")
	}
	switch result.Status {
	case ProgressFound:
		if result.Progress == nil || result.Progress.Identity != identity {
			return errors.New("alarmd execution: FOUND Progress must match its identity")
		}
		return result.Progress.Validate()
	case ProgressMissing:
		if result.Progress != nil {
			return errors.New("alarmd execution: MISSING Progress must not carry persisted facts")
		}
		return nil
	default:
		return errors.New("alarmd execution: unknown Progress load status")
	}
}

type ProgressGapSummary struct {
	Kind       CompletionKind
	ReasonCode ReasonCode
	FirstSlot  EvaluationTime
	LastSlot   EvaluationTime
	// Count is how many Slots the gap spans, folded one per Slot as consecutive
	// completions of the same kind arrive.
	//
	// It is zero, and Uncounted true, for the one gap whose Slots cannot be
	// counted: a cursor moved past a pruned part of the Schedule timeline skips
	// the Slots that were in the pruned segments, and those segments are the
	// only thing that could have said how many. The span in time is still known
	// -- FirstSlot to LastSlot -- so the gap is not shapeless, only uncounted.
	//
	// It used to carry 1 there, meaning "one skip event". Count means Slots
	// everywhere else, so any reader adding these up, or comparing one gap
	// against another, read an unknown number of never-evaluated Slots as one.
	// A quantity that stands for "unknown" inside a field whose other values are
	// counts is read as a count by everything that does not know better, and
	// nothing in the type said which this was.
	Count uint32
	// Uncounted says Count could not be established, as opposed to being zero.
	// Absent it, a reader has no way to tell a gap of no Slots from a gap whose
	// Slots nobody can name -- and the second is the more serious of the two.
	Uncounted bool
	// ResumedAt is where the cursor jumped to, for an uncounted gap only. It is
	// not a Slot that was skipped and so cannot be LastSlot: it is the first one
	// the timeline still holds, and the Progress will evaluate it.
	//
	// It is here because without it the only two Slot times on the summary are
	// both the old cursor, and a span whose ends are equal reads as a single
	// instant -- indistinguishable from a gap of one Slot, which is exactly the
	// reading Count used to give as well. With it, the extent is stated even
	// though the population inside it cannot be.
	ResumedAt   EvaluationTime
	NextProbeAt *int64
	// Evidence is what the completion that opened or extended this gap knew
	// about an earlier attempt at the same Slot. It is persisted with the gap
	// so the page reads it from here rather than inferring it from a tracker
	// that only sees what is happening now: the gap outlives the round.
	//
	// Nil for every gap this build did not put evidence on, which is every kind
	// but the two query-free ones and every gap written before this existed.
	Evidence *ExecutionEvidence
}

func (progress ScheduleProgress) Validate() error {
	if progress.UnfinishedRange != nil {
		if progress.UnfinishedSlot != nil || progress.UnfinishedRange.First.Contract.Slot.QueryGroup != progress.Identity.QueryGroup ||
			progress.UnfinishedRange.First.Contract.Slot.EvaluationTime != progress.NextSlot {
			return errors.New("alarmd execution: expired range does not match exclusive Progress pending")
		}
		if err := progress.UnfinishedRange.Validate(); err != nil {
			return err
		}
	}
	if progress.Identity.QueryGroup == "" || progress.NextSlot <= 0 {
		return errors.New("alarmd execution: incomplete Schedule Progress")
	}
	if progress.LastFullSlot < 0 || progress.LastFullSlot >= progress.NextSlot {
		return errors.New("alarmd execution: invalid last FULL Slot")
	}
	if progress.LastCompletionKind != "" && !validCompletionKind(progress.LastCompletionKind) {
		return errors.New("alarmd execution: invalid last completion kind")
	}
	if progress.UnfinishedSlot != nil {
		if err := progress.UnfinishedSlot.Validate(); err != nil {
			return err
		}
		if progress.UnfinishedSlot.Contract.Slot.QueryGroup != progress.Identity.QueryGroup ||
			progress.UnfinishedSlot.Contract.Slot.EvaluationTime != progress.NextSlot {
			return errors.New("alarmd execution: unfinished Slot projection does not match Progress")
		}
	}
	if progress.CurrentOrRecentGap == nil {
		return nil
	}
	gap := progress.CurrentOrRecentGap
	if gap.Kind != CompletionPartialGap && gap.Kind != CompletionUnavailable && gap.Kind != CompletionTerminal &&
		gap.Kind != CompletionGapSkipped && gap.Kind != CompletionSnapshotUnavailable {
		return errors.New("alarmd execution: invalid Progress gap summary kind")
	}
	if gap.Kind == CompletionTerminal {
		if err := requireReasonClass(gap.ReasonCode, contract.ReasonClassDeterministic); err != nil {
			return err
		}
	} else if err := requireReasonClass(gap.ReasonCode, contract.ReasonClassCoverage); err != nil {
		return err
	}
	// Counted and uncounted are the two shapes, and a summary has to be exactly
	// one of them. A count of zero without Uncounted is a gap that spans no
	// Slots, which the span above has just said is not the case; a count beside
	// Uncounted is a number claiming to be what the segments that could have
	// produced it no longer exist to say.
	if (gap.Count == 0) != gap.Uncounted {
		return errors.New("alarmd execution: Progress gap summary must either count its Slots or say it cannot")
	}
	if gap.FirstSlot <= 0 || gap.LastSlot < gap.FirstSlot || gap.LastSlot >= progress.NextSlot ||
		(gap.NextProbeAt != nil && *gap.NextProbeAt <= 0) {
		return errors.New("alarmd execution: invalid bounded Progress gap summary")
	}
	return nil
}

func validCompletionKind(kind CompletionKind) bool {
	switch kind {
	case CompletionFull, CompletionFullEmpty, CompletionPartialGap, CompletionUnavailable, CompletionTerminal,
		CompletionGapSkipped, CompletionSnapshotUnavailable:
		return true
	default:
		return false
	}
}

type ProgressCommitResult struct {
	Status     ProgressCommitStatus
	ReasonCode ReasonCode
}

func (result ProgressCommitResult) Validate() error {
	switch result.Status {
	case ProgressCommitted:
		return ValidateResultReason(observability.ResultSuccess, result.ReasonCode)
	case ProgressRetryableIO:
		return requireReasonClass(result.ReasonCode, contract.ReasonClassRetryable)
	case ProgressConflict, ProgressStaleOwner:
		return requireReasonNone(result.ReasonCode)
	default:
		return errors.New("alarmd execution: unknown Progress commit status")
	}
}

func requireReasonNone(reason ReasonCode) error {
	return ValidateResultReason(observability.ResultSuccess, reason)
}

func requireReasonClass(reason ReasonCode, expected contract.ReasonClassV2) error {
	if err := ValidateResultReason(observability.ResultFailed, reason); err != nil {
		return err
	}
	definition, ok := contract.LookupReasonV2(string(reason))
	if !ok || definition.Class != expected {
		return fmt.Errorf("alarmd execution: reason %s has the wrong class", reason)
	}
	return nil
}

// QueryAvailability is non-persistent evidence from a validated query execution.
// The zero value means that this result cannot establish query health.
type QueryAvailability uint8

const (
	QueryAvailabilityUnknown QueryAvailability = iota
	QueryAvailabilityAvailable
	// Every PRIMARY query was unavailable due to source_backend, with no
	// usable primary stream. Local admission/unknown failures are not included.
	QueryAvailabilityUnavailable
)

type SlotExecutionResult struct {
	// Set only after successful Progress commit for the unchanged configuration.
	QueryAvailability QueryAvailability
	// Set only after a successful Progress commit, not inferred from Result.
	CompletionKind CompletionKind
	Completed      bool
	Result         Result
	ReasonCode     ReasonCode
	SourceRetry    bool
}
