package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestQueryFailureWrappedBudgetWinsOverProviderDiagnostic(t *testing.T) {
	var got observability.Observation
	c := &SlotExecutionCoordinator{ports: Ports{Observer: observability.ObserverFunc(func(_ context.Context, o observability.Observation) { got = observability.NormalizeObservation(o) })}}
	original := &provisionalBudgetExceededError{budget: observability.CapacityBudgetRetainedBytes}
	c.observeQueryFailure(context.Background(), execution.OperationReplay, time.Now(), "execute", providerLikeBudgetError{fmt.Errorf("https://user:secret@example.test/?token=secret: %w", original)})
	if got.QueryFailure == nil || got.QueryFailure.Category != "budget" || got.QueryFailure.Code != "retained_bytes" {
		t.Fatalf("diagnostics=%+v", got.QueryFailure)
	}
}

type providerLikeBudgetError struct{ error }

func (e providerLikeBudgetError) Unwrap() error { return e.error }
func (providerLikeBudgetError) QueryFailure() (string, string) {
	return "source_backend", "SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS"
}

func TestQueryContractErrorsKeepTextAndChainWhileExposingCodes(t *testing.T) {
	plain := completionContractError(codeDuplicateCompletionBinding, "alarmd worker: duplicate completion binding")
	if plain.Error() != "alarmd worker: duplicate completion binding" {
		t.Fatalf("text changed: %q", plain.Error())
	}
	cause := errors.New("alarmd execution: completion does not cover all frozen physical queries")
	wrapped := fmt.Errorf("alarmd worker: invalid query result: %w", wrapCompletionContractError(codeCompletionInvalid, cause))
	if !errors.Is(wrapped, cause) || wrapped.Error() != "alarmd worker: invalid query result: "+cause.Error() {
		t.Fatalf("wrapped chain or text lost: %v", wrapped)
	}
	var diagnostic interface{ QueryFailure() (string, string) }
	if !errors.As(wrapped, &diagnostic) {
		t.Fatal("wrapped contract error lost its diagnostic")
	}
	if category, code := diagnostic.QueryFailure(); category != "completion_contract" || code != codeCompletionInvalid {
		t.Fatalf("diagnostic=(%s,%s)", category, code)
	}
	for _, code := range []string{
		codeCompletionBeforeBegin, codeCompletionInvalid, codeCompletionBindingMismatch, codeCompletionBindingPhysicalMismatch,
		codeDuplicateCompletionBinding, codeStreamedBindingMismatch, codeStreamedBindingFullMismatch, codeStreamedBindingPartialMismatch,
		codeInvalidCompleteness, codeDuePlanResultMissing, codeNoSeriesPlanResultInvalid, codeCompletionOnlyRequirementMissing,
		codeCompletionOnlyExactSetInvalid, codeNamedInputExactSetInvalid, codePhysicalCompletionMissing, codePhysicalCompletenessInvalid,
		codeRequirementQueryAmbiguous, codeRequirementQueryMissing, codeSeriesBeforeBegin, codeSeriesBatchInvalid,
		codeSeriesBatchNotSingleSeries, codeSeriesBindingOutsideRequirements, codeSeriesBindingDuplicate, codeSeriesBindingMismatch,
		codeSeriesRecordOutsideWindow, codeStreamedNamedInputDuplicate, codeGapScopeReasonConflict,
	} {
		if !observability.ValidQueryFailureCode(code) {
			t.Fatalf("worker failure code %q violates the log code grammar", code)
		}
	}
}

func TestProviderFailureFactsProjectLastFailedAttempt(t *testing.T) {
	full := execution.PhysicalQueryCompletion{Completeness: execution.CompletenessFull}
	unavailable := execution.PhysicalQueryCompletion{
		Completeness: execution.CompletenessUnavailable,
		RouteFacts: execution.ProviderRouteFacts{Attempts: []execution.RouteAttemptFact{
			{AttemptNo: 1, Result: execution.RouteAttemptFailed, ReasonCode: execution.ReasonCode(contract.ReasonQueryUnavailable), Detail: execution.HTTPStatusRouteDetail(502)},
			{AttemptNo: 2, Result: execution.RouteAttemptFailed, ReasonCode: execution.ReasonCode(contract.ReasonQueryTimeout), Detail: execution.TransportRouteDetail(execution.TransportFailureTimeout)},
		}},
	}
	if facts := providerFailureFacts(execution.QueryExecutionCompletion{PhysicalQueries: []execution.PhysicalQueryCompletion{full}}); facts != nil {
		t.Fatalf("FULL completion produced failure facts: %+v", facts)
	}
	facts := providerFailureFacts(execution.QueryExecutionCompletion{PhysicalQueries: []execution.PhysicalQueryCompletion{full, unavailable}})
	want := observability.QueryFailureFacts{Stage: "provider", Category: "provider_transport", Code: contract.ReasonQueryTimeout, Detail: "transport=timeout"}
	if facts == nil || *facts != want {
		t.Fatalf("facts=%+v, want %+v", facts, want)
	}
	bare := execution.PhysicalQueryCompletion{Completeness: execution.CompletenessUnavailable}
	facts = providerFailureFacts(execution.QueryExecutionCompletion{PhysicalQueries: []execution.PhysicalQueryCompletion{bare}})
	want = observability.QueryFailureFacts{Stage: "provider", Category: "provider_transport", Code: contract.ReasonQueryUnavailable}
	if facts == nil || *facts != want {
		t.Fatalf("bare facts=%+v, want %+v", facts, want)
	}
}

// A Plan whose incomplete named inputs of one gap scope carry different
// completion reasons is refused under its own name, with both reasons: the
// code goes to the counter's category, the pair to the bounded detail, and
// the inputs to the text, so the shape can be read off one line instead of
// inferred from an internal_unknown.
func TestCompletionGapMutationNamesTheConflictingReasons(t *testing.T) {
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "1001"}
	due := execution.DuePlan{Identity: plan}
	consumer := execution.ConsumerRef{Plan: plan, LevelID: 3, HasLevel: true}
	bindings := []execution.NamedInputBinding{
		{Consumer: consumer, RequirementID: "req-a", DatasetName: "primary", Completeness: execution.CompletenessPartial, ReasonCode: "QUERY_PARTIAL"},
		{Consumer: consumer, RequirementID: "req-b", DatasetName: "baseline", Completeness: execution.CompletenessUnavailable, ReasonCode: "QUERY_TIMEOUT"},
	}
	var stream *streamedExecution
	_, err := stream.completionGapMutation(due, bindings)
	var conflict *gapScopeReasonConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("completionGapMutation error = %v, want the gap scope conflict", err)
	}
	text := err.Error()
	for _, want := range []string{"strategy 1001", "level 3", "req-a", "req-b", "QUERY_PARTIAL", "QUERY_TIMEOUT"} {
		if !strings.Contains(text, want) {
			t.Fatalf("error text %q does not name %q", text, want)
		}
	}
	var got observability.Observation
	c := &SlotExecutionCoordinator{ports: Ports{Observer: observability.ObserverFunc(func(_ context.Context, o observability.Observation) { got = observability.NormalizeObservation(o) })}}
	c.observeQueryFailure(context.Background(), execution.OperationNormal, time.Now(), observability.QueryFailureStageStreamComplete, err)
	if got.QueryFailure == nil || got.QueryFailure.Category != observability.QueryFailureCategoryNamedInput ||
		got.QueryFailure.Code != codeGapScopeReasonConflict || got.QueryFailure.Detail != "level=3-first=query_partial-second=query_timeout" {
		t.Fatalf("failure facts = %+v, want the named input conflict with both reasons in the detail", got.QueryFailure)
	}
	// The same reason twice in one scope is not a conflict.
	agreeing := []execution.NamedInputBinding{bindings[0], {Consumer: consumer, RequirementID: "req-b", DatasetName: "baseline", Completeness: execution.CompletenessPartial, ReasonCode: "QUERY_PARTIAL"}}
	func() {
		defer func() {
			// The nil stream cannot build the mutation; reaching that step is
			// the assertion, so the panic it causes is the expected end.
			if recover() == nil {
				t.Fatal("agreeing reasons were refused before the mutation was built")
			}
		}()
		_, _ = stream.completionGapMutation(due, agreeing)
	}()
}
