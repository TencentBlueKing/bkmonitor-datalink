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

// The schedule dimension: where each object is in its own cycle, and whether
// the deployment as a whole is keeping up.
//
// It answers the question the resource numbers cannot -- "is the work being
// done on time" -- the way Prometheus answers it for rule groups: not by CPU
// but by counting the evaluations that missed their turn. An object's period
// is its own deadline: a round is on time when it finishes before the next one
// falls due. The grace is therefore one period, which is this deployment's
// choice (Healthchecks.io lets each job set its own), and it is derived, not
// configured.

// ScheduleCensus is one replica's count of its owned objects by where they are
// in their cycle, plus how its rounds have been finishing. Summed across
// replicas by aggregateSchedule.
type ScheduleCensus struct {
	// Waiting objects have their next due time ahead of them. Cooling is the
	// part of Waiting that is waiting because of a query cooldown rather than
	// because the schedule says so.
	Waiting int `json:"waiting"`
	Cooling int `json:"cooling"`
	// Late objects are past their due time by less than one period: the
	// ordinary lag between falling due and being serviced, or a slow round.
	Late int `json:"late"`
	// Overdue objects are past their due time by more than one period. They
	// have missed at least one turn, and nothing in the schedule catches them
	// up.
	Overdue           int     `json:"overdue"`
	OldestLateSeconds float64 `json:"oldest_late_seconds,omitempty"`
	// Never is the owned objects no round has returned for since this replica
	// took them over. Normal for a minute after a restart; not after.
	Never int `json:"never"`
	// MissingPeriod counts entries whose period is not known, so their
	// lateness could not be judged against one. They are counted in Late.
	MissingPeriod int `json:"missing_period,omitempty"`
	// Rounds that finished in the last hour and the last six, and how many of
	// each finished before the next round fell due. Six hours beside one hour
	// is the SRE workbook's two-window reading: the short window says now, the
	// long one says trend, and a short-window rate below the long-window rate
	// is the deployment falling behind.
	Completed1h int `json:"completed_1h"`
	OnTime1h    int `json:"on_time_1h"`
	Completed6h int `json:"completed_6h"`
	OnTime6h    int `json:"on_time_6h"`
	// HeldBack1h is the rounds of the last hour whose object the dispatcher had
	// pushed back before they ran -- no room in the ready queue, or not urgent
	// enough for the recovery queue -- and HeldBackOnTime1h how many of those
	// still finished before their next turn fell due. Together they answer
	// whether being pushed back costs a deadline.
	HeldBack1h       int `json:"held_back_1h"`
	HeldBackOnTime1h int `json:"held_back_on_time_1h"`
	// OverdueAgo is the overdue count this index found OverdueAgoSeconds ago
	// -- the oldest sample of the last hour it holds -- so the backlog can be
	// read as growing or shrinking rather than as one number. Absent when the
	// index has no earlier sample (a process that just started). Summed
	// across replicas only when every replica has one; the span is then the
	// shortest of theirs, because a trend is only as long as its youngest
	// sample.
	OverdueAgo        *int    `json:"overdue_ago,omitempty"`
	OverdueAgoSeconds float64 `json:"overdue_ago_seconds,omitempty"`
	// Cohorts is the same entries counted by their period, so a number read
	// per cohort elsewhere -- a lag histogram's 15 s series, a skip rate --
	// has the population it is over and a way to the objects in it. Absent
	// from a build before this field existed. An entry whose period is not
	// known is counted under IntervalSeconds 0.
	Cohorts []ScheduleCohort `json:"cohorts,omitempty"`
}

// ScheduleCohort is the objects of one evaluation period on one replica, as
// the due index holds them: how many, how many are waiting in a query
// cooldown, how many are past due by more than one period.
type ScheduleCohort struct {
	IntervalSeconds int64 `json:"interval_seconds"`
	Objects         int   `json:"objects"`
	Cooling         int   `json:"cooling"`
	Overdue         int   `json:"overdue"`
}

// Add folds another replica's census into this one. Counts add; the oldest
// lateness is the worst anywhere; the earlier backlog adds only when both
// sides have one, and is dropped when either does not -- so the aggregate
// starts from the first replica's census as it is, not from an empty one
// that would read as a replica without a sample.
func (census *ScheduleCensus) Add(other ScheduleCensus) {
	if census.OverdueAgo == nil || other.OverdueAgo == nil {
		census.OverdueAgo, census.OverdueAgoSeconds = nil, 0
	} else {
		sum := *census.OverdueAgo + *other.OverdueAgo
		census.OverdueAgo = &sum
		if other.OverdueAgoSeconds < census.OverdueAgoSeconds {
			census.OverdueAgoSeconds = other.OverdueAgoSeconds
		}
	}
	census.Waiting += other.Waiting
	census.Cooling += other.Cooling
	census.Late += other.Late
	census.Overdue += other.Overdue
	census.Never += other.Never
	census.MissingPeriod += other.MissingPeriod
	census.Completed1h += other.Completed1h
	census.OnTime1h += other.OnTime1h
	census.Completed6h += other.Completed6h
	census.OnTime6h += other.OnTime6h
	census.HeldBack1h += other.HeldBack1h
	census.HeldBackOnTime1h += other.HeldBackOnTime1h
	if other.OldestLateSeconds > census.OldestLateSeconds {
		census.OldestLateSeconds = other.OldestLateSeconds
	}
	census.Cohorts = addCohorts(census.Cohorts, other.Cohorts)
}

// addCohorts sums two replicas' cohorts by period, keeping the list ordered
// by period so two reads of the same deployment list the same way.
func addCohorts(into, other []ScheduleCohort) []ScheduleCohort {
	if len(other) == 0 {
		return into
	}
	merged := make(map[int64]ScheduleCohort, len(into)+len(other))
	for _, cohort := range into {
		merged[cohort.IntervalSeconds] = cohort
	}
	for _, cohort := range other {
		sum := merged[cohort.IntervalSeconds]
		sum.IntervalSeconds = cohort.IntervalSeconds
		sum.Objects += cohort.Objects
		sum.Cooling += cohort.Cooling
		sum.Overdue += cohort.Overdue
		merged[cohort.IntervalSeconds] = sum
	}
	result := make([]ScheduleCohort, 0, len(merged))
	for _, cohort := range merged {
		result = append(result, cohort)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].IntervalSeconds < result[j].IntervalSeconds })
	return result
}

// aggregateSchedule sums the replicas' censuses. Absent on every replica means
// no replica has a due index, which is a different answer from a deployment
// that is keeping up, so the aggregate stays absent too.
func aggregateSchedule(view *View, snapshots []Snapshot) {
	var census *ScheduleCensus
	for _, snapshot := range snapshots {
		if snapshot.Schedule == nil {
			continue
		}
		if census == nil {
			first := *snapshot.Schedule
			census = &first
			continue
		}
		census.Add(*snapshot.Schedule)
	}
	view.Schedule = census
}

// WakeFacts is what the due index knows about one object: when it next falls
// due, its period, and why the bound is where it is. Attached to every listed
// object by the publisher so the row can say where the object is in its cycle.
type WakeFacts struct {
	// Known is false when the index holds no entry for the object, which has
	// exactly one meaning there: nothing has been evaluated for it since this
	// replica took it over.
	Known           bool      `json:"known"`
	DueAt           time.Time `json:"due_at,omitempty"`
	IntervalSeconds int64     `json:"interval_seconds,omitempty"`
	Deferred        bool      `json:"deferred,omitempty"`
	Cooling         bool      `json:"cooling,omitempty"`
}

// ScheduleSource is what the publisher asks the due index for.
type ScheduleSource interface {
	// Census counts the owned objects by where they are in their cycle.
	// owned is how many objects the replica holds, so the census can say how
	// many have no entry at all.
	Census(now time.Time, owned int) ScheduleCensus
	// WakeOf reports the entry for one object, Known false when there is none.
	WakeOf(queryGroup string) WakeFacts
}

// Schedule is where an object is in its own cycle right now: the dimension the
// row's first column shows and the first sentence of the brief is built from.
type Schedule string

const (
	// ScheduleNew: no round has returned since this replica took the object
	// over. Normal for a moment after a restart or a handover.
	ScheduleNew Schedule = "NEW"
	// ScheduleOnTime: the next due time is ahead. The object is waiting.
	ScheduleOnTime Schedule = "ON_TIME"
	// ScheduleLate: past due by less than one period.
	ScheduleLate Schedule = "LATE"
	// ScheduleOverdue: past due by more than one period. A turn has been
	// missed.
	ScheduleOverdue Schedule = "OVERDUE"
	// SchedulePaused: the strategy's own effective time says not to run now.
	SchedulePaused Schedule = "PAUSED"
	// ScheduleStalled: a round began and has not ended past the budget for
	// ending one. The schedule alone cannot see this -- the bound stays where
	// the last returned round left it -- so it is its own value.
	ScheduleStalled Schedule = "STALLED"
	// ScheduleCooling: waiting on purpose, because the backend kept not
	// answering. Waiting by the schedule and waiting in cooldown are opposite
	// facts about a backend, so they are not one value.
	ScheduleCooling Schedule = "COOLING"
)

// Schedules lists every schedule value, for the page's completeness check.
var Schedules = []Schedule{ScheduleNew, ScheduleOnTime, ScheduleLate, ScheduleOverdue, SchedulePaused,
	ScheduleStalled, ScheduleCooling}

// scheduleOf reads the dimension off the anomaly at time at. Empty when the
// object carries no wake facts at all -- an older replica, or a build with no
// due index -- which the page renders as not known rather than as any value.
func scheduleOf(anomaly Anomaly, at time.Time) Schedule {
	switch {
	case anomaly.Stalled:
		return ScheduleStalled
	case anomaly.Kind == KindOverdueWake:
		return ScheduleOverdue
	case anomaly.Kind == KindQueryCooldown, anomaly.Wake != nil && anomaly.Wake.Cooling:
		return ScheduleCooling
	case anomaly.CauseReason == "EFFECTIVE_TIME_INACTIVE":
		return SchedulePaused
	case anomaly.Wake == nil:
		return ""
	case !anomaly.Wake.Known:
		return ScheduleNew
	case anomaly.Wake.DueAt.After(at):
		return ScheduleOnTime
	}
	late := at.Sub(anomaly.Wake.DueAt)
	if period := time.Duration(anomaly.Wake.IntervalSeconds) * time.Second; period > 0 && late > period {
		return ScheduleOverdue
	}
	return ScheduleLate
}
