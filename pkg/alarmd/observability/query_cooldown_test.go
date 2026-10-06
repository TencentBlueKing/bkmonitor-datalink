package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestQueryCooldownStructuredLog(t *testing.T) {
	var output bytes.Buffer
	facts := &QueryCooldownFacts{Event: "entered", Until: time.Unix(1000, 0), LastQueryAt: time.Unix(900, 0), Failures: 2}
	newQueryFailureTestObserver(&output).Observe(context.Background(), Observation{Component: ComponentScheduler, Stage: StageQueryCooldown, Result: ResultDegraded, Trace: TraceFields{QueryGroupKey: "qg"}, QueryCooldown: facts})
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	f, ok := event["query_cooldown"].(map[string]any)
	if !ok || f["event"] != "entered" || f["failures"] != float64(2) || event["stage"] != StageQueryCooldown {
		t.Fatalf("lost facts: %v", event)
	}
	if !ValidRunOutcome("query_cooldown") {
		t.Fatal("cooldown becomes other_error")
	}
}
