package uq

import "fmt"

// These errors retain existing error text; only QueryFailure is safe to log.
// They do not change provider completeness, retry or cancellation behavior.
//
// UQ status codes are a bounded enum, so a code that matches the failure code
// grammar (^[A-Z][A-Z0-9_]{0,63}$) is kept verbatim as the diagnostic code;
// anything else (free text, URLs) collapses to OTHER.
type backendStatusError struct{ code string }

func (e *backendStatusError) Error() string {
	return fmt.Sprintf("alarmd access uq: query status %s", e.code)
}
func (e *backendStatusError) QueryFailure() (string, string) {
	return "source_backend", boundedFailureCode(e.code)
}

type identityFieldMissingError struct{ field string }

func (e *identityFieldMissingError) Error() string {
	return fmt.Sprintf("alarmd access uq: identity field %s is missing", e.field)
}
func (*identityFieldMissingError) QueryFailure() (string, string) {
	return "series_identity", "IDENTITY_FIELD_MISSING"
}

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

const maxFailureCodeLength = 64

func boundedFailureCode(code string) string {
	if code == "" || len(code) > maxFailureCodeLength {
		return "OTHER"
	}
	for index := 0; index < len(code); index++ {
		char := code[index]
		switch {
		case char >= 'A' && char <= 'Z':
		case index > 0 && (char >= '0' && char <= '9' || char == '_'):
		default:
			return "OTHER"
		}
	}
	return code
}
