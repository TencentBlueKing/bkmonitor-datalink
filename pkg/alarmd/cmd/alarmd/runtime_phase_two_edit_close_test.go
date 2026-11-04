package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/editclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/filterclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

type editTestPlans struct {
	unavailable bool
	facts       map[string]controlplane.StrategyPlanFacts
}

func (plans *editTestPlans) StrategyPlanFacts(_, strategyID string) (controlplane.StrategyPlanFacts, bool, bool) {
	if plans.unavailable {
		return controlplane.StrategyPlanFacts{}, false, false
	}
	facts, found := plans.facts[strategyID]
	return facts, true, found
}

type editFixture struct {
	loop     *editStrategyClose
	plans    *editTestPlans
	link     *fakeLinkd
	snapshot map[absentalerts.Key]struct{}
	bundle   *phaseTwoWorkerBundle
	alerts   int
}

// newEditFixture is strategy 10 at revision 9, grouping by ip and device,
// with the three built-in levels and no-data over ip, closed through the
// production filter route against the fake link.
func newEditFixture(t *testing.T) *editFixture {
	t.Helper()
	fixture := &editFixture{
		plans:    &editTestPlans{facts: map[string]controlplane.StrategyPlanFacts{"10": editPlan()}},
		link:     newFakeLinkd(t),
		snapshot: map[absentalerts.Key]struct{}{{TenantID: "system", StrategyID: "10"}: {}},
	}
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	fixture.bundle = &phaseTwoWorkerBundle{dependencies: phaseTwoWorkerBundleDependencies{
		Now: func() time.Time { return now }, Observer: observability.NopObserver{}}}
	fixture.bundle.controlLeader = true
	fixture.loop = newEditStrategyClose(fixture.bundle, fixture.plans, newFakeLinkdRoute(t, fixture.link))
	return fixture
}

func editPlan() controlplane.StrategyPlanFacts {
	return controlplane.StrategyPlanFacts{Revision: 9, HasOutputIdentity: true, DimensionFields: []string{"device", "ip"},
		Levels:      []contract.LevelDefinitionV2{{LevelID: 1}, {LevelID: 2}, {LevelID: 3}},
		LevelsWhole: true, NoData: true, NoDataAggDimension: []string{"ip"}, Agree: true}
}

// alert holds an active alert of strategy, opened under revision, at
// severity, in family, with the dimension names given, and no facts digest
// (so the closer reads and decides it on its own record).
func (fixture *editFixture) alert(strategy, id string, revision int64, severity, family string, dimensions ...string) {
	fixture.alerts++
	values := map[string]string{}
	for _, name := range dimensions {
		values[name] = "value"
	}
	extra, _ := json.Marshal(map[string]string{"evaluation_family": family})
	fixture.link.add(&fakeLinkdAlert{id: id, fingerprint: fmt.Sprintf("%032x", fixture.alerts), severity: severity,
		labels:     map[string]string{"strategy_id": strategy, "strategy_version": fmt.Sprint(revision)},
		dimensions: values, extra: extra})
}

// setFacts puts facts beside the alert's evaluation family.
func (fixture *editFixture) setFacts(id string, facts json.RawMessage) {
	fixture.link.mu.Lock()
	defer fixture.link.mu.Unlock()
	alert := fixture.link.alerts[id]
	var extra map[string]json.RawMessage
	_ = json.Unmarshal(alert.extra, &extra)
	extra["strategy_facts"] = facts
	alert.extra, _ = json.Marshal(extra)
}

func (fixture *editFixture) threshold(id string, revision int64, severity string, dimensions ...string) {
	fixture.alert("10", id, revision, severity, "metric_algorithm", dimensions...)
}

func (fixture *editFixture) noData(id string, revision int64, dimensions ...string) {
	fixture.alert("10", id, revision, "warning", "no_data", append(dimensions, contract.NoDataDimensionTag)...)
}

func (fixture *editFixture) step() { fixture.loop.step(context.Background(), fixture.snapshot) }

// decisions is the closer's edit decisions and notes, by editclose word.
func (fixture *editFixture) decisions() map[string]uint64 {
	out := map[string]uint64{}
	for word, n := range fixture.loop.filter.closer.Stats() {
		if strings.HasPrefix(word, "edit_") {
			out[strings.TrimPrefix(word, "edit_")] = n
		}
	}
	return out
}

// closed is every closed alert's close reason, by alert id.
func (fixture *editFixture) closed() map[string]string {
	fixture.link.mu.Lock()
	defer fixture.link.mu.Unlock()
	closes := map[string]string{}
	for id, alert := range fixture.link.alerts {
		if alert.closes > 0 {
			closes[id] = alert.reason
		}
	}
	return closes
}

// wantClosed is the alerts closed, each once, under the strategy-changed
// reason naming strategy 10 and the detail.
func (fixture *editFixture) wantClosed(t *testing.T, want map[string]string) {
	t.Helper()
	closes := fixture.closed()
	if len(closes) != len(want) {
		t.Fatalf("closed %+v, want exactly %v", closes, want)
	}
	counts := fixture.link.closed()
	for id, detail := range want {
		reason, ok := closes[id]
		if !ok || counts[id] != 1 || !strings.Contains(reason, "strategy 10 changed ("+detail+")") {
			t.Fatalf("close of %s = %q (%d times), want strategy 10 changed (%s) once", id, reason, counts[id], detail)
		}
	}
}

// A grouping dimension removed: the alerts that carry it are closed, an
// alert raised under the current revision is never read, and one whose
// dimensions the current grouping still names stays.
func TestAGroupingDimensionRemovedClosesTheAlertsThatCarryIt(t *testing.T) {
	fixture := newEditFixture(t)
	plan := editPlan()
	plan.DimensionFields = []string{"ip"}
	fixture.plans.facts["10"] = plan
	fixture.threshold("old-grouping", 8, "warning", "ip", "device")
	fixture.threshold("current-revision", 9, "warning", "ip", "device")
	fixture.threshold("still-grouped", 8, "warning", "ip")
	fixture.step()
	fixture.wantClosed(t, map[string]string{"old-grouping": "dimensions_changed"})
	stats := fixture.decisions()
	if stats[editclose.OutcomeDimensionsChanged] != 1 || stats[editclose.OutcomeCurrentRevision] != 0 || stats[editclose.OutcomeKept] != 1 {
		t.Fatalf("stats %+v, want one dimensions_changed, one kept, and the current revision not read", stats)
	}
}

// A target edit moves the revision and leaves the grouping alone: nothing
// is closed, and the alerts it kept are counted as kept.
func TestATargetEditClosesNothing(t *testing.T) {
	fixture := newEditFixture(t)
	fixture.threshold("host-a", 8, "critical", "ip", "device", "bk_target_ip", "bk_target_cloud_id")
	fixture.threshold("host-b", 8, "warning", "ip", "device", "bk_host_id")
	fixture.noData("group-a", 8, "ip")
	fixture.step()
	fixture.wantClosed(t, map[string]string{})
	if kept := fixture.decisions()[editclose.OutcomeKept]; kept != 3 {
		t.Fatalf("kept %d, want 3", kept)
	}
}

// A level removed with the others kept closes only that level's threshold
// alerts: not another level's, and not a no-data alert at the same level.
func TestALevelRemovedClosesOnlyThatLevelsThresholdAlerts(t *testing.T) {
	fixture := newEditFixture(t)
	plan := editPlan()
	plan.Levels = []contract.LevelDefinitionV2{{LevelID: 1}, {LevelID: 3}}
	fixture.plans.facts["10"] = plan
	fixture.threshold("at-warning", 8, "warning", "ip", "device")
	fixture.threshold("at-critical", 8, "critical", "ip", "device")
	fixture.threshold("at-info", 8, "info", "ip", "device")
	fixture.noData("no-data-at-warning", 8, "ip")
	fixture.step()
	fixture.wantClosed(t, map[string]string{"at-warning": "level_removed"})
}

// No-data switched off closes the no-data alerts and leaves the threshold
// alerts; configured and not attached is not switched off.
func TestNoDataDisabledClosesNoDataAlertsAndLeavesThresholdAlerts(t *testing.T) {
	fixture := newEditFixture(t)
	plan := editPlan()
	plan.NoData, plan.NoDataAggDimension = false, nil
	fixture.plans.facts["10"] = plan
	fixture.noData("group-a", 8, "ip")
	fixture.noData("group-b", 8, "ip")
	fixture.threshold("threshold", 8, "warning", "ip", "device")
	fixture.step()
	fixture.wantClosed(t, map[string]string{"group-a": "no_data_disabled", "group-b": "no_data_disabled"})

	suspended := newEditFixture(t)
	plan.NoDataSuspended = true
	suspended.plans.facts["10"] = plan
	suspended.noData("group-a", 8, "ip")
	suspended.step()
	suspended.wantClosed(t, map[string]string{})
	if suspended.decisions()[editclose.OutcomeNoDataSuspended] != 1 {
		t.Fatalf("stats %+v, want the suspended no-data alert named", suspended.decisions())
	}
}

// Nothing is remembered about the edit itself: a process that starts after
// it - a new loop and closer with nothing in memory - finds the alerts
// still open from their versions, reads the kept one again, and closes
// nothing twice.
func TestARestartedLoopStillFindsTheCandidates(t *testing.T) {
	fixture := newEditFixture(t)
	plan := editPlan()
	plan.Levels = []contract.LevelDefinitionV2{{LevelID: 1}}
	fixture.plans.facts["10"] = plan
	fixture.threshold("at-warning", 8, "warning", "ip", "device")
	fixture.threshold("at-critical", 8, "critical", "ip", "device")
	fixture.step()
	fixture.wantClosed(t, map[string]string{"at-warning": "level_removed"})
	restarted := newEditStrategyClose(fixture.bundle, fixture.plans, newFakeLinkdRoute(t, fixture.link))
	restarted.step(context.Background(), fixture.snapshot)
	fixture.wantClosed(t, map[string]string{"at-warning": "level_removed"})
	if kept := restarted.filter.closer.Stats()["edit_"+editclose.OutcomeKept]; kept != 1 {
		t.Fatalf("the restarted closer kept %d, want the critical alert read again and kept", kept)
	}
}

// A strategy a round found nothing of another version at is remembered
// clean at that revision and not searched again until the revision moves;
// one with a kept alert of an older version is searched every round.
func TestACleanStrategyIsSearchedOncePerRevision(t *testing.T) {
	clean := newEditFixture(t)
	clean.threshold("current", 9, "warning", "ip", "device")
	clean.step()
	clean.step()
	if searches := len(clean.link.requests("/api/v1/alerts/search")); searches != 1 || clean.loop.Stats()[editclose.OutcomeRemembered] != 1 {
		t.Fatalf("searches %d stats %+v, want one search and the second round from memory", searches, clean.loop.Stats())
	}
	plan := editPlan()
	plan.Revision = 10
	clean.plans.facts["10"] = plan
	clean.step()
	if searches := len(clean.link.requests("/api/v1/alerts/search")); searches <= 1 {
		t.Fatalf("searches %d, want the strategy searched again under the new revision", searches)
	}

	kept := newEditFixture(t)
	kept.threshold("firing", 8, "warning", "ip", "device")
	kept.step()
	before := len(kept.link.requests("/api/v1/alerts/search"))
	kept.step()
	if after := len(kept.link.requests("/api/v1/alerts/search")); after <= before || kept.decisions()[editclose.OutcomeKept] != 2 {
		t.Fatalf("searches %d then %d, kept %d: want the older version read again each round", before, after, kept.decisions()[editclose.OutcomeKept])
	}
}

// A round examines at most editFilterStrategies strategies and the next
// starts after the last one it got through; a strategy whose search fails
// does not hold up the others.
func TestStrategiesAreBoundedRotatedAndIsolated(t *testing.T) {
	fixture := newEditFixture(t)
	fixture.snapshot = map[absentalerts.Key]struct{}{}
	for i := 0; i < editFilterStrategies+2; i++ {
		id := fmt.Sprintf("%d", 100+i)
		fixture.snapshot[absentalerts.Key{TenantID: "system", StrategyID: id}] = struct{}{}
		fixture.plans.facts[id] = editPlan()
	}
	fixture.link.searchFails = func(body string) bool { return strings.Contains(body, `"labels.strategy_id":"100"`) }
	searched := func() []string {
		var ids []string
		seen := map[string]bool{}
		for _, body := range fixture.link.requests("/api/v1/alerts/search") {
			at := strings.Index(body, `"labels.strategy_id":"`)
			rest := body[at+len(`"labels.strategy_id":"`):]
			id := rest[:strings.Index(rest, `"`)]
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		return ids
	}
	fixture.step()
	first := searched()
	if len(first) != editFilterStrategies || first[0] != "100" || fixture.loop.filter.closer.Stats()[filterclose.OutcomeLinkFailed] != 1 {
		t.Fatalf("first round searched %d strategies from %v, closer %+v: want %d with the first failing and the rest searched",
			len(first), first[:1], fixture.loop.filter.closer.Stats(), editFilterStrategies)
	}
	fixture.step()
	all := searched()
	if len(all) != editFilterStrategies+2 || all[editFilterStrategies] != fmt.Sprintf("%d", 100+editFilterStrategies) {
		t.Fatalf("second round searched %v after the first, want it to start after the last strategy the first got through",
			all[editFilterStrategies:])
	}
}

// What cannot be decided is named and closes nothing: a follower, a leader
// without a publication or a snapshot, a strategy the publication does not
// have, Plans retained, disagreeing or unrevisioned.
func TestWhatCannotBeDecidedIsNamed(t *testing.T) {
	setup := func(t *testing.T) *editFixture {
		fixture := newEditFixture(t)
		plan := editPlan()
		plan.DimensionFields = []string{"ip"}
		fixture.plans.facts["10"] = plan
		fixture.threshold("old-grouping", 8, "warning", "ip", "device")
		return fixture
	}
	for word, change := range map[string]func(*editFixture){
		editclose.OutcomeNotLeader:              func(f *editFixture) { f.bundle.controlLeader = false },
		editclose.OutcomePublicationUnavailable: func(f *editFixture) { f.plans.unavailable = true },
		editclose.OutcomeRosterUnavailable:      func(f *editFixture) { f.snapshot = nil },
		editclose.OutcomeStrategyUnpublished:    func(f *editFixture) { delete(f.plans.facts, "10") },
		editclose.OutcomePlanRetained: func(f *editFixture) {
			plan := f.plans.facts["10"]
			plan.Retained = true
			f.plans.facts["10"] = plan
		},
		editclose.OutcomePlansDisagree: func(f *editFixture) {
			plan := f.plans.facts["10"]
			plan.Agree = false
			f.plans.facts["10"] = plan
		},
		editclose.OutcomePlanUnrevisioned: func(f *editFixture) {
			plan := f.plans.facts["10"]
			plan.Revision = 0
			f.plans.facts["10"] = plan
		},
	} {
		t.Run(word, func(t *testing.T) {
			fixture := setup(t)
			change(fixture)
			fixture.step()
			fixture.wantClosed(t, map[string]string{})
			if fixture.loop.Stats()[word] != 1 {
				t.Fatalf("stats %+v, want %s counted", fixture.loop.Stats(), word)
			}
		})
	}
	// The same alert closes once nothing stands in the way.
	fixture := setup(t)
	fixture.step()
	fixture.wantClosed(t, map[string]string{"old-grouping": "dimensions_changed"})
}

// A link that cannot search ends the round: counted filter_close_unsupported
// by the closer, nothing closed, and no other path tried.
func TestALinkThatCannotSearchEndsTheEditRound(t *testing.T) {
	fixture := newEditFixture(t)
	fixture.snapshot[absentalerts.Key{TenantID: "system", StrategyID: "11"}] = struct{}{}
	fixture.plans.facts["11"] = editPlan()
	plan := editPlan()
	plan.DimensionFields = []string{"ip"}
	fixture.plans.facts["10"] = plan
	fixture.threshold("old-grouping", 8, "warning", "ip", "device")
	fixture.link.unsupported = true
	fixture.step()
	fixture.wantClosed(t, map[string]string{})
	if searches := len(fixture.link.requests("/api/v1/alerts/search")); searches != 1 ||
		fixture.loop.filter.closer.Stats()[filterclose.OutcomeUnsupported] != 1 {
		t.Fatalf("searches %d closer %+v, want one search, unsupported once, the round ended", searches, fixture.loop.filter.closer.Stats())
	}
}

// The edit close runs on the absent close's round over the snapshot that
// round read, and a term that ends drops it.
func TestTheEditCloseRunsOverTheAbsentClosesSnapshot(t *testing.T) {
	absent := newAbsentFixture(t, nil)
	ctx := context.Background()
	absent.loop.step(ctx)
	if _, held := absent.loop.lastSnapshot[absentalerts.Key{TenantID: "system", StrategyID: "live-1"}]; !held || len(absent.loop.lastSnapshot) != 100 {
		t.Fatalf("snapshot %d strategies, want the strategies the round read", len(absent.loop.lastSnapshot))
	}
	absent.loop.bundle.controlLeader = false
	absent.loop.step(ctx)
	if absent.loop.lastSnapshot != nil {
		t.Fatal("a snapshot read in a term that ended was kept")
	}
}

// derivedFacts is the strategy facts of one query config, derived the way
// the catalog derives them.
func derivedFacts(t *testing.T, queryConfig string) *contract.StrategyFactsV2 {
	t.Helper()
	facts, err := contract.DeriveStrategyFactsV2([]json.RawMessage{json.RawMessage(queryConfig)},
		contract.StrategyGrouping{Fields: []string{"ip", "device"}, NoData: true, NoDataFields: []string{"ip"}})
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

// withFacts gives the alert the facts its opening event carried, in its
// extra data as the link stores them.
func (fixture *editFixture) withFacts(t *testing.T, id string, facts *contract.StrategyFactsV2) {
	t.Helper()
	encoded, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	fixture.setFacts(id, encoded)
}

const (
	cpuQuery    = `{"metric_id":"bk_monitor.system.cpu.usage","agg_condition":[{"key":"ip","value":["192.0.2.1"],"method":"eq"}]}`
	memoryQuery = `{"metric_id":"bk_monitor.system.mem.usage","agg_condition":[{"key":"ip","value":["192.0.2.1"],"method":"eq"}]}`
)

// A metric change closes every alert of the strategy, threshold and
// no-data alike.
func TestAMetricChangeClosesEveryAlertOfTheStrategy(t *testing.T) {
	fixture := newEditFixture(t)
	plan := editPlan()
	plan.StrategyFacts = derivedFacts(t, memoryQuery)
	fixture.plans.facts["10"] = plan
	fixture.threshold("threshold", 8, "critical", "ip", "device")
	fixture.noData("no-data", 8, "ip")
	fixture.threshold("current", 9, "critical", "ip", "device")
	for _, id := range []string{"threshold", "current"} {
		fixture.withEventFacts(t, id, derivedFacts(t, cpuQuery), false)
	}
	fixture.withEventFacts(t, "no-data", derivedFacts(t, cpuQuery), true)
	fixture.step()
	fixture.wantClosed(t, map[string]string{"threshold": "metric_changed", "no-data": "metric_changed"})
}

// An alert whose opening event carried no strategy facts, or facts of a
// schema this build does not read, is not decided by the metric and
// condition rules; it is counted, and the other rules still apply.
func TestMissingOrUnknownFactsAreSkippedAndCounted(t *testing.T) {
	fixture := newEditFixture(t)
	plan := editPlan()
	plan.StrategyFacts = derivedFacts(t, memoryQuery)
	plan.DimensionFields = []string{"ip"}
	fixture.plans.facts["10"] = plan
	fixture.threshold("no-facts", 8, "critical", "ip")
	fixture.threshold("other-schema", 8, "critical", "ip")
	fixture.setFacts("other-schema", json.RawMessage(`{"schema":"v3","metric_digest":"x","condition_md5":"y"}`))
	fixture.threshold("no-facts-dimension-dropped", 8, "critical", "ip", "device")
	fixture.step()
	fixture.wantClosed(t, map[string]string{"no-facts-dimension-dropped": "dimensions_changed"})
	stats := fixture.decisions()
	if stats[editclose.OutcomeFactsMissing] != 2 || stats[editclose.OutcomeFactsUnknownSchema] != 1 || stats[editclose.OutcomeKept] != 2 {
		t.Fatalf("stats %+v, want two facts_missing, one facts_unknown_schema and two kept", stats)
	}
	// The next round reads the two kept alerts again, with their notes; the
	// closed one is no longer active and is not read.
	fixture.step()
	if stats := fixture.decisions(); stats[editclose.OutcomeFactsMissing] != 3 || stats[editclose.OutcomeFactsUnknownSchema] != 2 {
		t.Fatalf("stats %+v, want the kept alerts counted again with their notes", stats)
	}
}

func groupedFacts(t *testing.T, fields []string, noDataFields []string) *contract.StrategyFactsV2 {
	t.Helper()
	facts, err := contract.DeriveStrategyFactsV2([]json.RawMessage{json.RawMessage(cpuQuery)},
		contract.StrategyGrouping{Fields: fields, NoData: true, NoDataFields: noDataFields})
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

// withEventFacts gives the alert's record the facts its opening event
// carried: the Plan's, with the grouping of the event's own series.
func (fixture *editFixture) withEventFacts(t *testing.T, id string, plan *contract.StrategyFactsV2, noData bool) {
	t.Helper()
	facts := plan.ForEvent(noData)
	fixture.withFacts(t, id, &facts)
}

// A dimension added to the grouping closes the alerts whose facts state the
// grouping they were raised under, threshold and no-data alike.
func TestADimensionAddedToTheGroupingClosesTheAlertsThatCarryFacts(t *testing.T) {
	fixture := newEditFixture(t)
	plan := editPlan()
	plan.DimensionFields = []string{"device", "ip", "nic"}
	plan.NoDataAggDimension = []string{"device", "ip"}
	plan.StrategyFacts = groupedFacts(t, plan.DimensionFields, plan.NoDataAggDimension)
	fixture.plans.facts["10"] = plan
	raisedUnder := groupedFacts(t, []string{"ip", "device"}, []string{"ip"})
	fixture.threshold("threshold", 8, "critical", "ip", "device")
	fixture.withEventFacts(t, "threshold", raisedUnder, false)
	fixture.noData("no-data", 8, "ip")
	fixture.withEventFacts(t, "no-data", raisedUnder, true)
	fixture.step()
	fixture.wantClosed(t, map[string]string{"threshold": "dimensions_changed", "no-data": "dimensions_changed"})
}

// The same key set listed in another order is no change.
func TestAReorderedGroupingIsNoChange(t *testing.T) {
	fixture := newEditFixture(t)
	plan := editPlan()
	plan.DimensionFields = []string{"device", "ip"}
	plan.StrategyFacts = groupedFacts(t, []string{"device", "ip"}, []string{"ip"})
	fixture.plans.facts["10"] = plan
	fixture.threshold("threshold", 8, "critical", "ip", "device")
	fixture.withEventFacts(t, "threshold", groupedFacts(t, []string{"ip", "device"}, []string{"ip"}), false)
	fixture.step()
	fixture.wantClosed(t, map[string]string{})
	if fixture.decisions()[editclose.OutcomeKept] != 1 {
		t.Fatalf("stats %+v, want the alert kept", fixture.decisions())
	}
}

// Without facts, a dimension added is not decided and is counted; a
// dimension removed still closes by the alert's own dimension names.
func TestWithoutFactsOnlyARemovedDimensionCloses(t *testing.T) {
	added := newEditFixture(t)
	plan := editPlan()
	plan.DimensionFields = []string{"device", "ip", "nic"}
	plan.StrategyFacts = groupedFacts(t, plan.DimensionFields, []string{"ip"})
	added.plans.facts["10"] = plan
	added.threshold("threshold", 8, "critical", "ip", "device")
	added.step()
	added.wantClosed(t, map[string]string{})
	if stats := added.decisions(); stats[editclose.OutcomeKept] != 1 || stats[editclose.OutcomeFactsMissing] != 1 {
		t.Fatalf("stats %+v, want the alert kept and counted facts_missing", stats)
	}

	removed := newEditFixture(t)
	plan.DimensionFields = []string{"ip"}
	plan.StrategyFacts = groupedFacts(t, plan.DimensionFields, []string{"ip"})
	removed.plans.facts["10"] = plan
	removed.threshold("threshold", 8, "critical", "ip", "device")
	removed.step()
	removed.wantClosed(t, map[string]string{"threshold": "dimensions_changed"})

	// A ProcPort alert carries the ports its record found beside its
	// identity; an edit that changed nothing of either keeps it.
	procPort := newEditFixture(t)
	plan = editPlan()
	plan.DimensionFields = []string{"bk_target_cloud_id", "bk_target_ip", "display_name"}
	plan.RecordDimensionFields = []string{"bind_ip", "listen", "nonlisten", "not_accurate_listen", "protocol"}
	plan.StrategyFacts = groupedFacts(t, plan.DimensionFields, []string{"ip"})
	procPort.plans.facts["10"] = plan
	procPort.threshold("threshold", 8, "critical", "bind_ip", "bk_target_cloud_id", "bk_target_ip", "display_name",
		"listen", "nonlisten", "not_accurate_listen", "protocol")
	procPort.step()
	procPort.wantClosed(t, map[string]string{})
	if stats := procPort.decisions(); stats[editclose.OutcomeKept] != 1 {
		t.Fatalf("stats %+v, want the ProcPort alert kept", stats)
	}
}

// Another producer's alert of the strategy is never read: every search
// names this deployment's source, so it is neither decided nor closed.
func TestAnotherProducersAlertIsNotRead(t *testing.T) {
	fixture := newEditFixture(t)
	plan := editPlan()
	plan.DimensionFields = []string{"ip"}
	fixture.plans.facts["10"] = plan
	fixture.threshold("threshold", 8, "critical", "ip", "device")
	fixture.link.mu.Lock()
	fixture.link.alerts["threshold"].source = "another-source"
	fixture.link.mu.Unlock()
	fixture.step()
	fixture.wantClosed(t, map[string]string{})
	if stats := fixture.decisions(); stats[editclose.OutcomeDimensionsChanged] != 0 || stats[editclose.OutcomeKept] != 0 {
		t.Fatalf("stats %+v, want nothing decided", stats)
	}
}

// The condition rule is held: a condition change closes nothing, and the
// no-data alerts it would have closed are counted condition_rule_held.
// Threshold alerts are not its to judge.
func TestAConditionChangeIsHeldAndClosesNothing(t *testing.T) {
	fixture := newEditFixture(t)
	plan := editPlan()
	plan.StrategyFacts = derivedFacts(t, `{"metric_id":"bk_monitor.system.cpu.usage","agg_condition":[{"key":"ip","value":["192.0.2.2"],"method":"eq"}]}`)
	fixture.plans.facts["10"] = plan
	fixture.threshold("threshold", 8, "critical", "ip", "device")
	fixture.noData("no-data", 8, "ip")
	fixture.withEventFacts(t, "threshold", derivedFacts(t, cpuQuery), false)
	fixture.withEventFacts(t, "no-data", derivedFacts(t, cpuQuery), true)
	fixture.step()
	fixture.wantClosed(t, map[string]string{})
	if stats := fixture.decisions(); stats[editclose.OutcomeConditionRuleHeld] != 1 || stats[editclose.OutcomeKept] != 2 {
		t.Fatalf("stats %+v, want one condition_rule_held and both alerts kept", stats)
	}
}

// What the held rule would compare: the first condition's connector is not
// a change (the platform's rule), the second's is; a catalog metric's merged
// conditions changing alone (object_filter) is not, its user conditions are;
// a query holding a publisher-injected key is named query_injected.
func TestTheHeldConditionRuleComparesTheUsersConditions(t *testing.T) {
	before := `{"metric_id":"m","agg_condition":[{"key":"ip","value":["192.0.2.1"],"method":"eq","condition":"and"},` +
		`{"condition":"or","key":"device","value":["eth0"],"method":"eq"}]}`
	catalog := func(merged, user string) string {
		return `{"metric_id":"m","agg_condition":[{"key":"ip","value":["192.0.2.1"],"method":"eq"},{"condition":"and","key":"model_id","method":"eq","value":["` +
			merged + `"]}],"metric_snapshot":{"metric_id":7,"agg_condition":[{"key":"ip","value":["` + user + `"],"method":"eq"}]}}`
	}
	disk := func(types string) string {
		return `{"metric_id":"m","agg_condition":[{"key":"device_type","method":"neq","value":[` + types + `]}]}`
	}
	for name, test := range map[string]struct {
		raised, current string
		held            uint64
		basis           string
	}{
		"first connector": {before, `{"metric_id":"m","agg_condition":[{"key":"ip","value":["192.0.2.1"],"method":"eq","condition":"or"},` +
			`{"condition":"or","key":"device","value":["eth0"],"method":"eq"}]}`, 0, "query"},
		"second connector": {before, `{"metric_id":"m","agg_condition":[{"key":"ip","value":["192.0.2.1"],"method":"eq","condition":"and"},` +
			`{"condition":"and","key":"device","value":["eth0"],"method":"eq"}]}`, 1, "query"},
		"object_filter only":    {catalog("host", "192.0.2.1"), catalog("vm", "192.0.2.1"), 0, "snapshot_user"},
		"user condition":        {catalog("host", "192.0.2.1"), catalog("host", "192.0.2.2"), 1, "snapshot_user"},
		"publisher's exclusion": {disk(`"tmpfs"`), disk(`"tmpfs","overlay"`), 1, "query_injected"},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newEditFixture(t)
			plan := editPlan()
			plan.StrategyFacts = derivedFacts(t, test.current)
			if plan.StrategyFacts.ConditionBasis != test.basis {
				t.Fatalf("basis %s, want %s", plan.StrategyFacts.ConditionBasis, test.basis)
			}
			fixture.plans.facts["10"] = plan
			fixture.noData("no-data", 8, "ip")
			fixture.withEventFacts(t, "no-data", derivedFacts(t, test.raised), true)
			fixture.step()
			fixture.wantClosed(t, map[string]string{})
			if held := fixture.decisions()[editclose.OutcomeConditionRuleHeld]; held != test.held {
				t.Fatalf("condition_rule_held %d, want %d", held, test.held)
			}
		})
	}
}
