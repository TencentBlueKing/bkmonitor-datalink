package uq

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestQueryFailureDiagnosticsPreserveDecodeFailure(t *testing.T) {
	for _, tc := range []struct{ name, payload, category, code string }{
		{"backend", `{"series":[],"status":{"code":"SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS"},"is_partial":false}`, "source_backend", "SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS"},
		{"backend_new_bounded_code", `{"series":[],"status":{"code":"QUERY_TS_STORAGE_TIMEOUT"},"is_partial":false}`, "source_backend", "QUERY_TS_STORAGE_TIMEOUT"},
		{"backend_unknown", `{"series":[],"status":{"code":"https://user:secret@example.test/?token=secret"},"is_partial":false}`, "source_backend", "OTHER"},
		{"backend_lowercase", `{"series":[],"status":{"code":"table missing"},"is_partial":false}`, "source_backend", "OTHER"},
		{"identity", `{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":[],"group_values":[],"values":[[1700123456789,1]]}],"is_partial":false}`, "series_identity", "IDENTITY_FIELD_MISSING"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{limits: DefaultLimits(), now: time.Now}
			sink := &collectingSink{}
			_, err := c.decode(context.Background(), strings.NewReader(tc.payload), validAttempt(t), sink)
			var diagnostic interface{ QueryFailure() (string, string) }
			if !errors.As(fmt.Errorf("wrapped: %w", err), &diagnostic) {
				t.Fatalf("no diagnostic on %T: %v", err, err)
			}
			category, code := diagnostic.QueryFailure()
			if category != tc.category || code != tc.code || len(sink.batches) != 0 {
				t.Fatalf("category=%s code=%s batches=%d", category, code, len(sink.batches))
			}
		})
	}
}

func TestQueryFailureAfterHealthyPrefixAndSinkError(t *testing.T) {
	good := `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["bk_target_ip"],"group_values":["127.0.0.1"],"values":[[1700123456789,1]]}`
	bad := `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":[],"group_values":[],"values":[[1700123456789,1]]}`
	c := &Client{limits: DefaultLimits(), now: time.Now}
	sink := &collectingSink{}
	completion, err := c.decode(context.Background(), strings.NewReader(`{"series":[`+good+`,`+bad+`],"is_partial":false}`), validAttempt(t), sink)
	var missing *identityFieldMissingError
	if !errors.As(err, &missing) || len(sink.batches) != 1 || completion.Completeness != "" {
		t.Fatalf("completion=%+v err=%v batches=%d", completion, err, len(sink.batches))
	}
	original := errors.New("consumer budget https://user:secret@example.test")
	_, err = c.decode(context.Background(), strings.NewReader(`{"series":[`+good+`],"is_partial":false}`), validAttempt(t), rejectingDiagnosticSink{original})
	var diagnostic interface{ QueryFailure() (string, string) }
	if !errors.Is(err, original) || errors.As(err, &diagnostic) {
		t.Fatalf("sink error reclassified: %v", err)
	}
}

func TestResponseLimitErrorsKeepTextAndExposeBudgetCodes(t *testing.T) {
	for err, code := range map[error]string{
		ErrResponseBytesExceeded: "RESPONSE_BYTES_EXCEEDED",
		ErrSeriesBytesExceeded:   "SERIES_BYTES_EXCEEDED",
		ErrTotalSeriesExceeded:   "TOTAL_SERIES_EXCEEDED",
		ErrTotalRecordsExceeded:  "TOTAL_RECORDS_EXCEEDED",
	} {
		if err.Error() != "alarmd access uq: "+code {
			t.Fatalf("sentinel text changed: %q", err.Error())
		}
		wrapped := fmt.Errorf("alarmd worker: query: %w", err)
		if !errors.Is(wrapped, err) {
			t.Fatalf("errors.Is lost for %s", code)
		}
		var diagnostic interface{ QueryFailure() (string, string) }
		if !errors.As(wrapped, &diagnostic) {
			t.Fatalf("no diagnostic for %s", code)
		}
		if category, got := diagnostic.QueryFailure(); category != "budget" || got != code {
			t.Fatalf("diagnostic for %s = (%s,%s)", code, category, got)
		}
	}
}

func TestBoundedFailureCodeGrammar(t *testing.T) {
	for code, want := range map[string]string{
		"SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS": "SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS",
		"QUERY_TS_PARTIAL":                   "QUERY_TS_PARTIAL",
		"A1":                                 "A1",
		"":                                   "OTHER",
		"1A":                                 "OTHER",
		"lower":                              "OTHER",
		"HAS-DASH":                           "OTHER",
		strings.Repeat("A", 65):              "OTHER",
	} {
		if got := boundedFailureCode(code); got != want {
			t.Fatalf("boundedFailureCode(%q) = %q, want %q", code, got, want)
		}
	}
}

type rejectingDiagnosticSink struct{ err error }

func (s rejectingDiagnosticSink) ConsumeProviderSeries(context.Context, execution.ProviderSeriesBatch) error {
	return s.err
}
