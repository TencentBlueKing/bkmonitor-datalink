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

	model "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
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
	// other objects did since. Across replicas the count is exact when both
	// sides name every object they count (Samples covers them) -- the
	// merge then counts distinct identities -- and a sum otherwise, in which
	// an object that recovered on one replica, moved, and recovered on
	// another is in twice; Samples is distinct either way, so on a large
	// fold Objects can exceed the identities named and that is the sum, not
	// a truncated sample.
	Objects int `json:"objects"`
	// FirstFailure is the earliest onset among them, LastFailure the latest
	// round any of them was seen failing before it recovered.
	FirstFailure time.Time `json:"first_failure"`
	LastFailure  time.Time `json:"last_failure"`
	// FirstRecovery and LastRecovery bound when they recovered.
	FirstRecovery time.Time `json:"first_recovery"`
	LastRecovery  time.Time `json:"last_recovery"`
	// Samples names up to MaxRecoveredSample of the objects, most recent
	// recovery first, with the strategies each one serves. A fold within the
	// bound names every object it counts; a larger one names the most recent
	// few and Objects says how many there are. Without them a fold that
	// has ended is a count and a pair of clocks, and the question a reader
	// has of it -- which two, and what failed -- was answered from the logs
	// or not at all once the objects' own rows had let the failure go.
	Samples []RecoveredSample `json:"samples,omitempty"`
}

// RecoveredSample is one recovered object of a fold, by identity.
type RecoveredSample struct {
	QueryGroup  string        `json:"query_group"`
	Strategies  []StrategyRef `json:"strategies,omitempty"`
	RecoveredAt time.Time     `json:"recovered_at"`
}

// MaxRecoveredSample bounds the identities a recovered fold names: enough to
// name the two or three objects a fold usually holds, and a bound on the
// fold that holds two hundred.
const MaxRecoveredSample = 4

// recoveredFold is the tracker's own record of one fold: the objects by
// identity with each one's latest recovery, so an object that recovers twice
// within the hour is one object and one that recovered before the hour is
// none, whatever the fold's other objects did since.
type recoveredFold struct {
	problem RecoveredProblem
	objects map[string]time.Time
	// strategies is what each recovered object served when it recovered,
	// for the fold's samples.
	strategies map[string][]StrategyRef
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

// noteDefectPassed records that a round ran through the stage the object's
// own failure was in, under the DEFECT line and the failure's fold, and lets
// the failure go. Called with the failure still on the state, because the
// fold is read from it.
//
// The DEFECT line's evidence of recovery is that the failed stage passed --
// a state apply that conflicted and then applied, an event write the client
// refused and then sent -- not that the object completed healthily. The two
// used to be one criterion: the failure was kept until a healthy completion,
// and a healthy completion needs every series of the object to have data. On
// a live deployment one state-version conflict, a single key out of two
// thousand, kept an object under DEFECT as "recovering" for as long as its
// 186 sparse series stayed short, which is the data's question and not this
// deployment's; the object's own row already answered that one, on its own
// line, as somebody else's. "Is our fault fixed" and "has the data come
// back" are two hypotheses, and a criterion that cannot tell them apart
// answers neither.
//
// The row's own line is not touched here: the object stays wherever the
// column put it. Only the second fact, the internal failure, is closed.
func (tracker *Tracker) noteDefectPassed(queryGroup string, state *queryGroupState, at time.Time) {
	failure := state.internal
	if failure == nil {
		return
	}
	// Under the fold the row was listed on: its own DEFECT fold when the
	// column decided DEFECT for it, the second line's fold -- the failure's
	// code -- otherwise. ReportChecks files the second line by that code.
	row := tracker.rowOf(queryGroup, state)
	attribute(&row, at)
	check, key := CheckDefect, failure.Code
	if row.Finding.Check == CheckDefect {
		key = row.Finding.Group
	}
	since, lastFailure := row.Since, time.Time{}
	if failure.At != nil {
		lastFailure = *failure.At
		if since.IsZero() || failure.At.Before(since) {
			since = *failure.At
		}
	}
	tracker.recordRecoveryUnder(queryGroup, check, key, since, lastFailure, at, row.Strategies)
	state.internal = nil
}

// defectPassedByCompletion reports whether a round that completed as kind at
// slot is the failed stage passing for failure: a round that ran -- every
// kind but the two that never reached the pipeline, a Slot skipped as a gap
// and a Slot whose snapshot was unavailable -- on a Slot that proves it (see
// laterThanFailure), for a failure in the stages a run passes through. An
// output failure is not proved by any completion: the events are written
// after the round completes, and only a write that went through proves that
// stage.
func defectPassedByCompletion(failure *FailureRef, endedRound bool, kind string, slot int64) bool {
	if failure == nil || failure.Category == observability.QueryFailureCategoryOutput || kind == "" {
		return false
	}
	// The two kinds that never ran are the module's own list, not a copy
	// kept here: a third query-free kind would otherwise count as a run.
	for _, queryFree := range model.QueryFreeCompletionKinds {
		if kind == string(queryFree) {
			return false
		}
	}
	return laterThanFailure(failure, endedRound, slot)
}

// defectPassedByOutput reports whether a write of the round's events that
// went through at slot is the failed stage passing for failure: only for a
// failure of the output stage, on a Slot that proves it.
func defectPassedByOutput(failure *FailureRef, endedRound bool, slot int64) bool {
	return failure != nil && failure.Category == observability.QueryFailureCategoryOutput && laterThanFailure(failure, endedRound, slot)
}

// laterThanFailure is the Slot comparison behind both proofs: a later Slot
// than the failure's, or the failure's own Slot when the round the failure
// was seen on ended without completing -- the success is then the retry
// getting through the failed stage. A round that carried the failure as a
// fact and still completed is the same round completing, and proves
// nothing; that failure is proved past by the next Slot going through
// without it. A Slot nobody named (zero) is compared as any other, except
// that two unnamed Slots are not the same Slot retried.
func laterThanFailure(failure *FailureRef, endedRound bool, slot int64) bool {
	if slot > failure.Slot {
		return true
	}
	return endedRound && slot == failure.Slot && slot != 0
}

// recordRecovery files one object's recovery under the line and fold its
// row was on at that moment.
func (tracker *Tracker) recordRecovery(queryGroup string, row Anomaly, at time.Time) {
	attribute(&row, at)
	if row.Finding.Check == "" {
		return
	}
	lastFailure := row.ReasonLastAt
	if row.Blocked != nil && row.Blocked.At != nil && row.Blocked.At.After(lastFailure) {
		lastFailure = *row.Blocked.At
	}
	tracker.recordRecoveryUnder(queryGroup, row.Finding.Check, row.Finding.Group, row.Since, lastFailure, at, row.Strategies)
}

// recordRecoveryUnder files one object's recovery under a line and fold, with
// when the object's problem began and was last seen failing, and which
// strategies it served.
func (tracker *Tracker) recordRecoveryUnder(queryGroup string, check Check, key string, since, lastFailure, at time.Time, strategies []StrategyRef) {
	if tracker.recovered == nil {
		tracker.recovered = map[string]*recoveredFold{}
	}
	id := recoveredID(check, key)
	fold := tracker.recovered[id]
	if fold == nil {
		fold = &recoveredFold{problem: RecoveredProblem{Check: check, Key: key, FirstRecovery: at},
			objects: map[string]time.Time{}, strategies: map[string][]StrategyRef{}}
		tracker.recovered[id] = fold
	}
	fold.objects[queryGroup] = at
	// Kept from the last recovery that named them: a row that carries no
	// strategy names -- an object seen only through Query Group observations
	// -- does not erase the names an earlier recovery of the same object
	// recorded.
	if len(strategies) > 0 {
		fold.strategies[queryGroup] = append([]StrategyRef(nil), strategies...)
	}
	fold.problem.Objects = len(fold.objects)
	if !since.IsZero() && (fold.problem.FirstFailure.IsZero() || since.Before(fold.problem.FirstFailure)) {
		fold.problem.FirstFailure = since
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
				delete(fold.strategies, queryGroup)
			}
		}
		fold.problem.Objects = len(fold.objects)
		if fold.problem.Objects == 0 {
			delete(tracker.recovered, id)
			continue
		}
		problem := fold.problem
		problem.Samples = recoveredSamples(fold)
		problems = append(problems, problem)
	}
	sort.Slice(problems, func(i, j int) bool {
		if problems[i].Check != problems[j].Check {
			return problems[i].Check < problems[j].Check
		}
		return problems[i].Key < problems[j].Key
	})
	return problems
}

// recoveredSamples names the fold's most recently recovered objects, at
// most MaxRecoveredSample, most recent first and by identity on a tie so
// two reads of one fold name the same objects.
func recoveredSamples(fold *recoveredFold) []RecoveredSample {
	samples := make([]RecoveredSample, 0, len(fold.objects))
	for queryGroup, recoveredAt := range fold.objects {
		samples = append(samples, RecoveredSample{QueryGroup: queryGroup, Strategies: fold.strategies[queryGroup], RecoveredAt: recoveredAt})
	}
	sort.Slice(samples, func(i, j int) bool {
		if !samples[i].RecoveredAt.Equal(samples[j].RecoveredAt) {
			return samples[i].RecoveredAt.After(samples[j].RecoveredAt)
		}
		return samples[i].QueryGroup < samples[j].QueryGroup
	})
	if len(samples) > MaxRecoveredSample {
		samples = samples[:MaxRecoveredSample]
	}
	return samples
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
			// Exact when both sides name every object they count: the
			// identities are here now, so the reason the count was a sum --
			// no way to tell an object seen on two replicas from two objects
			// -- no longer holds for folds within the sample bound. A larger
			// fold still adds, and says so in the field's comment.
			fullyNamed := len(existing.Samples) == existing.Objects && len(problem.Samples) == problem.Objects
			existing.Objects += problem.Objects
			if fullyNamed {
				existing.Objects = distinctRecovered(existing.Samples, problem.Samples)
			}
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
			// The samples merge the way the count does: the replicas' most
			// recent, cut to the bound, an object seen on two replicas kept
			// once. The count stays a sum, as the page says it is.
			existing.Samples = mergeRecoveredSamples(existing.Samples, problem.Samples)
			merged = true
			break
		}
		if !merged {
			view.Recovered = append(view.Recovered, problem)
		}
	}
}

// mergeRecoveredSamples joins two replicas' samples of one fold: most recent
// first, one entry per object, at most MaxRecoveredSample.
func mergeRecoveredSamples(left, right []RecoveredSample) []RecoveredSample {
	all := append(append([]RecoveredSample(nil), left...), right...)
	// Ordered before the objects are made unique, so the entry kept for an
	// object seen on two replicas is its most recent recovery.
	sort.Slice(all, func(i, j int) bool {
		if !all[i].RecoveredAt.Equal(all[j].RecoveredAt) {
			return all[i].RecoveredAt.After(all[j].RecoveredAt)
		}
		return all[i].QueryGroup < all[j].QueryGroup
	})
	seen := map[string]bool{}
	merged := make([]RecoveredSample, 0, len(all))
	for _, sample := range all {
		if seen[sample.QueryGroup] {
			continue
		}
		seen[sample.QueryGroup] = true
		merged = append(merged, sample)
		if len(merged) == MaxRecoveredSample {
			break
		}
	}
	return merged
}

// distinctRecovered is how many different objects two fully named sample
// lists hold between them.
func distinctRecovered(left, right []RecoveredSample) int {
	seen := map[string]struct{}{}
	for _, sample := range left {
		seen[sample.QueryGroup] = struct{}{}
	}
	for _, sample := range right {
		seen[sample.QueryGroup] = struct{}{}
	}
	return len(seen)
}
