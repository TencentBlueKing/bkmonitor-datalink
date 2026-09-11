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
	"errors"
	"fmt"

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

func ClassifyStateMutation(view RuntimeStateView, mutation StateMutation) StatePreflightDisposition {
	if mutation.ExpectedBlobRevision != view.BlobRevision {
		return StateVersionConflict
	}
	switch view.VersionComparison {
	case ApplyVersionPersistedOlder:
		return StateProceed
	case ApplyVersionPersistedNewer:
		return StateStaleVersion
	case ApplyVersionEqual:
		if view.PersistedMutationDigest != mutation.MutationDigest {
			return StateVersionConflict
		}
		return StateAlreadyApplied
	default:
		return StateVersionConflict
	}
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

type PlanEvaluationResult struct {
	Plan              PlanIdentity
	Disposition       PlanDisposition
	ReasonCode        ReasonCode
	LevelOutcomes     []LevelOutcome
	GuardBeforeEvents []PlanGapMutation
	StateResults      []StateEvaluation
	GuardAfterState   []PlanGapMutation
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
	if !found || due.CompiledPlan == nil || len(request.Inputs) != len(due.CompiledPlan.Levels()) {
		return InternalExecution{}, errors.New("alarmd execution: evaluation inputs do not exactly cover one due Plan")
	}
	input := InternalExecution{Contract: request.Header.Contract, DuePlans: []DuePlan{due}}
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
		if err := validateLoadedGapLevels(plan, request.Gaps); err != nil {
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
	for _, item := range gaps.Items {
		if item.Identity.Plan != plan.Plan {
			continue
		}
		switch item.Status {
		case GapTerminal:
			terminalReasons[item.ReasonCode] = struct{}{}
		case GapUnavailable:
			unavailableReasons[item.ReasonCode] = struct{}{}
		}
	}
	for _, item := range states.Items {
		if item.Identity.Plan == plan.Plan && item.Status == StateRetryableIO {
			unavailableReasons[item.ReasonCode] = struct{}{}
		}
	}
	if len(terminalReasons) > 0 {
		if plan.Disposition != PlanTerminal && plan.Disposition != PlanRetryPending {
			return errors.New("alarmd execution: terminal State/Gap load cannot be ignored")
		}
		if plan.Disposition == PlanTerminal {
			if _, ok := terminalReasons[plan.ReasonCode]; !ok {
				return errors.New("alarmd execution: terminal Plan reason does not match State/Gap load")
			}
		}
		if len(unavailableReasons) == 0 {
			return nil
		}
	}
	if len(unavailableReasons) > 0 {
		if plan.Disposition != PlanRetryPending {
			return errors.New("alarmd execution: retryable State/Gap load requires retry-pending Plan")
		}
		if _, ok := unavailableReasons[plan.ReasonCode]; !ok {
			return errors.New("alarmd execution: retry-pending Plan reason does not match State/Gap load")
		}
	} else if plan.Disposition == PlanRetryPending {
		return errors.New("alarmd execution: retry-pending Plan lacks a retryable State/Gap load")
	}
	return nil
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

func validateLoadedGapLevels(plan DuePlan, gaps GapLoadResult) error {
	for _, item := range gaps.Items {
		if item.Identity.Plan != plan.Identity {
			continue
		}
		for _, scope := range item.Scopes {
			if scope.Scope.HasLevel && !compiledPlanHasLevel(plan.CompiledPlan, scope.Scope.LevelID) {
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
		if scope.Scope.HasLevel && !compiledPlanHasLevel(plan.CompiledPlan, scope.Scope.LevelID) {
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

// UnavailableCause names which of the several conditions that all complete a
// Slot as UNAVAILABLE actually occurred.
//
// The completion kind folds four different things into one word, and they call
// for opposite responses: data that has not landed in storage yet resolves
// itself, while a Plan that could not be decided does not. Operators reading a
// list of hundreds of "degraded" objects could not tell which was which, so the
// list was not actionable and taught them to ignore it.
//
// This is carried as observation only. Putting it on the persisted completion
// would change how consecutive gaps fold into a Progress gap summary, which is
// a durable structure and a separate decision.
type UnavailableCause string

const (
	// CauseDataNotReady is a readiness gap: the data for this evaluation has
	// not arrived in storage yet. It resolves without anyone doing anything,
	// and it is the majority of what the page currently shows as degraded.
	CauseDataNotReady UnavailableCause = "DATA_NOT_READY"
	// CausePlanUnavailable is a Plan that could not be decided at all.
	CausePlanUnavailable UnavailableCause = "PLAN_UNAVAILABLE"
	// CausePrimaryInputUnavailable is the query for the primary input coming
	// back with nothing usable.
	CausePrimaryInputUnavailable UnavailableCause = "PRIMARY_INPUT_UNAVAILABLE"
	// CauseLevelOutcomeUnknown is a Level whose outcome could not be determined
	// even though its Plan was.
	CauseLevelOutcomeUnknown UnavailableCause = "LEVEL_OUTCOME_UNKNOWN"
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
func DeriveCompletion(input InternalExecution, result EvaluationResult) (CompletionKind, UnavailableCause, error) {
	return deriveCompletion(input, result)
}

func deriveCompletion(input InternalExecution, result EvaluationResult) (CompletionKind, UnavailableCause, error) {
	if len(result.Plans) == 0 {
		return "", "", errors.New("alarmd execution: no Plan results to complete")
	}
	primary, err := DerivePrimaryInputFact(input)
	if err != nil {
		return "", "", err
	}
	// A Slot can hit several of these at once. The cause reported is the most
	// actionable one rather than the first or the commonest: a readiness gap
	// beside a Plan that could not be decided is a Slot someone should look at,
	// and reporting the gap would say the opposite.
	cause := UnavailableCause("")
	note := func(candidate UnavailableCause) {
		if causeRank(candidate) > causeRank(cause) {
			cause = candidate
		}
	}
	hasPartial := primary.Completeness == CompletenessPartial
	hasUnavailable := primary.Completeness == CompletenessUnavailable
	if hasUnavailable {
		note(CausePrimaryInputUnavailable)
	}
	allFullEmpty := primary.Completeness == CompletenessFull && primary.DataState == DataStateEmpty
	hasTerminal := false
	for _, plan := range result.Plans {
		switch plan.Disposition {
		case PlanTerminal:
			hasTerminal = true
		case PlanUnavailable:
			hasUnavailable = true
			note(CausePlanUnavailable)
		case PlanReadinessGap:
			hasUnavailable = true
			note(CauseDataNotReady)
		case PlanRetryPending:
			return "", "", errors.New("alarmd execution: retry-pending Plan cannot derive a completed Slot")
		case PlanDecided, PlanDecidedDegraded:
			if plan.Disposition == PlanDecidedDegraded {
				hasPartial = true
			}
			for _, outcome := range plan.LevelOutcomes {
				switch outcome.Outcome {
				case LevelOutcomeTerminal:
					hasTerminal = true
				case LevelOutcomeUnknown:
					hasUnavailable = true
					note(CauseLevelOutcomeUnknown)
				}
			}
		default:
			return "", "", errors.New("alarmd execution: invalid Plan disposition for completion")
		}
	}
	switch {
	case hasTerminal:
		return CompletionTerminal, "", nil
	case hasUnavailable:
		return CompletionUnavailable, cause, nil
	case hasPartial:
		return CompletionPartialGap, "", nil
	case allFullEmpty:
		return CompletionFullEmpty, "", nil
	default:
		return CompletionFull, "", nil
	}
}

// causeRank orders the causes by how much a human can do about them. A higher
// rank wins when a Slot hits several at once.
//
// DATA_NOT_READY is lowest because nothing needs doing: it clears when the data
// lands. Everything above it is something that did not work.
func causeRank(cause UnavailableCause) int {
	switch cause {
	case CausePlanUnavailable:
		return 4
	case CausePrimaryInputUnavailable:
		return 3
	case CauseLevelOutcomeUnknown:
		return 2
	case CauseDataNotReady:
		return 1
	default:
		return 0
	}
}

type ProgressIdentity struct {
	QueryGroup QueryGroupIdentity
}

type SlotCompletion struct {
	Contract   FrozenExecutionContractRef
	Kind       CompletionKind
	Primary    *PrimaryInputFact
	Result     Result
	ReasonCode ReasonCode
}

type ProgressCommitRequest struct {
	Identity         ProgressIdentity
	OwnerFence       OwnerFence
	ExpectedNextSlot EvaluationTime
	Completion       SlotCompletion
	Projection       UnfinishedSlotProjection
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
		return nil
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
}

type UnfinishedSlotProjection struct {
	Contract                       FrozenExecutionContractRef
	DuePlanTargets                 FrozenDuePlanTargets
	EarliestQueryDeadlineUnixMilli int64
	KeepUntilUnixMilli             int64
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
	Kind        CompletionKind
	ReasonCode  ReasonCode
	FirstSlot   EvaluationTime
	LastSlot    EvaluationTime
	Count       uint32
	NextProbeAt *int64
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
	if gap.FirstSlot <= 0 || gap.LastSlot < gap.FirstSlot || gap.LastSlot >= progress.NextSlot || gap.Count == 0 ||
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
