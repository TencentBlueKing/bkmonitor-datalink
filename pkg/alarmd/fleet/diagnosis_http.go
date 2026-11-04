// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// UniverseReader reads the source's active strategy set: the same key the
// control plane lists strategies from, read now. Its error is written to
// the response as it is, so the wiring classifies it first: a reason word
// and a detail, never a dependency's address.
type UniverseReader func(ctx context.Context) ([]string, error)

// DiagnosisCacheTTL is how long one diagnosis keeps the universe and the
// view it read on its first page. Every later page reads the same pair, so
// one diagnosis reads the fleet's snapshots once however many pages it
// takes; a page asked after the TTL rereads and says so.
const DiagnosisCacheTTL = 10 * time.Minute

// DiagnosisUniverse is the population the pages cover.
type DiagnosisUniverse struct {
	// Status is ok, or unreadable with Reason: then there are no rows and
	// the page does not hold, because a diagnosis with no population has
	// covered nothing -- it is not a diagnosis of nothing wrong.
	Status      string              `json:"status"`
	Reason      string              `json:"reason,omitempty"`
	Source      string              `json:"source"`
	Count       int                 `json:"count"`
	Digest      string              `json:"digest,omitempty"`
	ReadAt      *time.Time          `json:"read_at,omitempty"`
	Publication StrategyPublication `json:"publication"`
}

// UniverseChange says a later page found a different universe than the
// cursor was written against. The CLI reruns from the first page.
type UniverseChange struct {
	FromDigest string `json:"from_digest"`
	ToDigest   string `json:"to_digest"`
	Count      int    `json:"count"`
}

// DiagnosisResponse is one page of GET /api/diagnose.
type DiagnosisResponse struct {
	Diagnosis  string            `json:"diagnosis_id"`
	AnsweredBy string            `json:"answered_by"`
	Universe   DiagnosisUniverse `json:"universe"`
	Strategies []DiagnosisRow    `json:"strategies"`
	Page       DiagnosisPage     `json:"page"`
	// Verdicts and UnknownReasons are the closed lists, on every page.
	Verdicts       []StateWord `json:"verdicts"`
	UnknownReasons []string    `json:"unknown_reasons"`
	// Progress is read, partial, not_wired, or unavailable: whether the
	// Plans' persisted progress could be read for this page. partial is some
	// objects' records left unread because the observation memory line had
	// no room for them: ProgressDeferred of the ProgressObjects the page
	// asked about, each under PROGRESS_DEFERRED on its row.
	Progress         string          `json:"progress"`
	ProgressObjects  int             `json:"progress_objects,omitempty"`
	ProgressDeferred int             `json:"progress_deferred,omitempty"`
	UniverseChanged  *UniverseChange `json:"universe_changed,omitempty"`
	// SnapshotReread says a later page read the universe and the view
	// again, because the first page's had expired or the answering
	// process changed.
	SnapshotReread bool   `json:"snapshot_reread,omitempty"`
	NextCursor     string `json:"next_cursor,omitempty"`
	// Timing is where this page's time went.
	Timing DiagnosisTiming `json:"timing_ms"`
	// Warmed is the warm-up this process ran when it took the catalog over,
	// absent where none has run.
	Warmed *DiagnosisWarm `json:"warmed,omitempty"`
}

// DiagnosisSummary is the whole universe's count by verdict, from the rows a
// diagnosis writes, with no row sent: what the page's first screen says of
// the strategies. Strategies is the universe's size and ByVerdict sums to
// it, as the pages of one diagnosis sum; QueryGroups is how many Query
// Groups the deployment expects, from the view the rows were decided
// against - the page's other denominator, a different unit that is never
// the strategies' count - and nil where that view had no expectation.
type DiagnosisSummary struct {
	Strategies  int               `json:"strategies"`
	ByVerdict   map[StateWord]int `json:"by_verdict"`
	QueryGroups *int              `json:"query_groups"`
}

// DiagnosisSummaryResponse is GET /api/diagnose?summary=1: the summary and
// the words it is rendered by, which travel with it for the reason they
// travel with the strategy list (StrategyListResponse.Words).
type DiagnosisSummaryResponse struct {
	Diagnosis  string            `json:"diagnosis_id"`
	AnsweredBy string            `json:"answered_by"`
	Universe   DiagnosisUniverse `json:"universe"`
	Summary    DiagnosisSummary  `json:"summary"`
	Verdicts   []StateWord       `json:"verdicts"`
	Words      Words             `json:"words"`
	Timing     DiagnosisTiming   `json:"timing_ms"`
	// OverdueEpisodes is the replicas' latest objects found overdue and
	// overdue no more, read from their snapshots with the view the
	// diagnosis is decided on; absent when none was kept.
	OverdueEpisodes []OverdueEpisode `json:"overdue_episodes,omitempty"`
}

// DiagnosisSummaryFreshFor is how long one first-screen count answers every
// page that asks. Each count reads every replica's snapshot and decides a
// row for every strategy - on one deployment about 2.4 MB of snapshots and
// three-quarters of a second - and a page asks on each refresh, so without
// it every open page would pay that on every refresh; with it the leader
// pays it once in this long however many pages are open. A count that could
// not be read is not kept.
const DiagnosisSummaryFreshFor = 30 * time.Second

// summaryCache is the one count kept, and when it was read.
type summaryCache struct {
	mu   sync.Mutex
	at   time.Time
	body *DiagnosisSummaryResponse
	// board is the strategy board decided in the same pass as body: one
	// computation answers both the first-screen count and the board's rows
	// and facets, so the two read one instant.
	board *boardIndex
}

// summarizeDiagnosis counts every id of the universe by the verdict its row
// takes, the row being the one a page writes for it.
func summarizeDiagnosis(universe []string, view *View, row func(string) DiagnosisRow) DiagnosisSummary {
	summary := DiagnosisSummary{Strategies: len(universe), ByVerdict: map[StateWord]int{}}
	for _, word := range DiagnosisVerdicts() {
		summary.ByVerdict[word] = 0
	}
	for _, id := range universe {
		summary.ByVerdict[row(id).Verdict]++
	}
	if view != nil && view.Expected != nil {
		expected := *view.Expected
		summary.QueryGroups = &expected
	}
	return summary
}

type diagnosisEntry struct {
	id        string
	universe  []string
	digest    string
	readAt    time.Time
	view      *View
	readError string
	expires   time.Time
	// stallAfter is the replay window the view was decided under, and the
	// tolerance the strategy page folds its store outages by, as the
	// overview does.
	stallAfter time.Duration
	// universeTook and viewTook are how long the read that filled the entry
	// took for each; viewTook is nil where there is no service to read.
	universeTook *int64
	viewTook     *int64
	// ready is closed when the read that fills the entry has finished; a
	// request for the same diagnosis waits on it instead of reading again.
	ready chan struct{}
}

// diagnosisReadFailed is the read error of a diagnosis whose read did not
// return, to the requests that waited on it.
const diagnosisReadFailed = "DIAGNOSIS_READ_FAILED"

// DiagnosisReadTimeout bounds one diagnosis's first read of the universe and
// the fleet's snapshots, inside the channel's own request deadline.
const DiagnosisReadTimeout = 2500 * time.Millisecond

// DiagnosisCacheEntries bounds the diagnoses kept at once: a few people
// diagnosing the same deployment each keep their own read, and a fifth
// evicts the entry closest to expiring.
const DiagnosisCacheEntries = 4

// diagnosisCache keeps each diagnosis's universe and view by its id. The
// reads happen outside the lock; a failed universe read is not kept, so a
// cancelled request does not answer "unreadable" to the pages after it.
//
// A kept view is observation state that stays: what its read held on the
// memory line is released once the view is built, and the view is admitted
// as it is kept (admitKept); refused, it is answered and not kept.
type diagnosisCache struct {
	mu        sync.Mutex
	entries   map[string]*diagnosisEntry
	admitKept func(bytes uint64) bool
}

func (cache *diagnosisCache) get(ctx context.Context, id string, at time.Time, read func(context.Context) *diagnosisEntry) (*diagnosisEntry, bool) {
	cache.mu.Lock()
	if entry, ok := cache.entries[id]; ok && id != "" {
		cache.mu.Unlock()
		select {
		case <-entry.ready:
		case <-ctx.Done():
			return &diagnosisEntry{id: id, readError: "DIAGNOSIS_READ_CANCELLED", ready: closedReady()}, false
		}
		if entry.readError == "" && !at.After(entry.expires) {
			return entry, false
		}
		cache.mu.Lock()
		if cache.entries[id] == entry {
			delete(cache.entries, id)
		}
	}
	if id == "" {
		id = newDiagnosisID()
	}
	placeholder := &diagnosisEntry{id: id, ready: make(chan struct{})}
	cache.evictFor(at)
	cache.entries[id] = placeholder
	cache.mu.Unlock()

	// The read serves every request waiting on this placeholder, so it is
	// not bound to the one that started it: a first request that went away
	// would fail the others. It keeps the first request's values and its
	// own bound.
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), DiagnosisReadTimeout)
	defer cancel()
	holds := &pageHolds{}
	// A read that never returns -- it panicked -- still releases what it
	// held, answers the requests waiting on its placeholder, and is not kept.
	defer holds.release()
	filledIn := false
	defer func() {
		if filledIn {
			return
		}
		placeholder.readError = diagnosisReadFailed
		close(placeholder.ready)
		cache.mu.Lock()
		if cache.entries[id] == placeholder {
			delete(cache.entries, id)
		}
		cache.mu.Unlock()
	}()
	filled := read(holds.in(readCtx))
	cancel()
	placeholder.universe, placeholder.digest, placeholder.readAt = filled.universe, filled.digest, filled.readAt
	placeholder.view, placeholder.readError, placeholder.expires = filled.view, filled.readError, filled.expires
	placeholder.universeTook, placeholder.viewTook = filled.universeTook, filled.viewTook
	filledIn = true
	close(placeholder.ready)
	// What the read held is garbage now but the view, which stays with the
	// entry: the hold is released first, and the view admitted as it is kept.
	stored := holds.storedBytes()
	holds.release()
	// A view this process did not read whole (viewUnread) -- the memory line
	// deferred it, its snapshots or the registry could not be read, or this
	// read's own bound was reached while it waited on a shared read -- is
	// answered once and not kept: the next request asks again, and may be
	// read. So is one the memory line has no room to keep.
	drop := placeholder.readError != "" || (placeholder.view != nil && viewUnread(placeholder.view))
	if !drop && placeholder.view != nil && cache.admitKept != nil {
		drop = !cache.admitKept(stored * keptViewChargeNum / keptViewChargeDen)
	}
	if drop {
		cache.mu.Lock()
		if cache.entries[id] == placeholder {
			delete(cache.entries, id)
		}
		cache.mu.Unlock()
	}
	return placeholder, true
}

// evictFor makes room for one entry: expired ones first, then the one
// closest to expiring. Called with the lock held.
func (cache *diagnosisCache) evictFor(at time.Time) {
	for id, entry := range cache.entries {
		if isReady(entry) && at.After(entry.expires) {
			delete(cache.entries, id)
		}
	}
	for len(cache.entries) >= DiagnosisCacheEntries {
		oldest := ""
		for id, entry := range cache.entries {
			if !isReady(entry) {
				continue
			}
			if oldest == "" || entry.expires.Before(cache.entries[oldest].expires) {
				oldest = id
			}
		}
		if oldest == "" {
			return
		}
		delete(cache.entries, oldest)
	}
}

func isReady(entry *diagnosisEntry) bool {
	select {
	case <-entry.ready:
		return true
	default:
		return false
	}
}

func closedReady() chan struct{} {
	ready := make(chan struct{})
	close(ready)
	return ready
}

// ForwardFailed is the refusal a forwarder gives when a Leader exists and
// the hop to it failed. A diagnosis page is then refused rather than
// answered here: a page of LOOKUP_UNAVAILABLE rows whose coverage holds
// reads as a diagnosis, and it is not one.
const ForwardFailed = "FORWARD_FAILED"

// WithDiagnosis serves GET /api/diagnose[?cursor=&limit=]. The page is
// answered where the catalog is: a process without one forwards the page to
// the Leader once, the way a strategy's standing is forwarded, so every
// page of one diagnosis is decided against one catalog and one cache.
// GET /api/diagnose?summary=1 is a diagnosis of its own whose answer is the
// universe counted by verdict instead of its pages, for the page's first
// screen: the same rows, read the same way, summed where they are decided.
func WithDiagnosis(next http.Handler, service *Service, lookup StrategyLookupFunc, forward LeaderForward,
	universe UniverseReader, progress ProgressReader, replica string, now func() time.Time, stallAfter time.Duration,
	warmer *DiagnosisWarmer) http.Handler {
	if now == nil {
		now = time.Now
	}
	cache := &diagnosisCache{entries: map[string]*diagnosisEntry{}, admitKept: service.admitKeptView}
	counted := &summaryCache{}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/diagnose" {
			next.ServeHTTP(response, request)
			return
		}
		if request.Method != http.MethodGet {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		query := request.URL.Query()
		summaryOnly := query.Get("summary") != ""
		boardMode, viewMode := query.Get("board") != "", query.Get("view") != ""
		if boardMode || viewMode {
			var filter BoardFilter
			var limit int
			if viewMode {
				// One strategy's drawer: view=1 with its id and nothing else.
				for name := range query {
					if name != "view" && name != "strategy_id" {
						writeJSON(response, http.StatusBadRequest, map[string]string{"error": "INVALID_VIEW", "detail": "view=1 takes strategy_id only"})
						return
					}
				}
				if query.Get("view") != "1" || query.Get("strategy_id") == "" {
					writeJSON(response, http.StatusBadRequest, map[string]string{"error": "INVALID_VIEW", "detail": "view=1 takes strategy_id only"})
					return
				}
			} else {
				if summaryOnly || query.Get("board") != "1" {
					writeJSON(response, http.StatusBadRequest, map[string]string{"error": "INVALID_BOARD", "detail": "board=1 alone selects the strategy board"})
					return
				}
				var refusal map[string]any
				filter, limit, refusal = parseBoardQuery(query)
				if refusal != nil {
					writeJSON(response, http.StatusBadRequest, refusal)
					return
				}
			}
			if lookup == nil || universe == nil {
				writeJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "DIAGNOSIS_NOT_WIRED"})
				return
			}
			if !lookup("0").Available && forward != nil && request.Header.Get(forwardedHeader) == "" {
				forwarded, refusal := forward(response, request)
				if forwarded {
					return
				}
				if refusal == ForwardFailed {
					writeJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "LEADER_UNAVAILABLE", "reason": refusal})
					return
				}
			}
			at := now()
			counted.mu.Lock()
			if counted.board == nil || at.Sub(counted.at) >= DiagnosisSummaryFreshFor {
				pageCtx, releaseHolds := withPageHolds(request.Context())
				readCtx, cancel := context.WithTimeout(context.WithoutCancel(pageCtx), DiagnosisReadTimeout)
				entry := readDiagnosisEntry(readCtx, service, universe, at, stallAfter)
				cancel()
				entry.id = newDiagnosisID()
				if entry.readError != "" {
					releaseHolds()
					counted.mu.Unlock()
					writeJSON(response, http.StatusOK, map[string]string{"error": "DIAGNOSIS_UNREADABLE", "reason": entry.readError})
					return
				}
				countDiagnosis(counted, entry, replica, at, lookup, warmer)
				releaseHolds()
			}
			board := counted.board
			counted.mu.Unlock()
			if viewMode {
				view, found := board.view(query.Get("strategy_id"), DiagnosisSummaryFreshFor)
				if !found {
					writeJSON(response, http.StatusNotFound, map[string]string{"error": "STRATEGY_NOT_IN_UNIVERSE"})
					return
				}
				writeJSON(response, http.StatusOK, view)
				return
			}
			writeJSON(response, http.StatusOK, board.page(filter, query.Get("cursor"), limit, DiagnosisSummaryFreshFor))
			return
		}
		if summaryOnly && (query.Get("summary") != "1" || query.Has("cursor") || query.Has("limit")) {
			writeJSON(response, http.StatusBadRequest, map[string]string{"error": "INVALID_SUMMARY",
				"detail": "summary=1 takes no cursor or limit: it counts the whole universe"})
			return
		}
		var cursor DiagnosisCursor
		if raw := query.Get("cursor"); raw != "" {
			parsed, ok := ParseDiagnosisCursor(raw)
			if !ok {
				writeJSON(response, http.StatusBadRequest, map[string]string{"error": "INVALID_CURSOR"})
				return
			}
			cursor = parsed
		}
		limit := DiagnosisPageRows
		if raw := query.Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > DiagnosisPageRows {
				writeJSON(response, http.StatusBadRequest, map[string]any{"error": "INVALID_LIMIT", "max": DiagnosisPageRows})
				return
			}
			limit = n
		}
		if lookup == nil || universe == nil {
			writeJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "DIAGNOSIS_NOT_WIRED"})
			return
		}
		if !lookup("0").Available && forward != nil && request.Header.Get(forwardedHeader) == "" {
			forwarded, refusal := forward(response, request)
			if forwarded {
				return
			}
			if refusal == ForwardFailed {
				writeJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "LEADER_UNAVAILABLE", "reason": refusal})
				return
			}
			// No Leader at all: answer here, where every row says
			// LOOKUP_UNAVAILABLE and the universe is still counted.
		}
		at := now()
		// held is the count's lock while this request reads for it; it is
		// given back before any answer is written, so a slow reader of one
		// answer holds no other page behind it.
		held := false
		release := func() {
			if held {
				held = false
				counted.mu.Unlock()
			}
		}
		defer release()
		if summaryOnly {
			// One count at a time, and the last one answers while it is
			// fresh: pages asking together wait for one read, not one each.
			counted.mu.Lock()
			held = true
			if last := counted.body; last != nil && at.Sub(counted.at) < DiagnosisSummaryFreshFor {
				release()
				writeJSON(response, http.StatusOK, last)
				return
			}
		}
		var entry *diagnosisEntry
		fresh := true
		if summaryOnly {
			// Read for this answer alone and kept out of the diagnoses'
			// cache: a page asks on every refresh, and each ask kept there
			// would push out a diagnosis somebody is paging through.
			// Held until this answer is written, as any page's read.
			pageCtx, releaseHolds := withPageHolds(request.Context())
			defer releaseHolds()
			readCtx, cancel := context.WithTimeout(context.WithoutCancel(pageCtx), DiagnosisReadTimeout)
			entry = readDiagnosisEntry(readCtx, service, universe, at, stallAfter)
			cancel()
			entry.id = newDiagnosisID()
		} else {
			entry, fresh = cache.get(request.Context(), cursor.Diagnosis, at, func(ctx context.Context) *diagnosisEntry {
				return readDiagnosisEntry(ctx, service, universe, at, stallAfter)
			})
		}
		body := DiagnosisResponse{Diagnosis: entry.id, AnsweredBy: replica, Strategies: []DiagnosisRow{},
			Verdicts: DiagnosisVerdicts(), UnknownReasons: append([]string(nil), DiagnosisUnknownReasons...),
			SnapshotReread: fresh && cursor.Diagnosis != "", Progress: "not_wired", Warmed: warmer.Last()}
		if fresh {
			body.Timing = entry.timing()
		}
		body.Universe = DiagnosisUniverse{Status: "ok", Source: "strategy_ids", Count: len(entry.universe), Digest: entry.digest}
		if !entry.readAt.IsZero() {
			readAt := entry.readAt
			body.Universe.ReadAt = &readAt
		}
		body.Universe.Publication = lookup("0").Publication
		if entry.readError != "" {
			body.Universe.Status, body.Universe.Reason = "unreadable", entry.readError
			if summaryOnly {
				// No population, no count: the reason is the answer, and the
				// page says the strategies could not be counted rather than
				// that there are none.
				release()
				writeJSON(response, http.StatusOK, DiagnosisSummaryResponse{Diagnosis: entry.id, AnsweredBy: replica,
					Universe: body.Universe, Summary: DiagnosisSummary{ByVerdict: map[StateWord]int{}},
					Verdicts: body.Verdicts, Words: ProductWords(), Timing: body.Timing})
				return
			}
			body.Page = DiagnosisPage{ByVerdict: map[StateWord]int{}, Holds: false}
			writeJSON(response, http.StatusOK, body)
			return
		}
		if cursor.Digest != "" && cursor.Digest != entry.digest {
			body.UniverseChanged = &UniverseChange{FromDigest: cursor.Digest, ToDigest: entry.digest, Count: len(entry.universe)}
		}
		ctx := newDiagnosisContext(entry.view, replica, at)
		started := time.Now()
		if summaryOnly {
			summary := countDiagnosis(counted, entry, replica, at, lookup, warmer)
			release()
			writeJSON(response, http.StatusOK, summary)
			return
		}
		page := buildDiagnosisPage(entry.universe, cursor.After, limit, func(id string) DiagnosisRow {
			return diagnoseStrategy(id, lookup(id), ctx)
		})
		body.Timing.RowsMillis = time.Since(started).Milliseconds()
		if progress != nil {
			started = time.Now()
			objects := pageQueryGroups(page.Rows)
			found, unread, err := progress(request.Context(), objects)
			body.Timing.ProgressMillis = millisOf(time.Since(started))
			if err != nil {
				body.Progress = "unavailable"
				applyProgress(page.Rows, nil, nil, ProgressUnreadable)
			} else {
				body.Progress = "read"
				for _, reason := range unread {
					if reason == ProgressDeferred {
						body.ProgressDeferred++
					}
				}
				if body.ProgressDeferred > 0 {
					body.Progress, body.ProgressObjects = "partial", len(objects)
				}
				applyProgress(page.Rows, found, unread, "")
			}
		}
		body.Strategies, body.Page = page.Rows, page
		if page.Last != "" {
			body.NextCursor = DiagnosisCursor{Diagnosis: entry.id, Digest: entry.digest, After: page.Last}.Encode()
		}
		writeJSON(response, http.StatusOK, body)
	})
}

func readDiagnosisEntry(ctx context.Context, service *Service, universe UniverseReader, at time.Time, stallAfter time.Duration) *diagnosisEntry {
	entry := &diagnosisEntry{readAt: at, expires: at.Add(DiagnosisCacheTTL), stallAfter: stallAfter}
	started := time.Now()
	ids, err := universe(ctx)
	entry.universeTook = millisOf(time.Since(started))
	if err != nil {
		entry.readError = err.Error()
		return entry
	}
	entry.universe, entry.digest = NormalizeUniverse(ids)
	if service != nil {
		started = time.Now()
		view := service.View(ctx)
		Decide(&view, at, stallAfter)
		entry.view = &view
		entry.viewTook = millisOf(time.Since(started))
	}
	return entry
}

// timing is the entry's read, as a page's timing.
func (entry *diagnosisEntry) timing() DiagnosisTiming {
	return DiagnosisTiming{UniverseMillis: entry.universeTook, ViewMillis: entry.viewTook}
}

func newDiagnosisID() string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

// countDiagnosis decides every strategy of the entry's universe once and keeps
// both answers that one pass makes: the first-screen count and the strategy
// board. The caller holds counted.mu.
func countDiagnosis(counted *summaryCache, entry *diagnosisEntry, replica string, at time.Time,
	lookup func(string) StrategyLookupFacts, warmer *DiagnosisWarmer) *DiagnosisSummaryResponse {
	started := time.Now()
	universe := DiagnosisUniverse{Status: "ok", Source: "strategy_ids", Count: len(entry.universe), Digest: entry.digest}
	if !entry.readAt.IsZero() {
		readAt := entry.readAt
		universe.ReadAt = &readAt
	}
	universe.Publication = lookup("0").Publication
	summary := DiagnosisSummaryResponse{Diagnosis: entry.id, AnsweredBy: replica, Universe: universe,
		Verdicts: DiagnosisVerdicts(), Words: ProductWords(), Timing: entry.timing()}
	ctx := newDiagnosisContext(entry.view, replica, at)
	decided := make(map[string]DiagnosisRow, len(entry.universe))
	for _, id := range entry.universe {
		decided[id] = diagnoseStrategy(id, lookup(id), ctx)
	}
	row := func(id string) DiagnosisRow { return decided[id] }
	summary.Summary = summarizeDiagnosis(entry.universe, entry.view, row)
	var lines []StrategyLine
	var due *DueView
	var census *ScheduleCensus
	var waits *lateWaitIndex
	if entry.view != nil {
		waits = lateWaitsOf(entry.view)
		summary.OverdueEpisodes = entry.view.OverdueEpisodes
		lines, due, census = StrategyLines(entry.view, at), entry.view.Due, entry.view.Schedule
	}
	summary.Timing.RowsMillis = time.Since(started).Milliseconds()
	counted.at, counted.body = at, &summary
	counted.board = buildBoard(entry.id, at, entry.digest, entry.universe, row, lines, due, census, waits)
	if entry.view != nil {
		counted.board.outages = strategyStoreOutages(entry.view, at, entry.stallAfter)
		counted.board.noOutage = noStrategyStoreOutage(entry.view, at)
	}
	return &summary
}

// BoardMaxRows bounds one board page. It is the diagnosis page's bound over the
// same universe -- a board row carries words only and is smaller than a
// diagnosis row (about 390 bytes against about 1 KB measured on a 3,355-strategy
// deployment), so whatever the channel carries for a diagnosis page it carries
// for a board page. The facets always count the whole universe.
const BoardMaxRows = DiagnosisPageRows

// BoardDefaultRows is a board page when the page names no limit.
const BoardDefaultRows = 50

// parseBoardQuery reads the board's selection: each facet a comma list of its
// closed words, a strategy id, and a page size.
func parseBoardQuery(query url.Values) (BoardFilter, int, map[string]any) {
	filter := BoardFilter{StrategyID: query.Get("strategy_id")}
	words := func(name string, known func(string) bool) (map[string]bool, map[string]any) {
		raw := query.Get(name)
		if raw == "" {
			return nil, nil
		}
		set := map[string]bool{}
		for _, word := range strings.Split(raw, ",") {
			if !known(word) {
				return nil, map[string]any{"error": "BOARD_FILTER_UNKNOWN", "facet": name, "value": word}
			}
			set[word] = true
		}
		return set, nil
	}
	states, refusal := words("state", func(word string) bool {
		for _, known := range DiagnosisVerdicts() {
			if string(known) == word {
				return true
			}
		}
		return false
	})
	if refusal != nil {
		return filter, 0, refusal
	}
	for word := range states {
		if filter.State == nil {
			filter.State = map[StateWord]bool{}
		}
		filter.State[StateWord(word)] = true
	}
	actions, refusal := words("action", func(word string) bool { return knownActionWord(ActionWord(word)) })
	if refusal != nil {
		return filter, 0, refusal
	}
	for word := range actions {
		if filter.Action == nil {
			filter.Action = map[ActionWord]bool{}
		}
		filter.Action[ActionWord(word)] = true
	}
	capabilities, refusal := words("capability", func(word string) bool {
		switch word {
		case CapabilitySupported, CapabilityPartial, CapabilityUnsupportedByDesign, CapabilityNotYetSupported:
			return true
		}
		return false
	})
	if refusal != nil {
		return filter, 0, refusal
	}
	filter.Capability = capabilities
	// An area reads the capability words in that area, so it comes with
	// them; a reason is any code, and one no strategy carries selects none.
	if area := query.Get("area"); area != "" {
		known := false
		for _, candidate := range CapabilityAreas {
			known = known || candidate == area
		}
		if !known {
			return filter, 0, map[string]any{"error": "BOARD_FILTER_UNKNOWN", "facet": "area", "value": area}
		}
		if len(filter.Capability) == 0 {
			return filter, 0, map[string]any{"error": "BOARD_AREA_WITHOUT_CAPABILITY", "detail": "area selects a capability outcome in that area: give capability too"}
		}
		filter.Area = area
	}
	if reason := query.Get("reason"); reason != "" {
		if len(reason) > 256 {
			return filter, 0, map[string]any{"error": "BOARD_FILTER_UNKNOWN", "facet": "reason", "value": reason[:64]}
		}
		filter.Reason = reason
	}
	punctualities, refusal := words("punctuality", func(word string) bool {
		for _, known := range Punctualities {
			if string(known) == word {
				return true
			}
		}
		return false
	})
	if refusal != nil {
		return filter, 0, refusal
	}
	for word := range punctualities {
		if filter.Punctuality == nil {
			filter.Punctuality = map[Punctuality]bool{}
		}
		filter.Punctuality[Punctuality(word)] = true
	}
	urgencies, refusal := words("urgency", func(word string) bool {
		for _, known := range Urgencies {
			if string(known) == word {
				return true
			}
		}
		return false
	})
	if refusal != nil {
		return filter, 0, refusal
	}
	for word := range urgencies {
		if filter.Urgency == nil {
			filter.Urgency = map[Urgency]bool{}
		}
		filter.Urgency[Urgency(word)] = true
	}
	limit := BoardDefaultRows
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > BoardMaxRows {
			return filter, 0, map[string]any{"error": "INVALID_LIMIT", "max": BoardMaxRows}
		}
		limit = n
	}
	return filter, limit, nil
}
