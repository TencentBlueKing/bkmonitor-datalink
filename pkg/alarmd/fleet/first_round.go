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

// FirstRoundWait is one object waiting for its first round.
type FirstRoundWait struct {
	QueryGroup      string    `json:"query_group"`
	IntervalSeconds int64     `json:"interval_seconds"`
	DueAt           time.Time `json:"due_at"`
}

// AwaitingFirstRound says whether an owned object that has no conclusion is
// only waiting for its first round: its period is longer than
// RestartCatchUpGrace, and its wake is a schedule bound -- not a backoff, not
// a query cooldown -- ahead of now by no more than one period.
//
// The tracker's "no conclusion" alone does not say no round has run: rounds
// that did not complete -- not due, deferred, still running, cancelled --
// leave the object undetermined on purpose. What makes the exemption safe is
// the bound the scheduler writes back for each of those (recordDueBound):
// a cancelled, preflight, single-flight-busy or source-error round writes a
// zero bound, so the wake is clamped to now and is not ahead; a backoff,
// retry, blocked source or failed execution writes a Deferred bound; a query
// cooldown writes QueryCooldown. The only bound that is ahead and none of
// those is source_not_due: the turn really has not come. A change to what
// recordDueBound writes for any of these moves this exemption's premise.
//
// The period bound is what keeps this from undoing "owning is not knowing".
// A replica that has just restarted owns everything and has observed nothing;
// its minute objects are not exempted and still hold the verdict at UNKNOWN
// until they speak, so a restart that runs nothing still reads UNKNOWN. What
// is exempted is an object whose first turn cannot have come yet whatever
// the replica is doing.
func AwaitingFirstRound(wake WakeFacts, now time.Time) bool {
	if !wake.Known || wake.Deferred || wake.Cooling || wake.IntervalSeconds <= 0 {
		return false
	}
	period := time.Duration(wake.IntervalSeconds) * time.Second
	return period > RestartCatchUpGrace && wake.DueAt.After(now) && !wake.DueAt.After(now.Add(period))
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
