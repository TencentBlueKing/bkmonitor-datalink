package worker

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
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
	reason := observability.ReasonInternalUnknown
	var exceeded *provisionalBudgetExceededError
	var diagnostic interface{ QueryFailure() (string, string) }
	if errors.As(err, &exceeded) {
		facts.Category = observability.QueryFailureCategoryBudget
		facts.Code = observability.CapacityBudgetFailureCode(exceeded.budget)
		// Named rather than internal_unknown, and by the same rule the capacity
		// rejection uses: a Slot whose own output is past a per-Slot cap gets
		// the terminal code, because no retry in this process produces a
		// smaller one, and a rejection on shared capacity gets the pause code,
		// because concurrent Slots do free their share.
		reason = observability.ReasonCode(contract.ReasonResourceHardStop)
		if exceeded.slot {
			reason = observability.ReasonCode(contract.ReasonSlotBudgetExceeded)
		}
	} else if errors.As(err, &diagnostic) {
		facts.Category, facts.Code = diagnostic.QueryFailure()
		// A provider that stopped answering partway is the provider's
		// timeout or unavailability, the reason the same failure before its
		// answer began completes with - not an internal error of alarmd's.
		if facts.Category == observability.QueryFailureCategoryProviderTransport {
			reason = observability.ReasonCode(facts.Code)
		}
		// A query that never reached its backend because the permit wait ran
		// out names that, not an internal error.
		if facts.Category == observability.QueryFailureCategoryAdmission {
			reason = observability.ReasonCode(facts.Code)
		}
		// A deadline that passed while alarmd was decoding or delivering an
		// answer that had begun: alarmd's own timeout, by its name.
		if facts.Category == observability.QueryFailureCategoryDelivery {
			reason = observability.ReasonCode(facts.Code)
		}
		// A host-placed series whose alert identity no CMDB index could read
		// yet: the query's own word, not an internal error.
		if facts.Category == observability.QueryFailureCategorySeriesIdentity {
			reason = observability.ReasonCode(facts.Code)
		}
	}
	// How far the answer got before the query failed.
	var sized interface {
		QueryFailureVolume() *observability.QueryVolume
	}
	if errors.As(err, &sized) {
		facts.Volume = sized.QueryFailureVolume()
	}
	// Which deadline was on the request and which ran out, and the physical
	// query's own plan.
	var requested interface {
		QueryFailureRequest() *execution.RequestFacts
	}
	if errors.As(err, &requested) {
		facts.Request = requested.QueryFailureRequest().Observed()
	}
	// A timeout the round filed at the query's own deadline names that
	// branch over whatever the request's own error said.
	var sourced interface{ QueryFailureTimeoutSource() string }
	if facts.Request != nil && errors.As(err, &sourced) {
		facts.Request.TimeoutSource = sourced.QueryFailureTimeoutSource()
	}
	var planned interface {
		QueryFailurePlanned() *execution.PlannedQueryFacts
	}
	if errors.As(err, &planned) {
		facts.Planned = planned.QueryFailurePlanned().Observed()
	}
	// A query stopped because the Slot's context was cancelled from above --
	// the process stopping or the Query Group leaving -- says so rather than
	// reading as an internal error on every rollout's outgoing replicas.
	if reason == observability.ReasonInternalUnknown && observability.CancelledFromAbove(ctx, err) {
		reason = observability.ReasonSlotCancelled
	}
	// A failure that can say more than its code, in the bounded detail
	// grammar, does so here: the detail is what the rate-limited line keeps
	// when the free text is gone.
	var detailed interface{ QueryFailureDetail() string }
	if errors.As(err, &detailed) {
		facts.Detail = detailed.QueryFailureDetail()
	}
	// And one that timed itself against its budget says so beside it.
	var timed interface {
		QueryFailureTiming() *execution.AttemptTiming
	}
	if errors.As(err, &timed) {
		facts.Timing = queryTimingOf(timed.QueryFailureTiming())
	}
	observation := observability.Observation{Component: observability.ComponentAccess, Stage: observability.StageQueryCompleted, Result: observability.ResultFailed, Operation: observability.Operation(operation), ReasonCode: reason, Duration: time.Since(started), Err: err, QueryFailure: &facts,
		// A failed query explains itself as a lost round does: the decision
		// that let it run as a normal round and the budget it ran under.
		DivertDecision: observability.DivertDecisionWithBudget(ctx, observability.BudgetCaptureFromContext(ctx)),
		QueryBudget:    queryLineBudget(ctx)}
	defer func() { _ = recover() }()
	coordinator.ports.Observer.Observe(ctx, observation)
}

// observeQueryCompleted records the Access query_completed boundary for a
// completion that was accepted by the stream contract. When the provider
// reported an UNAVAILABLE physical query, the observation carries the failed
// attempt's reason and bounded detail so the rate-limited log line explains
// the failure without changing completeness semantics.
//
// The line's result and reason are the physical queries' own
// (queryCompletedOutcome); the round's evaluation result travels beside them
// as evaluation_result and evaluation_reason. They used to be one: the
// evaluation's worst series, so a query that answered whole in 120 ms read as
// "degraded QUERY_TIMEOUT" on every round a Level stayed UNKNOWN under a
// guard an earlier timeout had raised.
func (coordinator *SlotExecutionCoordinator) observeQueryCompleted(
	ctx context.Context,
	operation execution.Operation,
	started time.Time,
	completion execution.QueryExecutionCompletion,
	evaluated execution.EvaluationResult,
) {
	evaluationResult, evaluationReason := provisionalResult(evaluated)
	if evaluationResult == "" {
		evaluationResult = observability.ResultSuccess
	}
	if evaluationReason == "" {
		evaluationReason = observability.ReasonNone
	}
	result, reason := queryCompletedOutcome(completion, evaluated)
	facts := providerFailureFacts(completion)
	if facts == nil {
		// An evaluation outcome that names no reason is explained here as it
		// was: it is the evaluation's, by category, and is read from the
		// evaluation's result, not the query's.
		facts = unexplainedOutcomeFacts(evaluationResult, evaluationReason, evaluated)
	}
	observation := observability.Observation{
		Component: observability.ComponentAccess, Stage: observability.StageQueryCompleted, Result: result,
		Operation:  observability.Operation(operation),
		ReasonCode: reason, Duration: time.Since(started),
		QueryFailure: facts, QueryStatus: providerStatusFacts(completion), QueryCodec: providerCodecFacts(completion),
		QueryUnavailable: providerUnavailableFacts(completion), QueryTruncation: providerTruncationFacts(completion),
		QueryRanges:      providerRangeFacts(completion),
		EvaluationResult: evaluationResult, EvaluationReason: evaluationReason,
		CarriedReason: execution.CarriedReasonFacts(evaluated.ReasonCarried && evaluationReason != observability.ReasonNone,
			evaluated.ReasonOrigin),
	}
	// A degraded or failed query explains itself as a lost round does. Only
	// the query's own result: a query that answered whole under an
	// evaluation a stored guard held lost nothing.
	lost := result == observability.ResultDegraded || result == observability.ResultFailed
	if lost {
		observation.DivertDecision = observability.DivertDecisionWithBudget(ctx, observability.BudgetCaptureFromContext(ctx))
		observation.QueryBudget = queryLineBudget(ctx)
	}
	// An answer degraded without failing (partial, empty) names the request
	// it answered, as a failed one does in its failure facts. An empty answer
	// is a whole one, so it degrades only the evaluation; the request is
	// named on either.
	if lost || evaluationResult == observability.ResultDegraded || evaluationResult == observability.ResultFailed {
		if facts == nil || facts.Request == nil {
			observation.QueryRequest = answeredRequestFacts(completion)
		}
	}
	defer func() { _ = recover() }()
	coordinator.ports.Observer.Observe(ctx, observation)
}

// queryCompletedOutcome is the result and reason the query_completed line
// reports: the round's physical queries' own.
//
// Success with no reason when every physical query of the round answered
// FULL - with data or empty, as the codec delivered it - since none failed.
// A round with a physical query PARTIAL or UNAVAILABLE keeps the result and
// reason it has always reported, the evaluation's, which a physical failure
// decides: the failed binding's reason is what the Plan folds to. The one
// exception is an evaluation whose reason is a stored guard's (carried): it
// says nothing about this round's queries, so the failed query's own reason
// is reported instead, degraded.
func queryCompletedOutcome(completion execution.QueryExecutionCompletion, evaluated execution.EvaluationResult) (observability.Result, execution.ReasonCode) {
	failed, failedReason := physicalQueryFailure(completion)
	if !failed {
		return observability.ResultSuccess, observability.ReasonNone
	}
	result, reason := provisionalResult(evaluated)
	if evaluated.ReasonCarried || result == "" || result == observability.ResultSuccess {
		return observability.ResultDegraded, failedReason
	}
	if reason == "" {
		reason = observability.ReasonNone
	}
	return result, reason
}

// physicalQueryFailure says whether a physical query of the round answered
// short of FULL, and the first such query's reason: the attributed code an
// UNAVAILABLE one completes with (the word its failure facts carry), or the
// reason access gave a PARTIAL one's binding.
func physicalQueryFailure(completion execution.QueryExecutionCompletion) (bool, execution.ReasonCode) {
	for _, item := range completion.PhysicalQueries {
		switch item.Completeness {
		case execution.CompletenessUnavailable:
			code, from := physicalFailureReason(item.RouteFacts)
			return true, execution.AttributedReason(code, from)
		case execution.CompletenessPartial:
			for _, binding := range completion.CompletionBindings {
				if binding.Provenance.PhysicalQuery == item.PhysicalQuery && binding.Completeness == item.Completeness && binding.ReasonCode != "" {
					return true, binding.ReasonCode
				}
			}
			return true, execution.ReasonCode(contract.ReasonQueryPartial)
		}
	}
	return false, ""
}

// answeredRequestFacts is the request of the completion's first degraded
// answer - partial, empty or unavailable, in that order of physical query -
// or of its first answered query when none is; nil when no attempt carries
// one.
func answeredRequestFacts(completion execution.QueryExecutionCompletion) *observability.QueryRequestFacts {
	var first *execution.RequestFacts
	for _, item := range completion.PhysicalQueries {
		attempt := lastAttempt(item.RouteFacts)
		if attempt == nil || attempt.Request == nil {
			continue
		}
		if item.Completeness != execution.CompletenessFull || item.DataState != execution.DataStateData {
			return attempt.Request.Observed()
		}
		if first == nil {
			first = attempt.Request
		}
	}
	return first.Observed()
}

// lastAttempt is a physical query's last route attempt; nil for none.
func lastAttempt(route execution.ProviderRouteFacts) *execution.RouteAttemptFact {
	if len(route.Attempts) == 0 {
		return nil
	}
	return &route.Attempts[len(route.Attempts)-1]
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
func providerCodecFacts(completion execution.QueryExecutionCompletion) []observability.QueryCodecFacts {
	var facts []observability.QueryCodecFacts
	for _, item := range completion.PhysicalQueries {
		if item.Stats.ResponseCodec == "" {
			continue
		}
		negotiation := "disabled"
		if item.Stats.SharedSchemaRequested {
			negotiation = "opt_in"
		}
		facts = append(facts, observability.QueryCodecFacts{Codec: item.Stats.ResponseCodec,
			Negotiation: negotiation, Completion: strings.ToLower(string(item.Completeness))})
	}
	return facts
}

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

// providerTruncationFacts projects every physical query whose answer the
// provider suspects was cut, one entry each, for the same reason the two
// beside it take every one: it feeds a counter.
func providerTruncationFacts(completion execution.QueryExecutionCompletion) []observability.QueryTruncationFacts {
	var facts []observability.QueryTruncationFacts
	for _, item := range completion.PhysicalQueries {
		if cut := item.RouteFacts.Truncation; cut != nil {
			facts = append(facts, observability.QueryTruncationFacts{Source: cut.SourceSemantics, Dimension: cut.Dimension})
		}
	}
	return facts
}

// providerRangeFacts projects every primary physical query's range, one
// entry each with its digest: a Query Group's primaries can differ, by an
// expression's clauses or by Plans of different windows, and each asks its
// own range.
func providerRangeFacts(completion execution.QueryExecutionCompletion) []observability.QueryRangeFacts {
	var facts []observability.QueryRangeFacts
	for _, item := range completion.PhysicalQueries {
		if item.Range != nil && item.Range.Primary {
			facts = append(facts, observability.QueryRangeFacts{Digest: string(item.PhysicalQuery),
				AskedSeconds: item.Range.AskedSeconds, AcceptedSeconds: item.Range.AcceptedSeconds})
		}
	}
	return facts
}

// providerUnavailableFacts projects every UNAVAILABLE physical query onto
// where its reason code came from: named by an attempt, guessed because no
// attempt named one, or produced with no attempt at all. One entry per
// physical query for the same reason providerStatusFacts takes every one: this
// feeds a counter, and a Query Group with several Plans that reported only its
// first would undercount by however many Plans share the group.
//
// This is the reading that tells "the provider was unavailable" apart from
// "nothing was ever sent": hundreds of objects held out of detection under
// QUERY_UNAVAILABLE with no HTTP error and no timeout anywhere could not be
// told apart from outside before it existed.
func providerUnavailableFacts(completion execution.QueryExecutionCompletion) []observability.QueryUnavailableFacts {
	var facts []observability.QueryUnavailableFacts
	for _, item := range completion.PhysicalQueries {
		if item.Completeness != execution.CompletenessUnavailable {
			continue
		}
		_, attribution := execution.AttributeUnavailable(item.RouteFacts, execution.ReasonCode(contract.ReasonQueryUnavailable))
		status := 0
		if attempt := lastFailedAttempt(item.RouteFacts); attempt != nil {
			status = attempt.HTTPStatus
		}
		facts = append(facts, observability.QueryUnavailableFacts{Attribution: string(attribution), HTTPClass: observability.QueryHTTPClassOf(status)})
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
		// The completion's reason and this failure name the same word, so a
		// query that was never sent is not filed under the backend by either.
		code, from := physicalFailureReason(item.RouteFacts)
		facts := &observability.QueryFailureFacts{
			Stage:    observability.QueryFailureStageProvider,
			Category: physicalFailureCategory(item.RouteFacts),
			Code:     string(execution.AttributedReason(code, from)),
		}
		if attempt := lastFailedAttempt(item.RouteFacts); attempt != nil {
			facts.Detail = attempt.Detail
			facts.HTTPStatus = attempt.HTTPStatus
			facts.Timing = queryTimingOf(attempt.Timing)
			if r := attempt.Rejection; r != nil {
				facts.Rejection = &observability.QueryRejection{Class: r.Class, TraceID: r.TraceID, Excerpt: r.Excerpt}
			}
			facts.Request = attempt.Request.Observed()
			facts.Planned = attempt.Planned.Observed()
		}
		return facts
	}
	return nil
}

// lastFailedAttempt is the attempt a failure reports: the last one that
// failed or named a reason. Its detail, status and timing are read together,
// so a replayed query reports the attempt it ended on.
func lastFailedAttempt(route execution.ProviderRouteFacts) *execution.RouteAttemptFact {
	for index := len(route.Attempts) - 1; index >= 0; index-- {
		if attempt := &route.Attempts[index]; attempt.Result == execution.RouteAttemptFailed || attempt.ReasonCode != "" {
			return attempt
		}
	}
	return nil
}

// queryLineBudget is the round's budget as a query line carries it: without
// the failed query's facts, which the line's own failure facts already say.
func queryLineBudget(ctx context.Context) *observability.QueryBudgetFacts {
	budget := observability.BudgetCaptureFromContext(ctx).Facts()
	if budget != nil {
		budget.FailedRequest, budget.FailedPlanned = nil, nil
	}
	return budget
}

// queryTimingOf is a failed attempt's timing as its failure reports it; nil
// for an attempt that did not measure it.
func queryTimingOf(timing *execution.AttemptTiming) *observability.QueryTiming {
	if timing == nil {
		return nil
	}
	return &observability.QueryTiming{SettleMillis: timing.SettleMillis, StartLateMillis: timing.StartLateMillis,
		BudgetMillis: timing.BudgetMillis, ElapsedMillis: timing.ElapsedMillis, LocalMillis: timing.LocalMillis}
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

// backendDidNotAnswer says a query timed out waiting on the backend: its
// last failed attempt went out, ran out its deadline with no answer or with
// the answer stopping partway (QUERY_TIMEOUT on a transport or body detail),
// and spent more of its window waiting on the backend than alarmd spent
// before sending it and on what had arrived (execution.AttemptTiming). Such a
// timeout is evidence for the degraded pool like a status the backend sent:
// the same query times out again on the same backend, and each try holds a
// query permit to its deadline, the costliest failure there is.
//
// A query begun late -- most of its window gone waiting for a permit or
// behind alarmd's own work -- is not: a burst of this process's own load
// would otherwise put healthy Query Groups in the pool. Neither is a timeout
// that measured no timing, nor alarmd's own delivery running out the
// deadline (a delivery detail), nor any transport failure other than a
// timeout.
func backendDidNotAnswer(routes execution.ProviderRouteFacts) bool {
	for index := len(routes.Attempts) - 1; index >= 0; index-- {
		attempt := routes.Attempts[index]
		if attempt.Result != execution.RouteAttemptFailed && attempt.ReasonCode == "" {
			continue
		}
		if attempt.ReasonCode != execution.ReasonCode(contract.ReasonQueryTimeout) || attempt.Timing == nil {
			return false
		}
		switch attempt.Detail {
		case execution.TransportRouteDetail(execution.TransportFailureTimeout), execution.BodyRouteDetail(execution.TransportFailureTimeout):
		default:
			return false
		}
		timing := attempt.Timing
		return timing.ElapsedMillis-timing.LocalMillis > timing.StartLateMillis+timing.LocalMillis
	}
	return false
}

// timedOutSentLate reports a query whose last failed attempt timed out with
// no answer, or with the answer stopping partway, but was sent so late that
// backendDidNotAnswer does not count it against the backend. It is not
// evidence for entering the degraded pool; a Query Group already in it reads
// it as its deciding round's timeout (QueryUnknownReason).
func timedOutSentLate(routes execution.ProviderRouteFacts) bool {
	for index := len(routes.Attempts) - 1; index >= 0; index-- {
		attempt := routes.Attempts[index]
		if attempt.Result != execution.RouteAttemptFailed && attempt.ReasonCode == "" {
			continue
		}
		if attempt.ReasonCode != execution.ReasonCode(contract.ReasonQueryTimeout) || attempt.Timing == nil {
			return false
		}
		switch attempt.Detail {
		case execution.TransportRouteDetail(execution.TransportFailureTimeout), execution.BodyRouteDetail(execution.TransportFailureTimeout):
		default:
			return false
		}
		timing := attempt.Timing
		return timing.ElapsedMillis-timing.LocalMillis <= timing.StartLateMillis+timing.LocalMillis
	}
	return false
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
