package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func newQueryFailureTestObserver(output *bytes.Buffer) *LoggingObserver {
	l, _ := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 10})
	p, _ := NewBoundedLogPolicy(l)
	return NewLoggingObserver(New("alarmd", output), p)
}

func TestQueryFailureLogRejectsSensitiveValues(t *testing.T) {
	secret := "https://user:secret@example.test/?token=secret"
	for _, facts := range []QueryFailureFacts{
		{Stage: "execute", Category: "source_backend", Code: "SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS"},
		{Stage: "stream_complete", Category: "budget", Code: "retained_bytes"},
		{Stage: secret, Category: secret, Code: secret, Detail: secret},
	} {
		var output bytes.Buffer
		newQueryFailureTestObserver(&output).Observe(context.Background(), Observation{Component: ComponentAccess, Stage: StageQueryCompleted, Result: ResultFailed, Err: errors.New("query " + secret + " failed"), QueryFailure: &facts})
		if strings.Contains(output.String(), "secret") || strings.Contains(output.String(), "https://") {
			t.Fatalf("sensitive log: %s", output.String())
		}
		var event map[string]any
		if err := json.Unmarshal(output.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		if event["error"] != "query <url> failed" {
			t.Fatalf("error text was not sanitized: %v", event)
		}
		if facts.Stage == secret {
			if event["failure_stage"] != "other" || event["failure_category"] != "other" || event["failure_code"] != "OTHER" || event["failure_detail"] != nil {
				t.Fatalf("unbounded facts: %v", event)
			}
		} else if event["failure_stage"] != facts.Stage || event["failure_category"] != facts.Category || event["failure_code"] != facts.Code {
			t.Fatalf("lost facts: %v", event)
		}
	}
}

func TestQueryFailureCodesFollowBoundedGrammarInsteadOfWhitelist(t *testing.T) {
	for _, test := range []struct {
		name  string
		input QueryFailureFacts
		want  QueryFailureFacts
	}{
		{"known UQ code", QueryFailureFacts{Stage: "execute", Category: "source_backend", Code: "SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS"}, QueryFailureFacts{Stage: "execute", Category: "source_backend", Code: "SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS"}},
		{"new UQ code kept", QueryFailureFacts{Stage: "execute", Category: "source_backend", Code: "QUERY_TS_SOME_NEW_STATUS"}, QueryFailureFacts{Stage: "execute", Category: "source_backend", Code: "QUERY_TS_SOME_NEW_STATUS"}},
		{"lowercase collapses", QueryFailureFacts{Stage: "execute", Category: "source_backend", Code: "not a code"}, QueryFailureFacts{Stage: "execute", Category: "source_backend", Code: "OTHER"}},
		{"leading digit collapses", QueryFailureFacts{Stage: "execute", Category: "series_identity", Code: "1IDENTITY"}, QueryFailureFacts{Stage: "execute", Category: "series_identity", Code: "OTHER"}},
		{"worker contract code", QueryFailureFacts{Stage: "stream_complete", Category: "completion_contract", Code: "COMPLETION_BINDING_MISMATCH"}, QueryFailureFacts{Stage: "stream_complete", Category: "completion_contract", Code: "COMPLETION_BINDING_MISMATCH"}},
		{"named input code", QueryFailureFacts{Stage: "stream_complete", Category: "named_input", Code: "COMPLETION_ONLY_REQUIREMENT_MISSING"}, QueryFailureFacts{Stage: "stream_complete", Category: "named_input", Code: "COMPLETION_ONLY_REQUIREMENT_MISSING"}},
		{"capacity budget", QueryFailureFacts{Stage: "stream_complete", Category: "budget", Code: "retained_bytes"}, QueryFailureFacts{Stage: "stream_complete", Category: "budget", Code: "retained_bytes"}},
		{"response budget", QueryFailureFacts{Stage: "execute", Category: "budget", Code: "RESPONSE_BYTES_EXCEEDED"}, QueryFailureFacts{Stage: "execute", Category: "budget", Code: "RESPONSE_BYTES_EXCEEDED"}},
		{"unknown budget", QueryFailureFacts{Stage: "execute", Category: "budget", Code: "free text"}, QueryFailureFacts{Stage: "execute", Category: "budget", Code: "OTHER"}},
		{"unknown category", QueryFailureFacts{Stage: "execute", Category: "mystery", Code: "VALID_CODE"}, QueryFailureFacts{Stage: "execute", Category: "other", Code: "OTHER"}},
		{"unknown stage", QueryFailureFacts{Stage: "elsewhere", Category: "source_backend", Code: "X"}, QueryFailureFacts{Stage: "other", Category: "source_backend", Code: "X"}},
		{"provider detail kept", QueryFailureFacts{Stage: "provider", Category: "provider_transport", Code: "QUERY_UNAVAILABLE", Detail: "transport=connection_refused"}, QueryFailureFacts{Stage: "provider", Category: "provider_transport", Code: "QUERY_UNAVAILABLE", Detail: "transport=connection_refused"}},
		{"http detail kept", QueryFailureFacts{Stage: "provider", Category: "source_backend", Code: "QUERY_UNAVAILABLE", Detail: "http_status=503"}, QueryFailureFacts{Stage: "provider", Category: "source_backend", Code: "QUERY_UNAVAILABLE", Detail: "http_status=503"}},
		{"free text detail dropped", QueryFailureFacts{Stage: "provider", Category: "source_backend", Code: "QUERY_UNAVAILABLE", Detail: "body: {\"error\":\"table x\"}"}, QueryFailureFacts{Stage: "provider", Category: "source_backend", Code: "QUERY_UNAVAILABLE"}},
		{"long detail dropped", QueryFailureFacts{Stage: "provider", Category: "source_backend", Code: "QUERY_UNAVAILABLE", Detail: strings.Repeat("a", 97)}, QueryFailureFacts{Stage: "provider", Category: "source_backend", Code: "QUERY_UNAVAILABLE"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := test.input
			got := normalizeQueryFailure(ComponentAccess, StageQueryCompleted, &input)
			if got == nil || *got != test.want {
				t.Fatalf("normalizeQueryFailure(%+v) = %+v, want %+v", test.input, got, test.want)
			}
			if input != test.input {
				t.Fatal("normalization mutated the caller's facts")
			}
		})
	}
	if got := normalizeQueryFailure(ComponentDetect, StageDetectCompleted, &QueryFailureFacts{Stage: "execute"}); got != nil {
		t.Fatalf("facts outside the Access query boundary were kept: %+v", got)
	}
}

func TestQueryFailureFactsSurviveWithoutErrorForDegradedProviderResults(t *testing.T) {
	var output bytes.Buffer
	facts := QueryFailureFacts{Stage: "provider", Category: "source_backend", Code: "QUERY_UNAVAILABLE", Detail: "http_status=503"}
	newQueryFailureTestObserver(&output).Observe(context.Background(), Observation{
		Component: ComponentAccess, Stage: StageQueryCompleted, Result: ResultDegraded,
		ReasonCode: "QUERY_UNAVAILABLE", QueryFailure: &facts,
		Trace: TraceFields{QueryGroupKey: "query-group-1"},
	})
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode: %v; log=%s", err, output.String())
	}
	for field, want := range map[string]any{
		"failure_stage": "provider", "failure_category": "source_backend", "failure_code": "QUERY_UNAVAILABLE",
		"failure_detail": "http_status=503", "reason_code": "QUERY_UNAVAILABLE", "query_group_key": "query-group-1",
	} {
		if event[field] != want {
			t.Fatalf("event[%q]=%#v, want %#v; event=%#v", field, event[field], want, event)
		}
	}
	if _, exists := event["error"]; exists {
		t.Fatalf("degraded provider line invented an error: %#v", event)
	}
}

func TestValidQueryFailureCodeGrammar(t *testing.T) {
	for code, want := range map[string]bool{
		"A": true, "OTHER": true, "A_1": true, "QUERY_TS_PARTIAL": true, strings.Repeat("A", 64): true,
		"": false, "a": false, "1A": false, "A-B": false, "A B": false, strings.Repeat("A", 65): false,
		"https://example.test": false,
	} {
		if got := ValidQueryFailureCode(code); got != want {
			t.Fatalf("ValidQueryFailureCode(%q) = %t, want %t", code, got, want)
		}
	}
	if NormalizeQueryFailureCode("bad code") != "OTHER" || NormalizeQueryFailureCode("GOOD_CODE") != "GOOD_CODE" {
		t.Fatal("NormalizeQueryFailureCode did not apply the grammar")
	}
}
