package access

import (
	"context"
	"errors"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func (source *Source) observeQueryTiming(ctx context.Context, request execution.QueryExecutionRequest, frozen FrozenPlan, prepared PreparedExecution, recoveryStart, recoveryDeadline time.Time) {
	// Observability is a fail-open side channel, as at the Coordinator boundary.
	defer func() { _ = recover() }()
	capture := observability.BudgetCaptureFromContext(ctx)
	if source.config.Observer == nil && capture == nil {
		return
	}
	schedules, err := frozenSchedules(frozen)
	if err != nil {
		return
	}
	for _, query := range prepared.Queries {
		for _, requirements := range [][]execution.DataRequirement{query.Requirements, query.ReadinessInvalidRequirements} {
			for _, requirement := range requirements {
				for _, consumer := range requirement.Consumers {
					spec := schedules[consumer.Consumer.Plan]
					// Every plan's budget goes to the round's capture, whatever its
					// period: the completion line of a round that ends degraded
					// renders it, so a lost round of any plan says what window its
					// read had. The query_budget_resolved line below stays the
					// short-period cohort's.
					if capture != nil {
						source.captureQueryBudget(capture, request, requirement, consumer, spec, query.Spec.PlanFacts.QueryDelaySeconds, recoveryStart, recoveryDeadline)
					}
					if source.config.Observer == nil {
						continue
					}
					// Gated on the completion deadline being thirty seconds, which is
					// what the short-period cohort means, rather than on the two
					// interval values that happen to reach it by carrying an
					// explicit offset. Written as the interval pair, this site and
					// the cohort label disagreed about which Slots are short: an
					// interval of 30 reaches a thirty-second deadline by the offset
					// defaulting to the interval, so it would have appeared in the
					// cohort lag and duration histograms while emitting no query
					// budget fact at all - present on one side of a cross-check and
					// absent on the other.
					if spec.CompletionOffsetSeconds() != observability.ShortPeriodCompletionOffsetSeconds {
						continue
					}
					ready, err := frozenConsumerReadyAt(request.Contract, requirement, requirement.AbsoluteWindow(request.Contract.Slot.EvaluationTime), spec, source.config.MinReadyDelay,
						time.Duration(consumer.DownstreamExecutionReserveMilliSec)*time.Millisecond,
						query.Spec.PlanFacts.QueryDelaySeconds)
					if err != nil {
						continue
					}
					facts := observability.QueryTimingFacts{IntervalSeconds: spec.EvaluationIntervalSeconds, CompletionOffsetSeconds: spec.CompletionOffsetSeconds(), ReadyAtUnixMilli: ready,
						FrozenQueryDeadlineUnixMilli: consumer.ConsumerDeadlineUnixMilli - consumer.DownstreamExecutionReserveMilliSec, CompletionDeadlineUnixMilli: consumer.ConsumerDeadlineUnixMilli}
					facts.QueryDeadlineUnixMilli, _ = source.config.LiveReserve.QueryDeadlineUnixMilli(consumer, request.Contract.Slot.QueryGroup, ready)
					if request.Operation != execution.OperationNormal {
						if recoveryDeadline.IsZero() {
							continue
						}
						facts.QueryDeadlineUnixMilli = recoveryDeadline.UnixMilli()
						facts.RecoveryBudgetMilli = recoveryDeadline.Sub(recoveryStart).Milliseconds()
					}
					source.config.Observer.Observe(ctx, observability.Observation{Component: observability.ComponentAccess, Stage: observability.StageQueryBudgetResolved,
						Result: observability.ResultSuccess, Operation: observability.Operation(request.Operation), QueryTiming: &facts,
						Trace: observability.TraceFields{QueryGroupKey: string(request.Contract.Slot.QueryGroup), EvaluationTime: int64(request.Contract.Slot.EvaluationTime),
							ScheduleRevision: string(request.Contract.ScheduleRevision), SnapshotRevision: string(request.Contract.SnapshotRevision), QueryRevision: string(request.Contract.QueryRevision),
							DuePlanSetDigest: string(request.Contract.DuePlanSetDigest),
							// Both halves of the strategy's identity, as every other
							// observation of the round carries them: a trace that named
							// the strategy alone made the fleet's per-object strategy
							// list show one strategy twice, once with its business and
							// once without.
							StrategyID: consumer.Consumer.Plan.StrategyID, BusinessID: consumer.Consumer.Plan.BusinessID}})
				}
			}
		}
	}
}

// captureQueryBudget records one consumer's normal-round query budget into
// the round's capture: its deadlines, its frozen reserve and the measured one
// in force, and when its data is due (frozenConsumerReadyAt), the same
// readiness and live deadline the read is prepared with.
func (source *Source) captureQueryBudget(
	capture *observability.BudgetCapture,
	request execution.QueryExecutionRequest,
	requirement execution.DataRequirement,
	consumer execution.DataRequirementConsumer,
	spec execution.ScheduleSpec,
	queryDelaySeconds int64,
	recoveryStart, recoveryDeadline time.Time,
) {
	ready, err := frozenConsumerReadyAt(request.Contract, requirement, requirement.AbsoluteWindow(request.Contract.Slot.EvaluationTime), spec, source.config.MinReadyDelay,
		time.Duration(consumer.DownstreamExecutionReserveMilliSec)*time.Millisecond, queryDelaySeconds)
	if err != nil {
		return
	}
	frozen := consumer.ConsumerDeadlineUnixMilli - consumer.DownstreamExecutionReserveMilliSec
	live, _ := source.config.LiveReserve.QueryDeadlineUnixMilli(consumer, request.Contract.Slot.QueryGroup, ready)
	inForce := int64(0)
	if live != frozen {
		inForce = consumer.ConsumerDeadlineUnixMilli - live
	}
	facts := observability.QueryBudgetFacts{ConsumerDeadlineUnixMilli: consumer.ConsumerDeadlineUnixMilli,
		FrozenReserveMillis: consumer.DownstreamExecutionReserveMilliSec, LiveReserveMillis: inForce, ReadyAtUnixMilli: ready,
		FrozenQueryDeadlineUnixMilli: frozen, LiveQueryDeadlineUnixMilli: live, QueryDeadlineUnixMilli: live}
	// A replay, retry or probe runs under the deadline access derived for it
	// from its arrival, not under the frozen Slot's.
	if request.Operation != execution.OperationNormal && !recoveryDeadline.IsZero() {
		facts.QueryDeadlineUnixMilli = recoveryDeadline.UnixMilli()
		facts.RecoveryBudgetMillis = recoveryDeadline.Sub(recoveryStart).Milliseconds()
	}
	capture.Record(facts)
}

// plannedQueryFacts is query as access planned it, for a failed attempt of it
// to carry (execution.PlannedQueryFacts): the primary input when it serves
// one, its first input otherwise.
func plannedQueryFacts(query PlannedQuery, fellBack bool) *execution.PlannedQueryFacts {
	facts := &execution.PlannedQueryFacts{DeadlineUnixMilli: query.DeadlineUnixMilli,
		FrozenDeadlineUnixMilli: query.FrozenDeadlineUnixMilli, CompletionDeadlineUnixMilli: query.CompletionDeadlineUnixMilli,
		FellBack: fellBack}
	for index, requirement := range query.Requirements {
		if index == 0 || requirement.Role == execution.InputRolePrimary {
			facts.Role, facts.Dataset = string(requirement.Role), string(requirement.DatasetName)
		}
		if requirement.Role == execution.InputRolePrimary {
			break
		}
	}
	return facts
}

// annotatePlanned puts planned on each attempt of completion that failed or
// named a reason: the attempts a failure reports.
func annotatePlanned(completion *execution.ProviderCompletion, planned *execution.PlannedQueryFacts) {
	for index := range completion.RouteFacts.Attempts {
		if attempt := &completion.RouteFacts.Attempts[index]; attempt.Result == execution.RouteAttemptFailed || attempt.ReasonCode != "" {
			attempt.Planned = planned
		}
	}
}

// plannedQueryError is a physical query's failure with its plan: it adds the
// plan and changes nothing else about the error it wraps.
type plannedQueryError struct {
	err     error
	planned *execution.PlannedQueryFacts
}

func (e *plannedQueryError) Error() string                                     { return e.err.Error() }
func (e *plannedQueryError) Unwrap() error                                     { return e.err }
func (e *plannedQueryError) QueryFailurePlanned() *execution.PlannedQueryFacts { return e.planned }

// recordFailedQuery keeps a failed physical query's request facts and plan
// with the round's budget, for the round's own line: from its failed attempt,
// or from the error it returned.
func recordFailedQuery(ctx context.Context, completion execution.ProviderCompletion, err error, planned *execution.PlannedQueryFacts) {
	capture := observability.BudgetCaptureFromContext(ctx)
	if capture == nil {
		return
	}
	if err != nil {
		var requested interface {
			QueryFailureRequest() *execution.RequestFacts
		}
		var request *execution.RequestFacts
		if errors.As(err, &requested) {
			request = requested.QueryFailureRequest()
		}
		capture.RecordFailedQuery(request.Observed(), planned.Observed())
		return
	}
	if completion.Completeness != execution.CompletenessUnavailable {
		return
	}
	for index := len(completion.RouteFacts.Attempts) - 1; index >= 0; index-- {
		if attempt := completion.RouteFacts.Attempts[index]; attempt.Result == execution.RouteAttemptFailed || attempt.ReasonCode != "" {
			capture.RecordFailedQuery(attempt.Request.Observed(), planned.Observed())
			return
		}
	}
}
