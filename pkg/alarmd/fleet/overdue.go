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
)

// OverdueWake is one object whose wake time has passed with nothing coming back
// for it.
//
// It exists because of what a due index removes. Today every owned object is
// picked up every couple of seconds whether or not it has anything to do, which
// is mostly waste -- and is also the only continuous evidence that an object is
// still being reached. Once objects that are not due are skipped before
// dispatch, an object left on a wake time nothing corrects produces no round,
// no failure and no entry anywhere: the tracker's determined flag is a latch
// that never clears, so the object stays counted as covered and spoken for, and
// the walk still reaches it, so round coverage still completes. A deployment
// that has stopped evaluating it reports HEALTHY.
//
// This is the signal that closes that hole, and it is why it has to ship before
// the index does rather than alongside it.
//
// It covers two conditions rather than one, because the index entry is rewritten
// when a round returns rather than when it is dispatched. So a wake time still
// in the past means either that nothing picked the object up, or that something
// did and never came back -- and the second is a hung round, which the phrase
// "no round since" would have missed entirely, since that round did start. An
// entry missing altogether means only that the object has not been evaluated
// since this replica took it over.
type OverdueWake struct {
	QueryGroup string
	// WakeAt is when the object was supposed to be picked up again.
	WakeAt time.Time
	// IntervalSeconds is the object's own evaluation period, which is what
	// decides how late is late. A fixed threshold cannot serve a population
	// whose periods run from ten seconds to ten minutes: it either flickers on
	// the slow objects or stays blind while a fast one misses dozens of turns.
	IntervalSeconds int64
}

// LateAt says whether the wake is a whole evaluation period late at now: the
// wakes OverdueAnomalies keeps. One that states no period never is -- its
// schedule was not read, and there is no period to be late by.
func (wake OverdueWake) LateAt(now time.Time) bool {
	if wake.WakeAt.IsZero() || wake.IntervalSeconds <= 0 {
		return false
	}
	return now.Sub(wake.WakeAt) > time.Duration(wake.IntervalSeconds)*time.Second
}

// OverdueWakeSource is what a scheduler's due index has to be able to answer.
//
// It returns entries rather than a count because this reaches the object list,
// not a tile: "something is not being evaluated" is not actionable and "these
// three objects are not being evaluated" is. It is an interface so this package
// states what it needs and stays independent of how the index is built --
// including of whether one exists, which is what a nil source means.
type OverdueWakeSource interface {
	// OverdueWakes returns parked objects whose wake time has passed, oldest
	// wake first, at most limit of them, with the true count before the limit
	// was applied.
	OverdueWakes(now time.Time, limit int) ([]OverdueWake, int)
}

// OverdueFacts is what one replica says about the objects it parked and did not
// come back to.
//
// Absent means this deployment has no due index at all, which is a different
// answer from "it has one and nothing is overdue" -- and the second is the one
// that should be read as good news. A zero in place of the absence would report
// the good news for a deployment where nothing is being watched.
type OverdueFacts struct {
	// Total counts objects past a full evaluation period since their wake time.
	Total int `json:"total"`
	// Truncated says the scheduler held more past-wake entries than it
	// published, so Total is a floor rather than a count.
	Truncated bool `json:"truncated,omitempty"`
	// OldestSeconds is how long the worst one has been waiting.
	OldestSeconds float64 `json:"oldest_seconds,omitempty"`
	// PeriodUnknown counts the past-wake entries that carry no evaluation
	// period: the due index writes one for a round that could not read the
	// object's schedule (a real state, stated on purpose, most often for a
	// moment after a start). Overdue is a whole period late, so such an entry
	// is never overdue and never listed; counted here instead, and a count
	// that stays up is its own finding: a schedule that is not being read.
	PeriodUnknown int `json:"period_unknown,omitempty"`
	// PeriodUnknownObjects names them, each with when this replica first saw
	// it without a period: a count says some schedule is not being read, the
	// names say which. OverdueAnomalies names every one the index returned;
	// the publisher dates them all, remembering each until its period
	// arrives, and publishes the MaxPeriodUnknownObjects longest without one
	// (LongestWithoutPeriod).
	PeriodUnknownObjects []PeriodUnknownObject `json:"period_unknown_objects,omitempty"`
}

// MaxPeriodUnknownObjects bounds the objects OverdueFacts names under
// PeriodUnknown: a stuck schedule is one or a few objects, and the count
// carries the rest.
const MaxPeriodUnknownObjects = 20

// PeriodUnknownObject is one object whose wake carries no period, and since
// when this replica has seen it so: no guessed duration stands in for the
// period it does not have, only how long it has gone without one.
type PeriodUnknownObject struct {
	QueryGroup string    `json:"query_group"`
	Since      time.Time `json:"since"`
}

// OverdueAnomalies turns parked objects into list entries, keeping the ones a
// whole evaluation period late.
//
// One period is the threshold because it is the only one that describes itself:
// past it, an evaluation that should have happened did not, whatever the
// object's period is. Anything shorter would report the ordinary lag between a
// wake time and the walk that services it, and any fixed duration would mean
// something different for a ten-second object than for a ten-minute one.
//
// It also absorbs the time a round spends running, which sits inside the window
// this measures: a round takes under a second in the ordinary case and tens of
// seconds in the slow one, so an object whose period is longer than its own
// execution is never reported for merely being busy. One whose period is
// shorter is reported -- correctly, because by then it has missed turns.
//
// An object whose wake carries no period has no period to be late by: its
// schedule was not read. It is not listed, not overdue and opens no episode,
// and is counted under PeriodUnknown, with no guessed duration standing in
// for the period it does not have.
func OverdueAnomalies(
	wakes []OverdueWake,
	total int,
	now time.Time,
	replica string,
	strategiesFor func(string) []StrategyRef,
) ([]Anomaly, OverdueFacts) {
	anomalies := make([]Anomaly, 0, len(wakes))
	facts := OverdueFacts{Truncated: total > len(wakes)}
	for _, wake := range wakes {
		if wake.QueryGroup == "" || wake.WakeAt.IsZero() {
			continue
		}
		if wake.IntervalSeconds <= 0 {
			facts.PeriodUnknown++
			facts.PeriodUnknownObjects = append(facts.PeriodUnknownObjects, PeriodUnknownObject{QueryGroup: wake.QueryGroup})
			continue
		}
		if !wake.LateAt(now) {
			continue
		}
		if late := now.Sub(wake.WakeAt); late.Seconds() > facts.OldestSeconds {
			facts.OldestSeconds = late.Seconds()
		}
		anomaly := Anomaly{
			QueryGroup: wake.QueryGroup,
			Kind:       KindOverdueWake,
			ReasonCode: ReasonWakeMissed,
			// Since is the moment it should have been picked up, so the list's
			// duration column reads as how long it has not been evaluated.
			Since: wake.WakeAt,
			// The wake time lives in a process-local index, so this anomaly
			// disappears when that process does -- like every other duration
			// here that is not backed by persisted business state.
			SinceFrom: SinceSnapshotContinuity,
			Replica:   replica,
		}
		if strategiesFor != nil {
			anomaly.Strategies = strategiesFor(wake.QueryGroup)
		}
		anomalies = append(anomalies, anomaly)
	}
	facts.Total = len(anomalies)
	// Worst first, so a truncated list keeps the objects that have been waiting
	// longest rather than whichever the map happened to yield.
	sort.Slice(anomalies, func(left, right int) bool {
		if anomalies[left].Since.Equal(anomalies[right].Since) {
			return anomalies[left].QueryGroup < anomalies[right].QueryGroup
		}
		return anomalies[left].Since.Before(anomalies[right].Since)
	})
	return anomalies, facts
}

// aggregateOverdue folds the replicas' counts into one. Counts add up because
// each replica parks its own objects; the oldest is the worst one anywhere,
// which is the number that decides whether anyone has to act.
func aggregateOverdue(view *View, snapshots []Snapshot) {
	var overdue *OverdueFacts
	for _, snapshot := range snapshots {
		facts := snapshot.Overdue
		if facts == nil {
			continue
		}
		if overdue == nil {
			overdue = &OverdueFacts{}
		}
		overdue.Total += facts.Total
		overdue.PeriodUnknown += facts.PeriodUnknown
		overdue.PeriodUnknownObjects = append(overdue.PeriodUnknownObjects, facts.PeriodUnknownObjects...)
		overdue.Truncated = overdue.Truncated || facts.Truncated
		if facts.OldestSeconds > overdue.OldestSeconds {
			overdue.OldestSeconds = facts.OldestSeconds
		}
	}
	if overdue != nil {
		overdue.PeriodUnknownObjects = LongestWithoutPeriod(overdue.PeriodUnknownObjects)
	}
	view.Overdue = overdue
}

// LongestWithoutPeriod is the MaxPeriodUnknownObjects of the objects that
// have gone longest without a period, longest first, and among equal ones in
// the order given: a replica's list comes oldest wake first, and the replicas'
// merged in the order the replicas are expected in.
func LongestWithoutPeriod(objects []PeriodUnknownObject) []PeriodUnknownObject {
	if len(objects) == 0 {
		return objects
	}
	sorted := append([]PeriodUnknownObject(nil), objects...)
	sort.SliceStable(sorted, func(left, right int) bool {
		l, r := sorted[left], sorted[right]
		if !l.Since.Equal(r.Since) {
			return l.Since.Before(r.Since)
		}
		return false
	})
	if len(sorted) > MaxPeriodUnknownObjects {
		sorted = sorted[:MaxPeriodUnknownObjects]
	}
	return sorted
}
