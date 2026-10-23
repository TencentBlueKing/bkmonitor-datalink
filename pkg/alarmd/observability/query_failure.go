package observability

import "strings"

// QueryFailureFacts is log-only: no raw error, query, URL or dimension values.
// Stage names the actual Coordinator boundary, not an inferred root cause.
// Code is a stable machine code drawn from a bounded grammar (UQ status codes,
// worker contract codes, capacity budgets); anything outside the grammar
// collapses to OTHER. Detail is an optional bounded provider detail such as
// "http_status=503" or "transport=connection_refused".
type QueryFailureFacts struct {
	Stage, Category, Code, Detail string
	// HTTPStatus is the backend's non-success HTTP status, 100 to 599, zero
	// where the failure got no HTTP answer. Like Timing, it is the last
	// failed attempt's when a query was attempted more than once.
	HTTPStatus int
	// Timing is a failed provider query read against its budget; nil where
	// the attempt did not measure it. See QueryTiming.
	Timing *QueryTiming
	// Rejection is what the provider said when it refused the query; nil
	// where the failure got no non-success answer. Its class and trace id
	// are log fields; its excerpt is kept with the Query Group's last
	// failure for an operator to read and is never written to a log line.
	Rejection *QueryRejection
	// Volume is how much of the answer had been read and handed on when the
	// query failed; nil where the failure came before any answer.
	Volume *QueryVolume
	// Request is which deadline governed the failed request and which had
	// expired (execution.RequestFacts); Planned is the physical query's own
	// plan (execution.PlannedQueryFacts). Nil where not known.
	Request *QueryRequestFacts
	Planned *QueryPlannedFacts
}

// QueryRequestFacts is execution.RequestFacts as a line renders it.
type QueryRequestFacts struct {
	DeadlineUnixMilli  int64
	DeadlineLeftAtDoMs int64
	DeadlineSource     string
	Expired            string
	ErrorChain         string
	ErrorTypes         []string
	// TraceID is the trace id the request was sent with (32 lower-case hex);
	// it renders as failure_provider_trace_id.
	TraceID string
	// AnswerStatus is the HTTP status the provider answered with, 200
	// included (provider_http_status); AnsweredUnixMilli when its headers
	// arrived (answered_at_ms) and CompletedUnixMilli when the attempt ended
	// (completed_at_ms). Zero where not known. The line's own time is when
	// it was written, after its round's work, and is neither.
	AnswerStatus       int
	AnsweredUnixMilli  int64
	CompletedUnixMilli int64
	// TimeoutSource is which branch filed a timeout, one of
	// QueryTimeoutSources (timeout_source); empty otherwise.
	TimeoutSource string
	// Excerpt is the provider's own message from an answer's body,
	// sanitized and at most MaxRejectionExcerptBytes. It is never written to
	// a log line or a row: like a refusal's excerpt, it is kept on the
	// replica with the Query Group's last refusal.
	Excerpt string
}

// Which branch filed a query's timeout. A timeout met while its request's
// own deadline was still ahead and one met at the deadline are different
// failures; the reason code alone is the same word for both.
const (
	// QueryTimeoutSourceDeadlinePassed: the request's deadline had passed
	// when the request or a read of its body failed.
	QueryTimeoutSourceDeadlinePassed = "deadline_passed"
	// QueryTimeoutSourceTransportClass: the request got no answer and the
	// transport's error reads as a timeout with its deadline still ahead.
	QueryTimeoutSourceTransportClass = "transport_class_timeout"
	// QueryTimeoutSourceBodyReadClass: the answer began and a read of its
	// body failed with an error that reads as a timeout, its deadline still
	// ahead (execution.ClassifyTransportFailure).
	QueryTimeoutSourceBodyReadClass = "body_read_class_timeout"
	// QueryTimeoutSourceDelivery: the deadline passed while alarmd was
	// decoding or delivering what had arrived (QUERY_DELIVERY_TIMEOUT).
	QueryTimeoutSourceDelivery = "delivery_deadline"
	// QueryTimeoutSourceRoundDeadline: the round ended the query at its own
	// deadline with a bare cancellation or expiry the provider did not name.
	QueryTimeoutSourceRoundDeadline = "round_deadline"
	// QueryTimeoutSourceProviderStatus: the provider answered with a status
	// that says it ran out of time (504, 408), its own backend's deadline
	// rather than this request's.
	QueryTimeoutSourceProviderStatus = "provider_status"
)

// QueryTimeoutSources is the closed list a rendered timeout_source comes from.
var QueryTimeoutSources = []string{QueryTimeoutSourceDeadlinePassed, QueryTimeoutSourceTransportClass,
	QueryTimeoutSourceBodyReadClass, QueryTimeoutSourceDelivery, QueryTimeoutSourceRoundDeadline,
	QueryTimeoutSourceProviderStatus}

func validQueryTimeoutSource(source string) bool {
	for _, known := range QueryTimeoutSources {
		if source == known {
			return true
		}
	}
	return false
}

// normalizeQueryRequest keeps a request's facts within their grammar: an
// unknown timeout source, a status outside 100 to 599 and an excerpt past
// its bound are dropped.
func normalizeQueryRequest(input *QueryRequestFacts) *QueryRequestFacts {
	if input == nil {
		return nil
	}
	r := *input
	if !validQueryTimeoutSource(r.TimeoutSource) {
		r.TimeoutSource = ""
	}
	if r.AnswerStatus < 100 || r.AnswerStatus > 599 {
		r.AnswerStatus = 0
	}
	if len(r.Excerpt) > MaxRejectionExcerptBytes {
		r.Excerpt = ""
	}
	return &r
}

// QueryPlannedFacts is execution.PlannedQueryFacts as a line renders it.
type QueryPlannedFacts struct {
	Role                        string
	Dataset                     string
	DeadlineUnixMilli           int64
	FrozenDeadlineUnixMilli     int64
	CompletionDeadlineUnixMilli int64
	FellBack                    bool
}

// QueryVolume is how far a failed query's answer got: the bytes of the
// answer read, and the series handed to the round's sink. A query whose
// deadline passed while alarmd was still decoding what had arrived says by
// these how much it had to decode.
type QueryVolume struct {
	BytesRead       uint64
	SeriesDelivered uint64
}

// QueryRejection is a provider's non-success answer as alarmd keeps it: a
// bounded class (QUERY_FAILURE code grammar), the provider's trace id (32
// lower-case hex), and the sanitized start of its body, at most
// MaxRejectionExcerptBytes.
type QueryRejection struct {
	Class   string `json:"class"`
	TraceID string `json:"trace_id,omitempty"`
	Excerpt string `json:"excerpt,omitempty"`
}

// MaxRejectionExcerptBytes bounds a kept excerpt; the provider client
// already cuts it shorter (with its "..." marker).
const MaxRejectionExcerptBytes = 400

func normalizeQueryRejection(input *QueryRejection) *QueryRejection {
	if input == nil {
		return nil
	}
	r := *input
	if !ValidQueryFailureCode(r.Class) {
		r.Class = QueryFailureCodeOther
	}
	if len(r.TraceID) != 32 || strings.Trim(r.TraceID, "0123456789abcdef") != "" {
		r.TraceID = ""
	}
	if len(r.Excerpt) > MaxRejectionExcerptBytes {
		r.Excerpt = ""
	}
	return &r
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
	// QueryFailureCategoryDelivery names a query whose deadline passed while
	// alarmd was decoding or delivering an answer that had begun to arrive:
	// the time ran out on alarmd's side, not the backend's, so it is neither
	// the provider's transport nor a defect without a name.
	QueryFailureCategoryDelivery = "delivery"
	// QueryFailureCodeDeliveryTimeout is the delivery category's one code.
	QueryFailureCodeDeliveryTimeout = "QUERY_DELIVERY_TIMEOUT"
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
		QueryFailureCategoryOutput, QueryFailureCategoryDelivery,
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
		QueryFailureCategoryEvaluation, QueryFailureCategoryDelivery:
		f.Code = NormalizeQueryFailureCode(f.Code)
	default:
		f.Category = QueryFailureCategoryOther
		f.Code = QueryFailureCodeOther
	}
	if !validQueryFailureDetail(f.Detail) {
		f.Detail = ""
	}
	if f.HTTPStatus < 100 || f.HTTPStatus > 599 {
		f.HTTPStatus = 0
	}
	f.Rejection = normalizeQueryRejection(f.Rejection)
	f.Request = normalizeQueryRequest(f.Request)
	return &f
}
