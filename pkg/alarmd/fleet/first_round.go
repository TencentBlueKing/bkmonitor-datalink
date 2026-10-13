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

// MaxFirstRoundWaits bounds the objects a snapshot names as awaiting their
// first round; the total counts every one.
const MaxFirstRoundWaits = 64

// MaxUndeterminedObjects bounds the objects a snapshot, and the deployment,
// names as undetermined; the total counts every one.
const MaxUndeterminedObjects = 64

// UndeterminedObject is one owned object with no conclusion that is not only
// waiting for its first round - one of those the verdict holds at UNKNOWN
// for: when this replica took it over, its period, and when its next Slot is
// runnable (zero when no bound is known). Replica is set on the deployment's
// list.
type UndeterminedObject struct {
	QueryGroup      string    `json:"query_group"`
	IntervalSeconds int64     `json:"interval_seconds,omitempty"`
	OwnedSince      time.Time `json:"owned_since"`
	NextDue         time.Time `json:"next_due,omitempty"`
	Replica         string    `json:"replica,omitempty"`
}

// aggregateUndetermined puts the replicas' undetermined objects together,
// longest owned first, each naming its replica, within the bound.
func aggregateUndetermined(view *View, snapshots []Snapshot) {
	var all []UndeterminedObject
	total := 0
	for _, snapshot := range snapshots {
		total += snapshot.UndeterminedTotal
		for _, object := range snapshot.Undetermined {
			object.Replica = snapshot.Replica
			all = append(all, object)
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if !all[i].OwnedSince.Equal(all[j].OwnedSince) {
			return all[i].OwnedSince.Before(all[j].OwnedSince)
		}
		return all[i].QueryGroup < all[j].QueryGroup
	})
	if len(all) > MaxUndeterminedObjects {
		all = all[:MaxUndeterminedObjects]
	}
	view.Undetermined, view.UndeterminedTotal = all, total
}

// FirstRoundWait is one object waiting for its first round.
type FirstRoundWait struct {
	QueryGroup      string    `json:"query_group"`
	IntervalSeconds int64     `json:"interval_seconds"`
	DueAt           time.Time `json:"due_at"`
}

// AwaitingFirstRound says whether an owned object that has no conclusion is
// only waiting for its first round since this replica took it over: the Slot
// its bound waits for is the first grid point since then (or one pending from
// before), and that Slot is not yet runnable. The comparison is on the grid,
// not on when the Slot becomes runnable: the read wait, a stagger phase and a
// read hold all sit between the two, and a held Slot is deferred for its data,
// not for a failure, so it is still waiting.
//
// The tracker's "no conclusion" alone does not say no round has run: rounds
// that did not complete -- not due, deferred, still running, cancelled --
// leave the object undetermined on purpose. What makes the exemption safe is
// the bound the scheduler writes back for each of those (recordDueBound):
// a cancelled, preflight, single-flight-busy or source-error round writes a
// zero bound, so the wake is clamped to now and is not ahead; a backoff,
// retry, blocked source or failed execution writes a Deferred bound that is
// not a readiness hold; a query cooldown writes QueryCooldown. A round that
// ran and did not conclude moves the next Slot past the first grid point. A
// change to what recordDueBound writes for any of these moves this
// exemption's premise.
//
// A first Slot behind a read hold longer than its period reads as waiting
// for the whole hold: its turn really has not come. A hold that sits until
// the replay age gives up on it therefore shows as waiting, not unknown, for
// up to that age - by design, not a gap in the rule.
//
// "Owning is not knowing" still holds: a replica that has just restarted and
// runs nothing has, once each object's first Slot is runnable and has not
// run, objects whose wake is no longer ahead, and the verdict is UNKNOWN.
func AwaitingFirstRound(wake WakeFacts, ownedSince, now time.Time) bool {
	if !wake.Known || wake.Cooling || wake.Deferred && !wake.ReadinessHold || wake.IntervalSeconds <= 0 {
		return false
	}
	if ownedSince.IsZero() || wake.NextSlot.IsZero() || !wake.DueAt.After(now) {
		return false
	}
	period := time.Duration(wake.IntervalSeconds) * time.Second
	return wake.NextSlot.Before(ownedSince.Add(period))
}

// FirstRoundWaits is the deployment's objects awaiting their first round, by
// period.
type FirstRoundWaits struct {
	Objects int                    `json:"objects"`
	Periods []FirstRoundWaitPeriod `json:"periods"`
	// Line is the above in plain words.
	Line string `json:"line"`
}

// FirstRoundWaitPeriod is the named waits of one period, and the earliest
// and latest of their first rounds.
type FirstRoundWaitPeriod struct {
	IntervalSeconds int64     `json:"interval_seconds"`
	Objects         int       `json:"objects"`
	FirstDueAt      time.Time `json:"first_due_at"`
	LastDueAt       time.Time `json:"last_due_at"`
}

func (waits *FirstRoundWaits) count() int {
	if waits == nil {
		return 0
	}
	return waits.Objects
}

// add folds one snapshot's waits in. The total is the snapshots' totals; the
// periods are over the objects they named.
func (waits *FirstRoundWaits) add(snapshot Snapshot) *FirstRoundWaits {
	if snapshot.AwaitingFirstRoundTotal == 0 {
		return waits
	}
	if waits == nil {
		waits = &FirstRoundWaits{}
	}
	waits.Objects += snapshot.AwaitingFirstRoundTotal
	for _, wait := range snapshot.AwaitingFirstRound {
		index := sort.Search(len(waits.Periods), func(i int) bool { return waits.Periods[i].IntervalSeconds >= wait.IntervalSeconds })
		if index == len(waits.Periods) || waits.Periods[index].IntervalSeconds != wait.IntervalSeconds {
			waits.Periods = append(waits.Periods, FirstRoundWaitPeriod{})
			copy(waits.Periods[index+1:], waits.Periods[index:])
			waits.Periods[index] = FirstRoundWaitPeriod{IntervalSeconds: wait.IntervalSeconds, FirstDueAt: wait.DueAt, LastDueAt: wait.DueAt}
		}
		period := &waits.Periods[index]
		period.Objects++
		if wait.DueAt.Before(period.FirstDueAt) {
			period.FirstDueAt = wait.DueAt
		}
		if wait.DueAt.After(period.LastDueAt) {
			period.LastDueAt = wait.DueAt
		}
	}
	waits.Line = waits.text()
	return waits
}

func (waits *FirstRoundWaits) text() string {
	parts := make([]string, 0, len(waits.Periods))
	named := 0
	for _, period := range waits.Periods {
		named += period.Objects
		parts = append(parts, fmt.Sprintf("周期 %s 的 %d 个，最迟 %s 到期", periodWords(period.IntervalSeconds), period.Objects,
			period.LastDueAt.UTC().Format("01-02 15:04Z")))
	}
	line := fmt.Sprintf("%d 个对象在等第一轮（%s）", waits.Objects, strings.Join(parts, "；"))
	if waits.Objects > named {
		line += fmt.Sprintf("；另有 %d 个没有列出", waits.Objects-named)
	}
	return line
}

func periodWords(seconds int64) string {
	period := time.Duration(seconds) * time.Second
	switch {
	case period%time.Hour == 0:
		return fmt.Sprintf("%d h", int64(period/time.Hour))
	case period%time.Minute == 0:
		return fmt.Sprintf("%d min", int64(period/time.Minute))
	default:
		return fmt.Sprintf("%d s", seconds)
	}
}
