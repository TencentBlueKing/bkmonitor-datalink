package uq

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A non-partial UQ status code is a deterministic answer for the queried table
// and field. It must complete the physical query as UNAVAILABLE with a bounded
// detail instead of returning a Go error that makes the Slot retry the same
// query until it ages out.
func TestDeterministicQueryStatusCompletesAsUnavailableWithBoundedDetail(t *testing.T) {
	for _, tc := range []struct{ name, payload, detail string }{
		{"backend", `{"series":[],"status":{"code":"SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS"},"is_partial":false}`, "response=status_space_table_id_field_is_not_exists"},
		{"backend_new_bounded_code", `{"series":[],"status":{"code":"QUERY_TS_STORAGE_TIMEOUT"},"is_partial":false}`, "response=status_query_ts_storage_timeout"},
		{"backend_unknown", `{"series":[],"status":{"code":"https://user:secret@example.test/?token=secret"},"is_partial":false}`, "response=status_other"},
		{"backend_lowercase", `{"series":[],"status":{"code":"table missing"},"is_partial":false}`, "response=status_other"},
		{"backend_without_is_partial", `{"series":[],"status":{"code":"SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS"}}`, "response=status_space_table_id_field_is_not_exists"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{limits: DefaultLimits(), now: time.Now}
			sink := &collectingSink{}
			completion, err := c.decode(context.Background(), strings.NewReader(tc.payload), validAttempt(t), sink)
			attempt := failedAttempt(t, completion, err)
			if attempt.ReasonCode != execution.ReasonCode(contract.ReasonQueryUnavailable) || attempt.Detail != tc.detail {
				t.Fatalf("attempt=%+v, want QUERY_UNAVAILABLE with detail %q", attempt, tc.detail)
			}
			if execution.RouteDetailKind(attempt.Detail) != execution.RouteDetailKindResponse {
				t.Fatalf("detail %q is not a response detail", attempt.Detail)
			}
			if completion.DataState != execution.DataStateEmpty || completion.Delivery != (execution.SeriesDelivery{}) || len(sink.batches) != 0 {
				t.Fatalf("completion=%+v batches=%d, want EMPTY without delivered series", completion, len(sink.batches))
			}
			if strings.Contains(attempt.Detail, "secret") || strings.Contains(attempt.Detail, " ") {
				t.Fatalf("detail leaked response content: %q", attempt.Detail)
			}
		})
	}
}

// UQ writes the series array before status. Series decoded before a failing
// status have already reached the sink; the UNAVAILABLE completion keeps their
// DataState and Delivery so the worker's delivery conservation check holds.
func TestDeterministicQueryStatusAfterStreamedSeriesConservesDelivery(t *testing.T) {
	good := `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["bk_target_ip"],"group_values":["127.0.0.1"],"values":[[1700123456789,1]]}`
	c := &Client{limits: DefaultLimits(), now: time.Now}
	sink := &collectingSink{}
	completion, err := c.decode(context.Background(), strings.NewReader(`{"series":[`+good+`],"status":{"code":"QUERY_TS_STORAGE_TIMEOUT"},"is_partial":false}`), validAttempt(t), sink)
	attempt := failedAttempt(t, completion, err)
	if attempt.Detail != "response=status_query_ts_storage_timeout" || len(sink.batches) != 1 {
		t.Fatalf("attempt=%+v batches=%d", attempt, len(sink.batches))
	}
	if completion.DataState != execution.DataStateData || completion.Delivery != sink.batches[0].Delivery ||
		completion.Stats.Series != 1 || completion.Stats.Records != 1 {
		t.Fatalf("completion=%+v batch delivery=%+v, want conserved delivered series", completion, sink.batches[0].Delivery)
	}
}

func TestAbsentIdentityFieldAfterHealthyPrefixAndSinkError(t *testing.T) {
	good := `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["bk_target_ip"],"group_values":["127.0.0.1"],"values":[[1700123456789,1]]}`
	absent := `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":[],"group_values":[],"values":[[1700123456789,1]]}`
	c := &Client{limits: DefaultLimits(), now: time.Now}
	sink := &collectingSink{}
	completion, err := c.decode(context.Background(), strings.NewReader(`{"series":[`+good+`,`+absent+`],"is_partial":false}`), validAttempt(t), sink)
	if err != nil || len(sink.batches) != 2 || completion.Completeness != execution.CompletenessFull ||
		completion.DataState != execution.DataStateData || completion.Stats.NullIdentityFields != 1 {
		t.Fatalf("completion=%+v err=%v batches=%d, want both series delivered with one null identity field", completion, err, len(sink.batches))
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

type rejectingDiagnosticSink struct{ err error }

func (s rejectingDiagnosticSink) ConsumeProviderSeries(context.Context, execution.ProviderSeriesBatch) error {
	return s.err
}
