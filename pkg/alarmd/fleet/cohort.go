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

// The join from a cohort to its objects.
//
// Two lines of investigation spent two days on four fifteen-second objects
// whose rows had said the whole time what was wrong with them -- a query
// target the backend did not have, a cooldown extended round after round --
// because the symptom was met somewhere else: a lag histogram's 15 s series
// and a skip rate, both counted per period, neither with a way to the objects
// they were counted over. The rows were there; the link from the number to
// the rows was not. This file is that link: every period the deployment
// evaluates on, with its population from the due indexes and what the listed
// rows say about it, and a list filter that opens exactly those rows.

// CohortView is one evaluation period across the deployment.
type CohortView struct {
	// IntervalSeconds is the period; 0 is the objects whose period the due
	// index does not know.
	IntervalSeconds int64 `json:"interval_seconds"`
	// Objects, Cooling and Overdue are the due indexes' count of every owned
	// object with this period -- healthy ones included -- summed over the
	// replicas that published a census. Zero on a deployment whose replicas
	// publish no cohorts (an older build): the listed counts below then stand
	// alone, and ListedOnly says so.
	Objects int `json:"objects"`
	Cooling int `json:"cooling"`
	Overdue int `json:"overdue"`
	// Listed is how many rows across every column carry this period, and the
	// three below partition what those rows say: rows whose round was given
	// up past the replay bound, rows sitting in a query cooldown, and the
	// first-screen line each row is under.
	Listed     int           `json:"listed"`
	GapSkipped int           `json:"gap_skipped"`
	InCooldown int           `json:"in_cooldown"`
	ByCheck    map[Check]int `json:"by_check,omitempty"`
	ByOwner    map[Owner]int `json:"by_owner,omitempty"`
	// ListedOnly is true when no replica published a cohort census, so the
	// population is unknown and Objects is not zero objects but no count.
	ListedOnly bool `json:"listed_only,omitempty"`
}

// CoolingFacts is the first-screen line's arithmetic for objects waiting in a
// query cooldown: the backend kept not answering, the object is deliberately
// not being asked, and whose line it is under. A deployment with hundreds of
// such objects read HEALTHY for two days with the number on no screen.
type CoolingFacts struct {
	// Objects is the due indexes' count of owned objects in cooldown, healthy
	// or not, over the replicas that published a census. Listed is how many
	// of the rows carry a cooldown record; Extended how many of those have
	// had their cooldown extended at least once -- a backend that keeps
	// failing rather than one that failed once.
	Objects  int `json:"objects"`
	Listed   int `json:"listed"`
	Extended int `json:"extended"`
	// ByOwner and ByCheck say whose problem the listed ones are.
	ByOwner map[Owner]int `json:"by_owner,omitempty"`
	ByCheck map[Check]int `json:"by_check,omitempty"`
	// OldestSinceSeconds is how long the longest-cooling listed row has been
	// under its reason, so "sustained" is a number and not a word.
	OldestSinceSeconds float64 `json:"oldest_since_seconds,omitempty"`
}

// intervalOf is the period a listed row is known to have: the due index's
// word first, then the skip record's, else 0.
func intervalOf(anomaly Anomaly) int64 {
	if anomaly.Wake != nil && anomaly.Wake.Known && anomaly.Wake.IntervalSeconds > 0 {
		return anomaly.Wake.IntervalSeconds
	}
	if anomaly.Skip != nil && anomaly.Skip.IntervalSeconds > 0 {
		return anomaly.Skip.IntervalSeconds
	}
	return 0
}

// rowInCooldown is whether a listed row is waiting in a query cooldown: the
// row's own record, or the due index saying so.
func rowInCooldown(anomaly Anomaly) bool {
	return anomaly.QueryCooldown != nil || (anomaly.Wake != nil && anomaly.Wake.Cooling)
}

// rowGapSkipped is whether a listed row's last round was given up past the
// replay bound: the reason word, or a skip record on the row.
func rowGapSkipped(anomaly Anomaly) bool {
	return anomaly.ReasonCode == "GAP_SKIPPED" || anomaly.Skip != nil
}

// Cohorts joins the due indexes' per-period census on the view with the rows
// of every column, one entry per period seen on either side, ordered by
// period. Rows are counted once each: a column is a partition of the objects.
func Cohorts(view *View, columns [][]Anomaly) []CohortView {
	byInterval := map[int64]*CohortView{}
	cohortOf := func(interval int64) *CohortView {
		cohort, ok := byInterval[interval]
		if !ok {
			cohort = &CohortView{IntervalSeconds: interval}
			byInterval[interval] = cohort
		}
		return cohort
	}
	published := view.Schedule != nil && len(view.Schedule.Cohorts) > 0
	if published {
		for _, census := range view.Schedule.Cohorts {
			cohort := cohortOf(census.IntervalSeconds)
			cohort.Objects, cohort.Cooling, cohort.Overdue = census.Objects, census.Cooling, census.Overdue
		}
	}
	for _, rows := range columns {
		for _, anomaly := range rows {
			cohort := cohortOf(intervalOf(anomaly))
			cohort.Listed++
			if rowGapSkipped(anomaly) {
				cohort.GapSkipped++
			}
			if rowInCooldown(anomaly) {
				cohort.InCooldown++
			}
			if anomaly.Finding.Check != "" {
				if cohort.ByCheck == nil {
					cohort.ByCheck = map[Check]int{}
				}
				cohort.ByCheck[anomaly.Finding.Check]++
			}
			if cohort.ByOwner == nil {
				cohort.ByOwner = map[Owner]int{}
			}
			cohort.ByOwner[anomaly.Finding.Owner]++
		}
	}
	cohorts := make([]CohortView, 0, len(byInterval))
	for _, cohort := range byInterval {
		cohort.ListedOnly = !published
		cohorts = append(cohorts, *cohort)
	}
	sort.Slice(cohorts, func(i, j int) bool { return cohorts[i].IntervalSeconds < cohorts[j].IntervalSeconds })
	return cohorts
}

// Cooling is the first-screen arithmetic for objects in a query cooldown, from
// the same rows and census as Cohorts.
func Cooling(view *View, columns [][]Anomaly, now time.Time) CoolingFacts {
	facts := CoolingFacts{}
	if view.Schedule != nil {
		facts.Objects = view.Schedule.Cooling
	}
	for _, rows := range columns {
		for _, anomaly := range rows {
			if !rowInCooldown(anomaly) {
				continue
			}
			facts.Listed++
			if anomaly.QueryCooldown != nil && anomaly.QueryCooldown.Event == "extended" {
				facts.Extended++
			}
			if facts.ByOwner == nil {
				facts.ByOwner, facts.ByCheck = map[Owner]int{}, map[Check]int{}
			}
			facts.ByOwner[anomaly.Finding.Owner]++
			if anomaly.Finding.Check != "" {
				facts.ByCheck[anomaly.Finding.Check]++
			}
			since := anomaly.ReasonSince
			if since.IsZero() {
				since = anomaly.Since
			}
			if !since.IsZero() && now.After(since) {
				if age := now.Sub(since).Seconds(); age > facts.OldestSinceSeconds {
					facts.OldestSinceSeconds = age
				}
			}
		}
	}
	return facts
}

// filterByInterval keeps the rows whose known period is the one asked for.
// 0 asks for the rows whose period is not known.
func filterByInterval(anomalies []Anomaly, interval int64) []Anomaly {
	kept := make([]Anomaly, 0, len(anomalies))
	for _, anomaly := range anomalies {
		if intervalOf(anomaly) == interval {
			kept = append(kept, anomaly)
		}
	}
	return kept
}
