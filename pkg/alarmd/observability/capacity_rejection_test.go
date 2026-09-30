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

// A rejection reports what every budget had taken, not only the one that
// refused.
//
// This is the sentence decision-019 could not get anyone to say from the logs:
// the count budget refused while the byte budget it was standing in for was
// almost untouched. With only the budget that was reached on the line, the two
// readings - a process at its memory limit, and a process stopped by a number
// that was supposed to represent memory - arrive identically, and the second
// one went a year without being visible.
func TestARejectionReportsEveryBudgetNotOnlyTheOneThatRefused(t *testing.T) {
	own := uint64(524288)
	var output bytes.Buffer
	limiter, _ := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 10})
	policy, _ := NewBoundedLogPolicy(limiter)
	NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
		Component: ComponentResource, Stage: StageResourceHard, Result: ResultPaused,
		Err:            errors.New("rejected"),
		CapacityBudget: CapacityBudgetStateMutations,
		CapacityRejection: &CapacityRejectionFacts{
			Phase: "normal_output", OwnUsed: &own, SharedUsed: 524288, Requested: 1, Limit: 524288,
			Usage: []CapacityBudgetUsage{
				{Budget: CapacityBudgetStateMutations, OwnUsed: 524288, Limit: 524288},
				{Budget: CapacityBudgetRetainedBytes, OwnUsed: 107374182, Limit: 1073741824},
				{Budget: "a_budget_this_build_does_not_name", OwnUsed: 7, Limit: 9},
			},
		},
	})
	var row map[string]any
	if err := json.Unmarshal(output.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	// The budget that refused, and one that did not, both readable against
	// their own limits. Ten percent of the byte budget while the count is at
	// its ceiling is the whole finding.
	for key, want := range map[string]any{
		"capacity_own_state_mutations":   float64(524288),
		"capacity_limit_state_mutations": float64(524288),
		"capacity_own_retained_bytes":    float64(107374182),
		"capacity_limit_retained_bytes":  float64(1073741824),
	} {
		if row[key] != want {
			t.Fatalf("%s = %#v, want %v; without it the line cannot say which budget was under pressure", key, row[key], want)
		}
	}
	if _, leaked := row["capacity_own_a_budget_this_build_does_not_name"]; leaked {
		t.Fatalf("a budget name this build does not know reached the log line: %v", row)
	}
}

// The completion row carries the same quantities whatever the outcome, so the
// rejections have a distribution to be read against.
func TestTheCompletionRowCarriesBudgetUsageOnASuccess(t *testing.T) {
	var output bytes.Buffer
	limiter, _ := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 10})
	policy, _ := NewBoundedLogPolicy(limiter)
	NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
		Component: ComponentScheduler, Stage: StageSlotCompleted, Result: ResultSuccess,
		Direction: DirectionInternal,
		SlotBudgetUsage: &SlotBudgetUsageFacts{
			StateMutations: 19539, RetainedBytes: 68681728,
			StateMutationsLimit: 524288, RetainedBytesLimit: 1073741824,
		},
	})
	var row map[string]any
	if err := json.Unmarshal(output.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	usage, present := row["slot_budget_usage"].(map[string]any)
	if !present {
		t.Fatalf("a successful Slot reported no budget usage: %v", row)
	}
	if usage["state_mutations"] != float64(19539) || usage["retained_bytes"] != float64(68681728) {
		t.Fatalf("slot_budget_usage = %v, want what this Slot took", usage)
	}
	// The limits are deliberately not on the row. They are process constants
	// measured against by every Slot on the replica and published once per
	// process; restating them on each completion row would roughly double it,
	// which at one row per object per Slot is most of a gigabyte a day for
	// numbers that did not change.
	for _, absent := range []string{"state_mutations_limit", "retained_bytes_limit", "events_limit"} {
		if _, present := usage[absent]; present {
			t.Fatalf("%s reached the completion row: %v", absent, usage)
		}
	}
}

// The row says which phase held the retained bytes, under the names a reader
// queries by.
//
// The names are asserted off the rendered row rather than off the struct
// because the row is where they are a contract. A field the encoder publishes
// under its Go name renders, reads as present and carries the right number,
// and every dashboard asking for it gets nothing back.
func TestTheCompletionRowSaysWhichPhaseHeldTheRetainedBytes(t *testing.T) {
	var output bytes.Buffer
	limiter, _ := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 10})
	policy, _ := NewBoundedLogPolicy(limiter)
	NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
		Component: ComponentScheduler, Stage: StageSlotCompleted, Result: ResultSuccess,
		Direction: DirectionInternal,
		SlotBudgetUsage: &SlotBudgetUsageFacts{
			RetainedBytes: 68681728, RetainedInputBytes: 1638400,
			RetainedGapBytes: 32768, RetainedOutputBytes: 67010560,
		},
	})
	var row map[string]any
	if err := json.Unmarshal(output.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	usage, present := row["slot_budget_usage"].(map[string]any)
	if !present {
		t.Fatalf("a successful Slot reported no budget usage: %v", row)
	}
	// Distinct values, so a row that carries one phase's number under another
	// phase's name fails here rather than reading as plausible.
	for name, want := range map[string]float64{
		"retained_input_bytes": 1638400, "retained_gap_bytes": 32768, "retained_output_bytes": 67010560,
	} {
		if usage[name] != want {
			t.Fatalf("%s = %v, want %v; the row is what a reader splits the pool by", name, usage[name], want)
		}
	}
	// Zero is reported rather than omitted. A phase that is nothing on most
	// Slots and the whole budget on a few is the one worth finding, and a phase
	// whose zeros are absent has no denominator to be occasional against.
	NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
		Component: ComponentScheduler, Stage: StageSlotCompleted, Result: ResultSuccess,
		Direction: DirectionInternal, SlotBudgetUsage: &SlotBudgetUsageFacts{RetainedBytes: 4096, RetainedInputBytes: 4096},
	})
	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
	var quiet map[string]any
	if err := json.Unmarshal(lines[len(lines)-1], &quiet); err != nil {
		t.Fatal(err)
	}
	quietUsage, _ := quiet["slot_budget_usage"].(map[string]any)
	for _, name := range []string{"retained_gap_bytes", "retained_output_bytes"} {
		if value, present := quietUsage[name]; !present || value != float64(0) {
			t.Fatalf("%s = %v (present=%t) on a Slot that held none, want an explicit zero", name, value, present)
		}
	}
}

// The completion line carries which of the Slot's two gap applies refused, as
// its own key.
//
// Asserted on the rendered line rather than on the Observation, because that
// is where the field is a contract. The first version of this carried the site
// on the error struct only: the reason code reached the line and the site
// stayed inside the error's sentence, so 63 refusals in a quarter of an hour
// said what the store did and nothing about which of the two writes it did it
// to - which was the one thing the field was added to answer.
func TestTheCompletionRowSaysWhichGapApplyRefused(t *testing.T) {
	var output bytes.Buffer
	limiter, _ := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 10})
	policy, _ := NewBoundedLogPolicy(limiter)
	NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
		Component: ComponentScheduler, Stage: StageSlotCompleted, Result: ResultFailed,
		Direction: DirectionInternal, ReasonCode: "GAP_APPLY_CONFLICT", GapApplySite: "after_state",
	})
	var row map[string]any
	if err := json.Unmarshal(output.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if row["gap_apply_site"] != "after_state" {
		t.Fatalf("gap_apply_site = %v, want the site as its own key: %v", row["gap_apply_site"], row)
	}
	// A completion that refused nothing carries no site, so the key's presence
	// means a refusal rather than meaning the line was rendered by this build.
	output.Reset()
	NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
		Component: ComponentScheduler, Stage: StageSlotCompleted, Result: ResultSuccess,
		Direction: DirectionInternal,
	})
	var healthy map[string]any
	if err := json.Unmarshal(output.Bytes(), &healthy); err != nil {
		t.Fatal(err)
	}
	if _, present := healthy["gap_apply_site"]; present {
		t.Fatalf("a completion that refused nothing carries a site: %v", healthy)
	}
}
