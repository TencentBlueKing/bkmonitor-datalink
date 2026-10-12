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

// ReplicaPart is one replica's share of the numbers the first screen draws
// from rows, in the shape that adds across replicas: counts add, sets take
// their union, and a moment keeps the earliest. The part of a view of one
// replica's snapshot, merged with the others', gives the numbers the whole
// view gives -- which is what lets a replica publish its part and the leader
// read parts instead of every row (fleet-read-scale-design §6).
//
// A duration is never kept, only the moment it runs from: the reader turns
// it into a duration at its own time, so a part read later reads older.
// What is decided against the clock -- a pooled object being due, a record
// being recent -- is decided at the replica's own now, so a part is read at
// most a publish interval late.
//
// The merge equals the whole view while no object is held by two replicas.
// During a handover one is, for the moment of the handover: the whole view
// keeps one record of the object's skips where each replica's part counts its
// own, so the parts count it twice -- and a skip the old owner records while
// the new owner has already pooled the object reads, on the old owner's part,
// as a loss in progress where the whole view reads it as the pool's
// consequence. It is the moment compareCoverage reports the object as held by
// several, and the first screen says so beside the counts it reads from the
// parts (held_by_several_total) rather than going back to the whole view,
// which is the read this exists to stop.
//
// A replica publishes its part beside its snapshot (ReplicaSummary), with
// the fields the health route and the verdict scrape read. CheckRows and
// TodoRows are not published yet: the routes that draw the check lines and
// the to-do still read rows, and a part read back has neither -- the scrape
// reads the lines' counts from Metrics.
type ReplicaPart struct {
	Replica string `json:"replica"`
	// Attribution counts the anomaly column's rows by who they are
	// attributed to: what the verdict and the per-replica split are read from.
	Attribution AttributionTally `json:"attribution"`
	Impact      ImpactTally      `json:"impact"`
	// EmptyEveryRound is the objects of KindEmptyEveryRound among the no-data
	// rows. An object is one replica's, so the count adds.
	EmptyEveryRound int `json:"empty_every_round"`
	// DemotedDue is the pooled objects whose cooldown has ended and
	// DemotedDueSince the earliest end among them, zero when none has.
	DemotedDue      int       `json:"demoted_due"`
	DemotedDueSince time.Time `json:"demoted_due_since"`
	// CohortRows and Cooling are the rows' sides of Cohorts and Cooling; the
	// census they are joined with is the replicas' schedule, which is not a
	// row count and is aggregated with it.
	CohortRows map[int64]*CohortView `json:"cohort_rows,omitempty"`
	Cooling    coolingRows           `json:"cooling"`
	// Loss is the load's loss, counted from the replica's skip records.
	Loss LoadLoss `json:"loss"`
	// PrunedSkips, RetainedShare and ReadEarly are the first
	// FirstScreenListBound of each of the health route's object lists, in the
	// list's order, and each total is how many there are. The first few of
	// the merged lists are among the first few of some replica's, so this is
	// all a merge needs.
	PrunedSkips        []PrunedSkipRef    `json:"pruned_skips,omitempty"`
	PrunedSkipsTotal   int                `json:"pruned_skips_total"`
	RetainedShare      []RetainedShareRef `json:"retained_share,omitempty"`
	RetainedShareTotal int                `json:"retained_share_total"`
	ReadEarly          []ReadEarlyRef     `json:"read_early,omitempty"`
	ReadEarlyTotal     int                `json:"read_early_total"`
	// Truncated is the columns this replica published cut, a bit per
	// position in columnNames; merged, the columns any replica cut. A check
	// line is a sample when any column its rows came from was cut on any
	// replica, which the replica holding the rows cannot know alone.
	Truncated uint8 `json:"truncated"`
	// CheckRows is the rows' half of the first screen's check lines
	// (checkRowsOf): the lines' standings, gaps and source records are the
	// replicas' facts and are added when the lines are made (Checks).
	CheckRows checkTallies `json:"-"`
	// TodoRows is the rows' half of the first screen's to-do (todoRowsOf).
	TodoRows Todo `json:"-"`
	// Overdue is the objects of the rows under SLOTS_OVERDUE, for the replica
	// that publishes the part to keep its overdue episodes by; not published,
	// as a reader of summaries has no use for which.
	Overdue []OverdueObject `json:"-"`
	// RunningStrategies is the replica's running strategies by state
	// (runningStrategiesOf); nil from a snapshot that does not say which
	// strategies evaluate on its objects, and nil merged when any part is.
	RunningStrategies map[StateWord]int `json:"running_strategies,omitempty"`
	// Metrics is what the verdict scrape counts from rows. Nil on a part a
	// build before it published, which says nothing of those counts: such a
	// summary is read as none, and the replica is summarized from its
	// snapshot (Service.summarize).
	Metrics *MetricRows `json:"metrics,omitempty"`
}

// AttributionTally is the anomaly column's rows by attribution: Ours and
// External as attributed, Unknown with no evidence either way, and Other
// with the field not set at all -- which the per-replica split counts as
// unattributed and the verdict does not (Settle).
type AttributionTally struct {
	Ours     int `json:"ours"`
	External int `json:"external"`
	Unknown  int `json:"unknown"`
	Other    int `json:"other"`
}

// ReplicaPartOf reads a view's part. A view of one replica's snapshot gives
// that replica's; the rows must have been decided at now (Decide).
func ReplicaPartOf(view View, now time.Time) ReplicaPart {
	part := ReplicaPart{Impact: ImpactTallyOf(view, now)}
	if len(view.PerReplica) == 1 {
		part.Replica = view.PerReplica[0].Replica
	}
	for _, anomaly := range view.Anomalies {
		part.Attribution.add(anomaly.Attribution)
	}
	part.EmptyEveryRound = countEmptyEveryRound(view.NoData)
	columns := viewColumns(&view)
	part.Truncated = truncatedColumns(columnsTruncated(&view))
	part.CheckRows = checkRowsOf(columns, &view, now)
	part.Overdue = overdueObjectsOf(columns)
	part.TodoRows = todoRowsOf(columns, &view, now)
	part.CohortRows = cohortRowsOf(columns)
	part.Cooling = coolingRowsOf(columns, now)
	loss, census, graceUnknown := lossesOfView(&view, now)
	part.Loss = loss
	part.Metrics = metricRowsOf(&view, part.CheckRows, census, graceUnknown)
	pruned, retained, readEarly := prunedSkipList(view.PrunedSkips), retainedShareList(view.RetainedShare), readEarlyList(view.ReadEarly)
	part.PrunedSkips, part.PrunedSkipsTotal = firstScreenList(pruned), len(pruned)
	part.RetainedShare, part.RetainedShareTotal = firstScreenList(retained), len(retained)
	part.ReadEarly, part.ReadEarlyTotal = firstScreenList(readEarly), len(readEarly)
	for _, demoted := range view.Demoted {
		if cooldown := demoted.QueryCooldown; cooldown != nil && !cooldown.Until.IsZero() && cooldown.Until.Before(now) {
			part.DemotedDue++
			if part.DemotedDueSince.IsZero() || cooldown.Until.Before(part.DemotedDueSince) {
				part.DemotedDueSince = cooldown.Until
			}
		}
	}
	return part
}

// MergeReplicaParts adds replicas' parts into the deployment's.
func MergeReplicaParts(parts ...ReplicaPart) ReplicaPart {
	merged := ReplicaPart{CohortRows: map[int64]*CohortView{}, CheckRows: checkTallies{}, Metrics: &MetricRows{}}
	running := len(parts) > 0
	merged.RunningStrategies = map[StateWord]int{}
	tallies := make([]ImpactTally, 0, len(parts))
	for _, part := range parts {
		merged.Attribution.Ours += part.Attribution.Ours
		merged.Attribution.External += part.Attribution.External
		merged.Attribution.Unknown += part.Attribution.Unknown
		merged.Attribution.Other += part.Attribution.Other
		tallies = append(tallies, part.Impact)
		merged.EmptyEveryRound += part.EmptyEveryRound
		merged.DemotedDue += part.DemotedDue
		if !part.DemotedDueSince.IsZero() && (merged.DemotedDueSince.IsZero() || part.DemotedDueSince.Before(merged.DemotedDueSince)) {
			merged.DemotedDueSince = part.DemotedDueSince
		}
		mergeCohortRows(merged.CohortRows, part.CohortRows)
		mergeCoolingRows(&merged.Cooling, part.Cooling)
		mergeLoss(&merged.Loss, part.Loss)
		merged.Truncated |= part.Truncated
		mergeCheckTallies(merged.CheckRows, part.CheckRows)
		mergeTodoRows(&merged.TodoRows, part.TodoRows)
		running = running && part.RunningStrategies != nil
		for word, count := range part.RunningStrategies {
			merged.RunningStrategies[word] += count
		}
		// One part without the counts leaves the merged counts unknown,
		// never short by that replica's rows.
		if part.Metrics == nil {
			merged.Metrics = nil
		} else if merged.Metrics != nil {
			mergeMetricRows(merged.Metrics, part.Metrics)
		}
		merged.PrunedSkips, merged.PrunedSkipsTotal = append(merged.PrunedSkips, part.PrunedSkips...), merged.PrunedSkipsTotal+part.PrunedSkipsTotal
		merged.RetainedShare = append(merged.RetainedShare, part.RetainedShare...)
		merged.RetainedShareTotal += part.RetainedShareTotal
		merged.ReadEarly, merged.ReadEarlyTotal = append(merged.ReadEarly, part.ReadEarly...), merged.ReadEarlyTotal+part.ReadEarlyTotal
	}
	if !running {
		merged.RunningStrategies = nil
	}
	merged.Loss = merged.Loss.settled()
	merged.PrunedSkips = latestPerObject(merged.PrunedSkips, func(ref PrunedSkipRef) (string, time.Time) { return ref.QueryGroup, ref.At }, prunedSkipBefore)
	merged.RetainedShare = latestPerObject(merged.RetainedShare, func(ref RetainedShareRef) (string, time.Time) { return ref.QueryGroup, ref.Since },
		retainedShareBefore)
	merged.ReadEarly = latestPerObject(merged.ReadEarly, func(ref ReadEarlyRef) (string, time.Time) { return ref.QueryGroup, ref.Since }, readEarlyBefore)
	sort.Slice(merged.PrunedSkips, func(l, r int) bool { return prunedSkipBefore(merged.PrunedSkips[l], merged.PrunedSkips[r]) })
	sort.Slice(merged.RetainedShare, func(l, r int) bool {
		return retainedShareBefore(merged.RetainedShare[l], merged.RetainedShare[r])
	})
	sort.Slice(merged.ReadEarly, func(l, r int) bool { return readEarlyBefore(merged.ReadEarly[l], merged.ReadEarly[r]) })
	merged.PrunedSkips = firstScreenList(merged.PrunedSkips)
	merged.RetainedShare = firstScreenList(merged.RetainedShare)
	merged.ReadEarly = firstScreenList(merged.ReadEarly)
	merged.Impact = MergeImpactTallies(tallies...)
	return merged
}

// DemotedDueOldestSeconds is how long the earliest due pooled object has
// waited at now, whole seconds, as the view counts it.
func (part ReplicaPart) DemotedDueOldestSeconds(now time.Time) int {
	if part.DemotedDueSince.IsZero() {
		return 0
	}
	return int(now.Sub(part.DemotedDueSince).Seconds())
}

// Cohorts joins the replicas' schedule census with the part's rows.
func (part ReplicaPart) Cohorts(schedule *ScheduleCensus) []CohortView {
	return cohortsOf(schedule, part.CohortRows)
}

// CoolingAt is Cooling from the part's rows and the replicas' schedule,
// measured at now.
func (part ReplicaPart) CoolingAt(schedule *ScheduleCensus, now time.Time) CoolingFacts {
	return coolingOf(schedule, part.Cooling, now)
}

// Load is the load judgment from the replicas' facts on view and the
// part's loss.
func (part ReplicaPart) Load(view *View) Load {
	return loadOf(view, part.Loss)
}

// Checks is the first screen's check lines from the part's rows and the
// replicas' facts on view, at now. It reads a copy: making the lines adds the
// view's own folds, which a second reading would otherwise count again.
func (part ReplicaPart) Checks(view *View, now time.Time) []CheckReport {
	rows := checkTallies{}
	mergeCheckTallies(rows, part.CheckRows)
	return reportChecksFrom(rows, part.Truncated, view, now)
}

// Todo is the first screen's to-do from the part's rows, the lines made from
// them (Checks) and the replicas' facts on view.
func (part ReplicaPart) Todo(reports []CheckReport, view *View) Todo {
	return todoFrom(part.TodoRows, reports, view)
}
