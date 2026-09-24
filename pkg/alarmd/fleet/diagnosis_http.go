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
	"strconv"
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
	// Progress is read, not_wired, or unavailable: whether the Plans'
	// persisted progress could be read for this page.
	Progress        string          `json:"progress"`
	UniverseChanged *UniverseChange `json:"universe_changed,omitempty"`
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

type diagnosisEntry struct {
	id        string
	universe  []string
	digest    string
	readAt    time.Time
	view      *View
	readError string
	expires   time.Time
	// universeTook and viewTook are how long the read that filled the entry
	// took for each; viewTook is nil where there is no service to read.
	universeTook *int64
	viewTook     *int64
	// ready is closed when the read that fills the entry has finished; a
	// request for the same diagnosis waits on it instead of reading again.
	ready chan struct{}
}

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
type diagnosisCache struct {
	mu      sync.Mutex
	entries map[string]*diagnosisEntry
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
	filled := read(readCtx)
	cancel()
	placeholder.universe, placeholder.digest, placeholder.readAt = filled.universe, filled.digest, filled.readAt
	placeholder.view, placeholder.readError, placeholder.expires = filled.view, filled.readError, filled.expires
	placeholder.universeTook, placeholder.viewTook = filled.universeTook, filled.viewTook
	close(placeholder.ready)
	if placeholder.readError != "" {
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
func WithDiagnosis(next http.Handler, service *Service, lookup StrategyLookupFunc, forward LeaderForward,
	universe UniverseReader, progress ProgressReader, replica string, now func() time.Time, stallAfter time.Duration,
	warmer *DiagnosisWarmer) http.Handler {
	if now == nil {
		now = time.Now
	}
	cache := &diagnosisCache{entries: map[string]*diagnosisEntry{}}
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
		entry, fresh := cache.get(request.Context(), cursor.Diagnosis, at, func(ctx context.Context) *diagnosisEntry {
			return readDiagnosisEntry(ctx, service, universe, at, stallAfter)
		})
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
			body.Page = DiagnosisPage{ByVerdict: map[StateWord]int{}, Holds: false}
			writeJSON(response, http.StatusOK, body)
			return
		}
		if cursor.Digest != "" && cursor.Digest != entry.digest {
			body.UniverseChanged = &UniverseChange{FromDigest: cursor.Digest, ToDigest: entry.digest, Count: len(entry.universe)}
		}
		ctx := newDiagnosisContext(entry.view, replica, at)
		started := time.Now()
		page := buildDiagnosisPage(entry.universe, cursor.After, limit, func(id string) DiagnosisRow {
			return diagnoseStrategy(id, lookup(id), ctx)
		})
		body.Timing.RowsMillis = time.Since(started).Milliseconds()
		if progress != nil {
			started = time.Now()
			found, failed, err := progress(request.Context(), pageQueryGroups(page.Rows))
			body.Timing.ProgressMillis = millisOf(time.Since(started))
			if err != nil {
				body.Progress = "unavailable"
				applyProgress(page.Rows, nil, nil, ProgressUnreadable)
			} else {
				body.Progress = "read"
				applyProgress(page.Rows, found, failed, "")
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
	entry := &diagnosisEntry{readAt: at, expires: at.Add(DiagnosisCacheTTL)}
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
