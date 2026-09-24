package state

import (
	"context"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// What a stored record has to give back, stated over the whole point rather
// than over the parts a particular representation happens to keep.
//
// This is the case decision-021 needs and did not have. The measurements that
// argued for changing the representation compared byte counts and point
// counts, and a representation can match on both while dropping a field
// inside the point: the packed codec in this package keeps the source time and
// the usable/anomalous bits, and a case looking at those alone sees a clean
// round trip while the detect fingerprint and the difference between an
// unusable Level's two kinds are gone. Both are read back - the fingerprint by
// the load-time contract check (execution/model.go:2658), the exact fact by the
// result contract (execution/result_contract.go:553) - so neither is spare.
//
// Stated through ApplyRuntime and LoadRuntime because that is where a
// representation lives. Asserted with the whole StateHistoryPoint, because an
// assertion naming the fields it expects to survive cannot report the loss of
// a field it does not name.
func TestALoadedRecordGivesBackEveryPointItWasGiven(t *testing.T) {
	backend := newPipelineMemoryBackend()
	store := newBatchStore(t, backend, nil)
	version := applyVersion()
	at := int64(version.EvaluationTime)

	// Every result kind a Level fact may carry, including a point no Level
	// found usable. The packed representation cannot store that point at all,
	// and cannot tell the two unusable kinds apart, so a fixture carrying only
	// NORMAL would pass over both losses.
	identity := seriesIdentity(0)
	points := []execution.StateHistoryPoint{
		derivedPoint(t, identity, at, "detect", execution.LevelFactNormal),
		derivedPoint(t, identity, at+60, "detect", execution.LevelFactAnomalous),
		derivedPoint(t, identity, at+120, "detect", execution.LevelFactError),
		derivedPoint(t, identity, at+180, "detect", execution.LevelFactUnavailable),
	}
	mutation, err := execution.BuildStateMutation(execution.StateMutation{
		Identity: identity, ExpectedBlobRevision: 0, ApplyVersion: version,
		AffectedRecords: []execution.RecordAnchor{derivedAnchor(t, identity, at)},
		Levels: []execution.RuntimeLevelStateMutation{{LevelID: 1, LevelStateCompatibility: "compat",
			HistoryCompleteness: execution.HistoryFull, WarmupRequirementRef: "warm", LastProcessedEventTime: at}},
		Points: points,
	})
	if err != nil {
		t.Fatalf("build mutation: %v", err)
	}

	applied, err := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{
		Contract: frozenRef(), Retention: testRetention(), Items: []execution.StateMutation{mutation},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	requireAllStatus(t, applied, execution.StateApplied)

	loaded, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{
		Contract: frozenRef(), Items: preflightItems([]execution.StateMutation{mutation}),
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded.Items) != 1 {
		t.Fatalf("loaded %d items, want 1", len(loaded.Items))
	}
	view := loaded.Items[0]

	// Asked before the history is counted, because a record the load path
	// refused comes back with an empty history too, and reporting that as
	// "points went missing" names the wrong thing: the fields inside the point
	// are load-bearing enough that dropping one can cost the whole record, and
	// the case should say which of the two happened.
	if view.Status != execution.StateFoundReady {
		t.Fatalf("the record came back %s (%s) rather than readable; it was refused on load, not merely "+
			"returned short", view.Status, view.ReasonCode)
	}
	if len(view.History) != len(points) {
		t.Fatalf("wrote %d points and read back %d; a point every Level found unusable is still a point the "+
			"result contract reads to justify carrying an unknown outcome forward", len(points), len(view.History))
	}
	for index, want := range points {
		if got := view.History[index]; !reflect.DeepEqual(got, want) {
			t.Errorf("point %d came back as %+v, wrote %+v", index, got, want)
		}
	}
}
