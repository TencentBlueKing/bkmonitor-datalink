package main

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The parts carry kinds and failure categories as the rows did. Bounded to
// the closed labels at the scrape, kinds that fold into one label add up
// and keep the earliest since, as the rows' read kept the oldest row;
// categories fold the same way.
func TestTheScrapeBoundsThePartsKindsAndCategoriesToItsLabels(t *testing.T) {
	at := time.Date(2026, 10, 6, 8, 0, 0, 0, time.UTC)
	view := fleet.View{Health: fleet.HealthDegraded, Covered: 10, Replicas: []string{"pod-a"}}
	part := fleet.ReplicaPart{Metrics: &fleet.MetricRows{
		Kinds: map[string]fleet.KindRows{
			"A_KIND_NOBODY_LISTED": {Count: 2, Since: at.Add(-10 * time.Minute)},
			"ANOTHER_UNLISTED":     {Count: 1, Since: at.Add(-30 * time.Minute)},
			fleet.KindDegradedRun:  {Count: 4, Since: at.Add(-5 * time.Minute)},
		},
		Failures: map[string]int{"a-category-nobody-listed": 2, "another": 3, "source_backend": 1},
		Cooldown: 3,
	}}
	verdict := fleetVerdictOf(view, part, at)
	anomalies := map[string]metric.FleetCount{}
	for _, count := range verdict.Anomalies {
		anomalies[count.Value] = count
	}
	other := anomalies[fleet.LabelOther]
	if len(anomalies) != 2 || other.Count != 3 || other.OldestAgeSeconds != (30*time.Minute).Seconds() ||
		anomalies[fleet.KindDegradedRun].Count != 4 || anomalies[fleet.KindDegradedRun].OldestAgeSeconds != (5*time.Minute).Seconds() {
		t.Fatalf("anomalies %+v, want the unlisted kinds folded into other with the earliest since", verdict.Anomalies)
	}
	failures := map[string]int{}
	for _, count := range verdict.Failures {
		failures[count.Value] = count.Count
	}
	if len(failures) != 2 || failures[observability.QueryFailureCategoryOther] != 5 || failures["source_backend"] != 1 {
		t.Fatalf("failures %+v, want the unlisted categories folded into other", verdict.Failures)
	}
	if verdict.QueryCooldown == nil || *verdict.QueryCooldown != 3 {
		t.Fatalf("query cooldown %v, want the parts' 3", verdict.QueryCooldown)
	}
	again := fleetVerdictOf(view, part, at)
	if len(again.Anomalies) != len(verdict.Anomalies) || again.Anomalies[0].Value != verdict.Anomalies[0].Value ||
		again.Failures[0].Value != verdict.Failures[0].Value {
		t.Fatalf("two scrapes of one deployment exported in different orders: %+v / %+v", verdict.Anomalies, again.Anomalies)
	}
}

// A part without the rows' counts while replicas were counted says nothing
// of the rows: their families are not exported as none. With no replica
// counted -- a registry that could not be read -- there are no rows, and
// they read as none, as they always did.
func TestRowsNobodyCountedAreUnknownAndRowsNoReplicaHasAreNone(t *testing.T) {
	at := time.Date(2026, 10, 6, 8, 0, 0, 0, time.UTC)
	counted := fleetVerdictOf(fleet.View{Health: fleet.HealthUnknown, Covered: 10, Replicas: []string{"pod-a"}}, fleet.ReplicaPart{}, at)
	if !counted.RowsUnknown || counted.Checks != nil || counted.Losses != nil || counted.QueryCooldown != nil || counted.Workers == nil {
		t.Fatalf("verdict %+v, want the row families unknown and the replicas' own facts kept", counted)
	}
	none := fleetVerdictOf(fleet.View{Health: fleet.HealthUnknown}, fleet.ReplicaPart{}, at)
	if none.RowsUnknown || len(none.Checks) != len(fleet.Checks()) || len(none.Losses) != len(fleet.Losses)+1 ||
		none.QueryCooldown != nil {
		t.Fatalf("verdict %+v, want every closed table at zero with no replica counted", none)
	}
}

// The handover gauge carries the number of objects two replicas hold at
// once, read from their summaries: one while a handover is in progress,
// zero once one replica holds it.
func TestTheScrapeExportsTheHandoverItsSummariesShow(t *testing.T) {
	at := time.Date(2026, 10, 6, 8, 0, 0, 0, time.UTC)
	for name, both := range map[string]bool{"held by both": true, "held by one": false} {
		owned := map[string][]string{"pod-a": {"qg-a"}, "pod-b": {"qg-b", "qg-moving"}}
		if both {
			owned["pod-a"] = append(owned["pod-a"], "qg-moving")
		}
		summaries := []fleet.ReplicaSummary{}
		for _, replica := range []string{"pod-a", "pod-b"} {
			snapshot := fleet.Snapshot{Replica: replica, TakenAt: at, Owned: len(owned[replica]), Determined: len(owned[replica])}
			summaries = append(summaries, fleet.SummaryOf(snapshot, owned[replica], time.Minute))
		}
		ids := []string{"qg-a", "qg-b", "qg-moving"}
		view, part := fleet.AggregateSummaries(fleet.Expectation{QueryGroups: len(ids), Known: true, IDs: ids}, fleet.DigestOf(ids),
			summaries, []string{"pod-a", "pod-b"}, at, time.Minute, func(replicas []string) ([][]string, bool) {
				sets := [][]string{}
				for _, replica := range replicas {
					sets = append(sets, owned[replica])
				}
				return sets, true
			})
		verdict := fleetVerdictOf(view, part, at)
		want := 0
		if both {
			want = 1
		}
		if verdict.HandoverObjects == nil || *verdict.HandoverObjects != want {
			t.Errorf("%s: handover %v, want %d", name, verdict.HandoverObjects, want)
		}
	}
	// The owned lists unread while the digests disagree: a handover may be
	// going on, and how many objects it holds is unknown -- absent, not zero.
	owned := map[string][]string{"pod-a": {"qg-a", "qg-moving"}, "pod-b": {"qg-b", "qg-moving"}}
	summaries := []fleet.ReplicaSummary{}
	for _, replica := range []string{"pod-a", "pod-b"} {
		snapshot := fleet.Snapshot{Replica: replica, TakenAt: at, Owned: len(owned[replica]), Determined: len(owned[replica])}
		summaries = append(summaries, fleet.SummaryOf(snapshot, owned[replica], time.Minute))
	}
	ids := []string{"qg-a", "qg-b", "qg-moving"}
	view, part := fleet.AggregateSummaries(fleet.Expectation{QueryGroups: len(ids), Known: true, IDs: ids}, fleet.DigestOf(ids),
		summaries, []string{"pod-a", "pod-b"}, at, time.Minute, func([]string) ([][]string, bool) { return nil, false })
	if verdict := fleetVerdictOf(view, part, at); verdict.HandoverObjects != nil {
		t.Fatalf("handover %d with the owned lists unread, want it absent", *verdict.HandoverObjects)
	}
}
