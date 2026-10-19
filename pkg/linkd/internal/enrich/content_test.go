package enrich

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store/storetest"
)

type contentTestBuilder func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (string, error)

func (f contentTestBuilder) BuildContent(ctx context.Context, e domain.Event, v domain.EventEvaluation, a domain.Alert) (string, error) {
	return f(ctx, e, v, a)
}

func contentTestOpening() (domain.Event, domain.EventEvaluation, domain.Alert) {
	event := storetest.Event("tenant", "event", "group", "warning")
	event.Content = "original source"
	opening := domain.Alert{BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID, EventSourceVersion: event.EventSourceVersion, TriggerEventID: event.EventID, Fingerprint: event.Fingerprint, Severity: "warning"}
	return event, event.Evaluations[0], opening
}

func TestContentFallbackDoesNotAcceptInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*domain.Event, *domain.EventEvaluation, *domain.Alert)
	}{
		{"invalid tenant", func(e *domain.Event, _ *domain.EventEvaluation, _ *domain.Alert) { e.BKTenantID = "" }},
		{"cross tenant", func(_ *domain.Event, _ *domain.EventEvaluation, a *domain.Alert) { a.BKTenantID = "other" }},
		{"cross source", func(_ *domain.Event, _ *domain.EventEvaluation, a *domain.Alert) { a.EventSourceID = "other" }},
		{"cross version", func(_ *domain.Event, _ *domain.EventEvaluation, a *domain.Alert) { a.EventSourceVersion++ }},
		{"cross opening event", func(_ *domain.Event, _ *domain.EventEvaluation, a *domain.Alert) { a.TriggerEventID = "other" }},
		{"cross fingerprint", func(_ *domain.Event, _ *domain.EventEvaluation, a *domain.Alert) { a.Fingerprint = "other" }},
		{"unselected evaluation", func(e *domain.Event, v *domain.EventEvaluation, _ *domain.Alert) {
			e.Evaluations[0].ActionReason = "other"
			v.ActionReason = "unselected"
		}},
		{"resolved evaluation", func(e *domain.Event, v *domain.EventEvaluation, _ *domain.Alert) {
			v.Action = domain.EventActionResolved
			e.Evaluations[0] = *v
		}},

		{"oversized source content", func(e *domain.Event, _ *domain.EventEvaluation, _ *domain.Alert) {
			e.Content = strings.Repeat("x", (1<<20)+1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event, v, opening := contentTestOpening()
			tc.change(&event, &v, &opening)
			calls := 0
			builder := contentTestBuilder(func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (string, error) {
				calls++
				return "", errors.New("should not reach builder")
			})
			if _, err := BuildAlertContent(t.Context(), builder, event, v, opening); err == nil || calls != 0 {
				t.Fatalf("error=%v calls=%d", err, calls)
			}
		})
	}
}

func TestContentFallbackHonorsOperationContext(t *testing.T) {
	event, v, opening := contentTestOpening()
	for _, failure := range []error{context.DeadlineExceeded, context.Canceled} {
		// 文案子操作的超时不取消整个事件，仍应正常生成告警。
		result, err := BuildAlertContent(t.Context(), contentTestBuilder(func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (string, error) {
			return "", failure
		}), event, v, opening)
		if err != nil || result.Content != event.Content || result.Diagnostic == nil {
			t.Fatalf("result=%+v error=%v", result, err)
		}
	}
	for _, mode := range []string{"nil", "cancelled", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			var ctx context.Context
			switch mode {
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(t.Context())
				cancel()
			case "deadline":
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(t.Context(), time.Unix(0, 0))
				defer cancel()
			}
			calls := 0
			_, err := BuildAlertContent(ctx, contentTestBuilder(func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (string, error) {
				calls++
				return "generated", nil
			}), event, v, opening)
			if err == nil || calls != 0 {
				t.Fatalf("error=%v calls=%d", err, calls)
			}
		})
	}
}

type invalidDiagnosticError struct {
	code   string
	panics bool
}

func (e invalidDiagnosticError) Error() string { panic("error text must never be evaluated") }
func (e invalidDiagnosticError) PermanentContentFailure() string {
	if e.panics {
		panic("secret")
	}
	return e.code
}

func TestContentDiagnosticCannotFailOrLeakErrorText(t *testing.T) {
	event, v, opening := contentTestOpening()
	for _, failure := range []invalidDiagnosticError{{code: "secret connection details"}, {code: strings.Repeat("x", 129)}, {panics: true}} {
		result, err := BuildAlertContent(t.Context(), contentTestBuilder(func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (string, error) {
			return "", failure
		}), event, v, opening)
		if err != nil || result.Content != event.Content || result.Diagnostic == nil || result.Diagnostic.ReasonCode != "builder_error" {
			t.Fatalf("result=%+v error=%v", result, err)
		}
	}
}

func TestInvalidSourceEncodingUsesDefaultWithoutChangingEvent(t *testing.T) {
	event, v, opening := contentTestOpening()
	event.Content = string([]byte{0xff})
	result, err := BuildAlertContent(t.Context(), contentTestBuilder(func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (string, error) {
		return "", errors.New("failed")
	}), event, v, opening)
	if err != nil || result.Content != DefaultAlertContent || result.Diagnostic == nil || result.Diagnostic.ContentMode != "default_fallback" || event.Content != string([]byte{0xff}) {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}
