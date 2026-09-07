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

	QueryFailureCategorySourceBackend      = "source_backend"
	QueryFailureCategorySeriesIdentity     = "series_identity"
	QueryFailureCategoryBudget             = "budget"
	QueryFailureCategoryCompletionContract = "completion_contract"
	QueryFailureCategoryNamedInput         = "named_input"
	QueryFailureCategoryProviderTransport  = "provider_transport"
	QueryFailureCategoryOther              = "other"

	QueryFailureCodeOther = "OTHER"

	maxQueryFailureCodeLength   = 64
	maxQueryFailureDetailLength = 96
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
	case QueryFailureCategoryBudget:
		if budget := NormalizeCapacityBudget(CapacityBudget(f.Code)); budget != "" && budget != CapacityBudgetOther {
			f.Code = string(budget)
		} else {
			f.Code = NormalizeQueryFailureCode(f.Code)
		}
	case QueryFailureCategorySourceBackend, QueryFailureCategorySeriesIdentity, QueryFailureCategoryCompletionContract,
		QueryFailureCategoryNamedInput, QueryFailureCategoryProviderTransport:
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
