package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// An object overdue at one publish and not at the last begins an episode
// with its read hold then; still overdue, nothing more; overdue no more, the
// episode ends and is kept, the latest fleet.MaxOverdueEpisodes of them, and
// the next snapshot carries them.
func TestTheFleetPublisherKeepsOverdueEpisodesWithTheirHold(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(1_791_400_000, 0)}
	type heard struct {
		episode fleet.OverdueEpisode
		began   bool
	}
	var events []heard
	publisher := fleetPublisher{
		tracker: fleet.NewTracker(nil, "replica-1", clock.now), replica: "replica-1", now: clock.now,
		owned: func() []execution.QueryGroupIdentity { return nil },
		holdOf: func(queryGroup string) (int64, bool) {
			if queryGroup == "qg-held" {
				return 308_000, true
			}
			return 0, queryGroup != "qg-unread"
		},
		onOverdue: func(episode fleet.OverdueEpisode, began bool) { events = append(events, heard{episode, began}) },
	}
	at := clock.now()
	overdue := []fleet.OverdueObject{{QueryGroup: "qg-held", IntervalSeconds: 60}, {QueryGroup: "qg-unread"}}
	publisher.noteOverdue(overdue, nil, at)
	publisher.noteOverdue(overdue, nil, at.Add(time.Minute))
	if len(events) != 2 || !events[0].began || !events[1].began {
		t.Fatalf("events %+v, want two beginnings and nothing for still overdue", events)
	}
	began := map[string]fleet.OverdueEpisode{}
	for _, event := range events {
		began[event.episode.QueryGroup] = event.episode
	}
	if held := began["qg-held"]; held.HoldClass() != "positive" || held.ReadHoldMillis != 308_000 || !held.Onset.Equal(at) || held.Replica != "replica-1" {
		t.Fatalf("held episode %+v", held)
	}
	if unread := began["qg-unread"]; unread.HoldClass() != "unknown" {
		t.Fatalf("unread episode %+v, want its hold unknown", unread)
	}
	publisher.noteOverdue(overdue[:1], nil, at.Add(2*time.Minute))
	if len(events) != 3 || events[2].began || events[2].episode.QueryGroup != "qg-unread" || !events[2].episode.Clear.Equal(at.Add(2*time.Minute)) {
		t.Fatalf("events %+v, want qg-unread ended at its clear", events)
	}
	if snapshot := publisher.snapshot(context.Background()); len(snapshot.OverdueEpisodes) != 1 || snapshot.OverdueEpisodes[0].QueryGroup != "qg-unread" {
		t.Fatalf("snapshot episodes %+v, want the ended one", snapshot.OverdueEpisodes)
	}
	for i := 0; i < fleet.MaxOverdueEpisodes+5; i++ {
		object := []fleet.OverdueObject{{QueryGroup: fmt.Sprintf("qg-%d", i)}}
		publisher.noteOverdue(append(object, overdue[0]), nil, at.Add(time.Duration(3+2*i)*time.Minute))
		publisher.noteOverdue(overdue[:1], nil, at.Add(time.Duration(4+2*i)*time.Minute))
	}
	kept := publisher.overdueEpisodes
	if len(kept) != fleet.MaxOverdueEpisodes || kept[len(kept)-1].QueryGroup != fmt.Sprintf("qg-%d", fleet.MaxOverdueEpisodes+4) {
		t.Fatalf("kept %d, last %+v, want the latest %d", len(kept), kept[len(kept)-1], fleet.MaxOverdueEpisodes)
	}
	if _, open := publisher.overdueOpen["qg-held"]; !open || len(publisher.overdueOpen) != 1 {
		t.Fatalf("open %+v, want only the object still overdue", publisher.overdueOpen)
	}
}

// The scrape exports the running strategies only from a view read whole:
// snapshots deferred under the memory line or unread leave no series, and so
// does a part from a build without the counts.
func TestTheVerdictExportsRunningStrategiesOnlyFromAViewReadWhole(t *testing.T) {
	at := time.Unix(1_791_400_000, 0)
	part := fleet.ReplicaPart{RunningStrategies: map[fleet.StateWord]int{fleet.StateDetecting: 120, fleet.StateDataAbsent: 30}}
	whole := fleet.View{Health: fleet.HealthHealthy, Replicas: []string{"pod-a", "pod-b"}}
	verdict := fleetVerdictOf(whole, part, at)
	if len(verdict.RunningStrategies) != len(fleet.StateWords) {
		t.Fatalf("running %+v, want every state", verdict.RunningStrategies)
	}
	for _, count := range verdict.RunningStrategies {
		if count.Value == string(fleet.StateDetecting) && count.Count != 120 {
			t.Fatalf("DETECTING %d, want 120", count.Count)
		}
	}
	for _, kind := range []fleet.GapKind{fleet.GapSnapshotsDeferred, fleet.GapSnapshotsUnreadable, fleet.GapReplicaMissing} {
		view := whole
		view.Gaps = []fleet.Gap{{Kind: kind}}
		if verdict := fleetVerdictOf(view, part, at); verdict.RunningStrategies != nil {
			t.Fatalf("%s: running %+v exported from a view not read whole", kind, verdict.RunningStrategies)
		}
	}
	if verdict := fleetVerdictOf(whole, fleet.ReplicaPart{}, at); verdict.RunningStrategies != nil {
		t.Fatalf("running %+v exported from a part without the counts", verdict.RunningStrategies)
	}
}

// The strategies evaluating on the objects a replica holds, each once.
func TestEvaluatingStrategiesAreEachStrategyOnce(t *testing.T) {
	refs := map[string][]fleet.StrategyRef{
		"qg-a": {{StrategyID: "9", BusinessID: "2"}, {StrategyID: "10", BusinessID: "2"}},
		"qg-b": {{StrategyID: "9", BusinessID: "2"}},
	}
	list := evaluatingStrategies([]execution.QueryGroupIdentity{"qg-a", "qg-b", "qg-none"}, func(qg string) []fleet.StrategyRef { return refs[qg] })
	if len(list) != 2 || list[0].StrategyID != "10" || list[1].StrategyID != "9" {
		t.Fatalf("list %+v, want 10 and 9 once each", list)
	}
}

// The snapshot says which strategies evaluate on the objects the replica
// holds, and says nothing of them without the tracker's names.
func TestTheFleetPublisherSaysWhichStrategiesEvaluate(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(1_791_400_000, 0)}
	publisher := fleetPublisher{
		tracker: fleet.NewTracker(nil, "replica-1", clock.now), replica: "replica-1", now: clock.now,
		owned: func() []execution.QueryGroupIdentity { return []execution.QueryGroupIdentity{"qg-a"} },
	}
	if snapshot := publisher.snapshot(context.Background()); snapshot.EvaluatingStrategiesKnown || len(snapshot.EvaluatingStrategies) != 0 {
		t.Fatalf("a publisher without strategy names said %+v (known %t)", snapshot.EvaluatingStrategies, snapshot.EvaluatingStrategiesKnown)
	}
	publisher.strategies = func(string) []fleet.StrategyRef { return []fleet.StrategyRef{{StrategyID: "4101", BusinessID: "2"}} }
	snapshot := publisher.snapshot(context.Background())
	if !snapshot.EvaluatingStrategiesKnown || len(snapshot.EvaluatingStrategies) != 1 || snapshot.EvaluatingStrategies[0].StrategyID != "4101" {
		t.Fatalf("snapshot strategies %+v (known %t)", snapshot.EvaluatingStrategies, snapshot.EvaluatingStrategiesKnown)
	}
}

// The rows may leave out an object still overdue only when they were cut:
// any object when a row column was cut to its budget; when the due index
// listed only its oldest wakes, an object whose wake the index holds a
// whole period late -- not one within its period, nor one it no longer
// holds. Rows not cut leave nothing out.
func TestTheRowsHideAnOverdueObjectOnlyWhenCut(t *testing.T) {
	at := time.Unix(1_791_400_000, 0)
	publisher := fleetPublisher{schedule: scriptedSchedule{
		"qg-late":     {Known: true, DueAt: at.Add(-2 * time.Minute), IntervalSeconds: 60},
		"qg-recent":   {Known: true, DueAt: at.Add(-30 * time.Second), IntervalSeconds: 60},
		"qg-noperiod": {Known: true, DueAt: at.Add(-time.Second)},
	}}
	whole := fleet.Snapshot{TakenAt: at, Overdue: &fleet.OverdueFacts{Total: 3}}
	if hidden := publisher.overdueHidden(whole, fleet.ReplicaPart{}); hidden != nil {
		t.Fatal("whole rows said an object may be hidden")
	}
	if hidden := publisher.overdueHidden(fleet.Snapshot{TakenAt: at}, fleet.ReplicaPart{}); hidden != nil {
		t.Fatal("rows without a due index said an object may be hidden")
	}
	if hidden := publisher.overdueHidden(whole, fleet.ReplicaPart{Truncated: 1}); hidden == nil || !hidden("qg-gone") || !hidden("qg-recent") {
		t.Fatal("rows with a column cut did not leave every object possibly hidden")
	}
	cut := whole
	cut.Overdue = &fleet.OverdueFacts{Total: 3, Truncated: true}
	hidden := publisher.overdueHidden(cut, fleet.ReplicaPart{})
	if hidden == nil {
		t.Fatal("rows from a cut list said nothing may be hidden")
	}
	for queryGroup, want := range map[string]bool{"qg-late": true, "qg-noperiod": false, "qg-recent": false, "qg-gone": false} {
		if got := hidden(queryGroup); got != want {
			t.Errorf("%s hidden %t, want %t", queryGroup, got, want)
		}
	}
	publisher.schedule = nil
	if hidden := publisher.overdueHidden(cut, fleet.ReplicaPart{}); hidden == nil || !hidden("qg-gone") {
		t.Fatal("a cut list with no index to ask did not leave every object possibly hidden")
	}
}

// Published, an object pushed off a cut overdue list while the index holds
// it overdue keeps its episode, and back on the list it is not counted
// again; one handed over meanwhile ends; a list whole again ends what is
// off it.
func TestAPublishFromACutOverdueListEndsOnlyWhatIsOverdueNoMore(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	store, err := fleet.NewRedisStore(client, "alarmd-overdue-cut", time.Minute, 0)
	if err != nil {
		t.Fatal(err)
	}
	clock := &dueIndexClock{at: time.Unix(1_791_400_000, 0)}
	at := clock.now()
	wake := func(queryGroup string, ago time.Duration) fleet.OverdueWake {
		return fleet.OverdueWake{QueryGroup: queryGroup, WakeAt: at.Add(-ago), IntervalSeconds: 60}
	}
	facts := func(ago time.Duration) fleet.WakeFacts {
		return fleet.WakeFacts{Known: true, DueAt: at.Add(-ago), IntervalSeconds: 60}
	}
	wakes := &stubWakeSource{wakes: []fleet.OverdueWake{wake("qg-a", 5*time.Minute), wake("qg-b", 4*time.Minute)}, total: 2}
	schedule := scriptedSchedule{"qg-a": facts(5 * time.Minute), "qg-b": facts(4 * time.Minute),
		"qg-c": facts(6 * time.Minute)}
	var began, ended []string
	publisher := fleetPublisher{
		tracker: fleet.NewTracker(nil, "replica-1", clock.now), replica: "replica-1", now: clock.now, store: store,
		owned: func() []execution.QueryGroupIdentity {
			return []execution.QueryGroupIdentity{"qg-a", "qg-b", "qg-c"}
		},
		overdue: wakes, schedule: schedule, staleAfter: 10 * time.Minute,
		onOverdue: func(episode fleet.OverdueEpisode, begun bool) {
			if begun {
				began = append(began, episode.QueryGroup)
			} else {
				ended = append(ended, episode.QueryGroup)
			}
		},
	}
	publisher.publishOnce(context.Background())
	if fmt.Sprint(began) != "[qg-a qg-b]" || len(ended) != 0 {
		t.Fatalf("began %v ended %v, want qg-a and qg-b begun", began, ended)
	}
	// qg-c, older, takes the head of a list cut to one; qg-a is handed over.
	wakes.wakes, wakes.total = []fleet.OverdueWake{wake("qg-c", 6*time.Minute)}, 3
	delete(schedule, "qg-a")
	publisher.publishOnce(context.Background())
	if fmt.Sprint(began) != "[qg-a qg-b qg-c]" || fmt.Sprint(ended) != "[qg-a]" {
		t.Fatalf("began %v ended %v, want qg-c begun and only qg-a ended", began, ended)
	}
	wakes.wakes, wakes.total = []fleet.OverdueWake{wake("qg-c", 6*time.Minute), wake("qg-b", 4*time.Minute)}, 2
	publisher.publishOnce(context.Background())
	if fmt.Sprint(began) != "[qg-a qg-b qg-c]" || fmt.Sprint(ended) != "[qg-a]" {
		t.Fatalf("began %v ended %v, want qg-b back on the list and not begun again", began, ended)
	}
	wakes.wakes, wakes.total = []fleet.OverdueWake{wake("qg-c", 6*time.Minute)}, 1
	publisher.publishOnce(context.Background())
	if fmt.Sprint(ended) != "[qg-a qg-b]" {
		t.Fatalf("ended %v, want qg-b ended once the list is whole", ended)
	}
}

// The snapshot lists, beside the read holds, a row for each object held on a
// measured arrival age, with its wake facts like every listed row; a hold
// that rests on no measurement is in the holds and has no row.
func TestTheFleetPublisherListsTheObjectsItHoldsTheReadsOf(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(1_791_400_000, 0)}
	tracker := fleet.NewTracker(nil, "replica-1", clock.now)
	for _, name := range []string{"qg-held", "qg-bound"} {
		tracker.Observe(context.Background(), observability.Observation{ProgressCompletionKind: "FULL_COMPLETED",
			Trace: observability.TraceFields{QueryGroupKey: name, StrategyID: "4101", BusinessID: "2", EvaluationTime: 1_791_399_940}})
	}
	holds := map[string]fleet.ReadHoldFacts{
		"qg-held":  {Millis: 99_000, ArrivalAgeMillis: 189_000, HeldSince: 1_791_396_000, DelaySeconds: 60, SuggestedDelaySeconds: 180},
		"qg-bound": {Millis: 600_000, HeldSince: 1_791_396_000, DelaySeconds: 60},
	}
	publisher := fleetPublisher{
		tracker: tracker, replica: "replica-1", now: clock.now,
		owned:     func() []execution.QueryGroupIdentity { return []execution.QueryGroupIdentity{"qg-held", "qg-bound"} },
		readHolds: func() map[string]fleet.ReadHoldFacts { return holds },
		schedule:  scriptedSchedule{"qg-held": {Known: true, DueAt: clock.now().Add(time.Minute), IntervalSeconds: 60}},
	}
	snapshot := publisher.snapshot(context.Background())
	if len(snapshot.ReadHolds) != 2 || len(snapshot.ReadHeld) != 1 {
		t.Fatalf("holds %+v rows %+v, want both holds and one row", snapshot.ReadHolds, snapshot.ReadHeld)
	}
	row := snapshot.ReadHeld[0]
	if row.QueryGroup != "qg-held" || row.Kind != fleet.KindReadHeld || row.Wake == nil || !row.Wake.Known || len(row.Strategies) != 1 {
		t.Fatalf("row %+v, want qg-held under its strategy with its wake facts", row)
	}
}

// A wake whose schedule was not read -- no period -- opens no overdue episode
// however late it is, and is counted as period unknown; the same object once
// its period is read and a whole period late opens one as any other.
func TestAWakeWithNoPeriodOpensNoOverdueEpisode(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	store, err := fleet.NewRedisStore(client, "alarmd-overdue-period", time.Minute, 0)
	if err != nil {
		t.Fatal(err)
	}
	clock := &dueIndexClock{at: time.Unix(1_791_400_000, 0)}
	at := clock.now()
	wakes := &stubWakeSource{wakes: []fleet.OverdueWake{{QueryGroup: "qg-unread", WakeAt: at.Add(-time.Hour)}}, total: 1}
	var began []string
	publisher := fleetPublisher{
		tracker: fleet.NewTracker(nil, "replica-1", clock.now), replica: "replica-1", now: clock.now, store: store,
		owned:   func() []execution.QueryGroupIdentity { return []execution.QueryGroupIdentity{"qg-unread"} },
		overdue: wakes, staleAfter: 10 * time.Minute,
		onOverdue: func(episode fleet.OverdueEpisode, begun bool) {
			if begun {
				began = append(began, episode.QueryGroup)
			}
		},
	}
	if snapshot := publisher.snapshot(context.Background()); snapshot.Overdue == nil || snapshot.Overdue.PeriodUnknown != 1 || snapshot.Overdue.Total != 0 {
		t.Fatalf("overdue facts %+v, want the wake counted as period unknown and none overdue", snapshot.Overdue)
	}
	publisher.publishOnce(context.Background())
	if len(began) != 0 {
		t.Fatalf("began %v, want no episode for a wake with no period", began)
	}
	wakes.wakes = []fleet.OverdueWake{{QueryGroup: "qg-unread", WakeAt: at.Add(-time.Hour), IntervalSeconds: 60}}
	publisher.publishOnce(context.Background())
	if fmt.Sprint(began) != "[qg-unread]" {
		t.Fatalf("began %v, want the episode once the period is read", began)
	}
}

// An object named without a period keeps when it was first seen so for as
// long as it is named, and while the index's list is cut and the index still
// holds it without one; once its period arrives it is forgotten, and named
// again it is new.
func TestAnObjectWithNoPeriodKeepsWhenItWasFirstSeenUntilItsPeriodArrives(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(1_791_400_000, 0)}
	start := clock.now()
	schedule := scriptedSchedule{"qg-unread": {Known: true, DueAt: start}}
	wakes := &stubWakeSource{wakes: []fleet.OverdueWake{{QueryGroup: "qg-unread", WakeAt: start}}, total: 1}
	publisher := fleetPublisher{
		tracker: fleet.NewTracker(nil, "replica-1", clock.now), replica: "replica-1", now: clock.now,
		owned:   func() []execution.QueryGroupIdentity { return []execution.QueryGroupIdentity{"qg-unread"} },
		overdue: wakes, schedule: schedule,
	}
	named := func() []fleet.PeriodUnknownObject {
		snapshot := publisher.snapshot(context.Background())
		if snapshot.Overdue == nil {
			t.Fatal("no overdue facts")
		}
		return snapshot.Overdue.PeriodUnknownObjects
	}
	if objects := named(); len(objects) != 1 || objects[0].QueryGroup != "qg-unread" || !objects[0].Since.Equal(start) {
		t.Fatalf("named %+v, want qg-unread since now", objects)
	}
	clock.at = clock.at.Add(10 * time.Minute)
	wakes.wakes = []fleet.OverdueWake{{QueryGroup: "qg-unread", WakeAt: clock.now()}}
	if objects := named(); len(objects) != 1 || !objects[0].Since.Equal(start) {
		t.Fatalf("named %+v, want qg-unread still since it was first seen", objects)
	}
	// Past a cut list, still without a period by the index: kept, not named.
	clock.at = clock.at.Add(time.Minute)
	wakes.wakes, wakes.total = nil, 60
	if objects := named(); len(objects) != 0 || !publisher.periodUnknownSince["qg-unread"].Equal(start) {
		t.Fatalf("named %+v remembered %v, want it kept past a cut list", objects, publisher.periodUnknownSince)
	}
	wakes.wakes, wakes.total = []fleet.OverdueWake{{QueryGroup: "qg-unread", WakeAt: clock.now()}}, 1
	if objects := named(); len(objects) != 1 || !objects[0].Since.Equal(start) {
		t.Fatalf("named %+v, want qg-unread back since it was first seen", objects)
	}
	// Its period arrives: forgotten; named again later, it is new.
	clock.at = clock.at.Add(time.Minute)
	schedule["qg-unread"] = fleet.WakeFacts{Known: true, DueAt: clock.now().Add(time.Minute), IntervalSeconds: 60}
	wakes.wakes, wakes.total = nil, 0
	if objects := named(); len(objects) != 0 || len(publisher.periodUnknownSince) != 0 {
		t.Fatalf("named %+v remembered %v, want it forgotten once its period arrived", objects, publisher.periodUnknownSince)
	}
	clock.at = clock.at.Add(time.Minute)
	wakes.wakes, wakes.total = []fleet.OverdueWake{{QueryGroup: "qg-unread", WakeAt: clock.now()}}, 1
	if objects := named(); len(objects) != 1 || !objects[0].Since.Equal(clock.now()) {
		t.Fatalf("named %+v, want qg-unread new since now", objects)
	}
}

// With more objects without a period than are published, every one is dated
// when first counted: when one of those published gets its period, the next
// one is published with when it was first counted, not with that moment.
func TestAnObjectPastThePublishedNamesIsDatedWhenFirstCounted(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(1_791_400_000, 0)}
	start := clock.now()
	wakesAt := func(skip string) []fleet.OverdueWake {
		var wakes []fleet.OverdueWake
		for index := 0; index < fleet.MaxPeriodUnknownObjects+5; index++ {
			if name := fmt.Sprintf("qg-%02d", index); name != skip {
				wakes = append(wakes, fleet.OverdueWake{QueryGroup: name, WakeAt: clock.now().Add(-time.Second)})
			}
		}
		return wakes
	}
	wakes := &stubWakeSource{wakes: wakesAt(""), total: fleet.MaxPeriodUnknownObjects + 5}
	publisher := fleetPublisher{
		tracker: fleet.NewTracker(nil, "replica-1", clock.now), replica: "replica-1", now: clock.now,
		owned:   func() []execution.QueryGroupIdentity { return nil },
		overdue: wakes,
	}
	first := publisher.snapshot(context.Background()).Overdue
	if first == nil || first.Truncated || first.PeriodUnknown != fleet.MaxPeriodUnknownObjects+5 || len(first.PeriodUnknownObjects) != fleet.MaxPeriodUnknownObjects {
		t.Fatalf("overdue %+v, want every one counted, the list whole and %d named", first, fleet.MaxPeriodUnknownObjects)
	}
	clock.at = clock.at.Add(10 * time.Minute)
	wakes.wakes, wakes.total = wakesAt("qg-00"), fleet.MaxPeriodUnknownObjects+4
	later := publisher.snapshot(context.Background()).Overdue
	since := map[string]time.Time{}
	for _, object := range later.PeriodUnknownObjects {
		since[object.QueryGroup] = object.Since
	}
	if len(later.PeriodUnknownObjects) != fleet.MaxPeriodUnknownObjects || !since["qg-20"].Equal(start) {
		t.Fatalf("named %+v, want qg-20 published with when it was first counted, %v", later.PeriodUnknownObjects, start)
	}
	if _, named := since["qg-00"]; named || len(publisher.periodUnknownSince) != fleet.MaxPeriodUnknownObjects+4 {
		t.Fatalf("qg-00 named %t, remembered %d, want it forgotten and the other %d remembered",
			named, len(publisher.periodUnknownSince), fleet.MaxPeriodUnknownObjects+4)
	}
}
