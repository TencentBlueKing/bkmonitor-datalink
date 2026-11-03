package fleet

import (
	"encoding/json"
	"testing"
	"time"
)

func TestReadHoldStandingRequiresOneCurrentOwner(t *testing.T) {
	now := time.Unix(1800, 0)
	facts := StrategyLookupFacts{Found: true, Plans: []StrategyPlanRef{{Tenant: "tenant", Business: "2", QueryGroup: "qg"}}}
	first := Snapshot{Replica: "alice", TakenAt: now, Owned: 1, OwnedObjects: []string{"qg"}, ReadHolds: map[string]ReadHoldFacts{"qg": {Millis: 60000, Annotation: "alarmd 当前自动推后 60 秒"}}}
	view := Aggregate(Expectation{Known: true, IDs: []string{"qg"}, QueryGroups: 1}, []Snapshot{first}, []string{"alice"}, now, time.Minute)
	standing := StrategyStandingOf("101", "", "", "alice", facts, &view, now)
	if standing.Plans[0].ReadHold == nil || standing.Plans[0].ReadHold.Millis != 60000 {
		t.Fatal("single owner's hold was lost")
	}
	if SummaryOf(first, first.OwnedObjects, time.Minute).Head.ReadHolds != nil {
		t.Fatal("per-QG holds inflated summary head")
	}
	second := Snapshot{Replica: "bob", TakenAt: now, Owned: 1, OwnedObjects: []string{"qg"}, ReadHolds: map[string]ReadHoldFacts{"qg": {Millis: 120000}}}
	for _, replicas := range [][]string{{"alice", "bob"}, {"bob", "alice"}} {
		view = Aggregate(Expectation{Known: true, IDs: []string{"qg"}, QueryGroups: 1}, []Snapshot{first, second}, replicas, now, time.Minute)
		standing = StrategyStandingOf("101", "", "", "alice", facts, &view, now)
		if hold := standing.Plans[0].ReadHold; hold == nil || !hold.Unknown || hold.Millis != 0 {
			t.Fatalf("ambiguous owner gave %+v, want the hold unknown rather than either replica's or none", hold)
		}
	}
}
func TestReadHoldSnapshotRespectsItsExistingByteBudget(t *testing.T) {
	facts := map[string]ReadHoldFacts{"a": {Millis: 1}, "b": {Millis: 2}}
	one, _ := json.Marshal(map[string]ReadHoldFacts{"a": facts["a"]})
	kept := withinReadHoldBudget(facts, len(one))
	if len(kept) != 1 || kept["a"].Millis != 1 {
		t.Fatalf("unstable or oversized prefix: %+v", kept)
	}
}

// A group its replica's whole list leaves out holds nothing; one a cut list
// leaves out, or that no one replica holds, is unknown; and the store marks
// the list it cut to its budget.
func TestAGroupLeftOutOfAWholeReadHoldListHoldsNothing(t *testing.T) {
	now := time.Unix(1800, 0)
	holds := map[string]ReadHoldFacts{"qg-a": {Millis: 1}, "qg-b": {Millis: 2}}
	snapshot := Snapshot{Replica: "alice", TakenAt: now, Owned: 3, OwnedObjects: []string{"qg-a", "qg-b", "qg-c"}, ReadHolds: holds}
	view := Aggregate(Expectation{Known: true, IDs: []string{"qg-a", "qg-b", "qg-c"}, QueryGroups: 3}, []Snapshot{snapshot}, []string{"alice"}, now, time.Minute)
	if hold := view.readHoldOf("qg-a"); hold == nil || hold.Millis != 1 || hold.Unknown {
		t.Fatalf("listed group read %+v", hold)
	}
	if hold := view.readHoldOf("qg-c"); hold != nil {
		t.Fatalf("a group the whole list leaves out read %+v, want no hold", hold)
	}
	if hold := view.readHoldOf("qg-unowned"); hold == nil || !hold.Unknown {
		t.Fatalf("a group no replica holds read %+v, want unknown", hold)
	}
	one, _ := json.Marshal(map[string]ReadHoldFacts{"qg-a": holds["qg-a"]})
	store := &RedisStore{maxAnomalyBytes: len(one)}
	written, err := store.written(snapshot)
	if err != nil || !written.ReadHoldsCut || len(written.ReadHolds) != 1 {
		t.Fatalf("written %+v %v, want the list cut to one and marked", written.ReadHolds, err)
	}
	view = Aggregate(Expectation{Known: true, IDs: []string{"qg-a", "qg-b", "qg-c"}, QueryGroups: 3}, []Snapshot{written}, []string{"alice"}, now, time.Minute)
	if hold := view.readHoldOf("qg-b"); hold == nil || !hold.Unknown {
		t.Fatalf("a held group the cut list dropped read %+v, want unknown rather than no hold", hold)
	}
	if hold := view.readHoldOf("qg-c"); hold == nil || !hold.Unknown {
		t.Fatalf("a group a cut list leaves out read %+v, want unknown", hold)
	}
	if whole, err := (&RedisStore{maxAnomalyBytes: 1 << 20}).written(snapshot); err != nil || whole.ReadHoldsCut {
		t.Fatalf("a list within its budget was marked cut: %v", err)
	}
}
