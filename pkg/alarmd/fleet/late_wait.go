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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// LateWait is what a late or overdue object's round waited on: the step and
// reason of the reading its own replica published for it (Blocked), and
// since when that reason has held.
//
// It exists because a late count alone cannot be read. A board that went
// from no late strategies to thirteen and back within four minutes could be
// explained only by ruling things out one by one -- the backend, the CPU,
// the restart -- and the mechanism stayed unknown. The replicas already say
// what each object they find wrong is stuck on; the board names it beside
// the lateness instead of leaving it on another page.
//
// Nothing here is decided anew: the stage and reason are the row's Blocked
// reading, the split is the row's coverage. An object no reading names is
// Unknown with the reason it could not be read, never a blank.
type LateWait struct {
	// Stage is absent on a reading that is not detection stopped at a step
	// (blockedOf gives none: no data, a read held).
	Stage  Stage  `json:"stage,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Since is when the object's current reason first held, else when its
	// row began.
	Since *time.Time `json:"since,omitempty"`
	// Unknown says why there is no reading: LateWaitNoReading when every
	// replica's lists were published whole and none names the object -- the
	// round is late with nothing its replica found wrong -- and
	// LateWaitReadingsCut when some list was cut, so its reading may be past
	// the cut.
	Unknown string `json:"unknown,omitempty"`
	// Warming is the window shortfall's split when the wait is the
	// evaluation waiting on history (HISTORY_WARMING): the row's coverage
	// counts, as the replica published them.
	Warming *WarmingSplit `json:"warming,omitempty"`
}

// The reasons a late object has no reading, closed.
const (
	LateWaitNoReading   = "NO_READING"
	LateWaitReadingsCut = "READINGS_CUT"
)

// WarmingSplit is HistoryCoverage's three counts that tell HISTORY_WARMING's
// situations apart, against Short, the short windows they are drawn from: a
// series new this round (ShortFresh: churning identity, or the round after a
// strategy edit), a window with no usable position (Empty: the series' data
// stopped), and a verdict held by a guard rather than computed (Guarded).
type WarmingSplit struct {
	Short      uint32 `json:"short"`
	ShortFresh uint32 `json:"short_fresh"`
	Empty      uint32 `json:"empty"`
	Guarded    uint32 `json:"guarded"`
}

// LateWaitGroup is the late and overdue objects of one strategy that waited
// on one thing, for the drawer: how many, since the earliest of them, and the
// warming split summed over them. The groups are as many as the distinct
// waits, not as many as the objects.
type LateWaitGroup struct {
	LateWait
	Objects int `json:"objects"`
}

// lateWaitIndex is each replica's published readings by object, in the
// order of the columns the check reads them in, so the first reading of an
// object is the one that decided its row's check.
type lateWaitIndex struct {
	readings map[string]map[string]Anomaly
	cut      bool
}

// lateWaitsOf indexes the view's readings. A previous owner's reading never
// decides an object: it is read under the replica its plan names.
func lateWaitsOf(view *View) *lateWaitIndex {
	if view == nil {
		return nil
	}
	index := &lateWaitIndex{readings: map[string]map[string]Anomaly{},
		cut: view.AnomaliesTotal > len(view.Anomalies) || view.DemotedTotal > len(view.Demoted) ||
			view.UndecidableTotal > len(view.Undecidable) || view.ByDesignTotal > len(view.ByDesign)}
	for _, column := range [][]Anomaly{view.Anomalies, view.Demoted, view.Undecidable, view.ByDesign, view.NoData, view.NoDataMemory, view.RetainedShare, view.ReadEarly, view.ReadHeld, view.LateSeries} {
		for _, anomaly := range column {
			byObject := index.readings[anomaly.Replica]
			if byObject == nil {
				byObject = map[string]Anomaly{}
				index.readings[anomaly.Replica] = byObject
			}
			if _, seen := byObject[anomaly.QueryGroup]; !seen {
				byObject[anomaly.QueryGroup] = anomaly
			}
		}
	}
	return index
}

// of is one object's wait as its own replica read it.
func (index *lateWaitIndex) of(queryGroup, replica string) LateWait {
	if index == nil {
		return LateWait{Unknown: LateWaitReadingsCut}
	}
	anomaly, found := index.readings[replica][queryGroup]
	if !found {
		if index.cut {
			return LateWait{Unknown: LateWaitReadingsCut}
		}
		return LateWait{Unknown: LateWaitNoReading}
	}
	blocked := anomaly.Blocked
	if blocked == nil {
		blocked = blockedOf(anomaly, anomaly.Finding.Schedule)
	}
	var wait LateWait
	// A row blockedOf gives no reading -- no data, a read held, a series the
	// lookback missed -- is not detection stopped at a step, and says so by
	// carrying no stage; its reason is still named.
	if blocked != nil {
		wait.Stage, wait.Reason = blocked.Stage, blocked.Code
	}
	// A reading with no failure code -- an overdue wake with nothing behind
	// it -- is named by its own kind, which is closed.
	if wait.Reason == "" {
		wait.Reason = anomaly.ReasonCode
	}
	if wait.Reason == "" {
		wait.Reason = anomaly.Kind
	}
	since := anomaly.ReasonSince
	if since.IsZero() {
		since = anomaly.Since
	}
	if !since.IsZero() {
		at := since.UTC()
		wait.Since = &at
	}
	// HISTORY_WARMING always reads as EVALUATE (failureFacets), so the
	// reason alone selects it.
	if wait.Reason == contract.ReasonHistoryWarming && anomaly.Coverage != nil {
		coverage := anomaly.Coverage
		wait.Warming = &WarmingSplit{Short: coverage.Short, ShortFresh: coverage.ShortFresh, Empty: coverage.Empty, Guarded: coverage.Guarded}
	}
	return wait
}

// strategyLateWait is the wait of the strategy's worst late object -- the one
// its punctuality was taken from, overdue before late, then by object -- and
// nil when no object is late.
func strategyLateWait(row DiagnosisRow, due *DueView, waits *lateWaitIndex) *LateWait {
	var chosen *DiagnosisPlan
	rank := 0
	for index := range row.Plans {
		plan := &row.Plans[index]
		if plan.QueryGroup == "" {
			continue
		}
		class := due.objectPunctuality(plan.QueryGroup, plan.Replica)
		if class != PunctualityLate && class != PunctualityOverdue {
			continue
		}
		if punctualityRank[class] > rank || punctualityRank[class] == rank && plan.QueryGroup < chosen.QueryGroup {
			chosen, rank = plan, punctualityRank[class]
		}
	}
	if chosen == nil {
		return nil
	}
	wait := waits.of(chosen.QueryGroup, chosen.Replica)
	return &wait
}

// strategyLateWaits groups the strategy's late and overdue objects by what
// they waited on, most objects first.
func strategyLateWaits(row DiagnosisRow, due *DueView, waits *lateWaitIndex) []LateWaitGroup {
	type key struct{ stage, reason, unknown string }
	groups := map[key]*LateWaitGroup{}
	seen := map[string]bool{}
	for _, plan := range row.Plans {
		if plan.QueryGroup == "" || seen[plan.Replica+"\x00"+plan.QueryGroup] {
			continue
		}
		class := due.objectPunctuality(plan.QueryGroup, plan.Replica)
		if class != PunctualityLate && class != PunctualityOverdue {
			continue
		}
		seen[plan.Replica+"\x00"+plan.QueryGroup] = true
		wait := waits.of(plan.QueryGroup, plan.Replica)
		k := key{string(wait.Stage), wait.Reason, wait.Unknown}
		group := groups[k]
		if group == nil {
			group = &LateWaitGroup{LateWait: LateWait{Stage: wait.Stage, Reason: wait.Reason, Unknown: wait.Unknown}}
			groups[k] = group
		}
		group.Objects++
		if wait.Since != nil && (group.Since == nil || wait.Since.Before(*group.Since)) {
			at := *wait.Since
			group.Since = &at
		}
		if wait.Warming != nil {
			if group.Warming == nil {
				group.Warming = &WarmingSplit{}
			}
			group.Warming.Short += wait.Warming.Short
			group.Warming.ShortFresh += wait.Warming.ShortFresh
			group.Warming.Empty += wait.Warming.Empty
			group.Warming.Guarded += wait.Warming.Guarded
		}
	}
	if len(groups) == 0 {
		return nil
	}
	list := make([]LateWaitGroup, 0, len(groups))
	for _, group := range groups {
		list = append(list, *group)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Objects != list[j].Objects {
			return list[i].Objects > list[j].Objects
		}
		a, b := list[i], list[j]
		return string(a.Stage)+"\x00"+a.Reason+"\x00"+a.Unknown < string(b.Stage)+"\x00"+b.Reason+"\x00"+b.Unknown
	})
	return list
}
