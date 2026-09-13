package worker

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Stable failure codes for worker-side query contract violations. Each code
// names one branch so a rate-limited log line identifies the exact check that
// failed instead of collapsing to OTHER. Codes must match the failure code
// grammar (^[A-Z][A-Z0-9_]{0,63}$).
const (
	codeCompletionBeforeBegin             = "COMPLETION_BEFORE_BEGIN"
	codeCompletionInvalid                 = "COMPLETION_INVALID"
	codeCompletionBindingMismatch         = "COMPLETION_BINDING_MISMATCH"
	codeCompletionBindingPhysicalMismatch = "COMPLETION_BINDING_PHYSICAL_MISMATCH"
	codeDuplicateCompletionBinding        = "DUPLICATE_COMPLETION_BINDING"
	codeStreamedBindingMismatch           = "STREAMED_BINDING_MISMATCH"
	codeStreamedBindingFullMismatch       = "STREAMED_BINDING_FULL_MISMATCH"
	codeStreamedBindingPartialMismatch    = "STREAMED_BINDING_PARTIAL_MISMATCH"
	codeInvalidCompleteness               = "INVALID_COMPLETENESS"
	codeDuePlanResultMissing              = "DUE_PLAN_RESULT_MISSING"
	codeNoSeriesPlanResultInvalid         = "NO_SERIES_PLAN_RESULT_INVALID"
	codeCompletionOnlyRequirementMissing  = "COMPLETION_ONLY_REQUIREMENT_MISSING"
	codeCompletionOnlyExactSetInvalid     = "COMPLETION_ONLY_EXACT_SET_INVALID"
	codeNamedInputExactSetInvalid         = "NAMED_INPUT_EXACT_SET_INVALID"
	codePhysicalCompletionMissing         = "PHYSICAL_COMPLETION_MISSING"
	codePhysicalCompletenessInvalid       = "PHYSICAL_COMPLETENESS_INVALID"
	codeRequirementQueryAmbiguous         = "REQUIREMENT_QUERY_AMBIGUOUS"
	codeRequirementQueryMissing           = "REQUIREMENT_QUERY_MISSING"
	codeSeriesBeforeBegin                 = "SERIES_BEFORE_BEGIN"
	codeSeriesBatchInvalid                = "SERIES_BATCH_INVALID"
	codeSeriesBatchNotSingleSeries        = "SERIES_BATCH_NOT_SINGLE_SERIES"
	codeSeriesBindingOutsideRequirements  = "SERIES_BINDING_OUTSIDE_REQUIREMENTS"
	codeSeriesBindingDuplicate            = "SERIES_BINDING_DUPLICATE"
	codeSeriesBindingMismatch             = "SERIES_BINDING_MISMATCH"
	codeSeriesRecordOutsideWindow         = "SERIES_RECORD_OUTSIDE_WINDOW"
	codeStreamedNamedInputDuplicate       = "STREAMED_NAMED_INPUT_DUPLICATE"
	codeStreamedNamedInputFoldInvalid     = "STREAMED_NAMED_INPUT_FOLD_INVALID"
	codeEvaluationFailed                  = "EVALUATION_FAILED"
	codeEvaluationResultInvalid           = "EVALUATION_RESULT_INVALID"
)

// queryContractError is a typed worker failure. It keeps the historical error
// text (and the wrapped cause when there is one) and exposes the bounded
// category/code pair that is safe to log.
type queryContractError struct {
	category string
	code     string
	err      error
}

func (e *queryContractError) Error() string                  { return e.err.Error() }
func (e *queryContractError) Unwrap() error                  { return e.err }
func (e *queryContractError) QueryFailure() (string, string) { return e.category, e.code }

func completionContractError(code, text string) error {
	return &queryContractError{category: observability.QueryFailureCategoryCompletionContract, code: code, err: errors.New(text)}
}

func wrapCompletionContractError(code string, err error) error {
	return &queryContractError{category: observability.QueryFailureCategoryCompletionContract, code: code, err: err}
}

func namedInputError(code, text string) error {
	return &queryContractError{category: observability.QueryFailureCategoryNamedInput, code: code, err: errors.New(text)}
}

func wrapNamedInputError(code string, err error) error {
	return &queryContractError{category: observability.QueryFailureCategoryNamedInput, code: code, err: err}
}

// wrapEvaluationError names a series evaluation failure at stream_complete.
// The evaluation and result contract errors are plain errors; without the
// bounded code the rate-limited query_completed line and the target-flow
// facts collapsed to other/OTHER and did not say that evaluation failed.
//
// The code passed in says which of the two wrap sites threw, not why, so every
// cause reaching one site collapsed into one value: a whole restart window of
// failures read as EVALUATION_FAILED whatever had actually gone wrong, and the
// cause survived only in the free-text message, which is rate limited. An
// error that names its own cause keeps that name here; the category stays with
// the stage, because where it failed is what the wrap site knows.
func wrapEvaluationError(code string, err error) error {
	var declared interface{ QueryFailure() (string, string) }
	if errors.As(err, &declared) {
		if _, inner := declared.QueryFailure(); inner != "" {
			code = inner
		}
	}
	return &queryContractError{category: observability.QueryFailureCategoryEvaluation, code: code, err: err}
}

func (coordinator *SlotExecutionCoordinator) observeQueryFailure(ctx context.Context, operation execution.Operation, started time.Time, stage string, err error) {
	facts := observability.QueryFailureFacts{Stage: stage, Category: observability.QueryFailureCategoryOther, Code: observability.QueryFailureCodeOther}
	var exceeded *provisionalBudgetExceededError
	var diagnostic interface{ QueryFailure() (string, string) }
	if errors.As(err, &exceeded) {
		facts.Category = observability.QueryFailureCategoryBudget
		facts.Code = string(exceeded.budget)
	} else if errors.As(err, &diagnostic) {
		facts.Category, facts.Code = diagnostic.QueryFailure()
	}
	observation := observability.Observation{Component: observability.ComponentAccess, Stage: observability.StageQueryCompleted, Result: observability.ResultFailed, Operation: observability.Operation(operation), Direction: observability.DirectionInternal, ReasonCode: observability.ReasonInternalUnknown, Duration: time.Since(started), Err: err, QueryFailure: &facts}
	defer func() { _ = recover() }()
	coordinator.ports.Observer.Observe(ctx, observation)
}

// observeQueryCompleted records the Access query_completed boundary for a
// completion that was accepted by the stream contract. When the provider
// reported an UNAVAILABLE physical query, the observation carries the failed
// attempt's reason and bounded detail so the rate-limited log line explains
// the failure without changing completeness semantics.
func (coordinator *SlotExecutionCoordinator) observeQueryCompleted(
	ctx context.Context,
	operation execution.Operation,
	started time.Time,
	result observability.Result,
	reason execution.ReasonCode,
	completion execution.QueryExecutionCompletion,
	evaluated execution.EvaluationResult,
) {
	if result == "" {
		result = observability.ResultSuccess
	}
	if reason == "" {
		reason = observability.ReasonNone
	}
	facts := providerFailureFacts(completion)
	if facts == nil {
		facts = unexplainedOutcomeFacts(result, reason, evaluated)
	}
	observation := observability.Observation{
		Component: observability.ComponentAccess, Stage: observability.StageQueryCompleted, Result: result,
		Operation: observability.Operation(operation), Direction: observability.DirectionInternal,
		ReasonCode: reason, Duration: time.Since(started),
		QueryFailure: facts, QueryStatus: providerStatusFacts(completion),
	}
	defer func() { _ = recover() }()
	coordinator.ports.Observer.Observe(ctx, observation)
}

// providerStatusFacts projects every physical query that carried a backend
// status code, not the first one.
//
// providerFailureFacts beside it takes only the first, and that is right for
// what it feeds: a log line explaining one failure. This feeds a counter, and
// the difference matters - a log missing an entry is an entry missing, a
// counter missing an entry is a wrong number. One completion holds one physical
// query per Plan, so a Query Group with several Plans reports several, and
// reporting only the first would undercount by however many Plans share the
// group.
func providerStatusFacts(completion execution.QueryExecutionCompletion) []observability.QueryStatusFacts {
	var facts []observability.QueryStatusFacts
	for _, item := range completion.PhysicalQueries {
		status := item.RouteFacts.Status
		if status == nil {
			continue
		}
		outcome := observability.QueryStatusOutcomeUnavailable
		if status.Allowed {
			outcome = observability.QueryStatusOutcomeAllowed
		}
		facts = append(facts, observability.QueryStatusFacts{Code: status.Code, Outcome: outcome})
	}
	return facts
}

// providerFailureFacts projects the first UNAVAILABLE physical completion onto
// log-only failure facts: the last failed route attempt's reason code and its
// bounded detail. A completion without an unavailable physical query yields
// nil so healthy lines stay unchanged.
func providerFailureFacts(completion execution.QueryExecutionCompletion) *observability.QueryFailureFacts {
	for _, item := range completion.PhysicalQueries {
		if item.Completeness != execution.CompletenessUnavailable {
			continue
		}
		facts := &observability.QueryFailureFacts{
			Stage:    observability.QueryFailureStageProvider,
			Category: physicalFailureCategory(item.RouteFacts),
			Code:     string(physicalFailureReason(item.RouteFacts)),
		}
		for index := len(item.RouteFacts.Attempts) - 1; index >= 0; index-- {
			attempt := item.RouteFacts.Attempts[index]
			if attempt.Result != execution.RouteAttemptFailed && attempt.ReasonCode == "" {
				continue
			}
			facts.Detail = attempt.Detail
			break
		}
		return facts
	}
	return nil
}

// physicalFailureCategory shares the existing log classification with query
// availability evidence. Admission/readiness and unknown failures have no
// backend response detail, so they cannot trigger backend cooldown.
func physicalFailureCategory(routes execution.ProviderRouteFacts) string {
	for index := len(routes.Attempts) - 1; index >= 0; index-- {
		attempt := routes.Attempts[index]
		if attempt.Result != execution.RouteAttemptFailed && attempt.ReasonCode == "" {
			continue
		}
		switch execution.RouteDetailKind(attempt.Detail) {
		case execution.RouteDetailKindHTTPStatus, execution.RouteDetailKindResponse:
			return observability.QueryFailureCategorySourceBackend
		}
		break
	}
	return observability.QueryFailureCategoryProviderTransport
}

// unexplainedOutcomeFacts names a non-successful evaluation outcome that
// carries no reason of its own. Without it the line normalises to
// internal_unknown with no error text, which is how several Query Groups
// retried for hours with nothing to root-cause them by. It reports the Plan
// disposition and the Level outcome mix, both already bounded enumerations,
// and stays nil for healthy or already-explained outcomes.
func unexplainedOutcomeFacts(
	result observability.Result, reason execution.ReasonCode, evaluated execution.EvaluationResult,
) *observability.QueryFailureFacts {
	if result == observability.ResultSuccess || result == "" {
		return nil
	}
	if reason != "" && reason != observability.ReasonNone {
		return nil
	}
	dispositions := make([]string, 0, len(evaluated.Plans))
	outcomes := map[execution.LevelOutcomeKind]int{}
	seen := map[execution.PlanDisposition]struct{}{}
	for _, plan := range evaluated.Plans {
		if _, ok := seen[plan.Disposition]; !ok {
			seen[plan.Disposition] = struct{}{}
			dispositions = append(dispositions, string(plan.Disposition))
		}
		for _, outcome := range plan.LevelOutcomes {
			outcomes[outcome.Outcome]++
		}
	}
	sort.Strings(dispositions)
	kinds := make([]string, 0, len(outcomes))
	for kind, count := range outcomes {
		kinds = append(kinds, fmt.Sprintf("%s:%d", kind, count))
	}
	sort.Strings(kinds)
	detail := "plans=" + strconv.Itoa(len(evaluated.Plans))
	if len(dispositions) > 0 {
		detail += " dispositions=" + strings.Join(dispositions, ",")
	}
	if len(kinds) > 0 {
		detail += " levels=" + strings.Join(kinds, ",")
	}
	return &observability.QueryFailureFacts{
		Stage:    observability.QueryFailureStageOther,
		Category: observability.QueryFailureCategoryEvaluation,
		Code:     "UNEXPLAINED_OUTCOME",
		Detail:   detail,
	}
}
