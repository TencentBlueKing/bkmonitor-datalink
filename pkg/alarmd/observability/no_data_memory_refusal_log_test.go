package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func refusalLogObserver(output *bytes.Buffer, t *testing.T) *LoggingObserver {
	t.Helper()
	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 100})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	return NewLoggingObserver(New("alarmd", output), policy)
}

// The size and the bound travel together on the line.
//
// Either one alone is unreadable: bytes with no bound does not say whether the
// record is over by a little or by a lot, and a bound with no bytes does not
// say anything at all. The pair is what turns "this Plan's memory did not fit"
// into a number somebody can act on.
func TestNoDataMemoryRefusalLineCarriesTheSizeAndTheBound(t *testing.T) {
	var output bytes.Buffer
	observer := refusalLogObserver(&output, t)
	observer.Observe(context.Background(), Observation{
		Component: ComponentState, Stage: StageNoDataMemoryRefused, Result: ResultDegraded,
		ReasonCode: ReasonCode("STATE_BUDGET_EXCEEDED"),
		Trace:      TraceFields{StrategyID: "8946"},
		NoDataMemoryRefusal: &NoDataMemoryRefusalFacts{
			Reason: "STATE_BUDGET_EXCEEDED", Record: "GROUPS", Groups: 120000, Limit: 100000,
		},
	})

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("wrote %d lines, want one; log=%s", len(lines), output.String())
	}
	var line map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &line); err != nil {
		t.Fatal(err)
	}
	for field, want := range map[string]any{
		"stage":                  string(StageNoDataMemoryRefused),
		"no_data_memory_refusal": "STATE_BUDGET_EXCEEDED",
		"no_data_memory_record":  "GROUPS",
		"no_data_memory_groups":  float64(120000),
		"no_data_memory_limit":   float64(100000),
		"strategy_id":            "8946",
	} {
		if line[field] != want {
			t.Fatalf("line[%q] = %#v, want %#v; line=%#v", field, line[field], want, line)
		}
	}
}

// A refusal that measured nothing writes no numbers rather than zeroes.
func TestNoDataMemoryRefusalLineOmitsNumbersItDoesNotHave(t *testing.T) {
	var output bytes.Buffer
	observer := refusalLogObserver(&output, t)
	observer.Observe(context.Background(), Observation{
		Component: ComponentState, Stage: StageNoDataMemoryRefused, Result: ResultDegraded,
		ReasonCode:          ReasonCode("STATE_CORRUPT"),
		NoDataMemoryRefusal: &NoDataMemoryRefusalFacts{Reason: "STATE_CORRUPT"},
	})

	var line map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(output.String())), &line); err != nil {
		t.Fatal(err)
	}
	if line["no_data_memory_refusal"] != "STATE_CORRUPT" {
		t.Fatalf("line = %#v, want the reason named", line)
	}
	for _, field := range []string{"no_data_memory_record", "no_data_memory_groups", "no_data_memory_limit"} {
		if _, present := line[field]; present {
			t.Fatalf("line carried %q for a refusal that measured nothing: %#v", field, line)
		}
	}
}

// The write line carries the outcome and whether it was kept.
//
// Both, because they answer different halves of one question: stored says
// whether to worry, outcome says where to look. A line with only the first
// sends a reader to the same place for a lost race and an unreachable store.
func TestNoDataMemoryWriteLineCarriesTheOutcomeAndWhetherItWasKept(t *testing.T) {
	for _, test := range []struct {
		outcome string
		stored  bool
	}{
		{"APPLIED", true},
		{"STALE_VERSION", false},
	} {
		var output bytes.Buffer
		observer := refusalLogObserver(&output, t)
		observer.Observe(context.Background(), Observation{
			Component: ComponentState, Stage: StageNoDataMemoryWritten, Result: ResultSuccess,
			Trace:             TraceFields{StrategyID: "8946"},
			NoDataMemoryWrite: &NoDataMemoryWriteFacts{Outcome: test.outcome, Stored: test.stored},
		})
		var line map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(output.String())), &line); err != nil {
			t.Fatal(err)
		}
		for field, want := range map[string]any{
			"stage":                  string(StageNoDataMemoryWritten),
			"no_data_memory_outcome": test.outcome,
			// Written even when false, which is the reading somebody is
			// looking for: an omitted false is a line that does not say the
			// memory was dropped.
			"no_data_memory_stored": test.stored,
			"strategy_id":           "8946",
		} {
			if line[field] != want {
				t.Fatalf("line[%q] = %#v, want %#v; line=%#v", field, line[field], want, line)
			}
		}
	}
}
