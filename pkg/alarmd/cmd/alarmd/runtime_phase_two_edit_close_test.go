package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/editclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
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

// editTestLink lists each strategy's alerts and serves each alert's record.
type editTestLink struct {
	alerts      map[string][]openalerts.Alert
	truncated   map[string]bool
	reconcileOK map[string]bool
	records     map[string]openalerts.AlertRecord
	reads       int
	listings    []string
}

func (link *editTestLink) Reconcile(_ context.Context, key openalerts.StrategyKey) (openalerts.Reconciliation, error) {
	link.listings = append(link.listings, key.StrategyID)
	if ok, stated := link.reconcileOK[key.StrategyID]; stated && !ok {
		return openalerts.Reconciliation{}, errors.New("listing failed")
	}
	return openalerts.Reconciliation{EventSourceID: "native", Alerts: link.alerts[key.StrategyID], Truncated: link.truncated[key.StrategyID]}, nil
}

func (link *editTestLink) AlertRecord(_ context.Context, _, alertID string) (openalerts.AlertRecord, error) {
	link.reads++
	record, ok := link.records[alertID]
	if !ok {
		return openalerts.AlertRecord{}, errors.New("record unreadable")
	}
	return record, nil
}

type editFixture struct {
	loop   *editStrategyClose
	plans  *editTestPlans
	link   *editTestLink
	writer *absentTestWriter
	roster map[absentalerts.Key]int
	bundle *phaseTwoWorkerBundle
}

// newEditFixture is strategy 10 at revision 9, grouping by ip and device,
// with the three built-in levels and no-data over ip.
func newEditFixture() *editFixture {
	fixture := &editFixture{
		plans:  &editTestPlans{facts: map[string]controlplane.StrategyPlanFacts{"10": editPlan()}},
		link:   &editTestLink{alerts: map[string][]openalerts.Alert{}, records: map[string]openalerts.AlertRecord{}},
		writer: &absentTestWriter{},
		roster: map[absentalerts.Key]int{{TenantID: "system", StrategyID: "10"}: 1},
	}
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	fixture.bundle = &phaseTwoWorkerBundle{dependencies: phaseTwoWorkerBundleDependencies{
		Now: func() time.Time { return now }, Observer: observability.NopObserver{}}}
	fixture.bundle.controlLeader = true
	fixture.loop = newEditStrategyClose(fixture.bundle, fixture.plans, fixture.link, fixture.writer)
	return fixture
}

func editPlan() controlplane.StrategyPlanFacts {
	return controlplane.StrategyPlanFacts{Revision: 9, HasOutputIdentity: true, DimensionFields: []string{"device", "ip"},
		Levels:      []contract.LevelDefinitionV2{{LevelID: 1}, {LevelID: 2}, {LevelID: 3}},
		LevelsWhole: true, NoData: true, NoDataAggDimension: []string{"ip"}, Agree: true}
}

// alert lists an alert of strategy and gives it a record raised under
// revision, at severity, in family, with the dimension names given.
func (fixture *editFixture) alert(strategy, id string, revision int64, severity, family string, dimensions ...string) {
	fingerprint := fmt.Sprintf("%032x", len(fixture.link.records)+1)
	fixture.link.alerts[strategy] = append(fixture.link.alerts[strategy], openalerts.Alert{AlertID: id, EventSourceID: "native", Fingerprint: fingerprint})
	fixture.link.records[id] = openalerts.AlertRecord{AlertID: id, TenantID: "system", EventSourceID: "native", Fingerprint: fingerprint,
		Status: "active", Severity: severity, StrategyID: strategy, BusinessID: 2, Revision: revision,
		DimensionKeys: dimensions, DimensionsRead: true, EvaluationFamily: family}
}

func (fixture *editFixture) threshold(id string, revision int64, severity string, dimensions ...string) {
	fixture.alert("10", id, revision, severity, "metric_algorithm", dimensions...)
}

func (fixture *editFixture) noData(id string, revision int64, dimensions ...string) {
	fixture.alert("10", id, revision, "warning", "no_data", append(dimensions, contract.NoDataDimensionTag)...)
}

func (fixture *editFixture) step() { fixture.loop.step(context.Background(), fixture.roster) }

// closed is every close sent, by alert id.
func (fixture *editFixture) closed() map[string]linkdoutput.CloseRequest {
	closes := map[string]linkdoutput.CloseRequest{}
	for _, batch := range fixture.writer.batches {
		for _, request := range batch {
			closes[request.AlertInstanceID] = request
		}
	}
	return closes
}

func (fixture *editFixture) wantClosed(t *testing.T, want map[string]string) {
	t.Helper()
	closes := fixture.closed()
	if len(closes) != len(want) {
		t.Fatalf("closed %+v, want exactly %v", closes, want)
	}
	for id, detail := range want {
		request, ok := closes[id]
		if !ok || request.Reason != linkdoutput.CloseReasonStrategyChanged || request.Detail != detail ||
			request.StrategyID != 10 || request.StrategyRevision != 9 || request.BusinessID != 2 ||
			request.Fingerprint != fixture.link.records[id].Fingerprint || request.TenantID != "system" {
			t.Fatalf("close of %s = %+v, want strategy_changed/%s under revision 9", id, request, detail)
		}
	}
}

// A grouping dimension removed: the alerts that carry it are closed, an
// alert raised under the current revision is never touched, and one whose
// dimensions the current grouping still names stays.
func TestAGroupingDimensionRemovedClosesTheAlertsThatCarryIt(t *testing.T) {
	fixture := newEditFixture()
	plan := editPlan()
	plan.DimensionFields = []string{"ip"}
	fixture.plans.facts["10"] = plan
	fixture.threshold("old-grouping", 8, "warning", "ip", "device")
	fixture.threshold("current-revision", 9, "warning", "ip", "device")
	fixture.threshold("still-grouped", 8, "warning", "ip")
	fixture.step()
	fixture.wantClosed(t, map[string]string{"old-grouping": "dimensions_changed"})
	stats := fixture.loop.Stats()
	if stats[editclose.OutcomeDimensionsChanged] != 1 || stats[editclose.OutcomeCurrentRevision] != 1 ||
		stats[editclose.OutcomeKept] != 1 || stats[editclose.OutcomeCloseSent] != 1 {
		t.Fatalf("stats %+v, want one dimensions_changed, one current_revision, one kept, one close_sent", stats)
	}
}

// A target edit moves the revision and leaves the grouping alone: nothing
// is closed, and the alerts it kept are counted as kept.
func TestATargetEditClosesNothing(t *testing.T) {
	fixture := newEditFixture()
	fixture.threshold("host-a", 8, "critical", "ip", "device", "bk_target_ip", "bk_target_cloud_id")
	fixture.threshold("host-b", 8, "warning", "ip", "device", "bk_host_id")
	fixture.noData("group-a", 8, "ip")
	fixture.step()
	fixture.wantClosed(t, map[string]string{})
	if kept := fixture.loop.Stats()[editclose.OutcomeKept]; kept != 3 {
		t.Fatalf("kept %d, want 3", kept)
	}
}

// A level removed with the others kept closes only that level's threshold
// alerts: not another level's, and not a no-data alert at the same level.
func TestALevelRemovedClosesOnlyThatLevelsThresholdAlerts(t *testing.T) {
	fixture := newEditFixture()
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
	fixture := newEditFixture()
	plan := editPlan()
	plan.NoData, plan.NoDataAggDimension = false, nil
	fixture.plans.facts["10"] = plan
	fixture.noData("group-a", 8, "ip")
	fixture.noData("group-b", 8, "ip")
	fixture.threshold("threshold", 8, "warning", "ip", "device")
	fixture.step()
	fixture.wantClosed(t, map[string]string{"group-a": "no_data_disabled", "group-b": "no_data_disabled"})

	suspended := newEditFixture()
	plan.NoDataSuspended = true
	suspended.plans.facts["10"] = plan
	suspended.noData("group-a", 8, "ip")
	suspended.step()
	suspended.wantClosed(t, map[string]string{})
	if suspended.loop.Stats()[editclose.OutcomeNoDataSuspended] != 1 {
		t.Fatalf("stats %+v, want the suspended no-data alert named", suspended.loop.Stats())
	}
}

// An alert whose record cannot be read is skipped and counted, the rest
// are decided, and the next round reads it again.
func TestAnUnreadableRecordIsSkippedCountedAndReadAgain(t *testing.T) {
	fixture := newEditFixture()
	plan := editPlan()
	plan.DimensionFields = []string{"ip"}
	fixture.plans.facts["10"] = plan
	fixture.threshold("unreadable", 8, "warning", "ip", "device")
	fixture.threshold("readable", 8, "warning", "ip", "device")
	record := fixture.link.records["unreadable"]
	delete(fixture.link.records, "unreadable")
	foreign := fixture.link.records["readable"]
	fixture.step()
	fixture.wantClosed(t, map[string]string{"readable": "dimensions_changed"})
	if fixture.loop.Stats()[editclose.OutcomeRecordUnreadable] != 1 {
		t.Fatalf("stats %+v, want one record_unreadable", fixture.loop.Stats())
	}
	// A record that is another strategy's is not this alert's either.
	foreign.StrategyID = "11"
	fixture.link.records["readable"] = foreign
	fixture.link.records["unreadable"] = record
	fixture.writer.batches = nil
	fixture.step()
	fixture.wantClosed(t, map[string]string{"unreadable": "dimensions_changed"})
	if fixture.loop.Stats()[editclose.OutcomeRecordUnreadable] != 2 {
		t.Fatalf("stats %+v, want a record of another strategy counted as unreadable", fixture.loop.Stats())
	}
}

// A truncated listing closes what it lists and says it was truncated.
func TestATruncatedListingClosesOnlyWhatItLists(t *testing.T) {
	fixture := newEditFixture()
	plan := editPlan()
	plan.DimensionFields = []string{"ip"}
	fixture.plans.facts["10"] = plan
	fixture.threshold("listed", 8, "warning", "ip", "device")
	fixture.link.records["unlisted"] = openalerts.AlertRecord{AlertID: "unlisted", TenantID: "system", EventSourceID: "native",
		StrategyID: "10", BusinessID: 2, Revision: 8, DimensionKeys: []string{"device", "ip"}, DimensionsRead: true, EvaluationFamily: "metric_algorithm"}
	fixture.link.truncated = map[string]bool{"10": true}
	fixture.step()
	fixture.wantClosed(t, map[string]string{"listed": "dimensions_changed"})
	if fixture.loop.Stats()[editclose.OutcomeEvidenceTruncated] != 1 {
		t.Fatalf("stats %+v, want the truncation named", fixture.loop.Stats())
	}
}

// Nothing is remembered about the edit itself: a process that starts after
// it - a new loop with nothing in memory - finds the same candidates from
// the alerts' revisions and closes them.
func TestARestartedLoopStillFindsTheCandidates(t *testing.T) {
	fixture := newEditFixture()
	plan := editPlan()
	plan.Levels = []contract.LevelDefinitionV2{{LevelID: 1}}
	fixture.plans.facts["10"] = plan
	fixture.threshold("at-warning", 8, "warning", "ip", "device")
	fixture.threshold("at-critical", 8, "critical", "ip", "device")
	fixture.step()
	fixture.wantClosed(t, map[string]string{"at-warning": "level_removed"})
	// The link has not dropped the closed alert yet when the process
	// restarts: the new loop, with nothing in memory, finds it again from
	// its revision, and reads the kept one again.
	fixture.writer.batches = nil
	reads := fixture.link.reads
	restarted := newEditStrategyClose(fixture.bundle, fixture.plans, fixture.link, fixture.writer)
	restarted.step(context.Background(), fixture.roster)
	fixture.wantClosed(t, map[string]string{"at-warning": "level_removed"})
	if fixture.link.reads != reads+2 || restarted.Stats()[editclose.OutcomeKept] != 1 {
		t.Fatalf("reads %d (were %d) stats %+v, want both records read again and the critical alert kept",
			fixture.link.reads, reads, restarted.Stats())
	}
}

// A still-firing alert the edit kept is read once against the current
// facts and decided from memory after that, counted kept every round; a
// change of the facts reads it again.
func TestAKeptAlertIsReadOncePerChangeOfTheFacts(t *testing.T) {
	fixture := newEditFixture()
	fixture.threshold("firing", 8, "warning", "ip", "device")
	fixture.step()
	fixture.step()
	stats := fixture.loop.Stats()
	if fixture.link.reads != 1 || stats[editclose.OutcomeKept] != 2 || stats[editclose.OutcomeRemembered] != 1 {
		t.Fatalf("reads %d stats %+v, want one read, kept twice, once from memory", fixture.link.reads, stats)
	}
	plan := editPlan()
	plan.Revision = 10
	fixture.plans.facts["10"] = plan
	fixture.step()
	if fixture.link.reads != 2 {
		t.Fatalf("reads %d, want the alert read again under the new facts", fixture.link.reads)
	}
}

// A round's record reads are bounded: what it does not reach is deferred,
// named, and reached by the next round.
func TestARoundsReadsAreBoundedAndTheRestDeferred(t *testing.T) {
	fixture := newEditFixture()
	plan := editPlan()
	plan.DimensionFields = []string{"ip"}
	fixture.plans.facts["10"] = plan
	for i := 0; i < editCloseRecordReads+3; i++ {
		fixture.threshold(fmt.Sprintf("alert-%04d", i), 8, "warning", "ip", "device")
	}
	fixture.step()
	if len(fixture.closed()) != editCloseRecordReads || fixture.loop.Stats()[editclose.OutcomeReadDeferred] != 3 {
		t.Fatalf("closed %d deferred %d, want %d closed and 3 deferred", len(fixture.closed()),
			fixture.loop.Stats()[editclose.OutcomeReadDeferred], editCloseRecordReads)
	}
	// The link would drop what was closed; the next round reaches the rest.
	fixture.link.alerts["10"] = fixture.link.alerts["10"][editCloseRecordReads:]
	fixture.step()
	if len(fixture.closed()) != editCloseRecordReads+3 {
		t.Fatalf("closed %d, want every alert closed by the second round", len(fixture.closed()))
	}
}

// A round lists at most editCloseStrategies strategies and the next starts
// after the last one it got through; a strategy whose listing fails does
// not hold up the others.
func TestStrategiesAreBoundedRotatedAndIsolated(t *testing.T) {
	fixture := newEditFixture()
	fixture.roster = map[absentalerts.Key]int{}
	for i := 0; i < editCloseStrategies+2; i++ {
		id := fmt.Sprintf("%d", 100+i)
		fixture.roster[absentalerts.Key{TenantID: "system", StrategyID: id}] = 1
		fixture.plans.facts[id] = editPlan()
	}
	fixture.link.reconcileOK = map[string]bool{"100": false}
	fixture.step()
	if len(fixture.link.listings) != editCloseStrategies || fixture.loop.Stats()[editclose.OutcomeEvidenceUnavailable] != 1 {
		t.Fatalf("listings %v stats %+v, want %d listed with one failing", fixture.link.listings, fixture.loop.Stats(), editCloseStrategies)
	}
	fixture.link.listings = nil
	fixture.step()
	if len(fixture.link.listings) == 0 || fixture.link.listings[0] != fmt.Sprintf("%d", 100+editCloseStrategies) {
		t.Fatalf("second round listed %v, want it to start after the last strategy the first got through", fixture.link.listings)
	}
}

// What cannot be decided is named and closes nothing: a follower, a leader
// without a publication, a strategy the publication does not have, Plans
// retained or disagreeing.
func TestWhatCannotBeDecidedIsNamed(t *testing.T) {
	setup := func() *editFixture {
		fixture := newEditFixture()
		plan := editPlan()
		plan.DimensionFields = []string{"ip"}
		fixture.plans.facts["10"] = plan
		fixture.threshold("old-grouping", 8, "warning", "ip", "device")
		return fixture
	}
	for word, change := range map[string]func(*editFixture){
		editclose.OutcomeNotLeader:              func(f *editFixture) { f.bundle.controlLeader = false },
		editclose.OutcomePublicationUnavailable: func(f *editFixture) { f.plans.unavailable = true },
		editclose.OutcomeRosterUnavailable:      func(f *editFixture) { f.roster = nil },
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
			fixture := setup()
			change(fixture)
			fixture.step()
			fixture.wantClosed(t, map[string]string{})
			if fixture.loop.Stats()[word] != 1 {
				t.Fatalf("stats %+v, want %s counted", fixture.loop.Stats(), word)
			}
		})
	}
	// The same alert closes once nothing stands in the way.
	fixture := setup()
	fixture.step()
	fixture.wantClosed(t, map[string]string{"old-grouping": "dimensions_changed"})
}

// The edit close runs on the absent close's round over the roster that
// round read.
func TestTheEditCloseRunsOverTheAbsentClosesRoster(t *testing.T) {
	absent := newAbsentFixture(t, nil)
	ctx := context.Background()
	absent.loop.step(ctx)
	if absent.loop.lastRoster == nil || absent.loop.lastRoster[absentalerts.Key{TenantID: "system", StrategyID: "10"}] != 1 {
		t.Fatalf("roster %+v, want the strategies the round read", absent.loop.lastRoster)
	}
	absent.loop.bundle.controlLeader = false
	absent.loop.step(ctx)
	if absent.loop.lastRoster != nil {
		t.Fatal("a roster read in a term that ended was kept")
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

// withFacts gives the alert's record the facts its opening event carried.
func (fixture *editFixture) withFacts(t *testing.T, id string, facts *contract.StrategyFactsV2) {
	t.Helper()
	encoded, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	record := fixture.link.records[id]
	record.StrategyFacts = encoded
	fixture.link.records[id] = record
}

const (
	cpuQuery    = `{"metric_id":"bk_monitor.system.cpu.usage","agg_condition":[{"key":"ip","value":["192.0.2.1"],"method":"eq"}]}`
	memoryQuery = `{"metric_id":"bk_monitor.system.mem.usage","agg_condition":[{"key":"ip","value":["192.0.2.1"],"method":"eq"}]}`
)

// A metric change closes every alert of the strategy, threshold and
// no-data alike.
func TestAMetricChangeClosesEveryAlertOfTheStrategy(t *testing.T) {
	fixture := newEditFixture()
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
	fixture := newEditFixture()
	plan := editPlan()
	plan.StrategyFacts = derivedFacts(t, memoryQuery)
	plan.DimensionFields = []string{"ip"}
	fixture.plans.facts["10"] = plan
	fixture.threshold("no-facts", 8, "critical", "ip")
	fixture.threshold("other-schema", 8, "critical", "ip")
	record := fixture.link.records["other-schema"]
	record.StrategyFacts = json.RawMessage(`{"schema":"v3","metric_digest":"x","condition_md5":"y"}`)
	fixture.link.records["other-schema"] = record
	fixture.threshold("no-facts-dimension-dropped", 8, "critical", "ip", "device")
	fixture.step()
	fixture.wantClosed(t, map[string]string{"no-facts-dimension-dropped": "dimensions_changed"})
	stats := fixture.loop.Stats()
	if stats[editclose.OutcomeFactsMissing] != 2 || stats[editclose.OutcomeFactsUnknownSchema] != 1 || stats[editclose.OutcomeKept] != 2 {
		t.Fatalf("stats %+v, want two facts_missing, one facts_unknown_schema and two kept", stats)
	}
	// Remembered decisions keep their note. The link has dropped the alert
	// that was closed.
	fixture.link.alerts["10"] = fixture.link.alerts["10"][:2]
	fixture.step()
	if stats := fixture.loop.Stats(); stats[editclose.OutcomeFactsMissing] != 3 || stats[editclose.OutcomeFactsUnknownSchema] != 2 {
		t.Fatalf("stats %+v, want the remembered decisions counted with their notes", stats)
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
	fixture := newEditFixture()
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
	fixture := newEditFixture()
	plan := editPlan()
	plan.DimensionFields = []string{"device", "ip"}
	plan.StrategyFacts = groupedFacts(t, []string{"device", "ip"}, []string{"ip"})
	fixture.plans.facts["10"] = plan
	fixture.threshold("threshold", 8, "critical", "ip", "device")
	fixture.withEventFacts(t, "threshold", groupedFacts(t, []string{"ip", "device"}, []string{"ip"}), false)
	fixture.step()
	fixture.wantClosed(t, map[string]string{})
	if fixture.loop.Stats()[editclose.OutcomeKept] != 1 {
		t.Fatalf("stats %+v, want the alert kept", fixture.loop.Stats())
	}
}

// Without facts, a dimension added is not decided and is counted; a
// dimension removed still closes by the alert's own dimension names.
func TestWithoutFactsOnlyARemovedDimensionCloses(t *testing.T) {
	added := newEditFixture()
	plan := editPlan()
	plan.DimensionFields = []string{"device", "ip", "nic"}
	plan.StrategyFacts = groupedFacts(t, plan.DimensionFields, []string{"ip"})
	added.plans.facts["10"] = plan
	added.threshold("threshold", 8, "critical", "ip", "device")
	added.step()
	added.wantClosed(t, map[string]string{})
	if stats := added.loop.Stats(); stats[editclose.OutcomeKept] != 1 || stats[editclose.OutcomeFactsMissing] != 1 {
		t.Fatalf("stats %+v, want the alert kept and counted facts_missing", stats)
	}

	removed := newEditFixture()
	plan.DimensionFields = []string{"ip"}
	plan.StrategyFacts = groupedFacts(t, plan.DimensionFields, []string{"ip"})
	removed.plans.facts["10"] = plan
	removed.threshold("threshold", 8, "critical", "ip", "device")
	removed.step()
	removed.wantClosed(t, map[string]string{"threshold": "dimensions_changed"})

	// A ProcPort alert carries the ports its record found beside its
	// identity; an edit that changed nothing of either keeps it.
	procPort := newEditFixture()
	plan = editPlan()
	plan.DimensionFields = []string{"bk_target_cloud_id", "bk_target_ip", "display_name"}
	plan.RecordDimensionFields = []string{"bind_ip", "listen", "nonlisten", "not_accurate_listen", "protocol"}
	plan.StrategyFacts = groupedFacts(t, plan.DimensionFields, []string{"ip"})
	procPort.plans.facts["10"] = plan
	procPort.threshold("threshold", 8, "critical", "bind_ip", "bk_target_cloud_id", "bk_target_ip", "display_name",
		"listen", "nonlisten", "not_accurate_listen", "protocol")
	procPort.step()
	procPort.wantClosed(t, map[string]string{})
	if stats := procPort.loop.Stats(); stats[editclose.OutcomeKept] != 1 {
		t.Fatalf("stats %+v, want the ProcPort alert kept", stats)
	}
}

// A listed alert that names no producer is not decided, even where its
// record says it is this process's: the listing is what binds an alert to
// the source, and one that does not is counted, not closed.
func TestAListedAlertWithoutAProducerIsNotDecided(t *testing.T) {
	fixture := newEditFixture()
	plan := editPlan()
	plan.DimensionFields = []string{"ip"}
	fixture.plans.facts["10"] = plan
	fixture.threshold("threshold", 8, "critical", "ip", "device")
	fixture.link.alerts["10"][0].EventSourceID = ""
	fixture.step()
	fixture.wantClosed(t, map[string]string{})
	if stats := fixture.loop.Stats(); stats[editclose.OutcomeProducerUnknown] != 1 || stats[editclose.OutcomeDimensionsChanged] != 0 {
		t.Fatalf("stats %+v, want producer_unknown counted and nothing decided", stats)
	}
}

// The condition rule is held: a condition change closes nothing, and the
// no-data alerts it would have closed are counted condition_rule_held.
// Threshold alerts are not its to judge.
func TestAConditionChangeIsHeldAndClosesNothing(t *testing.T) {
	fixture := newEditFixture()
	plan := editPlan()
	plan.StrategyFacts = derivedFacts(t, `{"metric_id":"bk_monitor.system.cpu.usage","agg_condition":[{"key":"ip","value":["192.0.2.2"],"method":"eq"}]}`)
	fixture.plans.facts["10"] = plan
	fixture.threshold("threshold", 8, "critical", "ip", "device")
	fixture.noData("no-data", 8, "ip")
	fixture.withEventFacts(t, "threshold", derivedFacts(t, cpuQuery), false)
	fixture.withEventFacts(t, "no-data", derivedFacts(t, cpuQuery), true)
	fixture.step()
	fixture.wantClosed(t, map[string]string{})
	if stats := fixture.loop.Stats(); stats[editclose.OutcomeConditionRuleHeld] != 1 || stats[editclose.OutcomeKept] != 2 {
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
			fixture := newEditFixture()
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
			if held := fixture.loop.Stats()[editclose.OutcomeConditionRuleHeld]; held != test.held {
				t.Fatalf("condition_rule_held %d, want %d", held, test.held)
			}
		})
	}
}
