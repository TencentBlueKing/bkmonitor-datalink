package worker

import (
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

type namedCause struct{ code string }

func (c *namedCause) Error() string                  { return "named cause" }
func (c *namedCause) QueryFailure() (string, string) { return "", c.code }

// TestEvaluationFailureKeepsTheCauseName pins that an evaluation error naming
// its own cause is reported under that name rather than under the wrap site.
//
// The code used to be whichever constant the throwing site passed, so every
// cause reaching one site aggregated into one value: a restart window's worth
// of failures all read EVALUATION_FAILED, and what had actually gone wrong
// survived only in the free-text message - the one field that says why, and
// the only one that is rate limited. The category still comes from the stage,
// because where it failed is what the wrap site knows.
func TestEvaluationFailureKeepsTheCauseName(t *testing.T) {
	for _, one := range []struct {
		name     string
		err      error
		wantCode string
	}{
		{name: "cause names itself", err: &namedCause{code: "STATE_RECORD_IDENTITY_CONFLICT"}, wantCode: "STATE_RECORD_IDENTITY_CONFLICT"},
		{name: "cause names itself through a wrap", err: errWrap(&namedCause{code: "STATE_LEVEL_FACT_DISAGREEMENT"}), wantCode: "STATE_LEVEL_FACT_DISAGREEMENT"},
		{name: "plain cause keeps the wrap site name", err: errors.New("plain"), wantCode: codeEvaluationFailed},
		{name: "empty declaration keeps the wrap site name", err: &namedCause{code: ""}, wantCode: codeEvaluationFailed},
	} {
		t.Run(one.name, func(t *testing.T) {
			wrapped := wrapEvaluationError(codeEvaluationFailed, one.err)
			var declared interface{ QueryFailure() (string, string) }
			if !errors.As(wrapped, &declared) {
				t.Fatal("the wrapped evaluation error does not report a query failure")
			}
			category, code := declared.QueryFailure()
			if category != observability.QueryFailureCategoryEvaluation {
				t.Errorf("category = %q, want the evaluation stage", category)
			}
			if code != one.wantCode {
				t.Errorf("code = %q, want %q", code, one.wantCode)
			}
		})
	}
}

func errWrap(err error) error { return errors.Join(err) }
