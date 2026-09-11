package fleet

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestQueryCooldownPreservesFailureUntilRealRecovery(t *testing.T) {
	for _, exit := range []string{"recovered", "config_changed", "disabled"} {
		t.Run(exit, func(t *testing.T) {
			at := time.Unix(1000, 0)
			tracker := NewTracker(nil, "replica", func() time.Time { return at })
			observe := func(o observability.Observation) {
				o.Trace.QueryGroupKey = "qg"
				tracker.Observe(context.Background(), o)
			}
			observe(observability.Observation{QueryFailure: &observability.QueryFailureFacts{Stage: "execute", Category: "source_backend", Code: "NO_TABLE"}})
			observe(observability.Observation{ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE", ProgressCompletionCause: "query_failed"})
			if len(tracker.Anomalies()) != 0 {
				t.Fatal("test must start below the degraded threshold")
			}
			facts := &observability.QueryCooldownFacts{Event: "entered", Until: at.Add(time.Minute), LastQueryAt: at, Failures: 1}
			observe(observability.Observation{QueryCooldown: facts})
			facts.Failures = 999 // The observer must own its copy.
			at = at.Add(2 * time.Minute)
			rows := tracker.Anomalies()
			if len(rows) != 1 || rows[0].QueryCooldown.Failures != 1 || rows[0].Kind != KindDegradedRun {
				t.Fatalf("expired cooldown lost: %+v", rows)
			}
			before := rows[0]
			observe(observability.Observation{RunOutcome: "query_cooldown"})
			observe(observability.Observation{QueryCooldown: &observability.QueryCooldownFacts{Event: exit}})
			rows = tracker.Anomalies()
			if len(rows) != 1 || rows[0].QueryCooldown != nil || rows[0].Failure.Code != "NO_TABLE" || rows[0].ReasonCode != before.ReasonCode || rows[0].Cause != before.Cause || rows[0].Since != before.Since {
				t.Fatalf("policy exit rewrote real failure evidence: %+v", rows)
			}
			observe(observability.Observation{ProgressCompletionKind: "FULL_COMPLETED"})
			if len(tracker.Anomalies()) != 0 {
				t.Fatal("real recovery did not clear anomaly")
			}
		})
	}
}

func TestQueryCooldownMetadataDoesNotDetermineHealth(t *testing.T) {
	tracker := NewTracker(nil, "replica", nil)
	for _, event := range []string{"entered", "extended", "config_changed", "disabled", "recovered"} {
		tracker.Observe(context.Background(), observability.Observation{Trace: observability.TraceFields{QueryGroupKey: "qg"}, QueryCooldown: &observability.QueryCooldownFacts{Event: event}})
		if tracker.Determined() != 0 || tracker.HasConclusion("qg") {
			t.Fatalf("%s invented a conclusion", event)
		}
	}
}
