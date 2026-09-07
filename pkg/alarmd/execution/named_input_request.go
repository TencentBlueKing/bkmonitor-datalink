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
	"fmt"
	"reflect"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// SeriesEvaluationInputRequest is the side-effect-free, series-local input to
// one compiled Level. Frozen static facts remain in the shared header; this
// request carries only the named immutable views consumed by Evaluation.
type SeriesEvaluationInputRequest struct {
	Contract       FrozenExecutionContractRef
	Consumer       ConsumerRef
	SeriesIdentity SeriesIdentityDigest
	RequirementIDs []RequirementID
	Inputs         []NamedInputBinding
}

// SeriesEvaluationInputBuilder holds one validated, immutable Slot-local
// named-input index. Its zero value rejects all operations.
type SeriesEvaluationInputBuilder struct {
	contract  FrozenExecutionContractRef
	consumers map[ConsumerRef]preparedSeriesEvaluationConsumer
	queries   map[PhysicalQueryDigest]PlannedPhysicalQueryRef
}

type preparedSeriesEvaluationConsumer struct {
	requirements []DataRequirement
	byID         map[RequirementID]DataRequirement
}

// EvaluationInputContractError keeps an invalid input set local to the
// affected Plan/Level/series. It does not authorize Event, State or Progress.
type EvaluationInputContractError struct {
	Consumer       ConsumerRef
	SeriesIdentity SeriesIdentityDigest
	err            error
}

func (err *EvaluationInputContractError) Error() string {
	return fmt.Sprintf("alarmd execution: invalid evaluation input for plan %s level %d series %s: %v",
		err.Consumer.Plan.StrategyID, err.Consumer.LevelID, err.SeriesIdentity, err.err)
}

func (err *EvaluationInputContractError) Unwrap() error { return err.err }

// BuildSeriesEvaluationInputRequest derives the expected exact set from the
// frozen header. The caller supplies only completed consumer bindings.
func BuildSeriesEvaluationInputRequest(
	header InternalExecutionHeader,
	consumer ConsumerRef,
	series SeriesIdentityDigest,
	bindings []NamedInputBinding,
	completions []PhysicalQueryCompletion,
) (SeriesEvaluationInputRequest, error) {
	builder, err := PrepareSeriesEvaluationInputBuilder(header)
	if err != nil {
		return SeriesEvaluationInputRequest{}, scopedEvaluationInputError(consumer, series, err)
	}
	return builder.Build(consumer, series, bindings, completions)
}

// PrepareSeriesEvaluationInputBuilder validates and indexes the frozen header
// once for one Slot-local streamed execution.
func PrepareSeriesEvaluationInputBuilder(header InternalExecutionHeader) (*SeriesEvaluationInputBuilder, error) {
	if err := header.Validate(header.Contract); err != nil {
		return nil, err
	}
	builder := &SeriesEvaluationInputBuilder{
		contract:  header.Contract,
		consumers: make(map[ConsumerRef]preparedSeriesEvaluationConsumer),
		queries:   make(map[PhysicalQueryDigest]PlannedPhysicalQueryRef, len(header.RequiredPhysicalQueries)),
	}
	for _, due := range header.DuePlans {
		for _, level := range due.CompiledPlan.Levels() {
			builder.consumers[ConsumerRef{Plan: due.Identity, LevelID: level.Definition().LevelID, HasLevel: true}] = preparedSeriesEvaluationConsumer{}
		}
	}
	for _, requirement := range header.Requirements {
		for _, consumer := range requirement.Consumers {
			prepared, due := builder.consumers[consumer.Consumer]
			if due {
				prepared.requirements = append(prepared.requirements, cloneBuilderDataRequirement(requirement))
				builder.consumers[consumer.Consumer] = prepared
			}
		}
	}
	for consumer, prepared := range builder.consumers {
		sort.Slice(prepared.requirements, func(i, j int) bool {
			if prepared.requirements[i].Role != prepared.requirements[j].Role {
				return prepared.requirements[i].Role == InputRolePrimary
			}
			return prepared.requirements[i].RequirementID < prepared.requirements[j].RequirementID
		})
		prepared.byID = make(map[RequirementID]DataRequirement, len(prepared.requirements))
		datasetNames := make(map[DatasetName]struct{}, len(prepared.requirements))
		primaryCount := 0
		for _, requirement := range prepared.requirements {
			if _, duplicate := prepared.byID[requirement.RequirementID]; duplicate {
				return nil, errors.New("duplicate frozen requirement identity")
			}
			if _, duplicate := datasetNames[requirement.DatasetName]; duplicate {
				return nil, errors.New("duplicate frozen named-input dataset")
			}
			prepared.byID[requirement.RequirementID] = requirement
			datasetNames[requirement.DatasetName] = struct{}{}
			if requirement.Role == InputRolePrimary {
				primaryCount++
			}
		}
		if len(prepared.requirements) == 0 {
			return nil, errors.New("consumer has no frozen DataRequirement")
		}
		if primaryCount != 1 {
			return nil, errors.New("one frozen PRIMARY named input is required")
		}
		builder.consumers[consumer] = prepared
	}
	for _, query := range header.RequiredPhysicalQueries {
		builder.queries[query.Digest] = query
	}
	return builder, nil
}

func (builder *SeriesEvaluationInputBuilder) Build(
	consumer ConsumerRef,
	series SeriesIdentityDigest,
	bindings []NamedInputBinding,
	completions []PhysicalQueryCompletion,
) (SeriesEvaluationInputRequest, error) {
	if builder == nil || builder.consumers == nil {
		return SeriesEvaluationInputRequest{}, scopedEvaluationInputError(consumer, series, errors.New("unprepared named-input builder"))
	}
	request, err := buildSeriesEvaluationInputRequest(builder, consumer, series, bindings, completions)
	if err != nil {
		return SeriesEvaluationInputRequest{}, scopedEvaluationInputError(consumer, series, err)
	}
	return request, nil
}

// ValidateCompletionOnly validates one Plan Level's exact no-series binding
// set against the same frozen requirements and physical completions as Build.
func (builder *SeriesEvaluationInputBuilder) ValidateCompletionOnly(
	consumer ConsumerRef,
	bindings []NamedInputBinding,
	completions []PhysicalQueryCompletion,
) error {
	if builder == nil || builder.consumers == nil {
		return scopedEvaluationInputError(consumer, "", errors.New("unprepared named-input builder"))
	}
	prepared, actualByID, err := validateNamedInputExactSet(builder, consumer, "", bindings, completions)
	if err != nil {
		return scopedEvaluationInputError(consumer, "", err)
	}
	for _, requirement := range prepared.requirements {
		binding := actualByID[requirement.RequirementID]
		if binding.ImpactScope == ImpactSeries {
			return scopedEvaluationInputError(consumer, "", errors.New("completion-only named input cannot use series impact scope"))
		}
		if binding.Completeness != CompletenessUnavailable &&
			(binding.DataState != DataStateEmpty || binding.Dataset == nil || binding.View == nil || binding.View.Len() != 0) {
			return scopedEvaluationInputError(consumer, "", errors.New("completion-only available input must carry an empty dataset view"))
		}
	}
	return nil
}

// Validate replays the contract against authoritative completion facts kept by
// the Slot session; request-local facts cannot self-authorize a completion.
func (request SeriesEvaluationInputRequest) Validate(
	header InternalExecutionHeader,
	completions []PhysicalQueryCompletion,
) error {
	builder, err := PrepareSeriesEvaluationInputBuilder(header)
	if err != nil {
		return scopedEvaluationInputError(request.Consumer, request.SeriesIdentity, err)
	}
	rebuilt, err := builder.Build(request.Consumer, request.SeriesIdentity, request.Inputs, completions)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(request, rebuilt) {
		return scopedEvaluationInputError(request.Consumer, request.SeriesIdentity,
			errors.New("request differs from frozen named-input facts"))
	}
	return nil
}

func buildSeriesEvaluationInputRequest(
	builder *SeriesEvaluationInputBuilder,
	consumer ConsumerRef,
	series SeriesIdentityDigest,
	bindings []NamedInputBinding,
	completions []PhysicalQueryCompletion,
) (SeriesEvaluationInputRequest, error) {
	if series == "" || !consumer.HasLevel || consumer.LevelID == 0 {
		return SeriesEvaluationInputRequest{}, errors.New("complete Level and series scope is required")
	}
	prepared, actualByID, err := validateNamedInputExactSet(builder, consumer, series, bindings, completions)
	if err != nil {
		return SeriesEvaluationInputRequest{}, err
	}

	request := SeriesEvaluationInputRequest{Contract: builder.contract, Consumer: consumer, SeriesIdentity: series}
	for _, requirement := range prepared.requirements {
		request.RequirementIDs = append(request.RequirementIDs, requirement.RequirementID)
		request.Inputs = append(request.Inputs, cloneNamedInputBinding(actualByID[requirement.RequirementID]))
	}
	return request, nil
}

func validateNamedInputExactSet(
	builder *SeriesEvaluationInputBuilder,
	consumer ConsumerRef,
	series SeriesIdentityDigest,
	bindings []NamedInputBinding,
	completions []PhysicalQueryCompletion,
) (preparedSeriesEvaluationConsumer, map[RequirementID]NamedInputBinding, error) {
	if !consumer.HasLevel || consumer.LevelID == 0 {
		return preparedSeriesEvaluationConsumer{}, nil, errors.New("complete Level consumer scope is required")
	}
	prepared, dueFound := builder.consumers[consumer]
	if !dueFound {
		return preparedSeriesEvaluationConsumer{}, nil, errors.New("consumer does not reference a frozen due Plan Level")
	}

	actualByID := make(map[RequirementID]NamedInputBinding, len(bindings))
	for _, binding := range bindings {
		if binding.Consumer != consumer {
			return preparedSeriesEvaluationConsumer{}, nil, errors.New("named input has a different consumer")
		}
		if _, known := prepared.byID[binding.RequirementID]; !known {
			return preparedSeriesEvaluationConsumer{}, nil, errors.New("named input is not in the frozen exact set")
		}
		if _, duplicate := actualByID[binding.RequirementID]; duplicate {
			return preparedSeriesEvaluationConsumer{}, nil, errors.New("duplicate named input requirement")
		}
		actualByID[binding.RequirementID] = binding
	}
	if len(actualByID) != len(prepared.byID) {
		return preparedSeriesEvaluationConsumer{}, nil, errors.New("named inputs do not cover the frozen exact set")
	}

	for _, requirement := range prepared.requirements {
		binding := actualByID[requirement.RequirementID]
		if err := validateSeriesNamedInputBinding(binding, requirement, series, builder.contract.Slot.EvaluationTime); err != nil {
			return preparedSeriesEvaluationConsumer{}, nil, err
		}
		query, ok := builder.queries[binding.Provenance.PhysicalQuery]
		if !ok || LogicalQueryRef(query.QueryRevision) != requirement.LogicalQueryRef {
			return preparedSeriesEvaluationConsumer{}, nil, errors.New("named input differs from its frozen physical query")
		}
		completion, err := matchingPhysicalQueryCompletion(completions, query)
		if err != nil {
			return preparedSeriesEvaluationConsumer{}, nil, err
		}
		if err := ValidateNamedInputCompletion(binding, completion); err != nil {
			return preparedSeriesEvaluationConsumer{}, nil, err
		}
	}
	return prepared, actualByID, nil
}

func cloneBuilderDataRequirement(requirement DataRequirement) DataRequirement {
	requirement.InputProjection.ValueFields = append([]string(nil), requirement.InputProjection.ValueFields...)
	requirement.InputProjection.DimensionFields = append([]string(nil), requirement.InputProjection.DimensionFields...)
	requirement.InputProjection.IdentityFields = append([]string(nil), requirement.InputProjection.IdentityFields...)
	requirement.RequiredColumns = append([]string(nil), requirement.RequiredColumns...)
	requirement.PointOffsetsSeconds = append([]int64(nil), requirement.PointOffsetsSeconds...)
	requirement.NamedPoints = append([]NamedInputPoint(nil), requirement.NamedPoints...)
	requirement.Consumers = append([]DataRequirementConsumer(nil), requirement.Consumers...)
	return requirement
}

func scopedEvaluationInputError(consumer ConsumerRef, series SeriesIdentityDigest, err error) error {
	if err == nil {
		return nil
	}
	return &EvaluationInputContractError{Consumer: consumer, SeriesIdentity: series, err: err}
}

func matchingPhysicalQueryCompletion(
	completions []PhysicalQueryCompletion,
	query PlannedPhysicalQueryRef,
) (PhysicalQueryCompletion, error) {
	var result PhysicalQueryCompletion
	found := false
	for _, completion := range completions {
		if completion.PhysicalQuery != query.Digest {
			continue
		}
		if found {
			return PhysicalQueryCompletion{}, errors.New("duplicate physical query completion")
		}
		found = true
		result = completion
	}
	if !found || result.QueryRevision != query.QueryRevision {
		return PhysicalQueryCompletion{}, errors.New("frozen physical query completion is missing or mismatched")
	}
	return result, nil
}

func validateSeriesNamedInputBinding(
	binding NamedInputBinding,
	requirement DataRequirement,
	series SeriesIdentityDigest,
	evaluationTime EvaluationTime,
) error {
	if binding.RequirementID != requirement.RequirementID || binding.DatasetName != requirement.DatasetName ||
		binding.Role != requirement.Role || binding.QueryWindow != requirement.AbsoluteWindow(evaluationTime) {
		return errors.New("named input differs from its frozen DataRequirement")
	}
	if err := binding.QueryWindow.Validate(); err != nil {
		return err
	}
	if binding.ProviderResult == "" || binding.Provenance.PhysicalQuery == "" || binding.Provenance.AttemptNo == 0 {
		return errors.New("named input lacks completion or query provenance")
	}
	if binding.ImpactScope != ImpactPlan && binding.ImpactScope != ImpactLevel && binding.ImpactScope != ImpactSeries {
		return errors.New("invalid named-input impact scope")
	}
	if binding.ImpactScope == ImpactLevel && !binding.Consumer.HasLevel {
		return errors.New("LEVEL named-input impact requires a Level consumer")
	}
	for _, fact := range binding.QualityFacts {
		if err := validateInputFact(fact.ReasonCode, fact.ImpactScope, fact.RecordID, fact.SourceTime, fact.SeriesIdentity, false); err != nil {
			return err
		}
		if fact.ImpactScope == ImpactSeries && fact.SeriesIdentity != series {
			return errors.New("quality fact belongs to a different series")
		}
	}
	for _, terminal := range binding.Terminals {
		if err := validateInputFact(terminal.ReasonCode, terminal.ImpactScope, terminal.RecordID, terminal.SourceTime, terminal.SeriesIdentity, true); err != nil {
			return err
		}
		if terminal.ImpactScope == ImpactSeries && terminal.SeriesIdentity != series {
			return errors.New("terminal fact belongs to a different series")
		}
	}
	if err := validateSeriesBindingAvailability(binding); err != nil {
		return err
	}
	if binding.View != nil {
		for index := 0; index < binding.View.Len(); index++ {
			record, ok := binding.View.Record(index)
			if !ok || SeriesIdentityDigest(record.DimensionIdentity().Digest) != series {
				return errors.New("named-input record belongs to a different series")
			}
			if record.SourceTime() < binding.QueryWindow.Start || record.SourceTime() >= binding.QueryWindow.End {
				return errors.New("named-input record is outside its frozen query window")
			}
		}
	}
	return nil
}

func validateSeriesBindingAvailability(binding NamedInputBinding) error {
	switch binding.Completeness {
	case CompletenessFull:
		if binding.Dataset == nil || binding.View == nil || !binding.View.Uses(binding.Dataset) ||
			(binding.DataState != DataStateData && binding.DataState != DataStateEmpty) ||
			(binding.Disposition != AccessAvailable && binding.Disposition != AccessDegraded) || binding.PartialEvidence != nil {
			return errors.New("invalid FULL named input")
		}
		if err := validateDatasetState(binding.Dataset, binding.DataState); err != nil {
			return err
		}
		if binding.Disposition == AccessDegraded && len(binding.QualityFacts) == 0 && len(binding.Terminals) == 0 {
			return errors.New("degraded FULL named input lacks localized quality evidence")
		}
	case CompletenessPartial:
		if binding.Dataset == nil || binding.View == nil || !binding.View.Uses(binding.Dataset) ||
			(binding.DataState != DataStateData && binding.DataState != DataStateEmpty) || binding.Disposition != AccessDegraded {
			return errors.New("invalid PARTIAL named input")
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
			(binding.Disposition != AccessUnavailable && binding.Disposition != AccessTerminal) || binding.PartialEvidence != nil {
			return errors.New("invalid UNAVAILABLE named input")
		}
	default:
		return errors.New("invalid named-input completeness")
	}
	switch binding.Disposition {
	case AccessAvailable:
		return ValidateResultReason(observability.ResultSuccess, binding.ReasonCode)
	case AccessDegraded, AccessUnavailable:
		return requireReasonClass(binding.ReasonCode, contract.ReasonClassCoverage)
	case AccessTerminal:
		return requireReasonClass(binding.ReasonCode, contract.ReasonClassDeterministic)
	default:
		return errors.New("invalid named-input disposition")
	}
}

// ValidateNamedInputCompletion verifies that a consumer binding belongs to its
// physical completion. Exactly three consumer-local projections may differ
// from the shared physical result:
//
//   - the readiness-invalid Plan-local disposition narrows a healthy result;
//   - an EMPTY binding with an empty view is the share of a DATA completion
//     whose delivered series were not bound to this consumer (or series). Access
//     emits it as the completion binding of every DATA query; the worker uses
//     it only where no streamed binding exists for the (consumer, series,
//     requirement) key, so it can never hide delivered records;
//   - an UNAVAILABLE binding without a dataset is the only projection of an
//     UNAVAILABLE completion, which may still carry the DataState and Delivery
//     of series streamed before the provider failed so that delivery
//     conservation holds.
func ValidateNamedInputCompletion(binding NamedInputBinding, completion PhysicalQueryCompletion) error {
	if completion.Ref == "" || completion.Ref != binding.ProviderResult ||
		completion.PhysicalQuery != binding.Provenance.PhysicalQuery {
		return errors.New("named input differs from its physical query completion")
	}
	if completion.Completeness != binding.Completeness || completion.DataState != binding.DataState ||
		!reflect.DeepEqual(completion.PartialEvidence, binding.PartialEvidence) {
		if !readinessBudgetInvalidBinding(binding) && !emptyShareOfDataCompletion(binding, completion) &&
			!unavailableProjection(binding, completion) {
			return errors.New("named input differs from its physical query completion")
		}
	}
	if completion.DataState == DataStateData {
		if completion.Delivery.PhysicalQuery != completion.PhysicalQuery ||
			completion.Delivery.QueryRevision != completion.QueryRevision || completion.Delivery.Series == 0 ||
			completion.Delivery.Records == 0 || completion.Delivery.Digest == "" {
			return errors.New("DATA completion lacks delivery provenance")
		}
	} else if completion.Delivery.Series != 0 || completion.Delivery.Records != 0 {
		return errors.New("non-DATA completion claims delivered series")
	}
	return nil
}

func readinessBudgetInvalidBinding(binding NamedInputBinding) bool {
	return binding.Dataset == nil && binding.View == nil && binding.PartialEvidence == nil &&
		binding.Completeness == CompletenessUnavailable && binding.DataState == DataStateUnknown &&
		binding.Disposition == AccessUnavailable &&
		binding.ReasonCode == ReasonCode(contract.ReasonReadinessBudgetInvalid) &&
		binding.ImpactScope == ImpactPlan
}

// emptyShareOfDataCompletion reports whether binding is the EMPTY consumer
// share of a FULL or PARTIAL completion that delivered series. Completeness and
// PARTIAL evidence must still match; only DataState narrows, and only with an
// empty dataset and view, so the binding cannot present records that were not
// streamed and validated for this consumer.
func emptyShareOfDataCompletion(binding NamedInputBinding, completion PhysicalQueryCompletion) bool {
	return completion.DataState == DataStateData && binding.DataState == DataStateEmpty &&
		completion.Completeness == binding.Completeness &&
		(completion.Completeness == CompletenessFull || completion.Completeness == CompletenessPartial) &&
		binding.Dataset != nil && binding.Dataset.Len() == 0 && binding.View != nil && binding.View.Len() == 0 &&
		reflect.DeepEqual(completion.PartialEvidence, binding.PartialEvidence)
}

// unavailableProjection reports whether binding is the UNKNOWN, dataset-free
// consumer projection of an UNAVAILABLE completion. The completion itself may
// carry DATA or EMPTY plus the delivery of series streamed before the failure;
// the consumer never trusts that partial stream.
func unavailableProjection(binding NamedInputBinding, completion PhysicalQueryCompletion) bool {
	return completion.Completeness == CompletenessUnavailable && binding.Completeness == CompletenessUnavailable &&
		binding.DataState == DataStateUnknown && binding.Dataset == nil && binding.View == nil &&
		binding.PartialEvidence == nil
}

func cloneNamedInputBinding(source NamedInputBinding) NamedInputBinding {
	cloned := source
	cloned.QualityFacts = append([]InputQualityFact(nil), source.QualityFacts...)
	cloned.Terminals = append([]InputTerminal(nil), source.Terminals...)
	if source.PartialEvidence != nil {
		evidence := *source.PartialEvidence
		cloned.PartialEvidence = &evidence
	}
	return cloned
}
