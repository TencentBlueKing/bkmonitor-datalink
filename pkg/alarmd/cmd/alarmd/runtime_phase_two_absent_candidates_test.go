package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/filterclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
)

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

// linkRequests is every request the fake link was sent.
func linkRequests(link *fakeLinkd) int {
	return len(link.requests("/api/v1/alerts/search")) + len(link.requests("/api/v1/alerts/close"))
}

// The page names the strategy behind the counts: what the round decided and
// when, what the close did, where the identity came from, and whether the
// source has it now. Reading it asks the link nothing.
func TestTheCandidatePageNamesTheStrategyAndWhatItsCloseFound(t *testing.T) {
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("mine", "10")})
	firstRound := fixture.now
	fixture.mature(context.Background())
	decided := fixture.now
	stub := &presenceStub{stored: map[string]bool{"10": true}}
	fixture.loop.documents = stub
	asked := linkRequests(fixture.link)

	page := readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{})
	if calls := linkRequests(fixture.link) - asked; calls != 0 {
		t.Fatalf("reading the page called the link %d times", calls)
	}
	if page.State != fleet.AbsentStateReady || page.LastRound == nil || page.LastRound.Refusal != absentalerts.RefusalNone ||
		page.LastRound.At != stamp(decided) || page.Table == nil || page.Table.At != stamp(decided) {
		t.Fatalf("the header does not say which round the rows are of: %+v %+v %+v", page, page.LastRound, page.Table)
	}
	if table := page.Table; table.Roster != 1 || table.Candidates != 1 || table.CloseDecided != 1 || table.Snapshot != 100 ||
		table.Rows != 1 || table.RowsNotKept != 0 {
		t.Fatalf("the table's counts are not the round's: %+v", table)
	}
	if len(page.Rows) != 1 {
		t.Fatalf("a strategy the snapshot lists is on the page, or the candidate is missing: %+v", page.Rows)
	}
	row := page.Rows[0]
	if row.TenantID != "system" || row.Outcome != absentalerts.OutcomeCloseDecided ||
		row.OutcomeAt != stamp(decided) || row.AbsentSince != stamp(firstRound) {
		t.Fatalf("the row is not the round's decision: %+v", row)
	}
	execution := row.Execution
	if execution == nil || execution.Word != filterclose.OutcomeJobDone || execution.DecidedAt != stamp(decided) {
		t.Fatalf("the row does not say what the close did: %+v", execution)
	}
	if identity := execution.Identity; identity == nil || identity.Source != fleet.AbsentIdentityNotRead {
		t.Fatalf("the row does not say no identity was read: %+v", execution.Identity)
	}
	if row.SourceNow != fleet.AbsentSourceDocument || len(stub.asked) != 1 || len(stub.asked[0]) != 1 || stub.asked[0][0] != "10" {
		t.Fatalf("the source's document was not checked once, for the one row the snapshot does not list: %+v %+v", row, stub.asked)
	}
}

// A strategy the catalog remembers is answered from memory; one it does not
// has no identity read: the page says which.
func TestTheIdentityOfARowSaysWhereItCameFrom(t *testing.T) {
	remembered := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("mine", "10")})
	remembered.control.departed = []controlplane.DepartedStrategy{{TenantID: "system", StrategyID: "10", BusinessID: 5, Revision: 9}}
	remembered.mature(context.Background())
	identity := absentRowOf(t, readAbsentPage(t, remembered.loop, fleet.AbsentCandidateQuery{}), "10").Execution.Identity
	if identity == nil || identity.Source != fleet.AbsentIdentityCatalog || identity.Business != 5 || identity.Revision != 9 || identity.RecordsRead != 0 {
		t.Fatalf("a remembered identity is not named as the catalog's: %+v", identity)
	}

	unknown := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("mine", "10")})
	unknown.mature(context.Background())
	identity = absentRowOf(t, readAbsentPage(t, unknown.loop, fleet.AbsentCandidateQuery{}), "10").Execution.Identity
	if identity == nil || identity.Source != fleet.AbsentIdentityNotRead || identity.Business != 0 || identity.Revision != 0 {
		t.Fatalf("an identity nobody had is not said so: %+v", identity)
	}
}

// A row says what the close did, and counts the close as sent.
func TestARowSaysWhatTheCloseSent(t *testing.T) {
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("mine", "10")})
	fixture.mature(context.Background())
	page := readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{})
	row := absentRowOf(t, page, "10")
	if row.Execution == nil || row.Execution.Word != filterclose.OutcomeJobDone || row.Sends != 1 || fixture.link.closed()["mine"] != 1 {
		t.Fatalf("the row does not say what the close did: %+v %+v", row, row.Execution)
	}
}

// A row counts its strategy's sends and when the first went out. A close
// the link refused is not a send; a strategy whose alerts are all closed
// leaves the page, and one that comes back starts again from nothing.
func TestARowSaysHowOftenItsCloseWentOut(t *testing.T) {
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("mine", "10")})
	ctx := context.Background()
	fixture.link.closeBusy = true
	fixture.mature(ctx)
	round := 0
	advance := func() {
		round++
		fixture.now = fixture.now.Add(absentCloseInterval)
		fixture.control.snapshot = liveSnapshot("observation-later-"+strconv.Itoa(round), fixture.now, 100)
		fixture.loop.step(ctx)
	}
	row := absentRowOf(t, readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{}), "10")
	if row.Execution == nil || row.Execution.Word != filterclose.OutcomeLinkBusy || row.Sends != 0 || row.FirstSentAt != "" {
		t.Fatalf("a refused close: %+v sends %d since %q; want link_busy, no send", row.Execution, row.Sends, row.FirstSentAt)
	}
	fixture.link.closeBusy = false
	advance()
	first := fixture.now
	row = absentRowOf(t, readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{}), "10")
	if row.Sends != 1 || row.FirstSentAt != stamp(first) || fixture.link.closed()["mine"] != 1 {
		t.Fatalf("the first send: sends %d since %q; want 1 since %q", row.Sends, row.FirstSentAt, stamp(first))
	}
	// The link closed it: the strategy is no longer found, and leaves the page.
	advance()
	if page := readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{}); slices.ContainsFunc(page.Rows,
		func(r fleet.AbsentCandidateRow) bool { return r.StrategyID == "10" }) {
		t.Fatalf("a strategy the link no longer holds alerts of is still on the page: %+v", page.Rows)
	}
	// An alert of it opens again: its count starts over.
	fixture.link.add(absentAlert("mine-again", "10"))
	for range 4 {
		if advance(); absentRowOf(t, readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{}), "10").Sends > 0 {
			break
		}
	}
	row = absentRowOf(t, readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{}), "10")
	if row.Sends != 1 || row.FirstSentAt != stamp(fixture.now) {
		t.Fatalf("a strategy back in the difference: sends %d since %q; want 1 since %q", row.Sends, row.FirstSentAt, stamp(fixture.now))
	}
}

// A refused round decides about no one, so it leaves the rows as the last
// deciding round left them, and the header says both rounds' times.
func TestARefusedRoundLeavesTheRowsAndSaysHowOldTheyAre(t *testing.T) {
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("mine", "10")})
	fixture.mature(context.Background())
	decided := fixture.now
	fixture.now = fixture.now.Add(absentCloseInterval)
	fixture.link.busy = true
	fixture.loop.step(context.Background())

	page := readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{})
	if page.LastRound == nil || page.LastRound.Refusal != absentalerts.RefusalLinkUnavailable || page.LastRound.At != stamp(fixture.now) {
		t.Fatalf("the refused round is not the latest round: %+v", page.LastRound)
	}
	if page.Table == nil || page.Table.At != stamp(decided) || page.Table.CloseDecided != 1 {
		t.Fatalf("the refused round changed which round the rows are of: %+v", page.Table)
	}
	row := absentRowOf(t, page, "10")
	if row.Outcome != absentalerts.OutcomeCloseDecided || row.OutcomeAt != stamp(decided) || row.Execution == nil ||
		row.Execution.Word != filterclose.OutcomeJobDone {
		t.Fatalf("the refused round changed a row: %+v", row)
	}
}

// A strategy that leaves the difference leaves the page; one that stays
// keeps what its latest close did until it is closed again.
func TestARowKeepsItsLatestCloseUntilItLeaves(t *testing.T) {
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("mine", "10"), absentAlert("one", "1")})
	fixture.mature(context.Background())
	decided := fixture.now
	// Both strategies open alerts again. One close a round, and the walk
	// resumes after 10, the last closed: 1 is closed again and 10 waits.
	fixture.link.add(absentAlert("mine-2", "10"))
	fixture.link.add(absentAlert("one-2", "1"))
	fixture.loop.bounds.MaxCloseStrategies = 1
	fixture.now = fixture.now.Add(absentCloseInterval)
	fixture.control.snapshot = liveSnapshot("observation-three", fixture.now, 100)
	fixture.loop.step(context.Background())
	page := readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{})
	kept := absentRowOf(t, page, "10")
	if kept.Outcome != absentalerts.OutcomeDeferred || kept.OutcomeAt != stamp(fixture.now) {
		t.Fatalf("the row does not carry this round's decision: %+v", kept)
	}
	if kept.Execution == nil || kept.Execution.DecidedAt != stamp(decided) || kept.Execution.Word != filterclose.OutcomeJobDone {
		t.Fatalf("a strategy still in the difference lost what its latest close did: %+v", kept.Execution)
	}
	if closed := fixture.link.closed(); closed["one-2"] != 1 || closed["mine-2"] != 0 {
		t.Fatalf("closed %+v, want strategy 1 closed again and 10 waiting", closed)
	}

	// Strategy 10's alert is closed by someone else; 1 opens one more.
	fixture.link.mu.Lock()
	fixture.link.alerts["mine-2"].status = "closed"
	fixture.link.mu.Unlock()
	fixture.link.add(absentAlert("one-3", "1"))
	fixture.now = fixture.now.Add(absentCloseInterval)
	fixture.loop.step(context.Background())
	page = readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{})
	if len(page.Rows) != 1 || page.Rows[0].StrategyID != "1" {
		t.Fatalf("a strategy the link no longer holds alerts of is still on the page: %+v", page.Rows)
	}
}

// The round's deadline can come before every close it decided has run.
// Those rows say so, as of this round, instead of showing an older close.
func TestACloseTheRoundsDeadlineCutOffReadsNotRun(t *testing.T) {
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("mine", "10"), absentAlert("eleven", "11")})
	fixture.loop.step(context.Background())
	fixture.now = fixture.now.Add(absentCloseGrace + time.Minute)
	fixture.control.snapshot = liveSnapshot("observation-two", fixture.now, 100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture.link.onClose = cancel
	fixture.loop.step(ctx)

	page := readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{})
	first, second := absentRowOf(t, page, "10"), absentRowOf(t, page, "11")
	if first.Execution == nil || first.Execution.Word == fleet.AbsentExecutionNotRun || first.Execution.Word == "" {
		t.Fatalf("the close that ran is not on its row: %+v", first.Execution)
	}
	if second.Outcome != absentalerts.OutcomeCloseDecided || second.Execution == nil || second.Execution.Word != fleet.AbsentExecutionNotRun ||
		second.Execution.DecidedAt != stamp(fixture.now) {
		t.Fatalf("a close the deadline cut off does not read as not run this round: %+v %+v", second, second.Execution)
	}
}

// The table holds what its bound holds, the first in key order, and counts
// the rest.
func TestTheTableKeepsItsBoundInKeyOrderAndCountsTheRest(t *testing.T) {
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("mine", "10"), absentAlert("eleven", "11"), absentAlert("twelve", "12")})
	fixture.loop.table = newAbsentCandidateTable(2)
	fixture.loop.step(context.Background())
	page := readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{})
	if len(page.Rows) != 2 || page.Rows[0].StrategyID != "10" || page.Rows[1].StrategyID != "11" {
		t.Fatalf("the table did not keep the first rows in key order: %+v", page.Rows)
	}
	if page.Table.Rows != 2 || page.Table.RowsNotKept != 1 || page.Table.Candidates != 3 {
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
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("mine", "10")})
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
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("mine", "10"), absentAlert("eleven", "11")})
	fixture.loop.step(context.Background())

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

// The page over HTTP: a deployment without the link's control plane says so
// on any replica, a follower forwards once, and a forwarded request that
// lands on another follower is refused, not forwarded again.
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
		t.Fatalf("a deployment without the link's control plane did not say so: %d %s", answer.Code, answer.Body.String())
	}

	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("mine", "10")})
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

// Many candidates are listed in key order however the link's discovery
// found them, so that the page's cursor can find its place among them.
func TestCandidatesAreListedInKeyOrder(t *testing.T) {
	alerts := []*fakeLinkdAlert{absentAlert("mine", "10")}
	for i := 20; i > 0; i-- {
		alerts = append(alerts, absentAlert("alert-"+strconv.Itoa(100-i), "9"+strconv.Itoa(i)))
	}
	fixture := newAbsentFixture(t, alerts)
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
// leading zero, a zero. Such a row says so by itself, and the page's other
// unlisted rows are still checked.
func TestAnIDTheSourceCannotNameLeavesTheOtherRowsTheirAnswer(t *testing.T) {
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("mine", "10"), absentAlert("lead", "0123"), absentAlert("zero", "0")})
	fixture.loop.step(context.Background())
	stub := &presenceStub{stored: map[string]bool{"10": true}}
	fixture.loop.documents = stub
	page := readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{})
	if row := absentRowOf(t, page, "10"); row.SourceNow != fleet.AbsentSourceDocument {
		t.Fatalf("a row the source can name lost its answer to the others: %+v", row)
	}
	for _, id := range []string{"0123", "0"} {
		if row := absentRowOf(t, page, id); row.SourceNow != fleet.AbsentSourceUnread || row.SourceNowReason != fleet.AbsentUnreadIDNotCanonical {
			t.Fatalf("an id the source cannot name is not said to be one: %+v", row)
		}
	}
	if len(stub.asked) != 1 || len(stub.asked[0]) != 1 || stub.asked[0][0] != "10" {
		t.Fatalf("the check was not sent for exactly the ids the source can name: %+v", stub.asked)
	}
}
