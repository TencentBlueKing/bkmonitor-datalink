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

// An unavailable result also says why, in the primary binding's own words:
// the query cooldown it feeds reports its entries under that reason.
func TestUnavailableQueryAvailabilityRequiresBackendFailure(t *testing.T) {
	unavailable, targetMissing := execution.ReasonCode(contract.ReasonQueryUnavailable), execution.ReasonCode(contract.ReasonQueryTargetMissing)
	for _, test := range []struct {
		name, detail string
		reason       execution.ReasonCode
		want         execution.QueryAvailability
	}{
		{"backend status", execution.HTTPStatusRouteDetail(503), unavailable, execution.QueryAvailabilityUnavailable},
		{"backend response", "response=status_table_not_found", unavailable, execution.QueryAvailabilityUnavailable},
		{"target missing", "response=status_space_table_id_field_is_not_exists", targetMissing, execution.QueryAvailabilityUnavailable},
		{"admission or unknown", "", unavailable, execution.QueryAvailabilityUnknown},
		{"transport", execution.TransportRouteDetail(execution.TransportFailureTimeout), unavailable, execution.QueryAvailabilityUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			plans, requirements := baseDuePlanAndRequirements()
			fixture, request := newCompletionOnlyFixture(t, plans, requirements, func(completion *execution.QueryExecutionCompletion) {
				physical := &completion.PhysicalQueries[0]
				physical.Completeness, physical.DataState = execution.CompletenessUnavailable, execution.DataStateUnknown
				physical.RouteFacts.Attempts = []execution.RouteAttemptFact{{AttemptNo: 1, Result: execution.RouteAttemptFailed, ReasonCode: test.reason, Detail: test.detail}}
				for i := range completion.CompletionBindings {
					binding := &completion.CompletionBindings[i]
					binding.Dataset, binding.View = nil, nil
					binding.Completeness, binding.DataState = execution.CompletenessUnavailable, execution.DataStateUnknown
					binding.Disposition, binding.ReasonCode = execution.AccessUnavailable, test.reason
				}
			})
			result, err := fixture.coordinator.Execute(context.Background(), request)
			if err != nil || !result.Completed {
				t.Fatalf("Execute() result=%+v error=%v", result, err)
			}
			assertQueryAvailability(t, result, test.want)
			if want := map[bool]execution.ReasonCode{true: test.reason}[test.want == execution.QueryAvailabilityUnavailable]; result.QueryUnavailableReason != want {
				t.Fatalf("QueryUnavailableReason=%q, want %q", result.QueryUnavailableReason, want)
			}
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
