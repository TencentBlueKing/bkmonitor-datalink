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

// RecoveredRetention is how long a problem is remembered after its last
// object recovered. A problem whose objects all completed healthily leaves
// the current lines the moment they do, and a page that only shows the
// current lines then shows nothing where the problem was: the reader who
// saw "结果提交 · Redis · 不可用：86 个，仍然受阻" ten minutes ago cannot
// tell "fixed" from "the page lost it". An hour is the same horizon the
// retained skip records are counted against.
const RecoveredRetention = time.Hour

// RecoveredProblem is one fold of a line whose objects recovered: how many
// objects completed healthily after being listed under it, when the fold
// began failing and was last seen failing, and when its objects recovered.
// It is the producer of the RECOVERED reading -- the positive evidence
// recovery needs, kept from the moment the evidence arrived rather than
// inferred from a fold's absence, which a restart or a change of owner also
// produces.
type RecoveredProblem struct {
	Check Check  `json:"check"`
	Key   string `json:"key"`
	// Objects is the distinct objects this replica saw recover from the fold
	// within the retention, each counted from its own recovery: an object
	// that recovered seventy minutes ago is not in it, whatever the fold's
	// other objects did since. Across replicas the counts add, and an object
	// that recovered on one replica, moved, and recovered on another is in
	// both -- the snapshot carries no object identities to tell, and the
	// page says the sum is a sum.
	Objects int `json:"objects"`
	// FirstFailure is the earliest onset among them, LastFailure the latest
	// round any of them was seen failing before it recovered.
	FirstFailure time.Time `json:"first_failure"`
	LastFailure  time.Time `json:"last_failure"`
	// FirstRecovery and LastRecovery bound when they recovered.
	FirstRecovery time.Time `json:"first_recovery"`
	LastRecovery  time.Time `json:"last_recovery"`
}

// recoveredFold is the tracker's own record of one fold: the objects by
// identity with each one's latest recovery, so an object that recovers twice
// within the hour is one object and one that recovered before the hour is
// none, whatever the fold's other objects did since.
type recoveredFold struct {
	problem RecoveredProblem
	objects map[string]time.Time
}

func recoveredID(check Check, key string) string { return string(check) + "\x00" + key }

// noteRecovery records the healthy completion that ends a listed object's
// run, under the line and fold the object was on at that moment. Called
// before the run is reset, because the fold is read from the row as it was.
// Only the anomaly column is a problem that recovers: a pool object leaves
// by a cooldown event, and the undecidable and by-design columns describe
// conditions, not failures.
func (tracker *Tracker) noteRecovery(queryGroup string, state *queryGroupState, at time.Time) {
	if !tracker.over(state) || columnOf(state) != ColumnAnomalies {
		return
	}
	tracker.recordRecovery(queryGroup, tracker.rowOf(queryGroup, state), at)
}

// noteMemoryRecovery records the write that ended an object's refused
// absence memory, under the line the refusal was listed on. Called before
// the refusal is cleared, because the row is read from it.
func (tracker *Tracker) noteMemoryRecovery(queryGroup string, state *queryGroupState, at time.Time) {
	tracker.recordRecovery(queryGroup, tracker.memoryRowOf(queryGroup, state), at)
}

// recordRecovery files one object's recovery under the line and fold its
// row was on at that moment.
func (tracker *Tracker) recordRecovery(queryGroup string, row Anomaly, at time.Time) {
	attribute(&row, at)
	if row.Finding.Check == "" {
		return
	}
	if tracker.recovered == nil {
		tracker.recovered = map[string]*recoveredFold{}
	}
	id := recoveredID(row.Finding.Check, row.Finding.Group)
	fold := tracker.recovered[id]
	if fold == nil {
		fold = &recoveredFold{problem: RecoveredProblem{Check: row.Finding.Check, Key: row.Finding.Group, FirstRecovery: at}, objects: map[string]time.Time{}}
		tracker.recovered[id] = fold
	}
	fold.objects[queryGroup] = at
	fold.problem.Objects = len(fold.objects)
	if !row.Since.IsZero() && (fold.problem.FirstFailure.IsZero() || row.Since.Before(fold.problem.FirstFailure)) {
		fold.problem.FirstFailure = row.Since
	}
	lastFailure := row.ReasonLastAt
	if row.Blocked != nil && row.Blocked.At != nil && row.Blocked.At.After(lastFailure) {
		lastFailure = *row.Blocked.At
	}
	if lastFailure.After(fold.problem.LastFailure) {
		fold.problem.LastFailure = lastFailure
	}
	if fold.problem.FirstRecovery.IsZero() || at.Before(fold.problem.FirstRecovery) {
		fold.problem.FirstRecovery = at
	}
	if at.After(fold.problem.LastRecovery) {
		fold.problem.LastRecovery = at
	}
}

// Recovered returns the problems whose objects recovered within the
// retention, and forgets the rest: each object drops out of its fold's
// count an hour after its own recovery, and a fold with no object left is
// gone. Ordered by check and key so a publisher that cuts the list keeps a
// deterministic prefix.
func (tracker *Tracker) Recovered() []RecoveredProblem {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	now := tracker.now()
	problems := make([]RecoveredProblem, 0, len(tracker.recovered))
	for id, fold := range tracker.recovered {
		for queryGroup, recoveredAt := range fold.objects {
			if now.Sub(recoveredAt) > RecoveredRetention {
				delete(fold.objects, queryGroup)
			}
		}
		fold.problem.Objects = len(fold.objects)
		if fold.problem.Objects == 0 {
			delete(tracker.recovered, id)
			continue
		}
		problems = append(problems, fold.problem)
	}
	sort.Slice(problems, func(i, j int) bool {
		if problems[i].Check != problems[j].Check {
			return problems[i].Check < problems[j].Check
		}
		return problems[i].Key < problems[j].Key
	})
	return problems
}

// mergeRecovered folds one replica's recovered problems into the view's,
// by check and key: objects add up -- each replica counts the objects it
// saw recover, and the snapshot carries no identities to tell an object
// that recovered on two replicas from two objects, so the sum is what the
// page has and what it says it has -- and the clocks take the earliest
// onset and the latest of everything else.
func mergeRecovered(view *View, problems []RecoveredProblem) {
	for _, problem := range problems {
		merged := false
		for index := range view.Recovered {
			existing := &view.Recovered[index]
			if existing.Check != problem.Check || existing.Key != problem.Key {
				continue
			}
			existing.Objects += problem.Objects
			if !problem.FirstFailure.IsZero() && (existing.FirstFailure.IsZero() || problem.FirstFailure.Before(existing.FirstFailure)) {
				existing.FirstFailure = problem.FirstFailure
			}
			if problem.LastFailure.After(existing.LastFailure) {
				existing.LastFailure = problem.LastFailure
			}
			if !problem.FirstRecovery.IsZero() && (existing.FirstRecovery.IsZero() || problem.FirstRecovery.Before(existing.FirstRecovery)) {
				existing.FirstRecovery = problem.FirstRecovery
			}
			if problem.LastRecovery.After(existing.LastRecovery) {
				existing.LastRecovery = problem.LastRecovery
			}
			merged = true
			break
		}
		if !merged {
			view.Recovered = append(view.Recovered, problem)
		}
	}
}
