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
	"sort"
	"strconv"
	"strings"
	"time"
)

// Punctuality is whether a strategy is being detected on time: the worst of
// its detection objects by the due index's own rule (DueClasses), so the
// strategy page and the overview's late and overdue count read one source.
type Punctuality string

const (
	PunctualityOnTime       Punctuality = "on_time"
	PunctualityLate         Punctuality = "late"
	PunctualityOverdue      Punctuality = "overdue"
	PunctualityNotStarted   Punctuality = "not_started"
	PunctualityNotDetecting Punctuality = "not_detecting"
	PunctualityUnknown      Punctuality = "unknown"
)

// Punctualities is the closed list, in the page's order.
var Punctualities = []Punctuality{PunctualityOnTime, PunctualityLate, PunctualityOverdue,
	PunctualityNotStarted, PunctualityNotDetecting, PunctualityUnknown}

// punctualityRank orders a strategy's objects worst first: a known overdue or
// late object outranks one nobody can read, so a strategy with one listed
// overdue object and one cut from a list is overdue and counted among the
// late strategies; an unreadable object outranks one not started and one on
// time.
var punctualityRank = map[Punctuality]int{
	PunctualityOverdue: 5, PunctualityLate: 4, PunctualityUnknown: 3, PunctualityNotStarted: 2, PunctualityOnTime: 1,
}

// DueView is the replicas' due lists merged, for the strategy page. It is
// held in memory with the view a diagnosis decides on and never written into
// the health answer: the lists are the size of what is wrong, and the health
// answer is read on every page.
type DueView struct {
	// classes is each replica's own lists, by replica then object: an object
	// is read from the replica its plan names, so a previous owner's snapshot
	// still listing it never decides it.
	classes map[string]map[string]Punctuality
	// complete is, per replica, whether its lists and its owned set were
	// published whole: an object of a complete replica in no list is on
	// time; of any other, unknown.
	complete map[string]bool
	// Cut says some replica's lists or owned set were cut, or a replica
	// counted objects without naming them: every count drawn from the
	// lists is then a lower bound.
	Cut bool
}

// aggregateDue merges the replicas' due lists. Absent on every replica is no
// due index anywhere, and the view stays without one.
func aggregateDue(view *View, snapshots []Snapshot) {
	var due *DueView
	for _, snapshot := range snapshots {
		if snapshot.Schedule == nil {
			continue
		}
		if due == nil {
			due = &DueView{classes: map[string]map[string]Punctuality{}, complete: map[string]bool{}}
		}
		if snapshot.Due == nil {
			// A census with no names: a build before the lists, during a
			// rollout. Its objects read unknown and every count is "at least".
			due.complete[snapshot.Replica] = false
			due.Cut = true
			continue
		}
		listed := snapshot.Due
		whole := !listed.LateCut && !listed.OverdueCut && !listed.NeverCut && len(snapshot.OwnedObjects) >= snapshot.Owned
		due.complete[snapshot.Replica] = whole
		if !whole {
			due.Cut = true
		}
		own := map[string]Punctuality{}
		for _, id := range listed.Late {
			own[id] = PunctualityLate
		}
		for _, id := range listed.Overdue {
			own[id] = PunctualityOverdue
		}
		for _, id := range listed.Never {
			own[id] = PunctualityNotStarted
		}
		due.classes[snapshot.Replica] = own
	}
	view.Due = due
}

// objectPunctuality is one object's class as its own replica gives it: listed
// there, or on time when that replica published whole, or unknown.
func (due *DueView) objectPunctuality(queryGroup, replica string) Punctuality {
	if due == nil {
		return PunctualityUnknown
	}
	if class, listed := due.classes[replica][queryGroup]; listed {
		return class
	}
	if due.complete[replica] {
		return PunctualityOnTime
	}
	return PunctualityUnknown
}

// strategyPunctuality is the worst of the strategy's objects; a strategy with
// no object is not being detected.
func strategyPunctuality(row DiagnosisRow, due *DueView) Punctuality {
	worst, rank := PunctualityNotDetecting, 0
	for _, plan := range row.Plans {
		if plan.QueryGroup == "" {
			continue
		}
		class := due.objectPunctuality(plan.QueryGroup, plan.Replica)
		if punctualityRank[class] > rank {
			worst, rank = class, punctualityRank[class]
		}
	}
	return worst
}

// BoardRow is one strategy as the strategy page lists it: the server's words
// for its state, who acts and how sure its duration is, and nothing of the
// developer's coordinates (objects, replicas, revisions, Slots).
type BoardRow struct {
	StrategyID string `json:"strategy_id"`
	// Tenant is the strategy's tenant (DiagnosisRow.Tenant), always present:
	// empty is unknown. A caller passes it back as it is to
	// strategy.linkd_reconcile, and does not reconcile when it is empty.
	Tenant          string      `json:"tenant"`
	BusinessID      string      `json:"business_id,omitempty"`
	State           StateWord   `json:"state"`
	StateWord       string      `json:"state_word"`
	Action          ActionWord  `json:"action,omitempty"`
	ActionWord      string      `json:"action_word,omitempty"`
	Watch           WatchReason `json:"watch,omitempty"`
	WatchWord       string      `json:"watch_word,omitempty"`
	CapabilityClass string      `json:"capability_class"`
	Line            string      `json:"line"`
	Punctuality     Punctuality `json:"punctuality"`
	// LateWait is what the strategy's worst late or overdue object waited
	// on (strategyLateWait); present exactly when Punctuality is late or
	// overdue.
	LateWait *LateWait `json:"late_wait,omitempty"`
	// Urgency is whether acting is needed, optional or not asked
	// (urgencyOf), and Reason the cause in one sentence with the numbers it
	// was written from (strategyReasonOf); Reason is absent exactly when the
	// strategy detects under no check.
	Urgency     Urgency         `json:"urgency"`
	UrgencyWord string          `json:"urgency_word"`
	Reason      *StrategyReason `json:"reason,omitempty"`
	Since       *time.Time      `json:"since,omitempty"`
	SinceBasis  SinceBasis      `json:"since_basis,omitempty"`
	SinceWord   string          `json:"since_word,omitempty"`
	// OpenAlerts is the strategy's open alerts on alarmd's copy of the
	// consumer's set; absent when not known, at_least when a replica could
	// not count it. The alerts themselves are read in Linkd.
	OpenAlerts *BoardCount `json:"open_alerts,omitempty"`
	KBAnchor   string      `json:"kb_anchor"`
	// areas is the row's outcome in each capability area it has a record
	// in, and reasons its refusal codes, counted as the Leader counts the
	// overview's matrix (CountCapability): the overview's drill keys select
	// on them. Neither is a page field.
	areas   map[string]string
	reasons map[string]bool
}

// BoardFacets counts the whole universe on each facet, never the page.
type BoardFacets struct {
	State       map[StateWord]int   `json:"state"`
	Action      map[ActionWord]int  `json:"action"`
	Capability  map[string]int      `json:"capability"`
	Punctuality map[Punctuality]int `json:"punctuality"`
	Urgency     map[Urgency]int     `json:"urgency"`
}

// BoardCount is a count the page shows with how sure it is: AtLeast when a
// list it is drawn from was cut, so the page writes "至少" and never an exact
// number.
type BoardCount struct {
	Value   int  `json:"value"`
	AtLeast bool `json:"at_least,omitempty"`
}

// BoardFilter is the page's selection; an empty field selects every value.
type BoardFilter struct {
	State       map[StateWord]bool
	Action      map[ActionWord]bool
	Capability  map[string]bool
	Punctuality map[Punctuality]bool
	Urgency     map[Urgency]bool
	StrategyID  string
	// Area reads Capability in one area: the row's outcome there, supported
	// when it has no record in it, as the matrix counts a cell. Reason is a
	// refusal code the row carries.
	Area   string
	Reason string
}

// BoardResponse is one page of the strategy board.
type BoardResponse struct {
	GeneratedAt    time.Time   `json:"generated_at"`
	MaxAgeSeconds  int         `json:"max_age_seconds"`
	UniverseDigest string      `json:"universe_digest"`
	Universe       int         `json:"universe"`
	Facets         BoardFacets `json:"facets"`
	// LateObjects and LateStrategies are the page's one summary line, in the
	// overview's units: detection objects late or overdue, and the strategies
	// with at least one of them.
	LateObjects    BoardCount `json:"late_objects"`
	LateStrategies BoardCount `json:"late_strategies"`
	// AtLeastReason says why a count is a lower bound, in the page's words.
	AtLeastReason string     `json:"at_least_reason,omitempty"`
	Matched       int        `json:"matched"`
	Rows          []BoardRow `json:"rows"`
	NextCursor    string     `json:"next_cursor,omitempty"`
	// Reread says the cursor belonged to an earlier computation: this page is
	// the first of the current one, and the page starts over rather than
	// joining two computations' rows.
	Reread bool `json:"reread,omitempty"`
}

// boardIndex is one computation of the board, kept with the summary it was
// computed beside, so both read one instant.
type boardIndex struct {
	id          string
	at          time.Time
	digest      string
	rows        []BoardRow
	facets      BoardFacets
	lateObjects BoardCount
	lateStrats  BoardCount
	atLeast     string
	// views is each strategy's drawer facts beyond its row.
	views map[string]boardViewFacts
	// outages is each strategy's windows lost to a store outage, and
	// noOutage the answer for one that lost none.
	outages  map[string]StoreOutageSummary
	noOutage StoreOutageSummary
}

// boardAtLeastReason is the page's sentence for a lower bound.
const boardAtLeastReason = "部分实例的名单超过了快照的大小上限，或还没升级到会列出名单的版本，这里只数到了列出的部分"

// capabilityOf is the strategy's most severe capability outcome across its
// dispositions, supported when it has none.
func capabilityOf(row DiagnosisRow) string {
	outcome := CapabilitySupported
	for _, disposition := range row.Dispositions {
		class, _ := ClassifyCapability(disposition.Disposition, disposition.Reason, disposition.Scope)
		if class.Outcome != "" {
			outcome = MoreSevereCapability(outcome, class.Outcome)
		}
	}
	return outcome
}

// capabilityFactsOf is the strategy's outcome per capability area and its
// refusal codes by CountCapability's rules: a record with no area -- ignored
// by design, or about the source's state -- is no capability fact; the first record in an area is its
// outcome there, a later one counts only when more severe; the code is the
// reason, the disposition when there is none.
func capabilityFactsOf(row DiagnosisRow) (map[string]string, map[string]bool) {
	areas, reasons := map[string]string{}, map[string]bool{}
	for _, disposition := range row.Dispositions {
		class, _ := ClassifyCapability(disposition.Disposition, disposition.Reason, disposition.Scope)
		if class.Area == "" {
			continue
		}
		if held, seen := areas[class.Area]; seen {
			areas[class.Area] = MoreSevereCapability(held, class.Outcome)
		} else {
			areas[class.Area] = class.Outcome
		}
		code := disposition.Reason
		if code == "" {
			code = disposition.Disposition
		}
		reasons[code] = true
	}
	return areas, reasons
}

// boardLine is the row's one sentence: the state and who acts, and the wait's
// reason when it is a wait.
func boardLine(row BoardRow) string {
	parts := []string{row.StateWord}
	if row.ActionWord != "" {
		parts = append(parts, row.ActionWord)
	}
	line := strings.Join(parts, " · ")
	if row.WatchWord != "" {
		line += "（" + row.WatchWord + "）"
	}
	return line
}

// buildBoard decides every strategy of the universe once, in the action
// order the words give and then longest standing first, and counts every
// facet over the whole universe.
func buildBoard(id string, at time.Time, digest string, universe []string, decide func(string) DiagnosisRow,
	lines []StrategyLine, due *DueView, census *ScheduleCensus, open *OpenView, waits *lateWaitIndex) *boardIndex {
	words := ProductWords()
	byStrategy := make(map[string]StrategyLine, len(lines))
	for _, line := range lines {
		byStrategy[line.StrategyID] = line
	}
	index := &boardIndex{id: id, at: at, digest: digest, rows: make([]BoardRow, 0, len(universe)), views: make(map[string]boardViewFacts, len(universe)),
		facets: BoardFacets{State: map[StateWord]int{}, Action: map[ActionWord]int{}, Capability: map[string]int{},
			Punctuality: map[Punctuality]int{}, Urgency: map[Urgency]int{}}}
	for _, word := range DiagnosisVerdicts() {
		index.facets.State[word] = 0
	}
	for _, word := range ActionWords {
		index.facets.Action[word] = 0
	}
	for _, outcome := range []string{CapabilitySupported, CapabilityPartial, CapabilityUnsupportedByDesign, CapabilityNotYetSupported} {
		index.facets.Capability[outcome] = 0
	}
	for _, punctuality := range Punctualities {
		index.facets.Punctuality[punctuality] = 0
	}
	for _, urgency := range Urgencies {
		index.facets.Urgency[urgency] = 0
	}
	for _, id := range universe {
		decided := decide(id)
		row := BoardRow{StrategyID: decided.StrategyID, Tenant: decided.Tenant, BusinessID: decided.Business, State: decided.Verdict,
			StateWord: words.State[decided.Verdict], Action: decided.Action, ActionWord: words.Action[decided.Action],
			CapabilityClass: capabilityOf(decided), Punctuality: strategyPunctuality(decided, due), OpenAlerts: strategyOpenAlerts(decided, open),
			KBAnchor: "state-" + string(decided.Verdict)}
		row.LateWait = strategyLateWait(decided, due, waits)
		row.Urgency = urgencyOf(decided)
		row.UrgencyWord, row.Reason = words.Urgency[row.Urgency], strategyReasonOf(decided, at)
		if line, ok := byStrategy[decided.StrategyID]; ok {
			row.Since, row.SinceBasis = line.Since, line.SinceBasis
			row.SinceWord = words.SinceBasis[line.SinceBasis]
			if line.Standing.Watch != "" {
				row.Watch, row.WatchWord = line.Standing.Watch, words.Watch[line.Standing.Watch]
			}
		}
		row.Line = boardLine(row)
		row.areas, row.reasons = capabilityFactsOf(decided)
		index.rows = append(index.rows, row)
		index.views[row.StrategyID] = viewFactsOf(decided, due, waits)
		index.facets.State[row.State]++
		if row.Action != "" {
			index.facets.Action[row.Action]++
		}
		index.facets.Capability[row.CapabilityClass]++
		index.facets.Punctuality[row.Punctuality]++
		index.facets.Urgency[row.Urgency]++
	}
	actionRank, urgencyRank := map[ActionWord]int{}, map[Urgency]int{}
	for rank, word := range ActionWords {
		actionRank[word] = rank
	}
	for rank, urgency := range Urgencies {
		urgencyRank[urgency] = rank
	}
	// What needs a hand first, then by owner and longest standing: an
	// optional edit never sorts ahead of a strategy whose results are wrong.
	sort.SliceStable(index.rows, func(i, j int) bool {
		a, b := index.rows[i], index.rows[j]
		if urgencyRank[a.Urgency] != urgencyRank[b.Urgency] {
			return urgencyRank[a.Urgency] < urgencyRank[b.Urgency]
		}
		if actionRank[a.Action] != actionRank[b.Action] {
			return actionRank[a.Action] < actionRank[b.Action]
		}
		if (a.Since == nil) != (b.Since == nil) {
			return a.Since != nil
		}
		if a.Since != nil && !a.Since.Equal(*b.Since) {
			return a.Since.Before(*b.Since)
		}
		return a.StrategyID < b.StrategyID
	})
	cut := due == nil || due.Cut
	index.lateStrats = BoardCount{Value: index.facets.Punctuality[PunctualityLate] + index.facets.Punctuality[PunctualityOverdue], AtLeast: cut}
	if census != nil {
		index.lateObjects = BoardCount{Value: census.Late + census.Overdue, AtLeast: cut}
	} else {
		index.lateObjects = BoardCount{AtLeast: true}
	}
	if cut {
		index.atLeast = boardAtLeastReason
	}
	return index
}

// matches says whether a row is in the page's selection.
func (filter BoardFilter) matches(row BoardRow) bool {
	if filter.StrategyID != "" && row.StrategyID != filter.StrategyID {
		return false
	}
	if len(filter.State) > 0 && !filter.State[row.State] {
		return false
	}
	if len(filter.Action) > 0 && !filter.Action[row.Action] {
		return false
	}
	if filter.Area != "" {
		outcome, recorded := row.areas[filter.Area]
		if !recorded {
			outcome = CapabilitySupported
		}
		if !filter.Capability[outcome] {
			return false
		}
	} else if len(filter.Capability) > 0 && !filter.Capability[row.CapabilityClass] {
		return false
	}
	if filter.Reason != "" && !row.reasons[filter.Reason] {
		return false
	}
	if len(filter.Punctuality) > 0 && !filter.Punctuality[row.Punctuality] {
		return false
	}
	if len(filter.Urgency) > 0 && !filter.Urgency[row.Urgency] {
		return false
	}
	return true
}

// key is the selection in a canonical form: each facet's words sorted, and the
// strategy id. Two requests select the same rows exactly when their keys match.
func (filter BoardFilter) key() string {
	words := func(set map[string]bool) string {
		list := make([]string, 0, len(set))
		for word := range set {
			list = append(list, word)
		}
		sort.Strings(list)
		return strings.Join(list, ",")
	}
	states, actions, punctualities, urgencies := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for word := range filter.State {
		states[string(word)] = true
	}
	for word := range filter.Action {
		actions[string(word)] = true
	}
	for word := range filter.Punctuality {
		punctualities[string(word)] = true
	}
	for word := range filter.Urgency {
		urgencies[string(word)] = true
	}
	return strings.Join([]string{words(states), words(actions), words(filter.Capability), words(punctualities), filter.StrategyID, filter.Area, filter.Reason, words(urgencies)}, ";")
}

// boardCursor binds a position to one computation and one selection: the
// computation's id, the universe digest, the selection's key and the offset.
func boardCursor(id, digest, selection string, offset int) string {
	return encodeCursorParts("board", id, digest, selection, strconv.Itoa(offset))
}

func parseBoardCursor(raw string) (id, digest, selection string, offset int, ok bool) {
	parts, decoded := decodeCursorParts(raw)
	if !decoded || len(parts) != 5 || parts[0] != "board" {
		return "", "", "", 0, false
	}
	offset, err := strconv.Atoi(parts[4])
	if err != nil || offset < 0 {
		return "", "", "", 0, false
	}
	return parts[1], parts[2], parts[3], offset, true
}

// page answers one page of the selection from this computation. A cursor from
// another computation or another selection is not continued: the answer is
// this selection's first page in this computation, marked Reread.
func (index *boardIndex) page(filter BoardFilter, cursor string, limit int, maxAge time.Duration) BoardResponse {
	response := BoardResponse{GeneratedAt: index.at, MaxAgeSeconds: int(maxAge / time.Second), UniverseDigest: index.digest,
		Universe: len(index.rows), Facets: index.facets, LateObjects: index.lateObjects, LateStrategies: index.lateStrats,
		AtLeastReason: index.atLeast, Rows: []BoardRow{}}
	offset, selection := 0, filter.key()
	if cursor != "" {
		id, digest, selected, at, ok := parseBoardCursor(cursor)
		if ok && id == index.id && digest == index.digest && selected == selection {
			offset = at
		} else {
			response.Reread = true
		}
	}
	selected := make([]BoardRow, 0, len(index.rows))
	for _, row := range index.rows {
		if filter.matches(row) {
			selected = append(selected, row)
		}
	}
	response.Matched = len(selected)
	if offset > len(selected) {
		offset = len(selected)
	}
	end := offset + limit
	if end > len(selected) {
		end = len(selected)
	}
	response.Rows = append(response.Rows, selected[offset:end]...)
	if end < len(selected) {
		response.NextCursor = boardCursor(index.id, index.digest, selection, end)
	}
	return response
}
