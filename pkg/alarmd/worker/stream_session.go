package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// streamedExecution is the Coordinator-owned provisional lifetime. Begin and
// ConsumeSeries only validate and retain immutable query facts. State/Gap
// reads and evaluation start after the authoritative completion is validated.
type streamedExecution struct {
	coordinator *SlotExecutionCoordinator
	request     execution.SlotExecutionRequest
	header      execution.InternalExecutionHeader
	prepared    preparedNamedInputIndex
	streamed    map[streamedInputKey]execution.NamedInputBinding
	planSeries  map[execution.PlanIdentity]map[execution.SeriesIdentityDigest]struct{}
	bindings    []execution.NamedInputBinding
	stateItems  []execution.StatePreflightItem
	gapItems    []execution.PlanGapLoadItem
	state       execution.StatePreflightResult
	gaps        execution.GapLoadResult
	effective   map[execution.ConsumerRef]strategy.EffectiveTimeFact
	evaluated   execution.EvaluationResult
	delivered   []execution.SeriesDelivery
	series      uint64
	retained    uint64
	effects     effectCounts
	gapFacts    uint64
	began       bool
}

type streamedInputKey struct {
	consumer    execution.ConsumerRef
	series      execution.SeriesIdentityDigest
	requirement execution.RequirementID
}

type preparedNamedInputIndex struct {
	inputBuilder           *execution.SeriesEvaluationInputBuilder
	queries                map[execution.PhysicalQueryDigest]execution.PlannedPhysicalQueryRef
	requirementsByConsumer map[execution.ConsumerRef][]execution.DataRequirement
	requirementByKey       map[struct {
		consumer    execution.ConsumerRef
		requirement execution.RequirementID
	}]execution.DataRequirement
	consumersByPlan map[execution.PlanIdentity][]execution.ConsumerRef
}

func (stream *streamedExecution) Begin(ctx context.Context, header execution.InternalExecutionHeader) error {
	if stream.began {
		return errors.New("alarmd worker: QueryExecutionSource called Begin more than once")
	}
	if header.Contract != stream.request.Contract {
		return errors.New("alarmd worker: execution header changed frozen contract")
	}
	inputBuilder, err := execution.PrepareSeriesEvaluationInputBuilder(header)
	if err != nil {
		return fmt.Errorf("alarmd worker: invalid execution header: %w", err)
	}
	prepared, err := prepareNamedInputIndex(header)
	if err != nil {
		return fmt.Errorf("alarmd worker: prepare named-input index: %w", err)
	}
	prepared.inputBuilder = inputBuilder
	stream.began = true
	stream.header = header
	stream.prepared = prepared
	stream.streamed = make(map[streamedInputKey]execution.NamedInputBinding)
	stream.planSeries = make(map[execution.PlanIdentity]map[execution.SeriesIdentityDigest]struct{})
	effective, err := prepareAlwaysEffectiveTimeFacts(ctx, header)
	if err != nil {
		return fmt.Errorf("alarmd worker: prepare EffectiveTime facts: %w", err)
	}
	stream.effective = effective
	execution.CaptureSlotCoverage(ctx, func(c *execution.SlotCoverageCapture) {
		if c.Prepared != nil {
			c.Prepared(header, effective)
		}
	})
	return nil
}

func (stream *streamedExecution) ConsumeSeries(ctx context.Context, batch execution.SeriesExecutionBatch) error {
	if !stream.began {
		return namedInputError(codeSeriesBeforeBegin, "alarmd worker: QueryExecutionSource delivered series before Begin")
	}
	series, err := stream.validateSeriesBatch(batch)
	if err != nil {
		return fmt.Errorf("alarmd worker: invalid series batch: %w", err)
	}
	retained, err := streamedRetainedSize(batch.Inputs, batch.Delivery)
	if err != nil {
		return fmt.Errorf("alarmd worker: measure provisional retention: %w", err)
	}
	if err := stream.reserveProvisional(ctx, batch.Delivery.Series, retained); err != nil {
		return err
	}
	stream.series += batch.Delivery.Series
	stream.retained += retained
	for _, binding := range batch.Inputs {
		key := streamedInputKey{consumer: binding.Consumer, series: series, requirement: binding.RequirementID}
		if _, duplicate := stream.streamed[key]; duplicate {
			return namedInputError(codeStreamedNamedInputDuplicate, "alarmd worker: duplicate streamed named input")
		}
		stream.streamed[key] = binding
		if binding.Role == execution.InputRolePrimary {
			seriesByPlan := stream.planSeries[binding.Consumer.Plan]
			if seriesByPlan == nil {
				seriesByPlan = make(map[execution.SeriesIdentityDigest]struct{})
				stream.planSeries[binding.Consumer.Plan] = seriesByPlan
			}
			seriesByPlan[series] = struct{}{}
		}
	}
	merged := false
	for index := range stream.delivered {
		if stream.delivered[index].PhysicalQuery == batch.PhysicalQuery {
			stream.delivered[index], err = execution.AccumulateSeriesDelivery(stream.delivered[index], batch.Delivery)
			if err != nil {
				return fmt.Errorf("alarmd worker: accumulate series delivery: %w", err)
			}
			merged = true
			break
		}
	}
	if !merged {
		stream.delivered = append(stream.delivered, batch.Delivery)
	}
	return nil
}

func (stream *streamedExecution) reserveProvisional(ctx context.Context, series, retainedBytes uint64) error {
	return stream.reserveProvisionalAt(ctx, series, retainedBytes, stream.reservationPhase("normal_input"))
}

func (stream *streamedExecution) reserveProvisionalAt(ctx context.Context, series, retainedBytes uint64, phase string) error {
	err := stream.coordinator.acquireProvisional(series, retainedBytes, stream, phase)
	if err == nil {
		return nil
	}
	var exceeded *provisionalBudgetExceededError
	if errors.As(err, &exceeded) {
		stream.coordinator.observeCapacityRejection(ctx, stream.request.Operation, exceeded.budget, err)
	}
	return err
}

func (stream *streamedExecution) releaseProvisional() {
	// QueryExecutionSource has joined all deliveries before Execute returns.
	// Drop the owning references before advertising reusable capacity.
	stream.header = execution.InternalExecutionHeader{}
	stream.prepared = preparedNamedInputIndex{}
	stream.streamed, stream.planSeries, stream.effective = nil, nil, nil
	stream.bindings, stream.stateItems, stream.gapItems, stream.delivered = nil, nil, nil, nil
	stream.state, stream.gaps = execution.StatePreflightResult{}, execution.GapLoadResult{}
	stream.evaluated = execution.EvaluationResult{}
	stream.coordinator.reservations.mu.Lock()
	stream.coordinator.reservations.gapFacts -= stream.gapFacts
	stream.coordinator.reservations.mu.Unlock()
	stream.gapFacts = 0
	stream.coordinator.releaseEffects(stream.effects)
	stream.effects = effectCounts{}
	stream.coordinator.releaseProvisional(stream.series, stream.retained)
	stream.series, stream.retained = 0, 0
}

func streamedRetainedSize(bindings []execution.NamedInputBinding, delivery execution.SeriesDelivery) (uint64, error) {
	compact := make([]execution.NamedInputBinding, len(bindings))
	copy(compact, bindings)
	for index := range compact {
		compact[index].Dataset = nil
		compact[index].View = nil
	}
	encoded, err := json.Marshal(struct {
		Bindings []execution.NamedInputBinding
		Delivery execution.SeriesDelivery
	}{Bindings: compact, Delivery: delivery})
	if err != nil {
		return 0, err
	}
	return uint64(len(encoded)) + delivery.Bytes, nil
}

func prepareNamedInputIndex(header execution.InternalExecutionHeader) (preparedNamedInputIndex, error) {
	prepared := preparedNamedInputIndex{
		queries:                make(map[execution.PhysicalQueryDigest]execution.PlannedPhysicalQueryRef, len(header.RequiredPhysicalQueries)),
		requirementsByConsumer: make(map[execution.ConsumerRef][]execution.DataRequirement),
		requirementByKey: make(map[struct {
			consumer    execution.ConsumerRef
			requirement execution.RequirementID
		}]execution.DataRequirement),
		consumersByPlan: make(map[execution.PlanIdentity][]execution.ConsumerRef, len(header.DuePlans)),
	}
	for _, due := range header.DuePlans {
		for _, level := range due.CompiledPlan.Levels() {
			prepared.consumersByPlan[due.Identity] = append(prepared.consumersByPlan[due.Identity], execution.ConsumerRef{
				Plan: due.Identity, LevelID: level.Definition().LevelID, HasLevel: true,
			})
		}
	}
	for _, query := range header.RequiredPhysicalQueries {
		prepared.queries[query.Digest] = query
	}
	for _, requirement := range header.Requirements {
		for _, binding := range requirement.Consumers {
			consumer := binding.Consumer
			if !consumer.HasLevel {
				return preparedNamedInputIndex{}, errors.New("alarmd worker: frozen named input consumer requires a Level")
			}
			key := struct {
				consumer    execution.ConsumerRef
				requirement execution.RequirementID
			}{consumer: consumer, requirement: requirement.RequirementID}
			if _, duplicate := prepared.requirementByKey[key]; duplicate {
				return preparedNamedInputIndex{}, errors.New("alarmd worker: duplicate frozen consumer requirement")
			}
			prepared.requirementByKey[key] = requirement
			prepared.requirementsByConsumer[consumer] = append(prepared.requirementsByConsumer[consumer], requirement)
		}
	}
	for plan, consumers := range prepared.consumersByPlan {
		for _, consumer := range consumers {
			requirements := prepared.requirementsByConsumer[consumer]
			if len(requirements) == 0 {
				return preparedNamedInputIndex{}, fmt.Errorf("alarmd worker: Plan %s Level %d has no frozen named input", plan.StrategyID, consumer.LevelID)
			}
			sort.Slice(requirements, func(i, j int) bool {
				if requirements[i].Role != requirements[j].Role {
					return requirements[i].Role == execution.InputRolePrimary
				}
				return requirements[i].RequirementID < requirements[j].RequirementID
			})
			primary := 0
			for _, requirement := range requirements {
				if requirement.Role == execution.InputRolePrimary {
					primary++
				}
			}
			if primary != 1 {
				return preparedNamedInputIndex{}, fmt.Errorf("alarmd worker: Plan %s Level %d requires exactly one PRIMARY named input", plan.StrategyID, consumer.LevelID)
			}
			prepared.requirementsByConsumer[consumer] = requirements
		}
	}
	return prepared, nil
}

func (stream *streamedExecution) validateSeriesBatch(batch execution.SeriesExecutionBatch) (execution.SeriesIdentityDigest, error) {
	query, found := stream.prepared.queries[batch.PhysicalQuery]
	if !found || query.QueryRevision != batch.QueryRevision || batch.CompletionRef == "" || batch.Dataset == nil ||
		batch.Dataset.Len() == 0 || len(batch.Inputs) == 0 || batch.Delivery.PhysicalQuery != batch.PhysicalQuery ||
		batch.Delivery.QueryRevision != batch.QueryRevision || batch.Delivery.Series != 1 ||
		batch.Delivery.Records != uint64(batch.Dataset.Len()) || batch.Delivery.Digest == "" {
		return "", namedInputError(codeSeriesBatchInvalid, "incomplete batch or frozen query mismatch")
	}
	var series execution.SeriesIdentityDigest
	for index := 0; index < batch.Dataset.Len(); index++ {
		record, ok := batch.Dataset.Record(index)
		candidate := execution.SeriesIdentityDigest(record.DimensionIdentity().Digest)
		if !ok || candidate == "" || (series != "" && candidate != series) {
			return "", namedInputError(codeSeriesBatchNotSingleSeries, "batch does not contain exactly one stable series")
		}
		series = candidate
	}
	seen := make(map[struct {
		consumer    execution.ConsumerRef
		requirement execution.RequirementID
	}]struct{}, len(batch.Inputs))
	for _, binding := range batch.Inputs {
		key := struct {
			consumer    execution.ConsumerRef
			requirement execution.RequirementID
		}{consumer: binding.Consumer, requirement: binding.RequirementID}
		requirement, known := stream.prepared.requirementByKey[key]
		if !known {
			return "", namedInputError(codeSeriesBindingOutsideRequirements, "binding is outside the frozen consumer requirement set")
		}
		if _, duplicate := seen[key]; duplicate {
			return "", namedInputError(codeSeriesBindingDuplicate, "batch repeats a frozen consumer requirement")
		}
		seen[key] = struct{}{}
		if binding.Dataset != batch.Dataset || binding.View == nil || !binding.View.Uses(batch.Dataset) ||
			binding.Consumer != key.consumer || binding.DatasetName != requirement.DatasetName ||
			binding.Role != requirement.Role || binding.QueryWindow != requirement.AbsoluteWindow(stream.header.Contract.Slot.EvaluationTime) ||
			binding.ProviderResult != batch.CompletionRef || binding.Provenance.PhysicalQuery != batch.PhysicalQuery ||
			binding.Provenance.AttemptNo == 0 || execution.LogicalQueryRef(query.QueryRevision) != requirement.LogicalQueryRef ||
			binding.Completeness != execution.CompletenessFull || binding.DataState != execution.DataStateData ||
			binding.Disposition != execution.AccessAvailable {
			return "", namedInputError(codeSeriesBindingMismatch, "binding differs from frozen DataRequirement or physical query")
		}
		for recordIndex := 0; recordIndex < binding.View.Len(); recordIndex++ {
			record, ok := binding.View.Record(recordIndex)
			if !ok || execution.SeriesIdentityDigest(record.DimensionIdentity().Digest) != series ||
				record.SourceTime() < binding.QueryWindow.Start || record.SourceTime() >= binding.QueryWindow.End {
				return "", namedInputError(codeSeriesRecordOutsideWindow, "binding contains a record outside its series or frozen query window")
			}
		}
	}
	return series, nil
}

func gapPreflightForHeader(header execution.InternalExecutionHeader) ([]execution.PlanGapLoadItem, error) {
	items := make([]execution.PlanGapLoadItem, 0, len(header.DuePlans))
	for _, due := range header.DuePlans {
		version, err := execution.BuildApplyVersion(header.Contract, due.StateApplyEpoch)
		if err != nil {
			return nil, err
		}
		items = append(items, execution.PlanGapLoadItem{
			Identity:     execution.PlanGapIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration},
			ApplyVersion: version, ScheduleRevision: due.ScheduleRevision,
		})
	}
	return items, nil
}

func mergeProvisional(target *execution.EvaluationResult, next execution.EvaluationResult, budget ProvisionalBudget) error {
	if target.Contract != (execution.FrozenExecutionContractRef{}) && target.Contract != next.Contract {
		return errors.New("alarmd worker: series evaluations changed frozen contract")
	}
	previous, delta := countEffects(*target), addedEffectCounts(*target, next)
	if err := checkEffectCounts(effectCounts{previous.states + delta.states, previous.events + delta.events, previous.gaps + delta.gaps}, budget); err != nil {
		return err
	}
	appendProvisional(target, next)
	return nil
}

// appendProvisional is called only after contract and capacity checks succeed.
func appendProvisional(target *execution.EvaluationResult, next execution.EvaluationResult) {
	if target.Contract == (execution.FrozenExecutionContractRef{}) {
		target.Contract, target.Result, target.ReasonCode = next.Contract, next.Result, next.ReasonCode
	} else if resultRank(next.Result) > resultRank(target.Result) {
		target.Result, target.ReasonCode = next.Result, next.ReasonCode
	}
	for _, nextPlan := range next.Plans {
		index := -1
		for current := range target.Plans {
			if target.Plans[current].Plan == nextPlan.Plan {
				index = current
				break
			}
		}
		if index < 0 {
			target.Plans = append(target.Plans, nextPlan)
			continue
		}
		plan := &target.Plans[index]
		if dispositionRank(nextPlan.Disposition) > dispositionRank(plan.Disposition) {
			plan.Disposition, plan.ReasonCode = nextPlan.Disposition, nextPlan.ReasonCode
		}
		plan.LevelOutcomes = append(plan.LevelOutcomes, nextPlan.LevelOutcomes...)
		plan.StateResults = append(plan.StateResults, nextPlan.StateResults...)
		plan.GuardBeforeEvents = appendUniqueGapMutations(plan.GuardBeforeEvents, nextPlan.GuardBeforeEvents)
		plan.GuardAfterState = appendUniqueGapMutations(plan.GuardAfterState, nextPlan.GuardAfterState)
	}
}

type provisionalBudgetExceededError struct {
	budget observability.CapacityBudget
	facts  *observability.CapacityRejectionFacts
}

func (err *provisionalBudgetExceededError) Error() string {
	return "alarmd worker: provisional " + string(err.budget) + " budget exceeded"
}

func resultRank(result observability.Result) int {
	switch result {
	case observability.ResultRetrying:
		return 4
	case observability.ResultTerminal:
		return 3
	case observability.ResultDegraded:
		return 2
	case observability.ResultSuccess:
		return 1
	default:
		return 0
	}
}

func dispositionRank(disposition execution.PlanDisposition) int {
	switch disposition {
	case execution.PlanRetryPending:
		return 5
	case execution.PlanTerminal:
		return 4
	case execution.PlanUnavailable, execution.PlanReadinessGap:
		return 3
	case execution.PlanDecidedDegraded:
		return 2
	case execution.PlanDecided:
		return 1
	default:
		return 0
	}
}

func appendUniqueGapMutations(current, next []execution.PlanGapMutation) []execution.PlanGapMutation {
	for _, candidate := range next {
		duplicate := false
		for _, existing := range current {
			if existing.Identity == candidate.Identity && existing.MutationDigest == candidate.MutationDigest {
				duplicate = true
				break
			}
		}
		if !duplicate {
			current = append(current, candidate)
		}
	}
	return current
}

func (stream *streamedExecution) complete(ctx context.Context, completion execution.QueryExecutionCompletion) error {
	if !stream.began {
		return completionContractError(codeCompletionBeforeBegin, "alarmd worker: QueryExecutionSource returned completion before Begin")
	}
	if err := completion.Validate(stream.header, stream.delivered); err != nil {
		return wrapCompletionContractError(codeCompletionInvalid, err)
	}
	physical := make(map[execution.PhysicalQueryDigest]execution.PhysicalQueryCompletion, len(completion.PhysicalQueries))
	for _, item := range completion.PhysicalQueries {
		physical[item.PhysicalQuery] = item
	}
	completionBindings := make(map[struct {
		consumer    execution.ConsumerRef
		requirement execution.RequirementID
	}]execution.NamedInputBinding, len(completion.CompletionBindings))
	for _, binding := range completion.CompletionBindings {
		key := struct {
			consumer    execution.ConsumerRef
			requirement execution.RequirementID
		}{consumer: binding.Consumer, requirement: binding.RequirementID}
		requirement, known := stream.prepared.requirementByKey[key]
		item, ok := physical[binding.Provenance.PhysicalQuery]
		query, queryOK := stream.prepared.queries[binding.Provenance.PhysicalQuery]
		if !known || !ok || !queryOK || execution.LogicalQueryRef(query.QueryRevision) != requirement.LogicalQueryRef ||
			binding.DatasetName != requirement.DatasetName || binding.Role != requirement.Role ||
			binding.QueryWindow != requirement.AbsoluteWindow(stream.header.Contract.Slot.EvaluationTime) ||
			binding.Provenance.AttemptNo == 0 {
			return completionContractError(codeCompletionBindingMismatch, "alarmd worker: completion binding differs from frozen requirement or physical completion")
		}
		if err := execution.ValidateNamedInputCompletion(binding, item); err != nil {
			return completionContractError(codeCompletionBindingPhysicalMismatch, "alarmd worker: completion binding differs from frozen requirement or physical completion")
		}
		if _, duplicate := completionBindings[key]; duplicate {
			return completionContractError(codeDuplicateCompletionBinding, "alarmd worker: duplicate completion binding")
		}
		completionBindings[key] = binding
	}
	for key, binding := range stream.streamed {
		item, ok := physical[binding.Provenance.PhysicalQuery]
		if !ok || item.Ref != binding.ProviderResult {
			return completionContractError(codeStreamedBindingMismatch, "alarmd worker: streamed binding differs from physical completion")
		}
		switch item.Completeness {
		case execution.CompletenessFull:
			if binding.Completeness != execution.CompletenessFull {
				return completionContractError(codeStreamedBindingFullMismatch, "alarmd worker: streamed binding differs from FULL physical completion")
			}
		case execution.CompletenessPartial:
			if binding.Completeness != execution.CompletenessFull {
				return completionContractError(codeStreamedBindingPartialMismatch, "alarmd worker: streamed binding differs from PARTIAL physical completion")
			}
			binding.Completeness = execution.CompletenessPartial
			binding.Disposition = execution.AccessDegraded
			binding.ReasonCode = execution.ReasonCode(contract.ReasonQueryPartial)
			binding.PartialEvidence = item.PartialEvidence
		case execution.CompletenessUnavailable:
			binding.Dataset, binding.View = nil, nil
			binding.Completeness = execution.CompletenessUnavailable
			binding.DataState = execution.DataStateUnknown
			binding.Disposition = execution.AccessUnavailable
			binding.ReasonCode = physicalFailureReason(item.RouteFacts)
			binding.PartialEvidence = nil
		default:
			return completionContractError(codeInvalidCompleteness, "alarmd worker: invalid physical completion completeness")
		}
		stream.streamed[key] = binding
	}

	type preparedSeries struct {
		due      execution.DuePlan
		identity execution.SeriesIdentityDigest
		inputs   []execution.SeriesEvaluationInputRequest
	}
	preparedSeriesEvaluations := make([]preparedSeries, 0, len(stream.streamed))
	for _, due := range stream.header.DuePlans {
		series := make([]execution.SeriesIdentityDigest, 0, len(stream.planSeries[due.Identity]))
		for identity := range stream.planSeries[due.Identity] {
			series = append(series, identity)
		}
		sort.Slice(series, func(i, j int) bool { return series[i] < series[j] })
		for _, identity := range series {
			inputs, err := stream.seriesInputs(due, identity, physical, completionBindings, completion.PhysicalQueries)
			if err != nil {
				return err
			}
			preparedSeriesEvaluations = append(preparedSeriesEvaluations,
				preparedSeries{due: due, identity: identity, inputs: inputs})
		}
	}
	for _, binding := range stream.streamed {
		stream.bindings = append(stream.bindings, compactNamedInputBinding(binding))
	}
	for _, binding := range completion.CompletionBindings {
		stream.bindings = append(stream.bindings, compactNamedInputBinding(binding))
	}
	sort.SliceStable(stream.bindings, func(i, j int) bool {
		left, right := stream.bindings[i], stream.bindings[j]
		if left.Consumer.Plan != right.Consumer.Plan {
			return planIdentityLess(left.Consumer.Plan, right.Consumer.Plan)
		}
		if left.Consumer.LevelID != right.Consumer.LevelID {
			return left.Consumer.LevelID < right.Consumer.LevelID
		}
		return left.RequirementID < right.RequirementID
	})
	for _, due := range stream.header.DuePlans {
		if len(stream.planSeries[due.Identity]) == 0 {
			if err := stream.validateCompletionOnlyExactSet(due, completionBindings, completion.PhysicalQueries); err != nil {
				return err
			}
		}
	}
	if err := stream.loadGaps(ctx); err != nil {
		return err
	}
	// Runtime State is read in bounded batches ahead of evaluation while
	// evaluation itself keeps the exact Plan-then-series order. A series whose
	// PRIMARY input is incomplete needs no State read; the pending batch is
	// flushed first so its degraded result merges in order.
	batchLimit := stream.statePreflightBatchLimit()
	pending := make([]completedSeries, 0, batchLimit)
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		err := stream.evaluateCompletedSeriesBatch(ctx, pending)
		pending = pending[:0]
		return err
	}
	for _, prepared := range preparedSeriesEvaluations {
		if incomplete := primaryIncompleteBindings(prepared.inputs); len(incomplete) != 0 {
			if err := flush(); err != nil {
				return err
			}
			if err := stream.mergePrimaryIncompleteSeries(ctx, prepared.due, incomplete); err != nil {
				return err
			}
			continue
		}
		version, err := execution.BuildApplyVersion(stream.header.Contract, prepared.due.StateApplyEpoch)
		if err != nil {
			return err
		}
		pending = append(pending, completedSeries{due: prepared.due, series: prepared.identity, inputs: prepared.inputs,
			item: execution.StatePreflightItem{Identity: execution.StateKeyIdentity{
				Plan: prepared.due.Identity, StateGeneration: prepared.due.StateGeneration, SeriesIdentityDigest: prepared.identity,
			}, ApplyVersion: version}})
		if len(pending) >= batchLimit {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if len(stream.evaluated.Plans) == 0 {
		return stream.completeWithoutSeries(ctx, completion)
	}
	for _, due := range stream.header.DuePlans {
		if len(stream.planSeries[due.Identity]) != 0 {
			continue
		}
		result, err := stream.noSeriesPlanResult(due)
		if err != nil {
			return err
		}
		stream.observeCompletionOnlyPlan(ctx, due, result)
		if err := stream.mergeProvisional(ctx, result, 0); err != nil {
			return err
		}
	}
	if len(stream.evaluated.Plans) != len(stream.header.DuePlans) {
		return completionContractError(codeDuePlanResultMissing, "alarmd worker: trustworthy completion did not produce every due Plan result")
	}
	return nil
}

func (stream *streamedExecution) validateCompletionOnlyExactSet(
	due execution.DuePlan,
	completionBindings map[struct {
		consumer    execution.ConsumerRef
		requirement execution.RequirementID
	}]execution.NamedInputBinding,
	completions []execution.PhysicalQueryCompletion,
) error {
	for _, consumer := range stream.prepared.consumersByPlan[due.Identity] {
		bindings := make([]execution.NamedInputBinding, 0, len(stream.prepared.requirementsByConsumer[consumer]))
		for _, requirement := range stream.prepared.requirementsByConsumer[consumer] {
			key := struct {
				consumer    execution.ConsumerRef
				requirement execution.RequirementID
			}{consumer: consumer, requirement: requirement.RequirementID}
			if _, found := completionBindings[key]; !found {
				return namedInputError(codeCompletionOnlyRequirementMissing,
					fmt.Sprintf("alarmd worker: completion-only Plan %s Level %d is missing frozen requirement %s",
						due.Identity.StrategyID, consumer.LevelID, requirement.RequirementID))
			}
			bindings = append(bindings, completionBindings[key])
		}
		if err := stream.prepared.inputBuilder.ValidateCompletionOnly(consumer, bindings, completions); err != nil {
			return wrapNamedInputError(codeCompletionOnlyExactSetInvalid,
				fmt.Errorf("alarmd worker: Plan %s Level %d completion-only exact set: %w",
					due.Identity.StrategyID, consumer.LevelID, err))
		}
	}
	return nil
}

func (stream *streamedExecution) loadGaps(ctx context.Context) error {
	var targetBytes uint64
	for _, due := range stream.header.DuePlans {
		targetBytes += retainedObjectBytes(execution.PlanGapLoadItem{Identity: execution.PlanGapIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration}})
	}
	if err := stream.retainTargetBytes(ctx, len(stream.header.DuePlans), targetBytes); err != nil {
		return err
	}
	items, err := gapPreflightForHeader(stream.header)
	if err != nil {
		return err
	}
	stream.gapItems = items
	request := execution.GapLoadRequest{Contract: stream.header.Contract, Items: items}
	started := time.Now()
	stream.gaps, err = stream.loadGapFacts(ctx, request)
	if err == nil {
		err = execution.ValidateGapLoad(request, stream.gaps)
	}
	if err != nil {
		stream.coordinator.observe(ctx, observability.ComponentState, observability.StageGapLoaded,
			stream.request.Operation, started, "", "", err)
		return fmt.Errorf("alarmd worker: gap preflight: %w", err)
	}
	result, reason := summarizeGapLoad(stream.gaps)
	stream.coordinator.observeWithCounts(ctx, observability.ComponentState, observability.StageGapLoaded,
		stream.request.Operation, started, result, reason, observability.Counts{Keys: int64(len(stream.gaps.Items))}, nil)
	return nil
}

func (stream *streamedExecution) completeWithoutSeries(
	ctx context.Context,
	completion execution.QueryExecutionCompletion,
) error {
	if stream.request.Operation == execution.OperationProbe {
		for _, binding := range completion.CompletionBindings {
			if binding.Completeness != execution.CompletenessFull {
				stream.evaluated = execution.EvaluationResult{Contract: stream.header.Contract,
					Result: observability.ResultDegraded, ReasonCode: binding.ReasonCode}
				stream.observeCompletionOnlyProbe(ctx)
				return nil
			}
		}
	}
	stream.evaluated = execution.EvaluationResult{Contract: stream.header.Contract, Result: observability.ResultSuccess,
		ReasonCode: observability.ReasonNone}
	for _, due := range stream.header.DuePlans {
		planResult, err := stream.noSeriesPlanResult(due)
		if err != nil {
			return err
		}
		stream.observeCompletionOnlyPlan(ctx, due, planResult)
		if err := stream.mergeProvisional(ctx, planResult, 0); err != nil {
			return err
		}
	}
	return nil
}

func (stream *streamedExecution) observeCompletionOnlyProbe(ctx context.Context) {
	for _, due := range stream.header.DuePlans {
		result := execution.EvaluationResult{Contract: stream.header.Contract,
			Result: observability.ResultSuccess, ReasonCode: observability.ReasonNone}
		for _, binding := range planBindings(stream.bindings, due.Identity) {
			if binding.Completeness != execution.CompletenessFull {
				result.Result, result.ReasonCode = observability.ResultDegraded, binding.ReasonCode
				break
			}
		}
		stream.observeCompletionOnlyPlan(ctx, due, result)
	}
}

func (stream *streamedExecution) noSeriesPlanResult(due execution.DuePlan) (execution.EvaluationResult, error) {
	bindings := planBindings(stream.bindings, due.Identity)
	primary, found := firstNonFullPrimary(bindings)
	if !found {
		if !planCompletedFullEmpty(bindings, due.Identity) {
			return execution.EvaluationResult{}, completionContractError(codeNoSeriesPlanResultInvalid, "alarmd worker: trustworthy completion produced no series or FULL EMPTY Plan")
		}
		return execution.EvaluationResult{Contract: stream.header.Contract, Result: observability.ResultSuccess,
			ReasonCode: observability.ReasonNone,
			Plans:      []execution.PlanEvaluationResult{{Plan: due.Identity, Disposition: execution.PlanDecided}}}, nil
	}
	mutation, err := stream.completionGapMutation(due, bindings)
	if err != nil {
		return execution.EvaluationResult{}, err
	}
	disposition := execution.PlanDecidedDegraded
	if primary.Completeness == execution.CompletenessUnavailable {
		disposition = execution.PlanUnavailable
	}
	return execution.EvaluationResult{Contract: stream.header.Contract, Result: observability.ResultDegraded,
		ReasonCode: primary.ReasonCode,
		Plans: []execution.PlanEvaluationResult{{Plan: due.Identity, Disposition: disposition,
			ReasonCode: primary.ReasonCode, GuardBeforeEvents: []execution.PlanGapMutation{mutation}}}}, nil
}

func planBindings(bindings []execution.NamedInputBinding, plan execution.PlanIdentity) []execution.NamedInputBinding {
	result := make([]execution.NamedInputBinding, 0)
	for _, binding := range bindings {
		if binding.Consumer.Plan == plan {
			result = append(result, binding)
		}
	}
	return result
}

func firstNonFullPrimary(bindings []execution.NamedInputBinding) (execution.NamedInputBinding, bool) {
	for _, binding := range bindings {
		if binding.Role == execution.InputRolePrimary && binding.Completeness != execution.CompletenessFull {
			return binding, true
		}
	}
	return execution.NamedInputBinding{}, false
}

func compactNamedInputBinding(binding execution.NamedInputBinding) execution.NamedInputBinding {
	binding.Dataset, binding.View = nil, nil
	return binding
}

func planIdentityLess(left, right execution.PlanIdentity) bool {
	if left.TenantID != right.TenantID {
		return left.TenantID < right.TenantID
	}
	if left.BusinessID != right.BusinessID {
		return left.BusinessID < right.BusinessID
	}
	return left.StrategyID < right.StrategyID
}

func (stream *streamedExecution) seriesInputs(
	due execution.DuePlan,
	series execution.SeriesIdentityDigest,
	physical map[execution.PhysicalQueryDigest]execution.PhysicalQueryCompletion,
	completionBindings map[struct {
		consumer    execution.ConsumerRef
		requirement execution.RequirementID
	}]execution.NamedInputBinding,
	completions []execution.PhysicalQueryCompletion,
) ([]execution.SeriesEvaluationInputRequest, error) {
	consumers := stream.prepared.consumersByPlan[due.Identity]
	inputs := make([]execution.SeriesEvaluationInputRequest, 0, len(consumers))
	for _, consumer := range consumers {
		bindings := make([]execution.NamedInputBinding, 0, len(stream.prepared.requirementsByConsumer[consumer]))
		for _, requirement := range stream.prepared.requirementsByConsumer[consumer] {
			binding, err := stream.completedBinding(consumer, series, requirement, physical, completionBindings)
			if err != nil {
				return nil, fmt.Errorf("alarmd worker: Plan %s Level %d named input: %w",
					due.Identity.StrategyID, consumer.LevelID, err)
			}
			bindings = append(bindings, binding)
		}
		input, err := stream.prepared.inputBuilder.Build(consumer, series, bindings, completions)
		if err != nil {
			return nil, wrapNamedInputError(codeNamedInputExactSetInvalid,
				fmt.Errorf("alarmd worker: Plan %s Level %d named-input exact set: %w",
					due.Identity.StrategyID, consumer.LevelID, err))
		}
		inputs = append(inputs, input)
	}
	return inputs, nil
}

func (stream *streamedExecution) completedBinding(
	consumer execution.ConsumerRef,
	series execution.SeriesIdentityDigest,
	requirement execution.DataRequirement,
	physical map[execution.PhysicalQueryDigest]execution.PhysicalQueryCompletion,
	completionBindings map[struct {
		consumer    execution.ConsumerRef
		requirement execution.RequirementID
	}]execution.NamedInputBinding,
) (execution.NamedInputBinding, error) {
	key := streamedInputKey{consumer: consumer, series: series, requirement: requirement.RequirementID}
	if binding, found := stream.streamed[key]; found {
		return binding, nil
	}
	templateKey := struct {
		consumer    execution.ConsumerRef
		requirement execution.RequirementID
	}{consumer: consumer, requirement: requirement.RequirementID}
	if binding, found := completionBindings[templateKey]; found {
		return binding, nil
	}
	query, err := stream.queryForRequirement(consumer, requirement, completionBindings)
	if err != nil {
		return execution.NamedInputBinding{}, err
	}
	completed, found := physical[query.Digest]
	if !found {
		return execution.NamedInputBinding{}, namedInputError(codePhysicalCompletionMissing, "authoritative physical completion is missing")
	}
	binding := execution.NamedInputBinding{
		Consumer: consumer, RequirementID: requirement.RequirementID, DatasetName: requirement.DatasetName,
		Role: requirement.Role, ProviderResult: completed.Ref,
		QueryWindow:  requirement.AbsoluteWindow(stream.header.Contract.Slot.EvaluationTime),
		Completeness: completed.Completeness, Disposition: execution.AccessAvailable, ImpactScope: execution.ImpactLevel,
		PartialEvidence: completed.PartialEvidence,
		Provenance:      execution.InputProvenance{PhysicalQuery: query.Digest, AttemptNo: stream.request.AttemptNo},
	}
	switch completed.Completeness {
	case execution.CompletenessFull:
		binding.DataState = execution.DataStateEmpty
		binding.Dataset = execution.NewDataset(nil)
		binding.View, _ = execution.NewDatasetView(binding.Dataset, nil)
	case execution.CompletenessPartial:
		binding.DataState = execution.DataStateEmpty
		binding.Dataset = execution.NewDataset(nil)
		binding.View, _ = execution.NewDatasetView(binding.Dataset, nil)
		binding.Disposition = execution.AccessDegraded
		binding.ReasonCode = execution.ReasonCode(contract.ReasonQueryPartial)
	case execution.CompletenessUnavailable:
		binding.DataState = execution.DataStateUnknown
		binding.Disposition = execution.AccessUnavailable
		binding.ReasonCode = physicalFailureReason(completed.RouteFacts)
	default:
		return execution.NamedInputBinding{}, namedInputError(codePhysicalCompletenessInvalid, "invalid physical completion completeness")
	}
	return binding, nil
}

func (stream *streamedExecution) queryForRequirement(
	consumer execution.ConsumerRef,
	requirement execution.DataRequirement,
	completionBindings map[struct {
		consumer    execution.ConsumerRef
		requirement execution.RequirementID
	}]execution.NamedInputBinding,
) (execution.PlannedPhysicalQueryRef, error) {
	for key, binding := range stream.streamed {
		if key.consumer == consumer && key.requirement == requirement.RequirementID {
			return stream.prepared.queries[binding.Provenance.PhysicalQuery], nil
		}
	}
	templateKey := struct {
		consumer    execution.ConsumerRef
		requirement execution.RequirementID
	}{consumer: consumer, requirement: requirement.RequirementID}
	if binding, found := completionBindings[templateKey]; found {
		return stream.prepared.queries[binding.Provenance.PhysicalQuery], nil
	}
	var selected execution.PlannedPhysicalQueryRef
	found := false
	for _, query := range stream.prepared.queries {
		if execution.LogicalQueryRef(query.QueryRevision) != requirement.LogicalQueryRef {
			continue
		}
		if found {
			return execution.PlannedPhysicalQueryRef{}, namedInputError(codeRequirementQueryAmbiguous, "frozen requirement physical query is ambiguous")
		}
		selected, found = query, true
	}
	if !found {
		return execution.PlannedPhysicalQueryRef{}, namedInputError(codeRequirementQueryMissing, "frozen requirement physical query is missing")
	}
	return selected, nil
}

// completedSeries is one series whose inputs are complete and whose Runtime
// State view is still to be read. Views are read in one bounded batch and the
// series are then evaluated one by one in their original order.
type completedSeries struct {
	due    execution.DuePlan
	series execution.SeriesIdentityDigest
	inputs []execution.SeriesEvaluationInputRequest
	item   execution.StatePreflightItem
}

// statePreflightBatchLimit is the shared batch bound clamped to the Slot's
// State mutation budget, which validated configuration keeps at or below the
// store's per-call item limit.
func (stream *streamedExecution) statePreflightBatchLimit() int {
	limit := execution.StatePreflightBatchItems
	if budget := stream.coordinator.budget.MaxStateMutations; budget > 0 && budget < uint64(limit) {
		limit = int(budget)
	}
	return limit
}

func primaryIncompleteBindings(inputs []execution.SeriesEvaluationInputRequest) []execution.NamedInputBinding {
	primaryIncomplete := make([]execution.NamedInputBinding, 0)
	for _, input := range inputs {
		for _, binding := range input.Inputs {
			if binding.Role == execution.InputRolePrimary && binding.Completeness != execution.CompletenessFull {
				primaryIncomplete = append(primaryIncomplete, binding)
			}
		}
	}
	return primaryIncomplete
}

func (stream *streamedExecution) mergePrimaryIncompleteSeries(
	ctx context.Context,
	due execution.DuePlan,
	primaryIncomplete []execution.NamedInputBinding,
) error {
	mutation, err := stream.completionGapMutation(due, primaryIncomplete)
	if err != nil {
		return err
	}
	disposition := execution.PlanDecidedDegraded
	reason := primaryIncomplete[0].ReasonCode
	for _, binding := range primaryIncomplete {
		if binding.Completeness == execution.CompletenessUnavailable {
			disposition = execution.PlanUnavailable
			reason = binding.ReasonCode
			break
		}
	}
	return stream.mergeProvisional(ctx, execution.EvaluationResult{Contract: stream.header.Contract,
		Result: observability.ResultDegraded, ReasonCode: reason,
		Plans: []execution.PlanEvaluationResult{{Plan: due.Identity, Disposition: disposition,
			ReasonCode: reason, GuardBeforeEvents: []execution.PlanGapMutation{mutation}}}}, 0)
}

// evaluateCompletedSeriesBatch reads the Runtime State of every series in the
// batch with one preflight call, then evaluates each series against exactly its
// own view. Per-item read outcomes stay isolated: a corrupt blob degrades only
// its series, as it did when every series was read alone.
func (stream *streamedExecution) evaluateCompletedSeriesBatch(ctx context.Context, batch []completedSeries) error {
	stateItems := make([]execution.StatePreflightItem, len(batch))
	for index, entry := range batch {
		stateItems[index] = entry.item
	}
	stateRequest := execution.StatePreflightRequest{Contract: stream.request.Contract, Items: stateItems}
	started := time.Now()
	loaded, err := stream.coordinator.ports.State.LoadRuntime(ctx, stateRequest)
	if err == nil {
		loaded, err = execution.ClassifyStatePreflight(stateRequest, loaded)
	}
	if err != nil {
		stream.coordinator.observe(ctx, observability.ComponentState, observability.StageStatePreflight,
			stream.request.Operation, started, "", "", err)
		return fmt.Errorf("alarmd worker: series state preflight: %w", err)
	}
	stateResult, stateReason := summarizeStateLoad(loaded)
	stream.coordinator.observeWithCounts(ctx, observability.ComponentState, observability.StageStatePreflight,
		stream.request.Operation, started, stateResult, stateReason, observability.Counts{Keys: int64(len(loaded.Items))}, nil)
	for index, entry := range batch {
		if err := stream.evaluateLoadedSeries(ctx, entry, loaded.Items[index]); err != nil {
			return err
		}
	}
	return nil
}

func (stream *streamedExecution) evaluateLoadedSeries(ctx context.Context, entry completedSeries, view execution.RuntimeStateView) error {
	due, series, inputs := entry.due, entry.series, entry.inputs
	stateItems := []execution.StatePreflightItem{entry.item}
	loaded := execution.StatePreflightResult{Items: []execution.RuntimeStateView{view}}
	evaluationHeader, err := bindAlwaysEffectiveTimeFacts(stream.header, stateItems, stream.effective)
	if err != nil {
		return fmt.Errorf("alarmd worker: bind series EffectiveTime facts: %w", err)
	}
	request := execution.EvaluationRequest{Header: evaluationHeader, Inputs: inputs, State: loaded, Gaps: stream.gaps}
	started := time.Now()
	evaluated, err := stream.coordinator.ports.Evaluator.Evaluate(ctx, request)
	if err != nil {
		stream.coordinator.observe(ctx, observability.ComponentEvaluation, observability.StageEvaluationCompleted,
			stream.request.Operation, started, "", "", err)
		return fmt.Errorf("alarmd worker: evaluate series: %w", err)
	}
	incomplete := make([]execution.NamedInputBinding, 0)
	for _, input := range inputs {
		for _, binding := range input.Inputs {
			if binding.Completeness != execution.CompletenessFull {
				incomplete = append(incomplete, binding)
			}
		}
	}
	if len(incomplete) != 0 {
		if len(evaluated.Plans) != 1 || evaluated.Plans[0].Plan != due.Identity {
			return errors.New("alarmd worker: incomplete named input produced an invalid Plan result")
		}
		mutation, mutationErr := stream.completionGapMutation(due, incomplete)
		if mutationErr != nil {
			return mutationErr
		}
		evaluated.Plans[0].GuardBeforeEvents = []execution.PlanGapMutation{mutation}
		evaluated.Plans[0].GuardAfterState = nil
	}
	if err := evaluated.Validate(request); err != nil {
		stream.coordinator.observe(ctx, observability.ComponentEvaluation, observability.StageEvaluationCompleted,
			stream.request.Operation, started, "", "", err)
		return fmt.Errorf("alarmd worker: invalid series evaluation: %w", err)
	}
	stream.observeEvaluationCompleted(ctx, started, due, series, inputs, evaluated)
	retained, err := evaluationRetainedSize(loaded, execution.EvaluationResult{})
	if err != nil {
		return fmt.Errorf("alarmd worker: measure evaluated retention: %w", err)
	}
	if err := stream.mergeProvisional(ctx, evaluated, retained); err != nil {
		return err
	}
	stream.state.Items = append(stream.state.Items, loaded.Items...)
	stream.stateItems = append(stream.stateItems, stateItems...)
	return nil
}

func (stream *streamedExecution) observeEvaluationCompleted(
	ctx context.Context,
	started time.Time,
	due execution.DuePlan,
	series execution.SeriesIdentityDigest,
	inputs []execution.SeriesEvaluationInputRequest,
	evaluated execution.EvaluationResult,
) {
	defer func() { _ = recover() }()
	evaluations, namedInputs := stream.algorithmObservationFacts(due, inputs, evaluated)
	observation := observability.Observation{
		Component: observability.ComponentEvaluation, Stage: observability.StageEvaluationCompleted,
		Result: evaluated.Result, Operation: observability.Operation(stream.request.Operation),
		Direction: observability.DirectionInternal, ReasonCode: evaluated.ReasonCode,
		Duration: time.Since(started), Counts: observability.Counts{Records: evaluationRecordCount(inputs)},
		Trace:                observability.TraceFields{StrategyID: due.Identity.StrategyID, DimensionIdentityDigest: string(series)},
		AlgorithmEvaluations: evaluations, AlgorithmInputs: namedInputs,
	}
	stream.coordinator.ports.Observer.Observe(ctx, observation)
}

func (stream *streamedExecution) observeCompletionOnlyPlan(
	ctx context.Context,
	due execution.DuePlan,
	evaluated execution.EvaluationResult,
) {
	defer func() { _ = recover() }()
	observation := observability.Observation{
		Component: observability.ComponentEvaluation, Stage: observability.StageEvaluationCompleted,
		Result: evaluated.Result, Operation: observability.Operation(stream.request.Operation),
		Direction: observability.DirectionInternal, ReasonCode: evaluated.ReasonCode,
		Trace:           observability.TraceFields{StrategyID: due.Identity.StrategyID},
		AlgorithmInputs: stream.completionOnlyAlgorithmInputFacts(due),
	}
	stream.coordinator.ports.Observer.Observe(ctx, observation)
}

type observedAlgorithm struct {
	family       observability.AlgorithmFamily
	detector     observability.AlgorithmDetectorKind
	requirements map[execution.RequirementID]struct{}
}

func (stream *streamedExecution) algorithmObservationFacts(
	due execution.DuePlan,
	inputs []execution.SeriesEvaluationInputRequest,
	evaluated execution.EvaluationResult,
) ([]observability.AlgorithmEvaluationFact, []observability.AlgorithmInputFact) {
	if due.CompiledPlan == nil || len(evaluated.Plans) != 1 || evaluated.Plans[0].Plan != due.Identity {
		return nil, nil
	}
	levels := make(map[uint32][]observedAlgorithm, len(due.CompiledPlan.Levels()))
	for _, level := range due.CompiledPlan.Levels() {
		for _, algorithm := range level.Algorithms() {
			observed, ok := observeAlgorithm(algorithm)
			if ok {
				levels[level.Definition().LevelID] = append(levels[level.Definition().LevelID], observed)
			}
		}
	}
	evaluations := make([]observability.AlgorithmEvaluationFact, 0)
	inputsByLevel := make(map[uint32]execution.SeriesEvaluationInputRequest, len(inputs))
	for _, input := range inputs {
		inputsByLevel[input.Consumer.LevelID] = input
	}
	inputFacts := make([]observability.AlgorithmInputFact, 0)
	for _, outcome := range evaluated.Plans[0].LevelOutcomes {
		algorithms := levels[outcome.LevelID]
		for _, algorithm := range algorithms {
			evaluations = append(evaluations, observability.AlgorithmEvaluationFact{
				SourceAlgorithmFamily: algorithm.family, DetectorKind: algorithm.detector,
				Result: algorithmEvaluationResult(outcome.Outcome), ReasonCode: outcome.ReasonCode,
				Provenance: observability.AlgorithmProvenance{LevelID: outcome.LevelID, SourceTime: outcome.Record.SourceTime},
			})
			input, found := inputsByLevel[outcome.LevelID]
			if !found {
				continue
			}
			for _, binding := range input.Inputs {
				if !algorithmObservesBinding(algorithm, binding) {
					continue
				}
				inputFacts = append(inputFacts, stream.algorithmInputFacts(algorithm, outcome, binding)...)
			}
		}
	}
	return evaluations, inputFacts
}

func observeAlgorithm(algorithm strategy.CompiledAlgorithmPlan) (observedAlgorithm, bool) {
	observed := observedAlgorithm{requirements: make(map[execution.RequirementID]struct{})}
	switch algorithm.Kind() {
	case strategy.DetectorKindThreshold:
		observed.family = observability.AlgorithmFamilyThreshold
		observed.detector = observability.AlgorithmDetectorKindThreshold
	case strategy.DetectorKindSimpleRingRatio:
		observed.family = observability.AlgorithmFamilySimpleRingRatio
		observed.detector = observability.AlgorithmDetectorKindSimpleRingRatio
	case strategy.DetectorKindOsRestart:
		observed.family = observability.AlgorithmFamilyOsRestart
		observed.detector = observability.AlgorithmDetectorKindOsRestart
	case strategy.DetectorKindProcPort:
		observed.family = observability.AlgorithmFamilyProcPort
		observed.detector = observability.AlgorithmDetectorKindProcPort
	default:
		return observedAlgorithm{}, false
	}
	if provenance, ok := algorithm.SourceProvenance(); ok &&
		provenance.SourceAlgorithmFamily == strategy.SourceAlgorithmFamilyPingUnreachable &&
		algorithm.Kind() == strategy.DetectorKindThreshold {
		observed.family = observability.AlgorithmFamilyPingUnreachable
	}
	for _, requirement := range algorithm.InputRequirements() {
		observed.requirements[execution.RequirementID(requirement.RequirementID)] = struct{}{}
	}
	return observed, true
}

func algorithmObservesBinding(algorithm observedAlgorithm, binding execution.NamedInputBinding) bool {
	if len(algorithm.requirements) == 0 {
		return binding.Role == execution.InputRolePrimary
	}
	_, found := algorithm.requirements[binding.RequirementID]
	return found
}

func (stream *streamedExecution) completionOnlyAlgorithmInputFacts(
	due execution.DuePlan,
) []observability.AlgorithmInputFact {
	if due.CompiledPlan == nil {
		return nil
	}
	bindings := planBindings(stream.bindings, due.Identity)
	facts := make([]observability.AlgorithmInputFact, 0)
	for _, level := range due.CompiledPlan.Levels() {
		levelID := level.Definition().LevelID
		for _, compiled := range level.Algorithms() {
			algorithm, observed := observeAlgorithm(compiled)
			if !observed {
				continue
			}
			for _, binding := range bindings {
				if !binding.Consumer.HasLevel || binding.Consumer.LevelID != levelID ||
					!algorithmObservesBinding(algorithm, binding) {
					continue
				}
				facts = append(facts, stream.completionOnlyAlgorithmBindingFacts(algorithm, levelID, binding)...)
			}
		}
	}
	return facts
}

func (stream *streamedExecution) completionOnlyAlgorithmBindingFacts(
	algorithm observedAlgorithm,
	levelID uint32,
	binding execution.NamedInputBinding,
) []observability.AlgorithmInputFact {
	key := struct {
		consumer    execution.ConsumerRef
		requirement execution.RequirementID
	}{consumer: binding.Consumer, requirement: binding.RequirementID}
	requirement, known := stream.prepared.requirementByKey[key]
	query, queryKnown := stream.prepared.queries[binding.Provenance.PhysicalQuery]
	if !known || !queryKnown || execution.LogicalQueryRef(query.QueryRevision) != requirement.LogicalQueryRef {
		return nil
	}
	result, reason, valid := completionOnlyAlgorithmInputResult(binding)
	if !valid {
		return nil
	}
	points := make([]struct {
		name       observability.AlgorithmInputName
		dependency observability.AlgorithmDependencyPoint
	}, 0, len(requirement.NamedPoints)+1)
	if binding.Role == execution.InputRolePrimary {
		points = append(points, struct {
			name       observability.AlgorithmInputName
			dependency observability.AlgorithmDependencyPoint
		}{name: observability.AlgorithmInputNamePrimary, dependency: observability.AlgorithmDependencyPointCurrent})
	} else {
		for _, point := range requirement.NamedPoints {
			dependency, observed := observedDependencyPoint(point.Name)
			if observed {
				points = append(points, struct {
					name       observability.AlgorithmInputName
					dependency observability.AlgorithmDependencyPoint
				}{name: observability.AlgorithmInputNameHistory, dependency: dependency})
			}
		}
	}
	facts := make([]observability.AlgorithmInputFact, 0, len(points))
	for _, point := range points {
		facts = append(facts, observability.AlgorithmInputFact{
			SourceAlgorithmFamily: algorithm.family, DetectorKind: algorithm.detector,
			InputName: point.name, DependencyPoint: point.dependency, Result: result, ReasonCode: reason,
			Provenance: observability.AlgorithmProvenance{
				LevelID: levelID, RequirementID: string(binding.RequirementID),
				QueryRef: string(binding.Provenance.PhysicalQuery), QueryRevision: string(query.QueryRevision),
				QueryStart: binding.QueryWindow.Start, QueryEnd: binding.QueryWindow.End,
			},
		})
	}
	return facts
}

func completionOnlyAlgorithmInputResult(
	binding execution.NamedInputBinding,
) (observability.AlgorithmInputResult, observability.ReasonCode, bool) {
	switch binding.Completeness {
	case execution.CompletenessFull:
		if binding.DataState == execution.DataStateEmpty && binding.Disposition == execution.AccessAvailable {
			return observability.AlgorithmInputResultMissing, observability.ReasonNone, true
		}
	case execution.CompletenessPartial:
		return observability.AlgorithmInputResultPartial, binding.ReasonCode, true
	case execution.CompletenessUnavailable:
		return observability.AlgorithmInputResultUnavailable, binding.ReasonCode, true
	}
	return "", "", false
}

func algorithmEvaluationResult(outcome execution.LevelOutcomeKind) observability.AlgorithmEvaluationResult {
	switch outcome {
	case execution.LevelOutcomeNormal:
		return observability.AlgorithmEvaluationResultNormal
	case execution.LevelOutcomeAbnormal:
		return observability.AlgorithmEvaluationResultAbnormal
	case execution.LevelOutcomeRecovery:
		return observability.AlgorithmEvaluationResultRecovery
	case execution.LevelOutcomeTerminal:
		return observability.AlgorithmEvaluationResultTerminal
	default:
		return observability.AlgorithmEvaluationResultUnavailable
	}
}

func (stream *streamedExecution) algorithmInputFacts(
	algorithm observedAlgorithm,
	outcome execution.LevelOutcome,
	binding execution.NamedInputBinding,
) []observability.AlgorithmInputFact {
	key := struct {
		consumer    execution.ConsumerRef
		requirement execution.RequirementID
	}{consumer: binding.Consumer, requirement: binding.RequirementID}
	requirement, known := stream.prepared.requirementByKey[key]
	query, queryKnown := stream.prepared.queries[binding.Provenance.PhysicalQuery]
	if !known || !queryKnown || execution.LogicalQueryRef(query.QueryRevision) != requirement.LogicalQueryRef {
		return nil
	}
	points := []struct {
		name       observability.AlgorithmInputName
		dependency observability.AlgorithmDependencyPoint
		sourceTime int64
	}{}
	if binding.Role == execution.InputRolePrimary {
		points = append(points, struct {
			name       observability.AlgorithmInputName
			dependency observability.AlgorithmDependencyPoint
			sourceTime int64
		}{observability.AlgorithmInputNamePrimary, observability.AlgorithmDependencyPointCurrent, outcome.Record.SourceTime})
	} else {
		for _, point := range requirement.NamedPoints {
			dependency, ok := observedDependencyPoint(point.Name)
			if ok {
				points = append(points, struct {
					name       observability.AlgorithmInputName
					dependency observability.AlgorithmDependencyPoint
					sourceTime int64
				}{observability.AlgorithmInputNameHistory, dependency, outcome.Record.SourceTime - point.OffsetSeconds})
			}
		}
	}
	facts := make([]observability.AlgorithmInputFact, 0, len(points))
	for _, point := range points {
		result, reason := algorithmInputResult(binding, outcome.SeriesIdentityDigest, point.sourceTime)
		facts = append(facts, observability.AlgorithmInputFact{
			SourceAlgorithmFamily: algorithm.family, DetectorKind: algorithm.detector,
			InputName: point.name, DependencyPoint: point.dependency, Result: result, ReasonCode: reason,
			Provenance: observability.AlgorithmProvenance{
				LevelID: outcome.LevelID, RequirementID: string(binding.RequirementID),
				QueryRef: string(binding.Provenance.PhysicalQuery), QueryRevision: string(query.QueryRevision),
				SourceTime: point.sourceTime, QueryStart: binding.QueryWindow.Start, QueryEnd: binding.QueryWindow.End,
			},
		})
	}
	return facts
}

func observedDependencyPoint(name string) (observability.AlgorithmDependencyPoint, bool) {
	switch name {
	case "previous":
		return observability.AlgorithmDependencyPointPrevious, true
	case "previous_10m":
		return observability.AlgorithmDependencyPointTenMinute, true
	case "previous_25m":
		return observability.AlgorithmDependencyPointTwentyFiveMinute, true
	default:
		return "", false
	}
}

func algorithmInputResult(
	binding execution.NamedInputBinding,
	series execution.SeriesIdentityDigest,
	sourceTime int64,
) (observability.AlgorithmInputResult, observability.ReasonCode) {
	switch binding.Completeness {
	case execution.CompletenessPartial:
		return observability.AlgorithmInputResultPartial, binding.ReasonCode
	case execution.CompletenessUnavailable:
		return observability.AlgorithmInputResultUnavailable, binding.ReasonCode
	case execution.CompletenessFull:
	default:
		return observability.AlgorithmInputResultUnavailable, binding.ReasonCode
	}
	if binding.Disposition != execution.AccessAvailable || binding.Dataset == nil || binding.View == nil {
		return observability.AlgorithmInputResultUnavailable, binding.ReasonCode
	}
	for _, fact := range binding.QualityFacts {
		if inputFactApplies(fact.ImpactScope, fact.SeriesIdentity, fact.SourceTime, series, sourceTime) {
			return observability.AlgorithmInputResultUnavailable, fact.ReasonCode
		}
	}
	for _, terminal := range binding.Terminals {
		if inputFactApplies(terminal.ImpactScope, terminal.SeriesIdentity, terminal.SourceTime, series, sourceTime) {
			return observability.AlgorithmInputResultUnavailable, terminal.ReasonCode
		}
	}
	for index := 0; index < binding.View.Len(); index++ {
		record, ok := binding.View.Record(index)
		if ok && execution.SeriesIdentityDigest(record.DimensionIdentity().Digest) == series && record.SourceTime() == sourceTime {
			return observability.AlgorithmInputResultAvailable, observability.ReasonNone
		}
	}
	return observability.AlgorithmInputResultMissing, observability.ReasonCode(contract.ReasonHistoryGapped)
}

func inputFactApplies(
	scope execution.ImpactScope,
	factSeries execution.SeriesIdentityDigest,
	factSourceTime int64,
	series execution.SeriesIdentityDigest,
	sourceTime int64,
) bool {
	if scope == execution.ImpactPlan || scope == execution.ImpactLevel {
		return true
	}
	return scope == execution.ImpactSeries && factSeries == series && factSourceTime == sourceTime
}

func evaluationRecordCount(inputs []execution.SeriesEvaluationInputRequest) int64 {
	for _, input := range inputs {
		for _, binding := range input.Inputs {
			if binding.Role == execution.InputRolePrimary && binding.View != nil {
				return int64(binding.View.Len())
			}
		}
	}
	return 0
}

func evaluationRetainedSize(state execution.StatePreflightResult, result execution.EvaluationResult) (uint64, error) {
	return retainedObjectBytes(state) + retainedObjectBytes(result), nil
}

func planCompletedFullEmpty(bindings []execution.NamedInputBinding, plan execution.PlanIdentity) bool {
	found := false
	for _, binding := range bindings {
		if binding.Consumer.Plan != plan {
			continue
		}
		found = true
		if binding.Completeness != execution.CompletenessFull || binding.DataState != execution.DataStateEmpty ||
			binding.Disposition != execution.AccessAvailable {
			return false
		}
	}
	return found
}

func (stream *streamedExecution) completionGapMutation(
	due execution.DuePlan,
	bindings []execution.NamedInputBinding,
) (execution.PlanGapMutation, error) {
	identity := execution.PlanGapIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration}
	marker, found := stream.gaps.Find(identity)
	if !found {
		return execution.PlanGapMutation{}, errors.New("alarmd worker: Plan gap marker is absent from validated result")
	}
	kind, err := ensureGappedKind(marker)
	if err != nil {
		return execution.PlanGapMutation{}, err
	}
	version, err := execution.BuildApplyVersion(stream.header.Contract, due.StateApplyEpoch)
	if err != nil {
		return execution.PlanGapMutation{}, err
	}
	required := requiredFullSlots(due.CompiledPlan)
	if required == 0 {
		return execution.PlanGapMutation{}, errors.New("alarmd worker: Plan gap requires positive FULL warmup slots")
	}
	reasons := make(map[execution.GapScope]execution.ReasonCode)
	for _, binding := range bindings {
		if binding.Consumer.Plan != due.Identity || binding.Completeness == execution.CompletenessFull {
			continue
		}
		scope := execution.GapScope{LevelID: binding.Consumer.LevelID, HasLevel: binding.Consumer.HasLevel}
		if previous, duplicate := reasons[scope]; duplicate && previous != binding.ReasonCode {
			return execution.PlanGapMutation{}, errors.New("alarmd worker: one gap scope has conflicting completion reasons")
		}
		reasons[scope] = binding.ReasonCode
	}
	if len(reasons) == 0 {
		return execution.PlanGapMutation{}, errors.New("alarmd worker: incomplete named input requires a gap scope")
	}
	scopes := make([]execution.GapScope, 0, len(reasons))
	for scope := range reasons {
		scopes = append(scopes, scope)
	}
	sort.Slice(scopes, func(i, j int) bool {
		if scopes[i].HasLevel != scopes[j].HasLevel {
			return !scopes[i].HasLevel
		}
		return scopes[i].LevelID < scopes[j].LevelID
	})
	mutations := make([]execution.GapScopeMutation, 0, len(scopes))
	for _, scope := range scopes {
		mutations = append(mutations, execution.GapScopeMutation{Scope: scope, Kind: kind,
			ReasonCode: reasons[scope], RequiredFullSlots: required})
	}
	mutation, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{
		Identity: identity, ExpectedMarkerRevision: marker.MarkerRevision,
		ApplyVersion: version, ScheduleRevision: due.ScheduleRevision,
		Scopes: mutations,
	})
	if err != nil {
		return execution.PlanGapMutation{}, fmt.Errorf("alarmd worker: build completion Plan gap: %w", err)
	}
	return mutation, nil
}

func requiredFullSlots(plan *strategy.CompiledPlan) uint32 {
	if plan == nil {
		return 0
	}
	var required uint32
	for _, level := range plan.Levels() {
		if points := level.RequiredDetectHistoryPoints(); points > required {
			required = points
		}
	}
	return required
}

func physicalFailureReason(facts execution.ProviderRouteFacts) execution.ReasonCode {
	for index := len(facts.Attempts) - 1; index >= 0; index-- {
		if facts.Attempts[index].ReasonCode != "" {
			return facts.Attempts[index].ReasonCode
		}
	}
	return execution.ReasonCode(contract.ReasonQueryUnavailable)
}

func provisionalResult(result execution.EvaluationResult) (observability.Result, execution.ReasonCode) {
	return result.Result, result.ReasonCode
}
