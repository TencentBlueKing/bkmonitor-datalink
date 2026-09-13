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
	_, err := completionGapReasons(due, bindings, nil, "")
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
		got.QueryFailure.Code != codeGapScopeReasonConflict || got.QueryFailure.Detail != "level=3-first=query_partial-second=query_timeout-why=undecided" {
		t.Fatalf("failure facts = %+v, want the named input conflict with both reasons in the detail", got.QueryFailure)
	}
	// The same reason twice in one scope is not a conflict.
	agreeing := []execution.NamedInputBinding{bindings[0], {Consumer: consumer, RequirementID: "req-b", DatasetName: "baseline", Completeness: execution.CompletenessPartial, ReasonCode: "QUERY_PARTIAL"}}
	reasons, err := completionGapReasons(due, agreeing, nil, "")
	if err != nil || reasons[execution.GapScope{LevelID: 3, HasLevel: true}] != "QUERY_PARTIAL" {
		t.Fatalf("agreeing reasons: reasons = %v, err = %v; want the one reason on the Level scope", reasons, err)
	}
}

// The shape the reference deployment shows on every replay after a restart:
// two ALGORITHM_DEPENDENCY inputs of one Level, both UNAVAILABLE, one timed
// out and one refused. The evaluator gave the Level an UNKNOWN outcome with
// one of the two reasons, and the result contract will compare the Level's
// marker with exactly that, so that is the reason the scope carries.
func TestCompletionGapReasonsTakeTheLevelOutcomeReasonWhenInputsDisagree(t *testing.T) {
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "9022"}
	due := execution.DuePlan{Identity: plan}
	consumer := execution.ConsumerRef{Plan: plan, LevelID: 1, HasLevel: true}
	previous := execution.NamedInputBinding{Consumer: consumer, RequirementID: "req-previous", DatasetName: "previous",
		Role: execution.InputRoleAlgorithmDependency, Completeness: execution.CompletenessUnavailable, ReasonCode: "QUERY_TIMEOUT"}
	history := execution.NamedInputBinding{Consumer: consumer, RequirementID: "req-history", DatasetName: "history_86400",
		Role: execution.InputRoleAlgorithmDependency, Completeness: execution.CompletenessUnavailable, ReasonCode: "QUERY_UNAVAILABLE"}
	outcome := func(reason execution.ReasonCode) []execution.LevelOutcome {
		return []execution.LevelOutcome{{Plan: plan, LevelID: 1, Outcome: execution.LevelOutcomeUnknown, ReasonCode: reason}}
	}
	level := execution.GapScope{LevelID: 1, HasLevel: true}
	for _, decided := range []execution.ReasonCode{"QUERY_UNAVAILABLE", "QUERY_TIMEOUT"} {
		reasons, err := completionGapReasons(due, []execution.NamedInputBinding{previous, history}, outcome(decided), "")
		if err != nil || len(reasons) != 1 || reasons[level] != decided {
			t.Fatalf("outcome %s: reasons = %v, err = %v; want the outcome's reason on the Level scope", decided, reasons, err)
		}
	}
	// Inputs that agree decide the scope without the outcome: an outcome that
	// says otherwise is the evaluator's business, not a conflict.
	agreeing := history
	agreeing.ReasonCode = "QUERY_TIMEOUT"
	reasons, err := completionGapReasons(due, []execution.NamedInputBinding{previous, agreeing}, outcome("QUERY_UNAVAILABLE"), "")
	if err != nil || len(reasons) != 1 || reasons[level] != "QUERY_TIMEOUT" {
		t.Fatalf("agreeing inputs: reasons = %v, err = %v; want the inputs' own reason", reasons, err)
	}
	// A Plan-scope input beside the Level keeps its own scope and reason.
	planInput := execution.NamedInputBinding{Consumer: execution.ConsumerRef{Plan: plan}, RequirementID: "req-plan", DatasetName: "primary",
		Role: execution.InputRolePrimary, Completeness: execution.CompletenessUnavailable, ReasonCode: "QUERY_PARTIAL"}
	reasons, err = completionGapReasons(due, []execution.NamedInputBinding{planInput, previous, history}, outcome("QUERY_UNAVAILABLE"), "")
	if err != nil || len(reasons) != 2 || reasons[execution.GapScope{}] != "QUERY_PARTIAL" || reasons[level] != "QUERY_UNAVAILABLE" {
		t.Fatalf("plan and level scopes: reasons = %v, err = %v", reasons, err)
	}
	// Another Plan's bindings and FULL inputs are not this Plan's scopes.
	foreign := previous
	foreign.Consumer.Plan = execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "other"}
	full := history
	full.Completeness = execution.CompletenessFull
	full.ReasonCode = ""
	if _, err := completionGapReasons(due, []execution.NamedInputBinding{foreign, full}, outcome("QUERY_UNAVAILABLE"), ""); err == nil {
		t.Fatal("no incomplete input of this Plan was accepted as a gap scope")
	}
}

// The refusal stays reachable, under its name, wherever the outcome cannot
// decide: the Level has no UNKNOWN outcome, its series disagree, its reason
// is not one the inputs gave, a PARTIAL input carries another reason, or the
// disagreement is on the Plan scope, which has no outcome of its own.
func TestCompletionGapReasonsStillRefuseWhatTheOutcomeCannotDecide(t *testing.T) {
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "9022"}
	due := execution.DuePlan{Identity: plan}
	consumer := execution.ConsumerRef{Plan: plan, LevelID: 1, HasLevel: true}
	previous := execution.NamedInputBinding{Consumer: consumer, RequirementID: "req-previous", DatasetName: "previous",
		Role: execution.InputRoleAlgorithmDependency, Completeness: execution.CompletenessUnavailable, ReasonCode: "QUERY_TIMEOUT"}
	history := execution.NamedInputBinding{Consumer: consumer, RequirementID: "req-history", DatasetName: "history_86400",
		Role: execution.InputRoleAlgorithmDependency, Completeness: execution.CompletenessUnavailable, ReasonCode: "QUERY_UNAVAILABLE"}
	unknown := func(levelID uint32, reason execution.ReasonCode) execution.LevelOutcome {
		return execution.LevelOutcome{Plan: plan, LevelID: levelID, Outcome: execution.LevelOutcomeUnknown, ReasonCode: reason}
	}
	partial := previous
	partial.Completeness = execution.CompletenessPartial
	planPrevious, planHistory := previous, history
	planPrevious.Consumer = execution.ConsumerRef{Plan: plan}
	planHistory.Consumer = execution.ConsumerRef{Plan: plan}
	cases := []struct {
		name       string
		bindings   []execution.NamedInputBinding
		outcomes   []execution.LevelOutcome
		planReason execution.ReasonCode
		detail     string
	}{
		{"no outcome and no plan reason", []execution.NamedInputBinding{previous, history}, nil, "", "level=1-first=query_timeout-second=query_unavailable-why=undecided"},
		{"no outcome and the plan reason is none", []execution.NamedInputBinding{previous, history}, nil, "none", "level=1-first=query_timeout-second=query_unavailable-why=undecided"},
		{"no outcome and a plan reason no input gave", []execution.NamedInputBinding{previous, history}, nil, "CONFIG_DRIFT", "level=1-first=query_timeout-second=query_unavailable-why=undecided"},
		{"outcome of another level", []execution.NamedInputBinding{previous, history}, []execution.LevelOutcome{unknown(2, "QUERY_UNAVAILABLE")}, "", "level=1-first=query_timeout-second=query_unavailable-why=undecided"},
		{"outcome is not unknown", []execution.NamedInputBinding{previous, history},
			[]execution.LevelOutcome{{Plan: plan, LevelID: 1, Outcome: execution.LevelOutcomeNormal}}, "", "level=1-first=query_timeout-second=query_unavailable-why=undecided"},
		{"series disagree", []execution.NamedInputBinding{previous, history},
			[]execution.LevelOutcome{unknown(1, "QUERY_UNAVAILABLE"), unknown(1, "QUERY_TIMEOUT")}, "", "level=1-first=query_timeout-second=query_unavailable-why=undecided"},
		{"outcome reason no input gave", []execution.NamedInputBinding{previous, history}, []execution.LevelOutcome{unknown(1, "CONFIG_DRIFT")}, "", "level=1-first=query_timeout-second=query_unavailable-why=undecided"},
		{"partial input carries another reason than the outcome", []execution.NamedInputBinding{partial, history}, []execution.LevelOutcome{unknown(1, "QUERY_UNAVAILABLE")}, "", "level=1-first=query_timeout-second=query_unavailable-why=partial"},
		{"partial input carries another reason than the plan", []execution.NamedInputBinding{partial, history}, nil, "QUERY_UNAVAILABLE", "level=1-first=query_timeout-second=query_unavailable-why=partial"},
		{"plan scope has no outcome and no plan reason", []execution.NamedInputBinding{planPrevious, planHistory}, []execution.LevelOutcome{unknown(1, "QUERY_UNAVAILABLE")}, "", "level=plan-first=query_timeout-second=query_unavailable-why=undecided"},
	}
	for _, c := range cases {
		_, err := completionGapReasons(due, c.bindings, c.outcomes, c.planReason)
		var conflict *gapScopeReasonConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("%s: err = %v, want the named conflict", c.name, err)
		}
		if got := conflict.QueryFailureDetail(); got != c.detail {
			t.Fatalf("%s: detail = %q, want %q", c.name, got, c.detail)
		}
		for _, want := range []string{"strategy 9022", "req-previous", "req-history"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("%s: error text %q does not name %q", c.name, err.Error(), want)
			}
		}
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

// The shape the reference deployment shows on the no-series path in steady
// state: the PRIMARY input timed out and a dependency input was refused, both
// UNAVAILABLE on one Level, no series and so no outcome. The Plan result
// carries the PRIMARY's reason, and the contract compares no marker with a
// reason on that path, so the scope takes the Plan's reason. On the series
// path the Level's outcome still decides first, and the Plan's reason is
// only consulted where there is none.
func TestCompletionGapReasonsTakeThePlanReasonWhereNoOutcomeDecides(t *testing.T) {
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "9022"}
	due := execution.DuePlan{Identity: plan}
	consumer := execution.ConsumerRef{Plan: plan, LevelID: 1, HasLevel: true}
	primary := execution.NamedInputBinding{Consumer: consumer, RequirementID: "req-primary", DatasetName: "primary",
		Role: execution.InputRolePrimary, Completeness: execution.CompletenessUnavailable, ReasonCode: "QUERY_TIMEOUT"}
	history := execution.NamedInputBinding{Consumer: consumer, RequirementID: "req-history", DatasetName: "history_86400",
		Role: execution.InputRoleAlgorithmDependency, Completeness: execution.CompletenessUnavailable, ReasonCode: "QUERY_UNAVAILABLE"}
	level := execution.GapScope{LevelID: 1, HasLevel: true}
	reasons, err := completionGapReasons(due, []execution.NamedInputBinding{primary, history}, nil, primary.ReasonCode)
	if err != nil || len(reasons) != 1 || reasons[level] != "QUERY_TIMEOUT" {
		t.Fatalf("no-series shape: reasons = %v, err = %v; want the Plan's reason on the Level scope", reasons, err)
	}
	// The Plan's reason may be the dependency's when the merge path chose an
	// UNAVAILABLE input; whichever input carries it decides.
	reasons, err = completionGapReasons(due, []execution.NamedInputBinding{primary, history}, nil, "QUERY_UNAVAILABLE")
	if err != nil || reasons[level] != "QUERY_UNAVAILABLE" {
		t.Fatalf("plan reason of the dependency: reasons = %v, err = %v", reasons, err)
	}
	// With an outcome, the outcome decides even when the Plan's reason differs.
	outcome := []execution.LevelOutcome{{Plan: plan, LevelID: 1, Outcome: execution.LevelOutcomeUnknown, ReasonCode: "QUERY_UNAVAILABLE"}}
	reasons, err = completionGapReasons(due, []execution.NamedInputBinding{primary, history}, outcome, "QUERY_TIMEOUT")
	if err != nil || reasons[level] != "QUERY_UNAVAILABLE" {
		t.Fatalf("outcome over plan reason: reasons = %v, err = %v", reasons, err)
	}
}
