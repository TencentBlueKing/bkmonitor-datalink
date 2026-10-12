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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

var ErrFrozenQueryPlanUnavailable = errors.New("alarmd access: frozen QueryPlanFacts unavailable")

// minimumSettlingWait is the shortest wait this product has ever treated as
// enough for data to land: it is Python's ACCESS_DATA_TIME_DELAY, which that
// side adds to every strategy's lookback whatever the period. A Slot falls
// back to it when its completion budget cannot afford the deployment's
// configured wait.
//
// It replaces a constant named thirtySecondReadyDelay whose value was ten
// seconds and which was applied by listing the interval values that needed
// it. The name said thirty, the value was ten, and the list had a hole.
const minimumSettlingWait = execution.MinimumSettlingWait

// settlingWaitWithinBudget reports how long after its query window closes a
// Slot waits before reading it, for the schedule one of its Plans carries.
//
// The rule itself is execution.SettlingWaitWithinQueryBudget, shared with the
// scheduler, which needs the same answer to decide whether a replay can still
// be read before its distance runs out. This function is the translation of a
// Plan's schedule into that rule's terms: the query budget is the completion
// offset less the downstream execution reserve.
//
// A wait that outlasts its own budget does not delay the read, it cancels it:
// the consumer fails the readiness comparison at every round and is bound as
// unavailable forever. That is what intervals of 16 to 35 seconds did, and
// the strategy serializer accepts any of them -- it validates agg_interval
// only as a non-negative integer, with no tier list behind it.
func settlingWaitWithinBudget(schedule execution.ScheduleSpec, configured, reserve time.Duration) time.Duration {
	budget := time.Duration(schedule.CompletionOffsetSeconds())*time.Second - reserve
	return execution.SettlingWaitWithinQueryBudget(budget, configured)
}

// lookbackQuery is a physical query as the lookback sees it before it is
// sent: its Slot, its frozen spec, the attempt, when it was ready, and the
// Slot the frozen schedule has next, which the Runner put on ctx.
func lookbackQuery(ctx context.Context, request execution.QueryExecutionRequest, spec execution.PhysicalQuerySpec,
	attemptNo uint32, readyAtUnixMilli int64, secondary bool) lookback.Query {
	return lookback.Query{Contract: request.Contract, Spec: spec, Operation: request.Operation, AttemptNo: attemptNo,
		ReadyAt: time.UnixMilli(readyAtUnixMilli), FollowingSlot: execution.FollowingSlotOf(ctx), Secondary: secondary}
}

// slotFirstRead is the index in queries of the Slot's first read: the query
// carrying the Slot's primary requirement with the smallest RequirementID
// (the h design, section 14). The requirement id, a digest of the relative
// window and the role, is the same at every Slot, where the queries' own
// order, by deadline and then by a digest of the absolute window, can change
// from one Slot to the next. A Slot with no primary requirement, which the
// plan's validation rules out, has its first query as its first read, so
// every Slot with a query has exactly one; -1 only for a Slot with none.
func slotFirstRead(queries []PlannedQuery) int {
	first, id := -1, execution.RequirementID("")
	for index, query := range queries {
		for _, requirement := range query.Requirements {
			if requirement.Role != execution.InputRolePrimary {
				continue
			}
			if first < 0 || requirement.RequirementID < id {
				first, id = index, requirement.RequirementID
			}
		}
	}
	if first < 0 && len(queries) > 0 {
		return 0
	}
	return first
}

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

type PhysicalQueryPermitAcquirer interface {
	AcquireQueryPermit(context.Context, execution.SlotIdentity, execution.Operation, time.Time) (QueryPermit, error)
}

type RecoveryChannels interface {
	PhysicalQueryPermitAcquirer
	Release()
}

type QueryPermitAcquirer interface {
	PhysicalQueryPermitAcquirer
	AcquireRecoveryChannels(context.Context, execution.SlotIdentity, execution.Operation, time.Time, int, func()) (RecoveryChannels, error)
}

type Config struct {
	MinReadyDelay time.Duration
	Now           func() time.Time // Defaults to time.Now.
	Observer      observability.Observer
	// Admission decides whether a series falls inside a plan's monitoring
	// target. A nil value evaluates every series for every plan it feeds,
	// which is what alarmd did before the target filter existed; it stays
	// possible only so tests can state that they mean it.
	Admission SeriesAdmission
	// ObserveAdmission counts decisions. Optional.
	ObserveAdmission AdmissionObserver
	// ObserveHostPlacement counts each admitted series a Plan reads whose
	// host CMDB placed by id or agent (admission.Facts.PlacedByHostID), with
	// whether its own address is not that host's
	// (admission.Facts.ReportedAddressDiffers), by whether the Plan's alert
	// identity groups by bk_target_ip. Optional.
	ObserveHostPlacement func(groupedByTargetIP, addressDiffers bool)
	// ScopeDrops receives the series the target filters turned away, for
	// the target-scope close; see ScopeDropSink. Optional.
	ScopeDrops ScopeDropSink
	// ObserveSeriesPulled counts what this process actually read: one call per
	// series handed into the pipeline, carrying the datapoints it held. It is
	// the load figure the per-Slot ceilings have to be read against -- a limit
	// printed with no measured load beside it says nothing about whether
	// anything is near it. Optional.
	ObserveSeriesPulled func(records uint64)
	// Lookback takes one first read of each owned Query Group at a time as
	// the provider delivered it, before any target filtering, as a summary to
	// compare later reads of the same window with; see package lookback. It
	// sees the query and hands nothing back into the pipeline. Optional.
	Lookback *lookback.Engine
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
	if request.Operation == execution.OperationSupplement {
		return source.executeSupplement(ctx, request, consumer)
	}
	// Read once, before anything this call does can take time. It is what "how
	// late did we arrive" is measured from, and a clock read taken after the
	// plan resolve would fold that resolve into the answer.
	arrivedAt := source.now()
	var recoveryStartedAt time.Time
	if request.Operation != execution.OperationNormal {
		recoveryStartedAt = arrivedAt
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
	// The deadline this recovery runs under, from here on: its channel wait
	// and its queries are bounded by it, not by the frozen Slot's first
	// attempt's, which is minutes gone for a replay of a taken-over Slot.
	execution.ExtendDeadline(ctx, recoveryDeadline, recoveryStartedAt)
	if budgetErr != nil || (!recoveryDeadline.IsZero() && !recoveryDeadline.After(source.now())) {
		if err := consumer.Begin(ctx, prepared.Header); err != nil {
			return execution.QueryExecutionCompletion{}, err
		}
		return completeBudgetExhaustedQueries(execution.QueryExecutionCompletion{}, prepared.Queries, request.AttemptNo), nil
	}
	firstRead := slotFirstRead(prepared.Queries)
	if request.Operation == execution.OperationNormal && source.config.Lookback != nil {
		for index, query := range prepared.Queries {
			source.config.Lookback.Prepare(lookbackQuery(ctx, request, query.Spec, request.AttemptNo, query.ReadyAtUnixMilli,
				index != firstRead))
		}
	}
	if readyAt := slotPendingReadiness(prepared.Queries, request.Contract.ReadHoldMillis, source.now()); !readyAt.IsZero() {
		return execution.QueryExecutionCompletion{}, &ReadinessDeferredError{readyAt: readyAt}
	}
	// Past the turn-away, so this arrival is one that goes on to do the work.
	// Deferred arrivals are already counted as deferrals; recording them here
	// too would count one round of work twice and put the early arrivals and
	// the late ones in the same distribution, where they cancel.
	source.observeSlotReadiness(ctx, request, prepared, arrivedAt)
	var permits PhysicalQueryPermitAcquirer = source.permits
	if request.Operation != execution.OperationNormal {
		maximum := len(prepared.Queries)
		// Drop per-execution preparation before waiting for recovery capacity.
		// The production acquirer also clears the outer RunOne content scope.
		cleared := false
		channels, acquireErr := source.permits.AcquireRecoveryChannels(ctx, request.Contract.Slot, request.Operation, recoveryDeadline, maximum, func() {
			frozen = FrozenPlan{}
			prepared = PreparedExecution{}
			cleared = true
		})
		if acquireErr != nil {
			if errors.Is(acquireErr, context.DeadlineExceeded) && ctx.Err() == nil {
				frozen, err = source.plans.ResolveFrozenPlan(ctx, request.Contract)
				if err != nil {
					return execution.QueryExecutionCompletion{}, err
				}
				prepared, err = prepare(request.Contract, frozen, source.config.MinReadyDelay, true)
				if err != nil {
					return execution.QueryExecutionCompletion{}, err
				}
				if err = consumer.Begin(ctx, prepared.Header); err != nil {
					return execution.QueryExecutionCompletion{}, err
				}
				return completeBudgetExhaustedQueries(execution.QueryExecutionCompletion{}, prepared.Queries, request.AttemptNo), nil
			}
			return execution.QueryExecutionCompletion{}, fmt.Errorf("alarmd access: acquire recovery channels: %w", acquireErr)
		}
		defer channels.Release()
		permits = channels
		if cleared {
			frozen, err = source.plans.ResolveFrozenPlan(ctx, request.Contract)
			if err != nil {
				return execution.QueryExecutionCompletion{}, err
			}
			prepared, err = prepare(request.Contract, frozen, source.config.MinReadyDelay, true)
			if err != nil {
				return execution.QueryExecutionCompletion{}, err
			}
		}
		// The original deadline includes both preparations and the R wait.
		if !recoveryDeadline.After(source.now()) {
			if err := consumer.Begin(ctx, prepared.Header); err != nil {
				return execution.QueryExecutionCompletion{}, err
			}
			return completeBudgetExhaustedQueries(execution.QueryExecutionCompletion{}, prepared.Queries, request.AttemptNo), nil
		}
	}
	if err := consumer.Begin(ctx, prepared.Header); err != nil {
		return execution.QueryExecutionCompletion{}, err
	}
	// The monitoring targets of this execution's plans are indexed once, not
	// per series: one query commonly delivers thousands of series and every
	// one of them would otherwise repeat the same lookup.
	scopes := buildPlanScopes(prepared.Header.DuePlans, consumer.ResolvedTargets(), consumer.ResolvedScopeGroups())
	var outputs planOutputs
	if source.config.ScopeDrops != nil {
		outputs = buildPlanOutputs(prepared.Header.DuePlans, prepared.Queries)
	}
	// Only one permit acquisition per Source is pending at a time. Queries
	// already admitted run independently; all attempts join before returning.
	queryCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var running sync.WaitGroup
	// The adapters are kept so that the target rejections of the whole
	// attempt are handed over once, after every query has ended.
	adapters := make([]*seriesAdapter, len(prepared.Queries))
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
		// Where the budget ending at that deadline began: a recovery's is
		// counted from its arrival, a normal query's from its Slot.
		budgetStart := int64(request.Contract.Slot.EvaluationTime) * 1000
		if !recoveryDeadline.IsZero() {
			queryDeadline = recoveryDeadline
			budgetStart = recoveryStartedAt.UnixMilli()
			// An execution the query cooldown pool let run decides whether
			// the Query Group leaves it, so it gets what a normal round of
			// this Slot has: from its arrival, the span from the query's
			// readiness to its deadline as prepare plans both for the normal
			// round. Not the whole interval a catch-up gets.
			if execution.NormalQueryBudget(ctx) {
				normal := recoveryStartedAt.Add(time.Duration(query.DeadlineUnixMilli-query.ReadyAtUnixMilli) * time.Millisecond)
				if normal.Before(queryDeadline) {
					queryDeadline = normal
				}
			}
		}
		permit, err := permits.AcquireQueryPermit(queryCtx, request.Contract.Slot, request.Operation, queryDeadline)
		if err != nil {
			if request.Operation != execution.OperationNormal && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				results[queryIndex].budgetExhausted = true
				for _, remaining := range pending {
					results[remaining].budgetExhausted = true
				}
				break
			}
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				err = &queryPermitDeadlineError{err: err}
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
			AttemptNo: request.AttemptNo, DeadlineUnixMilli: queryDeadline.UnixMilli(), RecoveryPermit: permit.RecoveryPermit(),
			BudgetStartUnixMilli: budgetStart, ReadyAtUnixMilli: query.ReadyAtUnixMilli}
		if err := attempt.Validate(); err != nil {
			permit.Release()
			dispatchErr = err
			break
		}
		kept := source.config.Lookback.Begin(lookbackQuery(ctx, request, query.Spec, attempt.AttemptNo, query.ReadyAtUnixMilli,
			queryIndex != firstRead))
		// Each query raises the execution's deadline to its own as it
		// starts, so a later query is judged by its deadline and not by an
		// earlier one's that has passed.
		execution.ExtendDeadline(ctx, queryDeadline, source.now())
		// What the query has from its send to its deadline, read here on the
		// dispatch loop just before it is sent.
		budgetMillis := attempt.DeadlineUnixMilli - source.now().UnixMilli()
		running.Add(1)
		go func(index int, query PlannedQuery, attempt execution.QueryAttempt, permit QueryPermit) {
			defer running.Done()
			adapter := &seriesAdapter{consumer: consumer, query: query, attemptNo: attempt.AttemptNo,
				admission: source.config.Admission, observe: source.config.ObserveAdmission,
				hostPlacement: source.config.ObserveHostPlacement, pulled: source.config.ObserveSeriesPulled, scopes: scopes,
				scopeSink: source.config.ScopeDrops, outputs: outputs, round: int64(request.Contract.Slot.EvaluationTime),
				lookback: kept}
			adapters[index] = adapter
			completion, err := source.executeWithPermit(queryCtx, attempt, adapter, permit)
			// The time it took is the provider's own measure, from its
			// request to its answer or failure; this goroutine reads no
			// clock of the Source's, which the dispatch loop owns.
			results[index].clock = &execution.PhysicalQueryClock{BudgetMillis: budgetMillis,
				ElapsedMillis: int64(completion.Stats.QueryMillis)}
			if err != nil {
				err = fmt.Errorf("alarmd access: execute physical query: %w", err)
			} else if !trustedProviderCompletion(query.Spec.Digest, completion) {
				err = errors.New("alarmd access: G1 provider returned an untrusted completion")
			}
			// The provider's own completion: a first read is kept as the
			// provider delivered it, and a recheck is compared with that.
			kept.Complete(completion, err)
			if err == nil {
				completion = adapter.reconcileCompletion(completion)
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
	flushScopeDrops(source.config.ScopeDrops, adapters, request.Contract.Slot, outputs)
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
		completion = appendQueryCompletion(completion, query, result.completion, request.AttemptNo)
		completion.PhysicalQueries[len(completion.PhysicalQueries)-1].Clock = result.clock
	}
	completion.AllRequiredCompleted = true
	return completion, nil
}

// appendQueryCompletion adds one physical query's completion, restated for
// what was forwarded, and its completion bindings.
func appendQueryCompletion(
	completion execution.QueryExecutionCompletion,
	query PlannedQuery,
	providerCompletion execution.ProviderCompletion,
	attemptNo uint32,
) execution.QueryExecutionCompletion {
	completion.PhysicalQueries = append(completion.PhysicalQueries, execution.PhysicalQueryCompletion{
		Ref: providerCompletion.Ref, PhysicalQuery: providerCompletion.PhysicalQuery,
		QueryRevision: query.Spec.PlanFacts.QueryRevision, Completeness: providerCompletion.Completeness,
		DataState: providerCompletion.DataState, Delivery: providerCompletion.Delivery,
		RouteFacts: providerCompletion.RouteFacts, PartialEvidence: providerCompletion.PartialEvidence,
		Stats: providerCompletion.Stats, Withheld: providerCompletion.Withheld, WithheldOutsideTarget: providerCompletion.WithheldOutsideTarget,
		Range: queryRange(query),
	})
	// Every valid requirement receives a completion binding, also when the
	// query delivered series. A Plan whose PRIMARY query returned no series
	// is completion-only at the worker and requires one binding per frozen
	// requirement of every Level; a dependency query that returned DATA for
	// other Plans or series would otherwise leave that Plan without a
	// binding and fail the Slot deterministically on every attempt. The
	// worker prefers a streamed binding for each (consumer, series,
	// requirement) key and falls back to this one only where none exists.
	completion.CompletionBindings = append(completion.CompletionBindings, completionBindings(query, providerCompletion, attemptNo)...)
	completion.CompletionBindings = append(completion.CompletionBindings,
		readinessInvalidBindings(query, providerCompletion.Ref, attemptNo)...)
	return completion
}

// queryRange is how long a range the query was sent asking for and how long
// a range it accepts, and whether any requirement it serves is primary.
func queryRange(query PlannedQuery) *execution.PhysicalQueryRange {
	primary := false
	for _, requirement := range query.Requirements {
		if requirement.Role == execution.InputRolePrimary {
			primary = true
		}
	}
	return &execution.PhysicalQueryRange{Primary: primary,
		AskedSeconds:    query.Spec.ProviderRange.End - query.Spec.ProviderRange.Start,
		AcceptedSeconds: query.Spec.AcceptedRange.End - query.Spec.AcceptedRange.Start}
}

type physicalQueryResult struct {
	completion      execution.ProviderCompletion
	invalid         bool
	budgetExhausted bool
	// clock is what the query had from its send and what it used.
	clock *execution.PhysicalQueryClock
}

// queryPermitDeadlineError names a normal-operation permit wait that ended at
// the frozen query deadline (consumer deadline minus the downstream reserve)
// while the caller's context was still live. It keeps the historical error
// text and exposes the bounded admission facts so that the query_completed
// line and the target-flow facts name the cause instead of other/OTHER.
type queryPermitDeadlineError struct{ err error }

func (e *queryPermitDeadlineError) Error() string { return e.err.Error() }
func (e *queryPermitDeadlineError) Unwrap() error { return e.err }
func (e *queryPermitDeadlineError) QueryFailure() (string, string) {
	return observability.QueryFailureCategoryAdmission, "QUERY_PERMIT_DEADLINE"
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
	observability.EmitTargetFlow(ctx, "runner_decision", observability.TraceFields{}, observability.TargetFlowFacts{Decision: "query_running"})
	completion, err := source.provider.Execute(ctx, attempt, adapter)
	if err == nil && completion.Stats.NullIdentityFields > 0 {
		// Bounded diagnostics marker: at least one declared identity dimension
		// was absent from a delivered series and was bound to null (the Python
		// None equivalent). The count stays on completion.Stats; no evaluation
		// or completeness semantics change.
		observability.EmitTargetFlow(ctx, "runner_decision", observability.TraceFields{}, observability.TargetFlowFacts{Decision: "identity_field_null"})
	}
	return completion, err
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

// prepare plans one Slot's physical queries.
//
// allowExhaustedRecoveryBudget means one thing: a recovery operation runs
// against the recovery deadline rather than the Slot's own, so a consumer
// whose frozen deadline has already passed is still planned instead of being
// set aside as readiness-invalid. It used to mean a second thing as well --
// that the readiness boundary itself was computed differently -- and that
// second meaning made a ten-second replay wait past the point where it could
// still run. Readiness is now the same instant for every operation.
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
		// The provider is asked from earlier for an event count (its
		// requirement's lead); only the window is accepted.
		spec, err := execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: facts,
			LogicalWindow: window, ProviderRange: requirement.ProviderWindow(contractRef.Slot.EvaluationTime), AcceptedRange: window,
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
				contractRef, requirement, window, schedule, minReadyDelay,
				time.Duration(consumer.DownstreamExecutionReserveMilliSec)*time.Millisecond,
				facts.QueryDelaySeconds,
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

// frozenConsumerReadyAt is when this consumer's window may be read. It is the
// same instant whichever operation is asking.
//
// A recovery operation used to be given the configured wait unclamped, on the
// reasoning that a replay is already late so it may as well be careful. On a
// ten-second Slot that reasoning killed the replay: the clamped wait was ten
// seconds and the configured one thirty, and by the thirtieth second the Slot
// was four grid points behind -- past the replay distance, skipped. The wait
// meant to protect the read is what consumed the only window the read had.
//
// There is also nothing the later read gains from it. The settling wait exists
// so the data has landed; the normal path judges ten seconds enough for this
// Slot, and a replay reads the same window later still.
func frozenConsumerReadyAt(
	contractRef execution.FrozenExecutionContractRef,
	requirement execution.DataRequirement,
	window execution.QueryWindow,
	schedule execution.ScheduleSpec,
	configuredDelay time.Duration,
	reserve time.Duration,
	sourceDelaySeconds int64,
) (int64, error) {
	if int64(contractRef.Slot.EvaluationTime) > math.MaxInt64/1000 {
		return 0, errors.New("alarmd access: evaluation time exceeds readiness range")
	}
	// Source delay selects an older data window; it must not move the
	// scheduler's global readiness boundary earlier by the same amount.
	window.End += sourceDelaySeconds
	ready, err := frozenRequirementReadyAt(requirement, window, settlingWaitWithinBudget(schedule, configuredDelay, reserve))
	if err != nil {
		return 0, err
	}
	// The contract's read hold reads the Slot that much later, after the
	// settling wait: the wait is still chosen within the schedule's own
	// budget, which the hold does not widen, as the scheduler chooses it.
	if contractRef.ReadHoldMillis < 0 || ready > math.MaxInt64-contractRef.ReadHoldMillis {
		return 0, errors.New("alarmd access: read hold exceeds readiness range")
	}
	return ready + contractRef.ReadHoldMillis, nil
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

// slotPendingReadiness is when a Slot that is not ready comes back, instead
// of waiting inside its execution. A Slot carrying a read hold comes back
// when the last of its queries is ready, whenever any is still pending: it
// waits in the due index, as its held readiness is its due time there, and
// does not sleep through the hold holding a dispatch slot and its Query
// Group's flight. A query already readable is read then; its window is the
// same and no less complete. A Slot without a hold keeps the shared rule:
// deferred only when every query waits for the same moment, otherwise the
// settling wait for the earliest is spent inside the execution.
func slotPendingReadiness(queries []PlannedQuery, readHoldMillis int64, now time.Time) time.Time {
	if readHoldMillis <= 0 {
		return sharedPendingReadiness(queries, now)
	}
	var latest time.Time
	for _, query := range queries {
		if len(query.Requirements) == 0 {
			continue
		}
		if readyAt := time.UnixMilli(query.ReadyAtUnixMilli); readyAt.After(now) && readyAt.After(latest) {
			latest = readyAt
		}
	}
	return latest
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
	admission SeriesAdmission
	observe   AdmissionObserver
	// hostPlacement counts an admitted series whose host CMDB placed by id
	// or agent, with whether its address differs, per Plan. Optional.
	hostPlacement func(groupedByTargetIP, addressDiffers bool)
	// pulled counts the series this query actually handed on. Optional.
	pulled func(records uint64)
	scopes planScopes
	// scopeSink, outputs and round serve the target-scope close; see
	// ScopeDropSink. scopeScreens and scopeTallies are this query's own
	// memory of it: one Screen per Plan, and the bulk counts handed over
	// when the query ends. Empty when nothing listens.
	scopeSink    ScopeDropSink
	outputs      planOutputs
	round        int64
	scopeScreens map[execution.PlanIdentity]string
	scopeTallies map[scopeTallyKey]int
	// lookback is this query's first read as the lookback sees it: its bytes
	// counted, and a summary kept when it was taken as its Query Group's
	// sample; nil for a query that is not a formal first read.
	lookback *lookback.Read

	// forwarded accumulates the delivery proofs of the batches that actually
	// reached the consumer, and withheld counts the ones the monitoring target
	// excluded. The provider counts every series it decoded, so the moment one
	// is withheld its completion stops describing what was delivered.
	//
	// One adapter serves exactly one physical query and the sink is called in
	// order - the delivery digest is a chain, so out-of-order delivery is
	// undefined for the worker too - which is why this needs no lock.
	forwarded execution.SeriesDelivery
	withheld  uint64
	// withheldOutside counts the withheld series every Plan refused as
	// outside its monitoring target on facts that were all there: data that
	// exists and that the target did not select.
	withheldOutside uint64
}

func (adapter *seriesAdapter) ConsumeProviderSeries(ctx context.Context, batch execution.ProviderSeriesBatch) error {
	if batch.PhysicalQuery != adapter.query.Spec.Digest || batch.CompletionRef == "" || batch.Dataset == nil || batch.Dataset.Len() == 0 {
		return errors.New("alarmd access: provider delivered an invalid series")
	}
	admitted, outside := adapter.admittedPlans(batch)
	// Kept before the target filter returns: a recheck reads the whole
	// dimension set, and a series every Plan turned away is still data.
	adapter.lookback.Series(batch.Dataset, batch.Delivery.Bytes)
	bindings, err := dataBindings(adapter.query, batch, adapter.attemptNo, admitted)
	if err != nil {
		return err
	}
	if len(bindings) == 0 {
		// Every plan this series could feed excludes it. Passing an empty
		// batch on would be rejected as incomplete, and there is nothing to
		// evaluate: the series simply does not belong to any of them.
		adapter.withheld++
		if outside {
			adapter.withheldOutside++
		}
		return nil
	}
	if err := adapter.consumer.ConsumeSeries(ctx, execution.SeriesExecutionBatch{PhysicalQuery: batch.PhysicalQuery,
		QueryRevision: adapter.query.Spec.PlanFacts.QueryRevision, CompletionRef: batch.CompletionRef,
		Dataset: batch.Dataset, Inputs: bindings, Delivery: batch.Delivery}); err != nil {
		return err
	}
	// Counted here rather than at the provider: this is the point a series
	// becomes work for this process. One call is one series -- the batch
	// contract fixes Delivery.Series at 1 -- so the count needs no arithmetic
	// and cannot drift from what the pipeline was actually handed.
	if adapter.pulled != nil {
		adapter.pulled(batch.Delivery.Records)
	}
	// Fold the same proof the worker folds, in the same order, so the two
	// accumulations stay identical down to the chained digest.
	forwarded, err := execution.AccumulateSeriesDelivery(adapter.forwarded, batch.Delivery)
	if err != nil {
		return err
	}
	adapter.forwarded = forwarded
	return nil
}

// reconcileCompletion restates a provider completion in terms of what the
// access layer forwarded.
//
// The worker validates a completion against the series it actually received
// (execution.QueryExecutionCompletion.Validate), so a completion that still
// counts a withheld series fails the whole Slot at stream_complete - not just
// the plan that excluded the series, but every plan sharing the query group,
// including the ones whose series were admitted. Filtering therefore has to
// subtract from the completion in the same layer that does the filtering.
func (adapter *seriesAdapter) reconcileCompletion(completion execution.ProviderCompletion) execution.ProviderCompletion {
	completion.Withheld, completion.WithheldOutsideTarget = adapter.withheld, adapter.withheldOutside
	if adapter.withheld == 0 || completion.DataState != execution.DataStateData {
		return completion
	}
	if adapter.forwarded.PhysicalQuery == "" {
		// Nothing survived the target: the query is empty for every plan it
		// feeds, exactly as it would be had the series never been returned.
		// Completeness stays the provider's own fact.
		completion.DataState = execution.DataStateEmpty
		completion.Delivery = execution.SeriesDelivery{}
		return completion
	}
	completion.Delivery = adapter.forwarded
	return completion
}

// admittedPlans enriches the series once per tenant among the plans it could
// feed - the CMDB facts are the plan's tenant's - and then decides for each
// plan, and says whether every plan refused it as outside its monitoring
// target, decided on facts that were all there. A nil result means no
// filtering is installed and every plan is admitted.
func (adapter *seriesAdapter) admittedPlans(batch execution.ProviderSeriesBatch) (map[execution.PlanIdentity]bool, bool) {
	if adapter.admission == nil {
		return nil, false
	}
	outside := true
	dimensions := seriesDimensions(batch.Dataset)
	// The plans of one query are one tenant's in practice: the facts are
	// kept for the last tenant enriched for, and only a plan of another
	// tenant enriches again.
	var facts admission.Facts
	factsTenant, enriched := "", false
	decisions := make(map[execution.PlanIdentity]bool)
	for _, requirement := range adapter.query.Requirements {
		for _, consumer := range requirement.Consumers {
			identity := consumer.Consumer.Plan
			if _, decided := decisions[identity]; decided {
				continue
			}
			plan := adapter.scopes[identity]
			if !enriched || plan.TenantID != factsTenant {
				facts, factsTenant, enriched = adapter.admission.EnrichFor(plan.TenantID, dimensions), plan.TenantID, true
			}
			admit, filter, reason := adapter.admission.Admit(plan, &facts)
			decisions[identity] = admit
			if !admit {
				adapter.reportScopeDrop(identity, plan, &facts, filter, reason)
			}
			if admit && facts.PlacedByHostID && adapter.hostPlacement != nil {
				adapter.hostPlacement(adapter.query.groupsByTargetIP(), facts.ReportedAddressDiffers)
			}
			outside = outside && !admit && admission.DefinitelyOutside(plan, &facts, filter, reason)
			if adapter.observe != nil {
				switch {
				case !admit:
					adapter.observe(filter, "rejected", reason)
				case reason != "":
					// Admitted, but the filter said why it could not decide.
					// That is the case worth separating from an ordinary
					// admission: it is what a degraded CMDB cache looks like.
					adapter.observe(filter, "admitted", reason)
				case plan.TargetPlan != nil:
					// Admitted by the target plan filter: counted under its
					// own name so the two forms' admission ratios can be read
					// apart.
					adapter.observe("target_plan", "admitted", "in_target")
				default:
					adapter.observe("target_scope", "admitted", "in_scope")
				}
			}
		}
	}
	return decisions, outside && len(decisions) > 0
}

func dataBindings(
	query PlannedQuery,
	batch execution.ProviderSeriesBatch,
	attemptNo uint32,
	admitted map[execution.PlanIdentity]bool,
) ([]execution.NamedInputBinding, error) {
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
			if admitted != nil && !admitted[consumer.Consumer.Plan] {
				// The series is outside this strategy's monitoring target, so
				// it never becomes one of its inputs - and therefore never
				// reaches its State, evaluation or events.
				continue
			}
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

// completionBindings projects one physical completion onto every valid
// consumer requirement of the query. The binding never carries records: series
// reach consumers only through streamed batches. For a FULL or PARTIAL query
// that delivered series the binding is therefore the EMPTY share of that
// completion (the consumer or series received nothing from it); for an
// UNAVAILABLE query it is UNKNOWN regardless of any series streamed before the
// provider failed. execution.ValidateNamedInputCompletion accepts exactly
// these two projections.
func completionBindings(query PlannedQuery, completion execution.ProviderCompletion, attemptNo uint32) []execution.NamedInputBinding {
	bindings := make([]execution.NamedInputBinding, 0)
	var dataset *execution.Dataset
	var view *execution.DatasetView
	dataState := completion.DataState
	if dataState == execution.DataStateData {
		dataState = execution.DataStateEmpty
	}
	disposition := execution.AccessAvailable
	var reason execution.ReasonCode
	var attribution execution.UnavailableAttribution
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
		reason, attribution = execution.AttributeUnavailable(completion.RouteFacts, execution.ReasonCode(contract.ReasonQueryUnavailable))
	}
	for _, requirement := range query.Requirements {
		for _, consumer := range requirement.Consumers {
			bindings = append(bindings, execution.NamedInputBinding{Consumer: consumer.Consumer,
				RequirementID: requirement.RequirementID, DatasetName: requirement.DatasetName, Role: requirement.Role,
				ProviderResult: completion.Ref, QueryWindow: query.Spec.LogicalWindow, Dataset: dataset, View: view,
				Completeness: completion.Completeness, DataState: dataState,
				Disposition: disposition, ReasonCode: reason, ImpactScope: execution.ImpactPlan,
				PartialEvidence: completion.PartialEvidence, UnavailableAttribution: attribution,
				Provenance: execution.InputProvenance{PhysicalQuery: query.Spec.Digest, AttemptNo: attemptNo}})
		}
	}
	return bindings
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
