package access

import (
	"context"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func (source *Source) observeQueryTiming(ctx context.Context, request execution.QueryExecutionRequest, frozen FrozenPlan, prepared PreparedExecution, recoveryStart, recoveryDeadline time.Time) {
	// Observability is a fail-open side channel, as at the Coordinator boundary.
	defer func() { _ = recover() }()
	if source.config.Observer == nil {
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
					if spec.EvaluationIntervalSeconds != 10 && spec.EvaluationIntervalSeconds != 15 {
						continue
					}
					ready, err := frozenConsumerReadyAt(request.Contract, requirement, requirement.AbsoluteWindow(request.Contract.Slot.EvaluationTime), spec, source.config.MinReadyDelay, request.Operation != execution.OperationNormal)
					if err != nil {
						continue
					}
					facts := observability.QueryTimingFacts{IntervalSeconds: spec.EvaluationIntervalSeconds, CompletionOffsetSeconds: spec.CompletionOffsetSeconds(), ReadyAtUnixMilli: ready,
						FrozenQueryDeadlineUnixMilli: consumer.ConsumerDeadlineUnixMilli - consumer.DownstreamExecutionReserveMilliSec, CompletionDeadlineUnixMilli: consumer.ConsumerDeadlineUnixMilli}
					facts.QueryDeadlineUnixMilli = facts.FrozenQueryDeadlineUnixMilli
					if request.Operation != execution.OperationNormal {
						if recoveryDeadline.IsZero() {
							continue
						}
						facts.QueryDeadlineUnixMilli = recoveryDeadline.UnixMilli()
						facts.RecoveryBudgetMilli = recoveryDeadline.Sub(recoveryStart).Milliseconds()
					}
					source.config.Observer.Observe(ctx, observability.Observation{Component: observability.ComponentAccess, Stage: observability.StageQueryBudgetResolved,
						Result: observability.ResultSuccess, Operation: observability.Operation(request.Operation), Direction: observability.DirectionInternal, QueryTiming: &facts,
						Trace: observability.TraceFields{QueryGroupKey: string(request.Contract.Slot.QueryGroup), EvaluationTime: int64(request.Contract.Slot.EvaluationTime),
							ScheduleRevision: string(request.Contract.ScheduleRevision), SnapshotRevision: string(request.Contract.SnapshotRevision), QueryRevision: string(request.Contract.QueryRevision),
							DuePlanSetDigest: string(request.Contract.DuePlanSetDigest), StrategyID: consumer.Consumer.Plan.StrategyID}})
				}
			}
		}
	}
}
