// Package access implements the phase-two Go query execution source. It owns
// query planning and provider adaptation, but no evaluation or side effects.
package access

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

var ErrFinalizedRequiredUnsupported = errors.New("alarmd access: FINALIZED_REQUIRED is not supported in G1")

type FrozenPlan struct {
	DuePlans           []execution.DuePlan
	Requirements       []execution.DataRequirement
	EffectiveTimeFacts []execution.BoundEffectiveTimeFact
	QueryFacts         map[execution.LogicalQueryRef]execution.QueryPlanFacts
}

type FrozenPlanSource interface {
	ResolveFrozenPlan(context.Context, execution.FrozenExecutionContractRef) (FrozenPlan, error)
}

type Config struct {
	MinReadyDelay time.Duration
}

type Source struct {
	plans    FrozenPlanSource
	provider execution.QueryProvider
	config   Config
	now      func() time.Time
	wait     func(context.Context, time.Duration) error
}

func NewSource(plans FrozenPlanSource, provider execution.QueryProvider, config Config) (*Source, error) {
	if plans == nil || provider == nil || config.MinReadyDelay <= 0 {
		return nil, errors.New("alarmd access: frozen plan source, provider and non-zero readiness delay are required")
	}
	return &Source{plans: plans, provider: provider, config: config, now: time.Now, wait: waitContext}, nil
}

func (source *Source) Execute(ctx context.Context, request execution.QueryExecutionRequest, consumer execution.QueryExecutionConsumer) (execution.QueryExecutionCompletion, error) {
	if source == nil || consumer == nil {
		return execution.QueryExecutionCompletion{}, errors.New("alarmd access: initialized source and consumer are required")
	}
	if err := request.Contract.Validate(); err != nil {
		return execution.QueryExecutionCompletion{}, err
	}
	if err := request.Operation.Validate(); err != nil {
		return execution.QueryExecutionCompletion{}, err
	}
	// G1 executes only the normal path. Recovery operations remain owned by the
	// single coordinator and scheduler in later gates.
	if request.Operation != execution.OperationNormal {
		return execution.QueryExecutionCompletion{}, errors.New("alarmd access: G1 supports only normal query execution")
	}
	frozen, err := source.plans.ResolveFrozenPlan(ctx, request.Contract)
	if err != nil {
		return execution.QueryExecutionCompletion{}, fmt.Errorf("alarmd access: resolve frozen plan: %w", err)
	}
	prepared, err := Prepare(request.Contract, frozen, source.config.MinReadyDelay)
	if err != nil {
		return execution.QueryExecutionCompletion{}, err
	}
	if err := consumer.Begin(ctx, prepared.Header); err != nil {
		return execution.QueryExecutionCompletion{}, err
	}
	completion := execution.QueryExecutionCompletion{PhysicalQueries: make([]execution.PhysicalQueryCompletion, 0, len(prepared.Queries))}
	for _, query := range prepared.Queries {
		if delay := time.UnixMilli(query.ReadyAtUnixMilli).Sub(source.now()); delay > 0 {
			if err := source.wait(ctx, delay); err != nil {
				return execution.QueryExecutionCompletion{}, err
			}
		}
		attempt := execution.QueryAttempt{Spec: query.Spec, Slot: request.Contract.Slot, Operation: request.Operation,
			AttemptNo: 1, DeadlineUnixMilli: query.DeadlineUnixMilli}
		adapter := &seriesAdapter{consumer: consumer, query: query, attemptNo: attempt.AttemptNo}
		providerCompletion, err := source.provider.Execute(ctx, attempt, adapter)
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
	if err := contractRef.Validate(); err != nil {
		return PreparedExecution{}, err
	}
	if minReadyDelay <= 0 {
		return PreparedExecution{}, errors.New("alarmd access: non-zero readiness delay is required")
	}
	if len(frozen.DuePlans) == 0 || len(frozen.Requirements) == 0 || len(frozen.QueryFacts) == 0 {
		return PreparedExecution{}, errors.New("alarmd access: frozen execution facts are incomplete")
	}
	for _, requirement := range frozen.Requirements {
		if requirement.ReadinessClass == execution.ReadinessFinalizedRequired {
			return PreparedExecution{}, ErrFinalizedRequiredUnsupported
		}
	}
	queriesByDigest := make(map[execution.PhysicalQueryDigest]*PlannedQuery)
	for _, requirement := range frozen.Requirements {
		facts, ok := frozen.QueryFacts[requirement.LogicalQueryRef]
		if !ok {
			return PreparedExecution{}, errors.New("alarmd access: DataRequirement has no frozen QueryPlanFacts")
		}
		if facts.QueryRevision != contractRef.QueryRevision {
			return PreparedExecution{}, errors.New("alarmd access: frozen query revision mismatch")
		}
		window := requirement.AbsoluteWindow(contractRef.Slot.EvaluationTime)
		spec, err := execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: facts,
			LogicalWindow: window, ProviderRange: window, AcceptedRange: window,
			RequiredColumns: append([]string(nil), requirement.RequiredColumns...)})
		if err != nil {
			return PreparedExecution{}, fmt.Errorf("alarmd access: build physical query: %w", err)
		}
		readyAt := window.End*1000 + minReadyDelay.Milliseconds()
		deadline := int64(0)
		for _, consumer := range requirement.Consumers {
			candidate := consumer.ConsumerDeadlineUnixMilli - consumer.DownstreamExecutionReserveMilliSec
			if candidate <= readyAt {
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
