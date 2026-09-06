// Package access implements the phase-two Go query execution source. It owns
// query planning and provider adaptation, but no evaluation or side effects.
package access

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

var ErrFrozenQueryPlanUnavailable = errors.New("alarmd access: frozen QueryPlanFacts unavailable")

const thirtySecondReadyDelay = 10 * time.Second

type ReadinessDeferredError struct{ readyAt time.Time }

func (err *ReadinessDeferredError) Error() string { return "alarmd access: execution is not ready" }

func (err *ReadinessDeferredError) ReadinessReadyAt() time.Time {
	if err == nil {
		return time.Time{}
	}
	return err.readyAt
}

// ReadinessDeferredAt returns the next normal-execution readiness boundary
// carried by err. Wrapped errors preserve the boundary.
func ReadinessDeferredAt(err error) (time.Time, bool) {
	var deferred interface{ ReadinessReadyAt() time.Time }
	if !errors.As(err, &deferred) {
		return time.Time{}, false
	}
	readyAt := deferred.ReadinessReadyAt()
	return readyAt, !readyAt.IsZero()
}

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
	Now           func() time.Time // Defaults to time.Now.
	Observer      observability.Observer
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
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &Source{plans: plans, provider: provider, permits: permits, config: config, now: now, wait: waitContext}, nil
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
	source.observeQueryTiming(ctx, request, frozen, prepared, recoveryStartedAt, recoveryDeadline)
	if budgetErr != nil || (!recoveryDeadline.IsZero() && !recoveryDeadline.After(source.now())) {
		if err := consumer.Begin(ctx, prepared.Header); err != nil {
			return execution.QueryExecutionCompletion{}, err
		}
		return completeBudgetExhaustedQueries(execution.QueryExecutionCompletion{}, prepared.Queries, request.AttemptNo), nil
	}
	if request.Operation == execution.OperationNormal {
		readyAt := sharedPendingReadiness(prepared.Queries, source.now())
		if !readyAt.IsZero() {
			return execution.QueryExecutionCompletion{}, &ReadinessDeferredError{readyAt: readyAt}
		}
	}
	if err := consumer.Begin(ctx, prepared.Header); err != nil {
		return execution.QueryExecutionCompletion{}, err
	}
	// Only one permit acquisition per Source is pending at a time. Queries
	// already admitted run independently; all attempts join before returning.
	queryCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var running sync.WaitGroup
	var queryFailure sync.Once
	var firstQueryErr error
	consumer = &serializedQueryConsumer{QueryExecutionConsumer: consumer}
	results := make([]physicalQueryResult, len(prepared.Queries))
	pending := make([]int, len(prepared.Queries))
	for index := range pending {
		pending[index] = index
	}
	var dispatchErr error
	for len(pending) != 0 {
		if err := queryCtx.Err(); err != nil {
			dispatchErr = err
			break
		}
		position := nextReadyQuery(prepared.Queries, pending, source.now())
		queryIndex := pending[position]
		pending = append(pending[:position], pending[position+1:]...)
		query := prepared.Queries[queryIndex]
		if len(query.Requirements) == 0 {
			results[queryIndex].invalid = true
			continue
		}
		if delay := time.UnixMilli(query.ReadyAtUnixMilli).Sub(source.now()); delay > 0 {
			if err := source.wait(queryCtx, delay); err != nil {
				dispatchErr = err
				break
			}
		}
		queryDeadline := time.UnixMilli(query.DeadlineUnixMilli)
		if !recoveryDeadline.IsZero() {
			queryDeadline = recoveryDeadline
		}
		permit, err := source.permits.AcquireQueryPermit(queryCtx, request.Contract.Slot, request.Operation, queryDeadline)
		if err != nil {
			if request.Operation != execution.OperationNormal && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				results[queryIndex].budgetExhausted = true
				for _, remaining := range pending {
					results[remaining].budgetExhausted = true
				}
				break
			}
			dispatchErr = fmt.Errorf("alarmd access: acquire physical query permit: %w", err)
			break
		}
		if err := queryCtx.Err(); err != nil {
			permit.Release()
			dispatchErr = err
			break
		}
		attempt := execution.QueryAttempt{Spec: query.Spec, Slot: request.Contract.Slot, Operation: request.Operation,
			AttemptNo: request.AttemptNo, DeadlineUnixMilli: queryDeadline.UnixMilli(), RecoveryPermit: permit.RecoveryPermit()}
		if err := attempt.Validate(); err != nil {
			permit.Release()
			dispatchErr = err
			break
		}
		running.Add(1)
		go func(index int, query PlannedQuery, attempt execution.QueryAttempt, permit QueryPermit) {
			defer running.Done()
			adapter := &seriesAdapter{consumer: consumer, query: query, attemptNo: attempt.AttemptNo}
			completion, err := source.executeWithPermit(queryCtx, attempt, adapter, permit)
			if err != nil {
				err = fmt.Errorf("alarmd access: execute physical query: %w", err)
			} else if !trustedProviderCompletion(query.Spec.Digest, completion) {
				err = errors.New("alarmd access: G1 provider returned an untrusted completion")
			}
			results[index].completion = completion
			if err != nil {
				queryFailure.Do(func() {
					firstQueryErr = err
					cancel()
				})
			}
		}(queryIndex, query, attempt, permit)
	}
	if dispatchErr != nil {
		cancel()
	}
	running.Wait()
	if firstQueryErr != nil {
		return execution.QueryExecutionCompletion{}, firstQueryErr
	}
	if dispatchErr != nil {
		return execution.QueryExecutionCompletion{}, dispatchErr
	}
	completion := execution.QueryExecutionCompletion{PhysicalQueries: make([]execution.PhysicalQueryCompletion, 0, len(prepared.Queries))}
	for index, query := range prepared.Queries {
		result := results[index]
		if result.invalid {
			completion = completeReadinessInvalidQuery(completion, query, request.AttemptNo)
			continue
		}
		if result.budgetExhausted {
			completion = completeBudgetExhaustedQueries(completion, []PlannedQuery{query}, request.AttemptNo)
			continue
		}
		providerCompletion := result.completion
		completion.PhysicalQueries = append(completion.PhysicalQueries, execution.PhysicalQueryCompletion{
			Ref: providerCompletion.Ref, PhysicalQuery: providerCompletion.PhysicalQuery,
			QueryRevision: query.Spec.PlanFacts.QueryRevision, Completeness: providerCompletion.Completeness,
			DataState: providerCompletion.DataState, Delivery: providerCompletion.Delivery,
			RouteFacts: providerCompletion.RouteFacts, PartialEvidence: providerCompletion.PartialEvidence,
			Stats: providerCompletion.Stats,
		})
		if providerCompletion.DataState != execution.DataStateData {
			completion.CompletionBindings = append(completion.CompletionBindings, completionBindings(query, providerCompletion, request.AttemptNo)...)
		}
		completion.CompletionBindings = append(completion.CompletionBindings,
			readinessInvalidBindings(query, providerCompletion.Ref, request.AttemptNo)...)
	}
	completion.AllRequiredCompleted = true
	return completion, nil
}

type physicalQueryResult struct {
	completion      execution.ProviderCompletion
	invalid         bool
	budgetExhausted bool
}

// prepared queries already have stable deadline ordering. Select the first
// ready query in that order, or the earliest readiness when all must wait.
func nextReadyQuery(queries []PlannedQuery, pending []int, now time.Time) int {
	earliest := 0
	for position, index := range pending {
		if len(queries[index].Requirements) == 0 || queries[index].ReadyAtUnixMilli <= now.UnixMilli() {
			return position
		}
		if queries[index].ReadyAtUnixMilli < queries[pending[earliest]].ReadyAtUnixMilli {
			earliest = position
		}
	}
	return earliest
}

type serializedQueryConsumer struct {
	execution.QueryExecutionConsumer
	mu sync.Mutex
}

func (consumer *serializedQueryConsumer) ConsumeSeries(ctx context.Context, batch execution.SeriesExecutionBatch) error {
	consumer.mu.Lock()
	defer consumer.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return consumer.QueryExecutionConsumer.ConsumeSeries(ctx, batch)
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

func completeReadinessInvalidQuery(
	completion execution.QueryExecutionCompletion,
	query PlannedQuery,
	attemptNo uint32,
) execution.QueryExecutionCompletion {
	ref := execution.ProviderResultRef(fmt.Sprintf("%s:readiness:%d", query.Spec.Digest, attemptNo))
	completion.PhysicalQueries = append(completion.PhysicalQueries, execution.PhysicalQueryCompletion{
		Ref: ref, PhysicalQuery: query.Spec.Digest, QueryRevision: query.Spec.PlanFacts.QueryRevision,
		Completeness: execution.CompletenessUnavailable, DataState: execution.DataStateUnknown,
	})
	completion.CompletionBindings = append(completion.CompletionBindings,
		readinessInvalidBindings(query, ref, attemptNo)...)
	return completion
}

func readinessInvalidBindings(
	query PlannedQuery,
	ref execution.ProviderResultRef,
	attemptNo uint32,
) []execution.NamedInputBinding {
	bindings := make([]execution.NamedInputBinding, 0)
	for _, requirement := range query.ReadinessInvalidRequirements {
		for _, consumer := range requirement.Consumers {
			bindings = append(bindings, execution.NamedInputBinding{
				Consumer: consumer.Consumer, RequirementID: requirement.RequirementID,
				DatasetName: requirement.DatasetName, Role: requirement.Role,
				ProviderResult: ref, QueryWindow: query.Spec.LogicalWindow,
				Completeness: execution.CompletenessUnavailable, DataState: execution.DataStateUnknown,
				Disposition: execution.AccessUnavailable,
				ReasonCode:  execution.ReasonCode(contract.ReasonReadinessBudgetInvalid),
				ImpactScope: execution.ImpactPlan,
				Provenance: execution.InputProvenance{
					PhysicalQuery: query.Spec.Digest, AttemptNo: attemptNo,
				},
			})
		}
	}
	return bindings
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
	schedules, err := frozenSchedules(frozen)
	if err != nil {
		return time.Time{}, err
	}
	budgetMillis := int64(0)
	for _, requirement := range frozen.Requirements {
		for _, consumer := range requirement.Consumers {
			spec, found := schedules[consumer.Consumer.Plan]
			if !found || spec.EvaluationIntervalSeconds > math.MaxInt64/1000 {
				return time.Time{}, errors.New("alarmd access: frozen consumer schedule unavailable")
			}
			intervalMillis := spec.EvaluationIntervalSeconds * 1000
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
	execution.CaptureSlotCoverage(ctx, func(c *execution.SlotCoverageCapture) {
		if c.QueryCalled != nil {
			c.QueryCalled(attempt)
		}
	})
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
	Spec                         execution.PhysicalQuerySpec
	Requirements                 []execution.DataRequirement
	ReadinessInvalidRequirements []execution.DataRequirement
	ReadyAtUnixMilli             int64
	DeadlineUnixMilli            int64
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
	schedules, err := frozenSchedules(frozen)
	if err != nil {
		return PreparedExecution{}, err
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
		if _, err := frozenRequirementReadyAt(requirement, window, minReadyDelay); err != nil {
			return PreparedExecution{}, err
		}
		planned, exists := queriesByDigest[spec.Digest]
		if !exists {
			planned = &PlannedQuery{Spec: spec}
			queriesByDigest[spec.Digest] = planned
		}
		validRequirement := requirement
		validRequirement.Consumers = nil
		invalidRequirement := requirement
		invalidRequirement.Consumers = nil
		for _, consumer := range requirement.Consumers {
			schedule, found := schedules[consumer.Consumer.Plan]
			if !found {
				return PreparedExecution{}, errors.New("alarmd access: frozen consumer schedule unavailable")
			}
			readyAt, err := frozenConsumerReadyAt(
				contractRef, requirement, window, schedule, minReadyDelay, allowExhaustedRecoveryBudget,
			)
			if err != nil {
				return PreparedExecution{}, err
			}
			candidate := consumer.ConsumerDeadlineUnixMilli - consumer.DownstreamExecutionReserveMilliSec
			if candidate <= readyAt && !allowExhaustedRecoveryBudget {
				invalidRequirement.Consumers = append(invalidRequirement.Consumers, consumer)
				continue
			}
			validRequirement.Consumers = append(validRequirement.Consumers, consumer)
			if planned.ReadyAtUnixMilli == 0 || readyAt < planned.ReadyAtUnixMilli {
				planned.ReadyAtUnixMilli = readyAt
			}
			if planned.DeadlineUnixMilli == 0 || candidate < planned.DeadlineUnixMilli {
				planned.DeadlineUnixMilli = candidate
			}
		}
		if len(validRequirement.Consumers) != 0 {
			planned.Requirements = append(planned.Requirements, validRequirement)
		}
		if len(invalidRequirement.Consumers) != 0 {
			planned.ReadinessInvalidRequirements = append(planned.ReadinessInvalidRequirements, invalidRequirement)
		}
	}
	queries := make([]PlannedQuery, 0, len(queriesByDigest))
	refs := make([]execution.PlannedPhysicalQueryRef, 0, len(queriesByDigest))
	for _, query := range queriesByDigest {
		queries = append(queries, *query)
	}
	sort.Slice(queries, func(i, j int) bool {
		if queries[i].DeadlineUnixMilli == 0 || queries[j].DeadlineUnixMilli == 0 {
			return queries[j].DeadlineUnixMilli == 0 && queries[i].DeadlineUnixMilli != 0
		}
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

func frozenConsumerReadyAt(
	contractRef execution.FrozenExecutionContractRef,
	requirement execution.DataRequirement,
	window execution.QueryWindow,
	schedule execution.ScheduleSpec,
	configuredDelay time.Duration,
	allowExhaustedRecoveryBudget bool,
) (int64, error) {
	if int64(contractRef.Slot.EvaluationTime) > math.MaxInt64/1000 {
		return 0, errors.New("alarmd access: evaluation time exceeds readiness range")
	}
	readyDelay := configuredDelay
	short := (schedule.EvaluationIntervalSeconds == 10 || schedule.EvaluationIntervalSeconds == 15) && schedule.CompletionDeadlineOffsetSeconds == 30
	if !allowExhaustedRecoveryBudget && short {
		readyDelay = thirtySecondReadyDelay
	} else if !allowExhaustedRecoveryBudget && schedule.EvaluationIntervalSeconds == 30 && readyDelay > thirtySecondReadyDelay {
		readyDelay = thirtySecondReadyDelay
	}
	return frozenRequirementReadyAt(requirement, window, readyDelay)
}

func frozenSchedules(frozen FrozenPlan) (map[execution.PlanIdentity]execution.ScheduleSpec, error) {
	schedules := make(map[execution.PlanIdentity]execution.ScheduleSpec, len(frozen.DuePlans))
	for _, due := range frozen.DuePlans {
		if due.ScheduleSpec.Validate() != nil {
			return nil, errors.New("alarmd access: frozen ScheduleSpec required")
		}
		if _, duplicate := schedules[due.Identity]; duplicate {
			return nil, errors.New("alarmd access: duplicate frozen ScheduleSpec")
		}
		schedules[due.Identity] = due.ScheduleSpec
	}
	return schedules, nil
}

func sharedPendingReadiness(queries []PlannedQuery, now time.Time) time.Time {
	var shared time.Time
	for _, query := range queries {
		if len(query.Requirements) == 0 {
			continue
		}
		readyAt := time.UnixMilli(query.ReadyAtUnixMilli)
		if !readyAt.After(now) {
			return time.Time{}
		}
		if shared.IsZero() {
			shared = readyAt
			continue
		}
		if !shared.Equal(readyAt) {
			return time.Time{}
		}
	}
	return shared
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
