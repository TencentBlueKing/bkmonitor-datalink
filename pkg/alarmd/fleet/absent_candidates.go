// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package fleet

import (
	"context"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
)

// The absent close candidate page: one row per strategy the control
// leader's last deciding round of the absent close decided about, with what
// the latest close of it found. It is how a reader names the strategies
// behind the absent_strategy_* counts.
//
// The rows are kept by the loop from what each round already read and
// decided; reading them calls nothing on the alert link. What the page does
// read when asked is whether each row's strategy is in the source now: the
// current observation, in memory, and for a strategy it does not list, one
// existence check of the strategy's document, all of one page's in one
// pipeline.
//
// What the page cannot say, and says it cannot: whether a strategy the
// source no longer lists was deleted, or never existed under this id in the
// table the source is written from now. Neither the source nor the link
// records that; the rows are the facts to look an id up by.

// AbsentPageRows is the most rows one page returns, and the most existence
// checks it sends.
const AbsentPageRows = 200

// AbsentPageDefaultRows is a page's size when none is asked for.
const AbsentPageDefaultRows = 50

// The page's state words. Closed.
const (
	// AbsentStateReady: the leader has run a round this term; the header
	// says whether it decided, and the rows are of the last one that did.
	AbsentStateReady = "ready"
	// AbsentStateNoRoundYet: the leader has not run a round this term.
	AbsentStateNoRoundYet = "no_round_yet"
	// AbsentStateNotConfigured: the deployment names no alert link Console,
	// so there is no roster, no difference and nothing to page. Said so
	// that no rows here is not read as no candidates.
	AbsentStateNotConfigured = "not_configured"
)

// AbsentStates is every state word.
var AbsentStates = []string{AbsentStateReady, AbsentStateNoRoundYet, AbsentStateNotConfigured}

// AbsentOutcomes is every word a row's outcome takes: the four decisions a
// round makes about a candidate, and the link listing a strategy whose set
// it could not read.
var AbsentOutcomes = []string{absentalerts.OutcomeWithinGrace, absentalerts.OutcomeUnconfirmed,
	absentalerts.OutcomeDeferred, absentalerts.OutcomeCloseDecided, absentalerts.OutcomeIndexUnreadable}

// The execution words that are the page's own; the rest are the absent
// close's outcome words for the same facts.
const (
	// AbsentExecutionNotRun: the round decided to close the strategy and
	// its deadline came before the close ran. decided_at is that round.
	AbsentExecutionNotRun = "not_run"
	// AbsentExecutionNoOwnAlerts: every alert the link holds for the
	// strategy is another producer's or names none, so there was nothing
	// of this deployment's to close.
	AbsentExecutionNoOwnAlerts = "no_own_alerts"
)

// AbsentExecutions is every word a row's execution takes.
var AbsentExecutions = []string{AbsentExecutionNotRun,
	absentalerts.OutcomeCloseSent, absentalerts.OutcomeSendFailed, AbsentExecutionNoOwnAlerts,
	absentalerts.OutcomeIdentityUnknown, absentalerts.OutcomeRevisionUnknown, absentalerts.OutcomeEvidenceUnavailable}

// Where a close's business and revision came from. Closed.
const (
	// AbsentIdentityCatalog: the catalog's memory of the strategies it let
	// go gave every part that is known.
	AbsentIdentityCatalog = "catalog"
	// AbsentIdentityAlertRecord: at least one part was read from the
	// link's record of one of the strategy's own alerts.
	AbsentIdentityAlertRecord = "alert_record"
	// AbsentIdentityNone: looked for, and neither part was found.
	AbsentIdentityNone = "none"
	// AbsentIdentityNotRead: there was no own alert to close, so no record
	// was read; what the catalog remembers is still given.
	AbsentIdentityNotRead = "not_read"
)

// AbsentIdentitySources is every identity source word.
var AbsentIdentitySources = []string{AbsentIdentityCatalog, AbsentIdentityAlertRecord, AbsentIdentityNone, AbsentIdentityNotRead}

// Whether a row's strategy is in the source now, read when the page is
// asked. Closed.
const (
	// AbsentSourceListed: the current observation of the source lists the
	// strategy id - it came back since the round, or, for a row the link
	// could not read, it never left.
	AbsentSourceListed = "listed"
	// AbsentSourceDocument: the observation does not list it and the
	// strategy's document is still stored.
	AbsentSourceDocument = "document"
	// AbsentSourceNone: not listed, and no document is stored.
	AbsentSourceNone = "none"
	// AbsentSourceUnread: it could not be read; the reason says why.
	AbsentSourceUnread = "unread"
)

// AbsentSourceWords is every source_now word.
var AbsentSourceWords = []string{AbsentSourceListed, AbsentSourceDocument, AbsentSourceNone, AbsentSourceUnread}

// Why source_now is unread. Closed.
const (
	// AbsentUnreadNotObserved: this leader holds no observation of the
	// source to compare against.
	AbsentUnreadNotObserved = "not_observed"
	// AbsentUnreadUnsupported: the source cannot check a document's
	// existence by id.
	AbsentUnreadUnsupported = "unsupported"
	// AbsentUnreadFailed: the existence check failed or ran out of time.
	AbsentUnreadFailed = "read_failed"
	// AbsentUnreadIDNotCanonical: the link keys the strategy by an id that
	// is not a canonical positive integer, which the source names no
	// document by. The other rows of the page are checked all the same.
	AbsentUnreadIDNotCanonical = "id_not_canonical"
)

// AbsentCandidatesResponse is GET /api/absent.
type AbsentCandidatesResponse struct {
	AnsweredBy string `json:"answered_by"`
	State      string `json:"state"`
	// LastRound is the latest round this leader ran, decided or refused.
	LastRound *AbsentRoundFacts `json:"last_round,omitempty"`
	// Table is the round the rows are of: the latest one that decided. A
	// refused round leaves the rows as they were, so this can be much
	// older than LastRound, and a row can read closed beside a source_now
	// that says the strategy is listed again.
	Table      *AbsentTableFacts    `json:"table,omitempty"`
	Rows       []AbsentCandidateRow `json:"rows"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

// AbsentRoundFacts is one round: when it ran and its refusal word, "none"
// for a round that decided.
type AbsentRoundFacts struct {
	At      string `json:"at"`
	Refusal string `json:"refusal"`
}

// AbsentTableFacts is the deciding round the rows are of, with its counts -
// the same counts as the absent_strategy_difference gauge's sides and the
// outcome counters' increase in that round - and the table's own bound.
type AbsentTableFacts struct {
	At               string `json:"at"`
	Roster           int    `json:"roster_strategies"`
	RosterUnreadable int    `json:"roster_unreadable"`
	Snapshot         int    `json:"snapshot_strategies"`
	Candidates       int    `json:"candidates"`
	WithinGrace      int    `json:"within_grace"`
	Unconfirmed      int    `json:"unconfirmed"`
	Deferred         int    `json:"deferred"`
	CloseDecided     int    `json:"close_decided"`
	// Rows is how many rows the table keeps; RowsNotKept how many the
	// round decided about beyond the table's bound, in key order.
	Rows        int `json:"rows"`
	RowsNotKept int `json:"rows_not_kept"`
	// MemoryFull is the absent close's memory_full total: candidates whose
	// first absence the bounded memory would not hold.
	MemoryFull uint64 `json:"memory_full"`
}

// AbsentCandidateRow is one strategy.
type AbsentCandidateRow struct {
	TenantID   string `json:"tenant"`
	StrategyID string `json:"strategy_id"`
	// Members is how many members the link read in the strategy's open
	// alert set; absent for a set it could not read.
	Members   *int   `json:"members,omitempty"`
	Outcome   string `json:"outcome"`
	OutcomeAt string `json:"outcome_at"`
	// AbsentSince is when the loop first found the candidate missing; empty
	// for a candidate the memory did not hold and for an unreadable row.
	AbsentSince string `json:"absent_since,omitempty"`
	// Execution is the latest close of the strategy in this term; absent
	// for one never decided to close.
	Execution *AbsentExecutionFacts `json:"execution,omitempty"`
	// Sends is how many closes of the strategy went out this term, the
	// broker acknowledging each, and FirstSentAt when the first did; both
	// last as long as the strategy stays a candidate. Sent is not closed:
	// the link closes the alerts or does not, which
	// Execution.ActiveAfterSend reads.
	Sends           int    `json:"sends"`
	FirstSentAt     string `json:"first_sent_at,omitempty"`
	SourceNow       string `json:"source_now"`
	SourceNowReason string `json:"source_now_reason,omitempty"`
}

// AbsentExecutionFacts is what one close of a strategy found and did.
type AbsentExecutionFacts struct {
	DecidedAt string `json:"decided_at"`
	Word      string `json:"word"`
	// Alerts is absent when the strategy's alerts were not read.
	Alerts *AbsentAlertCounts `json:"alerts,omitempty"`
	// Batch is how many alerts the close addressed.
	Batch    int                  `json:"batch"`
	Identity *AbsentIdentityFacts `json:"identity,omitempty"`
	// SampleAlertID is one of the strategy's alerts, its own first.
	SampleAlertID string `json:"sample_alert_id,omitempty"`
	// ActiveAfterSend is how many of this deployment's alerts of the
	// strategy the link's reconcile still listed active when this close
	// read them, an earlier close of them having gone out: above zero, the
	// link did not close what it was sent. Absent before the first send.
	ActiveAfterSend *int `json:"active_after_send,omitempty"`
}

// AbsentAlertCounts is the strategy's alerts by whose they are.
type AbsentAlertCounts struct {
	Own     int `json:"own"`
	Foreign int `json:"foreign"`
	Unknown int `json:"unknown"`
}

// AbsentIdentityFacts is the business and revision a close carries and
// where they came from.
type AbsentIdentityFacts struct {
	Source      string `json:"source"`
	Business    int64  `json:"business,omitempty"`
	Revision    int64  `json:"revision,omitempty"`
	RecordsRead int    `json:"records_read"`
}

// AbsentCandidateQuery is one page asked for.
type AbsentCandidateQuery struct {
	Limit int
	// After is the last row of the previous page; nil for the first.
	After      *absentalerts.Key
	Outcome    string
	Execution  string
	StrategyID string
}

// AbsentCandidateSource answers a page from the loop's table. leading is
// false on a replica that does not lead, which forwards the request.
type AbsentCandidateSource func(ctx context.Context, query AbsentCandidateQuery) (page AbsentCandidatesResponse, leading bool)

// EncodeAbsentCursor is a row's key as the cursor that resumes after it.
func EncodeAbsentCursor(key absentalerts.Key) string {
	return base64.RawURLEncoding.EncodeToString([]byte(key.TenantID + "\x00" + key.StrategyID))
}

// ParseAbsentCursor reads a cursor EncodeAbsentCursor wrote.
func ParseAbsentCursor(raw string) (absentalerts.Key, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return absentalerts.Key{}, false
	}
	tenant, strategy, found := strings.Cut(string(decoded), "\x00")
	if !found || strategy == "" {
		return absentalerts.Key{}, false
	}
	return absentalerts.Key{TenantID: tenant, StrategyID: strategy}, true
}

// WithAbsentCandidates serves GET /api/absent in front of the fleet API.
// source returns nil on a deployment without the alert link's Console,
// which answers not_configured on any replica. A replica that does not lead
// forwards the request to the Leader once; a forwarded request that lands
// on one that does not lead either is refused rather than forwarded again.
func WithAbsentCandidates(next http.Handler, source func() AbsentCandidateSource, forward LeaderForward, replica string) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/absent" {
			next.ServeHTTP(response, request)
			return
		}
		if request.Method != http.MethodGet {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		query, refusal := absentQueryOf(request)
		if refusal != nil {
			writeJSON(response, http.StatusBadRequest, refusal)
			return
		}
		var answer AbsentCandidateSource
		if source != nil {
			answer = source()
		}
		if answer == nil {
			writeJSON(response, http.StatusOK, AbsentCandidatesResponse{AnsweredBy: replica,
				State: AbsentStateNotConfigured, Rows: []AbsentCandidateRow{}})
			return
		}
		page, leading := answer(request.Context(), query)
		if !leading {
			if forward != nil && request.Header.Get(forwardedHeader) == "" {
				forwarded, reason := forward(response, request)
				if forwarded {
					return
				}
				writeJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "LEADER_UNAVAILABLE", "reason": reason})
				return
			}
			writeJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "NOT_LEADER", "replica": replica})
			return
		}
		page.AnsweredBy = replica
		if page.Rows == nil {
			page.Rows = []AbsentCandidateRow{}
		}
		writeJSON(response, http.StatusOK, page)
	})
}

// absentQueryOf reads the page's parameters, refusing a word outside its
// closed list rather than ignoring it: a reader who filters and gets every
// row back would read them as all matching.
func absentQueryOf(request *http.Request) (AbsentCandidateQuery, map[string]any) {
	values := request.URL.Query()
	query := AbsentCandidateQuery{Limit: AbsentPageDefaultRows, StrategyID: values.Get("strategy_id")}
	if raw := values.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > AbsentPageRows {
			return query, map[string]any{"error": "INVALID_LIMIT", "max": AbsentPageRows}
		}
		query.Limit = n
	}
	if raw := values.Get("cursor"); raw != "" {
		after, ok := ParseAbsentCursor(raw)
		if !ok {
			return query, map[string]any{"error": "INVALID_CURSOR"}
		}
		query.After = &after
	}
	if word := values.Get("outcome"); word != "" {
		if !containsWord(AbsentOutcomes, word) {
			return query, map[string]any{"error": "OUTCOME_UNKNOWN", "accepted": AbsentOutcomes}
		}
		query.Outcome = word
	}
	if word := values.Get("execution"); word != "" {
		if !containsWord(AbsentExecutions, word) {
			return query, map[string]any{"error": "EXECUTION_UNKNOWN", "accepted": AbsentExecutions}
		}
		query.Execution = word
	}
	return query, nil
}

func containsWord(words []string, word string) bool {
	for _, candidate := range words {
		if candidate == word {
			return true
		}
	}
	return false
}
