package observability

// QueryFailureFacts is log-only: no raw error, query, URL or dimension values.
// Stage names the actual Coordinator boundary, not an inferred root cause.
// Code is a stable machine code drawn from a bounded grammar (UQ status codes,
// worker contract codes, capacity budgets); anything outside the grammar
// collapses to OTHER. Detail is an optional bounded provider detail such as
// "http_status=503" or "transport=connection_refused".
type QueryFailureFacts struct {
	Stage, Category, Code, Detail string
	// Timing is a failed provider query read against its budget; nil where
	// the attempt did not measure it. See QueryTiming.
	Timing *QueryTiming
}

// QueryTiming splits a Slot query's budget - from its Slot's evaluation
// time, or a recovery's arrival, to its query deadline - at the moment its
// window could be read and the moment its request went out: the settling
// wait the Plan puts first by design, the part lost after it before the
// query began, what was left to the deadline then, and what the request
// used, of which local is what alarmd spent on what had arrived of the
// answer - the rest was waiting on the backend. The first three add up to
// the whole budget. It is what tells a timeout apart: a backend that did not
// answer in time uses its whole budget having begun on time, little of it
// local; alarmd's own delivery that did not keep up spends most of it local;
// a query begun late had little left; a budget short to begin with is small
// in all three added together. See execution.AttemptTiming.
type QueryTiming struct {
	SettleMillis    int64 `json:"settle_ms"`
	StartLateMillis int64 `json:"start_late_ms"`
	BudgetMillis    int64 `json:"budget_ms"`
	ElapsedMillis   int64 `json:"elapsed_ms"`
	LocalMillis     int64 `json:"local_ms"`
}

const (
	QueryFailureStageExecute        = "execute"
	QueryFailureStageStreamComplete = "stream_complete"
	QueryFailureStageProvider       = "provider"
	QueryFailureStageOther          = "other"
	// QueryFailureStageOutput is the write of the round's events, after the
	// evaluation: a failure there is the sink's, and the row reads it by the
	// error's own words rather than by a query failure's detail grammar.
	QueryFailureStageOutput = "output"

	QueryFailureCategorySourceBackend      = "source_backend"
	QueryFailureCategorySeriesIdentity     = "series_identity"
	QueryFailureCategoryBudget             = "budget"
	QueryFailureCategoryCompletionContract = "completion_contract"
	QueryFailureCategoryNamedInput         = "named_input"
	QueryFailureCategoryProviderTransport  = "provider_transport"
	// QueryFailureCategoryAdmission names a physical query that never reached
	// the provider because its permit wait ended at the frozen query deadline.
	QueryFailureCategoryAdmission = "admission"
	// QueryFailureCategoryEvaluation names a Slot that failed at
	// stream_complete because a series evaluation errored or produced a result
	// the result contract rejected.
	QueryFailureCategoryEvaluation = "evaluation"
	QueryFailureCategoryOther      = "other"
	// QueryFailureCategoryOutput is a failure writing the round's events.
	QueryFailureCategoryOutput = "output"

	QueryFailureCodeOther = "OTHER"

	maxQueryFailureCodeLength   = 64
	maxQueryFailureDetailLength = 96
)

// QueryFailureStages and QueryFailureCategories are the closed vocabularies a
// normalized failure's Stage and Category come from; a metric keyed on them
// has a bounded series count.
var (
	QueryFailureStages = []string{
		QueryFailureStageExecute, QueryFailureStageStreamComplete, QueryFailureStageProvider, QueryFailureStageOther,
		QueryFailureStageOutput,
	}
	QueryFailureCategories = []string{
		QueryFailureCategorySourceBackend, QueryFailureCategorySeriesIdentity, QueryFailureCategoryBudget,
		QueryFailureCategoryCompletionContract, QueryFailureCategoryNamedInput, QueryFailureCategoryProviderTransport,
		QueryFailureCategoryAdmission, QueryFailureCategoryEvaluation, QueryFailureCategoryOther,
		QueryFailureCategoryOutput,
	}
)

// ValidQueryFailureCode reports whether code matches ^[A-Z][A-Z0-9_]{0,63}$.
// The grammar keeps codes to bounded enums (UQ status codes, contract codes)
// and rejects URLs, messages and other free text.
func ValidQueryFailureCode(code string) bool {
	if code == "" || len(code) > maxQueryFailureCodeLength {
		return false
	}
	for index := 0; index < len(code); index++ {
		char := code[index]
		switch {
		case char >= 'A' && char <= 'Z':
		case index > 0 && (char >= '0' && char <= '9' || char == '_'):
		default:
			return false
		}
	}
	return true
}

// CapacityBudgetFailureCode is the failure code a rejection by one budget
// carries.
//
// It exists because the budget's own value is a metric label -- lower case,
// chosen to read well beside other labels -- and the failure code grammar is
// upper case. The budget was being passed straight through as the code, so
// every budget rejection published a code no reader could parse: fleet
// normalised it away and the page was left with the free text, which is rate
// limited and gone first. The two spellings are the same fact, and this is the
// one place that says so.
func CapacityBudgetFailureCode(budget CapacityBudget) string {
	switch NormalizeCapacityBudget(budget) {
	case CapacityBudgetSeries:
		return "BUDGET_SERIES"
	case CapacityBudgetRetainedBytes:
		return "BUDGET_RETAINED_BYTES"
	case CapacityBudgetStateMutations:
		return "BUDGET_STATE_MUTATIONS"
	case CapacityBudgetEvents:
		return "BUDGET_EVENTS"
	case CapacityBudgetGapMutations:
		return "BUDGET_GAP_MUTATIONS"
	default:
		return "BUDGET_OTHER"
	}
}

// NormalizeQueryFailureCode returns code when it matches the grammar and OTHER
// otherwise.
func NormalizeQueryFailureCode(code string) string {
	if ValidQueryFailureCode(code) {
		return code
	}
	return QueryFailureCodeOther
}

func validQueryFailureDetail(detail string) bool {
	if detail == "" || len(detail) > maxQueryFailureDetailLength {
		return false
	}
	for index := 0; index < len(detail); index++ {
		char := detail[index]
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
		case char == '_', char == '=', char == '.', char == '-':
		default:
			return false
		}
	}
	return true
}

func normalizeQueryFailure(component Component, stage Stage, input *QueryFailureFacts) *QueryFailureFacts {
	if component != ComponentAccess || stage != StageQueryCompleted || input == nil {
		return nil
	}
	f := *input
	switch f.Stage {
	case QueryFailureStageExecute, QueryFailureStageStreamComplete, QueryFailureStageProvider:
	default:
		f.Stage = QueryFailureStageOther
	}
	switch f.Category {
	// Budget goes through the same grammar as every other category. It used to
	// have an exemption: a code that spelled a known budget was kept as it was,
	// which is how every budget rejection came to publish a lower-case label as
	// its code without anything noticing. The exemption was what made it
	// invisible -- OTHER on that path would have said at once that the code was
	// not a code. Publishers map the budget to its code themselves now, with
	// CapacityBudgetFailureCode.
	case QueryFailureCategoryBudget, QueryFailureCategorySourceBackend, QueryFailureCategorySeriesIdentity,
		QueryFailureCategoryCompletionContract,
		QueryFailureCategoryNamedInput, QueryFailureCategoryProviderTransport, QueryFailureCategoryAdmission,
		QueryFailureCategoryEvaluation:
		f.Code = NormalizeQueryFailureCode(f.Code)
	default:
		f.Category = QueryFailureCategoryOther
		f.Code = QueryFailureCodeOther
	}
	if !validQueryFailureDetail(f.Detail) {
		f.Detail = ""
	}
	return &f
}
