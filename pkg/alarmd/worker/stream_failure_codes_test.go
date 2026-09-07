package worker_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

type queryCompletedRecorder struct {
	mu           sync.Mutex
	observations []observability.Observation
}

func (r *queryCompletedRecorder) Observe(_ context.Context, observation observability.Observation) {
	if observation.Component != observability.ComponentAccess || observation.Stage != observability.StageQueryCompleted {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observations = append(r.observations, observability.NormalizeObservation(observation))
}

func (r *queryCompletedRecorder) last(t *testing.T) observability.Observation {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.observations) == 0 {
		t.Fatal("no query_completed observation was recorded")
	}
	return r.observations[len(r.observations)-1]
}

// Every rejected completion must surface a distinct stable failure code so a
// single rate-limited log line identifies the branch instead of collapsing
// to stream_complete OTHER.
func TestStreamCompletionRejectionsSurfaceStableFailureCodes(t *testing.T) {
	tests := []struct {
		name           string
		completionOnly bool
		mutate         func(*execution.QueryExecutionCompletion, []execution.SeriesExecutionBatch)
		category       string
		code           string
		text           string
	}{
		{
			name: "completion binding mismatch",
			mutate: func(completion *execution.QueryExecutionCompletion, batches []execution.SeriesExecutionBatch) {
				tampered := batches[0].Inputs[0]
				tampered.RequirementID = "tampered-requirement"
				completion.CompletionBindings = append(completion.CompletionBindings, tampered)
			},
			category: "completion_contract", code: "COMPLETION_BINDING_MISMATCH",
			text: "alarmd worker: completion binding differs from frozen requirement or physical completion",
		},
		{
			name:           "completion binding physical mismatch",
			completionOnly: true,
			mutate: func(completion *execution.QueryExecutionCompletion, _ []execution.SeriesExecutionBatch) {
				completion.CompletionBindings[0].DataState = execution.DataStateData
			},
			category: "completion_contract", code: "COMPLETION_BINDING_PHYSICAL_MISMATCH",
			text: "alarmd worker: completion binding differs from frozen requirement or physical completion",
		},
		{
			name:           "duplicate completion binding",
			completionOnly: true,
			mutate: func(completion *execution.QueryExecutionCompletion, _ []execution.SeriesExecutionBatch) {
				completion.CompletionBindings = append(completion.CompletionBindings, completion.CompletionBindings[0])
			},
			category: "completion_contract", code: "DUPLICATE_COMPLETION_BINDING",
			text: "alarmd worker: duplicate completion binding",
		},
		{
			name:           "completion-only requirement missing",
			completionOnly: true,
			mutate: func(completion *execution.QueryExecutionCompletion, _ []execution.SeriesExecutionBatch) {
				for index, binding := range completion.CompletionBindings {
					if binding.Consumer.LevelID == 4 {
						completion.CompletionBindings = append(completion.CompletionBindings[:index], completion.CompletionBindings[index+1:]...)
						return
					}
				}
			},
			category: "named_input", code: "COMPLETION_ONLY_REQUIREMENT_MISSING",
			text: "is missing frozen requirement",
		},
		{
			name: "streamed binding mismatch",
			mutate: func(completion *execution.QueryExecutionCompletion, _ []execution.SeriesExecutionBatch) {
				completion.PhysicalQueries[0].Ref = "another-provider-result"
			},
			category: "completion_contract", code: "STREAMED_BINDING_MISMATCH",
			text: "alarmd worker: streamed binding differs from physical completion",
		},
		{
			name: "invalid completeness",
			mutate: func(completion *execution.QueryExecutionCompletion, _ []execution.SeriesExecutionBatch) {
				completion.PhysicalQueries[0].Completeness = "BOGUS"
			},
			category: "completion_contract", code: "INVALID_COMPLETENESS",
			text: "alarmd worker: invalid physical completion completeness",
		},
		{
			name: "completion invalid",
			mutate: func(completion *execution.QueryExecutionCompletion, _ []execution.SeriesExecutionBatch) {
				completion.PhysicalQueries = nil
			},
			category: "completion_contract", code: "COMPLETION_INVALID",
			text: "alarmd execution:",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var header execution.InternalExecutionHeader
			var batches []execution.SeriesExecutionBatch
			var completion execution.QueryExecutionCompletion
			if test.completionOnly {
				header, completion = workerG4MultiLevelCompletionOnlyFixture(t)
			} else {
				header, batches, completion = workerG4MultiLevelStreamFixture(t, false)
			}
			test.mutate(&completion, batches)
			recorder := &queryCompletedRecorder{}
			ports, evaluator, coordinator := workerG4CoordinatorWithObserver(t, recorder)
			ports.executeOverride = streamExecution(header, batches, completion)

			result, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
			if err == nil || result.Completed {
				t.Fatalf("Execute() result=%+v error=%v, want completion rejection", result, err)
			}
			if ports.stateApplyCalls != 0 || ports.eventCount != 0 || len(evaluator.results) != 0 || !isZeroProgressCommit(ports.lastProgress) {
				t.Fatalf("rejected completion reached side effects: state=%d events=%d evaluate=%d progress=%+v",
					ports.stateApplyCalls, ports.eventCount, len(evaluator.results), ports.lastProgress)
			}
			observation := recorder.last(t)
			facts := observation.QueryFailure
			if observation.Result != observability.Result(observability.ResultFailed) || facts == nil {
				t.Fatalf("query_completed observation=%+v, want failed with failure facts", observation)
			}
			if facts.Stage != "stream_complete" || facts.Category != test.category || facts.Code != test.code {
				t.Fatalf("failure facts=%+v, want stage=stream_complete category=%s code=%s", *facts, test.category, test.code)
			}
			if observation.Err == nil || !strings.Contains(observation.Err.Error(), test.text) {
				t.Fatalf("error text=%v, want it to contain %q", observation.Err, test.text)
			}
			if !strings.Contains(err.Error(), test.text) {
				t.Fatalf("Execute() error=%v, want original text preserved", err)
			}
		})
	}
}

// A completion whose physical query is UNAVAILABLE is accepted (completeness
// semantics are unchanged) but the query_completed observation must carry the
// provider reason and bounded detail so the log line explains the failure.
func TestUnavailablePhysicalQueryObservationCarriesProviderDetail(t *testing.T) {
	tests := []struct {
		name     string
		detail   string
		category string
	}{
		{name: "http status", detail: execution.HTTPStatusRouteDetail(503), category: "source_backend"},
		{name: "transport", detail: execution.TransportRouteDetail(execution.TransportFailureConnectionRefused), category: "provider_transport"},
		{name: "response contract", detail: execution.ResponseRouteDetail(execution.ResponseFailureIsPartialMissing), category: "source_backend"},
		{name: "no detail", detail: "", category: "provider_transport"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header, batches, completion := workerG4MultiLevelStreamFixture(t, true)
			marked := false
			for index := range completion.PhysicalQueries {
				item := &completion.PhysicalQueries[index]
				if item.Completeness != execution.CompletenessUnavailable {
					continue
				}
				for attempt := range item.RouteFacts.Attempts {
					item.RouteFacts.Attempts[attempt].Detail = test.detail
					marked = true
				}
			}
			if !marked {
				t.Fatal("fixture has no UNAVAILABLE physical completion")
			}
			recorder := &queryCompletedRecorder{}
			ports, _, coordinator := workerG4CoordinatorWithObserver(t, recorder)
			ports.gapMissing = true
			ports.executeOverride = streamExecution(header, batches, completion)

			result, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
			if err != nil || !result.Completed || result.Result != observability.ResultDegraded {
				t.Fatalf("Execute() result=%+v error=%v", result, err)
			}
			observation := recorder.last(t)
			if observation.Result != observability.ResultDegraded || observation.Err != nil {
				t.Fatalf("query_completed observation=%+v, want degraded without error", observation)
			}
			want := observability.QueryFailureFacts{
				Stage: "provider", Category: test.category,
				Code: contract.ReasonQueryUnavailable, Detail: test.detail,
			}
			if observation.QueryFailure == nil || *observation.QueryFailure != want {
				t.Fatalf("failure facts=%+v, want %+v", observation.QueryFailure, want)
			}
		})
	}
}

func TestHealthyCompletionObservationCarriesNoFailureFacts(t *testing.T) {
	header, batches, completion := workerG4MultiLevelStreamFixture(t, false)
	recorder := &queryCompletedRecorder{}
	ports, _, coordinator := workerG4CoordinatorWithObserver(t, recorder)
	ports.executeOverride = streamExecution(header, batches, completion)

	result, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	if observation := recorder.last(t); observation.QueryFailure != nil {
		t.Fatalf("healthy completion carried failure facts: %+v", observation.QueryFailure)
	}
}
