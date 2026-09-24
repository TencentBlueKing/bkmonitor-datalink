package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

type absentTestControl struct {
	snapshot    controlplane.ObservedSnapshot
	haveSnaphot bool
	departed    []controlplane.DepartedStrategy
	refused     uint64
}

func (control *absentTestControl) ObservedSnapshot() (controlplane.ObservedSnapshot, bool) {
	return control.snapshot, control.haveSnaphot
}
func (control *absentTestControl) DepartedStrategies() ([]controlplane.DepartedStrategy, uint64) {
	return control.departed, control.refused
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
}

func (link *absentTestLink) Roster(_ context.Context, cursor string) (openalerts.RosterPage, error) {
	if link.rosterErr != nil {
		return openalerts.RosterPage{}, link.rosterErr
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
}

func (w *absentTestWriter) WriteCloseBatch(_ context.Context, requests []linkdoutput.CloseRequest) error {
	if w.err != nil {
		return w.err
	}
	w.batches = append(w.batches, append([]linkdoutput.CloseRequest(nil), requests...))
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
	fixture.loop = newAbsentStrategyClose(bundle, fixture.control, fixture.link, fixture.writer, true)
	return fixture
}

// mature runs the first round, then a round after the grace under a second
// observation of the source: the earliest a close can be sent.
func (fixture *absentTestFixture) mature(ctx context.Context) {
	fixture.loop.step(ctx)
	fixture.now = fixture.now.Add(controlplane.AbsenceGracePeriod + time.Minute)
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
	fixture.now = fixture.now.Add(controlplane.AbsenceGracePeriod + time.Minute)
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
	fixture.now = fixture.now.Add(controlplane.AbsenceGracePeriod + time.Minute)
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
	fixture.now = fixture.now.Add(controlplane.AbsenceGracePeriod + time.Minute)
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

func TestASnapshotThatShrankRefusesTheRoundByName(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("alert-1", "0123456789abcdef0123456789abcdef")})
	ctx := context.Background()
	fixture.loop.step(ctx)
	fixture.now = fixture.now.Add(controlplane.AbsenceGracePeriod + time.Minute)
	fixture.control.snapshot = liveSnapshot("observation-two", fixture.now, 40)
	for i := range fixture.link.pages {
		fixture.link.pages[i].Health.LastSuccess = fixture.now.Add(-time.Minute)
	}
	fixture.loop.step(ctx)
	if len(fixture.writer.batches) != 0 || fixture.loop.Rounds()[absentalerts.RefusalSnapshotShrunk] != 1 {
		t.Fatalf("a shrunken snapshot was decided on, or the refusal was not named: %+v", fixture.loop.Rounds())
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

// Until the deployment arms it, the difference runs in full and sends
// nothing, and every per-alert guard is read exactly as it would be armed.
func TestAnUnarmedDifferenceDecidesEverythingAndSendsNothing(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{
		nativeAlert("mine", "0123456789abcdef0123456789abcdef"),
		{AlertID: "theirs", EventSourceID: "another-source", Fingerprint: "1123456789abcdef0123456789abcdef"},
	})
	fixture.loop.send = false
	fixture.mature(context.Background())
	stats := fixture.loop.Stats()
	if len(fixture.writer.batches) != 0 || stats[absentalerts.OutcomeAlertClosed] != 0 {
		t.Fatalf("an unarmed difference sent a close: %+v", fixture.writer.batches)
	}
	if stats[absentalerts.OutcomeClosed] != 1 || stats[absentalerts.OutcomeWouldSend] != 1 || stats[absentalerts.OutcomeProducerForeign] != 1 {
		t.Fatalf("an unarmed difference did not report what arming would do: %+v", stats)
	}
	if fixture.loop.Difference()["send_armed"] != 0 {
		t.Fatalf("the reading does not say the close is unarmed: %+v", fixture.loop.Difference())
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
	fixture.now = fixture.now.Add(controlplane.AbsenceGracePeriod + time.Minute)
	fixture.control.snapshot = withoutTenant(liveSnapshot("observation-two", fixture.now, 100))
	for i := range fixture.link.pages {
		fixture.link.pages[i].Health.LastSuccess = fixture.now.Add(-time.Minute)
	}
	fixture.loop.step(ctx)
	if len(fixture.writer.batches) != 0 || fixture.loop.Difference()["candidates"] != 0 {
		t.Fatalf("an existing strategy without a tenant in the snapshot had its alert closed: %+v", fixture.writer.batches)
	}
}
