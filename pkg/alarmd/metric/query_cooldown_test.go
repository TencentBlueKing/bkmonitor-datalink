package metric

import (
	"context"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestQueryCooldownMetricHasOnlyBoundedEvent(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	for _, event := range []string{"entered", "extended", "recovered", "query_revision_changed", "qg-secret"} {
		r.Observe(context.Background(), observability.Observation{Component: observability.ComponentScheduler, Stage: observability.StageQueryCooldown, Trace: observability.TraceFields{QueryGroupKey: "qg-secret"}, QueryCooldown: &observability.QueryCooldownFacts{Event: event}})
	}
	families, err := r.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_query_cooldown_events_total" {
			continue
		}
		found = true
		// Every event of the closed list and other, created at zero before
		// the first observation: a restore at start-up happens before the
		// first scrape, and a series created by it would read as no increase.
		if len(family.Metric) != len(observability.QueryCooldownEvents)+1 {
			t.Fatalf("unexpected vocabulary: %v", family)
		}
		for _, m := range family.Metric {
			if len(m.Label) != 1 || m.Label[0].GetName() != "event" || m.Label[0].GetValue() == "qg-secret" {
				t.Fatalf("unbounded labels: %v", m)
			}
		}
	}
	if !found {
		t.Fatal("cooldown metric missing")
	}
}

func TestQueryCooldownDispatchIsNotNotDue(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	r.RecordDispatchSkipped("query_cooldown")
	if got := testutil.ToFloat64(r.phaseTwo.dueIndex.skipped.WithLabelValues("query_cooldown")); got != 1 {
		t.Fatalf("cooldown count %v", got)
	}
	if got := testutil.ToFloat64(r.phaseTwo.dueIndex.skipped.WithLabelValues("not_due")); got != 0 {
		t.Fatalf("not_due count %v", got)
	}
}

func TestFleetCooldownCountRequiresEvidence(t *testing.T) {
	const name = "bkmonitor_alarmd_fleet_query_cooldown_objects"
	if _, ok := gatherFleet(t, FleetVerdict{Health: "UNKNOWN"})[name]; ok {
		t.Fatal("unmeasured cooldown published as zero")
	}
	count := 7
	if got := gatherFleet(t, FleetVerdict{Health: "DEGRADED", QueryCooldown: &count})[name][""]; got != 7 {
		t.Fatalf("cooldown population %v", got)
	}
}

// The events are there, at zero, before anything happened.
func TestQueryCooldownEventsArePreCreated(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	counts := map[string]float64{}
	for _, m := range gatherFamily(t, r, "bkmonitor_alarmd_query_cooldown_events_total") {
		counts[m.GetLabel()[0].GetValue()] = m.GetCounter().GetValue()
	}
	for _, event := range append(append([]string(nil), observability.QueryCooldownEvents...), "other") {
		if value, present := counts[event]; !present || value != 0 {
			t.Fatalf("events before any observation = %v, want %q at zero", counts, event)
		}
	}
}
