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
	// MissingPeriod counts entries that arrived without the object's own
	// evaluation period. There is no ordinary path that produces one -- an
	// object with no due Plans never gets a wake time written at all -- so a
	// non-zero here is a defect in whatever wrote the entry, not a state of the
	// deployment. It is counted rather than left to the defensive default alone,
	// because that default keeps the object visible and would otherwise make the
	// defect indistinguishable from ordinary reporting.
	MissingPeriod int `json:"missing_period,omitempty"`
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
// An object with no period stated is kept once its wake time has passed at all.
// Dropping it would make a missing field into silence about the object, and
// silence here is indistinguishable from health.
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
		late := now.Sub(wake.WakeAt)
		if late <= 0 {
			continue
		}
		if period := time.Duration(wake.IntervalSeconds) * time.Second; period > 0 {
			if late <= period {
				continue
			}
		} else {
			facts.MissingPeriod++
		}
		if late.Seconds() > facts.OldestSeconds {
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
		overdue.MissingPeriod += facts.MissingPeriod
		overdue.Truncated = overdue.Truncated || facts.Truncated
		if facts.OldestSeconds > overdue.OldestSeconds {
			overdue.OldestSeconds = facts.OldestSeconds
		}
	}
	view.Overdue = overdue
}
