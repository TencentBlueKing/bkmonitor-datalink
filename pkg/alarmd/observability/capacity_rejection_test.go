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

func TestCapacityRejectionLogWhitelistAndUnknownOwnUsage(t *testing.T) {
	zero := uint64(0)
	for _, test := range []struct {
		phase     string
		own       *uint64
		wantPhase string
	}{
		{"normal_output", &zero, "normal_output"},
		{"query_free", nil, "query_free"},
		{"https://user:secret@example.test/query", nil, "other"},
	} {
		var output bytes.Buffer
		limiter, _ := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 10})
		policy, _ := NewBoundedLogPolicy(limiter)
		NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
			Component: ComponentResource, Stage: StageResourceHard, Result: ResultPaused, Err: errors.New("reserve https://user:secret@example.test/query: rejected"),
			CapacityBudget: CapacityBudgetRetainedBytes, CapacityRejection: &CapacityRejectionFacts{Phase: test.phase, OwnUsed: test.own, SharedUsed: 60, Requested: 50, Limit: 100},
		})
		if strings.Contains(output.String(), "secret") || strings.Contains(output.String(), "https://") {
			t.Fatalf("unbounded log: %s", output.String())
		}
		var row map[string]any
		if err := json.Unmarshal(output.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		if row["capacity_phase"] != test.wantPhase {
			t.Fatal(row)
		}
		if row["error"] != "reserve <url>: rejected" {
			t.Fatalf("sanitized error text=%#v", row["error"])
		}
		own, present := row["capacity_own_used"]
		if present != (test.own != nil) || (present && own != float64(0)) {
			t.Fatalf("unknown confused with zero: %v", row)
		}
		if row["capacity_shared_used"] != float64(60) || row["capacity_requested"] != float64(50) || row["capacity_limit"] != float64(100) {
			t.Fatal(row)
		}
	}
}
