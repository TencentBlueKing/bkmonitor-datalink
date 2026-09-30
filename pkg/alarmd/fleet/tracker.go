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
	"fmt"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	model "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
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
	// KindSkippedSpan is a retained record of Slots this deployment never
	// evaluated -- skipped past the replay window, or lost to a pruned
	// timeline. It is not a round: the object is usually running normally now.
	// It exists as a kind so the record can be listed under its check like any
	// other row, rather than only counted.
	KindSkippedSpan = "SKIPPED_SPAN"
	// KindNoData is an object whose rounds complete and whose query has
	// returned no records for a run of rounds after having returned some. It
	// is not a failure -- the round ran, the backend answered -- and it is not
	// in any column of the health equation; it is the data having stopped, and
	// it is listed under the data side's line for as long as it holds.
	KindNoData = "NO_DATA"
	// KindNoDataMemoryRefused is an object one of whose Plans the store will
	// not take an absence memory for. Not a failure of the round -- it judged
	// and its results went out -- and in no column; listed under its own line
	// because what it loses is silent: the memory stays at the last version
	// that was written, a group that goes absent after that is never recorded
	// as first absent, and its no-data alert never fires.
	KindNoDataMemoryRefused = "NO_DATA_MEMORY_REFUSED"
	// KindRetainedShareApproaching is an object whose latest completed Slot
	// held at least RetainedShareApproachPercent of the retained pool's
	// one-object share. Its rounds complete and its results stand; the row is
	// the warning before the refusal, which stops the strategy whole.
	KindRetainedShareApproaching = "RETAINED_SHARE_APPROACHING"
	// KindEmptyEveryRound is an object whose every round in this process has
	// completed with no records, for at least EmptyEveryRoundAfter, and that
	// this process has never seen return any. It is the other half of
	// KindNoData: data that stopped is the data side's, data that never came
	// is usually the strategy's -- a query over a source that has nothing
	// there, or an aggregation period shorter than the source reports at, so
	// that every window is empty however the data flows. Five such strategies
	// read HEALTHY on the page for a day, because FULL_EMPTY is a healthy
	// completion and the line for no-data waits for data to have been seen.
	KindEmptyEveryRound = "EMPTY_EVERY_ROUND"
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
	// DefaultEmptyEveryRoundAfter is how long an object must have completed
	// every round empty, never having returned records in this process,
	// before it is listed. A duration and not a round count, against the
	// rule above: the objects this exists for run every fifteen seconds, and
	// three empty rounds of those is forty-five seconds -- a source that
	// merely reports each minute would be listed on the first pass. An hour
	// is long enough for any source in the deployed population to have
	// spoken at least once if it speaks at all, and short enough that the
	// line is on the page the same day the strategy is created.
	DefaultEmptyEveryRoundAfter = time.Hour
	// emptyRunStrideStall is how many of an object's own strides a gap between
	// two empty rounds has to cover before it is read as a hole in the
	// evidence rather than the object's ordinary pace. Three: two consecutive
	// rounds missing is a blip on any period, and an object that has produced
	// nothing for three of its own cycles has stopped being watched in the
	// sense this line cares about, whether its cycle is fifteen seconds or two
	// hours.
	emptyRunStrideStall = 3
	// DefaultNoDataAfter is how long, on the source's clock, an object that
	// did return records must have gone without them -- latest empty Slot
	// minus the Slot records were last seen at -- before its empty rounds
	// are read as data that stopped. The same hour as the line above, and
	// for the same reason: a source that reports every ten minutes over a
	// sixty-second object completes nine empty rounds between two with
	// records, and a round count alone listed every such source as stopped
	// within three minutes of each report. The round count still applies
	// on top -- one hour of Slots in three rounds is a slow object, not a
	// stopped one -- so the two lines share one clock and one threshold and
	// differ only in whether records were ever seen.
	DefaultNoDataAfter = time.Hour
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
	// TerminalCompletions end a round with a deterministic refusal on at
	// least one of its series: the same round run again gives the same
	// refusal, so one is as much evidence as three. Listed on the first,
	// where a degraded completion waits for DefaultDegradedRounds -- an
	// hourly strategy whose state the store refused on every round from a
	// release onward took three hours to reach the page, and the third hour
	// was bought with nothing.
	TerminalCompletions = []string{"COMPLETED_WITH_TERMINAL"}
	// BlockedOutcomes are rounds that produced nothing at all. A round the
	// Worker's executable view did not allow (decision-016 batch 4b) is one
	// of them: the object stops being checked until the view and the
	// records agree, and the row has to say so or a Query Group refused on
	// every round would have no row at all.
	BlockedOutcomes = []string{"source_blocked", "source_error", "source_retry", "view_not_executable", "panic", "other_error"}
	// FailedExecutions are rounds that reached execution and did not finish.
	FailedExecutions = []string{"error", "retrying", "incomplete"}
)

// gapGuardState is one held scope as the tracker keeps it.
type gapGuardState struct {
	guard GapGuard
	gen   int
}

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
	prunedSkip *PrunedSkip
	// gapSkip is the last run of Slots this object skipped because they fell
	// past the replay window -- this deployment giving up on work it could not
	// catch up. Retained for the same reason prunedSkip is: the object recovers
	// at the next round and every later round reads healthy, while the Slots
	// in the span were never evaluated and never will be.
	gapSkip *SkippedSpan
	// lastEvidence is what the latest completion said about an earlier
	// attempt at its Slot, for the row; nil when it carried none.
	lastEvidence *ExecutionEvidence
	// emptyRuns counts consecutive rounds whose query returned no records at
	// all, emptySince when that run began, and sawData whether any round in
	// this process ever returned records. Data that stopped is a different
	// fact from data that never came: the first is the data side's, the second
	// is usually a strategy over a source that only speaks when something
	// happens, and only the first is listed.
	emptyRuns  int
	emptySince time.Time
	sawData    bool
	// emptySinceSlot and lastEmptySlot bound the run of empty completions on
	// the source's own clock: the Slot of the first empty round of the run and
	// of the latest. The "every round" line gates on their distance, Slot to
	// Slot, and on nothing else: the object's question is whether the source
	// has been silent for an hour of its time, not how long this process has
	// watched -- and a gate that mixed the two clocks would list an object two
	// hours behind and catching up after its first restored empty round, the
	// one case it most needs to get right. Only records end this run; a gap
	// or a failed round between two empty ones is not evidence of records.
	// emptySinceFrom and emptySlotFrom say who dated each start -- this
	// process watching, or the record the object was restored from -- one for
	// the consecutive run above and one for the Slot run, because a failed
	// round restarts the first and not the second.
	emptySinceSlot int64
	lastEmptySlot  int64
	emptySinceFrom SinceSource
	emptySlotFrom  SinceSource
	// emptyStride is the longest Slot distance between two empty rounds this
	// run has accepted: the object's own cadence, learned from its own rounds
	// rather than declared anywhere. A hole in the evidence is measured
	// against it, because a stretch that is long by the clock is not long for
	// an object whose next round was never due until then. It rises and does
	// not fall inside a run, so that a round arriving far sooner than the
	// pace cannot make the next ordinary one look like a hole; a hole clears
	// it along with the run's start, because a hole is not a pace.
	emptyStride int64
	// lastDataSlot is the Slot records were last seen at, on the source's
	// clock: the other end of the data side's hour. Zero until a round with
	// records is watched or restored; sawData without it is a round that
	// carried no Slot, which no emitter produces, and the data side's line
	// then waits for one that does rather than measuring from nothing.
	lastDataSlot int64
	// noDataMemory is the refused absence-memory write of each of this
	// object's Plans still refused, by Plan, with how often and since when.
	// A Plan's entry is kept until a write for that Plan is seen to store;
	// the object is listed while any entry remains, and recovers when the
	// last one goes -- one Plan's write storing says nothing about another's.
	noDataMemory map[StrategyRef]*NoDataMemoryRefusal
	// shareUsage is the latest completed Slot's retained bytes against the
	// one-object share of the pool it was admitted under, nil until a Slot
	// has completed with both.
	shareUsage *RetainedShareFacts
	// upkeep is the last this process saw of the store keeping this object's
	// memories alive, by Plan: the last read's stored shape and the last
	// renewal that reached the store. Positive evidence, kept apart from the
	// refusals above; the row carries the Plan attempted most recently.
	upkeep map[StrategyRef]*NoDataMemoryUpkeep
	// noDataTracking is what the last deciding no-data round of each of this
	// object's Plans counted, by Plan. Replaced whole on every deciding
	// round; a Plan that stops deciding keeps its last word, dated.
	noDataTracking map[StrategyRef]*NoDataTracking
	// wireFormats is the wire format each of this object's Plans last said
	// its events go out as, by Plan, from the Plan's evaluation line.
	wireFormats map[StrategyRef]*PlanWireFormat
	// planSeries is how many PRIMARY series the latest evaluated round bound
	// to each of this object's Plans, by Plan, from the Plan's evaluation
	// lines.
	planSeries map[StrategyRef]*PlanSeriesMatched
	// rounds is the object's recent completions, oldest first, at most
	// RecentRoundsKept of them: what each hole on a window is read against
	// to say whose minute it is. slotOffset is the object's distance from a
	// Slot to the record minute it evaluates, from the latest round that
	// reported one, so a round that carried no record can still be matched
	// to the minute a hole names; slotOffsetKnown says one has been seen.
	rounds          []roundMark
	slotOffset      int64
	slotOffsetKnown bool
	// worstWindow is the key of the window the worst pair belonged to on
	// the last round, so the round-over-round counters know when the pair
	// moved to another window.
	worstWindow string
	// guards is the held gap scopes reported for this object, by Plan and
	// scope, with the completion generation each was last reported in. A
	// completion prunes the scopes the round did not report -- a released
	// guard is silent -- and advances the generation.
	guards   map[string]*gapGuardState
	guardGen int
	// firstSeenAt is when this process first saw a round of the object: the
	// anchor a restart's catch-up is judged from on the skip record, since a
	// replica starts working an object well after its process starts.
	firstSeenAt time.Time
	// reasonKey names the current result and reason as one string, reasonSince
	// is when that pair first held and reasonRuns how many consecutive rounds
	// it has held for. It is the object's own clock for "how long has it been
	// saying this", and it is not runStartedAt: an object can have been
	// anomalous for hours and saying its current reason for a minute.
	// Kubernetes' lastTransitionTime does not reset when only the reason
	// changes; this one does, on purpose, because the reason is what the
	// reader acts on.
	reasonKey   string
	reasonSince time.Time
	reasonRuns  int
	// reasonLastAt is the latest round that said the current reason: the
	// other end of the reason's clock. reasonSince says when it started
	// and cannot say whether it is still happening; a group's "still
	// blocked" is read from this end.
	reasonLastAt  time.Time
	queryCooldown *observability.QueryCooldownFacts
	// demotedSince is when the object entered the pool: the first entered or
	// extended event with no cooldown standing. Zero outside the pool. It is
	// the bound a retained record is read against to decide whether the
	// skip was the cooldown's doing; the anomaly's onset is earlier and was
	// an approximation on the refusal's side.
	demotedSince time.Time
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
	// coverageRejected is what stands where coverage would when the round's
	// coverage facts did not pass the observer: the rule they broke. Kept
	// and cleared exactly as coverage is, so the row always says one of
	// three things -- the windows, "the server refused the reading", or
	// nothing because the round reported no coverage.
	coverageRejected *CoverageRejected
	// shortRounds counts consecutive rounds whose windows were short. It is
	// the one part of the coverage a single observation cannot carry, and the
	// only part that separates a window that is filling from one that never
	// will.
	shortRounds uint32
	// refusedRounds counts the rounds of the current short run whose window
	// reading the observer refused. Such a round says nothing about the
	// windows, so it neither extends the run nor ends it; this says how many
	// the run was held over. Ends with the run.
	refusedRounds uint32
	// lastRead says the latest round that was read -- not refused -- carried
	// a reading, which is what the round after compares against. It is not
	// "coverage is on the row": a refused round clears the row and leaves
	// the comparison where the last read round put it.
	lastRead bool
	// constrainedRounds and resumedRounds are the runs of the two partial
	// rounds; see HistoryCoverage.ConstrainedRounds.
	constrainedRounds uint32
	resumedRounds     uint32
	// heldFullRounds counts consecutive rounds whose windows were all full
	// while a guard held them; a short round, an unguarded round or a
	// healthy completion ends it.
	heldFullRounds uint32
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
	// lastFailureSlot is the Slot the last failure was observed on, so a
	// skip can carry the failure that preceded it only when it was this
	// Slot's -- lastFailure itself is never cleared and would otherwise
	// hand a skip a failure from another round.
	lastFailureSlot int64
	// lastRoundSlot is the Slot of the latest round that ended, however it
	// ended; published so the reading can tell this round's failure and
	// error from an earlier round's by Slot rather than by clock.
	lastRoundSlot int64
	// internal is the last failure of this deployment's own making seen in
	// the current run -- a contract or evaluation error -- kept until the
	// stage it failed in passes (or a healthy completion, which is that too)
	// and published beside the row's finding. The object's line is decided
	// by its column (a pool object is the refusal's), and an internal error
	// under it was invisible: a live row filed as HTTP 400 had also hit an
	// aggregation conflict every round.
	internal *FailureRef
	// internalEndedRound says whether the round internal was seen on ended
	// there, without completing. It decides what a later completion at the
	// same Slot means: after a round that failed outright, a completion at
	// that Slot is the retry getting through the failed stage; after a round
	// that carried the failure as a fact and still completed degraded, a
	// completion at that Slot is that same round, and proves nothing.
	internalEndedRound bool
	// previousWorstValid and noProgressRounds say whether a short window is
	// filling: the worst level's valid count last round, and how many
	// consecutive rounds it has not risen. A window at 8/24 that was 7/24
	// is being filled; one at 0/5 for thirty rounds is not, and "等窗口填满"
	// is only advice for the first.
	previousWorstValid uint32
	noProgressRounds   uint32
	// unchangedRounds is how many consecutive rounds the worst valid count
	// has been exactly the count before. Its own counter beside
	// noProgressRounds because "not risen" covers two windows that need
	// different words: one whose count is falling -- every round adds a hole
	// and drops a good point, the window is being emptied -- and one whose
	// count is flat -- a fixed set of holes sliding through a window that is
	// otherwise filling as fast as it drains. Read on a live object, the
	// no-progress count was 115 for both readings and could not tell them
	// apart; the flat count could, and it is what the fill projection rests
	// on.
	unchangedRounds uint32
	// lastError is the last round that returned an error, verbatim, with the
	// Slot it was on and how many rounds in a row have failed on that Slot.
	// Cleared by a healthy completion, like everything else about a run.
	lastError *LastError
	// lastHealthyAt is when the object last completed healthily as far as
	// this process can vouch: a round it watched, or the healthy round the
	// commit recorded and it was restored from. Not cleared by resetRun --
	// it is what the next run's recovery is judged against -- and zero when
	// neither is known.
	lastHealthyAt time.Time
	// restoredRound is the persisted summary of the last committed round the
	// object was restored from, published on the row until this process
	// completes a round of its own, when the row speaks for itself.
	restoredRound *RestoredRound
	// seenRevisions is the snapshot/query/schedule triple the latest
	// observation of this object carried; completedRevisions the triple at
	// the last completed round; configChanged whether the two differed when
	// the round completed. A carried CONFIG_DRIFT moves no revision.
	seenRevisions      revisionTriple
	completedRevisions revisionTriple
	configChanged      bool
}

// revisionTriple is the configuration an object's round ran under.
type revisionTriple struct{ snapshot, query, schedule string }

func (triple revisionTriple) known() bool {
	return triple.snapshot != "" || triple.query != "" || triple.schedule != ""
}

// lastErrorTextLimit bounds the error text a row carries: it travels on
// every snapshot, and an error can quote a response body.
const lastErrorTextLimit = 256

// boundedErrorText cuts at the limit on a rune boundary. Cutting bytes
// leaves half a character at the end of a text with Chinese in it, which
// the JSON encoder turns into U+FFFD.
func boundedErrorText(text string) string {
	if len(text) <= lastErrorTextLimit {
		return text
	}
	cut := lastErrorTextLimit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "..."
}

// boundedErrorTail keeps the end of an error's words rather than the start.
// A wrapped error is read from the outside in -- the worker's stage, the
// sink's, the client's -- and the cause is the innermost, at the end; the
// live chain of a client refusing to send was 300 bytes of wrapping before
// the one sentence that decided it, and a head-bounded copy cut that
// sentence in half.
func boundedErrorTail(text string) string {
	if len(text) <= lastErrorTextLimit {
		return text
	}
	cut := len(text) - lastErrorTextLimit
	for cut < len(text) && !utf8.RuneStart(text[cut]) {
		cut++
	}
	return "..." + text[cut:]
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
	// emptyEveryRoundAfter is how long an object that never returned records
	// must have completed every round empty before NoData lists it as such;
	// noDataAfter how long one that did must have gone without them.
	emptyEveryRoundAfter time.Duration
	noDataAfter          time.Duration
	maxTracked           int
	now                  func() time.Time
	// platformHorizon reads the deployment's no-data tracking horizon, the
	// number a Plan's effective horizon is read against to say where it came
	// from; nil is a tracker that was not told it at all.
	platformHorizon func() int64

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
	// demotionRestored is the objects that came back into the pool from their
	// persisted record -- after a restart or a change of owner -- and are not
	// entries; demotionHandovers the objects that left this replica while in
	// the pool, which are not exits: the pool moves with the object. With
	// them the pool's own arithmetic closes: entries plus restored equal
	// exits plus handovers plus the objects in it now. demotionReentries is
	// the entries that came back within the re-entry window of an exit.
	demotionRestored  int
	demotionHandovers int
	demotionReentries int
	// recovered is the problems whose listed objects completed healthily,
	// by line and fold, kept for RecoveredRetention after the last one did.
	recovered map[string]*recoveredFold
	// bookkeeping is the running count of Slots an earlier attempt fully
	// executed that a query-free completion then closed, and the objects it
	// happened to. Not derivable from the records, which keep one span per
	// object; the trend is what says the control-plane store is failing
	// writes.
	bookkeeping        BookkeepingFacts
	bookkeepingObjects map[string]struct{}
}

// NewTracker wraps an observer. A nil next observer is allowed; the tracker is
// then purely a sink.
func NewTracker(next observability.Observer, replica string, now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	return &Tracker{
		next:                 next,
		replica:              replica,
		degradedRounds:       DefaultDegradedRounds,
		blockedRounds:        DefaultBlockedRounds,
		emptyEveryRoundAfter: DefaultEmptyEveryRoundAfter,
		noDataAfter:          DefaultNoDataAfter,
		maxTracked:           DefaultTrackedQueryGroups,
		now:                  now,
		groups:               make(map[string]*queryGroupState),
	}
}

// SetPlatformNoDataHorizon tells the tracker how to read the deployment's
// no-data tracking horizon, so a Plan's effective horizon can be read as the
// platform's, the strategy's own, or none. Read through the same seam the
// reconciler freezes it through rather than captured as a value, so the two
// cannot disagree about what the platform's number is. Zero is a deployment
// that configured none. Until this is called every row reads the source as
// unknown rather than guessing.
func (tracker *Tracker) SetPlatformNoDataHorizon(read func() int64) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	tracker.platformHorizon = read
}

// horizonSourceOf reads where a Plan's effective horizon came from: what
// compilation froze beside the number when the line carries it, and the
// comparison against the platform's horizon only for a line that does not -
// a Plan compiled before the source was frozen, or a Worker from before the
// line carried it. See NoDataHorizonSources for why the comparison is only
// the fallback and where it is ambiguous.
func (tracker *Tracker) horizonSourceOf(horizon int64, frozen string) (source, basis string) {
	switch {
	case frozen == NoDataHorizonPlatform || frozen == NoDataHorizonStrategy:
		return frozen, NoDataHorizonSourceFrozen
	case tracker.platformHorizon == nil:
		return NoDataHorizonUnknown, NoDataHorizonSourceInferred
	case horizon <= 0:
		return NoDataHorizonNone, NoDataHorizonSourceInferred
	case horizon == tracker.platformHorizon():
		return NoDataHorizonPlatform, NoDataHorizonSourceInferred
	default:
		return NoDataHorizonStrategy, NoDataHorizonSourceInferred
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
	if trace.QueryGroupKey == "" || trace.StrategyID == "" || trace.EvaluationTime == 0 {
		fromContext := observability.TraceFieldsFromContext(ctx)
		if trace.QueryGroupKey == "" {
			trace.QueryGroupKey = fromContext.QueryGroupKey
		}
		if trace.StrategyID == "" {
			trace.StrategyID = fromContext.StrategyID
			trace.BusinessID = fromContext.BusinessID
		}
		if trace.EvaluationTime == 0 {
			trace.EvaluationTime = fromContext.EvaluationTime
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
	// A failed event write is the same kind of fact as a query failure: no
	// outcome of its own, and the only observation whose words say why the
	// round's events did not go.
	outputFailed := observation.Stage == observability.StageEventACKed && observation.Err != nil
	// A write of the round's events that went through is the one observation
	// that proves an output failure of this deployment's own making has
	// passed; it carries nothing else the tracker keeps, and is read for that
	// alone below.
	outputACKed := observation.Stage == observability.StageEventACKed && observation.Err == nil
	if trace.StrategyID == "" && completion == "" && runOutcome == "" && executeOutcome == "" &&
		failure == nil && !outputFailed && !outputACKed && observation.QueryCooldown == nil && cursorAdvance == nil &&
		observation.NoDataMemoryRefusal == nil && observation.NoDataMemoryWrite == nil && observation.GapProgress == nil &&
		observation.NoDataMemoryRead == nil && observation.NoDataMemoryRenewal == nil {
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
	if state.firstSeenAt.IsZero() {
		state.firstSeenAt = at
	}
	// The round's events went to the sink: for an object whose own failure
	// was the client refusing to send, this is the failed stage passing.
	if outputACKed && defectPassedByOutput(state.internal, state.internalEndedRound, trace.EvaluationTime) {
		tracker.noteDefectPassed(queryGroup, state, at)
	}
	// A refused absence-memory write is not a round either: the round it
	// happened in was fine and is reported separately. Recorded on the object
	// with the store's reason and the size it measured, and it does not
	// touch the round bookkeeping -- the observation's strategy is still
	// learned below, since the Plan it names is the one whose memory is lost.
	plan := StrategyRef{StrategyID: trace.StrategyID, BusinessID: trace.BusinessID}
	// A held gap scope, as the round that read it reports it, every round it
	// is held. Not a round either: the round it belongs to completes on its
	// own and prunes the scopes it did not report.
	switch {
	case string(observation.ReasonCode) == contract.ReasonQGBudgetShareExceeded:
		// Past the share: the object is on the refusal's line now, and a
		// warning that it is near the share would say less than that line
		// and say it twice. The reading stays, marked, so the next completion
		// keeps the Since this one had.
		if state.shareUsage != nil {
			state.shareUsage.refused = true
		}
	case observation.Stage == observability.StageSlotCompleted && observation.Err == nil:
		state.noteShareUsage(observation.SlotBudgetUsage, trace.EvaluationTime, at)
	}
	if progress := observation.GapProgress; progress != nil {
		if state.guards == nil {
			state.guards = map[string]*gapGuardState{}
		}
		key := plan.StrategyID + "\x00" + progress.Scope
		held := state.guards[key]
		if held == nil {
			held = &gapGuardState{guard: GapGuard{Plan: plan, Scope: progress.Scope, FirstAt: at}}
			state.guards[key] = held
		} else if held.guard.Observed == progress.Observed {
			held.guard.UnchangedRounds++
		} else {
			held.guard.UnchangedRounds = 0
		}
		held.guard.Status, held.guard.Reason = progress.Status, progress.Reason
		held.guard.Required, held.guard.Observed = progress.Required, progress.Observed
		held.guard.Progress = progress.Progress
		held.guard.Measure = GuardObservedMeasure
		held.guard.LastAt = at
		held.guard.Rounds++
		held.gen = state.guardGen
	}
	if refusal := observation.NoDataMemoryRefusal; refusal != nil {
		if state.noDataMemory == nil {
			state.noDataMemory = map[StrategyRef]*NoDataMemoryRefusal{}
		}
		memory := state.noDataMemory[plan]
		if memory == nil || memory.Kind != NoDataMemoryRefusalWrite {
			// A Plan whose renewal was refused and whose write is now refused
			// too is listed for the write: the write is the loss that stands
			// whatever happens to the key's lifetime.
			memory = &NoDataMemoryRefusal{Kind: NoDataMemoryRefusalWrite, FirstAt: at, Plan: plan}
			state.noDataMemory[plan] = memory
		}
		memory.Reason, memory.Record, memory.Groups, memory.Limit =
			refusal.Reason, refusal.Record, refusal.Groups, refusal.Limit
		memory.LastAt = at
		memory.Refusals++
	}
	// What the last read said the memory was stored as. On every read of a
	// Plan with a memory, so the row can say what it has -- and, once the
	// rollout is over, that nothing is still on the old record.
	if read := observation.NoDataMemoryRead; read != nil {
		upkeep := state.upkeepOf(plan)
		readAt := at
		upkeep.Representation, upkeep.LastReadAt = read.Representation, &readAt
	}
	// The wire format the Plan's events go out as, from its evaluation line.
	// Not a round either; the word is the Plan's and changes only when the
	// Plan is recompiled, so the latest line's word is the current one.
	if format := observation.OutputWireFormat; format != "" && plan.StrategyID != "" {
		if state.wireFormats == nil {
			state.wireFormats = map[StrategyRef]*PlanWireFormat{}
		}
		state.wireFormats[plan] = &PlanWireFormat{Plan: plan, WireFormat: format, LastSeenAt: at}
	}
	// How many series the round bound to the Plan, from its evaluation
	// lines: every line of one Plan in one round says the same number, so
	// the latest line's is the round's.
	if matched := observation.PlanSeriesMatched; matched != nil && plan.StrategyID != "" {
		if state.planSeries == nil {
			state.planSeries = map[StrategyRef]*PlanSeriesMatched{}
		}
		state.planSeries[plan] = &PlanSeriesMatched{Plan: plan, Matched: *matched, EvaluationTime: trace.EvaluationTime, LastSeenAt: at}
	}
	// What the Plan's no-data round decided, on the round it decided. Not a
	// round of the object either: the Slot it belongs to completes on its
	// own. The whole word is replaced -- the counts are that round's, not a
	// running total -- and the source of the horizon is read here, against
	// the platform's, because the Plan carries only the number. The emitter
	// names the Plan on every line, which is what gets the observation past
	// the anonymous-round return above; a line without one has no Plan to
	// file under and is not recorded.
	if absence := observation.NoDataAbsence; absence != nil && plan.StrategyID != "" {
		if state.noDataTracking == nil {
			state.noDataTracking = map[StrategyRef]*NoDataTracking{}
		}
		source, basis := tracker.horizonSourceOf(absence.HorizonSeconds, absence.HorizonSource)
		state.noDataTracking[plan] = &NoDataTracking{
			Plan:           plan,
			HorizonSeconds: absence.HorizonSeconds, HorizonSource: source, HorizonSourceBasis: basis,
			RosterSource: absence.RosterSource, Expected: absence.Expected, Present: absence.Present,
			Absent: absence.Absent, ExpiredThisRound: absence.Expired, Suppressed: absence.Suppressed,
			Dropped: absence.Dropped, EvaluationTime: trace.EvaluationTime, DecidedAt: at,
			AbsentAges: NoDataAbsentAges{
				ThisRound: absence.AbsentAges.ThisRound, UnderHour: absence.AbsentAges.UnderHour,
				UnderDay: absence.AbsentAges.UnderDay, DayOrMore: absence.AbsentAges.DayOrMore,
			},
		}
	}
	// A renewal that reached the store. Success -- whether or not it set a
	// new lifetime; "enough life left" is the ordinary answer -- is the one
	// positive fact about upkeep, and it ends a refused renewal for the Plan.
	// A refusal is listed at once and stays until a renewal or a write goes
	// through: the store is asked again every round while it refuses, and a
	// memory whose key nobody can renew is on its way to expiring for as long
	// as that lasts.
	if renewal := observation.NoDataMemoryRenewal; renewal != nil {
		upkeep := state.upkeepOf(plan)
		attemptAt := at
		upkeep.LastAttemptAt, upkeep.TTLSeconds = &attemptAt, renewal.TTLSeconds
		if renewal.Renewed {
			renewedAt := at
			upkeep.LastRenewedAt = &renewedAt
		}
		reason := string(observation.ReasonCode)
		if observation.Result == observability.ResultSuccess || reason == "" || reason == string(observability.ReasonNone) {
			if memory := state.noDataMemory[plan]; memory != nil && memory.Kind == NoDataMemoryRefusalRenewal {
				tracker.endMemoryRefusal(queryGroup, state, plan, at)
			}
		} else {
			if state.noDataMemory == nil {
				state.noDataMemory = map[StrategyRef]*NoDataMemoryRefusal{}
			}
			memory := state.noDataMemory[plan]
			if memory == nil {
				memory = &NoDataMemoryRefusal{Kind: NoDataMemoryRefusalRenewal, FirstAt: at, Plan: plan}
				state.noDataMemory[plan] = memory
			}
			if memory.Kind == NoDataMemoryRefusalRenewal {
				memory.Reason, memory.LastAt = reason, at
				memory.Refusals++
			}
		}
	}
	// A write that went through ends that Plan's refusal: the store now holds
	// what the round wanted written, which is the one positive fact recovery
	// is read from here. Stored is the emitter's word for it, carried rather
	// than derived from the outcome -- ALREADY_APPLIED is stored and
	// STALE_VERSION is not, and the one place that decides is the emitter.
	// A write that did not store is not a refusal either: those have their
	// own stage, and a stale or conflicting write is a different situation
	// from a record the store will not take. Only the matching Plan's entry
	// goes: two Plans of one object refused and one of them storing is one
	// Plan recovered and an object still listed, and read on, it was the
	// whole object recovered on the strength of the wrong Plan's write.
	// A stored write also sets the key's lifetime, so it ends a refused
	// renewal as much as a refused write.
	if write := observation.NoDataMemoryWrite; write != nil && write.Stored && state.noDataMemory[plan] != nil {
		tracker.endMemoryRefusal(queryGroup, state, plan, at)
	}
	// Recorded before anything else, because it is not a round and none of the
	// round bookkeeping below applies to it. The object is very likely running
	// normally now -- what happened is in its past and is permanent, which is
	// exactly why nothing that describes the current round can carry it.
	if cursorAdvance != nil {
		if cursorAdvance.Status == observability.CursorAdvanceApplied {
			state.prunedSkip = &PrunedSkip{
				From: cursorAdvance.From, To: cursorAdvance.To, At: at,
				DiscardedSlot: cursorAdvance.InFlightSlot, Replica: tracker.replica,
			}
		}
		if completion == "" && runOutcome == "" && executeOutcome == "" && failure == nil &&
			observation.QueryCooldown == nil {
			return
		}
	}
	// A Slot skipped because it fell past the replay window. Consecutive skips
	// are one span; a round that is not a skip ends it, and the span stays as
	// the record of what was never evaluated.
	// What the completion found about an earlier attempt at the Slot, as the
	// emitter read it; carried on the row and, for a skip, into the span.
	// Read through the emitter's own reading rather than the counts, so the
	// row, the gap fold and the counter cannot classify one Slot three ways.
	if completion != "" {
		state.lastEvidence = nil
		if facts := observation.ExecutionEvidence; facts != nil {
			evidence := model.ExecutionEvidence{Kind: model.ExecutionEvidenceKind(facts.Kind),
				PlansApplied: facts.PlansApplied, PlansTotal: facts.PlansTotal}
			state.lastEvidence = &ExecutionEvidence{Kind: facts.Kind, Reading: model.ReadEvidence(&evidence),
				PlansApplied: facts.PlansApplied, PlansTotal: facts.PlansTotal}
		}
	}
	if completion == "GAP_SKIPPED" {
		if state.gapSkip == nil || state.lastCompleted != "GAP_SKIPPED" {
			state.gapSkip = &SkippedSpan{FirstSlot: trace.EvaluationTime, Replica: tracker.replica, FirstSeenAt: state.firstSeenAt}
		}
		state.gapSkip.LastSlot = trace.EvaluationTime
		state.gapSkip.Slots++
		state.gapSkip.At = at
		// What held the Slot before it was given up, as the completion says:
		// the record's own fact, read for the loss ahead of where the object
		// sits when the record is read.
		state.gapSkip.HeldBy = heldByOf(observation)
		tracker.noteSkipEvidence(queryGroup, state, at)
		// The last step before the skip, when it was this Slot's: a permit
		// deadline missed says the retry came too late, a budget rejection
		// says the resources did not fit -- and the two are different
		// conversations that a bare GAP_SKIPPED had folded into "容量".
		if state.lastFailure != nil && state.lastFailureSlot == trace.EvaluationTime {
			state.gapSkip.Reason, state.gapSkip.ReasonCategory = state.lastFailure.Code, state.lastFailure.Category
		}
	}
	// Captured before this round is folded in: by the time the run-start block
	// runs, this round has already made the object determined, and the question
	// is whether anything came before it.
	seenBefore := state.determined
	if facts := observation.QueryCooldown; facts != nil {
		switch facts.Event {
		case "entered", "reentered", "extended", "restored":
			copy := *facts
			if state.queryCooldown == nil && facts.Event == "restored" {
				// Back from its record: in the pool since it first entered,
				// on whichever replica that was.
				tracker.demotionRestored++
				state.demotedSince = facts.EnteredAt
				if state.demotedSince.IsZero() {
					state.demotedSince = at
				}
			} else if state.queryCooldown == nil {
				tracker.demotionEntries++
				state.demotedSince = at
				if facts.Event == "reentered" {
					tracker.demotionReentries++
				}
			} else if facts.Event != "restored" {
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
		case "recovered", "query_revision_changed", "disabled":
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
			state.demotedSince = time.Time{}
		}
	}
	if failure != nil {
		// Remembered, not counted: this is context for an anomaly the outcome
		// paths decide on. Treating a failure as conclusive on its own would
		// make a retried transient look like a determined verdict.
		seen := at
		state.lastFailure = &FailureRef{Stage: failure.Stage, Category: failure.Category,
			Code: failure.Code, Detail: failure.Detail, At: &seen, Slot: trace.EvaluationTime}
		state.lastFailureSlot = trace.EvaluationTime
		if internalFailure(failure.Category) {
			copy := *state.lastFailure
			state.internal = &copy
			state.internalEndedRound = false
		}
	}
	// The write of the round's events failing is a failure of its own stage,
	// and the only observation that carries its words is this one: the
	// terminal that follows names the round's reason and nothing else. Six
	// objects failed every round for an afternoon with the row saying
	// OUTPUT_ACK_UNKNOWN at "other" and the page saying the broker was
	// unavailable, while the client had refused to send at all -- a sentence
	// on this observation that reached no row. The words are kept, bounded
	// and sanitized as the row's last error is; the reading decides from them
	// whether the broker or this deployment's own client is the one that
	// said no, and files the failure as internal only in the second case.
	if outputFailed {
		seen := at
		code := string(observation.ReasonCode)
		if !observability.ValidQueryFailureCode(code) {
			code = ""
		}
		// The sink's own account first, when it gave one: its reason word
		// and its sentence, apart from the chain. The chain's words are the
		// fallback for a failure the sink did not name -- a broker that did
		// not answer, wrapped by everything on the way up.
		text := boundedErrorTail(observability.SanitizeErrorText(observation.Err.Error()))
		if r := observation.OutputRejection; r != nil {
			if r.Reason != "" && observability.ValidQueryFailureCode(r.Reason) {
				code = r.Reason
			}
			if r.Detail != "" {
				text = boundedErrorTail(observability.SanitizeErrorText(r.Detail))
			}
		}
		state.lastFailure = &FailureRef{Stage: observability.QueryFailureStageOutput, Category: observability.QueryFailureCategoryOutput,
			Code: code, Text: text, At: &seen, Slot: trace.EvaluationTime}
		state.lastFailureSlot = trace.EvaluationTime
		// This deployment's own, when the sink said it refused or when the
		// words say the client did.
		if observation.OutputRejection != nil || OutputFailureKind(text) == OutputFailureClientRejected {
			copy := *state.lastFailure
			state.internal = &copy
			state.internalEndedRound = false
		}
	}
	// The observation that ends a failed round can name the failure itself,
	// and for one class of failure it is the only observation that does: a
	// query-free Slot never passes through the query stage, so a gap guard
	// refusing it at finalize reaches here as a terminal error with its
	// reason on the observation and no query failure before it. The tracker
	// read only the query stage's failures, so the row carried no code at
	// all: it fell through to the unclassified defect, and when the stall
	// budget ran out ten minutes later it moved to "rounds stalled", with
	// "restart the replica" as the next step for a refusal that repeats
	// until fixed. The code the terminal names is the round's code when
	// this Slot has no failure of its own yet; a query stage that already
	// named this Slot's failure saw it closer to where it happened and
	// keeps the name. Only a code in the contract grammar is a code -- the
	// scheduler's own class words (internal_unknown, contract_deterministic)
	// say which kind of failure it could not name, not what failed. The
	// category is the completion contract's because that is the one place
	// the scheduler names a terminal reason from: its own refusal of what
	// the round produced, read off the returned error.
	if failedExecution(executeOutcome) {
		if code := string(observation.ReasonCode); observability.ValidQueryFailureCode(code) &&
			(state.lastFailure == nil || state.lastFailureSlot != trace.EvaluationTime) {
			seen := at
			state.lastFailure = &FailureRef{Stage: observability.QueryFailureStageOther,
				Category: observability.QueryFailureCategoryCompletionContract, Code: code, At: &seen, Slot: trace.EvaluationTime}
			state.lastFailureSlot = trace.EvaluationTime
			copy := *state.lastFailure
			state.internal = &copy
			state.internalEndedRound = true
		}
	}
	// The error's own words, kept beside the classification, from the one
	// observation that ends the round: slot_completed with a failed outcome.
	// A round that fails emits its error more than once on the way there --
	// the query, the commit, the state admission each report it before the
	// Slot completes with the same error -- and counting every carrier would
	// call one failed round two or three. The terminal one wraps the words
	// of the rest, so nothing is lost by reading only it. Attempts counts
	// rounds in a row on the same Slot: the same Slot failing again is a
	// stuck object, a new Slot failing is a new failure.
	if observation.Err != nil && failedExecution(executeOutcome) {
		text := boundedErrorText(observability.SanitizeErrorText(observation.Err.Error()))
		attempts := 1
		if state.lastError != nil && state.lastError.EvaluationTime == trace.EvaluationTime && trace.EvaluationTime != 0 {
			attempts = state.lastError.Attempts + 1
		}
		state.lastError = &LastError{Text: text, Type: fmt.Sprintf("%T", observation.Err),
			EvaluationTime: trace.EvaluationTime, At: at, Attempts: attempts, Operation: string(observation.Operation)}
	}
	recordStrategy(state.strategies, StrategyRef{StrategyID: trace.StrategyID, BusinessID: trace.BusinessID})

	// The revisions this round ran under, from whichever observation carries
	// them; read against the last completed round's when this one completes.
	if seen := (revisionTriple{trace.SnapshotRevision, trace.QueryRevision, trace.ScheduleRevision}); seen.known() {
		state.seenRevisions = seen
	}

	switch {
	case completion != "":
		// The round that reported its held scopes is over: a scope it did
		// not report was released, and the next round reports into a new
		// generation.
		for key, held := range state.guards {
			if held.gen != state.guardGen {
				delete(state.guards, key)
			}
		}
		state.guardGen++
		// A round that ran through the pipeline at or after the Slot the
		// object's own failure was on is that failure passing, whatever the
		// round found about the data. Read before the healthy completion is,
		// which also ends the failure: the fold is recorded from the failure
		// while it is still there.
		if defectPassedByCompletion(state.internal, state.internalEndedRound, completion, trace.EvaluationTime) {
			tracker.noteDefectPassed(queryGroup, state, at)
		}
		if healthyCompletion(completion) {
			// The one moment recovery has positive evidence: the object that
			// was a row completed healthily. Remembered under the line and
			// fold it was on -- read from the row as it was before this round
			// is folded in, since this round is the one that ends it.
			tracker.noteRecovery(queryGroup, state, at)
		}
		state.determined = true
		state.lastCompleted = completion
		state.lastRoundSlot = trace.EvaluationTime
		// Every completion, healthy or not, goes on the ring the holes are
		// read against: a healthy round is exactly the one a later hole at
		// its minute has to be matched to.
		rememberRound(state, trace.EvaluationTime, completion, observation.ProgressCompletionReason,
			observation.HistoryCoverage, observation.PrimaryInput)
		// A round completed in this process speaks for the object; the
		// summary it was restored from is history now.
		state.restoredRound = nil
		state.configChanged = state.completedRevisions.known() && state.seenRevisions.known() &&
			state.seenRevisions != state.completedRevisions
		state.completedRevisions = state.seenRevisions
		tracker.noteReason(state, completion+"/"+observation.ProgressCompletionReason, at)
		// The no-data run is kept apart from the anomaly run: an empty
		// completion is healthy for the equation and ends any anomaly run, and
		// a round with records -- degraded or not -- ends the empty run.
		if completion == "FULL_EMPTY_COMPLETED" {
			if state.emptyRuns == 0 {
				state.emptySince = at
				state.emptySinceFrom = SinceSnapshotContinuity
			}
			state.emptyRuns++
			// A hole in the evidence costs the run its head start. The run
			// itself continues -- the object still has not seen data, and a
			// blocked or failing stretch leaves it on that stretch's own line
			// meanwhile -- but an hour of empty completions has to be an hour
			// this process watched, near enough. A day of rounds that produced
			// no completion at all once sat inside a run dated the day before,
			// and the release that ended them put three hundred objects on
			// this line at once, each with fifty-two rounds of evidence behind
			// an inherited hour.
			//
			// A hole is measured against the object's own cadence, not against
			// the clock. The first draft of this compared the distance to the
			// hour the line waits for, which reads every round of an object
			// slower than an hour as a hole: the start is cleared each round,
			// the distance stays zero, and the line can never list it at all.
			// Nothing here says otherwise -- the hour is a threshold, not a
			// period -- and a predicate that goes silent exactly at its own
			// constant is measuring the wrong thing.
			//
			// The cadence rises with the longest gap the run has accepted and
			// does not fall inside it. A draft that let every gap set the
			// cadence let one round arriving early decide it: a catch-up, a
			// retry, a burst after a restart is a gap nothing like the pace,
			// and the ordinary round after it then read as a hole. The row
			// stayed on the page with a Since later than the truth -- not a
			// missing reading but a smaller one in its place, which looks as
			// credible as the right one. Letting it fall halfway instead
			// survives one such round and not two in a row, which is a
			// restart burst; rising only is the shape with no such edge.
			// What it costs is the other direction: a run that has already
			// accepted one near-hour gap holds a higher bar for the rest of
			// the run, so a later stall shorter than three of that gap is not
			// called one. The row is then listed from its earlier start --
			// which is what SinceIsLowerBound on this row already says it is.
			//
			// Measured from the latest empty round and from nothing else. A
			// record that carries the run's start without a round summary
			// leaves no second point, and the distance from a run's start is
			// not a gap in its evidence -- an object empty for ninety minutes
			// at fifteen seconds a round has a start ninety minutes back and
			// no hole anywhere. Judging that distance took such a record off
			// its own restored start, which the test for it caught.
			if state.lastEmptySlot != 0 {
				gap := trace.EvaluationTime - state.lastEmptySlot
				switch {
				case emptyRunHole(gap, state.emptyStride, state.emptySlotFrom == SinceRestoredEmptyRun, tracker.emptyEveryRoundAfter):
					// The run starts again here, and so does the cadence:
					// the hole is not a pace, and seeding the cadence with
					// it would raise the bar for the whole run that follows.
					state.emptySinceSlot, state.emptyStride = 0, 0
				case gap > state.emptyStride:
					state.emptyStride = gap
				}
			}
			if state.emptySinceSlot == 0 {
				state.emptySinceSlot = trace.EvaluationTime
				state.emptySlotFrom = SinceSnapshotContinuity
			}
			state.lastEmptySlot = trace.EvaluationTime
		} else {
			state.emptyRuns = 0
			if completion == "FULL_COMPLETED" {
				// Seen retires the Slot run for good: the line asks for no
				// round known to have had records, and never reads the
				// run's bounds again once one has. They are left as they
				// are rather than cleared, so nothing here looks like it
				// decides what "seen" already has. The data side's clock
				// starts here instead.
				state.sawData = true
				if trace.EvaluationTime > state.lastDataSlot {
					state.lastDataSlot = trace.EvaluationTime
				}
			}
		}
		if healthyCompletion(completion) {
			tracker.resetRun(state)
			state.lastHealthyAt = at
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
		// A round whose reading the observer refused carries no windows, and
		// that is not the same as a round with no windows short. It holds
		// every run counter below as it stands -- neither extending a run
		// nor ending one -- and is counted beside them. Ending the runs
		// there let a reading refused every few rounds keep a window that
		// never fills from ever reaching its own length: the object read as
		// still filling, on no line, for as long as the refusals lasted.
		refusedRound := observation.HistoryCoverage == nil && observation.HistoryCoverageRejected != nil
		worstWindow, worstWindowChanged := state.worstWindow, false
		previous, hadReading := state.previousWorstValid, state.lastRead
		if refusedRound {
			state.refusedRounds++
		} else {
			// Counted before the coverage is replaced, because the run length is
			// the only thing here that one round cannot supply. A round that
			// reports a complete window ends the run: a window that filled once
			// was filling, whatever it does next.
			if facts := observation.HistoryCoverage; facts == nil || facts.Short == 0 {
				state.shortRounds, state.refusedRounds = 0, 0
			} else {
				state.shortRounds++
			}
			if facts := observation.HistoryCoverage; facts == nil || facts.Empty == 0 {
				state.emptyRounds = 0
			} else {
				state.emptyRounds++
			}
			// Full under a guard: the round the guard converges on, and every
			// round after it that it has not.
			if facts := observation.HistoryCoverage; facts == nil || facts.Guarded == 0 || facts.Short != 0 {
				state.heldFullRounds = 0
			} else {
				state.heldFullRounds++
			}
			// The two partial-round runs. Their per-round counts are replaced by
			// the next round, so without these a round that could load no State
			// is gone the moment the next one lands, and the interface can only
			// answer for whoever happened to be looking.
			if facts := observation.HistoryCoverage; facts == nil || facts.Constrained == 0 {
				state.constrainedRounds = 0
			} else {
				state.constrainedRounds++
			}
			if facts := observation.HistoryCoverage; facts == nil || facts.Resumed == 0 {
				state.resumedRounds = 0
			} else {
				state.resumedRounds++
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
			// Whether the worst window is filling: compared with the previous
			// round's valid count on the same object; a round with nothing short
			// ends the comparison, like every other run counter here.
			// The comparison is between the worst pairs of two rounds, and the
			// pair belongs to a window. When the worst window is another one
			// than last round's, the two pairs are not one window's progress and
			// the counters start over; the row says so.
			worstWindow = ""
			if facts := observation.HistoryCoverage; facts != nil && facts.Short > 0 && len(facts.Windows) > 0 {
				worstWindow = windowKey(facts.Windows[0].Strategy, facts.Windows[0].Series, facts.Windows[0].Level)
				worstWindowChanged = state.worstWindow != "" && state.worstWindow != worstWindow
			}
			if facts := observation.HistoryCoverage; facts == nil || facts.Short == 0 {
				state.noProgressRounds, state.previousWorstValid, state.unchangedRounds = 0, 0, 0
			} else {
				if hadReading && !worstWindowChanged && facts.WorstValid <= previous {
					state.noProgressRounds++
				} else {
					state.noProgressRounds = 0
				}
				if hadReading && !worstWindowChanged && facts.WorstValid == previous {
					state.unchangedRounds++
				} else {
					state.unchangedRounds = 0
				}
				state.previousWorstValid = facts.WorstValid
			}
			state.worstWindow = worstWindow
			state.lastRead = observation.HistoryCoverage != nil
		}
		// A refused round inside a run that has been read keeps the last
		// reading on the row, marked held and carrying the run's refusals,
		// beside the refusal: the run counters were held over the round, so
		// the verdict they support still stands. Dropping the reading put
		// such an object on the refusal's line every refused round and back
		// on its window's line every read one, so the judgement the held
		// counts reached was out of sight half the time. Only a run with no
		// reading at all leaves the refusal alone on the row.
		held := state.coverage
		state.coverage = nil
		if refusedRound && held != nil {
			kept := *held
			kept.Held, kept.RefusedRounds = true, state.refusedRounds
			state.coverage = &kept
		}
		state.coverageRejected = nil
		if rejected := observation.HistoryCoverageRejected; rejected != nil {
			state.coverageRejected = &CoverageRejected{Rule: string(rejected.Rule), Series: rejected.Series}
		}
		if facts := observation.HistoryCoverage; facts != nil {
			state.coverage = &HistoryCoverage{
				Levels: facts.Levels, Short: facts.Short, Empty: facts.Empty,
				WorstValid: facts.WorstValid, WorstRequired: facts.WorstRequired, Measure: CoverageValidMeasure,
				ShortRounds: state.shortRounds, EmptyRounds: state.emptyRounds, RefusedRounds: state.refusedRounds,
				Guarded: facts.Guarded, HeldFullRounds: state.heldFullRounds,
				Fresh: facts.Fresh, ShortFresh: facts.ShortFresh, FreshRounds: state.freshRounds,
				Unusable: facts.Unusable, UnusableReason: facts.UnusableReason,
				Resumed: facts.Resumed, Constrained: facts.Constrained,
				ConstrainedRounds: state.constrainedRounds, ResumedRounds: state.resumedRounds,
				Abnormal: facts.Abnormal, AbnormalOnIncomplete: facts.AbnormalOnIncomplete,
				NoProgressRounds: state.noProgressRounds, UnchangedRounds: state.unchangedRounds,
				WorstWindow: worstWindow, WorstWindowChanged: worstWindowChanged,
				Windows: windowRows(state.rounds, facts),
			}
			if len(state.coverage.Windows) > 0 {
				state.coverage.RoundsRemembered, state.coverage.RoundsKept = len(state.rounds), RecentRoundsKept
			}
			// The previous count is the previous window's, and is named as
			// such only when it is this window's.
			if hadReading && facts.Short != 0 && !worstWindowChanged {
				state.coverage.PreviousWorstValid, state.coverage.PreviousKnown = previous, true
			}
		}
	case runOutcome == "ownership_rejected":
		// The fence refused this replica's round: the object is another
		// replica's now, or the store could not confirm whose it is. Either
		// way this replica has stopped running its rounds, and the clock
		// that says "the rounds stopped ending" must stop with them -- read
		// on, it marked an object the rebalance had just moved away as
		// stalled here while its new owner had not yet listed it, and the
		// first screen read a deterministic defect as a stall and then as
		// fixed. Nothing else about the row changes: what it last said is
		// still what it last said, until the publisher forgets an object
		// this replica no longer owns.
		state.failingSince = time.Time{}
		return
	case blockedOutcome(runOutcome):
		state.determined = true
		state.lastRoundSlot = trace.EvaluationTime
		tracker.noteReason(state, "blocked/"+runOutcome, at)
		state.failingSince = time.Time{}
		state.blockedRuns++
		state.currentKind = KindBlockedRun
		state.reasonCode = runOutcome
		// A round that never reached the window is not a window declining to
		// decide. It is a round that did not happen.
		state.sawSomethingWrong = true
	case failedExecution(executeOutcome):
		state.determined = true
		state.lastRoundSlot = trace.EvaluationTime
		// The round this failure was seen on ended here without completing;
		// a completion at this Slot later is a retry that got through.
		if state.internal != nil && state.internal.Slot == trace.EvaluationTime {
			state.internalEndedRound = true
		}
		failureCode := ""
		if state.lastFailure != nil {
			failureCode = state.lastFailure.Code
		}
		tracker.noteReason(state, "failed/"+executeOutcome+"/"+failureCode, at)
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

// heldByOf is the decision that held the completed Slot, from wherever the
// completion carries it; empty for none, so the record's field stays absent
// when nothing held the Slot.
func heldByOf(observation observability.Observation) string {
	facts := observation.HeldBy
	if facts == nil && observation.ReplayExpiry != nil {
		facts = observation.ReplayExpiry.HeldBy
	}
	if facts == nil || facts.Decision == "" || facts.Decision == observability.HeldByNothing {
		return ""
	}
	return facts.Decision
}

// noteReason advances the object's own clock: a new result-and-reason pair
// starts it, the same pair counts one more round on it.
func (tracker *Tracker) noteReason(state *queryGroupState, key string, at time.Time) {
	if state.reasonKey != key {
		state.reasonKey, state.reasonSince, state.reasonRuns = key, at, 0
	}
	state.reasonRuns++
	state.reasonLastAt = at
}

func (tracker *Tracker) resetRun(state *queryGroupState) {
	// The cause described the run that just ended. Leaving it would let a
	// recovered object still explain itself with the last thing that went wrong.
	state.cooldownExposed = false
	state.cause = ""
	state.causeReason = ""
	state.coverage = nil
	state.coverageRejected = nil
	state.shortRounds = 0
	state.refusedRounds = 0
	state.lastRead = false
	state.emptyRounds = 0
	state.constrainedRounds = 0
	state.resumedRounds = 0
	state.heldFullRounds = 0
	state.sawSomethingWrong = false
	state.degradedRuns = 0
	state.blockedRuns = 0
	state.inAnomalyRun = false
	state.currentKind = ""
	state.reasonCode = ""
	state.runStartedAt = time.Time{}
	state.sinceFrom = ""
	state.failingSince = time.Time{}
	state.lastError = nil
	state.internal = nil
	state.restoredRound = nil
	// The window's progress counters reset with the coverage they compare
	// against: a round with no short window clears them where it clears
	// state.coverage, so nothing is left for this to do.
}

// internalFailure reports whether a failure category is this deployment's
// own doing rather than the backend's or the strategy's: a contract the
// pipeline set for itself and broke, or an evaluation that could not run.
func internalFailure(category string) bool {
	return category == observability.QueryFailureCategoryCompletionContract || category == observability.QueryFailureCategoryEvaluation
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
func (tracker *Tracker) DemotionFlow() DemotionFlowFacts {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return DemotionFlowFacts{Entries: tracker.demotionEntries, Extensions: tracker.demotionExtensions,
		Exits: tracker.demotionExits, LastExit: tracker.lastDemotionExit, Restored: tracker.demotionRestored,
		Handovers: tracker.demotionHandovers, Reentries: tracker.demotionReentries}
}

// DemotionFlowFacts is the pool's flow on one replica since it started.
// Entries plus Restored equal Exits plus Handovers plus the objects in the
// pool now; Reentries is the part of Entries that came back within the
// re-entry window of an exit.
type DemotionFlowFacts struct {
	Entries, Extensions, Exits int
	LastExit                   time.Time
	Restored, Handovers        int
	Reentries                  int
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

// over says whether the object's run has lasted long enough to be listed,
// or the object is exposed by the pool: the one predicate for "is this a
// row", shared by the list and by the recovery that ends a row.
func (tracker *Tracker) over(state *queryGroupState) bool {
	over := (state.currentKind == KindDegradedRun && (state.degradedRuns >= tracker.degradedRounds || terminalCompletion(state.lastCompleted))) ||
		(state.currentKind == KindBlockedRun && state.blockedRuns >= tracker.blockedRounds)
	return over || state.queryCooldown != nil || (state.cooldownExposed && state.inAnomalyRun)
}

// terminalCompletion reports whether a completion kind is a deterministic
// refusal, which is listed on its first round.
func terminalCompletion(kind string) bool {
	return inVocabulary(kind, TerminalCompletions)
}

// rowOf is the object's row as the list publishes it. Caller holds the lock.
func (tracker *Tracker) rowOf(queryGroup string, state *queryGroupState) Anomaly {
	anomaly := Anomaly{
		QueryGroup:    queryGroup,
		QueryCooldown: state.queryCooldown,
		DemotedSince:  state.demotedSince,
		Kind:          state.currentKind,
		ReasonCode:    state.reasonCode, Cause: state.cause, CauseReason: state.causeReason,
		Coverage:         state.coverage,
		CoverageRejected: state.coverageRejected,
		Since:            state.runStartedAt,
		SinceFrom:        state.sinceFrom,
		FailingSince:     state.failingSince,
		ReasonSince:      state.reasonSince,
		ReasonLastAt:     state.reasonLastAt,
		RoundSlot:        state.lastRoundSlot,
		Consecutive:      state.reasonRuns,
		Replica:          tracker.replica,
		Failure:          state.lastFailure,
		Internal:         state.internal,
		LastError:        state.lastError,
		LastHealthyAt:    state.lastHealthyAt,
		ConfigChanged:    state.configChanged,
		Restored:         state.restoredRound,
	}
	anomaly.Guards, anomaly.GuardsTotal = worstGuards(state.guards)
	// The guard's Plan's input beside the guard: the number that says
	// whether 0 of N is warming or has nothing to warm on.
	for index := range anomaly.Guards {
		if matched := state.planSeries[anomaly.Guards[index].Plan]; matched != nil {
			anomaly.Guards[index].SeriesMatched, anomaly.Guards[index].SeriesMatchedKnown = matched.Matched, true
		}
	}
	anomaly.PlanSeries = planSeriesRows(state)
	anomaly.NoDataMemoryUpkeep = latestUpkeep(state)
	anomaly.NoDataTracking = noDataTrackingRows(state)
	anomaly.WireFormats = wireFormatRows(state)
	// The holder of the latest round's Slot, when that round gave it up. The
	// span keeps the word from the completion that wrote it; the row carries
	// it only while the skip is the latest round -- a round since, run or
	// failed, has its own reason and the skip is the span's record alone.
	if state.gapSkip != nil && state.reasonCode == "GAP_SKIPPED" && state.gapSkip.LastSlot == state.lastRoundSlot {
		anomaly.HeldBy = state.gapSkip.HeldBy
	}
	if state.lastEvidence != nil {
		evidence := *state.lastEvidence
		anomaly.ExecutionEvidence = &evidence
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
	return anomaly
}

// worstGuards is the row's held scopes, the worst MaxGuardsPerRow of them:
// gapped before warming, then the least advanced -- observed against
// required -- then the longest unchanged, then by Plan and scope so the
// order is total. The count is of all of them.
func worstGuards(held map[string]*gapGuardState) ([]GapGuard, int) {
	if len(held) == 0 {
		return nil, 0
	}
	guards := make([]GapGuard, 0, len(held))
	for _, state := range held {
		guards = append(guards, state.guard)
	}
	sort.Slice(guards, func(i, j int) bool {
		left, right := guards[i], guards[j]
		gapped := string(model.GapStatusGapped)
		if (left.Status == gapped) != (right.Status == gapped) {
			return left.Status == gapped
		}
		leftShare, rightShare := guardShare(left), guardShare(right)
		if leftShare != rightShare {
			return leftShare < rightShare
		}
		if left.UnchangedRounds != right.UnchangedRounds {
			return left.UnchangedRounds > right.UnchangedRounds
		}
		if left.Plan.StrategyID != right.Plan.StrategyID {
			return left.Plan.StrategyID < right.Plan.StrategyID
		}
		return left.Scope < right.Scope
	})
	total := len(guards)
	if total > MaxGuardsPerRow {
		guards = guards[:MaxGuardsPerRow]
	}
	return guards, total
}

// guardShare is how far a held scope's count has got, as a fraction; a
// scope requiring nothing is read as complete.
func guardShare(guard GapGuard) float64 {
	if guard.Required == 0 {
		return 1
	}
	return float64(guard.Observed) / float64(guard.Required)
}

func (tracker *Tracker) listed(column string) []Anomaly {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	anomalies := make([]Anomaly, 0)
	for queryGroup, state := range tracker.groups {
		if !tracker.over(state) || columnOf(state) != column {
			continue
		}
		anomalies = append(anomalies, tracker.rowOf(queryGroup, state))
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
//
// An object in the pool that is dropped here has not left the pool: its
// record goes with it to its next owner, which restores it. It is counted as
// a handover, so this replica's entries, exits and occupancy still add up.
func (tracker *Tracker) Forget(owned map[string]struct{}) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	for queryGroup, state := range tracker.groups {
		if _, kept := owned[queryGroup]; !kept {
			if state.queryCooldown != nil {
				tracker.demotionHandovers++
			}
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

// recordStrategy adds the strategy a trace names to an object's set, one entry
// per strategy. Observations of one round do not all carry both halves of the
// identity -- a trace that names the strategy and not its business arrived
// from the query budget stage on a live deployment -- and keying the set by
// the whole reference made that one strategy two rows on the page, one with
// its business and one without. The entry that names the business is the one
// kept: a later trace with the business replaces the one without, and a later
// trace without it adds nothing to an entry that already has it. The bound
// counts strategies, so a replacement never trips it.
func recordStrategy(strategies map[StrategyRef]struct{}, ref StrategyRef) {
	if ref.StrategyID == "" {
		return
	}
	bare := StrategyRef{StrategyID: ref.StrategyID}
	if ref.BusinessID == "" {
		for known := range strategies {
			if known.StrategyID == ref.StrategyID {
				return
			}
		}
	} else {
		delete(strategies, bare)
	}
	if _, known := strategies[ref]; known || len(strategies) >= maxStrategiesPerQueryGroup {
		return
	}
	strategies[ref] = struct{}{}
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

// NoData is every object whose query is returning no records, in two kinds
// that are two different conversations. KindNoData is an object that has
// returned records in this process and has now returned none for at least the
// degraded-rounds threshold: the data stopped, which is the data side's line.
// KindEmptyEveryRound is an object that has never returned records in this
// process and has completed every round empty for at least
// emptyEveryRoundAfter: the data never came, which is usually the strategy's
// -- a source with nothing there, or an aggregation period the source never
// fills. Under no column either way: the rounds complete and the health
// equation counts the object as healthy, which it is as far as this
// deployment goes.
//
// A source that only speaks when something happens looks exactly like one
// that stopped, and only the run that follows records says which; that is
// why the two are told apart by whether records were ever seen, and why the
// second waits an hour rather than a few rounds.
//
// The hour is measured on the source's clock, first empty Slot to latest
// empty Slot, and on that clock alone. It is the object's question -- has
// this source been silent for an hour of its time -- not "how long has this
// process watched"; and a gate that read the start from one clock and the
// end from the other would fail on the case it most needs to get right: an
// object two hours behind and catching up passes it after its first empty
// round. The same rule makes two hours of empty Slots replayed in a minute
// list the object, which is correct: that hour of source time was empty.
//
// A restart restores the run from the object's record -- the last Slot known
// to have had records, and the first empty Slot of the run -- so the hour
// survives a release rather than starting again at every one; a record from
// before those facts were kept restores only what its last round says.
func (tracker *Tracker) NoData() []Anomaly {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	anomalies := make([]Anomaly, 0)
	for queryGroup, state := range tracker.groups {
		if state.emptyRuns == 0 {
			continue
		}
		anomaly := Anomaly{
			QueryGroup: queryGroup, ReasonCode: "FULL_EMPTY_COMPLETED",
			Since: state.emptySince, SinceFrom: state.emptySinceFrom, Replica: tracker.replica,
		}
		switch {
		case state.sawData && state.emptyRuns >= tracker.degradedRounds && state.lastDataSlot != 0 &&
			time.Duration(state.lastEmptySlot-state.lastDataSlot)*time.Second >= tracker.noDataAfter:
			// The data side's hour, on the same clock as the line below:
			// the rounds are enough and the Slots span the hour since
			// records were last seen. Either alone lists a source that
			// speaks every ten minutes, or a slow one, as stopped.
			anomaly.Kind = KindNoData
		case !state.sawData && state.currentKind == "" && state.emptySinceSlot != 0 &&
			time.Duration(state.lastEmptySlot-state.emptySinceSlot)*time.Second >= tracker.emptyEveryRoundAfter:
			// currentKind empty is "every round": a blocked or failing run
			// after the empty completions does not reset emptyRuns, and an
			// object in such a run is on its own line, not on this one.
			anomaly.Kind = KindEmptyEveryRound
			anomaly.Since, anomaly.SinceFrom = time.Unix(state.emptySinceSlot, 0).UTC(), state.emptySlotFrom
			anomaly.EmptyEveryRound = &EmptyEveryRoundFacts{
				Rounds: state.emptyRuns, Since: anomaly.Since, NeverSawData: true, SinceIsLowerBound: true,
				Cause: EmptyEveryRoundCauseUnknown,
			}
		default:
			continue
		}
		for strategy := range state.strategies {
			anomaly.Strategies = append(anomaly.Strategies, strategy)
		}
		sortStrategies(anomaly.Strategies)
		anomalies = append(anomalies, anomaly)
	}
	sort.Slice(anomalies, func(left, right int) bool {
		if anomalies[left].Since.Equal(anomalies[right].Since) {
			return anomalies[left].QueryGroup < anomalies[right].QueryGroup
		}
		return anomalies[left].Since.Before(anomalies[right].Since)
	})
	return anomalies
}

// upkeepOf is the Plan's upkeep record, made on first sight.
func (state *queryGroupState) upkeepOf(plan StrategyRef) *NoDataMemoryUpkeep {
	if state.upkeep == nil {
		state.upkeep = map[StrategyRef]*NoDataMemoryUpkeep{}
	}
	upkeep := state.upkeep[plan]
	if upkeep == nil {
		upkeep = &NoDataMemoryUpkeep{Plan: plan}
		state.upkeep[plan] = upkeep
	}
	return upkeep
}

// endMemoryRefusal removes one Plan's refusal; the object recovers when the
// last one goes, and the recovery is recorded under the line and fold the
// row was on. Two Plans of one object refused and one of them ending is one
// Plan recovered and an object still listed.
func (tracker *Tracker) endMemoryRefusal(queryGroup string, state *queryGroupState, plan StrategyRef, at time.Time) {
	if len(state.noDataMemory) == 1 {
		tracker.noteMemoryRecovery(queryGroup, state, at)
		state.noDataMemory = nil
		return
	}
	delete(state.noDataMemory, plan)
}

// latestUpkeep is the object's upkeep as the row carries it: the Plan whose
// renewal reached the store most recently (or, before any did, whose memory
// was read most recently), with how many Plans have upkeep at all.
func latestUpkeep(state *queryGroupState) *NoDataMemoryUpkeep {
	if len(state.upkeep) == 0 {
		return nil
	}
	stamp := func(at *time.Time) time.Time {
		if at == nil {
			return time.Time{}
		}
		return *at
	}
	var latest *NoDataMemoryUpkeep
	for _, candidate := range state.upkeep {
		if latest == nil || stamp(candidate.LastAttemptAt).After(stamp(latest.LastAttemptAt)) ||
			(stamp(candidate.LastAttemptAt).Equal(stamp(latest.LastAttemptAt)) && stamp(candidate.LastReadAt).After(stamp(latest.LastReadAt))) {
			latest = candidate
		}
	}
	copied := *latest
	copied.Plans = len(state.upkeep)
	return &copied
}

// noDataTrackingRows is every Plan's last deciding word, smallest strategy
// first, copied so the row does not alias the tracker's state.
func noDataTrackingRows(state *queryGroupState) []NoDataTracking {
	if len(state.noDataTracking) == 0 {
		return nil
	}
	rows := make([]NoDataTracking, 0, len(state.noDataTracking))
	for _, tracking := range state.noDataTracking {
		rows = append(rows, *tracking)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Plan.StrategyID != rows[j].Plan.StrategyID {
			return rows[i].Plan.StrategyID < rows[j].Plan.StrategyID
		}
		return rows[i].Plan.BusinessID < rows[j].Plan.BusinessID
	})
	return rows
}

// NoDataTrackingSummary is what every tracked object's no-data Plans last
// counted, summed: over all tracked objects, listed or not, because the
// horizon acts on healthy objects as much as on listed ones and the question
// is fleet-wide. Nil while no Plan has decided.
func (tracker *Tracker) NoDataTrackingSummary() *NoDataTrackingSummary {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	var summary *NoDataTrackingSummary
	for _, state := range tracker.groups {
		for _, tracking := range state.noDataTracking {
			if summary == nil {
				summary = &NoDataTrackingSummary{}
			}
			summary.add(tracking.summaryOf())
		}
	}
	return summary
}

// planSeriesRows is every Plan's latest matched-series count, smallest
// strategy first, copied so the row does not alias the tracker's state.
func planSeriesRows(state *queryGroupState) []PlanSeriesMatched {
	if len(state.planSeries) == 0 {
		return nil
	}
	rows := make([]PlanSeriesMatched, 0, len(state.planSeries))
	for _, matched := range state.planSeries {
		rows = append(rows, *matched)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Plan.StrategyID != rows[j].Plan.StrategyID {
			return rows[i].Plan.StrategyID < rows[j].Plan.StrategyID
		}
		return rows[i].Plan.BusinessID < rows[j].Plan.BusinessID
	})
	return rows
}

// wireFormatRows is every Plan's wire format, smallest strategy first,
// copied so the row does not alias the tracker's state.
func wireFormatRows(state *queryGroupState) []PlanWireFormat {
	if len(state.wireFormats) == 0 {
		return nil
	}
	rows := make([]PlanWireFormat, 0, len(state.wireFormats))
	for _, format := range state.wireFormats {
		rows = append(rows, *format)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Plan.StrategyID != rows[j].Plan.StrategyID {
			return rows[i].Plan.StrategyID < rows[j].Plan.StrategyID
		}
		return rows[i].Plan.BusinessID < rows[j].Plan.BusinessID
	})
	return rows
}

// NoDataMemory is every object one of whose Plans the store has refused an
// absence memory for. Under no column: the rounds complete and the health
// equation counts the object as healthy, which as far as its threshold
// detection goes it is. What it has lost is silent -- the memory stays at
// the last version written, so a group that goes absent after that is never
// recorded as first absent and its no-data alert never fires -- which is why
// it needs a line of its own rather than a mark on a row nobody opens.
//
// Kept until a write for the Plan is seen to store -- the one positive fact
// recovery is read from here -- or the process forgets the object. A row
// says "refused since", never "recovered": once the write stores, the row is
// gone and the recovery is on the ledger.
func (tracker *Tracker) NoDataMemory() []Anomaly {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	anomalies := make([]Anomaly, 0)
	for queryGroup, state := range tracker.groups {
		if len(state.noDataMemory) == 0 {
			continue
		}
		anomalies = append(anomalies, tracker.memoryRowOf(queryGroup, state))
	}
	sort.Slice(anomalies, func(left, right int) bool {
		if anomalies[left].Since.Equal(anomalies[right].Since) {
			return anomalies[left].QueryGroup < anomalies[right].QueryGroup
		}
		return anomalies[left].Since.Before(anomalies[right].Since)
	})
	return anomalies
}

// memoryRowOf is the object's refused-memory row as the list publishes it:
// one row per object, carrying the latest refusal among its Plans whole and
// how many Plans are refused, since the earliest, with every refused Plan as
// a strategy. Caller holds the lock and has checked a refusal is there.
func (tracker *Tracker) memoryRowOf(queryGroup string, state *queryGroupState) Anomaly {
	var latest *NoDataMemoryRefusal
	first := time.Time{}
	refusals := 0
	strategies := make([]StrategyRef, 0, len(state.noDataMemory))
	for plan, memory := range state.noDataMemory {
		if latest == nil || memory.LastAt.After(latest.LastAt) {
			latest = memory
		}
		if first.IsZero() || memory.FirstAt.Before(first) {
			first = memory.FirstAt
		}
		refusals += memory.Refusals
		if plan.StrategyID != "" {
			strategies = append(strategies, plan)
		}
	}
	memory := *latest
	memory.Plans = len(state.noDataMemory)
	sortStrategies(strategies)
	return Anomaly{
		QueryGroup: queryGroup, Kind: KindNoDataMemoryRefused, ReasonCode: memory.Reason,
		Since: first, SinceFrom: SinceSnapshotContinuity, Replica: tracker.replica,
		ReasonSince: first, ReasonLastAt: memory.LastAt, Consecutive: refusals,
		NoDataMemory: &memory, NoDataMemoryUpkeep: latestUpkeep(state), Strategies: strategies,
		// The object's rounds complete; the row says when the last did, so
		// the loss reads as the memory's and not as the round's.
		LastHealthyAt: state.lastHealthyAt,
	}
}

// GapSkips is every object this replica has seen skip a run of Slots because
// they fell past the replay window, with the last such run. Like PrunedSkips,
// it outlives the rounds around it: the loss is in the object's past and no
// later round can carry it.
// noteSkipEvidence folds the latest completion's evidence into the object's
// current span -- one reading per Slot, the emitter's -- and keeps the
// running count of Slots that were fully executed and then closed without
// their Progress. A Slot without evidence leaves the span's counts alone and
// its last reading ABSENT. Caller holds the lock; the span exists.
func (tracker *Tracker) noteSkipEvidence(queryGroup string, state *queryGroupState, at time.Time) {
	skip := state.gapSkip
	evidence := state.lastEvidence
	if evidence == nil {
		if skip.Evidence != nil {
			skip.Evidence.Reading = model.EvidenceReadingAbsent
			skip.Evidence.PlansApplied, skip.Evidence.PlansTotal = 0, 0
		}
		return
	}
	if skip.Evidence == nil {
		skip.Evidence = &SkipEvidence{}
	}
	skip.Evidence.Reading = evidence.Reading
	skip.Evidence.PlansApplied, skip.Evidence.PlansTotal = evidence.PlansApplied, evidence.PlansTotal
	switch evidence.Reading {
	case model.EvidenceReadingFullyApplied:
		skip.Evidence.SlotsApplied++
		tracker.bookkeeping.Slots++
		tracker.bookkeeping.LastAt = at
		if tracker.bookkeepingObjects == nil {
			tracker.bookkeepingObjects = map[string]struct{}{}
		}
		tracker.bookkeepingObjects[queryGroup] = struct{}{}
		tracker.bookkeeping.Objects = len(tracker.bookkeepingObjects)
	case model.EvidenceReadingMixed:
		skip.Evidence.SlotsPartial++
	case model.EvidenceReadingUnreadable:
		skip.Evidence.SlotsUnreadable++
	}
}

// BookkeepingAbandoned is the running count of interrupted bookkeeping this
// process has seen: Slots an earlier attempt fully executed, closed later by a
// query-free completion. Nil until the first.
func (tracker *Tracker) BookkeepingAbandoned() *BookkeepingFacts {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.bookkeeping.Slots == 0 {
		return nil
	}
	facts := tracker.bookkeeping
	return &facts
}

func (tracker *Tracker) GapSkips() map[string]SkippedSpan {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	var skips map[string]SkippedSpan
	for queryGroup, state := range tracker.groups {
		if state.gapSkip == nil {
			continue
		}
		if skips == nil {
			skips = make(map[string]SkippedSpan, 4)
		}
		skips[queryGroup] = *state.gapSkip
	}
	return skips
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
