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
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A store outage is this deployment's own Redis-backed stores not answering
// long enough that Slots were given up: the windows are never evaluated, and
// each object keeps one retained span of them (SkippedSpan). A span is
// attributed to the outage by the store failure its object's rounds reported
// from the replay window before its first skip up to the skip
// (SkippedSpan.DependencyReason, StoreOutageCodes); spans so attributed whose
// times lie within one replay window of each other are one outage. Nothing
// here is stored: the spans are the replicas' retained records, and what a
// replica did not see - before its process started, or on a build before
// the attribution - is named as the coverage, so that no outage seen never
// reads as no outage.

const (
	// StoreOutageMaxOutages bounds the outages one answer lists, newest first.
	StoreOutageMaxOutages = 16
	// StoreOutageMaxRows bounds the rows listed under one outage, and
	// StoreOutageDefaultRows is how many a read gets without asking.
	StoreOutageMaxRows     = 200
	StoreOutageDefaultRows = 50
	// storeOutageMaxStrategies bounds the strategies an outage keeps to
	// count them distinct; past it the count is at least that.
	storeOutageMaxStrategies = 512
)

// Store outage answers' states, closed.
const (
	// StoreOutageStateOutages: at least one outage seen.
	StoreOutageStateOutages = "outages"
	// StoreOutageStateNoneSeen: none seen since Coverage.Since - a statement
	// about that interval, not about all time.
	StoreOutageStateNoneSeen = "none_seen"
	// StoreOutageStateCoverageUnknown: no publishing replica says when it
	// started, so there is no interval to say "none" of.
	StoreOutageStateCoverageUnknown = "coverage_unknown"
)

// StoreOutageRow is one object's span of Slots given up in an outage.
type StoreOutageRow struct {
	QueryGroup string        `json:"query_group"`
	Replica    string        `json:"replica,omitempty"`
	Strategies []StrategyRef `json:"strategies,omitempty"`
	FirstSlot  int64         `json:"first_slot"`
	LastSlot   int64         `json:"last_slot"`
	Slots      int           `json:"slots"`
	FirstAt    time.Time     `json:"first_at"`
	At         time.Time     `json:"at"`
	// DependencyReason is the store failure the span is attributed to, and
	// Reason the span's own last query failure, when it had one: side by
	// side, neither overwriting the other.
	DependencyReason string     `json:"dependency_reason"`
	DependencyAt     *time.Time `json:"dependency_at,omitempty"`
	Reason           string     `json:"reason,omitempty"`
}

// StoreOutage is one outage: from the first span's first skip to the last
// span's last, the objects and strategies whose windows were given up in
// it, and the store failures, by code, the spans were attributed to.
type StoreOutage struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// Objects is the distinct objects: an object handed over in the outage
	// can have a span given up on each of two replicas.
	Objects int `json:"objects"`
	// Strategies is the distinct strategies; StrategiesAtLeast says the
	// count stopped at its bound and is a floor.
	Strategies        int  `json:"strategies"`
	StrategiesAtLeast bool `json:"strategies_at_least,omitempty"`
	// AtLeast says the outage's spans were cut at the part bound
	// (storeOutagePartMaxSpans): its counts are floors.
	AtLeast bool           `json:"at_least,omitempty"`
	Slots   int            `json:"slots"`
	Reasons map[string]int `json:"reasons"`
	Line    string         `json:"line"`
	// Rows are the spans, oldest first, cut to the read's bound; RowsCut
	// says some were left out (Objects counts them all).
	Rows    []StoreOutageRow `json:"rows,omitempty"`
	RowsCut bool             `json:"rows_cut,omitempty"`

	strategyKeys map[string]struct{}
	objectKeys   map[string]struct{}
}

// StoreOutageReplica is one publishing replica's coverage: what it can have
// seen starts at its process start, the store failures being kept in memory.
type StoreOutageReplica struct {
	Replica   string    `json:"replica"`
	StartedAt time.Time `json:"started_at,omitempty"`
}

// StoreOutageCoverage is the interval an answer speaks for: Since is the
// latest process start among the publishing replicas, from when every one of
// them was watching; a replica's earlier outages are gone with its process.
type StoreOutageCoverage struct {
	Since       *time.Time           `json:"since,omitempty"`
	Replicas    []StoreOutageReplica `json:"replicas"`
	Limitations []string             `json:"limitations"`
	// Unattributed is the retained spans given up with no store failure
	// behind them (unattributedSkips): a store failure this process did not
	// see stays among them, so "none seen" is said beside their count.
	Unattributed int `json:"unattributed_spans"`
}

// StoreOutagesResponse is the outage.list answer.
type StoreOutagesResponse struct {
	State      string              `json:"state"`
	Line       string              `json:"line"`
	Outages    []StoreOutage       `json:"outages"`
	OutagesCut bool                `json:"outages_cut,omitempty"`
	Coverage   StoreOutageCoverage `json:"coverage"`
	// Codes is the closed set of store failures a span is attributed by
	// (StoreOutageCodes), and ToleranceSeconds how near in time two spans
	// must lie to be one outage: the replay window.
	Codes            []string  `json:"codes"`
	ToleranceSeconds int64     `json:"tolerance_seconds"`
	ReadAt           time.Time `json:"read_at"`
}

// storeOutageSpans is the view's retained spans attributed to a store
// outage, optionally only those of one strategy, oldest first.
func storeOutageSpans(view *View, strategyID string) []StoreOutageRow {
	rows := []StoreOutageRow{}
	for queryGroup, span := range view.GapSkips {
		if !storeOutageCode(span.DependencyReason) || span.FirstAt.IsZero() {
			continue
		}
		if strategyID != "" && !servesStrategy(span.Strategies, strategyID) {
			continue
		}
		rows = append(rows, StoreOutageRow{QueryGroup: queryGroup, Replica: span.Replica, Strategies: span.Strategies,
			FirstSlot: span.FirstSlot, LastSlot: span.LastSlot, Slots: span.Slots, FirstAt: span.FirstAt, At: span.At,
			DependencyReason: span.DependencyReason, DependencyAt: span.DependencyAt, Reason: span.Reason})
	}
	sortStoreOutageSpans(rows)
	return rows
}

// storeOutagePartMaxSpans bounds the spans one replica's part, and the
// merged part, carries to the overview.
const storeOutagePartMaxSpans = 4096

// sortStoreOutageSpans orders spans oldest first, then by object.
func sortStoreOutageSpans(rows []StoreOutageRow) {
	sort.Slice(rows, func(l, r int) bool {
		if !rows[l].FirstAt.Equal(rows[r].FirstAt) {
			return rows[l].FirstAt.Before(rows[r].FirstAt)
		}
		return rows[l].QueryGroup < rows[r].QueryGroup
	})
}

// boundStoreOutageSpans keeps the newest storeOutagePartMaxSpans of spans
// ordered oldest first, and says whether any were left out.
func boundStoreOutageSpans(rows []StoreOutageRow) ([]StoreOutageRow, bool) {
	if len(rows) <= storeOutagePartMaxSpans {
		return rows, false
	}
	return append([]StoreOutageRow(nil), rows[len(rows)-storeOutagePartMaxSpans:]...), true
}

func servesStrategy(strategies []StrategyRef, strategyID string) bool {
	for _, ref := range strategies {
		if ref.StrategyID == strategyID {
			return true
		}
	}
	return false
}

// foldStoreOutages folds spans, oldest first, into outages: a span starting
// within tolerance of the current outage's end joins it. Rows keeps at most
// rows of them per outage.
func foldStoreOutages(spans []StoreOutageRow, tolerance time.Duration, rows int) []StoreOutage {
	var outages []StoreOutage
	for _, span := range spans {
		if n := len(outages); n == 0 || span.FirstAt.After(outages[n-1].To.Add(tolerance)) {
			outages = append(outages, StoreOutage{From: span.FirstAt, To: span.At, Reasons: map[string]int{},
				strategyKeys: map[string]struct{}{}, objectKeys: map[string]struct{}{}})
		}
		outage := &outages[len(outages)-1]
		if span.At.After(outage.To) {
			outage.To = span.At
		}
		outage.objectKeys[span.QueryGroup] = struct{}{}
		outage.Slots += span.Slots
		outage.Reasons[span.DependencyReason]++
		outage.addStrategies(span.Strategies)
		switch {
		case rows <= 0:
		case len(outage.Rows) < rows:
			outage.Rows = append(outage.Rows, span)
		default:
			outage.RowsCut = true
		}
	}
	for index := range outages {
		outages[index].settle()
	}
	return outages
}

func (outage *StoreOutage) addStrategies(strategies []StrategyRef) {
	for _, ref := range strategies {
		key := ref.BusinessID + "/" + ref.StrategyID
		if _, kept := outage.strategyKeys[key]; kept {
			continue
		}
		if len(outage.strategyKeys) >= storeOutageMaxStrategies {
			outage.StrategiesAtLeast = true
			continue
		}
		outage.strategyKeys[key] = struct{}{}
	}
}

// settle fills the count and the line from what was folded in.
func (outage *StoreOutage) settle() {
	outage.Objects, outage.Strategies = len(outage.objectKeys), len(outage.strategyKeys)
	objects, strategies := fmt.Sprint(outage.Objects), fmt.Sprint(outage.Strategies)
	if outage.AtLeast {
		objects = "至少 " + objects
	}
	if outage.AtLeast || outage.StrategiesAtLeast {
		strategies = "至少 " + strategies
	}
	outage.Line = fmt.Sprintf("Redis 不可用 %s–%s：%s 个对象、%s 条策略丢了这些窗口",
		outageClock(outage.From), outageClock(outage.To), objects, strategies)
}

func outageClock(at time.Time) string {
	return at.UTC().Format("2006-01-02 15:04:05Z")
}

// storeOutageCoverage is the coverage of the view's publishing replicas.
func storeOutageCoverage(view *View) StoreOutageCoverage {
	coverage := StoreOutageCoverage{Replicas: []StoreOutageReplica{}, Limitations: []string{
		"只看得到各进程启动以后的存储失败：失败只记在进程内存里，进程重启就丢；",
		"更早的构建不归因，它们留下的跨度不列；",
		"一个对象只保留最近一段被放弃的 Slot，同一对象更早的一段已被它替换；",
		"对象换到别的进程后，新进程看不到旧进程见过的存储失败，换手前后被放弃的窗口可能未归因。",
	}}
	known := true
	for _, replica := range view.PerReplica {
		coverage.Replicas = append(coverage.Replicas, StoreOutageReplica{Replica: replica.Replica, StartedAt: replica.StartedAt})
		if replica.StartedAt.IsZero() {
			known = false
			continue
		}
		if coverage.Since == nil || replica.StartedAt.After(*coverage.Since) {
			started := replica.StartedAt
			coverage.Since = &started
		}
	}
	sort.Slice(coverage.Replicas, func(l, r int) bool { return coverage.Replicas[l].Replica < coverage.Replicas[r].Replica })
	if !known || len(coverage.Replicas) == 0 {
		coverage.Since = nil
	}
	return coverage
}

// StoreOutageRefusal is a read the route refuses, by name.
type StoreOutageRefusal struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// storeOutages serves GET /api/outages from the fleet view: rows bounds the
// rows under each outage, strategy_id keeps one strategy's spans. stallAfter
// is the deployment's replay budget, the folding tolerance.
func storeOutages(response http.ResponseWriter, request *http.Request, service *Service, now func() time.Time, stallAfter time.Duration) {
	if request.Method != http.MethodGet {
		response.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	rows := StoreOutageDefaultRows
	if raw := request.URL.Query().Get("rows"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > StoreOutageMaxRows {
			writeJSON(response, http.StatusBadRequest, StoreOutageRefusal{Error: "invalid_rows",
				Message: fmt.Sprintf("rows must be 1-%d", StoreOutageMaxRows)})
			return
		}
		rows = parsed
	}
	view := service.View(request.Context())
	writeJSON(response, http.StatusOK, StoreOutagesOf(&view, now(), stallAfter, rows, request.URL.Query().Get("strategy_id")))
}

// StoreOutagesOf is the outages the view's retained spans show, newest
// first, with rows rows at most under each, and only the spans of
// strategyID when it is set. tolerance is the replay window; zero or less
// is DefaultSkipDependencyWindow.
func StoreOutagesOf(view *View, now time.Time, tolerance time.Duration, rows int, strategyID string) StoreOutagesResponse {
	coverage := storeOutageCoverage(view)
	coverage.Unattributed = unattributedSkips(view, strategyID)
	return storeOutagesFromSpans(storeOutageSpans(view, strategyID), coverage, now, tolerance, rows, false)
}

// unattributedSkips counts the view's retained spans, optionally of one
// strategy, given up with no store failure attributed to them.
func unattributedSkips(view *View, strategyID string) int {
	count := 0
	for _, span := range view.GapSkips {
		if storeOutageCode(span.DependencyReason) || strategyID != "" && !servesStrategy(span.Strategies, strategyID) {
			continue
		}
		count++
	}
	return count
}

// StoreOutageSummary is the outages as a page shows them: the lines and the
// counts, with the interval they speak for, and nothing that names an
// object, a replica or a revision - outage.list lists the rows.
type StoreOutageSummary struct {
	State       string        `json:"state"`
	Line        string        `json:"line"`
	Outages     []StoreOutage `json:"outages"`
	OutagesCut  bool          `json:"outages_cut,omitempty"`
	Since       *time.Time    `json:"since,omitempty"`
	Limitations []string      `json:"limitations"`
	// Unattributed is StoreOutageCoverage.Unattributed.
	Unattributed int `json:"unattributed_spans"`
}

// summary is the answer without its rows, replicas and codes.
func (response StoreOutagesResponse) summary() StoreOutageSummary {
	outages := make([]StoreOutage, 0, len(response.Outages))
	for _, outage := range response.Outages {
		outage.Rows, outage.RowsCut = nil, false
		outages = append(outages, outage)
	}
	summary := StoreOutageSummary{State: response.State, Line: response.Line, Outages: outages, OutagesCut: response.OutagesCut,
		Limitations: response.Coverage.Limitations, Unattributed: response.Coverage.Unattributed}
	if since := response.Coverage.Since; since != nil {
		at := *since
		summary.Since = &at
	}
	return summary
}

// StoreOutagesOverview is the overview's line: the merged parts' spans
// folded the same way.
func StoreOutagesOverview(view *View, part ReplicaPart, now time.Time, tolerance time.Duration) StoreOutageSummary {
	coverage := storeOutageCoverage(view)
	coverage.Unattributed = part.StoreOutageUnattributed
	return storeOutagesFromSpans(part.StoreOutageSpans, coverage, now, tolerance, 0, part.StoreOutageSpansCut).summary()
}

// strategyStoreOutages is each strategy's outages, for the drawer: the
// view's spans of that strategy folded alone, by strategy ID.
func strategyStoreOutages(view *View, now time.Time, tolerance time.Duration) map[string]StoreOutageSummary {
	coverage := storeOutageCoverage(view)
	byStrategy := map[string][]StoreOutageRow{}
	for _, span := range storeOutageSpans(view, "") {
		seen := map[string]bool{}
		for _, ref := range span.Strategies {
			if !seen[ref.StrategyID] {
				seen[ref.StrategyID] = true
				byStrategy[ref.StrategyID] = append(byStrategy[ref.StrategyID], span)
			}
		}
	}
	unattributed := map[string]int{}
	for _, span := range view.GapSkips {
		if storeOutageCode(span.DependencyReason) {
			continue
		}
		seen := map[string]bool{}
		for _, ref := range span.Strategies {
			if !seen[ref.StrategyID] {
				seen[ref.StrategyID] = true
				unattributed[ref.StrategyID]++
				if _, listed := byStrategy[ref.StrategyID]; !listed {
					byStrategy[ref.StrategyID] = nil
				}
			}
		}
	}
	outages := make(map[string]StoreOutageSummary, len(byStrategy))
	for id, spans := range byStrategy {
		own := coverage
		own.Limitations = append([]string(nil), coverage.Limitations...)
		own.Unattributed = unattributed[id]
		outages[id] = storeOutagesFromSpans(spans, own, now, tolerance, 0, false).summary()
	}
	return outages
}

// unknownStoreOutage is the answer when no view was read to fold.
func unknownStoreOutage() StoreOutageSummary {
	return storeOutagesFromSpans(nil, StoreOutageCoverage{Replicas: []StoreOutageReplica{}, Limitations: []string{}}, time.Time{}, 0, 0, false).summary()
}

// noStrategyStoreOutage is the drawer's answer for a strategy no span names.
func noStrategyStoreOutage(view *View, now time.Time) StoreOutageSummary {
	return storeOutagesFromSpans(nil, storeOutageCoverage(view), now, 0, 0, false).summary()
}

// storeOutagesFromSpans folds spans ordered oldest first into the answer.
// spansCut says older spans were left out before they got here, which the
// answer names beside its coverage.
func storeOutagesFromSpans(spans []StoreOutageRow, coverage StoreOutageCoverage, now time.Time, tolerance time.Duration, rows int, spansCut bool) StoreOutagesResponse {
	if tolerance <= 0 {
		tolerance = DefaultSkipDependencyWindow
	}
	if spansCut {
		coverage.Limitations = append(coverage.Limitations, fmt.Sprintf("跨度超过 %d 段，只折算了最新的部分。", storeOutagePartMaxSpans))
	}
	outages := foldStoreOutages(spans, tolerance, rows)
	// The cut keeps the newest spans: only the oldest outage can have lost
	// some, and its counts are floors.
	if spansCut && len(outages) > 0 {
		outages[0].AtLeast = true
		outages[0].settle()
	}
	sort.Slice(outages, func(l, r int) bool { return outages[l].From.After(outages[r].From) })
	response := StoreOutagesResponse{Outages: []StoreOutage{}, Coverage: coverage, Codes: StoreOutageCodes(),
		ToleranceSeconds: int64(tolerance / time.Second), ReadAt: now.UTC()}
	if len(outages) > StoreOutageMaxOutages {
		outages, response.OutagesCut = outages[:StoreOutageMaxOutages], true
	}
	response.Outages = append(response.Outages, outages...)
	switch {
	case len(outages) > 0:
		response.State = StoreOutageStateOutages
		lines := make([]string, 0, len(outages))
		for _, outage := range outages {
			lines = append(lines, outage.Line)
		}
		response.Line = strings.Join(lines, "；")
	case response.Coverage.Since == nil:
		response.State = StoreOutageStateCoverageUnknown
		response.Line = "说不出有没有：没有一个运行中的进程报出启动时刻"
	default:
		response.State = StoreOutageStateNoneSeen
		response.Line = "自 " + outageClock(*response.Coverage.Since) + " 起未见 Redis 不可用造成的漏检窗口"
	}
	if response.Coverage.Unattributed > 0 {
		response.Line += fmt.Sprintf("；另有 %d 段被放弃的窗口未归因到存储", response.Coverage.Unattributed)
	}
	return response
}
