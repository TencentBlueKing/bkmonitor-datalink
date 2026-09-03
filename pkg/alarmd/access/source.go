// Package access implements the phase-two Go query execution source. It owns
// query planning and provider adaptation, but no evaluation or side effects.
package access

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

var ErrFrozenQueryPlanUnavailable = errors.New("alarmd access: frozen QueryPlanFacts unavailable")

type FrozenPlan struct {
	DuePlans           []execution.DuePlan
	Requirements       []execution.DataRequirement
	EffectiveTimeFacts []execution.BoundEffectiveTimeFact
	QueryFacts         map[execution.LogicalQueryRef]execution.QueryPlanFacts
}

type FrozenPlanSource interface {
	ResolveFrozenPlan(context.Context, execution.FrozenExecutionContractRef) (FrozenPlan, error)
}

type QueryPermit interface {
	RecoveryPermit() *execution.RecoveryPermit
	Release()
}

type QueryPermitAcquirer interface {
	AcquireQueryPermit(context.Context, execution.SlotIdentity, execution.Operation, time.Time) (QueryPermit, error)
}

type Config struct {
	MinReadyDelay time.Duration
}

type Source struct {
	plans    FrozenPlanSource
	provider execution.QueryProvider
	permits  QueryPermitAcquirer
	config   Config
	now      func() time.Time
	wait     func(context.Context, time.Duration) error
}

func NewSource(
	plans FrozenPlanSource,
	provider execution.QueryProvider,
	permits QueryPermitAcquirer,
	config Config,
) (*Source, error) {
	if plans == nil || provider == nil || permits == nil || config.MinReadyDelay <= 0 {
		return nil, errors.New("alarmd access: frozen plan source, provider, query permits and non-zero readiness delay are required")
	}
	return &Source{plans: plans, provider: provider, permits: permits, config: config, now: time.Now, wait: waitContext}, nil
}

func (source *Source) Execute(ctx context.Context, request execution.QueryExecutionRequest, consumer execution.QueryExecutionConsumer) (execution.QueryExecutionCompletion, error) {
	if source == nil || consumer == nil {
		return execution.QueryExecutionCompletion{}, errors.New("alarmd access: initialized source and consumer are required")
	}
	if err := request.Validate(); err != nil {
		return execution.QueryExecutionCompletion{}, err
	}
	var recoveryStartedAt time.Time
	if request.Operation != execution.OperationNormal {
		recoveryStartedAt = source.now()
		if recoveryStartedAt.IsZero() {
			return execution.QueryExecutionCompletion{}, errors.New("alarmd access: recovery query start time is required")
		}
	}
	frozen, err := source.plans.ResolveFrozenPlan(ctx, request.Contract)
	if err != nil {
		return execution.QueryExecutionCompletion{}, fmt.Errorf("alarmd access: resolve frozen plan: %w", err)
	}
	prepared, err := prepare(request.Contract, frozen, source.config.MinReadyDelay, request.Operation != execution.OperationNormal)
	if err != nil {
		return execution.QueryExecutionCompletion{}, err
	}
	recoveryDeadline, budgetErr := deriveRecoveryQueryDeadline(request, frozen, recoveryStartedAt)
	if budgetErr != nil || (!recoveryDeadline.IsZero() && !recoveryDeadline.After(source.now())) {
		if err := consumer.Begin(ctx, prepared.Header); err != nil {
			return execution.QueryExecutionCompletion{}, err
		}
		return completeBudgetExhaustedQueries(execution.QueryExecutionCompletion{}, prepared.Queries, request.AttemptNo), nil
	}
	if err := consumer.Begin(ctx, prepared.Header); err != nil {
		return execution.QueryExecutionCompletion{}, err
	}
	completion := execution.QueryExecutionCompletion{PhysicalQueries: make([]execution.PhysicalQueryCompletion, 0, len(prepared.Queries))}
	for queryIndex, query := range prepared.Queries {
		if delay := time.UnixMilli(query.ReadyAtUnixMilli).Sub(source.now()); delay > 0 {
			if err := source.wait(ctx, delay); err != nil {
				return execution.QueryExecutionCompletion{}, err
			}
		}
		queryDeadline := time.UnixMilli(query.DeadlineUnixMilli)
		if !recoveryDeadline.IsZero() {
			queryDeadline = recoveryDeadline
		}
		permit, err := source.permits.AcquireQueryPermit(ctx, request.Contract.Slot, request.Operation, queryDeadline)
		if err != nil {
			if request.Operation != execution.OperationNormal && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				return completeBudgetExhaustedQueries(completion, prepared.Queries[queryIndex:], request.AttemptNo), nil
			}
			return execution.QueryExecutionCompletion{}, fmt.Errorf("alarmd access: acquire physical query permit: %w", err)
		}
		attempt := execution.QueryAttempt{Spec: query.Spec, Slot: request.Contract.Slot, Operation: request.Operation,
			AttemptNo: request.AttemptNo, DeadlineUnixMilli: queryDeadline.UnixMilli(), RecoveryPermit: permit.RecoveryPermit()}
		if err := attempt.Validate(); err != nil {
			permit.Release()
			return execution.QueryExecutionCompletion{}, err
		}
		adapter := &seriesAdapter{consumer: consumer, query: query, attemptNo: attempt.AttemptNo}
		providerCompletion, err := source.executeWithPermit(ctx, attempt, adapter, permit)
		if err != nil {
			return execution.QueryExecutionCompletion{}, fmt.Errorf("alarmd access: execute physical query: %w", err)
		}
		if !trustedProviderCompletion(query.Spec.Digest, providerCompletion) {
			return execution.QueryExecutionCompletion{}, errors.New("alarmd access: G1 provider returned an untrusted completion")
		}
		completion.PhysicalQueries = append(completion.PhysicalQueries, execution.PhysicalQueryCompletion{
			Ref: providerCompletion.Ref, PhysicalQuery: providerCompletion.PhysicalQuery,
			QueryRevision: query.Spec.PlanFacts.QueryRevision, Completeness: providerCompletion.Completeness,
			DataState: providerCompletion.DataState, Delivery: providerCompletion.Delivery,
			RouteFacts: providerCompletion.RouteFacts, PartialEvidence: providerCompletion.PartialEvidence,
			Stats: providerCompletion.Stats,
		})
		if providerCompletion.DataState != execution.DataStateData {
			completion.CompletionBindings = append(completion.CompletionBindings, completionBindings(query, providerCompletion, attempt.AttemptNo)...)
		}
	}
	completion.AllRequiredCompleted = true
	return completion, nil
}

func completeBudgetExhaustedQueries(
	completion execution.QueryExecutionCompletion,
	queries []PlannedQuery,
	attemptNo uint32,
) execution.QueryExecutionCompletion {
	for _, query := range queries {
		ref := execution.ProviderResultRef(fmt.Sprintf("%s:budget:%d", query.Spec.Digest, attemptNo))
		completion.PhysicalQueries = append(completion.PhysicalQueries, execution.PhysicalQueryCompletion{
			Ref: ref, PhysicalQuery: query.Spec.Digest, QueryRevision: query.Spec.PlanFacts.QueryRevision,
			Completeness: execution.CompletenessUnavailable, DataState: execution.DataStateUnknown,
		})
		for _, requirement := range query.Requirements {
			for _, consumer := range requirement.Consumers {
				completion.CompletionBindings = append(completion.CompletionBindings, execution.NamedInputBinding{
					Consumer: consumer.Consumer, RequirementID: requirement.RequirementID,
					DatasetName: requirement.DatasetName, Role: requirement.Role,
					ProviderResult: ref, QueryWindow: query.Spec.LogicalWindow,
					Completeness: execution.CompletenessUnavailable, DataState: execution.DataStateUnknown,
					Disposition: execution.AccessUnavailable,
					ReasonCode:  execution.ReasonCode(contract.ReasonExecutionBudgetExhausted),
					ImpactScope: execution.ImpactPlan,
					Provenance: execution.InputProvenance{
						PhysicalQuery: query.Spec.Digest, AttemptNo: attemptNo,
					},
				})
			}
		}
	}
	completion.AllRequiredCompleted = true
	return completion
}

func deriveRecoveryQueryDeadline(
	request execution.QueryExecutionRequest,
	frozen FrozenPlan,
	startedAt time.Time,
) (time.Time, error) {
	if request.Operation == execution.OperationNormal {
		return time.Time{}, nil
	}
	if startedAt.IsZero() || request.Contract.Slot.EvaluationTime <= 0 ||
		int64(request.Contract.Slot.EvaluationTime) > math.MaxInt64/1000 {
		return time.Time{}, errors.New("alarmd access: recovery query budget is invalid")
	}
	slotUnixMilli := int64(request.Contract.Slot.EvaluationTime) * 1000
	budgetMillis := int64(0)
	for _, requirement := range frozen.Requirements {
		for _, consumer := range requirement.Consumers {
			intervalMillis := consumer.ConsumerDeadlineUnixMilli - slotUnixMilli
			candidate := intervalMillis - consumer.DownstreamExecutionReserveMilliSec
			if intervalMillis <= 0 || consumer.DownstreamExecutionReserveMilliSec <= 0 || candidate <= 0 {
				return time.Time{}, errors.New("alarmd access: recovery query budget is invalid")
			}
			if budgetMillis == 0 || candidate < budgetMillis {
				budgetMillis = candidate
			}
		}
	}
	if budgetMillis <= 0 || budgetMillis > math.MaxInt64/int64(time.Millisecond) {
		return time.Time{}, errors.New("alarmd access: recovery query budget is invalid")
	}
	return startedAt.Add(time.Duration(budgetMillis) * time.Millisecond), nil
}

func (source *Source) executeWithPermit(
	ctx context.Context,
	attempt execution.QueryAttempt,
	adapter execution.ProviderSeriesSink,
	permit QueryPermit,
) (execution.ProviderCompletion, error) {
	defer permit.Release()
	return source.provider.Execute(ctx, attempt, adapter)
}

func trustedProviderCompletion(digest execution.PhysicalQueryDigest, completion execution.ProviderCompletion) bool {
	if completion.PhysicalQuery != digest || completion.Ref == "" {
		return false
	}
	switch completion.Completeness {
	case execution.CompletenessFull, execution.CompletenessPartial:
		return completion.DataState == execution.DataStateData || completion.DataState == execution.DataStateEmpty
	case execution.CompletenessUnavailable:
		return completion.DataState == execution.DataStateData || completion.DataState == execution.DataStateEmpty ||
			completion.DataState == execution.DataStateUnknown
	default:
		return false
	}
}

type PreparedExecution struct {
	Header  execution.InternalExecutionHeader
	Queries []PlannedQuery
}

type PlannedQuery struct {
	Spec              execution.PhysicalQuerySpec
	Requirements      []execution.DataRequirement
	ReadyAtUnixMilli  int64
	DeadlineUnixMilli int64
}

func Prepare(contractRef execution.FrozenExecutionContractRef, frozen FrozenPlan, minReadyDelay time.Duration) (PreparedExecution, error) {
	return prepare(contractRef, frozen, minReadyDelay, false)
}

func prepare(
	contractRef execution.FrozenExecutionContractRef,
	frozen FrozenPlan,
	minReadyDelay time.Duration,
	allowExhaustedRecoveryBudget bool,
) (PreparedExecution, error) {
	if err := contractRef.Validate(); err != nil {
		return PreparedExecution{}, err
	}
	if minReadyDelay <= 0 {
		return PreparedExecution{}, errors.New("alarmd access: non-zero readiness delay is required")
	}
	if len(frozen.DuePlans) == 0 || len(frozen.Requirements) == 0 {
		return PreparedExecution{}, errors.New("alarmd access: frozen execution facts are incomplete")
	}
	if len(frozen.QueryFacts) == 0 {
		return PreparedExecution{}, ErrFrozenQueryPlanUnavailable
	}
	queriesByDigest := make(map[execution.PhysicalQueryDigest]*PlannedQuery)
	for _, requirement := range frozen.Requirements {
		facts, ok := frozen.QueryFacts[requirement.LogicalQueryRef]
		if !ok {
			return PreparedExecution{}, fmt.Errorf("%w: logical query %s", ErrFrozenQueryPlanUnavailable, requirement.LogicalQueryRef)
		}
		if err := facts.Validate(); err != nil || execution.LogicalQueryRef(facts.QueryRevision) != requirement.LogicalQueryRef {
			return PreparedExecution{}, fmt.Errorf("%w: logical query %s", ErrFrozenQueryPlanUnavailable, requirement.LogicalQueryRef)
		}
		if requirement.Role == execution.InputRolePrimary && facts.QueryRevision != contractRef.QueryRevision {
			return PreparedExecution{}, errors.New("alarmd access: frozen query revision mismatch")
		}
		window := requirement.AbsoluteWindow(contractRef.Slot.EvaluationTime)
		spec, err := execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: facts,
			LogicalWindow: window, ProviderRange: window, AcceptedRange: window,
			RequiredColumns: append([]string(nil), requirement.RequiredColumns...)})
		if err != nil {
			return PreparedExecution{}, fmt.Errorf("alarmd access: build physical query: %w", err)
		}
		readyAt, err := frozenRequirementReadyAt(requirement, window, minReadyDelay)
		if err != nil {
			return PreparedExecution{}, err
		}
		deadline := int64(0)
		for _, consumer := range requirement.Consumers {
			candidate := consumer.ConsumerDeadlineUnixMilli - consumer.DownstreamExecutionReserveMilliSec
			if candidate <= readyAt && !allowExhaustedRecoveryBudget {
				return PreparedExecution{}, errors.New("alarmd access: readiness budget is invalid")
			}
			if deadline == 0 || candidate < deadline {
				deadline = candidate
			}
		}
		planned, exists := queriesByDigest[spec.Digest]
		if !exists {
			planned = &PlannedQuery{Spec: spec, ReadyAtUnixMilli: readyAt, DeadlineUnixMilli: deadline}
			queriesByDigest[spec.Digest] = planned
		} else {
			if readyAt < planned.ReadyAtUnixMilli {
				planned.ReadyAtUnixMilli = readyAt
			}
			if deadline < planned.DeadlineUnixMilli {
				planned.DeadlineUnixMilli = deadline
			}
		}
		planned.Requirements = append(planned.Requirements, requirement)
	}
	queries := make([]PlannedQuery, 0, len(queriesByDigest))
	refs := make([]execution.PlannedPhysicalQueryRef, 0, len(queriesByDigest))
	for _, query := range queriesByDigest {
		queries = append(queries, *query)
	}
	sort.Slice(queries, func(i, j int) bool {
		if queries[i].DeadlineUnixMilli == queries[j].DeadlineUnixMilli {
			return queries[i].Spec.Digest < queries[j].Spec.Digest
		}
		return queries[i].DeadlineUnixMilli < queries[j].DeadlineUnixMilli
	})
	for _, query := range queries {
		refs = append(refs, execution.PlannedPhysicalQueryRef{Digest: query.Spec.Digest, QueryRevision: query.Spec.PlanFacts.QueryRevision})
	}
	deadline := int64(0)
	for _, due := range frozen.DuePlans {
		if deadline == 0 || due.CompletionDeadlineUnixMilli < deadline {
			deadline = due.CompletionDeadlineUnixMilli
		}
	}
	executionDigest, err := contract.DeriveCanonicalDigestV2("alarmd-go-access-execution-v1", contractRef)
	if err != nil {
		return PreparedExecution{}, err
	}
	header := execution.InternalExecutionHeader{ExecutionID: executionDigest, Contract: contractRef,
		DuePlans: append([]execution.DuePlan(nil), frozen.DuePlans...), Requirements: append([]execution.DataRequirement(nil), frozen.Requirements...),
		EffectiveTimeFacts: append([]execution.BoundEffectiveTimeFact(nil), frozen.EffectiveTimeFacts...), RequiredPhysicalQueries: refs,
		DeadlineUnixMilli: deadline}
	if err := header.Validate(contractRef); err != nil {
		return PreparedExecution{}, err
	}
	return PreparedExecution{Header: header, Queries: queries}, nil
}

func frozenRequirementReadyAt(
	requirement execution.DataRequirement,
	window execution.QueryWindow,
	minReadyDelay time.Duration,
) (int64, error) {
	switch requirement.ReadinessClass {
	case execution.ReadinessEager, execution.ReadinessFinalizedRequired:
		// FINALIZED_REQUIRED is queried only after the frozen window's effective
		// readiness boundary. A bounded probe is the same Source execution with
		// OperationProbe; it does not create another query or readiness model.
		return window.End*1000 + minReadyDelay.Milliseconds(), nil
	default:
		return 0, errors.New("alarmd access: invalid frozen readiness class")
	}
}

type seriesAdapter struct {
	consumer  execution.QueryExecutionConsumer
	query     PlannedQuery
	attemptNo uint32
}

func (adapter *seriesAdapter) ConsumeProviderSeries(ctx context.Context, batch execution.ProviderSeriesBatch) error {
	if batch.PhysicalQuery != adapter.query.Spec.Digest || batch.CompletionRef == "" || batch.Dataset == nil || batch.Dataset.Len() == 0 {
		return errors.New("alarmd access: provider delivered an invalid series")
	}
	bindings, err := dataBindings(adapter.query, batch, adapter.attemptNo)
	if err != nil {
		return err
	}
	return adapter.consumer.ConsumeSeries(ctx, execution.SeriesExecutionBatch{PhysicalQuery: batch.PhysicalQuery,
		QueryRevision: adapter.query.Spec.PlanFacts.QueryRevision, CompletionRef: batch.CompletionRef,
		Dataset: batch.Dataset, Inputs: bindings, Delivery: batch.Delivery})
}

func dataBindings(query PlannedQuery, batch execution.ProviderSeriesBatch, attemptNo uint32) ([]execution.NamedInputBinding, error) {
	ordinals := make([]uint32, batch.Dataset.Len())
	for index := range ordinals {
		ordinals[index] = uint32(index)
	}
	view, err := execution.NewDatasetView(batch.Dataset, ordinals)
	if err != nil {
		return nil, err
	}
	bindings := make([]execution.NamedInputBinding, 0)
	for _, requirement := range query.Requirements {
		for _, consumer := range requirement.Consumers {
			bindings = append(bindings, execution.NamedInputBinding{Consumer: consumer.Consumer,
				RequirementID: requirement.RequirementID, DatasetName: requirement.DatasetName, Role: requirement.Role,
				ProviderResult: batch.CompletionRef, QueryWindow: query.Spec.LogicalWindow,
				Dataset: batch.Dataset, View: view, Completeness: execution.CompletenessFull, DataState: execution.DataStateData,
				Disposition: execution.AccessAvailable, ImpactScope: execution.ImpactPlan,
				Provenance: execution.InputProvenance{PhysicalQuery: batch.PhysicalQuery, AttemptNo: attemptNo}})
		}
	}
	return bindings, nil
}

func completionBindings(query PlannedQuery, completion execution.ProviderCompletion, attemptNo uint32) []execution.NamedInputBinding {
	bindings := make([]execution.NamedInputBinding, 0)
	var dataset *execution.Dataset
	var view *execution.DatasetView
	dataState := completion.DataState
	disposition := execution.AccessAvailable
	var reason execution.ReasonCode
	switch completion.Completeness {
	case execution.CompletenessFull:
		dataset = execution.NewDataset(nil)
		view, _ = execution.NewDatasetView(dataset, nil)
	case execution.CompletenessPartial:
		dataset = execution.NewDataset(nil)
		view, _ = execution.NewDatasetView(dataset, nil)
		disposition = execution.AccessDegraded
		reason = execution.ReasonCode(contract.ReasonQueryPartial)
	case execution.CompletenessUnavailable:
		dataState = execution.DataStateUnknown
		disposition = execution.AccessUnavailable
		reason = providerFailureReason(completion.RouteFacts)
	}
	for _, requirement := range query.Requirements {
		for _, consumer := range requirement.Consumers {
			bindings = append(bindings, execution.NamedInputBinding{Consumer: consumer.Consumer,
				RequirementID: requirement.RequirementID, DatasetName: requirement.DatasetName, Role: requirement.Role,
				ProviderResult: completion.Ref, QueryWindow: query.Spec.LogicalWindow, Dataset: dataset, View: view,
				Completeness: completion.Completeness, DataState: dataState,
				Disposition: disposition, ReasonCode: reason, ImpactScope: execution.ImpactPlan,
				PartialEvidence: completion.PartialEvidence,
				Provenance:      execution.InputProvenance{PhysicalQuery: query.Spec.Digest, AttemptNo: attemptNo}})
		}
	}
	return bindings
}

func providerFailureReason(facts execution.ProviderRouteFacts) execution.ReasonCode {
	for index := len(facts.Attempts) - 1; index >= 0; index-- {
		if facts.Attempts[index].ReasonCode != "" {
			return facts.Attempts[index].ReasonCode
		}
	}
	return execution.ReasonCode(contract.ReasonQueryUnavailable)
}

func waitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
