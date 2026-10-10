package observability

// QueryFailureFacts is log-only: no raw error, query, URL or dimension values.
// Stage names the actual Coordinator boundary, not an inferred root cause.
// Code is a stable machine code drawn from a bounded grammar (UQ status codes,
// worker contract codes, capacity budgets); anything outside the grammar
// collapses to OTHER. Detail is an optional bounded provider detail such as
// "http_status=503" or "transport=connection_refused".
type QueryFailureFacts struct{ Stage, Category, Code, Detail string }

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
