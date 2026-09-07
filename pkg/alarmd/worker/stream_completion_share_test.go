package worker_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// These tests drive the worker as a black box with the completion shape the
// Access source emits since completion bindings cover every valid requirement:
// the EMPTY share of a FULL or PARTIAL completion and the UNKNOWN projection of
// an UNAVAILABLE completion, both regardless of the physical DataState.

// The OsRestart production defect: PRIMARY FULL EMPTY (no host restarted) while
// the history query delivered DATA. The Plan has no series, so the worker needs
// a completion binding for the history requirement too; the Slot must complete
// as FULL_EMPTY_COMPLETED without evaluation or side effects.
func TestSlotExecutionCoordinatorCompletesFullEmptyWhenPrimaryIsEmptyAndDependencyDeliveredData(t *testing.T) {
	header, batches := workerG4StreamFixture(t, strategy.DetectorKindOsRestart)
	primary, history := shareFixtureQueries(t, header)
	historyBatch := batches[history.index]
	completion := execution.QueryExecutionCompletion{AllRequiredCompleted: true, PhysicalQueries: []execution.PhysicalQueryCompletion{
		{Ref: "empty-primary", PhysicalQuery: primary.query.Digest, QueryRevision: primary.query.QueryRevision,
			Completeness: execution.CompletenessFull, DataState: execution.DataStateEmpty},
		{Ref: historyBatch.CompletionRef, PhysicalQuery: history.query.Digest, QueryRevision: history.query.QueryRevision,
			Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Delivery: historyBatch.Delivery},
	}}
	completion.CompletionBindings = accessShapedCompletionBindings(t, header, completion.PhysicalQueries)

	observations := make([]observability.Observation, 0)
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		observations = append(observations, observability.NormalizeObservation(observation))
	})
	ports, evaluator, coordinator := workerG4CoordinatorWithObserver(t, observer)
	ports.executeOverride = streamExecution(header, []execution.SeriesExecutionBatch{historyBatch}, completion)

	result, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
	if err != nil || !result.Completed || result.Result != observability.ResultSuccess {
		t.Fatalf("Execute() result=%+v error=%v, want FULL EMPTY completion without error", result, err)
	}
	if len(evaluator.results) != 0 || ports.eventCount != 0 || ports.stateApplyCalls != 0 || ports.stateLoadCalls != 0 || len(ports.gapMutations) != 0 {
		t.Fatalf("no-series Plan produced business effects: evaluations=%d events=%d state_apply=%d state_load=%d gaps=%d",
			len(evaluator.results), ports.eventCount, ports.stateApplyCalls, ports.stateLoadCalls, len(ports.gapMutations))
	}
	progress := ports.lastProgress
	if progress.Completion.Kind != execution.CompletionFullEmpty || progress.Completion.Primary == nil ||
		progress.Completion.Primary.Completeness != execution.CompletenessFull || progress.Completion.Primary.DataState != execution.DataStateEmpty {
		t.Fatalf("Progress=%+v, want FULL_EMPTY_COMPLETED", progress)
	}
	for _, observation := range observations {
		if observation.Stage == observability.StageQueryCompleted && (observation.Result == observability.ResultFailed || observation.QueryFailure != nil) {
			t.Fatalf("query_completed reported a failure: %+v", observation)
		}
	}
}

// The PRIMARY query failed while the history query delivered DATA. The Plan
// has no PRIMARY series either, but the no-series judgement reads the PRIMARY
// completion of the exact set: an UNAVAILABLE PRIMARY opens the Plan gap and
// the Slot completes as COMPLETED_WITH_UNAVAILABLE, never as
// FULL_EMPTY_COMPLETED, and the streamed history series stay unevaluated.
func TestSlotExecutionCoordinatorDoesNotCompleteFullEmptyWhenPrimaryIsUnavailableAndDependencyDeliveredData(t *testing.T) {
	header, batches := workerG4StreamFixture(t, strategy.DetectorKindOsRestart)
	primary, history := shareFixtureQueries(t, header)
	historyBatch := batches[history.index]
	reason := execution.ReasonCode(contract.ReasonQueryUnavailable)
	completion := execution.QueryExecutionCompletion{AllRequiredCompleted: true, PhysicalQueries: []execution.PhysicalQueryCompletion{
		{Ref: "unavailable-primary", PhysicalQuery: primary.query.Digest, QueryRevision: primary.query.QueryRevision,
			Completeness: execution.CompletenessUnavailable, DataState: execution.DataStateUnknown,
			RouteFacts: execution.ProviderRouteFacts{Attempts: []execution.RouteAttemptFact{{
				AttemptNo: 1, Endpoint: "uq", Result: execution.RouteAttemptFailed, ReasonCode: reason,
			}}}},
		{Ref: historyBatch.CompletionRef, PhysicalQuery: history.query.Digest, QueryRevision: history.query.QueryRevision,
			Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Delivery: historyBatch.Delivery},
	}}
	completion.CompletionBindings = accessShapedCompletionBindings(t, header, completion.PhysicalQueries)

	ports, evaluator, coordinator := workerG4Coordinator(t)
	ports.gapMissing = true
	ports.executeOverride = streamExecution(header, []execution.SeriesExecutionBatch{historyBatch}, completion)

	result, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
	if err != nil || !result.Completed || result.Result != observability.ResultDegraded || result.ReasonCode != reason {
		t.Fatalf("Execute() result=%+v error=%v, want degraded QUERY_UNAVAILABLE completion", result, err)
	}
	if len(evaluator.requests) != 0 || ports.eventCount != 0 || ports.stateApplyCalls != 0 || ports.stateLoadCalls != 0 {
		t.Fatalf("UNAVAILABLE PRIMARY produced business effects: evaluations=%d events=%d state_apply=%d state_load=%d",
			len(evaluator.requests), ports.eventCount, ports.stateApplyCalls, ports.stateLoadCalls)
	}
	if len(ports.gapMutations) != 1 || len(ports.gapMutations[0].Scopes) != 1 ||
		ports.gapMutations[0].Scopes[0].ReasonCode != reason || ports.gapMutations[0].Scopes[0].Scope.LevelID != 5 {
		t.Fatalf("gap mutations=%+v, want one Level 5 QUERY_UNAVAILABLE gap", ports.gapMutations)
	}
	progress := ports.lastProgress
	if progress.Completion.Kind == execution.CompletionFullEmpty {
		t.Fatalf("Progress=%+v, UNAVAILABLE PRIMARY with DATA history completed as FULL_EMPTY_COMPLETED", progress)
	}
	if progress.Completion.Kind != execution.CompletionUnavailable || progress.Completion.Primary == nil ||
		progress.Completion.Primary.Completeness != execution.CompletenessUnavailable ||
		progress.Completion.Primary.DataState != execution.DataStateUnknown || progress.Completion.ReasonCode != reason {
		t.Fatalf("Progress=%+v, want COMPLETED_WITH_UNAVAILABLE", progress)
	}
}

// A dependency query that delivered DATA for other series lacks the streamed
// PRIMARY series: the worker falls back to the EMPTY share of that completion
// and reaches evaluation with it instead of failing the named-input exact set.
// The recording fixture has no gap store, so the UNKNOWN Level outcome that a
// missing ring-ratio dependency produces cannot obtain its durable guard here;
// that is the same result the fixture gives a FULL EMPTY dependency completion
// and is outside this test, which stops at the evaluation input.
func TestSlotExecutionCoordinatorBindsEmptyShareWhenDependencyDataLacksTheSeries(t *testing.T) {
	header, batches := workerG4StreamFixture(t, strategy.DetectorKindSimpleRingRatio)
	primary, previous := shareFixtureQueries(t, header)
	primaryBatch := batches[primary.index]
	otherSeriesBatch := rebindBatchToSeries(t, batches[previous.index], strings.Repeat("d", 64))
	completion := execution.QueryExecutionCompletion{AllRequiredCompleted: true, PhysicalQueries: []execution.PhysicalQueryCompletion{
		{Ref: primaryBatch.CompletionRef, PhysicalQuery: primary.query.Digest, QueryRevision: primary.query.QueryRevision,
			Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Delivery: primaryBatch.Delivery},
		{Ref: otherSeriesBatch.CompletionRef, PhysicalQuery: previous.query.Digest, QueryRevision: previous.query.QueryRevision,
			Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Delivery: otherSeriesBatch.Delivery},
	}}
	completion.CompletionBindings = accessShapedCompletionBindings(t, header, completion.PhysicalQueries)

	ports, evaluator, coordinator := workerG4Coordinator(t)
	ports.gapMissing = true
	ports.executeOverride = streamExecution(header, []execution.SeriesExecutionBatch{primaryBatch, otherSeriesBatch}, completion)

	_, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
	if err != nil && strings.Contains(err.Error(), "named-input exact set") {
		t.Fatalf("Execute() rejected the EMPTY dependency share at the named-input exact set: %v", err)
	}
	if len(evaluator.requests) != 1 {
		t.Fatalf("evaluation requests=%d (error=%v), want the streamed PRIMARY series evaluated once", len(evaluator.requests), err)
	}
	found := false
	for _, series := range evaluator.requests[0].Inputs {
		for _, input := range series.Inputs {
			if input.Role != execution.InputRoleAlgorithmDependency {
				continue
			}
			found = true
			if input.Completeness != execution.CompletenessFull || input.DataState != execution.DataStateEmpty || input.View == nil || input.View.Len() != 0 {
				t.Fatalf("dependency input=%+v, want the FULL EMPTY share of the DATA dependency query", input)
			}
		}
	}
	if !found {
		t.Fatalf("evaluation request carried no dependency input: %+v", evaluator.requests[0])
	}
}

// UQ writes series before status, so a deterministic backend status may arrive
// after series were streamed. The UNAVAILABLE completion conserves the
// delivered series; the worker completes the Slot and the query_completed line
// carries the bounded backend status detail.
func TestSlotExecutionCoordinatorCompletesUnavailableAfterPartialStreamWithBackendStatusDetail(t *testing.T) {
	header, batches := workerG4StreamFixture(t, strategy.DetectorKindSimpleRingRatio)
	primary, previous := shareFixtureQueries(t, header)
	primaryBatch, previousBatch := batches[primary.index], batches[previous.index]
	detail := execution.ResponseStatusRouteDetail("SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS")
	completion := execution.QueryExecutionCompletion{AllRequiredCompleted: true, PhysicalQueries: []execution.PhysicalQueryCompletion{
		{Ref: primaryBatch.CompletionRef, PhysicalQuery: primary.query.Digest, QueryRevision: primary.query.QueryRevision,
			Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Delivery: primaryBatch.Delivery},
		{Ref: previousBatch.CompletionRef, PhysicalQuery: previous.query.Digest, QueryRevision: previous.query.QueryRevision,
			Completeness: execution.CompletenessUnavailable, DataState: execution.DataStateData, Delivery: previousBatch.Delivery,
			RouteFacts: execution.ProviderRouteFacts{Attempts: []execution.RouteAttemptFact{{
				AttemptNo: 1, Endpoint: "uq", Result: execution.RouteAttemptFailed,
				ReasonCode: execution.ReasonCode(contract.ReasonQueryUnavailable), Detail: detail,
			}}}},
	}}
	completion.CompletionBindings = accessShapedCompletionBindings(t, header, completion.PhysicalQueries)

	observations := make([]observability.Observation, 0)
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		observations = append(observations, observability.NormalizeObservation(observation))
	})
	ports, _, coordinator := workerG4CoordinatorWithObserver(t, observer)
	ports.gapMissing = true
	ports.executeOverride = streamExecution(header, []execution.SeriesExecutionBatch{primaryBatch, previousBatch}, completion)

	result, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v error=%v, want UNAVAILABLE dependency completed without a contract error", result, err)
	}
	if ports.eventCount != 0 {
		t.Fatalf("UNAVAILABLE dependency produced %d events", ports.eventCount)
	}
	var facts *observability.QueryFailureFacts
	for _, observation := range observations {
		if observation.Stage == observability.StageQueryCompleted {
			facts = observation.QueryFailure
		}
	}
	if facts == nil || facts.Stage != observability.QueryFailureStageProvider || facts.Category != observability.QueryFailureCategorySourceBackend ||
		facts.Code != contract.ReasonQueryUnavailable || facts.Detail != "response=status_space_table_id_field_is_not_exists" {
		t.Fatalf("query_completed failure facts=%+v, want source_backend QUERY_UNAVAILABLE with the UQ status detail", facts)
	}
}

type shareFixtureQuery struct {
	index int
	query execution.PlannedPhysicalQueryRef
}

// shareFixtureQueries returns the PRIMARY and the dependency query of a
// workerG4StreamFixture header; batches and RequiredPhysicalQueries follow the
// requirement order there.
func shareFixtureQueries(t *testing.T, header execution.InternalExecutionHeader) (shareFixtureQuery, shareFixtureQuery) {
	t.Helper()
	var primary, dependency shareFixtureQuery
	for index, requirement := range header.Requirements {
		query := header.RequiredPhysicalQueries[index]
		if execution.LogicalQueryRef(query.QueryRevision) != requirement.LogicalQueryRef {
			t.Fatalf("fixture query %d does not match requirement %s", index, requirement.RequirementID)
		}
		if requirement.Role == execution.InputRolePrimary {
			primary = shareFixtureQuery{index: index, query: query}
		} else {
			dependency = shareFixtureQuery{index: index, query: query}
		}
	}
	if primary.query.Digest == "" || dependency.query.Digest == "" {
		t.Fatalf("fixture lacks PRIMARY or dependency query: %+v", header.RequiredPhysicalQueries)
	}
	return primary, dependency
}

// accessShapedCompletionBindings mirrors access.Source completion bindings: one
// per (consumer, requirement), never carrying records.
func accessShapedCompletionBindings(t *testing.T, header execution.InternalExecutionHeader, completions []execution.PhysicalQueryCompletion) []execution.NamedInputBinding {
	t.Helper()
	byQuery := make(map[execution.LogicalQueryRef]execution.PhysicalQueryCompletion, len(completions))
	for _, completion := range completions {
		byQuery[execution.LogicalQueryRef(completion.QueryRevision)] = completion
	}
	bindings := make([]execution.NamedInputBinding, 0)
	for _, requirement := range header.Requirements {
		completion, found := byQuery[requirement.LogicalQueryRef]
		if !found {
			t.Fatalf("requirement %s has no physical completion", requirement.RequirementID)
		}
		for _, consumer := range requirement.Consumers {
			binding := execution.NamedInputBinding{
				Consumer: consumer.Consumer, RequirementID: requirement.RequirementID, DatasetName: requirement.DatasetName,
				Role: requirement.Role, ProviderResult: completion.Ref,
				QueryWindow:  requirement.AbsoluteWindow(header.Contract.Slot.EvaluationTime),
				Completeness: completion.Completeness, ImpactScope: execution.ImpactPlan, PartialEvidence: completion.PartialEvidence,
				Provenance: execution.InputProvenance{PhysicalQuery: completion.PhysicalQuery, AttemptNo: 1},
			}
			switch completion.Completeness {
			case execution.CompletenessFull, execution.CompletenessPartial:
				binding.Dataset = execution.NewDataset(nil)
				binding.View, _ = execution.NewDatasetView(binding.Dataset, nil)
				binding.DataState = execution.DataStateEmpty
				binding.Disposition = execution.AccessAvailable
				if completion.Completeness == execution.CompletenessPartial {
					binding.Disposition = execution.AccessDegraded
					binding.ReasonCode = execution.ReasonCode(contract.ReasonQueryPartial)
				}
			case execution.CompletenessUnavailable:
				binding.DataState = execution.DataStateUnknown
				binding.Disposition = execution.AccessUnavailable
				binding.ReasonCode = execution.ReasonCode(contract.ReasonQueryUnavailable)
				for index := len(completion.RouteFacts.Attempts) - 1; index >= 0; index-- {
					if reason := completion.RouteFacts.Attempts[index].ReasonCode; reason != "" {
						binding.ReasonCode = reason
						break
					}
				}
			}
			bindings = append(bindings, binding)
		}
	}
	return bindings
}

// rebindBatchToSeries copies a single-record fixture batch onto another series
// identity so a query can deliver DATA that does not include the PRIMARY series.
func rebindBatchToSeries(t *testing.T, batch execution.SeriesExecutionBatch, series string) execution.SeriesExecutionBatch {
	t.Helper()
	record, ok := batch.Dataset.Record(0)
	if !ok || batch.Dataset.Len() != 1 {
		t.Fatalf("fixture batch must carry exactly one record: %d", batch.Dataset.Len())
	}
	dataset := execution.NewDataset([]contract.CanonicalRecordV2{{
		RecordID: strings.Repeat("9", 64), SourceTime: record.SourceTime(), BusinessID: record.BusinessID(),
		DimensionIdentity: contract.DimensionIdentityV2{Digest: series},
		Values:            map[string]json.RawMessage{"value": json.RawMessage(`100`)},
		Dimensions:        map[string]json.RawMessage{}, ReceivedTime: record.ReceivedTime(),
	}})
	view, err := execution.NewDatasetView(dataset, []uint32{0})
	if err != nil {
		t.Fatal(err)
	}
	rebound := batch
	rebound.Dataset = dataset
	rebound.Inputs = make([]execution.NamedInputBinding, len(batch.Inputs))
	for index, binding := range batch.Inputs {
		binding.Dataset, binding.View = dataset, view
		rebound.Inputs[index] = binding
	}
	rebound.Delivery.Digest = strings.Repeat("8", 64)
	return rebound
}
