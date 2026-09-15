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
	KindDegradedRun   = "DEGRADED_RUN"
	KindQueryCooldown = "QUERY_COOLDOWN"
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
// periods in the deployed population range from ten seconds to one hour (read
// from the persisted schedules of one deployment: most groups run every
// minute, about a tenth of them faster, and a few every ten minutes to every
// hour). A fixed duration would call a slow group broken while it is merely
// slow, and would let a fast group fail dozens of times before saying
// anything.
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
// HealthyCompletions, BlockedOutcomes and FailedExecutions are the tracker's
// own vocabularies: the words it writes into an anomaly's reason code, which is
// what reaches the page.
//
// Declared rather than written inline in the switches they drive, because two
// other places have to agree with them -- the attribution table and the test
// that checks the table is complete -- and neither can see inside a switch. The
// twelve retired-strategy objects were being attributed by a fall-through for
// exactly this reason: the rule written for them names a contract code, and
// this is the vocabulary the field actually carries.
var (
	// HealthyCompletions end a round with a result.
	HealthyCompletions = []string{"FULL_COMPLETED", "FULL_EMPTY_COMPLETED"}
	// BlockedOutcomes are rounds that produced nothing at all.
	BlockedOutcomes = []string{"source_blocked", "source_error", "source_retry", "panic", "other_error"}
	// FailedExecutions are rounds that reached execution and did not finish.
	FailedExecutions = []string{"error", "retrying", "incomplete"}
)

func inVocabulary(value string, vocabulary []string) bool {
	for _, known := range vocabulary {
		if value == known {
			return true
		}
	}
	return false
}

func healthyCompletion(kind string) bool {
	return inVocabulary(kind, HealthyCompletions)
}

// blockedOutcome reports whether a round produced nothing at all.
//
// ownership_rejected is deliberately absent: a replica never runs a query group
// it does not hold, so that outcome appears once when a lease is lost and the
// runner is torn down immediately after, and can never occur twice in a row.
// Listing it would be a branch that cannot fire.
func blockedOutcome(outcome string) bool {
	return inVocabulary(outcome, BlockedOutcomes)
}

// failedExecution reports whether a round reached execution and did not finish.
// Without this, a query group whose every execution fails is invisible: it
// reports execute_returned, which is not blocked, and commits no progress, so
// it never produces a completion kind either.
func failedExecution(outcome string) bool {
	return inVocabulary(outcome, FailedExecutions)
}

type queryGroupState struct {
	// prunedSkip is the last span of Slots this object never had evaluated
	// because its cursor was moved past a pruned part of the timeline. It
	// outlives the rounds around it on purpose: the object recovers immediately
	// and every later round looks healthy, while the detection inside the span
	// never happened and cannot be made to happen.
	prunedSkip    *PrunedSkip
	queryCooldown *observability.QueryCooldownFacts
	// Once cooldown exposes a failure, keep that evidence visible until a real healthy completion.
	cooldownExposed bool
	strategies      map[StrategyRef]struct{}
	runStartedAt    time.Time
	// sinceFrom says what runStartedAt actually is. A run this process watched
	// begin has a start time; a run restored from a persisted cursor has only a
	// bound, and the two are indistinguishable once they are both a timestamp
	// in the same column.
	sinceFrom SinceSource
	// failingSince is when the current unbroken sequence of rounds that
	// reached execution and did not finish began. It is kept apart from
	// runStartedAt on purpose: that clock starts at the first degraded round
	// and runs for as long as the object stays anomalous in any way, so an
	// object that has completed degraded for hours already carries an old
	// start point, and judging "the rounds stopped finishing" against it made
	// a single retrying round flag the object as stalled and the next degraded
	// completion clear it again. The flag is meant to say the rounds stopped
	// ending and will not come back on their own; it needs its own clock,
	// started by the first round that did not finish and stopped by any round
	// that did, however it did.
	failingSince time.Time
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
	cause string
	// causeReason is the cause's own reason, which is where the answer to
	// "whose problem is this" actually lives.
	causeReason string
	// coverage is the evidence behind causeReason when that reason is about
	// the detection window. It is kept beside the reason and cleared with it,
	// because a shortfall left over from an earlier round would be read as
	// describing the reason currently on display.
	coverage *HistoryCoverage
	// shortRounds counts consecutive rounds whose windows were short. It is
	// the one part of the coverage a single observation cannot carry, and the
	// only part that separates a window that is filling from one that never
	// will.
	shortRounds uint32
	// emptyRounds counts consecutive rounds whose windows held nothing at all.
	// Separate from shortRounds because a window can be short for an hour and
	// empty only for the last two, and those last two are the ones that say
	// the data stopped rather than that the series churns.
	emptyRounds uint32
	// freshRounds counts consecutive rounds where every short window belonged
	// to a series this round had no history for. It is what separates a
	// strategy whose series identity churns from one whose long-lived series
	// are missing data -- the two conditions that drive shortRounds up for ever
	// and read identically at every other layer.
	freshRounds uint32
	// sawSomethingWrong records that at least one round of the current run went
	// wrong in a way that is not merely "recovery could not be decided".
	//
	// Judged over the whole run rather than off the latest round, because the
	// two give opposite answers and only one is honest. An object that failed
	// for an hour and then reported one warming round would, on the
	// latest-round reading, leave the anomaly column and be described as
	// normal, taking the hour with it.
	//
	// Stated in the negative deliberately. The positive form ("every round was
	// undecidable") has to be true for a state nothing has happened to yet,
	// which is not what a zero value gives, and every path that creates or
	// resets a state would have to remember to set it. This form starts
	// correct at zero and only ever rises.
	sawSomethingWrong bool
	lastFailure       *FailureRef
}

// undecidableReason is a completion reason that means the detection window
// could not decide recovery, rather than that anything went wrong.
//
// HISTORY_WARMING says the window does not hold the points the algorithm needs
// yet. That is not a failure of anything: the data that exists is being read
// correctly, the anomalous branch is settled before the completeness gate and
// still fires, and what cannot be settled is recovery -- because deciding
// "this has gone back to normal" requires a complete window and there is not
// one.
//
// Whether the window ever fills is a property of the strategy, not of this
// deployment. A series younger than its window fills it shortly. A series
// whose lifetime is shorter than the window never does, and that too is the
// strategy working as configured. Both are normal; both mean the same thing,
// which is that recovery has no basis to be decided on.
//
// HISTORY_GAPPED is deliberately not here. It also blocks the gate, but it
// says the data arrived, stopped, and came back -- a hole in a stream that was
// flowing, which is a question about the data rather than about how long the
// series lives. Folding it in would answer that question by assumption.
func undecidableReason(reason string) bool {
	return reason == "HISTORY_WARMING"
}

// byDesignReasons are rounds that ended without a business result because the
// configuration says so. Nobody acts on these: the configuration is already
// what somebody meant it to be.
//
// Every entry carries why it is here, because a list with a vague rule grows
// until the column means nothing. A reason not on this list stays in the
// anomaly column -- the direction that keeps something visible rather than
// the one that hides it.
var byDesignReasons = map[string]bool{
	// CONFIG_DRIFT was here and is not any more.
	//
	// It was filed as a passing event -- a strategy edited mid-round, the next
	// round runs under the new configuration, nobody acts. That story cannot be
	// true of anything in this column: an object reaches any column only after
	// DefaultDegradedRounds consecutive degraded rounds, so every object filed
	// here had been reporting CONFIG_DRIFT for at least three rounds in a row.
	// The column was structurally incapable of holding the one-round event its
	// own wording described.
	//
	// The predicate says the same thing. CONFIG_DRIFT is what the admitter
	// returns when IsPlanActive is false, and that is false in two unrelated
	// cases: the plan is not in the activation set at all -- an edit, a
	// deactivation, a reassignment -- or the plan is current and
	// Selected.StateApplyEpoch does not equal the epoch this round froze. Only
	// the first is a configuration change. The second is two views of the same
	// live plan failing to line up, and nothing about it clears on its own.
	//
	// A live read settled it: an object executing its slot thirty seconds after
	// that slot's time returned CONFIG_DRIFT, committed progress, moved to the
	// next slot, and did it again -- with query, schedule and snapshot revisions
	// identical across the runs, so no configuration had changed. Every one of
	// those slots was voided, and this page filed the object under "没有人需要
	// 做什么".
	//
	// That is the worst thing this page can do: an object that will never
	// evaluate again, shown as requiring nothing. It goes back on the to-do
	// list until the code can say which of the two cases it is -- the field
	// that separates them is computed in IsPlanActive and published nowhere.

	// The strategy is outside its own active window: its uptime schedule or
	// calendar says not to run now, and the round was suppressed for exactly
	// that reason.
	//
	// This is the configuration doing what it was written to do, and it is a
	// standing state rather than a passing one -- a strategy that only runs in
	// business hours is in it sixteen hours a day. A suppressed round is
	// completed as COMPLETED_WITH_UNAVAILABLE, the same kind a real failure
	// gets, so the completion alone cannot tell them apart; after three
	// consecutive rounds the object would be listed as something for somebody
	// to work through, every night, for ever.
	//
	// EFFECTIVE_TIME_UNKNOWN is deliberately not here. That one says the
	// schedule could not be resolved at all -- a timezone or calendar that did
	// not load -- so nobody can say whether the strategy should be running.
	// That is a fault, and it looks identical on the page unless the two are
	// kept apart.
	"EFFECTIVE_TIME_INACTIVE": true,
}

func byDesignReason(reason string) bool {
	return byDesignReasons[reason]
}

// noActionReason is every reason that has a column of its own because nobody
// acts on it. It exists so the run-level flag is written against the union
// rather than against whichever column happens to be tested first.
func noActionReason(reason string) bool {
	return undecidableReason(reason) || byDesignReason(reason)
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
	// Cumulative since this process started, never reset by anything the pool
	// does. They are a pair on purpose: the size of the pool alone cannot tell a
	// backend that is still down from an exit path that has stopped working, and
	// the second reads as the first on every panel that shows only occupancy.
	demotionEntries    int
	demotionExtensions int
	demotionExits      int
	lastDemotionExit   time.Time
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
	// A cursor moved past a pruned part of the timeline carries none of the
	// above: it is not a round, so it has no completion and no outcome. It was
	// therefore dropped here, which is why the most complete form of "detection
	// did not happen" -- a span of Slots that were never evaluated and never
	// will be -- was the one thing on this deployment with nothing on screen.
	cursorAdvance := observation.CursorAdvance
	if trace.StrategyID == "" && completion == "" && runOutcome == "" && executeOutcome == "" &&
		failure == nil && observation.QueryCooldown == nil && cursorAdvance == nil {
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
	// Recorded before anything else, because it is not a round and none of the
	// round bookkeeping below applies to it. The object is very likely running
	// normally now -- what happened is in its past and is permanent, which is
	// exactly why nothing that describes the current round can carry it.
	if cursorAdvance != nil {
		if cursorAdvance.Status == observability.CursorAdvanceApplied {
			state.prunedSkip = &PrunedSkip{
				From: cursorAdvance.From, To: cursorAdvance.To, At: at,
				DiscardedSlot: cursorAdvance.InFlightSlot,
			}
		}
		if completion == "" && runOutcome == "" && executeOutcome == "" && failure == nil &&
			observation.QueryCooldown == nil {
			return
		}
	}
	// Captured before this round is folded in: by the time the run-start block
	// runs, this round has already made the object determined, and the question
	// is whether anything came before it.
	seenBefore := state.determined
	if facts := observation.QueryCooldown; facts != nil {
		switch facts.Event {
		case "entered", "extended":
			copy := *facts
			if state.queryCooldown == nil {
				tracker.demotionEntries++
			} else {
				// Counted apart from entries because it is the only thing that
				// separates a pool that is still working from one that is stuck.
				// During a real outage nothing exits -- there is nothing to
				// recover to -- so exits alone would call a genuine outage a
				// broken mechanism. A pool being extended is being retried and
				// failing; a pool with deadlines in the past and no entries,
				// extensions or exits is not being touched at all.
				tracker.demotionExtensions++
			}
			state.queryCooldown = &copy
			state.cooldownExposed = true
		case "recovered", "config_changed", "disabled":
			// Counted only on the way out of the pool, not on every event that
			// could clear one. Demotion takes objects out of the health
			// denominator, so the number that matters is not how big the pool is
			// -- a pool that only fills reports a perfectly steady size once it
			// has swallowed everything it can. It is whether anything ever comes
			// back out. Zero exits beside a non-empty pool is the shape of an
			// exit path that has stopped working, and nothing else in the view
			// distinguishes that from a backend that is genuinely still down.
			if state.queryCooldown != nil {
				tracker.demotionExits++
				tracker.lastDemotionExit = at
			}
			state.queryCooldown = nil
		}
	}
	if failure != nil {
		// Remembered, not counted: this is context for an anomaly the outcome
		// paths decide on. Treating a failure as conclusive on its own would
		// make a retried transient look like a determined verdict.
		state.lastFailure = &FailureRef{Stage: failure.Stage, Category: failure.Category,
			Code: failure.Code, Detail: failure.Detail}
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
		// A degraded completion is still a round that ended and moved the
		// cursor, which is exactly what a stalled object cannot do.
		state.failingSince = time.Time{}
		// One round that was more than a no-action reason settles the whole
		// run, and no later round takes it back.
		//
		// "No action" is the union of every reason that has its own column,
		// not just the one checked first. Written against a single column it
		// would mark the run for the others -- so an object interrupted by a
		// config edit would be filed as a fault, which is the shape of bug
		// this flag exists to prevent, pointing the other way.
		if !noActionReason(observation.ProgressCompletionReason) {
			state.sawSomethingWrong = true
		}
		state.degradedRuns++
		state.currentKind = KindDegradedRun
		state.reasonCode = completion
		state.cause = observation.ProgressCompletionCause
		state.causeReason = observation.ProgressCompletionReason
		// Counted before the coverage is replaced, because the run length is
		// the only thing here that one round cannot supply. A round that
		// reports a complete window ends the run: a window that filled once
		// was filling, whatever it does next.
		if facts := observation.HistoryCoverage; facts == nil || facts.Short == 0 {
			state.shortRounds = 0
		} else {
			state.shortRounds++
		}
		if facts := observation.HistoryCoverage; facts == nil || facts.Empty == 0 {
			state.emptyRounds = 0
		} else {
			state.emptyRounds++
		}
		// Every short window belonged to a series with no loaded history, or
		// the run ends. "Every", not "any": one short window that did have
		// history is a round where churn is not the whole story, and this
		// counter is the one that sends a reader to edit a strategy.
		//
		// A round with nothing short also ends it, for the same reason it ends
		// shortRounds -- a window that filled once was filling.
		if facts := observation.HistoryCoverage; facts == nil || facts.Short == 0 ||
			facts.ShortFresh != facts.Short {
			state.freshRounds = 0
		} else {
			state.freshRounds++
		}
		state.coverage = nil
		if facts := observation.HistoryCoverage; facts != nil {
			state.coverage = &HistoryCoverage{
				Levels: facts.Levels, Short: facts.Short, Empty: facts.Empty,
				WorstValid: facts.WorstValid, WorstRequired: facts.WorstRequired,
				ShortRounds: state.shortRounds, EmptyRounds: state.emptyRounds,
				Guarded: facts.Guarded,
				Fresh:   facts.Fresh, ShortFresh: facts.ShortFresh, FreshRounds: state.freshRounds,
			}
		}
	case blockedOutcome(runOutcome):
		state.determined = true
		state.failingSince = time.Time{}
		state.blockedRuns++
		state.currentKind = KindBlockedRun
		state.reasonCode = runOutcome
		// A round that never reached the window is not a window declining to
		// decide. It is a round that did not happen.
		state.sawSomethingWrong = true
	case failedExecution(executeOutcome):
		state.determined = true
		if state.failingSince.IsZero() {
			state.failingSince = at
		}
		state.degradedRuns++
		state.currentKind = KindDegradedRun
		state.reasonCode = executeOutcome
		state.sawSomethingWrong = true
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
		// This process watched the run begin, so the timestamp is a start time
		// rather than a bound. A restored object overwrites neither, because
		// Restore leaves a determined object alone.
		state.sinceFrom = SinceSnapshotContinuity
		// Unless this object's very first conclusive round was already the bad
		// one. Then nothing was watched going wrong: the object was in this
		// state when the replica picked it up, and the clock is measuring how
		// long this process has been watching. That is the difference between
		// "wrong for 40 hours" and "wrong for at least 40 hours", and a
		// deployment showed 55 objects reporting the first when the number was
		// simply the age of the process.
		//
		// Tested on whether a conclusive round had been seen before this one,
		// not on how soon after startup it happened. A time window would also
		// catch an object that completed healthily and then genuinely failed
		// minutes after startup -- a transition this process did watch.
		if !seenBefore {
			state.sinceFrom = SinceProcessStart
		}
	}
}

func (tracker *Tracker) resetRun(state *queryGroupState) {
	// The cause described the run that just ended. Leaving it would let a
	// recovered object still explain itself with the last thing that went wrong.
	state.cooldownExposed = false
	state.cause = ""
	state.causeReason = ""
	state.coverage = nil
	state.shortRounds = 0
	state.emptyRounds = 0
	state.sawSomethingWrong = false
	state.degradedRuns = 0
	state.blockedRuns = 0
	state.inAnomalyRun = false
	state.currentKind = ""
	state.reasonCode = ""
	state.runStartedAt = time.Time{}
	state.sinceFrom = ""
	state.failingSince = time.Time{}
}

// Anomalies returns the query groups this deployment's own execution is failing
// on, ordered by how long their current run has lasted.
//
// Objects held in the demotion pool are not here. They are in Demoted, because
// their backend is the thing that is not answering and counting them as this
// deployment's anomalies makes a bigger pool read as a sicker deployment -- the
// exact inversion that makes the health number useless during a backend outage.
// Every object lands in exactly one column, which is what lets the counts add
// up against Determined; TestEveryColumnAccountsForEveryObject is what keeps
// that true rather than this sentence.
func (tracker *Tracker) Anomalies() []Anomaly {
	return tracker.listed(ColumnAnomalies)
}

// Undecidable returns the objects whose rounds end without deciding recovery,
// and where nothing else has gone wrong.
//
// These are not anomalies and are published apart from them. The detection
// window does not hold the points the algorithm needs, so recovery has nothing
// to be decided on -- but the data that exists is read correctly, an anomalous
// result is still settled and still fires, and neither capacity nor any change
// to this deployment alters any of it.
//
// How long the window stays that way is a property of the strategy. A series
// younger than its window fills it shortly; a series whose lifetime is shorter
// than its window never does. Both are the strategy working as configured, and
// the split between them rides on each row so a reader can see which.
//
// Counting them as anomalies made a normal, permanent condition read as a
// standing fault, and it was the largest single population in the list: every
// reader worked through the same rows and reached the same non-conclusion.
func (tracker *Tracker) Undecidable() []Anomaly {
	return tracker.listed(ColumnUndecidable)
}

// Transitional returns the objects whose round was interrupted by a change
// already being made on purpose.
//
// Not a fault and not a limitation: the next round runs under the new state.
// They are published apart from the anomalies because the anomaly column is
// meant to be the list somebody works through, and a round that was
// interrupted by an edit somebody already made is finished business.
func (tracker *Tracker) ByDesign() []Anomaly {
	return tracker.listed(ColumnByDesign)
}

// Demoted returns the query groups held back because their backend kept
// answering unavailable.
//
// It carries the same evidence an anomaly does -- reason, cause, last failure --
// because "this is not our fault" is a claim a reader has to be able to check.
// A pool whose members explain nothing is indistinguishable from a pool that
// swallowed a real failure.
func (tracker *Tracker) Demoted() []Anomaly {
	return tracker.listed(ColumnDemoted)
}

// DemotionFlow reports how many objects have entered and left the pool since
// this process started, and when the last one left.
//
// Occupancy alone cannot be read: a pool that only fills settles at a steady
// size, which looks exactly like a backend that is still down. Exits are the
// positive control -- if they stay at zero while the pool is not empty, the way
// out has stopped working and everything else on the page still says fine.
//
// Extensions are what keeps that control honest in the other direction. A real
// outage produces no exits either, because there is nothing to recover to, so
// exits alone would report every sustained outage as a broken mechanism. A pool
// being extended is being retried and failing; a pool whose deadlines have
// passed with no entries, extensions or exits is not being touched at all.
func (tracker *Tracker) DemotionFlow() (entries, extensions, exits int, lastExit time.Time) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return tracker.demotionEntries, tracker.demotionExtensions, tracker.demotionExits, tracker.lastDemotionExit
}

// columnOf decides which of the three lists an object belongs to. Exactly one,
// which is what lets Healthy be a subtraction rather than its own walk.
//
// The order is the precedence, and it is not arbitrary. The pool comes first:
// an object can be both over threshold and held back, and attributing it to
// this deployment while its backend is the thing not answering is the reading
// the demotion split exists to prevent. Undecidable comes next and only claims
// a run in which nothing else went wrong, so anything genuinely failing falls
// through to the anomaly column even while its latest round says warming.
func columnOf(state *queryGroupState) string {
	if state.queryCooldown != nil {
		return ColumnDemoted
	}
	// A starved window is not a window with nothing to decide on -- it is a
	// series producing nothing usable, which is a question for someone. It
	// reports the same reason as the normal case, so the reason alone cannot
	// keep it out of the column that says "nothing to do here".
	if !state.sawSomethingWrong && state.currentKind == KindDegradedRun &&
		undecidableReason(state.causeReason) && !state.coverage.Starved() {
		return ColumnUndecidable
	}
	// Same run-level guard as the column above, for the same reason: judged off
	// the latest round, a run that had been failing for an hour would leave the
	// anomaly column the moment somebody edited the strategy.
	if !state.sawSomethingWrong && state.currentKind == KindDegradedRun &&
		byDesignReason(state.causeReason) {
		return ColumnByDesign
	}
	return ColumnAnomalies
}

func (tracker *Tracker) listed(column string) []Anomaly {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	anomalies := make([]Anomaly, 0)
	for queryGroup, state := range tracker.groups {
		over := (state.currentKind == KindDegradedRun && state.degradedRuns >= tracker.degradedRounds) ||
			(state.currentKind == KindBlockedRun && state.blockedRuns >= tracker.blockedRounds)
		if !over && state.queryCooldown == nil && !(state.cooldownExposed && state.inAnomalyRun) {
			continue
		}
		if columnOf(state) != column {
			continue
		}
		anomaly := Anomaly{
			QueryGroup:    queryGroup,
			QueryCooldown: state.queryCooldown,
			Kind:          state.currentKind,
			ReasonCode:    state.reasonCode, Cause: state.cause, CauseReason: state.causeReason,
			Coverage:     state.coverage,
			Since:        state.runStartedAt,
			SinceFrom:    state.sinceFrom,
			FailingSince: state.failingSince,
			Replica:      tracker.replica,
			Failure:      state.lastFailure,
		}
		if anomaly.Kind == "" && state.queryCooldown != nil {
			anomaly.Kind = KindQueryCooldown
			if anomaly.Since.IsZero() {
				anomaly.Since = state.queryCooldown.LastQueryAt
				anomaly.SinceFrom = SinceSnapshotContinuity
			}
		}
		// Nothing can have started later than the moment it is being read, so a
		// start time in the future is a defect in whatever produced it, not a
		// long-running object. Refusing it here rather than letting the sort
		// absorb it is deliberate: ordered oldest-first, a future timestamp
		// sorts last, which is where a shortened list stops showing rows -- the
		// mis-stamped object disappears exactly when the list gets interesting.
		if now := tracker.now(); anomaly.Since.After(now) {
			anomaly.Since = now
			anomaly.SinceFrom = SinceRefusedFuture
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

// HasConclusion reports whether the object already has conclusive evidence.
// Merely seeing a not-due round or strategy metadata must not prevent restore.
func (tracker *Tracker) HasConclusion(queryGroup string) bool {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	state := tracker.groups[queryGroup]
	return state != nil && state.determined
}

// StrategiesFor names the strategies this process has seen behind an object.
//
// It exists for anomalies that do not come from a round -- an overdue object
// produced no trace this table could learn from, and without this it would
// reach the list as a bare hash while every neighbouring row carries a strategy
// somebody recognises. Empty is the honest answer for an object this replica
// has never evaluated, which is exactly the object most likely to be overdue.
func (tracker *Tracker) StrategiesFor(queryGroup string) []StrategyRef {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	state, seen := tracker.groups[queryGroup]
	if !seen {
		return nil
	}
	strategies := make([]StrategyRef, 0, len(state.strategies))
	for strategy := range state.strategies {
		strategies = append(strategies, strategy)
	}
	sortStrategies(strategies)
	return strategies
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

// PrunedSkips is every object this replica has seen lose a span of Slots to a
// pruned timeline, keyed by Query Group.
//
// Kept for the life of the process rather than cleared when the object next
// runs. Clearing on a healthy round would remove it immediately -- the object
// resumes at once, which is the whole difficulty: every signal about the
// current round correctly says it is fine, and the span it lost stays lost.
func (tracker *Tracker) PrunedSkips() map[string]PrunedSkip {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	var skips map[string]PrunedSkip
	for queryGroup, state := range tracker.groups {
		if state.prunedSkip == nil {
			continue
		}
		if skips == nil {
			skips = make(map[string]PrunedSkip, 4)
		}
		skips[queryGroup] = *state.prunedSkip
	}
	return skips
}
