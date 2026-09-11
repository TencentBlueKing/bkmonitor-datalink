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
	"context"
	"sort"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Anomaly kinds produced by the tracker.
const (
	// KindDegradedRun is a query group whose recent rounds all finished in a
	// degraded state: it is running, but not producing the result it exists to
	// produce.
	KindDegradedRun = "DEGRADED_RUN"
	// KindBlockedRun is a query group whose recent rounds never reached
	// execution at all.
	KindBlockedRun = "BLOCKED_RUN"
	// KindOverdueWake is a query group whose wake time has passed with nothing
	// coming back for it. Unlike the other two it does not come from the
	// observation stream, because there is nothing to observe: the condition is
	// the absence of rounds, and the only thing that can report it is whatever
	// is holding the object's next wake time.
	KindOverdueWake = "OVERDUE_WAKE"
)

// ReasonWakeMissed is the reason code carried by an overdue object. The other
// kinds carry the pipeline's own completion or outcome string; this one has no
// round to take a string from, so it states the condition itself.
const ReasonWakeMissed = "WAKE_MISSED"

// Thresholds are counted in rounds, not in wall-clock time, because query group
// periods in the deployed population range from ten seconds to ten minutes. A
// fixed duration would call a slow group broken while it is merely slow, and
// would let a fast group fail dozens of times before saying anything.
//
// A round here is an attempt that produced an outcome, not a period. A blocked
// object spends most ticks in backoff, which produces no outcome and advances
// nothing, so two blocked rounds can span far more wall-clock time than two
// periods. That delays the report; it does not make it wrong.
const (
	// DefaultDegradedRounds is how many consecutive degraded completions make a
	// query group worth reporting.
	DefaultDegradedRounds = 3
	// DefaultBlockedRounds is how many consecutive blocked rounds make one
	// worth reporting. Blocked rounds produce nothing at all, so the bar is
	// lower than for degraded ones.
	DefaultBlockedRounds = 2
	// maxStrategiesPerQueryGroup bounds how many strategies one object records.
	// Query groups are keyed by query semantics, so several strategies can share
	// one; the bound keeps a pathological group from growing without limit.
	maxStrategiesPerQueryGroup = 32
	// DefaultTrackedQueryGroups bounds the per-replica table. A replica in the
	// deployed shadow owns a few hundred query groups; the bound is generous
	// enough that reaching it means something changed, not that the deployment
	// grew normally.
	DefaultTrackedQueryGroups = 16384
)

// healthyCompletion reports whether a completion kind means the query group did
// the job it exists for. FULL_EMPTY counts: no data is a correct business
// answer, while every other kind means the round produced something less than
// its own contract promises.
func healthyCompletion(kind string) bool {
	return kind == "FULL_COMPLETED" || kind == "FULL_EMPTY_COMPLETED"
}

// blockedOutcome reports whether a round produced nothing at all.
//
// ownership_rejected is deliberately absent: a replica never runs a query group
// it does not hold, so that outcome appears once when a lease is lost and the
// runner is torn down immediately after, and can never occur twice in a row.
// Listing it would be a branch that cannot fire.
func blockedOutcome(outcome string) bool {
	switch outcome {
	case "source_blocked", "source_error", "source_retry", "panic", "other_error":
		return true
	default:
		return false
	}
}

// failedExecution reports whether a round reached execution and did not finish.
// Without this, a query group whose every execution fails is invisible: it
// reports execute_returned, which is not blocked, and commits no progress, so
// it never produces a completion kind either.
func failedExecution(outcome string) bool {
	switch outcome {
	case "error", "retrying", "incomplete":
		return true
	default:
		return false
	}
}

type queryGroupState struct {
	strategies   map[StrategyRef]struct{}
	runStartedAt time.Time
	reasonCode   string
	degradedRuns int
	blockedRuns  int
	currentKind  string
	inAnomalyRun bool
	// determined records that at least one round said something conclusive
	// about this object. Until it does, the replica cannot report the object as
	// healthy: an empty anomaly list is what a freshly restarted tracker looks
	// like, and it is also what an object that never reports anything looks
	// like.
	determined    bool
	lastCompleted string
	// cause separates the conditions that share one completion kind, so the
	// object list can say which entries anyone can act on.
	cause       string
	lastFailure *FailureRef
}

// Tracker turns the observation stream into the anomaly list a replica
// publishes. It reads what the replica already emits rather than issuing its
// own reads, so producing the list costs no extra dependency traffic.
//
// Durations here are only as old as the uninterrupted run of observations that
// produced them: a process restart resets them. That is why every anomaly it
// produces is labelled as such, rather than presented as an absolute age.
type Tracker struct {
	next           observability.Observer
	replica        string
	degradedRounds int
	blockedRounds  int
	maxTracked     int
	now            func() time.Time

	mu     sync.Mutex
	groups map[string]*queryGroupState
}

// NewTracker wraps an observer. A nil next observer is allowed; the tracker is
// then purely a sink.
func NewTracker(next observability.Observer, replica string, now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	return &Tracker{
		next:           next,
		replica:        replica,
		degradedRounds: DefaultDegradedRounds,
		blockedRounds:  DefaultBlockedRounds,
		maxTracked:     DefaultTrackedQueryGroups,
		now:            now,
		groups:         make(map[string]*queryGroupState),
	}
}

// Observe records the round and forwards the observation untouched. Diagnostics
// must never change what the pipeline reports about itself.
func (tracker *Tracker) Observe(ctx context.Context, observation observability.Observation) {
	if tracker.next != nil {
		defer tracker.next.Observe(ctx, observation)
	}
	// The emitters do not all populate the trace: several rely on observers
	// merging the fields the context carries. Reading only the observation
	// would leave this tracker blind on exactly the paths that matter, and the
	// resulting anomaly list would be permanently empty.
	//
	// The fields are merged one at a time rather than swapped wholesale. The
	// observation that names the strategy does not name the query group, and
	// the context that names the query group does not name the strategy;
	// replacing one with the other loses whichever half it did not come from.
	trace := observation.Trace
	if trace.QueryGroupKey == "" || trace.StrategyID == "" {
		fromContext := observability.TraceFieldsFromContext(ctx)
		if trace.QueryGroupKey == "" {
			trace.QueryGroupKey = fromContext.QueryGroupKey
		}
		if trace.StrategyID == "" {
			trace.StrategyID = fromContext.StrategyID
			trace.BusinessID = fromContext.BusinessID
		}
	}
	queryGroup := trace.QueryGroupKey
	if queryGroup == "" {
		return
	}

	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	completion := observation.ProgressCompletionKind
	runOutcome := observation.RunOutcome
	executeOutcome := observation.ExecuteOutcome
	// Which strategies an object serves is a fact about the object, learned
	// from whichever observation happens to mention both. The observations that
	// carry an outcome never carry a strategy, so requiring an outcome here
	// would leave every anomaly without the one field an operator can act on.
	// A query failure carries no outcome of its own -- the round it belongs to
	// reports that separately -- but it is the only place the pipeline says why
	// the round went wrong, so it is let through to be remembered.
	failure := observation.QueryFailure
	if trace.StrategyID == "" && completion == "" && runOutcome == "" && executeOutcome == "" && failure == nil {
		return
	}

	state := tracker.groups[queryGroup]
	if state == nil {
		if len(tracker.groups) >= tracker.maxTracked {
			return
		}
		state = &queryGroupState{strategies: map[StrategyRef]struct{}{}}
		tracker.groups[queryGroup] = state
	}
	at := tracker.now()
	if failure != nil {
		// Remembered, not counted: this is context for an anomaly the outcome
		// paths decide on. Treating a failure as conclusive on its own would
		// make a retried transient look like a determined verdict.
		state.lastFailure = &FailureRef{Stage: failure.Stage, Category: failure.Category, Code: failure.Code}
	}
	if trace.StrategyID != "" && len(state.strategies) < maxStrategiesPerQueryGroup {
		state.strategies[StrategyRef{StrategyID: trace.StrategyID, BusinessID: trace.BusinessID}] = struct{}{}
	}

	switch {
	case completion != "":
		state.determined = true
		state.lastCompleted = completion
		if healthyCompletion(completion) {
			tracker.resetRun(state)
			return
		}
		state.degradedRuns++
		state.currentKind = KindDegradedRun
		state.reasonCode = completion
		state.cause = observation.ProgressCompletionCause
	case blockedOutcome(runOutcome):
		state.determined = true
		state.blockedRuns++
		state.currentKind = KindBlockedRun
		state.reasonCode = runOutcome
	case failedExecution(executeOutcome):
		state.determined = true
		state.degradedRuns++
		state.currentKind = KindDegradedRun
		state.reasonCode = executeOutcome
	default:
		// Rounds that neither completed nor were blocked -- not due, deferred,
		// still running, cancelled -- say nothing about whether the group is
		// healthy, so they neither start nor clear a run, and they leave the
		// object undetermined. "cancelled" in particular covers both an ordinary
		// shutdown and a short-period object that keeps blowing its completion
		// deadline; the second must not be reported as healthy just because this
		// classifier cannot tell it from the first.
		return
	}
	if !state.inAnomalyRun {
		state.inAnomalyRun = true
		state.runStartedAt = at
	}
}

func (tracker *Tracker) resetRun(state *queryGroupState) {
	// The cause described the run that just ended. Leaving it would let a
	// recovered object still explain itself with the last thing that went wrong.
	state.cause = ""
	state.degradedRuns = 0
	state.blockedRuns = 0
	state.inAnomalyRun = false
	state.currentKind = ""
	state.reasonCode = ""
	state.runStartedAt = time.Time{}
}

// Anomalies returns the query groups over threshold, ordered by how long their
// current run has lasted.
func (tracker *Tracker) Anomalies() []Anomaly {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	anomalies := make([]Anomaly, 0)
	for queryGroup, state := range tracker.groups {
		over := (state.currentKind == KindDegradedRun && state.degradedRuns >= tracker.degradedRounds) ||
			(state.currentKind == KindBlockedRun && state.blockedRuns >= tracker.blockedRounds)
		if !over {
			continue
		}
		anomaly := Anomaly{
			QueryGroup: queryGroup,
			Kind:       state.currentKind,
			ReasonCode: state.reasonCode, Cause: state.cause,
			Since:     state.runStartedAt,
			SinceFrom: SinceSnapshotContinuity,
			Replica:   tracker.replica,
			Failure:   state.lastFailure,
		}
		for strategy := range state.strategies {
			anomaly.Strategies = append(anomaly.Strategies, strategy)
		}
		sortStrategies(anomaly.Strategies)
		anomalies = append(anomalies, anomaly)
	}
	// Sorted here rather than only in the aggregate, because the publisher cuts
	// this list at the cap before anyone aggregates it. Ranging over a Go map is
	// randomised, so cutting an unsorted list would keep an arbitrary subset and
	// keep a different one on every tick -- the longest-running object, the one
	// worth reporting, would appear and disappear while nothing changed.
	sort.Slice(anomalies, func(left, right int) bool {
		if anomalies[left].Since.Equal(anomalies[right].Since) {
			return anomalies[left].QueryGroup < anomalies[right].QueryGroup
		}
		return anomalies[left].Since.Before(anomalies[right].Since)
	})
	return anomalies
}

// Determined reports how many tracked objects have said something conclusive
// about themselves at least once.
//
// The publisher compares it against what the replica owns, so that objects the
// tracker cannot speak for are counted as unknown rather than silently included
// in a healthy answer. That covers three otherwise invisible cases with one
// number: a tracker emptied by a restart, an object whose rounds are all
// inconclusive, and an object dropped because the table hit its bound.
func (tracker *Tracker) Determined() int {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	determined := 0
	for _, state := range tracker.groups {
		if state.determined {
			determined++
		}
	}
	return determined
}

// Forget drops query groups this replica no longer owns, so a handover does not
// leave their last known state behind to be republished forever.
func (tracker *Tracker) Forget(owned map[string]struct{}) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	for queryGroup := range tracker.groups {
		if _, kept := owned[queryGroup]; !kept {
			delete(tracker.groups, queryGroup)
		}
	}
}

// HasObserved reports whether a round in this process has already said
// something about the object, so a restore does not overwrite live evidence
// with a persisted cursor.
func (tracker *Tracker) HasObserved(queryGroup string) bool {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	_, seen := tracker.groups[queryGroup]
	return seen
}

// Tracked reports how many query groups the table holds, so the bound is
// observable rather than a number in a comment.
func (tracker *Tracker) Tracked() int {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return len(tracker.groups)
}

func sortStrategies(strategies []StrategyRef) {
	for outer := 1; outer < len(strategies); outer++ {
		for inner := outer; inner > 0; inner-- {
			left, right := strategies[inner-1], strategies[inner]
			if left.StrategyID < right.StrategyID ||
				(left.StrategyID == right.StrategyID && left.BusinessID <= right.BusinessID) {
				break
			}
			strategies[inner-1], strategies[inner] = right, left
		}
	}
}
