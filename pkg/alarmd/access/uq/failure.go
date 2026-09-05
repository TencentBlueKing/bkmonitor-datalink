package uq

import "fmt"

// These errors retain existing error text; only QueryFailure is safe to log.
// They do not change provider completeness, retry or cancellation behavior.
type backendStatusError struct{ code string }

func (e *backendStatusError) Error() string {
	return fmt.Sprintf("alarmd access uq: query status %s", e.code)
}
func (e *backendStatusError) QueryFailure() (string, string) {
	code := "OTHER"
	if e.code == "SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS" {
		code = e.code
	}
	return "source_backend", code
}

type identityFieldMissingError struct{ field string }

func (e *identityFieldMissingError) Error() string {
	return fmt.Sprintf("alarmd access uq: identity field %s is missing", e.field)
}
func (*identityFieldMissingError) QueryFailure() (string, string) {
	return "series_identity", "IDENTITY_FIELD_MISSING"
}
