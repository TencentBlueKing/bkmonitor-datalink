package uq

import (
	"context"
	"errors"
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"strings"
	"testing"
	"time"
)

func TestQueryFailureDiagnosticsPreserveDecodeFailure(t *testing.T) {
	for _, tc := range []struct{ name, payload, category, code string }{
		{"backend", `{"series":[],"status":{"code":"SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS"},"is_partial":false}`, "source_backend", "SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS"},
		{"backend_unknown", `{"series":[],"status":{"code":"https://user:secret@example.test/?token=secret"},"is_partial":false}`, "source_backend", "OTHER"},
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

type rejectingDiagnosticSink struct{ err error }

func (s rejectingDiagnosticSink) ConsumeProviderSeries(context.Context, execution.ProviderSeriesBatch) error {
	return s.err
}
