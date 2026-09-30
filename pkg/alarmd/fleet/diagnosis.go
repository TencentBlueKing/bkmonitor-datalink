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
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The environment diagnosis: one row for every strategy the source lists,
// each with one word from the closed vocabulary the page already uses. The
// population is the source's active set read at the time of the diagnosis,
// not the catalog that accepted some of it: a strategy the catalog never
// took is exactly the one a diagnosis must not skip.
//
// Nothing here decides a word of its own. The row reads the strategy's
// standing (StrategyStandingOf), its line (StrategyLines) and, for a
// withheld strategy, the pair its disposition's check already has
// (sourceChecks, checkWords). What a diagnosis adds is only UNKNOWN, for
// the rows none of those can answer, and it always says why.

// DiagnosisUnknown is the one word a diagnosis adds to StateWords: no
// standing could be read for the row. Never written without a reason.
const DiagnosisUnknown StateWord = "UNKNOWN"

// The reasons a row or a part of it is unknown, closed.
const (
	UnknownLookupUnavailable = "LOOKUP_UNAVAILABLE"
	UnknownNotYetPublished   = "NOT_YET_PUBLISHED"
	UnknownObjectNotObserved = "OBJECT_NOT_OBSERVED"
	UnknownReplicaUnreadable = "REPLICA_UNREADABLE"
	UnknownSnapshotStale     = "SNAPSHOT_STALE"
)

// DiagnosisUnknownReasons is the closed list.
var DiagnosisUnknownReasons = []string{UnknownLookupUnavailable, UnknownNotYetPublished,
	UnknownObjectNotObserved, UnknownReplicaUnreadable, UnknownSnapshotStale}

// DiagnosisVerdicts is every word a row can carry, in the page's order.
func DiagnosisVerdicts() []StateWord {
	return append(append([]StateWord(nil), StateWords...), DiagnosisUnknown)
}

// DiagnosisExtent is how much of the deciding object the deciding row
// covers, from its own coverage: windows, not series (see HistoryCoverage).
type DiagnosisExtent struct {
	Levels      uint32 `json:"levels"`
	Short       uint32 `json:"short"`
	Guarded     uint32 `json:"guarded"`
	Resumed     uint32 `json:"resumed"`
	Constrained uint32 `json:"constrained"`
}

// DiagnosisDisposition is one withheld item of the strategy, and whose it
// is to fix, in plain words read from the action its check already pairs.
type DiagnosisDisposition struct {
	StrategyDisposition
	Attribution string `json:"attribution,omitempty"`
}

// DiagnosisPlan is one Plan of the strategy as the fleet sees its object.
type DiagnosisPlan struct {
	StrategyPlanRef
	Replica   string `json:"replica,omitempty"`
	Existence string `json:"existence"`
	// LastFullSlot and NextSlot are the object's persisted progress: the
	// last Slot completed in full and the front the cursor stands at, both
	// evaluation times in seconds. Absent when not read; the part saying
	// why is in the row's UnknownParts.
	LastFullSlot *int64 `json:"last_full_slot,omitempty"`
	NextSlot     *int64 `json:"next_slot,omitempty"`
}

// DiagnosisPart is a part of a row that could not be read, and why.
type DiagnosisPart struct {
	What   string `json:"what"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// DiagnosisRow is one strategy.
type DiagnosisRow struct {
	StrategyID string     `json:"strategy_id"`
	Business   string     `json:"business,omitempty"`
	Verdict    StateWord  `json:"verdict"`
	Action     ActionWord `json:"action,omitempty"`
	// Reason is the check the words were decided under, the unknown
	// reason for UNKNOWN, or the deciding row's own reason where it names
	// one more precisely (cause_reason).
	Reason string `json:"reason,omitempty"`
	Check  Check  `json:"check,omitempty"`
	// Catalog is the strategy's standing in the publication answered from.
	Catalog        StrategyStandingKind `json:"catalog,omitempty"`
	DecidingObject string               `json:"deciding_object,omitempty"`
	LastGoodAt     *time.Time           `json:"last_good_at,omitempty"`
	Extent         *DiagnosisExtent     `json:"extent,omitempty"`
	// WindowClears is, for a result not yet trusted because its windows are
	// filling, when the last listed hole slides out of its window and that
	// window is full -- from the deciding row's named windows and the
	// object's evaluation interval -- or why that cannot be computed. It is
	// not when the result can be taken; see WindowClearing.
	WindowClears *WindowClearing        `json:"window_clears,omitempty"`
	Dispositions []DiagnosisDisposition `json:"dispositions,omitempty"`
	Plans        []DiagnosisPlan        `json:"plans"`
	UnknownParts []DiagnosisPart        `json:"unknown_parts,omitempty"`
}

// ProgressFacts is one object's persisted progress, as the runtime reads it.
type ProgressFacts struct {
	LastFullSlot int64
	NextSlot     int64
}

// ProgressReader reads the persisted progress of the given objects in
// bounded reads. An object with no record is absent from both maps; an object
// whose own read failed is in failed; a read that failed as a whole is the
// error, and every object's progress is then unknown.
type ProgressReader func(ctx context.Context, queryGroups []string) (found map[string]ProgressFacts, failed map[string]bool, err error)

// The reasons a Plan's progress is unknown, closed.
const (
	ProgressUnreadable = "PROGRESS_UNREADABLE"
	ProgressReadFailed = "PROGRESS_READ_FAILED"
	ProgressNotFound   = "PROGRESS_NOT_FOUND"
)

// diagnosisContext is what every row of one page is decided against.
type diagnosisContext struct {
	view    *View
	lines   map[string][]StrategyLine
	stale   map[string]bool
	unread  map[string]bool
	replica string
	now     time.Time
}

func newDiagnosisContext(view *View, replica string, now time.Time) diagnosisContext {
	ctx := diagnosisContext{view: view, stale: map[string]bool{}, unread: map[string]bool{},
		replica: replica, now: now}
	if view == nil {
		return ctx
	}
	for _, gap := range view.Gaps {
		switch gap.Kind {
		case GapSnapshotStale:
			ctx.stale[gap.Replica] = true
		case GapSnapshotsUnreadable:
			ctx.unread[""] = true
		default:
			if gap.Replica != "" {
				ctx.unread[gap.Replica] = true
			}
		}
	}
	return ctx
}

// diagnoseStrategy decides one row.
func diagnoseStrategy(id string, facts StrategyLookupFacts, ctx diagnosisContext) DiagnosisRow {
	row := DiagnosisRow{StrategyID: id, Plans: []DiagnosisPlan{}}
	if !facts.Available {
		row.Verdict, row.Reason = DiagnosisUnknown, UnknownLookupUnavailable
		return row
	}
	standing := StrategyStandingOf(id, "", "", ctx.replica, facts, ctx.view, ctx.now)
	row.Catalog = standing.Standing
	for _, disposition := range standing.Dispositions {
		if !isWithheld(disposition.Disposition) {
			continue
		}
		row.Dispositions = append(row.Dispositions, DiagnosisDisposition{StrategyDisposition: disposition,
			Attribution: dispositionAttribution(withheldWords(disposition.Disposition).Action)})
	}
	observed := 0
	for _, plan := range standing.Plans {
		entry := DiagnosisPlan{StrategyPlanRef: plan.StrategyPlanRef, Replica: plan.Replica, Existence: plan.Existence}
		if row.Business == "" {
			row.Business = plan.Business
		}
		switch {
		case ctx.unread[""]:
			row.UnknownParts = append(row.UnknownParts, DiagnosisPart{What: plan.QueryGroup, Reason: UnknownReplicaUnreadable, Detail: "fleet snapshots unreadable"})
		case plan.Replica != "" && ctx.unread[plan.Replica]:
			row.UnknownParts = append(row.UnknownParts, DiagnosisPart{What: plan.QueryGroup, Reason: UnknownReplicaUnreadable, Detail: plan.Replica})
		case plan.Replica != "" && ctx.stale[plan.Replica]:
			row.UnknownParts = append(row.UnknownParts, DiagnosisPart{What: plan.QueryGroup, Reason: UnknownSnapshotStale, Detail: plan.Replica})
		case plan.Replica == "" || plan.Existence != "active":
			row.UnknownParts = append(row.UnknownParts, DiagnosisPart{What: plan.QueryGroup, Reason: UnknownObjectNotObserved, Detail: "existence " + plan.Existence})
		default:
			observed++
		}
		row.Plans = append(row.Plans, entry)
	}
	switch {
	case standing.Standing == StandingNotListed:
		// In the source's active set, not in the publication answered from:
		// the source is newer than the catalog, or the catalog took it out.
		row.Verdict, row.Reason = DiagnosisUnknown, UnknownNotYetPublished
		return row
	case standing.Standing == StandingWithheld:
		// No Plan: the words are the first withheld item's check's pair.
		if len(row.Dispositions) > 0 {
			words := withheldWords(row.Dispositions[0].Disposition)
			row.Verdict, row.Action = words.State, words.Action
			row.Check, row.Reason = sourceChecks[row.Dispositions[0].Disposition], row.Dispositions[0].Reason
			return row
		}
		row.Verdict, row.Reason = DiagnosisUnknown, UnknownLookupUnavailable
		return row
	}
	// The deciding row is read from the strategy's own Plans, the rows the
	// standing already carries, most severe first as a strategy's line
	// ranks them; its words are the ones scoped to this strategy when the
	// row speaks for several.
	best, bestRank, found := Anomaly{}, unranked, false
	var bestWords Standing
	for _, plan := range standing.Plans {
		for _, anomaly := range plan.Rows {
			words, given := standingForStrategy(anomaly, StrategyRef{StrategyID: id, BusinessID: plan.Business})
			if !given {
				if anomaly.Standing == nil {
					continue
				}
				words = *anomaly.Standing
			}
			if rank := foldRank(anomaly.Finding.Check, anomaly.Loss); !found || rank < bestRank {
				best, bestRank, bestWords, found = anomaly, rank, words, true
			}
		}
	}
	if found {
		row.Verdict, row.Action, row.Check = bestWords.State, bestWords.Action, bestWords.Check
		row.Reason, row.DecidingObject = string(bestWords.Check), best.QueryGroup
		if best.CauseReason != "" {
			row.Reason = best.CauseReason
		}
		if !best.LastHealthyAt.IsZero() {
			last := best.LastHealthyAt
			row.LastGoodAt = &last
		}
		if coverage := best.Coverage; coverage != nil {
			row.Extent = &DiagnosisExtent{Levels: coverage.Levels, Short: coverage.Short, Guarded: coverage.Guarded,
				Resumed: coverage.Resumed, Constrained: coverage.Constrained}
		}
		row.WindowClears = WindowClearsOf(best, bestWords)
		return row
	}
	if observed == 0 {
		// No row, and no object anyone could see: nothing says it detects.
		row.Verdict, row.Reason = DiagnosisUnknown, UnknownObjectNotObserved
		if len(row.UnknownParts) > 0 {
			row.Reason = row.UnknownParts[0].Reason
		}
		return row
	}
	row.Verdict, row.Action = StateDetecting, ActionNone
	return row
}

// WindowClearsOf is the deciding row's clearing when its words say the
// result is waiting on windows to fill, and nil otherwise. A refusal is
// returned too, so the row says why there is no time; the Plan-level gap
// guards the object carries are counted beside it, since they release on
// their own count.
func WindowClearsOf(anomaly Anomaly, words Standing) *WindowClearing {
	if words.State != StateResultUntrusted && words.Watch != WatchWindowFilling {
		return nil
	}
	var interval time.Duration
	if anomaly.Wake != nil && anomaly.Wake.IntervalSeconds > 0 {
		interval = time.Duration(anomaly.Wake.IntervalSeconds) * time.Second
	}
	clearing, ok := LastClearing(anomaly.Coverage, interval)
	if !ok {
		return nil
	}
	clearing.PlanGuards = len(anomaly.Guards)
	clearing.Line = clearing.text()
	return &clearing
}

// applyProgress fills each Plan's persisted progress after the page's
// rows are chosen, from one read over the page's objects. A failed read
// leaves every Plan's progress unknown with the read's reason; an object
// the read did not find is unknown on its own. Progress never changes a
// verdict: it says when, not whether.
func applyProgress(rows []DiagnosisRow, progress map[string]ProgressFacts, failed map[string]bool, unknown string) {
	for i := range rows {
		for j := range rows[i].Plans {
			plan := &rows[i].Plans[j]
			if unknown != "" {
				rows[i].UnknownParts = append(rows[i].UnknownParts, DiagnosisPart{What: plan.QueryGroup + " progress", Reason: unknown})
				continue
			}
			if failed[plan.QueryGroup] {
				rows[i].UnknownParts = append(rows[i].UnknownParts, DiagnosisPart{What: plan.QueryGroup + " progress", Reason: ProgressReadFailed})
				continue
			}
			facts, found := progress[plan.QueryGroup]
			if !found {
				rows[i].UnknownParts = append(rows[i].UnknownParts, DiagnosisPart{What: plan.QueryGroup + " progress", Reason: ProgressNotFound})
				continue
			}
			last, next := facts.LastFullSlot, facts.NextSlot
			plan.LastFullSlot, plan.NextSlot = &last, &next
		}
	}
}

// pageQueryGroups is every object the page's rows run on, once each.
func pageQueryGroups(rows []DiagnosisRow) []string {
	seen := map[string]bool{}
	var out []string
	for _, row := range rows {
		for _, plan := range row.Plans {
			if plan.QueryGroup != "" && !seen[plan.QueryGroup] {
				seen[plan.QueryGroup] = true
				out = append(out, plan.QueryGroup)
			}
		}
	}
	sort.Strings(out)
	return out
}

// withheldWords is the pair the disposition's check already carries; a
// disposition with no check folds the way an unpaired check does.
func withheldWords(disposition string) wordPair {
	if check, ok := sourceChecks[disposition]; ok {
		if words, ok := checkWords[check]; ok {
			return words
		}
	}
	return unpairedWords
}

// dispositionAttribution says whose a withheld item is to fix, from the action word.
func dispositionAttribution(action ActionWord) string {
	switch action {
	case ActionCacheWriterFill:
		return "writer"
	case ActionStrategyEdit:
		return "strategy"
	case ActionServiceFix:
		return "alarmd"
	}
	return ""
}

// NormalizeUniverse returns the active set sorted by numeric id, without
// duplicates, and its digest. The ids are canonical decimal (the source
// refuses anything else), so length then text is numeric order.
func NormalizeUniverse(ids []string) ([]string, string) {
	sorted := append([]string(nil), ids...)
	sort.Slice(sorted, func(i, j int) bool { return idLess(sorted[i], sorted[j]) })
	out := sorted[:0]
	for i, id := range sorted {
		if i > 0 && id == sorted[i-1] {
			continue
		}
		out = append(out, id)
	}
	sum := sha256.Sum256([]byte(strings.Join(out, ",")))
	return out, hex.EncodeToString(sum[:8])
}

func idLess(left, right string) bool {
	if len(left) != len(right) {
		return len(left) < len(right)
	}
	return left < right
}

// DiagnosisPageBytes is the byte budget of one page's rows: half the CLI
// channel's response bound, which leaves the other half to the envelope,
// the deployment section of the first page, and the encoder's slack.
const DiagnosisPageBytes = 1 << 20

// DiagnosisPageRows bounds a page's rows whatever their size.
const DiagnosisPageRows = 2000

// DiagnosisPage is one page of rows over the universe, after the given id.
type DiagnosisPage struct {
	Rows []DiagnosisRow `json:"-"`
	// IDsExpected is how many ids of the universe fall in this page's range
	// (after, last row], counted by searching the universe for the range's
	// ends and not from the loop that wrote the rows. The page's rows equal
	// to it is the page's own check; the proof that the pages cover the
	// universe is the CLI's, from the ids against the universe's digest.
	IDsExpected      int               `json:"ids_expected"`
	RowsWritten      int               `json:"rows"`
	Holds            bool              `json:"holds"`
	ByVerdict        map[StateWord]int `json:"by_verdict"`
	TruncatedByBytes bool              `json:"truncated_by_bytes,omitempty"`
	// Last is the id the next page starts after; empty on the last page.
	Last string `json:"-"`
}

// buildDiagnosisPage writes rows for the universe's ids after `after`, up
// to limit rows and DiagnosisPageBytes of encoded rows.
func buildDiagnosisPage(universe []string, after string, limit int, row func(string) DiagnosisRow) DiagnosisPage {
	if limit <= 0 || limit > DiagnosisPageRows {
		limit = DiagnosisPageRows
	}
	start := 0
	if after != "" {
		start = sort.Search(len(universe), func(i int) bool { return idLess(after, universe[i]) })
	}
	page := DiagnosisPage{Rows: []DiagnosisRow{}, ByVerdict: map[StateWord]int{}}
	for _, word := range DiagnosisVerdicts() {
		page.ByVerdict[word] = 0
	}
	bytes := 0
	end := start
	for end < len(universe) && len(page.Rows) < limit {
		next := row(universe[end])
		encoded, _ := json.Marshal(next)
		if len(page.Rows) > 0 && bytes+len(encoded) > DiagnosisPageBytes {
			page.TruncatedByBytes = true
			break
		}
		bytes += len(encoded)
		page.Rows = append(page.Rows, next)
		page.ByVerdict[next.Verdict]++
		end++
	}
	page.RowsWritten = len(page.Rows)
	if len(page.Rows) > 0 {
		last := page.Rows[len(page.Rows)-1].StrategyID
		upper := sort.Search(len(universe), func(i int) bool { return idLess(last, universe[i]) })
		page.IDsExpected = upper - start
	}
	page.Holds = page.IDsExpected == page.RowsWritten
	if end < len(universe) {
		page.Last = universe[end-1]
	}
	return page
}

// DiagnosisCursor is what the next page is asked with.
type DiagnosisCursor struct {
	Diagnosis string
	Digest    string
	After     string
}

func encodeCursorParts(parts ...string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join(parts, "\n")))
}

func decodeCursorParts(raw string) ([]string, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, false
	}
	return strings.Split(string(decoded), "\n"), true
}

// Encode is the cursor's wire form.
func (cursor DiagnosisCursor) Encode() string {
	return encodeCursorParts("d1", cursor.Diagnosis, cursor.Digest, cursor.After)
}

// ParseDiagnosisCursor reads a cursor; false for anything else.
func ParseDiagnosisCursor(raw string) (DiagnosisCursor, bool) {
	parts, ok := decodeCursorParts(raw)
	if !ok || len(parts) != 4 || parts[0] != "d1" || parts[1] == "" || parts[2] == "" {
		return DiagnosisCursor{}, false
	}
	if _, err := strconv.ParseUint(parts[3], 10, 64); err != nil {
		return DiagnosisCursor{}, false
	}
	return DiagnosisCursor{Diagnosis: parts[1], Digest: parts[2], After: parts[3]}, true
}
