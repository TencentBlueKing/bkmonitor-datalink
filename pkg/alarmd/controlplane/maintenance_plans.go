package controlplane

import (
	"context"
	"errors"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// MaintenancePlan is the current activated plan, independent of query cadence
// and input availability. Compiled content uses the ordinary bounded compiler
// cache. Callers must hold and validate the QG's existing ownership session.
type MaintenancePlan struct {
	Identity execution.PlanIdentity
	Compiled *strategy.CompiledPlan
	// Partial detector compilation cannot prove every level inactive, but the
	// executable levels still need membership tracking for ordinary recovery.
	CloseUnavailable bool
}

// MaintenancePlans is one read of a Query Group's activated Plans: the ones
// that compiled, and how many did not. An activated Plan that does not
// compile is a Plan the maintenance cannot judge; the count travels with the
// read so the caller can count it under its own name rather than the read
// reporting it as an unclassified failure.
type MaintenancePlans struct {
	Plans        []MaintenancePlan
	Uncompilable int
}

func (runtime *RedisCatalogRuntime) CurrentPlans(ctx context.Context, qg execution.QueryGroupIdentity, at execution.EvaluationTime) (MaintenancePlans, error) {
	segment, err := runtime.readPersistedSegment(ctx, qg, at)
	if err != nil {
		return MaintenancePlans{}, err
	}
	s := segment.Schedule.Segment
	publication := SnapshotPublicationRef{SnapshotRevision: s.Publication.SnapshotRevision, PublicationEpoch: uint64(s.Publication.PublicationEpoch)}
	group, err := runtime.repository.LoadSegmentQueryGroup(ctx, s, at, func(ctx context.Context) (QueryGroup, error) {
		return runtime.repository.loadPublishedQueryGroup(ctx, publication, qg)
	})
	if err != nil {
		return MaintenancePlans{}, err
	}
	active := make(map[execution.PlanIdentity]PlanActivationRecord, len(segment.Plans))
	for _, record := range segment.Plans {
		active[record.Fact.Plan] = record
	}
	result := MaintenancePlans{Plans: make([]MaintenancePlan, 0, len(group.Plans))}
	for _, plan := range group.Plans {
		record, ok := active[plan.Identity]
		if !ok || record.Fact.Selected.ScheduleRevision != plan.ScheduleRevision {
			continue
		}
		compileResult, err := runtime.compiler.Compile(ctx, strategy.CompileRequest{Plan: plan.Plan,
			DatasetContract: group.QueryPlan.Normalization.DatasetContract, StateSemantics: runtime.stateSemantics})
		if ctx.Err() != nil {
			return MaintenancePlans{}, ctx.Err()
		}
		compiled, ok := compileResult.Plan()
		if err != nil || !ok || compileResult.PlanTerminal() != nil {
			if err == nil {
				err = errors.New("activated maintenance plan cannot be compiled")
			}
			result.Uncompilable++
			runtime.repository.observe(ctx, observability.Observation{Component: observability.ComponentRuntime,
				Stage: observability.StageEffectiveTimeMaintenance, Result: observability.ResultDegraded,
				ReasonCode: observability.EffectiveClosePlanUncompilable, Err: err,
				Trace: observability.TraceFields{QueryGroupKey: string(qg)}})
			continue
		}
		result.Plans = append(result.Plans, MaintenancePlan{Identity: plan.Identity, Compiled: compiled, CloseUnavailable: len(compileResult.LevelTerminals()) != 0})
	}
	return result, nil
}
