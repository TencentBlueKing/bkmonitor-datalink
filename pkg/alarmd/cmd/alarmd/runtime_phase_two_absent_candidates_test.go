package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
)

// countingLink is the fixture's link, counting every call, so that a page
// read can be shown to call none.
type countingLink struct {
	link  absentCloseLink
	calls int
	// onReconcile runs before each reconciliation is answered.
	onReconcile func()
}

func (link *countingLink) Roster(ctx context.Context, cursor string) (openalerts.RosterPage, error) {
	link.calls++
	return link.link.Roster(ctx, cursor)
}

func (link *countingLink) Reconcile(ctx context.Context, key openalerts.StrategyKey) (openalerts.Reconciliation, error) {
	link.calls++
	if link.onReconcile != nil {
		link.onReconcile()
	}
	return link.link.Reconcile(ctx, key)
}

func (link *countingLink) AlertRecord(ctx context.Context, tenant, alertID string) (openalerts.AlertRecord, error) {
	link.calls++
	return link.link.AlertRecord(ctx, tenant, alertID)
}

// presenceStub answers existence checks from a set, and counts them.
type presenceStub struct {
	stored map[string]bool
	err    error
	asked  [][]string
}

func (stub *presenceStub) StrategyDocumentsPresent(_ context.Context, ids []string) ([]bool, error) {
	stub.asked = append(stub.asked, append([]string(nil), ids...))
	if stub.err != nil {
		return nil, stub.err
	}
	present := make([]bool, len(ids))
	for i, id := range ids {
		present[i] = stub.stored[id]
	}
	return present, nil
}

func readAbsentPage(t *testing.T, loop *absentStrategyClose, query fleet.AbsentCandidateQuery) fleet.AbsentCandidatesResponse {
	t.Helper()
	if query.Limit == 0 {
		query.Limit = fleet.AbsentPageDefaultRows
	}
	page, leading := loop.Page(context.Background(), query)
	if !leading {
		t.Fatal("the leader's page answered as a follower")
	}
	return page
}

func absentRowOf(t *testing.T, page fleet.AbsentCandidatesResponse, strategyID string) fleet.AbsentCandidateRow {
	t.Helper()
	for _, row := range page.Rows {
		if row.StrategyID == strategyID {
			return row
		}
	}
	t.Fatalf("no row for strategy %s: %+v", strategyID, page.Rows)
	return fleet.AbsentCandidateRow{}
}

func stamp(at time.Time) string { return at.UTC().Format(time.RFC3339) }

// The page names the strategy behind the counts: how many members the link
// read, what the round decided and when, what the close found - whose
// alerts, where the identity came from - and whether the source has it now.
// Reading it asks the link nothing.
func TestTheCandidatePageNamesTheStrategyAndWhatItsCloseFound(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{
		{AlertID: "theirs", EventSourceID: "another-source", Fingerprint: "1123456789abcdef0123456789abcdef"},
		nativeAlert("mine", "0123456789abcdef0123456789abcdef"),
		{AlertID: "nameless", Fingerprint: "2123456789abcdef0123456789abcdef"},
	})
	fixture.link.pages[1].Rows[0].Members = members(3)
	firstRound := fixture.now
	fixture.mature(context.Background())
	decided := fixture.now
	stub := &presenceStub{stored: map[string]bool{"10": true}}
	fixture.loop.documents = stub
	link := &countingLink{link: fixture.link}
	fixture.loop.link = link

	page := readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{})
	if link.calls != 0 {
		t.Fatalf("reading the page called the link %d times", link.calls)
	}
	if page.State != fleet.AbsentStateReady || page.LastRound == nil || page.LastRound.Refusal != absentalerts.RefusalNone ||
		page.LastRound.At != stamp(decided) || page.Table == nil || page.Table.At != stamp(decided) {
		t.Fatalf("the header does not say which round the rows are of: %+v %+v %+v", page, page.LastRound, page.Table)
	}
	if table := page.Table; table.Roster != 2 || table.Candidates != 1 || table.CloseDecided != 1 || table.Snapshot != 100 ||
		table.Rows != 1 || table.RowsNotKept != 0 {
		t.Fatalf("the table's counts are not the round's: %+v", table)
	}
	if len(page.Rows) != 1 {
		t.Fatalf("a strategy the snapshot lists is on the page, or the candidate is missing: %+v", page.Rows)
	}
	row := page.Rows[0]
	if row.TenantID != "system" || row.Members == nil || *row.Members != 3 || row.Outcome != absentalerts.OutcomeCloseDecided ||
		row.OutcomeAt != stamp(decided) || row.AbsentSince != stamp(firstRound) {
		t.Fatalf("the row is not the round's decision: %+v", row)
	}
	execution := row.Execution
	if execution == nil || execution.Word != absentalerts.OutcomeCloseSent || execution.DecidedAt != stamp(decided) ||
		execution.Batch != 1 || execution.SampleAlertID != "mine" {
		t.Fatalf("the row does not say what the close did: %+v", execution)
	}
	if alerts := execution.Alerts; alerts == nil || alerts.Own != 1 || alerts.Foreign != 1 || alerts.Unknown != 1 {
		t.Fatalf("the row does not say whose alerts they were: %+v", execution.Alerts)
	}
	if identity := execution.Identity; identity == nil || identity.Source != fleet.AbsentIdentityAlertRecord ||
		identity.Business != 2 || identity.Revision != 7 || identity.RecordsRead != 1 {
		t.Fatalf("the row does not say where the identity came from: %+v", execution.Identity)
	}
	if row.SourceNow != fleet.AbsentSourceDocument || len(stub.asked) != 1 || len(stub.asked[0]) != 1 || stub.asked[0][0] != "10" {
		t.Fatalf("the source's document was not checked once, for the one row the snapshot does not list: %+v %+v", row, stub.asked)
	}
}

// A strategy the catalog remembers is answered from memory, and one with no
// own alert reads no record at all: the page says which.
func TestTheIdentityOfARowSaysWhereItCameFrom(t *testing.T) {
	remembered := newAbsentFixture(t, []openalerts.Alert{nativeAlert("mine", "0123456789abcdef0123456789abcdef")})
	remembered.control.departed = []controlplane.DepartedStrategy{{TenantID: "system", StrategyID: "10", BusinessID: 5, Revision: 9}}
	remembered.mature(context.Background())
	identity := absentRowOf(t, readAbsentPage(t, remembered.loop, fleet.AbsentCandidateQuery{}), "10").Execution.Identity
	if identity == nil || identity.Source != fleet.AbsentIdentityCatalog || identity.Business != 5 || identity.Revision != 9 || identity.RecordsRead != 0 {
		t.Fatalf("a remembered identity is not named as the catalog's: %+v", identity)
	}

	foreign := newAbsentFixture(t, []openalerts.Alert{{AlertID: "theirs", EventSourceID: "another-source", Fingerprint: "1123456789abcdef0123456789abcdef"}})
	foreign.control.departed = []controlplane.DepartedStrategy{{TenantID: "system", StrategyID: "10", BusinessID: 5}}
	foreign.mature(context.Background())
	execution := absentRowOf(t, readAbsentPage(t, foreign.loop, fleet.AbsentCandidateQuery{}), "10").Execution
	if execution == nil || execution.Word != fleet.AbsentExecutionNoOwnAlerts || execution.Batch != 0 || execution.SampleAlertID != "theirs" ||
		execution.Identity == nil || execution.Identity.Source != fleet.AbsentIdentityNotRead || execution.Identity.Business != 5 ||
		foreign.link.reads != 0 {
		t.Fatalf("a strategy with nothing of ours to close is not said so, or a record was read for it: %+v reads=%d", execution, foreign.link.reads)
	}

	partial := newAbsentFixture(t, []openalerts.Alert{nativeAlert("mine", "0123456789abcdef0123456789abcdef")})
	partial.control.departed = []controlplane.DepartedStrategy{{TenantID: "system", StrategyID: "10", BusinessID: 5}}
	record := partial.link.records["mine"]
	record.Revision = 0
	partial.link.records["mine"] = record
	partial.mature(context.Background())
	execution = absentRowOf(t, readAbsentPage(t, partial.loop, fleet.AbsentCandidateQuery{}), "10").Execution
	if execution == nil || execution.Word != absentalerts.OutcomeRevisionUnknown || execution.Identity == nil ||
		execution.Identity.Source != fleet.AbsentIdentityCatalog || execution.Identity.Business != 5 || execution.Identity.RecordsRead != 1 {
		t.Fatalf("a business only the catalog had is not named as the catalog's: %+v", execution)
	}

	unknown := newAbsentFixture(t, []openalerts.Alert{nativeAlert("mine", "0123456789abcdef0123456789abcdef")})
	record = unknown.link.records["mine"]
	record.BusinessID, record.Revision = 0, 0
	unknown.link.records["mine"] = record
	unknown.mature(context.Background())
	execution = absentRowOf(t, readAbsentPage(t, unknown.loop, fleet.AbsentCandidateQuery{}), "10").Execution
	if execution == nil || execution.Word != absentalerts.OutcomeIdentityUnknown || execution.Identity == nil ||
		execution.Identity.Source != fleet.AbsentIdentityNone || execution.Identity.RecordsRead != 1 {
		t.Fatalf("an identity nobody had is not said so: %+v", execution)
	}
}

// A row says what the close sent: the word, and how many alerts.
func TestARowSaysWhatTheCloseSent(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("mine", "0123456789abcdef0123456789abcdef")})
	fixture.mature(context.Background())
	page := readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{})
	execution := absentRowOf(t, page, "10").Execution
	if execution == nil || execution.Word != absentalerts.OutcomeCloseSent || execution.Batch != 1 || len(fixture.writer.batches) != 1 {
		t.Fatalf("the row does not say what the close sent: %+v %+v", page, execution)
	}
}

// A close sent is not a close made. A row counts its strategy's sends and
// when the first went out, and a close that reads the alerts after one went
// out says how many the link still lists active - the reading of whether the
// earlier close took. A refused send is not a send, and a strategy that
// leaves the difference starts again from nothing if it comes back.
func TestARowSaysHowOftenItsCloseWentOutAndWhatTheLinkStillLists(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("mine", "0123456789abcdef0123456789abcdef")})
	ctx := context.Background()
	fixture.mature(ctx)
	first := fixture.now
	round := 0
	advance := func() {
		round++
		fixture.now = fixture.now.Add(absentCloseInterval)
		fixture.control.snapshot = liveSnapshot("observation-later-"+strconv.Itoa(round), fixture.now, 100)
		for i := range fixture.link.pages {
			fixture.link.pages[i].Health.LastSuccess = fixture.now.Add(-time.Minute)
		}
		fixture.loop.step(ctx)
	}
	step := func() fleet.AbsentCandidateRow {
		t.Helper()
		advance()
		return absentRowOf(t, readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{}), "10")
	}
	active := func(row fleet.AbsentCandidateRow) int {
		if row.Execution == nil || row.Execution.ActiveAfterSend == nil {
			return -1
		}
		return *row.Execution.ActiveAfterSend
	}

	row := absentRowOf(t, readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{}), "10")
	if row.Sends != 1 || row.FirstSentAt != stamp(first) || active(row) != -1 {
		t.Fatalf("the first send: sends %d since %q, active after send %d; want 1 since %q, none read yet",
			row.Sends, row.FirstSentAt, active(row), stamp(first))
	}
	// The link keeps listing the alert: the next close finds it and sends again.
	if row = step(); row.Sends != 2 || row.FirstSentAt != stamp(first) || active(row) != 1 {
		t.Fatalf("a close sent again: sends %d since %q, active after send %d; want 2 since %q, 1 still listed",
			row.Sends, row.FirstSentAt, active(row), stamp(first))
	}
	// The broker refuses: nothing went out, though the alert was read still listed.
	fixture.writer.err = errors.New("broker unavailable")
	if row = step(); row.Execution == nil || row.Execution.Word != absentalerts.OutcomeSendFailed || row.Sends != 2 || active(row) != 1 {
		t.Fatalf("a refused send: %+v sends %d, active after send %d; want send_failed, still 2 sends, 1 listed",
			row.Execution, row.Sends, active(row))
	}
	fixture.writer.err = nil
	// The link's alerts could not be read: that says nothing about whether the
	// close took, and the row does not read it as zero still listed.
	fixture.link.alertsErr = errors.New("reconcile unavailable")
	if row = step(); row.Execution == nil || row.Execution.Word != absentalerts.OutcomeEvidenceUnavailable || row.Sends != 2 || active(row) != -1 {
		t.Fatalf("alerts not read: %+v sends %d, active after send %d; want evidence_unavailable, 2 sends, nothing read",
			row.Execution, row.Sends, active(row))
	}
	fixture.link.alertsErr = nil
	// The link closed it: the next close reads none of this deployment's alerts.
	fixture.link.alerts = nil
	if row = step(); row.Execution == nil || row.Execution.Word != fleet.AbsentExecutionNoOwnAlerts || row.Sends != 2 || active(row) != 0 {
		t.Fatalf("after the link closed the alert: %+v sends %d, active after send %d; want no_own_alerts, 2 sends, 0 listed",
			row.Execution, row.Sends, active(row))
	}

	// It leaves the link's roster, and the page; when it is listed again, its
	// count starts over.
	fixture.link.alerts = []openalerts.Alert{nativeAlert("mine-again", "fedcba9876543210fedcba9876543210")}
	fixture.link.records["mine-again"] = openalerts.AlertRecord{AlertID: "mine-again", TenantID: "system",
		EventSourceID: "native", Fingerprint: "fedcba9876543210fedcba9876543210", Status: "active",
		StrategyID: "10", BusinessID: 2, Revision: 7}
	listed := fixture.link.pages[1].Rows
	fixture.link.pages[1].Rows = nil
	advance()
	if page := readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{}); slices.ContainsFunc(page.Rows,
		func(r fleet.AbsentCandidateRow) bool { return r.StrategyID == "10" }) {
		t.Fatalf("a strategy the link no longer lists is still on the page: %+v", page.Rows)
	}
	fixture.link.pages[1].Rows = listed
	for range 4 {
		if row = step(); row.Sends > 0 {
			break
		}
	}
	if row.Sends != 1 || row.FirstSentAt != stamp(fixture.now) || active(row) != -1 {
		t.Fatalf("a strategy back in the difference: sends %d since %q, active after send %d; want 1 since %q, none read yet",
			row.Sends, row.FirstSentAt, active(row), stamp(fixture.now))
	}
}

// A refused round decides about no one, so it leaves the rows as the last
// deciding round left them, and the header says both rounds' times.
func TestARefusedRoundLeavesTheRowsAndSaysHowOldTheyAre(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("mine", "0123456789abcdef0123456789abcdef")})
	fixture.mature(context.Background())
	decided := fixture.now
	fixture.now = fixture.now.Add(absentCloseInterval)
	fixture.link.pages[0].Health.Error = "discovery_failed"
	fixture.loop.step(context.Background())

	page := readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{})
	if page.LastRound == nil || page.LastRound.Refusal != absentalerts.RefusalLinkUnhealthy || page.LastRound.At != stamp(fixture.now) {
		t.Fatalf("the refused round is not the latest round: %+v", page.LastRound)
	}
	if page.Table == nil || page.Table.At != stamp(decided) || page.Table.CloseDecided != 1 {
		t.Fatalf("the refused round changed which round the rows are of: %+v", page.Table)
	}
	row := absentRowOf(t, page, "10")
	if row.Outcome != absentalerts.OutcomeCloseDecided || row.OutcomeAt != stamp(decided) || row.Execution == nil ||
		row.Execution.Word != absentalerts.OutcomeCloseSent {
		t.Fatalf("the refused round changed a row: %+v", row)
	}
}

// A strategy that leaves the difference leaves the page; one that stays
// keeps what its latest close found until it is closed again.
func TestARowKeepsItsLatestCloseUntilItLeaves(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("mine", "0123456789abcdef0123456789abcdef")})
	fixture.link.pages[1].Rows = append(fixture.link.pages[1].Rows, openalerts.RosterRow{TenantID: "system", StrategyID: "1", Members: members(1)})
	fixture.mature(context.Background())
	decided := fixture.now
	// One close a round, and the walk resumes after 10, the last closed: 1
	// is closed again and 10 waits.
	fixture.loop.bounds.MaxCloseStrategies = 1
	fixture.now = fixture.now.Add(absentCloseInterval)
	fixture.control.snapshot = liveSnapshot("observation-three", fixture.now, 100)
	for i := range fixture.link.pages {
		fixture.link.pages[i].Health.LastSuccess = fixture.now.Add(-time.Minute)
	}
	fixture.loop.step(context.Background())
	page := readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{})
	kept := absentRowOf(t, page, "10")
	if kept.Outcome != absentalerts.OutcomeDeferred || kept.OutcomeAt != stamp(fixture.now) {
		t.Fatalf("the row does not carry this round's decision: %+v", kept)
	}
	if kept.Execution == nil || kept.Execution.DecidedAt != stamp(decided) || kept.Execution.Word != absentalerts.OutcomeCloseSent {
		t.Fatalf("a strategy still in the difference lost what its latest close found: %+v", kept.Execution)
	}

	fixture.link.pages[1].Rows = fixture.link.pages[1].Rows[1:]
	fixture.now = fixture.now.Add(absentCloseInterval)
	for i := range fixture.link.pages {
		fixture.link.pages[i].Health.LastSuccess = fixture.now.Add(-time.Minute)
	}
	fixture.loop.step(context.Background())
	page = readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{})
	if len(page.Rows) != 1 || page.Rows[0].StrategyID != "1" {
		t.Fatalf("a strategy the link no longer lists is still on the page: %+v", page.Rows)
	}
}

// The round's deadline can come before every close it decided has run.
// Those rows say so, as of this round, instead of showing an older close.
func TestACloseTheRoundsDeadlineCutOffReadsNotRun(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("mine", "0123456789abcdef0123456789abcdef")})
	fixture.link.pages[1].Rows = append(fixture.link.pages[1].Rows, openalerts.RosterRow{TenantID: "system", StrategyID: "11", Members: members(1)})
	fixture.loop.step(context.Background())
	fixture.now = fixture.now.Add(absentCloseGrace + time.Minute)
	fixture.control.snapshot = liveSnapshot("observation-two", fixture.now, 100)
	for i := range fixture.link.pages {
		fixture.link.pages[i].Health.LastSuccess = fixture.now.Add(-time.Minute)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture.loop.link = &countingLink{link: fixture.link, onReconcile: cancel}
	fixture.loop.step(ctx)

	page := readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{})
	first, second := absentRowOf(t, page, "10"), absentRowOf(t, page, "11")
	if first.Execution == nil || first.Execution.Word != absentalerts.OutcomeCloseSent {
		t.Fatalf("the close that ran is not on its row: %+v", first.Execution)
	}
	if second.Outcome != absentalerts.OutcomeCloseDecided || second.Execution == nil || second.Execution.Word != fleet.AbsentExecutionNotRun ||
		second.Execution.DecidedAt != stamp(fixture.now) || second.Execution.Alerts != nil {
		t.Fatalf("a close the deadline cut off does not read as not run this round: %+v %+v", second, second.Execution)
	}
}

// A strategy the link listed and could not read is a row of its own, so
// that the header's count of them can be answered with which ones.
func TestAStrategyWhoseSetCouldNotBeReadIsListedWithoutMembers(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("mine", "0123456789abcdef0123456789abcdef")})
	fixture.link.pages[1].Rows = append(fixture.link.pages[1].Rows, openalerts.RosterRow{TenantID: "system", StrategyID: "live-7"})
	fixture.mature(context.Background())
	if all := readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{}); len(all.Rows) != 2 || all.Rows[0].StrategyID != "10" {
		t.Fatalf("the unreadable strategy is not in its place among the candidates: %+v", all.Rows)
	}
	page := readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{Outcome: absentalerts.OutcomeIndexUnreadable})
	if len(page.Rows) != 1 || page.Table.RosterUnreadable != 1 {
		t.Fatalf("the unreadable strategies on the page are not the header's count: %+v %+v", page.Rows, page.Table)
	}
	row := page.Rows[0]
	if row.StrategyID != "live-7" || row.Members != nil || row.Execution != nil || row.AbsentSince != "" ||
		row.SourceNow != fleet.AbsentSourceListed {
		t.Fatalf("an unreadable strategy is not listed as one: %+v", row)
	}
}

// The table holds what its bound holds, the first in key order - an
// unreadable strategy among them in its place - and counts the rest.
func TestTheTableKeepsItsBoundInKeyOrderAndCountsTheRest(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("mine", "0123456789abcdef0123456789abcdef")})
	fixture.link.pages[1].Rows = append(fixture.link.pages[1].Rows,
		openalerts.RosterRow{TenantID: "system", StrategyID: "11", Members: members(1)},
		openalerts.RosterRow{TenantID: "system", StrategyID: "12", Members: members(1)},
		openalerts.RosterRow{TenantID: "system", StrategyID: "105"})
	fixture.loop.table = newAbsentCandidateTable(2)
	fixture.loop.step(context.Background())
	page := readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{})
	if len(page.Rows) != 2 || page.Rows[0].StrategyID != "10" || page.Rows[1].StrategyID != "105" ||
		page.Rows[1].Outcome != absentalerts.OutcomeIndexUnreadable {
		t.Fatalf("the table did not keep the first rows in key order: %+v", page.Rows)
	}
	if page.Table.Rows != 2 || page.Table.RowsNotKept != 2 || page.Table.Candidates != 3 {
		t.Fatalf("the rows past the bound are not counted: %+v", page.Table)
	}
}

// Pages follow their cursor in key order; a filter narrows them and the
// cursor still only points where another matching row is.
func TestPagesFollowTheCursorAndTheFilters(t *testing.T) {
	table := newAbsentCandidateTable(100)
	at := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	keys := []absentalerts.Key{{TenantID: "a", StrategyID: "10"}, {TenantID: "a", StrategyID: "11"},
		{TenantID: "b", StrategyID: "10"}, {TenantID: "b", StrategyID: "12"}, {TenantID: "b", StrategyID: "13"}}
	result := absentalerts.Result{Refusal: absentalerts.RefusalNone}
	for i, key := range keys {
		outcome := absentalerts.OutcomeWithinGrace
		if i%2 == 1 {
			outcome = absentalerts.OutcomeDeferred
		}
		result.Decisions = append(result.Decisions, absentalerts.Decision{Key: key, Outcome: outcome})
	}
	table.noteRound(at, absentalerts.RefusalNone)
	table.rebuild(at, result, map[absentalerts.Key]int{}, nil)

	var seen []absentalerts.Key
	query := fleet.AbsentCandidateQuery{Limit: 2}
	for pages := 0; pages < 5; pages++ {
		rows, next, _ := table.page(query)
		for _, row := range rows {
			seen = append(seen, row.key)
		}
		if next == nil {
			break
		}
		query.After = next
	}
	if len(seen) != len(keys) {
		t.Fatalf("paging did not visit every row once: %+v", seen)
	}
	for i := range keys {
		if seen[i] != keys[i] {
			t.Fatalf("paging is not in key order: %+v", seen)
		}
	}

	rows, next, _ := table.page(fleet.AbsentCandidateQuery{Limit: 1, Outcome: absentalerts.OutcomeDeferred})
	if len(rows) != 1 || rows[0].key != keys[1] || next == nil || *next != keys[1] {
		t.Fatalf("the filtered first page is wrong: %+v %+v", rows, next)
	}
	rows, next, _ = table.page(fleet.AbsentCandidateQuery{Limit: 1, Outcome: absentalerts.OutcomeDeferred, After: next})
	if len(rows) != 1 || rows[0].key != keys[3] || next != nil {
		t.Fatalf("the filtered last page is wrong, or points past the last match: %+v %+v", rows, next)
	}
	rows, _, _ = table.page(fleet.AbsentCandidateQuery{Limit: 10, StrategyID: "10"})
	if len(rows) != 2 || rows[0].key != keys[0] || rows[1].key != keys[2] {
		t.Fatalf("a strategy id did not find its rows under every tenant: %+v", rows)
	}
	table.executed(keys[4], absentExecution{word: absentalerts.OutcomeSendFailed})
	rows, _, _ = table.page(fleet.AbsentCandidateQuery{Limit: 10, Execution: absentalerts.OutcomeSendFailed})
	if len(rows) != 1 || rows[0].key != keys[4] {
		t.Fatalf("the execution filter did not find the one row: %+v", rows)
	}
}

// The term's end empties the page with the memory it was decided on, and a
// replica that does not lead answers nothing of its own.
func TestLosingTheTermEmptiesThePage(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("mine", "0123456789abcdef0123456789abcdef")})
	fixture.mature(context.Background())
	fixture.loop.bundle.controlLeader = false
	if _, leading := fixture.loop.Page(context.Background(), fleet.AbsentCandidateQuery{Limit: 10}); leading {
		t.Fatal("a replica that does not lead answered the page")
	}
	fixture.loop.step(context.Background())
	fixture.loop.bundle.controlLeader = true
	page := readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{})
	if page.State != fleet.AbsentStateNoRoundYet || page.LastRound != nil || page.Table != nil || len(page.Rows) != 0 {
		t.Fatalf("the page kept rows decided under a term that ended: %+v", page)
	}
}

// Whether the source has the strategy now: listed by the observation, read
// from memory; otherwise its document, checked in one call; and a check that
// cannot be made leaves the rows unread with why, and the page answering.
func TestSourceNowSaysWhatTheSourceHasAndWhyItCouldNotSay(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("mine", "0123456789abcdef0123456789abcdef")})
	fixture.link.pages[1].Rows = append(fixture.link.pages[1].Rows, openalerts.RosterRow{TenantID: "system", StrategyID: "11", Members: members(1)})
	fixture.mature(context.Background())

	stub := &presenceStub{stored: map[string]bool{}}
	fixture.loop.documents = stub
	fixture.control.snapshot.Strategies = append(fixture.control.snapshot.Strategies, controlplane.DepartedStrategy{TenantID: "system", StrategyID: "11"})
	page := readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{})
	if row := absentRowOf(t, page, "11"); row.SourceNow != fleet.AbsentSourceListed {
		t.Fatalf("a strategy the source lists again is not said to be listed: %+v", row)
	}
	if row := absentRowOf(t, page, "10"); row.SourceNow != fleet.AbsentSourceNone || len(stub.asked) != 1 || len(stub.asked[0]) != 1 {
		t.Fatalf("a strategy with no document is not said to have none, or a listed one was checked: %+v %+v", row, stub.asked)
	}

	stub.err = context.DeadlineExceeded
	page = readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{})
	if row := absentRowOf(t, page, "10"); row.SourceNow != fleet.AbsentSourceUnread || row.SourceNowReason != fleet.AbsentUnreadFailed {
		t.Fatalf("a failed check is not said to be one: %+v", row)
	}
	if row := absentRowOf(t, page, "11"); row.SourceNow != fleet.AbsentSourceListed {
		t.Fatalf("a failed check took the listed rows with it: %+v", row)
	}

	fixture.loop.documents = nil
	if row := absentRowOf(t, readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{}), "10"); row.SourceNow != fleet.AbsentSourceUnread ||
		row.SourceNowReason != fleet.AbsentUnreadUnsupported {
		t.Fatalf("a source with no check is not said to have none: %+v", row)
	}
	fixture.control.haveSnaphot = false
	if row := absentRowOf(t, readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{}), "11"); row.SourceNow != fleet.AbsentSourceUnread ||
		row.SourceNowReason != fleet.AbsentUnreadNotObserved {
		t.Fatalf("without an observation a row was said to be listed or not: %+v", row)
	}
}

// The existence check on the real store, through the client the production
// wiring gives the strategy source: one single-key EXISTS per id, a document
// stored and one not, and an id the layout cannot name refused whole.
func TestTheStrategySourceChecksDocumentsOneKeyAtATime(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	if err := client.Set(ctx, "bkmonitor.cache.strategy_10", `{"id":10}`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	source, err := controlplane.NewLegacyRedisStrategySource(redisForCaller(client, redisfailure.CallerStrategySource), "bkmonitor.cache")
	if err != nil {
		t.Fatal(err)
	}
	present, err := source.StrategyDocumentsPresent(ctx, []string{"10", "11"})
	if err != nil || len(present) != 2 || !present[0] || present[1] {
		t.Fatalf("the documents stored are not the ones said to be: %v %v", present, err)
	}
	if _, err := source.StrategyDocumentsPresent(ctx, []string{"10", "0x"}); err == nil {
		t.Fatal("an id the layout cannot name was checked")
	}
	tooMany := make([]string, controlplane.MaxStrategyDocumentChecks+1)
	for i := range tooMany {
		tooMany[i] = strconv.Itoa(i + 1)
	}
	if _, err := source.StrategyDocumentsPresent(ctx, tooMany); err == nil {
		t.Fatal("a check beyond its bound was sent")
	}
}

// The page over HTTP: a deployment without a Console says so on any replica,
// a follower forwards once, and a forwarded request that lands on another
// follower is refused, not forwarded again.
func TestThePageIsAnsweredWhereTheLoopLeads(t *testing.T) {
	notFound := http.NotFoundHandler()
	get := func(handler http.Handler, header string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/api/absent?limit=5", nil)
		if header != "" {
			request.Header.Set("X-Alarmd-Forwarded", header)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	unbound := &absentCandidatePage{}
	answer := get(fleet.WithAbsentCandidates(notFound, unbound.source, nil, "replica-a"), "")
	var page fleet.AbsentCandidatesResponse
	if answer.Code != http.StatusOK || json.Unmarshal(answer.Body.Bytes(), &page) != nil ||
		page.State != fleet.AbsentStateNotConfigured || page.AnsweredBy != "replica-a" || page.Rows == nil {
		t.Fatalf("a deployment without the Console did not say so: %d %s", answer.Code, answer.Body.String())
	}

	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("mine", "0123456789abcdef0123456789abcdef")})
	fixture.mature(context.Background())
	bound := &absentCandidatePage{}
	bound.bind(fixture.loop)
	forwards := 0
	forward := func(response http.ResponseWriter, _ *http.Request) (bool, string) {
		forwards++
		response.WriteHeader(http.StatusTeapot)
		return true, ""
	}
	handler := fleet.WithAbsentCandidates(notFound, bound.source, forward, "replica-a")
	answer = get(handler, "")
	if answer.Code != http.StatusOK || forwards != 0 || !strings.Contains(answer.Body.String(), `"strategy_id":"10"`) {
		t.Fatalf("the leader did not answer its own page: %d %s", answer.Code, answer.Body.String())
	}
	fixture.loop.bundle.controlLeader = false
	if answer = get(handler, ""); answer.Code != http.StatusTeapot || forwards != 1 {
		t.Fatalf("a follower did not forward the page once: %d forwards=%d", answer.Code, forwards)
	}
	if answer = get(handler, "replica-b"); answer.Code != http.StatusServiceUnavailable || forwards != 1 ||
		!strings.Contains(answer.Body.String(), "NOT_LEADER") {
		t.Fatalf("a forwarded page was forwarded again: %d %s", answer.Code, answer.Body.String())
	}
}

// Many unreadable strategies are listed in key order however the link
// returned them, so that the page's cursor can find its place among them.
func TestUnreadableStrategiesAreListedInKeyOrder(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("mine", "0123456789abcdef0123456789abcdef")})
	for i := 20; i > 0; i-- {
		fixture.link.pages[1].Rows = append(fixture.link.pages[1].Rows, openalerts.RosterRow{TenantID: "system", StrategyID: "9" + strconv.Itoa(i)})
	}
	fixture.loop.step(context.Background())
	page := readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{Limit: 200})
	if len(page.Rows) != 21 {
		t.Fatalf("not every strategy is on the page: %+v", page.Rows)
	}
	for i := 1; i < len(page.Rows); i++ {
		previous := absentalerts.Key{TenantID: page.Rows[i-1].TenantID, StrategyID: page.Rows[i-1].StrategyID}
		current := absentalerts.Key{TenantID: page.Rows[i].TenantID, StrategyID: page.Rows[i].StrategyID}
		if !absentalerts.LessKey(previous, current) {
			t.Fatalf("the rows are not in key order at %d: %+v", i, page.Rows)
		}
	}
}

// A hop to the Leader for this page is recorded under its own route: a route
// outside the metric's closed list is not recorded at all.
func TestTheCandidatePagesHopIsRecorded(t *testing.T) {
	if !slices.Contains(metric.LeaderForwardRoutes, absentForwardRoute) {
		t.Fatalf("route %q is not among %v", absentForwardRoute, metric.LeaderForwardRoutes)
	}
}

// The link may key a strategy by an id the source names no document by - a
// leading zero, letters. Such a row says so by itself, and the page's other
// unlisted rows are still checked: one such id used to refuse the whole
// check, and every unlisted row of the page read as a source that cannot
// check at all.
func TestAnIDTheSourceCannotNameLeavesTheOtherRowsTheirAnswer(t *testing.T) {
	fixture := newAbsentFixture(t, []openalerts.Alert{nativeAlert("mine", "0123456789abcdef0123456789abcdef")})
	fixture.link.pages[1].Rows = append(fixture.link.pages[1].Rows,
		openalerts.RosterRow{TenantID: "system", StrategyID: "0123", Members: members(1)},
		openalerts.RosterRow{TenantID: "system", StrategyID: "abc", Members: members(1)},
		openalerts.RosterRow{TenantID: "system", StrategyID: "0", Members: members(1)})
	fixture.mature(context.Background())
	stub := &presenceStub{stored: map[string]bool{"10": true}}
	fixture.loop.documents = stub
	page := readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{})
	if row := absentRowOf(t, page, "10"); row.SourceNow != fleet.AbsentSourceDocument {
		t.Fatalf("a row the source can name lost its answer to the others: %+v", row)
	}
	for _, id := range []string{"0123", "abc", "0"} {
		if row := absentRowOf(t, page, id); row.SourceNow != fleet.AbsentSourceUnread || row.SourceNowReason != fleet.AbsentUnreadIDNotCanonical {
			t.Fatalf("an id the source cannot name is not said to be one: %+v", row)
		}
	}
	if len(stub.asked) != 1 || len(stub.asked[0]) != 1 || stub.asked[0][0] != "10" {
		t.Fatalf("the check was not sent for exactly the ids the source can name: %+v", stub.asked)
	}
}
