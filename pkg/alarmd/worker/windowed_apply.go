// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A Slot is applied window by window once its results no longer fit one
// (decision-020 batch 8, 2.1-2.4). Series are evaluated in the order they
// always were - Plan, then series digest, in the State preflight's batches -
// and before a series' result is merged, a window it would overfill is
// applied first: its Plans' events written and acknowledged, then their
// State, under a Sequence of the window's own State keys. A window is cut
// between series only, so every binding of one (Plan, series) - a Plan of
// several inputs included - is in the window its series is in, and each
// State key belongs to exactly one window.
//
// The gap markers go in a head and a tail. The head, one Sequence of every
// due Plan's gap key, runs before the first window's events and writes each
// Plan's open statement, derived from every binding the completion handed
// the Plan (P2) rather than from the series evaluated so far: a series in
// the fifth window whose input was incomplete must have opened the marker
// before the first window's events went out. The tail, another Sequence of
// the same keys, runs after the last window and writes the clears, the
// no-data memory and ends, the activation recheck and Progress.
//
// A Slot that fits one window never gets here: it is applied whole, inside
// the one Sequence of every key it always ran under (finalizeSummarized),
// which is a head, one window and a tail at once with nothing to nest.
//
// Head, windows and tail are each their own Sequence, never one inside
// another, so a Slot holds at most one at any time. Nested, they deadlock in
// two places. The key gate grants a reservation only at the head of every
// one of its keys' queues (OrderedKeyGate.canGrantLocked): a reservation
// holding a gap key and a State key of this Slot - a supplement takes the
// Slot's whole scope, supplement.go - registered between the head and a
// window waits behind the head on the gap key, the window waits behind it on
// the State key, and the head waits for the window it encloses. And the
// admission semaphore would hold two of its places per Slot
// (KeyedSideEffectSequencer.Sequence), so N Slots each holding one and
// asking for the second wait on each other at full capacity.
//
// Between the head and the tail the gap keys are free. A supplement or a
// replay of the same Plan that runs there finds the marker the head opened
// and no clear: the shape a Slot that crashed between its open statement and
// its clear has always left, which is what an open marker says - events may
// be out, State may not all have landed - and which every reader already
// handles as a gap. Nothing new is stored for it.
//
// A supplement is left on the one-window path: it writes no marker, memory
// or Progress, so there is no head or tail to split it around, and its
// series are the scope a Slot left undecided.

// slotWindows is what a Slot keeps while it is applied window by window.
//
// The window being filled is what the Slot holds of its series: every State
// result in stream.evaluated, every view in stream.state.Items. Once a window
// lands - its output acknowledged and its State applied, or its Plan withheld
// - it lets them go (releaseWindow), with the inputs of its series, and gives
// their bytes back; what is read of the Slot as a whole after that is in the
// summary (slotEvaluationSummary) and in the few derived facts kept here.
type slotWindows struct {
	// prepared is every series the completion handed the evaluation, for the
	// head's open statements (headOpenStatements). It is the same slice the
	// evaluation walks, not a copy; a window landed zeroes the entries of its
	// series, from dropped up to reached, so their inputs go with it.
	prepared []preparedSeries
	// reached is where the series being merged sits in prepared: every one
	// before it is merged, so a window applied now holds none of the later
	// ones. dropped is how far landed windows have let prepared go.
	reached, dropped int
	// present is, by no-data Plan, the groups of the series a landed window
	// let go of (nodata.PresentSet): the absence is judged after every Plan's
	// own series, from which groups reported, and that is all it reads of
	// them - one key and a few bits a group, not the series' dimensions.
	present map[execution.PlanIdentity]*nodata.PresentSet
	// staging is, by no-data Plan whose round writes its whole record, what
	// of it the landed windows staged ahead of the statement
	// (no_data_stage_ahead.go).
	staging map[execution.PlanIdentity]*noDataStageAhead
	// seen is the Plans a landed window's views showed something written
	// before the Slot, for unseenPlans in the tail: the views themselves are
	// gone by then.
	seen map[execution.PlanIdentity]struct{}
	// streamedPeak is the most bindings stream.streamed held since it was
	// last rebuilt (compactStreamed).
	streamedPeak int
	// started is set once the head has run: from then on the Slot is applied
	// window by window, and its tail runs after the evaluation in place of
	// the one Sequence a Slot that fits a window runs.
	started bool
	// single is set when the head found the Slot cannot be cut - its
	// activations could not be read - and nothing was written: no window is
	// cut from then on, and the Slot is applied whole, as it was before
	// windows.
	single bool
	apply  *slotApply
	// headOpened is the Plans the head opened a statement for. Their tail
	// writes no clear: any open statement of the Slot excludes a clear
	// (mergeGapGuardStatements), and one statement per Plan per Slot is what
	// the marker takes.
	headOpened map[execution.PlanIdentity]struct{}
	// headStatements is the open statement each of them was written with,
	// for a later slice to extend (applySliceHead): the marker takes one
	// statement per Plan per Slot, grown, never a second one.
	headStatements map[execution.PlanIdentity]execution.PlanGapMutation
	// bytes is what the output and state phases have retained for the
	// window's results and views since the last window landed, and the input
	// phase for its record id buffers: the window's own share of the Slot.
	// stateBytes is the state phase's part of it, the views, which go back
	// whole; the output phase gives back all it holds but what the Plan
	// results and the summary keep (releaseWindow).
	bytes, stateBytes uint64
	// recordIDs is the record id buffers the window's series were read
	// through (materializeRecordIDs), by Dataset: the last prepared position
	// that read each, and what it holds. A buffer goes when the window of
	// that position lands (dropWindowRecordIDs); one a later series of the
	// next window also reads stays, and moves to that window.
	recordIDs map[*execution.Dataset]*windowRecordIDs
	// blocked is the Plans whose output was not written in a window -
	// deferred, its ACK unknown, or refused whole (planOutputWithheld). None
	// of their later windows is written and their tail writes nothing, as a
	// Slot applied whole skips the rest of such a Plan: a clear over series
	// whose decisions were never sent would report the Plan's history whole
	// while those series sit one Slot behind. A refusal stays TERMINAL and
	// Progress moves past it; the others leave the Slot to be retried.
	//
	// A batch the sink wrote but for some events (planOutputPartial) does
	// not block the Plan: those series keep their State and every other
	// series moves, in this window and the later ones, which is what the
	// same batch does to a Slot applied whole.
	blocked map[execution.PlanIdentity]struct{}
	// replaced is the Plans a Slot over its budget answers UNAVAILABLE for
	// (completeWindowsBeyondSlotBudget): their results past the cursors are
	// not written, and their tail writes the budget's open statement.
	replaced map[execution.PlanIdentity]struct{}
	// largestSeries is the most input one series was charged while the
	// Slot's records arrived, and ceiling the latest its windows may move its
	// deadline to, fixed at the completion (fixWindowCeiling); zero when the
	// execution has no deadline to move.
	largestSeries uint64
	ceiling       time.Time
	// perWindow is s_min, the fewest series a window holds, fixed with the
	// ceiling; ceilingWindows is how many windows the ceiling allows, and
	// raisedWindows how many of them a no-data round's synthetic series
	// added - its absences after its scan, the rest of what it says after it
	// finished (raiseWindowCeiling). Both are on the completion row.
	perWindow                     uint64
	ceilingWindows, raisedWindows uint64
	// evaluating is the Plan whose result the evaluation is producing, and
	// passed the Plans whose series it has moved past.
	evaluating    execution.PlanIdentity
	evaluatingSet bool
	passed        map[execution.PlanIdentity]struct{}
	// resumed is, by Plan, the series this attempt found already written at
	// this Slot and did not evaluate again (resumedSeriesResult).
	resumed map[execution.PlanIdentity]int
	landing planLanding
	decided map[execution.PlanIdentity]struct{}
	count   int
}

// beginEvaluating notes the Plan the next result is for. The Plan before it
// has had every series of the phase evaluated; the Plan itself is in
// progress again, which a no-data Plan is when its synthetic series come
// after every Plan's own.
func (stream *streamedExecution) beginEvaluating(plan execution.PlanIdentity) {
	windows := &stream.windows
	if windows.evaluatingSet && windows.evaluating == plan {
		return
	}
	if windows.passed == nil {
		windows.passed = make(map[execution.PlanIdentity]struct{})
	}
	if windows.evaluatingSet {
		windows.passed[windows.evaluating] = struct{}{}
	}
	delete(windows.passed, plan)
	windows.evaluating, windows.evaluatingSet = plan, true
}

// noteResumed counts a series this attempt resumed rather than evaluated.
func (stream *streamedExecution) noteResumed(plan execution.PlanIdentity) {
	if stream.windows.resumed == nil {
		stream.windows.resumed = make(map[execution.PlanIdentity]int)
	}
	stream.windows.resumed[plan]++
}

// windowReleasable is what landing the window gives back of what the Slot
// holds, as far as the window check counts it: its results and views
// (windows.bytes). Landing gives back more - the inputs of its series, and
// the witnesses its writes consumed - which the check leaves outside the
// window, so it cuts a window no later than the share needs; and a little
// less - the Plan results' own fields, which stay - which a window only
// counts once per Plan.
func (stream *streamedExecution) windowReleasable() uint64 {
	return stream.windows.bytes
}

// windowBoundPercent, between 1 and 99, bounds windows at that percent of
// the share instead of all of it. Zero, as every build but the measurement's
// leaves it, is the whole share: only the alarmd_measure build has a setter
// (window_bound_measure.go), for the reading of how the collector paces when
// the windows leave the soft limit room.
var windowBoundPercent atomic.Uint32

// windowBound is the most the window being filled may hold: the Slot's
// share of the pool, less what the Slot holds outside the window - the
// inputs of the series no window has landed yet, the gap facts, the no-data
// memory, the per-key structures and the summary, which grow across windows
// and stay to the Slot's end. The windows landed before are not in it: they
// gave their bytes back when they landed.
//
// A Slot that never fills a window never asks: its window is the Slot, the
// bound is the share less what it holds outside its results, and the merge
// that would pass it is the merge the share refuses.
func (stream *streamedExecution) windowBound() uint64 {
	share := stream.coordinator.qgShareBytes()
	if share == 0 {
		return math.MaxUint64
	}
	if percent := windowBoundPercent.Load(); percent > 0 && percent < 100 {
		share = share / 100 * uint64(percent)
	}
	outside := stream.retainedTotal() - min(stream.windowReleasable(), stream.retainedTotal())
	if outside >= share {
		return 0
	}
	return share - outside
}

// windowBeforeMerge applies the window being filled when the evaluated
// series next would overfill it: past the per-Slot State mutation or event
// cap, or past the window's bytes bound - what the window holds and what the
// merge would reserve (measured) together above windowBound, which is the
// merge the share would refuse. The series then starts the next window. A
// window holding no State and no event is not applied for it: a series that
// alone does not fit is refused by name, as it always was.
func (stream *streamedExecution) windowBeforeMerge(ctx context.Context, next execution.EvaluationResult, measured mergeBytes) error {
	windows := &stream.windows
	if stream.supplement != nil || windows.single || stream.effects.states+stream.effects.events == 0 {
		return nil
	}
	delta := addedEffectCounts(stream.evaluated, next)
	budget := stream.coordinator.slotBudget()
	full := stream.effects.states+delta.states > budget.MaxStateMutations ||
		stream.effects.events+delta.events > budget.MaxEvents
	if !full && stream.windowReleasable() > 0 {
		// A series that alone would not fit the window the read ahead leaves
		// it is judged again once the read ahead has yielded. No refusal was
		// judged here, so what yieldReadAhead says - read against the last
		// refusal's aheadAtRefusal - is not used: full is worked out again
		// from windowBound once, with no loop.
		if measured.total() > stream.windowBound() {
			stream.yieldReadAhead()
		}
		full = stream.windows.bytes+measured.total() > stream.windowBound()
	}
	if !full {
		return nil
	}
	return stream.cutWindow(ctx)
}

// cutWindow applies the window being filled, every series before
// windows.reached, running the head first if no window has been applied.
func (stream *streamedExecution) cutWindow(ctx context.Context) error {
	defer stream.enter(slotPhaseCut)()
	windows := &stream.windows
	if !windows.started {
		started, err := stream.coordinator.applyWindowHead(ctx, stream)
		if err != nil {
			return err
		}
		if !started {
			windows.single = true
			return nil
		}
	}
	return stream.coordinator.applyWindow(ctx, stream, false)
}

// headOpenStatements is the open statement each due Plan writes this Slot,
// derived from every binding the completion handed it, by the rules each
// result is built by: a series whose PRIMARY did not complete proposes from
// its PRIMARY bindings (mergePrimaryIncompleteSeries), every other series
// from the inputs the guard reads (evaluateLoadedSeries), and a Plan without
// series from its completion-only set (noSeriesPlanResult). The fold is one
// per scope over all of them, which is what the batches' statements union
// to (unionGapOpenStatements).
//
// Every series counts, the ones this attempt will find already written or
// superseded included: which of them those are is only known from their
// State, and the statement has to be out before the first window's events.
func (stream *streamedExecution) headOpenStatements() (map[execution.PlanIdentity]execution.PlanGapMutation, error) {
	incomplete := make(map[execution.PlanIdentity][]execution.NamedInputBinding)
	for _, prepared := range stream.windows.prepared {
		if prepared.due == nil {
			continue
		}
		plan := prepared.due.Identity
		bindings := stream.preparedBindings(prepared)
		if primary := primaryIncompleteIn(bindings); len(primary) != 0 {
			incomplete[plan] = append(incomplete[plan], primary...)
			continue
		}
		for _, binding := range bindings {
			if execution.InputIncompleteForGuard(binding) {
				incomplete[plan] = append(incomplete[plan], binding)
			}
		}
	}
	if stream.readInSlices() {
		// A slice's PRIMARY that did not complete may have left out series
		// of a Plan this slice holds none of, and no other slice reads them:
		// its completion proposes the Plan's statement, as its series would
		// have had the query returned them.
		for _, binding := range stream.completionInputs.bindings {
			if binding.Role == execution.InputRolePrimary && binding.Completeness != execution.CompletenessFull {
				incomplete[binding.Consumer.Plan] = append(incomplete[binding.Consumer.Plan], binding)
			}
		}
	}
	for _, due := range stream.header.DuePlans {
		if stream.planSeriesCount(due.Identity) != 0 {
			continue
		}
		bindings := stream.noSeriesBindings(due)
		if _, found := firstNonFullPrimary(bindings); found {
			incomplete[due.Identity] = bindings
		}
	}
	opens := make(map[execution.PlanIdentity]execution.PlanGapMutation, len(incomplete))
	for _, due := range stream.header.DuePlans {
		bindings := incomplete[due.Identity]
		if len(bindings) == 0 {
			continue
		}
		mutation, err := stream.completionGapMutationFor(due, bindings)
		if err != nil {
			return nil, err
		}
		opens[due.Identity] = mutation
	}
	return opens, nil
}

// applyWindowHead runs the head: the activations read, the first-seen
// judgement made, and every Plan's open statement written before the first
// window's events. It returns false, having written nothing, when the Slot
// cannot be cut.
//
// A Plan that could be first-seen is judged here from every one of its
// series' State, read ahead of the windows (firstSeenPrepass). The marker
// recording that a first-seen Plan ran is not written here but in the tail,
// and only when no other statement of the Plan is written this Slot
// (windowedTail): a Plan the budget cuts after some of its windows gets its
// budget statement in the tail, and a marker the head had written at the same
// version would refuse it as another statement, on every retry. A retry of
// the Slot judges the Plan again as this attempt did, since State written at
// the Slot's own version is not something from before it (viewsSeenBefore).
//
// A first-seen Plan whose Slot lands windows and never reaches its tail -
// its retries run out, or the Slot is skipped and Progress moves past it -
// leaves State at that Slot's version and no marker. The next Slot reads that
// State as written before it (firstSeenPrepass), so the Plan is not
// first-seen there: it is still forced to warm up and has no marker, so it
// joins the changed Plans, writes nothing that Slot, and the protection path
// writes its WARMING marker - it warms up rather than being taken for a Plan
// that ran, which is the direction first_seen.go errs in. The reads hold the
// head's gap keys, which a supplement of the same Plans waits on as it waits
// on the head's writes.
func (coordinator *SlotExecutionCoordinator) applyWindowHead(ctx context.Context, stream *streamedExecution) (bool, error) {
	opens, err := stream.headOpenStatements()
	if err != nil {
		return false, err
	}
	started := false
	err = coordinator.ports.Sequencer.Sequence(ctx, sequencingScope(stream.header, nil, stream.gapItems), func(sequenceCtx context.Context) error {
		apply, read := coordinator.readSlotActivations(sequenceCtx, stream.request, stream.header, stream.gaps)
		if !read {
			return nil
		}
		// Only the candidates' answer depends on unseen; every other forced
		// Plan is warmed up whatever it is.
		var unseen map[execution.PlanKey]struct{}
		if candidates := firstSeenCandidates(apply.forced, stream.gaps); len(candidates) != 0 {
			if err := stream.firstSeenPrepass(sequenceCtx, candidates); err != nil {
				return err
			}
			unseen = stream.unseenPlans()
		}
		apply.judgeForced(unseen)
		apply.keys = make(slotStateKeys)
		headOpened := make(map[execution.PlanIdentity]struct{}, len(opens))
		headStatements := make(map[execution.PlanIdentity]execution.PlanGapMutation, len(opens))
		for _, due := range sortedDuePlans(stream.header.DuePlans) {
			if apply.changed(due) {
				apply.withheldNoData[due.Identity] = struct{}{}
				continue
			}
			open, found := opens[due.Identity]
			if !found {
				continue
			}
			if err := coordinator.admit(sequenceCtx, stream.request, due); err != nil {
				return err
			}
			headOpened[due.Identity] = struct{}{}
			statement := execution.PlanEvaluationResult{Plan: due.Identity,
				GuardBeforeEvents: []execution.PlanGapMutation{open}}
			if _, err := apply.planGapsBefore(sequenceCtx, due, &statement); err != nil {
				return err
			}
			headStatements[due.Identity] = open
		}
		stream.windows.apply, stream.windows.headOpened, stream.windows.started = apply, headOpened, true
		stream.windows.headStatements = headStatements
		started = true
		return nil
	})
	return started, err
}

// windowPart is one Plan's part of a window.
type windowPart struct {
	due     execution.DuePlan
	results []execution.StateEvaluation
}

// windowParts is the window being filled: by Plan, the State results the
// Slot holds - every earlier window let its own go - and every Plan with a
// view in the window even when it has no result there: its frozen series are
// renewed from that window.
func (stream *streamedExecution) windowParts(views []execution.RuntimeStateView) ([]windowPart, error) {
	parts := make(map[execution.PlanIdentity]*windowPart)
	partFor := func(plan execution.PlanIdentity) (*windowPart, error) {
		if part, found := parts[plan]; found {
			return part, nil
		}
		due, found := duePlan(stream.header.DuePlans, plan)
		if !found {
			return nil, errors.New("alarmd worker: evaluated plan is not due")
		}
		part := &windowPart{due: due}
		parts[plan] = part
		return part, nil
	}
	for index := range stream.evaluated.Plans {
		plan := &stream.evaluated.Plans[index]
		if len(plan.StateResults) == 0 {
			continue
		}
		part, err := partFor(plan.Plan)
		if err != nil {
			return nil, err
		}
		part.results = plan.StateResults
	}
	for _, view := range views {
		if _, err := partFor(view.Identity.Plan); err != nil {
			return nil, err
		}
	}
	ordered := make([]windowPart, 0, len(parts))
	for _, part := range parts {
		ordered = append(ordered, *part)
	}
	sort.Slice(ordered, func(left, right int) bool {
		return lessPlanIdentity(ordered[left].due.Identity, ordered[right].due.Identity)
	})
	return ordered, nil
}

// applyWindow writes the window being filled, as its own Sequence of the
// window's State keys: Plan by Plan, admission, the events written and
// acknowledged, then the State. Frozen series are renewed from this window's
// own views and results (P1): a key this window read and does not write.
// Reading them off views kept from earlier windows against this window's
// results would renew a key an earlier window wrote, and the Slot would
// write it twice.
func (coordinator *SlotExecutionCoordinator) applyWindow(ctx context.Context, stream *streamedExecution, final bool) error {
	defer stream.enter(slotPhaseApply)()
	windows := &stream.windows
	apply := windows.apply
	views := stream.state.Items
	items := stream.stateItems
	written := apply.written
	parts, err := stream.windowParts(views)
	if err != nil {
		return err
	}
	if len(parts) > 0 {
		windowState := execution.StatePreflightResult{Items: views}
		stateIndex := indexStatePreflight(windowState)
		windowCtx := withPlanLanding(withNoDataAbsenceStarts(ctx, stream.noDataAbsenceStarts), &windows.landing)
		err = coordinator.ports.Sequencer.Sequence(windowCtx, sequencingScope(stream.header, items, nil), func(sequenceCtx context.Context) error {
			// From open: the window before left the commit (windowBoundary),
			// and a watchdog that claimed the Slot since stops it here,
			// before anything of this window is written.
			if err := execution.EnterCommit(sequenceCtx, execution.SlotStageOutput); err != nil {
				return err
			}
			for _, part := range parts {
				plan := part.due.Identity
				if apply.changed(part.due) {
					continue
				}
				if _, blocked := windows.blocked[plan]; blocked {
					continue
				}
				if _, replaced := windows.replaced[plan]; replaced {
					continue
				}
				output, err := apply.applyPlanState(sequenceCtx, part.due, part.results, windowState, stateIndex)
				if err != nil {
					return err
				}
				if output == planOutputWithheld {
					if windows.blocked == nil {
						windows.blocked = make(map[execution.PlanIdentity]struct{})
					}
					windows.blocked[plan] = struct{}{}
				}
			}
			return nil
		})
	}
	// The window has landed, or its Plans are withheld, or the attempt ends
	// on err: either way nothing reads its results, views or inputs again.
	releaseErr := stream.releaseWindow(ctx, final, apply.written-written)
	// The window's State and event counts go back to the pool once it is
	// applied: the per-Slot caps bound one window, not the Slot. The gap
	// count stays, because the gap statements are the head's and the tail's.
	stream.coordinator.releaseEffects(effectCounts{states: stream.effects.states, events: stream.effects.events})
	stream.effectsSpent.states += stream.effects.states
	stream.effectsSpent.events += stream.effects.events
	stream.effects.states, stream.effects.events = 0, 0
	windows.count++
	stream.decideLanding(ctx, final)
	if err != nil {
		return err
	}
	if releaseErr != nil {
		return releaseErr
	}
	stream.windowBoundary(ctx)
	return nil
}

// windowStepBound (T_w) is how long one window of a Slot applied window by
// window may take, from the boundary before it to the one after: the
// evaluation of its series and its writes. At each boundary the Slot's
// deadline is moved to that far ahead (windowBoundary), so the watchdog -
// which claims an execution outside its commit once it is past its deadline
// by its grace (cmd/alarmd executionPastDeadlineGrace) - lets go of a window
// that is stuck in itself, and not of a large Slot still making progress on a
// slow Redis or Kafka, which past its query deadline would otherwise be
// declined at its first boundary and its Query Group moved every round.
//
// A minute, the grace itself: that grace is what the watchdog gives a whole
// Slot past its deadline for what remains after its query - its evaluation -
// and a window evaluates a part of the Slot's series, while its writes run
// under the State, event and gap clients' own bounded timeouts. A stuck
// window is claimed T_w and the grace after the boundary before it, two
// minutes, inside the five the liveness probe allows every execution slot
// stuck at once (executionsStuckBound). Not a configuration key: what it
// bounds is the code's own pace, not the deployment's.
const windowStepBound = time.Minute

// fixWindowCeiling fixes, at the completion, the latest the Slot's windows
// may move its deadline to: the deadline then, plus T_w for each window the
// Slot can need, ceil(S / s_min). S is the series the completion handed the
// evaluation; s_min the fewest series a window holds - the window bound now
// over the most input one series was charged, at least one, and no more than
// the per-Slot State cap, since each series writes one key, nor the event cap
// over the most events one series can send in this Slot (eventsPerSeries).
// However the Slot is cut, its windows are no more than that, so moving the
// deadline at each boundary never lets a Slot that really is stuck out-wait
// the watchdog: the latest it can be claimed is the deadline, the grace and
// ceil(S / s_min) T_w (decision-020 batch 8, 2.2 step 6 b). A no-data round's
// synthetic series - its absences, the groups no stored entry named, and its
// present groups' NORMAL - add their own windows once the round has counted
// them (raiseWindowCeiling).
func (stream *streamedExecution) fixWindowCeiling(ctx context.Context, series int) {
	deadline := execution.ExecutionDeadline(ctx)
	if deadline.IsZero() || series == 0 || stream.supplement != nil {
		return
	}
	perWindow := uint64(series)
	if largest := stream.windows.largestSeries; largest > 0 {
		perWindow = stream.windowBound() / largest
	}
	budget := stream.coordinator.slotBudget()
	if limit := budget.MaxStateMutations; limit > 0 {
		perWindow = min(perWindow, limit)
	}
	if limit := budget.MaxEvents; limit > 0 {
		perWindow = min(perWindow, limit/stream.eventsPerSeries())
	}
	perWindow = max(perWindow, 1)
	windows := (uint64(series) + perWindow - 1) / perWindow
	stream.windows.perWindow, stream.windows.ceilingWindows = perWindow, windows
	stream.windows.ceiling = deadline.Add(time.Duration(windows*stream.sliceCeilingFactor()) * windowStepBound)
}

// raiseSliceCeiling moves a Slot read in slices' ceiling as it reads them:
// the first slice's windows once for every slice read or let go so far,
// never past 2*K_max-1 of them (§7.5).
func (stream *streamedExecution) raiseSliceCeiling(ctx context.Context) {
	deadline := execution.ExecutionDeadline(ctx)
	if deadline.IsZero() || stream.windows.ceilingWindows == 0 {
		return
	}
	stream.windows.ceiling = deadline.Add(time.Duration(stream.windows.ceilingWindows*stream.sliceCeilingFactor()) * windowStepBound)
}

// eventsPerSeries is the most events one prepared series can send in this
// Slot: one per PRIMARY record it holds, at least one. The trigger decides one
// event at most per record, whatever its Levels (evaluation/evaluator.go:672,
// keptEvents, one TriggerEvent or none), and a series' events are its records'
// put together (evaluation/evaluator.go:861, appendKept over the series'
// PRIMARY records): so the bound is per record, not per Level, and the
// records are what the completion handed it. A synthetic no-data series holds
// one record.
func (stream *streamedExecution) eventsPerSeries() uint64 {
	most := max(stream.primaryRecords, 1)
	for _, prepared := range stream.windows.prepared {
		for _, input := range prepared.inputs {
			for _, binding := range input.Inputs {
				if binding.Role == execution.InputRolePrimary && binding.View != nil {
					most = max(most, uint64(binding.View.Len()))
				}
			}
		}
	}
	return most
}

// raiseWindowCeiling moves the ceiling for synthetic no-data series a round
// is about to say: the absent names its scan found, then, once it finished,
// the groups no stored entry named and the present groups' NORMAL. They are
// said after every prepared one and were not in S, so they get their own
// ceil(synthetic / s_min) windows. Still bounded: a round says at most one
// series a group, and a memory holds at most its Plan's max_groups groups.
func (stream *streamedExecution) raiseWindowCeiling(synthetic int) {
	windows := &stream.windows
	if windows.ceiling.IsZero() || synthetic == 0 || windows.perWindow == 0 {
		return
	}
	more := (uint64(synthetic) + windows.perWindow - 1) / windows.perWindow
	windows.ceiling = windows.ceiling.Add(time.Duration(more) * windowStepBound)
	windows.ceilingWindows += more
	windows.raisedWindows += more
}

// windowBoundary is a window landed: the Slot's deadline moved to T_w past
// now and no later than the ceiling, then the commit left. In that order, so
// the watchdog never finds the Slot open under a deadline already behind it.
//
// Only here, after the window's applies returned - its State applied, its
// frozen series renewed and its held frames released - and never after its
// output alone: between two windows nothing the Slot acknowledged waits on a
// write it has yet to make, which is the shape a crash between them leaves
// and a retry resumes series by series. The next window and the tail enter
// the commit again from open (applyWindow, finishWindowedSlot), and once the
// watchdog claimed the Slot they write nothing and return
// execution.ErrExecutionDeclined. A Slot past its ceiling is not moved: it
// stays as late as it is, and the next tick between two windows claims it.
func (stream *streamedExecution) windowBoundary(ctx context.Context) {
	if ceiling := stream.windows.ceiling; !ceiling.IsZero() {
		now := stream.coordinator.now()
		target := now.Add(windowStepBound)
		if ceiling.Before(target) {
			target = ceiling
		}
		if target.After(now) {
			execution.ExtendDeadline(ctx, target, now)
		}
	}
	execution.LeaveCommit(ctx, execution.SlotStageEvaluate)
}

// loadedKeyHeldBytes is what a Slot holds for each State key it loaded,
// beside the view, until the Slot ends: the witness the State store keeps of
// the read for the apply (state.runtimeWitnessEntryBytes, 320 B: its key and
// value as a group's map keeps them, unexported there, so restated here, plus
// the 64-character series digest it alone keeps alive), and the key's entry
// in the Slot's repeat check (slotStateKeys) or, for a Slot applied whole, in
// the stateItems its one Sequence is scoped by. The witness goes when an apply
// consumes it, which releaseWindow gives back per key a window wrote; a key
// renewed or left unwritten keeps it to the Slot's end, as the store does.
var loadedKeyHeldBytes = stateWitnessHeldBytes +
	(uint64(unsafe.Sizeof(execution.StateKeyIdentity{})+unsafe.Sizeof(""))+1)*8/7

const stateWitnessHeldBytes = 320 + 64

// releaseWindow lets go of what a landed window held and gives its bytes
// back, phase by phase (decision-020 batch 8, 2.2 step 4, 2.6):
//
//   - State: its views, from stream.state.Items and stream.stateItems, and
//     their charge (windows.stateBytes); the witnesses its writes consumed,
//     one per key written. Whether a Plan saw anything written before the
//     Slot is kept first (noteSeenViews), for unseenPlans in the tail.
//   - Output: each Plan's State results - mutations and events with their
//     message-less identities - Level outcomes and no-data end updates. The
//     summary holds what is read of them after this; what stays is each Plan
//     result's own fields - disposition, reason, gap statements, coverage -
//     which the tail writes and decides from. The phase keeps what those and
//     the summary are counted at and gives back the rest.
//   - Input: the bindings of every prepared series before reached (all of
//     them on the last window), from stream.streamed and prepared, with their
//     host overlays (dropWindowInputs); and the record id buffers those
//     series were read through (dropWindowRecordIDs).
//
// Effect counts went back already, by applyWindow.
func (stream *streamedExecution) releaseWindow(ctx context.Context, final bool, written int) error {
	windows := &stream.windows
	stream.noteSeenViews(stream.state.Items)
	stream.state.Items, stream.stateItems = nil, nil
	stream.unretain(retainPhaseState, windows.stateBytes)
	if written > 0 {
		consumed := min(uint64(written)*stateWitnessHeldBytes, stream.keysHeld)
		stream.keysHeld -= consumed
		stream.unretain(retainPhaseState, consumed)
	}
	var kept uint64
	for index := range stream.evaluated.Plans {
		plan := &stream.evaluated.Plans[index]
		plan.StateResults, plan.LevelOutcomes, plan.NoDataEnds = nil, nil, nil
		// Counted as newEffectBytes counts a Plan result's first series.
		kept += 2 * retainedObjectBytes(*plan)
	}
	if held, keep := stream.retainedByPhase[retainPhaseOutput], kept+stream.summaryHeld; held > keep {
		stream.unretain(retainPhaseOutput, held-keep)
	}
	if note := windowSizesForMeasurement.Load(); note != nil {
		through := windows.reached
		if final {
			through = len(windows.prepared)
		}
		(*note)(windows.bytes, through-windows.dropped)
	}
	windows.bytes, windows.stateBytes = 0, 0
	through := windows.reached
	if final {
		through = len(windows.prepared)
	}
	stream.dropWindowRecordIDs(through, final)
	return stream.dropWindowInputs(ctx, through)
}

// noteSeenViews keeps, for each due Plan, whether the views show anything of
// it written before the Slot, by the rule unseenPlans reads them by.
func (stream *streamedExecution) noteSeenViews(views []execution.RuntimeStateView) {
	if len(views) == 0 {
		return
	}
	windows := &stream.windows
	for _, due := range stream.header.DuePlans {
		if _, seen := windows.seen[due.Identity]; seen {
			continue
		}
		version, err := execution.BuildApplyVersion(stream.header.Contract, due.StateApplyEpoch)
		if err != nil {
			continue
		}
		if viewsSeenBefore(due, version, views) {
			if windows.seen == nil {
				windows.seen = make(map[execution.PlanIdentity]struct{})
			}
			windows.seen[due.Identity] = struct{}{}
		}
	}
}

// seriesInputHold is what one series' batches were charged to the input
// phase, and how many of their streamed bindings are still held.
type seriesInputHold struct {
	bytes uint64
	// bindings is the series' streamed bindings still in stream.streamed.
	bindings int
}

// holdInput adds a batch's charge to its series' hold.
func (stream *streamedExecution) holdInput(series execution.SeriesIdentityDigest, bytes uint64) *seriesInputHold {
	if stream.inputHolds == nil {
		stream.inputHolds = make(map[execution.SeriesIdentityDigest]*seriesInputHold)
	}
	hold := stream.inputHolds[series]
	if hold == nil {
		hold = &seriesInputHold{}
		stream.inputHolds[series] = hold
	}
	hold.bytes += bytes
	stream.windows.largestSeries = max(stream.windows.largestSeries, hold.bytes)
	return hold
}

// dropWindowInputs lets go of the inputs of the prepared series from dropped
// up to through, every one merged into a window that landed, and gives back
// what they were charged: a series' batches once none of its streamed
// bindings is held - one series digest can feed several Plans, and a binding
// no Plan's series consumes stays to the Slot's end. Named outputs stay to the Slot's end (the
// events' detection snapshots read them), as do the completion-only
// bindings.
//
// A no-data Plan's series are judged present after every Plan's own, from
// the groups their PRIMARY dimensions project onto: each group's key is kept
// in the Plan's present set (presentKeyHeldBytes), charged in place of the
// inputs it came from.
func (stream *streamedExecution) dropWindowInputs(ctx context.Context, through int) error {
	windows := &stream.windows
	through = min(through, len(windows.prepared))
	if through <= windows.dropped {
		return nil
	}
	windows.streamedPeak = max(windows.streamedPeak, len(stream.streamed))
	var released, kept uint64
	for index := windows.dropped; index < through; index++ {
		entry := windows.prepared[index]
		if entry.due.CompiledPlan != nil && entry.due.CompiledPlan.NoData() != nil {
			for _, dimensions := range stream.seriesDimensionsFor(windows.prepared[index:index+1], entry.due.Identity) {
				if key, fresh := windows.presentSet(*entry.due).Add(dimensions); fresh {
					kept += presentKeyHeldBytes(key)
					stream.stageAhead(ctx, *entry.due, key)
				}
			}
		}
		released += stream.dropSeriesInputs(entry.due.Identity, entry.identity)
		windows.prepared[index] = preparedSeries{}
	}
	windows.dropped = through
	stream.compactStreamed()
	if released >= kept {
		stream.unretainInput(released - kept)
		return nil
	}
	return stream.reserveRetainedInput(ctx, kept-released)
}

// dropSeriesInputs takes one prepared series' streamed bindings out of
// stream.streamed, with its host overlay, and returns the bytes given back
// by the series' hold if that was its last binding.
func (stream *streamedExecution) dropSeriesInputs(plan execution.PlanIdentity, series execution.SeriesIdentityDigest) uint64 {
	var released uint64
	for _, consumer := range stream.prepared.consumersByPlan[plan] {
		for _, requirement := range stream.prepared.requirementsByConsumer[consumer] {
			key := streamedInputKey{consumer: consumer, series: series, requirement: requirement.RequirementID}
			if _, found := stream.streamed[key]; !found {
				continue
			}
			delete(stream.streamed, key)
			hold := stream.inputHolds[series]
			if hold == nil {
				continue
			}
			hold.bindings--
			if hold.bindings > 0 {
				continue
			}
			delete(stream.inputHolds, series)
			released += hold.bytes
		}
	}
	if overlays := stream.hostOverlays[plan]; overlays != nil {
		delete(overlays, series)
	}
	return released
}

// compactStreamed rebuilds stream.streamed once it holds half of what it did:
// a map does not shrink as its keys are deleted, and its buckets are most of
// what a binding costs once its Dataset is gone, so the bytes a landed window
// gave back would otherwise stay held by the map. Rebuilt at each halving,
// it costs a copy of what is left, which sums to the map once over the Slot.
func (stream *streamedExecution) compactStreamed() {
	windows := &stream.windows
	if stream.streamed == nil || len(stream.streamed)*2 > windows.streamedPeak {
		return
	}
	rebuilt := make(map[streamedInputKey]streamedInput, len(stream.streamed))
	for key, entry := range stream.streamed {
		rebuilt[key] = entry
	}
	stream.streamed, windows.streamedPeak = rebuilt, len(rebuilt)
}

// finishWindowedSlot applies the last window and runs the tail.
func (coordinator *SlotExecutionCoordinator) finishWindowedSlot(ctx context.Context, stream *streamedExecution) (execution.SlotExecutionResult, error) {
	if err := coordinator.applyWindow(ctx, stream, true); err != nil {
		return execution.SlotExecutionResult{}, err
	}
	ctx = withUnseenPlans(withNoDataEnds(withNoDataAbsenceStarts(ctx, stream.noDataAbsenceStarts), stream.noDataEndRound()), stream.unseenPlans())
	var result execution.SlotExecutionResult
	err := coordinator.ports.Sequencer.Sequence(ctx, sequencingScope(stream.header, nil, stream.gapItems), func(sequenceCtx context.Context) error {
		// From open, as each window: the last one left the commit.
		if err := execution.EnterCommit(sequenceCtx, execution.SlotStageState); err != nil {
			return err
		}
		var tailErr error
		result, tailErr = stream.windows.apply.windowedTail(sequenceCtx, stream)
		return tailErr
	})
	return result, err
}

// windowedTail writes what follows the last window: each Plan's clear, or
// the open statement of a Plan the head opened none for and the budget
// answered UNAVAILABLE, then everything the one-window path writes after its
// Plans.
func (apply *slotApply) windowedTail(ctx context.Context, stream *streamedExecution) (execution.SlotExecutionResult, error) {
	windows := &stream.windows
	for _, planResult := range sortedPlanResults(stream.evaluated) {
		due, ok := duePlan(apply.header.DuePlans, planResult.Plan)
		if !ok {
			return execution.SlotExecutionResult{}, errors.New("alarmd worker: evaluated plan is not due")
		}
		if apply.changed(due) {
			continue
		}
		if _, blocked := windows.blocked[planResult.Plan]; blocked {
			continue
		}
		if _, opened := windows.headOpened[planResult.Plan]; !opened {
			wrote, err := apply.tailStatements(ctx, due, planResult)
			if err != nil {
				return execution.SlotExecutionResult{}, err
			}
			// A first-seen Plan's marker, when nothing else of it is written
			// this Slot: see applyWindowHead.
			if _, first := apply.firstSeen[due.Key()]; first && !wrote {
				if err := apply.coordinator.admit(ctx, apply.request, due); err != nil {
					return execution.SlotExecutionResult{}, err
				}
				if err := apply.firstSeenMarker(ctx, due, GapSiteAfterState); err != nil {
					return execution.SlotExecutionResult{}, err
				}
			}
		}
		apply.noteRetryPending(planResult)
	}
	census := stream.seriesCensus
	census.Written += apply.written
	return apply.complete(ctx, stream.bindings, &stream.summary, stream.noDataMutations,
		stream.evaluated.Result, stream.evaluated.ReasonCode, stream.queryEvidence, census, stream.targetSummaries())
}

// tailStatements writes a Plan's statement in the tail: its clear, as merged
// across the windows by agreedGapClearStatement - a window that proposed no
// clear does not veto one, two different ones are refused by name - or, for
// a Plan the budget answered UNAVAILABLE, the open statement that came with
// it. Nothing of such a Plan was written past what its earlier windows
// carried, so an open written here precedes no event it guards that the
// head could have preceded. It reports whether the Plan has a statement of
// its own this Slot, written here or by an earlier attempt.
func (apply *slotApply) tailStatements(ctx context.Context, due execution.DuePlan, planResult execution.PlanEvaluationResult) (bool, error) {
	before, after := planResult.GuardBeforeEvents, planResult.GuardAfterState
	if len(before) != 0 {
		after = nil
	}
	if len(before)+len(after) == 0 {
		return false, nil
	}
	gapRetention, err := generationRetentionOf(due)
	if err != nil {
		return true, err
	}
	before = uncommittedGapMutations(apply.loadedGaps, before)
	after = uncommittedGapMutations(apply.loadedGaps, after)
	apply.coordinator.observeDuplicatedGapStatements(ctx, apply.request,
		execution.PlanEvaluationResult{Plan: planResult.Plan, GuardBeforeEvents: before, GuardAfterState: after})
	if len(before)+len(after) == 0 {
		return true, nil
	}
	if err := apply.coordinator.admit(ctx, apply.request, due); err != nil {
		return true, err
	}
	if err := apply.coordinator.applyGap(ctx, apply.request.Operation, apply.request.Contract, before, gapRetention, GapSiteBeforeEvents); err != nil {
		return true, err
	}
	return true, apply.planGapAfter(ctx, after, gapRetention)
}

// completeWindowsBeyondSlotBudget answers a Slot over a per-Slot cap once
// windows of it are written. Those windows stay written: their events are
// out and their State landed, and voiding them would only leave the store
// saying one thing and the completion another. What it does:
//
//   - A Plan whose every series the evaluation got past keeps its results;
//     the ones no window took yet go out in the last window.
//   - Every other Plan - the one the refused series belongs to, and every
//     Plan the evaluation had not reached - is answered as a Slot over its
//     budget answers every Plan: UNAVAILABLE, with a Plan-wide budget gap,
//     written in the tail unless the head already opened its marker. None of
//     its results past what earlier windows took is written. Its windows
//     already written stay, and its marker records that the rest of it was
//     not decided.
//   - The no-data memory is applied only for the Plans of the first kind,
//     by the withheld rule (decision-020 batch 8, 2.4): every window of
//     theirs lands, so what they decided was said whole. Not applying it
//     would have the next round decide again from the memory before this
//     one, and send again the absences a window here already sent. A Plan of
//     the second kind writes none, nor its ends: its present series are only
//     those its landed windows got to, and a memory written from them would
//     record every series the evaluation never reached as absent. It is
//     withheld as a Plan whose output did not all land is.
//
// The released counts are the window's, so the replacement gaps are counted
// against an empty window; the retained bytes stay until the stream releases
// everything.
func (stream *streamedExecution) completeWindowsBeyondSlotBudget(ctx context.Context) error {
	stream.coordinator.releaseEffects(stream.effects)
	stream.effectsSpent.states += stream.effects.states
	stream.effectsSpent.events += stream.effects.events
	stream.effectsSpent.gaps += stream.effects.gaps
	stream.effects = effectCounts{}
	windows := &stream.windows
	landed := make(map[execution.PlanIdentity]struct{}, len(windows.passed))
	for _, due := range stream.header.DuePlans {
		if _, passed := windows.passed[due.Identity]; passed {
			landed[due.Identity] = struct{}{}
			continue
		}
		if windows.replaced == nil {
			windows.replaced = make(map[execution.PlanIdentity]struct{})
		}
		windows.replaced[due.Identity] = struct{}{}
		windows.apply.withheldNoData[due.Identity] = struct{}{}
	}
	stream.closeNoDataBeyondSlotBudget(ctx, landed)
	reason := execution.ReasonCode(contract.ReasonExecutionBudgetExhausted)
	for _, due := range stream.header.DuePlans {
		if _, replaced := windows.replaced[due.Identity]; !replaced {
			continue
		}
		mutation, err := stream.gapMutationForReasons(due, map[execution.GapScope]execution.ReasonCode{{}: reason})
		if err != nil {
			return err
		}
		result := execution.EvaluationResult{Contract: stream.header.Contract, Result: observability.ResultDegraded,
			ReasonCode: reason, Plans: []execution.PlanEvaluationResult{{Plan: due.Identity,
				Disposition: execution.PlanUnavailable, ReasonCode: reason,
				GuardBeforeEvents: []execution.PlanGapMutation{mutation}}}}
		if err := stream.mergeProvisional(ctx, result, 0); err != nil {
			return err
		}
	}
	return nil
}

// decideLanding records, for every Plan whose windows are all applied, whether
// this attempt got it whole into the store (appliedPlanRecorder). Its keys
// are counted across every window, and the series this attempt resumed count
// as landed - they are in the store at this Slot - so a retry that resumes
// the windows an earlier attempt wrote and writes the rest records the Plan
// as applied. A Plan is decided once its last window is applied: before then
// the keys of its later windows have not been sent, and counting what landed
// so far would vouch for them. A no-data Plan's synthetic series come after
// every Plan's own, so it is decided with the last window.
func (stream *streamedExecution) decideLanding(ctx context.Context, final bool) {
	windows := &stream.windows
	recorder := appliedPlansFrom(ctx)
	for _, due := range stream.header.DuePlans {
		plan := due.Identity
		if _, decided := windows.decided[plan]; decided {
			continue
		}
		_, passed := windows.passed[plan]
		noData := due.CompiledPlan != nil && due.CompiledPlan.NoData() != nil
		if !final && (!passed || noData) {
			continue
		}
		if windows.decided == nil {
			windows.decided = make(map[execution.PlanIdentity]struct{})
		}
		windows.decided[plan] = struct{}{}
		resumed := windows.resumed[plan]
		landed, expected := windows.landing.landed[plan]+resumed, windows.landing.expected[plan]+resumed
		if expected == 0 {
			continue
		}
		_, blocked := windows.blocked[plan]
		_, replaced := windows.replaced[plan]
		if blocked || replaced || landed != expected {
			recorder.recordIncomplete(plan)
			continue
		}
		recorder.recordApplied(plan)
	}
}

// planLanding is a windowed Slot's count, by Plan, of the keys its applies
// sent and of those the store wrote, kept across windows: applyState decides
// a Plan per call, which for a Plan written in several windows would decide
// it off its first one.
type planLanding struct {
	landed, expected map[execution.PlanIdentity]int
}

func (landing *planLanding) add(plan execution.PlanIdentity, landed, expected int) {
	if landing.landed == nil {
		landing.landed = make(map[execution.PlanIdentity]int)
		landing.expected = make(map[execution.PlanIdentity]int)
	}
	landing.landed[plan] += landed
	landing.expected[plan] += expected
}

type planLandingKey struct{}

func withPlanLanding(ctx context.Context, landing *planLanding) context.Context {
	return context.WithValue(ctx, planLandingKey{}, landing)
}

func planLandingFrom(ctx context.Context) *planLanding {
	landing, _ := ctx.Value(planLandingKey{}).(*planLanding)
	return landing
}

// StateKeyRepeatedInSlotError is a Slot asking the store to write or renew
// one State key a second time.
//
// A Slot writes each of its State keys at most once: the ApplyVersion carries
// the epoch, the evaluation time and the Slot digest and nothing finer, so a
// second write of one key in one Slot is refused by the store as another
// statement at the same version, or as a revision conflict. Windows are cut
// between series and every series is one key, so this can only be a window
// cut through a series or a view kept into the next window; it is named here,
// before anything of the repeat is sent, rather than left to read as the
// store's conflict.
type StateKeyRepeatedInSlotError struct {
	Identity execution.StateKeyIdentity
	// First and Again are the requests the key was in: apply or renewal.
	First, Again string
}

func (err *StateKeyRepeatedInSlotError) Error() string {
	return fmt.Sprintf("alarmd worker: State key %s/%s/%s asked for in a %s after a %s of the same Slot",
		err.Identity.Plan.StrategyID, err.Identity.StateGeneration, err.Identity.SeriesIdentityDigest, err.Again, err.First)
}

// slotStateKeys is every State key a windowed Slot has sent to an apply or a
// renewal, and which. One digest-sized entry per series, held only for the
// Slot and across its windows; charged per loaded key, with the key's
// witness, as each result merges (loadedKeyHeldBytes).
type slotStateKeys map[execution.StateKeyIdentity]string

const (
	slotKeyApply   = "apply"
	slotKeyRenewal = "renewal"
)

// claim takes one Plan's part of a window, its frozen series and its
// mutations, refusing any key the Slot asked for before or that the part
// names twice.
func (keys slotStateKeys) claim(frozen []execution.FrozenSeriesState, mutations []execution.StateMutation) error {
	take := func(identity execution.StateKeyIdentity, site string) error {
		if first, taken := keys[identity]; taken {
			return &StateKeyRepeatedInSlotError{Identity: identity, First: first, Again: site}
		}
		keys[identity] = site
		return nil
	}
	for _, series := range frozen {
		if err := take(series.Identity, slotKeyRenewal); err != nil {
			return err
		}
	}
	for _, mutation := range mutations {
		if err := take(mutation.Identity, slotKeyApply); err != nil {
			return err
		}
	}
	return nil
}

func sortedDuePlans(plans []execution.DuePlan) []execution.DuePlan {
	sorted := append([]execution.DuePlan(nil), plans...)
	sort.Slice(sorted, func(left, right int) bool { return lessPlanIdentity(sorted[left].Identity, sorted[right].Identity) })
	return sorted
}
