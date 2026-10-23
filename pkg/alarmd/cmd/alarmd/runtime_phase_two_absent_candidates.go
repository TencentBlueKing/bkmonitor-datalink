package main

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// absentCandidateDocumentTimeout bounds one page's existence checks. They
// are one pipelined round trip; a page that cannot have them in this long
// answers without them, each row saying it could not read them. It sits
// inside the forward's bound, which sits inside the channel's request
// deadline (3 s).
const absentCandidateDocumentTimeout = time.Second

// absentForwardTimeout bounds a page's hop to the Leader: the Leader's
// memory read and its one existence check, inside the channel's deadline.
const absentForwardTimeout = 2500 * time.Millisecond

// absentForwardRoute is the route the page's hops are recorded under.
const absentForwardRoute = "absent"

// absentCandidatePage is the page's way to the loop, which the bundle makes
// after the handler. Until it is bound, and on a deployment without the
// alert link's Console, there is no loop and the page says not_configured.
type absentCandidatePage struct {
	loop atomic.Pointer[absentStrategyClose]
}

func (page *absentCandidatePage) bind(loop *absentStrategyClose) { page.loop.Store(loop) }

func (page *absentCandidatePage) source() fleet.AbsentCandidateSource {
	loop := page.loop.Load()
	if loop == nil {
		return nil
	}
	return loop.Page
}

// absentSampleAlertIDBytes bounds the sample alert id a row keeps.
const absentSampleAlertIDBytes = 128

// absentCandidateTable is the absent close's last deciding round, one row
// per strategy it decided about, with what the latest close of each found.
// The loop writes it from what the round already read and decided; the
// page reads it. Nothing here reads the link or the store.
//
// The rows are kept in the candidates' own order (absentalerts.LessKey), so
// that a page is a search for its cursor and a copy of its rows, never a
// sort; byStrategy finds a strategy id's rows, under every tenant the link
// listed it with.
type absentCandidateTable struct {
	mu         sync.Mutex
	limit      int
	rows       []absentCandidateRow
	byStrategy map[string][]int
	// ran and lastRound are the latest round this term, decided or
	// refused; decided and table are the latest that decided, which the
	// rows are of.
	ran       bool
	lastRound fleet.AbsentRoundFacts
	decided   bool
	table     fleet.AbsentTableFacts
}

// absentCandidateRow is one strategy. execution points to facts that are
// never changed once made - a later close replaces the pointer - so a page
// copies rows without copying them.
//
// sends and firstSentAt count the closes of the strategy that went out this
// term, and stay with it for as long as it stays a candidate: a close sent is
// not a close made, and a strategy sent for again and again is one the link
// is not closing.
type absentCandidateRow struct {
	key         absentalerts.Key
	members     int
	readable    bool
	outcome     string
	outcomeAt   time.Time
	absentSince time.Time
	execution   *absentExecution
	sends       int
	firstSentAt time.Time
}

// absentExecution is what one close of a strategy found and did.
type absentExecution struct {
	decidedAt     time.Time
	word          string
	alertsRead    bool
	own           int
	foreign       int
	unknown       int
	batch         int
	identity      string
	business      int64
	revision      int64
	recordsRead   int
	sampleAlertID string
	// activeAfterSend is own, read by a close that came after an earlier one
	// went out: how many of this deployment's alerts the link's reconcile
	// still listed active once it had been sent their close. Nil before the
	// first send and when the alerts were not read.
	activeAfterSend *int
}

func newAbsentCandidateTable(limit int) *absentCandidateTable {
	if limit <= 0 {
		limit = 1
	}
	return &absentCandidateTable{limit: limit, byStrategy: map[string][]int{}}
}

// noteRound records a round this leader ran, whatever it decided.
func (table *absentCandidateTable) noteRound(at time.Time, refusal string) {
	table.mu.Lock()
	defer table.mu.Unlock()
	table.ran = true
	table.lastRound = fleet.AbsentRoundFacts{At: at.UTC().Format(time.RFC3339), Refusal: refusal}
}

// rebuild replaces the rows with a deciding round's: every candidate's
// decision and every strategy the link could not read, merged in key
// order, as many as the bound holds. A strategy still here keeps the facts
// of its latest close; one the round decided to close is marked not run
// until its close says otherwise, so a close the round's deadline cut off
// reads as that and not as an older close's word.
func (table *absentCandidateTable) rebuild(at time.Time, result absentalerts.Result, roster map[absentalerts.Key]int,
	unreadable []absentalerts.Key) {
	table.mu.Lock()
	defer table.mu.Unlock()
	previous := table.rows
	previousAt := func(key absentalerts.Key) absentCandidateRow {
		for _, index := range table.byStrategy[key.StrategyID] {
			if previous[index].key == key {
				return previous[index]
			}
		}
		return absentCandidateRow{}
	}
	total := len(result.Decisions) + len(unreadable)
	rows := make([]absentCandidateRow, 0, min(total, table.limit))
	byStrategy := make(map[string][]int, min(total, table.limit))
	admit := func(row absentCandidateRow) {
		byStrategy[row.key.StrategyID] = append(byStrategy[row.key.StrategyID], len(rows))
		rows = append(rows, row)
	}
	decisions, i, j := result.Decisions, 0, 0
	for len(rows) < table.limit && (i < len(decisions) || j < len(unreadable)) {
		if j >= len(unreadable) || (i < len(decisions) && absentalerts.LessKey(decisions[i].Key, unreadable[j])) {
			decision := decisions[i]
			i++
			before := previousAt(decision.Key)
			row := absentCandidateRow{key: decision.Key, outcome: decision.Outcome, outcomeAt: at,
				absentSince: decision.AbsentSince, execution: before.execution, sends: before.sends, firstSentAt: before.firstSentAt}
			row.members, row.readable = roster[decision.Key], true
			if decision.Outcome == absentalerts.OutcomeCloseDecided {
				row.execution = &absentExecution{decidedAt: at, word: fleet.AbsentExecutionNotRun}
			}
			admit(row)
			continue
		}
		admit(absentCandidateRow{key: unreadable[j], outcome: absentalerts.OutcomeIndexUnreadable, outcomeAt: at})
		j++
	}
	table.rows, table.byStrategy = rows, byStrategy
	counts := result.Counts
	table.decided = true
	table.table = fleet.AbsentTableFacts{At: at.UTC().Format(time.RFC3339), Roster: counts.Roster,
		RosterUnreadable: counts.RosterUnreadable, Snapshot: counts.SnapshotStrategies, Candidates: counts.Candidates,
		WithinGrace: counts.WithinGrace, Unconfirmed: counts.Unconfirmed, Deferred: counts.Deferred, CloseDecided: counts.CloseDecided,
		Rows: len(rows), RowsNotKept: total - len(rows)}
}

// executed records what a close of the strategy found. A strategy the
// table did not keep has nowhere to record it, which rows_not_kept already
// says.
//
// A close that reads the strategy's alerts after an earlier close of it went
// out is the reading of whether that one took: the alerts the link still
// lists active were sent their close and are open all the same.
func (table *absentCandidateTable) executed(key absentalerts.Key, execution absentExecution) {
	table.mu.Lock()
	defer table.mu.Unlock()
	for _, index := range table.byStrategy[key.StrategyID] {
		row := &table.rows[index]
		if row.key != key {
			continue
		}
		if row.sends > 0 && execution.alertsRead {
			active := execution.own
			execution.activeAfterSend = &active
		}
		if execution.word == absentalerts.OutcomeCloseSent {
			if row.sends == 0 {
				row.firstSentAt = execution.decidedAt
			}
			row.sends++
		}
		row.execution = &execution
		return
	}
}

// forget empties the table when the term ends, with the memory it was
// decided on.
func (table *absentCandidateTable) forget() {
	table.mu.Lock()
	defer table.mu.Unlock()
	table.rows, table.byStrategy = nil, map[string][]int{}
	table.ran, table.decided = false, false
	table.lastRound, table.table = fleet.AbsentRoundFacts{}, fleet.AbsentTableFacts{}
}

// page copies one page of rows out, with the header, under the lock; what
// the page reads besides is read after it is released.
func (table *absentCandidateTable) page(query fleet.AbsentCandidateQuery) (rows []absentCandidateRow, next *absentalerts.Key,
	header fleet.AbsentCandidatesResponse) {
	table.mu.Lock()
	defer table.mu.Unlock()
	header.State = fleet.AbsentStateNoRoundYet
	if table.ran {
		header.State = fleet.AbsentStateReady
		lastRound := table.lastRound
		header.LastRound = &lastRound
	}
	if table.decided {
		facts := table.table
		header.Table = &facts
	}
	matches := func(row absentCandidateRow) bool {
		if query.StrategyID != "" && row.key.StrategyID != query.StrategyID {
			return false
		}
		if query.Outcome != "" && row.outcome != query.Outcome {
			return false
		}
		if query.Execution != "" && (row.execution == nil || row.execution.word != query.Execution) {
			return false
		}
		return true
	}
	limit := max(query.Limit, 1)
	collect := func(index int) bool {
		row := table.rows[index]
		if !matches(row) {
			return true
		}
		if len(rows) == limit {
			last := rows[len(rows)-1].key
			next = &last
			return false
		}
		rows = append(rows, row)
		return true
	}
	if query.StrategyID != "" {
		// A strategy id is a lookup, its rows already in key order.
		for _, index := range table.byStrategy[query.StrategyID] {
			if query.After != nil && !absentalerts.LessKey(*query.After, table.rows[index].key) {
				continue
			}
			if !collect(index) {
				break
			}
		}
		return rows, next, header
	}
	start := 0
	if query.After != nil {
		after := *query.After
		start = sort.Search(len(table.rows), func(i int) bool { return absentalerts.LessKey(after, table.rows[i].key) })
	}
	for index := start; index < len(table.rows); index++ {
		if !collect(index) {
			break
		}
	}
	return rows, next, header
}

// Page answers GET /api/absent on the control leader: the table's rows for
// the page asked, and for each whether its strategy is in the source now.
// A replica that does not lead answers leading=false and the request is
// forwarded.
func (loop *absentStrategyClose) Page(ctx context.Context, query fleet.AbsentCandidateQuery) (fleet.AbsentCandidatesResponse, bool) {
	loop.bundle.mu.RLock()
	leader := loop.bundle.controlLeader && !loop.bundle.draining && !loop.bundle.closed
	loop.bundle.mu.RUnlock()
	if !leader {
		return fleet.AbsentCandidatesResponse{}, false
	}
	rows, next, page := loop.table.page(query)
	if page.Table != nil {
		page.Table.MemoryFull = loop.Stats()[absentalerts.OutcomeMemoryFull]
	}
	if next != nil {
		page.NextCursor = fleet.EncodeAbsentCursor(*next)
	}
	page.Rows = make([]fleet.AbsentCandidateRow, len(rows))
	for index, row := range rows {
		page.Rows[index] = absentRowFacts(row)
	}
	loop.readSourceNow(ctx, page.Rows)
	return page, true
}

// readSourceNow says, for each row, whether its strategy is in the source
// now: listed by the current observation, read from memory; and for the
// rows it does not list, whether the strategy's document is stored - every
// one of them in one pipelined check, with its own short deadline and no
// retry. A check that fails leaves those rows unread, with why, and the
// page still answers.
func (loop *absentStrategyClose) readSourceNow(ctx context.Context, rows []fleet.AbsentCandidateRow) {
	observed, haveSnapshot := loop.control.ObservedSnapshot()
	if !haveSnapshot {
		for index := range rows {
			rows[index].SourceNow, rows[index].SourceNowReason = fleet.AbsentSourceUnread, fleet.AbsentUnreadNotObserved
		}
		return
	}
	// The page's ids, at most a page of them, looked up in one walk over the
	// observation: no set the size of the source is built for a page.
	listed := make(map[string]bool, len(rows))
	for index := range rows {
		listed[rows[index].StrategyID] = false
	}
	for _, strategy := range observed.Strategies {
		if _, onPage := listed[strategy.StrategyID]; onPage {
			listed[strategy.StrategyID] = true
		}
	}
	unlisted := make([]int, 0, len(rows))
	for index := range rows {
		switch {
		case listed[rows[index].StrategyID]:
			rows[index].SourceNow = fleet.AbsentSourceListed
		case !controlplane.CanonicalStrategyID(rows[index].StrategyID):
			// Checked apart, so that one such id leaves the rest of the
			// page's rows their answer.
			rows[index].SourceNow, rows[index].SourceNowReason = fleet.AbsentSourceUnread, fleet.AbsentUnreadIDNotCanonical
		default:
			unlisted = append(unlisted, index)
		}
	}
	if len(unlisted) == 0 {
		return
	}
	unread := func(reason string) {
		for _, index := range unlisted {
			rows[index].SourceNow, rows[index].SourceNowReason = fleet.AbsentSourceUnread, reason
		}
	}
	if loop.documents == nil {
		unread(fleet.AbsentUnreadUnsupported)
		return
	}
	ids := make([]string, len(unlisted))
	for position, index := range unlisted {
		ids[position] = rows[index].StrategyID
	}
	checkCtx, cancel := context.WithTimeout(ctx, absentCandidateDocumentTimeout)
	defer cancel()
	present, err := loop.documents.StrategyDocumentsPresent(checkCtx, ids)
	switch {
	case errors.Is(err, controlplane.ErrStrategyDocumentPresenceUnsupported):
		unread(fleet.AbsentUnreadUnsupported)
		return
	case err != nil || len(present) != len(ids):
		unread(fleet.AbsentUnreadFailed)
		return
	}
	for position, index := range unlisted {
		rows[index].SourceNow = fleet.AbsentSourceNone
		if present[position] {
			rows[index].SourceNow = fleet.AbsentSourceDocument
		}
	}
}

func absentRowFacts(row absentCandidateRow) fleet.AbsentCandidateRow {
	facts := fleet.AbsentCandidateRow{TenantID: row.key.TenantID, StrategyID: row.key.StrategyID,
		Outcome: row.outcome, OutcomeAt: row.outcomeAt.UTC().Format(time.RFC3339)}
	if row.readable {
		members := row.members
		facts.Members = &members
	}
	if !row.absentSince.IsZero() {
		facts.AbsentSince = row.absentSince.UTC().Format(time.RFC3339)
	}
	facts.Sends = row.sends
	if row.sends > 0 {
		facts.FirstSentAt = row.firstSentAt.UTC().Format(time.RFC3339)
	}
	if execution := row.execution; execution != nil {
		facts.Execution = &fleet.AbsentExecutionFacts{DecidedAt: execution.decidedAt.UTC().Format(time.RFC3339),
			Word: execution.word, Batch: execution.batch, SampleAlertID: execution.sampleAlertID}
		if execution.activeAfterSend != nil {
			active := *execution.activeAfterSend
			facts.Execution.ActiveAfterSend = &active
		}
		if execution.alertsRead {
			facts.Execution.Alerts = &fleet.AbsentAlertCounts{Own: execution.own, Foreign: execution.foreign, Unknown: execution.unknown}
		}
		if execution.identity != "" {
			facts.Execution.Identity = &fleet.AbsentIdentityFacts{Source: execution.identity,
				Business: execution.business, Revision: execution.revision, RecordsRead: execution.recordsRead}
		}
	}
	return facts
}

// boundedAlertID is an alert id as a row keeps it: at most
// absentSampleAlertIDBytes, cut at a character boundary and marked.
func boundedAlertID(id string) string {
	if len(id) <= absentSampleAlertIDBytes {
		return id
	}
	cut := absentSampleAlertIDBytes - len("...")
	for cut > 0 && !utf8.RuneStart(id[cut]) {
		cut--
	}
	return id[:cut] + "..."
}
