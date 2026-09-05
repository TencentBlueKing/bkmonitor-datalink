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

func TestQueryFailureLogRejectsSensitiveValues(t *testing.T) {
	secret := "https://user:secret@example.test/?token=secret"
	for _, facts := range []QueryFailureFacts{
		{Stage: "execute", Category: "source_backend", Code: "SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS"},
		{Stage: "stream_complete", Category: "budget", Code: "retained_bytes"},
		{Stage: secret, Category: secret, Code: secret},
	} {
		var output bytes.Buffer
		func() *LoggingObserver {
			l, _ := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 10})
			p, _ := NewBoundedLogPolicy(l)
			return NewLoggingObserver(New("alarmd", &output), p)
		}().Observe(context.Background(), Observation{Component: ComponentAccess, Stage: StageQueryCompleted, Result: ResultFailed, Err: errors.New(secret), QueryFailure: &facts})
		if strings.Contains(output.String(), "secret") || strings.Contains(output.String(), "https://") {
			t.Fatalf("sensitive log: %s", output.String())
		}
		var event map[string]any
		if err := json.Unmarshal(output.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		if facts.Stage == secret {
			if event["failure_stage"] != "other" || event["failure_category"] != "other" || event["failure_code"] != "OTHER" {
				t.Fatalf("unbounded facts: %v", event)
			}
		} else if event["failure_stage"] != facts.Stage || event["failure_category"] != facts.Category || event["failure_code"] != facts.Code {
			t.Fatalf("lost facts: %v", event)
		}
	}
}
