package worker

import (
	"context"
	"errors"
	"fmt"
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
	if got.QueryFailure == nil || got.QueryFailure.Category != "budget" ||
		got.QueryFailure.Code != observability.CapacityBudgetFailureCode(observability.CapacityBudgetRetainedBytes) {
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
		codeSeriesRecordOutsideWindow, codeStreamedNamedInputDuplicate,
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

// Every UNAVAILABLE physical query reports where its code came from, one entry
// each: a Query Group with several Plans that reported only its first would
// undercount the counter by however many Plans share the group. The FULL
// query beside them contributes nothing, and a completion with no unavailable
// query yields nil so healthy completions carry no facts.
func TestProviderUnavailableFactsCountEveryUnavailablePhysicalQuery(t *testing.T) {
	full := execution.PhysicalQueryCompletion{Completeness: execution.CompletenessFull}
	named := execution.PhysicalQueryCompletion{
		Completeness: execution.CompletenessUnavailable,
		RouteFacts: execution.ProviderRouteFacts{Attempts: []execution.RouteAttemptFact{
			{AttemptNo: 1, Result: execution.RouteAttemptFailed, ReasonCode: execution.ReasonCode(contract.ReasonQueryTimeout)},
		}},
	}
	unclassified := execution.PhysicalQueryCompletion{
		Completeness: execution.CompletenessUnavailable,
		RouteFacts:   execution.ProviderRouteFacts{Attempts: []execution.RouteAttemptFact{{AttemptNo: 1, Result: execution.RouteAttemptFailed}}},
	}
	neverSent := execution.PhysicalQueryCompletion{Completeness: execution.CompletenessUnavailable}
	facts := providerUnavailableFacts(execution.QueryExecutionCompletion{PhysicalQueries: []execution.PhysicalQueryCompletion{full, named, unclassified, neverSent, neverSent}})
	want := []string{
		observability.QueryUnavailableFromAttempt, observability.QueryUnavailableNoAttemptReason,
		observability.QueryUnavailableNoAttempts, observability.QueryUnavailableNoAttempts,
	}
	if len(facts) != len(want) {
		t.Fatalf("facts=%+v, want one entry per unavailable physical query: %v", facts, want)
	}
	for index, entry := range facts {
		if entry.Attribution != want[index] {
			t.Fatalf("entry %d = %q, want %q", index, entry.Attribution, want[index])
		}
	}
	if healthy := providerUnavailableFacts(execution.QueryExecutionCompletion{PhysicalQueries: []execution.PhysicalQueryCompletion{full}}); healthy != nil {
		t.Fatalf("a completion without unavailable queries produced facts: %+v", healthy)
	}
	// The code the binding carries is unchanged by the attribution: the last
	// classified attempt names it, else the fallback.
	if code := physicalFailureReason(named.RouteFacts); code != execution.ReasonCode(contract.ReasonQueryTimeout) {
		t.Fatalf("named code = %s", code)
	}
	if code := physicalFailureReason(neverSent.RouteFacts); code != execution.ReasonCode(contract.ReasonQueryUnavailable) {
		t.Fatalf("fallback code = %s", code)
	}
}

// Inputs of one scope that failed differently fold to one reason, and the
// fold is the whole answer.
//
// These three cases were the refusal. A Level whose two inputs came back
// QUERY_TIMEOUT and QUERY_UNAVAILABLE -- which one backend outage produces on
// every round -- left nothing able to satisfy both of the result contract's
// comparisons, and the Plan's entire evaluation was refused for as long as it
// lasted. There is nothing left to decide between them: the marker's reason
// and the Level's own UNKNOWN reason are now the same function of the same
// inputs.
func TestInputsThatFailedDifferentlyFoldToOneReason(t *testing.T) {
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "9022"}
	due := execution.DuePlan{Identity: plan}
	consumer := execution.ConsumerRef{Plan: plan, LevelID: 1, HasLevel: true}
	previous := execution.NamedInputBinding{Consumer: consumer, RequirementID: "req-previous", DatasetName: "previous",
		Role: execution.InputRoleAlgorithmDependency, Completeness: execution.CompletenessUnavailable, ReasonCode: "QUERY_TIMEOUT"}
	history := execution.NamedInputBinding{Consumer: consumer, RequirementID: "req-history", DatasetName: "history_86400",
		Role: execution.InputRoleAlgorithmDependency, Completeness: execution.CompletenessUnavailable, ReasonCode: "QUERY_UNAVAILABLE"}
	partial := previous
	partial.Completeness = execution.CompletenessPartial
	planPrevious, planHistory := previous, history
	planPrevious.Consumer = execution.ConsumerRef{Plan: plan}
	planHistory.Consumer = execution.ConsumerRef{Plan: plan}
	levelScope := execution.GapScope{LevelID: 1, HasLevel: true}

	for _, test := range []struct {
		name     string
		bindings []execution.NamedInputBinding
		scope    execution.GapScope
	}{
		{"a level scope, nothing else deciding", []execution.NamedInputBinding{previous, history}, levelScope},
		{"the other way round", []execution.NamedInputBinding{history, previous}, levelScope},
		{"a partial input disagreeing with an unavailable one", []execution.NamedInputBinding{partial, history}, levelScope},
		{"a plan scope, which has no outcome of its own", []execution.NamedInputBinding{planPrevious, planHistory}, execution.GapScope{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// The signature is the assertion: the fold takes the bindings and
			// nothing else. The Level outcomes and the Plan reason, which used
			// to have to agree with the marker, are no longer reachable from
			// here.
			reasons, err := completionGapReasons(due, test.bindings)
			if err != nil {
				t.Fatalf("completionGapReasons() = %v; inputs that failed differently are not a refusal", err)
			}
			if got := reasons[test.scope]; got != "QUERY_UNAVAILABLE" {
				t.Fatalf("scope %+v folded to %q, want QUERY_UNAVAILABLE: a backend that did not answer "+
					"at all outranks one that answered late", test.scope, got)
			}
		})
	}
}

// An evaluation result the contract refuses for disagreeing with its loads
// reaches the query failure facts under the refusal's own code and detail,
// through the evaluation wrapper, the way a State contract mismatch does.
func TestLoadedFactDispositionRefusalKeepsItsCodeThroughTheEvaluationWrapper(t *testing.T) {
	refused := &execution.LoadedFactDispositionError{Code: execution.QueryFailureCodeRetryableLoadNotRetryPending,
		Plan: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "1001"}, Disposition: execution.PlanDecided,
		PlanReason: "none", RetryableStates: 3, LoadReasons: []execution.ReasonCode{"REDIS_UNAVAILABLE"}}
	wrapped := wrapEvaluationError(codeEvaluationResultInvalid, refused)
	var got observability.Observation
	c := &SlotExecutionCoordinator{ports: Ports{Observer: observability.ObserverFunc(func(_ context.Context, o observability.Observation) { got = observability.NormalizeObservation(o) })}}
	c.observeQueryFailure(context.Background(), execution.OperationNormal, time.Now(), "execute", wrapped)
	if got.QueryFailure == nil || got.QueryFailure.Category != observability.QueryFailureCategoryEvaluation ||
		got.QueryFailure.Code != execution.QueryFailureCodeRetryableLoadNotRetryPending ||
		got.QueryFailure.Detail != "plan=decided-reason=none-states=3-gaps=0-terminal=0-load=redis_unavailable" {
		t.Fatalf("failure facts = %+v, want the evaluation category with the refusal's code and detail", got.QueryFailure)
	}
}

// The no-series path takes the fold too, and the Plan result keeps its own
// reason.
//
// This is the one behaviour the fold changes rather than repairs. On that path
// there is no Level outcome, and the marker used to take whatever reason the
// Plan result carried -- the PRIMARY input's, chosen by the merge. It now
// takes the fold of the scope's inputs, which can be a different input's
// reason. Nothing compares the two: the Plan result's reason is what the round
// reports about itself, the marker's is what protects the scope, and they
// answer different questions. What matters is that the marker is now the same
// function of the same inputs as everywhere else, so no round can be refused
// for the two derivations disagreeing.
func TestTheNoSeriesPathTakesTheFoldAndLeavesThePlanResultAlone(t *testing.T) {
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "9022"}
	due := execution.DuePlan{Identity: plan}
	consumer := execution.ConsumerRef{Plan: plan, LevelID: 1, HasLevel: true}
	primary := execution.NamedInputBinding{Consumer: consumer, RequirementID: "req-primary", DatasetName: "primary",
		Role: execution.InputRolePrimary, Completeness: execution.CompletenessUnavailable, ReasonCode: "QUERY_TIMEOUT"}
	history := execution.NamedInputBinding{Consumer: consumer, RequirementID: "req-history", DatasetName: "history_86400",
		Role: execution.InputRoleAlgorithmDependency, Completeness: execution.CompletenessUnavailable, ReasonCode: "QUERY_UNAVAILABLE"}
	level := execution.GapScope{LevelID: 1, HasLevel: true}

	reasons, err := completionGapReasons(due, []execution.NamedInputBinding{primary, history})
	if err != nil || len(reasons) != 1 || reasons[level] != "QUERY_UNAVAILABLE" {
		t.Fatalf("no-series shape: reasons = %v, err = %v; want the fold of the scope's inputs and not "+
			"whichever one the merge happened to report", reasons, err)
	}
}
