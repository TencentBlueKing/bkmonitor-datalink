package worker

import (
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// This exercises the production aggregation boundary, not synthetic reservation
// counters. Both streams remain alive until the second merge has returned.
func TestProcessSideEffectBudgetCapsTwoLiveSlotAggregates(t *testing.T) {
	for _, kind := range []string{"state", "event", "gap"} {
		t.Run(kind, func(t *testing.T) {
			coordinator := &SlotExecutionCoordinator{budget: sideEffectTestBudget(kind)}
			first := &streamedExecution{coordinator: coordinator}
			second := &streamedExecution{coordinator: coordinator}
			defer first.releaseProvisional()
			defer second.releaseProvisional()
			firstResult := sideEffectTestResult(kind, "qg-first")
			secondResult := sideEffectTestResult(kind, "qg-second")
			if err := mergeProvisional(&first.evaluated, firstResult, coordinator.budget); err != nil {
				t.Fatalf("first Slot fitting budget rejected: %v", err)
			}
			err := mergeProvisional(&second.evaluated, secondResult, coordinator.budget)
			if err == nil {
				t.Fatalf("two live Slots each retain one %s under process limit 1: second merge accepted", kind)
			}
			if !reflect.DeepEqual(first.evaluated, firstResult) {
				t.Fatal("rejection changed another Slot's accepted aggregate")
			}
			if len(second.evaluated.Plans) != 0 {
				t.Fatal("rejected Slot retained effects before reservation succeeded")
			}
		})
	}
}

func TestSideEffectBudgetRejectionDoesNotMutateAcceptedAggregate(t *testing.T) {
	for _, kind := range []string{"state", "event", "gap"} {
		t.Run(kind, func(t *testing.T) {
			budget := sideEffectTestBudget(kind)
			var target execution.EvaluationResult
			first := sideEffectTestResult(kind, "qg")
			if err := mergeProvisional(&target, first, budget); err != nil {
				t.Fatal(err)
			}
			next := sideEffectTestResult(kind, "qg")
			// A distinct Plan prevents Gap deduplication from hiding growth.
			next.Plans[0].Plan.StrategyID = "second-plan"
			if err := mergeProvisional(&target, next, budget); err == nil {
				t.Fatal("second effect unexpectedly fit the limit")
			}
			if !reflect.DeepEqual(target, first) {
				t.Fatalf("rejected %s merge changed accepted aggregate: plans=%d", kind, len(target.Plans))
			}
		})
	}
}

func sideEffectTestBudget(kind string) ProvisionalBudget {
	budget := ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20,
		MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 100}
	switch kind {
	case "state":
		budget.MaxStateMutations = 1
	case "event":
		budget.MaxEvents = 1
	case "gap":
		budget.MaxGapMutations = 1
	}
	return budget
}

func sideEffectTestResult(kind, qg string) execution.EvaluationResult {
	plan := execution.PlanEvaluationResult{Plan: execution.PlanIdentity{StrategyID: "first-plan"}}
	switch kind {
	case "state":
		plan.StateResults = []execution.StateEvaluation{{}}
	case "event":
		plan.StateResults = []execution.StateEvaluation{{Events: []contract.TriggerEventV1{{}}}}
	case "gap":
		plan.GuardBeforeEvents = []execution.PlanGapMutation{{}}
	}
	return execution.EvaluationResult{
		Contract: execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: execution.QueryGroupIdentity(qg)}},
		Plans:    []execution.PlanEvaluationResult{plan},
	}
}
