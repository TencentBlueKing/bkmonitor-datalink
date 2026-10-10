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
	"sort"
	"strings"
	"time"
)

// The page's first list is strategies, one line each, and a strategy is on
// it once: the objects that run it fold into one standing, the most severe
// of theirs by the check table's own order. This is the fold the redesign
// rests on -- the object-by-check listing put one strategy under every
// check any of its objects failed, and a reader who came for the strategy
// found it in four places saying four things.

// StrategyLine is one strategy's standing over every object that runs it.
type StrategyLine struct {
	StrategyID string `json:"strategy_id"`
	BusinessID string `json:"business_id,omitempty"`
	// Standing is the deciding object's, the most severe by checkRank.
	Standing Standing `json:"standing"`
	// Objects is how many distinct objects of the strategy are on a row.
	Objects int `json:"objects"`
	// Since is the earliest onset among them and SinceFrom its basis;
	// LastGoodAt the latest healthy completion any of them had.
	Since      *time.Time  `json:"since,omitempty"`
	SinceFrom  SinceSource `json:"since_from,omitempty"`
	SinceBasis SinceBasis  `json:"since_basis,omitempty"`
	LastGoodAt *time.Time  `json:"last_good_at,omitempty"`
	// DecidingObject is the object whose standing this is: the coordinate
	// a reader opens.
	DecidingObject string `json:"deciding_object"`
	// Line is the sentence, composed here from four slots -- the strategy,
	// the reach, the state word, the action word -- and one evidence clause
	// the check's rule supplies. Nothing else is appended.
	Line string `json:"line"`
}

// StrategyListResponse is GET /api/strategies.
type StrategyListResponse struct {
	// Words travels on every response, beside the lines it renders, and
	// not from an endpoint of its own: the vocabulary and the data it
	// words must arrive together. Served separately, a page could hold
	// last release's table against this release's lines, and a word the
	// release added would render blank or as its code -- the very thing
	// keeping the words out of the page exists to prevent. Its size is
	// not the argument either way.
	Words      Words          `json:"words"`
	Strategies []StrategyLine `json:"strategies"`
	// Summary is over every line before the filter: how many strategies
	// each word holds, and the one line to act on first.
	Summary StrategySummary `json:"summary"`
	// Total is how many strategies have a standing to list after the
	// filter, Listed how many this page holds, Truncated whether Total
	// exceeded the limit.
	Total     int  `json:"total"`
	Listed    int  `json:"listed"`
	Truncated bool `json:"truncated"`
	// State and Action echo the filter the list was asked with.
	State  StateWord  `json:"state,omitempty"`
	Action ActionWord `json:"action,omitempty"`
}

// StrategySummary is the first screen's arithmetic over the lines: how
// many strategies stand under each word, and which line to act on first --
// the most severe line whose action asks somebody to act. Decided here so
// the page's first sentence is the server's.
type StrategySummary struct {
	Strategies int                `json:"strategies"`
	ByAction   map[ActionWord]int `json:"by_action"`
	ByState    map[StateWord]int  `json:"by_state"`
	Lead       *StrategyLine      `json:"lead,omitempty"`
}

// SummarizeStrategyLines counts the lines by word and picks the lead: the
// first line, in severity order, whose action is a hand -- not a wait, not
// nothing to do.
func SummarizeStrategyLines(lines []StrategyLine) StrategySummary {
	summary := StrategySummary{Strategies: len(lines), ByAction: map[ActionWord]int{}, ByState: map[StateWord]int{}}
	for _, word := range ActionWords {
		summary.ByAction[word] = 0
	}
	for _, word := range StateWords {
		summary.ByState[word] = 0
	}
	for i := range lines {
		line := lines[i]
		summary.ByAction[line.Standing.Action]++
		summary.ByState[line.Standing.State]++
		if summary.Lead == nil && line.Standing.Action != ActionWatch && line.Standing.Action != ActionNone {
			lead := line
			summary.Lead = &lead
		}
	}
	return summary
}

// MaxStrategyLines bounds one page of the list.
const MaxStrategyLines = 2000

type strategyFold struct {
	line     StrategyLine
	deciding Anomaly
	rank     int
	objects  map[string]struct{}
}

// StrategyLines folds every listed row into one line per strategy, most
// severe first, then by strategy id.
func StrategyLines(view *View, now time.Time) []StrategyLine {
	folds := map[StrategyRef]*strategyFold{}
	walkObjectRows("", "", "", view, now, func(row Anomaly) {
		if row.Standing == nil {
			return
		}
		rank := foldRank(row.Finding.Check, row.Loss)
		for _, ref := range strategiesOf(row) {
			standing, given := standingForStrategy(row, ref)
			if !given {
				continue
			}
			fold := folds[ref]
			if fold == nil {
				fold = &strategyFold{line: StrategyLine{StrategyID: ref.StrategyID, BusinessID: ref.BusinessID}, rank: unranked, objects: map[string]struct{}{}}
				folds[ref] = fold
			}
			fold.objects[row.QueryGroup] = struct{}{}
			if !row.Since.IsZero() && (fold.line.Since == nil || row.Since.Before(*fold.line.Since)) {
				since := row.Since
				fold.line.Since, fold.line.SinceFrom, fold.line.SinceBasis = &since, row.SinceFrom, sinceBasisOf(row.SinceFrom)
			}
			if !row.LastHealthyAt.IsZero() && (fold.line.LastGoodAt == nil || row.LastHealthyAt.After(*fold.line.LastGoodAt)) {
				last := row.LastHealthyAt
				fold.line.LastGoodAt = &last
			}
			// The deciding row: the most severe check; among equals the
			// earliest onset, so the sentence does not change with the
			// order rows were walked in.
			if rank < fold.rank || (rank == fold.rank && !row.Since.IsZero() && (fold.deciding.Since.IsZero() || row.Since.Before(fold.deciding.Since))) {
				fold.rank, fold.deciding = rank, row
				fold.line.Standing, fold.line.DecidingObject = standing, row.QueryGroup
			}
		}
	})
	lines := make([]StrategyLine, 0, len(folds))
	for _, fold := range folds {
		fold.line.Objects = len(fold.objects)
		fold.line.Line = strategyLineOf(fold.line, fold.deciding)
		lines = append(lines, fold.line)
	}
	sort.Slice(lines, func(i, j int) bool {
		left, right := lineRank(lines[i]), lineRank(lines[j])
		if left != right {
			return left < right
		}
		if lines[i].StrategyID != lines[j].StrategyID {
			return lines[i].StrategyID < lines[j].StrategyID
		}
		return lines[i].BusinessID < lines[j].BusinessID
	})
	return lines
}

// unranked is a fold no row has decided yet: below every current and every
// historical rank.
var unranked = 2*(len(checkOrder)+1) + 1

// foldRank orders the rows competing to decide a strategy's line: by the
// check's severity, and every current row before every historical one. A
// historical row is what is left of a loss the object has run past; the
// object beside it is under a current check now. On a verification cluster
// a strategy losing history under an undecided window read "recovered,
// nothing to do" because its recovered restart loss ranked higher -- a past
// tense outranking the present.
func foldRank(check Check, loss Loss) int {
	rank := checkRank(check)
	if loss == LossHistorical {
		rank += len(checkOrder) + 1
	}
	return rank
}

// lineRank is foldRank read back from the line, for ordering the list.
func lineRank(line StrategyLine) int {
	loss := Loss("")
	if line.Standing.RefinedBy == RuleHistoricalLoss {
		loss = LossHistorical
	}
	return foldRank(line.Standing.Check, loss)
}

// strategyLineOf composes the sentence from its slots.
func strategyLineOf(line StrategyLine, deciding Anomaly) string {
	words := ProductWords()
	parts := []string{"策略 " + line.StrategyID, fmt.Sprintf("%d 个对象", line.Objects),
		words.State[line.Standing.State], words.Action[line.Standing.Action]}
	if line.Standing.Watch != "" {
		parts = append(parts, words.Watch[line.Standing.Watch])
	}
	if clause := evidenceClause(deciding); clause != "" {
		parts = append(parts, clause)
	}
	return strings.Join(parts, " · ")
}

// evidenceClause is the one number the check's rule supplies for the
// sentence: for the undecided windows, the worst window's pair and where
// its holes fall. Other checks supply none in this batch; the slot stays
// empty rather than filled from prose.
func evidenceClause(row Anomaly) string {
	coverage := row.Coverage
	if coverage == nil || coverage.Short == 0 {
		return ""
	}
	clause := fmt.Sprintf("最差窗口 %d/%d", coverage.WorstValid, coverage.WorstRequired)
	if len(coverage.Windows) == 0 {
		return clause
	}
	var data, incomplete, unusable, unknown uint32
	for _, window := range coverage.Windows {
		data += window.HolesBy.AnsweredWithoutSeries + window.HolesBy.AnsweredEmpty
		incomplete += window.HolesBy.InputIncomplete
		unusable += window.HolesBy.Unusable
		unknown += window.HolesBy.NotInMemory + window.HolesBy.PrimaryUnrecorded
	}
	switch {
	case incomplete > 0:
		return clause + fmt.Sprintf("，缺的分钟里 %d 分钟本侧没查全", incomplete)
	case unusable > 0:
		return clause + fmt.Sprintf("，%d 分钟的记录检测用不了", unusable)
	case unknown > 0:
		return clause + fmt.Sprintf("，缺的分钟里 %d 分钟说不出是谁的", unknown)
	default:
		return clause + fmt.Sprintf("，缺的 %d 分钟查询都正常返回、序列不在结果里", data)
	}
}

// FilterStrategyLines keeps the lines matching the words given; an empty
// word matches every line.
func FilterStrategyLines(lines []StrategyLine, state StateWord, action ActionWord) []StrategyLine {
	kept := make([]StrategyLine, 0, len(lines))
	for _, line := range lines {
		if (state != "" && line.Standing.State != state) || (action != "" && line.Standing.Action != action) {
			continue
		}
		kept = append(kept, line)
	}
	return kept
}
