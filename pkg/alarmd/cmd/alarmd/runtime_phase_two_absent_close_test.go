package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

type absentTestControl struct {
	snapshot    controlplane.ObservedSnapshot
	haveSnaphot bool
	departed    []controlplane.DepartedStrategy
	refused     uint64
	capability  map[string]string
}

func (control *absentTestControl) ObservedSnapshot() (controlplane.ObservedSnapshot, bool) {
	return control.snapshot, control.haveSnaphot
}
func (control *absentTestControl) DepartedStrategies() ([]controlplane.DepartedStrategy, uint64) {
	return control.departed, control.refused
}
func (control *absentTestControl) CapabilityRefused() map[string]string {
	return control.capability
}

// absentTestLink is the alert link: a roster in pages, one strategy's
// alerts, and each alert's record.
type absentTestLink struct {
	pages     []openalerts.RosterPage
	rosterErr error
	// failAfter fails every page after the first n.
	failAfter int
	alerts    []openalerts.Alert
	alertsErr error
	records   map[string]openalerts.AlertRecord
	reads     int
	// perRound, when set, is how many pages a round has time for: the page
	// after them fails as the round's deadline would. served counts this
	// round's pages; the test resets it between rounds.
	perRound, served int
}

func (link *absentTestLink) Roster(_ context.Context, cursor string) (openalerts.RosterPage, error) {
	if link.rosterErr != nil {
		return openalerts.RosterPage{}, link.rosterErr
	}
	if link.perRound > 0 {
		if link.served >= link.perRound {
			return openalerts.RosterPage{}, context.DeadlineExceeded
		}
		link.served++
	}
	index := 0
	if cursor != "" {
		index = int(cursor[0] - '0')
	}
	if link.failAfter > 0 && index >= link.failAfter {
		return openalerts.RosterPage{}, errors.New("page failed")
	}
	return link.pages[index], nil
}

func (link *absentTestLink) Reconcile(context.Context, openalerts.StrategyKey) (openalerts.Reconciliation, error) {
	if link.alertsErr != nil {
		return openalerts.Reconciliation{}, link.alertsErr
	}
	return openalerts.Reconciliation{EventSourceID: "native", Alerts: link.alerts}, nil
}

func (link *absentTestLink) AlertRecord(_ context.Context, _, alertID string) (openalerts.AlertRecord, error) {
	link.reads++
	record, ok := link.records[alertID]
	if !ok {
		return openalerts.AlertRecord{}, openalerts.ErrAlertNotFound
	}
	return record, nil
}

type absentTestWriter struct {
	batches [][]linkdoutput.CloseRequest
	err     error
	// onWrite, when set, runs after a batch is written.
	onWrite func()
}

func (w *absentTestWriter) WriteCloseBatch(_ context.Context, requests []linkdoutput.CloseRequest) error {
	if w.err != nil {
		return w.err
	}
	w.batches = append(w.batches, append([]linkdoutput.CloseRequest(nil), requests...))
	if w.onWrite != nil {
		w.onWrite()
	}
	return nil
}

// liveSnapshot is a source observation of the given size that never lists
// the deleted strategy.
func liveSnapshot(observation string, at time.Time, size int) controlplane.ObservedSnapshot {
	snapshot := controlplane.ObservedSnapshot{Observation: observation, ReadAt: at}
	for i := 0; i < size; i++ {
		snapshot.Strategies = append(snapshot.Strategies, controlplane.DepartedStrategy{
			TenantID: "system", StrategyID: "live-" + itoaAbsent(i), BusinessID: 2})
	}
	return snapshot
}

func itoaAbsent(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}

func members(n int) *int { return &n }

type absentTestFixture struct {
	loop    *absentStrategyClose
	control *absentTestControl
	writer  *absentTestWriter
	link    *absentTestLink
	now     time.Time
}

// An empty strategy list under the writer's statement is a fact: the loop
// decides on it and, after its own grace and under a second observation,
// closes the alerts of a strategy that is gone. Without the statement the
// same empty list is refused as snapshot_empty and closes nothing.
func TestAnEmptyListIsDecidedOnOnlyUnderTheWritersStatement(t *testing.T) {
	for _, stated := range []bool{true, false} {
		fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("alert-1", "0123456789abcdef0123456789abcdef")})
		ctx := context.Background()
		fixture.control.snapshot = liveSnapshot("observation-one", fixture.now, 0)
		fixture.control.snapshot.HoldsLastGood = stated
		fixture.loop.step(ctx)
		fixture.now = fixture.now.Add(absentCloseGrace + time.Minute)
		fixture.control.snapshot = liveSnapshot("observation-two", fixture.now, 0)
		fixture.control.snapshot.HoldsLastGood = stated
		for i := range fixture.link.pages {
			fixture.link.pages[i].Health.LastSuccess = fixture.now.Add(-time.Minute)
		}
		fixture.loop.step(ctx)
		rounds := fixture.loop.Rounds()
		if stated {
			if rounds[absentalerts.RefusalNone] != 2 || rounds[absentalerts.RefusalSnapshotEmpty] != 0 ||
				len(fixture.writer.batches) == 0 || fixture.loop.Stats()[absentalerts.OutcomeCloseDecided] == 0 {
				t.Fatalf("empty list under the statement: rounds %+v stats %+v batches %+v, want decided and closed",
					rounds, fixture.loop.Stats(), fixture.writer.batches)
			}
			continue
		}
		if rounds[absentalerts.RefusalSnapshotEmpty] != 2 || rounds[absentalerts.RefusalNone] != 0 || len(fixture.writer.batches) != 0 {
			t.Fatalf("empty list without the statement: rounds %+v batches %+v, want refused as snapshot_empty", rounds, fixture.writer.batches)
		}
	}
}

// newAbsentFixture is a deployment where strategy 10 was deleted before
// this process ever saw it: the link lists it with an open alert, the
// snapshot does not list it, and nothing in the catalog remembers it. Its
// identity is on its alerts' records.
func newAbsentFixture(t *testing.T, alerts []openalerts.Alert) *absentTestFixture {
	t.Helper()
	start := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	fixture := &absentTestFixture{now: start}
	fixture.control = &absentTestControl{snapshot: liveSnapshot("observation-one", start, 100), haveSnaphot: true}
	fixture.writer = &absentTestWriter{}
	health := openalerts.LinkHealth{LastSuccess: start.Add(-time.Minute), LastAttempt: start.Add(-time.Minute)}
	fixture.link = &absentTestLink{
		pages: []openalerts.RosterPage{
			{Rows: []openalerts.RosterRow{{TenantID: "system", StrategyID: "live-1", Members: members(3)}}, Next: "1", Health: health},
			{Rows: []openalerts.RosterRow{{TenantID: "system", StrategyID: "10", Members: members(1)}}, Health: health},
		},
		alerts:  alerts,
		records: map[string]openalerts.AlertRecord{},
	}
	for _, alert := range alerts {
		fixture.link.records[alert.AlertID] = openalerts.AlertRecord{AlertID: alert.AlertID, TenantID: "system",
			EventSourceID: alert.EventSourceID, Fingerprint: alert.Fingerprint, Status: "active",
			StrategyID: "10", BusinessID: 2, Revision: 7}
	}
	bundle := &phaseTwoWorkerBundle{dependencies: phaseTwoWorkerBundleDependencies{
		Now: func() time.Time { return fixture.now }, Observer: observability.NopObserver{}}}
	bundle.controlLeader = true
	fixture.loop = newAbsentStrategyClose(bundle, fixture.control, fixture.link, fixture.writer)
	return fixture
}

// mature runs the first round, then a round after the grace under a second
// observation of the source: the earliest a close can be sent.
func (fixture *absentTestFixture) mature(ctx context.Context) {
	fixture.loop.step(ctx)
	fixture.now = fixture.now.Add(absentCloseGrace + time.Minute)
	fixture.control.snapshot = liveSnapshot("observation-two", fixture.now, 100)
	for i := range fixture.link.pages {
		fixture.link.pages[i].Health.LastSuccess = fixture.now.Add(-time.Minute)
	}
	fixture.loop.step(ctx)
}

// The link's reconciliation carries no severity; the alerts here have none.
func nativeAlert(id, fingerprint string) openalerts.Alert {
	return openalerts.Alert{AlertID: id, EventSourceID: "native", Fingerprint: fingerprint}
}

// The whole capability, end to end: a strategy deleted before this process
// saw it, still holding an alert this deployment produced, gets it closed -
// with the identity read from the alert's own record, at every level, and
// not on the first round or against one observation.
func TestTheAlertsOfAStrategyDeletedBeforeThisProcessSawItAreClosed(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("alert-1", "0123456789abcdef0123456789abcdef")})
	ctx := context.Background()

	fixture.loop.step(ctx)
	if len(fixture.writer.batches) != 0 || fixture.loop.Stats()[absentalerts.OutcomeWithinGrace] != 1 {
		t.Fatalf("the first round closed, or did not say why it did not: %+v", fixture.loop.Stats())
	}
	fixture.now = fixture.now.Add(absentCloseGrace + time.Minute)
	for i := range fixture.link.pages {
		fixture.link.pages[i].Health.LastSuccess = fixture.now.Add(-time.Minute)
	}
	fixture.loop.step(ctx)
	if len(fixture.writer.batches) != 0 || fixture.loop.Stats()[absentalerts.OutcomeUnconfirmed] != 1 {
		t.Fatalf("one observation confirmed an absence: %+v", fixture.loop.Stats())
	}
	fixture.control.snapshot = liveSnapshot("observation-two", fixture.now, 100)
	fixture.loop.step(ctx)
	if len(fixture.writer.batches) != 1 || len(fixture.writer.batches[0]) != 1 {
		t.Fatalf("the alert of a deleted strategy was not closed: %+v", fixture.writer.batches)
	}
	request := fixture.writer.batches[0][0]
	if request.Reason != linkdoutput.CloseReasonAbsent || request.StrategyID != 10 ||
		request.BusinessID != 2 || request.StrategyRevision != 7 {
		t.Fatalf("the close lost its reason or the identity on the alert's record: %+v", request)
	}
	event, err := linkdoutput.ConvertClose(request)
	if err != nil {
		t.Fatalf("the close this loop builds is not a close the contract accepts: %v", err)
	}
	var wire struct {
		Evaluations []struct{ Severity, Action string } `json:"evaluations"`
	}
	if err := json.Unmarshal(event.Payload, &wire); err != nil || len(wire.Evaluations) != 1 || wire.Evaluations[0].Severity != linkdoutput.SeverityAllLevels {
		t.Fatalf("a strategy close has to go out at every level: %s", event.Payload)
	}
	if difference := fixture.loop.Difference(); difference["roster_strategies"] != 2 ||
		difference["roster_pages"] != 2 || difference["roster_complete"] != 1 || difference["candidates"] != 1 {
		t.Fatalf("the round's sizes are not what it read: %+v", difference)
	}
}

// A strategy that still exists and that its publication refused for a
// capability: its alert is closed as no longer detected by alarmd - closed,
// naming the capability - after the grace under two observations, as an
// absent strategy's is. The same strategy refused for nothing keeps its
// alert, and one refused for a round and supported the next starts its
// grace again when it is refused once more.
func TestTheAlertsOfAStrategyRefusedForACapabilityAreClosedAsNoLongerDetected(t *testing.T) {
	listed := func(observation string, at time.Time) controlplane.ObservedSnapshot {
		snapshot := liveSnapshot(observation, at, 100)
		snapshot.Strategies = append(snapshot.Strategies, controlplane.DepartedStrategy{TenantID: "system", StrategyID: "10", BusinessID: 2})
		return snapshot
	}
	newRefusedFixture := func(capability map[string]string) *absentTestFixture {
		fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("alert-1", "0123456789abcdef0123456789abcdef")})
		fixture.control.snapshot = listed("observation-one", fixture.now)
		fixture.control.capability = capability
		return fixture
	}
	advance := func(fixture *absentTestFixture, observation string) {
		fixture.now = fixture.now.Add(absentCloseGrace + time.Minute)
		fixture.control.snapshot = listed(observation, fixture.now)
		for i := range fixture.link.pages {
			fixture.link.pages[i].Health.LastSuccess = fixture.now.Add(-time.Minute)
		}
	}
	ctx := context.Background()

	refused := newRefusedFixture(map[string]string{"10": "QUERY_SOURCE_RETIRED"})
	refused.loop.step(ctx)
	if len(refused.writer.batches) != 0 {
		t.Fatalf("the first round closed: %+v", refused.writer.batches)
	}
	advance(refused, "observation-two")
	refused.loop.step(ctx)
	if len(refused.writer.batches) != 1 || len(refused.writer.batches[0]) != 1 {
		t.Fatalf("the alert of a refused strategy was not closed: %+v (stats %+v)", refused.writer.batches, refused.loop.Stats())
	}
	request := refused.writer.batches[0][0]
	if request.Reason != linkdoutput.CloseReasonUnsupported || request.Detail != "QUERY_SOURCE_RETIRED" || request.StrategyID != 10 {
		t.Fatalf("close = %+v, want strategy_unsupported naming the capability", request)
	}
	event, err := linkdoutput.ConvertClose(request)
	if err != nil {
		t.Fatalf("the close is not one the contract accepts: %v", err)
	}
	var wire struct {
		Content     string `json:"content"`
		Evaluations []struct {
			Action       string `json:"action"`
			ActionReason string `json:"action_reason"`
		} `json:"evaluations"`
	}
	if err := json.Unmarshal(event.Payload, &wire); err != nil || len(wire.Evaluations) != 1 ||
		wire.Evaluations[0].Action != linkdoutput.ActionClosed || wire.Evaluations[0].ActionReason != linkdoutput.CloseReasonUnsupported ||
		!strings.Contains(wire.Content, "QUERY_SOURCE_RETIRED") {
		t.Fatalf("event = %s, want closed as strategy_unsupported with the capability in the content", event.Payload)
	}

	page, ok := refused.loop.Page(ctx, fleet.AbsentCandidateQuery{Limit: 10})
	if !ok || len(page.Rows) != 1 || page.Rows[0].StrategyID != "10" || page.Rows[0].RefusedFor != "QUERY_SOURCE_RETIRED" {
		t.Fatalf("absent.list rows = %+v, want strategy 10 naming the capability it was refused for", page.Rows)
	}

	supported := newRefusedFixture(nil)
	supported.loop.step(ctx)
	advance(supported, "observation-two")
	supported.loop.step(ctx)
	if len(supported.writer.batches) != 0 {
		t.Fatalf("a listed strategy refused for nothing was closed: %+v", supported.writer.batches)
	}
	// Refused for the deployment's configuration - a change lifts it - the
	// strategy keeps its alerts, with the refusal named on its object.
	reversible := newRefusedFixture(map[string]string{"10": "COMPLETION_OFFSET_BELOW_RESERVE"})
	reversible.loop.step(ctx)
	advance(reversible, "observation-two")
	reversible.loop.step(ctx)
	if len(reversible.writer.batches) != 0 {
		t.Fatalf("a strategy refused for a reversible configuration was closed: %+v", reversible.writer.batches)
	}

	flapping := newRefusedFixture(map[string]string{"10": "QUERY_SOURCE_RETIRED"})
	flapping.loop.step(ctx)
	advance(flapping, "observation-two")
	flapping.control.capability = nil
	flapping.loop.step(ctx)
	advance(flapping, "observation-three")
	flapping.control.capability = map[string]string{"10": "QUERY_SOURCE_RETIRED"}
	flapping.loop.step(ctx)
	if len(flapping.writer.batches) != 0 {
		t.Fatalf("a strategy supported in between was closed on its earlier refusal: %+v", flapping.writer.batches)
	}
}

// What the catalog remembers is used without asking the link.
func TestARememberedIdentityIsNotReadAgain(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("alert-1", "0123456789abcdef0123456789abcdef")})
	fixture.control.departed = []controlplane.DepartedStrategy{{TenantID: "system", StrategyID: "10", BusinessID: 5, Revision: 9}}
	fixture.mature(context.Background())
	if len(fixture.writer.batches) != 1 || fixture.writer.batches[0][0].BusinessID != 5 || fixture.link.reads != 0 {
		t.Fatalf("the remembered identity was not used, or the link was asked anyway: %+v reads=%d", fixture.writer.batches, fixture.link.reads)
	}
}

// A record that is not this deployment's alert of this strategy is not an
// identity for it. With no other, the strategy is named, not guessed.
func TestARecordOfAnotherStrategyIsNotAnIdentity(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("alert-1", "0123456789abcdef0123456789abcdef")})
	record := fixture.link.records["alert-1"]
	record.StrategyID = "11"
	fixture.link.records["alert-1"] = record
	fixture.mature(context.Background())
	if len(fixture.writer.batches) != 0 || fixture.loop.Stats()[absentalerts.OutcomeIdentityUnknown] != 1 {
		t.Fatalf("a close was addressed with another strategy's identity: %+v %+v", fixture.writer.batches, fixture.loop.Stats())
	}
}

// Nor is a record that says another producer wrote the alert, even when the
// reconciliation said it was ours: the two disagreeing is not a reason to
// believe either about whose business it is.
func TestARecordOfAnotherProducerIsNotAnIdentity(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("alert-1", "0123456789abcdef0123456789abcdef")})
	record := fixture.link.records["alert-1"]
	record.EventSourceID = "another-source"
	fixture.link.records["alert-1"] = record
	fixture.mature(context.Background())
	if len(fixture.writer.batches) != 0 || fixture.loop.Stats()[absentalerts.OutcomeIdentityUnknown] != 1 {
		t.Fatalf("a close was addressed with an identity from another producer's record: %+v %+v", fixture.writer.batches, fixture.loop.Stats())
	}
}

// A business without a revision is its own answer.
func TestARecordWithoutARevisionHasItsOwnAnswer(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("alert-1", "0123456789abcdef0123456789abcdef")})
	record := fixture.link.records["alert-1"]
	record.Revision = 0
	fixture.link.records["alert-1"] = record
	fixture.mature(context.Background())
	stats := fixture.loop.Stats()
	if len(fixture.writer.batches) != 0 || stats[absentalerts.OutcomeRevisionUnknown] != 1 || stats[absentalerts.OutcomeIdentityUnknown] != 0 {
		t.Fatalf("a missing revision was sent or filed as a missing business: %+v", stats)
	}
}

// Whose alert it is, is a fact the alert carries.
func TestOnlyThisDeploymentsOwnAlertsAreClosed(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{
		nativeAlert("mine", "0123456789abcdef0123456789abcdef"),
		{AlertID: "theirs", EventSourceID: "another-source", Fingerprint: "1123456789abcdef0123456789abcdef"},
		{AlertID: "nameless", Fingerprint: "2123456789abcdef0123456789abcdef"},
	})
	fixture.mature(context.Background())
	if len(fixture.writer.batches) != 1 || len(fixture.writer.batches[0]) != 1 || fixture.writer.batches[0][0].AlertInstanceID != "mine" {
		t.Fatalf("the close batch is not exactly this deployment's own alert: %+v", fixture.writer.batches)
	}
	stats := fixture.loop.Stats()
	if stats[absentalerts.OutcomeProducerForeign] != 1 || stats[absentalerts.OutcomeProducerUnknown] != 1 {
		t.Fatalf("the alerts that were not closed were not reported: %+v", stats)
	}
}

func TestAFollowerClosesNothingAndSaysWhy(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("alert-1", "0123456789abcdef0123456789abcdef")})
	fixture.loop.bundle.controlLeader = false
	fixture.loop.step(context.Background())
	if len(fixture.writer.batches) != 0 || fixture.loop.Stats()[absentalerts.OutcomeNotLeader] != 1 {
		t.Fatalf("a follower decided something, or did not say it was one: %+v", fixture.loop.Stats())
	}
}

func TestLosingTheControlTermRestartsTheGrace(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("alert-1", "0123456789abcdef0123456789abcdef")})
	ctx := context.Background()
	fixture.loop.step(ctx)
	fixture.loop.bundle.controlLeader = false
	fixture.loop.step(ctx)
	fixture.loop.bundle.controlLeader = true
	fixture.now = fixture.now.Add(absentCloseGrace + time.Minute)
	fixture.control.snapshot = liveSnapshot("observation-two", fixture.now, 100)
	for i := range fixture.link.pages {
		fixture.link.pages[i].Health.LastSuccess = fixture.now.Add(-time.Minute)
	}
	fixture.loop.step(ctx)
	if len(fixture.writer.batches) != 0 {
		t.Fatalf("a close matured on an absence observed under a lost term: %+v", fixture.writer.batches)
	}
}

// No roster, no difference, and the round names why.
func TestAnUnreachableLinkRefusesTheRoundByName(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("alert-1", "0123456789abcdef0123456789abcdef")})
	fixture.link.rosterErr = errors.New("console down")
	fixture.mature(context.Background())
	if len(fixture.writer.batches) != 0 || fixture.loop.Rounds()[absentalerts.RefusalLinkUnavailable] != 2 {
		t.Fatalf("a round without the roster decided, or did not say why: %+v", fixture.loop.Rounds())
	}
}

// The link's own health, read off the same response as the roster.
func TestALinkWhoseMaintenanceFailedRefusesTheRound(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("alert-1", "0123456789abcdef0123456789abcdef")})
	fixture.link.pages[0].Health.Error = "discovery_failed"
	ctx := context.Background()
	fixture.loop.step(ctx)
	fixture.now = fixture.now.Add(absentCloseGrace + time.Minute)
	fixture.control.snapshot = liveSnapshot("observation-two", fixture.now, 100)
	fixture.loop.step(ctx)
	if len(fixture.writer.batches) != 0 || fixture.loop.Rounds()[absentalerts.RefusalLinkUnhealthy] != 2 {
		t.Fatalf("an unhealthy link was decided on: %+v", fixture.loop.Rounds())
	}
	if fixture.loop.Difference()["link_health_age_seconds"] == 0 {
		t.Fatal("the refused round did not say how old the link's last success was")
	}
}

// A walk that fails after its first page is incomplete, says so, and still
// decides on what it read - which here does not include strategy 10.
func TestAWalkThatBreaksOffIsReportedAndDecidesOnWhatItRead(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("alert-1", "0123456789abcdef0123456789abcdef")})
	fixture.link.failAfter = 1
	fixture.mature(context.Background())
	difference := fixture.loop.Difference()
	if difference["roster_complete"] != 0 || difference["roster_pages"] != 1 || fixture.loop.Rounds()[absentalerts.RefusalNone] != 2 {
		t.Fatalf("an incomplete walk was not reported, or refused the round: %+v %+v", difference, fixture.loop.Rounds())
	}
	if len(fixture.writer.batches) != 0 {
		t.Fatalf("a strategy the walk never reached was closed: %+v", fixture.writer.batches)
	}
}

// A listed strategy whose set could not be read is reported and not closed.
func TestAListedStrategyWhoseSetCouldNotBeReadIsReported(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("alert-1", "0123456789abcdef0123456789abcdef")})
	fixture.link.pages[1].Rows[0].Members = nil
	fixture.mature(context.Background())
	if len(fixture.writer.batches) != 0 || fixture.loop.Stats()[absentalerts.OutcomeIndexUnreadable] != 2 {
		t.Fatalf("an unread set was closed or hidden: %+v", fixture.loop.Stats())
	}
}

// A strategy list that lost most of its strategies since the round before
// is decided on, with or without the writer's statement: the writer takes a
// strategy out only when it is gone, and the reader keeps no size gate to
// second-guess it with. Nor does a later round compare its list with the
// size before the deletion, which is what used to refuse every round after
// a legitimate bulk deletion until the leader changed.
func TestASnapshotThatShrankIsDecidedOn(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("alert-1", "0123456789abcdef0123456789abcdef")})
	ctx := context.Background()
	fixture.loop.step(ctx)
	for round, observation := range []string{"observation-two", "observation-three"} {
		fixture.now = fixture.now.Add(absentCloseGrace + time.Minute)
		fixture.control.snapshot = liveSnapshot(observation, fixture.now, 40)
		for i := range fixture.link.pages {
			fixture.link.pages[i].Health.LastSuccess = fixture.now.Add(-time.Minute)
		}
		fixture.loop.step(ctx)
		if decided := fixture.loop.Rounds()[absentalerts.RefusalNone]; decided != uint64(round+2) {
			t.Fatalf("round %d after the shrink: %d rounds decided, want %d: %+v", round+1, decided, round+2, fixture.loop.Rounds())
		}
	}
	if fixture.loop.Stats()[absentalerts.OutcomeCloseDecided] < 1 || len(fixture.writer.batches) < 1 {
		t.Fatalf("the absent strategy was not closed after the shrink: %+v %+v", fixture.loop.Stats(), fixture.writer.batches)
	}
}

// absentSourceStrategyIDs is the strategy_ids value a writer stores for
// strategies 1001 up to 1000+n.
func absentSourceStrategyIDs(n int) string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = strconv.Itoa(1001 + i)
	}
	return "[" + strings.Join(ids, ",") + "]"
}

// absentSourceReconciler is the control plane the close loop asks in
// production: a SourceReconciler reading the Legacy strategy cache on a real
// Redis, which holds strategies 1001..1100 and nothing else.
func absentSourceReconciler(t *testing.T, client *redis.Client, now func() time.Time) (*controlplane.SourceReconciler, func()) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		id := 1001 + i
		document := fmt.Sprintf(`{"id":%d,"bk_biz_id":2,"bk_tenant_id":"system","space_uid":"bkcc__2","update_time":1,`+
			`"items":[{"id":1,"query_md5":"absent-%d","expression":"a","unit":"","query_configs":[{"data_source_label":"bk_monitor",`+
			`"data_type_label":"time_series","metric_field":"usage","alias":"a","agg_dimension":["host"],"agg_method":"MAX",`+
			`"agg_interval":60,"result_table_id":"system.cpu"}],"algorithms":[{"level":1,"type":"Threshold","config":[[{"method":"gte","threshold":80}]]}]}],`+
			`"detects":[{"level":1,"priority":1,"connector":"and","trigger_config":{"count":1,"check_window":1}}]}`, id, id)
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+strconv.Itoa(id), document, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:absent-close", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), cfg.CompilerLimits())
	if err != nil {
		t.Fatal(err)
	}
	sm, err := state.RuntimeStateSemantics()
	if err != nil {
		t.Fatal(err)
	}
	semantics := strategy.StateSemantics{StateSchemaVersion: sm.StateSchemaVersion, CodecSemanticsVersion: sm.CodecSemanticsVersion,
		IdentitySchemaDigest: sm.IdentitySchemaDigest, SourceTimeSemanticsVersion: sm.SourceTimeSemanticsVersion, HistoryCellSemanticsVersion: sm.HistoryCellSemanticsVersion}
	accessBKData := true
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", controlplane.LegacyQueryRuntimeFacts{AccessBKData: &accessBKData,
		BKDataCMDBLevelTables: []string{}, SystemDiskFilter: controlplane.LegacyRuntimeFilterFact{FieldName: "device_type", Values: []string{"iso9660"}},
		SystemNetworkFilter: controlplane.LegacyRuntimeFilterFact{FieldName: "device_name", Values: []string{"lo"}}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache")
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, semantics)
	if err != nil {
		t.Fatal(err)
	}
	if err := reconciler.ConfigureClock(now); err != nil {
		t.Fatal(err)
	}
	refresh := func() {
		t.Helper()
		result, err := reconciler.Refresh(ctx, source, planner)
		if err != nil || result.ReadMode != controlplane.SourceReadFull {
			t.Fatalf("Refresh() = (%+v, %v), want a full read of the source", result, err)
		}
	}
	return reconciler, refresh
}

// The production control plane, end to end: a writer rewrites strategy_ids
// in place and drops sixty of a hundred strategies, without moving
// last_updated and without a statement. The round after is decided on, and
// so is the one after that: nothing compares a list with the size of the one
// before it.
func TestAListThatLostMostOfItsStrategiesIsDecidedOnThroughTheSource(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("alert-1", "0123456789abcdef0123456789abcdef")})
	fixture.link.pages[0].Rows[0].StrategyID = "1001"
	reconciler, refresh := absentSourceReconciler(t, client, func() time.Time { return fixture.now })
	fixture.loop.control = reconciler
	later := func() {
		fixture.now = fixture.now.Add(absentCloseGrace + time.Minute)
		for i := range fixture.link.pages {
			fixture.link.pages[i].Health.LastSuccess = fixture.now.Add(-time.Minute)
		}
	}

	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", absentSourceStrategyIDs(100), 0).Err(); err != nil {
		t.Fatal(err)
	}
	refresh()
	fixture.loop.step(ctx)
	if fixture.loop.Rounds()[absentalerts.RefusalNone] != 1 || fixture.loop.Difference()["snapshot_strategies"] != 100 {
		t.Fatalf("the first round did not decide on the hundred: %+v %+v", fixture.loop.Rounds(), fixture.loop.Difference())
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", absentSourceStrategyIDs(40), 0).Err(); err != nil {
		t.Fatal(err)
	}
	for round := 2; round <= 3; round++ {
		later()
		refresh()
		fixture.loop.step(ctx)
		if fixture.loop.Rounds()[absentalerts.RefusalNone] != uint64(round) || fixture.loop.Difference()["snapshot_strategies"] != 40 {
			t.Fatalf("round %d on the forty: %+v %+v", round, fixture.loop.Rounds(), fixture.loop.Difference())
		}
	}
	if fixture.loop.Stats()[absentalerts.OutcomeCloseDecided] < 1 {
		t.Fatalf("the absent strategy was not decided: %+v", fixture.loop.Stats())
	}
}

func TestAnUnreadableAlertListIsReported(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("alert-1", "0123456789abcdef0123456789abcdef")})
	fixture.link.alertsErr = errors.New("reconcile failed")
	fixture.mature(context.Background())
	if len(fixture.writer.batches) != 0 || fixture.loop.Stats()[absentalerts.OutcomeEvidenceUnavailable] != 1 {
		t.Fatalf("an unread alert list was not reported: %+v", fixture.loop.Stats())
	}
}

// A candidate past its grace is closed by the loop production builds, with
// nothing to switch on: its own alert is sent, and another producer's is
// counted and left.
func TestACandidatePastGraceIsSentWithNoSwitch(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{
		nativeAlert("mine", "0123456789abcdef0123456789abcdef"),
		{AlertID: "theirs", EventSourceID: "another-source", Fingerprint: "1123456789abcdef0123456789abcdef"},
	})
	fixture.mature(context.Background())
	stats := fixture.loop.Stats()
	if len(fixture.writer.batches) != 1 || len(fixture.writer.batches[0]) != 1 || fixture.writer.batches[0][0].AlertInstanceID != "mine" {
		t.Fatalf("the candidate's own alert was not sent alone: %+v", fixture.writer.batches)
	}
	if stats[absentalerts.OutcomeCloseDecided] != 1 || stats[absentalerts.OutcomeCloseSent] != 1 || stats[absentalerts.OutcomeProducerForeign] != 1 {
		t.Fatalf("outcomes = %+v, want one strategy closed, one alert closed, one foreign", stats)
	}
}

func TestARefusedRoundIsCountedAsARoundNotAsACandidate(t *testing.T) {
	fixture := newAbsentFixture(t, nil)
	fixture.control.haveSnaphot = false
	fixture.loop.step(context.Background())
	if fixture.loop.Rounds()[absentalerts.RefusalSnapshotUnusable] != 1 {
		t.Fatalf("the refused round was not counted as a round: %+v", fixture.loop.Rounds())
	}
	for outcome, count := range fixture.loop.Stats() {
		if count != 0 {
			t.Fatalf("a refused round put %s in the candidate family: %+v", outcome, fixture.loop.Stats())
		}
	}
}

// The reviewer's case end to end: the source document of strategy 10 lost its
// tenant, so the snapshot lists it under an empty one, and nothing runs a
// Plan of it. It still exists, and its alert stays open.
func TestAStrategyWhoseDocumentLostItsTenantKeepsItsAlerts(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("alert-1", "0123456789abcdef0123456789abcdef")})
	withoutTenant := func(snapshot controlplane.ObservedSnapshot) controlplane.ObservedSnapshot {
		snapshot.Strategies = append(snapshot.Strategies, controlplane.DepartedStrategy{StrategyID: "10"})
		return snapshot
	}
	ctx := context.Background()
	fixture.control.snapshot = withoutTenant(fixture.control.snapshot)
	fixture.loop.step(ctx)
	fixture.now = fixture.now.Add(absentCloseGrace + time.Minute)
	fixture.control.snapshot = withoutTenant(liveSnapshot("observation-two", fixture.now, 100))
	for i := range fixture.link.pages {
		fixture.link.pages[i].Health.LastSuccess = fixture.now.Add(-time.Minute)
	}
	fixture.loop.step(ctx)
	if len(fixture.writer.batches) != 0 || fixture.loop.Difference()["candidates"] != 0 {
		t.Fatalf("an existing strategy without a tenant in the snapshot had its alert closed: %+v", fixture.writer.batches)
	}
}

// A deleted strategy's alerts are all closed in the round that decides them,
// in batches, rather than one batch a round: a strategy with thousands of
// open alerts is not hours of rounds from being closed.
func TestEveryAlertOfADeletedStrategyIsClosedInOneRound(t *testing.T) {
	var alerts []openalerts.Alert
	for i := 0; i < 150; i++ {
		alerts = append(alerts, nativeAlert("alert-"+itoaAbsent(i), fmt.Sprintf("%032x", i)))
	}
	fixture := newAbsentFixture(t, alerts)
	fixture.link.pages[1].Rows[0].Members = members(len(alerts))
	fixture.mature(context.Background())
	sent := 0
	for _, batch := range fixture.writer.batches {
		if len(batch) > absentCloseAlertBatch {
			t.Fatalf("a batch of %d is past the batch size %d", len(batch), absentCloseAlertBatch)
		}
		sent += len(batch)
	}
	if sent != len(alerts) || fixture.loop.Stats()[absentalerts.OutcomeCloseSent] != uint64(len(alerts)) {
		t.Fatalf("closed %d of %d alerts in the deciding round (stats %+v)", sent, len(alerts), fixture.loop.Stats())
	}
}

// A roster walk longer than one round carries on where it stopped in the
// next, and the rounds in between decide on the last walk that reached its
// end: with time for one page a round, the strategy on the second page is
// still found missing, matures and is closed. Walking from the first page
// every round, it was never reached.
func TestARosterWalkLongerThanARoundCarriesOver(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("alert-1", "0123456789abcdef0123456789abcdef")})
	fixture.link.perRound = 1
	ctx := context.Background()
	for round, observation := range []string{"observation-one", "observation-two", "observation-three"} {
		if round > 0 {
			fixture.now = fixture.now.Add(absentCloseGrace + time.Minute)
		}
		fixture.control.snapshot = liveSnapshot(observation, fixture.now, 100)
		for i := range fixture.link.pages {
			fixture.link.pages[i].Health.LastSuccess = fixture.now.Add(-time.Minute)
		}
		fixture.link.served = 0
		fixture.loop.step(ctx)
	}
	if len(fixture.writer.batches) != 1 || fixture.writer.batches[0][0].StrategyID != 10 {
		t.Fatalf("the strategy on the walk's second page was not closed: %+v %+v", fixture.writer.batches, fixture.loop.Stats())
	}
}

// A round cut while it closes leaves the strategies it did not reach first
// in line: the next round starts at the first close not executed, not past
// the last one decided, which would go round to the first again and cut
// there every time.
func TestARoundCutWhileClosingResumesAtTheFirstCloseNotExecuted(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("alert-1", "0123456789abcdef0123456789abcdef")})
	health := fixture.link.pages[0].Health
	fixture.link.pages[1] = openalerts.RosterPage{Health: health, Rows: []openalerts.RosterRow{
		{TenantID: "system", StrategyID: "10", Members: members(1)},
		{TenantID: "system", StrategyID: "11", Members: members(1)},
		{TenantID: "system", StrategyID: "12", Members: members(1)}}}
	for _, id := range []string{"10", "11", "12"} {
		fixture.control.departed = append(fixture.control.departed, controlplane.DepartedStrategy{TenantID: "system", StrategyID: id, BusinessID: 5, Revision: 9})
	}
	fixture.mature(context.Background())
	fixture.writer.batches = nil
	var closed []int64
	for round := 0; round < 3; round++ {
		ctx, cancel := context.WithCancel(context.Background())
		fixture.writer.onWrite = cancel
		fixture.now = fixture.now.Add(absentCloseInterval)
		fixture.control.snapshot = liveSnapshot("observation-"+itoaAbsent(round+3), fixture.now, 100)
		for i := range fixture.link.pages {
			fixture.link.pages[i].Health.LastSuccess = fixture.now.Add(-time.Minute)
		}
		fixture.loop.step(ctx)
		cancel()
		for _, batch := range fixture.writer.batches[len(closed):] {
			closed = append(closed, batch[0].StrategyID)
		}
	}
	if len(closed) != 3 || closed[0] == closed[1] || closed[1] == closed[2] || closed[0] == closed[2] {
		t.Fatalf("rounds cut after one close each closed %v, want each strategy once", closed)
	}
}

// The absent-close round follows the source's re-read bound rather than
// repeating it as a second number: a round no slower than the re-read, and
// grace plus one round within the close bound.
func TestTheAbsentCloseRoundFollowsTheSourceReadBound(t *testing.T) {
	if absentCloseInterval > controlplane.SourceFullReadInterval {
		t.Fatalf("absent-close round %s is slower than the source re-read %s", absentCloseInterval, controlplane.SourceFullReadInterval)
	}
	if absentCloseGrace+absentCloseInterval > absentCloseBound {
		t.Fatalf("grace %s plus a round %s exceeds the close bound %s", absentCloseGrace, absentCloseInterval, absentCloseBound)
	}
}
