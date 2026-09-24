package uq

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
type responseLimitError struct{ code string }

func (e *responseLimitError) Error() string {
	return "alarmd access uq: " + e.code
}
func (e *responseLimitError) QueryFailure() (string, string) {
	return "budget", e.code
}
