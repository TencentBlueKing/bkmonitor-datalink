// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import "time"

// A retained skip record says an object gave up on a span of Slots. It does
// not say why, and the two record lines read every one of them as this
// deployment giving up for want of capacity: a live page filed some four
// hundred objects that way, under "曾经漏检、不用处理", while the records were
// three different things -- most of them objects the backend was refusing
// whose cooldown had carried their backlog past the replay range, a few
// dozen a rollout's catch-up an hour earlier, and the rest short-period
// objects still losing rounds to the scheduler's replay bound that minute.
// The first is the refusal's consequence and no amount of capacity changes
// it; the second is over; the third is happening. This file tells them apart
// from what the view already knows about each object.

// Loss is what a retained skip record is a record of.
type Loss string

const (
	// LossWhileDemoted: the object is in the demoted pool right now. The
	// skip is what a query cooldown does to an object's backlog -- rounds
	// wait out the cooldown, the oldest fall past the replay range and are
	// given up -- so the loss belongs on the line the object is already
	// under, as that line's consequence, not on a line of its own.
	LossWhileDemoted Loss = "WHILE_DEMOTED"
	// LossAfterRestart: not demoted, within the window, and made within
	// RestartCatchUpGrace of its replica's process start. A restart puts
	// every short-period object past its replay bound at once -- a live
	// rollout skipped a hundred and sixty in a minute -- and for the ten
	// minutes after each one the page would otherwise read "正在漏检" with
	// no mechanism named. It is a loss; it is also expected to stop on its
	// own, and it asks no capacity question.
	LossAfterRestart Loss = "AFTER_RESTART"
	// LossOngoing: not demoted, within the window, and not the restart's.
	// The object is losing rounds now; it is a current line, and this
	// deployment's.
	LossOngoing Loss = "ONGOING"
	// LossHistorical: not demoted, and the record is older than the window.
	// A loss that stopped; it stays on record because the span is never
	// re-evaluated, and it is not work.
	LossHistorical Loss = "HISTORICAL"
)

// Losses lists every kind, for the page's completeness test.
var Losses = []Loss{LossWhileDemoted, LossAfterRestart, LossOngoing, LossHistorical}

// RestartCatchUpGrace is how long after a replica's process start a skip is
// read as the restart's catch-up. The spike is over within the first
// minutes; five covers it and leaves the rest of the window to the
// mechanisms that are not the restart's. It travels on the response.
const RestartCatchUpGrace = 5 * time.Minute

// RecentSkipWindow is the bound on "still happening". A record younger than
// this is a loss in progress; older, it is history. Ten minutes is several
// rounds of any object this deployment schedules, so a record that old
// belongs to an object that has since run clean for that many rounds. It
// travels on the response, so the page prints it rather than assuming it.
const RecentSkipWindow = 10 * time.Minute

// GroupByLoss folds the two record checks on what each record is: the
// loss kinds above for records, the deciding code for the objects a column
// put there -- which are the budget rejections, the one case where the
// word capacity is earned.
const GroupByLoss GroupBy = "loss"

// lossOf decides a record's kind from the object's column, the record's
// age, and whether it was made in its replica's restart grace.
func lossOf(demoted, afterRestart bool, at, now time.Time) Loss {
	switch {
	case demoted:
		return LossWhileDemoted
	case now.Sub(at) > RecentSkipWindow:
		return LossHistorical
	case afterRestart:
		return LossAfterRestart
	default:
		return LossOngoing
	}
}

// replicaStarts maps each replica to its process start, where published.
func replicaStarts(view *View) map[string]time.Time {
	starts := map[string]time.Time{}
	for _, replica := range view.PerReplica {
		if !replica.StartedAt.IsZero() {
			starts[replica.Replica] = replica.StartedAt
		}
	}
	return starts
}

// inRestartGrace reports whether a record made at by replica falls within
// the grace after that replica's start. Unknown start: no.
func inRestartGrace(starts map[string]time.Time, replica string, at time.Time) bool {
	start, known := starts[replica]
	return known && !at.Before(start) && at.Sub(start) <= RestartCatchUpGrace
}

// Consequence is what a line's objects lost while under it: how many also
// skipped detection, how many of those within the window, and the newest.
// It is carried on the refusal and backend lines, whose cooldown is what
// produced the skips.
type Consequence struct {
	Skipped       int        `json:"skipped"`
	SkippedRecent int        `json:"skipped_recent"`
	SkippedNewest *time.Time `json:"skipped_newest,omitempty"`
}

func (consequence *Consequence) note(at, now time.Time) {
	consequence.Skipped++
	if now.Sub(at) <= RecentSkipWindow {
		consequence.SkippedRecent++
	}
	if consequence.SkippedNewest == nil || at.After(*consequence.SkippedNewest) {
		newest := at
		consequence.SkippedNewest = &newest
	}
}

// demotedObject is one object in the demoted column -- the pool a query
// cooldown holds: the line it is under, and since when its record is the
// cooldown's doing: the pool entry where the row carries it, the anomaly's
// onset where it does not (a publisher older than the field), which is
// earlier and errs on the refusal's side.
type demotedObject struct {
	line  Check
	since time.Time
}

func demotedObjects(view *View) map[string]demotedObject {
	under := map[string]demotedObject{}
	for _, anomaly := range view.Demoted {
		since := anomaly.DemotedSince
		if since.IsZero() {
			since = anomaly.Since
		}
		under[anomaly.QueryGroup] = demotedObject{line: anomaly.Finding.Check, since: since}
	}
	return under
}

// lossRecords walks the view's retained records once and hands each to the
// caller with its kind: the record's own check, and for a demoted object
// the line it is under, whose consequence the record is. One walk, so the
// lines, the rows and the arithmetic count the same records the same way.
//
// A record is the cooldown's consequence only if it was made after the
// object entered the pool: an object that lost rounds to the replay bound
// before it ever entered has an older record, and folding it into the
// refusal would hide a loss the refusal did not cause. The row carries the
// pool entry; a row from a publisher older than that field carries only
// the anomaly's onset, which is earlier, so a record made between onset and
// entry is then still folded -- an approximation on the side of the
// refusal, and a record keeps only the latest skip per object, so on a
// demoted object it is almost always the cooldown's. A demoted object under
// no line -- which the tracker does not produce -- is read by its age like
// any other, rather than counted on a line that does not exist.
func lossRecords(view *View, now time.Time, visit func(queryGroup string, check, line Check, code string, skip SkippedSpan, loss Loss)) {
	if view == nil {
		return
	}
	demoted := demotedObjects(view)
	starts := replicaStarts(view)
	each := func(queryGroup string, check Check, code string, skip SkippedSpan) {
		object, isDemoted := demoted[queryGroup]
		consequence := isDemoted && object.line != "" && !skip.At.Before(object.since)
		loss := lossOf(consequence, inRestartGrace(starts, skip.Replica, skip.At), skip.At, now)
		line := Check("")
		if loss == LossWhileDemoted {
			line = object.line
		}
		visit(queryGroup, check, line, code, skip, loss)
	}
	for queryGroup, skip := range view.GapSkips {
		check := CheckDetectionAbandoned
		if skip.FullyApplied() {
			// Every Slot of the span had been executed by an earlier attempt:
			// not detection given up, bookkeeping interrupted.
			check = CheckBookkeepingAbandoned
		}
		each(queryGroup, check, "GAP_SKIPPED", skip)
	}
	for queryGroup, pruned := range view.PrunedSkips {
		each(queryGroup, CheckTimelinePruned, "SCHEDULE_PRUNED", SkippedSpan{FirstSlot: pruned.From, LastSlot: pruned.To,
			At: pruned.At, Replica: pruned.Replica, Strategies: pruned.Strategies, IntervalSeconds: pruned.IntervalSeconds})
	}
}
