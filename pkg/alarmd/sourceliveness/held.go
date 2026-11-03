// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sourceliveness

import (
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// HeldBoundSpans and HeldBoundFloor are the bound past which a Level held on
// a source that cannot be shown alive is named on the fleet (HeldBound).
//
// The bound is four of the Level's own no-data spans, (R + T + 1) periods
// each, and an hour at least. The Level has already waited one span with no
// data before its first probe; a probe is asked every period after that and
// answers within its own 20 s, so a passing cause - a query service rolling,
// one route partial for a round, an index rotating - clears in a period or
// two. Four spans more is a source unreadable for five times as long as the
// strategy itself takes to call its data missing: not a blip, and an alert
// that would otherwise stay open with nobody told. The floor is the fleet's
// own clock for data that stopped (fleet.DefaultNoDataAfter, an hour): a
// one-minute strategy with R = T = 1 has a three-minute span, and twelve
// minutes would name a routine restart of the query service. So: 1 min, R =
// T = 1 -> 1 h; 5 min, R = T = 1 (15 min span) -> 1 h; 1 h, R = T = 1 ->
// 12 h.
const (
	HeldBoundSpans = 4
	HeldBoundFloor = time.Hour
)

// HeldBound is how long a Level whose no-data span is spanSeconds may stay
// held on an unknown source before the fleet names it.
func HeldBound(spanSeconds int64) time.Duration {
	return max(HeldBoundSpans*time.Duration(spanSeconds)*time.Second, HeldBoundFloor)
}

// MaxHeldLevels bounds the held Levels one process remembers, a few hundred
// bytes each. Past it the Level asked about least recently - the likeliest
// to have ended another way - makes room for the new one, counted: a new
// held Level dropped instead would be held with no line ever naming it.
const MaxHeldLevels = 4096

// HeldReport is one Plan's Levels held on a source that cannot be shown
// alive, as this process holds them: how many, how many past their bound,
// and the oldest - its source, table, when it was first held, the last
// verdict and its bound.
type HeldReport struct {
	Levels, PastBound int
	Kind              Kind
	TableID           string
	FirstHeld         time.Time
	Last              Verdict
	Bound             time.Duration
}

type heldLevel struct {
	kind         Kind
	table        string
	first, asked time.Time
	verdict      Verdict
	span         int64
}

type heldKey struct {
	plan   execution.PlanIdentity
	level  uint32
	series execution.SeriesIdentityDigest
}

// heldLevels is the Levels held now, in this process only: a Level is held
// from its first unknown verdict until a verdict ends it, or until it is not
// asked about for two of its no-data spans, ten minutes at least
// (heldStaleAfter) - its alert ended another way, or its Plan left this
// process. A round that cannot judge (an incomplete answer) asks nothing,
// so one such round does not restart a held Level's clock.
type heldLevels struct {
	mu       sync.Mutex
	levels   map[heldKey]*heldLevel
	reported map[execution.PlanIdentity]bool
	// max is MaxHeldLevels; a test sets it lower. evicted hears each Level
	// let go to make room.
	max     int
	evicted func()
}

func newHeldLevels() heldLevels {
	return heldLevels{levels: map[heldKey]*heldLevel{}, reported: map[execution.PlanIdentity]bool{}, max: MaxHeldLevels}
}

// note keeps what a verdict says of subject: held while unknown, let go on
// any other verdict.
func (held *heldLevels) note(subject Subject, probe Probe, facts execution.QueryPlanFacts, verdict Verdict, now time.Time) {
	key := heldKey{plan: subject.Plan, level: subject.LevelID, series: subject.Series}
	held.mu.Lock()
	defer held.mu.Unlock()
	if verdict.Result != ResultUnknown {
		delete(held.levels, key)
		return
	}
	level := held.levels[key]
	if level == nil {
		if len(held.levels) >= held.max {
			held.evictLeastAskedLocked()
		}
		level = &heldLevel{first: now}
		held.levels[key] = level
	}
	level.kind, level.asked, level.verdict, level.span = probe.Kind, now, verdict, subject.NoDataSpanSeconds
	level.table = probe.Key.TableID
	if level.table == "" && len(facts.QueryList) > 0 {
		level.table = facts.QueryList[0].TableID
	}
}

// evictLeastAskedLocked lets go of the Level asked about least recently,
// counted.
func (held *heldLevels) evictLeastAskedLocked() {
	var oldest heldKey
	var asked time.Time
	found := false
	for key, level := range held.levels {
		if !found || level.asked.Before(asked) {
			oldest, asked, found = key, level.asked, true
		}
	}
	if !found {
		return
	}
	delete(held.levels, oldest)
	if held.evicted != nil {
		held.evicted()
	}
}

func heldStaleAfter(spanSeconds int64) time.Duration {
	return max(2*time.Duration(spanSeconds)*time.Second, 10*time.Minute)
}

// report is plan's held Levels at now, and whether there is anything to say:
// Levels held, or none held after some were last said - the line that named
// them is cleared by saying so.
func (held *heldLevels) report(plan execution.PlanIdentity, now time.Time) (HeldReport, bool) {
	held.mu.Lock()
	defer held.mu.Unlock()
	var report HeldReport
	for key, level := range held.levels {
		if key.plan != plan {
			continue
		}
		if now.Sub(level.asked) > heldStaleAfter(level.span) {
			delete(held.levels, key)
			continue
		}
		report.Levels++
		bound := HeldBound(level.span)
		if now.Sub(level.first) >= bound {
			report.PastBound++
		}
		if report.FirstHeld.IsZero() || level.first.Before(report.FirstHeld) {
			report.Kind, report.TableID, report.FirstHeld, report.Last, report.Bound =
				level.kind, level.table, level.first, level.verdict, bound
		}
	}
	said := held.reported[plan]
	if report.Levels > 0 {
		held.reported[plan] = true
	} else {
		delete(held.reported, plan)
	}
	return report, report.Levels > 0 || said
}

// keep marks plan's held Levels asked about at now: a round that could not
// ask is no sign they ended.
func (held *heldLevels) keep(plan execution.PlanIdentity, now time.Time) {
	held.mu.Lock()
	defer held.mu.Unlock()
	for key, level := range held.levels {
		if key.plan == plan {
			level.asked = now
		}
	}
}

// Keep keeps plan's held Levels held through a round that could not ask
// about them: an index unreadable, an answer incomplete, an output
// withheld. Their first held time stays.
func (checker *Checker) Keep(plan execution.PlanIdentity) {
	checker.held.keep(plan, checker.now())
}

// Held is plan's held Levels at the Checker's now, and whether there is
// anything to say (heldLevels.report).
func (checker *Checker) Held(plan execution.PlanIdentity) (HeldReport, bool) {
	return checker.held.report(plan, checker.now())
}
