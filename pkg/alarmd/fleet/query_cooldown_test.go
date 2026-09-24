package fleet

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestQueryCooldownPreservesFailureUntilRealRecovery(t *testing.T) {
	for _, exit := range []string{"recovered", "query_revision_changed", "disabled"} {
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
			// A pooled object is in the demoted column, not the anomaly one: its
			// backend is what stopped answering, and counting it as this
			// deployment's anomaly makes a wider backend outage read as alarmd
			// getting worse.
			if listed := tracker.Anomalies(); len(listed) != 0 {
				t.Fatalf("a pooled object was reported as this deployment's anomaly: %+v", listed)
			}
			rows := tracker.Demoted()
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
	for _, event := range []string{"entered", "extended", "query_revision_changed", "disabled", "recovered"} {
		tracker.Observe(context.Background(), observability.Observation{Trace: observability.TraceFields{QueryGroupKey: "qg"}, QueryCooldown: &observability.QueryCooldownFacts{Event: event}})
		if tracker.Determined() != 0 || tracker.HasConclusion("qg") {
			t.Fatalf("%s invented a conclusion", event)
		}
	}
}

// How long the most overdue pooled object has waited, not how many are waiting.
//
// Objects fall due continuously, so a steady count of them is what a working
// retry path looks like from the outside -- and so is a path that stopped days
// ago. The two differ only in how long any one object has been sitting there,
// and the page's pool panel had no way to tell a reader which it was looking at.
func TestThePoolReportsHowOverdueItsWorstObjectIs(t *testing.T) {
	snapshots := healthySnapshots()
	recent := anomaly("qg-just-due")
	recent.QueryCooldown = &observability.QueryCooldownFacts{
		Event: "entered", Until: now.Add(-30 * time.Second), LastQueryAt: now.Add(-5 * time.Minute)}
	stuck := anomaly("qg-long-due")
	stuck.QueryCooldown = &observability.QueryCooldownFacts{
		Event: "entered", Until: now.Add(-6 * time.Hour), LastQueryAt: now.Add(-7 * time.Hour)}
	waiting := anomaly("qg-not-due")
	waiting.QueryCooldown = &observability.QueryCooldownFacts{
		Event: "entered", Until: now.Add(time.Hour), LastQueryAt: now.Add(-time.Minute)}
	snapshots[1].Demoted = []Anomaly{recent, stuck, waiting}
	snapshots[1].TotalDemoted = 3

	view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, freshness)
	if view.DemotedDue != 2 {
		t.Fatalf("demoted_due = %d, want 2", view.DemotedDue)
	}
	if got := time.Duration(view.DemotedDueOldestSeconds) * time.Second; got != 6*time.Hour {
		t.Errorf("oldest overdue = %s, want 6h: the count alone cannot tell a retry path that is"+
			" working from one that stopped", got)
	}
}

// A pooled row says when it entered the pool: the first entered or extended
// event with no cooldown standing, kept across extensions, cleared on the
// way out. The anomaly's onset is earlier -- the failures that put it there
// come first -- and a retained record between the two is not the
// cooldown's doing.
func TestAPooledRowSaysWhenItEnteredThePool(t *testing.T) {
	at := time.Unix(1000, 0)
	tracker := NewTracker(nil, "replica", func() time.Time { return at })
	observe := func(o observability.Observation) {
		o.Trace.QueryGroupKey = "qg"
		tracker.Observe(context.Background(), o)
	}
	observe(observability.Observation{QueryFailure: &observability.QueryFailureFacts{Stage: "execute", Category: "source_backend", Code: "NO_TABLE"}})
	observe(observability.Observation{ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE", ProgressCompletionCause: "query_failed"})
	at = at.Add(5 * time.Minute)
	observe(observability.Observation{QueryCooldown: &observability.QueryCooldownFacts{Event: "entered", Until: at.Add(time.Minute), LastQueryAt: at, Failures: 3}})
	entered := at
	at = at.Add(10 * time.Minute)
	observe(observability.Observation{QueryCooldown: &observability.QueryCooldownFacts{Event: "extended", Until: at.Add(time.Minute), LastQueryAt: at, Failures: 4}})
	rows := tracker.Demoted()
	if len(rows) != 1 || !rows[0].DemotedSince.Equal(entered) || !rows[0].Since.Before(entered) {
		t.Fatalf("pooled row = %+v, want demoted_since at the entry (%v), after the anomaly's onset", rows, entered)
	}
	observe(observability.Observation{RunOutcome: "query_cooldown"})
	observe(observability.Observation{QueryCooldown: &observability.QueryCooldownFacts{Event: "recovered"}})
	if rows := tracker.Anomalies(); len(rows) != 1 || !rows[0].DemotedSince.IsZero() {
		t.Fatalf("out of the pool the row still carries demoted_since: %+v", rows)
	}
}

// The pool's arithmetic closes on one replica across restarts and owners: an
// object restored from its record is in the pool since it first entered,
// and is not an entry; an object handed to another replica while in the
// pool is not an exit; and a return within the window of an exit is counted
// as a re-entry. Entries plus restored equal exits plus handovers plus the
// objects in the pool.
func TestThePoolFlowClosesAcrossRestoresHandoversAndReentries(t *testing.T) {
	at := time.Unix(10_000, 0)
	tracker := NewTracker(nil, "replica", func() time.Time { return at })
	event := func(queryGroup string, facts observability.QueryCooldownFacts) {
		tracker.Observe(context.Background(), observability.Observation{
			Trace: observability.TraceFields{QueryGroupKey: queryGroup}, QueryCooldown: &facts})
	}
	firstEntered := at.Add(-3 * time.Hour)
	event("restored", observability.QueryCooldownFacts{Event: "restored", EnteredAt: firstEntered, Until: at.Add(time.Minute), Source: "restored"})
	event("entered", observability.QueryCooldownFacts{Event: "entered", Until: at.Add(time.Minute), Source: "probe"})
	event("handed", observability.QueryCooldownFacts{Event: "entered", Until: at.Add(time.Minute), Source: "probe"})
	event("flapping", observability.QueryCooldownFacts{Event: "reentered", Until: at.Add(time.Minute), Reentries: 1, LastExitReason: "recovered"})
	event("entered", observability.QueryCooldownFacts{Event: "recovered"})

	var restored *Anomaly
	for _, row := range tracker.Demoted() {
		if row.QueryGroup == "restored" {
			copy := row
			restored = &copy
		}
	}
	if restored == nil || !restored.DemotedSince.Equal(firstEntered) || restored.QueryCooldown.Source != "restored" {
		t.Fatalf("restored row = %+v, want in the pool since it first entered", restored)
	}
	tracker.Forget(map[string]struct{}{"restored": {}, "entered": {}, "flapping": {}})

	flow := tracker.DemotionFlow()
	if flow.Entries != 3 || flow.Restored != 1 || flow.Exits != 1 || flow.Handovers != 1 || flow.Reentries != 1 {
		t.Fatalf("flow = %+v, want 3 entries, 1 restored, 1 exit, 1 handover, 1 re-entry", flow)
	}
	if pooled := len(tracker.Demoted()); flow.Entries+flow.Restored != flow.Exits+flow.Handovers+pooled {
		t.Fatalf("flow %+v with %d pooled does not close", flow, pooled)
	}
}
