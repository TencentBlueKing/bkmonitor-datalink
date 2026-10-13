// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A full table counts every fact it could not keep, by what, and names the
// objects lost, newest last and each once: one answered query for an object
// with no row loses its truncation, its ranges and its round.
func TestAFullTableCountsAndNamesWhatItDrops(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	tracker.maxTracked = 1
	tracker.Observe(context.Background(), completion("qg-1", "FULL_COMPLETED", "854"))
	if dropped := tracker.Dropped(); dropped.Lost() || len(dropped.Counts) != len(TrackerDroppedKinds) {
		t.Fatalf("dropped before the table filled = %+v, want every kind at zero", dropped)
	}
	tracker.Observe(context.Background(), observability.Observation{Component: observability.ComponentAccess,
		Stage: observability.StageQueryCompleted, Result: observability.ResultSuccess,
		QueryRanges:     []observability.QueryRangeFacts{{Digest: "query-a", AskedSeconds: 600, AcceptedSeconds: 60}},
		QueryTruncation: []observability.QueryTruncationFacts{{Source: "custom/event", Dimension: "tags.host"}},
		Trace:           observability.TraceFields{QueryGroupKey: "qg-2", StrategyID: "410", BusinessID: "2", EvaluationTime: 60}})
	want := map[string]uint64{TrackerDroppedObservation: 1, TrackerDroppedTruncation: 1, TrackerDroppedRange: 1}
	if dropped := tracker.Dropped(); !reflect.DeepEqual(dropped.Counts, want) || !reflect.DeepEqual(dropped.Recent, []string{"qg-2"}) {
		t.Fatalf("dropped = %+v, want %v naming qg-2 once", dropped, want)
	}
	tracker.Observe(context.Background(), completion("qg-3", "FULL_COMPLETED", "854"))
	tracker.Observe(context.Background(), completion("qg-2", "FULL_COMPLETED", "854"))
	dropped := tracker.Dropped()
	if dropped.Counts[TrackerDroppedObservation] != 3 || !reflect.DeepEqual(dropped.Recent, []string{"qg-3", "qg-2"}) {
		t.Fatalf("dropped = %+v, want three rounds lost and qg-2 newest", dropped)
	}
	for index := 0; index < MaxTrackerDroppedNamed+2; index++ {
		tracker.Observe(context.Background(), completion(fmt.Sprintf("qg-n%d", index), "FULL_COMPLETED", "854"))
	}
	dropped = tracker.Dropped()
	if len(dropped.Recent) != MaxTrackerDroppedNamed || dropped.Recent[0] != "qg-n2" || dropped.Recent[MaxTrackerDroppedNamed-1] != fmt.Sprintf("qg-n%d", MaxTrackerDroppedNamed+1) {
		t.Fatalf("named = %v, want the latest %d", dropped.Recent, MaxTrackerDroppedNamed)
	}
	if tracker.Tracked() != 1 {
		t.Fatalf("tracked = %d, want the bound kept", tracker.Tracked())
	}
}

// The table holds four times what the replica owns, never less than the
// default, so a replica that owns more than a quarter of the default does not
// start losing objects it owns.
func TestTheTableBoundFollowsWhatTheReplicaOwns(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	owned := func(n int) map[string]struct{} {
		out := make(map[string]struct{}, n)
		for index := 0; index < n; index++ {
			out[fmt.Sprintf("qg-%d", index)] = struct{}{}
		}
		return out
	}
	for _, c := range []struct{ owned, want int }{{0, DefaultTrackedQueryGroups}, {DefaultTrackedQueryGroups / 4, DefaultTrackedQueryGroups},
		{DefaultTrackedQueryGroups/4 + 1, DefaultTrackedQueryGroups + 4}, {5000, 20000}, {10, DefaultTrackedQueryGroups}} {
		tracker.Forget(owned(c.owned))
		if tracker.maxTracked != c.want {
			t.Fatalf("owning %d: bound %d, want %d", c.owned, tracker.maxTracked, c.want)
		}
	}
}

// The deployment sums what the replicas lost and names each object with its
// replica, within the bound; a replica that lost nothing adds nothing, and
// with none the answer is absent rather than zero.
func TestTheDeploymentSumsAndNamesWhatTheTablesDropped(t *testing.T) {
	var view View
	aggregateTrackerDropped(&view, []Snapshot{{Replica: "pod-a"}})
	if view.TrackerDropped != nil {
		t.Fatalf("view = %+v, want absent while nothing was lost", view.TrackerDropped)
	}
	a := &TrackerDropped{Counts: map[string]uint64{TrackerDroppedObservation: 2, TrackerDroppedRange: 1}, Recent: []string{"qg-1"}}
	b := &TrackerDropped{Counts: map[string]uint64{TrackerDroppedObservation: 3, TrackerDroppedTruncation: 4}, Recent: []string{"qg-2", "qg-3"}}
	aggregateTrackerDropped(&view, []Snapshot{{Replica: "pod-a", TrackerDropped: a}, {Replica: "pod-b"}, {Replica: "pod-c", TrackerDropped: b}})
	want := TrackerDropped{Counts: map[string]uint64{TrackerDroppedObservation: 5, TrackerDroppedRange: 1, TrackerDroppedTruncation: 4},
		Recent: []string{"pod-a/qg-1", "pod-c/qg-2", "pod-c/qg-3"}}
	if view.TrackerDropped == nil || !reflect.DeepEqual(*view.TrackerDropped, want) {
		t.Fatalf("view = %+v, want %+v", view.TrackerDropped, want)
	}
	many := &TrackerDropped{Counts: map[string]uint64{TrackerDroppedObservation: 1}}
	for index := 0; index < MaxTrackerDroppedNamed; index++ {
		many.Recent = append(many.Recent, fmt.Sprintf("qg-many%d", index))
	}
	aggregateTrackerDropped(&view, []Snapshot{{Replica: "pod-a", TrackerDropped: a}, {Replica: "pod-b", TrackerDropped: many}})
	if got := view.TrackerDropped.Recent; len(got) != MaxTrackerDroppedNamed || got[0] != "pod-b/qg-many0" {
		t.Fatalf("named = %v, want the bound kept", got)
	}
	health, _ := json.Marshal(healthOf(&view, ReplicaPart{}, now))
	if !strings.Contains(string(health), `"tracker_dropped":{"counts":{"observation":3,"range":1}`) {
		t.Fatalf("health = %s, want tracker_dropped", health)
	}
}
