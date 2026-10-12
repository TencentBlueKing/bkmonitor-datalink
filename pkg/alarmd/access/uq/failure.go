package uq

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"

// These errors retain existing error text; only QueryFailure is safe to log.
// They do not change provider completeness, retry or cancellation behavior.
//
// Deterministic UQ status codes and absent identity dimensions are no longer
// Go errors: a status code completes the physical query as UNAVAILABLE with a
// bounded "response=status_<code>" route detail (see decode), and an absent
// identity dimension is bound to JSON null like Python binds it to None (see
// normalizeSeries).

// responseLimitError is a client-side response budget violation. It keeps the
// historical sentinel text so errors.Is against the exported variables keeps
// working, and exposes the budget name as a stable diagnostic code.
type responseLimitError struct {
	code string
	// class is the limit's name in the response detail grammar
	// (execution.ResponseFailureLimit*).
	class string
}

func (e *responseLimitError) Error() string {
	return "alarmd access uq: " + e.code
}
func (e *responseLimitError) QueryFailure() (string, string) {
	return "budget", e.code
}

// bodyFailureError is a query whose answer began and whose body did not
// arrive in full (see Client.bodyFailure). It keeps the error's own text and
// says what the failure was in the bounded grammar a failed attempt uses: a
// provider transport failure with its "body=<class>" detail, or a deadline
// that passed on alarmd's side, "delivery=timeout"; and for a Slot's query
// its timing, whose local part says how much of the time went to alarmd's
// own delivery.
type bodyFailureError struct {
	err      error
	category string
	code     string
	detail   string
	timing   *execution.AttemptTiming
}

func (e *bodyFailureError) Error() string                                { return e.err.Error() }
func (e *bodyFailureError) Unwrap() error                                { return e.err }
func (e *bodyFailureError) QueryFailure() (string, string)               { return e.category, e.code }
func (e *bodyFailureError) QueryFailureDetail() string                   { return e.detail }
func (e *bodyFailureError) QueryFailureTiming() *execution.AttemptTiming { return e.timing }
