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
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// slotApply is what one attempt carries across the writes of a Slot: the
// activation facts read before the first write, and what the Plans' writes
// have decided for the completion so far.
//
// The writes are the same steps whether the Slot is applied whole or window
// by window (windowed_apply.go): a Plan's open statement before its events, its
// events acknowledged before its State, its clear after its State, and the
// no-data memory, the activation recheck and Progress after every Plan. A
// Slot that fits one window runs all of them inside one Sequence
// (finalizeSummarized), Plan by Plan, as it always has; a Slot cut into
// windows runs its head, each window and its tail as Sequences of their own.
// Keeping the steps here is what keeps the two from drifting apart.
type slotApply struct {
	coordinator *SlotExecutionCoordinator
	request     execution.SlotExecutionRequest
	header      execution.InternalExecutionHeader
	loadedGaps  execution.GapLoadResult

	activationRequest  execution.PlanActivationRequest
	guardActivations   execution.PlanActivationResult
	changedPlans       map[execution.PlanKey]struct{}
	changedActivations execution.PlanActivationResult
	firstSeen          map[execution.PlanKey]execution.PlanActivationFact
	// forced is the Plans this Slot forces to warm up, as read before
	// judgeForced splits off the first-seen ones.
	forced execution.PlanActivationResult

	retryPendingReason execution.ReasonCode
	// deterministicTerminalReason is the first deterministic refusal that
	// ends a Plan by name: a State admission that will not take its writes,
	// or an output the sink will not write. Either finishes the Slot as
	// TERMINAL with that name; the other Plans run, and Progress advances by
	// the rule every terminal completion follows. Neither waits on anything,
	// so neither is retried.
	deterministicTerminalReason execution.ReasonCode
	// frozenRenewals is one reading per Slot rather than per Plan: the
	// question is how many of this Slot's keys were read and not written,
	// and a Plan is not a population anyone reads that against.
	frozenRenewals observability.FrozenStateRenewalFacts
	// withheldNoData is the Plans whose output did not all land this
	// attempt. Their no-data memory is not applied: the memory follows the
	// output exactly as State does (output acknowledged, then State and
	// memory, then Progress), and the round that decides them again has to
	// decide from the memory the unsent decision started from. Applied
	// anyway, a closing NORMAL that deletes a group would be gone for good -
	// the next round finds no group and says nothing, and the alert stays
	// open.
	withheldNoData map[execution.PlanIdentity]struct{}
	// written is how many State keys this attempt's applies wrote, for the
	// census the frozen renewals are read against.
	written int
	// keys is every State key this Slot has asked the store to write or
	// renew, held for a Slot applied window by window (slotStateKeys); nil
	// for a Slot applied whole.
	keys slotStateKeys
}

// beginSlotApply reads the activation facts the Slot's writes are guarded by
// and judges its forced Plans with unseen. It returns false when the facts
// could not be read, which the caller answers with an activation retry before
// anything is written.
func (coordinator *SlotExecutionCoordinator) beginSlotApply(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	header execution.InternalExecutionHeader,
	loadedGaps execution.GapLoadResult,
	unseen map[execution.PlanKey]struct{},
) (*slotApply, bool) {
	apply, read := coordinator.readSlotActivations(ctx, request, header, loadedGaps)
	if !read {
		return nil, false
	}
	apply.judgeForced(unseen)
	return apply, true
}

// readSlotActivations reads the activation facts and the Plans the Slot
// forces to warm up, leaving them unjudged: whether a forced Plan is
// first-seen waits on unseen (judgeForced), which a Slot applied window by
// window learns between the two (firstSeenPrepass).
func (coordinator *SlotExecutionCoordinator) readSlotActivations(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	header execution.InternalExecutionHeader,
	loadedGaps execution.GapLoadResult,
) (*slotApply, bool) {
	activationRequest := duePlanActivationRequest(request.Contract, header.DuePlans)
	guardActivations, err := coordinator.loadActivations(ctx, activationRequest)
	if err != nil {
		return nil, false
	}
	changedPlans, changedActivations := changedDuePlanActivations(header.DuePlans, guardActivations)
	return &slotApply{
		coordinator: coordinator, request: request, header: header, loadedGaps: loadedGaps,
		activationRequest: activationRequest, guardActivations: guardActivations,
		changedPlans: changedPlans, changedActivations: changedActivations,
		forced:         unsatisfiedForcedWarmingActivations(guardActivations, loadedGaps, changedPlans),
		withheldNoData: make(map[execution.PlanIdentity]struct{}),
	}, true
}

// judgeForced splits the forced Plans into the first-seen ones and the ones
// still warmed up, which join the changed Plans.
//
// A Plan forced to warm up is one Plan of this Slot, not the Slot. Its
// WARMING marker is written at this Slot's ApplyVersion, and the Gap store
// takes one write per version: evaluating under the marker would advance it
// with a second write at the same version, which the store refuses as a
// conflict on every retry until the Slot ages out. So the Plan writes nothing
// else here and warms from the next Slot, whose version is newer than the
// marker's. The other Plans write as they would: the forced one joins the
// changed Plans, skipped by every write, and its marker is written by the
// protection path after their writes, as a changed Plan's is. A retry of the
// Slot finds the marker at the Slot's own version and still skips the Plan
// (unsatisfiedForcedWarmingActivations), under the word the marker was
// written with (classifyActivationChange).
//
// A forced Plan the Slot found nothing of from before it never ran under its
// generation: it is not warmed up (first_seen.go).
func (apply *slotApply) judgeForced(unseen map[execution.PlanKey]struct{}) {
	forced, firstSeen := firstSeenActivations(apply.forced, apply.loadedGaps, unseen)
	for _, fact := range forced.Facts {
		// Judged again by a later slice (applySliceHead): a Plan already
		// forced stays as it was.
		if _, already := apply.changedPlans[fact.Key()]; already {
			continue
		}
		apply.changedPlans[fact.Key()] = struct{}{}
		apply.changedActivations.Facts = append(apply.changedActivations.Facts, fact)
	}
	apply.firstSeen = firstSeen
}

// changed reports whether the Plan writes nothing this attempt because its
// activation moved under the Slot, or the Slot forces it to warm up.
func (apply *slotApply) changed(due execution.DuePlan) bool {
	_, changed := apply.changedPlans[due.Key()]
	return changed
}

// planGapsBefore writes what a Plan says about its marker before any of its
// events: a first-seen Plan's judgement, and its open statement. It filters
// the Plan's two guard lists to what this attempt has still to write, in
// place, and returns what its markers live for, read only when it has any.
func (apply *slotApply) planGapsBefore(
	ctx context.Context, due execution.DuePlan, planResult *execution.PlanEvaluationResult,
) (execution.GenerationRetention, error) {
	coordinator, request := apply.coordinator, apply.request
	// A first-seen Plan's judgement is recorded before anything of it is
	// written, so a retry finds it whatever the attempt got to. A Plan
	// whose evaluation writes a marker of its own this Slot has its
	// record in that one: a second write at the same version would be a
	// conflict.
	if len(planResult.GuardBeforeEvents) == 0 && len(planResult.GuardAfterState) == 0 {
		if err := apply.firstSeenMarker(ctx, due, GapSiteBeforeEvents); err != nil {
			return execution.GenerationRetention{}, err
		}
	}
	var gapRetention execution.GenerationRetention
	if len(planResult.GuardBeforeEvents) > 0 || len(planResult.GuardAfterState) > 0 {
		var err error
		if gapRetention, err = generationRetentionOf(due); err != nil {
			return execution.GenerationRetention{}, err
		}
	}
	// Gap statements commit independently from series State. This also
	// covers retries whose query became partial or exceeded its budget,
	// which never enter the evaluator.
	planResult.GuardBeforeEvents = uncommittedGapMutations(apply.loadedGaps, planResult.GuardBeforeEvents)
	planResult.GuardAfterState = uncommittedGapMutations(apply.loadedGaps, planResult.GuardAfterState)
	// Recorded, not refused, and read here because this is the first place
	// the Slot's whole evaluation is in hand: the contract's rule that one
	// Plan carries one gap statement per Slot is checked on each series
	// batch's result, and the accumulation across batches can assemble a
	// shape no batch produced. Measuring it before changing anything on
	// its account is deliberate - the shape has to be counted before a
	// merge rule is written for it, and refusing here would turn a Slot
	// the retry recovers into one that does not run.
	coordinator.observeDuplicatedGapStatements(ctx, request, *planResult)
	if err := coordinator.applyGap(ctx, request.Operation, request.Contract, planResult.GuardBeforeEvents, gapRetention, GapSiteBeforeEvents); err != nil {
		return execution.GenerationRetention{}, err
	}
	return gapRetention, nil
}

// firstSeenMarker writes, for a Plan this Slot judged first-seen, the marker
// recording that it ran (satisfiedMarker); nothing for any other Plan. The
// caller has made sure the Plan writes no other marker this Slot: a second
// write at the same version is a conflict.
func (apply *slotApply) firstSeenMarker(ctx context.Context, due execution.DuePlan, site string) error {
	fact, first := apply.firstSeen[due.Key()]
	if !first {
		return nil
	}
	marker, err := satisfiedMarker(apply.request.Contract, fact)
	if err != nil {
		return err
	}
	retention, err := generationRetentionOf(due)
	if err != nil {
		return err
	}
	return apply.coordinator.applyGap(ctx, apply.request.Operation, apply.request.Contract,
		[]execution.PlanGapMutation{marker}, retention, site)
}

// planGapAfter writes a Plan's clear, once its State is written.
func (apply *slotApply) planGapAfter(
	ctx context.Context, after []execution.PlanGapMutation, gapRetention execution.GenerationRetention,
) error {
	coordinator, request := apply.coordinator, apply.request
	coordinator.observeGapScheduleRestart(ctx, request.Operation, apply.loadedGaps, after)
	return coordinator.applyGap(ctx, request.Operation, request.Contract, after, gapRetention, GapSiteAfterState)
}

// planOutput is what became of a Plan's output in one apply.
type planOutput int

const (
	// planOutputLanded is every event the apply sent acknowledged, and the
	// State of the series that sent them written.
	planOutputLanded planOutput = iota
	// planOutputPartial is the sink writing the batch but for some events it
	// would not represent: those series' State stays where it was, every
	// other series moves.
	planOutputPartial
	// planOutputWithheld is the output not written - deferred, its ACK
	// unknown, or refused - and none of the Plan's State with it.
	planOutputWithheld
)

// applyPlanState writes one Plan's State results and their events against
// the views they were evaluated from: compared against the views, the
// frozen series renewed, admitted, the events written and acknowledged, and
// the State applied. For a Slot applied whole the results are the Plan's
// all; for a window, the ones the window holds.
func (apply *slotApply) applyPlanState(
	ctx context.Context,
	due execution.DuePlan,
	results []execution.StateEvaluation,
	loadedState execution.StatePreflightResult,
	stateIndex map[execution.StateKeyIdentity]int,
) (planOutput, error) {
	coordinator, request := apply.coordinator, apply.request
	plan := due.Identity
	output := planOutputLanded
	mutations := make([]execution.StateMutation, 0, len(results))
	eventsByState := make(map[execution.StateKeyIdentity][]contract.TriggerEventV1, len(results))
	withoutMessageByState := make(map[execution.StateKeyIdentity][]execution.EventWithoutMessage)
	stateResults := append([]execution.StateEvaluation(nil), results...)
	sort.Slice(stateResults, func(left, right int) bool {
		return lessStateIdentity(stateResults[left].Mutation.Identity, stateResults[right].Mutation.Identity)
	})
	// How much of what each admitted write carries is already stored. This
	// changes nothing: every mutation below is written exactly as before.
	// It is here rather than beside the write because this is where the
	// witnessed view and the mutation are both in hand, and the question
	// only has meaning for mutations that are actually about to be written.
	var writeReuse observability.StateWriteReuseFacts
	var alreadyApplied observability.StateAlreadyAppliedFacts
	for _, stateResult := range stateResults {
		statePosition, found := stateIndex[stateResult.Mutation.Identity]
		if !found {
			return 0, errors.New("alarmd worker: evaluation returned state without preflight view")
		}
		view := loadedState.Items[statePosition]
		started := time.Now()
		classified := execution.ClassifyStateMutationDetail(view, stateResult.Mutation)
		switch classified.Disposition {
		case execution.StateProceed:
			writeReuse.Record(
				observability.StateWriteReuseClass(execution.ClassifyStateWriteReuse(view, stateResult.Mutation)),
				storedStateLabel(view.Status),
			)
			coordinator.observe(ctx, observability.ComponentState, observability.StageMutationCompared, request.Operation, started, observability.ResultSuccess, observability.ReasonNone, nil)
			mutations = append(mutations, stateResult.Mutation)
			eventsByState[stateResult.Mutation.Identity] = append([]contract.TriggerEventV1(nil), stateResult.Events...)
			if len(stateResult.WithoutMessage) != 0 {
				withoutMessageByState[stateResult.Mutation.Identity] = stateResult.WithoutMessage
			}
		case execution.StateAlreadyApplied:
			alreadyApplied.Record(observability.StateAlreadyAppliedAtPreflight, observability.StateAlreadyAppliedKind(classified.AlreadyApplied),
				string(stateResult.Mutation.Identity.SeriesIdentityDigest), stateResult.Mutation.ExpectedBlobRevision, view.BlobRevision)
			coordinator.observe(ctx, observability.ComponentState, observability.StageMutationCompared, request.Operation, started, observability.ResultSuccess, observability.ReasonNone, nil)
			continue
		case execution.StateStaleVersion, execution.StateVersionConflict:
			// The refusal names itself and the line carries the name:
			// the reason from the error, and for a conflict the kind
			// with the two revisions compared, so the line does not read
			// internal_unknown beside an error_type that already knew.
			refusal := &StateConflictError{Stage: "state mutation preflight", Status: string(classified.Disposition)}
			var conflicts *observability.StateVersionConflictFacts
			if classified.Disposition == execution.StateVersionConflict {
				refusal.Kind, refusal.ExpectedRevision, refusal.StoredRevision, refusal.VersionComparison =
					classified.VersionConflict, stateResult.Mutation.ExpectedBlobRevision, view.BlobRevision, view.VersionComparison
				conflicts = &observability.StateVersionConflictFacts{}
				conflicts.Record(observability.StateAlreadyAppliedAtPreflight, observability.StateVersionConflictKind(classified.VersionConflict),
					string(stateResult.Mutation.Identity.SeriesIdentityDigest), stateResult.Mutation.ExpectedBlobRevision, view.BlobRevision,
					string(view.VersionComparison), false)
			}
			var err error = refusal
			reason, _ := StateConflictReason(err)
			coordinator.emitObservation(ctx, observability.Observation{
				Component: observability.ComponentState, Stage: observability.StageMutationCompared,
				Operation:  observability.Operation(request.Operation),
				ReasonCode: observability.ReasonCode(reason), Duration: time.Since(started), DurationKnown: true, Err: err,
				StateVersionConflict: conflicts,
			})
			return 0, fmt.Errorf("alarmd worker: %w", err)
		default:
			err := errors.New("unknown state mutation preflight result")
			coordinator.observe(ctx, observability.ComponentState, observability.StageMutationCompared, request.Operation, started, "", "", err)
			return 0, fmt.Errorf("alarmd worker: %w", err)
		}
	}

	if !writeReuse.Empty() {
		facts := writeReuse
		coordinator.emitObservation(ctx, observability.Observation{
			Component: observability.ComponentState, Stage: observability.StageMutationCompared,
			Result: observability.ResultSuccess, Operation: observability.Operation(request.Operation),
			ReasonCode:      observability.ReasonNone,
			StateWriteReuse: &facts,
		})
	}
	if !alreadyApplied.Empty() {
		coordinator.emitAlreadyApplied(ctx, request.Operation, alreadyApplied)
	}

	// The retention need travels with the Plan's mutations so the store
	// sizes their TTL against the Plan frozen with this Slot. The frozen
	// series below need it for the same reason and must get the same
	// answer: a renewal computed from a different retention would give a
	// key a different life from the one its write gave it.
	frozen := frozenSeriesOf(plan, loadedState, results)
	// Before any of it is sent: a key this Slot already wrote or renewed in
	// an earlier window, asked for again, is the repeat the store refuses
	// as another statement at the same version, and refusing it here names
	// the window cut that produced it rather than the store's conflict.
	if apply.keys != nil {
		if err := apply.keys.claim(frozen, mutations); err != nil {
			return 0, err
		}
	}
	var retention []execution.StateRetentionRequirement
	if len(mutations) > 0 || len(frozen) > 0 {
		var err error
		retention, err = execution.DeriveStateRetentionRequirement(due.CompiledPlan)
		if err != nil {
			return 0, fmt.Errorf("alarmd worker: %w", err)
		}
	}
	horizon := coordinator.stateHorizon(due)
	// Before the writes, not after. A renewal is about the keys this Plan
	// is not writing, so nothing below can change what it decides -- but
	// an apply that fails returns from this function, and the keys that
	// were about to expire would then go one more Slot without anyone
	// asking about them, on the Slot that already went wrong.
	coordinator.renewFrozenState(ctx, request, retention, horizon, frozen, &apply.frozenRenewals)

	if len(mutations) == 0 {
		return output, nil
	}
	if err := coordinator.admit(ctx, request, due); err != nil {
		return 0, err
	}
	// Under the Plan's strategy, so the refusal is filed under the
	// Plan it refused rather than the Query Group alone.
	planCtx := observability.ContextWithTraceFields(ctx, observability.TraceFields{
		StrategyID: plan.StrategyID, BusinessID: plan.BusinessID})
	rejected, encodedBytes, held, err := coordinator.admitState(planCtx, request.Operation, request.Contract, retention, horizon, mutations)
	if err != nil {
		return 0, err
	}
	// The frames admission kept wait for the write under the reservation
	// admission took for them, released when this Plan's write is over or
	// it stops short of one; deferred as well, for the exits that return.
	defer held.release()
	accepted := make([]execution.StateMutation, 0, len(mutations)-len(rejected))
	acceptedBytes := make([]admittedState, 0, len(mutations)-len(rejected))
	for index, mutation := range mutations {
		if reason, terminal := rejected[mutation.Identity]; terminal {
			if apply.deterministicTerminalReason == "" {
				apply.deterministicTerminalReason = reason
			}
			// Its series' events are not sent either.
			apply.withheldNoData[plan] = struct{}{}
			continue
		}
		accepted = append(accepted, mutation)
		acceptedBytes = append(acceptedBytes, encodedBytes[index])
	}
	held.settle(acceptedBytes)
	// A user may have closed one of these absences after the round
	// read its memory; its alert must not go out (decision-024). The
	// series' events are taken out where they are kept, so what is
	// counted as sent below is what went to the sink.
	eventsByState = coordinator.withoutManuallyClosed(planCtx, request.Operation, plan, accepted, eventsByState)
	events, withoutMessage := outputsOf(accepted, eventsByState, withoutMessageByState)
	sortTriggerEvents(events)
	if err := coordinator.writeEvents(ctx, request.Operation, plan, events, withoutMessage); err != nil {
		if notWritten, partial := outputNotWritten(err); partial {
			// The sink wrote the batch but for some events it would
			// not represent, and withheld the other events of their
			// series. Those series did not send what they decided,
			// so their State stays where it was: the next round
			// decides them again from it, and nothing records an
			// alert the consumer never received or a recovery
			// behind one. Every other series moves as it would
			// have, and the Slot completes by the rejection's name.
			kept, keptBytes, keepErr := withoutSeriesNotWritten(accepted, acceptedBytes, eventsByState, notWritten)
			if keepErr != nil {
				return 0, keepErr
			}
			if reason, rejected := outputRejectionReason(err); rejected && apply.deterministicTerminalReason == "" {
				apply.deterministicTerminalReason = reason
			}
			apply.withheldNoData[plan] = struct{}{}
			accepted, acceptedBytes = kept, keptBytes
			held.settle(acceptedBytes)
			output = planOutputPartial
		} else if reason, deferred := outputDeferralReason(err); deferred {
			// The sink did not start the batch: the lease has less
			// life left than one batch needs to land. Nothing is
			// unknown and nothing is wrong with the content; the
			// Plan waits, by that name, for the next renewal.
			if apply.retryPendingReason == "" {
				apply.retryPendingReason = reason
			}
			apply.withheldNoData[plan] = struct{}{}
			held.release()
			return planOutputWithheld, nil
		} else if reason, rejected := outputRejectionReason(err); rejected {
			// Decided in this process, from this Plan's own decisions
			// or this deployment's own client: the same events meet
			// the same refusal on every retry. This Plan's State is
			// not applied, because its output was not written and
			// State follows the ACK; the Slot completes by the name,
			// the sibling Plans go on, and Progress moves past it.
			// Retrying instead would report a Kafka that is up as
			// down, every round, and commit nothing.
			if apply.deterministicTerminalReason == "" {
				apply.deterministicTerminalReason = reason
			}
			apply.withheldNoData[plan] = struct{}{}
			held.release()
			return planOutputWithheld, nil
		} else {
			if !isRetryableOutputDependency(err) {
				return 0, err
			}
			if apply.retryPendingReason == "" {
				apply.retryPendingReason = execution.ReasonCode(contract.ReasonOutputACKUnknown)
			}
			// Event acknowledgement is Plan-local. Keep the Slot retryable and
			// continue healthy sibling Plans, but do not apply this Plan's State
			// or advance Progress until the stable event identity is replayed.
			apply.withheldNoData[plan] = struct{}{}
			held.release()
			return planOutputWithheld, nil
		}
	}
	if len(accepted) > 0 {
		rejectedApply, err := coordinator.applyState(ctx, request.Operation, request.Contract, request.OwnerFence, request.ContentScope, retention, horizon, accepted, acceptedBytes)
		if err != nil {
			coordinator.observeOutputUnapplied(planCtx, request.Operation, accepted, eventsByState, withoutMessageByState, err)
			return 0, err
		}
		// Counted after the write, not from the mutations built: a
		// key whose write was refused is a key that did not have its
		// life refreshed, and it belongs on the other side of the
		// census.
		apply.written += len(accepted) - len(rejectedApply)
		for _, mutation := range accepted {
			reason, terminal := rejectedApply[mutation.Identity]
			if !terminal {
				continue
			}
			return 0, fmt.Errorf(
				"alarmd worker: deterministic State apply violates its successful admission contract: %s", reason,
			)
		}
	}
	// The write is over, or a partial output left no series to write;
	// the frames go either way. An apply that failed returned above,
	// and the deferred release has them.
	held.release()
	return output, nil
}

// noteRetryPending keeps the first Plan reason a retry is pending on.
func (apply *slotApply) noteRetryPending(planResult execution.PlanEvaluationResult) {
	if planResult.Disposition == execution.PlanRetryPending && apply.retryPendingReason == "" {
		apply.retryPendingReason = planResult.ReasonCode
	}
}

// complete writes what follows every Plan's State and gap: the no-data
// memory and ends of the Plans whose output landed, the completion, the
// activation recheck and Progress.
func (apply *slotApply) complete(
	ctx context.Context,
	bindings []execution.NamedInputBinding,
	summary *slotEvaluationSummary,
	noDataMemory []execution.PlanNoDataMutation,
	evaluatedResult observability.Result,
	evaluatedReason execution.ReasonCode,
	query queryAvailabilityEvidence,
	census seriesCensus,
	targets []execution.TargetResolutionSummary,
) (execution.SlotExecutionResult, error) {
	coordinator, request, header := apply.coordinator, apply.request, apply.header
	loadedGaps := apply.loadedGaps
	frozenRenewals := apply.frozenRenewals
	frozenRenewals.RecordCensus(census.Due, census.Read, census.Written)
	coordinator.observeFrozenStateRenewal(ctx, request.Operation, frozenRenewals)
	// After every Plan's state and gap, inside the same sequenced scope, and
	// only for the Plans whose output landed. The memory decides which groups
	// the next round expects and what it says about them, so it follows the
	// writes that carry this round's decisions rather than racing them.
	if err := coordinator.applyNoDataMemory(ctx, request, header.DuePlans,
		withoutWithheldNoData(noDataMemory, apply.withheldNoData)); err != nil {
		return execution.SlotExecutionResult{}, err
	}
	if err := coordinator.applyNoDataEnds(ctx, request, header, summary, apply.withheldNoData); err != nil {
		return execution.SlotExecutionResult{}, err
	}
	coordinator.observeSourceHeldUnindexed(ctx, request, header)
	if apply.retryPendingReason != "" {
		return execution.SlotExecutionResult{
			Completed: false, Result: observability.ResultRetrying, ReasonCode: apply.retryPendingReason,
		}, nil
	}
	primary, err := execution.DeriveStreamingPrimaryInputFact(header, bindings)
	if err != nil {
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: derive PRIMARY input fact: %w", err)
	}
	primary.EmptiedByTarget = query.emptiedByTarget(primary)
	primary.Identity = query.identityEvidence(primary)
	primary.QuietWhenEmpty = query.quietWhenEmpty(primary, header.Requirements)
	primary.UnfilledWhenEmpty = query.unfilledWhenEmpty(primary, header.Requirements)
	query.labelFiltered(&primary, header.Requirements, bindings)
	completion := execution.SlotCompletion{Contract: request.Contract, Primary: &primary, TargetResolutions: targets}
	// Observation only. The cause is deliberately not put on
	// completion.ReasonCode, which is persisted and decides how consecutive gaps
	// fold into a Progress gap summary; changing that is a separate decision
	// about a durable structure.
	var attribution execution.CompletionAttribution
	attribution.Coverage = summary.coverage()
	if len(apply.changedPlans) > 0 {
		var drift execution.CompletionAttribution
		completion, drift = activationChangeCompletion(request.Contract, &primary,
			classifyActivationChange(header.DuePlans, apply.guardActivations, loadedGaps, unseenPlansFrom(ctx)), bindings, targets)
		attribution.Cause, attribution.Reason, attribution.Scope = drift.Cause, drift.Reason, drift.Scope
	} else {
		var derived execution.CompletionAttribution
		completion.Kind, derived, err = execution.DeriveStreamingFoldedCompletionAttribution(header, bindings, summary.completion)
		attribution.Cause, attribution.Reason, attribution.Scope = derived.Cause, derived.Reason, derived.Scope
		attribution.ReasonCarried, attribution.ReasonOrigin = derived.ReasonCarried, derived.ReasonOrigin
		if err != nil {
			return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: derive completion: %w", err)
		}
		completion.Result = evaluatedResult
		completion.ReasonCode = evaluatedReason
	}
	if apply.deterministicTerminalReason != "" {
		completion.Kind = execution.CompletionTerminal
		completion.Result = observability.ResultTerminal
		completion.ReasonCode = apply.deterministicTerminalReason
		// A terminal Slot is no longer an unavailable one, so whatever the
		// traversal was about to say is now about a completion that did not
		// happen. deriveCompletion reports no cause for TERMINAL for the same
		// reason; the override has to follow it or the two disagree here.
		//
		// The coverage goes with it. The windows really were summarised and
		// the counts are not wrong, but they would arrive as the evidence for
		// a reason no longer being reported, and evidence with no claim beside
		// it gets read as whatever the reader came expecting.
		attribution = execution.CompletionAttribution{}
	}
	if len(apply.changedPlans) > 0 {
		return execution.SlotExecutionResult{}, &activationProtectionRequiredError{
			activationRequest: apply.activationRequest,
			currentFacts:      apply.guardActivations,
			activations:       apply.changedActivations,
			completion:        completion,
			completionCause:   attribution,
			guardReason:       guardReasonFor(header.DuePlans, apply.changedActivations, loadedGaps, unseenPlansFrom(ctx)),
		}
	}
	progressActivations, err := coordinator.loadActivations(ctx, apply.activationRequest)
	if err != nil {
		return activationRetry(execution.ReasonCode(contract.ReasonActivationReadFailed)), nil
	}
	if !apply.guardActivations.SameSelections(progressActivations) {
		driftCompletion, driftAttribution := activationChangeCompletion(request.Contract, &primary,
			classifyActivationChange(header.DuePlans, progressActivations, loadedGaps, unseenPlansFrom(ctx)), bindings, targets)
		changedSelected := changedSelectedActivations(apply.guardActivations, progressActivations)
		return execution.SlotExecutionResult{}, &activationProtectionRequiredError{
			activationRequest: apply.activationRequest,
			currentFacts:      progressActivations,
			activations:       changedSelected,
			completion:        driftCompletion,
			completionCause:   driftAttribution,
			guardReason:       guardReasonFor(header.DuePlans, changedSelected, loadedGaps, unseenPlansFrom(ctx)),
		}
	}
	// The activation facts are read again before Progress; the owner fence is
	// not, because the commit's script runs it at the moment of the write
	// (02 §7.5), and nothing between here and there is a side effect.
	if err := coordinator.admitDuePlanActivations(ctx, request, header.DuePlans); err != nil {
		return execution.SlotExecutionResult{}, err
	}

	coordinator.markOutOfHours(ctx, header, &attribution.Coverage)
	result, err := coordinator.commitProgress(ctx, request, completion, attribution)
	if err == nil && result.Completed {
		result.QueryAvailability, result.QueryUnavailableReason = query.availability(), query.unavailableReason()
		result.QueryUnknownReason = query.unknownReason()
		result.PrimaryQueryClock = query.clock
	}
	return result, err
}

// sortedPlanResults is the Slot's Plan results in Plan order, the order every
// Plan's writes go in.
func sortedPlanResults(evaluated execution.EvaluationResult) []execution.PlanEvaluationResult {
	planResults := append([]execution.PlanEvaluationResult(nil), evaluated.Plans...)
	sort.Slice(planResults, func(left, right int) bool {
		return lessPlanIdentity(planResults[left].Plan, planResults[right].Plan)
	})
	return planResults
}
