package worker_test

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func assertQueryAvailability(t *testing.T, result execution.SlotExecutionResult, want execution.QueryAvailability) {
	t.Helper()
	if result.QueryAvailability != want {
		t.Fatalf("QueryAvailability=%v, want %v", result.QueryAvailability, want)
	}
}

func TestUnavailableQueryAvailabilityRequiresBackendFailure(t *testing.T) {
	for _, test := range []struct {
		name, detail string
		want         execution.QueryAvailability
	}{
		{"backend status", execution.HTTPStatusRouteDetail(503), execution.QueryAvailabilityUnavailable},
		{"backend response", "response=status_table_not_found", execution.QueryAvailabilityUnavailable},
		{"admission or unknown", "", execution.QueryAvailabilityUnknown},
		{"transport", execution.TransportRouteDetail(execution.TransportFailureTimeout), execution.QueryAvailabilityUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			plans, requirements := baseDuePlanAndRequirements()
			fixture, request := newCompletionOnlyFixture(t, plans, requirements, func(completion *execution.QueryExecutionCompletion) {
				physical := &completion.PhysicalQueries[0]
				physical.Completeness, physical.DataState = execution.CompletenessUnavailable, execution.DataStateUnknown
				physical.RouteFacts.Attempts = []execution.RouteAttemptFact{{AttemptNo: 1, Result: execution.RouteAttemptFailed, ReasonCode: execution.ReasonCode(contract.ReasonQueryUnavailable), Detail: test.detail}}
				for i := range completion.CompletionBindings {
					binding := &completion.CompletionBindings[i]
					binding.Dataset, binding.View = nil, nil
					binding.Completeness, binding.DataState = execution.CompletenessUnavailable, execution.DataStateUnknown
					binding.Disposition, binding.ReasonCode = execution.AccessUnavailable, execution.ReasonCode(contract.ReasonQueryUnavailable)
				}
			})
			result, err := fixture.coordinator.Execute(context.Background(), request)
			if err != nil || !result.Completed {
				t.Fatalf("Execute() result=%+v error=%v", result, err)
			}
			assertQueryAvailability(t, result, test.want)
		})
	}
}

func TestQueryAvailabilityRequiresProgressCommit(t *testing.T) {
	plans, requirements := baseDuePlanAndRequirements()
	fixture, request := newCompletionOnlyFixture(t, plans, requirements, nil)
	fixture.ports.progressConflict = true
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err == nil || result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	assertQueryAvailability(t, result, execution.QueryAvailabilityUnknown)
}
